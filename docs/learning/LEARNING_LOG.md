# Learning log

One line per closed task: what was learned, in Go or in the domain. This is
the raw material for each release's write-up. Newest first.

Format: `YYYY-MM-DD · ID · topic — what clicked (link to code)`

## v0.1 Edge

- 2026-10-18 · EDGE-007 · real ACME DNS-01 — the simulated tests were the
  contract; the operator run proved it end to end: token as a file reference,
  TXT created and removed by its own API ID, certificate served on :443, and a
  restart reusing the stored generation instead of re-issuing (serial stable).
  One state directory belongs to one ACME directory forever, so staging and
  production never share `origin-cert*`
  ([certs](../../internal/certs/), [hetzner-public](../../../server-ops/deploy/hetzner-public/README.md)).
  Production repeated the path privately, and the origin boundary held: no
  certificate and an untrusted leaf both die with a TLS alert, a CA-signed
  disposable leaf serves the site. `openssl s_client` can exit 0 while
  printing an alert — trust the full output or a real client (`curl`).
- 2026-10-18 · SHD-002 · header policy and log safety — HSTS only means
  something over TLS, so it is set conditionally; baseline security headers go
  before the host check so even a rejected 421 carries them. Same principle
  for logs: a denial anyone can trigger must aggregate, or the log becomes the
  attack surface ([server.go](../../internal/admin/server.go),
  [originauth.go](../../internal/originauth/originauth.go)).
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

## 2026-10-18 — who owns a resource across reloads

A resource that outlives one request needs an owner with a lifetime. The
transport was built per snapshot, so reloads threw away warm connections and
leaked idle ones. The route manager already solved this for the OIDC gate and
the sessions: keep the installed object while its settings are unchanged, build
a new one when they change, and release the old one at the swap.
`CloseIdleConnections` only affects idle connections, so in-flight requests
finish normally. A test that counts connections races with net/http returning a
connection to its pool, so the deterministic check compares transport identity
and the closing test waits a moment before reloading. Mutating the fix (removing
the close) and seeing the test fail proves the test actually guards it.

## 2026-10-18 — authenticating the proxy, not the network

An IP allowlist says "this packet came from Cloudflare's network", any
customer's included; a client certificate from our own CA says "this is our
deployment". Let Go ask for the certificate (`RequestClientCert`) but decide in
`VerifyConnection`: verification failures then carry a code and an event, and
the decision is a pure function that is easy to table-test. TLS 1.3 lets the
client finish its handshake before the server judges its certificate, so a test
only sees the rejection when it reads. ACME tls-alpn-01 cannot present a client
certificate, so exactly that hello is exempted — and since net/http would then
serve plain HTTP on that connection, a second lock (`RequireVerified`) refuses
any request without a verified peer certificate. Mutating each rule and watching
a test fail is how I know the tests guard them.

## 2026-10-18 — who is allowed to say who the visitor is

A header is only as trustworthy as the channel it arrived on. Over the public
listener any client can send `X-Switchboard-Visitor-IP`, so every ordinary stage
strips it; inside the mutually authenticated tunnel only the edge can, so the
connector believes it. The trust decision belongs to the *listener*, not to the
header name, which is why it is a separate stage (`TrustAuthenticatedTunnel`)
that must be used only on a handler the tunnel alone can reach. Parsing is shared
(`parseVisitorAddr`) so no path accepts a weaker address than another, and a bad
claim falls back to the peer instead of failing the request. Refactoring the
Trust stage into one `trustStage(resolve)` function removed the duplicated
strip-then-store logic: the difference between the two stages is one function.

## 2026-10-18 — owning a certificate lifecycle (EDGE-007)

`x/crypto/acme` is a protocol client, not a manager: it signs JWS, fetches
nonces and polls authorizations, but choosing when to renew, where to store the
result and what to do after a crash is the caller's job. The package keeps
those jobs separate on purpose. `RenewalDue` is six lines of pure arithmetic
(the boundary is `notAfter - lifetime/3`) and takes `now` as an argument, so
every test runs without sleeping. The manager owns exactly one goroutine that
may attempt issuance (`atomic.Bool.CompareAndSwap`), keeps the previous
generation in service until a validated replacement is durably on disk, and
fails closed when there is none: a handshake without a valid certificate is
rejected instead of answered.

Two details made the difference between "works in tests" and "safe on a box".
First, a crash can interrupt anything, so the file store writes `cert.pem`,
`key.pem`, fsyncs them, and only then writes a completion marker; `Load` scans
for the newest directory that has the marker, which makes an interrupted save
invisible and never pairs a certificate with another generation's key. The
second is DNS cleanup: the TXT record is deleted by the exact record ID the API
returned, the intent to delete it is persisted *before* the create call, and a
create with an ambiguous outcome (timeout, 5xx) is recorded but never guessed
at — recovery reports it instead of deleting by name. An unrelated
`_acme-challenge` record survives every path, which the simulated-provider
tests assert directly.

