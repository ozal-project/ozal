# Ozal project page

Static site for `ozal.octaviosantacruz.com` (the old `switchboard.` hostname stays as an
alias), built with Astro and served by Ozal Pages. Its tokens (warm graphite, Marfil
ivory and camel, Geist) are shared with the
console (`UI-005`).

Publishing is CI: a push to `main` that touches `site/` or
`scripts/pages/` runs `.github/workflows/site.yml`, which builds, refuses secrets
in `dist/`, uploads through the deploy endpoint with a GitHub OIDC token (project
`ozal-site`, renamed on the edge with `ozal project rename`), checks the public page and rolls back to the last good build
if that check fails. The explanation, walkthrough tabs and release plan work without JavaScript.
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

The first screen says what Ozal is (the front door for self-hosted apps) with a
before/after comparison. "Everyone goes through one door" follows: an animated map (CSS
only, still under reduced motion, a plain list under 640 px) of three people reaching
Ozal and four apps, with the request pipeline and a `policy explain` trace beside
it. "Make an app private in four steps" and "Your domains, one certificate each" reuse
the console's sign-in explanation and Domains card (demo data). The live engine is a
console-style window: compact choices, the request's path through Ozal and the answer
with its stages. The walkthrough is five real console screens behind native radio tabs
that advance on their own while on screen (paused on hover, handed to the visitor on the
first click or key, off under reduced motion; no video; `scripts/demo/tour.mjs`
regenerates the stills by signing in with a host login). Below it: one card and a status
tag per capability ("In use" on the author's server, "Tested" by automated tests,
"Unreleased" on main) and the four releases on a year-long rail. There is no separate
roadmap page; the trackers stay in `docs/`. The public source is linked; release downloads stay unadvertised until Ozal artifacts exist.
Automatic site publication requires repository variable `OZAL_SITE_DEPLOY_ENABLED=true`,
set only after the edge trusts the new repository owner and workflow.

Browser regression checks reuse the tour's existing Playwright dependency:

```bash
# From the repository root, after building site/ and serving site/dist on loopback:
npm --prefix scripts/demo ci
node scripts/demo/site-check.mjs --url http://127.0.0.1:4321 --chrome /path/to/chrome --out /tmp/ozal-site-check
```

The check covers 1440/1024/768/390/320 px, horizontal overflow, visible navigation,
keyboard walkthrough tabs, tour autoplay and hand-off, live policy decisions, a failed WASM download and
retry, Save-Data, and the page with JavaScript disabled. It does not publish the
site or exercise a production sign-in. `scripts/verify.sh` remains the full gate;
the browser check is separate because it requires an installed Chrome/Chromium.
