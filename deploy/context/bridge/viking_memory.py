"""Explicit memories stored/indexed by OpenViking; native root API stays internal."""
import hashlib
import math
import os
import re
import json
import sqlite3
import io
import zipfile
from concurrent.futures import ThreadPoolExecutor
from fastapi import HTTPException
import httpx
from milvus_service import MilvusTransport, service_url


class VikingMemory:
    SESSION_EXPORT_ITEMS = 10000
    SESSION_EXPORT_BYTES = 16 * 1024 * 1024
    SESSION_EXPORT_NODES = 20000

    def __init__(self, namespace, service=None):
        self.namespace = namespace
        self.keys = sqlite3.connect("/data/viking-keys.db")
        self.keys.execute(
            "CREATE TABLE IF NOT EXISTS tenant_keys(namespace TEXT PRIMARY KEY, key TEXT)"
        )
        self.root_key = service.token if service else os.environ["HC_CONTEXT_TEST_KEY"]
        if not self.root_key:
            raise ValueError("OpenViking administrator API key required")
        saved = self.keys.execute(
            "SELECT key FROM tenant_keys WHERE namespace=?", (namespace,)
        ).fetchone()
        self.has_tenant = bool(saved)
        self.http = httpx.Client(
            base_url=service_url(service.url) if service else os.environ["OPENVIKING_URL"],
            headers={
                "X-API-Key": saved[0] if saved else self.root_key,
                "X-OpenViking-Account": "hc_" + namespace,
                "X-OpenViking-User": "owner",
            },
            timeout=180,
            trust_env=False,
            transport=MilvusTransport() if service else None,
        )

    def request(self, method, path, data=None, params=None, missing=False):
        response = self.http.request(method, path, json=data, params=params)
        if missing and response.status_code == 404:
            return None
        response.raise_for_status()
        result = response.json()
        if result.get("status") != "ok":
            raise RuntimeError("OpenViking rejected operation")
        return result.get("result")

    def root(self, scope):
        return "viking://user/owner/memories/harness_" + hashlib.sha256(scope.encode()).hexdigest()

    def uri(self, record):
        return (
            self.root(record["scope"]) + "/" + record["id"] + "_v" + str(record["version"]) + ".md"
        )

    def verify(self):
        # Creating an account is administrative and remains inside this companion.
        if self.http.headers["X-API-Key"] != self.root_key:
            return "0.4.16"
        response = self.http.post(
            "/api/v1/admin/accounts",
            json={
                "account_id": "hc_" + self.namespace,
                "admin_user_id": "owner",
                "seed": self.namespace,
            },
        )
        if response.status_code not in (200, 201, 409):
            response.raise_for_status()
        result = response.json().get("result") or {}
        key = result.get("user_key")
        if not key:
            raise RuntimeError("OpenViking tenant credential unavailable")
        self.keys.execute("INSERT OR REPLACE INTO tenant_keys VALUES(?,?)", (self.namespace, key))
        self.keys.commit()
        self.has_tenant = True
        self.http.headers["X-API-Key"] = key
        return "0.4.16"

    @staticmethod
    def session_uri(uri):
        if (
            not isinstance(uri, str)
            or len(uri) > 2048
            or any(ord(c) < 32 for c in uri)
            or any(c in uri for c in ("%", "?", "#", "\\"))
        ):
            return False
        prefix = "viking://user/owner/"
        if not uri.startswith(prefix):
            return False
        parts = uri[len(prefix) :].split("/")
        if any(not part or part in (".", "..") or part.startswith(".") for part in parts):
            return False
        return (
            parts[0] == "memories"
            or len(parts) >= 4
            and parts[0] == "peers"
            and parts[2] == "memories"
        ) and parts[-1].lower().endswith(".md")

    def session_list(self):
        if not self.has_tenant:
            return {"items": [], "truncated": False}
        items = []
        truncated = False
        for root in ("viking://user/owner/memories", "viking://user/owner/peers"):
            entries = (
                self.request(
                    "GET",
                    "/api/v1/fs/tree",
                    params={
                        "uri": root,
                        "output": "original",
                        "level_limit": 8,
                        "node_limit": 1000,
                    },
                    missing=True,
                )
                or []
            )
            if not isinstance(entries, list):
                raise RuntimeError("Invalid OpenViking listing")
            truncated = truncated or len(entries) >= 1000
            for entry in entries:
                if not isinstance(entry, dict) or entry.get("isDir") is not False:
                    continue
                uri = entry.get("uri")
                if not self.session_uri(uri):
                    continue
                parts = uri.split("/")
                items.append(
                    {
                        "id": uri,
                        "title": parts[-1][:-3],
                        "category": parts[-2] if parts[-2] != "memories" else "",
                        "updated_at": str(entry.get("modTime") or ""),
                    }
                )
        return {"items": items, "truncated": truncated}

    def session_get(self, uri):
        if not self.has_tenant or not self.session_uri(uri):
            raise ValueError("Invalid memory")
        content = self.request("GET", "/api/v1/content/read", params={"uri": uri})
        if not isinstance(content, str) or len(content) > 262144:
            raise RuntimeError("Invalid memory content")
        parts = uri.split("/")
        return {
            "id": uri,
            "title": parts[-1][:-3],
            "category": parts[-2] if parts[-2] != "memories" else "",
            "content": content,
        }

    def session_search(self, query, limit):
        if not self.has_tenant:
            return []
        if self.http.headers.get("X-API-Key") == self.root_key:
            raise RuntimeError("OpenViking tenant credential unavailable")
        if (
            not isinstance(query, str)
            or not query.strip()
            or not isinstance(limit, int)
            or isinstance(limit, bool)
            or limit < 1
        ):
            raise ValueError("Invalid memory search")
        limit = min(limit, 20)
        # The owner root includes every peer's memories. Find searches the
        # index directly, independent of the management listing's depth/node cap.
        result = self.request(
            "POST",
            "/api/v1/search/find",
            {
                "query": query,
                "target_uri": "viking://user/owner",
                "context_type": "memory",
                "limit": limit,
                "level": 2,
            },
        )
        if not isinstance(result, dict) or not isinstance(result.get("memories"), list):
            raise RuntimeError("Invalid OpenViking search result")
        candidates = {}
        for hit in result["memories"]:
            if not isinstance(hit, dict) or not self.session_uri(hit.get("uri")):
                continue
            if hit.get("context_type", "memory") != "memory" or hit.get("level", 2) != 2:
                continue
            score = hit.get("score")
            if isinstance(score, bool) or not isinstance(score, (int, float)):
                continue
            try:
                if not math.isfinite(score):
                    continue
            except OverflowError:
                continue
            uri = hit["uri"]
            if uri not in candidates or score > candidates[uri]["score"]:
                candidates[uri] = {"uri": uri, "score": score}
        ordered = sorted(candidates.values(), key=lambda hit: (-hit["score"], hit["uri"]))[:limit]

        def visible(hit):
            # Reading visible content gives a useful excerpt from the actual
            # memory and drops stale vector hits. Never return raw metadata.
            content = self.request(
                "GET",
                "/api/v1/content/read",
                params={"uri": hit["uri"], "offset": 0, "limit": 40, "raw": "false"},
                missing=True,
            )
            if content is None:
                return None
            if not isinstance(content, str):
                raise RuntimeError("Invalid memory content")
            # raw=false removes metadata before line slicing. Only strip a
            # complete reserved trailer defensively; preserve quoted comments
            # inside the actual body.
            content = re.sub(
                r"(?:\n\n|\A)<!--\s*MEMORY_FIELDS\s*\n(?:(?!-->).)*\n-->\s*\Z",
                "",
                content,
                flags=re.DOTALL,
            ).strip()
            parts = hit["uri"].split("/")
            return {
                "id": hit["uri"],
                "title": parts[-1][:-3],
                "type": "memory",
                "snippet": content[:2000],
                "source": "DSH 会话",
                "scope": "personal",
                "version": 0,
                "status": "READY",
                "created_at": "",
                "updated_at": "",
                "score": hit["score"],
                "category": parts[-2] if parts[-2] != "memories" else "",
                "memory_source": "automatic",
            }

        with ThreadPoolExecutor(max_workers=8) as pool:
            return [record for record in pool.map(visible, ordered) if record is not None]

    def session_read_optional(self, uri):
        if not self.session_uri(uri):
            raise ValueError("Invalid memory")
        if not self.has_tenant:
            return None
        content = self.request(
            "GET",
            "/api/v1/content/read",
            params={"uri": uri, "limit": -1, "raw": "true"},
            missing=True,
        )
        if content is not None and not isinstance(content, str):
            raise RuntimeError("Invalid memory content")
        if content is not None and len(content.encode("utf-8")) > self.SESSION_EXPORT_BYTES:
            raise HTTPException(413, "Memory export size limit exceeded")
        return content

    def session_export(self):
        if not self.has_tenant:
            return []
        # OpenViking 0.4.16 ls has no cursor and slices at node_limit. Walk
        # individual directories without a depth limit and fail at the sentinel
        # limit instead of producing a silently incomplete archive.
        memories = "viking://user/owner/memories"
        peers = "viking://user/owner/peers"
        pending = [(memories, True), (peers, True)]
        visited = set()
        items = {}
        nodes = 0
        while pending:
            directory, optional = pending.pop()
            if directory in visited:
                raise RuntimeError("Invalid OpenViking listing")
            visited.add(directory)
            entries = self.request(
                "GET",
                "/api/v1/fs/ls",
                params={
                    "uri": directory,
                    "recursive": "false",
                    "output": "original",
                    "node_limit": self.SESSION_EXPORT_ITEMS + 1,
                },
                missing=optional,
            )
            if entries is None:
                continue
            if not isinstance(entries, list):
                raise RuntimeError("Invalid OpenViking listing")
            if len(entries) >= self.SESSION_EXPORT_ITEMS + 1:
                raise HTTPException(413, "Memory export listing limit exceeded")
            nodes += len(entries)
            if nodes > self.SESSION_EXPORT_NODES:
                raise HTTPException(413, "Memory export listing limit exceeded")
            for entry in entries:
                if not isinstance(entry, dict) or not isinstance(entry.get("isDir"), bool):
                    raise RuntimeError("Invalid OpenViking listing")
                uri = entry.get("uri")
                if not isinstance(uri, str) or not uri.startswith(directory + "/"):
                    raise RuntimeError("Invalid OpenViking listing")
                name = uri[len(directory) + 1 :]
                if not name or "/" in name or name in (".", ".."):
                    raise RuntimeError("Invalid OpenViking listing")
                if name.startswith("."):
                    continue
                if entry["isDir"]:
                    child = uri + "/memories" if directory == peers else uri
                    if not self.session_uri(child + "/entry.md"):
                        raise RuntimeError("Invalid OpenViking listing")
                    pending.append((child, directory == peers))
                elif directory != peers and self.session_uri(uri):
                    if uri in items:
                        raise RuntimeError("Invalid OpenViking listing")
                    items[uri] = {
                        "uri": uri,
                        **({"updated_at": str(entry["modTime"])} if entry.get("modTime") else {}),
                    }
                    if len(items) > self.SESSION_EXPORT_ITEMS:
                        raise HTTPException(413, "Memory export item limit exceeded")
        result = []
        total = 0
        records = [items[uri] for uri in sorted(items)]
        # Only eight requests are outstanding, including on failure. The client
        # is thread-safe; closing the pool drains them before the caller closes it.
        with ThreadPoolExecutor(max_workers=8) as pool:
            for offset in range(0, len(records), 8):
                batch = records[offset : offset + 8]
                contents = pool.map(self.session_read_optional, (entry["uri"] for entry in batch))
                for entry, content in zip(batch, contents):
                    if content is None:
                        raise RuntimeError("Memory changed during export")
                    total += len(entry["uri"].encode("utf-8")) + len(content.encode("utf-8"))
                    if total > self.SESSION_EXPORT_BYTES:
                        raise HTTPException(413, "Memory export size limit exceeded")
                    result.append({**entry, "content": content})
        return result

    def session_put(self, uri, content):
        if (
            not self.session_uri(uri)
            or not isinstance(content, str)
            or len(content.encode("utf-8")) > self.SESSION_EXPORT_BYTES
            or "\x00" in content
        ):
            raise ValueError("Invalid memory")
        if not self.has_tenant:
            self.verify()
        parent = uri.rsplit("/", 1)[0]
        # mkdir creates missing ancestors and remains inside the tenant.
        response = self.http.post("/api/v1/fs/mkdir", json={"uri": parent})
        if response.status_code not in (200, 201, 409):
            response.raise_for_status()
        if not parent.endswith("/memories") and len(content.encode("utf-8")) <= 8 * 1024 * 1024:
            # Unlike content/write replace, batch-write preserves the complete
            # stored MEMORY_FIELDS trailer instead of treating it as body text.
            result = self.request(
                "POST",
                "/api/v1/content/batch-write",
                {
                    "root_uri": parent,
                    "operations": [{"uri": uri, "content": content, "mode": "upsert"}],
                    "wait": True,
                    "timeout": 170,
                },
            )
            if not isinstance(result, dict) or not any(
                isinstance(result.get(field), list) and uri in result[field]
                for field in ("created", "updated", "unchanged")
            ):
                raise RuntimeError("Memory indexing result is not confirmed")
            if uri in result.get("unchanged", []):
                self._session_reindex(uri)
        else:
            self._session_put_pack(uri, content)

    def _session_put_pack(self, uri, content):
        # The official batch API disallows a memory-root directory. A minimal
        # single-file OVPack restores these files without deleting siblings,
        # moving temporary directories or changing native metadata.
        name = uri.rsplit("/", 1)[1]
        raw = content.encode("utf-8")
        entry = {
            "path": "",
            "kind": "file",
            "size": len(raw),
            "sha256": hashlib.sha256(raw).hexdigest(),
            "user_id": "owner",
        }
        content_hash = hashlib.sha256(
            json.dumps(
                [{key: entry[key] for key in ("path", "size", "sha256")}],
                ensure_ascii=False,
                sort_keys=True,
                separators=(",", ":"),
            ).encode("utf-8")
        ).hexdigest()
        manifest = {
            "kind": "openviking.ovpack",
            "format_version": 3,
            "root": {"name": name, "uri": uri, "scope": "user", "user_id": "owner"},
            "entries": [entry],
            "content_sha256": content_hash,
            "index": {
                "records": {
                    "path": "_ovpack/index_records.jsonl",
                    "count": 0,
                    "sha256": hashlib.sha256(b"").hexdigest(),
                }
            },
        }
        with io.BytesIO() as buffer:
            with zipfile.ZipFile(buffer, "w", zipfile.ZIP_DEFLATED) as archive:
                archive.writestr(name + "/files", raw)
                archive.writestr(name + "/_ovpack/index_records.jsonl", b"")
                archive.writestr(
                    name + "/_ovpack/manifest.json", json.dumps(manifest, ensure_ascii=False)
                )
            response = self.http.post(
                "/api/v1/resources/temp_upload",
                files={"file": ("memory.ovpack", buffer.getvalue(), "application/zip")},
            )
        response.raise_for_status()
        uploaded = response.json()
        upload_result = (
            uploaded.get("result")
            if isinstance(uploaded, dict) and uploaded.get("status") == "ok"
            else None
        )
        upload_id = upload_result.get("temp_file_id") if isinstance(upload_result, dict) else None
        if not isinstance(upload_id, str) or not re.fullmatch(
            r"(?:upload_[a-f0-9]{32}\.ovpack|shared_[0-9]{13}-[a-f0-9]{32})", upload_id
        ):
            raise RuntimeError("Invalid OpenViking upload receipt")
        try:
            result = self.request(
                "POST",
                "/api/v1/pack/import",
                {
                    "temp_file_id": upload_id,
                    "parent": uri.rsplit("/", 1)[0],
                    "on_conflict": "overwrite",
                    "vector_mode": "recompute",
                },
            )
            if not isinstance(result, dict) or result.get("uri") != uri:
                raise RuntimeError("Invalid OpenViking import receipt")
        except httpx.HTTPStatusError:
            # 0.4.16 writes a single-file pack before its directory-only final
            # indexing step rejects that root. Confirm exact bytes, then perform
            # the file-aware reindex below; any unwritten result remains a failure.
            if self.session_read_optional(uri) != content:
                raise
        self._session_reindex(uri)

    def _session_reindex(self, uri):
        indexed = self.request(
            "POST", "/api/v1/content/reindex", {"uri": uri, "mode": "vectors_only", "wait": True}
        )
        if (
            not isinstance(indexed, dict)
            or indexed.get("status") != "completed"
            or indexed.get("uri") != uri
            or indexed.get("failed_records") != 0
            or indexed.get("unsupported_records") != 0
        ):
            raise RuntimeError("Memory indexing result is not confirmed")

    def put(self, record, chunks, probe=False):
        response = self.http.post("/api/v1/fs/mkdir", json={"uri": self.root(record["scope"])})
        if response.status_code not in (200, 201, 409):
            response.raise_for_status()
        self.request(
            "POST",
            "/api/v1/content/write",
            {
                "uri": self.uri(record),
                "content": record["content"],
                "mode": "create",
                "wait": True,
                "timeout": 170,
                **({"processing_mode": "vectors_only"} if probe else {}),
            },
        )

    def search(self, query, scope, limit):
        result = self.request(
            "POST",
            "/api/v1/search/find",
            {
                "query": query,
                "target_uri": self.root(scope),
                "context_type": "memory",
                "limit": limit,
                "level": 2,
            },
        )
        hits = []
        for hit in result.get("memories", []):
            uri = hit.get("uri", "")
            match = re.fullmatch(
                re.escape(self.root(scope)) + r"/([a-zA-Z0-9_-]+)_v([0-9]+)\.md", uri
            )
            if match:
                hits.append(
                    {
                        "record_id": match[1],
                        "version": int(match[2]),
                        "chunk": 0,
                        "score": hit.get("score", 0),
                    }
                )
        return hits

    def delete(self, record_id, version=None):
        # List only the current namespace, never trust a caller-supplied native URI.
        tree = self.request(
            "GET",
            "/api/v1/fs/tree",
            params={"uri": "viking://user/owner/memories", "level_limit": 5},
            missing=True,
        )

        def visit(value):
            if isinstance(value, dict):
                uri = value.get("uri", "")
                if re.fullmatch(
                    r"viking://user/owner/memories/harness_[a-f0-9]{64}/"
                    + re.escape(record_id)
                    + r"_v"
                    + (str(version) if version is not None else "[0-9]+")
                    + r"\.md",
                    uri,
                ):
                    self.request(
                        "DELETE",
                        "/api/v1/fs",
                        params={"uri": uri, "wait": "true", "timeout": 170},
                        missing=True,
                    )
                for child in value.values():
                    visit(child)
            elif isinstance(value, list):
                for child in value:
                    visit(child)

        visit(tree)

    def close(self):
        self.keys.close()
        self.http.close()
