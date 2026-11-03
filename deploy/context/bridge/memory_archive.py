"""Complete memory archives and replay-safe native-file import bookkeeping."""
import hashlib
import json
import re
from datetime import datetime

from fastapi import HTTPException

MAX_BYTES = 16 * 1024 * 1024
MAX_ENTRIES = 10000


def preview_content(content):
    # Only the final reserved metadata comments are hidden in the preview.
    # Staging and the downloaded archive retain the original file unchanged.
    visible = content
    comments = list(re.finditer(r"<!--\s*MEMORY_FIELDS\b.*?-->", content, re.DOTALL))
    for comment in reversed(comments):
        if visible[comment.end() :].strip():
            break
        visible = visible[: comment.start()].rstrip()
    return visible


def canonical(value):
    try:
        return json.dumps(
            value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False
        ).encode("utf-8")
    except (TypeError, ValueError, UnicodeError):
        raise HTTPException(422, "Invalid memory archive") from None


def digest(value):
    return hashlib.sha256(canonical(value)).hexdigest()


def content_hash(content):
    return None if content is None else hashlib.sha256(content.encode("utf-8")).hexdigest()


def timestamp(value):
    if not isinstance(value, str) or len(value) > 64:
        raise HTTPException(422, "Invalid archive timestamp")
    try:
        if datetime.fromisoformat(value.replace("Z", "+00:00")).tzinfo is None:
            raise ValueError()
    except ValueError:
        raise HTTPException(422, "Invalid archive timestamp") from None


def check_size(value):
    if len(canonical(value)) > MAX_BYTES:
        raise HTTPException(413, "Memory archive exceeds 16 MiB")


def native_entry(value, valid_uri):
    if not isinstance(value, dict) or set(value) not in (
        {"uri", "content"},
        {"uri", "content", "updated_at"},
    ):
        raise HTTPException(422, "Invalid automatic memory entry")
    uri = value["uri"]
    if not valid_uri(uri) or not isinstance(value["content"], str) or "\x00" in value["content"]:
        raise HTTPException(422, "Invalid automatic memory entry")
    if "updated_at" in value:
        timestamp(value["updated_at"])
    canonical(value)
    return dict(value)


def normalize(package, backend, inp, validate_manual, valid_uri):
    check_size(package)
    fields = {"schema", "backend", "exported_at", "entries", "automatic_entries", "sha256"}
    if (
        not isinstance(package, dict)
        or set(package) != fields
        or package["schema"] != "hc-memory-v2"
        or package["backend"] != "openviking"
        or backend != "openviking"
    ):
        raise HTTPException(422, "Invalid memory archive format")
    timestamp(package["exported_at"])
    manual, automatic = package["entries"], package["automatic_entries"]
    if not isinstance(manual, list) or not isinstance(automatic, list):
        raise HTTPException(422, "Invalid memory archive entries")
    if len(manual) + len(automatic) > MAX_ENTRIES:
        raise HTTPException(413, "Memory archive exceeds 10000 entries")
    if (
        not isinstance(package["sha256"], str)
        or not re.fullmatch(r"[a-f0-9]{64}", package["sha256"])
        or digest({"entries": manual, "automatic_entries": automatic}) != package["sha256"]
    ):
        raise HTTPException(422, "Memory archive checksum mismatch")
    required = {
        "id",
        "title",
        "content",
        "type",
        "source",
        "scope",
        "created_at",
        "updated_at",
        "version",
    }
    for entry in manual:
        if (
            not isinstance(entry, dict)
            or not required.issubset(entry)
            or set(entry) - required - {"graph"}
            or any(not isinstance(entry[key], str) for key in required - {"version"})
            or not entry["id"]
            or type(entry["version"]) is not int
            or entry["version"] < 1
            or not re.fullmatch(r"personal|workspace:[a-f0-9-]{36}", entry["scope"])
        ):
            raise HTTPException(422, "Invalid manual memory entry")
    entries = [validate_manual(entry, inp) for entry in manual]
    automatic_entries = [native_entry(entry, valid_uri) for entry in automatic]
    if len({entry["id"] for entry in entries}) != len(entries) or len(
        {entry["uri"] for entry in automatic_entries}
    ) != len(automatic_entries):
        raise HTTPException(422, "Duplicate memory archive entry")
    return entries, automatic_entries


