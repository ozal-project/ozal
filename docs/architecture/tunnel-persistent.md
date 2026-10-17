# TUN-002: registry-backed local commands

The `switchboard tunnel edge|connect` commands use certificates supplied from
files and an explicitly migrated SQLite connector registry. Startup never
replaces or issues an identity. Both commands are local-only while the exposure
gates remain open; the [tracker](../TO_IMPLEMENT.md) owns the remaining work.
They run separately from the normal proxy/admin process.

## Operator flow

1. Provision the edge and connector with separate certificate/key files and
   their trusted CA certificates. Keep private keys local to their role.
2. Apply migrations with `scripts/migrate.sh up -db PATH`. `PATH` is the actual
   SQLite file, with an existing parent directory. The edge and enrolment
   commands must use that same path.
3. Enrol the connector's **public leaf certificate** with
   `switchboard connector enroll -db PATH -name portfolio -cert CERT_PATH`.
   `CERT_PATH` is the public connector certificate, never its private key.
4. Run `switchboard tunnel edge` with the flags below. It refuses to start if
   migrations are pending or the configured connector has no active enrolment.
5. Run `switchboard tunnel connect` with its own identity and fixed local route.
   It reconnects automatically after graceful or abrupt edge loss using the
   same identity. No connector listener is created.
6. `switchboard connector revoke -db PATH -name portfolio` marks the enrolment
   revoked. The edge revalidates live sessions every 5 seconds and closes a
   revoked session on a successful registry lookup; subsequent attachments are
   rejected. Registry errors leave established sessions intact but deny new
   attachments. `connector list` keeps revoked enrolments visible.

`PATH` and `CERT_PATH` above denote operator-selected paths, not literal paths
to copy into a command. Run `go run ./cmd/switchboard tunnel edge -h` or
`go run ./cmd/switchboard tunnel connect -h` from the repository for flag help.

| Both commands | Meaning |
|---|---|
| `-cert`, `-key`, `-ca` | Required file paths to the role's leaf chain, private key and trusted CA certificates. |
| `-host` | Required exact request hostname; unrelated hosts return 404. |

| Edge flags | Meaning |
|---|---|
| `-db` | SQLite registry, defaults to `switchboard.db` relative to the process working directory. |
| `-connector` | Required active registry name that serves this host. The peer's name is resolved from its certificate fingerprint, never a name claimed over the wire. |
| `-listen` | HTTP loopback IP/port; defaults to `127.0.0.1:0`. |
| `-tunnel-listen` | mTLS loopback IP/port; defaults to `127.0.0.1:0`. |

| Connect flags | Meaning |
|---|---|
| `-edge` | Required literal loopback IP and nonzero mTLS ingress port printed by the edge. |
| `-server-name` | Required expected edge DNS identity, checked against the server certificate. |
| `-root` | Absolute directory for a static site. |
| `-upstream` | Fixed HTTP origin on a literal loopback IP with a port. Choose exactly one of `-root` and `-upstream`. |

Both edge listeners use ephemeral ports by default. This avoids collisions with
the registered normal Switchboard service. No new persistent host port or
systemd unit is installed. HTTP origins reject credentials, paths, queries,
fragments and non-loopback destinations; the edge cannot supply a new upstream.

## Current visitor contract

These commands expose one **anonymous** HTTP/static route. They have no OIDC or
visitor-assertion flags. Each side runs the existing trust stage; client-supplied
identity headers, forwarding headers and the edge session cookie are stripped.
The connector does not interpret HTTP headers as an authenticated visitor
identity. Original visitor IP/identity attribution is not preserved end to end.
The authenticated certificate identifies the connector to the edge; it does not
identify the visitor to an application. Protected-app visitor assertions remain
an explicit TUN-002 requirement in the tracker.

## Reproduce the process smoke

From the Switchboard repository, with the Portfolio build already present:

```bash
SB_TUN_CHECK_DIR=$(mktemp -d /tmp/switchboard-tun002-check.XXXXXX)
go build -o "$SB_TUN_CHECK_DIR/switchboard" ./cmd/switchboard
python3 scripts/smoke-tunnel-persistent.py \
  --binary "$SB_TUN_CHECK_DIR/switchboard" \
  --site-root /home/saint/projects/portfolio/dist
```

The smoke creates its own database, short-lived lab identities and ephemeral
listeners. It verifies enrolment, Portfolio ES/EN and assets, unknown/hidden
paths, graceful and abrupt edge restart with the same live connector process,
revocation of a live session, rejection on reconnect, migration idempotency and
clean shutdown. Its databases, identities and processes are removed on exit.
The build directory above contains only the temporary executable.

The lab certificate issuer is only a disposable test fixture: its CA key is not
persisted and its leaves expire after 24 hours. Long-lived operation needs
operator-provisioned certificates and the rotation work tracked in TUN-002.
No public deployment, WAN recovery or host-service restart is established by
this smoke.

## Observed verification — 2026-10-17

- `go test -race ./cmd/switchboard ./internal/tunnel`: passed, including command
  validation, loopback/fixed-origin restrictions, migrated-registry requirement,
  active/revoked enrolment checks and cancellation.
- `scripts/smoke-tunnel-persistent.py`: passed against the real Portfolio build
  using separate edge/connector processes and only disposable resources.
