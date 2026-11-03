-- 016_passkey_label_and_session_client.sql
-- Applied migrations are immutable. Add a new numbered migration for changes.
-- The runner wraps this file in a transaction; do not add BEGIN or COMMIT.
--
-- A visitor tells their passkeys and sessions apart. label names a passkey:
-- where it was made ("Chrome on macOS") until its owner renames it. client
-- is the browser a login came from, and passkey_id the credential that
-- signed it, so the page can say "used for this sign-in". All three are
-- hints for the owner, never authorization inputs; older rows have none.

ALTER TABLE passkeys ADD COLUMN label TEXT CHECK (label IS NULL OR length(label) BETWEEN 1 AND 60);
ALTER TABLE sessions ADD COLUMN client TEXT CHECK (client IS NULL OR length(client) BETWEEN 1 AND 60);
ALTER TABLE sessions ADD COLUMN passkey_id BLOB CHECK (passkey_id IS NULL OR length(passkey_id) BETWEEN 16 AND 1023);
