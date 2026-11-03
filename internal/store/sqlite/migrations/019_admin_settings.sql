-- 019_admin_settings.sql
-- Applied migrations are immutable. Add a new numbered migration for changes.
-- The runner wraps this file in a transaction; do not add BEGIN or COMMIT.
--
-- Switches an administrator turns on or off from the console. Today it holds
-- one: whether the sign-in page lets a person ask for administrator access.
-- A missing row means off, so a fresh or restored database never opens the
-- request form by itself.

CREATE TABLE admin_settings (
    name  TEXT PRIMARY KEY CHECK (length(name) BETWEEN 1 AND 64),
    value TEXT NOT NULL CHECK (length(value) <= 256)
) STRICT;