The ACME client also taught a subtle lesson: it retries 429 responses itself,
honoring `Retry-After` by sleeping inside the call, which in a five-minute
attempt looks like a hang. Setting `Client.RetryBackoff` to stop on rate limits
returns the error to the manager, which schedules the retry — the component
that owns time should own waiting.

## 2026-10-18 — a rate limiter that can say when it is full (SHD-001)

A token bucket answers yes or no; the caller that wants a `Retry-After`
header needs the third answer, "not yet, and here is when". `Bucket.RetryAfter`
computes the missing fraction of a token and converts it to time with one
`math.Ceil`, which makes the header honest: the tests assert both directions —
a request at the advertised wait is allowed, and a wait that is one nanosecond
short is not. The two dimensions that were missing from the stage have
different homes because the pipeline has a fixed order: the route host is
known before authentication, so the stage appends it to the IP key, while the
identity only exists after the OIDC gate, so the per-identity budget is
charged inside the proxy right after the policy allows the request — a
rate-limited request is never logged as allowed.

Capacity is where failing closed does not mean failing dumb. At `max_keys` the
limiter first reclaims buckets that are already refilled to full, because
dropping one of those gives nothing away, and only then refuses the new key:
evicting a bucket that still owes a restriction would let someone with many
keys reset a victim's budget. The reload lesson was the opposite of the usual
one: state that is *deliberately* kept (sessions, upstream transport) needed
the same treatment for buckets, because rebuilding a limiter is a silent
"everyone gets a fresh burst". Reusing the installed limiters while their
settings are unchanged takes six lines and one regression test that spends a
burst, reloads, and expects the 429 to still be there.

## 2026-10-18 — sessions that outlive the process (ARCH-004)

The port that made this small was already implicit: the OIDC gate needs three
operations (create, look up, delete) and the route manager owns the periodic
sweep, so `auth.SessionBackend` is four methods with `context.Context` first
— the same shape the in-memory store had, with the context added because the
SQLite half does I/O. The adapter keeps two details from leaking upward: the
raw session ID never reaches disk (only `SHA-256(id)` goes into the `id_hash`
column, so a leaked database file cannot be replayed as cookies) and the
capacity check is one transaction that first deletes expired rows, then
counts, then inserts. `_txlock=immediate` is what makes that safe under
concurrency: the transaction takes the write lock at `BEGIN`, so two logins
serialize and neither sees the same free slot.

Two smaller decisions worth naming. The startup path refuses to run migrations
(`openMigratedDB` errors on a pending schema) because applying DDL as a side
effect of starting a server makes rollbacks and backups unpredictable; the
operator runs `switchboard migrate` first. And a session TTL change rebuilds
the backend while every other reload reuses it — a lifetime change is a policy
change, and keeping old rows under a new policy would be worse than logging
everyone out.

## 2026-10-18 — a chat channel that cannot slow the gate (NTF-001)

Two constraints shaped this adapter. The first is that `events.Sink.Emit` runs
on the request path, so the alert bridge cannot call the network inline: it
enqueues per paired chat into a bounded channel and one worker delivers, with
a drop counter instead of backpressure. Losing an alert is a smaller failure
than slowing down the login it reports. The second is that the Bot API puts
the bot token in the request URL — the credential is unavoidable in the path —
so `errors.As` with `*url.Error` unwraps the transport error before it can be
formatted into a log line: `Post "https://api.telegram.org/bot<token>/..."`
would otherwise leak the token everywhere `err` goes. A test asserts the token
string never appears in an error.

The callback design came from a hard limit rather than taste: Telegram caps
`callback_data` at 64 bytes, so the signed payload cannot carry context. The
button carries an action, a nonce and an expiry — three short fields — and the
HMAC tag is truncated to 128 bits, which fits and is standard practice for
short-lived tokens. Everything else (who, what, why) stays in the pending
record the nonce addresses, which is also what makes single-use enforcement
possible later: consuming a nonce exactly once is the approver's job. Retry
classification stays boring on purpose: 429 and 5xx get at most two extra
attempts with capped backoff, honoring the server's `retry_after`; a 4xx never
retries, because repeating a refusal only burns the rate limit.


## 2026-10-18 — closing the worktree review defects (SHD-001, ARCH-004, EDGE-007)

