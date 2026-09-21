# TO_IMPLEMENT.md — Switchboard roadmap

The ambition: Switchboard becomes a single Go binary that is simultaneously
**your own Caddy** (reverse proxy, automatic TLS) **and your own
Pomerium/oauth2-proxy** (identity-aware gating), with a real dashboard, a
runtime API, and eventually its own identity provider — built to be
genuinely useful for publishing every other project in `~/projects`, not
just a learning toy.

Status snapshot as of 2026-09-21: routing, health checks, TLS/ACME, and
phase-1 OIDC login (delegated to an external provider) are done and tested.
Everything below is what's left, organized so each phase stands on its own
and none of them require guessing at scope later.

---

## Phase A — Status dashboard (`GET /_/status`)

- Read-only view of every route: host, target, healthy/unhealthy (from the
  existing `healthcheck.Checker`), and whether it requires auth.
- Stack decision (already made): `html/template` (stdlib) + HTMX for live
  refresh, no build step, no JS framework. Templates embedded via
  `embed.FS` so the binary stays a single deployable artifact — no React,
  Astro, or Next.js; see the conversation history for the full reasoning
  (breaks the "one binary" story, unjustified for a read-only status table).
- No auth required on this endpoint initially (it's read-only, no secrets
  in it) — revisit if the route list itself is considered sensitive.
- Should reuse `healthcheck.Checker`'s existing state rather than adding a
  second probing mechanism.

## Phase B — Config reload without restart

- Watch the config file (e.g. `fsnotify`) or accept `SIGHUP` to reload
  routes without dropping connections.
- Must re-validate the new config with the same rules as `config.Load`
  before swapping — an invalid reload should log and keep the old config
  running, never crash or serve nothing.

## Phase C — Runtime route management API

- `POST /routes`, `DELETE /routes/{host}` to add/remove routes without
  touching the JSON file or restarting.
- **Must live on a separate, authenticated admin listener** — never on the
  public `:80`/`:443` alongside proxied traffic. This is a control-plane
  surface: whoever can call it can redirect any protected route's traffic
  anywhere.
- Auth for this API: a bearer token (simplest, config-supplied secret) is
  enough for v1 — this doesn't need OIDC, it's a single operator (you)
  managing your own box.
- Changes made at runtime should be persisted back to the config file (or
  a separate state file) so a restart doesn't lose them — decide which
  before building, don't bolt it on after.

## Phase D — OIDC phase 2: homemade identity provider (stretch, curiosity-driven)

The part that made this project interesting in the first place: Switchboard
becomes its own OIDC **provider**, not just a client of someone else's.

- Own user store (start with a simple embedded store — SQLite via
  `database/sql` + a driver, or even a flat file for v0 — don't reach for
  Postgres until there's a real reason).
- Credential handling: password hashing (bcrypt/argon2), never anything
  home-rolled for the actual hashing primitive itself (same rule as
  everywhere else — stdlib/well-known libs for crypto, custom code only
  for the protocol glue around it).
- Own token issuance: signing keys (rotate them, don't hardcode one
  forever), a `/.well-known/openid-configuration` + `/jwks` of its own,
  an authorization endpoint, a token endpoint.
- **Security bar for this phase is higher than everything else in this
  repo.** Treat it exactly like the Sigea projects' "moonlighter" rule:
  build it for real, with tests for every credential/token path, but don't
  point anything that matters (a route with real data behind it) at this
  as its only auth until it's had real scrutiny — dogfood it on a
  throwaway route first.
- Good reference points already identified: [Dex](https://github.com/dexidp/dex)
  (closest in spirit — Go, lightweight), [Ory Hydra](https://github.com/ory/hydra)
  (pure OAuth2/OIDC server, no user store), [Zitadel](https://github.com/zitadel/zitadel)
  (full IAM, useful as a feature reference even if way bigger in scope).

## Phase E — Distribution via npm

- Real, proven pattern (esbuild, swc, turbo, and `@playwright/cli` — which
  this session installed and used directly — all work this way): the
  actual binary is native, npm is just a discovery/install layer.
- Mechanics:
  1. [goreleaser](https://goreleaser.com) cross-compiles the Go binary for
     Linux/macOS/Windows × amd64/arm64 and publishes them as GitHub Release
     assets on each tag.
  2. A small npm package (`postinstall` script, ~50 lines of Node) detects
     the user's OS/arch and downloads the matching binary from that
     release.
  3. Result: `npx switchboard` or `npm install -g switchboard` works for
     anyone with Node installed, no Go toolchain required.
- Purely a packaging layer — doesn't change the runtime, doesn't compromise
  the "one Go binary" story once actually installed.

---

## Already done (for context, not re-litigated)

- Host-based reverse proxy (`net/http/httputil`), structured logging,
  graceful shutdown.
- Background health checks per upstream (`healthcheck.Checker`); an
  unhealthy upstream gets a 503 instead of being proxied to.
- Automatic TLS via ACME (`golang.org/x/crypto/acme/autocert`): `:443` for
  HTTPS, `:80` for HTTP-01 challenges + redirect.
- OIDC login phase 1 (delegated to an external provider): per-route
  `auth_required`, authorization code + PKCE, JWKS-verified ID tokens,
  opaque server-side sessions, multi-host support via `cookie_domain`,
  open-redirect guard on `return_to`. Tested end-to-end against a fake
  in-process OIDC provider with a real signed JWT; real discovery
  confirmed against Google; not yet tried against a real provider live in
  a browser.
- 32 tests across `config`, `healthcheck`, `proxy`, and `auth`; `go vet`
  clean throughout.
