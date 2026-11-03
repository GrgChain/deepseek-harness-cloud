"""Local OSS REST test entry point backed by the real Mem0 SDK, infer=False only.

Implements the four OSS routes used by Memories; not the Mem0 hosted API or the
upstream dashboard server. Runs in its own container, collection and data volume.
"""
import os
import secrets
from typing import Literal
from uuid import UUID

from fastapi import APIRouter, Depends, FastAPI, Header, HTTPException, Query
from pydantic import BaseModel, ConfigDict, Field

from memories import memory


def authenticate(x_api_key: str = Header(default="")):
    if not secrets.compare_digest(x_api_key.encode(), os.environ["HC_CONTEXT_TEST_KEY"].encode()):
        raise HTTPException(401, "Invalid API key")


app = FastAPI(
    title="Local Mem0 OSS REST test service", docs_url=None, redoc_url=None, openapi_url=None
)
routes = APIRouter(dependencies=[Depends(authenticate)])


class Message(BaseModel):
    model_config = ConfigDict(extra="forbid")
    role: Literal["user"]
    content: str = Field(min_length=1, max_length=262144)


class MemoryCreate(BaseModel):
    model_config = ConfigDict(extra="forbid")
    messages: list[Message] = Field(min_length=1, max_length=1)
    user_id: str = Field(min_length=1, max_length=256)
    agent_id: str = Field(min_length=1, max_length=256)
    metadata: dict
    infer: Literal[False] = False


class MemorySearch(BaseModel):
    model_config = ConfigDict(extra="forbid")
    query: str = Field(min_length=1, max_length=4000)
    filters: dict
    top_k: int = Field(default=5, ge=1, le=1000)


@app.get("/healthz")
def health():
    memory().get_all(filters={"user_id": "healthcheck"}, top_k=1)
    return {"status": "ready"}


@routes.get("/memories")
def list_memories(
    user_id: str = Query(min_length=1, max_length=256),
    agent_id: str | None = Query(default=None, min_length=1, max_length=256),
    top_k: int = Query(default=1000, ge=1, le=1000),
):
    filters = {"user_id": user_id}
    if agent_id:
        filters["agent_id"] = agent_id
    return memory().get_all(filters=filters, top_k=top_k)


@routes.post("/memories")
def add_memories(body: MemoryCreate):
    return memory().add(
        [message.model_dump() for message in body.messages],
        user_id=body.user_id,
        agent_id=body.agent_id,
        metadata=body.metadata,
        infer=False,
    )


@routes.post("/search")
def search_memories(body: MemorySearch):
    if not isinstance(body.filters.get("user_id"), str) or not body.filters["user_id"]:
        raise HTTPException(422, "user_id filter required")
    return memory().search(body.query, filters=body.filters, top_k=body.top_k)


@routes.delete("/memories/{memory_id}")
def delete_memory(memory_id: UUID):
    return memory().delete(str(memory_id))


app.include_router(routes)
