import hashlib
import json
import os
import tempfile
import unittest
import uuid
from unittest.mock import patch
from urllib.parse import urlencode

import httpx
from fastapi import HTTPException

os.environ.setdefault("CONTEXT_DB", tempfile.mktemp(suffix=".db"))
import app
from viking_memory import VikingMemory
from viking_sessions import SessionRequest
from viking_session_memory import ManualMemories, relay_shared


class Target:
    root = VikingMemory.root
    uri = VikingMemory.uri

    def __init__(self, handler):
        self.http = httpx.Client(base_url="https://viking.example", transport=httpx.MockTransport(handler))
        self.has_tenant = True
        self.hits = []
        self.searches = []

    def verify(self):
        pass

    def close(self):
        self.http.close()

    def search(self, query, scope, limit):
        self.searches.append((query, scope, limit))
        return self.hits


class SharedNativeReads(unittest.TestCase):
    def setUp(self):
        self.seen = []

        def native(request):
            self.seen.append(request)
            if request.url.path == "/mcp":
                body = json.loads(request.content)
                payload = {"jsonrpc": "2.0", "id": body["id"], "result": {
                    "content": [{"type": "text", "text": "native memory"}]}}
                return httpx.Response(200, headers={"content-type": "text/event-stream"},
                                      text="event: message\ndata: " + json.dumps(payload) + "\n\n")
            return httpx.Response(200, json={"status": "ok", "result": {
                "memories": [{"uri": "viking://user/owner/memories/profile.md", "score": 0.6}],
                "resources": [], "skills": [], "total": 1}})

        self.native = Target(native)
        self.manual = Target(lambda request: httpx.Response(200, json={"status": "ok", "result": "周三发布"}))
        self.addCleanup(self.native.close)
        self.addCleanup(self.manual.close)
        self.record = {"id": "release", "scope": "personal", "version": 2,
                       "title": "项目发布约定", "content": "周三发布"}
        self.uri = self.manual.uri(self.record)
        self.manual.hits = [{"record_id": "release", "version": 2, "score": 0.9},
                            {"record_id": "release", "version": 1, "score": 1},
                            {"record_id": "deleted", "version": 1, "score": 1},
                            {"record_id": "foreign", "version": 1, "score": 1}]
        self.memories = ManualMemories(self.manual, [self.record])

    def call(self, path, body=None, method="POST"):
        return relay_shared(self.native, SessionRequest(method=method, path=path,
                            body=json.dumps(body) if body else ""), self.memories)

    def test_auto_recall_merges_ranked_current_memories_and_read_uses_manual_tenant(self):
        body = {"query": "发布约定", "target_uri": "viking://~/memories", "limit": 2,
                "session_id": "dsh-test", "score_threshold": 0}
        result = json.loads(self.call("/api/v1/search/search", body)["body"])["result"]
        self.assertEqual([hit["uri"] for hit in result["memories"]],
                         [self.uri, "viking://user/owner/memories/profile.md"])
        self.assertEqual(result["total"], 2)
        self.assertEqual(json.loads(self.seen[0].content), body)
        self.assertEqual(self.manual.searches, [("发布约定", "personal", 10)])
        response = self.call("/api/v1/content/read?" + urlencode({"uri": self.uri}), method="GET")
        self.assertEqual(json.loads(response["body"])["result"], "周三发布")
        self.assertEqual(len(self.seen), 1)
        old = self.uri.replace("_v2.md", "_v1.md")
        self.assertEqual(self.call("/api/v1/content/read?" + urlencode({"uri": old}), method="GET")["status"], 404)

    def test_narrower_uri_type_score_and_level_filters_are_respected(self):
        for args in ({"target_uri": "viking://user/someone-else"},
                     {"target_uri": "viking://~/memories/experiences"},
                     {"context_type": "resource"}, {"level": [0, 1]},
                     {"score_threshold": 0.95}, {"exclude_uris": [self.uri]}):
            with self.subTest(args=args):
                self.assertEqual(self.memories.search({"query": "发布", **args}), [])
        self.assertEqual(len(self.memories.search({"query": "发布", "target_uri": self.uri})), 1)
        self.manual.has_tenant = False
        self.assertEqual(self.memories.search({"query": "发布"}), [])

    def test_official_context_face_includes_manual_content_within_budget(self):
        def reply(request):
            return httpx.Response(200, json={"status": "ok", "result": {
                "rendered": "", "entries": [], "digest": "", "stats": {
                    "max_tokens": 1600, "used_tokens": 0, "rewrite": "no_relevant"}}})
        with Target(reply).http as client:
            target = type("Native", (), {"http": client, "verify": lambda _: None})()
            result = json.loads(relay_shared(target, SessionRequest(method="POST",
                path="/api/v1/search/search", body='{"query":"发布","mode":"context","purpose":"coding","score_threshold":0.35}'), self.memories)["body"])["result"]
        self.assertIn("周三发布", result["rendered"])
        self.assertEqual(result["entries"][0]["uri"], self.uri)
        self.assertNotEqual(result["stats"]["rewrite"], "no_relevant")
        self.record["content"] = "发布约定" * 1000
        small = {"rendered": "native", "entries": [], "digest": "summary",
                 "stats": {"used_tokens": 10}}
        self.memories.assemble(small, self.memories.search({"query": "发布"}), {"max_tokens": 250})
        self.assertLessEqual(small["stats"]["used_tokens"], 250)
        self.assertTrue(small["digest"].startswith("summary"))
        self.assertEqual(small["entries"][0]["detail"], "overview")

    def test_streamed_mcp_find_and_mixed_read_keep_official_tool_contract(self):
        body = {"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": {
            "name": "find", "arguments": {"query": "发布", "min_score": 0.35}}}
        response = self.call("/mcp", body)
        self.assertIn("text/event-stream", response["headers"]["content-type"])
        payload = json.loads(next(line[5:] for line in response["body"].splitlines() if line.startswith("data:")))
        self.assertEqual(payload["id"], 7)
        self.assertEqual(payload["result"]["content"][0]["text"], "native memory")
        self.assertIn(self.uri, payload["result"]["content"][1]["text"])
        body["params"] = {"name": "read", "arguments": {"uris": [self.uri,
                         "viking://user/owner/memories/profile.md"]}}
        payload = json.loads(self.call("/mcp", body)["body"])
        text = payload["result"]["content"][0]["text"]
        self.assertIn("周三发布", text)
        self.assertIn("native memory", text)
        self.assertEqual(len(self.seen), 2)

    def test_mcp_manual_hit_replaces_empty_native_message(self):
        def reply(request):
            return httpx.Response(200, json={"jsonrpc": "2.0", "id": 7, "result": {
                "content": [{"type": "text", "text": "No matching context found."}]}})
        with Target(reply).http as client:
            target = type("Native", (), {"http": client, "verify": lambda _: None})()
            response = relay_shared(target, SessionRequest(method="POST", path="/mcp", body=json.dumps({
                "jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": {
                    "name": "search", "arguments": {"query": "发布", "min_score": None}}})), self.memories)
        self.assertNotIn("No matching context", response["body"])
        self.assertIn(self.uri, response["body"])

    def test_bridge_resolves_active_records_in_the_signed_manual_namespace(self):
        tenant = hashlib.sha256(uuid.uuid4().bytes).hexdigest()
        native = hashlib.sha256(uuid.uuid4().bytes).hexdigest()
        other = hashlib.sha256(uuid.uuid4().bytes).hexdigest()
        with app.closing(app.connect()) as db:
            for namespace, status, identity in ((tenant, "READY", "release"),
                                                 (tenant, "DELETED", "deleted"),
                                                 (other, "READY", "foreign")):
                record = {**self.record, "id": identity, "version": 2 if identity == "release" else 1}
                db.execute("INSERT INTO records VALUES(?,?,?,?,?,?,?,?,?)",
                           ("openviking", namespace, identity, "personal", record["version"], status,
                            app.canonical(record).decode(), '[]', 'seed'))
            db.commit()
        inp = app.Input(contract=app.CONTRACT, namespace=native, manual_namespace=tenant,
                        kind="memory", session_request=SessionRequest(method="POST", path="/api/v1/search/find",
                        body='{"query":"发布"}'))
        with patch.object(app, "engine", side_effect=lambda backend, namespace, *_:
                          self.manual if namespace == tenant else self.native):
            result = json.loads(app.execute("openviking", "session", inp)["body"])["result"]
        self.assertEqual(len(result["memories"]), 2)
        self.assertEqual(result["memories"][0]["uri"], self.uri)
        for backend, action in (("mem0", "session"), ("openviking", "search")):
            with self.subTest(backend=backend, action=action), self.assertRaises(HTTPException):
                app.execute(backend, action, inp)


if __name__ == "__main__":
    unittest.main()
