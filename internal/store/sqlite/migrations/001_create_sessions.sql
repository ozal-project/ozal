-- 001_create_sessions.sql
-- Server-side login sessions (ARCH-004). The cookie carries a random ID; only
-- its SHA-256 is stored, so a leaked database file cannot be replayed as
-- session cookies. Times are Unix seconds.
--
-- Applied migrations are immutable: never edit this file once it has run
-- anywhere. Add a new numbered migration instead (scripts/new-migration.sh).

CREATE TABLE sessions (
    id_hash        BLOB    PRIMARY KEY,
    subject        TEXT    NOT NULL,
    email          TEXT    NOT NULL DEFAULT '',
    email_verified INTEGER NOT NULL DEFAULT 0 CHECK (email_verified IN (0, 1)),
    groups_json    TEXT    NOT NULL DEFAULT '[]',
    created_at     INTEGER NOT NULL,
    expires_at     INTEGER NOT NULL
) STRICT;

CREATE INDEX sessions_expires_at ON sessions (expires_at);
