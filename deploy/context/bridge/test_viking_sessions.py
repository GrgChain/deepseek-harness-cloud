import json
import unittest
from types import SimpleNamespace

import httpx
from fastapi import HTTPException
from viking_sessions import SessionRequest, relay, validate


class NativeSessions(unittest.TestCase):
    def test_preserves_duplicate_session_code_without_upstream_diagnostics(self):
        def reply(request):
            return httpx.Response(
                409,
                json={
                    "status": "error",
                    "error": {
                        "code": "ALREADY_EXISTS",
                        "message": "private upstream diagnostic",
                        "details": {"credential": "secret"},
                    },
                },
            )

        with httpx.Client(
            base_url="https://viking.example", transport=httpx.MockTransport(reply)
        ) as client:
            target = SimpleNamespace(http=client, verify=lambda: None)
            response = relay(
                target,
                SessionRequest(
                    method="POST", path="/api/v1/sessions", body='{"session_id":"dsh-existing"}'
                ),
            )
            self.assertEqual(response["status"], 409)
            self.assertEqual(
                json.loads(response["body"])["error"],
                {"code": "ALREADY_EXISTS", "message": "OpenViking request failed"},
            )
            self.assertNotIn("private", response["body"])
            self.assertNotIn("secret", response["body"])
            other = relay(
                target,
                SessionRequest(
                    method="POST", path="/api/v1/search/search", body='{"query":"test"}'
                ),
            )
            self.assertNotIn("code", json.loads(other["body"])["error"])

    def test_rejects_admin_mutations_arbitrary_urls_and_identity_headers(self):
        for method, path, body, headers in [
            ("POST", "/api/v1/admin/accounts", {}, {}),
            ("DELETE", "/api/v1/fs?uri=viking://~/memories", None, {}),
            ("GET", "https://elsewhere/health", None, {}),
            ("GET", "//elsewhere/health", None, {}),
            ("GET", "/api/v1/../health", None, {}),
            ("GET", "/health", None, {"Authorization": "Bearer injected"}),
            ("POST", "/mcp", {"method": "tools/call", "params": {"name": "forget"}}, {}),
        ]:
            with self.subTest(path=path), self.assertRaises(HTTPException):
                validate(
                    SessionRequest(
                        method=method,
                        path=path,
                        body=json.dumps(body) if body else "",
                        headers=headers,
                    )
                )

    def test_allows_native_session_capture_and_filters_mcp_tools(self):
        for method, path, body in [
            ("POST", "/api/v1/sessions", {"session_id": "dsh-session-1"}),
            (
                "POST",
                "/api/v1/sessions/dsh-session-1/messages",
                {"role": "user", "content": "完整对话"},
            ),
            ("POST", "/api/v1/sessions/dsh-session-1/commit", {}),
            ("POST", "/api/v1/search/search", {"query": "跨会话记忆"}),
        ]:
            validate(SessionRequest(method=method, path=path, body=json.dumps(body)))
        seen = []

        def reply(request):
            seen.append(request.headers)
            return httpx.Response(
                200,
                json={
                    "jsonrpc": "2.0",
                    "id": 1,
                    "result": {
                        "tools": [
                            {"name": "search"},
                            {"name": "find"},
                            {"name": "remember"},
                            {"name": "forget"},
                            {"name": "write"},
                        ]
                    },
                },
            )

        with httpx.Client(
            base_url="https://viking.example",
            headers={"X-API-Key": "tenant-key", "X-OpenViking-Account": "isolated"},
            transport=httpx.MockTransport(reply),
        ) as client:
            target = SimpleNamespace(http=client, verify=lambda: None)
            response = relay(
                target,
                SessionRequest(method="POST", path="/mcp", body='{"method":"tools/list","id":1}'),
            )
        self.assertEqual(
            [tool["name"] for tool in json.loads(response["body"])["result"]["tools"]],
            ["search", "find", "remember"],
        )
        self.assertEqual(seen[0]["X-API-Key"], "tenant-key")
        self.assertNotIn("tenant-key", json.dumps(response))

    def test_find_preserves_native_arguments_and_tenant_identity(self):
        payload = {
            "jsonrpc": "2.0",
            "id": 7,
            "method": "tools/call",
            "params": {
                "name": "find",
                "arguments": {
                    "query": "发布检查",
                    "target_uri": "viking://~/memories/experiences",
                    "limit": 5,
                    "min_score": 0.35,
                    "context_type": ["memory"],
                },
            },
        }
        seen = []

        def reply(request):
            seen.append(request)
            self.assertEqual(json.loads(request.content), payload)
            return httpx.Response(
                200,
                json={
                    "jsonrpc": "2.0",
                    "id": 7,
                    "result": {"content": [{"type": "text", "text": "检索结果"}]},
                },
            )

        headers = {
            "X-API-Key": "tenant-key",
            "X-OpenViking-Account": "isolated",
            "X-OpenViking-User": "owner",
        }
        with httpx.Client(
            base_url="https://viking.example", headers=headers, transport=httpx.MockTransport(reply)
        ) as client:
            target = SimpleNamespace(http=client, verify=lambda: None)
            response = relay(
                target, SessionRequest(method="POST", path="/mcp", body=json.dumps(payload))
            )
        self.assertEqual(len(seen), 1)
        self.assertEqual(seen[0].url.path, "/mcp")
        for name, value in headers.items():
            self.assertEqual(seen[0].headers[name], value)
        self.assertEqual(json.loads(response["body"])["result"]["content"][0]["text"], "检索结果")
        self.assertNotIn("tenant-key", json.dumps(response))

    def test_find_is_discoverable_in_streamed_tool_lists(self):
        payload = {
            "jsonrpc": "2.0",
            "id": 1,
            "result": {"tools": [{"name": "find"}, {"name": "add_skill"}]},
        }

        def reply(request):
            return httpx.Response(
                200,
                headers={"content-type": "text/event-stream"},
                text="event: message\ndata: " + json.dumps(payload) + "\n\n",
            )

        with httpx.Client(
            base_url="https://viking.example", transport=httpx.MockTransport(reply)
        ) as client:
            target = SimpleNamespace(http=client, verify=lambda: None)
            response = relay(
                target,
                SessionRequest(method="POST", path="/mcp", body='{"method":"tools/list","id":1}'),
            )
        data = next(
            line[5:].strip() for line in response["body"].splitlines() if line.startswith("data:")
        )
        self.assertEqual(json.loads(data)["result"]["tools"], [{"name": "find"}])


if __name__ == "__main__":
    unittest.main()
