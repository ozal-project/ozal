# Implemented — Switchboard

**Last review:** 2026-10-17
**Code reviewed up to:** `c24b50c`

Closed work and its evidence. Active work, blockers and pending tasks live in
[`TO_IMPLEMENT.md`](TO_IMPLEMENT.md). Do not add open tasks here.

Entries before the tracker existed (September 2026) are reconstructed from git
history and grouped by the original phases.

## Foundation (2026-09-21)

| Commit | Result |
|---|---|
| `473faa8` | Host-based reverse proxy with tests: one `httputil.ReverseProxy` per route, keyed by `Host`. |
| `d00464b` | Background health checks (unhealthy upstream → `503`) and automatic TLS via ACME (`autocert`) with `:80` challenge + HTTPS redirect. |
| `38b6d60` | OIDC login delegated to an external provider: discovery, authorization code + PKCE, ID-token verification, opaque server-side sessions, open-redirect guard for `return_to`. |

## Phase 1 — core hardening (2026-09-21)

| Commit | Result |
|---|---|
| `b228ad1` | `ReadHeaderTimeout`/`IdleTimeout` on every listener; session store sweep + 10k ceiling (`ErrStoreFull` → `503`); MIT `LICENSE`. |
| `ede51cc` | Pending-login store: independent sweep and 10k ceiling. |
| `3997491` | `Principal`, `AccessPolicy` and a pure `Authorize` with stable decision codes. |
| `6268a0b` | Session stores the full `Principal`, extracted during the OIDC callback. |
| `7c9a8c4` | Per-route claim authorization (emails, domains, groups; verified email required), strict route validation (duplicates, http/https targets, normalized hosts, cookie-domain coverage), verified identity headers upstream with client values stripped. |

## Phase 2 — transactional runtime (2026-09-21)

| Commit | Result |
|---|---|
| `b57bedd` | `routemanager`: candidate config validated and fully built before one `atomic.Pointer` swap; failed candidates keep the last-good snapshot. Listener/TLS changes rejected on reload. |

## Phase 3 — control plane (2026-09-21)

| Commit | Result |
|---|---|
| `1ab05e8` | Loopback-only admin listener: `/_/status` dashboard (`html/template`, CSP), bearer-token `POST /routes`, `DELETE /routes/{host}`, `POST /_/reload`; atomic persistence to the config file; audit lines; `SIGHUP` reload through the same path; per-route health detail. |
| `1822e51` | Scope and goals clarified in README and roadmap. |

## Documentation and process (2026-10-07)

| Commit | Result |
|---|---|
| _uncommitted_ | Full audit (findings now `SEC-001`–`SEC-005`, `BUG-001`–`BUG-008`); `VISION.md`; `AGENTS.md`; mentor skill; design pattern, stack and tracker docs; `scripts/verify.sh`. |

## Security findings (2026-10-17)

| Commit | Result |
|---|---|
| `2a7f000` | `SEC-005`: the `SessionStore` is owned by the route manager and reused across gate rebuilds, so adding a protected route no longer logs everyone out. A changed session TTL is a policy change and builds a fresh store (promoted only when the candidate installs; discarded candidates cancel their own sweep). New test: a session created before the rebuild authenticates on the new gate. |
| `a385806` | `SEC-004`: the login attempt (state, PKCE verifier, return_to) travels in a short-lived HMAC-SHA256-signed `sb_login` cookie instead of a server-side store; the signing key is derived from the client secret with a domain separator. The callback requires the cookie, matches `state` in constant time and clears the cookie on first use; missing, mismatched, forged, tampered and expired cookies all get `400`. The pending store, its sweep and its 10k capacity error are gone. |
| `4297810` | `SEC-003`: the admin listener answers only loopback hosts. A `Host` header that is not 127.0.0.0/8, `::1` or `localhost` (with/without port, trailing dot) gets `421` before the mux, so DNS rebinding cannot read `/_/status` or reach mutations. Table tests + rebound-request regression. |
| `1868ab1` | `SEC-002`: every request header whose name normalizes to an identity header (case- and dash/underscore-insensitive) is deleted before routing, including non-canonical keys written straight into the map. Raw-socket regression test with `X_Auth_Request_Email`/`X_Auth_Request_User` on public and protected routes; proven to fail with the previous exact-name deletion. |
| `b249f83` | `SEC-001`: the proxy strips Switchboard's `sb_session` cookie from every request before it reaches an upstream, preserving the other cookies (and removing the header when nothing is left). Pure `stripCookie` + table tests; e2e tests on public and protected routes; proven to fail without the wiring. |

