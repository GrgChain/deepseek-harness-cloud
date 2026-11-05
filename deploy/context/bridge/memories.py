"""Mem0 storage/recall and explicit session-commit extraction."""
import os
import hashlib
import uuid
import base64
import json
import httpx
from milvus_service import MilvusTransport, service_url

_memory = None
_inference_memory = None


def memory(infer=False):
    from mem0 import Memory

    global _memory, _inference_memory
    current = _inference_memory if infer else _memory
    if current is None:
        llm = {"api_key": "disabled-manual-memory-only", "model": "disabled"}
        if infer:
            model = json.loads(base64.b64decode(os.environ["HC_CONTEXT_MODEL_CONFIG"]))
            llm = {
                "api_key": model["api_key"],
                "model": model["model"],
                "openai_base_url": model["api_base"],
                "temperature": 0,
                "max_tokens": 4096,
            }
        current = Memory.from_config(
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
                # Manual writes never invoke inference or need a model key.
                "llm": {"provider": "openai", "config": llm},
                "history_db_path": "/data/mem0-history.db",
            }
        )
        if infer:
            _inference_memory = current
        else:
            _memory = current
    return current


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

    def extraction_scope(self, commit_id):
        return hashlib.sha256((self.namespace + ":mem0-extraction").encode()).hexdigest(), commit_id

    def extraction_rows(self, commit_id):
        owner, agent = self.extraction_scope(commit_id)
        if self.http:
            return self.request(
                "GET", "/memories", params={"user_id": owner, "agent_id": agent, "top_k": 1000}
            )["results"]
        return self.client.get_all(filters={"user_id": owner, "agent_id": agent}, top_k=1000)[
            "results"
        ]

    def extract_session(self, messages, commit_id):
        # A separate staging identity prevents inference from editing manual
        # memories. Reconcile a prior timed-out write before calling the model.
        existing = self.extraction_rows(commit_id)
        if existing:
            return [row["memory"] for row in existing]
        owner, agent = self.extraction_scope(commit_id)
        transcript = json.dumps({"messages": messages}, ensure_ascii=False)
        body = {
            "messages": [{"role": "user", "content": transcript}],
            "user_id": owner,
            "agent_id": agent,
            "infer": True,
            "metadata": {"commit_id": commit_id},
        }
        if self.http:
            result = self.request("POST", "/memories", json=body)
        else:
            result = memory(infer=True).add(**body)
        if not isinstance(result, dict) or not isinstance(result.get("results"), list):
            raise RuntimeError("Missing Mem0 extraction receipt")
        return [row["memory"] for row in result["results"] if row.get("event") == "ADD"]

    def cleanup_extraction(self, commit_id):
        for row in self.extraction_rows(commit_id):
            identity = str(uuid.UUID(str(row["id"])))
            if self.http:
                self.request("DELETE", "/memories/" + identity)
            else:
                self.client.delete(identity)

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
