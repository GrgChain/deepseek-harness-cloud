"""Graphiti SDK backed by an isolated Neo4j graph; partition is never client chosen."""
import asyncio
import base64
import json
import os
import ipaddress
import socket
import sqlite3
import uuid
from datetime import datetime
from urllib.parse import urlsplit
from neo4j import AsyncGraphDatabase
from neo4j._addressing import ResolvedAddress
from graphiti_core import Graphiti
from graphiti_core.driver.neo4j_driver import Neo4jDriver
from graphiti_core.embedder.client import EmbedderClient, EmbedderConfig
from graphiti_core.llm_client import LLMConfig
from graphiti_core.llm_client.openai_client import OpenAIClient
from graphiti_core.nodes import EpisodeType
from graphiti_core.cross_encoder.openai_reranker_client import OpenAIRerankerClient
from vectors import embed


class LocalEmbedder(EmbedderClient):
    config = EmbedderConfig(embedding_dim=512)

    async def create(self, input_data):
        return embed([input_data if isinstance(input_data, str) else " ".join(input_data)])[0]

    async def create_batch(self, input_data_list):
        return embed(input_data_list)


def graphiti_target(service):
    parsed = urlsplit(service.url)
    if (
        parsed.scheme not in ("bolt", "bolt+s")
        or not parsed.hostname
        or parsed.username
        or parsed.password
        or parsed.path not in ("", "/")
        or parsed.query
        or parsed.fragment
    ):
        raise ValueError("Invalid Neo4j address")
    port = parsed.port or 7687
    user, separator, password = service.token.partition(":")
    if not separator or not user.strip() or not password:
        raise ValueError("Neo4j username and password required")

    def resolver(address):
        if address.host != parsed.hostname or address.port_number != port:
            raise ValueError("Unexpected Neo4j address")
        entries = socket.getaddrinfo(parsed.hostname, port, type=socket.SOCK_STREAM)
        if not entries:
            raise ValueError("Neo4j address unavailable")
        result = []
        for _, _, _, _, sockaddr in entries:
            ip = ipaddress.ip_address(sockaddr[0])
            ip = getattr(ip, "ipv4_mapped", None) or ip
            if (
                ip.is_loopback
                or ip.is_link_local
                or ip.is_unspecified
                or ip.is_multicast
                or ip.is_reserved
                or ip in ipaddress.ip_network("100.64.0.0/10")
                or (parsed.scheme == "bolt" and not ip.is_private)
            ):
                raise ValueError("Neo4j address denied")
            if len(sockaddr) == 4 and sockaddr[3]:
                raise ValueError("Neo4j scoped address denied")
            result.append(ResolvedAddress(sockaddr, host_name=parsed.hostname))
        return result

    return user, password, resolver


