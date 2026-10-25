# Deploy product: projects, CI builds and a public deploy endpoint

**Status:** accepted 2026-10-25. The owner approved the direction (builds run
in GitHub Actions and Switchboard receives finished artifacts) and the
recommendations for decisions D1–D7 the same day. Project configuration and
read-only views are implemented as `PAGES-007` (`1e1bb3e`); the deploy endpoint
is in progress. Tracker: `PAGES-004`, `PAGES-005`,
`PAGES-008`–`PAGES-009` in [TO_IMPLEMENT](../TO_IMPLEMENT.md); workspace
`CROSS-011`.

## Problem

Publishing is the part of Switchboard the owner uses most, and today it
lives in three places that do not know about each other:

| Step | Where it runs | Owner |
|---|---|---|
| Build (`npm run build`) | Homelab, when the owner runs `publish-site.py` by hand | Server Ops script |
| Upload and activate | `switchboard site deploy` through an SSH tunnel to the edge's loopback admin listener | Switchboard Pages (`PAGES-002`) |
| Routing config | Rendered by `render-preview-config.py`, installed with `sudo`, SIGHUP or restart | Server Ops script |
| DNS record and origin pull | Cloudflare API, called from the homelab | Server Ops script |
| Public acceptance | Status, asset hash, hidden paths, HTTP→HTTPS | Server Ops script |
| Tunnelled APIs (`sigea-api`) | Map a host to a connector on the tunnel edge | Server Ops script |

Consequences:

- Nothing is published unless the owner runs a script on the homelab. A push
  changes nothing in production.
- The script hard-codes one zone, one IP, one SSH target and per-site presets,
  so no other operator could use it. It is infrastructure glue, not product.
- The edge (VPS) serves every site. The homelab is only the builder and the
  host of `sigea-api`. Turning the homelab off keeps static sites up but stops
  publishing; turning the VPS off takes every site down.

The workspace decision of 2026-10-23 already assigns edge deployment to
Switchboard (`CROSS-011`) and leaves Server Ops as a maintenance floor
(backups with tested restore). The `ports.json` registry and the Server Ops
panel get no further investment.

## Decisions taken (2026-10-25)

1. **Builds run in GitHub Actions.** They run whether or not the homelab is on,
   are triggered by a push, and never execute third-party `npm install` on the
   internet-facing edge. A private builder on the homelab stays possible: it is
   just another CI client of the same endpoint.
2. **Switchboard does not build.** It receives an artifact, verifies who sent
   it, stores it immutably, activates it atomically and can roll it back. That
   is `PAGES-002`, already in production since `PAGES-006`.
3. **`publish-site.py` is retired step by step.** Each phase below removes one
   of its steps; acceptance is always publishing Portfolio through the new path.

## Product model: the project

```
Project  (e.g. "portfolio", "sigea-web")
 ├─ kind         static site (now) | service behind a connector (later)
 ├─ site         the Pages site name its deployments go to
 ├─ hostnames    routes that serve the site, each with its access policy
 ├─ deployers    who may deploy: a GitHub repository + ref, or a scoped token
 ├─ deployments  immutable history, live pointer, rollback, previews
 └─ commit       every deployment records the source commit and workflow run
```

Projects are declared in config, like routes. The control plane may read
them and later mutate them through the admin API; the CLI and the console are
clients of that API (`CLI-001`).

Project deploy tokens are secret references (`env:NAME` or `file:/path`),
never inline values. Only the reference is kept in configuration; the opt-in
`PAGES-008` endpoint compares resolved tokens in constant time, and the admin
API never returns them. The read-only projects view
reports only that a token deployer is configured.

## Flow

```
git push to main
      │
      ▼
GitHub Actions (the project's own repository)
  checkout → build with GitHub vars/secrets → pack dist
  request a GitHub OIDC token (audience = this edge)
      │  HTTPS POST: archive + OIDC token
      ▼
Switchboard edge · public deploy endpoint
  verify token → match project trust (repository_id, ref, workflow)
  → existing Pages pipeline: CheckEntry, limits, manifest hash, store
  → activate (production) or keep as preview
  → event pages.deployed with project, commit, run URL
      │
      ▼
Acceptance (in the Action): status, asset hash, hidden paths → fail the job
and roll back if production does not serve the new build
```

