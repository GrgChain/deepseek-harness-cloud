"""Scoped keyword retrieval over the same active document chunks as vector search."""
import json

import jieba
import document_chunks


def terms(text):
    # Hex encoding makes each segmented word a single safe FTS token.
    return ["t" + word.casefold().encode("utf-8").hex()
            for word in jieba.cut_for_search(text) if word.strip() and any(c.isalnum() for c in word)]


def replace(db, backend, namespace, record):
    db.execute("DELETE FROM keyword_chunks WHERE backend=? AND namespace=? AND record_id=?",
               (backend, namespace, record["id"]))
    title = " ".join(terms(json.loads(record["payload"])["title"]))
    db.executemany("INSERT INTO keyword_chunks(backend,namespace,scope,record_id,version,chunk,title_terms,body_terms) VALUES(?,?,?,?,?,?,?,?)",
                   [(backend, namespace, record["scope"], record["id"], record["version"], index, title, " ".join(terms(document_chunks.section(chunk) + " " + document_chunks.text(chunk))))
                    for index, chunk in enumerate(json.loads(record["chunks"]))])


def remove(db, backend, namespace, identity):
    db.execute("DELETE FROM keyword_chunks WHERE backend=? AND namespace=? AND record_id=?", (backend, namespace, identity))


def initialize(db, active, memory):
    db.executescript("""
    CREATE TABLE IF NOT EXISTS keyword_chunks (
      id INTEGER PRIMARY KEY, backend TEXT NOT NULL, namespace TEXT NOT NULL, scope TEXT NOT NULL,
      record_id TEXT NOT NULL, version INTEGER NOT NULL, chunk INTEGER NOT NULL,
      title_terms TEXT NOT NULL, body_terms TEXT NOT NULL);
    CREATE INDEX IF NOT EXISTS keyword_chunks_record ON keyword_chunks(backend,namespace,record_id);
    CREATE VIRTUAL TABLE IF NOT EXISTS keyword_fts USING fts5(title_terms, body_terms, content='keyword_chunks', content_rowid='id');
    CREATE TRIGGER IF NOT EXISTS keyword_chunks_insert AFTER INSERT ON keyword_chunks BEGIN
      INSERT INTO keyword_fts(rowid,title_terms,body_terms) VALUES(new.id,new.title_terms,new.body_terms);
    END;
    CREATE TRIGGER IF NOT EXISTS keyword_chunks_delete AFTER DELETE ON keyword_chunks BEGIN
      INSERT INTO keyword_fts(keyword_fts,rowid,title_terms,body_terms) VALUES('delete',old.id,old.title_terms,old.body_terms);
    END;
    CREATE TABLE IF NOT EXISTS keyword_index_meta (version INTEGER NOT NULL);
    """)
    db.execute("BEGIN IMMEDIATE")
    if db.execute("SELECT 1 FROM keyword_index_meta WHERE version=1").fetchone() is None:
        db.execute("DELETE FROM keyword_chunks")
        for row in db.execute("SELECT * FROM records WHERE status != 'DELETED'").fetchall():
            if row["backend"] not in memory:
                selected = active(db, row)
                if selected is not None:
                    replace(db, row["backend"], row["namespace"], selected)
        db.execute("INSERT INTO keyword_index_meta(version) VALUES(1)")
    db.commit()


def search(db, backend, namespace, scope, query, limit):
    query_terms = list(dict.fromkeys(terms(query)))[:64]
    if not query_terms:
        return []
    expression = " OR ".join(query_terms)
    rows = db.execute("""SELECT c.record_id,c.version,c.chunk
        FROM keyword_fts JOIN keyword_chunks c ON c.id=keyword_fts.rowid
        WHERE keyword_fts MATCH ? AND c.backend=? AND c.namespace=? AND c.scope=?
        ORDER BY bm25(keyword_fts, 3.0, 1.0), c.record_id, c.chunk LIMIT ?""",
        (expression, backend, namespace, scope, limit)).fetchall()
    return [dict(row) for row in rows]
