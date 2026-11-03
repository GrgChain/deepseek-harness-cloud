"""Shared Mem0/Graphiti lifecycle and v1 archive recovery, without vendor IO."""
import copy
import hashlib
import os
import tempfile
import unittest
import uuid
from unittest.mock import patch

os.environ.setdefault("CONTEXT_DB", tempfile.mktemp(suffix=".db"))
import app


class ManualMemoryStore:
    def __init__(self):
        self.values = {}
        self.calls = []
        self.stale_hits = []
        self.fail_next_put = False

    def delete(self, identity, version=None):
        self.calls.append(("delete", identity, version))
        self.values = {
            key: value
            for key, value in self.values.items()
            if key[0] != identity or version is not None and key[1] != version
        }

    def put(self, entry, chunks):
        self.calls.append(("put", entry["id"], entry["version"]))
        if self.fail_next_put:
            self.fail_next_put = False
            raise RuntimeError("temporary memory write failure")
        self.values[(entry["id"], entry["version"])] = copy.deepcopy(entry)

    def search(self, query, scope, limit):
        current = [
            {"record_id": identity, "version": version, "chunk": 0, "score": 0.9}
            for (identity, version), value in self.values.items()
            if value["scope"] == scope
        ]
        return copy.deepcopy(self.stale_hits + current)[:limit]

    def close(self):
        pass


class ManualMemoryFixture(unittest.TestCase):
    backends = ("mem0", "graphiti")

    def setUp(self):
        def engine(backend, namespace, service=None, embedding_config=None):
            self.assertEqual((backend, namespace), (self.backend, self.namespace))
            return self.store

        patcher = patch.object(app, "engine", side_effect=engine)
        patcher.start()
        self.addCleanup(patcher.stop)

    def start(self, backend):
        self.backend = backend
        self.namespace = hashlib.sha256(uuid.uuid4().bytes).hexdigest()
        self.store = ManualMemoryStore()

    def execute(self, action, **values):
        return app.execute(
            self.backend,
            action,
            app.Input(contract=app.CONTRACT, kind="memory", namespace=self.namespace, **values),
        )

    def entry(self, **values):
        return {
            "id": "product-memory",
            "title": "产品",
            "content": "产品名称是星河。",
            "type": "note",
            "source": "本人确认",
            "scope": "personal",
            "version": 7,
            "created_at": "2026-01-02T00:00:00Z",
            "updated_at": "2026-11-02T00:00:00Z",
            **values,
        }

    def preview(self, entries):
        return self.execute(
            "preview",
            package={
                "schema": "hc-memory-v1",
                "backend": self.backend,
                "exported_at": app.now(),
                "entries": entries,
                "sha256": app.digest(entries),
            },
        )

    def apply(self, staged, mode="skip", operation=None):
        return self.execute(
            "apply",
            token=staged["token"],
            mode=mode,
            idempotency_key=operation or str(uuid.uuid4()),
            confirmed=True,
        )

    def delete(self, identity, version, operation=None):
        return self.execute(
            "delete",
            record_id=identity,
            record_version=version,
            idempotency_key=operation or str(uuid.uuid4()),
            confirmed=True,
        )


