"""The OSS Mem0 SDK owns memory storage and recall, without automatic extraction."""
import os

from mem0 import Memory

_memory = None


def memory():
    global _memory
    if _memory is None:
        _memory = Memory.from_config({
            "version": "v1.1",
            "vector_store": {"provider": "qdrant", "config": {
                "url": os.environ["QDRANT_URL"], "api_key": os.environ["HC_CONTEXT_TEST_KEY"],
                "collection_name": "hc_explicit_memories", "embedding_model_dims": 512,
            }},
            "embedder": {"provider": "fastembed", "config": {
                "model": "BAAI/bge-small-zh-v1.5", "model_kwargs": {"cache_dir": "/models", "threads": 2},
            }},
            # infer=False never calls this LLM. No external model key is supplied.
            "llm": {"provider": "openai", "config": {"api_key": "disabled-manual-memory-only", "model": "disabled"}},
            "history_db_path": "/data/mem0-history.db",
        })
    return _memory


class Memories:
    def __init__(self, namespace):
        self.namespace = namespace
        self.client = memory()

    def verify(self):
        self.client.get_all(filters={"user_id": self.namespace}, top_k=1)
        return "2.2.1"

    def put(self, record, chunks):
        result = self.client.add(record["content"], user_id=self.namespace, infer=False, metadata={
            "record_id": record["id"], "version": record["version"], "scope": record["scope"],
        })
        if not result.get("results"):
            raise RuntimeError("memory write did not return a receipt")

    def search(self, query, scope, limit):
        rows = self.client.search(query, filters={"user_id": self.namespace, "scope": scope}, top_k=limit)["results"]
        return [{**r.get("metadata", {}), "score": r.get("score", 0), "chunk": 0} for r in rows]

    def delete(self, record_id, version=None):
        rows = self.client.get_all(filters={"user_id": self.namespace, "record_id": record_id}, top_k=1000)["results"]
        for row in rows:
            # Scope is enforced before using a vendor internal ID for deletion.
            if row.get("metadata", {}).get("record_id") == record_id and (version is None or row.get("metadata", {}).get("version") == version):
                self.client.delete(row["id"])

    def close(self):
        pass
