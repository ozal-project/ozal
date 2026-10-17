# Implemented — Switchboard

**Last review:** 2026-10-17
**Code reviewed up to:** `2a7f000`

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
| _uncommitted_ | `BUG-007`: README now says what the code does: `return_to` is honored only for **protected** route hosts (plus docs on endpoint scoping and POST logout from `BUG-005`). Code was the intended behavior. |
| _uncommitted_ | `BUG-008`: with `dev_errors: true` (config, off by default) a 404 lists the configured hosts and a 502 names the unreachable upstream with a hint; without it the production errors stay terse and leak nothing. Proxy `Option` (`WithDevErrors`) keeps the flag out of the default path; tests for both modes. |
| _uncommitted_ | `BUG-006`: `WriteAtomic` resolves a symlinked config path first, so the rename replaces the target and the link survives; after the rename it fsyncs the directory (ignored where unsupported). Test: writing through a symlink keeps it a symlink and updates the target. |
| _uncommitted_ | `BUG-005`: `/_/auth/*` is answered only on hosts where it belongs (protected routes plus the redirect_url host); on any other host the path reaches the upstream. Logout is POST-only (`405` with `Allow: POST` otherwise) and redirects with `303`. Tests cover public passthrough, protected login, and GET/POST logout. |
| _uncommitted_ | `BUG-004`: the admin bearer parser requires the `Bearer` scheme (case-insensitive per RFC 7235); a bare token, a wrong scheme or an empty token get `401`. Table test covers all six forms. |
| _uncommitted_ | `BUG-003`: the plain and TLS public listeners bind synchronously (`net.Listen`) before serving, like the admin listener; an occupied port now fails startup with a clear error instead of a `log.Fatalf` inside a goroutine that skips shutdown. Tests: busy port returns an error, free port serves. |
| _uncommitted_ | `BUG-002`: `health_path` must start with `/` (validated at load), and the probe URL is built with `url.URL.JoinPath`, so a target with a base path (`http://host/prefix`) probes `/prefix/healthz` instead of double slashes or a broken relative URL. |
| _uncommitted_ | `BUG-001`: the reverse proxy uses `Rewrite` (`SetURL` + `SetXForwarded`): `X-Forwarded-For` is replaced with the client IP (a client-supplied chain is dropped, not appended), `X-Forwarded-Host`/`X-Forwarded-Proto` are set by Switchboard, and the public `Host` is preserved for the upstream. Tests cover plain HTTP and TLS; proven to fail with the old `NewSingleHostReverseProxy`. |

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