def export(entries, target, exported_at):
    automatic = target.session_export()
    if len(entries) + len(automatic) > MAX_ENTRIES:
        raise HTTPException(413, "Memory archive exceeds 10000 entries")
    automatic = [native_entry(entry, target.session_uri) for entry in automatic]
    if len({entry["id"] for entry in entries}) != len(entries) or len(
        {entry["uri"] for entry in automatic}
    ) != len(automatic):
        raise HTTPException(422, "Duplicate memory archive entry")
    value = {
        "schema": "hc-memory-v2",
        "backend": "openviking",
        "exported_at": exported_at,
        "entries": entries,
        "automatic_entries": automatic,
        "sha256": digest({"entries": entries, "automatic_entries": automatic}),
    }
    check_size(value)
    return value


def snapshot(entries, target):
    return {
        entry["uri"]: content_hash(target.session_read_optional(entry["uri"])) for entry in entries
    }


def progress(db, backend, inp):
    rows = db.execute(
        "SELECT uri,state,content_hash,applied FROM archive_progress WHERE backend=? AND namespace=? AND operation_id=?",
        (backend, inp.namespace, inp.idempotency_key),
    ).fetchall()
    return {
        row["uri"]: {
            "state": row["state"],
            "hash": row["content_hash"],
            "applied": bool(row["applied"]),
        }
        for row in rows
    }


def persist(db, backend, inp, uri, value):
    db.execute(
        "INSERT OR REPLACE INTO archive_progress VALUES(?,?,?,?,?,?,?)",
        (
            backend,
            inp.namespace,
            inp.idempotency_key,
            uri,
            value["state"],
            value["hash"],
            bool(value.get("applied")),
        ),
    )
    # Store one bounded marker instead of rewriting the entire archive for each
    # file. The existing operation receipt separately retains its full staging.
    db.commit()


def validate_current(staged, target, completed):
    for entry in staged.get("automatic_entries", []):
        uri = entry["uri"]
        current = content_hash(target.session_read_optional(uri))
        wanted = content_hash(entry["content"])
        previous = completed.get(uri)
        if previous and previous["state"] == "done":
            if current != previous["hash"]:
                raise HTTPException(409, "Automatic memory changed after import")
            continue
        if current == wanted:
            continue
        if current != staged["automatic_expected"][uri]:
            raise HTTPException(409, "Automatic memory changed after preview")


def apply(staged, target, db, backend, inp):
    completed = progress(db, backend, inp)
    applied, skipped = [], []
    for entry in staged.get("automatic_entries", []):
        uri = entry["uri"]
        wanted = content_hash(entry["content"])
        current = content_hash(target.session_read_optional(uri))
        previous = completed.get(uri)
        if previous and previous["state"] == "done":
            if current != previous["hash"]:
                raise HTTPException(409, "Automatic memory changed during import")
            (applied if previous["applied"] else skipped).append(uri)
            continue
        if current == wanted:
            if previous and previous["state"] == "intent":
                # A prior attempt may have stored the raw file before vector
                # rebuilding failed. Equal text alone does not confirm indexing;
                # repeat the idempotent upsert and wait for its confirmation.
                target.session_put(uri, entry["content"])
                if content_hash(target.session_read_optional(uri)) != wanted:
                    raise HTTPException(409, "Automatic memory write could not be confirmed")
                applied.append(uri)
            else:
                skipped.append(uri)
            completed[uri] = {"state": "done", "hash": wanted, "applied": uri in applied}
            persist(db, backend, inp, uri, completed[uri])
            continue
        if current != staged["automatic_expected"][uri]:
            raise HTTPException(409, "Automatic memory changed during import")
        if current is not None and inp.mode == "skip":
            skipped.append(uri)
            completed[uri] = {"state": "done", "hash": current, "applied": False}
            persist(db, backend, inp, uri, completed[uri])
            continue
        completed[uri] = {"state": "intent", "hash": wanted}
        persist(db, backend, inp, uri, completed[uri])
        # Native calls share this tenant lock with the official plugin channel.
        # The upstream API has no conditional-write option; recheck immediately
        # before sending and verify the exact text afterwards.
        if content_hash(target.session_read_optional(uri)) != current:
            raise HTTPException(409, "Automatic memory changed during import")
        target.session_put(uri, entry["content"])
        if content_hash(target.session_read_optional(uri)) != wanted:
            raise HTTPException(409, "Automatic memory write could not be confirmed")
        applied.append(uri)
        completed[uri] = {"state": "done", "hash": wanted, "applied": True}
        persist(db, backend, inp, uri, completed[uri])
    return applied, skipped
