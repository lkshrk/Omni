CREATE TABLE IF NOT EXISTS tool_cache (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT    NOT NULL,
    provider     TEXT    NOT NULL,
    package      TEXT    NOT NULL,
    installed    INTEGER NOT NULL DEFAULT 0,   -- bool: 0=false 1=true
    version      TEXT,
    last_checked INTEGER NOT NULL DEFAULT 0,   -- unix timestamp
    UNIQUE(name, provider, package)
);

CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY
);
