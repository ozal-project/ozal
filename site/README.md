# Switchboard project page

Static site for `switchboard.octaviosantacruz.com`, built with Astro and served by
Switchboard Pages. Its tokens (dark graphite, violet brand, Geist) are the ones the
console adopts in `UI-005`.

The roadmap and changelog are not written here: `src/lib/tracker.ts` parses
`../docs/TO_IMPLEMENT.md` and `../docs/IMPLEMENTED.md` at build time. A task counts
as done only when `IMPLEMENTED.md` names it and `TO_IMPLEMENT.md` no longer lists it.

Publishing is CI: a push to `main` that touches `site/`, a tracker or
`scripts/pages/` runs `.github/workflows/site.yml`, which builds, refuses secrets
in `dist/`, uploads through the deploy endpoint with a GitHub OIDC token (project
`switchboard-site`), checks the public page and rolls back to the last good build
if that check fails. The page needs no client JavaScript; the roadmap filter is CSS.

```bash
npm ci
npm run dev     # local preview
npm run build   # astro check + static build into dist/
```

Describe capabilities only at the level supported by the current alpha. Keep
automated test coverage, observed single-host operation, experimental code and
roadmap items distinct (`REL-008`); a passing build alone is not deployment
evidence.
