# Deploy product: projects, CI builds and a public deploy endpoint

**Status:** accepted 2026-10-25. The owner approved the direction (builds run
in GitHub Actions and Switchboard receives finished artifacts) and the
recommendations for decisions D1–D7 the same day. Not implemented yet. Tracker: `PAGES-004`, `PAGES-005`,
`PAGES-007`–`PAGES-009` in [TO_IMPLEMENT](../TO_IMPLEMENT.md); workspace
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
never inline values. Only the reference is kept in configuration; resolved
tokens will be compared in constant time when `PAGES-008` adds the deploy
endpoint, and are not returned by the admin API. The read-only projects view
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
  (production only from `refs/heads/main`), optionally `workflow_ref`. The
  token's `sha` becomes the deployment's recorded commit.
- **Fallback: a per-project deploy token** for other CI systems or the homelab,
  stored as a hash, scoped to one project, revocable from the console. It
  replaces the global `admin.sites_token` for CI use.
- **Abuse limits:** the existing archive limits (100 MiB compressed, 500 MiB
  extracted, 20 000 entries), a per-project rate limit, one deployment in
  flight per project, and a refusal before reading the body when the token is
  invalid.
- **Audit:** every attempt, accepted or refused, emits an event with project,
  repository, ref, commit and a stable refusal code. No token or raw error.
- **Pure core (maintainer piece):** `DecideDeployer(claims, trust) Decision`,
  table-tested like `Authorize`, never importing `net/http` or the OIDC SDK.

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
