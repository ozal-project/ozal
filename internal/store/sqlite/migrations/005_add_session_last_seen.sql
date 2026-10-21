-- 005_add_session_last_seen.sql
-- Applied migrations are immutable. Add a new numbered migration for changes.
-- The runner wraps this file in a transaction; do not add BEGIN or COMMIT.
--
-- Idle timeout for sessions (ID-005). A session ends at the earlier of
-- last_seen_at + idle and created_at + absolute lifetime. last_seen_at is
-- written at most once a minute per session, not on every request. Rows
-- from before this migration start as last seen at their creation.

ALTER TABLE sessions ADD COLUMN last_seen_at INTEGER NOT NULL DEFAULT 0;

UPDATE sessions SET last_seen_at = created_at;
