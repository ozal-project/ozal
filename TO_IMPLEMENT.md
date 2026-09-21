# TO_IMPLEMENT.md — Switchboard roadmap

## Positioning

Not "a homemade Caddy plus a homemade Okta" — that's a feature-count fight
against two mature projects (Caddy, Pomerium) we can't win. The actual
pitch, per external review (2026-09-21) and adopted here:

> Switchboard is a local-first, identity-aware application gateway for
> developers and small self-hosted deployments. One binary, one config,
> from localhost to production.

Differentiator: excellent local dev experience, the *same* config from
localhost to a real VPS, OIDC without standing up five services, explainable
and observable configuration, a lightweight operational dashboard, secure
defaults.

**A from-scratch OIDC identity provider is explicitly out of scope for
Switchboard.** It's the highest-risk, most product-diluting thing we could
build here (credential recovery, MFA, key rotation, revocation, consent,
protocol compatibility) and doesn't serve this positioning. It's real
curiosity-driven learning territory, so it becomes **its own separate
project** later, not a Switchboard phase — see the end of this file.

## Status snapshot (2026-09-21)

Routing, health checks, graceful shutdown, TLS/ACME, OIDC login, and
per-route authorization by claims are implemented, including a full
login/PKCE/JWT-verification round trip against a fake in-process provider.
Solid prototype, **not yet production-ready**:
see Phase 1 below for exactly what's blocking that claim.

External review credited as especially solid:
[`internal/auth/oidc.go`](internal/auth/oidc.go),
[`internal/auth/session.go`](internal/auth/session.go), and the integration
test in [`internal/auth/oidc_integration_test.go`](internal/auth/oidc_integration_test.go).

---

## Phase 1 — Core hardening (do this first, before anything else)

Converts the prototype into a trustworthy base. Nothing else on this
roadmap should be built on top of the current state until this phase is
done.

- [x] **Authorization by claims, not just authentication.** Per-route access
      policies support:
      ```yaml
      access:
        allowed_emails: ["octavio@example.com"]
        allowed_domains: ["example.com"]
        required_groups: ["engineering"]
      ```
      The proxy enforces the policy after authentication. `groups` depends
      on the provider actually issuing a groups claim (works with
      Okta/Google Workspace when scoped correctly); a missing claim fails
      closed when groups are required.
- [x] **HTTP server timeouts.** `ReadHeaderTimeout` + `IdleTimeout` set on
      every server we construct (plain, TLS, ACME challenge). `ReadTimeout`/
      `WriteTimeout` deliberately left unset — a route may need to stream a
      long-lived or large response (WebSocket/SSE support is planned).
- [x] **Stricter, normalized route validation.**
      - Reject duplicate `host` entries instead of silently letting the
        last one win.
      - Restrict `target` to `http`/`https` schemes only.
      - Normalize hosts (case, trailing dot) before using them as map keys,
        so `Magiclink.Localhost` and `magiclink.localhost` don't silently
        diverge.
      - Validate `cookie_domain` actually covers every protected
        route's host (a real suffix check, not just "is it set").
- [x] **Session store cleanup + limits.** Periodic sweep (same ticker
      pattern as `healthcheck.Checker`) plus a 10k capacity ceiling
      (`ErrStoreFull` -> 503).
- [x] **`pendingStore` cleanup + limits.** In-flight logins have an
      independent periodic sweep and a 10k capacity ceiling, matching the
      bounded-session-store approach.
- [x] **Secure identity propagation upstream.** Protected routes receive
      `X-Auth-Request-Email` and `X-Auth-Request-User` (OIDC subject)
      from the verified session. Client-supplied values are stripped on
      every route before any trusted values are added.
- [ ] **Secrets from env/file, not only inline JSON.** `client_secret`
      currently has to live in the JSON config in plaintext. Support
      `client_secret_file` (read the value from a file) and/or environment
      variable expansion, so the secret doesn't have to sit in a
      version-controllable file.
- [ ] **Test hardening**: the existing suite is clean with `-race`; add
      fuzz tests for config parsing / host validation / the `return_to`
      open-redirect guard, plus more negative-path tests generally.
- [x] **Fix the README inconsistency:** the status and roadmap now describe
      OIDC login and claim authorization consistently.

## Phase 2 — Transactional runtime (prerequisite for reload/API/dashboard)

Do **not** build hot-reload, the runtime API, or the dashboard directly on
top of today's plain maps — three things would then mutate routes, health
checks, and the proxy handler independently, with no consistency
guarantee. Instead:

- [ ] A `RouteManager` (or similarly named) that: parses config, validates
      it, builds the proxies and health checkers for it, and produces one
      immutable **snapshot**.
