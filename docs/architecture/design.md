# Design pattern

Switchboard follows one architecture: **functional core, imperative shell**,
organized as **ports and adapters** (hexagonal), with the request path built
as a **pipeline of stages**. This file explains the pattern, why it fits, and
the concrete rules every package follows.

## Why this pattern

An access platform makes security decisions. Those decisions must be easy to
test exhaustively, easy to explain ("why was Ana denied?") and impossible to
skip by accident. That argues for:

- **Decisions as pure functions.** Given inputs, return a `Decision` with a
  stable code and a reason. No network, no disk, no clock unless passed in.
  These are tested with tables and fuzzing.
- **Side effects at the edges.** HTTP, tunnels, SQLite, ACME, Telegram,
  LLMs: thin adapters that gather inputs for the core and carry out its
  decisions.
- **One fixed order of checks.** The pipeline makes the order of trust
  checks visible in one place instead of scattered across handlers.

`auth.Authorize` (pure) called from `proxy` (shell) is the first instance of
this pattern in the codebase; everything new follows it.

## The layers

```
            ┌────────────────────────── shell (adapters) ──────────────────────────┐
 inbound →  │ http listener · tunnel server · admin API · Telegram callbacks       │
            │            │ builds inputs                    ▲ executes decision    │
            │            ▼                                  │                      │
            │   ┌──────────────── core (pure) ─────────────────────────┐           │
            │   │ policy · rules · risk · identity types · decisions   │           │
            │   └──────────────────────────────────────────────────────┘           │
            │            │ needs data / effects via ports (interfaces)             │
            │            ▼                                                         │
 outbound → │ sqlite store · OIDC/ACME clients · CA signer · notifier · tlog · LLM │
            └──────────────────────────────────────────────────────────────────────┘
```

- **Core** packages import only the standard library and other core packages.
  They never import `net/http`, `database/sql` or an SDK.
- **Ports** are small Go interfaces declared *by the package that uses them*
  (the consumer), usually 1–3 methods: `SessionStore`, `Notifier`,
  `CertSigner`, `EventSink`, `Upstream`.
- **Adapters** implement ports for one technology each: `sqlite`,
  `telegram`, `oidc`, `acme`, `yamux`.
- **`cmd/switchboard`** is the **composition root**: the only place that
  constructs adapters and wires them into the core. No globals, no `init()`
  with side effects.

## The request pipeline

Every inbound request or stream passes the same ordered stages. Each stage is
an `http.Handler` middleware (`func(next http.Handler) http.Handler`) or, for
raw streams, the equivalent stream stage. Order is part of the security model
and lives in one function.

| # | Stage | Responsibility | Core decision |
|---|---|---|---|
| 1 | `trust` | Request ID, strip spoofable headers (identity, `X-Forwarded-*`, session cookie toward upstream), normalize host | — |
| 2 | `ratelimit` | Token buckets per IP / identity / route | `ratelimit.Allow` |
| 3 | `shield` | WAF rules, bot signals | `rules.Evaluate` |
| 4 | `identify` | Resolve the Principal (session, passkey, mTLS, agent token, signed agent) | — (adapters return a Principal) |
| 5 | `authorize` | Policy + risk signals → allow / deny / step-up / require approval | `policy.Authorize` |
| 6 | `route` | Pick the upstream (HTTP target or tunnel), health-aware | `routing.Select` |
| 7 | `forward` | Proxy bytes, inject verified identity headers, record session | — |

Every stage emits an event with its decision; nothing decides silently.

## Supporting patterns

- **Immutable snapshot + atomic swap** (already in `routemanager`): config is
  validated and fully built before one `atomic.Pointer` swap. A request sees
  one complete generation.
- **Strategy** for interchangeable mechanisms behind one port:
  authenticators (OIDC, passkey, email code, mTLS, agent token), notifiers
  (Telegram, ntfy, webhook), upstreams (HTTP target, tunnel stream).
- **Observer / event sink:** components publish structured events to an
  `EventSink`; logs, metrics, the dashboard, risk scoring, notifications and
  the transparency log subscribe. Publishers never know who listens.
- **Decision value objects:** `Decision{Allowed, Code, Reason, Trace}`.
  `Code` is stable for tests and metrics; `Reason` is for humans; `Trace` lists
  the rules evaluated, which powers explanations.
- **Raw vs resolved config:** the file holds intent (with secret references);
  the resolved config holds secrets in memory only and is never written back.

## Package layout (target)

New code goes straight into this layout; existing packages move when they are
next touched, never in a big-bang refactor.

```
cmd/switchboard/          composition root, subcommands (edge, connect, share, validate)
internal/
  identity/               Principal, session types                      (core)
  policy/                 AccessPolicy, Authorize, Decision             (core)
  rules/                  rules language: lexer, parser, evaluator      (core, Q2)
  ratelimit/              token-bucket logic                            (core)
  routing/                routing table, snapshot, upstream selection   (core + snapshot)
  config/                 parse, validate, raw/resolved                 (core)
  pipeline/               stage order and middleware glue               (shell)
  httpedge/               listeners, TLS, reverse proxy                 (adapter)
  admin/                  control plane API and dashboard               (adapter)
  oidc/                   upstream OIDC login                           (adapter)
  tunnel/                 agent ↔ edge transport                        (adapter, Q1)
  store/sqlite/           persistent state                              (adapter, Q1)
  notify/telegram/        notifications and approvals                   (adapter, Q1)
  events/                 event types and sinks                         (core types + adapters)
```

Today's mapping: `auth/authz.go` → `policy`, `auth/principal.go` →
`identity`, `auth/oidc.go` → `oidc`, `proxy` → `httpedge` + `pipeline`, and
`routemanager` → immutable routing snapshot. `routing.Select` is the pure,
health-aware upstream-index decision; the proxy advances a per-snapshot
round-robin cursor across it.

## Proposed extensions (not current behavior)

These design records keep future work aligned with the pattern; none is
implemented or a release promise:

- [Durable audit contract](audit-contract.md): a separate append-only audit
  writer and signed checkpoints, not `events.Broker`.
- [Policy explain](policy-explain.md): a diagnostic CLI over the pure
  `auth.Authorize`, with no second policy engine.
- [Shield challenge](shield-challenge.md): a proof-of-work stage in the
  `shield` position of the pipeline, with a signed clearance cookie.
- [Identity integration](identity-integration.md): how an app behind the edge
  trusts (or does not trust) the gateway's identity.

`LAB-*` learning labs are tracked in [TO_IMPLEMENT](../TO_IMPLEMENT.md); they
are experiments and artifacts, not commitments.

## Go rules that make the pattern real

1. **`context.Context` is the first parameter** of anything that blocks or does
   I/O; every goroutine has an owner that can cancel it.
2. **Accept interfaces, return structs.** Interfaces live with the consumer.
3. **Errors are values:** wrap with `fmt.Errorf("...: %w", err)`; use sentinel
   errors (`ErrRouteExists`) only where callers branch on them.
4. **Inject time and randomness** into core code (`now func() time.Time`,
   `io.Reader` for randomness) so tests are deterministic.
5. **No package named `utils`, `common`, `models` or `helpers`.** Packages are
   named for what they provide.
6. **Tests at the seams:** core gets table tests and fuzz targets; adapters
   get `httptest`/integration tests; the pipeline gets end-to-end tests that
   prove the stage order (e.g. a spoofed identity header never reaches the
   upstream).
