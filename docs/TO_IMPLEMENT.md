# To implement — Switchboard

**Last review:** 2026-10-18
**Code reviewed up to:** `16f05bc` plus the CLI/console changes included in this commit
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
2. Follow the [Cloudflare coexistence decision](architecture/cloudflare-coexistence.md):
   keep the public Portfolio on Pages; start `EDGE-006` with the actual provider
   configuration inventory and origin-boundary acceptance contract. A separate
   preview will combine Cloudflare protection with Switchboard identity/policy.
   `TUN-001` local spike is verified and committed (`fd72b2c`); continue with `TUN-002`
   for durable named connections. A Hetzner CX23 is now available over WireGuard;
   private Switchboard/Portfolio staging is installed there. `SHD-001` remains a gate before
   public exposure; `EDGE-001` still requires exposure gates and operator DNS/TLS cutover;
   Cloudflare Tunnel is an optional transport, not a development prerequisite.
3. Identity (`ID-*`), then `NTF-001`, `SHD-001`.

---

## Release v0.1 · Edge (October–December 2026)

### Architecture

| ID | Task | State | Done when |
|---|---|---|---|
| `ARCH-004` | Embedded SQLite store with migration runner (sessions, later tunnels/passkeys). Store and runner are committed (`7d0c56e`, `cmd/switchboard migrate`); the local session wiring is included below and deployment acceptance remains pending. Local operator scripts now expose `scripts/migrate.sh up\|status -db PATH` and `scripts/new-migration.sh name`; verification is recorded in the learning log. **Wired 2026-10-18 (working tree, uncommitted):** `auth.SessionBackend` port (consumer-declared) with the in-memory store and `sqlite.Sessions` (migration 001 reuse, SHA-256 IDs at rest, purge+capacity in one immediate transaction); `switchboard -db PATH` selects persistence after `openMigratedDB` refuses a stale schema. Tests cover restart-by-close/reopen survival, expiry rejection/eviction, capacity, cancellation and the TTL-driven factory. Review correction: existing rows are bounded by the earlier of issued expiry and creation plus the current TTL; expired rows are purged under that policy on lookup/sweep/create, without mutating storage while building a reload candidate. | In progress | Sessions survive restart (proven at the database boundary); migrations numbered and immutable. Residual: deployment acceptance with `-db` on the real unit and backup coverage of the state file. |
| `ARCH-005` | Remove duplicated clone helpers in `routemanager` (`cloneRoute`, `cloneConfig` wrapper). | Ready | Single `config.Clone`; tests pass. |

### Alpha release gates

| ID | Task | State | Done when |
|---|---|---|---|
| `REL-003` | CI on GitHub Actions: `scripts/verify.sh` on Linux, macOS, Windows. Workflow is `.github/workflows/ci.yml` (commit `b2602e9`); hosted CI has not been observed. | In progress | Green on a pull request. |
| `REL-005` | GoReleaser config, checksums, SBOM. | Ready | Snapshot build produces artifacts locally. |
| `REL-006` | Tag `v0.1.0-alpha.1` after every gate above is green. | Blocked on `REL-002`–`REL-005` | Tag pushed with release notes. |
| `REL-007` | Dependency advisories: `govulncheck` reports GO-2026-5932 in `golang.org/x/crypto` v0.57.0 with no fixed version; the code does not call the affected symbols (2026-10-18, Go 1.27.2). | Blocked on upstream fix | Re-run `govulncheck` before each release; bump `x/crypto` when a fix exists and keep the gate at 0 reachable. |

### Edge, tunnels, identity, Shield, notifications