## Defects (2026-10-17)

| Commit | Result |
|---|---|
| `50a0f2c` | `BUG-007`: README now says what the code does: `return_to` is honored only for **protected** route hosts (plus docs on endpoint scoping and POST logout from `BUG-005`). Code was the intended behavior. |
| `8c5568e` | `BUG-008`: with `dev_errors: true` (config, off by default) a 404 lists the configured hosts and a 502 names the unreachable upstream with a hint; without it the production errors stay terse and leak nothing. Proxy `Option` (`WithDevErrors`) keeps the flag out of the default path; tests for both modes. |
| `dd200d6` | `BUG-006`: `WriteAtomic` resolves a symlinked config path first, so the rename replaces the target and the link survives; after the rename it fsyncs the directory (ignored where unsupported). Test: writing through a symlink keeps it a symlink and updates the target. |
| `8d48458` | `BUG-005`: `/_/auth/*` is answered only on hosts where it belongs (protected routes plus the redirect_url host); on any other host the path reaches the upstream. Logout is POST-only (`405` with `Allow: POST` otherwise) and redirects with `303`. Tests cover public passthrough, protected login, and GET/POST logout. |
| `0af2bf4` | `BUG-004`: the admin bearer parser requires the `Bearer` scheme (case-insensitive per RFC 7235); a bare token, a wrong scheme or an empty token get `401`. Table test covers all six forms. |
| `f347c5f` | `BUG-003`: the plain and TLS public listeners bind synchronously (`net.Listen`) before serving, like the admin listener; an occupied port now fails startup with a clear error instead of a `log.Fatalf` inside a goroutine that skips shutdown. Tests: busy port returns an error, free port serves. |
| `d873374` | `BUG-002`: `health_path` must start with `/` (validated at load), and the probe URL is built with `url.URL.JoinPath`, so a target with a base path (`http://host/prefix`) probes `/prefix/healthz` instead of double slashes or a broken relative URL. |
| `f7e22e9` | `BUG-001`: the reverse proxy uses `Rewrite` (`SetURL` + `SetXForwarded`): `X-Forwarded-For` is replaced with the client IP (a client-supplied chain is dropped, not appended), `X-Forwarded-Host`/`X-Forwarded-Proto` are set by Switchboard, and the public `Host` is preserved for the upstream. Tests cover plain HTTP and TLS; proven to fail with the old `NewSingleHostReverseProxy`. |

## Request pipeline (2026-10-17)

| Commit | Result |
|---|---|
| `08738fe` | `ARCH-001`: `internal/pipeline` owns the stage order from `design.md` (trust → ratelimit → shield → identify → authorize → route → forward) and the `Trust` stage strips spoofed identity headers before anything reads the request. The session-cookie strip moved from the request entry to the outbound clone (`ReverseProxy.Rewrite`): the identify stage must read `sb_session`, the upstream must not — a real ordering bug (login loop) that the pilot's cookie-reading gate test caught. `Chain` order test plus gate-visible/upstream-hidden cookie test. |

## Trusted-proxy client IP (2026-10-17)

| Commit | Result |
|---|---|
| _uncommitted_ | `EDGE-005`: opt-in `trusted_proxies` IP/CIDR allowlist, normalized/validated and deep-cloned with config. Trust accepts one valid `CF-Connecting-IP` only from a configured TCP peer, strips forwarding-IP headers/aliases, stores the selected `netip.Addr` under a private context key and preserves `RemoteAddr`. The outbound proxy rebuilds one `X-Forwarded-For`; edge-cookie stripping is preserved. `config.tunnel.example.json` serves only Portfolio on `preview.` with loopback listeners and `dev_errors: false`; the live apex stays on Cloudflare Pages. |

Verification: `scripts/verify.sh` green (gofmt, vet, all tests with `-race`,
build, diff check), including concurrent event changes present at the time.
Optional staticcheck/govulncheck were not installed. Table and integration tests
cover trusted/untrusted/absent/malformed/duplicate/scoped headers, IPv4/IPv6 and
mapped addresses, spoofed chains, original-peer preservation, immutable snapshots,
config persistence and rejected reloads. A compiler overlay removing the trusted
stage made the trusted-peer regression fail; no shared source was mutated.

