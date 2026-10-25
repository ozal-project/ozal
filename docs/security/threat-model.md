# Switchboard trust boundaries and threat model

**Status:** initial, source-grounded model written 2026-10-25 for `LAB-003`.
This is an engineering aid, not a penetration test, a complete source audit, a
deployment assessment, or an ASVS conformance claim. It records controls and
limits visible in this checkout. A passing unit test proves only the behavior
its assertions cover.

**Scope:** the current single-process gateway: host routing, OIDC and passkey
login, route authorization, the loopback admin plane, outbound connector
tunnels, static-site deployment, ACME TLS, local SQLite state, and optional
local access audit. Planned private CA, SSH, JIT approval, Shield, agent/MCP
gateway and public transparency log are not current capabilities.

**Not observable from source:** public ingress and firewall rules, DNS/proxy
configuration, operator account and filesystem ownership on a deployment,
external IdP configuration, upstream application trust, host compromise,
backup location, and whether a checkpoint is held outside the gateway host.
These require separate owner-observed acceptance; the code cannot establish
them.

## Security goals

1. Only a configured authentication path can create a trusted principal.
2. Route authorization runs in the trusted Go process with owner-declared
   policy; client-supplied identity and forwarding headers do not become
   identity evidence.
3. The upstream receives identity only after the edge has authenticated and
   authorized the request. It must not be directly reachable through an
   untrusted path that bypasses the edge.
4. The admin surface stays on loopback or the owner-controlled Unix socket and
   mutations require the corresponding local or bearer authority.
5. Connector identity is tied to a verified certificate and enrolled
   connector name; visitor assertions are accepted only from an authenticated
   tunnel path.
6. Missing authorization evidence fails closed. Audit claims stay within the
   records and checkpoints actually verified.

## Assets and principals

| Asset | Security consequence if exposed or changed |
|---|---|
| Upstream applications and their data | Unauthorized access or actions |
| OIDC client secret, passkey directory, session IDs and signing material | Login impersonation, session theft or recovery bypass |
| Route config, admin bearer token and admin socket | Unauthorized policy changes or control-plane actions |
| Connector keys, registry and live tunnel sessions | Impersonation of a trusted network path or origin |
| ACME account key, DNS token, certificate/key pairs | Certificate issuance or TLS identity compromise |
| SQLite sessions, passkey and deployment state | Session persistence, identity or site integrity compromise |
| Access audit records and checkpoints | Loss or falsification of the evidence used to explain decisions |
| Visitor IP, subject, route and decision metadata | Privacy exposure or unwanted correlation |

Relevant principals are anonymous Internet clients, authenticated visitors,
the external OIDC provider, operator browser/CLI, connector processes,
upstream applications, the local service account, host administrator/root,
and build/release infrastructure. Root and a compromised host are outside the
protection offered by local file permissions or a local hash chain.

## Trust boundaries

