-- 018_groups.sql
-- Applied migrations are immutable. Add a new numbered migration for changes.
-- The runner wraps this file in a transaction; do not add BEGIN or COMMIT.
--
-- Groups an operator created on purpose, before anyone belongs to them. A
-- group also exists through its members (users.groups_json) or an
-- application that requires it; this table only adds the empty ones, so the
-- console offers a fixed set of names to pick instead of free text.

CREATE TABLE groups (
    name       TEXT    PRIMARY KEY CHECK (length(name) BETWEEN 1 AND 128),
    created_at INTEGER NOT NULL
) STRICT;
