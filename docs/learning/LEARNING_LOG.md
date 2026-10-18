# Learning log

One line per closed task: what was learned, in Go or in the domain. This is
the raw material for each release's write-up. Newest first.

Format: `YYYY-MM-DD · ID · topic — what clicked (link to code)`

## v0.1 Edge

- 2026-10-17 · REL-002 · fuzzing invariants — a fuzz target is only useful
  when it asserts a property, not just "does not panic": round-trip,
  idempotence and allowlist membership turn random bytes into security
  evidence ([fuzz_test.go](../../internal/config/fuzz_test.go),
  [fuzz_test.go](../../internal/auth/fuzz_test.go)).
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

## 2026-10-17 — local outbound tunnel experiment (assistant implementation)

The connector initiates TCP while the edge opens yamux streams: multiplexing
client/server roles are independent of TCP dial/listen roles. TLS verifies CA,
EKU and peer identity; the edge additionally authorizes the connector SAN.
`http.Transport.DialContext` swaps the delivery mechanism while preserving the
existing request pipeline. `context.AfterFunc` and joined workers close sessions
and listeners; a request cancellation closes one stream rather than the tunnel.
The maintainer requested the complete implementation; this does not tick any
"written yourself" boxes above. Local checks and a repeated full repository gate passed after the separate
SHD-001 bucket exercise was completed in parallel.

## 2026-10-17 — commit checkpoint: rate limits and live activity

`6185098` injects time into the pure token bucket and protects keyed buckets with
a mutex. Trusted client-IP context survives nested trust stages; identity and
route-specific limits remain pending. `e432abf` uses bounded channels and a
non-blocking select for event fan-out; slow subscribers drop events instead of
blocking requests. Request-context cancellation releases the SSE subscription.
CI workflow `b2602e9` is committed but hosted verification is still pending.
The local tunnel delivery is committed in `fd72b2c`.

## 2026-10-17 — TUN-002 partial checkpoint: named connectors

Certificate-chain verification establishes trust in the issuing CA; a separate
`Authorizer` checks the leaf-certificate fingerprint against active enrolments.
Partial unique SQLite indexes preserve revoked history while preventing duplicate
active names and certificates. `ConnectLoop` uses cancellable timers and jittered
exponential backoff; `Link` publishes snapshots under a mutex. The full local
gate passed again before committing this slice, including race tests for named
routing, rejection, live revocation and reattachment after an edge restart.
TUN-002 remains open for command/config/admin integration, visitor assertions
and credential rotation.

## 2026-10-17 — migration operator scripts (ARCH-004 partial)

`migrate.sh up|status` delegates to the existing embedded Go runner, preserving
checksums, ordered transactions and history. Status can initialize the history
table on a fresh database; it is not a read-only database open. The missing
`new-migration.sh` now validates contiguous numbering and uses Bash noclobber to
avoid overwriting an existing file. Both scripts are executable. A disposable
SQLite smoke exercised pending status, both shipped migrations, repeated apply
and applied status; an isolated generator fixture checked invalid names and a
numbering gap. Application session wiring remains pending under ARCH-004.

## 2026-10-17 — TUN-002 command integration (partial)

The composition root now wires the same fingerprint registry into `tunnel edge`
and uses `ConnectLoop` for `tunnel connect`. File-based identities survive
process restart; schema migration stays an explicit operator action. A shared
`openMigratedDB` helper checks migration history for enrolment and edge startup.
The edge owns both listeners and joins both workers on cancellation/error;
`signal.NotifyContext` propagates SIGINT/SIGTERM into that ownership tree.
CLI race tests passed. A separate-process smoke served Portfolio ES/EN and
assets, restarted/killed only its disposable edge, reattached the original
connector process and cut access after live revocation. Visitor propagation
remains anonymous and strips identity/forwarding headers; config/reload/admin,
authenticated visitor assertions and credential rotation remain open.

## 2026-10-17 — UI-001 console redesign (partial)

Embedded HTML/CSS/JS assets separate presentation from the Go handler while
keeping a single binary and same-origin CSP (inline styles are no longer
required). The overview serializes an explicit access-rule projection, keeping
secrets, allowlist identities and upstream paths out. Event fields use
textContent; browser checks confirmed that HTML-looking event text stays inert.
Filters use retained events, pause freezes a snapshot while buffering continues,
and animation-frame batching coalesces stream replay renders. Navigation makes
activity the entry point; route-debug probes are expandable and static files
are marked Not probed. Gate and browser checks passed; automated axe reported
zero WCAG A/AA violations, with decorative glyph contrast flagged for manual
review. Mobile at 320px has no document overflow. Protected OIDC-route acceptance
and integration of the separate tunnel CLI remain open.