| Boundary | Lower-trust input | Enforcement visible in this repository | Residual limit |
|---|---|---|---|
| Client → public listener | Host, path, cookies, headers, body and peer address | Host normalization and route lookup; trust stage removes client identity and forwarding headers; optional trusted-proxy list is explicit. See `internal/pipeline/client_ip.go`, `internal/pipeline/pipeline.go`, and `internal/proxy/proxy.go`. | Real firewall, proxy ranges and IPv6 reachability are deployment facts. A directly reachable origin may bypass upstream edge controls unless the operator closes that path. |
| Browser → OIDC callback | Authorization response, state cookie, return URL and provider claims | Random state/verifier, sealed short-lived login cookie, PKCE S256, issuer/client token verification, allowlisted return hosts and session creation. See `internal/auth/oidc.go`, `internal/auth/gate.go`. | Trust still depends on the configured issuer, client registration, TLS, provider account security and deployment callback URL. |
| Browser → passkey login | WebAuthn challenge, credential assertion and origin/RP ID | WebAuthn validation, single-use invitation flow, credential directory lookup, user/session checks and device revocation. See `internal/passkey/gate.go`, `internal/passkey/directory.go`. | Recovery is host-operator controlled; account proofing, browser compatibility and real passkey lifecycle acceptance remain separate. |
| Visitor → route authorization | Session principal, route policy and request context | Pure authorization in `internal/auth/authz.go`; context signals preserve source/unknown; protected audit can be required before forwarding. See `internal/pipeline/policy_context.go` and `internal/proxy/proxy.go`. | Policies describe the gateway's decision, not authorization inside the upstream. App bypass and downstream trust must be checked per deployment. |
| Gateway → upstream | Forwarded request and injected identity headers | Client identity headers and session cookie are stripped; edge writes the authenticated identity after authorization. See `internal/pipeline/visitor.go` and `internal/proxy/proxy.go`. | Upstream must only trust these headers from Switchboard or an equally trusted edge. Network reachability is not enforced by this code. |
| Edge → connector | Client certificate, connector name, framed HTTP request and visitor assertion | TLS peer identity, enrolled-name authorization, duplicate-session protection, revocation and stream cancellation. See `internal/tunnel/tls.go`, `internal/tunnel/registry.go`, `internal/tunnel/transport.go`. | Private CA, certificate distribution, host networking and any public ingress remain deployment boundaries; tunnel acceptance is tracked separately. |
| Operator → admin plane | HTTP Host, bearer token, local socket, mutation payload | Loopback-host check, bearer checks on TCP mutations, Unix-socket local marker and bounded request bodies. See `internal/admin/server.go`, `cmd/switchboard/main.go`, `internal/admin/local.go`. | Loopback binding does not defend against a compromised local browser/process. Unix permissions and systemd runtime ownership need host verification. |
| Process → persistent state | Config, SQLite, ACME files, static deployments and audit directory | Strict config validation, secret references, explicit migrations, bounded site archives, atomic route snapshots and owner-only audit files. | Broader filesystem ACLs, disk-full/read-only behavior, backups, encryption at rest and Windows audit ACLs need separate verification. |
| Local audit → independent witness | Local JSONL chain, signing key, checkpoint and public-key pin | Sequence/hash validation and optional Ed25519 checkpoint verification. See `internal/audit`. | The verifier explicitly does not claim tamper evidence without an independently retained checkpoint. Admin/login/cert/tunnel coverage, external-retention drills, key rotation and pruning remain open. |

## STRIDE abuse cases and current evidence

| STRIDE | Abuse path | Existing evidence in source/tests | Remaining condition or work |
|---|---|---|---|
| Spoofing | Submit identity, forwarding or tunnel-visitor headers from an ordinary client. | `TestTrustIsOptInAndRejectsAmbiguousAliases`, `TestVisitorAssertionIsNeverTrustedOnOrdinaryListeners`, `TestSpoofedIdentityHeadersOverRawSocket`, `TestVisitorAssertionIsSentOnlyWhenAskedAndNeverFromTheClient`. | Recheck the exact upstream route and ingress topology before exposing a new deployment. |
| Tampering | Change route policy or persistent access history without operator authority. | Route mutation bearer/socket checks; immutable numbered SQLite migrations; local audit sequence/hash checks and tamper/truncate tests. | Local admins can still rewrite local state; no external witness is configured or proven. |
| Repudiation | Deny that a protected decision occurred or claim the audit proves more than it does. | Required audit append precedes protected response/forwarding; hash-chain and process-kill recovery tests exist. | Login, admin, certificate and tunnel changes are not yet in durable audit; local chain alone is not independently tamper-evident. |
| Information disclosure | Send cookies, auth headers, secrets or raw request details to the upstream, event stream or diagnostics. | Cookie/header stripping tests; admin/status and policy explain tests assert config secrets are absent. Production event sinks remove `Event.Error`; broker, fan-out, structured-log, admin-projection, notification, path-bearing Pages and invalid-route-host regressions passed the constrained package run below. `BUG-014` gives storage and admin failures distinct stable types so they cannot look successful. | Audit data includes subject, host, decision reason and peer IP; define retention/access and validate journal access. |
| Denial of service | Exhaust proxy connections, upload/archive space, login endpoint, audit disk or connector streams. | Bounded proxy transports and site archive limits; rate limiter; session cap; append failure is fail-closed for required protected routes; tunnel cancellation tests. | Shared host resource budgets, slow/5xx upstream, real disk-full/read-only behavior, traffic limits and recovery timing remain lab/deployment work. |
| Elevation of privilege | Use a weak claim, stale session, unauthorized connector or an omitted context signal to reach a protected route. | Pure authorization, fail-closed unknown context, session rotation/expiry, disabled-user refusal, connector certificate/name checks and passkey step-up tests. | The upstream's own authorization and the real provider's claims/configuration are outside this source boundary. |

