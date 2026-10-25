# LAB-005: test quality report

Date: 2026-10-25

## Property checks

Added Go fuzz properties for `Authorize`, certificate name and wildcard
handling, and tunnel reconnect backoff. The existing properties for
`NormalizeHost` and cookie stripping remain in place. Each target ran locally
for two seconds and passed:

| Target | Executions | Result |
|---|---:|---|
| `FuzzNormalizeHost` | 24,433 | Pass |
| `FuzzAuthorize` | 5,517 | Pass |
| `FuzzStripCookie` | 10,039 | Pass |
| `FuzzCertificateNameRules` | 6,555 | Pass |
| `FuzzBackoffDelay` | 7,872 | Pass |

The new invariants check authorization against an independent policy model,
stable host normalization, cookie-removal idempotence, safe ACME probe/storage
names, exact one-label wildcard coverage, and bounded tunnel retry delays.

## Defects found and fixed

- Exponential tunnel backoff could overflow a large `time.Duration`; a
  positive base paired with a zero cap could also produce a zero-delay retry.
  Growth now saturates before multiplication can overflow, and a cap below the
  base is treated as the base.
- Certificate name validation enforced the 63-byte label limit but not the
  253-byte full-name limit. It now rejects longer plain and wildcard names.

## Representative mutation checks

`go-mutesting` and `go-mutest` were unavailable. To check whether important
regressions are detected, four single-line mutants were applied to a temporary
copy of the working tree and each corresponding test was run. All four were
killed:

| Mutant | Focused check | Result |
|---|---|---|
| Bypass verified-email guard | `go test ./internal/auth -run=^FuzzAuthorize$` | Killed by unverified-email seed |
| Reverse cookie-name comparison | `go test ./internal/proxy -run=^FuzzStripCookie$` | Killed by session-cookie seed |
| Remove overflow saturation | `go test ./internal/tunnel -run=^FuzzBackoffDelay$` | Killed by near-`MaxInt64` seed |
| Bypass DNS name length check | `go test ./internal/certs -run=^TestValidateNameEnforcesDNSNameLength$` | Killed by 254-byte name |

This is a representative mutation smoke check, not a complete mutation score.

## Differential harness and remaining work

`internal/proxy/differential_test.go` starts the installed Caddy or nginx binary,
forwards the same method, path, query, host, header and body through it and
Switchboard, and compares what the upstream receives. Neither binary is
installed in this environment, so the harness could not be executed here.
Install either binary and run `go test ./internal/proxy -run
TestDifferentialProxyAgainstCaddyOrNginx` to produce the differential result.

The package race tests passed with `go test -race` for `internal/auth`,
`internal/certs`, `internal/tunnel`, `internal/proxy` and `internal/config`.
LAB-005 remains in progress until the external differential test is run and a
full mutation run is available.
