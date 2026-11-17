# Ozal project page

Static site for `ozal.dev` (`ozal.octaviosantacruz.com` and the old `switchboard.`
hostname stay as aliases), built with Astro. The live site is published by the
Cloudflare Pages project `ozal` from this repository; the Ozal Pages workflow is
the migration path and stays disabled until the edge is ready. It follows
[product design](../docs/architecture/product-design.md): a graphite ground and grey-white
text, indigo as the only loud colour, on the detail to see first (the headline, the next action,
the current choice), Geist, and green, red and blue kept for states.

Publishing today: a push to `main` is built by the Cloudflare Pages project
`ozal` (GitHub `ozal-project/ozal`, production branch `main`). Its build command
is `bash scripts/build-site-engine.sh && npm --prefix site ci && npm --prefix site run build`;
the output directory is `site/dist`, and `ozal.dev` is a custom domain of that
project. The project deploys independently of the Ozal edge.

The Ozal Pages workflow (`.github/workflows/site.yml`) is the migration path. It
stays disabled while `OZAL_SITE_DEPLOY_ENABLED` is false: it builds, refuses
secrets in `dist/`, uploads through the deploy endpoint with a GitHub OIDC token
(project `ozal-site`, renamed on the edge with `ozal project rename`), checks the
public page and rolls back to the last good build if that check fails. Enable it
only after the deploy endpoint is reachable, the edge runs the current binary and
the DNS moves from `ozal.pages.dev` to the edge. Every claim, the doorway's three requests, the tour and the status list work without JavaScript.
Only the live engine, the doorway's playback, the tour's sticky stage, the copy button and entrance motion use client scripts;
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

The page is ordered as a product story:

1. **First screen.** What Ozal is, "Get started", "Try the live demo", and the doorway: three
   recorded requests for `grafana.acme.dev` (let in, refused with a reason, sent to sign in) that
   walk the checks one by one. Native radios choose the request; `page.ts` plays the three twice
   while on screen and stops at the first click or key. Under reduced motion it rests on the first
   request.
2. **Live demo.** Right after the first screen, so the real engine is one scroll away. An access
   matrix: four people against four apps, every cell answered by the real engine. Choosing a cell
   explains that answer; the sign-in settings re-answer the whole matrix, and a cell whose answer
   changed flashes once. At 480 px and below the matrix hides each app's rule, so the answer
   names it ("Grafana's rule: Company email").
3. **Why it exists.** The three usual ways to share an internal tool, what each costs, and the
   one front door.
4. **How it works.** The checks in order, a `policy explain` trace, and the identity headers an
   app receives. Only stages that run by default are listed.
5. **The console.** Five real screens, each labelled with who sees it (operator, Ana or Bob)
   and cropped in CSS to the part the step is about; the link opens the whole still. On a wide
   screen `page.ts` adds `.is-scrolly`: the screens share one sticky stage and the one shown is
   the step whose text is at the middle of the viewport. Each step's text is about 40% of the
   viewport tall, and a step not on stage stays readable in the muted colour. The page scrolls
   normally and never moves on its own. Without scripts, or on a narrow screen, every step keeps
   its own screen. Below 861 px a console step shows a phone-width still of the one detail it
   names (`<name>-narrow.jpg`, through `<picture>`), whole and readable.
   The page carries no video. `scripts/demo/tour.mjs` regenerates every still by signing in with
   a host login, including the narrow ones, clipped from the texts each `shot()` call names; if
   the wide layout changes, update the `.crop-*` rules in `src/styles.css`, and if a narrow
   still's height changes, its `narrowH` in `src/pages/index.astro`. Each still's URL carries a
   hash of its content (`?v=`), so a new capture is never hidden by a cached old one. The section
   says the console is on main, not yet in alpha.1, with the status list's "Unreleased" tag.
6. **Also in the binary, status, and get started.** A Domains card (demo data, marked as on main
   and not yet in alpha.1) and static sites, what works today with how far each feature is
   proven, who Ozal is and is not for, and the commands for a local trial. The guided next step,
   "Publish your first application" at `/docs/first-application/`, leads; the source is the
   secondary link. Each capability is described once: sign-in lives in the pipeline and the
   tour, the decision record in the tour's last step.

Once a visitor chooses a request in the doorway, the answer arrives in 150 ms; the slower
walk-through is only for autoplay. Buttons, toggles and summaries a thumb presses are at least
44 px tall, as [product design](../docs/architecture/product-design.md) asks.

## Documentation at /docs

`/docs/` renders the user guides from the repository's own Markdown at build time; nothing is
copied into `site/`. `src/lib/guides.mjs` lists them (Start here, the quickstart, install,
publish an application, administer, security, and configuration and CLI) with their slugs, and
`src/content.config.ts` loads those files from the repository root. `src/pages/docs/[...slug].astro` renders each one with the list of guides, its sections and a link to the file on GitHub, and
rewrites its links: a link to another listed guide becomes its page with the anchor kept, any
other repository file (design, engineering, examples) its GitHub page. A link that leaves the
repository fails the build. Code blocks are plain, like the page's terminals: no highlighting
styles. Each page says the guides follow main and that "Unreleased" features are not in alpha.1.
A change to a listed guide republishes the site: Cloudflare Pages rebuilds on a push that
touches `site/` or the guide, and the Ozal workflow has its own paths for the migration. The
guides are in the generated `/sitemap.xml` and `public/llms.txt`.

