"""Persist raw sessions separately; only committed facts enter memory search."""
import hashlib
import json
import math
from datetime import datetime, timezone
from typing import Literal

from fastapi import HTTPException
from pydantic import BaseModel, ConfigDict, Field


COMMIT_TOKENS = 20000
MAX_SESSION_BYTES = 16 * 1024 * 1024


class Message(BaseModel):
    model_config = ConfigDict(extra="forbid")
    id: str = Field(pattern=r"^[a-f0-9]{64}$")
    role: Literal["user", "assistant", "tool"]
    content: str = Field(min_length=1, max_length=240 * 1024)


class Messages(BaseModel):
    model_config = ConfigDict(extra="forbid")
    session_id: str = Field(pattern=r"^[a-f0-9]{64}$")
    messages: list[Message] = Field(min_length=1, max_length=32)


class Commit(BaseModel):
    model_config = ConfigDict(extra="forbid")
    session_id: str = Field(pattern=r"^[a-f0-9]{64}$")
    force: bool = False


def initialize(db, backend):
    if backend not in {"mem0", "graphiti"}:
        raise HTTPException(403)
    db.executescript(
        f"""
        CREATE TABLE IF NOT EXISTS {backend}_session_messages (
          seq INTEGER PRIMARY KEY AUTOINCREMENT,
          namespace TEXT NOT NULL, session_id TEXT NOT NULL, message_id TEXT NOT NULL,
          role TEXT NOT NULL, content TEXT NOT NULL, tokens INTEGER NOT NULL,
          UNIQUE(namespace,session_id,message_id));
        CREATE INDEX IF NOT EXISTS {backend}_session_order
          ON {backend}_session_messages(namespace,session_id,seq);
        CREATE TABLE IF NOT EXISTS {backend}_session_commits (
          namespace TEXT NOT NULL, session_id TEXT NOT NULL, id TEXT NOT NULL,
          until_seq INTEGER NOT NULL, status TEXT NOT NULL, facts TEXT,
          PRIMARY KEY(namespace,id));
    """
    )


def pending(db, backend, namespace, session_id):
    cursor = db.execute(
        f"SELECT COALESCE(MAX(until_seq),0) FROM {backend}_session_commits "
        "WHERE namespace=? AND session_id=? AND status='DONE'",
        (namespace, session_id),
    ).fetchone()[0]
    return db.execute(
        f"SELECT * FROM {backend}_session_messages WHERE namespace=? AND session_id=? AND seq>? ORDER BY seq",
        (namespace, session_id, cursor),
    ).fetchall()


def status(db, backend, namespace, session_id):
    rows = pending(db, backend, namespace, session_id)
    return {"pending_tokens": sum(row["tokens"] for row in rows), "pending_messages": len(rows)}


def execute(db, backend, inp, target):
    request = inp.session_request
    if inp.scope != "personal" or request.method != "POST" or request.headers:
        raise HTTPException(403)
    initialize(db, backend)
    try:
        if request.path == "/sessions/messages":
            batch = Messages.model_validate_json(request.body)
            if any(
                not row.content.strip()
                or "\x00" in row.content
                or len(row.content.encode()) > 240 * 1024
                for row in batch.messages
            ):
                raise HTTPException(422)
            existing = db.execute(
                f"SELECT message_id,role,content FROM {backend}_session_messages WHERE namespace=? AND session_id=?",
                (inp.namespace, batch.session_id),
            ).fetchall()
            known = {row["message_id"]: row for row in existing}
            fresh = {}
            for row in batch.messages:
                old = known.get(row.id) or fresh.get(row.id)
                if old and (old["role"] != row.role or old["content"] != row.content):
                    raise HTTPException(409)
                fresh[row.id] = row.model_dump()
            added = [row for identity, row in fresh.items() if identity not in known]
            size = sum(len(row["content"].encode()) for row in existing + added)
            if size > MAX_SESSION_BYTES or len(existing) + len(added) > 10000:
                raise HTTPException(413)
            for row in added:
                db.execute(
                    f"INSERT INTO {backend}_session_messages(namespace,session_id,message_id,role,content,tokens) VALUES(?,?,?,?,?,?)",
                    (
                        inp.namespace,
                        batch.session_id,
                        row["id"],
                        row["role"],
                        row["content"],
                        max(1, math.ceil(len(row["content"].encode()) / 4)),
                    ),
                )
            db.commit()
            # Legacy plugins already flush at turn/end. They can also trigger
            # threshold commits without turning short sessions into memories.
            if (
                status(db, backend, inp.namespace, batch.session_id)["pending_tokens"]
                >= COMMIT_TOKENS
            ):
                commit(db, backend, inp.namespace, batch.session_id, target, False)
            return {
                "accepted": len(batch.messages),
                **status(db, backend, inp.namespace, batch.session_id),
            }
        if request.path in ("/sessions/commit", "/sessions/status"):
            body = Commit.model_validate_json(request.body)
            if request.path == "/sessions/status":
                return status(db, backend, inp.namespace, body.session_id)
            return commit(db, backend, inp.namespace, body.session_id, target, body.force)
    except ValueError:
        raise HTTPException(422) from None
    raise HTTPException(403)


