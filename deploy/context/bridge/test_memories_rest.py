import unittest
from types import SimpleNamespace
from unittest.mock import patch

import httpx
from memories import Memories


class RemoteMemories(unittest.TestCase):
    def test_scoped_write_search_and_delete(self):
        seen = []

        def reply(request):
            seen.append(request)
            if request.method == "POST" and request.url.path == "/memories":
                return httpx.Response(
                    200, json={"results": [{"id": "11111111-1111-4111-8111-111111111111"}]}
                )
            if request.method == "POST" and request.url.path == "/search":
                return httpx.Response(
                    200,
                    json={
                        "results": [
                            {
                                "metadata": {"record_id": "r1", "version": 1, "scope": "personal"},
                                "score": 0.8,
                            }
                        ]
                    },
                )
            if request.method == "GET":
                return httpx.Response(
                    200,
                    json={
                        "results": [
                            {
                                "id": "11111111-1111-4111-8111-111111111111",
                                "metadata": {"record_id": "r1", "version": 1},
                            },
                            {"id": "other", "metadata": {"record_id": "r2", "version": 1}},
                        ]
                    },
                )
            return httpx.Response(200, json={"message": "deleted"})

        with patch("memories.MilvusTransport", return_value=httpx.MockTransport(reply)):
            target = Memories("a" * 64, SimpleNamespace(url="https://memory.example", token="key"))
            try:
                target.verify()
                target.put({"id": "r1", "version": 1, "scope": "personal", "content": "hello"}, [])
                self.assertEqual(target.search("hello", "personal", 5)[0]["record_id"], "r1")
                target.delete("r1", 1)
                self.assertEqual(
                    [r.url.path for r in seen if r.method == "DELETE"],
                    ["/memories/11111111-1111-4111-8111-111111111111"],
                )
                self.assertTrue(all(r.headers.get("X-API-Key") == "key" for r in seen))
                self.assertEqual(seen[0].url.params["user_id"], "a" * 64)
            finally:
                target.close()


if __name__ == "__main__":
    unittest.main()
