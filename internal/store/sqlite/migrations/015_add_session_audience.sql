-- 015_add_session_audience.sql
-- Applied migrations are immutable. Add a new numbered migration for changes.
-- The runner wraps this file in a transaction; do not add BEGIN or COMMIT.
--
-- Host sessions behind the central login (ID-015). audience is the one host
-- a session is valid on; '' is a central session, valid on the login host
-- and the hosts auth.cookie_domain covers (every row before this migration).
-- parent_hash links a host session to the central session that approved
-- it: deleting the parent ends it, and a lookup refuses it once the parent
-- is gone or expired. Explicit deletes keep the cascade in the store's code.

ALTER TABLE sessions ADD COLUMN audience TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN parent_hash BLOB;

CREATE INDEX sessions_parent_hash ON sessions (parent_hash);
