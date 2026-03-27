package db

const schemaSQL = `
CREATE TABLE IF NOT EXISTS directories (
    path              TEXT PRIMARY KEY,
    parent_path       TEXT NOT NULL,
    name              TEXT NOT NULL,
    size              INTEGER NOT NULL DEFAULT 0,
    mtime             INTEGER NOT NULL DEFAULT 0,
    shallow           INTEGER NOT NULL DEFAULT 0,
    scanned_at        INTEGER NOT NULL DEFAULT 0,
    pending_deletion  INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_directories_parent ON directories(parent_path);
CREATE INDEX IF NOT EXISTS idx_directories_size ON directories(size DESC);
`
