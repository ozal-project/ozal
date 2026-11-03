# AGENTS.md

The single instruction file for any coding agent working on Ozal.

## Required context

Before changing code, read completely:

1. `AGENTS.md` (this file).
2. `docs/architecture/design.md`: the design pattern every package follows.
3. The task's ID, state and done criteria come from the maintainer. The public
   `docs/TO_IMPLEMENT.md` only points to where the status lives; ask for it if a
   task needs one.

Read `VISION.md` when a task touches direction or scope, and
`docs/architecture/stack.md` before adding or changing a dependency.

If these documents conflict with the source code, inspect the source and
report the difference before making a broad change.

## What this is

Ozal is an open-source, self-hostable access gateway in a single Go binary.
Today it is a host-based reverse proxy with health checks, ACME TLS, native
passkey and OIDC login, per-route policy, atomic routing snapshots, static
sites with deployment history, an embedded console on loopback or a local
socket, and signed update plumbing. Tunnels are experimental. Direction is in
`VISION.md`; what a release includes is in its release notes and the README
status table. The maintainer's detailed plan is private.

It is dogfooded on `ozal.dev` and `octaviosantacruz.com`. It is not a
commercial product.

## Boundary with Server Ops

Ozal is the **trust plane** (identity, policy, access decisions, tunnels,
verifiable log). Server Ops is the **host plane** (processes, systemd units,
ports, databases, backups, service health). Do not add a service-health or
process-control dashboard here, and do not copy `server-ops/ports.json`.
`/_/status` is a route-debug view, not the product UI. Cross-project coordination
is tracked by the maintainer.

## Commands

```bash
go run ./cmd/ozal -config config.json   # run locally
go test -race ./...                            # all tests
go test -race ./internal/auth -run TestName    # one test
go test ./internal/config -fuzz FuzzName       # a fuzz target
scripts/verify.sh                              # full gate before reporting done
```

`config.json` is git-ignored; start from `config.example.json`.

## Code map (current)

- `cmd/ozal/` — composition root: flags, listeners, signals, shutdown.
- `cmd/ozalctl/` — SSH operator CLI (targets, upgrades, publication).
- `cmd/policywasm/` — the access decision compiled to WebAssembly for the site's live demo.
- `internal/config/` — parse, validate and normalize config; atomic persistence.
- `internal/auth/` — `Principal`, pure `Authorize`, OIDC `Gate`, sessions.
- `internal/passkey/` — WebAuthn ceremonies, enrolment, devices and login pages.
- `internal/proxy/` — host-routed reverse proxy; strips/injects identity headers.
- `internal/pipeline/` — inbound stage order; `trust` is a middleware today.
- `internal/routing/` — pure, health-aware upstream selection.
- `internal/healthcheck/` — background upstream probes.
- `internal/originauth/` — client-certificate policy for the public HTTPS listener (pure `Decide` + TLS/HTTP adapters).
- `internal/routemanager/` — immutable snapshots, atomic swap, reload, route mutations.
- `internal/certs/`, `internal/acmedns/`, `internal/cloudflare/`, `internal/zonesetup/`, `internal/domaincheck/` — ACME, DNS providers and domain state.
- `internal/pages/` — static sites, uploads, GitHub workflow generation and deploy auth.
- `internal/tunnel/`, `internal/tunnelrenew/` — connector transport and renewal.
- `internal/admin/` — loopback/socket control plane, embedded console and login host.
- `internal/audit/`, `internal/events/`, `internal/notify/` — durable journal, event stream and alerts.
- `internal/accesshistory/` — who reached which protected application and why, from the journal when audit is on.
- `internal/store/sqlite/` — sessions, identities, connectors and deployments.
- `internal/release/`, `internal/updateexec/`, `internal/updatejob/`, `internal/updatelock/`, `internal/upgrade/`, `internal/pairhelper/` — signed releases and updates; `pairhelper` is the privileged half of a coordinated edge/connector update.
- `internal/ctl/`, `internal/githubapp/` — operator and GitHub App adapters.
- Smaller adapters: `internal/ratelimit`, `internal/static`, `internal/policycontext`, `internal/policydemo`, `internal/install`, `internal/diskspace`, `internal/adminid`, `internal/useragent`.

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
- `cmd/ozal` is the only composition root. No globals, no `init()` side
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
- Active work and closed evidence are tracked by the maintainer, not in this
  repository. Do not create parallel roadmaps or task lists in the repo; the
  public trackers only point to where the status lives.
- Keep customer docs to the guides linked from `docs/README.md`; configuration
  and CLI details belong in `docs/reference.md`, implementation contracts and
  verification in `docs/engineering.md`. Update the existing section rather
  than creating a dated review, plan or delivery report for each task.
- Public and private documentation (maintainer policy). The public
  repository holds the product, its guides, its contracts and its release
  notes. It must not hold:
  - owner decisions, dated acceptance records, VM or host evidence, or work
    status (`docs/TO_IMPLEMENT.md` and `docs/IMPLEMENTED.md` are public only as
    short notes that point to the maintainer);
  - IP addresses, host names, home-lab paths, unit names or private domains
    used only for the maintainer's deployment;
  - tracker codes (`REL-*`, `UI-*`, `ID-*`, `EDGE-*`, `SEC-*`, and similar) in
    user-facing text; a contract may name a behavior, not its ticket.
  Put such material in `docs/local/` (ignored). When a public sentence depends on
  private evidence, state the limit it describes, not the evidence.
- Do not put a plan, a schedule or a quarter in public text. `VISION.md` states
  direction only; the detailed plan is private.
- Every public guide under `docs/` must be reachable from
  [START-HERE](docs/START-HERE.md) by following links. When you add or move a
  public guide, link it from there.
- Check links before reporting a documentation change complete: no link may
  point to a missing file or a missing anchor, and a link may not point into
  `docs/local/`.
- Write everything in the repository in English (maintainer policy):
  docs, trackers, site, product copy, code comments and commit messages, with
  a product mindset for public readers. Quote an owner's words in their
  original language only as a cited decision. When a heading changes, update
  every link to its anchor.
- Keep temporary drafts, captures and historical review copies in ignored
  `docs/local/` or `.local/docs-archive/`. They must never be build dependencies.
  Preserve security contracts and canonical reference material in Git.
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
