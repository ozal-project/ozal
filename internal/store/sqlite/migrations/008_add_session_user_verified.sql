-- 008_add_session_user_verified.sql
-- CTX-001: preserve WebAuthn user-verification evidence with the session.
-- NULL means the method or an older session did not record the signal.
-- Applied migrations are immutable; add a new numbered migration for changes.

ALTER TABLE sessions ADD COLUMN user_verified INTEGER CHECK (user_verified IN (0, 1));