## Deploy endpoint (`PAGES-008`)

The admin listener stays loopback-only (security rule in `AGENTS.md`). CI
reaches the edge through a separate, narrow public handler:

- **Where:** a dedicated hostname, `deploy.<zone>` (D2), routed to an
  internal handler, so exterior rules (Cloudflare WAF, rate limits) can target
  it and nothing else changes on application hosts. It accepts only
  `POST /v1/projects/{project}/deployments` and `GET` of one deployment's
  status.
- **Authentication, preferred: GitHub OIDC, no stored secret.** The workflow
  requests an ID token (`permissions: id-token: write`). Switchboard verifies
  it with the existing `go-oidc` dependency against
  `https://token.actions.githubusercontent.com`, requires the configured
  audience, a short lifetime, and matches claims against the project's trust:
  `repository_id` and `repository_owner_id` (immutable, unlike names), `ref`
  (production only from `refs/heads/main`), the pinned `workflow_ref` of the
  deploy workflow file, and an `event_name` of `push` or `workflow_dispatch`.
  The ref alone is not enough: a `pull_request_target` workflow runs with the
  base branch's ref while handling a fork's pull request. With a reusable
  workflow, `workflow_ref` names the caller in the project's repository. The
  token's `sha` becomes the deployment's recorded commit.
- **Fallback: a per-project deploy token** for other CI systems or the homelab,
  referenced by `env:` or `file:` and compared through fixed-size SHA-256
  digests in constant time. Resolved values are never persisted. Rotate the
  external secret to revoke it; the current console is read-only. It replaces
  the global `admin.sites_token` for CI use.
- **Abuse limits:** the existing archive limits (100 MiB compressed, 500 MiB
  extracted, 20 000 entries), a per-project rate limit, one deployment in
  flight per project, and a refusal before reading the body when the token is
  invalid.
- **Audit:** every request that names a configured project emits an event
  with project, repository, ref, commit and a stable refusal code; requests
  that never reach a project (wrong host, IP limit, method, path, unknown
  project) only get their code in the response, so scanners cannot flood the
  activity feed. With the `admin` audit family, the intent and then the
  outcome of each authenticated upload are durable. No token or raw error.
- **Key fetches:** GitHub's JWKS is fetched at most once a minute, also for
  tokens naming an unknown key, and refreshed hourly so retired keys stop
  verifying. A failed refresh keeps the keys already fetched from GitHub.
- **Pure core (maintainer piece):** `DecideDeployer(claims, trust) Decision`,
  table-tested like `Authorize`, never importing `net/http` or the OIDC SDK.

### Endpoint contract

Enable the internal hostname with
`"pages":{"dir":"/var/lib/switchboard/pages","deploy":{"host":"deploy.example.com","audience":"https://deploy.example.com"}}`.
It must not overlap an application route or login hostname. Public listeners
require TLS; plaintext is allowed only on loopback for local acceptance. Include
the deploy hostname in the TLS certificate configuration. Host/audience settings
require restart; project trust follows the current routing snapshot on reload.

Both POST and GET require `Authorization: Bearer ...`. GitHub tokens must have
the configured audience, valid signature/issuer/expiry, positive pinned numeric
IDs, matching ref and pinned workflow, a `push` or `workflow_dispatch` event,
`iat`/`exp` spanning at most ten minutes, and a nonempty `jti`. An upload consumes
its verified `jti` once in the SQLite replay store, across projects and process
restarts, until expiry; a retried upload needs a fresh token. A status GET
authenticates the same way but consumes neither the `jti` nor the deploy budget,
so the workflow can poll with the token that uploaded; reading never re-arms it
for writing. Capacity exhaustion fails closed. Per-project uploads are limited to
one request/second with a burst of five; only one upload per project may run. An independent IP budget protects verification. Authentication, replay,
limits and required durable intent all precede reading the archive.

