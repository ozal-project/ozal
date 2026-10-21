# Switchboard

A small reverse proxy, written in Go, that routes incoming requests to local
services by hostname — like an old telephone switchboard connecting each
call to the right line.

## What this is

Switchboard is a local-first, identity-aware application gateway for
developers and small self-hosted deployments. One binary, one config, from
localhost to production.

It started from a concrete annoyance: running several independent projects
locally (each on its own port) makes it easy to collide on ports and
annoying to remember which port serves what. Switchboard lets each project
answer to a name (`test1.localhost`, `test2.localhost`, ...)
instead of a port number, and grows into the same mechanism used to
publish those projects on a real domain later, with automatic TLS and,
optionally, a login requirement per route.

Not a Caddy or Pomerium replacement — those are mature, feature-complete
projects and competing on feature count would be a losing game. The
differentiator here is narrower and deliberate: an excellent local-dev
experience, the *same* config from a laptop to a real VPS, OIDC without
standing up five separate services, and configuration that stays legible
enough to actually explain. See [TO_IMPLEMENT.md](docs/TO_IMPLEMENT.md) for the
full reasoning and roadmap, including what's explicitly *not* planned here
(a full identity provider with passwords and account recovery stays its own
separate project; a narrow passwordless identity layer is planned — see
[VISION.md](VISION.md)).

## Where it's going

Switchboard is a year-long project (October 2026 – September 2027) aiming to
become an open-source, self-hostable **transparent zero-trust access
platform**: outbound tunnels so nothing needs an open port, passwordless
identity, a private ACME CA with short-lived certificates, infrastructure
access with recorded sessions, a WAF built to learn from, Telegram approvals,
an AI-agent gateway, and a transparency log
that makes every trust decision verifiable — even against the operator. The
full map, quarterly plan and principles are in [VISION.md](VISION.md); the
near-term checklist stays in [TO_IMPLEMENT.md](docs/TO_IMPLEMENT.md).

## How it fits

Switchboard is not "Caddy with Okta built in."

- **Caddy** is a broad, production-grade web server and proxy platform with a
  large module ecosystem. Switchboard deliberately has a much smaller scope:
  host-based routing for local services and small self-hosted deployments,
  with a configuration a developer can read in one sitting.
- **Okta, Google, Auth0, and similar services** are identity providers.
  Switchboard does not manage users, passwords, MFA, consent, signing keys,
  or token issuance. It delegates login to one of those providers via OIDC,
  verifies the resulting identity, and applies a route's access policy.
- **Pomerium** is a mature, policy-heavy identity-aware proxy. Switchboard
  borrows the useful shape of that idea for a smaller audience: a single Go
  binary that lets a developer put a local or self-hosted app behind a name,
  TLS, and optional OIDC access control without assembling several services.

Put plainly: Switchboard is an **identity-aware application gateway**. It
connects `app.localhost` or `app.example.com` to an upstream service, then
optionally asks an external identity provider who the user is before allowing
that request through. Its differentiator is the compact, local-first path from
development to a small real deployment, not a larger feature checklist.

## Open-source direction

The initial product is deliberately narrow: make it easy to expose several
local or self-hosted applications through human-readable hosts, then apply
TLS and optional external OIDC access control with one understandable config.
That is the MVP, not a temporary compromise before rebuilding Caddy or Okta.

