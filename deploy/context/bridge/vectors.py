"""Native OSS backends; no local vector index or fallback search engine."""
import json
import os
import uuid

import httpx
from fastembed import TextEmbedding
import document_chunks


MODEL = "BAAI/bge-small-zh-v1.5"
DIMENSIONS = 512
_embedding = None


def embedding():
    global _embedding
    if _embedding is None:
        _embedding = TextEmbedding(MODEL, cache_dir="/models", threads=2)
    return _embedding


def embed(texts, query=False):
    method = embedding().query_embed if query else embedding().passage_embed
    return [x.tolist() for x in method(texts)]


class Vectors:
    def __init__(self, backend, namespace, service=None, embedding_config=None):
        from embedding_provider import EmbeddingProvider
        self.embedder = EmbeddingProvider(embedding_config)
        self.backend, self.namespace = backend, namespace
        self.collection = ("Hc" if backend == "weaviate" else "hc_") + namespace
        self.url = service.url if service else os.environ[backend.upper() + "_URL"]
        key = service.token if service else os.environ["HC_CONTEXT_TEST_KEY"]
        headers = {"api-key": key} if backend == "qdrant" else {"Authorization": "Bearer " + key}
        transport = None
        if service:
            from milvus_service import MilvusTransport, service_url
            if backend not in ("milvus", "weaviate", "qdrant"):
                raise ValueError("Custom service backend not supported")
            service_url(self.url)
            transport = MilvusTransport()
            if not key:
                headers = {}
        self.http = httpx.Client(base_url=self.url, headers=headers, timeout=12, trust_env=False, transport=transport, follow_redirects=False)

    def request(self, method, path, data=None):
        response = self.http.request(method, path, json=data)
        response.raise_for_status()
        result = response.json()
        if self.backend == "milvus" and result.get("code", 0) != 0:
            raise RuntimeError("milvus rejected request")
        if isinstance(result, dict) and result.get("errors"):
            raise RuntimeError("weaviate rejected request")
        return result

    def verify(self):
        c = self.collection
        if self.backend == "qdrant":
            version = self.request("GET", "/")["version"]
            if self.http.get("/collections/" + c).status_code == 404:
                self.request("PUT", "/collections/" + c, {"vectors": {"size": self.embedder.dimensions, "distance": "Cosine"}})
            info = self.request("GET", "/collections/" + c)["result"]
            if info["config"]["params"]["vectors"]["size"] != self.embedder.dimensions:
                raise RuntimeError("embedding dimension mismatch")
        elif self.backend == "weaviate":
            version = self.request("GET", "/v1/meta")["version"]
            response = self.http.get("/v1/schema/" + c)
            if response.status_code == 404:
                self.request("POST", "/v1/schema", {
                    "class": c, "vectorizer": "none",
                    "properties": [
                        {"name": "record_id", "dataType": ["text"], "tokenization": "field"},
                        {"name": "scope", "dataType": ["text"], "tokenization": "field"},
                        {"name": "version", "dataType": ["int"]}, {"name": "chunk", "dataType": ["int"]},
                    ],
                })
            else:
                response.raise_for_status()
        else:
            version = "REST-v2"  # the native REST endpoint does not advertise its server version
            collections = self.request("POST", "/v2/vectordb/collections/list", {})["data"]
            if c not in collections:
                self.request("POST", "/v2/vectordb/collections/create", {
                    "collectionName": c, "dimension": self.embedder.dimensions, "metricType": "COSINE",
                    "primaryFieldName": "id", "idType": "VarChar",
                    "autoId": False, "vectorFieldName": "vector",
                    "params": {"max_length": "64", "enableDynamicField": True},
                })
            info = self.request("POST", "/v2/vectordb/collections/describe", {"collectionName": c})["data"]
            field = next((f for f in info["fields"] if f["name"] == "vector"), None)
            params = field.get("params", {}) if field else {}
            if isinstance(params, list):
                params = {p["key"]: p["value"] for p in params}
            if int(params.get("dim", 0)) != self.embedder.dimensions:
                raise RuntimeError("embedding dimension mismatch")
        # Verification exercises the configured provider before any document write.
        self.embedder.embed(["服务验证"])
        return version

    def put(self, record, chunks):
        c = self.collection
        vectors = self.embedder.embed([document_chunks.embedding_text(record.get("title", ""), chunk) for chunk in chunks])
        rows = []
        for i, vector in enumerate(vectors):
            identity = str(uuid.uuid5(uuid.NAMESPACE_URL, self.namespace + record["id"] + str(record["version"]) + ":" + str(i)))
            payload = {"record_id": record["id"], "scope": record["scope"], "version": record["version"], "chunk": i}
            if self.backend == "qdrant":
                rows.append({"id": identity, "vector": vector, "payload": payload})
            elif self.backend == "weaviate":
                rows.append({"class": c, "id": identity, "vector": vector, "properties": payload})
            else:
                rows.append({"id": identity, "vector": vector, **payload})
        if self.backend == "qdrant":
            self.request("PUT", "/collections/" + c + "/points?wait=true", {"points": rows})
        elif self.backend == "weaviate":
            result = self.request("POST", "/v1/batch/objects", {"objects": rows})
            if any(item.get("result", {}).get("errors") for item in result):
                raise RuntimeError("partial indexing failure")
        else:
            self.request("POST", "/v2/vectordb/entities/upsert", {"collectionName": c, "data": rows})

    def search(self, query, scope, limit):
        vector = self.embedder.embed([query], query=True)[0]
        c = self.collection
        if self.backend == "qdrant":
            result = self.request("POST", "/collections/" + c + "/points/query", {
                "query": vector, "limit": limit, "with_payload": True,
                "filter": {"must": [{"key": "scope", "match": {"value": scope}}]},
            })["result"]["points"]
            return [{**x["payload"], "score": x["score"]} for x in result]
        if self.backend == "weaviate":
            expr = ("{Get{" + c + "(nearVector:{vector:" + json.dumps(vector)
                    + "},where:{path:[\"scope\"],operator:Equal,valueText:" + json.dumps(scope)
                    + "},limit:" + str(limit) + "){record_id scope version chunk _additional{distance}}}}")
            result = self.request("POST", "/v1/graphql", {"query": expr})["data"]["Get"][c]
            return [{**x, "score": 1 - x["_additional"]["distance"]} for x in result]
        result = self.request("POST", "/v2/vectordb/entities/search", {
            "collectionName": c, "data": [vector], "annsField": "vector", "limit": limit,
            "filter": "scope == " + json.dumps(scope), "consistencyLevel": "Strong",
            "outputFields": ["record_id", "scope", "version", "chunk"],
        })["data"]
        return [{**x, "score": x["distance"]} for x in result]

    def delete(self, record_id, version=None):
        c = self.collection
        if self.backend == "qdrant":
            conditions = [{"key": "record_id", "match": {"value": record_id}}]
            if version is not None: conditions.append({"key":"version","match":{"value":version}})
            self.request("POST", "/collections/" + c + "/points/delete?wait=true", {"filter": {"must": conditions}})
        elif self.backend == "weaviate":
            where = {"path": ["record_id"], "operator": "Equal", "valueText": record_id}
            if version is not None: where = {"operator":"And","operands":[where,{"path":["version"],"operator":"Equal","valueInt":version}]}
            self.request("DELETE", "/v1/batch/objects", {"match": {"class":c,"where":where}})
        else:
            expr = "record_id == " + json.dumps(record_id)
            if version is not None: expr += " and version == " + str(version)
            self.request("POST", "/v2/vectordb/entities/delete", {"collectionName":c,"filter":expr})

    def close(self):
        self.http.close()
        self.embedder.close()