GitHub provenance comes from verified claims. Opaque project tokens record the
project without accepting caller-supplied commit/run claims. Content IDs retain
the first nonempty provenance when the same artifact is uploaded again. Existing
archive and entry limits apply. Status reads are scoped to the named project;
neither endpoint exposes filesystem paths, credentials or raw errors. An upload
already authenticated uses its captured project trust until it finishes.

The durable contract records `admin.mutation_requested` / `project.deploy`
before upload execution when the `admin` audit family is enabled, using its
existing required/best_effort policy. After the store returns, the outcome is
appended too: `pages.deployed` / `deployment.created`, or `pages.deploy_refused`
with the refusal code. The outcome append cannot undo a committed deployment;
its failure is counted and logged by the audit adapter, and an intent without
an outcome means the process stopped or the append failed.

### Verification and operator handoff

Apply the new immutable migrations explicitly before starting this binary:
`switchboard migrate up -db /path/to/state.db -backup /path/to/new-backup.db`.
Migration 010 adds provenance columns; 011 adds persistent replay digests. The
configured deploy hostname has no port, and consumes requests for that name
even when the HTTP Host includes an explicit port. The normal admin listener
and its bearer retain their loopback boundary.

`GOTMPDIR=$HOME/.cache/go-tmp node scripts/pages-008-acceptance.mjs` builds and
starts an isolated loopback process with disposable state. It generates an
opaque test token in memory, deploys a tiny artifact through curl, verifies the
served bytes and reads project status. It cleans up its own process/files.
Set `SWITCHBOARD_BIN` to reuse a built binary. Go, Node, curl and tar are needed.

The manual workflow `.github/workflows/pages-oidc-acceptance.yml` runs the same
script with `--github` and job-scoped `id-token: write`. It requests real GitHub
tokens, uses the current repository/owner/ref/workflow as trust, tests another
repository/owner/ref and wrong audience, deploys through curl, checks commit/run
provenance and rejects replay before and after restarting Switchboard. Tokens
are kept in memory and curl stdin; they are not printed, stored in config, put
in argv or uploaded as artifacts. This workflow is prepared but has not been
published or executed; `PAGES-008` stays open until its real-issuer run succeeds.

Responses expose a stable `code`: `deployer.token_invalid` for SDK verification
failure (including signature, issuer, audience or expiry), more specific
`deployer.repository` / `owner` / `ref` / `workflow` / `event` for verified but
untrusted claims, and `deployer.replay` for a consumed token (401). Busy/provenance conflicts
return 409, IP/project budgets 429, compressed-size refusal 413, archive-rule
refusal 422 with the existing `entry.*` code, and replay/audit/storage availability
failures 503. Unknown project/deployment is 404; methods other than GET/POST are
405. The existing admin/sites bearer is not a project deploy credential.