These cases are a threat-model map, not a confirmed vulnerability list. The
initial documentation pass did not confirm a source-level defect. The
package-level source review found raw error strings could reach production
event observers, including a filesystem path from failed static-site writes;
it is tracked as `BUG-014`; the selected regressions below passed. Open
operational and coverage gaps below are not assigned
vulnerability severity.

## Controlled validation plan

The tests named above are repository regressions, not proof of deployment
behavior. The following cases define the next disposable-fixture run. Use only
loopback listeners, temporary credentials and throwaway state; never run
failure injection against the public edge or a shared host. Record the commit,
configuration shape, commands, expected and observed results, and cleanup for
each case. The full listener-level fixture plan has not run; a constrained
package-level subset was run as recorded below.

| Case | Fixture and stimulus | Expected evidence | Current automated coverage / remaining gap |
|---|---|---|---|
| Forged visitor identity | Start a disposable ordinary listener and an echo upstream. Send identity headers using hyphen and underscore spellings plus forwarded-IP aliases. | Upstream sees only edge-generated identity after valid authorization; rejected or public requests carry no trusted visitor assertion. | Raw-socket proxy and trust-stage regressions cover representative spellings. Repeat against the listener composition to confirm adapter wiring. |
| Admin boundary | Run with a temporary mode-0700 runtime directory, Unix socket and loopback TCP admin listener. Try mutations with no token, wrong token, correct token on TCP, and the same requests on the socket. | Unauthorized TCP mutations fail; permitted socket-only operations are marked local; a regular file at the socket path is not replaced. | Server/socket tests and CLI socket integration cover these branches. Verify behavior with the actual disposable service unit and record filesystem ownership. |
| Login replay and callback confusion | Use a local stub OIDC provider and fresh browser/session store. Replay state, change issuer/redirect host, reuse callback data, and attempt a second login before the first flow expires. | No session is created for invalid or replayed callbacks; valid state creates one fresh session and the configured return host is enforced. | OIDC callback integration covers the flow. Confirm the negative cases in a loopback-only fixture; do not use production IdP credentials. |
| Connector identity | Start a disposable tunnel listener and registry. Try an untrusted certificate, a valid certificate under an unenrolled name, duplicate enrollment/session, and a revoked certificate. | Handshake or registration is refused before an upstream request is served; accepted connector identity matches the enrolled name. | Tunnel package tests cover certificate/name and lifecycle checks. End-to-end listener acceptance remains with `TUN-002`/`EDGE-006`. |
| Archive and storage limits | Upload traversal entries, symlinks, excessive entry counts and compressed files over configured limits to a temporary site store. | Extraction stops within limits, writes no path outside the site root, and preserves the existing deployment on rejection. | Hostile-archive and upload-cap regressions exist. Recheck atomic deployment behavior with the disposable store and quota settings. |
| Audit failure and recovery | Use a temporary audit directory; inject append failure and terminate a child after a successful sync, then reopen and verify. | Required protected routes fail closed before forwarding; best-effort behavior follows route policy; recovered records verify without silently accepting truncation. | Append-failure and SIGKILL recovery tests exist. Disk-full/read-only and external checkpoint retention remain separate `LAB-002`/`AUD-002` drills. |

For each run, stop if the fixture can reach any non-loopback address, if a
credential is not disposable, or if cleanup ownership is unclear. A pass is
limited to the exact commit and fixture recorded; deployment controls and an
independent review remain separate evidence.

### Offline package-level regression subset — 2026-10-25

The worktree was based on `d67c76d` and contained other uncommitted changes.
The source snapshot copied only `cmd/`, `internal/`, `go.mod` and `go.sum` to a
temporary directory, made the copy read-only, used a minimal environment with
module and checksum downloads disabled, and applied CPU, file, process and
descriptor limits. The selected tests ran with the race detector. They did not
start listeners or contact a provider; the archive tests used temporary
directories. `env -i` exposed only Go toolchain, module-cache, compiler, cache,
and scratch paths. The temporary source and scratch directory were removed
after each run; the separate Go build cache was removed after verification.

