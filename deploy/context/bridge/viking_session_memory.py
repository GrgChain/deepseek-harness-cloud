"""Read platform memories through the official plugin's existing search/read API.

Capture remains in the native tenant. The signed broker envelope supplies the
second tenant; tool arguments only narrow paths inside that authorized corpus.
"""
import json
import math
from urllib.parse import parse_qs, urlsplit

from fastapi import HTTPException
from viking_sessions import SEARCH_PATHS, relay, validate


def reads_memory(request):
    path, body = validate(request)
    return path in SEARCH_PATHS or path.startswith("/api/v1/content/") or (
        path == "/mcp" and isinstance(body, dict) and body.get("method") == "tools/call"
        and body.get("params", {}).get("name") in {"find", "search", "read"}
    )


def canonical_uri(uri):
    if isinstance(uri, str) and (uri == "viking://~" or uri.startswith("viking://~/")):
        return "viking://user/owner" + uri[len("viking://~"):]
    return uri


class ManualMemories:
    def __init__(self, target, records):
        self.target = target
        self.root = target.root("personal")
        # Only active revisions enter this map. Deleted, pending and obsolete
        # remote index entries must never become visible through native APIs.
        self.records = {target.uri(record): record for record in records}
        self.versions = {(record["id"], record["version"]): record for record in records}

    def owns(self, uri):
        uri = canonical_uri(uri)
        return isinstance(uri, str) and (uri == self.root or uri.startswith(self.root + "/"))

    def search(self, args, mcp=False):
        query = args.get("query")
        if not isinstance(query, str) or not query.strip() or len(query) > 4000:
            return []
        uri = canonical_uri(args.get("target_uri") or "viking://")
        if not isinstance(uri, str):
            return []
        uri = uri.rstrip("/")
        root = self.root.rstrip("/")
        if uri not in ("viking:", "viking://user") and not (
            root == uri or root.startswith(uri + "/") or uri.startswith(root + "/")
        ):
            return []
        types = args.get("context_type")
        if types and "memory" not in ([types] if isinstance(types, str) else types):
            return []
        levels = args.get("level")
        if levels is not None and 2 not in (levels if isinstance(levels, list) else [levels]):
            return []
        limit = args.get("limit", 10)
        threshold = args.get("min_score", 0.35) if mcp else args.get("score_threshold", 0)
        if threshold is None:
            threshold = 0.35 if mcp else 0
        if (not isinstance(limit, int) or isinstance(limit, bool) or limit < 1
            or not isinstance(threshold, (int, float)) or isinstance(threshold, bool)
            or not math.isfinite(threshold)):
            return []
        limit = min(limit, 100)
        if not self.records or not self.target.has_tenant:
            return []
        excluded = {canonical_uri(value) for value in args.get("exclude_uris", []) or []}
        matches = {}
        for hit in self.target.search(query, "personal", min(100, limit * 5)):
            record = self.versions.get((hit.get("record_id"), hit.get("version")))
            score = hit.get("score")
            if record is None or not isinstance(score, (int, float)) or isinstance(score, bool) or not math.isfinite(score) or score < threshold:
                continue
            path = self.target.uri(record)
            if path in excluded or not (uri in ("viking:", "viking://user") or path == uri or path.startswith(uri + "/")):
                continue
            if path not in matches or score > matches[path]["score"]:
                matches[path] = {"uri": path, "context_type": "memory", "level": 2,
                                 "score": score, "abstract": record["title"]}
        return sorted(matches.values(), key=lambda item: (-item["score"], item["uri"]))[:limit]

    def assemble(self, result, hits, args):
        # 0.5.31 normally uses the context face rather than ranked bucket lists.
        # Append active manual contents within the server's remaining budget.
        stats = result.setdefault("stats", {})
        budget = args.get("max_tokens", stats.get("max_tokens", 1600))
        used = stats.get("used_tokens", 0)
        remaining = max(0, budget - used)
        entries = result.setdefault("entries", [])
        seen = {entry["uri"] for entry in entries}
        blocks = []
        for hit in hits:
            if hit["uri"] in seen:
                continue
            prefix = f'\n\n{hit["abstract"]}\n{hit["uri"]}\n'
            capacity = remaining - len(prefix.encode())
            if capacity <= 0:
                continue
            content = self.records[hit["uri"]]["content"]
            # UTF-8 bytes conservatively bound token usage without a different
            # model tokenizer or any additional inference call.
            text = content.encode()[:capacity].decode("utf-8", errors="ignore")
            block = prefix + text
            cost = len(block.encode())
            remaining -= cost
            used += cost
            detail = "full" if text == content else "overview"
            entries.append({"uri": hit["uri"], "category": "memories", "score": hit["score"],
                            "detail": detail, "text": text})
            blocks.append(block)
            tiers = stats.setdefault("tier_counts", {})
            tiers[detail] = tiers.get(detail, 0) + 1
        if blocks:
            addition = "".join(blocks)
            result["rendered"] = result.get("rendered", "") + addition
            if result.get("digest"):
                result["digest"] += addition
            if stats.get("rewrite") == "no_relevant":
                stats["rewrite"] = "off"
            stats.update(used_tokens=used, returned=len(entries))
        return result