class ManualMemoryLifecycle(ManualMemoryFixture):
    def test_delete_excludes_memory_from_export_list_get_and_stale_search(self):
        for backend in self.backends:
            with self.subTest(backend=backend):
                self.start(backend)
                target = self.entry()
                survivor = self.entry(
                    id="remaining-memory", title="保留的记忆", content="请用中文回答。"
                )
                self.apply(self.preview([target, survivor]))
                self.assertEqual(len(self.execute("export")["entries"]), 2)
                operation = str(uuid.uuid4())
                result = self.delete(target["id"], 1, operation)
                self.assertEqual(result, {"deleted": True, "rebuilt": False})
                self.assertEqual(self.store.calls[-1], ("delete", target["id"], None))
                self.assertNotIn((target["id"], 1), self.store.values)
                self.assertIn((survivor["id"], 1), self.store.values)
                calls = list(self.store.calls)
                self.assertEqual(self.delete(target["id"], 1, operation), result)
                self.assertEqual(self.store.calls, calls)

                # Even an upstream index returning an old hit must not revive it.
                self.store.stale_hits = [
                    {"record_id": target["id"], "version": 1, "chunk": 0, "score": 1.0}
                ]
                package = self.execute("export")
                self.assertEqual((package["schema"], package["backend"]), ("hc-memory-v1", backend))
                self.assertEqual([row["id"] for row in package["entries"]], [survivor["id"]])
                self.assertEqual(package["sha256"], app.digest(package["entries"]))
                self.assertEqual(
                    [row["id"] for row in self.execute("list")["items"]], [survivor["id"]]
                )
                with self.assertRaises(app.HTTPException) as caught:
                    self.execute("get", record_id=target["id"])
                self.assertEqual(caught.exception.status_code, 404)
                found = self.execute("search", query="产品叫什么", limit=5)
                self.assertEqual([row["id"] for row in found["items"]], [survivor["id"]])

    def test_deleted_memory_previews_as_new_and_default_import_increments_local_version(self):
        for backend in self.backends:
            with self.subTest(backend=backend):
                self.start(backend)
                self.apply(self.preview([self.entry()]))
                changed = self.execute(
                    "preview",
                    record_id="product-memory",
                    record_version=1,
                    title="产品",
                    content="产品名称已改为星海。",
                )
                self.apply(changed, mode="replace")
                package = self.execute("export")
                self.assertEqual(package["entries"][0]["version"], 2)
                self.delete("product-memory", 2)
                self.assertEqual(self.execute("export")["entries"], [])

                staged = self.execute("preview", package=package)
                self.assertFalse(staged["items"][0]["conflict"])
                self.assertEqual(staged["items"][0]["memory_source"], "manual")
                operation = str(uuid.uuid4())
                result = self.apply(staged, operation=operation)
                self.assertEqual(result["applied"], ["product-memory"])
                self.assertEqual(result["skipped"], [])
                current = self.execute("get", record_id="product-memory")
                self.assertEqual(
                    (current["version"], current["content"]), (3, "产品名称已改为星海。")
                )
                calls = list(self.store.calls)
                self.assertEqual(self.apply(staged, operation=operation), result)
                self.assertEqual(self.store.calls, calls)

    def test_default_import_skips_live_conflicts_and_restores_only_deleted_memory(self):
        for backend in self.backends:
            with self.subTest(backend=backend):
                self.start(backend)
                live = self.entry(id="live-memory", title="当前偏好", content="保留当前内容。")
                deleted = self.entry()
                self.apply(self.preview([live, deleted]))
                self.delete(deleted["id"], 1)
                staged = self.preview([{**live, "content": "旧备份内容。"}, deleted])
                self.assertEqual(
                    {row["id"]: row["conflict"] for row in staged["items"]},
                    {live["id"]: True, deleted["id"]: False},
                )
                calls = len(self.store.calls)
                result = self.apply(staged)
                self.assertEqual(result["applied"], [deleted["id"]])
                self.assertEqual(result["skipped"], [live["id"]])
                self.assertEqual(
                    self.store.calls[calls:],
                    [("delete", deleted["id"], 2), ("put", deleted["id"], 2)],
                )
                current = self.execute("get", record_id=live["id"])
                self.assertEqual((current["version"], current["content"]), (1, live["content"]))

    def test_delete_after_preview_invalidates_old_import_before_any_remote_write(self):
        for backend in self.backends:
            for mode in ("skip", "replace"):
                with self.subTest(backend=backend, mode=mode):
                    self.start(backend)
                    self.apply(self.preview([self.entry()]))
                    staged = self.preview([self.entry(content="旧备份内容。")])
                    self.delete("product-memory", 1)
                    calls = list(self.store.calls)
                    with self.assertRaises(app.HTTPException) as caught:
                        self.apply(staged, mode=mode)
                    self.assertEqual(caught.exception.status_code, 409)
                    self.assertEqual(self.store.calls, calls)
                    self.assertEqual(self.execute("list")["items"], [])
                    self.assertEqual(self.execute("export")["entries"], [])

    def test_interrupted_restore_replays_same_operation_after_preview_expiry(self):
        for backend in self.backends:
            with self.subTest(backend=backend):
                self.start(backend)
                self.apply(self.preview([self.entry()]))
                self.delete("product-memory", 1)
                staged = self.preview([self.entry()])
                operation = str(uuid.uuid4())
                self.store.fail_next_put = True
                with self.assertRaises(RuntimeError):
                    self.apply(staged, operation=operation)
                with app.closing(app.connect()) as db:
                    row = db.execute(
                        "SELECT version,status FROM records WHERE backend=? AND namespace=? AND id=?",
                        (backend, self.namespace, "product-memory"),
                    ).fetchone()
                    self.assertEqual((row["version"], row["status"]), (2, "ERROR"))
                    db.execute(
                        "UPDATE previews SET expires=0 WHERE backend=? AND namespace=?",
                        (backend, self.namespace),
                    )
                    db.commit()
                result = self.apply(staged, operation=operation)
                self.assertEqual(result["applied"], ["product-memory"])
                self.assertEqual(result["skipped"], [])
                self.assertEqual(self.execute("get", record_id="product-memory")["version"], 2)
                self.assertEqual(set(self.store.values), {("product-memory", 2)})
                calls = list(self.store.calls)
                self.assertEqual(self.apply(staged, operation=operation), result)
                self.assertEqual(self.store.calls, calls)


if __name__ == "__main__":
    unittest.main()
