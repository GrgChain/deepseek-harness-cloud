"""Semantic search across isolated manual and official-plugin memory tenants."""
import copy
import hashlib
import os
import tempfile
import unittest
import uuid
from unittest.mock import patch

os.environ.setdefault("CONTEXT_DB", tempfile.mktemp(suffix=".db"))
import app


class ManualSearch:
    def __init__(self):
        self.results = []
        self.calls = []
        self.failure = None
        self.closed = 0

    def search(self, query, scope, limit):
        self.calls.append((query, scope, limit))
        if self.failure:
            raise self.failure
        return copy.deepcopy(self.results)

    def close(self):
        self.closed += 1


class NativeSearch:
    def __init__(self):
        self.results = []
        self.calls = []
        self.failure = None
        self.has_tenant = True
        self.closed = 0

    def session_search(self, query, limit):
        self.calls.append((query, limit))
        if self.failure:
            raise self.failure
        return copy.deepcopy(self.results) if self.has_tenant else []

    def close(self):
        self.closed += 1


class ManualMemorySearch(unittest.TestCase):
    def test_mem0_and_graphiti_apply_minimum_score_before_the_requested_limit(self):
        for backend in ("mem0", "graphiti"):
            with self.subTest(backend=backend):
                namespace = hashlib.sha256(uuid.uuid4().bytes).hexdigest()
                target = ManualSearch()
                with app.closing(app.connect()) as db:
                    for index, score in enumerate((1.0, 0.9, 0.75, 0.0, -1.0)):
                        identity = "manual-" + str(index)
                        payload = {
                            "id": identity,
                            "title": "公司名称",
                            "content": "公司叫示例公司",
                            "type": "note",
                            "source": "本人确认",
                            "scope": "personal",
                            "version": 1,
                            "created_at": app.now(),
                            "updated_at": app.now(),
                        }
                        db.execute(
                            "INSERT INTO records VALUES(?,?,?,?,?,'READY',?,?,?)",
                            (
                                backend,
                                namespace,
                                identity,
                                "personal",
                                1,
                                app.canonical(payload).decode(),
                                app.canonical([payload["content"]]).decode(),
                                "seed",
                            ),
                        )
                        target.results.append(
                            {"record_id": identity, "version": 1, "chunk": 0, "score": score}
                        )
                    db.commit()
                with patch.object(app, "engine", return_value=target) as engine:
                    for minimum, limit, expected in (
                        (None, 20, [1.0, 0.9, 0.75, 0.0, -1.0]),
                        (-1, 20, [1.0, 0.9, 0.75, 0.0, -1.0]),
                        (0, 20, [1.0, 0.9, 0.75, 0.0]),
                        (0.75, 2, [1.0, 0.9]),
                        (1, 20, [1.0]),
                    ):
                        with self.subTest(minimum=minimum, limit=limit):
                            inp = app.Input(
                                contract=app.CONTRACT,
                                namespace=namespace,
                                kind="memory",
                                query="公司叫什么",
                                limit=limit,
                                min_similarity=minimum,
                            )
                            result = app.execute(backend, "search", inp)
                            self.assertEqual([hit["score"] for hit in result["items"]], expected)
                            self.assertEqual(
                                target.calls[-1], ("公司叫什么", "personal", min(100, limit * 5))
                            )
                    self.assertEqual(engine.call_count, 5)
                    self.assertEqual(target.closed, 5)

    def test_manual_backends_keep_nonsearch_threshold_and_source_restrictions(self):
        for backend in ("mem0", "graphiti"):
            namespace = hashlib.sha256(uuid.uuid4().bytes).hexdigest()
            for action, values in (
                ("list", {"min_similarity": 0}),
                ("list", {"memory_source": "manual"}),
                ("list", {"memory_source": "automatic"}),
                ("search", {"search_mode": "keyword", "min_similarity": 0}),
                ("search", {"search_mode": "exact", "min_similarity": 0}),
            ):
                with self.subTest(backend=backend, action=action, values=values), patch.object(
                    app, "engine"
                ) as engine:
                    inp = app.Input(
                        contract=app.CONTRACT,
                        namespace=namespace,
                        kind="memory",
                        query="公司叫什么",
                        **values,
                    )
                    with self.assertRaises(app.HTTPException) as caught:
                        app.execute(backend, action, inp)
                    self.assertEqual(caught.exception.status_code, 422)
                    engine.assert_not_called()


