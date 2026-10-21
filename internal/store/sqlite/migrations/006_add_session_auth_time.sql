-- 006_add_session_auth_time.sql
-- Applied migrations are immutable. Add a new numbered migration for changes.
-- The runner wraps this file in a transaction; do not add BEGIN or COMMIT.
--
-- Step-up (ID-005): when the session's user last proved their identity
-- (auth_time) and how (amr: "passkey" or "oidc"). A route with
-- max_auth_age_minutes asks for a fresh login once auth_time is older; the
-- new login replaces the session, so auth_time is never moved forward in
-- place. Rows from before this migration count as authenticated at their
-- creation, by an unknown method.

ALTER TABLE sessions ADD COLUMN auth_time INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN amr TEXT NOT NULL DEFAULT '';

UPDATE sessions SET auth_time = created_at;
