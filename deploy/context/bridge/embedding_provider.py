"""Per-knowledge-base embeddings; credentials never enter stored receipts."""
import math
import time

import httpx
from milvus_service import MilvusTransport


DEFAULT_MODEL = "BAAI/bge-small-zh-v1.5"
DEFAULT_DIMENSIONS = 512
API_BATCH_SIZE = 8


class EmbeddingProvider:
    def __init__(self, config=None):
        self.provider = config.provider if config else "local"
        self.model = config.model if config else DEFAULT_MODEL
        self.dimensions = config.dimensions if config else DEFAULT_DIMENSIONS
        self.http = None
        if self.provider == "local":
            if self.model != DEFAULT_MODEL or self.dimensions != DEFAULT_DIMENSIONS or config and (config.api_base or config.api_key):
                raise ValueError("Invalid local embedding model")
        elif self.provider == "openai_compatible":
            url = httpx.URL(config.api_base)
            if (url.scheme not in ("http", "https") or not url.host or url.userinfo or url.query
                    or url.fragment or ".." in url.path or not self.model.strip() or not 1 <= self.dimensions <= 32768):
                raise ValueError("Invalid embedding service configuration")
            headers = {"Authorization": "Bearer " + config.api_key} if config.api_key else {}
            self.http = httpx.Client(base_url=str(url).rstrip("/") + "/", headers=headers,
                                     timeout=12, transport=MilvusTransport(), trust_env=False, follow_redirects=False)
        else:
            raise ValueError("Unknown embedding provider")

    def embed(self, texts, query=False):
        if self.provider == "local":
            from vectors import embed
            result = embed(texts, query=query)
        else:
            result = []
            for offset in range(0, len(texts), API_BATCH_SIZE):
                batch = texts[offset:offset + API_BATCH_SIZE]
                for attempt in range(4):
                    response = self.http.post("embeddings", json={"model": self.model, "input": batch, "encoding_format": "float"})
                    if response.status_code != 429 or attempt == 3:
                        break
                    time.sleep(0.25 * 2 ** attempt)
                response.raise_for_status()
                rows = response.json().get("data")
                if not isinstance(rows, list) or len(rows) != len(batch):
                    raise ValueError("Embedding response count mismatch")
                if any(not isinstance(row, dict) or not isinstance(row.get("index"), int) or isinstance(row["index"], bool) for row in rows) or sorted(row["index"] for row in rows) != list(range(len(batch))):
                    raise ValueError("Embedding response indices mismatch")
                result.extend(row["embedding"] for row in sorted(rows, key=lambda row: row["index"]))
        if len(result) != len(texts) or any(not isinstance(v, list) or len(v) != self.dimensions or any(
                isinstance(x, bool) or not isinstance(x, (int, float)) or not math.isfinite(x) for x in v) for v in result):
            raise ValueError("Embedding vector dimension or value mismatch")
        return result

    def close(self):
        if self.http:
            self.http.close()
