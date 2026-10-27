-- Administrative identities (UI-007). People who run the console are not
-- visitors: they live apart from `users` and `passkeys`, so an application
-- passkey or group can never open the console. The first administrator is
-- created by a host-issued one-use enrolment (`switchboard console enrol`);
-- recovery is another host-issued enrolment that revokes the old passkeys.
-- Only public keys and the SHA-256 of enrolment tokens are stored. Times are
-- Unix seconds.

CREATE TABLE admin_accounts (
    id          TEXT    PRIMARY KEY CHECK (length(id) = 32),
    handle      BLOB    NOT NULL UNIQUE CHECK (length(handle) = 32),
    email       TEXT    NOT NULL UNIQUE CHECK (length(email) BETWEEN 3 AND 254),
    name        TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    -- The console tools this person may change; the first account has all.
    tools_json  TEXT    NOT NULL,
    created_at  INTEGER NOT NULL,
    disabled_at INTEGER
) STRICT;

CREATE TABLE admin_passkeys (
    credential_id   BLOB    PRIMARY KEY CHECK (length(credential_id) BETWEEN 16 AND 1023),
    account_id      TEXT    NOT NULL REFERENCES admin_accounts (id),
    credential_json TEXT    NOT NULL,
    created_at      INTEGER NOT NULL,
    last_used_at    INTEGER,
    revoked_at      INTEGER
) STRICT;

CREATE INDEX admin_passkeys_account ON admin_passkeys (account_id);

CREATE TABLE admin_enrolments (
    token_hash BLOB    PRIMARY KEY CHECK (length(token_hash) = 32),
    kind       TEXT    NOT NULL CHECK (kind IN ('bootstrap', 'recover', 'invite')),
    -- NULL for the first administrator; the account being recovered otherwise.
    account_id TEXT    REFERENCES admin_accounts (id),
    -- An invitation fixes who may enrol and with which tools; the person only
    -- chooses their own name and creates the passkey.
    email      TEXT,
    name       TEXT,
    tools_json TEXT,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER
) STRICT;
