# To implement — Switchboard

**Last review:** 2026-10-17
**Code reviewed up to:** `1868ab1`
**Purpose:** the single source of active work, priorities, blockers and done
criteria. Closed work moves to [`IMPLEMENTED.md`](IMPLEMENTED.md). Direction
and the long-range map live in [`VISION.md`](../VISION.md); if another
document contradicts the state of a task, this tracker wins and is updated
with the change.

## How to maintain this document

States:

- **Ready** — can be implemented with current decisions and code.
- **In progress** — started; not all done criteria met. Link the branch/PR.
- **Verify** — reported earlier but code changed; reproduce before fixing.
- **Blocked** — needs a decision, account or resource not yet available.
- **Planned** — belongs to a later release; not refined yet.

When starting a task, set it to **In progress**. When done, remove it from the
active tables and add it to [`IMPLEMENTED.md`](IMPLEMENTED.md) with commit,
date and evidence, plus one line in
[`learning/LEARNING_LOG.md`](learning/LEARNING_LOG.md). Never duplicate a
task in two places.

ID prefixes: `SEC` security finding · `BUG` defect · `REL` release hygiene ·
`ARCH` architecture · `EDGE` edge/deployment · `TUN` tunnels · `ID` identity ·
`SHD` Shield/WAF · `NTF` notifications · `PKI` private CA · `ACC`
infrastructure access · `TLOG` transparency log · `AI` AI-assisted decisions ·
`AGT` AI-agent gateway · `UI` dashboard · `PAGES` static sites.

## Recommended order

1. `SEC-001`–`SEC-005`: the edge will be public; known holes first.
2. `ARCH-001`–`ARCH-002`: put the pipeline and event sink in place so every
   later feature plugs into one order of checks.
3. `REL-*`: secrets from env/file, fuzzing, CI, quickstart → tag `v0.1.0-alpha.1`.
4. `TUN-*` spike in parallel with the edge decisions (`EDGE-001`/`002` are
   blocked on VPS and DNS).
5. Identity (`ID-*`), then `NTF-001`, `SHD-001`.

---

## Release v0.1 · Edge (October–December 2026)

### Security findings (audit 2026-10-07)

| ID | Task | State | Done when |
|---|---|---|---|
| `SEC-003` | Admin listener has no `Host` check → DNS rebinding can read `/_/status`. Accept only loopback hosts. | Ready | Test: request with `Host: evil.example` gets `421`/`403`; loopback hosts work. |
| `SEC-004` | Login CSRF and pending-store exhaustion: OIDC `state` is not bound to the browser; 10k `/_/auth/login` hits block real logins. Move state + PKCE verifier + `return_to` into a short-lived HMAC-signed cookie. | Ready | Callback without the matching cookie fails; no server-side pending store; tests for replay, expiry, tampering. |
| `SEC-005` | Rebuilding the OIDC gate (e.g. adding a protected route) creates a new session store and logs everyone out. Session store must outlive gate rebuilds. | Ready | Test: sessions survive a reload that changes protected hosts. |

### Defects

| ID | Task | State | Done when |
|---|---|---|---|
| `BUG-001` | Upstreams receive no `X-Forwarded-Proto`/`X-Forwarded-Host`, and client `X-Forwarded-For` is passed through. Migrate to `ReverseProxy.Rewrite` with `SetURL` + `SetXForwarded`. | Ready | Test asserts forwarded headers are set by Switchboard and client values are replaced. |
| `BUG-002` | `health_path` is not validated (`"healthz"` without `/` builds a broken URL; target paths produce `//`). | Ready | Validation rejects relative paths; URL joined with `url.URL.JoinPath`; tests. |
| `BUG-003` | Public listener bind errors surface late via `log.Fatalf` in a goroutine, skipping shutdown. Listen synchronously like the admin listener. | Ready | Port-in-use fails at startup with a clear error. |
| `BUG-004` | Bearer parsing accepts a bare token without the `Bearer ` prefix. | Ready | Test: header without prefix gets `401`. |
| `BUG-005` | `/_/auth/*` is intercepted on every host, shadowing upstream paths on public routes; logout is a GET (logout CSRF). | Ready | Auth endpoints only on hosts that need them; logout via POST; tests. |
| `BUG-006` | `WriteAtomic` replaces a symlinked config with a regular file and skips directory fsync. | Ready | Resolves symlinks before rename; fsyncs the directory; test with a symlinked config. |
| `BUG-007` | README says `return_to` accepts any route host; code accepts only protected hosts. | Ready | Docs and code agree (decide which is intended). |
| `BUG-008` | Upstream down → bare empty `502`; unknown host → plain `404`. | Ready | Friendly local-dev error pages (upstream address, hint, route list on 404) behind a flag safe for production. |

