# Console frontend contract

This document defines the security, routing, accessibility and behavior
requirements for the console frontend. The Go binary embeds the React app. Vite
browser acceptance writes to a separate local output so it cannot overwrite the
committed Go assets. Do not combine frameworks in one bundle.

## Change boundary

**In scope**

- `web/console` source, dependencies and build configuration.
- `scripts/build-console.sh` (comments and any check command change).
- The committed build output in `internal/admin/console`.
- Console selectors in `scripts/demo/*.mjs` where the DOM legitimately changes.
- The durable console and CSP contracts in `docs/architecture/stack.md` and
  `docs/engineering.md`.

**Out of scope**

- Any Go handler, API shape, JSON field or route in `internal/admin`.
- The admin listener (loopback or Unix socket only) and its authentication.
- Design, tokens, typography and the Umbral/Marfil identity.
- Permissions, tools and the session model.
- Server-side rendering, a Node runtime or a meta-framework.
- New product behavior. API or permission changes need their own security
  review and regression coverage.

## Frontend stack

Use React function components and hooks, `react-router`, Radix primitives
following the shadcn/ui pattern, and `lucide-react`. Keep Vite, the `$lib` alias,
and the strict TypeScript settings in `tsconfig.json`. Do not add state,
data-fetching or form libraries without a concrete need and a stack entry.

The frontend uses `react`, `react-dom`, `react-router`,
`@radix-ui/*` as used by the copied primitives, `lucide-react`, `clsx`,
`tailwind-merge`, `tailwind-variants`, `tw-animate-css`, `@fontsource/*`, and
the Vite build toolchain. The embedded app and the browser preview use the same
React source but have separate output directories.

## Constraints that must survive

Read `internal/admin/console.go` before touching the build.

- **CSP is strict and non-negotiable except for the documented directive
  below.** Current value:
  `default-src 'none'; script-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'; form-action https://github.com; base-uri 'none'; frame-ancestors 'none'`.
- **No inline scripts.** Vite must keep emitting external files; do not add a
  plugin that inlines the entry or the runtime.
- **`assetsInlineLimit: 0`** exists because `font-src 'self'` rejects `data:`
  fonts. Keep it.
- **`Console` must stay a lazy chunk** (`React.lazy` + `Suspense`), so the
  sign-in screen does not download the whole app. `Root.tsx` loads the router
  after sign-in, and the router loads the console lazily.
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

## CSP and runtime styles

`style-src 'self'` blocks `style` attributes as well as `<style>` elements.
Radix floating components position themselves by setting inline style
properties on elements; their positioning requires the narrow exception.

Use the narrow directive `style-src 'self'; style-src-attr 'unsafe-inline'`.
Keep `<style>` injection blocked; only element-level style attributes are
allowed. A Go regression test checks both directives on the console routes.
Application layout should use classes; the attribute directive exists for
library positioning.

Browsers without `style-src-attr` fall back to `style-src` and lose floating
positioning, not access. Do not use `style-src 'unsafe-inline'`: that would also
allow injected `<style>` blocks. Keep Radix within the narrow directive.

## Parity checklist

Every item is observable behavior, not an implementation detail. Verify each in
a browser, and keep the scripted coverage that already exists.

- **Sign-in:** the animated Ozal doorway mark (disabled for reduced motion),
  passkey ceremony, host-login fallback, disabled user, expired session, resume
  after reload, "session ended" wording.
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
horizontal overflow at four viewport widths. It checks that the sign-in
animation is disabled when the browser requests reduced motion, the desktop
shell retains its animated wordmark and the mobile drawer retains its static
wordmark, Tab and
Shift+Tab wrap between the first and last controls in the mobile drawer, Escape
closes it and restores focus, navigation focuses the destination heading, and
Escape from the desktop account menu restores its trigger. A cutover must also
verify passkey and update flows on the installed edge; this local preview does
not exercise those flows against an installed instance.

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

Budget: entry ≤ 110 KB, lazy console ≤ 200
KB, CSS ≤ 20 KB, total ≤ 260 KB. The sign-in path is what matters for perceived
speed. If the measured result exceeds the budget, report the number and the
cause before changing the budget; do not raise it to make the task pass.
