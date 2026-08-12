-- the code graph. three layers over the same set of symbols:
--   structural (symbols, edges, graph_meta) - what the code is and what connects to what
--   temporal   (symbol_history)            - how each symbol changed over time, from git
--   behavioral (agent_interactions)         - what the agent actually needed, per task

-- one row per named thing in the repo: function, method, type, package-level const/var.
-- byte_start/byte_end locate the symbol in the file so its source can be sliced out on
-- demand rather than copied here. body_hash is the symbol's own source text, which is how
-- a reindex tells "this file changed" from "this symbol changed".
CREATE TABLE IF NOT EXISTS symbols (
    id TEXT PRIMARY KEY,
    file_path TEXT NOT NULL,
    name TEXT NOT NULL,
    receiver TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL,
    visibility TEXT NOT NULL,
    byte_start INTEGER NOT NULL,
    byte_end INTEGER NOT NULL,
    body_hash TEXT NOT NULL
);

-- file_path: incremental reindex deletes/refreshes a whole file at a time
-- name: exact lookups ("where is Dispatch defined")
CREATE INDEX IF NOT EXISTS idx_symbols_file ON symbols (file_path);
CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols (name);

-- the graph itself: one row per relationship. kind is calls, imports, type_refs, or implements.
CREATE TABLE IF NOT EXISTS edges (
    from_symbol_id TEXT NOT NULL,
    to_symbol_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    PRIMARY KEY (from_symbol_id, to_symbol_id, kind),
    FOREIGN KEY (from_symbol_id) REFERENCES symbols(id) ON DELETE CASCADE,
    FOREIGN KEY (to_symbol_id) REFERENCES symbols(id) ON DELETE CASCADE
);

-- the primary key already covers lookups by from_symbol_id (callees). walking the graph
-- backwards (callers, impact analysis) searches on to_symbol_id, which it does not cover.
CREATE INDEX IF NOT EXISTS idx_edges_to ON edges (to_symbol_id);

-- one row per indexed file. content_hash is a fingerprint of the whole file: if it matches
-- what is stored here, the file is unchanged and gets skipped without being parsed.
CREATE TABLE IF NOT EXISTS graph_meta (
    file_path TEXT PRIMARY KEY,
    content_hash TEXT NOT NULL,
    indexed_at INTEGER NOT NULL
);

-- keyword search over symbol names. kind and file_path ride along UNINDEXED so they can be
-- read back with a hit without themselves being searchable - otherwise a query like
-- "agent" would match every symbol under internal/agent/ and drown the real matches.
CREATE VIRTUAL TABLE IF NOT EXISTS symbols_fts USING fts5 (
    name,
    kind UNINDEXED,
    file_path UNINDEXED
);

-- symbols_fts is a separate table and does not observe writes to symbols on its own, so
-- these keep the two in step. without them the index silently rots: searches return symbols
-- that were deleted and miss ones that were added.
CREATE TRIGGER IF NOT EXISTS symbols_fts_insert AFTER INSERT ON symbols BEGIN
    INSERT INTO symbols_fts (rowid, name, kind, file_path)
    VALUES (new.rowid, new.name, new.kind, new.file_path);
END;

CREATE TRIGGER IF NOT EXISTS symbols_fts_delete AFTER DELETE ON symbols BEGIN
    DELETE FROM symbols_fts WHERE rowid = old.rowid;
END;

CREATE TRIGGER IF NOT EXISTS symbols_fts_update AFTER UPDATE ON symbols BEGIN
    DELETE FROM symbols_fts WHERE rowid = old.rowid;
    INSERT INTO symbols_fts (rowid, name, kind, file_path)
    VALUES (new.rowid, new.name, new.kind, new.file_path);
END;

-- temporal layer: one row per (symbol, commit) that touched it, derived from git log.
-- cascades on symbol delete because it is fully rebuildable by re-walking history.
CREATE TABLE IF NOT EXISTS symbol_history (
    symbol_id TEXT NOT NULL,
    commit_hash TEXT NOT NULL,
    author TEXT NOT NULL,
    committed_at INTEGER NOT NULL,
    change_type TEXT NOT NULL,
    PRIMARY KEY (symbol_id, commit_hash),
    FOREIGN KEY (symbol_id) REFERENCES symbols(id) ON DELETE CASCADE
);

-- recency and churn queries ("what has moved lately") sort on time across all symbols
CREATE INDEX IF NOT EXISTS idx_symbol_history_time ON symbol_history (committed_at);

-- behavioral layer: one row per time the agent touched a symbol, tagged with the request
-- that prompted it. deliberately has no foreign key to symbols: this is an observation log
-- that cannot be recomputed from anything, so it has to outlive symbol churn. a file rename
-- changes every symbol id in that file, and cascading here would erase the record instead.
-- readers must tolerate a symbol_id with no matching symbols row.
CREATE TABLE IF NOT EXISTS agent_interactions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    symbol_id TEXT NOT NULL,
    interaction_type TEXT NOT NULL,
    task_context TEXT,
    created_at INTEGER NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE INDEX IF NOT EXISTS idx_agent_interactions_symbol ON agent_interactions (symbol_id);
CREATE INDEX IF NOT EXISTS idx_agent_interactions_session ON agent_interactions (session_id);