The final UI assets passed the full gate again. The local Switchboard binary
was rebuilt and its development unit restarted; live loopback HTTP/browser
checks confirmed the new pages. Server Ops returned 200 from the host browser;
the remote VS Code client needs its own 8095 forward.


## 2026-10-17 — console visual identity and access-entry boundary

Switchboard now uses a dark/amber console with sidebar navigation on desktop
and compact tabs on mobile. The self-hosted Manrope font includes its OFL
license and requires same-origin font-src in CSP. Routes & hostnames explicitly
separates local routing rules from publishing DNS records. Server Ops owns the
browser launcher and SSH forwarding metadata in ports.json; Switchboard does
not duplicate that registry. The full gate passed and Chromium reported no
automated WCAG A/AA violations; decorative glyph contrast/empty table checks
were incomplete. Both console pages fit 320/768/1024/1440 px without document
overflow. Browser behavior from UI-001 remains intact.


## 2026-10-17 — CLI and console commit checkpoint

The maintainer requested committing the verified working tree. This checkpoint
includes the migration operator scripts, registry-backed persistent tunnel CLI,
real-process smoke, embedded access console and dark/amber visual identity.
The full repository gate and browser/process checks were run during this session;
no product code changed after the final checks. TUN-002 and UI-001 retain their
explicit remaining acceptance work in TO_IMPLEMENT.md. No push is part of this
checkpoint.
## 2026-10-18 — explicit private transport and two-host mTLS pilot

`netip.Prefix` represents the transport allowlist as a value. Checking both
containment in an RFC1918/ULA block and prefix length prevents a prefix such
as 10.0.0.0/7 from including public space. This permission applies only to
mTLS addresses, not HTTP listeners or connector origins. CLI table tests
exercise the policy before keys/sockets are opened; the real two-host smoke
proves the authenticated data path. Graceful and abrupt edge recovery preserve
the connector process. Revocation and a new key/certificate demonstrate why
certificate validity and registry enrolment are separate authorization checks.
Delivered in the working tree without a commit; TUN-002's remaining scope is
unchanged in TO_IMPLEMENT.md.
## 2026-10-18 — native leaf renewal and guarded recovery

SQLite transactions make retiring the exact previous fingerprint and enrolling
the new one a single operation. The expected value prevents stale maintenance
or rollback from undoing an operator revocation. A fsynced intent journal plus
atomic symlink replacement makes the multi-host operation resumable; the file
lock disappears with its process. Go's crypto/x509 signs and verifies public
CSRs while each host keeps its own private key. Restoring both roles before
the health probe handles a pair that expired while offline. Native tests and a
real forced renewal passed; the daily timer was enabled without publishing.

## 2026-10-18 — coexistence and measurable independence

A private mTLS transport proves connector identity, not visitor WAF coverage.
An exterior proxy can protect the network while Switchboard owns access
policy; source filtering alone does not identify our deployment. Requests
blocked before the origin cannot measure our detector's coverage. Gradual
independence needs controlled traffic, explicit budgets and a removal/rollback
drill. Architecture decision documented; public Portfolio stays on Pages.
No Go code or runtime changed in this documentation task.

## 2026-10-18 — a loop that cannot loop, and reachable advisories

A `for` whose body always returns is an `if` in disguise: it checked only the
first PEM block, so a certificate followed by a private key passed a check that
promised "only the public certificate". staticcheck's SA4004 pointed at it; the
regression test failed before the fix. `pem.Decode` returns the rest of the
input, and the loop must run until the block is nil. govulncheck reports only
advisories the code can reach: the fix was a newer toolchain, and an exported
`GOROOT` silently pins the old standard library even when `go` itself is new.

## 2026-10-18 — a proxy that waits forever

`http.DefaultTransport` bounds dialing and the TLS handshake but not how long
an upstream may stay silent after the request: `ResponseHeaderTimeout` is zero.
A bounded transport limits every phase before the body and leaves the body
alone so streams keep working. Building it field by field, instead of cloning
the default, also drops `ProxyFromEnvironment`, which would reroute internal
upstreams if `HTTP_PROXY` were set. `net.Error.Timeout()` through `errors.As`
tells "too slow" (504) from "unreachable" (502). `slog.SetDefault` in the
composition root routes `log.Printf` through the same JSON handler, so one
line unified every log. A `map` of functions replaces four copied `if`s, and
`signal.NotifyContext` cancels them all on SIGTERM.