- [ ] Swapping to a new snapshot is atomic (e.g. `atomic.Pointer`) — a
      request either sees the old snapshot in full or the new one in full,
      never a half-updated mix.
- [ ] A snapshot that fails to build (bad config) never replaces the
      running one — log the error, keep serving the last-good snapshot.

This is the foundation Phase 3's reload/API/dashboard are built on.

## Phase 3 — Separate control plane

- [ ] Admin listener bound to `127.0.0.1` by default (not the public
      `:80`/`:443`) — config reload trigger, the route-management API, and
      the dashboard all live here.
- [ ] `GET /_/status` dashboard (per earlier decision: `html/template` +
      HTMX, no JS framework) moves to this listener — **not public**, since
      route topology/targets can be sensitive information on their own.
- [ ] `POST /routes`, `DELETE /routes/{host}` on this same listener,
      authenticated with a bearer token from config.
- [ ] Changes made at runtime persist back to disk (config file or a
      separate state file — decide which before building) so a restart
      doesn't silently lose them.
- [ ] Audit log of control-plane changes: who/what changed, when.
- [ ] Per-route operational detail beyond up/down: last check time,
      latency, last error, time of last state transition.
- [ ] Config hot-reload (`SIGHUP` or file watch via `fsnotify`) rebuilds a
      snapshot through the same `RouteManager` path as the API — one
      mechanism, not two.

## Phase 4 — Serious proxy behavior

- [ ] Multiple upstreams per route, round-robin load balancing.
- [ ] Active *and* passive health checks (today is active-only).
- [ ] Retries — idempotent/safe methods only, never blindly retry a POST.
- [ ] Circuit breaker per upstream.
- [ ] Per-route timeouts (today's server timeouts are global).
- [ ] WebSocket and Server-Sent Events proxying, explicitly tested (
      `httputil.ReverseProxy` handles upgrade requests, but this needs a
      real test, not an assumption).
- [ ] Request IDs (generate if absent, propagate if present).
- [ ] Trusted-proxy policy for `X-Forwarded-For`/`X-Forwarded-Proto` —
      don't blindly trust client-supplied values when Switchboard itself
      is behind another proxy/load balancer.

## Phase 5 — Observability

- [ ] Structured JSON logs (replace the current plain `log.Printf` calls).
- [ ] Prometheus metrics: requests, latency, errors, upstream selected,
      auth allow/deny decisions — per route.
- [ ] OpenTelemetry tracing.
- [ ] `/healthz` (process alive) and `/readyz` (ready to serve) distinct
      from the per-upstream health checks.

## Phase 6 — Open-source experience

- [x] `LICENSE` file (MIT).
- [ ] `switchboard validate` (check a config file without running),
      `switchboard doctor` (environment/config sanity check),
      `switchboard routes` (print the resolved routing table).
- [ ] `CONTRIBUTING.md`, `SECURITY.md`, `CHANGELOG.md`.
- [ ] Dockerfile, a documented systemd unit, GoReleaser config.
- [ ] Signed binaries, checksums, an SBOM.
- [ ] CI (GitHub Actions) across Linux/macOS/Windows.
- [ ] A working, copy-pasteable end-to-end example in the docs.
- [ ] GitHub Releases with real binaries — **before** npm distribution
      (Phase 7), since the npm wrapper downloads from these releases.

## Phase 7 — Distribution via npm

- [ ] [goreleaser](https://goreleaser.com) cross-compiles for
      Linux/macOS/Windows × amd64/arm64, publishes to GitHub Releases on
      each tag (this is Phase 6's GoReleaser item, feeding this phase).
- [ ] A small npm package with a `postinstall` script that detects the
      user's OS/arch and downloads the matching release binary — the same
      pattern `esbuild`, `swc`, `turbo`, and `@playwright/cli` use.
- [ ] Result: `npx switchboard` / `npm install -g switchboard` works with
      no Go toolchain required, while the binary itself stays 100% Go.

---

## Explicitly deferred to a separate project: homemade OIDC provider

Switchboard's own identity provider (own user store, credential handling,
token issuance, signing-key rotation) is real, valuable, curiosity-driven
learning — just not as part of Switchboard's story. When picked up, it
becomes its own repo, evaluated with the same "moonlighter" discipline as
the Sigea projects: built for real, but never trusted with anything that
matters until it's proven out on its own. Reference points already
identified: [Dex](https://github.com/dexidp/dex) (closest in spirit — Go,
lightweight), [Ory Hydra](https://github.com/ory/hydra) (pure OAuth2/OIDC
server, no user store), [Zitadel](https://github.com/zitadel/zitadel) (full
IAM, useful as a feature reference even if far bigger in scope). Once it
exists, Switchboard could optionally support it as *one more* OIDC issuer
choice alongside Google/Okta/etc. — not a special case.