The comparison and setup pages live outside `/docs`: `/compare/cloudflare-access/`,
`/compare/pomerium/`, `/guides/grafana-sso/`, `/guides/vpn-vs-access-gateway/`,
`/guides/protect-internal-app/`, `/guides/oidc-or-passkeys/` and
`/guides/reverse-proxy-vs-access-gateway/`.
`src/lib/articles.mjs` is their single list: the sitemap, the home page's read-next row and
the page metadata all come from it. They share `src/components/Article.astro` for the
breadcrumb, the reviewed date, the schema and the read-next list. They are long-form, use
the same prose styles as the guides, and link to each other and to the public documentation.

The site presents capabilities without a public roadmap. The public source is linked; the page
does not link to release downloads.
The live site does not need `OZAL_SITE_DEPLOY_ENABLED`. That variable enables the Ozal Pages
migration workflow only; the Cloudflare Pages project publishes on its own. When the migration
is ready, set the variable to true and move the DNS in the same change.

## Discovery by search engines and assistants

The page is static HTML, so every claim is readable without JavaScript. The
home title and hero name the category in the words people search
(`self-hosted access gateway`, `SSO`, `OIDC`, `passkeys`, `without a VPN`), and
the guides carry it through. The machine-readable layer:

- `public/robots.txt` allows ordinary search and names the assistant search
  and on-request agents explicitly (`OAI-SearchBot`, `ChatGPT-User`,
  `Claude-SearchBot`, `Claude-User`, `PerplexityBot`, `Perplexity-User`), so
  Ozal can appear in answers. Training collectors (`GPTBot`, `ClaudeBot`,
  `Google-Extended`, `Applebot-Extended`, `CCBot`) are disallowed: appearing
  in answers does not need them. Cloudflare prepends its Content
  Signals preamble to the origin file, so the live response starts with that
  comment block and then this one.
- `src/pages/sitemap.xml.ts` builds the sitemap from the same lists as the
  pages (guides and articles), so a new page appears without a second edit;
  `robots.txt` references it. The 404 page is not listed.
- `src/lib/articles.mjs` lists the comparison and setup pages under
  `/compare/` and `/guides/`; the sitemap and the home's read-next row use
  the same list.
- `public/llms.txt` is a short Markdown summary for agents: what Ozal is, what
  it does, what it does not do, and links. No major provider documents reading
  `llms.txt` at inference time, so treat it as a summary for tools and people,
  not as a switch that makes ChatGPT or Claude cite the page.
- `src/layouts/Base.astro` emits JSON-LD: `WebSite` and `SoftwareApplication`
  on the home page, a `WebPage` on every page, and a `TechArticle` with a
  `BreadcrumbList` on each guide. It also sets `og:type=article` for guides,
  `robots` with `max-snippet:-1`, and Open Graph/Twitter metadata.
- `src/pages/404.astro` builds `/404.html`. A deployment that ships that file
  answers missing paths with it and status 404; the static handler gives it
  precedence over the SPA fallback, so an Astro site does not need `spa`.
- `public/<key>.txt` is the IndexNow key (public by design; the value is
  `INDEXNOW_KEY` in `.github/workflows/site.yml`). The Ozal Pages workflow posts
  the sitemap's URLs to `api.indexnow.org` after a deploy passes public
  acceptance, which tells Bing and the other IndexNow engines at once. The
  Cloudflare Pages path does not submit automatically: after a content change,
  submit the sitemap in Bing or make the same request. A refusal is a warning,
  never a failed deploy. To rotate the key, replace the file and the variable in
  the same commit.
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

On the Ozal Pages migration path, changing the static handler and the site
together matters: `ozalctl site accept` already rejects a 200 on an unknown
path. Deploy the edge build that serves `404.html` before publishing a site that
ships it, or acceptance will roll the site back. Cloudflare Pages serves
`404.html` with status 404 without that step.

Measurement uses Search Console, Bing and the server's request logs. The
Cloudflare Pages project keeps Web Analytics off and the page loads no
third-party script, as the product design requires.

Verify the live result after publishing:

```bash
curl -sS https://ozal.dev/robots.txt | grep -E 'OAI-SearchBot|Claude-SearchBot|Sitemap'
curl -sSI https://ozal.dev/sitemap.xml | head -1
curl -sSI https://ozal.dev/llms.txt | head -1
curl -sS https://ozal.dev/ | grep -o 'application/ld+json'
curl -sS -o /dev/null -w '%{http_code}\n' https://ozal.dev/no-such-page   # 404
curl -sSI https://ozal.dev/ | grep -iE 'content-security-policy|strict-transport-security|x-frame-options|permissions-policy'
```

Browser regression checks reuse the tour's existing Playwright dependency:

```bash
# From the repository root, after building site/ and serving site/dist on loopback:
npm --prefix scripts/demo ci
node scripts/demo/site-check.mjs --url http://127.0.0.1:4321 --chrome /path/to/chrome --out /tmp/ozal-site-check
```

The check covers 1440/1024/768/390/320 px, horizontal overflow, visible navigation,
the tour's sticky stage, the doorway's playback and hand-off, the matrix and its live decisions, a failed WASM download and
retry, Save-Data, the page with JavaScript disabled, the answer naming the app's rule on a
phone, whole app names at 320 px, 44 px targets, the guided next step, the doorway answering a
chosen request at once, and every guide at /docs with its links, anchors and phone widths. It does not publish the
site or exercise a production sign-in. `scripts/verify.sh` remains the full gate;
the browser check is separate because it requires an installed Chrome/Chromium.
