"""Custom Milvus targets, request routing, and boundary regression checks."""
import os
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import patch

import httpx

os.environ.setdefault("CONTEXT_DB", tempfile.mktemp(suffix=".db"))
import app
from milvus_service import MilvusTransport, service_url
from vectors import Vectors


class CustomMilvus(unittest.TestCase):
    def test_weaviate_and_qdrant_use_their_own_addresses_and_api_keys(self):
        for backend, header in [("weaviate", "Authorization"), ("qdrant", "api-key")]:
            for token in ("service-key", ""):
                with self.subTest(backend=backend, token=bool(token)), patch.dict(os.environ, {}, clear=True), patch("milvus_service.MilvusTransport", return_value=httpx.MockTransport(lambda r: httpx.Response(200, json={}))):
                    target = Vectors(backend, "a" * 64, SimpleNamespace(url="https://own.example:8443", token=token))
                    self.assertEqual(str(target.http.base_url), "https://own.example:8443")
                    self.assertEqual(target.http.headers.get(header), ("Bearer " if backend == "weaviate" else "") + token if token else None)
                    target.http.get("/health"); target.close()

    def test_each_connection_uses_its_url_token_and_collection(self):
        seen = []
        def reply(request):
            seen.append((str(request.url), request.headers.get("Authorization"), request.read()))
            return httpx.Response(200, json={"code": 0, "data": []})
        with patch("milvus_service.MilvusTransport", return_value=httpx.MockTransport(reply)):
            first = Vectors("milvus", "a" * 64, SimpleNamespace(url="https://first.example:19530", token="root:secret"))
            second = Vectors("milvus", "b" * 64, SimpleNamespace(url="https://second.example", token="another-token"))
            try:
                first.delete("document")
                second.delete("document")
                self.assertTrue(seen[0][0].startswith("https://first.example:19530/"))
                self.assertTrue(seen[1][0].startswith("https://second.example/"))
                self.assertEqual([r[1] for r in seen], ["Bearer root:secret", "Bearer another-token"])
                self.assertIn(("hc_" + "a" * 64).encode(), seen[0][2])
                self.assertIn(("hc_" + "b" * 64).encode(), seen[1][2])
            finally:
                first.close(); second.close()

    def test_custom_target_requires_no_global_milvus_url_and_supports_no_auth(self):
        with patch.dict(os.environ, {}, clear=True), patch("milvus_service.MilvusTransport", return_value=httpx.MockTransport(lambda r: httpx.Response(200, json={"data": [], "code": 0}))):
            target = Vectors("milvus", "a" * 64, SimpleNamespace(url="https://custom.example", token=""))
            self.assertNotIn("Authorization", target.http.headers)
            target.delete("document"); target.close()

    def test_dns_is_pinned_and_tls_uses_the_original_host(self):
        transport = MilvusTransport()
        transport.transport.close()
        reached = []
        def reply(request):
            reached.append((str(request.url), request.headers["Host"], request.extensions["sni_hostname"]))
            return httpx.Response(200)
        transport.transport = httpx.MockTransport(reply)
        with patch("socket.getaddrinfo", return_value=[(2, 1, 6, "", ("203.0.113.5", 443))]):
            with httpx.Client(transport=transport) as client:
                client.post("https://customer.example:19530/v2/vectordb/collections/list")
        self.assertEqual(reached, [("https://203.0.113.5:19530/v2/vectordb/collections/list", "customer.example:19530", "customer.example")])

    def test_private_lan_allowed_but_metadata_loopback_and_public_http_denied(self):
        for address in ["169.254.169.254", "127.0.0.1", "::1", "::ffff:127.0.0.1", "0.0.0.0", "224.0.0.1", "100.100.100.200", "1.1.1.1"]:
            with self.subTest(address=address), patch("socket.getaddrinfo", return_value=[(2,1,6,"",(address,19530))]):
                with httpx.Client(transport=MilvusTransport()) as client:
                    with self.assertRaises(ValueError):
                        client.post("http://customer.internal:19530/v2/vectordb/collections/list")
        transport = MilvusTransport(); transport.transport.close()
        transport.transport = httpx.MockTransport(lambda r: httpx.Response(200))
        with patch("socket.getaddrinfo", return_value=[(2,1,6,"",("10.3.2.1",19530))]):
            with httpx.Client(transport=transport) as client:
                self.assertEqual(client.post("http://milvus.internal:19530/v2/vectordb/collections/list").status_code,200)

    def test_service_url_and_receipts_exclude_credentials(self):
        for value in ["file:///data", "https://user:secret@milvus.example", "https://milvus.example/path", "https://milvus.example?key=secret"]:
            with self.assertRaises(ValueError): service_url(value)
        data=dict(contract=app.CONTRACT, namespace="c"*64, kind="knowledge", service={"url":"https://milvus.example", "token":"secret"})
        first=app.Input(**data); data["service"]["token"]="rotated"
        self.assertEqual(app.operation_input(first),app.operation_input(app.Input(**data)))
        legacy=app.Input(contract=app.CONTRACT,namespace="d"*64,kind="knowledge")
        self.assertNotIn("service",app.operation_input(legacy))
        self.assertNotIn("search_mode",app.operation_input(legacy))
        self.assertNotIn("secret",repr(first))


if __name__ == "__main__": unittest.main()
