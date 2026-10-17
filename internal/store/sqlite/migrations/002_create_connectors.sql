-- 002_create_connectors.sql
-- Tunnel connectors enrolled with the edge (TUN-002). A connector is
-- identified by the SHA-256 of its leaf certificate (DER), not by a name it
-- claims: a certificate from our CA that is not enrolled is not authorized.
-- Revocation sets revoked_at; rows are kept so the history stays auditable.
-- Times are Unix seconds.
--
-- Applied migrations are immutable: never edit this file once it has run
-- anywhere. Add a new numbered migration instead (scripts/new-migration.sh).

CREATE TABLE connectors (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 63),
    cert_sha256 BLOB    NOT NULL CHECK (length(cert_sha256) = 32),
    created_at  INTEGER NOT NULL,
    revoked_at  INTEGER
) STRICT;

-- One active connector per name, and one active enrolment per certificate;
-- revoked rows may repeat both so a name can be re-enrolled after revocation.
CREATE UNIQUE INDEX connectors_active_name ON connectors (name) WHERE revoked_at IS NULL;
CREATE UNIQUE INDEX connectors_active_cert ON connectors (cert_sha256) WHERE revoked_at IS NULL;