Discardable binary smoke: actual TCP sources `127.0.0.1` (trusted) and
`127.0.0.2` (untrusted) produce the expected single upstream address; raw
CF/forwarding headers and `sb_session` never reach the echo upstream. Fresh
Portfolio `/` and `/es/` and hashed assets return 200 through Switchboard;
unknown host, hidden path and traversal return 404 with production errors.
Browser smoke confirmed bilingual project states with no JS errors observed.
All temporary listeners/processes were stopped; no tunnel, DNS or public
site was changed. Credentials and restart-safe deployment remain in `EDGE-001`.

## Events (2026-10-17)

| Commit | Result |
|---|---|
| `5de6ebf` | `ARCH-002`: `internal/events` defines stable event types (`access.allowed`/`denied`, `login.started`/`failed`/`succeeded`, `route.created`/`deleted`, `config.reloaded`), a `Sink` interface, a JSON `slog` sink and an in-memory `Recorder`. Access decisions, the login flow and admin/SIGHUP changes publish events, replacing the `log.Printf` audit lines. Tests capture events with the recorder (stable types asserted). |

## Secrets (2026-10-17)

| Commit | Result |
|---|---|
| `4659f5a` | `ARCH-003`: `auth.client_secret` and `admin.token` accept `env:NAME` / `file:/path` references. Validation checks the reference shape without reading it; the manager resolves a copy for the OIDC gate (unresolved → startup error) and the admin resolves per mutation (unresolved → `503`). Control-plane writes persist the raw reference — a test proves the resolved value never reaches the config file. Inline values remain local-only and documented as such. |

## Release gates (2026-10-17)

| Commit | Result |
|---|---|
| `c24b50c` | `REL-004`: `QUICKSTART.md` + `config.quickstart.json` take a clean checkout to a demo upstream, a routed host, the local error page and the read-only status page. Followed end-to-end locally (route body, 404 route list, status 200, admin 421 on a non-loopback host, no leftover listeners). |
| `3713c5b` | `REL-002`: fuzz targets for `config.Parse` (round-trip invariant), `NormalizeHost` (idempotent), `safeReturnTo` (only allowed absolute HTTP(S) hosts, fixed point) and `stripCookie` (target never survives, idempotent). Seed corpora run with `go test`; `scripts/verify.sh` adds a 1s fuzz pass per target. |
| `4659f5a` | `REL-001`: `auth.client_secret` and `admin.token` come from `env:`/`file:` references (inline values documented as local-only). Tests cover both sources, an unresolved reference (startup error / `503` on mutations) and that control-plane writes never persist the resolved value. |

## SQLite store (2026-10-17)

| Commit | Result |
|---|---|
| `7d0c56e` | Embedded SQLite store with migration runner (`internal/store/sqlite/`, `cmd/switchboard migrate`). Numbered migrations and a session schema; not yet wired into `auth` (tracked as `ARCH-004`). |

## Pages (2026-10-17)

| Commit | Result |
|---|---|
| `35bfa9c` | `PAGES-001`: static routes (`"root"`, optional `"spa"`). Pure `static.Resolve` (table tests + fuzz target) plus a handler that opens files through `os.OpenInRoot`; no listings, hidden files refused except `.well-known`, directory redirects built from the cleaned path (no `//host` open redirect). Regression test proven to fail with plain `os.Open` (symlink escape). Access policies apply to static routes. Smoke test: the portfolio `dist/` served through Switchboard. |

Re-verified 2026-10-17 (`CROSS-003`): `scripts/verify.sh` green; fresh
`portfolio` build served as a static route — `/` 200, `/es/` 200, hashed asset
200; unknown path, `.env`, traversal attempt and unknown host all 404.

## Local outbound tunnel spike (2026-10-17)

| Commit | Result |
|---|---|
| `fd72b2c` | `TUN-001`: `tunnel-spike init/edge/connect` runs both roles locally with TLS 1.3 mutual authentication, exact connector SAN authorization and yamux streams. Fixed static/HTTP route; loopback-only lab CLI; duplicate rejection; bounded handshakes/requests; context cancellation; explicit lifecycle events. Existing proxy pipeline reused via an optional outbound `http.RoundTripper`. |

