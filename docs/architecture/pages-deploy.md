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
  `pages.rolled_back` / `pages.refused` to the activity feed; an internal
  deployment failure emits `pages.failed` with a stable generic code and no
  raw filesystem error.
- CLI: `switchboard site deploy -site NAME ./dist` (packs the directory,
  refusing symlinks in the source), `switchboard site list`, and
  `switchboard site rollback -site NAME -to ID`.
- Retention: `pages.keep` is the total retained deployments per site
  (default 5), including the live one; older directories are removed after a
  successful deploy.
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

1. `internal/pages`: `CheckEntry`, `Extract`, manifest hash,
   filesystem layout. Tests with malicious archives.
2. SQLite deployments + `Live` registry + `"site"` routes.
3. Admin API, CLI, events.
4. Console Sites page. The CLI and control-plane integration are implemented;
   deployment automation is documented with the `switchboard site` commands.

Non-goals here: Git builds (`PAGES-004`), previews (`PAGES-005`).

## Rules files (`PAGES-003`, 2026-10-20)

`_redirects` and `_headers` at a deployment's root are parsed by the pure
`pages.ParseRules` (table tests and `FuzzRules`). They are validated during
`Deploy` (refusal `rules.invalid` with file and line) and reloaded from disk
by `NewStore` and `Rollback`; a deployment whose rules no longer parse fails
the start rather than serving without its headers. The parsed `*Rules`
travels in `pages.Live` with the directory, so one atomic pointer swap
changes files and rules together. The static handler answers a matching
redirect before looking at files and adds matching headers to files it
serves; `nosniff` stays a baseline a site cannot change.

Security choices: `Set-Cookie` is refused because every site shares the
parent domain with Switchboard's own session cookie; placeholders may not
reach a destination's host; a relative destination that becomes `//` or
`/\` after substitution is not followed (fuzzed); control characters in
the substituted value are refused. Limits: 256 KiB per file, 1000-byte
lines, 2100 redirects (100 with placeholders), 100 header rules.
