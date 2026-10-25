# EDGE-007: certificate renewal decision

`internal/certs/renewal.go` implements the pure decision `RenewalDue`.
The manager in the same package owns ACME, storage and retries. See the
[lifecycle contract](stack.md#public-origin-certificate-contract) for the
operational requirements and the [tracker](../TO_IMPLEMENT.md) for current
acceptance evidence and remaining work.

```go
func RenewalDue(now, notBefore, notAfter time.Time) bool
```

Policy: renewal is due at the beginning of the last third of the certificate's
actual lifetime, including the exact boundary. A valid certificate before the
boundary returns false; at or after expiry returns true. A zero validity time,
a reversed/empty validity interval, or `now` before `notBefore` returns true so
that the caller revalidates unusable state instead of treating it as healthy.
The caller handles retry/backoff: true never means it should issue in a tight
loop. This function does not check hostname, signature or key matching.

## Regression coverage

`internal/certs/renewal_test.go` covers issuance, before/at/after the boundary,
expiry, not-yet-valid state, zero validity times, empty and reversed intervals,
short lifetimes, and the same instant expressed in another time zone.
The clock is an explicit argument and comparisons use instants.

Manager acceptance additionally covers storage interruption, retry, DNS record
ownership, cancellation and real renewal/restart. Passing the pure decision
tests alone does not prove those operational properties.
