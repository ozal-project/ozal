# To implement — Switchboard

**Last review:** 2026-10-17
**Code reviewed up to:** `e432abf`
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

Every `SEC-*` and `BUG-*` finding is closed (2026-10-17).

1. `REL-003` (workflow committed in `b2602e9`, awaiting first green PR), `REL-005`–`REL-006`: CI, GoReleaser → tag `v0.1.0-alpha.1`.<br>`REL-001`–`REL-002` and `REL-004` closed (secret references, fuzz, quickstart).
2. `TUN-001` local spike is verified and committed (`fd72b2c`); continue with `TUN-002`
   for durable named connections, still without a VPS. `SHD-001` remains a gate before
   public exposure. `EDGE-001` public deployment waits for an external host;
   Cloudflare Tunnel is an optional transport, not a development prerequisite.
3. Identity (`ID-*`), then `NTF-001`, `SHD-001`.

---

## Release v0.1 · Edge (October–December 2026)

### Architecture

| ID | Task | State | Done when |
|---|---|---|---|
| `ARCH-004` | Embedded SQLite store with migration runner (sessions, later tunnels/passkeys). Store and runner are committed (`7d0c56e`, `cmd/switchboard migrate`); wiring into `auth`/sessions is still pending. | In progress | Sessions survive restart; migrations numbered and immutable. |
| `ARCH-005` | Remove duplicated clone helpers in `routemanager` (`cloneRoute`, `cloneConfig` wrapper). | Ready | Single `config.Clone`; tests pass. |

### Alpha release gates

| ID | Task | State | Done when |
|---|---|---|---|
| `REL-003` | CI on GitHub Actions: `scripts/verify.sh` on Linux, macOS, Windows. Workflow is `.github/workflows/ci.yml` (commit `b2602e9`); hosted CI has not been observed. | In progress | Green on a pull request. |
| `REL-005` | GoReleaser config, checksums, SBOM. | Ready | Snapshot build produces artifacts locally. |
| `REL-006` | Tag `v0.1.0-alpha.1` after every gate above is green. | Blocked on `REL-002`–`REL-005` | Tag pushed with release notes. |

### Edge, tunnels, identity, Shield, notifications

| ID | Task | State | Done when |
|---|---|---|---|
| `EDGE-001` | Public Switchboard edge on an external host with an outbound connector from the homelab. Cloudflare Tunnel remains an [optional alternative](architecture/edge-cloudflared.md). | Public deployment deferred: no VPS budget; local TUN-001 development is unblocked. Requires TUN-002 and exposure gates. | Portfolio reachable via the owned edge/tunnel; restart-safe; no inbound homelab port or public DNS pointing at the residential IP. Actual DNS cutover is a separate operator action. |
| `EDGE-002` | Wildcard TLS via DNS-01 (CertMagic + libdns provider). | Planned for wildcard hosts on an owned edge; not needed for the local lab | Wildcard issuance/renewal verified before wildcard public deployment. Existing single-host ACME remains an option. |
| `EDGE-003` | Route hosts without a port match any listener port (local-dev ergonomics). | Ready | `app.localhost` route works on `:8888`; tests. |
| `EDGE-004` | Multiple upstreams per route, round-robin, active + passive health. | Planned | Failed upstream receives no new traffic; tests. |
| `TUN-002` | Named persistent connectors: configuration/reload integration, authenticated enrolment/revocation, automatic reconnect/backoff, heartbeats, status and an explicit visitor-identity propagation contract. Reuse the local TUN-001 transport. | In progress. Verified checkpoint in this commit; `scripts/verify.sh` rerun green (2026-10-17): migration `002_create_connectors` (enrolment by leaf-certificate SHA-256, revocation keeps history); `tunnel.Registry` + `switchboard connector enroll\|revoke\|list`; `Edge` with named connectors, registry authorization and revocation that closes live sessions (revalidation every 5s); `ConnectLoop` with jittered exponential backoff and observable `Link` status; tests for enrol/revoke/unenrolled-cert/edge-restart reattach. **Remaining:** edge/connector commands that use the registry and a persistent identity (the spike CLI is still lab-only), config/reload and admin integration (connector status on the activity UI, host→connector routes), visitor identity/IP assertion contract, key/certificate rotation. | Survives edge restart and network drop without manual restart; identities and fixed upstream permissions enforced; tests. Public deployment remains EDGE-001. |
| `TUN-003` | `sb share <url> --allow <emails>`: ephemeral protected shares; guest list enforced on the agent via signed assertion (Ed25519). | Planned | Share dies with the agent; non-listed email denied. |
| `ID-001` | Email one-time codes / magic links. | Planned | Rate-limited; codes single-use and short-lived. |
| `ID-002` | Email delivery provider. | Blocked: choose provider | Decision recorded in `stack.md`. |
| `ID-003` | Passkeys (WebAuthn) registration and login. | Planned | Works on desktop and phone; step-up supported. |
| `ID-004` | OIDC provider: discovery, JWKS, key rotation, authorization code + PKCE. | Planned | A sample app signs in via Switchboard. |
| `SHD-001` | Rate limiting stage (per IP, identity, route; strict on `/_/auth/*`). | Partial delivery committed in `6185098`: per-IP token buckets, separate auth-path bucket, opt-in config, trusted client-IP key and 429 integration test. Residual: identity/route-specific keys. Limits: buckets reset on reload, fixed `Retry-After: 1`, no hard cap on recent keys; disabled unless configured. | Complete the remaining key dimensions and verify limits on protected routes before public exposure. |
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
| `UI-001` | Live event stream on the admin dashboard over Server-Sent Events, in pure Go: `events.Broker` (history + non-blocking fan-out), `GET /_/events`, `/_/activity` page. Delivery committed in `e432abf`; `scripts/verify.sh` green (2026-10-17); SSE checked live via SIGHUP. Product UI = access decisions; host/service health stays in Server Ops (`CROSS-010`). | In progress: residual — filters, auth-decision events need an OIDC route to exercise end to end. | Events appear live; a slow or disconnected client never blocks the sink; the stream goroutine ends when the request context is cancelled; tests. |

---

## Later releases (planned, refined at the start of each quarter)

| Release | IDs | Scope |
|---|---|---|
| v0.2 PKI (Jan–Mar 2027) | `PKI-*`, `SHD-0xx`, `UI-002` | ACME server, internal CA, mTLS agent/origin, developer certs, SSH CA, workload identity; rules language + compiler, PoW challenge; `UI-002`: JSON admin API + first Svelte island, a policy simulator over the pure `auth.Authorize` (blocked on `UI-001`) |
| v0.3 Access (Apr–Jun 2027) | `ACC-*`, `NTF-*`, `SHD-0xx` | SSH/DB over tunnels, JIT with Telegram approvals, session recording, credential injection, CAEP/SSF, SCIM; Coraza + CRS, API schema validation, leaked credentials, CSP reporting |
| v0.4 Trust (Jul–Sep 2027) | `TLOG-*`, `AI-*`, `AGT-*`, `SHD-0xx` | Transparency log, witnesses, risk signals and explanations, AI-agent/MCP gateway; Web Bot Auth, Firewall for AI, DLP |

Details for each are in [`VISION.md`](../VISION.md).
