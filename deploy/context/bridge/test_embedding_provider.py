"""Embedding selection, API validation and real vector request boundaries."""
import json
import os
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import patch

import httpx

os.environ.setdefault("CONTEXT_DB", tempfile.mktemp(suffix=".db"))
import app
from embedding_provider import EmbeddingProvider
from vectors import Vectors


def config(**changes):
    return SimpleNamespace(provider="openai_compatible", model="chosen-model", api_base="https://embed.example/v1",
                           dimensions=changes.pop("dimensions", 3), api_key="embedding-only-secret", **changes)


class EmbeddingSelection(unittest.TestCase):
    def test_large_documents_use_ordered_batches_and_retry_rate_limits(self):
        sizes=[]; attempts=0
        def response(request):
            nonlocal attempts
            attempts += 1
            batch=json.loads(request.read())["input"]
            sizes.append(len(batch))
            if attempts == 1: return httpx.Response(429)
            return httpx.Response(200,json={"data":[{"index":i,"embedding":[float(text),0,0]} for i,text in reversed(list(enumerate(batch)))]})
        with patch("embedding_provider.MilvusTransport",return_value=httpx.MockTransport(response)), patch("embedding_provider.time.sleep") as sleep:
            provider=EmbeddingProvider(config())
            try:
                result=provider.embed([str(i) for i in range(42)])
                self.assertEqual([row[0] for row in result],list(range(42)))
                self.assertEqual(sizes,[8,8,8,8,8,8,2])
                sleep.assert_called_once_with(0.25)
            finally: provider.close()

    def test_external_api_model_auth_batch_order_and_dimensions(self):
        requests=[]
        def response(request):
            requests.append((str(request.url), request.headers.get("Authorization"),json.loads(request.read())))
            return httpx.Response(200,json={"data":[{"index":1,"embedding":[4,5,6]},{"index":0,"embedding":[1,2,3]}]})
        with patch("embedding_provider.MilvusTransport",return_value=httpx.MockTransport(response)), patch("vectors.embed",side_effect=AssertionError("local fallback")):
            provider=EmbeddingProvider(config())
            try:
                self.assertEqual(provider.embed(["正文一","正文二"]),[[1,2,3],[4,5,6]])
                self.assertEqual(requests,[("https://embed.example/v1/embeddings","Bearer embedding-only-secret",{"model":"chosen-model","input":["正文一","正文二"],"encoding_format":"float"})])
            finally: provider.close()

    def test_invalid_output_auth_errors_and_redirects_do_not_fall_back(self):
        for body in [{"data":[]},{"data":[{"index":0,"embedding":[1,2]}]},{"data":[{"index":0,"embedding":[1,2,float('inf')]}]},{"data":[{"index":0,"embedding":[True,2,3]}]},{"data":[{"index":1,"embedding":[1,2,3]}]}]:
            with self.subTest(body=body), patch("embedding_provider.MilvusTransport",return_value=httpx.MockTransport(lambda r:httpx.Response(200,content=json.dumps(body)))), patch("vectors.embed",side_effect=AssertionError("local fallback")):
                provider=EmbeddingProvider(config())
                with self.assertRaises(ValueError): provider.embed(["问题"],query=True)
                provider.close()
        for status in [401,302,503]:
            with patch("embedding_provider.MilvusTransport",return_value=httpx.MockTransport(lambda r:httpx.Response(status,headers={"Location":"https://other.example/"}))), patch("vectors.embed",side_effect=AssertionError("local fallback")):
                provider=EmbeddingProvider(config())
                with self.assertRaises(httpx.HTTPStatusError): provider.embed(["问题"])
                provider.close()

    def test_local_default_keeps_passage_and_query_behavior(self):
        with patch("vectors.embed",return_value=[[0.1]*512]) as local:
            provider=EmbeddingProvider()
            self.assertEqual(len(provider.embed(["正文"])[0]),512)
            self.assertEqual(len(provider.embed(["问题"],query=True)[0]),512)
            self.assertEqual(local.call_args_list[0].kwargs,{"query":False})
            self.assertEqual(local.call_args_list[1].kwargs,{"query":True})
            provider.close()

    def test_selected_dimensions_reach_collection_and_vectors(self):
        native=SimpleNamespace(url="https://milvus.example",token="milvus-only-secret")
        requests=[]
        def db(request):
            body=json.loads(request.read());requests.append((str(request.url),request.headers.get("Authorization"),body))
            if request.url.path.endswith("/list"):return httpx.Response(200,json={"code":0,"data":[]})
            if request.url.path.endswith("/describe"):return httpx.Response(200,json={"code":0,"data":{"fields":[{"name":"vector","params":[{"key":"dim","value":"3"}]}]}})
            return httpx.Response(200,json={"code":0,"data":[]})
        def embedding(request):
            body=json.loads(request.read())
            return httpx.Response(200,json={"data":[{"index":i,"embedding":[1.0,0.0,0.0]} for i,_ in enumerate(body['input'])]})
        with patch("embedding_provider.MilvusTransport",return_value=httpx.MockTransport(embedding)),patch("milvus_service.MilvusTransport",return_value=httpx.MockTransport(db)):
            target=Vectors("milvus","a"*64,native,config())
            try:
                target.verify()
                target.put({"id":"doc","version":1,"scope":"personal"},["正文"])
                target.search("问题","personal",5)
                self.assertEqual(next(r[2]["dimension"] for r in requests if r[0].endswith('/create')),3)
                self.assertTrue(all(r[1]=="Bearer milvus-only-secret" for r in requests))
                self.assertEqual(next(r[2]['data'][0]['vector'] for r in requests if r[0].endswith('/upsert')),[1.0,0.0,0.0])
                self.assertEqual(next(r[2]['data'] for r in requests if r[0].endswith('/search')),[[1.0,0.0,0.0]])
            finally:target.close()

    def test_receipts_and_preview_match_selected_provider_without_keys(self):
        common=dict(contract=app.CONTRACT,kind="knowledge",namespace="f"*64,content="测试文档")
        external=app.Input(**common,embedding={"provider":"openai_compatible","model":"model","dimensions":3,"api_base":"https://embed.example/v1","api_key":"secret"})
        self.assertNotIn("secret",json.dumps(app.operation_input(external)))
        self.assertIn("文档片段和检索问题会发送到配置的 Embedding 服务",app.data_use("milvus",external))
        self.assertNotIn("费用",app.data_use("milvus",external))
        self.assertIn("平台本地模型",app.data_use("milvus",app.Input(**common)))


if __name__=="__main__":unittest.main()