Observed: targeted race tests and the full `scripts/verify.sh` passed; separate
real processes served Portfolio ES/EN and assets. Foreign CA/connector identity,
expired certificate, wrong EKU, missing certificate/ALPN and wrong edge identity
were rejected. Abrupt disconnect produced 502; manual reconnect restored service;
a request cancellation left the session usable. A compiler overlay removing the
exact identity check makes its regression fail. Temporary processes/listeners and
lab identities were removed. Optional staticcheck/govulncheck are not installed.

The first gate stopped at a concurrent unfinished rate-limit exercise; that work
was preserved and later completed, after which the full gate was repeated green.
No VPS, live credential, DNS change or deployment was needed. See the
[lab guide and findings](architecture/tunnel-local-spike.md). Automatic reconnect,
configuration/enrolment/revocation and visitor attribution are residual TUN-002;
actual public deployment remains EDGE-001. Code committed in `fd72b2c`; no push
or public deployment made.

TUN-001 documentation follow-up (2026-10-17, included in `fd72b2c`): both lab listeners
now default to `127.0.0.1:0`. The existing CLI lifecycle test starts the edge
without a `-listen` override. The lab examples use the printed allocated ports;
the README labels Cloudflare deployment as optional. Full `scripts/verify.sh`
was repeated successfully; documentation links and diff whitespace checked.

## Commit checkpoint (2026-10-17)

- `6185098`: per-IP rate limiting and stricter authentication-path bucket;
  residual identity/route dimensions remain in SHD-001.
- `b2602e9`: CI workflow written for Linux/macOS/Windows; REL-003 stays open
  until a green hosted PR run is observed. Local success is not hosted CI.
- `e432abf`: broker and admin SSE activity delivery; UI-001 remains open for
  its documented filters and protected-route acceptance.
- `fd72b2c`: TUN-001 lab, including ephemeral default listeners and real-process
  Portfolio smoke.

Verification repeated before this checkpoint: `scripts/verify.sh` passed
(format, vet, all race tests, build, four fuzz targets, diff check); real-process
Portfolio ES/EN/assets, duplicate/foreign-CA rejection, abrupt disconnect and
manual reconnect smoke passed. No services, DNS or deployments were changed.
Optional staticcheck/govulncheck are not installed. All source deliveries above
are local commits on `spike/tun-001-local`; no push in this checkpoint.


## Console layout and filters (2026-10-17, included in this commit)

Completed delivery within UI-001: shared navigation, default activity entry,
category/search filters, pause/resume, safe access-rule overview, explicit empty
and reconnect/error states, and expandable route-debug probes. Static sites are
labelled Not probed. Assets are embedded and served from the same origin under
CSP; overview tests verify that secrets and private configuration fields stay
out. The remaining UI-001 acceptance is tracked only in TO_IMPLEMENT.md.

Observed: full `scripts/verify.sh` passed on the final assets; Node syntax check
passed; browser checks exercised filters, paused snapshots, resume, error/retry,
inert HTML-looking event text, keyboard focus and real SSE from an isolated
SIGHUP reload. Activity and route-debug layouts had no document overflow at
320/768/1024/1440 px. Axe reported zero automated WCAG A/AA violations; glyph
contrast checks were incomplete and were reviewed against the color tokens.

The local binary was rebuilt and only `switchboard-dev.service` was restarted.
It is active; `/`, activity, route-debug, CSS, JS and overview returned 200.
A real-browser smoke confirmed the new console on loopback 9090 and the Server
Ops panel on loopback 8095. The remote user's 8095 forwarding remains a client
step. No bind expansion or public deployment was performed. This code checkpoint is committed; no push was performed.


Visual follow-up (2026-10-17, included in this commit): dark/amber access-console
identity, self-hosted Manrope with OFL license, desktop sidebar/mobile tabs and
Routes & hostnames copy separating local names from published DNS. Same-origin
font-src was added to CSP. Full gate passed; browser layouts fit 320/768/1024/1440
px and automated axe reported zero WCAG A/AA violations, with empty-table/glyph
checks incomplete. The local development unit was rebuilt/restarted and both
console pages plus the font returned 200. Server Ops owns the separate launcher
and SSH metadata in ports.json; no port registry is copied into Switchboard.