The project should grow only where a capability makes that path more reliable,
safer, or easier to operate. Its first release goal is a trustworthy public
alpha, with secrets outside JSON, fuzz coverage for the security boundaries,
CI and release artifacts. The measurable release gates and dates are in
[TO_IMPLEMENT.md](docs/TO_IMPLEMENT.md#alpha-release-gates).

## Local tunnel experiment (no VPS required)

`switchboard tunnel-spike init|edge|connect` exercises an outbound connector
and edge on loopback with mutual TLS and yamux. It serves one fixed HTTP origin
or static site through the tunnel. See the [local lab guide](docs/architecture/tunnel-local-spike.md).
This is the `TUN-001` experiment, not a publicly deployable tunnel product.
`switchboard tunnel edge|connect` adds registry-backed local commands with
operator-supplied certificate/key files and automatic reconnect. See the
[persistent tunnel guide](docs/architecture/tunnel-persistent.md) for enrolment,
revocation, flags and the reproducible process smoke. Config/reload/admin
integration, visitor assertions and credential rotation remain in `TUN-002`.

## SQLite migrations

The embedded state database currently has migrations for `sessions` and
`connectors`. Apply them through the Go runner so transaction boundaries,
checksums and migration history are preserved:

```bash
./scripts/migrate.sh status -db /absolute/path/state.db
./scripts/migrate.sh up -db /absolute/path/state.db
./scripts/migrate.sh status -db /absolute/path/state.db
```

The parent directory must already exist. Without `-db`, the target is
`switchboard.db` in the repository root; relative paths also resolve from that
root. `status` checks history and lists pending migrations without applying
application SQL, but can create a fresh database and its history table.
Repeated `up` runs skip applied migrations. A changed applied migration or a
database newer than the runner is rejected. Use this checkout's runner so the
embedded migrations include `002_create_connectors`.

To author a future schema change:

```bash
./scripts/new-migration.sh add_connector_metadata
# Edit the generated SQL, then test it on a disposable database first.
```

The generator checks contiguous numbering and never overwrites an existing
file. Applied SQL files stay immutable. The runner owns transactions; do not
add `BEGIN`/`COMMIT` to migration files. Session persistence in the normal
application is enabled only by starting it with `-db PATH` (see sessions below);
creating its table alone does not enable it.

The module keeps `go 1.26.0` as its language minimum and recommends toolchain
`go1.27.3` (fixes `GO-2026-6809` in `crypto/x509`); normal Go toolchain
auto-selection uses that patched version.

## Optional notifications (Telegram)

With a `notify.telegram` block, Switchboard sends login and admin-change
alerts (logins, route changes, config reloads) to every paired chat:

```json
"notify": {
  "telegram": {
    "token": "file:/etc/switchboard/telegram-token",
    "chats_file": "/var/lib/switchboard/telegram-chats.json"
  }
}
```

`token` is a secret reference (`env:` or `file:`) like every other
credential; the bot token never appears in logs or events. `chats_file` keeps
the paired chat IDs with owner-only permissions and is re-read when it
changes, so pairing does not need a restart. `api_base` optionally points at a
self-hosted [local Bot API server](https://github.com/tdlib/telegram-bot-api)
(https; plain http only on loopback).

Pair a chat — a private one — with a one-time code that only exists while the
command waits:

```bash
go run ./cmd/switchboard notify pair -config config.json
```

Send the printed `/pair CODE` to the bot in the chat you want alerts in. A
wrong code neither pairs nor ends the command; the code dies with it. Alerts
never block the request path: a bounded queue drops and counts notifications
if the API is slow, and the adapter retries 429/5xx answers a couple of times
with a capped backoff, honoring the server's `retry_after`. Inline-button
callback data (for the future approval flow) is signed with a key derived from
the bot token, carries an action, a nonce and an expiry inside Telegram's
64-byte limit, and is verified in constant time before use. No message is sent
to any chat unless you configure the block and pair one.

## Optional rate limiting
The normal routing config can enable per-IP buckets; requests under `/_/auth/`
use the separate auth bucket, and an optional per-identity bucket is charged
after the access policy allows a request. The feature is disabled when
`rate_limit` is absent.

```json
"rate_limit": {
  "general":  { "rate": 20, "burst": 40 },
  "auth":     { "rate": 1,  "burst": 3 },
  "identity": { "rate": 10, "burst": 30 },
  "max_keys": 10000
}
```

Rates are requests per second. Client keys come from the trust stage, not raw
visitor forwarding headers, and include the route host, so one busy hostname
does not spend another's budget. `identity` is a separate budget per
authenticated principal (the value after login); omit it to disable that
dimension. `max_keys` caps how many distinct buckets each limiter keeps
(default 10000): a scan that varies its key cannot grow the maps without
bound, and at capacity new keys are refused rather than evicting a bucket that
still owes a restriction. A refused request answers `429` with the real
`Retry-After` (rounded up to whole seconds, minimum 1) and emits an
`access.rate_limited` event into the activity feed. Buckets survive a config
reload while the settings do not change; changing `rate_limit` starts with
fresh buckets.
The local tunnel experiment is separate from this normal configuration path.

## Routes through persistent connectors

The ordinary process can own the mTLS tunnel listener and forward an anonymous
route directly to an enrolled connector:

```json
"tunnel": {
  "listen": "127.0.0.1:9443",
  "cert_file": "/etc/switchboard/tunnel/edge.crt",
  "key_file": "/etc/switchboard/tunnel/edge.key",
  "ca_file": "/etc/switchboard/tunnel/ca.crt"
},
"routes": [{ "host": "preview.example.com", "connector": "portfolio" }]
```

Start with `switchboard -config PATH -db STATE` after explicitly migrating the
state and enrolling `portfolio`. One destination per route: `target`, `root`,
or `connector`. A private bind requires an explicit wholly private `network`
CIDR; public/wildcard tunnel binds are refused. Connector routes reload without
restarting the listener or dropping sessions, and an offline connector returns
502 without a direct-origin fallback. Tunnel listener/identity changes require
a process restart. Protected connector routes are rejected until their visitor
identity contract is implemented; no certificate identifying a connector is
interpreted as a visitor login.

The normal trust stage resolves the visitor IP once, and the tunnel carries it
to the connector without a facade hop. Connection events and safe attachment
status appear in the existing console. See the
[configuration contract and process smoke](docs/architecture/tunnel-persistent.md#normal-process-integration--2026-10-18-local-delivery).

## Local trust activity

The normal admin listener serves `/_/activity` and a live SSE feed at `/_/events`,
with recent events replayed from memory. The page shows access decisions,
logins and control-plane changes; it does not control host processes. It stays
on the loopback admin listener. History is bounded and not durable; reconnects
can replay events, and slow subscribers can miss events. The console includes category/search filters, pause/resume, and a route-access
overview. `/` opens the activity page; `/_/status` is a read-only route-debug
snapshot with expandable probe details. Static sites have no HTTP probe. Full
protected-route acceptance remains tracked in `UI-001`; tunnel CLI events are
not integrated into this normal-process feed yet.

## Status

Solid prototype, **not yet production-ready** — being hardened per an
external review (2026-09-21) before anything else gets built on top.
Working and tested: host-based routing, health checks, graceful shutdown,
automatic TLS via ACME, OIDC login, and per-route authorization by verified
email, email domain, and group claims. `go test ./... -race` is clean.
The routing runtime is built and swapped as atomic, immutable snapshots. A
loopback-only control plane now provides persistent route changes, reloads, and
operational status.

In progress (Phase 1 hardening, see [TO_IMPLEMENT.md](docs/TO_IMPLEMENT.md)):
- [x] HTTP server timeouts (`ReadHeaderTimeout`, `IdleTimeout`) on every listener
- [x] Session store: periodic cleanup sweep + capacity ceiling (`ErrStoreFull`)
- [x] Authorization by claims (allowed emails/domains/required groups)
- [x] Strict route validation (duplicates, HTTP(S) targets, normalized hosts/cookies)
- [x] Verified identity headers propagated upstream; client-forged values stripped
- [x] Secrets via `env:`/`file:` references, not only inline JSON (`REL-001`)
- [x] `LICENSE` (MIT)
- [x] CI on Linux, macOS and Windows runs `scripts/verify.sh` (green on `main` and on a pull request, 2026-10-21; Windows is a build/test target and skips seven config tests with POSIX path fixtures)
- [x] Deployed static sites with history and rollback (`PAGES-002`)
- [x] Release artifacts with checksums and SBOMs (`REL-005`); release workflow on tags (`REL-006`)
- [x] [`v0.1.0-alpha.1`](https://github.com/octaviosantacruz/switchboard/releases/tag/v0.1.0-alpha.1) published 2026-10-21 ([notes](docs/releases/v0.1.0-alpha.1.md))

## Next work

The next engineering block is the release work that makes the alpha usable by
someone else, before adding more proxy behavior:

1. Release engineering (`REL-005`, `REL-006`): GoReleaser artifacts with
   checksums, then the `v0.1.0-alpha.1` tag. CI already runs
   `scripts/verify.sh` (including the short fuzz pass) on Linux, macOS and
   Windows.

After that comes Phase 4: multiple upstreams with round-robin selection,
active and passive health checks, safe retries, circuit breaking, per-route
timeouts, explicit WebSocket/SSE coverage and request IDs. The full ordered
backlog remains in [TO_IMPLEMENT.md](docs/TO_IMPLEMENT.md).

### Design records (proposed, not shipped)

These documents record proposed work and its limits; they are not features yet:

- [Durable audit contract](docs/architecture/audit-contract.md) — append-only
  audit, signed checkpoints and failure semantics.
- [Policy explain](docs/architecture/policy-explain.md) — explain an
  authorization decision with the existing pure `Authorize`.
- [Shield challenge](docs/architecture/shield-challenge.md) — a self-hosted
  proof-of-work challenge, not a Turnstile parity claim.
- [Identity integration](docs/architecture/identity-integration.md) — how apps
  behind the edge trust (or do not trust) the gateway identity.

Learning labs live in [TO_IMPLEMENT.md](docs/TO_IMPLEMENT.md) under `LAB-*`;
they are not release commitments.

## Roadmap

Done: project scaffold, host-based routing, request logging (plain
`log.Printf` today — structured JSON logging is still Phase 5, not done
yet), graceful shutdown, per-upstream health checks, automatic TLS via
ACME, and OIDC login with per-route claim authorization (delegated to an
external provider — see below).

Everything still ahead — serious proxy behavior, production observability,
and npm-based distribution — is tracked in
detail in [TO_IMPLEMENT.md](docs/TO_IMPLEMENT.md), including the stack decisions
already made for each and why.

The Phase 2 transactional runtime is complete: a reload candidate is fully
validated and built before one atomic swap. Invalid candidates leave the
last-good routing snapshot active. Listener and TLS settings remain static
and require a process restart; Phase 3 exposes reload through the control plane.

## Control plane

Switchboard starts a separate administrative listener on `127.0.0.1:9090` by
default. Its status dashboard is available at `http://127.0.0.1:9090/_/status`
and shows health, latency, the latest check, the latest state transition, and the
last probe error for every route. The admin listener is restricted to loopback
IPs by configuration validation.

Configure a bearer token to enable mutations:

```json
"admin": {
  "listen": "127.0.0.1:9090",
  "token": "replace-with-a-long-random-token"
}
```

`POST /routes`, `DELETE /routes/{host}`, and `POST /_/reload` require
`Authorization: Bearer <token>`. Route changes are validated, written atomically
to the active config file, and only then installed as a complete runtime
snapshot. Duplicate creates return `409`; deleting the final route is rejected.
Every accepted or rejected control-plane change is written to the audit log.

Sending `SIGHUP` performs the same last-good reload from disk. Changes to the
public listener, `admin.listen`, or TLS settings require a process restart.

## Usage

New here? [QUICKSTART.md](QUICKSTART.md) goes from a clean checkout to a routed
app and the status page.

```bash
go run ./cmd/switchboard -config config.json
```

Check a candidate config without starting anything (deployment runbooks use it
before installing a file; it never resolves or prints secrets):

```bash
go run ./cmd/switchboard validate -config config.preview.json
# config OK: routes=1 protected=0 tunnel=0 tls=true dns01=true trusted_proxies=22 admin=127.0.0.1:9091
```

The admin console (loopback, default `127.0.0.1:9090`) is a Svelte app embedded in
the binary: Activity, Routes and a Policy simulator. Its source is `web/console`;
rebuild it after changing the source:

```bash
scripts/build-console.sh   # needs Node; output in internal/admin/console (commit it)
```

Explain why a route would allow or deny an example identity (authorization only;
no listener, no OIDC flow, no secrets printed):

```bash
go run ./cmd/switchboard policy explain -config config.json \
  -host app.example.com -email ana@example.com -verified -groups engineering
go run ./cmd/switchboard policy explain -config config.json -host app.example.com -no-session -json
```

### Local development (plain HTTP)

`config.json`:

```json
{
  "listen": ":8888",
  "routes": [
    { "host": "magiclink.localhost:8888", "target": "http://localhost:8090" },
    { "host": "auditanchor.localhost:8888", "target": "http://localhost:8081", "health_path": "/healthz" }
  ]
}
```

`health_path` defaults to `/` if omitted. An unchecked or healthy upstream
gets traffic; an upstream that fails its check (5xx or unreachable) gets a
503 from Switchboard instead of being proxied to.

### Static sites

A route can serve a directory instead of proxying, the way Cloudflare Pages
does. Use `root` (an absolute path) instead of `target`:

```json
{ "host": "blog.localhost:8888", "root": "/srv/sites/blog", "spa": false }
```

Directories are served through their `index.html` and never listed; hidden
files (`.git`, `.env`) are refused except `.well-known`; files are opened
with `os.OpenInRoot`, so neither `..` nor a symlink can leave the directory.
`spa: true` serves the root `index.html` for unknown extensionless paths, for
client-side routers. Static routes take `access` policies like any other.

#### Deployed sites (`site` instead of `root`)

With a `pages` block, Switchboard stores the builds itself and switches
between them atomically, with history and rollback:

```json
"pages": { "dir": "/var/lib/switchboard/pages", "keep": 5 },
"routes": [{ "host": "blog.example.com", "site": "blog", "spa": false }]
```

```bash
switchboard migrate -db state.db up          # adds the deployments table
export SWITCHBOARD_ADMIN_TOKEN=...           # the admin.token value
switchboard site deploy -site blog ./dist    # pack, upload, make live
switchboard site list                        # history, * marks the live one
switchboard site rollback -site blog -to <id>
```

`pages` requires `-db`. A deployment is named by the SHA-256 of its file list,
so uploading the same build twice reuses it. The admin API refuses archives
with `..`, absolute paths, links, special files, more than 20 000 entries,
files over 100 MiB or more than 500 MiB in total (counting the bytes actually
written), and writes through `os.Root`. A site with no deployment yet answers
`503`. The console's **Sites** page shows each site's history and can roll
back; every deploy, rollback and refusal appears in Activity. The CLI sends
the token over plain HTTP only to a loopback address: for a remote edge, use an
SSH tunnel to its admin listener. Design: [pages-deploy.md](docs/architecture/pages-deploy.md).

A deployment may carry Cloudflare Pages-style `_redirects` and `_headers`
files at its root (a subset of the syntax; they are never served):

```text
# _redirects: SOURCE DESTINATION [301|302|303|307|308]
/old            /new
/blog/*         /posts/:splat
/u/:name        https://example.com/people/:name  302

# _headers: a pattern, then indented headers; "! Name" removes one
/*
  X-Frame-Options: DENY
/assets/*
  Cache-Control: public, max-age=31536000, immutable
```

The first matching redirect wins, even over an existing file; every matching
header rule applies in order. Both files are checked at deploy time and an
invalid one refuses the deploy (`rules.invalid`, with the line). Refused on
purpose: `200` rewrites, absolute-URL patterns, placeholders in a destination
host, and headers a site must not set (`Set-Cookie`, which could plant a
cookie for every host under the parent domain, hop-by-hop and framing headers,
`Location`, `X-Content-Type-Options`). A redirect built from the visitor's
path never becomes protocol-relative (`//host`). Rollback restores a
deployment's rules with its files.

Route hosts are matched case-insensitively and normalized without a trailing
DNS dot. Duplicate normalized hosts are rejected, and upstream targets must
be absolute `http://` or `https://` URLs.

### Local error pages

`dev_errors: true` renders development-friendly errors: a 404 that lists the
configured hosts when the `Host` doesn't match any route, and a 502 that names
the unreachable upstream plus a hint. It is off by default and meant for local
development only — those pages reveal internal hostnames and addresses, so
never enable them on a public edge.

### Restricting the public listener to your proxy (origin client certificate)

With `tls.enabled`, `tls.origin_client_ca_file` points at a PEM bundle: the
public HTTPS listener then answers only connections that present a client
certificate issued by it, with the client-authentication key usage. This is how
the origin recognises the proxy in front of it (for example Cloudflare
Authenticated Origin Pulls with your zone's own certificate) instead of
trusting source IP ranges. Every refused connection emits an `origin.denied`
event with a code (`no_certificate`, `untrusted`, `expired`, `invalid`). An
unreadable or empty bundle stops startup, and changing the file needs a
restart. ACME certificate issuance keeps working — on the autocert path the
tls-alpn-01 validation exchange stays exempt (it cannot present a client
certificate); on the DNS-01 path below that exemption is gone on purpose.

### Origin certificate by DNS-01 (`tls.dns01`)

Behind a proxy with a restricted firewall nothing can reach ports 80/443 to
validate a certificate, so Switchboard can issue its own origin certificate
with the ACME DNS-01 challenge instead: it writes a `_acme-challenge` TXT
record through the Cloudflare API, waits for the value to be visible, lets the
CA validate, and removes exactly the record it created (by its API ID, on
success and on every failure path, with a persisted intent so a crash does not
lose the cleanup). No port 80, no inbound connection.

```json
{
  "tls": {
    "enabled": true,
    "domains": ["preview.example.com"],
    "email": "you@example.com",
    "origin_client_ca_file": "/etc/switchboard/origin-ca.pem",
    "dns01": {
      "state_dir": "/var/lib/switchboard/origin-cert",
      "directory_url": "https://acme-v02.api.letsencrypt.org/directory",
      "dns_token": "file:/etc/switchboard/cloudflare-token"
    }
  }
}
```

- One hostname, one Cloudflare zone, zone-scoped API token (`dns_token` is a
  secret reference, `file:` or `env:`; the resolved value is never logged).
- `state_dir` holds the ACME account (separate from every certificate key),
  the complete certificate generations and the DNS cleanup intents, all with
  owner-only permissions. It is claimed for one ACME directory forever, so
  staging and production can never mix — use a different directory for each.
- Renewal starts in the last third of the certificate's real lifetime, retries
  with bounded jittered backoff, honors the CA's rate-limit `Retry-After`, and
  keeps serving the previous certificate when renewal fails. With no valid
  certificate the listener refuses handshakes (fail closed); `/_/status` shows
  expiry, last outcome and next attempt.
- `cert.renewed` and `cert.renewal_failed` events carry the hostname, a stable
  code and the next attempt — never keys, tokens or challenge values.
- Certificate chains are verified against the system roots before activation.
  For a direct staging test, set `tls.dns01.certificate_roots_file` to an absolute
  path containing the explicitly trusted staging CA roots; this replaces the
  system pool for certificate validation. Use a separate state directory and
  remove that setting for production. This trust is separate from
  `origin_client_ca_file`, which authenticates inbound clients.
- A second process pointed at the same `state_dir` is refused at startup.
  Shutdown cancels and joins the renewal worker, including bounded DNS cleanup,
  before releasing the state lock.
- Changing DNS-01 or Telegram settings requires a restart; reload rejects the
  change and keeps the installed snapshot.

### Slow upstreams

An upstream that accepts a connection but never starts answering would hold the
visitor's request open forever. Switchboard waits up to
`upstream_timeout_seconds` (default 60, at most 3600) for the response headers
and then answers `504`; an unreachable upstream answers `502`. The limit never
applies to the response body, so downloads and streams keep working. The
setting is applied on reload; connections to unchanged settings are reused.

### Real deployment (HTTPS via Let's Encrypt)

Set `tls.enabled: true` and point real DNS at the box. Switchboard then
listens on `:80` (ACME challenges + redirect to HTTPS) and `:443` (TLS,
certificates issued and renewed automatically):

```json
{
  "listen": ":8888",
  "tls": {
    "enabled": true,
    "domains": ["magiclink.example.com", "auditanchor.example.com"],
    "cache_dir": "/var/lib/switchboard/certs",
    "email": "you@example.com"
  },
  "routes": [
    { "host": "magiclink.example.com", "target": "http://localhost:8090" },
    { "host": "auditanchor.example.com", "target": "http://localhost:8081", "health_path": "/healthz" }
  ]
}
```

`cache_dir` must be a writable, persistent directory — certificates are
cached there so they aren't re-issued on every restart. `listen` is ignored
in this mode (ports 80/443 are fixed, as ACME and browsers expect).

### Gating a route behind login (OIDC, phase 1)

Add an `access` policy to a route and fill in `auth` with a real OIDC
provider's details (Google, Okta, Auth0, ...). An empty policy (`"access": {}`)
allows any authenticated identity. A request without a valid session gets
redirected to the provider's login page; after a successful login,
Switchboard sets a session cookie (an opaque, server-side session ID — it
never signs or encodes anything into the cookie itself, so there's no
separate signing secret to manage) and checks the route policy before
proxying the request.

Sessions live in memory by default: they don't survive a restart and aren't
shared across replicas — fine for a single-box deployment. Start Switchboard
with `-db PATH` pointing at a migrated state database (`switchboard migrate
-db PATH` first; migrations are never applied silently at startup) and
sessions persist there instead: only the SHA-256 of the session ID is stored,
expired sessions are rejected on lookup and swept periodically, and the same
capacity cap applies. Lookups cap validity at the earlier of the issued expiry
and `created_at + current TTL`, so reducing the TTL takes effect on existing
sessions, and increasing it never extends the original expiry. Reload candidates
do not mutate session rows; expired rows are purged on lookup, sweep and creation.
This is the wiring for later persistent state (tunnels,
passkeys) on the same database.

**Single host** — the protected route lives on the same host as
`redirect_url`:

```json
{
  "listen": ":8888",
  "auth": {
    "issuer": "https://accounts.google.com",
    "client_id": "your-client-id",
    "client_secret": "your-client-secret",
    "redirect_url": "http://localhost:8888/_/auth/callback",
    "session_ttl_minutes": 480
  },
  "routes": [
    { "host": "localhost:8888", "target": "http://localhost:8090", "access": {} }
  ]
}
```

**Multiple hosts** — an OIDC provider only allows a fixed, pre-registered
redirect URI, so the callback always lands on one host. To protect routes
on *other* hosts too, set `cookie_domain` to a suffix they all share, so
the session cookie set during the callback is sent back on every one of
them:

```json
{
  "listen": ":8888",
  "tls": { "enabled": true, "domains": ["auth.example.com", "app.example.com"], "cache_dir": "/var/lib/switchboard/certs" },
  "auth": {
    "issuer": "https://accounts.google.com",
    "client_id": "your-client-id",
    "client_secret": "your-client-secret",
    "redirect_url": "https://auth.example.com/_/auth/callback",
    "cookie_domain": ".example.com"
  },
  "routes": [
    { "host": "auth.example.com", "target": "http://localhost:9000" },
    {
      "host": "app.example.com",
      "target": "http://localhost:9001",
      "access": {
        "allowed_domains": ["example.com"],
        "required_groups": ["engineering"]
      }
    }
  ]
}
```

Without `cookie_domain`, Switchboard refuses to start if any protected route
is on a host other than `redirect_url`'s — that combination silently loops
back to login forever otherwise, and it's better to fail at startup than
discover it live.

`allowed_emails` and `allowed_domains` are alternative ways to pass the
identity allowlist; `required_groups` is additionally enforced and every
listed group must be present. Email matching is case-insensitive and requires
the provider's `email_verified` claim. Group names are case-sensitive. Group
authorization only works when the provider includes a `groups` claim in the
ID token; if a route requires groups and that claim is absent, access is
denied. The older `auth_required: true` remains supported as shorthand for
an empty access policy.

### Secret references

`auth.client_secret` and `admin.token` accept a reference instead of an inline
value:

- `"env:NAME"` reads environment variable `NAME` (must be non-empty);
- `"file:/path/to/secret"` reads the file and trims surrounding whitespace.

The control plane persists the reference itself, never the resolved value, so
reloading routes or editing the config through the admin API cannot leak a
secret into the file. Inline values still work for local development but are
not suitable for a deployed edge. The `env:` and `file:` prefixes are reserved.

A `client_secret` reference that cannot be resolved fails startup; an
unresolvable `admin.token` reference answers `503` on mutations until fixed.

`redirect_url` must be registered as an authorized redirect URI in the
OIDC provider's app configuration, and must exactly match Switchboard's
`/_/auth/callback` endpoint on whatever host/port it's actually reachable
at. `session_ttl_minutes` defaults to 480 (8 hours) if omitted. Switchboard
never sees or stores a password — it only receives the provider's ID token
after a successful login and trusts that identity for the session. A
post-login redirect (`return_to`) is only honored when it is an absolute
HTTP(S) URL without userinfo and points at one of the **protected** route
hosts — a host whose route sets an access policy or `auth_required` —
otherwise Switchboard would be an open redirect for anyone who can craft a
login link. `/_/auth/login`, `/_/auth/callback` and `/_/auth/logout` are
answered only on those protected hosts (plus the `redirect_url` host); on
any other host the path belongs to the route's own upstream. Logout is a
`POST`.

## Client IP behind a trusted proxy

Forwarding-IP trust is disabled by default. `trusted_proxies` explicitly lists
TCP peer IPs or CIDRs allowed to supply a single `CF-Connecting-IP` address:

```json
"trusted_proxies": ["127.0.0.1/32", "::1/128"]
```

Use those loopback entries only for a local `cloudflared` connector, with the
origin listener on loopback. This trusts local processes able to reach that
listener; it does not authenticate the connector process. Do not add visitor
networks or a catch-all CIDR. IP literals and network prefixes are normalized;
hostnames and scoped addresses are rejected. Changes reload atomically with
the routing snapshot and are preserved by route mutations.

Trust runs before routing/authentication: a configured peer with one valid IP
header supplies the client address; otherwise the socket peer is used. Duplicate,
comma-separated, malformed, scoped, unspecified or multicast header values are
ignored. Invalid socket peers produce no client IP. IPv4-mapped addresses are
normalized to IPv4. `RemoteAddr` always remains the transport peer;
`pipeline.ClientIP(ctx)` exposes the selected address for later stages.

Incoming forwarding/real-IP headers (including underscore aliases) are stripped.
The reverse proxy builds one `X-Forwarded-For` from the context; a caller's chain
never survives, and `CF-Connecting-IP` itself is not passed upstream. Existing
Host/protocol handling and edge-cookie stripping remain in the outbound adapter.

See [`config.tunnel.example.json`](config.tunnel.example.json) for a Portfolio-only
route on `preview.octaviosantacruz.com` (the live apex stays on Cloudflare Pages),
and the [optional Cloudflare deployment proposal](docs/architecture/edge-cloudflared.md)
for that alternative only. The example is not an installed/public deployment.
The owned transport is developed in the [local tunnel lab](docs/architecture/tunnel-local-spike.md);
Cloudflare credentials are not required for it.
