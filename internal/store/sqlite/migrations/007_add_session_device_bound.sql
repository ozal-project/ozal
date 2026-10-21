-- 007_add_session_device_bound.sql
-- Applied migrations are immutable. Add a new numbered migration for changes.
-- The runner wraps this file in a transaction; do not add BEGIN or COMMIT.
--
-- require_device_bound (ID-005): whether the passkey behind the session's
-- login cannot be synced to other devices (its authenticator did not set the
-- backup-eligible flag: a security key, a device-bound platform key). Rows
-- from before this migration are not device bound.

ALTER TABLE sessions ADD COLUMN device_bound INTEGER NOT NULL DEFAULT 0 CHECK (device_bound IN (0, 1));
