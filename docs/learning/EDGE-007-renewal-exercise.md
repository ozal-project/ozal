# EDGE-007: deciding when renewal is due

Status: solution implemented 2026-10-18 in `internal/certs/renewal.go`
(`RenewalDue`) with the test below copied into `renewal_test.go`; the manager
around it lives in the same package. The task is In progress in
[the tracker](../TO_IMPLEMENT.md): the local lifecycle is tested with
simulated ACME/DNS/storage, and real staging/production issuance still needs
the operator token. See the [lifecycle contract](../architecture/stack.md#public-origin-certificate-contract)
for the full acceptance order.

## The maintainer's piece

Implement the pure decision below in the future certificate core. Its package
name is illustrative; this document adds no Go file to the build.

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

Use stdlib `time` and `testing`. Start by copying this test into the chosen
package when implementation work begins. It should fail until you supply the
function; no failing test has been added to the repository build now.

```go
package certificates

import (
    "testing"
    "time"
)

func TestRenewalDue(t *testing.T) {
    start := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
    end := start.Add(90 * 24 * time.Hour)
    boundary := start.Add(60 * 24 * time.Hour)
    tests := []struct {
        name string
        now, notBefore, notAfter time.Time
        want bool
    }{
        {"just issued", start, start, end, false},
        {"before boundary", boundary.Add(-time.Nanosecond), start, end, false},
        {"at boundary", boundary, start, end, true},
        {"after boundary", boundary.Add(time.Nanosecond), start, end, true},
        {"at expiry", end, start, end, true},
        {"expired", end.Add(time.Hour), start, end, true},
        {"not yet valid", start.Add(-time.Second), start, end, true},
        {"zero notBefore", start, time.Time{}, end, true},
        {"zero notAfter", start, start, time.Time{}, true},
        {"empty interval", start, start, start, true},
        {"reversed interval", start, end, start, true},
        {"short lifetime before boundary", start.Add(3*time.Hour-time.Nanosecond), start, start.Add(4*time.Hour+30*time.Minute), false},
        {"short lifetime at boundary", start.Add(3*time.Hour), start, start.Add(4*time.Hour+30*time.Minute), true},
        {"same instant in another zone", boundary.In(time.FixedZone("UTC-3", -3*60*60)), start, end, true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            if got := RenewalDue(tt.now, tt.notBefore, tt.notAfter); got != tt.want {
                t.Fatalf("RenewalDue() = %v, want %v", got, tt.want)
            }
        })
    }
}
```

## Three hints

1. Check invalid intervals before computing their duration. `Time.IsZero`,
   `Time.Before` and `Time.Equal` let you state the cases explicitly.
2. Think in durations from `notBefore`, rather than calendar months or a
   hard-coded number of days. A fractional duration uses nanosecond precision;
   define the boundary as `notAfter` minus one third of the lifetime.
3. Compare instants, not formatted strings or `==` on `time.Time`. Keep the
   clock as the explicit `now` argument; do not call `time.Now` in the function.

After you write it, run the focused test and review each boundary together.
Later manager acceptance must additionally test storage interruption, retry,
DNS record ownership, cancellation and real renewal/restart. Passing this pure
exercise alone proves none of those operational properties.
