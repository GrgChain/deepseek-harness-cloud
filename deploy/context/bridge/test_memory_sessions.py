"""Managed session capture shares recall/export and remains safe to replay."""
import copy
import hashlib
import json
import os
import tempfile
import unittest
import uuid
from concurrent.futures import ThreadPoolExecutor
from contextlib import closing
from unittest.mock import patch

from fastapi import HTTPException
from viking_sessions import SessionRequest

os.environ.setdefault("CONTEXT_DB", tempfile.mktemp(suffix=".db"))
import app


class Store:
    def __init__(self):
        self.points = {}
        self.puts = 0
        self.fail_at = None

    def put(self, record, chunks):
        self.puts += 1
        self.points[record["id"]] = copy.deepcopy(record)
        if self.puts == self.fail_at:
            raise RuntimeError("unknown upstream outcome")

    def delete(self, identity, version=None):
        self.points.pop(identity, None)

    def search(self, query, scope, limit):
        return [
            {"record_id": row["id"], "version": row["version"], "chunk": 0, "score": 1}
            for row in self.points.values()
            if row["scope"] == scope
        ][:limit]

    def close(self):
        pass


class SessionMemory(unittest.TestCase):
    def setUp(self):
        self.namespace = hashlib.sha256(uuid.uuid4().bytes).hexdigest()
        self.stores = {}
        self.patch = patch.object(app, "engine", side_effect=self.engine)
        self.patch.start()
        self.addCleanup(self.patch.stop)

    def engine(self, backend, namespace, *args):
        return self.stores.setdefault((backend, namespace), Store())

    def inp(self, namespace=None, **kwargs):
        return app.Input(
            contract=app.CONTRACT, namespace=namespace or self.namespace, kind="memory", **kwargs
        )

    def batch(self, backend, session="a", tenant=None, **changes):
        payload = {
            "session_id": session * 64,
            "messages": [
                {"id": str(index) * 64, "role": role, "content": role + " 周三发布"}
                for index, role in enumerate(("user", "assistant", "tool"), 1)
            ],
        }
        payload.update(changes)
        return app.execute(
            backend,
            "session",
            self.inp(
                tenant,
                session_request=SessionRequest(
                    method="POST", path="/sessions/messages", body=json.dumps(payload)
                ),
            ),
        )

    def legacy_batch(self, backend, session="a", tenant=None, **changes):
        """Exercise the former format retained only for migration compatibility."""
        from memory_sessions import Batch, append_mem0_session

        payload = {
            "session_id": session * 64,
            "messages": [
                {"id": str(index) * 64, "role": role, "content": role + " 周三发布"}
                for index, role in enumerate(("user", "assistant", "tool"), 1)
            ],
        }
        payload.update(changes)
        batch = Batch.model_validate(payload)
        with app.namespace_lock(backend, tenant or self.namespace), closing(app.connect()) as db:
            append_mem0_session(
                db,
                tenant or self.namespace,
                batch.session_id,
                batch.messages,
                self.engine(backend, tenant or self.namespace),
            )
        return {"accepted": len(batch.messages)}

    def test_mem0_appends_new_turns_to_the_same_record_and_replays_are_noops(self):
        self.legacy_batch("mem0")
        store = self.engine("mem0", self.namespace)
        original = copy.deepcopy(next(iter(store.points.values())))
        message = {"id": "4" * 64, "role": "user", "content": "改为周四发布。"}
        self.legacy_batch("mem0", messages=[message])
        self.legacy_batch("mem0", messages=[message])
        exported = app.execute("mem0", "export", self.inp())["entries"]
        self.assertEqual(len(exported), 1)
        self.assertEqual(len(store.points), 1)
        current = exported[0]
        self.assertEqual(
            (current["id"], current["created_at"]), (original["id"], original["created_at"])
        )
        self.assertEqual((current["version"], store.puts), (2, 2))
        messages = json.loads(current["content"])["messages"]
        self.assertEqual([row["role"] for row in messages], ["user", "assistant", "tool", "user"])
        self.assertEqual(messages[-1]["content"], message["content"])

    def test_concurrent_mem0_batches_preserve_both_updates_in_one_record(self):
        self.legacy_batch("mem0")
        messages = [
            {"id": str(index) * 64, "role": "user", "content": "并发消息 " + str(index)}
            for index in (4, 5)
        ]
        with ThreadPoolExecutor(max_workers=2) as executor:
            results = list(
                executor.map(
                    lambda message: self.legacy_batch("mem0", messages=[message]), messages
                )
            )
        self.assertEqual(results, [{"accepted": 1}, {"accepted": 1}])
        exported = app.execute("mem0", "export", self.inp())["entries"]
        self.assertEqual(len(exported), 1)
        self.assertEqual(len(json.loads(exported[0]["content"])["messages"]), 5)
        self.assertEqual(exported[0]["version"], 3)

    def test_failed_mem0_update_preserves_original_and_replays_pending_content(self):
        self.legacy_batch("mem0")
        store = self.engine("mem0", self.namespace)
        original = copy.deepcopy(next(iter(store.points.values())))
        store.fail_at = 2
        message = {"id": "4" * 64, "role": "assistant", "content": "新增的助手回复。"}
        with self.assertRaises(RuntimeError):
            self.legacy_batch("mem0", messages=[message])
        previous = app.execute("mem0", "get", self.inp(record_id=original["id"]))
        self.assertEqual(previous["content"], original["content"])
        self.assertEqual((previous["version"], previous["active_version"]), (2, 1))
        with self.assertRaises(HTTPException) as failed_export:
            app.execute("mem0", "export", self.inp())
        self.assertEqual(failed_export.exception.status_code, 409)
        self.legacy_batch("mem0", messages=[message])
        current = app.execute("mem0", "export", self.inp())["entries"]
        self.assertEqual(len(current), 1)
        self.assertEqual(len(json.loads(current[0]["content"])["messages"]), 4)
        self.assertEqual((current[0]["version"], len(store.points)), (2, 1))

    def test_manually_edited_mem0_session_is_not_overwritten_by_new_messages(self):
        self.legacy_batch("mem0")
        original = next(iter(self.engine("mem0", self.namespace).points.values()))
        preview = app.execute(
            "mem0",
            "preview",
            self.inp(record_id=original["id"], record_version=1, content="我手动整理的会话。"),
        )
        app.execute(
            "mem0",
            "apply",
            self.inp(
                token=preview["token"],
                mode="replace",
                confirmed=True,
                idempotency_key="edit-session",
            ),
        )
        self.legacy_batch("mem0", messages=[{"id": "4" * 64, "role": "user", "content": "新消息"}])
        current = app.execute("mem0", "export", self.inp())["entries"]
        self.assertEqual(len(current), 1)
        self.assertEqual((current[0]["version"], current[0]["content"]), (2, "我手动整理的会话。"))

    def seed_legacy(self):
        from datetime import datetime, timezone

        # Seed the actual former format independently of today's ingestion path.
        store = self.engine("mem0", self.namespace)
        with closing(app.connect()) as db:
            for index, role in enumerate(("user", "assistant", "tool"), 1):
                stamp = datetime.now(timezone.utc).isoformat()
                message_id = str(index) * 64
                identity = hashlib.sha256(
                    (self.namespace + ":dsh-session:" + "a" * 64 + ":" + message_id).encode()
                ).hexdigest()
                content = json.dumps(
                    {
                        "session_id": "a" * 64,
                        "message_id": message_id,
                        "role": role,
                        "content": role + " 历史原文",
                    },
                    ensure_ascii=False,
                )
                record = {
                    "id": identity,
                    "scope": "personal",
                    "version": 1,
                    "title": role + " · 历史原文",
                    "content": content,
                    "type": "conversation",
                    "source": "DSH 自动同步",
                    "created_at": stamp,
                    "updated_at": stamp,
                }
                db.execute(
                    "INSERT INTO records VALUES('mem0',?,?,'personal',1,'READY',?,?,?)",
                    (
                        self.namespace,
                        identity,
                        json.dumps(record, ensure_ascii=False),
                        json.dumps([content]),
                        "dsh-session-" + message_id,
                    ),
                )
                store.put(record, [content])
            db.commit()
        return store

    def test_existing_mem0_messages_merge_without_losing_content_or_resurrecting_deletions(self):
        from memory_sessions import consolidate_mem0_sessions

        store = self.seed_legacy()
        original = list(store.points.values())
        deleted = original[1]
        app.execute(
            "mem0",
            "delete",
            self.inp(
                record_id=deleted["id"],
                record_version=1,
                confirmed=True,
                idempotency_key="delete-old-message",
            ),
        )
        with closing(app.connect()) as db:
            self.assertEqual(consolidate_mem0_sessions(db, self.namespace, store), 1)
            self.assertEqual(consolidate_mem0_sessions(db, self.namespace, store), 0)
        self.batch("mem0")  # DSH can replay the original message IDs after an upgrade.
        exported = app.execute("mem0", "export", self.inp())["entries"]
        self.assertEqual((len(exported), len(store.points)), (1, 1))
        messages = json.loads(exported[0]["content"])["messages"]
        self.assertEqual([row["role"] for row in messages], ["user", "tool"])
        self.assertEqual([row["content"] for row in messages], ["user 历史原文", "tool 历史原文"])
        self.assertEqual(exported[0]["created_at"], original[0]["created_at"])

    def test_legacy_mem0_merge_retries_unknown_writes_before_retiring_originals(self):
        from memory_sessions import consolidate_mem0_sessions

        store = self.seed_legacy()
        store.fail_at = 4
        with closing(app.connect()) as db:
            with self.assertRaises(RuntimeError):
                consolidate_mem0_sessions(db, self.namespace, store)
            self.assertEqual(
                db.execute(
                    "SELECT count(*) FROM records WHERE backend='mem0' "
                    "AND namespace=? AND status='READY'",
                    (self.namespace,),
                ).fetchone()[0],
                3,
            )
            self.assertEqual(consolidate_mem0_sessions(db, self.namespace, store), 1)
        exported = app.execute("mem0", "export", self.inp())["entries"]
        self.assertEqual((len(exported), len(store.points)), (1, 1))
        self.assertEqual(len(json.loads(exported[0]["content"])["messages"]), 3)

    def test_oversized_mem0_session_is_rejected_before_changing_saved_content(self):
        self.legacy_batch(
            "mem0", messages=[{"id": "1" * 64, "role": "user", "content": "a" * 20000}]
        )
        store = self.engine("mem0", self.namespace)
        original = copy.deepcopy(store.points)
        with self.assertRaises(HTTPException) as overflow:
            self.legacy_batch(
                "mem0", messages=[{"id": "2" * 64, "role": "tool", "content": "b" * (240 * 1024)}]
            )
        self.assertEqual(overflow.exception.status_code, 413)
        self.assertEqual(store.points, original)
        self.assertEqual(store.puts, 1)
        self.assertEqual(app.execute("mem0", "export", self.inp())["entries"][0]["version"], 1)

    def test_invalid_batches_and_identity_overrides_write_nothing(self):
        for payload in (
            {"namespace": "b" * 64},
            {"messages": [{"id": "1" * 64, "role": "system", "content": "do not capture"}]},
            {
                "messages": [
                    {"id": "1" * 64, "role": "user", "content": "valid"},
                    {"id": "2" * 64, "role": "tool", "content": "\x00"},
                ]
            },
        ):
            with self.subTest(payload=payload), self.assertRaises(HTTPException):
                self.batch("mem0", **payload)
        self.assertEqual(self.engine("mem0", self.namespace).puts, 0)
        for request in (
            SessionRequest(method="GET", path="/sessions/messages"),
            SessionRequest(method="POST", path="/memories", body="{}"),
            SessionRequest(
                method="POST", path="/sessions/messages", headers={"user_id": "foreign"}
            ),
        ):
            with self.subTest(request=request), self.assertRaises(HTTPException):
                app.execute("mem0", "session", self.inp(session_request=request))


if __name__ == "__main__":
    unittest.main()
