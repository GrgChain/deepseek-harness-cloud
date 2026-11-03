import hashlib
import io
import json
import sqlite3
import threading
import unittest
import zipfile
from types import SimpleNamespace
from unittest.mock import patch

import httpx
from fastapi import HTTPException
from viking_memory import VikingMemory


MEMORIES = "viking://user/owner/memories"
PEERS = "viking://user/owner/peers"
RAW = '# 回答偏好\n\n默认使用中文。\n\n<!-- MEMORY_FIELDS\n{"version":7,"custom":"preserve"}\n-->'


def ok(result):
    return httpx.Response(200, json={"status": "ok", "result": result})


def entry(uri, directory=False, **extra):
    return {"uri": uri, "isDir": directory, **extra}


class VikingBackup(unittest.TestCase):
    def target(self, reply, existing=True):
        db = sqlite3.connect(":memory:")
        db.execute("CREATE TABLE tenant_keys(namespace TEXT PRIMARY KEY,key TEXT)")
        if existing:
            db.execute("INSERT INTO tenant_keys VALUES(?,?)", ("d" * 64, "tenant-key"))
            db.commit()
        with patch("viking_memory.sqlite3.connect", return_value=db), patch(
            "viking_memory.MilvusTransport", return_value=httpx.MockTransport(reply)
        ):
            target = VikingMemory(
                "d" * 64, SimpleNamespace(url="https://viking.example", token="root-key")
            )
        self.addCleanup(target.close)
        return target

    def test_unused_connection_reads_no_admin_content_and_creates_no_tenant(self):
        def reply(request):
            self.fail("An unused connection must not access the upstream service")

        target = self.target(reply, existing=False)
        self.assertEqual(target.session_export(), [])
        self.assertIsNone(target.session_read_optional(MEMORIES + "/profile.md"))

    def test_export_walks_past_old_node_and_depth_limits_and_preserves_raw_metadata(self):
        deep = MEMORIES + "/nested" + ("/level" * 10)
        directories = {
            MEMORIES: [entry(MEMORIES + "/m" + str(i) + ".md") for i in range(1001)]
            + [
                entry(MEMORIES + "/nested", True),
                entry(MEMORIES + "/.abstract.md"),
                entry(MEMORIES + "/.internal", True),
                entry(MEMORIES + "/notes.txt"),
                entry(MEMORIES + "/harness_" + "a" * 64, True),
            ],
            PEERS: [entry(PEERS + "/shared", True), entry(PEERS + "/unused", True)],
            PEERS + "/shared/memories": [entry(PEERS + "/shared/memories/experiences", True)],
            PEERS + "/shared/memories/experiences": [
                entry(
                    PEERS + "/shared/memories/experiences/task.md", modTime="2026-11-02T00:00:00Z"
                )
            ],
            MEMORIES + "/harness_" + "a" * 64: [
                entry(MEMORIES + "/harness_" + "a" * 64 + "/native.md")
            ],
            deep: [entry(deep + "/deep.md")],
        }
        for level in range(10):
            parent = MEMORIES + "/nested" + ("/level" * level)
            directories[parent] = [entry(parent + "/level", True)]
        read = []

        def reply(request):
            self.assertEqual(request.headers["X-API-Key"], "tenant-key")
            self.assertEqual(request.headers["X-OpenViking-Account"], "hc_" + "d" * 64)
            uri = request.url.params["uri"]
            if request.url.path == "/api/v1/fs/ls":
                self.assertEqual(request.url.params["node_limit"], "10001")
                self.assertEqual(request.url.params["recursive"], "false")
                self.assertNotIn("level_limit", request.url.params)
                if uri == PEERS + "/unused/memories":
                    return httpx.Response(404)
                self.assertIn(uri, directories)
                return ok(directories[uri])
            self.assertEqual(request.url.path, "/api/v1/content/read")
            self.assertEqual(request.url.params["raw"], "true")
            self.assertEqual(request.url.params["limit"], "-1")
            read.append(uri)
            return ok(RAW)

        exported = self.target(reply).session_export()
        self.assertEqual(len(exported), 1004)
        self.assertEqual(len(set(read)), 1004)
        self.assertEqual([item["uri"] for item in exported], sorted(read))
        self.assertTrue(all(item["content"] == RAW for item in exported))
        self.assertIn(deep + "/deep.md", read)
        peer = next(item for item in exported if item["uri"].startswith(PEERS))
        self.assertEqual(peer["updated_at"], "2026-11-02T00:00:00Z")
        self.assertNotIn("updated_at", exported[0])

    def test_export_read_parallelism_is_bounded_and_drained(self):
        barrier = threading.Barrier(8)
        lock = threading.Lock()
        active = 0
        maximum = 0

        def reply(request):
            nonlocal active, maximum
            if request.url.path.endswith("/ls"):
                return ok(
                    [entry(MEMORIES + "/item" + str(i) + ".md") for i in range(8)]
                    if request.url.params["uri"] == MEMORIES
                    else []
                )
            with lock:
                active += 1
                maximum = max(maximum, active)
            try:
                barrier.wait(timeout=5)
                return ok(RAW)
            finally:
                with lock:
                    active -= 1

        self.assertEqual(len(self.target(reply).session_export()), 8)
        self.assertEqual(maximum, 8)
        self.assertEqual(active, 0)

    def test_export_rejects_possible_truncation_and_total_item_limit(self):
        for split in (False, True):

            def reply(request):
                uri = request.url.params["uri"]
                if uri == PEERS:
                    return ok([])
                rows = (
                    [entry(uri + "/dir" + str(i), True) for i in range(2)]
                    if split and uri == MEMORIES
                    else [entry(uri + "/item" + str(i) + ".md") for i in range(2 if split else 3)]
                )
                return ok(rows)

            target = self.target(reply)
            target.SESSION_EXPORT_ITEMS = 2
            with self.subTest(split=split), self.assertRaises(HTTPException) as error:
                target.session_export()
            self.assertEqual(error.exception.status_code, 413)

    def test_export_rejects_unbounded_directories_instead_of_silently_omitting_them(self):
        def reply(request):
            uri = request.url.params["uri"]
            return ok([entry(uri + "/child", True)])

        target = self.target(reply)
        target.SESSION_EXPORT_NODES = 3
        with self.assertRaises(HTTPException) as error:
            target.session_export()
        self.assertEqual(error.exception.status_code, 413)

    def test_export_rejects_invalid_or_incomplete_listings(self):
        for bad in (
            {},
            [entry("viking://user/other/memories/private.md")],
            [entry(MEMORIES + "/dir/nested.md")],
            [{"uri": MEMORIES + "/x.md"}],
        ):
            target = self.target(
                lambda request: ok(bad) if request.url.params["uri"] == MEMORIES else ok([])
            )
            with self.subTest(bad=bad), self.assertRaises(RuntimeError):
                target.session_export()

        def missing_child(request):
            uri = request.url.params["uri"]
            if uri == MEMORIES:
                return ok([entry(MEMORIES + "/gone", True)])
            if uri == PEERS:
                return ok([])
            return httpx.Response(404)

        with self.assertRaises(httpx.HTTPStatusError):
            self.target(missing_child).session_export()

    def test_export_rejects_utf8_byte_overflow_and_disappearing_content(self):
        uri = MEMORIES + "/language.md"
        for content in ("中文" * 40, None):

            def reply(request):
                if request.url.path.endswith("/ls"):
                    return ok([entry(uri)] if request.url.params["uri"] == MEMORIES else [])
                return httpx.Response(404) if content is None else ok(content)

            target = self.target(reply)
            target.SESSION_EXPORT_BYTES = 100
            with self.subTest(content=content), self.assertRaises((HTTPException, RuntimeError)):
                target.session_export()

    def test_optional_read_distinguishes_missing_from_upstream_failure(self):
        for status in (404, 403, 503):
            target = self.target(lambda request: httpx.Response(status))
            if status == 404:
                self.assertIsNone(target.session_read_optional(MEMORIES + "/missing.md"))
            else:
                with self.assertRaises(httpx.HTTPStatusError):
                    target.session_read_optional(MEMORIES + "/missing.md")

    def test_read_and_write_reject_external_hidden_and_traversal_uris_before_requests(self):
        def reply(request):
            self.fail("Invalid URI reached OpenViking")

        target = self.target(reply)
        for uri in (
            "viking://user/other/memories/private.md",
            MEMORIES + "/../outside.md",
            MEMORIES + "/%2e%2e/secret.md",
            MEMORIES + "/.abstract.md",
            PEERS + "/peer/resources/file.md",
        ):
            with self.subTest(uri=uri), self.assertRaises(ValueError):
                target.session_read_optional(uri)
            with self.subTest(uri=uri), self.assertRaises(ValueError):
                target.session_put(uri, RAW)

    def test_category_import_creates_tenant_then_upserts_exact_raw_content_and_waits(self):
        seen = []
        uri = PEERS + "/shared/memories/experiences/task.md"

        def reply(request):
            seen.append(request.url.path)
            body = json.loads(request.content)
            if request.url.path == "/api/v1/admin/accounts":
                self.assertEqual(request.headers["X-API-Key"], "root-key")
                return ok({"user_key": "tenant-key"})
            self.assertEqual(request.headers["X-API-Key"], "tenant-key")
            if request.url.path == "/api/v1/fs/mkdir":
                self.assertEqual(body, {"uri": uri.rsplit("/", 1)[0]})
                return httpx.Response(409)
            self.assertEqual(request.url.path, "/api/v1/content/batch-write")
            self.assertEqual(
                body,
                {
                    "root_uri": uri.rsplit("/", 1)[0],
                    "operations": [{"uri": uri, "content": RAW, "mode": "upsert"}],
                    "wait": True,
                    "timeout": 170,
                },
            )
            return ok({"created": [uri], "updated": [], "unchanged": [], "queue_status": None})

        target = self.target(reply, existing=False)
        target.session_put(uri, RAW)
        target.session_put(uri, RAW)
        self.assertEqual(seen.count("/api/v1/admin/accounts"), 1)
        self.assertEqual(seen.count("/api/v1/content/batch-write"), 2)

    def test_category_import_keeps_failed_indexing_as_failure(self):
        for result in ({"updated": []}, {"created": MEMORIES + "/preferences/a.md"}, None):

            def reply(request):
                return ok({}) if request.url.path.endswith("/mkdir") else ok(result)

            with self.subTest(result=result), self.assertRaises(RuntimeError):
                self.target(reply).session_put(MEMORIES + "/preferences/a.md", RAW)

    def test_unchanged_raw_retry_must_confirm_indexing_before_succeeding(self):
        uri = MEMORIES + "/preferences/a.md"
        calls = []
        fail = True

        def reply(request):
            calls.append(request.url.path)
            if request.url.path.endswith("/mkdir"):
                return ok({})
            if request.url.path.endswith("/batch-write"):
                return ok({"unchanged": [uri]})
            self.assertEqual(request.url.path, "/api/v1/content/reindex")
            self.assertTrue(json.loads(request.content)["wait"])
            return ok(
                {
                    "status": "completed",
                    "uri": uri,
                    "failed_records": 1 if fail else 0,
                    "unsupported_records": 0,
                }
            )

        target = self.target(reply)
        with self.assertRaises(RuntimeError):
            target.session_put(uri, RAW)
        fail = False
        target.session_put(uri, RAW)
        self.assertEqual(calls.count("/api/v1/content/reindex"), 2)

    def root_import(self, import_status=200, indexed=None, stored=RAW, upload_id=None):
        uri = MEMORIES + "/profile.md"
        seen = []
        received = []
        upload_id = upload_id or "upload_" + "a" * 32 + ".ovpack"

        def reply(request):
            seen.append(request.url.path)
            self.assertEqual(request.headers["X-API-Key"], "tenant-key")
            if request.url.path.endswith("/mkdir"):
                return ok({})
            if request.url.path.endswith("/temp_upload"):
                data = request.read()
                boundary = request.headers["content-type"].split("boundary=")[1].encode()
                encoded = data.split(b"\r\n\r\n", 1)[1].rsplit(b"\r\n--" + boundary, 1)[0]
                with zipfile.ZipFile(io.BytesIO(encoded)) as archive:
                    self.assertEqual(
                        set(archive.namelist()),
                        {
                            "profile.md/files",
                            "profile.md/_ovpack/index_records.jsonl",
                            "profile.md/_ovpack/manifest.json",
                        },
                    )
                    self.assertEqual(archive.read("profile.md/files").decode(), RAW)
                    manifest = json.loads(archive.read("profile.md/_ovpack/manifest.json"))
                    self.assertEqual(manifest["root"]["uri"], uri)
                    self.assertEqual(
                        manifest["entries"][0]["sha256"], hashlib.sha256(RAW.encode()).hexdigest()
                    )
                    received.append(manifest)
                return ok({"temp_file_id": upload_id})
            if request.url.path.endswith("/import"):
                self.assertEqual(
                    json.loads(request.content),
                    {
                        "temp_file_id": upload_id,
                        "parent": MEMORIES,
                        "on_conflict": "overwrite",
                        "vector_mode": "recompute",
                    },
                )
                return ok({"uri": uri}) if import_status == 200 else httpx.Response(import_status)
            if request.url.path.endswith("/read"):
                self.assertEqual(request.url.params["raw"], "true")
                return ok(stored)
            self.assertEqual(request.url.path, "/api/v1/content/reindex")
            self.assertEqual(
                json.loads(request.content), {"uri": uri, "mode": "vectors_only", "wait": True}
            )
            return ok(
                indexed
                if indexed is not None
                else {
                    "status": "completed",
                    "uri": uri,
                    "failed_records": 0,
                    "unsupported_records": 0,
                }
            )

        return self.target(reply), uri, seen, received

    def test_root_import_preserves_raw_metadata_with_single_file_pack_and_reindexes(self):
        for import_status in (200, 503):
            target, uri, seen, received = self.root_import(import_status=import_status)
            target.session_put(uri, RAW)
            self.assertEqual(len(received), 1)
            self.assertEqual(seen[-1], "/api/v1/content/reindex")
            self.assertNotIn("/api/v1/fs/mv", seen)
            self.assertNotIn("/api/v1/content/write", seen)

    def test_root_import_never_accepts_uncertain_content_or_incomplete_indexing(self):
        target, uri, seen, _ = self.root_import(import_status=503, stored="different content")
        with self.assertRaises(httpx.HTTPStatusError):
            target.session_put(uri, RAW)
        self.assertNotIn("/api/v1/content/reindex", seen)
        for indexed in (
            {"status": "accepted"},
            {"status": "completed", "uri": uri, "failed_records": 1, "unsupported_records": 0},
        ):
            target, uri, _, _ = self.root_import(indexed=indexed)
            with self.assertRaises(RuntimeError):
                target.session_put(uri, RAW)

    def test_root_import_rejects_untrusted_upload_receipt_before_import(self):
        for upload_id in (
            "../../private.ovpack",
            "https://other/upload",
            "shared_../../private",
            "unknown-upload",
        ):
            target, uri, seen, _ = self.root_import(upload_id=upload_id)
            with self.subTest(upload_id=upload_id), self.assertRaises(RuntimeError):
                target.session_put(uri, RAW)
            self.assertNotIn("/api/v1/pack/import", seen)


if __name__ == "__main__":
    unittest.main()
