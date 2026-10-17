# Learning log

One line per closed task: what was learned, in Go or in the domain. This is
the raw material for each release's write-up. Newest first.

Format: `YYYY-MM-DD · ID · topic — what clicked (link to code)`

## v0.1 Edge

- 2026-10-17 · SEC-001 · cookie stripping — a proxy owns the trust boundary:
  parse the `Cookie` header, drop only Switchboard's `sb_session`, and prove
  with `httptest` that it never reaches the upstream
  ([proxy.go](../../internal/proxy/proxy.go),
  [proxy_test.go](../../internal/proxy/proxy_test.go)).
- 2026-10-17 · PAGES-001 · path traversal — `path.Clean` stops `..` in the
  URL, but only `os.OpenInRoot` stops symlinks; defense in depth = pure check
  + kernel-level check ([resolve.go](../../internal/static/resolve.go),
  [handler.go](../../internal/static/handler.go)). First fuzz target.
- 2026-10-07 · — · architecture — functional core / imperative shell and
  ports-and-adapters chosen; `auth.Authorize` is the reference example of a
  pure decision function ([authz.go](../../internal/auth/authz.go)).

## Go concepts met so far

Tick when you have written one yourself, not just read it.

- [ ] `net/http` handlers and middleware (`func(http.Handler) http.Handler`)
- [ ] `httptest.NewServer` / `httptest.NewRecorder`
- [ ] Table-driven tests and `t.Run`
- [ ] Error wrapping with `%w`, `errors.Is` / `errors.As`
- [ ] Interfaces declared by the consumer
- [ ] `sync.Mutex` vs `atomic.Pointer`
- [ ] Goroutines with `context` cancellation
- [ ] Channels and `select`
- [x] Native fuzzing (`go test -fuzz`) — `FuzzResolve`
- [ ] `embed.FS`
- [ ] `log/slog`
- [ ] `crypto/x509`, `crypto/ed25519`
- [ ] Benchmarks (`testing.B`)
