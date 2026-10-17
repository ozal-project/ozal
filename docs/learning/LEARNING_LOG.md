# Learning log

One line per closed task: what was learned, in Go or in the domain. This is
the raw material for each release's write-up. Newest first.

Format: `YYYY-MM-DD · ID · topic — what clicked (link to code)`

## v0.1 Edge

- 2026-10-17 · ARCH-003 · raw vs resolved — keep the config value as the
  operator wrote it and resolve a copy only for the consumer; otherwise the
  atomic write launders the secret into the file. Validation checks shape,
  never content ([secrets.go](../../internal/config/secrets.go)).
- 2026-10-17 · ARCH-002 · publishing decisions — ports and adapters again: a
  small `Sink` interface plus a JSON `slog` adapter and an in-memory recorder
  turn every decision into one stable event type instead of ad-hoc log lines
  ([events.go](../../internal/events/events.go)).
- 2026-10-17 · EDGE-005 · peer versus client IP — `netip.ParseAddrPort`
  validates the transport peer, IP/CIDR membership bounds header trust, and a
  private context-key type passes the selected `netip.Addr` without rewriting
  `RemoteAddr`. Clone slices when capturing policy in an immutable snapshot
  ([client_ip.go](../../internal/pipeline/client_ip.go),
  [trusted_proxies.go](../../internal/config/trusted_proxies.go)).

- 2026-10-17 · ARCH-001 · pipeline order — a stage's *position* is part of
  its contract: stripping the session cookie at the request entry broke the
  identify stage that needs it. Trust strips inbound spoofing; the outbound
  clone is where the cookie dies
  ([pipeline.go](../../internal/pipeline/pipeline.go),
  [proxy.go](../../internal/proxy/proxy.go)).
- 2026-10-17 · BUG-008 · error surfaces — debugging ergonomics and
  information disclosure pull in opposite directions; a variadic `Option`
  keeps the friendly pages off the default path and testable both ways
  ([proxy.go](../../internal/proxy/proxy.go)).
- 2026-10-17 · BUG-006 · atomic writes — `os.Rename` replaces the *name* it is
  given: through a symlink that means replacing the link, not its target;
  `EvalSymlinks` first, then rename and fsync the directory for durability
  ([persist.go](../../internal/config/persist.go)).
- 2026-10-17 · BUG-005 · path ownership — a reserved path is only reserved on
  the hosts that own it; intercepting `/_/auth/*` everywhere silently stole
  application routes. Logout is a state change: POST and `303` back to a GET
  ([manager.go](../../internal/routemanager/manager.go),
  [oidc.go](../../internal/auth/oidc.go)).
- 2026-10-17 · BUG-004 · parsing authorization — `strings.TrimPrefix` treats
  "no prefix" and "wrong prefix" as valid input; `strings.Cut` plus a scheme
  check makes the grammar explicit ([server.go](../../internal/admin/server.go)).
- 2026-10-17 · BUG-003 · startup contract — `ListenAndServe` hides the bind
  inside the serve loop; `net.Listen` first turns "port busy" into a startup
  error the process can report before claiming it is running
  ([main.go](../../cmd/switchboard/main.go)).
- 2026-10-17 · BUG-002 · URL construction — `r.Target + path` is string
  concatenation pretending to be URL logic; validate the input and let
  `url.URL.JoinPath` clean the join
  ([checker.go](../../internal/healthcheck/checker.go)).
- 2026-10-17 · BUG-001 · forwarded headers — `ReverseProxy.Rewrite` +
  `SetURL`/`SetXForwarded` replaces `Director`; `SetXForwarded` **appends** to
  an existing `X-Forwarded-For`, and `SetURL` clears `Host`, so the order
  (drop → rewrite → re-set `Host`) is part of the fix
  ([proxy.go](../../internal/proxy/proxy.go)).
- 2026-10-17 · SEC-005 · ownership of state — a session store built inside
  the object that gets rebuilt (the gate) dies with it; move ownership to the
  component that outlives rebuilds, promote candidates only on install, and
  test the behavior that matters (an old session still authenticates)
  ([manager.go](../../internal/routemanager/manager.go),
  [session.go](../../internal/auth/session.go)).
- 2026-10-17 · SEC-004 · login CSRF — binding a login attempt to the browser:
  sign state + PKCE verifier + `return_to` into a short-lived cookie, compare
  `state` in constant time, clear on first use; going stateless also deleted
  the 10k pending-login exhaustion
  ([oidc.go](../../internal/auth/oidc.go),
  [gate_test.go](../../internal/auth/gate_test.go)).
- 2026-10-17 · SEC-003 · DNS rebinding — the socket address proves nothing
  when a browser is tricked into connecting to 127.0.0.1; the admin plane
  checks the `Host` header itself and rejects everything that is not a local
  name with `421` ([server.go](../../internal/admin/server.go),
  [server_test.go](../../internal/admin/server_test.go)).
- 2026-10-17 · SEC-002 · header canonicalization — Go canonicalizes header
  keys but keeps `_`, and WSGI/PHP/Rack fold it back to `-`; compare normalized
  names and `delete` the raw map key, because `Header.Del` only removes the
  canonical spelling ([proxy.go](../../internal/proxy/proxy.go),
  [proxy_test.go](../../internal/proxy/proxy_test.go)). Raw-socket test.
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
