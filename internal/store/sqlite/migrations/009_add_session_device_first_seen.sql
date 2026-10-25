-- 009_add_session_device_first_seen.sql
-- CTX-003: record whether a newly added passkey was first seen for its user.
-- Existing credentials predate this evidence and are not labelled new.
-- Existing sessions keep an unknown value.
-- Applied migrations are immutable; add a new numbered migration for changes.

ALTER TABLE passkeys ADD COLUMN first_seen_pending INTEGER NOT NULL DEFAULT 0 CHECK (first_seen_pending IN (0, 1));
ALTER TABLE sessions ADD COLUMN device_first_seen INTEGER CHECK (device_first_seen IN (0, 1));
