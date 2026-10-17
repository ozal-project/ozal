# Implemented — Switchboard

**Last review:** 2026-10-07
**Code reviewed up to:** `1822e51`

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

## Pages (2026-10-17)

| Commit | Result |
|---|---|
| _uncommitted_ | `PAGES-001`: static routes (`"root"`, optional `"spa"`). Pure `static.Resolve` (table tests + fuzz target) plus a handler that opens files through `os.OpenInRoot`; no listings, hidden files refused except `.well-known`, directory redirects built from the cleaned path (no `//host` open redirect). Regression test proven to fail with plain `os.Open` (symlink escape). Access policies apply to static routes. Smoke test: the portfolio `dist/` served through Switchboard. |
