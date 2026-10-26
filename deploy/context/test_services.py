#!/usr/bin/env python3
"""Real, isolated external-service smoke tests. No DSH container is accessed."""
import hashlib
import os
import hmac
import json
from pathlib import Path
import time
import unittest
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[2]
PRIVATE = ROOT / ".local/context"
KEY = (PRIVATE / "test-key").read_text().strip()
PROXY_KEY = (PRIVATE / "proxy-key").read_text().strip().encode()
HTTP = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def call(backend, namespace, action, signed=True, **values):
    payload = {"contract": "hc-context-v1", "namespace": namespace,
               "kind": "memory" if backend in ("mem0","openviking","graphiti") else "knowledge", "scope": "personal", **values}
    data = json.dumps(payload, ensure_ascii=False).encode()
    path = "/" + backend + "/v1/" + action
    stamp = str(int(time.time()))
    mac = hmac.new(PROXY_KEY, stamp.encode() + b"\n" + path.encode() + b"\n" + data, hashlib.sha256).hexdigest()
    req = urllib.request.Request("http://127.0.0.1:18580" + path, data, {
        "Content-Type": "application/json", "Authorization": "Bearer " + KEY,
        "X-Context-Timestamp": stamp, "X-Context-Signature": mac if signed else "invalid",
    })
    deadline=time.monotonic()+300
    while True:
        try:
            stamp=str(int(time.time()))
            req.headers['X-context-timestamp']=stamp
            req.headers['X-context-signature']=hmac.new(PROXY_KEY,stamp.encode()+b"\n"+path.encode()+b"\n"+data,hashlib.sha256).hexdigest() if signed else 'invalid'
            with HTTP.open(req, timeout=300) as response:return json.load(response)
        except urllib.error.HTTPError as exc:
            if backend not in ("openviking","graphiti") or action not in ("apply","delete","rebuild") or exc.code!=503 or time.monotonic()>deadline:raise
            time.sleep(2)