| ID | Task | State | Done when |
|---|---|---|---|
| `EDGE-006` | Validate Cloudflare proxy + Switchboard coexistence and a measurable path to reduce provider dependence. [Decision](architecture/cloudflare-coexistence.md). | In progress 2026-10-18: [initial inventory and origin contract](../../server-ops/docs/CLOUDFLARE-INVENTORY.md) record public DNS/HTTP and private VPS listeners; the owner's dashboard screenshots and statements (2026-10-18, unverified by authenticated access) complete the account inventory: Pages-only hosting, one proxied apex CNAME, no Workers/Functions/secrets/bindings, no WAF/Access/transform rules or origin certificates; the account side is closed. Native public-listener client-certificate verification now exists (`tls.origin_client_ca_file`, package `originauth`, committed with this task): the listener refuses connections without a certificate from the configured bundle, emits `origin.denied` and fails closed at startup. Cloudflare side done 2026-10-18 (owner): own CA, per-hostname client certificate uploaded and active for `preview.octaviosantacruz.com`. Still unproven end to end: the first real pull needs the activated origin, which waits on SHD-001, TUN-002 visitor IP and real DNS-01 issuance with the operator token (manager implemented, `EDGE-007`; see the server-ops `hetzner-public` runbook). Residual: denial events are not aggregated, so a scan can flood the log. Before activation also: zone SSL mode Full (strict), Cloudflare ranges in `trusted_proxies` (an empty list makes the visitor IP Cloudflare's and every visitor share one rate-limit bucket), and `ca.crt` on the VPS; see the `hetzner-public` runbook. Previous open-origin runbook superseded. Portfolio stays on Pages; no combined deployment claimed. | Inventory has enabled/disabled/unknown evidence; origin network and deployment-specific authentication boundary defined and tested against direct IPv4/IPv6 and header spoofing; validated origin HTTPS/renewal and trusted visitor-IP mapping; applicable TUN-002/SHD-001 gates passed; legitimate/abusive traffic budgets, decision evidence, isolated removal drill and rollback recorded. Each later control replacement needs measured acceptance; upstream volumetric protection remains outside VPS scope. |
| `EDGE-007` | Origin certificate by ACME DNS-01: own bounded manager over existing `x/crypto/acme` + Cloudflare DNS API with `net/http`, zero new modules; revised 2026-10-18. One name, `preview.octaviosantacruz.com`; no port 80/TLS-ALPN challenge. [Lifecycle contract](architecture/stack.md#public-origin-certificate-contract), [maintainer exercise](learning/EDGE-007-renewal-exercise.md). | In progress 2026-10-18 (working tree, uncommitted): `internal/certs` (pure renewal decision, generation validation, manager, file store) + Cloudflare DNS adapter + `tls.dns01` config + `originauth` with an autocert-only opt-in ALPN exemption + console panel; package tests use fixed clocks and simulated ACME/DNS/storage. Review corrections add explicit chain verification against system roots (or an operator-selected `certificate_roots_file` for staging), cancel-and-join before releasing state, parent-directory sync before activation, and the DNS-01 listener has no ACME ALPN exemption. DNS-01 adapter changes require restart and reload retains its last-good snapshot. Residual: direct Let's Encrypt staging issuance with an operator-supplied token (explicit staging-root trust, origin client auth), then production; prove renewal/failure recovery/restart against the real CA and record the operator run. | Consumer-declared `GetCertificate` port; separate durable ACME account key/email and complete atomic certificate/key generations; invalid/missing token fails closed and secrets never leave references. One active attempt and exclusive state ownership; cancellable backoff honors rate limits; failed renewal retains a still-valid certificate, absence of one fails TLS/readiness closed. TXT propagation bounded, cleanup by owned record ID on success/error/recovery, with unrelated records preserved and ambiguous creates reconciled safely. Fixed-clock/simulated ACME/DNS/storage tests precede direct staging with explicit root trust (and origin client auth), then production; never weaken Full (strict). Events `cert.renewed`/`cert.renewal_failed` and console expiry/last outcome/next attempt verified. Renewal, failure recovery and restart proven from private persistent storage; `originauth` retained and ACME exemption removed for DNS-01 mode. Existing public exposure gates still apply. |
| `EDGE-001` | Public preview edge on the VPS with owned outbound homelab connector, initially behind Cloudflare's HTTP proxy under EDGE-006. Cloudflare Tunnel remains an [optional transport alternative](architecture/edge-cloudflared.md). | In progress: private two-host pilot and native renewal verified; public origin HTTPS, proxy boundary and remaining TUN-002/SHD-001 exposure acceptance pending. Existing staging and prepared HTTPS candidate retained. Public Portfolio stays on Pages; no apex migration required. [Operational evidence](../../server-ops/docs/INFRAESTRUCTURA.md). | Separate preview serves the real owned-tunnel origin through the combined path, survives restart, rejects origin bypass and forged identity/IP; no inbound homelab port or residential public DNS. EDGE-006 and applicable exposure gates pass. DNS/TLS activation is a separate operator action. |
| `EDGE-002` | Wildcard TLS via DNS-01. Decide manager suitability and concurrent TXT/state ownership before extending single-host `EDGE-007`; no wildcard implementation or CertMagic choice is implied by that task. | Planned for wildcard hosts on an owned edge; not needed for the local lab | Wildcard issuance/renewal verified before wildcard public deployment; implementation choice and additional scope recorded first. Existing single-host ACME remains an option. |
| `EDGE-003` | Route hosts without a port match any listener port (local-dev ergonomics). | Ready | `app.localhost` route works on `:8888`; tests. |
| `EDGE-004` | Multiple upstreams per route, round-robin, active + passive health. | Planned | Failed upstream receives no new traffic; tests. |
| `TUN-002` | Named persistent connectors: configuration/reload integration, authenticated enrolment/revocation, automatic reconnect/backoff, heartbeats, status and an explicit visitor-identity propagation contract. Reuse the local TUN-001 transport. | In progress on `spike/tun-001-local`. Registry/reconnect checkpoint committed in `16f05bc`; `scripts/verify.sh` rerun green (2026-10-17): migration `002_create_connectors` (enrolment by leaf-certificate SHA-256, revocation keeps history); `tunnel.Registry` + `switchboard connector enroll\|revoke\|list`; `Edge` with named connectors, registry authorization and revocation that closes live sessions (revalidation every 5s); `ConnectLoop` with jittered exponential backoff and observable `Link` status; tests for enrol/revoke/unenrolled-cert/edge-restart reattach. **CLI delivery included in this commit:** `switchboard tunnel edge\|connect` requires certificate/key/CA files, uses the migrated registry and active enrolment, routes the host to a named connector, reconnects automatically and joins workers on shutdown; HTTP remains loopback-only; the 2026-10-18 working-tree delivery permits mTLS on one explicitly configured private CIDR via `-tunnel-network`, retaining mandatory registry/TLS checks. CLI race tests and [separate-process Portfolio smoke](architecture/tunnel-persistent.md) passed with graceful/abrupt edge restart and live revocation. **Private two-host pilot (2026-10-18, working tree):** enabled user services on VPS/homelab, mTLS via 10.8.0.1:9443 with explicit 10.8.0.0/24 allowlist, HTTP edge on loopback 18889, local role keys and public CSR signing. Real homelab-origin/EN/ES/assets, graceful and abrupt edge recovery with stable connector PID, live revocation/reconnect rejection and replacement with a new connector key/certificate were verified. Existing staging remains active; public TLS/DNS and host reboot are not established. **Automatic leaf renewal (2026-10-18, working tree):** native Go `tunnel renew`/SSH role agent, local Ed25519 keys and x509 signing, daily systemd timer, seven-day threshold, durable batch recovery and guarded rollback; registry rotation is transactional and rejects stale/revoked identities. Real forced renewal passed. **Visitor address contract (2026-10-18, committed with this task):** the edge asserts the visitor IP to the connector inside the authenticated tunnel and the application receives it as `X-Forwarded-For` ([contract](architecture/tunnel-persistent.md)). **Remaining:** config/reload and admin integration (connector status on the activity UI, host→connector routes: running the edge inside the public process also removes the facade→edge hop that still hides the visitor), and protected-app visitor *identity* assertions. CA trust-anchor rollover remains a separate planned operation. | Survives edge restart and network drop without manual restart; identities and fixed upstream permissions enforced; tests. Public deployment remains EDGE-001. |
| `TUN-003` | `sb share <url> --allow <emails>`: ephemeral protected shares; guest list enforced on the agent via signed assertion (Ed25519). | Planned | Share dies with the agent; non-listed email denied. |
| `ID-001` | Email one-time codes / magic links. | Planned | Rate-limited; codes single-use and short-lived. |
| `ID-002` | Email delivery provider. | Blocked: choose provider | Decision recorded in `stack.md`. |
| `ID-003` | Passkeys (WebAuthn) registration and login. | Planned | Works on desktop and phone; step-up supported. |
| `ID-004` | OIDC provider: discovery, JWKS, key rotation, authorization code + PKCE. | Planned | A sample app signs in via Switchboard. |
| `SHD-001` | Rate limiting stage (per IP, identity, route; strict on `/_/auth/*`). | In progress 2026-10-18 (working tree, uncommitted): beyond the `6185098` delivery, keys now carry the route host, an optional per-identity budget (`rate_limit.identity`) is charged after the access policy allow, `Retry-After` reports the real refill wait, each limiter has an explicit key cap (`rate_limit.max_keys`, fail closed at capacity without evicting restricted buckets), buckets survive reloads while their settings are unchanged, and refusals emit `access.rate_limited` into the activity feed. `go test -race ./internal/ratelimit ./internal/pipeline ./internal/proxy ./internal/routemanager` green; route-dimension and reload-preservation regressions included. Review correction: route keys use the same canonical host as routing; equivalent DNS/IPv6 spellings share a budget. Residual: real-traffic acceptance on protected routes before public exposure; per-path budgets are not implemented (route = host). | Complete the remaining key dimensions and verify limits on protected routes before public exposure. |
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
| `UI-001` | Live event stream on the admin dashboard over Server-Sent Events, in pure Go: `events.Broker` (history + non-blocking fan-out), `GET /_/events`, `/_/activity` page. Delivery committed in `e432abf`; `scripts/verify.sh` green (2026-10-17); SSE checked live via SIGHUP. Product UI = access decisions; host/service health stays in Server Ops (`CROSS-010`). | In progress: local redesign adds category/search filters, pause/resume, safe route-access overview, shared navigation and expandable route diagnostics. Visual follow-up adds a dark/amber access-console style, local licensed Manrope font, sidebar navigation and a distinct Routes & hostnames view that does not imply published DNS. Root opens Access activity; static sites say Not probed. Browser checks passed for filters, paused snapshots, error/retry, text-only event rendering and 320px layout. Residual — auth-decision events need an OIDC route to exercise end to end; tunnel CLI events remain separate. | Events appear live; a slow or disconnected client never blocks the sink; the stream goroutine ends when the request context is cancelled; tests. |

---

## Later releases (planned, refined at the start of each quarter)

| Release | IDs | Scope |
|---|---|---|
| v0.2 PKI (Jan–Mar 2027) | `PKI-*`, `SHD-0xx`, `UI-002` | ACME server, internal CA, mTLS agent/origin, developer certs, SSH CA, workload identity; rules language + compiler, PoW challenge; `UI-002`: JSON admin API + first Svelte island, a policy simulator over the pure `auth.Authorize` (blocked on `UI-001`) |
| v0.3 Access (Apr–Jun 2027) | `ACC-*`, `NTF-*`, `SHD-0xx` | SSH/DB over tunnels, JIT with Telegram approvals, session recording, credential injection, CAEP/SSF, SCIM; Coraza + CRS, API schema validation, leaked credentials, CSP reporting |
| v0.4 Trust (Jul–Sep 2027) | `TLOG-*`, `AI-*`, `AGT-*`, `SHD-0xx` | Transparency log, witnesses, risk signals and explanations, AI-agent/MCP gateway; Web Bot Auth, Firewall for AI, DLP |

Details for each are in [`VISION.md`](../VISION.md).