A route budget must use the router's canonical hostname, otherwise spelling
variants buy extra bursts. The failing regression exercised both DNS root dots
and equivalent IPv6 forms, on normal and auth paths. Persistent sessions also
need the current policy at lookup: recreating a Go wrapper does not recreate
SQLite rows. Their effective expiry is the earlier of the issued expiry and
creation plus current TTL; candidate construction makes no database changes.

Cancellation is a request, not a join. The certificate worker now exposes a
completion channel; shutdown cancels it, waits through its bounded DNS cleanup,
and only then releases the single-writer lock. sync.Once makes the close
operation idempotent. DNS-01 uses the strict origin policy without the autocert
ALPN exception. Chain validation receives explicit roots in the pure core;
the shell loads system roots or an explicitly configured staging bundle, and
untrusted certificates embedded in a chain never establish trust by themselves.
Parent directories are synced before a certificate generation can activate.

The Go minimum stays 1.26; the recommended toolchain is pinned at 1.27.2 to
address the standard-library findings from the previous gate. DNS-01 and
notification settings now participate in the restart guard so reload cannot
advertise an adapter configuration the process did not construct.


Validation: the pre-fix host-key, SQLite TTL and malformed-chain regressions
failed as expected; affected-package race tests then passed. The full
`scripts/verify.sh` passed 7/7 on Go 1.27.2, including staticcheck and
govulncheck (no reachable vulnerabilities; one required-module advisory has
no reachable call). Real issuance, pairing/alert delivery and deployment
acceptance remain in the active tracker.


## 2026-10-18 — routing to a tunnel without a facade (TUN-002, partial)

The transport is an adapter, not a second proxy. The router now receives a
consumer-declared `ConnectorRoutes` port and builds an explicit transport per
configured connector before swapping snapshots. No import cycle or raw tunnel
state enters the proxy: an absent adapter fails construction, and the transport
opens yamux streams exclusively. Route reloads retain the edge and its sessions;
listener and identity configuration remains process-owned and requires restart.

A bounded round trip is not finished when response headers arrive. The shared
slot stays occupied until Body.Close, and sync.Once makes release idempotent.
Wrapping a body also changes its method set: HTTP 101 needs io.ReadWriteCloser,
so the wrapper must preserve Write. The bidirectional echo regression covers
that protocol seam and verifies the slot is returned after upgraded shutdown.

The normal pipeline resolves visitor IP from the trusted peer once, strips
spoofed assertions, and emits the internal claim only into the tunnel. The
connector still chooses its fixed origin. Protected tunnel routes fail closed
until visitor identity can be asserted and enforced; mTLS peer identity alone
never authorizes an application user. Connection events now share the console
broker, and attachment is displayed separately from application readiness.


Verification: affected-package race tests and the full seven-step gate passed
with Go 1.27.2, including staticcheck and govulncheck (zero reachable). The
separate-process configured-tunnel smoke passed restart/reconnect/revocation.
Browser checks observed the common tunnel event, online route state, no JS
errors and no overflow at the four activity breakpoints; axe had zero violations
and flagged existing decorative contrast for manual review. The ordinary VPS
pilot and public exposure were not modified.

## 2026-10-18 — feature commit boundaries

Shared composition/configuration files were staged as cumulative feature trees,
without replacing the live worktree. Each intermediate tree passed the race
suite in an isolated copy. A renewal test must wait for the certificate and its
event: activation can legitimately become visible before event emission finishes.
The language minimum stays independent from the patched toolchain selection.

Commit checkpoints: `802804a`, `a4d58c1`, `700d572`, `2698e62`, `5c681d3`;
integrated connector routing is committed with this entry. Deployment acceptance
remains in the active tracker.

## 2026-10-18 — private integrated deployment acceptance

A rollback restores executable/unit state, never an old registry over live
revocations. A real TCP-peer proof across the private tunnel establishes only
that trust boundary; Cloudflare visitor-IP acceptance is a separate test.
Disposable identities let revocation be tested without rotating Portfolio.

## 2026-10-19 — visitor IP in the origin log

Resolving the visitor address safely is not the same as being able to prove it:
`proxy.forward` now logs the address chosen by the trust stage as `client_ip`.
A log line must read what the pipeline decided from the request context, never
a header, or a spoofed value would reach the evidence. Table-driven tests cover
the trusted, untrusted, spoofed-header and duplicated-assertion cases.

## 2026-10-19 — wildcards and a pure name core

A wildcard changes four unrelated things at once (which names a certificate
covers, what an ACME challenge record is called, how a name is stored and how a
chain is verified). Putting those rules in one pure file with tests means the
shells never parse a `*` themselves. Wildcards cover exactly one label, never
the apex, and the CA's own answer (the authorized identifier) is checked rather
than trusted. `os.OpenInRoot` and `Resolve` showed the same lesson for files:
deny the control files in the pure resolver and keep the kernel check as a backstop.

