-- Connector expiry is descriptive metadata; TLS verification and registry
-- authorization remain authoritative. NULL means it was not recorded.
ALTER TABLE connectors ADD COLUMN cert_not_after INTEGER;
CREATE INDEX sessions_subject ON sessions (subject);
-- OIDC subjects now include their verified issuer. There is no safe mapping
-- from legacy raw subjects, so existing external sessions must sign in again.
-- Native users, credentials and sessions are preserved.
DELETE FROM sessions WHERE amr = 'oidc';
