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
answer to a name (`magiclink.localhost`, `auditanchor.localhost`, ...)
instead of a port number, and grows into the same mechanism used to
publish those projects on a real domain later, with automatic TLS and,
optionally, a login requirement per route.

Not a Caddy or Pomerium replacement — those are mature, feature-complete
projects and competing on feature count would be a losing game. The
differentiator here is narrower and deliberate: an excellent local-dev
experience, the *same* config from a laptop to a real VPS, OIDC without
standing up five separate services, and configuration that stays legible
enough to actually explain. See [TO_IMPLEMENT.md](TO_IMPLEMENT.md) for the
full reasoning and roadmap, including what's explicitly *not* planned here
(a from-scratch identity provider — real learning territory, but its own
separate project, not part of Switchboard's story).

## Status

Solid prototype, **not yet production-ready** — being hardened per an
external review (2026-09-21) before anything else gets built on top.
Working and tested: host-based routing, health checks, graceful shutdown,
automatic TLS via ACME, OIDC login, and per-route authorization by verified
email, email domain, and group claims. `go test ./... -race` is clean.
The routing runtime is now built and swapped as atomic, immutable snapshots.

In progress (Phase 1 hardening, see [TO_IMPLEMENT.md](TO_IMPLEMENT.md)):
- [x] HTTP server timeouts (`ReadHeaderTimeout`, `IdleTimeout`) on every listener
- [x] Session store: periodic cleanup sweep + capacity ceiling (`ErrStoreFull`)
- [x] Authorization by claims (allowed emails/domains/required groups)
- [x] Strict route validation (duplicates, HTTP(S) targets, normalized hosts/cookies)
- [x] Verified identity headers propagated upstream; client-forged values stripped
- [ ] Secrets via env/file, not only inline JSON
- [x] `LICENSE` (MIT)
- [ ] CI and the rest of Phase 6's open-source hygiene items

## Roadmap

Done: project scaffold, host-based routing, request logging (plain
`log.Printf` today — structured JSON logging is still Phase 5, not done
yet), graceful shutdown, per-upstream health checks, automatic TLS via
ACME, and OIDC login with per-route claim authorization (delegated to an
external provider — see below).

Everything still ahead — the status dashboard, a config reload trigger, a
runtime route-management API, and npm-based distribution — is tracked in
detail in [TO_IMPLEMENT.md](TO_IMPLEMENT.md), including the stack decisions
already made for each (e.g. `html/template` + HTMX for the dashboard, not a
JS framework) and why.

The Phase 2 transactional runtime is complete: a reload candidate is fully
validated and built before one atomic swap. Invalid candidates leave the
last-good routing snapshot active. Listener and TLS settings remain static
and require a process restart; Phase 3 will expose reload through the control
plane.

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

Route hosts are matched case-insensitively and normalized without a trailing
DNS dot. Duplicate normalized hosts are rejected, and upstream targets must
be absolute `http://` or `https://` URLs.

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
HTTP(S) URL without userinfo and points at one of the normalized hosts in
this config's `routes` — otherwise Switchboard would be an open redirect
for anyone who can craft a login link.