### Architecture

| ID | Task | State | Done when |
|---|---|---|---|
| `ARCH-001` | Introduce the request pipeline (`internal/pipeline`) with the stage order from `docs/architecture/design.md`; move header stripping into the `trust` stage. | Ready | Pipeline order defined in one function; end-to-end test proves order. |
| `ARCH-002` | `EventSink` interface + `log/slog` JSON sink; decisions, logins and admin changes emit events (replaces `log.Printf` audit lines). | Ready | Events have stable types; tests capture them with an in-memory sink. |
| `ARCH-003` | Raw vs resolved config, so secrets from env/files are never written back. | Ready | Control-plane writes preserve secret references; test. Prerequisite of `REL-001`. |
| `ARCH-004` | Embedded SQLite store with migration runner (sessions, later tunnels/passkeys). Store and runner are committed (`7d0c56e`, `cmd/switchboard migrate`); wiring into `auth`/sessions is still pending. | In progress | Sessions survive restart; migrations numbered and immutable. |
| `ARCH-005` | Remove duplicated clone helpers in `routemanager` (`cloneRoute`, `cloneConfig` wrapper). | Ready | Single `config.Clone`; tests pass. |

### Alpha release gates

| ID | Task | State | Done when |
|---|---|---|---|
| `REL-001` | `auth.client_secret` and `admin.token` from env or file references; inline values documented as local-only. | Ready (after `ARCH-003`) | Tests for each source and for never persisting resolved values. |
| `REL-002` | Fuzz targets: config parsing, `NormalizeHost`, `safeReturnTo`, cookie stripping. | Ready | Fuzz targets with seed corpora run in `scripts/verify.sh` (short mode). |
| `REL-003` | CI on GitHub Actions: `scripts/verify.sh` on Linux, macOS, Windows. | Ready | Green on a pull request. |
| `REL-004` | Copy-pasteable quickstart reaching a routed app and the status page. | Ready | Followed from a clean machine. |
| `REL-005` | GoReleaser config, checksums, SBOM. | Ready | Snapshot build produces artifacts locally. |
| `REL-006` | Tag `v0.1.0-alpha.1` after every gate above is green. | Blocked on `REL-001`–`REL-005` | Tag pushed with release notes. |

### Edge, tunnels, identity, Shield, notifications

| ID | Task | State | Done when |
|---|---|---|---|
| `EDGE-001` | VPS provisioned, systemd unit, Switchboard serving `octaviosantacruz.com` subdomains. | Blocked: choose VPS | Edge reachable; restart-safe; documented. |
| `EDGE-002` | Wildcard TLS via DNS-01 (CertMagic + libdns provider). | Blocked: choose DNS provider | `*.share.` and `*.apps.` certificates issued and renewed. |
| `EDGE-003` | Route hosts without a port match any listener port (local-dev ergonomics). | Ready | `app.localhost` route works on `:8888`; tests. |
| `EDGE-004` | Multiple upstreams per route, round-robin, active + passive health. | Planned | Failed upstream receives no new traffic; tests. |
| `TUN-001` | Spike: agent ↔ edge over yamux + TLS; one HTTP request through a tunnel. | Ready | Throwaway branch; findings written in `docs/architecture/`. |
| `TUN-002` | `sb connect --name <n> <url>`: named, persistent tunnels with tokens, reconnect, heartbeats. | Planned | Survives edge restart and network drop; tests. |
| `TUN-003` | `sb share <url> --allow <emails>`: ephemeral protected shares; guest list enforced on the agent via signed assertion (Ed25519). | Planned | Share dies with the agent; non-listed email denied. |
| `ID-001` | Email one-time codes / magic links. | Planned | Rate-limited; codes single-use and short-lived. |
| `ID-002` | Email delivery provider. | Blocked: choose provider | Decision recorded in `stack.md`. |
| `ID-003` | Passkeys (WebAuthn) registration and login. | Planned | Works on desktop and phone; step-up supported. |
| `ID-004` | OIDC provider: discovery, JWKS, key rotation, authorization code + PKCE. | Planned | A sample app signs in via Switchboard. |
| `SHD-001` | Rate limiting stage (per IP, identity, route; strict on `/_/auth/*`). | Ready (after `ARCH-001`) | Table tests on the core bucket; integration test returns `429`. |
| `SHD-002` | Security headers on Switchboard's own pages (HSTS, CSP, frame-ancestors). | Ready | Tests assert headers. |
| `NTF-001` | `Notifier` port + Telegram adapter: alerts for logins and admin changes; chat pairing code. | Planned | Pairing and an alert work end to end; callback data signed. |

