"""Complete OpenViking archives, concurrent edits and interrupted imports."""
import copy
import hashlib
import os
import tempfile
import unittest
import uuid
from unittest.mock import patch

os.environ.setdefault("CONTEXT_DB", tempfile.mktemp(suffix=".db"))
import app
import memory_archive
from viking_memory import VikingMemory


class ManualStore:
    def __init__(self):
        self.values = {}
        self.puts = 0
        self.closed = 0

    def delete(self, identity, version=None):
        self.values = {
            key: value
            for key, value in self.values.items()
            if key[0] != identity or version is not None and key[1] != version
        }

    def put(self, entry, chunks):
        self.puts += 1
        self.values[(entry["id"], entry["version"])] = copy.deepcopy(entry)

    def close(self):
        self.closed += 1


class NativeStore:
    session_uri = staticmethod(VikingMemory.session_uri)

    def __init__(self, values=None):
        self.values = dict(values or {})
        self.puts = []
        self.fail_before = set()
        self.fail_after = set()
        self.indexed = set()
        self.closed = 0

    def session_export(self):
        return [{"uri": uri, "content": text} for uri, text in sorted(self.values.items())]

    def session_read_optional(self, uri):
        return self.values.get(uri)

    def session_put(self, uri, content):
        self.puts.append(uri)
        if uri in self.fail_before:
            self.fail_before.remove(uri)
            raise RuntimeError("temporary write outage")
        self.values[uri] = content
        if uri in self.fail_after:
            self.fail_after.remove(uri)
            self.indexed.discard(uri)
            raise RuntimeError("index failed after raw write")
        self.indexed.add(uri)

    def close(self):
        self.closed += 1


