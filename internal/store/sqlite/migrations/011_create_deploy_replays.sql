-- PAGES-008: one-time GitHub deployment tokens survive process restarts.
-- Only a domain-separated jti digest is retained, never the token or raw jti.
-- Applied migrations are immutable; add a new numbered migration for changes.
CREATE TABLE deploy_replays (
    jti_sha256 TEXT PRIMARY KEY CHECK (length(jti_sha256) = 64),
    expires_at INTEGER NOT NULL
) STRICT;
CREATE INDEX deploy_replays_expiry ON deploy_replays(expires_at);
