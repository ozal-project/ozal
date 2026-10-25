# Failure drills

This runbook separates repository regressions from operational recovery
evidence for `LAB-002`. Automated tests use disposable fixtures, but they do
not prove behavior under a host, filesystem, provider, or service-manager
failure. No production or shared-host fault injection is part of this lab.

## Safety boundary

- Use a disposable local checkout or VM, throwaway state, and loopback-only
  listeners. Do not use the public preview, homelab services, live ACME
  credentials, production certificates, or real audit/database state.
- Never change the host clock, fill a shared filesystem, make a system
  directory read-only, or kill an unrelated process. Use injected clocks,
  test-owned subprocesses, and a filesystem quota in an isolated VM when that
  is explicitly available.
- Record commit, tool versions, fixture/config shape, commands, timestamps,
  expected and observed results, recovery time, cleanup, and any deviation.
  Keep credentials and private keys out of the record.
- Stop if a listener binds beyond loopback, a test can reach a non-disposable
  dependency, or cleanup ownership is uncertain.

## Existing repository evidence

These focused tests are regression checks, not a completed operational
failure drill. On 2026-10-25, the full curated script passed from the working
tree based on `0c46828`, using Go 1.27.3 on Linux/amd64:
`GOTMPDIR=/home/saint/.cache/go-tmp scripts/lab-002-regressions.sh`. All seven
`go test -race` invocations passed, including the proxy and tunnel cases that
bind fixture-only loopback listeners. Coverage included upstream timeout,
health refusal and 503→200 recovery; tunnel trust, cancellation, restart and
revocation; certificate renewal/fail-closed/restart; audit failure reporting;
SQLite read-only behavior; and audit child-process recovery. No host-level or
production fault was injected, so this result is not evidence of deployment
recovery.

The script uses local test fixtures and kills only the child created by its
audit recovery test. It is a repeatable regression gate, not a live operational
drill.

| Failure | Existing regression and its boundary | Remaining evidence |
|---|---|---|
| Silent/slow upstream | The full script passed `TestSilentUpstreamTimesOutWith504` and `TestUpstream5xxDoesNotPreventRecovery` with `-race`; these cover bounded 504 handling, response-address disclosure, preserving an upstream 503 and succeeding afterward. | The 5xx regression uses an injected `RoundTripper`, not an actual upstream process outage or recovery timeline. |
| Unhealthy upstream | The full script passed `TestUnhealthyUpstreamReturns503` with `-race`; it verifies the proxy refuses to forward when health state is false. | This is a fake health checker, not an actual downstream health-check outage/recovery timeline. |
| Tunnel disconnect/reconnect | `go test -race ./internal/tunnel -run '^(TestTunnelHTTPConcurrencyTrustAndReconnect|TestRequestCancellationClosesOnlyItsStream)$' -count=1` covers stream cancellation and reconnect semantics. `TestConnectorReattachesAfterEdgeRestart` in `internal/tunnel/enrolled_test.go` covers its named restart case. | These are controlled in-process/package fixtures. A disposable service/connector process kill, reconnect timing, and route recovery record remain open. |
| Certificate expiry and renewal failure | `go test -race ./internal/certs -run '^(TestRenewalDue|TestManagerFailsClosedWithoutCertificate|TestManagerKeepsServingAfterFailedRenewal|TestManagerServesPersistedCertificateAfterRestart)$' -count=1` uses fixed time and simulated storage/issuer. | It does not change wall-clock time or prove a live listener's external renewal and recovery. Use the existing explicit staging acceptance path only with a disposable state directory and separate operator authorization. |
| Disk-full/read-only | The full script passed the audit failure reporter, required-access fail-closed, and SQLite read-only cases with `-race`. | Injected errors and a read-only database open are not a physical disk-full event or a live service recovery drill. Exercise quotas only in an isolated VM; confirm required audit fails closed and the service recovers after writable storage returns. |
| Abrupt process death | `TestProcessKillAfterAppendCanRestartAndVerify` kills its own child after a successful sync. `TestAuditRepairIsExplicitAndRecordsTailTruncation` constructs a torn final JSON suffix and proves preview is read-only, explicit repair records the discarded byte count, and the chain verifies. `TestRepairPreservesACompleteRecordMissingOnlyItsNewline` proves a valid record is kept. | The regression models a persisted torn suffix; it does not kill a process during a physical write or simulate power loss. Host-level recovery remains a separate drill. |

## Disposable recovery sequence

Before an operational run, build or select one exact binary and use a fresh
temporary directory for its config, database, audit chain, certificate
fixture, and logs. Keep all listeners on `127.0.0.1` and use only fake
upstreams, a fake connector, and a simulated certificate issuer. Capture a
baseline request and the process health/status output.

For each fault, run one case at a time:

1. Delay or fail one fake upstream request. Confirm the proxy returns a
   bounded failure without exposing internal addresses, then restore the
   fake upstream and confirm a new request succeeds.
2. Stop the fixture connector process. Confirm connector-backed requests fail
   without direct-origin fallback. Restart it and measure time until the
   enrolled connector serves a new request.
3. Advance only an injected test clock across the certificate renewal and
   expiry boundaries. Confirm an existing valid certificate remains in
   service after a failed renewal; with no valid certificate, TLS and
   readiness fail closed. Do not change the machine clock.
4. In a quota-limited disposable filesystem, make only the fixture's audit
   destination unwritable or full. Confirm a `required` protected request is
   not forwarded and the failure is visible without raw filesystem details.
   Restore writability, then verify that new audit appends and protected
   requests recover.
5. Kill only a child process created by the fixture. Reopen the disposable
   state and record exactly which audit sequence is present. If only the
   final record is unterminated, first run `switchboard audit repair -dir
   "$drill/audit"` to inspect the plan; apply it with `-apply` only after
   reviewing the truncation count. Then run `switchboard audit verify -dir
   "$drill/audit"` and confirm the gateway starts. Complete-record hash
   failures and earlier-segment corruption must remain unrepaired.

After each case, inspect the audit chain and route response, record the
recovery interval, stop all fixture processes, and remove only the temporary
directory created by that run. A package test or a passing health check alone
does not establish the whole gateway path.

## Incident record still needed

The active tracker asks for a blameless postmortem of an `origin-cert` startup
failure. This checkout contains no incident timestamp, impact, triggering
configuration, observed logs, or operator actions for that specific failure;
the learning log records successful staging and production certificate
acceptance but is not evidence for the failure. Do not fill in a cause or
impact from inference. Attach the incident evidence first, then record impact,
detection, timeline, contributing conditions, recovery, and specific
follow-up actions without assigning individual blame.

## Completion boundary

`LAB-002` remains open until the focused regressions have been rerun on the
chosen commit, the disposable operational cases have recorded recovery
evidence, and the incident record is completed from source evidence. Physical
power-loss behavior, public deployment recovery, and real external-provider
failures require their own explicitly authorized environment and are not
claimed by this local lab.
