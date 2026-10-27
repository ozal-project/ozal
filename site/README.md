# Switchboard project page

Static site for `switchboard.octaviosantacruz.com`, built with Astro and served by
Switchboard Pages. Its tokens (dark graphite, violet brand, Geist) are shared with the
console (`UI-005`).

The roadmap and changelog are not written here: `src/lib/tracker.ts` parses
`../docs/TO_IMPLEMENT.md` and `../docs/IMPLEMENTED.md` at build time. A task counts
as done only when `IMPLEMENTED.md` names it and `TO_IMPLEMENT.md` no longer lists it.

Publishing is CI: a push to `main` that touches `site/`, a tracker or
`scripts/pages/` runs `.github/workflows/site.yml`, which builds, refuses secrets
in `dist/`, uploads through the deploy endpoint with a GitHub OIDC token (project
`switchboard-site`), checks the public page and rolls back to the last good build
if that check fails. The explanation, walkthrough tabs and roadmap work without JavaScript.
Only the optional live engine and progressive entrance motion use client scripts;
all content is visible if those scripts fail.

```bash
npm ci
npm run dev     # local preview
npm run build   # astro check + static build into dist/
```

Describe capabilities only at the level supported by the current alpha. Keep
automated test coverage, observed single-host operation, experimental code and
roadmap items distinct (`REL-008`); a passing build alone is not deployment
evidence.

The "Ask it a question" section runs `internal/auth` in the browser: `scripts/build-site-engine.sh`
compiles `cmd/policywasm` to `public/engine.wasm` (about 7 MB, 2 MB compressed, loaded only when the
section comes into view) and copies the matching `wasm_exec.js`. Both are build outputs and are not
committed. `internal/policydemo` is the only code in between; it adds input checks, not policy, and a
test compares its answer with `auth.NewReport`. The page states what it does not simulate.

The first screen says what Switchboard is (the front door for self-hosted apps) with a
before/after comparison. "Everyone goes through one door" follows: an animated map (CSS
only, still under reduced motion, a plain list under 640 px) of three people reaching
Switchboard and four apps, with the request pipeline and a `policy explain` trace beside
it. The live engine has its own section after it. The walkthrough is five real console
screens behind native radio tabs (no video; `scripts/demo/tour.mjs` regenerates the
stills by signing in with a host login). Below it the page stays short: one line and a
status tag per capability ("In use" on the author's server, "Tested" by automated tests
only, "Unreleased" on main), a four-release timeline, and a link to `/roadmap/`, which
is read from the trackers. Private downloads have no public CTA while `sourcePublic` is false.

Browser regression checks reuse the tour's existing Playwright dependency:

```bash
# From the repository root, after building site/ and serving site/dist on loopback:
npm --prefix scripts/demo ci
node scripts/demo/site-check.mjs --url http://127.0.0.1:4321 --chrome /path/to/chrome --out /tmp/switchboard-site-check
```

The check covers 1440/1024/768/390/320 px, horizontal overflow, visible navigation,
keyboard walkthrough tabs, live policy decisions, a failed WASM download and
retry, Save-Data, and the page with JavaScript disabled. It does not publish the
site or exercise a production sign-in. `scripts/verify.sh` remains the full gate;
the browser check is separate because it requires an installed Chrome/Chromium.
