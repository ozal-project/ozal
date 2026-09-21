# Switchboard

A small reverse proxy, written in Go, that routes incoming requests to local
services by hostname — like an old telephone switchboard connecting each
call to the right line.

## Why

Built to solve a real problem: running several independent projects locally
(each on its own port) makes it easy to collide on ports and annoying to
remember which port serves what. Switchboard lets each project answer to a
name (`magiclink.localhost`, `auditanchor.localhost`, ...) instead of a
port number, and is meant to grow into the same mechanism used to publish
these projects on a real domain later, with automatic TLS.

The longer-term ambition: Switchboard as "a homemade Caddy plus a homemade
Okta" — a single Go binary that both routes traffic *and* gates it behind
login, the same niche as [Pomerium](https://github.com/pomerium/pomerium)
or [oauth2-proxy](https://github.com/oauth2-proxy/oauth2-proxy). Phase 1
(current work) delegates login to a real external OIDC provider (Google,
Okta, etc.) — Switchboard only manages the session after that. A from-
scratch identity provider (own users, own token issuance) is a deliberately
separate, later phase, tracked in the roadmap below, and won't be wired
into anything that needs real security until it's had the same scrutiny.

## Status

Working: host-based routing, health checks, graceful shutdown, and
automatic TLS via ACME are all implemented. OIDC login (delegated to an
external provider) is in progress.

## Roadmap

- [x] Project scaffold
- [x] Host-based routing to local upstreams (plain HTTP)
- [x] Structured request logging
- [x] Graceful shutdown
- [x] Health checks per upstream (unhealthy upstream -> 503, doesn't block startup)
- [x] Automatic TLS via ACME (Let's Encrypt) for real domains
- [ ] Config reload without restart
- [ ] `GET /_/status` heartbeat/dashboard (read-only view of route health)
- [x] **OIDC login, phase 1 — delegated auth**: per-route
      `auth_required`, login redirect to an external OIDC provider
      (authorization code + PKCE), server-side session, session cookie.
      Switchboard trusts the external provider's identity; it does not
      store credentials or issue its own tokens. Verified: real OIDC
      discovery against Google succeeds, and the full login -> PKCE ->
      code exchange -> signed-JWT verification -> session -> callback
      round trip is tested end-to-end against a fake in-process OIDC
      provider (real RSA-signed ID token, real JWKS, real signature
      verification). **Not yet verified against a real provider in a
      browser** — that needs a registered OAuth app (Google/Okta/etc.) to
      try live; the mechanics are proven, the real-world integration isn't.
- [ ] Runtime route management API (`POST/DELETE /routes`) on a separate,
      authenticated admin listener — not the public :80/:443
- [ ] **OIDC login, phase 2 — homemade identity provider** (stretch goal,
      curiosity-driven): Switchboard's own user store, password/credential
      handling, and OIDC token issuance, instead of delegating to Google/
      Okta/etc. Meaningfully higher security stakes than phase 1 (signing
      keys, token lifecycle, credential storage) — treated with the same
      "moonlighter" discipline as the Sigea projects: build it for real,
      but don't point anything that matters at it until it's proven out
      on its own.

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

Set `auth_required: true` on a route and fill in `auth` with a real OIDC
provider's details (Google, Okta, Auth0, ...). A request to that route
without a valid session gets redirected to the provider's login page;
after a successful login, Switchboard sets a session cookie (an opaque,
server-side session ID — it never signs or encodes anything into the
cookie itself, so there's no separate signing secret to manage) and
proxies the request through as normal.

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
    { "host": "localhost:8888", "target": "http://localhost:8090", "auth_required": true }
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
    { "host": "app.example.com", "target": "http://localhost:9001", "auth_required": true }
  ]
}
```

Without `cookie_domain`, Switchboard refuses to start if any `auth_required`
route is on a host other than `redirect_url`'s — that combination silently
loops back to login forever otherwise, and it's better to fail at startup
than discover it live.

`redirect_url` must be registered as an authorized redirect URI in the
OIDC provider's app configuration, and must exactly match Switchboard's
`/_/auth/callback` endpoint on whatever host/port it's actually reachable
at. `session_ttl_minutes` defaults to 480 (8 hours) if omitted. Switchboard
never sees or stores a password — it only receives the provider's ID token
after a successful login and trusts that identity for the session. A
post-login redirect (`return_to`) is only honored if it points at one of
the hosts in this config's `routes` — otherwise Switchboard would be an
open redirect for anyone who can craft a login link.
