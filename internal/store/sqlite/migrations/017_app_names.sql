-- 017_app_names.sql
-- Applied migrations are immutable. Add a new numbered migration for changes.
-- The runner wraps this file in a transaction; do not add BEGIN or COMMIT.
--
-- An operator names an application ("Private wiki") so the console and its
-- invitations show that name instead of the bare hostname. The name lives
-- here, not in the configuration: earlier releases reject unknown config
-- fields, and a rollback restores this database with the binary. It is a
-- label, never an access input; a route without a row shows its host.

CREATE TABLE app_names (
    host       TEXT    PRIMARY KEY CHECK (length(host) BETWEEN 1 AND 253),
    name       TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
    updated_at INTEGER NOT NULL
) STRICT;
