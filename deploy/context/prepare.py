#!/usr/bin/env python3
"""Prepare private local test inputs; never print secret values."""
from pathlib import Path
import os
import secrets
import json
import base64
import argparse
parser=argparse.ArgumentParser()
parser.add_argument("--enable-model-memory",action="store_true",help="Approve local OpenViking/Graphiti endpoints using an explicitly approved private model configuration")
args=parser.parse_args()

root = Path(__file__).resolve().parents[2]
private = root / ".local" / "context"
private.mkdir(mode=0o700, parents=True, exist_ok=True)
os.chmod(private, 0o700)
for name in ("test-key", "proxy-key"):
    key = private / name
    if not key.exists():
        fd = os.open(key, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as stream:
            stream.write(secrets.token_urlsafe(32) + "\n")
    if key.is_symlink() or key.stat().st_mode & 0o077:
        raise SystemExit("Key file must be a private regular file")
env = private / "compose.env"
if env.is_symlink(): raise SystemExit("Environment file must not be a symlink")
fd = os.open(env, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w") as stream:
    stream.write("HC_CONTEXT_TEST_KEY=" + (private / "test-key").read_text().strip() + "\n")
    stream.write("HC_CONTEXT_PROXY_KEY=" + (private / "proxy-key").read_text().strip() + "\n")
model_file = private / "model.json"
if model_file.is_file():
    if model_file.stat().st_mode & 0o077: raise SystemExit("Model configuration must be private")
    model = json.loads(model_file.read_text())
    if set(("api_key","api_base","model")) - model.keys(): raise SystemExit("Incomplete approved model configuration")
    with env.open("a") as stream:
        stream.write("HC_CONTEXT_MODEL_CONFIG=" + base64.b64encode(model_file.read_bytes()).decode() + "\n")
    ov_file = private / "ov.conf"
    if not ov_file.exists():
        conf={"server":{"root_api_key":(private/"test-key").read_text().strip(),"cors_origins":[]},"storage":{"workspace":"/data","vectordb":{"backend":"local"},"agfs":{"backend":"local"}},"embedding":{"dense":{"provider":"openai","api_key":(private/"test-key").read_text().strip(),"api_base":"http://embeddings:8581/v1","model":"BAAI/bge-small-zh-v1.5","dimension":512,"input":"text"}},"vlm":{"provider":"openai","api_key":model["api_key"],"api_base":model["api_base"],"model":model["model"],"max_tokens":4096,"max_retries":1,"temperature":0}}
        fd=os.open(ov_file,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
        with os.fdopen(fd,"w") as stream: json.dump(conf,stream)

policy = private / "endpoints.local.json"
if not policy.exists():
    fd = os.open(policy, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w") as stream:
        json.dump({"endpoints": [{"id": "local-" + backend, "name": "本机测试 · " + backend,
                  "backend": backend, "url": "http://127.0.0.1:18580/" + backend,
                  "allow_private_http": True, "proxy_key_file": str(private / "proxy-key"),
                  "data_use": "数据保存在独立本机测试卷；使用本机中文 Embedding，无外部模型调用费用。"}
                 for backend in ("qdrant", "weaviate", "milvus", "mem0")]}, stream, ensure_ascii=False, indent=2)
print("Private Docker test inputs prepared in .local/context (no values printed).")

if args.enable_model_memory:
    if not model_file.is_file(): raise SystemExit("An explicitly approved private .local/context/model.json is required")
    approved=json.loads(policy.read_text())
    for backend in ("openviking","graphiti"):
        if not any(e["id"] == "local-"+backend for e in approved["endpoints"]):
            approved["endpoints"].append({"id":"local-"+backend,"name":"本机测试 · "+backend,"backend":backend,"url":"http://127.0.0.1:18580/"+backend,"allow_private_http":True,"proxy_key_file":str(private/"proxy-key"),"data_use":"数据保存在独立本机测试卷；正文将发送到已批准的推理模型进行记忆提取，可能产生调用费用。"})
    if policy.is_symlink() or policy.stat().st_mode & 0o077: raise SystemExit("Endpoint policy must be private")
    policy.write_text(json.dumps(approved,ensure_ascii=False,indent=2))
    print("Approved local OpenViking/Graphiti test endpoints enabled.")
