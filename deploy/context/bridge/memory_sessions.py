"""Session archive dispatch and legacy Mem0 record compatibility."""
import hashlib
import json
from datetime import datetime, timezone
from typing import Literal

from fastapi import HTTPException
from pydantic import BaseModel, ConfigDict, Field


class Message(BaseModel):
    model_config = ConfigDict(extra="forbid")
    id: str = Field(pattern=r"^[a-f0-9]{64}$")
    role: Literal["user", "assistant", "tool"]
    content: str = Field(min_length=1, max_length=240 * 1024)


class Batch(BaseModel):
    model_config = ConfigDict(extra="forbid")
    session_id: str = Field(pattern=r"^[a-f0-9]{64}$")
    messages: list[Message] = Field(min_length=1, max_length=32)


def append(db, backend, inp, target):
    from session_archive import execute

    return execute(db, backend, inp, target)


def legacy_mem0_messages(db, namespace):
    """Recognize only records written by the former per-message session writer."""
    rows = db.execute(
        "SELECT * FROM records WHERE backend='mem0' AND namespace=? AND scope='personal' "
        "AND json_extract(payload,'$.type')='conversation' "
        "AND json_extract(payload,'$.source')='DSH 自动同步' "
        "ORDER BY json_extract(payload,'$.created_at'),id",
        (namespace,),
    ).fetchall()
    result = []
    for row in rows:
        try:
            content = json.loads(json.loads(row["payload"])["content"])
            session_id, message_id = content["session_id"], content["message_id"]
            Message.model_validate(
                {"id": message_id, "role": content["role"], "content": content["content"]}
            )
            expected = hashlib.sha256(
                (namespace + ":dsh-session:" + session_id + ":" + message_id).encode()
            ).hexdigest()
        except (ValueError, KeyError, TypeError):
            continue
        if row["id"] == expected:
            result.append((row, content))
    return result


def append_mem0_session(db, namespace, session_id, messages, target):
    identity = hashlib.sha256((namespace + ":dsh-session:" + session_id).encode()).hexdigest()
    operation = "dsh-session-" + session_id
    current = db.execute(
        "SELECT * FROM records WHERE backend='mem0' AND namespace=? AND id=?",
        (namespace, identity),
    ).fetchone()
    # An explicit delete or edit owns this record, including subsequent replays.
    if current and (
        current["status"] in {"DELETED", "PENDING_DELETE"} or current["operation_id"] != operation
    ):
        return
    previous = json.loads(current["payload"]) if current else None
    merged = (
        {row["message_id"]: row for row in json.loads(previous["content"])["messages"]}
        if previous
        else {}
    )
    legacy = [
        (row, content)
        for row, content in legacy_mem0_messages(db, namespace)
        if content["session_id"] == session_id
    ]
    suppressed = set()
    retiring = []
    for row, content in legacy:
        if row["status"] in {"DELETED", "PENDING_DELETE"}:
            suppressed.add(content["message_id"])
            continue
        merged.setdefault(
            content["message_id"], {key: content[key] for key in ("message_id", "role", "content")}
        )
        retiring.append(row)
    for message in messages:
        if message.id not in suppressed:
            merged.setdefault(
                message.id,
                {"message_id": message.id, "role": message.role, "content": message.content},
            )
    if not merged:
        return
    content = json.dumps(
        {"session_id": session_id, "messages": list(merged.values())}, ensure_ascii=False
    )
    # Keep the existing memory read/export/REST limit. Never truncate a conversation.
    if len(content.encode()) > 262144:
        raise HTTPException(413, "Session memory exceeds 256 KiB")
    changed = not previous or previous["content"] != content
    if current and current["status"] == "READY" and not changed and not retiring:
        return
    stamp = datetime.now(timezone.utc).isoformat()
    first = next(
        (row for row in merged.values() if row["role"] == "user"), next(iter(merged.values()))
    )
    record = {
        "id": identity,
        "scope": "personal",
        "version": current["version"] + int(changed) if current else 1,
        "title": previous["title"]
        if previous
        else "会话记录 · " + first["content"][:60].replace("\n", " "),
        "content": content,
        "type": "conversation",
        "source": "DSH 自动同步",
        "created_at": previous["created_at"]
        if previous
        else min((json.loads(row["payload"])["created_at"] for row in retiring), default=stamp),
        "updated_at": stamp if changed else previous["updated_at"],
    }
    if current and current["status"] == "READY" and changed:
        db.execute(
            "INSERT OR IGNORE INTO revisions VALUES('mem0',?,?,?,?,?,?)",
            (
                namespace,
                identity,
                current["version"],
                "personal",
                current["payload"],
                current["chunks"],
            ),
        )
    db.execute(
        "INSERT OR REPLACE INTO records VALUES('mem0',?,?, 'personal',?,'INDEXING',?,?,?)",
        (
            namespace,
            identity,
            record["version"],
            json.dumps(record, ensure_ascii=False),
            json.dumps([content], ensure_ascii=False),
            operation,
        ),
    )
    db.commit()
    try:
        # Reconcile unknown writes and replace all old native versions, so there
        # is also only one current session record in the Mem0 service itself.
        target.delete(identity)
        target.put(record, [content])
        for row in retiring:
            target.delete(row["id"])
    except Exception:
        db.execute(
            "UPDATE records SET status='ERROR' WHERE backend='mem0' AND namespace=? AND id=?",
            (namespace, identity),
        )
        db.commit()
        raise
    # Retain legacy payloads as tombstones; only retire them after remote success.
    for row in retiring:
        db.execute(
            "UPDATE records SET status='DELETED' WHERE backend='mem0' AND namespace=? AND id=?",
            (namespace, row["id"]),
        )
    db.execute(
        "UPDATE records SET status='READY' WHERE backend='mem0' AND namespace=? AND id=?",
        (namespace, identity),
    )
    db.commit()


def consolidate_mem0_sessions(db, namespace, target):
    """Upgrade existing sessions using the connection's already authorized target."""
    sessions = dict.fromkeys(
        content["session_id"]
        for row, content in legacy_mem0_messages(db, namespace)
        if row["status"] not in {"DELETED", "PENDING_DELETE"}
    )
    for session_id in sessions:
        append_mem0_session(db, namespace, session_id, [], target)
    return len(sessions)
