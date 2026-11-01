# Console migration: Svelte to React

**Status:** decided 2026-11-01 (owner). Task `UI-012` in
[TO_IMPLEMENT](../TO_IMPLEMENT.md). The tree still ships the Svelte console;
this document is the execution contract for replacing it and the definition of
"done". It is deleted when the task closes, after its durable decisions move to
[engineering.md](../engineering.md) and [stack.md](stack.md).

## Decision and reason

React is the long-term base for the admin console. The reason is ecosystem
breadth and contributor familiarity: Radix primitives, TanStack Table, form and
data libraries, and the tooling most people and agents already know. Ozal keeps
choosing a library over writing primitives by hand, as it does for WebAuthn,
OIDC and JOSE.

This is **not** a claim about performance, security or accessibility. Svelte
resolves this application today and `bits-ui` provides its primitives. The
migration is a maintenance decision with a real cost, accepted by the owner.

Bundle weight was measured and is not a blocker: the console loads once per tab
over loopback or an SSH tunnel and every asset under `assets/` is served
`immutable`. The expected cost is roughly 30 KB more gzip on the lazy chunk. A
budget still applies (see below) so the change cannot silently grow.

## Scope

**In scope**

- `web/console` source, dependencies and build configuration.
- `scripts/build-console.sh` (comments and any check command change).
- The committed build output in `internal/admin/console`.
- Console selectors in `scripts/demo/*.mjs` where the DOM legitimately changes.
- `docs/architecture/stack.md` status and the durable CSP note.

**Out of scope**

- Any Go handler, API shape, JSON field or route in `internal/admin`.
- The admin listener (loopback or Unix socket only) and its authentication.
- Design, tokens, typography and the Umbral/Marfil identity.
- Permissions, tools and the session model.
- Server-side rendering, a Node runtime or a meta-framework.
- New product behavior. A migration that needs a feature change is two tasks.

## Current inventory (baseline 2026-11-01)

Re-measure before starting; these numbers are the reference, not a promise.

| Item | Count / value |
|---|---|
| Svelte components | 88 files |
| TypeScript modules | 48 files |
| Pure logic tests (`node --test src/lib/*.test.ts`) | 13 files, no Svelte or bits-ui imports |
| Pages | 12: Activity, Administrators, Applications, Connectors, Domains, Groups, Integrations, Overview, People, Projects, Routes, Sites |
| UI primitives (`src/lib/components/ui`) | 13: alert, badge, button, card, checkbox, input, label, radio-group, select, separator, skeleton, table, tooltip |
| Router | `src/lib/router.svelte.ts`, about 40 lines, `history.pushState` + `popstate` |
| Icons | `@lucide/svelte` in 27 files |
| Base CSS | `src/app.css`, 246 lines, Tailwind 4 + tokens |
| Built output | entry ~160 KB (~48 KB gzip), lazy console ~420 KB (~122 KB gzip), CSS ~76 KB (~14 KB gzip) |
| Browser scripts | 14 in `scripts/demo`, 186 scripted lookups overall; only the console ones are in scope |

The 13 pure tests and the 48 TypeScript modules are framework-free and must keep
passing **without modification**. If a test needs to change, the behavior
changed; stop and report it.

## Target stack

| Today | Target | Note |
|---|---|---|
| Svelte 5 + runes | React | Function components and hooks; no class components |
| `@sveltejs/vite-plugin-svelte` | `@vitejs/plugin-react` | Vite, `outDir`, `assetsDir` and `assetsInlineLimit` stay |
| `bits-ui` | Radix primitives with the shadcn/ui pattern | Components are copied into the repo, not imported from a package |
| `@lucide/svelte` | `lucide-react` | Same icon set |
| `router.svelte.ts` | `react-router` | The route table and aliases below stay identical |
| `svelte-check` | `tsc --noEmit` | Same gate role in `build-console.sh` |
| `@tsconfig/svelte` | a plain strict `tsconfig.json` with `jsx: react-jsx` | Keep `strict`, `noEmit`, `isolatedModules` |

Do not add a state library, a data-fetching library or a form library in this
task. Add them only when a page needs them, with a stack entry first. The
existing `$lib` alias and `components.json` aliases stay so imports keep
working; shadcn/ui supports custom aliases.

Expected dependencies after the cutover: `react`, `react-dom`, `react-router`,
`@radix-ui/*` as used by the copied primitives, `lucide-react`, `clsx`,
`tailwind-merge`, `tailwind-variants`, `tw-animate-css`, `@fontsource/*`, and
the build toolchain. Remove `svelte`, `svelte-check`, `@tsconfig/svelte`,
`@sveltejs/vite-plugin-svelte`, `bits-ui` and `@lucide/svelte` in the same
commit that removes the last `.svelte` file.

