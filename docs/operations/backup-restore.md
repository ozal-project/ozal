# Backup and restore

This runbook describes the state owned by Switchboard and a local restore
drill. The `db backup` command creates a consistent SQLite snapshot with
`VACUUM INTO`, which includes committed pages still in the source WAL. It
checks both the source and the completed snapshot, writes with mode `0600`,
and publishes only to a new path. `db restore` performs the same checks,
invalidates sessions by default, and never replaces an existing path. The
explicit `-keep-sessions` option preserves backup sessions and can reactivate
old cookies.

## What must be preserved

The SQLite file is only one part of an installation. Back up each item through
the host's approved backup system, with access limited to the service owner
and operators:

| State | Location | Recovery use |
|---|---|---|
| Database | The path passed with `-db` (for example, `/var/lib/switchboard/state.db`) | Sessions, users and passkey records, connector registry, and static-site deployment metadata. Treat it as sensitive. |
| Configuration | The path passed with `-config` (for example, `/etc/switchboard/config.json`) | Routes, policy, and references to secret sources. The config may also contain inline credentials in older/local setups; protect it as sensitive. |
| Pages content | The configured `pages.dir` | Deployment files are separate from their database metadata. Back up both or retain the original build artifacts so sites can be deployed again. |
| TLS state | `tls.cache_dir` or `tls.dns01.state_dir`, when configured | ACME account material and certificate private keys. Preserve it for continuity, or plan a controlled re-issuance with DNS/provider access. |
| Tunnel identity and origin trust | Files named by `tunnel.key_file` and `tunnel.cert_file`, plus the `tls.origin_client_ca_file` trust bundle and any operator-managed CA private keys | Needed to keep current peers and client-certificate trust working. The database stores connector authorization records, not a replacement for private keys. |
| Secret sources | Files referenced by `file:` and the secret manager/environment supplying `env:` references | Admin and site tokens, OIDC/Telegram/ACME credentials and other resolved secrets. The config contains references only when configured that way; the backup command does not collect these sources. |
| Paired notification state | The configured `notify.telegram.chats_file` | Paired chat IDs. Re-pairing is possible if this file is lost, but alerts stop reaching the previous chats until then. |

The database snapshot contains sessions, user/passkey records, invitations,
and connector authorization only when the database is the active store.
`db restore` clears the copied `sessions` table unless `-keep-sessions` is
given. This invalidates old cookies, but it does not merge later security
changes into the snapshot: an older backup can restore a passkey that was
revoked later, re-enable a user who was disabled later, or restore an unused
invitation or connector authorization. There is no revocation tombstone
history outside the snapshot. Restore from the newest trusted backup and
reconcile identities, invitations, and connector certificates against the
current operator record before returning service. Losing the database can end
sessions and require re-inviting users or re-enrolling connectors. Static
site files, config, TLS material, secret sources, and external CA keys are not
inside the database snapshot. Decide which of those are recoverable from a
build artifact or can be re-issued before relying on regeneration as a backup
plan.

## Local restore drill

Run this against the same binary version and a disposable directory. The
destination must be on a filesystem that supports creating a hard link within
the destination directory; the command uses that atomic no-overwrite publish
step deliberately.

```sh
umask 077
drill=$(mktemp -d)
trap 'rm -rf "$drill"' EXIT

switchboard doctor -config /etc/switchboard/config.json -db /var/lib/switchboard/state.db
switchboard db backup -db /var/lib/switchboard/state.db -out "$drill/state.backup.db"
switchboard db restore -from "$drill/state.backup.db" -to "$drill/state.restored.db"
# Use -keep-sessions only when preserving backup cookies is intentional.
switchboard doctor -config /etc/switchboard/config.json -db "$drill/state.restored.db"
switchboard migrate -db "$drill/state.restored.db" status
```

The repository gate also runs the backup and restore path against a fresh,
migrated SQLite database with a known row, verifies the restored row and
integrity, checks mode `0600`, refuses symlink sources and existing targets,
and confirms an invalid source never publishes a destination. This is a local
file-level drill; it does not prove restoration on a different host, service
account, filesystem, or backup provider.

## Recovering a host

1. Install the same or newer verified Switchboard binary and recreate the
   service account and private directory layout from
   [systemd operations](systemd.md).
2. Restore the config, required secret sources, Pages content, TLS state, and
   tunnel/origin trust material to their intended paths with their original
   owner and restrictive permissions.
3. Keep the service stopped. Restore the database to a new path; never write
   over a database that a running process may have open. Sessions are cleared
   by default. For an older backup, reconcile disabled users, revoked
   passkeys, invitations and connector authorization before starting it.
4. Run `switchboard doctor` with the restored config and database, then inspect
   `switchboard migrate status`. Apply migrations explicitly with
   `switchboard migrate up -db PATH` after retaining a pre-migration backup.
5. Point the service at the restored database and start it. Verify that the
   admin API answers and exercise one route and, when configured, a passkey,
   site, certificate, and connector flow before declaring recovery complete.

Do not infer recovery from a passing integrity check alone: it proves database
consistency, not that the external keys, DNS credentials, Pages files,
connectors, or user authenticators are available. Measure the real recovery
time and the newest usable backup during the host-level drill; this document
does not claim an RPO or RTO yet.
