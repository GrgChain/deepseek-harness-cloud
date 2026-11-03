import asyncio
import base64
import json
import os
import socket
import unittest
from types import SimpleNamespace
from unittest.mock import patch

from neo4j.addressing import Address
from neo4j._async_compat.network._util import AsyncNetworkUtil
from graph_memory import GraphMemory, graphiti_target


class GraphTarget(unittest.TestCase):
    @unittest.skipUnless(os.environ.get("HC_TEST_GRAPHITI_URI"), "isolated Neo4j required")
    def test_real_driver_uses_custom_database_and_authentication(self):
        config = base64.b64encode(
            json.dumps(
                {"api_base": "https://example.com/v1", "api_key": "test-key", "model": "test-model"}
            ).encode()
        ).decode()
        with patch.dict(os.environ, {"HC_CONTEXT_MODEL_CONFIG": config}):
            target = GraphMemory(
                "test" * 16,
                SimpleNamespace(
                    url=os.environ["HC_TEST_GRAPHITI_URI"], token="neo4j:graphiti-test-password"
                ),
            )
            try:

                async def probe(client):
                    rows, _, _ = await client.driver.execute_query("RETURN 42 AS value")
                    return rows[0]["value"]

                self.assertEqual(target.run(probe), 42)
                self.assertEqual(target.verify(), "0.31.2")
                with patch("graph_memory.embed", return_value=[[0.0] * 512]):
                    target.probe()
            finally:
                target.close()
            invalid = GraphMemory(
                "test" * 16,
                SimpleNamespace(
                    url=os.environ["HC_TEST_GRAPHITI_URI"], token="neo4j:wrong-password"
                ),
            )
            try:
                with self.assertRaises(Exception):
                    invalid.run(probe)
            finally:
                invalid.close()

    def test_pins_dns_and_preserves_hostname_for_tls(self):
        user, password, resolver = graphiti_target(
            SimpleNamespace(url="bolt+s://neo4j.example:7687", token="alice:secret:part")
        )
        self.assertEqual((user, password), ("alice", "secret:part"))
        addresses = [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("10.2.3.4", 7687))]

        async def resolve():
            return [
                address
                async for address in AsyncNetworkUtil.resolve_address(
                    Address(("neo4j.example", 7687)), resolver=resolver
                )
            ]

        with patch("graph_memory.socket.getaddrinfo", return_value=addresses):
            result = asyncio.run(resolve())
        self.assertEqual(result[0].host, "10.2.3.4")
        self.assertEqual(result[0]._host_name, "neo4j.example")

    def test_rejects_unsafe_addresses_and_credentials(self):
        for url, token in [
            ("https://example.com", "a:b"),
            ("bolt+s://user:pass@example.com", "a:b"),
            ("bolt://example.com/path", "a:b"),
            ("bolt://example.com", "password"),
        ]:
            with self.subTest(url=url), self.assertRaises(ValueError):
                graphiti_target(SimpleNamespace(url=url, token=token))
        for url, address in [
            ("bolt://example.com", "1.1.1.1"),
            ("bolt+s://example.com", "127.0.0.1"),
            ("bolt+s://example.com", "169.254.169.254"),
        ]:
            with self.subTest(url=url, address=address), patch(
                "graph_memory.socket.getaddrinfo",
                return_value=[(socket.AF_INET, socket.SOCK_STREAM, 6, "", (address, 7687))],
            ):
                _, _, resolver = graphiti_target(SimpleNamespace(url=url, token="a:b"))
                with self.assertRaises(ValueError):
                    resolver(Address(("example.com", 7687)))


if __name__ == "__main__":
    unittest.main()