### Pages (static sites)

Switchboard publishes static sites the way Cloudflare Pages does: a site is a
route whose upstream is a directory instead of a URL, deployments are
immutable and swapped atomically, and later a push builds and deploys.

| ID | Task | State | Done when |
|---|---|---|---|
| `PAGES-002` | Deployments: `switchboard deploy --site <name> ./dist` uploads an archive to the admin API; stored content-addressed under `sites/<name>/<hash>/`; the live pointer switches atomically; `rollback` and retention of the last N. | Planned (after `PAGES-001`) | A request never sees a half-written site; rollback is instant; archive extraction rejects absolute paths, `..`, links and size bombs; tests. |
| `PAGES-003` | `_headers` and `_redirects` files in the site, Cloudflare Pages syntax subset. | Planned | Parser with fuzz target; security headers and redirects applied; tests. |
| `PAGES-004` | Git builds: GitHub webhook verified with HMAC-SHA256, checkout of the pushed commit, build command run unprivileged with timeout and no access to Switchboard secrets, output deployed through `PAGES-002`. | Planned | Unsigned or replayed webhooks rejected; a failing build leaves the live site untouched; build logs kept per deployment. |
| `PAGES-005` | Preview deployments per branch at `<branch>.<site>.<domain>`, protected by an access policy by default. | Planned (after `PAGES-004`) | Preview URL per push; production unchanged until `main` builds. |

### Dashboard

Pure Go first, Svelte only where a feature needs it (see
[`stack.md`](architecture/stack.md)).

| ID | Task | State | Done when |
|---|---|---|---|
| `UI-001` | Live event stream on the admin dashboard over Server-Sent Events, in pure Go (`html/template` + HTMX, `http.Flusher`, fan-out from the `EventSink`). | Ready (after `ARCH-002`) | Events appear live; a slow or disconnected client never blocks the sink; the stream goroutine ends when the request context is cancelled; tests. |

---

## Later releases (planned, refined at the start of each quarter)

| Release | IDs | Scope |
|---|---|---|
| v0.2 PKI (Jan–Mar 2027) | `PKI-*`, `SHD-0xx`, `UI-002` | ACME server, internal CA, mTLS agent/origin, developer certs, SSH CA, workload identity; rules language + compiler, PoW challenge; `UI-002`: JSON admin API + first Svelte island, a policy simulator over the pure `auth.Authorize` (blocked on `UI-001`) |
| v0.3 Access (Apr–Jun 2027) | `ACC-*`, `NTF-*`, `SHD-0xx` | SSH/DB over tunnels, JIT with Telegram approvals, session recording, credential injection, CAEP/SSF, SCIM; Coraza + CRS, API schema validation, leaked credentials, CSP reporting |
| v0.4 Trust (Jul–Sep 2027) | `TLOG-*`, `AI-*`, `AGT-*`, `SHD-0xx` | Transparency log, witnesses, risk signals and explanations, AI-agent/MCP gateway; Web Bot Auth, Firewall for AI, DLP |

Details for each are in [`VISION.md`](../VISION.md).
