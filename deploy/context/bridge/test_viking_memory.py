import sqlite3
import unittest
from types import SimpleNamespace
from unittest.mock import patch

import httpx
from viking_memory import VikingMemory


class CustomViking(unittest.TestCase):
    def test_session_memory_uris_are_confined_to_own_memory_files(self):
        for uri in (
            "viking://user/owner/memories/preferences/language.md",
            "viking://user/owner/memories/profile.md",
            "viking://user/owner/peers/dsh/memories/experiences/build.md",
        ):
            self.assertTrue(VikingMemory.session_uri(uri), uri)
        for uri in (
            "viking://user/other/memories/private.md",
            "viking://resources/file.md",
            "viking://user/owner/privacy/key.md",
            "viking://user/owner/memories",
            "viking://user/owner/memories/../privacy/key.md",
            "viking://user/owner/memories/%2e%2e/secret.md",
            "viking://user/owner/memories/.abstract.md",
            "viking://user/owner/memories/x.md?uri=other",
            "viking://user/owner/peers/dsh/resources/x.md",
        ):
            self.assertFalse(VikingMemory.session_uri(uri), uri)

    def test_unused_connection_does_not_read_with_admin_key_or_create_account(self):
        def reply(request):
            self.fail("unused connection must not make any upstream request")

        with patch(
            "viking_memory.sqlite3.connect", return_value=sqlite3.connect(":memory:")
        ), patch("viking_memory.MilvusTransport", return_value=httpx.MockTransport(reply)):
            target = VikingMemory(
                "b" * 64, SimpleNamespace(url="https://viking.example", token="admin-key")
            )
            try:
                self.assertEqual(target.session_list(), {"items": [], "truncated": False})
                with self.assertRaises(ValueError):
                    target.session_get("viking://user/owner/memories/profile.md")
            finally:
                target.close()

    def test_native_list_and_get_use_plugin_tenant_not_manual_records(self):
        uri = "viking://user/owner/memories/preferences/language.md"
        seen = []

        def reply(request):
            seen.append(request)
            self.assertEqual(request.headers["X-API-Key"], "tenant-key")
            self.assertEqual(request.headers["X-OpenViking-Account"], "hc_" + "c" * 64)
            self.assertEqual(request.method, "GET")
            if request.url.path == "/api/v1/content/read":
                self.assertEqual(request.url.params["uri"], uri)
                return httpx.Response(
                    200, json={"status": "ok", "result": "# 语言偏好\n使用中文回复。"}
                )
            entries = [
                {"uri": uri, "isDir": False, "modTime": "2026-11-02T00:00:00Z"},
                {"uri": "viking://user/owner/memories/.overview.md", "isDir": False},
                {"uri": "viking://user/other/memories/private.md", "isDir": False},
                {"uri": "viking://user/owner/memories/preferences", "isDir": True},
            ]
            return httpx.Response(
                200,
                json={
                    "status": "ok",
                    "result": entries if request.url.params["uri"].endswith("/memories") else [],
                },
            )

        db = sqlite3.connect(":memory:")
        db.execute("CREATE TABLE tenant_keys(namespace TEXT PRIMARY KEY,key TEXT)")
        db.execute("INSERT INTO tenant_keys VALUES(?,?)", ("c" * 64, "tenant-key"))
        db.commit()
        with patch("viking_memory.sqlite3.connect", return_value=db), patch(
            "viking_memory.MilvusTransport", return_value=httpx.MockTransport(reply)
        ):
            target = VikingMemory(
                "c" * 64, SimpleNamespace(url="https://viking.example", token="admin-key")
            )
            try:
                result = target.session_list()
                self.assertEqual(len(result["items"]), 1)
                self.assertEqual(result["items"][0]["id"], uri)
                self.assertEqual(result["items"][0]["category"], "preferences")
                self.assertFalse(result["truncated"])
                self.assertEqual(target.session_get(uri)["content"], "# 语言偏好\n使用中文回复。")
                count = len(seen)
                with self.assertRaises(ValueError):
                    target.session_get("viking://user/other/memories/private.md")
                self.assertEqual(len(seen), count)
            finally:
                target.close()

    def test_uses_custom_address_and_admin_key(self):
        seen = []

        def reply(request):
            seen.append((str(request.url), request.headers.get("X-API-Key")))
            return httpx.Response(200, json={"status": "ok", "result": {"user_key": "tenant-key"}})

        with patch(
            "viking_memory.sqlite3.connect", return_value=sqlite3.connect(":memory:")
        ), patch("viking_memory.MilvusTransport", return_value=httpx.MockTransport(reply)):
            target = VikingMemory(
                "a" * 64, SimpleNamespace(url="https://viking.example", token="admin-key")
            )
            try:
                target.verify()
                self.assertEqual(
                    seen, [("https://viking.example/api/v1/admin/accounts", "admin-key")]
                )
                self.assertEqual(target.http.headers["X-API-Key"], "tenant-key")
            finally:
                target.close()


if __name__ == "__main__":
    unittest.main()