`@internationalized/date` stays in the tree because `bits-ui` requires it. The
direct declaration can be dropped when `bits-ui` leaves, not before.

## Constraints that must survive

Read `internal/admin/console.go` before touching the build.

- **CSP is strict and non-negotiable except for the documented directive
  below.** Current value:
  `default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'self'; form-action https://github.com; base-uri 'none'; frame-ancestors 'none'`.
- **No inline scripts.** Vite must keep emitting external files; do not add a
  plugin that inlines the entry or the runtime.
- **`assetsInlineLimit: 0`** exists because `font-src 'self'` rejects `data:`
  fonts. Keep it.
- **`Console` must stay a lazy chunk** (`React.lazy` + `Suspense`), so the
  sign-in screen does not download the whole app. The current split is
  `Root.svelte` importing `Console.svelte` dynamically; keep the equivalent.
- **Committed build output.** `scripts/verify.sh` stage `[6b]` rebuilds the
  console and fails when `internal/admin/console` is stale, and
  `OZAL_REQUIRE_FRONTEND=1` turns a skipped console into a failure in CI. Commit
  source and build together.
- **Logic tests stay framework-free.** Stage `[6d]` runs
  `node --test web/console/src/lib/*.test.ts`.
- **Caching:** `assets/*` is `immutable`, everything else is `no-store`. Hashed
  filenames are required.
- **Route table:** `consoleRoutes` in `internal/admin/console.go` is the server
  side. The client must keep these routes and aliases: `/projects` → `/sites`,
  `/simulator` → `/activity`, unknown routes → `/overview` while retaining the
  query and fragment, `/routes` keeps its `?host=` query, and the GitHub return
  restores the last tab (`sessionStorage`, one known route) before processing `code`, `state`,
  `setup_action` or `installation_id` on `/applications`.
- **No secrets in the bundle.** The admin token lives in tab memory; after a
  reload the edge returns it from the `HttpOnly` resume cookie.
- **Accessibility:** the shell keeps its keyboard behavior (Escape closes the
  menu, Tab stays contained, focus returns to the trigger). Acceptance runs axe
  at 1440, 1024, 390 and 320 px.

## Blocking decision: inline styles and CSP

`style-src 'self'` blocks `style` attributes as well as `<style>` elements.
Evidence in the current tree:

- `web/console/src/pages/Applications.svelte` sets
  `style="grid-template-columns: repeat(...)"` on the stepper.
- The built `bits-ui` bundle calls `element.style.setProperty(...)`; floating
  positioning needs it. Radix uses the same technique, and more often.

So the console already ships a CSP conflict. The migration must settle it
**before** porting pages, as its own small commit:

1. Add the narrow directive:
   `style-src 'self'; style-src-attr 'unsafe-inline'`.
2. Keep `<style>` injection blocked; only element-level style attributes are
   allowed. This is a data channel, not script execution.
3. Add a Go regression test asserting both `style-src 'self'` and
   `style-src-attr 'unsafe-inline'`, with a comment stating why.
4. Replace the `Applications.svelte` inline style with a class anyway; the
   directive is for library positioning, not for application layout.

Browsers without `style-src-attr` fall back to `style-src` and lose floating
positioning, not access. Do not use `style-src 'unsafe-inline'`: that would also
allow injected `<style>` blocks. If Radix cannot work under the narrow
directive, stop and report; do not widen it silently.

Move this decision to `docs/engineering.md` (Development and stack criteria)
when `UI-012` closes.

## Order of work

Each step is a commit that leaves the tree buildable. Do not run a hybrid
Svelte+React bundle: two frameworks in one bundle costs more than one cutover.

0. **Preconditions.** No in-flight console work in the tree. Baseline bundle
   measured and recorded in the task. Branch.
1. **CSP commit** (above), independent of the framework change.
2. **Scaffold.** Add React, `@vitejs/plugin-react`, `react-router` and the
   shadcn/ui React primitives. Replace `components.json` with the React schema,
   keeping `$lib` aliases. `index.html` stays structurally identical.
   `build-console.sh` runs `tsc --noEmit` instead of `svelte-check`.
3. **Primitives.** Port the 13 primitives to Radix/shadcn. Verify keyboard,
   focus trap and axe behavior before any page uses them.
4. **Shell.** Router, root, lazy console chunk, sign-in, session resume,
   step-up, mobile directory. This is where a CSP or routing mistake is
   cheapest to find.
