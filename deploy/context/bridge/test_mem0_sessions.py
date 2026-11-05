"""Raw session persistence, commit boundaries, extraction receipts and isolation."""
import hashlib
import json
import os
import tempfile
import unittest
import uuid
from unittest.mock import patch

import httpx
from fastapi import HTTPException

os.environ.setdefault("CONTEXT_DB", tempfile.mktemp(suffix=".db"))
import app
from memories import Memories
from test_memory_sessions import Store
from viking_sessions import SessionRequest


class ExtractingStore(Store):
    def __init__(self):
        super().__init__()
        self.extractions = []
        self.facts = ["星河项目每周三下午发布。"]
        self.extract_failure = False
        self.cleaned = []

    def extract_session(self, messages, commit_id):
        self.extractions.append((messages, commit_id))
        if self.extract_failure:
            raise RuntimeError("extraction unavailable")
        return self.facts

    def cleanup_extraction(self, commit_id):
        self.cleaned.append(commit_id)


class ArchiveCases:
    def setUp(self):
        self.namespace = hashlib.sha256(uuid.uuid4().bytes).hexdigest()
        self.store = ExtractingStore()
        self.patcher = patch.object(app, "engine", return_value=self.store)
        self.patcher.start()
        self.addCleanup(self.patcher.stop)

    def inp(self, **kw):
        return app.Input(contract=app.CONTRACT, namespace=self.namespace, kind="memory", **kw)

    def send(self, path="/sessions/messages", **body):
        return app.execute(
            self.backend,
            "session",
            self.inp(
                session_request=SessionRequest(
                    method="POST", path=path, body=json.dumps({"session_id": "a" * 64, **body})
                )
            ),
        )

    def batch(self):
        return self.send(
            messages=[
                {"id": str(i) * 64, "role": role, "content": content}
                for i, role, content in [
                    (1, "user", "星河项目周三发布"),
                    (2, "assistant", "收到"),
                    (3, "tool", "测试通过"),
                ]
            ]
        )

    def test_raw_roles_sync_without_becoming_searchable_memories(self):
        self.assertEqual(self.batch()["accepted"], 3)
        self.batch()
        self.assertEqual(self.store.points, {})
        self.assertEqual(self.store.extractions, [])
        self.assertEqual(app.execute(self.backend, "list", self.inp())["items"], [])
        self.assertEqual(app.execute(self.backend, "search", self.inp(query="发布"))["items"], [])
        with app.closing(app.connect()) as db:
            rows = db.execute(
                f"SELECT role FROM {self.backend}_session_messages WHERE namespace=? ORDER BY seq",
                (self.namespace,),
            ).fetchall()
        self.assertEqual([row["role"] for row in rows], ["user", "assistant", "tool"])
        self.assertFalse(self.send("/sessions/commit")["committed"])

    def test_forced_archive_indexes_only_extracted_facts_and_is_idempotent(self):
        self.batch()
        result = self.send("/sessions/commit", force=True)
        self.assertEqual(
            (result["committed"], result["memories"], result["pending_messages"]), (True, 1, 0)
        )
        hits = app.execute(
            self.backend, "search", self.inp(query="发布", memory_source="automatic")
        )["items"]
        self.assertEqual(len(hits), 1)
        self.assertEqual(
            (hits[0]["memory_source"], hits[0]["snippet"]), ("automatic", self.store.facts[0])
        )
        self.assertEqual(
            app.execute(self.backend, "search", self.inp(query="发布", memory_source="manual"))[
                "items"
            ],
            [],
        )
        self.assertEqual(
            app.execute(self.backend, "export", self.inp())["entries"][0]["content"],
            self.store.facts[0],
        )
        self.assertFalse(self.send("/sessions/commit", force=True)["committed"])
        self.assertEqual(len(self.store.extractions), 1)
        self.assertEqual(len(self.store.cleaned), 1)
        self.batch()
        self.assertEqual(self.send("/sessions/status")["pending_messages"], 0)

    def test_threshold_commits_and_other_owners_cannot_read_raw_sessions(self):
        result = self.send(messages=[{"id": "1" * 64, "role": "user", "content": "a" * 80000}])
        self.assertEqual(result["pending_messages"], 0)
        self.assertEqual(len(self.store.extractions), 1)
        self.namespace = hashlib.sha256(uuid.uuid4().bytes).hexdigest()
        self.assertEqual(self.send("/sessions/status")["pending_messages"], 0)
        self.assertFalse(self.send("/sessions/commit", force=True)["committed"])
        self.assertEqual(app.execute(self.backend, "list", self.inp())["items"], [])

    def test_extraction_failure_retains_raw_messages_then_recovers(self):
        self.batch()
        self.store.extract_failure = True
        with self.assertRaises(RuntimeError):
            self.send("/sessions/commit", force=True)
        self.assertEqual(self.send("/sessions/status")["pending_messages"], 3)
        self.assertEqual(self.store.points, {})
        self.store.extract_failure = False
        self.assertTrue(self.send("/sessions/commit", force=True)["committed"])

    def test_index_retry_reuses_the_extraction_receipt(self):
        self.batch()
        self.store.fail_at = 1
        with self.assertRaises(RuntimeError):
            self.send("/sessions/commit", force=True)
        self.assertEqual(app.execute(self.backend, "search", self.inp(query="发布"))["items"], [])
        self.assertTrue(self.send("/sessions/commit", force=True)["committed"])
        self.assertEqual((len(self.store.extractions), len(self.store.points)), (1, 1))

    def test_no_extracted_facts_is_a_successful_archive(self):
        self.store.facts = []
        self.batch()
        self.assertEqual(self.send("/sessions/commit", force=True)["memories"], 0)
        self.assertEqual(self.store.points, {})
        self.assertEqual(self.send("/sessions/status")["pending_messages"], 0)

    def test_changed_replay_is_rejected_without_rewriting_saved_raw_content(self):
        self.batch()
        with self.assertRaises(HTTPException) as changed:
            self.send(messages=[{"id": "1" * 64, "role": "user", "content": "改写的原文"}])
        self.assertEqual(changed.exception.status_code, 409)
        self.assertEqual(self.send("/sessions/status")["pending_messages"], 3)

    def test_deleted_content_is_not_resurrected_by_a_replay(self):
        self.batch()
        self.send("/sessions/commit", force=True)
        identity = next(iter(self.store.points))
        app.execute(
            self.backend,
            "delete",
            self.inp(
                record_id=identity,
                record_version=1,
                confirmed=True,
                idempotency_key="delete-archived-fact",
            ),
        )
        self.batch()
        self.send("/sessions/commit", force=True)
        # The same extracted fact in another archive must also respect deletion.
        self.send(messages=[{"id": "4" * 64, "role": "user", "content": "再次约定周三发布"}])
        self.send("/sessions/commit", force=True)
        self.assertEqual(self.store.points, {})
        self.assertEqual(app.execute(self.backend, "list", self.inp())["items"], [])

    def test_manual_and_archived_facts_share_management_without_exposing_raw_sessions(self):
        self.batch()
        self.send("/sessions/commit", force=True)
        preview = app.execute(
            self.backend, "preview", self.inp(title="手动约定", content="请使用中文回复。")
        )
        app.execute(
            self.backend,
            "apply",
            self.inp(
                token=preview["token"],
                mode="skip",
                confirmed=True,
                idempotency_key=str(uuid.uuid4()),
            ),
        )
        listed = app.execute(self.backend, "list", self.inp())["items"]
        self.assertEqual(len(listed), 2)
        for source in ("automatic", "manual"):
            expected = [row["id"] for row in listed if row["memory_source"] == source]
            hits = app.execute(
                self.backend, "search", self.inp(query="约定", memory_source=source)
            )["items"]
            self.assertEqual([row["id"] for row in hits], expected)
            self.assertEqual(len(expected), 1)


