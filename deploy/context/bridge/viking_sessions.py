"""Narrow native transport for the official DSH plugin; never a general proxy."""
import json
import re
from urllib.parse import urlsplit, unquote

from fastapi import HTTPException
from pydantic import BaseModel, ConfigDict, Field


class SessionRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")
    method: str
    path: str = Field(max_length=4096)
    body: str = Field(default="", max_length=512 * 1024)
    headers: dict[str, str] = Field(default_factory=dict, max_length=4)


READ_TOOLS = {"search", "find", "read", "list", "tree", "grep", "glob"}
TOOLS = READ_TOOLS | {"remember"}
READ_PATHS = {
    "/health",
    "/api/v1/system/status",
    "/api/v1/fs/ls",
    "/api/v1/fs/tree",
    "/api/v1/content/read",
    "/api/v1/content/abstract",
    "/api/v1/content/overview",
    "/api/v1/skills",
}
SEARCH_PATHS = {"/api/v1/search/search", "/api/v1/search/find", "/api/v1/search/recall"}
HEADERS = {"mcp-session-id", "mcp-protocol-version", "accept"}


def validate(request):
    parsed = urlsplit(request.path)
    path = unquote(parsed.path)
    if (
        parsed.scheme
        or parsed.netloc
        or parsed.fragment
        or not path.startswith("/")
        or ".." in path
        or "\\" in path
    ):
        raise HTTPException(403)
    if any(
        key.lower() not in HEADERS or len(value) > 512 for key, value in request.headers.items()
    ):
        raise HTTPException(403)
    try:
        body = json.loads(request.body) if request.body else None
    except ValueError:
        raise HTTPException(422) from None
    if path == "/mcp":
        if request.method == "DELETE":
            return path, body
        if request.method != "POST" or not isinstance(body, dict):
            raise HTTPException(403)
        method = body.get("method")
        if method not in {
            "initialize",
            "notifications/initialized",
            "ping",
            "tools/list",
            "tools/call",
        }:
            raise HTTPException(403)
        if method == "tools/call" and body.get("params", {}).get("name") not in TOOLS:
            raise HTTPException(403)
        return path, body
    if request.method == "GET" and (
        path in READ_PATHS or re.fullmatch(r"/api/v1/sessions/dsh-[a-zA-Z0-9_-]{1,200}", path)
    ):
        return path, body
    if request.method == "POST" and isinstance(body, dict):
        if path in SEARCH_PATHS:
            return path, body
        if path == "/api/v1/sessions" and re.fullmatch(
            r"dsh-[a-zA-Z0-9_-]{1,200}", str(body.get("session_id", ""))
        ):
            return path, body
        if re.fullmatch(r"/api/v1/sessions/dsh-[a-zA-Z0-9_-]{1,200}/(messages|commit)", path):
            return path, body
    raise HTTPException(403)


def relay(target, request):
    path, body = validate(request)
    target.verify()
    headers = {key: value for key, value in request.headers.items() if key.lower() in HEADERS}
    headers["Content-Type"] = "application/json"
    response = target.http.request(
        request.method,
        request.path,
        content=request.body.encode() if request.body else None,
        headers=headers,
        timeout=90,
    )
    if response.status_code >= 400:
        # Upstream diagnostics can contain private headers or configuration.
        error = {"message": "OpenViking request failed"}
        if response.status_code == 409 and path == "/api/v1/sessions" and request.method == "POST":
            try:
                upstream = response.json()
                if (
                    isinstance(upstream, dict)
                    and isinstance(upstream.get("error"), dict)
                    and upstream["error"].get("code") == "ALREADY_EXISTS"
                ):
                    error["code"] = "ALREADY_EXISTS"
            except ValueError:
                pass
        return {
            "status": response.status_code,
            "body": json.dumps({"status": "error", "error": error}),
            "headers": {"content-type": "application/json"},
        }
    if len(response.content) > 512 * 1024:
        raise HTTPException(502)
    text = response.text
    if path == "/mcp" and isinstance(body, dict) and body.get("method") == "tools/list":

        def filtered(payload):
            if isinstance(payload.get("result", {}).get("tools"), list):
                payload["result"]["tools"] = [
                    tool for tool in payload["result"]["tools"] if tool.get("name") in TOOLS
                ]
            return payload

        if "text/event-stream" in response.headers.get("content-type", ""):
            text = "\n".join(
                "data: " + json.dumps(filtered(json.loads(line[5:])))
                if line.startswith("data:") and line[5:].strip()
                else line
                for line in text.split("\n")
            )
        elif text.strip():
            text = json.dumps(filtered(response.json()))
    return {
        "status": response.status_code,
        "body": text,
        "headers": {
            key: value
            for key, value in response.headers.items()
            if key.lower() in {"content-type", "mcp-session-id", "mcp-protocol-version"}
        },
    }
