# Ozal project page

Static site for `ozal.dev` (`ozal.octaviosantacruz.com` and the old `switchboard.`
hostname stay as aliases), built with Astro and served by Ozal Pages. Its tokens (warm graphite, Marfil
ivory and camel, Geist) are shared with the
console.

Publishing is CI: a push to `main` that touches `site/` or the engine paths
runs `.github/workflows/site.yml`, which builds, refuses secrets
in `dist/`, uploads through the deploy endpoint with a GitHub OIDC token (project
`ozal-site`, renamed on the edge with `ozal project rename`), checks the public page and rolls back to the last good build
if that check fails. The explanation, walkthrough tabs and capability cards work without JavaScript.
Only the optional live engine and progressive entrance motion use client scripts;
all content is visible if those scripts fail.

```bash
npm ci
npm run dev     # local preview
npm run build   # astro check + static build into dist/
```

Describe capabilities only at the level supported by the current alpha. Keep
automated test coverage, observed single-host operation, experimental code and
unreleased work distinct; a passing build alone is not deployment
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
regenerates the stills by signing in with a host login). Below it, capability
cards summarize Ozal's access, identity, policy and publication surfaces. The
site presents capabilities without a public roadmap. The public source is
linked; release downloads stay unadvertised until Ozal artifacts exist.
Automatic site publication requires repository variable `OZAL_SITE_DEPLOY_ENABLED=true`,
set only after the edge trusts the new repository owner and workflow.

## Discovery by search engines and assistants

The page is static HTML, so every claim is readable without JavaScript. Four
pieces carry the machine-readable layer:

- `public/robots.txt` allows ordinary search and names the assistant search
  and on-request agents explicitly (`OAI-SearchBot`, `ChatGPT-User`,
  `Claude-SearchBot`, `Claude-User`, `PerplexityBot`, `Perplexity-User`), so
  Ozal can appear in answers. Training collectors (`GPTBot`, `ClaudeBot`,
  `Google-Extended`, `Applebot-Extended`, `CCBot`) are disallowed: appearing
  in answers does not need them. Cloudflare prepends its Content
  Signals preamble to the origin file, so the live response starts with that
  comment block and then this one.
- `public/sitemap.xml` lists the canonical URL; `robots.txt` references it.
- `public/llms.txt` is a short Markdown summary for agents: what Ozal is, what
  it does, what it does not do, and links. No major provider documents reading
  `llms.txt` at inference time, so treat it as a summary for tools and people,
  not as a switch that makes ChatGPT or Claude cite the page.
- `src/layouts/Base.astro` emits JSON-LD (`WebSite` + `SoftwareApplication`),
  `robots` with `max-snippet:-1`, and Open Graph/Twitter metadata.
- `public/<key>.txt` is the IndexNow key (public by design; the value is
  `INDEXNOW_KEY` in `.github/workflows/site.yml`). After a deploy passes
  public acceptance, the workflow posts the sitemap's URLs to
  `api.indexnow.org`, which tells Bing and the other IndexNow engines at once.
  A refusal is a warning in the run summary and never fails the deploy. To
  rotate the key, replace the file and the variable in the same commit.
- Cloudflare's Email Address Obfuscation is off for `ozal.dev` (2026-11-01):
  it rewrote the demo's fictional addresses into `/cdn-cgi/l/email-protection`
  links, so crawlers without JavaScript read "[email protected]" and followed
  dead links. The page holds no real address to protect.

The owner-only steps matter more than the files: verify `ozal.dev` in
[Bing Webmaster Tools](https://www.bing.com/webmasters) (ChatGPT search uses
Bing among its sources) and [Google Search Console](https://search.google.com/search-console),
submit `https://ozal.dev/sitemap.xml`, and check Cloudflare's AI Crawl Control
so it does not block the search agents (checked 2026-11-01: OpenAI's `OAI-SearchBot` and `ChatGPT-User` received 200, and ChatGPT read the page). Indexing takes days to weeks and is
never guaranteed. Third-party mentions (self-hosted directories, comparison
pages, GitHub topics and the README) are what make an assistant willing to
name the product, not just describe the page.

Verify the live result after publishing:

```bash
curl -sS https://ozal.dev/robots.txt | grep -E 'OAI-SearchBot|Claude-SearchBot|Sitemap'
curl -sSI https://ozal.dev/sitemap.xml | head -1
curl -sSI https://ozal.dev/llms.txt | head -1
curl -sS https://ozal.dev/ | grep -o 'application/ld+json'
```

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