class GraphitiArchive(ArchiveCases, unittest.TestCase):
    backend = "graphiti"

    def test_graph_metadata_is_preserved_with_extracted_memory(self):
        original = self.store.put

        def put(record, chunks):
            original(record, chunks)
            return {"graph": {"edges": [{"fact": record["content"]}], "nodes": []}}

        self.store.put = put
        self.batch()
        self.send("/sessions/commit", force=True)
        record = app.execute(self.backend, "get", self.inp(record_id=next(iter(self.store.points))))
        self.assertEqual(record["graph"]["edges"][0]["fact"], self.store.facts[0])

    def test_backend_sessions_are_isolated_even_with_the_same_namespace(self):
        self.batch()
        self.backend = "mem0"
        self.assertEqual(self.send("/sessions/status")["pending_messages"], 0)
        self.assertFalse(self.send("/sessions/commit", force=True)["committed"])
        self.backend = "graphiti"
        self.assertEqual(self.send("/sessions/status")["pending_messages"], 3)


class Mem0Archive(ArchiveCases, unittest.TestCase):
    backend = "mem0"

    def test_native_inference_uses_isolated_staging_and_recovers_unknown_writes(self):
        requests = []
        staged = []

        def reply(request):
            requests.append(request)
            if request.method == "GET":
                return httpx.Response(200, json={"results": staged})
            if request.method == "POST":
                body = json.loads(request.content)
                self.assertTrue(body["infer"])
                self.assertNotEqual(body["user_id"], self.namespace)
                staged.append(
                    {"id": str(uuid.uuid4()), "memory": "星河项目周三发布", "event": "ADD"}
                )
                return httpx.Response(200, json={"results": staged})
            return httpx.Response(200, json={})

        target = object.__new__(Memories)
        target.namespace = self.namespace
        with httpx.Client(
            base_url="https://mem0.example", transport=httpx.MockTransport(reply)
        ) as target.http:
            first = target.extract_session([{"role": "user", "content": "周三发布"}], "b" * 64)
            self.assertEqual(target.extract_session([], "b" * 64), first)
            self.assertEqual(sum(request.method == "POST" for request in requests), 1)
            target.cleanup_extraction("b" * 64)
            self.assertEqual(requests[-1].method, "DELETE")


if __name__ == "__main__":
    unittest.main()
