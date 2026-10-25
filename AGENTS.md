# AGENTS.md

The single instruction file for any coding agent working on Switchboard.

## Required context

Before changing code, read completely:

1. `AGENTS.md` (this file).
2. `docs/architecture/design.md`: the design pattern every package follows.
3. `docs/TO_IMPLEMENT.md`: find the task's ID, state and done criteria.

Read `VISION.md` when a task touches direction or scope, and
`docs/architecture/stack.md` before adding or changing a dependency.

If these documents conflict with the source code, inspect the source and
report the difference before making a broad change.

## What this is

Switchboard is a year-long open-source project (October 2026 –
September 2027): a self-hostable **transparent zero-trust access platform**
in a single Go binary. Today it is a host-based reverse proxy with health
checks, ACME TLS, OIDC login against an external provider, per-route claim
authorization, atomic routing snapshots and a loopback control plane. The
plan grows it into tunnels, passwordless identity, a private CA,
infrastructure access, a WAF ("Shield"), Telegram approvals, an AI-agent
gateway and a transparency log. See `VISION.md`.

It is dogfooded on `octaviosantacruz.com`. It is not a commercial product.

## Boundary with Server Ops

Switchboard is the **trust plane** (identity, policy, access decisions, tunnels,
verifiable log). Server Ops is the **host plane** (processes, systemd units,
ports, databases, backups, service health). Do not add a service-health or
process-control dashboard here, and do not copy `server-ops/ports.json`.
`/_/status` is a route-debug view, not the product UI. Tracker: `CROSS-010` in
the workspace `TO_IMPLEMENT.md`.

## Commands

```bash
go run ./cmd/switchboard -config config.json   # run locally
go test -race ./...                            # all tests
go test -race ./internal/auth -run TestName    # one test
go test ./internal/config -fuzz FuzzName       # a fuzz target
scripts/verify.sh                              # full gate before reporting done
```

`config.json` is git-ignored; start from `config.example.json`.

## Code map (current)

- `cmd/switchboard/` — composition root: flags, listeners, signals, shutdown.
- `internal/config/` — parse, validate and normalize config; atomic persistence.
- `internal/auth/` — `Principal`, pure `Authorize`, OIDC `Gate`, session and
  pending-login stores.
- `internal/proxy/` — host-routed reverse proxy; strips/injects identity headers.
- `internal/healthcheck/` — background upstream probes.
- `internal/originauth/` — client-certificate policy for the public HTTPS listener (pure `Decide` + TLS/HTTP adapters).
- `internal/routemanager/` — builds immutable snapshots, atomic swap, reload,
  route mutations.
- `internal/admin/` — loopback control plane: status dashboard, bearer-token
  mutations, audit lines.

The target layout and the mapping from these packages is in
`docs/architecture/design.md`. Move a package only when a task already touches
it, never as a standalone big-bang refactor.

## Design rules (summary of `docs/architecture/design.md`)

- Functional core, imperative shell: security decisions are pure functions
  returning a `Decision` with a stable code and a reason.
- Ports and adapters: core packages never import `net/http`, SQL drivers or
  SDKs; interfaces are small and declared by the consumer.
- The request pipeline order (trust → ratelimit → shield → identify →
  authorize → route → forward) lives in one place; every stage emits an event.
- `cmd/switchboard` is the only composition root. No globals, no `init()` side
  effects.
- `context.Context` first for blocking work; every goroutine is cancellable.

## Security rules

- Never read, print, copy or commit secrets: `config.json`, `*.key`, `*.pem`,
  `.env*`, tokens, ACME account keys, CA keys.
- Client-controlled input never becomes trusted identity: strip identity
  headers (any `-`/`_` spelling), never forward the session cookie upstream.
- Authorization, session, certificate, tunnel and Shield changes always get a
  regression test that fails without the change.
- Never weaken a check to make a test pass.
- Secrets come from env or files; the control plane never writes resolved
  secrets back to disk.
- The admin listener stays loopback-only.

## Product and implementation ownership

The maintainer defines product direction, priorities, requirements and scope.
The agent owns technical design and complete implementation: core logic,
adapters, tests, integration, documentation and verification.

- Implement all parts of the approved task, including security decisions and
  pure core functions. Do not reserve code for the maintainer, leave exercises,
  or skip tests while waiting for the maintainer to implement a function.
- Choose routine technical details within the approved scope autonomously.
  Ask only when a missing product decision or external authorization is needed.
- Explain material tradeoffs, resulting behavior and verification clearly.
  Lessons and learning journals are not required deliverables.
- Commit and push still require explicit authorization.

## Working rules

- Run `git status --short` before editing and preserve unrelated work.
- Prefer the standard library and the existing architecture. A new dependency
  needs an entry in `docs/architecture/stack.md` first.
- Keep `go.mod`'s `go` directive at the supported minimum.
- Run `scripts/verify.sh` before reporting a change as complete.
- Track work only in `docs/TO_IMPLEMENT.md` (active) and `docs/IMPLEMENTED.md`
  (closed, with commit). Do not create parallel roadmaps.
- Report changed files, tests, tracker updates, blockers and whether the work
  is committed. Do not commit or push unless explicitly asked.

## First response for a new task

After reading the required files and inspecting the repository, summarize:

- the concrete requirement and its tracker ID;
- the owning package(s) and where it sits in the design pattern;
- relevant current behavior;
- the implementation boundaries and intended behavior;
- the verification you will run.

Then implement the approved task rather than stopping at a proposal.