| Package | Selected regressions | Observed result |
|---|---|---|
| `internal/auth` | `TestAuthorize`, fail-closed context rule, step-up freshness, callback state mismatch | Pass |
| `internal/config` | context-rule validation and rejection of unknown signals | Pass |
| `internal/pipeline` | spoofed identity-header stripping | Pass |
| `internal/proxy` | required-audit failure before upstream; injected 503 then 200 recovery | Pass |
| `internal/pages` | hostile archive path/type/limit and invalid archive rejection | Pass |
| `internal/admin` | bearer checks, path-bearing deployment error, admin event projection | Pass |
| `internal/audit` | edit/reorder/truncate detection, damaged-chain refusal, SIGKILL reopen/recovery | Pass |
| `internal/events` | logger, fan-out and broker redact raw errors | Pass |

One focused `go test -race -p=1` invocation ran the eight packages with a union
of the named test functions, including `TestStorageFailureDoesNotExposeFilesystemError`,
`TestEventViewOmitsRawError`, `TestSlogSinkEmitsOneJSONRecordPerEvent`,
`TestMultiClearsRawErrorsBeforeForwarding` and
`TestBrokerRedactsRawErrorsFromHistoryAndLiveEvents`. All eight package
targets passed. This is regression evidence for the named cases, not the
disposable live-listener plan, a penetration test, or independent review. A
separate embedded-console check passed after the build: the Go test served the
index, confirmed its hashed script URL, and found the `pages.failed` label in
the returned JavaScript. The next run must exercise listener adapters in a
network-isolated fixture.

### Observer-event follow-up — 2026-10-25

After the initial eight-package subset, a separate read-only source snapshot
ran the full `internal/events` and `internal/notify` race suites plus
selected `internal/admin` regressions. The cases covered typed config/route
failure events, stable codes and reasons, raw-error omission, invalid
route-host redaction, notification delivery without error text, and all event
labels in the embedded console. All three targets passed. A subsequent
read-only race/vet matrix passed for the full `internal/events`,
`internal/notify`, `internal/config`, `internal/policycontext`,
`internal/pages`, `internal/ratelimit` and `internal/store/sqlite`
packages; focused admin regressions and `go vet` for admin also passed.

The console was checked and built from a temporary copy with source and
installed dependencies read-only except for Vite's generated `.vite-temp`
scratch directory, no package install or network fetch, and process/time/file
limits;
`svelte-check` reported zero errors and warnings and Vite built the new hashed
asset. The embedded-console test confirmed the index points to that asset and
the bundle contains the labels for Pages, route-create, route-delete and
config-reload failures. These checks did not start listeners. The full
`scripts/verify.sh`, browser acceptance, network-isolated listener plan and
independent review remain open.

## Highest-value remaining work

1. Complete `AUD-001`/`AUD-002`: durable audit for identity and trust changes,
   explicit retention, key rotation and an operator drill that preserves a
   checkpoint outside the host.
2. Complete `EDGE-001`/`EDGE-006` with separate direct-origin and
   proxy-origin acceptance, real deployment configuration evidence, rollback
   and visitor-IP provenance.
3. Complete tunnel and certificate lifecycle acceptance (`TUN-002`,
   `EDGE-007`) without treating package tests as proof of public deployment.
4. Close `LAB-002` using isolated slow/5xx, disk-full/read-only, clock-skew,
   certificate-expiry and process-kill drills with measured recovery and a
   factual postmortem; do not simulate host-wide failures on shared production.
5. Extend `CTX-010` only after a privacy-reviewed contract can relate configured
   principals/groups to audit subjects; until then the report must keep usage
   unknown.
6. Run browser/keyboard acceptance and the remaining ASVS subset checks in a
   controlled local fixture before making release-readiness claims.

## Evidence boundaries

The repository's `scripts/verify.sh` passed on 2026-10-25 during this work. It
ran the Go race suite, build, short fuzzers, `staticcheck`, `govulncheck`,
console checks/build, passkey-page checks, console state tests and
`git diff --check`. This gate is regression evidence, not a penetration test
or comprehensive ASVS assessment. The verifier reported no reachable/imported
vulnerabilities; it also reported one issue in a required module whose
affected symbols were not found to be called.

Browser review is still limited by the absence of Chromium in this
environment. Deployment network controls, provider settings, external
checkpoint retention, clean-host recovery and Windows private audit ACLs were
not verified in this pass.
