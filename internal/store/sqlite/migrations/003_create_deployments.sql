-- 003_create_deployments.sql
-- Static-site deployments (PAGES-002). A deployment is identified by the
-- SHA-256 of its manifest, so the same build always has the same id; its
-- files live in <pages.dir>/sites/<site>/<id>/ and never change. site_live
-- names the deployment each site serves: a deploy or rollback is one update
-- of that row. Times are Unix seconds.
--
-- Applied migrations are immutable: never edit this file once it has run
-- anywhere. Add a new numbered migration instead (scripts/new-migration.sh).

CREATE TABLE deployments (
    site        TEXT    NOT NULL CHECK (length(site) BETWEEN 1 AND 63),
    id          TEXT    NOT NULL CHECK (length(id) = 64),
    created_at  INTEGER NOT NULL,
    files       INTEGER NOT NULL CHECK (files >= 1),
    bytes       INTEGER NOT NULL CHECK (bytes >= 0),
    PRIMARY KEY (site, id)
) STRICT;

CREATE TABLE site_live (
    site          TEXT    PRIMARY KEY,
    deployment_id TEXT    NOT NULL,
    changed_at    INTEGER NOT NULL,
    FOREIGN KEY (site, deployment_id) REFERENCES deployments (site, id)
) STRICT;
