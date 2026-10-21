# To implement — Switchboard

**Last review:** 2026-10-20
**Code reviewed up to:** `0b4e21e` (policy explain, Svelte console, project page; wildcard `aa49358`, multi-host connector `d60ba24`); CI observed on hosted runners, see `REL-003`
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
`ARCH` architecture · `AUD` durable audit · `EDGE` edge/deployment · `TUN`
tunnels · `ID` identity · `SHD` Shield/WAF · `NTF` notifications · `PKI`
private CA · `ACC` infrastructure access · `TLOG` transparency log · `AI`
AI-assisted decisions · `AGT` AI-agent gateway · `UI` dashboard · `PAGES`
static sites · `LAB` learning lab (not a release commitment).

## Recommended order

Every `SEC-*` and `BUG-*` finding is closed (2026-10-17).

1. `REL-003` (Windows went red again from `741e2c6`; fixed 2026-10-20, a green hosted run and a green pull request run remain), `REL-006`: CI → tag `v0.1.0-alpha.1` (`REL-005` GoReleaser closed 2026-10-20).<br>`REL-001`–`REL-002` and `REL-004` closed (secret references, fuzz, quickstart).
2. Follow the [positioning and portability contract](architecture/positioning-and-portability.md)
   and [Cloudflare coexistence decision](architecture/cloudflare-coexistence.md).
   Direction 20/10: the owner intends to move Portfolio out of Pages; migration
   and operator cutover remain pending. `EDGE-006` owns measured direct and
   exterior-proxy acceptance, including an alternate DNS/provider path before
   claiming portability; current Cloudflare DNS-01 integration alone is insufficient.
   Existing PAGES-* tasks cover static delivery; workspace `CROSS-006` owns the
   migration coordination. No new authoritative DNS or distributed runtime is
   required for the initial offering.
   `TUN-001` local spike is verified and committed (`fd72b2c`); continue with `TUN-002`
   for durable named connections. A Hetzner CX23 is now available over WireGuard;
   private Switchboard/Portfolio staging is installed there. `SHD-001` remains a gate before
   public exposure; `EDGE-001` still requires exposure gates and operator DNS/TLS cutover;
   Cloudflare Tunnel is an optional transport, not a development prerequisite.
3. Identity (`ID-*`), then `NTF-001`, `SHD-001`.

### Product scope and portability evidence — 2026-10-20

The [contract](architecture/positioning-and-portability.md) records the initial
user, offering, capability boundaries and demo. `EDGE-006` remains open for
the two ingress modes and provider-switch evidence; `EDGE-007`/`EDGE-002`
retain certificate renewal acceptance, `TUN-002` protected identity,
`SHD-001` measured protection and `REL-008` release claims. No capability is
closed by this documentation. Historical Pages-retention statements in dated
entries below describe the earlier decision, superseded by the 20/10 direction.

### Release contract and learning labs

A VISION pillar is not a release promise. `REL-008` requires every release to
record its **promise, evidence, non-goals, rollback/recovery and residual
risks**. A claim stronger than the evidence (“verifiable”, “transparent”,
“production-ready”) is a release blocker.

`LAB-*` entries are learning work, not release commitments. They do not gate a
release unless the owner explicitly promotes one into a release table with an
acceptance criterion. Keep the broad VISION; narrow what each release promises.
Cross-project coordination stays in the workspace `TO_IMPLEMENT.md`
(`CROSS-006`, `CROSS-008`, `CROSS-010`, `CROSS-012`, `CROSS-013`), not here.

---

## Release v0.1 · Edge (October–December 2026)

### Architecture

| ID | Task | State | Done when |
|---|---|---|---|
| `ARCH-004` | Embedded SQLite store with migration runner (sessions, later tunnels/passkeys). Store and runner are committed (`7d0c56e`, `cmd/switchboard migrate`); the local session wiring is included below and deployment acceptance remains pending. Local operator scripts now expose `scripts/migrate.sh up\|status -db PATH` and `scripts/new-migration.sh name`; verification is recorded in the learning log. **Wired 2026-10-18 (commit `a4d58c1`):** `auth.SessionBackend` port (consumer-declared) with the in-memory store and `sqlite.Sessions` (migration 001 reuse, SHA-256 IDs at rest, purge+capacity in one immediate transaction); `switchboard -db PATH` selects persistence after `openMigratedDB` refuses a stale schema. Tests cover restart-by-close/reopen survival, expiry rejection/eviction, capacity, cancellation and the TTL-driven factory. Review correction: existing rows are bounded by the earlier of issued expiry and creation plus the current TTL; expired rows are purged under that policy on lookup/sweep/create, without mutating storage while building a reload candidate. | In progress | Sessions survive restart (proven at the database boundary); migrations numbered and immutable. Residual: deployment acceptance with `-db` on the real unit and backup coverage of the state file. |
| `ARCH-009` | Identity integration with apps that already authenticate: choose transport-only, coexistence, signed assertion or shared IdP; avoid double login and define what each proves. [Decision record](architecture/identity-integration.md). | Ready (decision pending) | One option accepted with threat model and acceptance test; disposable app plan; direct-bypass and header-spoofing tests defined; no app integration claimed as SSO before its acceptance. |

