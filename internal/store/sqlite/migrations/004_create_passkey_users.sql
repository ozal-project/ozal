-- 004_create_passkey_users.sql
-- Native login with passkeys (ID-003). Nothing here logs anyone in on its
-- own: passkeys keep only the public key, and invitations only the SHA-256
-- of the token printed to the operator. Users are created by the operator
-- (`switchboard user invite`); there is no self sign-up. Revocation and
-- disabling set a timestamp so the history stays auditable. Times are Unix
-- seconds.
--
-- Applied migrations are immutable: never edit this file once it has run
-- anywhere. Add a new numbered migration instead (scripts/new-migration.sh).

CREATE TABLE users (
    id          TEXT    PRIMARY KEY CHECK (length(id) = 32),
    -- The WebAuthn user handle: random, never shown, the key a discoverable
    -- login resolves to.
    handle      BLOB    NOT NULL UNIQUE CHECK (length(handle) = 32),
    email       TEXT    NOT NULL UNIQUE CHECK (length(email) BETWEEN 3 AND 254),
    name        TEXT    NOT NULL DEFAULT '',
    groups_json TEXT    NOT NULL DEFAULT '[]',
    created_at  INTEGER NOT NULL,
    disabled_at INTEGER
) STRICT;

CREATE TABLE passkeys (
    credential_id   BLOB    PRIMARY KEY CHECK (length(credential_id) BETWEEN 16 AND 1023),
    user_id         TEXT    NOT NULL REFERENCES users (id),
    -- The go-webauthn credential record as JSON: public key, sign count,
    -- flags, AAGUID, transports. The columns below repeat what the CLI
    -- and the policy read, so they need no JSON parsing.
    credential_json TEXT    NOT NULL,
    backup_eligible INTEGER NOT NULL CHECK (backup_eligible IN (0, 1)),
    created_at      INTEGER NOT NULL,
    last_used_at    INTEGER,
    revoked_at      INTEGER
) STRICT;

CREATE INDEX passkeys_user ON passkeys (user_id);

CREATE TABLE invitations (
    token_hash BLOB    PRIMARY KEY CHECK (length(token_hash) = 32),
    user_id    TEXT    NOT NULL REFERENCES users (id),
    purpose    TEXT    NOT NULL CHECK (purpose IN ('enrol', 'recover')),
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER
) STRICT;
