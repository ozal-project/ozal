# Implemented — Switchboard

**Last review:** 2026-10-18
**Code reviewed up to:** baseline `0ddb606`; feature commits listed below.

Closed work and its evidence. Active work, blockers and pending tasks live in
[`TO_IMPLEMENT.md`](TO_IMPLEMENT.md). Do not add open tasks here.

## Feature commit checkpoints — 2026-10-18

| Scope | Commit |
|---|---|
| Patched Go toolchain | `802804a` |
| ARCH-004 persistent sessions | `a4d58c1` |
| SHD-001 rate limits | `700d572` |
| EDGE-007 DNS-01 certificates | `2698e62` |
| NTF-001 Telegram notifications | `5c681d3` |
| TUN-002 integrated connector routes | Committed with this change |

Each intermediate feature tree passed `go test -race ./...` in a disposable
copy (later checks limited package-build concurrency to avoid resource-induced
timeouts). The certificate renewal test now waits for both activation and its
event, rather than racing the worker between those two observations. The final
worktree gate remains the completion evidence below. No push was performed.

## TUN-002 (part) — integrated connector routes and console — 2026-10-18 (committed with this change)

The ordinary process can own the private registry-backed mTLS listener and
forward `route.connector` directly through the existing proxy pipeline. The
listener, state database and identities are explicit; public/wildcard transport
binds, ambiguous destinations and protected connector routes fail closed.
The fixed-origin connector receives the visitor IP selected at the edge,
without the separate facade hop. No direct-TCP fallback is possible.

Route/admin reload validates every mapping before installing it, retains the
edge's live sessions, and rejects process-owned listener/identity changes.
Disconnected destinations return 502; revocation closes the live session and
prevents reconnect. The integrated transport has a shared 32-response cap,
released on body close exactly once, and preserves bidirectional HTTP upgrades.
Connect/disconnect/rejection events use the common broker/SSE; safe connector
name and attachment state appear in the existing route console.

Evidence: `TestIntegratedTunnelRoutesReloadAndPreserveVisitorIP`,
`TestIntegratedTunnelRequiresState`, `TestTunnelRouteValidationFailsClosed`,
`TestConnectorRouteRequiresExplicitTransport`, the configured transport
capacity/revocation/upgrade tests, and `TestConnectorStatusProjectionAndEscaping`.
`scripts/verify.sh` completed 7/7 on Go 1.27.2 with staticcheck and govulncheck
(no reachable vulnerabilities; the existing unreachable module advisory remains).
`scripts/smoke-tunnel-configured.py` passed using separate ordinary-edge and
connector processes: trusted visitor IP/spoof stripping, unknown-host rejection,
offline 502, mapping reload, failed-candidate preservation, console projection,
edge restart with the same connector PID, reconnect and live revocation.

