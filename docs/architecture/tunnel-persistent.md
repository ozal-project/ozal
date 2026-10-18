# TUN-002: registry-backed private commands

The `switchboard tunnel edge|connect` commands use certificates supplied from
files and an explicitly migrated SQLite connector registry. Startup never
replaces or issues an identity. HTTP is loopback-only; mTLS transport may opt
into one private CIDR. Public exposure remains gated; the
[tracker](../TO_IMPLEMENT.md) owns the remaining work.
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
| `-tunnel-network` | Optional canonical CIDR wholly within RFC1918/IPv6 ULA. Only mTLS ingress/destination may use literal IPs inside it; loopback remains allowed. No wildcard, public address or DNS resolution. Defaults to loopback-only. |

| Edge flags | Meaning |
|---|---|
| `-db` | SQLite registry, defaults to `switchboard.db` relative to the process working directory. |
| `-connector` | Required active registry name that serves this host. The peer's name is resolved from its certificate fingerprint, never a name claimed over the wire. |
| `-listen` | HTTP loopback IP/port; defaults to `127.0.0.1:0`. |
| `-tunnel-listen` | mTLS IP/port; defaults to `127.0.0.1:0`. Private-network binding requires explicit `-tunnel-network`. |

| Connect flags | Meaning |
|---|---|
| `-edge` | Required literal IP and nonzero mTLS ingress port printed by the edge. Loopback unless explicitly allowed by `-tunnel-network`. |
| `-server-name` | Required expected edge DNS identity, checked against the server certificate. |
| `-root` | Absolute directory for a static site. |
| `-upstream` | Fixed HTTP origin on a literal loopback IP with a port. Choose exactly one of `-root` and `-upstream`. |

Both edge listeners use ephemeral ports by default. This avoids collisions with
the registered normal Switchboard service. No new persistent host port or
systemd unit is installed. HTTP origins reject credentials, paths, queries,
fragments and non-loopback destinations; the edge cannot supply a new upstream.

## Normal process integration — 2026-10-18 (local delivery)

The ordinary `switchboard -config PATH -db STATE` process can now own the mTLS
listener and route directly to enrolled connectors. This removes the separate
loopback facade→edge hop and preserves the trusted visitor IP selected on the
public request. The standalone commands above remain available.

```json
{
  "listen": "127.0.0.1:8888",
  "tunnel": {
    "listen": "10.8.0.1:9443",
    "network": "10.8.0.0/24",
    "cert_file": "/etc/switchboard/tunnel/edge.crt",
    "key_file": "/etc/switchboard/tunnel/edge.key",
    "ca_file": "/etc/switchboard/tunnel/ca.crt"
  },
  "routes": [{"host": "preview.example.com", "connector": "portfolio"}]
}
```

The state database must already be migrated and the named connector actively
enrolled. The listener defaults to no private-network exception: only literal
loopback binds are accepted unless `network` explicitly permits one wholly
private subnet. Wildcard/public binds are rejected. TLS 1.3, verified client
authentication and registry authorization remain mandatory. Certificates are
operator-supplied; no identity or migration is generated silently.

A route selects exactly one of `target`, `root`, or `connector`. A connector
route has no HTTP health probe, `spa`, or visitor access policy in this delivery.
**Protected connector routes fail configuration validation** until the signed
visitor-identity contract is implemented; connector identity and visitor
identity remain distinct. The connector still enforces its fixed host/origin.
Forwarding, identity headers and the edge session cookie are stripped, while
the visitor IP assertion is emitted only into the authenticated transport.

Route changes and admin route mutations use the normal atomic snapshot swap;
a mapping to an unenrolled or revoked connector rejects the candidate. An
actively enrolled but disconnected connector is a valid destination and
answers 502, with no direct-TCP fallback. Reload retains live tunnel sessions;
listener, network, and identity-file settings require a restart. The request
capacity is shared across integrated connector routes (32 responses in flight)
and held until body close, including bidirectional HTTP upgrades. Cancellation
and shutdown join the listener worker before closing state.

The activity console consumes connect/disconnect/rejection events from the
same broker as access events. Its route overview shows connector name and
attachment state; the route debug view explicitly distinguishes attachment
from application functionality. No host/process dashboard is introduced.

Reproduce the separate-process smoke with disposable identities, database and
loopback listeners:

