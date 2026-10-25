-- PAGES-007: attach project and source provenance to deployments created by
-- the future authenticated deploy endpoint. Existing deployments remain
-- valid and have NULL metadata.
-- Applied migrations are immutable; add a new numbered migration for changes.

ALTER TABLE deployments ADD COLUMN project TEXT;
ALTER TABLE deployments ADD COLUMN commit_sha TEXT;
ALTER TABLE deployments ADD COLUMN run_url TEXT;
