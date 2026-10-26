"""Internal-only OpenAI-compatible local embedding endpoint for OpenViking."""
import hmac
import struct
import base64
import os
from fastapi import FastAPI, HTTPException, Header
from pydantic import BaseModel, ConfigDict, Field
from vectors import MODEL, embed
app = FastAPI(docs_url=None, redoc_url=None, openapi_url=None)
class Input(BaseModel):
    model_config = ConfigDict(extra='forbid')
    model: str
    input: str | list[str]
    encoding_format: str = 'float'
    dimensions: int | None = None
@app.get('/healthz')
def health():
    return {'status': 'alive'}
@app.post('/v1/embeddings')
def embeddings(inp: Input, authorization: str = Header('')):
    if not hmac.compare_digest(authorization, 'Bearer ' + os.environ['HC_CONTEXT_TEST_KEY']):
        raise HTTPException(401)
    texts = [inp.input] if isinstance(inp.input, str) else inp.input
    if inp.model != MODEL or not 0 < len(texts) <= 64 or sum(len(x.encode()) for x in texts) > 262144 or inp.encoding_format not in ('float','base64') or inp.dimensions not in (None, 512):
        raise HTTPException(422)
    return {'object': 'list', 'model': MODEL, 'data': [{'object': 'embedding', 'index': i, 'embedding': base64.b64encode(struct.pack('<'+'f'*len(v),*v)).decode() if inp.encoding_format=='base64' else v} for i,v in enumerate(embed(texts))], 'usage': {'prompt_tokens': 0, 'total_tokens': 0}}