class GraphMemory:
    def __init__(self, namespace, service=None):
        self.namespace = namespace
        self.service = service
        self.db = sqlite3.connect("/data/graphiti-map.db")
        self.db.execute(
            "CREATE TABLE IF NOT EXISTS episodes(namespace TEXT, id TEXT, version INTEGER, scope TEXT, episode TEXT, graph TEXT, PRIMARY KEY(namespace,id,version))"
        )

    async def client(self):
        model = json.loads(base64.b64decode(os.environ["HC_CONTEXT_MODEL_CONFIG"]))
        cfg = LLMConfig(
            api_key=model["api_key"],
            base_url=model["api_base"],
            model=model["model"],
            small_model=model["model"],
            max_tokens=4096,
            temperature=0,
        )
        if self.service:
            user, password, resolver = graphiti_target(self.service)
            driver = await asyncio.to_thread(Neo4jDriver, self.service.url, user, password)
            old = driver.client
            driver.client = AsyncGraphDatabase.driver(
                self.service.url, auth=(user, password), resolver=resolver
            )
            await old.close()
            return Graphiti(
                graph_driver=driver,
                llm_client=OpenAIClient(cfg, max_tokens=4096),
                embedder=LocalEmbedder(),
                cross_encoder=OpenAIRerankerClient(cfg),
                max_coroutines=2,
            )
        return Graphiti(
            os.environ["NEO4J_URI"],
            "neo4j",
            os.environ["NEO4J_PASSWORD"],
            llm_client=OpenAIClient(cfg, max_tokens=4096),
            embedder=LocalEmbedder(),
            cross_encoder=OpenAIRerankerClient(cfg),
            max_coroutines=2,
        )

    def group(self, scope):
        import hashlib

        return "hc_" + hashlib.sha256((self.namespace + ":" + scope).encode()).hexdigest()

    def run(self, action):
        async def execute():
            client = await self.client()
            try:
                return await action(client)
            finally:
                await client.close()
                await client.llm_client.client.close()
                await client.cross_encoder.client.close()

        return asyncio.run(execute())

    def verify(self):
        async def action(c):
            await c.build_indices_and_constraints()

        self.run(action)
        return "0.31.2"

    def probe(self):
        async def action(c):
            await c.driver.execute_query(
                "CREATE (n:HCConnectionProbe {group_id:$group}) WITH n DELETE n",
                group=self.group("personal"),
            )

        self.run(action)
        if len(embed(["服务验证"])[0]) != 512:
            raise RuntimeError("embedding dimension mismatch")

    def put(self, record, chunks):
        identity = str(
            uuid.uuid5(
                uuid.NAMESPACE_URL,
                self.namespace + ":" + record["id"] + ":" + str(record["version"]),
            )
        )
        # Save mapping before network IO so interrupted ingestion can be deleted/reconciled.
        self.db.execute(
            "INSERT OR REPLACE INTO episodes VALUES(?,?,?,?,?,?)",
            (self.namespace, record["id"], record["version"], record["scope"], identity, "{}"),
        )
        self.db.commit()

        async def action(c):
            from graphiti_core.nodes import EpisodicNode

            # Graphiti treats an explicit UUID as an existing episode reference.
            episode = EpisodicNode(
                uuid=identity,
                name=record.get("title") or record["id"],
                group_id=self.group(record["scope"] + ":v" + str(record["version"])),
                labels=[],
                source=EpisodeType.text,
                content=record["content"],
                source_description=record.get("source") or "explicitly confirmed memory",
                created_at=datetime.now().astimezone(),
                valid_at=datetime.fromisoformat(
                    record.get("updated_at") or datetime.now().astimezone().isoformat()
                ),
            )
            await episode.save(c.driver)
            result = await c.add_episode(
                name=record.get("title") or record["id"],
                episode_body=record["content"],
                source_description=record.get("source") or "explicitly confirmed user memory",
                reference_time=datetime.fromisoformat(
                    record.get("updated_at") or datetime.now().astimezone().isoformat()
                ),
                source=EpisodeType.text,
                group_id=self.group(record["scope"] + ":v" + str(record["version"])),
                uuid=identity,
                previous_episode_uuids=[],
            )
            return result.model_dump(mode="json", exclude={"episode": {"content"}})

        graph = self.run(action)
        self.db.execute(
            "UPDATE episodes SET graph=? WHERE namespace=? AND id=? AND version=?",
            (
                json.dumps(graph, ensure_ascii=False),
                self.namespace,
                record["id"],
                record["version"],
            ),
        )
        self.db.commit()
        return {"graph": graph}

    def search(self, query, scope, limit):
        async def action(c):
            from graphiti_core.search.search_config import (
                SearchConfig,
                EdgeSearchConfig,
                EdgeSearchMethod,
                NodeSearchConfig,
                NodeSearchMethod,
                EpisodeSearchConfig,
                EpisodeSearchMethod,
            )
            from graphiti_core.nodes import EpisodicNode

            groups = [
                self.group(scope + ":v" + str(v[0]))
                for v in self.db.execute(
                    "SELECT DISTINCT version FROM episodes WHERE namespace=? AND scope=?",
                    (self.namespace, scope),
                )
            ]
            if not groups:
                return []
            cfg = SearchConfig(
                edge_config=EdgeSearchConfig(
                    search_methods=[EdgeSearchMethod.bm25, EdgeSearchMethod.cosine_similarity]
                ),
                node_config=NodeSearchConfig(
                    search_methods=[NodeSearchMethod.bm25, NodeSearchMethod.cosine_similarity]
                ),
                episode_config=EpisodeSearchConfig(search_methods=[EpisodeSearchMethod.bm25]),
                limit=limit,
            )
            result = await c.search_(query, cfg, group_ids=groups)
            episodes = [e.uuid for e in result.episodes]
            for edge in result.edges:
                episodes.extend(edge.episodes)
            for node in result.nodes:
                episodes.extend(
                    e.uuid
                    for e in await EpisodicNode.get_by_entity_node_uuid(c.driver, node.uuid)
                    if e.group_id in groups
                )
            return list(dict.fromkeys(episodes))

        episodes = self.run(action)
        mapping = {
            r[0]: (r[1], r[2])
            for r in self.db.execute(
                "SELECT episode,id,version FROM episodes WHERE namespace=? AND scope=?",
                (self.namespace, scope),
            )
        }
        return [
            {"record_id": mapping[e][0], "version": mapping[e][1], "chunk": 0, "score": 1 / (i + 1)}
            for i, e in enumerate(episodes)
            if e in mapping
        ][:limit]

    def delete(self, record_id, version=None):
        rows = self.db.execute(
            "SELECT episode,version FROM episodes WHERE namespace=? AND id=?"
            + (" AND version=?" if version is not None else ""),
            (self.namespace, record_id, *([version] if version is not None else [])),
        ).fetchall()
        for identity, v in rows:

            async def action(c):
                from graphiti_core.nodes import EpisodicNode
                from graphiti_core.errors import NodeNotFoundError

                try:
                    node = await EpisodicNode.get_by_uuid(c.driver, identity)
                except NodeNotFoundError:
                    return
                if node.group_id not in [
                    self.group(r[0] + ":v" + str(v))
                    for r in self.db.execute(
                        "SELECT scope FROM episodes WHERE namespace=? AND id=? AND version=?",
                        (self.namespace, record_id, v),
                    )
                ]:
                    raise RuntimeError("graph partition mismatch")
                await c.remove_episode(identity)

            self.run(action)
            self.db.execute(
                "DELETE FROM episodes WHERE namespace=? AND id=? AND version=?",
                (self.namespace, record_id, v),
            )
            self.db.commit()

    def close(self):
        self.db.close()