### Audit (proposed contract)

[Contract](architecture/audit-contract.md). The contract is a proposal; none of
these tasks is a v0.1 release promise. Keep live activity (`events.Broker`) and
durable audit separate.

| ID | Task | State | Done when |
|---|---|---|---|
| `AUD-001` | Durable audit writer: append-only JSONL, sequence, hash chain, `fsync`, rotation, owner-only permissions, safe event schema. | Ready (design proposed) | Unit tests for encoding/chain/rotation; `required` vs `best_effort` failure semantics implemented per route; no secrets; a real append + restart passes. |
| `AUD-002` | Signed checkpoints and `switchboard audit verify`: Ed25519 checkpoint, external copy, verification from a retained checkpoint, tamper/truncation detection. | Blocked on `AUD-001` | Checkpoint signing/verification, key rotation, tamper/reorder/truncate tests; a real `SIGKILL` recovery drill; verification against a checkpoint copied outside the host. Do not claim tamper evidence without that drill. |
| `AUD-003` | Optional AuditAnchor adapter: batch export, stored receipts, no request-path dependency. Coordination: workspace `CROSS-012`. | Planned (P3, optional) | Adapter can be disabled without affecting the gateway; local audit remains authoritative; receipts verified; independence boundary documented (same admin/host is not a witness). |

### Alpha release gates

| ID | Task | State | Done when |
|---|---|---|---|
| `REL-003` | **Regressed 2026-10-21:** run `39528941061` (`95aa9a6`) red on ubuntu only: `govulncheck` reports `GO-2026-6809` (`crypto/x509` intermediate distrust, reachable through `originauth.Decide`), fixed by the go1.27.3 toolchain on the pull request branch. **Branch rename 2026-10-20:** local and GitHub default branch are now `main`; the push filter follows `main`. **Observed 2026-10-20:** run `39488263569` (`e35bc98`, push to `main`, pinned workflow) green on ubuntu-24.04, macOS and Windows, which also confirms the Windows fix of `31b5f52`. Still open: a green pull request run. CI on GitHub Actions: `scripts/verify.sh` on Linux, macOS, Windows. Workflow is `.github/workflows/ci.yml` (commit `b2602e9`). **Observed 2026-10-19:** every run on `master` from 2026-10-18 was red because Windows checked files out as CRLF and `gofmt -l` listed the whole tree (fixed by `.gitattributes` and `core.autocrlf=false`, `ccd5609`); that exposed nine Windows-only test failures, none a product bug (POSIX-path fixtures and Unix permission bits), fixed in `9542359`: seven config tests are skipped on Windows with the reason stated and the mode assertions are conditional. Run `39189670980` (`9542359`) is green on ubuntu, macOS and Windows. The image and linters are pinned in `5bb3cfe` (`ubuntu-24.04` because `ubuntu-latest` moves to 26.04 until 2026-11-19; `staticcheck` v0.8.2, `govulncheck` v1.9.0); that commit has not run on a hosted runner yet. **Regressed 2026-10-20:** runs `39350091062`, `39355130948` and `39428378728` (from `741e2c6` to `0b4e21e`) were red on Windows only; ubuntu and macOS green. Two failures, neither a Linux/macOS product bug: `TestConnectorRoutesAreFixedAndBounded` used the POSIX fixture `/srv/portfolio` (not absolute on Windows; now built from `t.TempDir()`), and the console served assets with the OS mime table, which on Windows can answer something other than `text/javascript` and break the script under `nosniff` (now `consoleTypes` fixes the type per extension). Local `scripts/verify.sh` green and `GOOS=windows go vet` clean; the fix has not run on a hosted Windows runner yet. | In progress | Green on a pull request (so far only a push to `master` is green); the pinned workflow observed green; decide whether to make the skipped Windows fixtures portable or keep Windows as a build/test target only. |
| `REL-006` | Tag `v0.1.0-alpha.1` after every gate above is green: a release workflow on tags (GoReleaser with syft, pinned like the CI linters) publishing the draft that `.goreleaser.yaml` (`REL-005`) defines, then release notes per the `REL-008` template. | Workflow written 2026-10-21 (`.github/workflows/release.yml`: same gate as CI, syft 1.14.0 checked against its checksums, GoReleaser v2.3.2, `contents: write` only in the job); passes `actionlint`, not run yet. Blocked on `REL-003` (green pull request run) | Tag pushed; draft release with archives, `checksums.txt` and SBOMs checked against a local rebuild, then published with notes. |
| `REL-007` | Dependency advisories: `govulncheck` reports GO-2026-5932 in `golang.org/x/crypto` v0.57.0 with no fixed version; the code does not call the affected symbols (2026-10-18, Go 1.27.2). | Blocked on upstream fix | Re-run `govulncheck` before each release; bump `x/crypto` when a fix exists and keep the gate at 0 reachable. |
| `REL-008` | Release contract: record promise, evidence, non-goals, rollback/recovery and residual risks for every release; keep VISION broad and release claims narrow. | Ready | Template used for `v0.1.0-alpha.1`; release notes distinguish durable/tamper-evident/transparent claims; no promise lacks a test or deployment evidence; labs are labeled as labs. |

