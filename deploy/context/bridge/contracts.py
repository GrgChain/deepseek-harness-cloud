"""Validated request models shared by the context companion entry points."""

from pydantic import BaseModel, ConfigDict, Field

from viking_sessions import SessionRequest


class MilvusService(BaseModel):
    model_config = ConfigDict(extra="forbid")
    url: str = Field(min_length=1, max_length=1000)
    token: str = Field(default="", max_length=4096, repr=False)


class EmbeddingConfig(BaseModel):
    model_config = ConfigDict(extra="forbid")
    provider: str
    model: str = Field(min_length=1, max_length=256)
    dimensions: int = Field(ge=1, le=32768)
    api_base: str = Field(default="", max_length=1000)
    api_key: str = Field(default="", max_length=4096, repr=False)


class Input(BaseModel):
    model_config = ConfigDict(extra="forbid")
    contract: str
    namespace: str = Field(pattern=r"^[a-f0-9]{64}$")
    session_namespace: str | None = Field(default=None, pattern=r"^[a-f0-9]{64}$", repr=False)
    kind: str
    session_request: SessionRequest | None = None
    expected_version: int = 1
    idempotency_key: str = ""
    scope: str = Field(default="personal", pattern=r"^(personal|workspace:[a-f0-9-]{36})$")
    query: str = Field(default="", max_length=4000)
    search_mode: str = ""
    min_similarity: float | None = Field(default=None, ge=-1, le=1, allow_inf_nan=False)
    memory_source: str = Field(default="", pattern=r"^(automatic|manual)?$")
    page: int = Field(default=0, ge=0, le=1000000)
    page_size: int = Field(default=0, ge=0, le=100)
    limit: int = Field(default=5, ge=1, le=20)
    record_id: str = Field(default="", max_length=2048)
    record_version: int = 0
    content: str = Field(default="", max_length=262144)
    title: str = Field(default="", max_length=200)
    type: str = Field(default="", max_length=100)
    source: str = Field(default="", max_length=1000)
    token: str = Field(default="", max_length=128)
    mode: str = ""
    package: dict | None = None
    confirmed: bool = False
    accept_loss: bool = False
    service: MilvusService | None = Field(default=None, repr=False)
    embedding: EmbeddingConfig | None = Field(default=None, repr=False)