5. **Pages.** The original sequence was Overview, Activity, Routes, Projects,
   Administrators, Groups, People, Domains, Integrations, Connectors, Sites,
   Applications. The implementation departed from that sequence after Routes:
   Domains and Integrations were moved forward to validate certificate/DNS and
   GitHub flows against the React API layer, then Applications was moved ahead
   of the remaining directory and publishing pages to establish the access
   configuration flow those pages depend on. This sequencing choice was made
   during implementation; it does not reduce the parity checklist. All pages
   now have React preview routes. Administrators is routed at `/administrators`
   with bounded tool grants and reviewed account actions; Groups at `/groups`
   with concurrency-aware membership updates; People at `/people` with
   invitations, membership editing, account status and passkey management; and
   Connectors at `/connectors` with registry state and the five-step enrolment
   guide. The Projects workflow is covered within the Sites/Publishing React
   preview port. Browser and edge cutover acceptance remains outstanding. Applications is the largest
   view (576 lines, stepper and uploads), so its early port was a deliberate
   vertical slice rather than a size-based priority.
6. **Acceptance selectors.** Prefer `getByRole` and accessible names, which
   survive a framework change. Update only selectors whose DOM legitimately
   changed; do not add test-only attributes to the app.
7. **Cutover.** Delete `*.svelte`, `svelte.config.js`, `@sveltejs/*`, `svelte*`,
   `bits-ui` and `@lucide/svelte`. Rebuild, commit source plus
   `internal/admin/console`.

The initial shell port omitted step-up and the update dialog. React now ports
both in `web/console/src/react/UpdateDialog.tsx`: the update executor's
`passkey_required` response triggers WebAuthn confirmation on the current
session, then retries the same reviewed plan once. The dialog reads a pending
job on open, polls by job ID, and closing it only stops browser polling; the
host job continues. A local browser fixture verified the passkey retry,
pending-job close/reopen, and completion flow. This is preview evidence only;
real-edge update execution and browser passkey acceptance remain part of the
cutover gate.

The account shell also preserves the Svelte passkey-management actions:
`UserMenu.tsx` can add a passkey on this device, issue a one-use link for
another browser, and open `PasskeysDialog.tsx` to review and revoke a lost
credential. Overview shows the second-passkey recovery reminder while an
administrator has only one passkey, and refreshes it when the tab regains
focus. `react-passkey-acceptance.mjs` exercises these flows against a disposable
Go edge with Chrome virtual authenticators; it is local preview evidence, not
the real-edge cutover acceptance.

Enrolment now preserves name and email input if `enrolInfo` resolves while the
operator is typing. `react-update-acceptance.mjs` delays that response to
exercise the race, then checks the React update review against the real Go
edge's WebAuthn step-up. The plan and update-job responses are fixtures because
the disposable edge has no configured update executor; this does not verify an
actual installation.

Publishing from Sites keeps the separate final confirmation before opening a
workflow pull request. `react-publishing-acceptance.mjs` verifies that
cancelling this confirmation sends no GitHub write and confirming it sends one
request with the reviewed workflow digest. GitHub responses are fixtures.

The Sites view keeps its refresh action when Pages is not enabled, so an
operator can retry after the host configuration changes. Its browser acceptance
also verifies hostname review, cancellation without a rollback request, and
reconciliation after restoring a retained deployment.

## Parity checklist

Every item is observable behavior, not an implementation detail. Verify each in
a browser, and keep the scripted coverage that already exists.

- **Sign-in:** passkey ceremony, host-login fallback, disabled user, expired
  session, resume after reload, "session ended" wording.
- **Enrolment and recovery:** token in the fragment, one use, expiry,
  replacement of previous passkeys, alert on `user.recovered`.
- **Step-up:** five-minute proof, single retry, no second session.
- **Update dialog:** phases, polling by job ID, close and reopen, intervention
  required, unreachable host versus "already current".
- **Sites, Projects and publishing:** upload, history, rollback, reviewed plan
  and digest, GitHub workflow proposal, and project workflow review.
- **Applications:** resource fields, validation, stepper, access methods,
  preview.
- **People, groups, administrators:** invite, revoke, tools, groups, device
  management.
- **Activity:** SSE stream, lost-stream state, bounded feed, filters.
- **Domains:** provider state, certificate status, order of operations.
- **Integrations:** GitHub return path and tab memory, manifest form posts to
  `github.com` only.
- **Connectors:** registry states, certificate expiry, five-step enrolment,
  observed enrolment/connectivity progress, and edge-host revocation guidance.