### Edge, tunnels, identity, Shield, notifications

| ID | Task | State | Done when |
|---|---|---|---|
| `EDGE-006` | Validate Cloudflare proxy + Switchboard coexistence and a measurable path to reduce provider dependence. [Decision](architecture/cloudflare-coexistence.md). | In progress 2026-10-18: [initial inventory and origin contract](../../server-ops/docs/CLOUDFLARE-INVENTORY.md) record public DNS/HTTP and private VPS listeners; the owner's dashboard screenshots and statements (2026-10-18, unverified by authenticated access) complete the account inventory: Pages-only hosting, one proxied apex CNAME, no Workers/Functions/secrets/bindings, no WAF/Access/transform rules or origin certificates; the account side is closed. Native public-listener client-certificate verification now exists (`tls.origin_client_ca_file`, package `originauth`, committed with this task): the listener refuses connections without a certificate from the configured bundle, emits `origin.denied` and fails closed at startup. Cloudflare side done 2026-10-18 (owner): own CA, per-hostname client certificate uploaded and active for `preview.octaviosantacruz.com`. Still unproven end to end: the first real pull needs the activated origin, which waits on SHD-001, TUN-002 visitor IP and real DNS-01 issuance with the operator token (manager implemented, `EDGE-007`; see the server-ops `hetzner-public` runbook). Denials now aggregate per window (one event per minute with the suppressed count), so a scan cannot flood the log. Before activation also: zone SSL mode Full (strict), Cloudflare ranges in `trusted_proxies` (an empty list makes the visitor IP Cloudflare's and every visitor share one rate-limit bucket), and `ca.crt` on the VPS; see the `hetzner-public` runbook. Previous open-origin runbook superseded. **Visitor-IP evidence gap found 2026-10-19 during public acceptance (deployed to the VPS preview 2026-10-19):** the public origin's `proxy.forward` record carried no client address, so the trusted Cloudflare visitor IP could not be shown at the origin even though `trusted_proxies` resolves it; the record now includes `client_ip` (the address selected by the trust stage, never a raw header), with table-driven tests for trusted, untrusted, spoofed and duplicated assertions. Verified on the public preview: all 8 requests, including spoofed `X-Forwarded-For` (also duplicated), `X-Real-IP`, `True-Client-IP`, underscore and `Forwarded` variants, logged the real visitor address; a client-sent `CF-Connecting-IP` is answered `403` by Cloudflare. Remaining: `access.*` events still log the TCP peer in `RemoteAddr`; direct IPv6 to the VPS (global address present) is untested from an IPv6 network; `/_headers` and `/_redirects` were served as files by the static edge: `static.Resolve` now refuses them at the site root (commit `af40aeb`, deployed to the homelab connector 2026-10-19: both answer `404` through Cloudflare; applying them stays `PAGES-003`); Shield acceptance needs a protected route (`protected=0` in the preview config). Portfolio stays on Pages; no combined deployment claimed. | Inventory has enabled/disabled/unknown evidence; origin network and deployment-specific authentication boundary defined and tested against direct IPv4/IPv6 and header spoofing; validated origin HTTPS/renewal and trusted visitor-IP mapping; applicable TUN-002/SHD-001 gates passed; legitimate/abusive traffic budgets, decision evidence, isolated removal drill and rollback recorded. Each later control replacement needs measured acceptance; upstream volumetric protection remains outside VPS scope. |
| `EDGE-007` | Origin certificate by ACME DNS-01: own bounded manager over existing `x/crypto/acme` + Cloudflare DNS API with `net/http`, zero new modules; revised 2026-10-18. One name, `preview.octaviosantacruz.com`; no port 80/TLS-ALPN challenge. [Lifecycle contract](architecture/stack.md#public-origin-certificate-contract), [maintainer exercise](learning/EDGE-007-renewal-exercise.md). | In progress 2026-10-18 (commit `2698e62`): `internal/certs` (pure renewal decision, generation validation, manager, file store) + Cloudflare DNS adapter + `tls.dns01` config + `originauth` with an autocert-only opt-in ALPN exemption + console panel; package tests use fixed clocks and simulated ACME/DNS/storage. Review corrections add explicit chain verification against system roots (or an operator-selected `certificate_roots_file` for staging), cancel-and-join before releasing state, parent-directory sync before activation, and the DNS-01 listener has no ACME ALPN exemption. DNS-01 adapter changes require restart and reload retains its last-good snapshot. **Real staging issuance accepted 2026-10-18:** DNS-01 against Let's Encrypt staging with the operator token (file reference); served on :443 with SNI; a manual restart reused the same certificate serial (no re-issuance); TXT cleaned up; DNS/firewall untouched; Portfolio EN/ES hashes match the homelab exactly. **Production accepted privately 2026-10-18:** Let's Encrypt production certificate (issuer `YE1`, 18/10 → 16/01/2027, next attempt 17/12) served on `:443`; a disposable origin-pull client certificate completed the handshake and served both Portfolio hashes, while no-certificate and untrusted self-signed attempts were rejected (`bad certificate`); DNS and firewall untouched. Residual: the real renewal window, and the public activation run (Full strict, Cloudflare ranges, DNS, external acceptance). | Consumer-declared `GetCertificate` port; separate durable ACME account key/email and complete atomic certificate/key generations; invalid/missing token fails closed and secrets never leave references. One active attempt and exclusive state ownership; cancellable backoff honors rate limits; failed renewal retains a still-valid certificate, absence of one fails TLS/readiness closed. TXT propagation bounded, cleanup by owned record ID on success/error/recovery, with unrelated records preserved and ambiguous creates reconciled safely. Fixed-clock/simulated ACME/DNS/storage tests precede direct staging with explicit root trust (and origin client auth), then production; never weaken Full (strict). Events `cert.renewed`/`cert.renewal_failed` and console expiry/last outcome/next attempt verified. Renewal, failure recovery and restart proven from private persistent storage; `originauth` retained and ACME exemption removed for DNS-01 mode. Existing public exposure gates still apply. |
| `EDGE-001` | Public preview edge on the VPS with owned outbound homelab connector, initially behind Cloudflare's HTTP proxy under EDGE-006. Cloudflare Tunnel remains an [optional transport alternative](architecture/edge-cloudflared.md). | In progress: private two-host pilot and native renewal verified; public origin HTTPS, proxy boundary and remaining TUN-002/SHD-001 exposure acceptance pending. Existing staging and prepared HTTPS candidate retained. Public Portfolio stays on Pages; no apex migration required. [Operational evidence](../../server-ops/docs/INFRAESTRUCTURA.md). | Separate preview serves the real owned-tunnel origin through the combined path, survives restart, rejects origin bypass and forged identity/IP; no inbound homelab port or residential public DNS. EDGE-006 and applicable exposure gates pass. DNS/TLS activation is a separate operator action. |
| `EDGE-002` | Wildcard TLS via DNS-01 for `*.octaviosantacruz.com`, so a second public host (`sigea.octaviosantacruz.com`, the SigeaWeb test site) needs no new certificate. Decision 2026-10-19: extend the own manager to one wildcard name, no CertMagic ([stack](architecture/stack.md)). | In progress 2026-10-19 (committed `aa49358`): `internal/certs/names.go` (pure: `ValidateName`, `Covers`, `ProbeName`, `ChallengeFQDN`, `StorageKey`), `GetCertificate` serves any single-label subdomain and refuses the apex and two-label names, chain verification against a concrete probe name, ACME order for `*.base` with the challenge TXT at `_acme-challenge.<base>` and a check that the CA authorized the base domain, safe `_wildcard.<base>` storage key that a plain name cannot alias, `tls.domains` accepts one `*.` name only with `tls.dns01`; tests with the fake ACME CA pass and `scripts/verify.sh` is green. **Real issuance observed 2026-10-20:** the VPS already held a staging wildcard (expires 2026-12-22) and a production wildcard issued 2026-10-19 (expires 2027-01-17), both loaded from storage on restart without re-issuance; `switchboard` and `sigea` are served publicly with it through Cloudflare (acceptance in the server-ops `CLOUDFLARE-INVENTORY.md`). **Pending:** a real renewal of the wildcard, and a second host through the tunnel (`sigea-api`, no DNS yet). | Wildcard issuance and renewal verified against Let's Encrypt staging then production with the TXT cleaned up and the apex untouched; `preview` keeps serving across the switch; one extra host served end to end; no new Go module. |
| `EDGE-003` | Route hosts without a port match any listener port (local-dev ergonomics). | Ready | `app.localhost` route works on `:8888`; tests. |
| `EDGE-004` | Multiple upstreams per route, round-robin, active + passive health. | Planned | Failed upstream receives no new traffic; tests. |
| `TUN-002` | Named persistent connectors: configuration/reload integration, authenticated enrolment/revocation, automatic reconnect/backoff, heartbeats, status and an explicit visitor-identity propagation contract. Reuse the local TUN-001 transport. | In progress; merged into `master` (multi-host connector `d60ba24`). Registry/reconnect checkpoint committed in `16f05bc`; `scripts/verify.sh` rerun green (2026-10-17): migration `002_create_connectors` (enrolment by leaf-certificate SHA-256, revocation keeps history); `tunnel.Registry` + `switchboard connector enroll\|revoke\|list`; `Edge` with named connectors, registry authorization and revocation that closes live sessions (revalidation every 5s); `ConnectLoop` with jittered exponential backoff and observable `Link` status; tests for enrol/revoke/unenrolled-cert/edge-restart reattach. **CLI delivery included in this commit:** `switchboard tunnel edge\|connect` requires certificate/key/CA files, uses the migrated registry and active enrolment, routes the host to a named connector, reconnects automatically and joins workers on shutdown; HTTP remains loopback-only; the 2026-10-18 working-tree delivery permits mTLS on one explicitly configured private CIDR via `-tunnel-network`, retaining mandatory registry/TLS checks. CLI race tests and [separate-process Portfolio smoke](architecture/tunnel-persistent.md) passed with graceful/abrupt edge restart and live revocation. **Private two-host pilot (2026-10-18, working tree):** enabled user services on VPS/homelab, mTLS via 10.8.0.1:9443 with explicit 10.8.0.0/24 allowlist, HTTP edge on loopback 18889, local role keys and public CSR signing. Real homelab-origin/EN/ES/assets, graceful and abrupt edge recovery with stable connector PID, live revocation/reconnect rejection and replacement with a new connector key/certificate were verified. Existing staging remains active; public TLS/DNS and host reboot are not established. **Automatic leaf renewal (2026-10-18, working tree):** native Go `tunnel renew`/SSH role agent, local Ed25519 keys and x509 signing, daily systemd timer, seven-day threshold, durable batch recovery and guarded rollback; registry rotation is transactional and rejects stale/revoked identities. Real forced renewal passed. **Visitor address contract (2026-10-18, committed with this task):** the edge asserts the visitor IP to the connector inside the authenticated tunnel and the application receives it as `X-Forwarded-For` ([contract](architecture/tunnel-persistent.md)). **Normal-process integration (2026-10-18, committed with this change):** `tunnel` settings + `route.connector` wire the private mTLS listener/registry into `switchboard -db`; host→connector mappings reload atomically with sessions retained, no direct-TCP fallback, shared request cap and HTTP upgrades preserved. Common broker events and connector attachment state appear in the activity/route console. Race/integration tests and the separate-process smoke verify trusted visitor IP with spoof stripping, offline/revoked failures, reload rejection, restart/reconnect and shutdown. **One connector, several hosts (2026-10-19, committed `d60ba24`):** `tunnel connect` accepts repeatable `-route HOST=root:DIR|upstream:URL` beside `-host`, so the single enrolled identity (and its automatic renewal, which is hard-wired to the `edge` and `connector` roles) also serves the SIGEA site API; a second connector would have had no renewal. Tests: `connectorRoutes` refusals and bound, and an end-to-end test through the integrated edge with a static host, an upstream host and an unserved host. Deployed to the homelab connector 2026-10-19; the preview stayed `200`. **Remaining:** protected-app visitor *identity* assertions (protected connector routes are rejected until that contract exists), and public EDGE-001/EDGE-006/EDGE-007/SHD-001 acceptance. **Private integrated deployment accepted (2026-10-18 UTC):** clean `731a204` binary on both hosts; live homelab content, reload, console/SSE, actual TCP peer IP with spoof stripping, graceful/abrupt edge recovery with stable connector PID, disposable-identity revocation/reconnect denial, and real rollback/reactivation passed. Renewal timer restored; original staging unchanged. This does not establish Cloudflare visitor IP or public TLS. [Operations and checkpoint](../../server-ops/deploy/hetzner-tunnel/README.md#proceso-integrado--aceptación-privada-2026-10-18-utc). CA trust-anchor rollover remains a separate planned operation. | Survives edge restart and network drop without manual restart; identities and fixed upstream permissions enforced; tests. Public deployment remains EDGE-001. |
| `TUN-003` | `sb share <url> --allow <emails>`: ephemeral protected shares; guest list enforced on the agent via signed assertion (Ed25519). | Planned | Share dies with the agent; non-listed email denied. |
| `ID-001` | Email one-time codes / magic links. | Planned | Rate-limited; codes single-use and short-lived. |
| `ID-002` | Email delivery provider. | Blocked: choose provider | Decision recorded in `stack.md`. |
| `ID-003` | Passkeys (WebAuthn) registration and login. | Planned | Works on desktop and phone; step-up supported. |
| `ID-004` | OIDC provider: discovery, JWKS, key rotation, authorization code + PKCE. | Planned | A sample app signs in via Switchboard. |
| `SHD-001` | Rate limiting stage (per IP, identity, route; strict on `/_/auth/*`). | In progress 2026-10-18 (commit `700d572`): beyond the `6185098` delivery, keys now carry the route host, an optional per-identity budget (`rate_limit.identity`) is charged after the access policy allow, `Retry-After` reports the real refill wait, each limiter has an explicit key cap (`rate_limit.max_keys`, fail closed at capacity without evicting restricted buckets), buckets survive reloads while their settings are unchanged, and refusals emit `access.rate_limited` into the activity feed. `go test -race ./internal/ratelimit ./internal/pipeline ./internal/proxy ./internal/routemanager` green; route-dimension and reload-preservation regressions included. Review correction: route keys use the same canonical host as routing; equivalent DNS/IPv6 spellings share a budget. Residual: real-traffic acceptance on protected routes before public exposure; per-path budgets are not implemented (route = host). | Complete the remaining key dimensions and verify limits on protected routes before public exposure. |
| `SHD-003` | Shield challenge: privacy-friendly proof-of-work + signed clearance cookie + per-route policy; in the spirit of Turnstile/Anubis, not a parity claim. [Design](architecture/shield-challenge.md). | Planned (design proposed; after SHD-001 real-traffic acceptance) | Log-only mode first; signed challenge, single-use nonce, host/action binding, clearance TTL; token/replay/expiry/tamper tests; accessible fallback; no third-party JS; events/metrics; explicit fail-open/closed policy; no claim of human verification or DDoS protection. |
| `NTF-001` | `Notifier` port + Telegram adapter: alerts for logins and admin changes; chat pairing code. | In progress 2026-10-18 (commit `5c681d3`): `internal/notify` (Notifier port, non-blocking alert sink with bounded queue, one-time pairing code, signed callback data with nonce/expiry) + `internal/notify/telegram` (net/http Bot API: bounded retries honoring `retry_after`, timeouts, token never in errors) + `switchboard notify pair` + `notify.telegram` config (secret reference, `chats_file`, loopback-only http `api_base`). Simulated-server tests cover send/retry/refusals/pairing; callback signing is verified pure. Notification settings require restart; reload rejects changes rather than claiming to activate an adapter that was only constructed at startup. Residual: accept the real run with the owner's bot token (pairing + one alert, explicitly authorized); approvals are not implemented (the nonce and expiry are ready for the approver). | Pairing and an alert work end to end; callback data signed. |

### Pages (static sites)

Switchboard publishes static sites the way Cloudflare Pages does: a site is a
route whose upstream is a directory instead of a URL, deployments are
immutable and swapped atomically, and later a push builds and deploys.

| ID | Task | State | Done when |
|---|---|---|---|
| `PAGES-004` | Git builds: GitHub webhook verified with HMAC-SHA256, checkout of the pushed commit, build command run unprivileged with timeout and no access to Switchboard secrets, output deployed through `PAGES-002`. | Planned (`PAGES-002` closed 2026-10-20, `PAGES-006` closed 2026-10-21). Today a push changes nothing in production; the owner runs `publish-site.py --apply`. Open: where builds run (VPS CX23 vs homelab), deploy key for private repos, webhook host exposed through Cloudflare. | Unsigned or replayed webhooks rejected; a failing build leaves the live site untouched; build logs kept per deployment. |
| `PAGES-005` | Preview deployments per branch at `<branch>.<site>.<domain>`, protected by an access policy by default. | Planned (after `PAGES-004`) | Preview URL per push; production unchanged until `main` builds. |

### Dashboard

Pure Go first, Svelte only where a feature needs it (see
[`stack.md`](architecture/stack.md)).

**Owner decision 2026-10-20:** finish the console and pull `UI-002` (policy
simulator) forward from v0.2; `ARCH-008` (`policy explain`, its CLI-first step)
is done. The console was rebuilt as a Svelte 5 + Tailwind + shadcn-svelte app (owner decision, see `stack.md`) with Activity, Routes and the Policy simulator over the bearer-protected `/_/api/policy/explain`; working tree, see `IMPLEMENTED.md`. Residual: an end-to-end auth-decision event (`UI-001`). The public project page (`SITE-001`, `site/`) is built but not deployed: publish the repository, then add the `switchboard` route, its proxied A record and the Pages root through the Server Ops runbook. The console stays loopback-only. `switchboard.octaviosantacruz.com` is
reserved for a future public project page (covered by the `EDGE-002` wildcard;
it still needs its own proxied A record and route, which are operator steps in
the Server Ops runbook); nothing is published there yet.

| ID | Task | State | Done when |
|---|---|---|---|
| `UI-001` | Live event stream on the admin dashboard over Server-Sent Events, in pure Go: `events.Broker` (history + non-blocking fan-out), `GET /_/events`, `/_/activity` page. Delivery committed in `e432abf`; `scripts/verify.sh` green (2026-10-17); SSE checked live via SIGHUP. Product UI = access decisions; host/service health stays in Server Ops (`CROSS-010`). | In progress: local redesign adds category/search filters, pause/resume, safe route-access overview, shared navigation and expandable route diagnostics. Visual follow-up adds a dark/amber access-console style, local licensed Manrope font, sidebar navigation and a distinct Routes & hostnames view that does not imply published DNS. Root opens Access activity; static sites say Not probed. Browser checks passed for filters, paused snapshots, error/retry, text-only event rendering and 320px layout. Integrated tunnel events/status now reach the normal console and were observed through SSE/browser. Residual — auth-decision events need an OIDC route to exercise end to end; standalone CLI process events remain separate. | Events appear live; a slow or disconnected client never blocks the sink; the stream goroutine ends when the request context is cancelled; tests. |

---

## Learning labs (not release commitments)

These are learning modules, not v0.1 features. Each leaves an artifact and a
line in the learning log; a lab that finds a product defect opens a `BUG-*` or
`SEC-*` task with a regression test. No lab gates a release unless the owner
promotes it explicitly. Some labs deepen existing work (`LAB-004` with
`ARCH-004`/`CROSS-008`, `LAB-007` with `UI-001`, `LAB-008` with `EDGE-007`/PKI,
`LAB-010` with `REL-005`/`REL-006`): the lab owns the learning artifact, while
the release or CROSS task owns any product commitment. Cross-project work stays
in the workspace tracker: `CROSS-012` (AuditAnchor, optional) and `CROSS-013`
(Server Ops service catalog/publish), not duplicated here.

| ID | Lab | State | Done when |
|---|---|---|---|
| `LAB-001` | Performance and profiling: proxy overhead, p50/p99, throughput, allocs, TLS handshakes, routing/auth/rate-limit cost; `pprof`, `benchstat`, load test. | Ready (lab) | Benchmark suite + `docs/performance.md`; one optimization with before/after; no regression. |
| `LAB-002` | Failure drills: slow/5xx upstream, tunnel kill, certificate expiry, disk full/read-only, clock skew, `SIGKILL`; assert fail-closed and recovery. | Ready (lab) | Drill script, recovery evidence, runbook and a blameless postmortem of the `origin-cert` startup failure. |
| `LAB-003` | Threat model + OWASP ASVS: STRIDE across trust boundaries, assets/adversaries, abuse cases, ASVS Level 2 subset. | Ready (lab) | `docs/security/threat-model.md`, ASVS matrix, abuse tests and fixes for findings. |
| `LAB-004` | Backup/restore and SQLite durability: `VACUUM INTO`/`.backup`, WAL, restore to a clean host, corruption/disk-full drill, RPO/RTO; config, certs, tunnels and sessions. | Ready (lab) | Script or command + restore drill + docs; state required for a working deployment is recoverable and the rest is explicitly regenerable. |
| `LAB-005` | Test quality: property-based invariants for `NormalizeHost`, `Authorize`, cookie stripping and ACME/tunnel state; mutation testing; differential proxy tests against Caddy/nginx. | Ready (lab) | Property tests, mutation report, differential harness; weak tests or bugs fixed and recorded. |
| `LAB-006` | CLI quick wins: `switchboard doctor`, `init`, JSON Schema, shell completion, `version`, backup/restore entry points. | Ready (lab) | Commands + tests + docs; `doctor` gives actionable diagnostics and never prints secrets. |
| `LAB-007` | Accessibility and UX: WCAG 2.2 AA for the console and any challenge, keyboard/screen reader/contrast/reduced motion, accessible fallback. | Planned (after alpha) | Audit with axe/Lighthouse plus manual checks; blocking issues fixed; fallback documented. |
| `LAB-008` | Conformance/interop: OIDC conformance suite, ACME with `certbot`/`lego` when PKI exists, HTTP/2 with `h2spec`, TLS with `testssl.sh`, gRPC with `grpcurl`. | Planned (after alpha) | Report with versions, passes/failures and deviations; fixes or explicit non-goals recorded. |
| `LAB-009` | SRE: SLIs/SLOs, error budget, dashboard, game day, MTTR and postmortems. | Planned (after alpha) | SLO doc + dashboard + game-day report; recovery measured, not asserted. |
| `LAB-010` | Supply chain/release security: GoReleaser, cosign, syft SBOM, SLSA, reproducible builds, `gosec`, `gitleaks`, Renovate/Dependabot. | Planned (lab; overlaps `REL-005`/`REL-006`) | Signed release + SBOM + reproducibility evidence + CI checks; no new runtime dependency. |
| `LAB-011` | Docs site and community: versioned docs, changelog, `CONTRIBUTING`, `SECURITY.md`, issue templates and a demo. | Planned | A new user can install and route an app without the author; release notes are public. |
| `LAB-012` | Formal methods: TLA+ or Alloy models for session/auth, ACME or tunnel; invariants such as no unauthorized access, no duplicate active generation, no split brain. | Planned (lab) | Spec + model-checking output + a proven invariant or a bug found and fixed. |
| `LAB-013` | Protocol deep dive: choose one of HTTP/3/QUIC, gRPC, PROXY protocol, DPoP or SPIFFE/SPIRE. | Planned (lab) | Spike + write-up + decision; merge only if a product need exists. |
| `LAB-014` | HA/distributed lab: Postgres adapter, leader election, shared sessions and consistency. | Planned (lab) | Disposable lab; explicit decision whether it belongs to this product; single-binary default remains unchanged. |
| `LAB-015` | AI idea — natural language → policy, graded by the real engine: an LLM drafts Switchboard policy from a sentence (“only ops reaches grafana after hours”); `auth.Authorize` over example Principals is the grader. Compare prompt baseline → prompt optimisation (DSPy/GEPA) → small LoRA fine-tune on the same eval. The model never decides access: drafts go through validation, the engine and human approval. | Idea (after `ARCH-008`) | Eval set + deterministic grader + comparison table (accuracy, cost, latency); no runtime dependency on a model. |
| `LAB-016` | AI idea — prompt-injection detector for the v0.4 “Firewall for AI”: fine-tune a small classifier (DeBERTa/ModernBERT class) on public injection datasets; measure false positives/negatives on a held-out set plus own adversarial attacks (garak/PyRIT); compare with open detectors such as Prompt Guard. | Idea (lab) | Eval report with FP/FN, adversarial results and an explicit statement that detection is one layer, not a control on its own (NIST AI 100-2). |

Optional explorations without an ID yet: hardware-backed keys (TPM/HSM/YubiKey)
and eBPF network observability. Pick a concrete question before assigning a
lab; do not add them to a release.

---

## Later releases (planned, refined at the start of each quarter)

| Release | IDs | Scope |
|---|---|---|
| v0.2 PKI (Jan–Mar 2027) | `PKI-*`, `SHD-003`, `UI-002` | ACME server, internal CA, mTLS agent/origin, developer certs, SSH CA, workload identity; rules language + compiler, PoW challenge (`SHD-003`); `UI-002`: JSON admin API + first Svelte island, a policy simulator over the pure `auth.Authorize` (pulled forward 2026-10-20; builds on `auth.Explain` from `ARCH-008`) |
| v0.3 Access (Apr–Jun 2027) | `ACC-*`, `NTF-*`, `SHD-0xx` | SSH/DB over tunnels, JIT with Telegram approvals, session recording, credential injection, CAEP/SSF, SCIM; Coraza + CRS, API schema validation, leaked credentials, CSP reporting |
| v0.4 Trust (Jul–Sep 2027) | `TLOG-*`, `AI-*`, `AGT-*`, `SHD-0xx` | Transparency log, witnesses, risk signals and explanations, AI-agent/MCP gateway; Web Bot Auth, Firewall for AI, DLP |

Details for each are in [`VISION.md`](../VISION.md).