class MemorySearch(unittest.TestCase):
    native_uri = "viking://user/owner/memories/preferences/language.md"

    def setUp(self):
        self.namespace = hashlib.sha256(uuid.uuid4().bytes).hexdigest()
        self.native_namespace = hashlib.sha256(uuid.uuid4().bytes).hexdigest()
        self.manual = ManualSearch()
        self.native = NativeSearch()
        self.engine_calls = []

        def engine(backend, namespace, service=None, embedding_config=None):
            self.assertEqual(backend, "openviking")
            self.engine_calls.append(namespace)
            if namespace == self.namespace:
                return self.manual
            self.assertEqual(namespace, self.native_namespace)
            return self.native

        patcher = patch.object(app, "engine", side_effect=engine)
        patcher.start()
        self.addCleanup(patcher.stop)

    def seed(self, identity, score, namespace=None, scope="personal", title="回答偏好", version=1):
        payload = {
            "id": identity,
            "title": title,
            "content": "请使用中文回答。",
            "type": "note",
            "source": "本人确认",
            "scope": scope,
            "version": version,
            "created_at": app.now(),
            "updated_at": app.now(),
        }
        with app.closing(app.connect()) as db:
            db.execute(
                "INSERT INTO records VALUES(?,?,?,?,?,'READY',?,?,?)",
                (
                    "openviking",
                    namespace or self.namespace,
                    identity,
                    scope,
                    version,
                    app.canonical(payload).decode(),
                    app.canonical([payload["content"]]).decode(),
                    "seed",
                ),
            )
            db.commit()
        return {"record_id": identity, "version": version, "chunk": 0, "score": score}

    def native_hit(self, score=0.9, **values):
        return {
            "id": self.native_uri,
            "title": "回答偏好",
            "snippet": "请使用中文回答。",
            "score": score,
            "category": "preferences",
            "source": "DSH 会话",
            **values,
        }

    def search(self, complete=True, **values):
        inp = app.Input(
            contract=app.CONTRACT,
            namespace=self.namespace,
            session_namespace=self.native_namespace if complete else None,
            kind="memory",
            query="使用什么语言回答",
            **values,
        )
        return app.execute("openviking", "search", inp)

    def test_global_score_order_and_limit_include_both_sources(self):
        self.manual.results = [
            self.seed("manual-medium", 0.6),
            self.seed("manual-best", 0.9),
            self.seed("manual-low", 0.3),
        ]
        self.native.results = [
            self.native_hit(0.95),
            self.native_hit(0.8, id=self.native_uri.replace("language", "tone")),
        ]
        result = self.search(limit=3)
        self.assertEqual([hit["score"] for hit in result["items"]], [0.95, 0.9, 0.8])
        self.assertEqual(
            [hit["memory_source"] for hit in result["items"]], ["automatic", "manual", "automatic"]
        )
        self.assertEqual(self.manual.calls, [("使用什么语言回答", "personal", 15)])
        self.assertEqual(self.native.calls, [("使用什么语言回答", 3)])
        self.assertEqual(self.engine_calls, [self.namespace, self.native_namespace])
        self.assertEqual((self.manual.closed, self.native.closed), (1, 1))

    def test_identity_is_deduplicated_within_each_source(self):
        manual = [
            {"id": "same-id", "title": "同名", "snippet": "同文", "score": 0.7},
            {"id": "same-id", "title": "同名", "snippet": "同文", "score": 0.9},
        ]
        automatic = [
            {"id": "same-id", "title": "同名", "snippet": "同文", "score": 0.8},
            {"id": "different-id", "title": "同名", "snippet": "同文", "score": 0.6},
        ]
        result = app.merge_memory_hits(manual, automatic, 20)
        self.assertEqual(
            [(hit["memory_source"], hit["id"], hit["score"]) for hit in result],
            [
                ("manual", "same-id", 0.9),
                ("automatic", "same-id", 0.8),
                ("automatic", "different-id", 0.6),
            ],
        )

    def test_equal_titles_and_content_from_both_sources_remain_visible(self):
        self.manual.results = [self.seed("manual-language", 0.8)]
        self.native.results = [self.native_hit(0.9)]
        result = self.search()
        self.assertEqual(len(result["items"]), 2)
        self.assertEqual({hit["memory_source"] for hit in result["items"]}, {"manual", "automatic"})
        self.assertEqual({hit["title"] for hit in result["items"]}, {"回答偏好"})
        self.assertEqual({hit["snippet"] for hit in result["items"]}, {"请使用中文回答。"})

    def test_source_filter_queries_only_the_selected_corpus(self):
        self.manual.results = [self.seed("manual-language", 0.8)]
        self.native.results = [self.native_hit(0.9)]
        self.manual.failure = RuntimeError("unselected manual source is unavailable")
        result = self.search(memory_source="automatic", limit=10)
        self.assertEqual(
            [(hit["id"], hit["memory_source"]) for hit in result["items"]],
            [(self.native_uri, "automatic")],
        )
        self.assertEqual(self.manual.calls, [])
        self.assertEqual(self.native.calls, [("使用什么语言回答", 10)])
        self.manual.failure = None
        self.native.failure = RuntimeError("unselected automatic source is unavailable")
        result = self.search(memory_source="manual", limit=20)
        self.assertEqual(
            [(hit["id"], hit["memory_source"]) for hit in result["items"]],
            [("manual-language", "manual")],
        )
        self.assertEqual(self.manual.calls, [("使用什么语言回答", "personal", 100)])
        self.assertEqual(len(self.native.calls), 1)

    def test_minimum_similarity_filters_both_sources_before_the_shared_limit(self):
        self.manual.results = [self.seed("manual-equal", 0.75), self.seed("manual-low", 0.74)]
        self.native.results = [
            self.native_hit(0.9),
            self.native_hit(0.7, id=self.native_uri.replace("language", "tone")),
        ]
        result = self.search(min_similarity=0.75, limit=2)
        self.assertEqual(
            [(hit["memory_source"], hit["score"]) for hit in result["items"]],
            [("automatic", 0.9), ("manual", 0.75)],
        )
        self.assertEqual(self.search(min_similarity=1)["status"], "NO_HITS")

    def test_zero_and_negative_minimum_similarity_are_respected(self):
        self.manual.results = [self.seed("manual-negative", -0.2)]
        self.native.results = [self.native_hit(0)]
        self.assertEqual([hit["score"] for hit in self.search(min_similarity=0)["items"]], [0])
        self.assertEqual(
            [hit["score"] for hit in self.search(min_similarity=-1)["items"]], [0, -0.2]
        )

    def test_source_filters_are_rejected_for_unrelated_operations_and_backends(self):
        for backend, action, source in (
            ("openviking", "list", "manual"),
            ("milvus", "search", "automatic"),
        ):
            with self.subTest(backend=backend, action=action):
                inp = app.Input(
                    contract=app.CONTRACT,
                    namespace=self.namespace,
                    kind="memory" if backend in app.MEMORY else "knowledge",
                    query="偏好",
                    memory_source=source,
                )
                with self.assertRaises(app.HTTPException) as caught:
                    app.execute(backend, action, inp)
                self.assertEqual(caught.exception.status_code, 422)
        with self.assertRaises(app.HTTPException):
            self.search(complete=False, memory_source="automatic")
        self.assertEqual(self.engine_calls, [])

    def test_new_search_defaults_do_not_change_existing_mutation_receipts(self):
        inp = app.Input(contract=app.CONTRACT, namespace=self.namespace, kind="memory")
        self.assertNotIn("memory_source", app.operation_input(inp))

    def test_either_source_failure_prevents_a_partial_success(self):
        self.manual.results = [self.seed("manual-language", 0.8)]
        self.native.results = [self.native_hit()]
        self.manual.failure = RuntimeError("manual unavailable")
        with self.assertRaisesRegex(RuntimeError, "manual unavailable"):
            self.search()
        self.assertEqual(self.native.calls, [])
        self.manual.failure = None
        self.native.failure = RuntimeError("automatic unavailable")
        with self.assertRaisesRegex(RuntimeError, "automatic unavailable"):
            self.search()
        self.assertEqual((self.manual.closed, self.native.closed), (2, 2))

    def test_missing_native_tenant_returns_manual_results_without_creation(self):
        self.manual.results = [self.seed("manual-language", 0.8)]
        self.native.has_tenant = False
        self.native.results = [self.native_hit()]
        result = self.search()
        self.assertEqual(
            [(hit["id"], hit["memory_source"]) for hit in result["items"]],
            [("manual-language", "manual")],
        )
        self.assertFalse(self.native.has_tenant)
        self.assertEqual(self.native.calls, [("使用什么语言回答", 5)])

    def test_manual_records_are_limited_to_the_trusted_namespace_and_scope(self):
        other_namespace = hashlib.sha256(uuid.uuid4().bytes).hexdigest()
        other_scope = "workspace:" + str(uuid.uuid4())
        self.manual.results = [
            self.seed("own", 0.7),
            self.seed("other-owner", 0.99, namespace=other_namespace),
            self.seed("other-workspace", 0.98, scope=other_scope),
            {"record_id": "missing", "version": 1, "chunk": 0, "score": 1.0},
        ]
        self.native.results = [self.native_hit(0.8)]
        result = self.search()
        self.assertEqual({hit["id"] for hit in result["items"]}, {"own", self.native_uri})
        self.assertEqual(self.engine_calls, [self.namespace, self.native_namespace])

    def test_manual_version_filter_remains_in_effect(self):
        current = self.seed("manual-language", 0.8, version=2)
        self.manual.results = [{**current, "version": 1, "score": 0.99}, current]
        result = self.search()
        self.assertEqual(
            [(hit["id"], hit["version"]) for hit in result["items"]], [("manual-language", 2)]
        )

    def test_legacy_search_without_native_namespace_keeps_manual_response(self):
        self.manual.results = [self.seed("manual-language", 0.8)]
        self.native.results = [self.native_hit()]
        result = self.search(complete=False)
        self.assertEqual([hit["id"] for hit in result["items"]], ["manual-language"])
        self.assertNotIn("memory_source", result["items"][0])
        self.assertEqual(self.native.calls, [])
        self.assertEqual(self.engine_calls, [self.namespace])

    def test_empty_sources_return_no_hits(self):
        result = self.search()
        self.assertEqual(result["items"], [])
        self.assertEqual(result["status"], "NO_HITS")

    def test_malformed_native_scores_fail_the_complete_search(self):
        self.manual.results = [self.seed("manual-language", 0.8)]
        for invalid in (True, None, float("nan"), float("inf")):
            with self.subTest(score=invalid):
                self.native.results = [self.native_hit(invalid)]
                with self.assertRaisesRegex(RuntimeError, "Invalid memory search score"):
                    self.search()

    def test_native_namespace_is_rejected_for_other_actions_and_scopes(self):
        for action, scope in (("get", "personal"), ("search", "workspace:" + str(uuid.uuid4()))):
            with self.subTest(action=action, scope=scope):
                inp = app.Input(
                    contract=app.CONTRACT,
                    namespace=self.namespace,
                    session_namespace=self.native_namespace,
                    kind="memory",
                    query="偏好",
                    scope=scope,
                )
                with self.assertRaises(app.HTTPException) as caught:
                    app.execute("openviking", action, inp)
                self.assertEqual(caught.exception.status_code, 422)
        self.assertEqual(self.engine_calls, [])


if __name__ == "__main__":
    unittest.main()
