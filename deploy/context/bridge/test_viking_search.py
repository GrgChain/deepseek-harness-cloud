import json
import sqlite3
import unittest
from types import SimpleNamespace
from unittest.mock import patch

import httpx
from viking_memory import VikingMemory


OWNER = "viking://user/owner"
BODY = "# 回答偏好\n\n请使用简体中文回复，发布之前需要完成测试。"


def ok(result):
    return httpx.Response(200, json={"status": "ok", "result": result})


class VikingSessionSearch(unittest.TestCase):
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

    def test_unused_connection_never_reads_with_admin_key_or_creates_tenant(self):
        target = self.target(
            lambda request: self.fail("Unused connection must not access OpenViking"),
            existing=False,
        )
        self.assertEqual(target.session_search("使用什么语言回复？", 10), [])
        self.assertFalse(target.has_tenant)
        self.assertIsNone(target.keys.execute("SELECT key FROM tenant_keys").fetchone())

    def test_existing_connection_refuses_administrator_credential(self):
        target = self.target(
            lambda request: self.fail("Search must not use an administrator credential")
        )
        target.http.headers["X-API-Key"] = "root-key"
        with self.assertRaisesRegex(RuntimeError, "tenant credential"):
            target.session_search("发布约定", 10)

    def test_one_index_query_covers_deep_owner_and_every_peer_without_listing(self):
        deep = OWNER + "/memories/" + ("nested/" * 12) + "item1001.md"
        peer = OWNER + "/peers/shared-workspace/memories/experiences/release.md"
        root = OWNER + "/memories/profile.md"
        reads = []

        def reply(request):
            self.assertEqual(request.headers["X-API-Key"], "tenant-key")
            self.assertEqual(request.headers["X-OpenViking-Account"], "hc_" + "d" * 64)
            self.assertEqual(request.headers["X-OpenViking-User"], "owner")
            if request.url.path == "/api/v1/search/find":
                self.assertEqual(
                    json.loads(request.content),
                    {
                        "query": "使用什么语言，发布前做什么？",
                        "target_uri": OWNER,
                        "context_type": "memory",
                        "limit": 3,
                        "level": 2,
                    },
                )
                return ok(
                    {
                        "memories": [
                            {"uri": deep, "score": 0.8},
                            {"uri": peer, "score": 0.95},
                            {"uri": root, "score": 0.7},
                        ]
                    }
                )
            self.assertEqual(request.url.path, "/api/v1/content/read")
            self.assertEqual(request.url.params["raw"], "false")
            self.assertEqual(request.url.params["offset"], "0")
            self.assertEqual(request.url.params["limit"], "40")
            reads.append(request.url.params["uri"])
            return ok(BODY + '\n\n<!-- MEMORY_FIELDS\n{"custom":"private"}\n-->')

        records = self.target(reply).session_search("使用什么语言，发布前做什么？", 3)
        self.assertEqual([record["id"] for record in records], [peer, deep, root])
        self.assertCountEqual(reads, [peer, deep, root])
        for record in records:
            self.assertEqual(record["snippet"], BODY)
            self.assertNotIn("content", record)
            self.assertEqual(record["source"], "DSH 会话")
            self.assertEqual(record["scope"], "personal")
            self.assertEqual(record["status"], "READY")
            self.assertEqual(record["version"], 0)
            self.assertEqual(record["memory_source"], "automatic")
        self.assertEqual(records[0]["title"], "release")
        self.assertEqual(records[0]["category"], "experiences")
        self.assertEqual(records[-1]["category"], "")

    def test_filters_unsafe_hits_and_keeps_best_duplicate_score(self):
        valid = OWNER + "/memories/preferences/reply.md"
        excluded = [
            "viking://user/another/memories/private.md",
            "viking://resources/notes.md",
            OWNER + "/resources/notes.md",
            OWNER + "/peers/team/resources/notes.md",
            OWNER + "/memories/.abstract.md",
            OWNER + "/memories/.hidden/secret.md",
            OWNER + "/memories/../secret.md",
            OWNER + "/memories/encoded%2fsecret.md",
            OWNER + "/memories/file.md?query",
            OWNER + "/memories/file.md#fragment",
            OWNER + "/memories/readme.txt",
            None,
        ]
        hits = [None, {"uri": valid, "score": 0.2}, {"uri": valid, "score": 0.9}]
        hits.extend({"uri": uri, "score": 1.0} for uri in excluded)
        hits.extend(
            {"uri": OWNER + "/memories/invalid" + str(index) + ".md", "score": score}
            for index, score in enumerate([None, True, "0.99", 10**400])
        )
        hits.extend(
            [
                {"uri": valid, "score": 1.1, "context_type": "resource"},
                {"uri": valid, "score": 1.2, "level": 1},
            ]
        )

        def reply(request):
            if request.url.path.endswith("/find"):
                return ok({"memories": hits, "resources": [{"uri": valid, "score": 1.3}]})
            self.assertEqual(request.url.params["uri"], valid)
            return ok(BODY)

        records = self.target(reply).session_search("回复语言", 20)
        self.assertEqual(len(records), 1)
        self.assertEqual(records[0]["id"], valid)
        self.assertEqual(records[0]["score"], 0.9)

    def test_rejects_nonfinite_scores(self):
        target = self.target(lambda request: self.fail("Only valid-score hits should be read"))
        with patch.object(
            target,
            "request",
            return_value={
                "memories": [
                    {"uri": OWNER + "/memories/" + str(index) + ".md", "score": score}
                    for index, score in enumerate([float("nan"), float("inf"), float("-inf")])
                ]
            },
        ):
            self.assertEqual(target.session_search("约定", 20), [])

    def test_limit_is_capped_and_excerpt_preserves_body_comments(self):
        calls = []
        body = BODY + "\n\n<!-- MEMORY_FIELDS\n这是正文中的注释示例\n-->\n\n下面仍是可见正文。"

        def reply(request):
            calls.append(request)
            if request.url.path.endswith("/find"):
                self.assertEqual(json.loads(request.content)["limit"], 20)
                return ok(
                    {
                        "memories": [
                            {"uri": OWNER + "/memories/item" + str(index) + ".md", "score": index}
                            for index in range(25)
                        ]
                    }
                )
            return ok(body + '\n\n<!-- MEMORY_FIELDS\n{"private":"reserved trailer"}\n-->')

        records = self.target(reply).session_search("发布步骤", 1000)
        self.assertEqual(len(records), 20)
        self.assertEqual(len(calls), 21)
        self.assertEqual([record["score"] for record in records], list(range(24, 4, -1)))
        self.assertTrue(all(record["snippet"] == body for record in records))

    def test_stale_hits_are_omitted_and_upstream_failures_are_not_silenced(self):
        uri = OWNER + "/memories/deleted.md"

        def reply(request):
            if request.url.path.endswith("/find"):
                return ok({"memories": [{"uri": uri, "score": 0.8}]})
            return httpx.Response(404)

        self.assertEqual(self.target(reply).session_search("偏好", 5), [])
        failed = self.target(lambda request: httpx.Response(503))
        with self.assertRaises(httpx.HTTPStatusError):
            failed.session_search("偏好", 5)

    def test_invalid_search_inputs_and_malformed_upstream_responses_fail(self):
        target = self.target(lambda request: self.fail("Invalid search must not make a request"))
        for query, limit in [
            ("", 5),
            ("  ", 5),
            (None, 5),
            ("偏好", 0),
            ("偏好", True),
            ("偏好", "5"),
        ]:
            with self.subTest(query=query, limit=limit), self.assertRaises(ValueError):
                target.session_search(query, limit)
        for result in [None, [], {}, {"memories": {}}]:
            malformed = self.target(lambda request: ok(result))
            with self.subTest(result=result), self.assertRaises(RuntimeError):
                malformed.session_search("偏好", 5)
        invalid_body = self.target(
            lambda request: ok(
                {"memories": [{"uri": OWNER + "/memories/profile.md", "score": 0.9}]}
                if request.url.path.endswith("/find")
                else {}
            )
        )
        with self.assertRaisesRegex(RuntimeError, "content"):
            invalid_body.session_search("偏好", 5)


if __name__ == "__main__":
    unittest.main()
