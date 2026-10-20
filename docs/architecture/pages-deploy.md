# Pages deployments (`PAGES-002`)

Status: implemented 2026-10-20 (slices 1–3 and the console Sites page); production adoption is `PAGES-006`.

## Problem

A static route serves a fixed directory (`"root"`). Publishing a new build is
done outside Switchboard (server-ops `publish-site.py`: SSH, a build
directory and a `current` symlink), so Switchboard cannot tell which build is
live, show it in the console, or roll it back. This task makes deployments a
Switchboard operation.

## Threat model

The upload is an archive written to disk by the proxy that serves the
internet. The attacks are the classic archive ones:

- **Path traversal ("zip slip"):** an entry named `../../etc/cron.d/x` or
  `/etc/passwd` writes outside the site.
- **Links:** a symlink entry (`assets -> /`) followed by a file under it
  (`assets/etc/x`) escapes without any `..`; a hard link aliases a file the
  site should not expose.
- **Bombs:** a small compressed archive that expands to gigabytes, or a
  million tiny files, fills the disk or exhausts inodes. Header sizes are
  claims; only bytes actually copied count.
- **Odd types:** devices, FIFOs and similar entries have no place in a site.
- **Half-written sites:** a request must never see a deployment mid-copy.

The admin listener stays loopback-only and deployments need the admin token,
so no new public surface is added.

## Design

**Core (pure, `internal/pages`):**

- `CheckEntry(name, kind, size, limits) EntryDecision` judges one archive
  entry: allowed (with its cleaned relative path) or refused with a stable
  code (`entry.unsafe_path`, `entry.link`, `entry.type`, `entry.too_large`,
  `entry.too_deep`). No I/O. Table tests and a fuzz target.
- `ManifestHash(files)`: the deployment ID is the SHA-256 of the sorted
  `path NUL sha256` list, so the same site gives the same ID whatever the tar
  order or timestamps.

**Shell:**

- `Extract` reads a `tar.gz` stream, asks `CheckEntry` about every header,
  counts files and real bytes against the limits, and writes through an
  `os.Root` opened on a staging directory, so even a bug in the path check
  cannot leave that directory.
- Layout under `pages.dir`: `sites/<name>/<id>/` holds immutable
  deployments; staging happens in `sites/<name>/.staging-*` and becomes
  `<id>` with one rename. A second upload of the same content reuses `<id>`.
- **Live pointer without symlinks:** SQLite (`003_create_deployments`)
  records every deployment and which one is live; the static handler asks a
  `pages.Live` registry (an `atomic.Pointer`) for the directory per request.
  A deploy or rollback is one database transaction plus one pointer swap;
  restart reloads it from the database. Symlinks are avoided because Windows
  is a supported platform and swapping one is not atomic everywhere.
- A route opts in with `"site": "<name>"` instead of `"root"`.
- Admin API (loopback, bearer token): `POST /_/api/sites/{name}/deployments`
  (archive body, size-capped), `GET /_/api/sites`, `POST
  /_/api/sites/{name}/rollback`. Each emits `pages.deployed` /
  `pages.rolled_back` / `pages.refused` to the activity feed.
- CLI: `switchboard deploy -site NAME ./dist` (packs the directory, refusing
  symlinks in the source) and `switchboard rollback -site NAME -to ID`.
- Retention: the last N deployments (default 5) plus the live one; older
  directories are removed after a successful deploy.
- Console: a **Sites** page with the live ID, history and rollback.

## Limits (defaults)

| Limit | Default |
|---|---|
| Compressed upload | 100 MiB |
| Extracted bytes | 500 MiB |
| One file | 100 MiB |
| Entries | 20 000 |
| Path depth | 32 components |

## Slices

1. `internal/pages`: `CheckEntry` (maintainer), `Extract`, manifest hash,
   filesystem layout. Tests with malicious archives.
2. SQLite deployments + `Live` registry + `"site"` routes.
3. Admin API, CLI, events.
4. Console Sites page; `publish-site.py` switches to `switchboard deploy`.

Non-goals here: `_headers`/`_redirects` (`PAGES-003`), Git builds
(`PAGES-004`), previews (`PAGES-005`).