References: [GitHub OIDC claims](https://docs.github.com/en/actions/reference/security/oidc),
[GitHub issuer metadata](https://token.actions.githubusercontent.com/.well-known/openid-configuration),
and [go-oidc verifier/key-set API](https://pkg.go.dev/github.com/coreos/go-oidc/v3/oidc).

## Variables and secrets (`PAGES-004`)

| Kind | Example | Where it lives | Rule |
|---|---|---|---|
| Public build variable | `VITE_API_BASE`, `PUBLIC_SITE_URL` | GitHub repository variables | Ends up in the downloaded JS. The docs and console say so. |
| Build secret | A CMS token used only while building | GitHub secrets | Must not reach `dist`: the endpoint scans the archive for known secret patterns and for `.env*` files and refuses it. |
| Application runtime value | `DATABASE_URL` of `sigea-api` | The host where the application runs | Out of scope: Switchboard does not run applications (D1). |

A console screen for build variables, Cloudflare-Pages style, is a later
option (D3): the Action would fetch public variables from Switchboard using
the same OIDC token. Secrets stay in GitHub either way, so Switchboard does
not become a secret store.

## Previews (`PAGES-005`)

Pull requests and non-`main` branches deploy without becoming live. Each
preview gets a hostname protected by an access policy by default.

Hostname constraint: Cloudflare's Universal SSL covers the apex and one
subdomain level only, so `<branch>.<site>.<zone>` would not get an edge
certificate behind the proxy. Use one level: `<site>-<branch>.<zone>`, with
the branch normalized to a DNS label (D6).

## Domains (`PAGES-009`)

Attach a hostname to a project from the API or console: create the route with
its policy, check that the certificate covers it, and, optionally, create the
DNS record and per-hostname origin pull through a provider adapter. Manual DNS
is the default mode; the Cloudflare adapter reuses the boundary described in
the [coexistence decision](cloudflare-coexistence.md) and needs its own
minimum-privilege token, distinct from the DNS-01 token. The Portfolio apex
cut-over logic in `publish-site.py` is not generalized: it was a one-time
migration.

## Retiring `publish-site.py`

| Script step | Replaced by | Phase |
|---|---|---|
| Build | GitHub Actions workflow template | `PAGES-004` |
| Upload through SSH tunnel | Public deploy endpoint with OIDC | `PAGES-008` |
| Render and install routing config | Project + route declared once; deploys no longer touch config | `PAGES-007` |
| Cloudflare DNS and origin pull | Domains API with optional adapter | `PAGES-009` |
| Public acceptance | Acceptance step in the workflow + automatic rollback | `PAGES-004` |
| Tunnelled APIs | Services as projects | Later (no ID yet) |

## Phases

| Phase | Tasks | Outcome |
|---|---|---|
| F1 | `PAGES-007` projects and per-project trust in config; `PAGES-008` public deploy endpoint | A `curl` with a GitHub OIDC token deploys Portfolio; the admin listener stays loopback |
| F2 | `PAGES-004` workflow template, variables, `dist` scan, acceptance and rollback | A push to Portfolio's `main` publishes it with the homelab off |
| F3 | `PAGES-005` previews | A pull request gets a protected preview URL |
| F4 | `PAGES-009` domains | A new site goes live without editing config by hand |
| Later | Services as projects | `sigea-api`-style apps published from the console; their runtime stays on their host |

## Decisions (taken 2026-10-25)

The owner accepted the recommendation for each question.

| ID | Question | Decision |
|---|---|---|
| D1 | Does Switchboard run applications (Workers/Functions-like)? | No. It publishes apps that run elsewhere, matching the [positioning](positioning-and-portability.md). Their runtime values stay on their host. |
| D2 | Deploy endpoint: dedicated hostname or a path on every host? | Dedicated `deploy.<zone>`. |
| D3 | Where are build variables edited: GitHub or the Switchboard console? | GitHub repository variables and secrets. A console view for public variables may come later; secrets stay in GitHub. |
| D4 | DNS automation in the product? | Manual by default; an optional provider adapter (Cloudflare first). |
| D5 | One operator or several users owning projects? | One operator; per-project deployers. |
| D6 | Preview hostname scheme | `<site>-<branch>.<zone>` (one level, Universal SSL). |
| D7 | Should a production deploy require approval (Telegram, console)? | Not initially; revisit with `NTF` approvals. |

## Core pieces for the maintainer

Per the [mentor skill](../../.agents/skills/switchboard-mentor/SKILL.md):

- `DecideDeployer`: OIDC claims + project trust → allow/deny with a stable code.
- The deployment state machine: `received → stored → preview | live →
  superseded | rolled_back`, with the legal transitions as a pure function.
- The `dist` secret scan rules: what counts as a leaked secret, tested with
  real-looking fixtures.

## Non-goals

- Building on the edge or running build commands inside Switchboard.
- A runtime for user code or storage of application runtime secrets.
- Global CDN distribution; Cloudflare stays the optional exterior proxy.
- Exposing the admin listener or the admin API to the internet.
