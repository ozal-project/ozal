# Performance baseline

This is a local microbenchmark baseline, not a capacity claim or an SLO. The
proxy benchmarks use a synthetic in-process `RoundTripper` and a small reusable
response writer; they measure the Switchboard handler, not a real upstream,
listener, TLS handshake, disk-backed session store, or network path. Go
benchmark `ns/op` is an average, not p50/p99 latency.

## Reproduce

Run on an otherwise idle machine and keep the Go version, CPU power mode,
process limits, and benchmark command the same when comparing revisions. Use
one `GOMAXPROCS` value for serial comparisons; the parallel limiter benchmark
should also be run with the host's normal setting:

```sh
GOMAXPROCS=1 go test -run '^$' \
  -bench 'Benchmark(Authorize|Fresh|Proxy)' \
  -benchmem -benchtime=250ms -count=5 \
  ./internal/auth ./internal/proxy
go test -run '^$' -bench '^BenchmarkLimiter' \
  -benchmem -benchtime=250ms -count=5 ./internal/ratelimit
GOMAXPROCS=8 GOTMPDIR="$HOME/.cache/go-tmp" go test \
  ./internal/audit ./internal/proxy -run '^$' \
  -bench '^(BenchmarkWriterAppendGrouped|BenchmarkProxyProtectedRoute|BenchmarkProxyProtectedRouteRequiredAudit(Parallel)?)$' \
  -benchmem -benchtime=1s -count=3
```

For a CPU profile of the protected proxy path:

```sh
go test ./internal/proxy -run '^$' \
  -bench '^BenchmarkProxyProtectedRoute$' -benchtime=5s \
  -cpuprofile /tmp/switchboard-proxy.cpu
go tool pprof -top /tmp/switchboard-proxy.cpu
```

The benchmark names cover authorization with an email and required group,
passkey freshness, public and protected proxy routes, and single-key rate
limiting both serially and under parallel contention. The proxy uses an
in-process response stub to isolate application overhead. Do not compare its
absolute timings to a live edge.

The durable-audit benchmarks use a private log directory created under the
package working directory, so they exercise that filesystem rather than `/tmp`.
`BenchmarkWriterAppendGrouped` starts exactly 1, 8 or 64 concurrent callers
against one writer; each caller waits for its result from `respondToBatch`,
which is returned after the batch sync. It includes record encoding, hashing,
queueing, writes and the shared `fsync`, but excludes writer startup and close.
The protected-route benchmarks compare the synthetic protected handler with no
audit, `required` audit serially, and `required` audit through `RunParallel`.

## First local result

Captured 2026-10-25 on Linux/amd64, Go 1.27.3, Intel Core i7-1255U, with
`GOMAXPROCS=1`. Each before/after result is the median of five one-second
samples. This was sequential rather than an isolated lab, so repeat on a quiet
host before making capacity claims.

| Benchmark | Before | After | Reading |
|---|---:|---:|---|
| `AuthorizeEmailAndGroup` median | 182.4 ns/op, 24 B/op, 1 alloc/op | 36.46 ns/op, 0 B/op, 0 allocs/op | Email allowlist comparison lost its temporary lowercase allocation. |
| `FreshPasskey` median | 16.42 ns/op, 0 allocs/op | 16.51 ns/op, 0 allocs/op | No meaningful change in the adjacent pure policy check. |
| `BenchmarkProxyProtectedRoute` median | 19.2 µs/op, 35,475 B/op, 32 allocs/op | 10.9 µs/op, 2,680 B/op, 30 allocs/op | Reusing and clearing the 32 KiB response-copy buffer reduced bytes/op by 92%; timing improved in this run. |

## Durable audit cost (AUD-001)

Captured 2026-10-25 on Linux/amd64, Go 1.27.3, Intel Core i7-1255U,
`GOMAXPROCS=8`. Each result is the median of three one-second samples from the
command above. The appenders share one writer and one local audit directory.

| Benchmark | Median | B/op | allocs/op |
|---|---:|---:|---:|
| `WriterAppendGrouped/appenders_1` | 9.09 ms/op | 1,736 | 17 |
| `WriterAppendGrouped/appenders_8` | 1.16 ms/op | 1,517 | 14 |
| `WriterAppendGrouped/appenders_64` | 185 µs/op | 1,489 | 14 |
| `ProxyProtectedRoute` (no audit) | 16.14 µs/op | 3,715 | 40 |
| `ProxyProtectedRouteRequiredAudit` | 9.00 ms/op | 9,152 | 57 |
| `ProxyProtectedRouteRequiredAuditParallel` | 1.48 ms/op | 8,793 | 55 |

The single-appender result includes a separate durable sync for each call. With
8 and 64 callers, group commit amortizes that sync across concurrent records;
all callers still wait for the group sync before `Append` returns. In this
synthetic route test, serial `required` audit is about 558 times the no-audit
average, while concurrent calls share sync work. These numbers are specific to
this host and filesystem. Benchmark `ns/op` is an average, not p50/p99; the
proxy transport is in-process, and the parallel benchmark's concurrency comes
from Go's benchmark harness rather than a controlled arrival-rate load test.
It does not measure physical power-loss guarantees, queue saturation, listener
overhead, TLS or storage on a deployment host. Keep `AUD-001` open until a
representative filesystem and listener-level latency budget are measured.

`internal/auth/authz.go` uses `strings.EqualFold` for email and domain
allowlists. That matches the documented case-insensitive policy, avoids
temporary lowercase strings, and applies Unicode simple case folding
consistently; tests cover ASCII and Unicode pairs. The pure authorization
median fell from 182.4 ns to 36.46 ns and removed one allocation.

The proxy profile showed that `httputil.ReverseProxy` allocated a 32 KiB copy
buffer per response. `internal/proxy/responseBufferPool` now shares one
bounded, non-blocking channel pool across a handler's routes and clears the
full buffer capacity before returning it. It retains at most 32 buffers (1 MiB)
per handler. In the protected-route benchmark, the median fell from 19.2 µs to
10.9 µs, bytes/op fell 92%, and allocations fell from 32 to 30. The response
buffer test checks that bytes and unused capacity are cleared and that the
cleared buffer is reused. This is a microbenchmark result; a listener-level
load test is still needed to confirm throughput and p50/p99 behavior.

## What remains to measure

- Listener-to-upstream throughput and p50/p99 under a repeatable concurrency
  and payload profile, including a real loopback upstream.
- TLS handshake cost with and without connection reuse.
- Persistent SQLite session lookup and write paths under representative load.
- Rate-limit contention with realistic distinct keys and route/identity mix.
- A `benchstat` comparison on repeated, longer runs and an optimization budget
  for each path.

No concurrency redesign or network tuning should be based on the synthetic
proxy numbers above. `LAB-001` remains open until listener-level load
measurements, TLS handshake costs, persistent-session profiles, p50/p99 and
an isolated no-regression comparison are recorded.