## 2026-10-19 — extend the small seam, not the risky machine

Serving a second site from the same tunnel looked like it needed a second connector,
which meant generalising the certificate renewal that rotates live credentials. The
connector already built its proxy from a list of routes and the CLI only passed one.
Allowing several fixed routes was a 60-line, test-first change; the renewal code stayed
untouched. Before generalising a risky mechanism, look for the narrow place where the
need can be met without it.

## 2026-10-20 — explain the engine, never copy it

`policy explain` looked like it only needed `auth.Authorize`, but the proxy also
held a small rule of its own: a route with `auth_required` and no `access` gets
the empty policy. Copying that rule into the CLI would have made a second engine
that could drift. Moving it to `config.Route.Policy()` and calling it from both
sides keeps one source of truth; `TestExplainMatchesAuthorize` pins the
equivalence. The Gate/Authorize split matters too: a request without a session
never reaches `Authorize`, so the simulator must not fake one with an empty
Principal.

## 2026-10-20 — a framework that respects the CSP

The owner wanted a styled console. SvelteKit was the obvious pick, but its static
build boots with an inline script, and the console's CSP forbids inline scripts. Plain
Svelte + Vite emits only external files, so the policy stays strict. A useful check
after any front-end build: grep the bundle for inline scripts, `eval` and
`setAttribute("style")` before trusting the CSP. The admin API also showed that "read
only" is not the same as "harmless": an explain endpoint without auth would be an
oracle for the allowlist.

## 2026-10-20 — identity is a metaphor you keep

A stock component library gave a clean but anonymous console. The name already held
the identity: a telephone switchboard connects or refuses calls, which is what every
request decision is. Lamps, jacks, patch cords and engraved plates turned that into a
small visual vocabulary reused by the console and the project page. Two technical
notes: Tailwind 4 utilities beat `@layer components`, so a component class cannot
restyle a library component that already sets the same utility; and a grid item needs
`min-w-0` before an overflowing `<pre>` will scroll instead of widening the page.


## 2026-10-20 — branch names are part of CI configuration

Renaming the default branch on GitHub does not rewrite a workflow's explicit
push filter. The CI filter now uses `main`; the pull-request trigger remains
independent of that filter. A local gate verifies the checkout, while a hosted
run is required to verify that GitHub actually schedules the workflow.


## 2026-10-20 — archive extraction is a parser of hostile input (PAGES-002)

Three lessons. A normalisation step can turn a refused name into an accepted
one: trimming `./` from `.//` leaves `/`, and the fuzzer found it in seconds;
clean with `path.Clean` and judge the original. Defence in depth is cheap with
`os.Root`: even a wrong path check cannot write outside the directory it was
opened on. And sizes in headers are claims: count the bytes actually copied,
reading one past the budget to detect an overflow without writing it.


## 2026-10-20 — portability has a network boundary

ACME and DNS integrations can make a gateway portable, while a saturated
upstream link cannot be repaired by application filtering. A direct-ingress
trial and an exterior-proxy trial must record different protection and trust
assumptions. Replacing the proxy while keeping its DNS API does not prove
independence from the DNS provider. The initial offering is the access circuit;
global infrastructure and distributed function execution are separate scope.


## 2026-10-20 — a config file in a site is input from the site's author (PAGES-003)

`_headers` looks harmless until a site on a shared parent domain sets
`Set-Cookie: ...; Domain=.example.com` and plants a cookie on every other
host, including Switchboard's own session. Refuse headers by what they can
reach, not by how common they are. And substitution is where open redirects
hide: `/go/* /:splat` is safe until the visitor's path starts with `/`, so
check the result after substituting, not only the rule.


## 2026-10-20 — a reproducible build is checked, not configured (REL-005)

`-trimpath` and a commit-based `mod_timestamp` looked sufficient, yet two
snapshot builds of one commit differed. The cause was a default: GoReleaser's
ldflags inject `-X main.date`, and although no such variable exists the
linker flags still feed the build ID. Rebuilding twice and comparing hashes
is the only proof; a release tool's defaults are part of the input.


## 2026-10-20 — test the property, not the fields you remember (ARCH-005)

A deep-copy test that names three slices protects those three. Filling the
whole struct by reflection and asserting "no pointer or backing array is
shared" protects the next field too. `reflect.Value.Pointer` gives a slice's
backing array address, which is what aliasing means for slices; and a test
is only trusted after breaking the code it guards and watching it fail.