- **Navigation:** 1440/1024/390/320 px, keyboard, Escape, focus return, reduced
  motion, no horizontal scroll.

## Verification

```sh
cd web/console && npm ci && npm run check && npm run build
scripts/build-console.sh
node --test web/console/src/lib/*.test.ts
OZAL_BIN=/path/to/ozal node scripts/demo/react-console-acceptance.mjs
OZAL_BIN=/path/to/ozal CHROME=/path/to/chrome node scripts/demo/react-passkey-acceptance.mjs
OZAL_BIN=/path/to/ozal CHROME=/path/to/chrome node scripts/demo/react-update-acceptance.mjs
OZAL_BIN=/path/to/ozal CHROME=/path/to/chrome node scripts/demo/react-publishing-acceptance.mjs
OZAL_BIN=/path/to/ozal CHROME=/path/to/chrome node scripts/demo/react-sites-acceptance.mjs
node scripts/demo/product-acceptance.mjs
node scripts/demo/passkey-acceptance.mjs
OZAL_BIN=/path/binary node scripts/demo/update-acceptance.mjs
node scripts/demo/workflow-acceptance.mjs
node scripts/demo/domains-acceptance.mjs
scripts/verify.sh
```

`react-console-acceptance.mjs` builds the isolated React preview, serves it
with the console CSP, and proxies its API calls to a disposable loopback edge.
It checks the host-issued operator login, page routes and aliases, resumed
session, unknown-route redirect with query/fragment preservation, axe and
horizontal overflow at four viewport widths. It also checks that Tab and
Shift+Tab wrap between the first and last controls in the mobile drawer, Escape
closes it and restores focus, navigation focuses the destination heading, and
Escape from the desktop account menu restores its trigger. This is local
preview evidence only; real-edge passkey and update acceptance still gate the
cutover.

`react-passkey-acceptance.mjs` uses the same production preview and disposable
edge with separate virtual-authenticator browser contexts. It covers first
administrator enrolment, the backup-passkey reminder, a one-use second-device
link, passkey listing and reviewed removal, and passkey sign-in after sign-out.
It does not establish live-edge or public acceptance.

`react-update-acceptance.mjs` uses the production preview and a disposable Go
edge. It exercises first-admin enrolment with delayed enrolment info, a real
WebAuthn step-up against that edge, and the React review/retry/polling flow.
The update plan and executor endpoints are fixtures, so it does not establish
real installation, live-edge, or public acceptance.

`react-publishing-acceptance.mjs` uses the production preview and a disposable
Go edge. It exercises workflow preview, the final pull-request confirmation,
cancellation without a write, and one confirmed request carrying the reviewed
digest. GitHub responses are fixtures, so it does not establish GitHub App or
repository acceptance.

`react-sites-acceptance.mjs` uses the production preview and a disposable Go
edge. It verifies recovery after Pages becomes available, review of the mapped
hostname before rollback, cancellation without a mutation, and the updated live
deployment after confirmation. Site and rollback responses are fixtures.

Also check by hand: Windows-style MIME behavior is covered by `consoleTypes`;
the CSP header on `index.html` and on a hashed asset; an old hashed asset is
still served after a rebuild only if it exists (otherwise 404 is correct); and
`/_/api/*` still never falls through to the app.

## Bundle budget

Baseline gzip: entry ~48 KB, lazy console ~122 KB, CSS ~14 KB (about 184 KB
total after sign-in). Budget after migration: entry ≤ 110 KB, lazy console ≤ 200
KB, CSS ≤ 20 KB, total ≤ 260 KB. The sign-in path is what matters for perceived
speed. If the measured result exceeds the budget, report the number and the
cause before changing the budget; do not raise it to make the task pass.

## Stop conditions

Stop and report instead of improvising when:

- Radix needs more than `style-src-attr 'unsafe-inline'`.
- Parity requires changing a Go handler, an API field or a route.
- A pure logic test has to change.
- Browser acceptance cannot reach parity after the shell step.
- The build output cannot be reproduced byte-for-byte across two runs.

In any of those cases, keep the Svelte console working and leave the branch
reversible.

## Rollback

The cutover is one commit containing source and built assets and nothing else.
Reverting it restores the Svelte console completely: no database migration, no
config change, no API change.

## When it closes

- Move the CSP decision to `docs/engineering.md`.
- Update the Console row in `docs/architecture/stack.md` to "shipped" and drop
  the pending note.
- Move `UI-012` from [TO_IMPLEMENT](../TO_IMPLEMENT.md) to
  [IMPLEMENTED](../IMPLEMENTED.md) with the commit and the measured bundle.
- Delete this document.