def commit(db, backend, namespace, session_id, target, force):
    rows = pending(db, backend, namespace, session_id)
    in_progress = db.execute(
        f"SELECT * FROM {backend}_session_commits WHERE namespace=? AND session_id=? AND status!='DONE' ORDER BY until_seq LIMIT 1",
        (namespace, session_id),
    ).fetchone()
    if not in_progress:
        if not rows or not force and sum(row["tokens"] for row in rows) < COMMIT_TOKENS:
            return {"committed": False, "memories": 0, **status(db, backend, namespace, session_id)}
        selected, tokens, encoded_bytes = [], 0, 32
        for row in rows:
            size = (
                len(
                    json.dumps(
                        {"role": row["role"], "content": row["content"]}, ensure_ascii=False
                    ).encode()
                )
                + 2
            )
            if selected and encoded_bytes + size > 240 * 1024:
                break
            if encoded_bytes + size > 262144:
                raise HTTPException(413, "Session message exceeds extraction limits")
            selected.append(row)
            encoded_bytes += size
            tokens += row["tokens"]
            if tokens >= COMMIT_TOKENS:
                break
        identity = hashlib.sha256(
            (namespace + ":" + session_id + ":" + str(selected[-1]["seq"])).encode()
        ).hexdigest()
        db.execute(
            f"INSERT INTO {backend}_session_commits VALUES(?,?,?,?,'PENDING',NULL)",
            (namespace, session_id, identity, selected[-1]["seq"]),
        )
        db.commit()
        in_progress = db.execute(
            f"SELECT * FROM {backend}_session_commits WHERE namespace=? AND id=?",
            (namespace, identity),
        ).fetchone()
    identity = in_progress["id"]
    if in_progress["facts"] is None:
        messages = [
            {"role": row["role"], "content": row["content"]}
            for row in rows
            if row["seq"] <= in_progress["until_seq"]
        ]
        facts = target.extract_session(messages, identity)
        if (
            not isinstance(facts, list)
            or len(facts) > 1000
            or any(
                not isinstance(fact, str) or not fact.strip() or len(fact.encode()) > 262144
                for fact in facts
            )
        ):
            raise RuntimeError("Invalid session extraction receipt")
        db.execute(
            f"UPDATE {backend}_session_commits SET status='EXTRACTED',facts=? WHERE namespace=? AND id=?",
            (json.dumps(facts, ensure_ascii=False), namespace, identity),
        )
        db.commit()
    else:
        facts = json.loads(in_progress["facts"])
    for fact in dict.fromkeys(facts):
        record_id = hashlib.sha256(
            (namespace + ":" + backend + "-fact:" + fact).encode()
        ).hexdigest()
        current = db.execute(
            "SELECT * FROM records WHERE backend=? AND namespace=? AND id=?",
            (backend, namespace, record_id),
        ).fetchone()
        # Respect explicit deletion/editing and idempotent replays.
        if current and (
            current["status"] in {"READY", "DELETED", "PENDING_DELETE"}
            or current["operation_id"] != identity
        ):
            continue
        stamp = datetime.now(timezone.utc).isoformat()
        record = {
            "id": record_id,
            "scope": "personal",
            "version": 1,
            "title": fact[:60].replace("\n", " "),
            "content": fact,
            "type": "memory",
            "source": "DSH 自动生成",
            "created_at": stamp,
            "updated_at": stamp,
        }
        db.execute(
            "INSERT OR REPLACE INTO records VALUES(?,?,?,'personal',1,'INDEXING',?,?,?)",
            (
                backend,
                namespace,
                record_id,
                json.dumps(record, ensure_ascii=False),
                json.dumps([fact], ensure_ascii=False),
                identity,
            ),
        )
        db.commit()
        try:
            target.delete(record_id)
            metadata = target.put(record, [fact])
            if metadata and metadata.get("graph"):
                record["graph"] = metadata["graph"]
        except Exception:
            db.execute(
                "UPDATE records SET status='ERROR' WHERE backend=? AND namespace=? AND id=?",
                (backend, namespace, record_id),
            )
            db.commit()
            raise
        db.execute(
            "UPDATE records SET status='READY',payload=? WHERE backend=? AND namespace=? AND id=?",
            (json.dumps(record, ensure_ascii=False), backend, namespace, record_id),
        )
        db.commit()
    db.execute(
        f"UPDATE {backend}_session_commits SET status='DONE' WHERE namespace=? AND id=?",
        (namespace, identity),
    )
    db.commit()
    # Staging is isolated from both the manual and searchable fact namespaces.
    # Its cleanup can be retried without repeating inference or hiding success.
    try:
        target.cleanup_extraction(identity)
    except Exception:
        pass
    return {
        "committed": True,
        "memories": len(set(facts)),
        **status(db, backend, namespace, session_id),
    }