Browser acceptance on that disposable process: connection state and tunnel
SSE event observed; activity at 320/768/1024/1440 px and routes at 320 px had no
page overflow. Skip-link keyboard focus worked, page errors/console were empty,
and axe WCAG A/AA reported zero violations, with contrast of existing decorative
marks left as manual-review items. No host services, VPS pilot, DNS or public
listener were changed. TUN-002 remains open for visitor identity assertions
and deployed acceptance; see the active tracker and
[configuration contract](architecture/tunnel-persistent.md#normal-process-integration--2026-10-18-local-delivery).

## Worktree review corrections — 2026-10-18 (feature commits below)

The defects found in the local review are corrected; this closes the review
fixes, not the remaining deployment acceptance of `SHD-001`, `ARCH-004`,
`EDGE-007` or `NTF-001`.

- Rate-limit keys use the router's canonical host: equivalent DNS and IPv6
  forms share both the general and authentication budget.
- SQLite sessions obey the earlier of issued expiry and creation plus current
  TTL. Lookup, sweep and creation purge expired rows under that policy;
  building a reload candidate does not mutate the installed store.
- Certificate shutdown cancels and joins the worker before releasing the
  state lock, including bounded DNS cleanup. DNS-01 has no ACME ALPN
  client-authentication exemption.
- Certificate chains must verify against explicit trusted roots. The shell
  uses system roots by default or an operator-configured staging bundle;
  embedded roots never establish trust. New generation parents are synced
  before activation.
- Reload rejects process-owned DNS-01/notification setting changes and keeps
  the installed snapshot. The recommended toolchain is `go1.27.2`, while the
  language minimum remains `go 1.26.0`.

Evidence: `TestRateLimitEquivalentHostsShareBudget`,
`TestSessionsApplyCurrentTTLWithoutExtendingOriginalExpiry`,
`TestCertificateShutdownWaitsBeforeReleasingState`,
`TestManagedTLSNeverExemptsACMEClientAuthentication`,
`TestParseGenerationRejectsBrokenChain`, the intermediate-chain regressions,
and `TestReloadRejectsProcessOwnedAdapterChanges`. The host-key, persisted-TTL
and malformed-chain regressions failed against the pre-fix working tree.
`go test -race` for the affected packages passed; `scripts/verify.sh` completed
7/7 with Go 1.27.2 (format, vet, uncached race tests, build, four short fuzz
passes, staticcheck, govulncheck, diff check). govulncheck reported no reachable
vulnerabilities; one advisory remains in a required module without a reachable
call. No real ACME issuance, Telegram message or deployment was performed.

## TUN-002 (part) — visitor address across the tunnel — 2026-10-18 (working tree, no commit)

Each side of the tunnel stripped forwarding headers, so the connector and the
application only ever saw the tunnel peer. The edge now sends the address its
trust stage selected in `X-Switchboard-Visitor-IP` (`proxy.WithVisitorAssertion`,
set by `Edge.HandlerFor`); the connector runs `pipeline.TrustAuthenticatedTunnel`
(`proxy.WithAuthenticatedTunnelTrust`, set in `tunnel connect` and the spike), so
one valid unicast address in the header becomes the visitor address and the
application gets a single `X-Forwarded-For`. The header is believed only there:
every other stage strips it in any `-`/`_` spelling, an aliased claim is
rejected whole, malformed/repeated/unspecified/multicast/scoped values fall back
to the tunnel peer, and it never reaches the application. Shared parsing moved to
`parseVisitorAddr`/`headerValues` so `CF-Connecting-IP` keeps the same rules.

Evidence: `TestAuthenticatedTunnelTakesVisitorIPFromTheEdgeAssertion`,
`TestVisitorAssertionIsNeverTrustedOnOrdinaryListeners`,
`TestVisitorAssertionIsSentOnlyWhenAskedAndNeverFromTheClient`,
`TestAuthenticatedTunnelTrustFeedsTheApplicationTheVisitorAddress` and the real
mTLS path in `TestVisitorAddressCrossesTheTunnelAndCannotBeForged`; four
mutations killed (header not stripped, assertion ignored, first of many values
accepted, edge not asserting). `scripts/verify.sh` 7/7 OK. `TUN-002` stays open
(see its tracker row); limits in `tunnel-persistent.md`.

## EDGE-006 (part) — origin client-certificate policy — 2026-10-18 (working tree, no commit)

New package `internal/originauth` and `tls.origin_client_ca_file`: when set, the
public HTTPS listener serves only connections presenting a client certificate
issued by the configured PEM bundle (the shape of Cloudflare Authenticated
Origin Pulls with the zone's own certificate). `Decide` is the pure decision
with stable codes (`origin.allowed`, `no_certificate`, `untrusted`, `expired`,
`invalid`) and requires the client-authentication key usage. `ServerTLS` applies
it through `GetConfigForClient`/`VerifyConnection`, refuses below TLS 1.2 and
emits `origin.denied` with the code and remote address; the activity console
labels it. ACME tls-alpn-01 validation, which cannot present a certificate, is
exempted only for a hello offering exactly `acme-tls/1`; because net/http would
serve a connection that negotiated that protocol, `RequireVerified` rejects
any request without a verified peer certificate. An unreadable or empty bundle
stops startup; setting the option without `tls.enabled` is a config error; a
change needs a restart (`sameServerConfig`).

Evidence: `TestDecide`, `TestLoadPoolRejectsUnusableFiles`,
`TestServerTLSRequiresOurClientCertificate` (real handshakes: no certificate,
other CA, expired), `TestServerTLSLetsACMEChallengesThrough`,
`TestRequireVerifiedFailsClosed`, `TestHTTPServerServesOnlyApprovedClients`
(real `http.Server`, HTTP/2 preserved) and `TestOriginClientCARequiresTLS`.
Three mutations were killed by the tests (ACME exemption widened, key-usage
check dropped, `RequireVerified` disabled). `scripts/verify.sh` 7/7 OK. Not
exercised against Cloudflare, and `EDGE-006` stays open (see its tracker row).

## ARCH-007 — upstream transport lifecycle and limit — 2026-10-18 (working tree, no commit)

- **One transport across reloads.** `ARCH-006` made every snapshot build its own
  upstream transport, so each reload dropped warm connections and left the old
  transport's idle ones open for up to 90 s. The route manager now owns the
  transport like it owns the gate and the sessions: an unchanged reload reuses
  it; a changed setting builds a new one and closes the idle connections of the
  old one when it is replaced (requests in flight finish normally).
- **Configurable limit.** `upstream_timeout_seconds` (default 60, rejected below
  0 or above 3600) sets the response-header limit; documented in the README.
  `proxy.NewUpstreamTransport` is exported for the manager; the single default
  lives in `config.DefaultUpstreamTimeout`.
- **Decision on the tunnel CLI loggers.** `tunnel`, `tunnel-spike` and the edge
  handler keep `slog.Default()`: they are human-run commands whose records go
  to stderr as plain text, and `proxy.WithLogger` already allows injection when
  one needs it. No new task.

Evidence: `TestReloadReusesUpstreamTransport`,
`TestChangedUpstreamTimeoutClosesIdleConnectionsOfPreviousTransport` (fails with
`CloseIdleConnections` removed; checked by mutation) and
`TestUpstreamTimeoutDefaultsAndRejectsOutOfRange`. `scripts/verify.sh` 7/7 OK on
Go 1.27.2. Requests in flight at the moment of replacement can still leave one
idle connection on the old transport for up to `IdleConnTimeout`; accepted.

## ARCH-006 — proxy hardening and one log stream — 2026-10-18 (committed `c097ed5`)

- **Bounded upstream transport.** Routes without a custom transport used
  `http.DefaultTransport`, so an upstream that accepted a connection and never
  answered held the visitor's request and goroutine forever. `proxy` now builds
  an explicit transport (`newUpstreamTransport`): dial 10 s, TLS handshake 10 s,
  response headers 60 s (`WithResponseHeaderTimeout`), idle 90 s; the body stays
  unbounded so downloads and streams work, and `Proxy` is nil so environment
  proxy variables cannot reroute internal upstreams. A timeout answers `504`,
  other upstream failures `502`; the visitor never sees the upstream address
  unless development errors are on.
- **One structured log.** The per-request `log.Printf` became a `proxy.forward`
  record (method, host, path, target; never the query string) and upstream
  failures a `proxy.upstream_error` record, through `slog`. The server path sets
  the JSON logger as the process default, so existing `log.Printf` lines also
  come out as JSON. `proxy.WithLogger` injects a logger in tests.
- **Subcommand table.** `switchboard tunnel|tunnel-spike|connector|migrate` are
  dispatched from `subcommands()` and all run under a context cancelled by
  SIGINT/SIGTERM (`connector` and `migrate` ignored signals before).
- Removed the redundant first `X-Forwarded-For` delete in the proxy rewrite.

Evidence: new tests `TestSilentUpstreamTimesOutWith504`,
`TestUpstreamTransportIsBounded`, `TestForwardedRequestIsLoggedAsStructuredRecord`
(failing to compile before the change: the API did not exist) and
`TestSubcommandsAreRegistered`; existing spoofing, cookie and dev-error tests
unchanged and passing. `scripts/verify.sh` 7/7 OK on Go 1.27.2. Not exercised
against a running server; follow-ups in `ARCH-007`.

## Toolchain, linters and connector PEM check — 2026-10-18 (working tree, no commit)

`staticcheck` and `govulncheck` installed and run through `scripts/verify.sh`.
`govulncheck` found nine standard-library advisories reachable under Go 1.27.1;
Go 1.27.2 fixes them and `golang.org/x/net` moved v0.58.0 → v0.60.0. Reachable
vulnerabilities: 0 in Switchboard and Server Ops. Remaining advisory GO-2026-5932
(`x/crypto`, no fix, not called) is tracked as `REL-007`. `staticcheck` findings
fixed: nil context in `events`, `time.Until` in `tunnelrenew`, an unused variable
in a `routemanager` test.

`fingerprintFromPEM` (`connector enroll`) inspected only the first PEM block, so
a certificate followed by a private key was accepted despite the "only the public
certificate" contract (SA4004 exposed it). It now checks every block. Regression
test `TestConnectorEnrollRejectsKeyAfterCertificate` failed before the change and
passes after. Evidence: `scripts/verify.sh` 7/7 OK on Go 1.27.2 with `GOROOT`
unset (an exported `GOROOT` pointing to 1.27.1 breaks the new toolchain).

## Cloudflare coexistence decision — 2026-10-18 (working tree, no commit)

Owner-approved [architecture decision](architecture/cloudflare-coexistence.md)
recorded: public Portfolio stays on Pages; a separate preview will validate
Cloudflare exterior protection with Switchboard identity/policy and owned
transport. Responsibilities, origin-bypass acceptance, evidence budgets and
control-by-control removal/rollback criteria are documented. VISION, active
tracker and workspace/Server Ops direction aligned. This closes documentation
only; EDGE-006 remains active implementation work. No deployment or provider
configuration change is claimed. Validation: local document links, task
references and whitespace checked; product suites not rerun for prose changes.

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
## Private two-host tunnel delivery — 2026-10-18 (working tree, no commit)

Delivered slice of TUN-002; the task remains open in TO_IMPLEMENT.md.
`switchboard tunnel edge|connect -tunnel-network CIDR` permits mTLS transport
on one explicitly configured private subnet; defaults and HTTP/origin limits
remain loopback-only. Public/wildcard destinations, DNS names, unrelated
subnets, noncanonical prefixes and prefixes spanning public space are rejected.
The disposable tunnel-spike command retains its original restrictions.

Verification: new CLI regression failed before implementation (flag absent),
then passed; the full scripts/verify.sh gate passed (race, fuzz, build; optional
staticcheck/govulncheck unavailable). Existing separate-process persistent smoke
passed. A pilot was installed as two enabled user services on Hetzner/homelab,
with role keys generated locally, public-CSR signing and registry enrolment.
Real origin marker, EN/ES and selected assets matched. Graceful/abrupt edge
recovery retained the live connector PID. Revocation closed the session and
rejected reconnects; a new key/certificate/enrolment restored service. Only
these new pilot services were interrupted; existing staging was preserved.
No public listener, DNS cutover or host reboot was performed.
## Automatic native leaf renewal — 2026-10-18 (working tree, no commit)

Delivered another TUN-002 slice; the task retains its explicit residuals.
`tunnel renew` and the SSH `renew-agent` live in the existing Go binary.
`internal/tunnelrenew` handles local Ed25519 key generation, public CSR signing
with crypto/x509, exact role validation, durable intent and atomic identity
pointers. Connector replacement uses a transactional expected-fingerprint
comparison; rollback cannot override an operator's revocation/replacement.
Both roles are restored before health validation, including an expired pair.
No new Go dependencies or Python/OpenSSL renewal runtime were added.

Observed: full gate/race/fuzz/build passed. Tests cover rotation collisions and
concurrency, lost replies, failed candidates, external revocation, key mismatch,
CA expiry, interrupted journals and the paired recovery/rollback order. A real
forced renewal replaced both role keys/certificates; the previous connector
row is revoked and the new one active. Live homelab-origin/content checks
passed afterward. The daily systemd timer is enabled on the homelab; its first
normal check succeeded without unnecessary renewal and no pending journal
remains. Server Ops owns the units and records the operational evidence.
CA trust-anchor rollover remains a separate managed change. No public exposure,
DNS/firewall change or host reboot was performed.
