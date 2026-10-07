# AGENTS.md

The single instruction file for any coding agent working on Switchboard.

## Required context

Before changing code, read completely:

1. `AGENTS.md` (this file).
2. `.agents/skills/switchboard-mentor/SKILL.md`: the working mode. The
   maintainer is learning Go and the protocols involved; every task follows
   that loop.
3. `docs/architecture/design.md`: the design pattern every package follows.
4. `docs/TO_IMPLEMENT.md`: find the task's ID, state and done criteria.

Read `VISION.md` when a task touches direction or scope, and
`docs/architecture/stack.md` before adding or changing a dependency.

If these documents conflict with the source code, inspect the source and
report the difference before making a broad change.

## What this is

Switchboard is a year-long open-source learning project (October 2026 –
September 2027): a self-hostable **transparent zero-trust access platform**
in a single Go binary. Today it is a host-based reverse proxy with health
checks, ACME TLS, OIDC login against an external provider, per-route claim
authorization, atomic routing snapshots and a loopback control plane. The
plan grows it into tunnels, passwordless identity, a private CA,
infrastructure access, a WAF ("Shield"), Telegram approvals, an AI-agent
gateway and a transparency log. See `VISION.md`.

It is dogfooded on `octaviosantacruz.com`. It is not a commercial product.

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

## Working rules

- Run `git status --short` before editing and preserve unrelated work.
- Prefer the standard library and the existing architecture. A new dependency
  needs an entry in `docs/architecture/stack.md` first.
- Keep `go.mod`'s `go` directive at the supported minimum.
- Run `scripts/verify.sh` before reporting a change as complete.
- Track work only in `docs/TO_IMPLEMENT.md` (active) and `docs/IMPLEMENTED.md`
  (closed, with commit). Do not create parallel roadmaps.
- Add a line to `docs/learning/LEARNING_LOG.md` for each closed task.
- Report changed files, tests, tracker updates, blockers and whether the work
  is committed. Do not commit or push unless explicitly asked.

## First response for a new task

After reading the required files and inspecting the repository, summarize:

- the concrete requirement and its tracker ID;
- the owning package(s) and where it sits in the design pattern;
- relevant current behavior;
- the core piece the maintainer could write (per the mentor skill);
- the verification you will run.

Then implement the approved task rather than stopping at a proposal.
