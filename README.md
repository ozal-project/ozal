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
CI, and a repeatable quickstart. The measurable release gates and dates are in
[TO_IMPLEMENT.md](docs/TO_IMPLEMENT.md#alpha-release-gates).

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
- [ ] Secrets via env/file, not only inline JSON
- [x] `LICENSE` (MIT)
- [ ] CI and the rest of Phase 6's open-source hygiene items

## Next work

The next engineering block closes the remaining hardening debt before adding
more proxy behavior:

1. Read `auth.client_secret` and `admin.token` from environment variables or
   files, so deployed credentials do not need to live in version-controlled
   JSON.
2. Add fuzz tests for configuration parsing, host normalization, and the OIDC
   `return_to` open-redirect guard.
3. Add CI that runs `go test -race ./...`, `go vet ./...`, and a build on each
   supported platform.

After that comes Phase 4: multiple upstreams with round-robin selection,
active and passive health checks, safe retries, circuit breaking, per-route
timeouts, explicit WebSocket/SSE coverage, request IDs, and a trusted-proxy
policy. The full ordered backlog remains in [TO_IMPLEMENT.md](docs/TO_IMPLEMENT.md).

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

```bash
go run ./cmd/switchboard -config config.json
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

Route hosts are matched case-insensitively and normalized without a trailing
DNS dot. Duplicate normalized hosts are rejected, and upstream targets must
be absolute `http://` or `https://` URLs.

### Local error pages

`dev_errors: true` renders development-friendly errors: a 404 that lists the
configured hosts when the `Host` doesn't match any route, and a 502 that names
the unreachable upstream plus a hint. It is off by default and meant for local
development only — those pages reveal internal hostnames and addresses, so
never enable them on a public edge.

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

Sessions are in-memory and per-process: they don't survive a restart and
aren't shared across replicas. Fine for a single-box deployment; revisit
if Switchboard is ever run more than once behind a load balancer.

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
