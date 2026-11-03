"""The OSS Mem0 SDK owns memory storage and recall, without automatic extraction."""
import os
import hashlib
import uuid
import httpx
from milvus_service import MilvusTransport, service_url

_memory = None


def memory():
    from mem0 import Memory

    global _memory
    if _memory is None:
        _memory = Memory.from_config(
            {
                "version": "v1.1",
                "vector_store": {
                    "provider": "qdrant",
                    "config": {
                        "url": os.environ["QDRANT_URL"],
                        "api_key": os.environ["HC_CONTEXT_TEST_KEY"],
                        "collection_name": os.environ.get(
                            "HC_MEM0_COLLECTION", "hc_explicit_memories"
                        ),
                        "embedding_model_dims": 512,
                    },
                },
                "embedder": {
                    "provider": "fastembed",
                    "config": {
                        "model": "BAAI/bge-small-zh-v1.5",
                        "model_kwargs": {"cache_dir": "/models", "threads": 2},
                    },
                },
                # infer=False never calls this LLM. No external model key is supplied.
                "llm": {
                    "provider": "openai",
                    "config": {"api_key": "disabled-manual-memory-only", "model": "disabled"},
                },
                "history_db_path": "/data/mem0-history.db",
            }
        )
    return _memory


class Memories:
    def __init__(self, namespace, service=None):
        self.namespace = namespace
        self.http = (
            httpx.Client(
                base_url=service_url(service.url),
                headers={"X-API-Key": service.token},
                timeout=180,
                trust_env=False,
                transport=MilvusTransport(),
            )
            if service
            else None
        )
        self.client = None if service else memory()

    def request(self, method, path, **kwargs):
        response = self.http.request(method, path, **kwargs)
        response.raise_for_status()
        return response.json()

    def verify(self):
        if self.http:
            self.request("GET", "/memories", params={"user_id": self.namespace, "top_k": 1})
            return "REST"
        self.client.get_all(filters={"user_id": self.namespace}, top_k=1)
        return "2.2.1"

    def put(self, record, chunks):
        if self.http:
            result = self.request(
                "POST",
                "/memories",
                json={
                    "messages": [{"role": "user", "content": record["content"]}],
                    "user_id": self.namespace,
                    "agent_id": hashlib.sha256(record["id"].encode()).hexdigest(),
                    "infer": False,
                    "metadata": {
                        "record_id": record["id"],
                        "version": record["version"],
                        "scope": record["scope"],
                    },
                },
            )
            if not result.get("results"):
                raise RuntimeError("memory write did not return a receipt")
            return
        result = self.client.add(
            record["content"],
            user_id=self.namespace,
            infer=False,
            metadata={
                "record_id": record["id"],
                "version": record["version"],
                "scope": record["scope"],
            },
        )
        if not result.get("results"):
            raise RuntimeError("memory write did not return a receipt")

    def search(self, query, scope, limit):
        if self.http:
            rows = self.request(
                "POST",
                "/search",
                json={
                    "query": query,
                    "filters": {"user_id": self.namespace, "scope": scope},
                    "top_k": limit,
                },
            )["results"]
            return [
                {**r.get("metadata", {}), "score": r.get("score", 0), "chunk": 0}
                for r in rows
                if r.get("metadata", {}).get("scope") == scope
            ]
        rows = self.client.search(
            query, filters={"user_id": self.namespace, "scope": scope}, top_k=limit
        )["results"]
        return [{**r.get("metadata", {}), "score": r.get("score", 0), "chunk": 0} for r in rows]

    def delete(self, record_id, version=None):
        if self.http:
            rows = self.request(
                "GET",
                "/memories",
                params={
                    "user_id": self.namespace,
                    "agent_id": hashlib.sha256(record_id.encode()).hexdigest(),
                    "top_k": 1000,
                },
            )["results"]
            for row in rows:
                metadata = row.get("metadata", {})
                if metadata.get("record_id") == record_id and (
                    version is None or metadata.get("version") == version
                ):
                    native_id = str(uuid.UUID(str(row["id"])))
                    self.request("DELETE", "/memories/" + native_id)
            return
        rows = self.client.get_all(
            filters={"user_id": self.namespace, "record_id": record_id}, top_k=1000
        )["results"]
        for row in rows:
            # Scope is enforced before using a vendor internal ID for deletion.
            if row.get("metadata", {}).get("record_id") == record_id and (
                version is None or row.get("metadata", {}).get("version") == version
            ):
                self.client.delete(row["id"])

    def close(self):
        if self.http:
            self.http.close()