class ExternalServices(unittest.TestCase):
    def test_native_write_recall_isolation_update_and_delete(self):
        for backend in os.environ.get("HC_CONTEXT_TEST_BACKENDS","qdrant,weaviate,milvus,mem0").split(","):
            with self.subTest(backend=backend):
                namespace = hashlib.sha256(uuid.uuid4().bytes).hexdigest()
                other = hashlib.sha256(uuid.uuid4().bytes).hexdigest()
                records = []
                print("Testing", backend, flush=True)
                result = call(backend, namespace, "verify")
                self.assertTrue(result["ready"])
                self.assertEqual(result["backend"], backend)
                with self.assertRaises(urllib.error.HTTPError) as rejected:
                    call(backend, namespace, "list", signed=False)
                self.assertEqual(rejected.exception.code, 401)
                try:
                    preview = call(backend, namespace, "preview", content="星河项目的发布日是每周三，发布负责人是李明。", title="发布约定", source="合成测试文档")
                    record_id = preview["items"][0]["id"]
                    records.append((record_id, 1))
                    operation = dict(token=preview["token"], mode="skip", idempotency_key=uuid.uuid4().hex, confirmed=True)
                    first = call(backend, namespace, "apply", **operation)
                    self.assertEqual(first, call(backend, namespace, "apply", **operation))
                    self.assertEqual(len(call(backend, namespace, "list")["items"]), 1)
                    hits = call(backend, namespace, "search", query="星河项目什么时候发布？", limit=5)["items"]
                    self.assertTrue(any(h["id"] == record_id and "每周三" in h["snippet"] for h in hits))
                    # The same service credential cannot bypass the platform's namespace MAC.
                    self.assertEqual(call(backend, other, "list")["items"], [])
                    with self.assertRaises(urllib.error.HTTPError) as rejected:
                        call(backend, other, "get", record_id=record_id)
                    self.assertEqual(rejected.exception.code, 404)
                    current = call(backend, namespace, "get", record_id=record_id)
                    preview = call(backend, namespace, "preview", record_id=record_id, record_version=1,
                                   content="星河项目的发布日改为每周五。", title=current["title"], source=current["source"])
                    call(backend, namespace, "apply", token=preview["token"], mode="replace", idempotency_key=uuid.uuid4().hex, confirmed=True)
                    records[0] = (record_id, 2)
                    hits = call(backend, namespace, "search", query="星河项目的发布日", limit=5)["items"]
                    self.assertTrue(any("每周五" in h["snippet"] for h in hits))
                    self.assertFalse(any("每周三" in h["snippet"] for h in hits))
                    if backend in ("mem0","openviking","graphiti"):
                        package = call(backend, namespace, "export")
                        self.assertEqual(package["schema"], "hc-memory-v1")
                        imported = call(backend, namespace, "preview", package=package)
                        self.assertTrue(imported["items"][0]["conflict"])
                    call(backend, namespace, "delete", record_id=record_id, record_version=2, idempotency_key=uuid.uuid4().hex, confirmed=True)
                    records.clear()
                    self.assertEqual(call(backend, namespace, "search", query="星河项目", limit=5)["items"], [])
                finally:
                    for identity, version in records:
                        try:
                            call(backend, namespace, "delete", record_id=identity, record_version=version, idempotency_key=uuid.uuid4().hex, confirmed=True)
                        except urllib.error.HTTPError:
                            pass

    @unittest.skipUnless(os.environ.get("HC_CONTEXT_TEST_MIGRATION")=="true","optional real model migration")
    def test_portable_graph_memory_migrates_without_deleting_source(self):
        source=hashlib.sha256(uuid.uuid4().bytes).hexdigest()
        targets=[]
        identity=None
        try:
            call('graphiti',source,'verify')
            p=call('graphiti',source,'preview',content='迁移测试项目的负责人是李明，发布日为每周三。',title='迁移约定',source='本人确认的合成测试')
            identity=p['items'][0]['id']
            call('graphiti',source,'apply',token=p['token'],mode='skip',idempotency_key=uuid.uuid4().hex,confirmed=True)
            package=call('graphiti',source,'export')
            self.assertIsInstance(package['entries'][0]['graph']['nodes'],list)
            self.assertTrue(package['entries'][0]['graph']['episode']['valid_at'])
            for backend in ('mem0','openviking'):
                target=hashlib.sha256(uuid.uuid4().bytes).hexdigest();targets.append((backend,target))
                call(backend,target,'verify')
                staged=call(backend,target,'preview',package=package)
                self.assertTrue(staged['warnings'])
                with self.assertRaises(urllib.error.HTTPError) as rejected:
                    call(backend,target,'apply',token=staged['token'],mode='skip',idempotency_key=uuid.uuid4().hex,confirmed=True)
                self.assertEqual(rejected.exception.code,422)
                call(backend,target,'apply',token=staged['token'],mode='skip',idempotency_key=uuid.uuid4().hex,confirmed=True,accept_loss=True)
                exported=call(backend,target,'export')
                for field in ('content','source','type','created_at','updated_at','graph'):
                    self.assertEqual(exported['entries'][0][field],package['entries'][0][field])
                self.assertTrue(call(backend,target,'search',query='迁移测试项目负责人和发布日',limit=5)['items'])
            self.assertEqual(call('graphiti',source,'export')['sha256'],package['sha256'])
        finally:
            for backend,namespace in targets:
                if identity:
                    try:call(backend,namespace,'delete',record_id=identity,record_version=1,idempotency_key=uuid.uuid4().hex,confirmed=True)
                    except urllib.error.HTTPError:pass
            if identity:
                call('graphiti',source,'delete',record_id=identity,record_version=1,idempotency_key=uuid.uuid4().hex,confirmed=True)


if __name__ == "__main__":
    unittest.main(verbosity=2)