class MemoryArchive(unittest.TestCase):
    first = "viking://user/owner/memories/preferences/language.md"
    second = "viking://user/owner/peers/project/memories/experiences/release.md"

    def setUp(self):
        self.namespace = hashlib.sha256(uuid.uuid4().bytes).hexdigest()
        self.native_namespace = hashlib.sha256(uuid.uuid4().bytes).hexdigest()
        self.manual = ManualStore()
        self.native = NativeStore()

        def engine(backend, namespace, service=None, embedding_config=None):
            self.assertEqual(backend, "openviking")
            return self.native if namespace == self.native_namespace else self.manual

        self.patcher = patch.object(app, "engine", side_effect=engine)
        self.patcher.start()
        self.addCleanup(self.patcher.stop)

    def input(self, **values):
        return app.Input(
            contract=app.CONTRACT,
            namespace=self.namespace,
            session_namespace=self.native_namespace,
            kind="memory",
            **values,
        )

    def execute(self, action, **values):
        inp = self.input(**values)
        if action not in ("export", "preview", "apply", "search"):
            inp.session_namespace = None
        return app.execute("openviking", action, inp)

    def entry(self, **values):
        return {
            "id": "manual-answer",
            "title": "回答偏好",
            "content": "# 偏好\n\n请用中文回答。",
            "type": "note",
            "source": "本人确认",
            "scope": "personal",
            "version": 4,
            "created_at": "2026-01-02T00:00:00Z",
            "updated_at": "2026-11-02T00:00:00Z",
            **values,
        }

    def archive(self, entries=None, automatic=None):
        entries, automatic = entries or [], automatic or []
        return {
            "schema": "hc-memory-v2",
            "backend": "openviking",
            "exported_at": "2026-11-02T00:00:00Z",
            "entries": entries,
            "automatic_entries": automatic,
            "sha256": app.digest({"entries": entries, "automatic_entries": automatic}),
        }

    def preview(self, entries=None, automatic=None):
        return self.execute("preview", package=self.archive(entries, automatic))

    def apply(self, preview, operation=None, mode="skip"):
        return self.execute(
            "apply",
            token=preview["token"],
            idempotency_key=operation or str(uuid.uuid4()),
            mode=mode,
            confirmed=True,
        )

    def delete(self, identity, version):
        return self.execute(
            "delete",
            record_id=identity,
            record_version=version,
            idempotency_key=str(uuid.uuid4()),
            confirmed=True,
        )

    def test_deleted_manual_memory_imports_as_new_with_next_local_version(self):
        self.apply(self.preview([self.entry()]))
        changed = self.execute(
            "preview",
            record_id="manual-answer",
            record_version=1,
            title="回答偏好",
            content="更新后的偏好",
        )
        self.apply(changed, mode="replace")
        package = self.execute("export")
        self.assertEqual(package["entries"][0]["version"], 2)
        self.delete("manual-answer", 2)
        self.assertEqual(self.execute("export")["entries"], [])
        staged = self.execute("preview", package=package)
        self.assertFalse(staged["items"][0]["conflict"])
        operation = str(uuid.uuid4())
        result = self.apply(staged, operation)
        self.assertEqual(result["applied"], ["manual-answer"])
        self.assertEqual(result["skipped"], [])
        current = self.execute("get", record_id="manual-answer")
        self.assertEqual((current["version"], current["content"]), (3, "更新后的偏好"))
        puts = self.manual.puts
        self.assertEqual(self.apply(staged, operation), result)
        self.assertEqual(self.manual.puts, puts)

    def test_skip_keeps_live_manual_memory_and_restores_only_deleted_memory(self):
        original = self.entry(id="live-memory", content="保留当前内容")
        deleted = self.entry(id="deleted-memory")
        self.apply(self.preview([original, deleted]))
        self.delete(deleted["id"], 1)
        staged = self.preview([{**original, "content": "旧备份内容"}, deleted])
        self.assertEqual(
            {row["id"]: row["conflict"] for row in staged["items"]},
            {original["id"]: True, deleted["id"]: False},
        )
        result = self.apply(staged)
        self.assertEqual(result["applied"], [deleted["id"]])
        self.assertEqual(result["skipped"], [original["id"]])
        current = self.execute("get", record_id=original["id"])
        self.assertEqual((current["version"], current["content"]), (1, original["content"]))

    def test_deletion_after_preview_invalidates_manual_import_before_writes(self):
        self.apply(self.preview([self.entry()]))
        staged = self.preview([self.entry(content="旧备份内容")])
        self.delete("manual-answer", 1)
        puts = self.manual.puts
        for mode in ("skip", "replace"):
            with self.subTest(mode=mode), self.assertRaises(app.HTTPException) as caught:
                self.apply(staged, mode=mode)
            self.assertEqual(caught.exception.status_code, 409)
        self.assertEqual(self.manual.puts, puts)
        self.assertEqual(self.execute("export")["entries"], [])

    def test_interrupted_deleted_memory_restore_replays_after_preview_expiry(self):
        self.apply(self.preview([self.entry()]))
        self.delete("manual-answer", 1)
        staged = self.preview([self.entry()], [{"uri": self.first, "content": "自动记忆"}])
        operation = str(uuid.uuid4())
        self.native.fail_before.add(self.first)
        with self.assertRaises(RuntimeError):
            self.apply(staged, operation)
        self.assertEqual(self.execute("get", record_id="manual-answer")["version"], 2)
        with app.closing(app.connect()) as db:
            db.execute("UPDATE previews SET expires=0 WHERE namespace=?", (self.namespace,))
            db.commit()
        result = self.apply(staged, operation)
        self.assertEqual(set(result["applied"]), {"manual-answer", self.first})
        self.assertEqual(result["skipped"], [])
        self.assertEqual(self.execute("get", record_id="manual-answer")["version"], 2)
        self.assertEqual(self.native.values[self.first], "自动记忆")
        puts = self.manual.puts
        self.assertEqual(self.apply(staged, operation), result)
        self.assertEqual(self.manual.puts, puts)

    def test_later_deletion_is_not_mistaken_for_same_import_replay(self):
        staged = self.preview([self.entry()], [{"uri": self.first, "content": "自动记忆"}])
        operation = str(uuid.uuid4())
        self.native.fail_before.add(self.first)
        with self.assertRaises(RuntimeError):
            self.apply(staged, operation)
        self.delete("manual-answer", 1)
        puts = self.manual.puts
        with self.assertRaises(app.HTTPException) as caught:
            self.apply(staged, operation)
        self.assertEqual(caught.exception.status_code, 409)
        self.assertEqual(self.manual.puts, puts)
        self.assertEqual(self.execute("export")["entries"], [])

    def test_legacy_pending_archive_keeps_skip_decisions_without_deleted_snapshot(self):
        live = self.entry(id="live-memory", content="保留内容")
        deleted = self.entry(id="deleted-memory")
        self.apply(self.preview([live, deleted]))
        self.delete(deleted["id"], 1)
        staged = self.preview([live, deleted], [{"uri": self.first, "content": "自动记忆"}])
        with app.closing(app.connect()) as db:
            row = db.execute(
                "SELECT payload FROM previews WHERE namespace=? AND id=?",
                (self.namespace, staged["token"]),
            ).fetchone()
            payload = app.json.loads(row["payload"])
            payload.pop("expected_deleted")
            db.execute(
                "UPDATE previews SET payload=? WHERE namespace=? AND id=?",
                (app.canonical(payload).decode(), self.namespace, staged["token"]),
            )
            db.commit()
        operation = str(uuid.uuid4())
        self.native.fail_before.add(self.first)
        with self.assertRaises(RuntimeError):
            self.apply(staged, operation)
        with app.closing(app.connect()) as db:
            db.execute("UPDATE previews SET expires=0 WHERE namespace=?", (self.namespace,))
            db.commit()
        result = self.apply(staged, operation)
        self.assertEqual(result["applied"], [self.first])
        self.assertEqual(set(result["skipped"]), {live["id"], deleted["id"]})
        self.assertEqual(self.execute("get", record_id=live["id"])["content"], live["content"])
        with self.assertRaises(app.HTTPException) as caught:
            self.execute("get", record_id=deleted["id"])
        self.assertEqual(caught.exception.status_code, 404)

    def test_complete_export_includes_current_manual_and_all_native_files(self):
        staged = self.execute("preview", title="项目约定", content="完整手动正文")
        self.apply(staged)
        self.native.values = {
            f"viking://user/owner/memories/events/event-{i}.md": "完整自动正文 " + str(i)
            for i in range(1005)
        }
        # A similarly named folder in the native tenant remains native content.
        nested = "viking://user/owner/memories/harness_" + "a" * 64 + "/native.md"
        self.native.values[nested] = "原生合法路径也完整导出"
        package = self.execute("export")
        self.assertEqual(package["schema"], "hc-memory-v2")
        self.assertEqual(len(package["entries"]), 1)
        self.assertEqual(len(package["automatic_entries"]), 1006)
        self.assertEqual(package["entries"][0]["content"], "完整手动正文")
        self.assertIn(
            {"uri": nested, "content": self.native.values[nested]}, package["automatic_entries"]
        )
        self.assertEqual(
            package["sha256"],
            app.digest(
                {"entries": package["entries"], "automatic_entries": package["automatic_entries"]}
            ),
        )
        self.assertEqual(self.native.puts, [])
        self.assertGreater(self.native.closed, 0)

    def test_export_refuses_nonready_manual_record_instead_of_omitting_it(self):
        staged = self.execute("preview", title="项目约定", content="完整手动正文")
        self.apply(staged)
        with app.closing(app.connect()) as db:
            db.execute("UPDATE records SET status='ERROR' WHERE namespace=?", (self.namespace,))
            db.commit()
        with self.assertRaises(app.HTTPException) as caught:
            self.execute("export")
        self.assertEqual(caught.exception.status_code, 409)

    def test_v1_import_and_receipt_digest_remain_compatible(self):
        entries = [self.entry()]
        old = {
            "schema": "hc-memory-v1",
            "backend": "mem0",
            "exported_at": app.now(),
            "entries": entries,
            "sha256": app.digest(entries),
        }
        staged = self.execute("preview", package=old)
        self.assertEqual(staged["source_counts"], {"manual": 1, "automatic": 0})
        self.apply(staged)
        self.assertEqual(self.native.values, {})
        current = self.input(token="preview", idempotency_key="old-receipt")
        previous = app.Input(**current.model_dump(exclude={"session_namespace"}))
        self.assertEqual(app.operation_input(current), app.operation_input(previous))
        self.assertNotIn("session_namespace", app.operation_input(current))

    def test_v2_preview_is_readonly_and_import_preserves_both_sources(self):
        text = (
            "# 自动记忆\n\n"
            + "模型生成的完整偏好。" * 150
            + '\n<!-- MEMORY_FIELDS: {"category":"preferences","score":0.8} -->\n'
        )
        staged = self.preview(
            [self.entry()],
            [{"uri": self.first, "content": text, "updated_at": "2026-11-02T00:00:00Z"}],
        )
        self.assertEqual(staged["source_counts"], {"manual": 1, "automatic": 1})
        self.assertEqual([row["memory_source"] for row in staged["items"]], ["manual", "automatic"])
        self.assertEqual(
            staged["items"][1]["content"], text.split("<!-- MEMORY_FIELDS")[0].rstrip()
        )
        self.assertEqual(staged["data_use"], "记忆将写入此连接的外部服务并重建索引。")
        self.assertEqual((self.manual.puts, self.native.puts), (0, []))
        result = self.apply(staged)
        self.assertEqual(set(result["applied"]), {"manual-answer", self.first})
        self.assertEqual(self.native.values, {self.first: text})
        with app.closing(app.connect()) as db:
            rows = db.execute(
                "SELECT id FROM records WHERE namespace=?", (self.namespace,)
            ).fetchall()
        self.assertEqual([row["id"] for row in rows], ["manual-answer"])

    def test_preview_hides_only_trailing_reserved_comments_and_keeps_raw_staging(self):
        body = "# 原文\n\n<!--普通注释-->\n<!-- MEMORY_FIELDS example -->\n这仍是正文。"
        text = (
            body
            + '\n\n<!-- MEMORY_FIELDS\n{"version":8,"content":"原始字段"}\n-->\n\n<!-- MEMORY_FIELDS\n{"source":"peer"}\n-->\n'
        )
        self.assertEqual(memory_archive.preview_content(text), body)
        self.assertEqual(memory_archive.preview_content(body), body)
        staged = self.preview(automatic=[{"uri": self.first, "content": text}])
        self.assertEqual(staged["items"][0]["content"], body)
        self.apply(staged)
        self.assertEqual(self.native.values[self.first], text)
        self.assertEqual(self.execute("export")["automatic_entries"][0]["content"], text)

    def test_empty_complete_archive_can_preview_without_creating_native_content(self):
        staged = self.preview()
        self.assertEqual(staged["items"], [])
        self.assertEqual(staged["source_counts"], {"manual": 0, "automatic": 0})
        self.assertEqual(self.native.values, {})
        self.assertEqual(self.apply(staged)["applied"], [])

    def test_native_skip_replace_and_identical_text(self):
        self.native.values[self.first] = "旧偏好"
        staged = self.preview(automatic=[{"uri": self.first, "content": "新偏好"}])
        self.assertTrue(staged["items"][0]["conflict"])
        self.assertEqual(self.apply(staged)["skipped"], [self.first])
        self.assertEqual(self.native.values[self.first], "旧偏好")
        staged = self.preview(automatic=[{"uri": self.first, "content": "新偏好"}])
        self.assertEqual(self.apply(staged, mode="replace")["applied"], [self.first])
        self.assertEqual(self.native.values[self.first], "新偏好")
        before = len(self.native.puts)
        staged = self.preview(automatic=[{"uri": self.first, "content": "新偏好"}])
        self.assertEqual(self.apply(staged, mode="replace")["skipped"], [self.first])
        self.assertEqual(len(self.native.puts), before)

    def test_native_concurrent_edit_rejects_entire_batch_before_manual_write(self):
        self.native.values[self.first] = "预览时内容"
        staged = self.preview([self.entry()], [{"uri": self.first, "content": "待导入内容"}])
        self.native.values[self.first] = "并发修改内容"
        with self.assertRaises(app.HTTPException) as caught:
            self.apply(staged, mode="replace")
        self.assertEqual(caught.exception.status_code, 409)
        self.assertEqual(self.manual.puts, 0)
        self.assertEqual(self.native.values[self.first], "并发修改内容")

    def test_unfinished_manual_operation_blocks_preview_and_later_apply(self):
        self.apply(self.preview([self.entry()]))
        with app.closing(app.connect()) as db:
            db.execute(
                "UPDATE records SET status='INDEXING',operation_id='still-pending' WHERE namespace=?",
                (self.namespace,),
            )
            db.commit()
        with self.assertRaises(app.HTTPException) as caught:
            self.preview([self.entry(content="后来的导入")])
        self.assertEqual(caught.exception.status_code, 409)
        with app.closing(app.connect()) as db:
            db.execute("UPDATE records SET status='READY' WHERE namespace=?", (self.namespace,))
            db.commit()
        staged = self.preview([self.entry(content="后来的导入")])
        with app.closing(app.connect()) as db:
            db.execute("UPDATE records SET status='INDEXING' WHERE namespace=?", (self.namespace,))
            db.commit()
        before = self.manual.puts
        with self.assertRaises(app.HTTPException) as caught:
            self.apply(staged, mode="replace")
        self.assertEqual(caught.exception.status_code, 409)
        self.assertEqual(self.manual.puts, before)
        self.assertEqual(self.native.puts, [])

    def test_failed_replay_never_overwrites_modified_completed_file(self):
        staged = self.preview(
            automatic=[
                {"uri": self.first, "content": "导入一"},
                {"uri": self.second, "content": "导入二"},
            ]
        )
        self.native.fail_before.add(self.second)
        operation = str(uuid.uuid4())
        with self.assertRaises(RuntimeError):
            self.apply(staged, operation)
        self.native.values[self.first] = "导入后的并发修改"
        with self.assertRaises(app.HTTPException) as caught:
            self.apply(staged, operation)
        self.assertEqual(caught.exception.status_code, 409)
        self.assertEqual(self.native.values[self.first], "导入后的并发修改")
        self.assertNotIn(self.second, self.native.values)
        self.assertEqual(self.native.puts.count(self.first), 1)

    def test_raw_write_without_index_confirmation_is_retried_after_preview_expiry(self):
        staged = self.preview(automatic=[{"uri": self.first, "content": "完整内容"}])
        self.native.fail_after.add(self.first)
        operation = str(uuid.uuid4())
        with self.assertRaises(RuntimeError):
            self.apply(staged, operation)
        self.assertEqual(self.native.values, {self.first: "完整内容"})
        self.assertEqual(self.native.indexed, set())
        with app.closing(app.connect()) as db:
            db.execute("UPDATE previews SET expires=0 WHERE namespace=?", (self.namespace,))
            db.commit()
        self.native.fail_after.add(self.first)
        with self.assertRaises(RuntimeError):
            self.apply(staged, operation)
        with app.closing(app.connect()) as db:
            status = db.execute(
                "SELECT status FROM operations WHERE namespace=? AND id=?",
                (self.namespace, operation),
            ).fetchone()["status"]
        self.assertEqual(status, "PENDING")
        self.assertEqual(self.native.indexed, set())
        result = self.apply(staged, operation)
        self.assertEqual(result["applied"], [self.first])
        self.assertEqual(self.native.values, {self.first: "完整内容"})
        self.assertEqual(self.native.indexed, {self.first})
        self.assertEqual(self.native.puts, [self.first] * 3)
        self.assertEqual(self.apply(staged, operation), result)
        self.assertEqual(self.native.puts, [self.first] * 3)

    def test_skipped_marker_is_replayable_after_later_write_failure(self):
        self.native.values[self.first] = "保留现有内容"
        staged = self.preview(
            automatic=[
                {"uri": self.first, "content": "跳过此内容"},
                {"uri": self.second, "content": "新增"},
            ]
        )
        operation = str(uuid.uuid4())
        self.native.fail_before.add(self.second)
        with self.assertRaises(RuntimeError):
            self.apply(staged, operation)
        result = self.apply(staged, operation)
        self.assertEqual(result["skipped"], [self.first])
        self.assertEqual(result["applied"], [self.second])
        self.assertEqual(self.native.values[self.first], "保留现有内容")

    def test_invalid_packages_are_rejected_before_any_external_write(self):
        valid = self.archive([self.entry()], [{"uri": self.first, "content": "正文"}])
        invalid = []
        corrupted = copy.deepcopy(valid)
        corrupted["automatic_entries"][0]["content"] = "篡改"
        invalid.append(corrupted)
        unknown = copy.deepcopy(valid)
        unknown["unknown"] = True
        invalid.append(unknown)
        invalid.extend(
            [
                self.archive([self.entry(), self.entry()]),
                self.archive(
                    automatic=[
                        {"uri": self.first, "content": "a"},
                        {"uri": self.first, "content": "b"},
                    ]
                ),
                self.archive(
                    automatic=[{"uri": "viking://user/other/memories/private.md", "content": "x"}]
                ),
                self.archive(
                    automatic=[
                        {"uri": "viking://user/owner/memories/%2e%2e/key.md", "content": "x"}
                    ]
                ),
                self.archive(
                    automatic=[{"uri": self.first, "content": "x", "updated_at": "invalid"}]
                ),
                self.archive([self.entry(version=True)]),
                self.archive([self.entry(source=False)]),
                self.archive([self.entry(title=None)]),
                self.archive(automatic=[{"uri": self.first, "content": None}]),
            ]
        )
        for package in invalid:
            with self.subTest(package=package), self.assertRaises(app.HTTPException) as caught:
                self.execute("preview", package=package)
            self.assertEqual(caught.exception.status_code, 422)
        self.assertEqual((self.manual.puts, self.native.puts), (0, []))

    def test_archive_limits_are_bytes_and_include_envelope_metadata(self):
        oversized = self.archive(
            automatic=[{"uri": self.first, "content": "中" * (memory_archive.MAX_BYTES // 3)}]
        )
        with self.assertRaises(app.HTTPException) as caught:
            self.execute("preview", package=oversized)
        self.assertEqual(caught.exception.status_code, 413)
        self.native.values[self.first] = "中" * (memory_archive.MAX_BYTES // 3)
        with self.assertRaises(app.HTTPException) as caught:
            self.execute("export")
        self.assertEqual(caught.exception.status_code, 413)
        many = self.archive(
            automatic=[
                {"uri": f"viking://user/owner/memories/events/{i}.md", "content": "x"}
                for i in range(10001)
            ]
        )
        with self.assertRaises(app.HTTPException) as caught:
            self.execute("preview", package=many)
        self.assertEqual(caught.exception.status_code, 413)


if __name__ == "__main__":
    unittest.main()