```bash
go build -o /tmp/switchboard-configured-check ./cmd/switchboard
python3 scripts/smoke-tunnel-configured.py --binary /tmp/switchboard-configured-check
```

It proves trusted visitor IP and spoof stripping, unknown-host rejection,
offline 502, live mapping reload, rejection preserving the previous mapping,
console status, edge restart with the same connector PID, reconnect, live
revocation, and graceful shutdown. It does not publish or change the existing
VPS pilot, DNS, TLS, or systemd units. Deployment acceptance and protected-app
identity remain open under TUN-002/EDGE-001.

## Current visitor contract

These commands expose one **anonymous** HTTP/static route. They have no OIDC or
visitor-assertion flags. Each side runs the existing trust stage; client-supplied
identity headers, forwarding headers and the edge session cookie are stripped.
The connector does not interpret HTTP headers as an authenticated visitor
*identity*. The authenticated certificate identifies the connector to the edge;
it does not identify the visitor to an application. Protected-app visitor
identity assertions remain an explicit TUN-002 requirement in the tracker.

**Visitor address (2026-10-18).** The edge puts the address its own trust stage
selected into `X-Switchboard-Visitor-IP` (`pipeline.VisitorIPHeader`) on every
request sent into the tunnel. The connector runs `TrustAuthenticatedTunnel`:
because its handler is reachable only through the mutually authenticated tunnel,
the edge is the only party that can have set the header, so one valid unicast
address in it becomes the visitor address and the application receives it as a
single `X-Forwarded-For`. Everywhere else the header is client input: it is
stripped in every `-`/`_` spelling and never believed. A missing, malformed,
repeated or aliased assertion falls back to the tunnel peer. The internal header
never reaches the application.

Limit: the edge itself still learns the address from its own transport peer (or
`CF-Connecting-IP` from a configured trusted proxy). A separate Switchboard in
front of standalone `tunnel edge` over loopback still hides the real client.
The normal-process integration above removes that hop; deployment acceptance
of that path on the VPS remains pending.

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
No public deployment or host reboot is established by this local smoke.

## Private two-host pilot

The 2026-10-18 working-tree delivery adds `-tunnel-network` without changing
the disposable `tunnel-spike` command. An explicit prefix such as
`10.8.0.0/24` permits only the mTLS transport to cross hosts. It never broadens
the edge HTTP listener or the connector's fixed HTTP-origin permissions.
TLS 1.3, certificate-name verification, registry authorization and revocation
remain mandatory. Prefixes containing public space are rejected even if their
first address is private.

The operator flow and systemd units are maintained in
[Server Ops](../../../server-ops/deploy/hetzner-tunnel/README.md): VPS mTLS on
`10.8.0.1:9443`, HTTP on `127.0.0.1:18889`, outbound homelab connector with
fixed Portfolio root. The host firewall allows that transport only from
`10.8.0.4` on wg0. Role keys are generated on their own hosts; only public CSRs
and certificates cross hosts. This remains an anonymous static route; the
visitor contract above is unchanged.

Observed on the real pilot: a file unique to the homelab, EN/ES and selected
CSS/font/SVG/PNG assets matched through the VPS. Graceful edge restart and
SIGKILL/systemd recovery kept the connector PID unchanged. Revocation closed
the live session, subsequent retries were rejected, and HTTP returned 502.
A newly generated connector key/certificate was then enrolled; the pilot is
left running with that new identity and the old enrolment retained as revoked.
This demonstrates a manual replacement, not automatic certificate rotation.

Subsequent delivery on 2026-10-18 adds native `switchboard tunnel renew` and a
daily host timer. It issues fresh local role keys, signs public CSRs, replaces
the expected connector fingerprint transactionally and verifies the pair after
restart. A real forced renewal was observed; automated leaf renewal supersedes
the manual-only operating limitation above. See [renewal](tunnel-renewal.md)
for journal recovery, rollback, operator revocation and CA-rollover boundaries.

## Observed verification — 2026-10-17

- `go test -race ./cmd/switchboard ./internal/tunnel`: passed, including command
  validation, loopback/fixed-origin restrictions, migrated-registry requirement,
  active/revoked enrolment checks and cancellation.
- `scripts/smoke-tunnel-persistent.py`: passed against the real Portfolio build
  using separate edge/connector processes and only disposable resources.
