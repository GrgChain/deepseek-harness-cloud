"""Complete, bounded v1 memory archives across Mem0 and Graphiti."""
import hashlib
import hmac
import os
import time
import unittest
import uuid
from unittest.mock import patch

from fastapi.testclient import TestClient

import test_manual_memory_lifecycle as lifecycle

app = lifecycle.app


class ManualMemoryArchive(lifecycle.ManualMemoryFixture):
    def package(self, entries, backend=None):
        return {
            "schema": "hc-memory-v1",
            "backend": backend or self.backend,
            "exported_at": "2026-11-03T00:00:00Z",
            "entries": entries,
            "sha256": app.digest(entries),
        }

    def seed(self, entries, status="READY"):
        with app.closing(app.connect()) as db:
            db.executemany(
                "INSERT INTO records VALUES(?,?,?,?,?,?,?,?,?)",
                (
                    (
                        self.backend,
                        self.namespace,
                        entry["id"],
                        entry["scope"],
                        entry["version"],
                        status,
                        app.canonical(entry).decode(),
                        app.canonical([entry["content"]]).decode(),
                        "seed",
                    )
                    for entry in entries
                ),
            )
            db.commit()

    def assert_http_error(self, status, action, **values):
        with self.assertRaises(app.HTTPException) as caught:
            self.execute(action, **values)
        self.assertEqual(caught.exception.status_code, status)
        self.assertEqual(self.store.calls, [])

    def test_v1_packages_over_two_mib_preserve_graph_metadata_between_backends(self):
        graph = {
            "nodes": [{"id": "company", "name": "星河", "summary": "g" * 90000}],
            "valid_at": "2026-01-02T00:00:00Z",
            "sequence": 9007199254740993,
        }
        entries = [
            self.entry(id=f"graph-memory-{i}", content="迁移" * 30000, graph=graph)
            for i in range(9)
        ]
        for source, target in (("graphiti", "mem0"), ("mem0", "graphiti")):
            with self.subTest(source=source, target=target):
                self.start(target)
                package = self.package(entries, backend=source)
                self.assertGreater(len(app.canonical(package)), 2 * 1024 * 1024)
                self.assertLess(len(app.canonical(package)), 16 * 1024 * 1024)
                staged = self.execute("preview", package=package)
                self.assertTrue(staged["warnings"])
                self.assertEqual(self.store.calls, [])
                result = self.execute(
                    "apply",
                    token=staged["token"],
                    mode="skip",
                    idempotency_key=str(uuid.uuid4()),
                    confirmed=True,
                    accept_loss=True,
                )
                self.assertEqual(result["applied"], [entry["id"] for entry in entries])
                exported = self.execute("export")
                self.assertEqual(exported["backend"], target)
                self.assertEqual(exported["sha256"], app.digest(exported["entries"]))
                for original, restored in zip(entries, exported["entries"]):
                    for field in (
                        "id",
                        "title",
                        "content",
                        "type",
                        "source",
                        "scope",
                        "created_at",
                        "updated_at",
                        "graph",
                    ):
                        self.assertEqual(restored[field], original[field])
                    self.assertEqual(restored["version"], 1)

    def test_graphiti_export_import_preserves_null_graph_fields_and_checksum(self):
        graph = {
            "edges": [{"fact": "每周三发布", "invalid_at": None, "expired_at": None}],
            "nodes": [{"name": "星河", "name_embedding": None, "attributes": {"owner": None}}],
            "sequence": 9007199254740993,
            "weight": 1.0,
        }
        self.start("graphiti")
        self.seed([self.entry(graph=graph)])
        exported = self.execute("export")
        self.assertEqual(exported["entries"][0]["graph"], graph)
        self.start("graphiti")
        staged = self.execute("preview", package=exported)
        self.assertEqual(staged["items"][0]["graph"], graph)
        self.assertTrue(staged["warnings"])
        self.assertEqual(self.store.calls, [])
        result = self.execute(
            "apply",
            token=staged["token"],
            mode="skip",
            idempotency_key=str(uuid.uuid4()),
            confirmed=True,
            accept_loss=True,
        )
        self.assertEqual(result["applied"], ["product-memory"])
        restored = self.execute("export")
        self.assertEqual(restored["entries"][0]["graph"], graph)
        self.assertEqual(restored["sha256"], app.digest(restored["entries"]))

    def test_v1_preview_accepts_ten_thousand_entries_and_rejects_one_more(self):
        entries = [self.entry(id=f"memory-{i:05d}", content="偏好") for i in range(10001)]
        for backend in self.backends:
            with self.subTest(backend=backend):
                self.start(backend)
                staged = self.execute("preview", package=self.package(entries[:10000]))
                self.assertEqual(len(staged["items"]), 10000)
                self.assertEqual(staged["source_counts"], {"manual": 10000, "automatic": 0})
                self.assertEqual(self.store.calls, [])
                self.assert_http_error(413, "preview", package=self.package(entries))

    def test_v1_preview_rejects_archive_over_sixteen_mib_without_writes(self):
        entries = [self.entry(id=f"large-memory-{i}", content="x" * 262000) for i in range(65)]
        for backend in self.backends:
            with self.subTest(backend=backend):
                self.start(backend)
                package = self.package(entries)
                self.assertGreater(len(app.canonical(package)), 16 * 1024 * 1024)
                self.assert_http_error(413, "preview", package=package)

    def test_export_rejects_nonready_content_instead_of_returning_a_partial_archive(self):
        for backend in ("mem0", "graphiti", "openviking"):
            for status in ("ERROR", "INDEXING", "PENDING_DELETE"):
                with self.subTest(backend=backend, status=status):
                    self.start(backend)
                    self.seed([self.entry(id="ready-memory", version=1)])
                    self.seed([self.entry(id="unfinished-memory", version=2)], status=status)
                    self.seed([self.entry(id="deleted-memory", version=1)], status="DELETED")
                    self.assert_http_error(409, "export")

    def test_v1_export_accepts_ten_thousand_entries_and_rejects_one_more(self):
        entries = [
            self.entry(id=f"memory-{i:05d}", content="偏好", version=1) for i in range(10001)
        ]
        for backend in self.backends:
            with self.subTest(backend=backend):
                self.start(backend)
                self.seed(entries[:10000])
                exported = self.execute("export")
                self.assertEqual(len(exported["entries"]), 10000)
                self.assertEqual(exported["entries"][0]["id"], entries[0]["id"])
                self.assertEqual(exported["entries"][-1]["id"], entries[9999]["id"])
                self.assertEqual(exported["sha256"], app.digest(exported["entries"]))
                self.seed(entries[10000:])
                self.assert_http_error(413, "export")

    def test_v1_export_rejects_archive_over_sixteen_mib(self):
        entries = [
            self.entry(id=f"large-memory-{i}", content="x" * 262000, version=1) for i in range(65)
        ]
        for backend in self.backends:
            with self.subTest(backend=backend):
                self.start(backend)
                self.seed(entries)
                self.assert_http_error(413, "export")

    def test_empty_memory_export_can_be_previewed_and_imported(self):
        for backend in ("mem0", "graphiti", "openviking"):
            with self.subTest(backend=backend):
                self.start(backend)
                exported = self.execute("export")
                self.assertEqual(exported["entries"], [])
                self.assertEqual(exported["sha256"], app.digest([]))
                staged = self.execute("preview", package=exported)
                self.assertEqual(staged["items"], [])
                self.assertEqual(staged["source_counts"], {"manual": 0, "automatic": 0})
                result = self.apply(staged)
                self.assertEqual((result["applied"], result["skipped"]), ([], []))
                self.assertEqual(self.store.calls, [])

    def test_knowledge_v1_preview_keeps_nonempty_thousand_entry_limit(self):
        entries = [self.entry(id=f"document-{i}", content="知识内容") for i in range(1001)]
        for count in (0, 1000, 1001):
            with self.subTest(count=count):
                self.start("qdrant")
                inp = app.Input(
                    contract=app.CONTRACT,
                    kind="knowledge",
                    namespace=self.namespace,
                    package=self.package(entries[:count], backend="mem0"),
                )
                if count == 1000:
                    staged = app.execute("qdrant", "preview", inp)
                    self.assertEqual(len(staged["items"]), count)
                else:
                    with self.assertRaises(app.HTTPException) as caught:
                        app.execute("qdrant", "preview", inp)
                    self.assertEqual(caught.exception.status_code, 422)
                self.assertEqual(self.store.calls, [])

    def test_authenticated_preview_accepts_more_than_three_mib_for_all_memory_backends(self):
        entries = [self.entry(id=f"http-memory-{i}", content="x" * 262000) for i in range(13)]
        proxy_key, service_key = "isolated-proxy-key", "isolated-service-key"
        with patch.dict(
            os.environ, {"HC_CONTEXT_PROXY_KEY": proxy_key, "HC_CONTEXT_TEST_KEY": service_key}
        ), TestClient(app.app) as client:
            for backend in ("mem0", "graphiti", "openviking"):
                with self.subTest(backend=backend):
                    self.start(backend)
                    path = f"/{backend}/v1/preview"
                    body = app.canonical(
                        {
                            "contract": app.CONTRACT,
                            "kind": "memory",
                            "namespace": self.namespace,
                            "package": self.package(entries),
                        }
                    )
                    self.assertGreater(len(body), 3 * 1024 * 1024)
                    self.assertLess(len(body), 16 * 1024 * 1024)
                    stamp = str(int(time.time()))
                    headers = {
                        "Content-Type": "application/json",
                        "Authorization": "Bearer " + service_key,
                        "X-Context-Timestamp": stamp,
                        "X-Context-Signature": "invalid",
                    }
                    denied = client.post(path, content=body, headers=headers)
                    self.assertEqual(denied.status_code, 401)
                    self.assertEqual(self.store.calls, [])
                    headers["X-Context-Signature"] = hmac.new(
                        proxy_key.encode(),
                        stamp.encode() + b"\n" + path.encode() + b"\n" + body,
                        hashlib.sha256,
                    ).hexdigest()
                    response = client.post(path, content=body, headers=headers)
                    self.assertEqual(response.status_code, 200, response.text[:200])
                    self.assertEqual(len(response.json()["items"]), len(entries))
                    self.assertEqual(self.store.calls, [])


if __name__ == "__main__":
    unittest.main()
