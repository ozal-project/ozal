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

## SMART goals (draft)

These are product goals, not a promise to reproduce a larger proxy or identity
platform. Dates are planning targets and should be reviewed at each milestone.
Success is a reproducible release with a clear user outcome, not stars,
downloads, or feature count.

### Goal 1: publish a trustworthy MVP alpha by 2026-10-19

**User outcome:** a developer can route local or small self-hosted services by
hostname, protect selected routes with an existing OIDC provider, and operate
the gateway without committing credentials to a repository.

**Release gates:**

- [ ] `auth.client_secret` and `admin.token` support environment or file
      references; inline values remain compatible but are documented as local
      development only.
- [ ] Fuzz targets cover config parsing, host validation, and `return_to`; each
      has deterministic regression seeds for previously discovered failures.
- [ ] CI runs `go test -race ./...`, `go vet ./...`, and a build on Linux,
      macOS, and Windows for every pull request.
- [ ] The README contains one copy-pasteable quickstart that starts a routed
      local app and reaches the administrative status page.
- [ ] A `v0.1.0-alpha.1` tag is cut only after every gate above is green.

### Goal 2: make the first release usable by contributors by 2026-11-16

**User outcome:** a new contributor can validate a configuration, understand
how to report a vulnerability, and run the project locally without reading the
source first.

**Release gates:**

- [ ] `switchboard validate` checks a config without serving traffic, and
      `switchboard routes` prints the resolved routing table.
- [ ] `CONTRIBUTING.md`, `SECURITY.md`, and `CHANGELOG.md` explain the normal
      contribution, disclosure, and release paths.
- [ ] The repository includes a Dockerfile and a documented systemd deployment
      example, both exercised by documentation checks or CI where practical.

### Goal 3: deliver a small reliability upgrade by 2026-12-14

**User outcome:** a route can use more than one upstream and continue serving
when one upstream fails, without retrying unsafe requests.

**Release gates:**

- [ ] Routes support multiple upstreams and deterministic round-robin
      selection.
- [ ] Active and passive health state prevent failed upstreams from receiving
      new traffic.
- [ ] Retries apply only to configured idempotent methods and have integration
      tests proving that `POST` is never retried automatically.

Everything else in Phases 4-7 remains backlog until these goals are met or
explicitly re-scoped.

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

- [x] A `RouteManager` (or similarly named) that: parses config, validates
      it, builds the proxies and health checkers for it, and produces one
      immutable **snapshot**.
- [x] Swapping to a new snapshot is atomic (e.g. `atomic.Pointer`) — a
      request either sees the old snapshot in full or the new one in full,
      never a half-updated mix.
- [x] A snapshot that fails to build (bad config) never replaces the
      running one — log the error, keep serving the last-good snapshot.

This is the foundation Phase 3's reload/API/dashboard are built on.

Implemented in `internal/routemanager`: listener and TLS changes are rejected
during reload because their sockets/certificate manager are process-level;
those settings require restart.

## Phase 3 — Separate control plane

- [x] Admin listener bound to `127.0.0.1` by default (not the public
      `:80`/`:443`) — config reload trigger, the route-management API, and
      the dashboard all live here.
- [x] `GET /_/status` dashboard uses `html/template` on this listener
      with a lightweight full-page refresh (no JS dependency; HTMX remains the
      choice when interactive dashboard controls are added) — **not public**, since
      route topology/targets can be sensitive information on their own.
- [x] `POST /routes`, `DELETE /routes/{host}` on this same listener,
      authenticated with a bearer token from config.
- [x] Changes made at runtime persist atomically back to the active config file so a restart
      doesn't silently lose them.
- [x] Audit log of control-plane changes: who/what changed, when.
- [x] Per-route operational detail beyond up/down: last check time,
      latency, last error, time of last state transition.
- [x] Config hot-reload (`SIGHUP` or file watch via `fsnotify`) rebuilds a
      snapshot through the same `RouteManager` path as the API — one
      mechanism, not two.

Implemented with same-file atomic persistence, loopback-only admin binding,
bearer authentication for every mutation, and audit entries in the process log.
`admin.listen`, the public listener, and TLS settings remain restart-only.

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