def payloads(response):
    if "text/event-stream" in response["headers"].get("content-type", ""):
        return [json.loads(line[5:]) for line in response["body"].splitlines()
                if line.startswith("data:") and line[5:].strip()]
    return [json.loads(response["body"])]


def transform(response, change):
    if "text/event-stream" in response["headers"].get("content-type", ""):
        response["body"] = "\n".join(
            "data: " + json.dumps(change(json.loads(line[5:])), ensure_ascii=False)
            if line.startswith("data:") and line[5:].strip() else line
            for line in response["body"].split("\n")
        )
    else:
        response["body"] = json.dumps(change(json.loads(response["body"])), ensure_ascii=False)
    return response


def mcp_read(target, request, body, manual):
    args = body.get("params", {}).get("arguments", {})
    uris = args.get("uris")
    uris = [uris] if isinstance(uris, str) else uris
    if not isinstance(uris, list) or not uris or len(uris) > 100 or not all(isinstance(uri, str) for uri in uris) or not any(manual.owns(uri) for uri in uris):
        return None
    parts = []
    for uri in uris:
        if manual.owns(uri):
            record = manual.records.get(canonical_uri(uri))
            text = record["content"] if record else "Memory not found"
        else:
            single = {**body, "params": {**body["params"], "arguments": {"uris": uri}}}
            response = relay(target, request.model_copy(update={"body": json.dumps(single)}))
            if response["status"] >= 400:
                return response
            result = next((item["result"] for item in payloads(response) if "result" in item), {})
            text = "\n".join(item["text"] for item in result.get("content", []) if item.get("type") == "text")
        parts.append(text if len(uris) == 1 else f"=== {uri} ===\n{text}")
    return {"status": 200, "headers": {"content-type": "application/json"},
            "body": json.dumps({"jsonrpc": "2.0", "id": body.get("id"), "result": {
                "content": [{"type": "text", "text": "\n\n".join(parts)}]}}, ensure_ascii=False)}


def relay_shared(target, request, manual):
    path, body = validate(request)
    if path.startswith("/api/v1/content/"):
        uri = parse_qs(urlsplit(request.path).query).get("uri", [""])[0]
        if manual.owns(uri):
            if canonical_uri(uri) not in manual.records:
                return {"status": 404, "headers": {"content-type": "application/json"},
                        "body": '{"status":"error","error":{"message":"Memory not found"}}'}
            return relay(manual.target, request)
    params = body.get("params", {}) if isinstance(body, dict) else {}
    if path == "/mcp" and params.get("name") == "read":
        response = mcp_read(target, request, body, manual)
        if response is not None:
            return bounded(response)
    response = relay(target, request)
    if response["status"] >= 400:
        return response
    if path in SEARCH_PATHS:
        hits = manual.search(body)

        def merge(payload):
            if payload.get("status") == "ok" and isinstance(payload.get("result"), dict):
                result = payload["result"]
                if body.get("mode") == "context":
                    manual.assemble(result, hits, body)
                    return payload
                memories = {hit["uri"]: hit for hit in result.get("memories", [])}
                memories.update({hit["uri"]: hit for hit in hits})
                result["memories"] = sorted(memories.values(), key=lambda hit: (-hit.get("score", 0), hit["uri"]))[:body.get("limit", 10)]
                result["total"] = sum(len(result.get(bucket, [])) for bucket in ("memories", "resources", "skills"))
            return payload

        response = transform(response, merge)
    elif path == "/mcp" and params.get("name") in {"find", "search"}:
        hits = manual.search(params.get("arguments", {}), mcp=True)

        def append(payload):
            result = payload.get("result", {})
            if hits and isinstance(result.get("content"), list) and not result.get("isError"):
                result["content"] = [item for item in result["content"] if item != {
                    "type": "text", "text": "No matching context found."}]
                text = "\n\n".join(f'- [memory {hit["score"] * 100:.0f}%] {hit["uri"]}\n    {hit["abstract"]}' for hit in hits)
                text += "\n\nUse the read tool to expand a URI."
                result["content"].append({"type": "text", "text": text})
            return payload

        response = transform(response, append)
    return bounded(response)


def bounded(response):
    if len(response["body"].encode()) > 512 * 1024:
        raise HTTPException(502)
    return response
