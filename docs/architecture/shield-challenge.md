# Shield challenge (design proposal)

**Status:** proposed 2026-10-19. Design only; no implementation. Tracker:
`SHD-003` in [TO_IMPLEMENT.md](../TO_IMPLEMENT.md). This is a learning module,
not a release promise.

Related: [VISION Shield](../../VISION.md), [design pattern](design.md),
[rate limiting](../../internal/ratelimit/), [auth](../../internal/auth/) and
[audit contract](audit-contract.md).

## Goal

A privacy-friendly proof-of-work challenge at the edge, in the spirit of
Cloudflare Turnstile and Anubis: a visitor proves some work before reaching a
route, without loading third-party JavaScript or sending browsing data to a
CAPTCHA vendor. Applications behind Switchboard need no integration.

This is **not** a Turnstile-equivalent. Turnstile uses global network signals,
managed/invisible challenges and a large telemetry base. A self-hosted gateway
can raise the cost of naive bots and crawlers; it cannot reproduce that
reputation network.

## Non-goals

- A stand-alone CAPTCHA SaaS with site keys, hosted widgets or a public
  `siteverify` API. That is a separate product and is explicitly deferred.
- A replacement for rate limiting, WAF rules, authentication or Cloudflare's
  volumetric DDoS protection.
- Protection for APIs and native clients that cannot run JavaScript. Those use
  tokens, allowlists or upstream controls.
- Invisible managed challenges driven by global reputation. “Managed” here
  means local signals only, and must be described as such.
- Accessibility parity without a fallback. A challenge without an accessible
  path is not acceptable.

## Threat model

| Adversary | Effect | Limit |
|---|---|---|
| Naive scanner or script with no JS | Challenge blocks or slows it | Trivial to bypass with a browser |
| Credential stuffing against `/_/auth/*` | Rate limit + challenge raises cost | A distributed botnet can still try |
| AI crawler ignoring `robots.txt` | PoW raises crawl cost | Determined crawler with headless browsers pays it |
| Headless browser / GPU farm | Solves PoW | PoW is a speed bump, not a wall |
| Volumetric DDoS | None | Still requires upstream protection |
| Low-power device or assistive technology | May fail the challenge | Needs fallback, time budget and accessibility review |

The honest claim: **filters unsophisticated automation and makes bulk abuse
more expensive; it does not prove a visitor is human.**

## Flow

```text
request without clearance
  -> challenge page / JSON 403 with challenge endpoint
  -> signed challenge (host, route, nonce, exp, difficulty)
  -> browser computes PoW (WebCrypto, worker)
  -> POST solution
  -> verify signature, nonce, expiry, difficulty
  -> set short-lived clearance cookie (host-only)
  -> redirect to the original URL (GET) or ask the client to retry (API)
```

## Token and clearance design

Challenge token (opaque to the client, signed by Switchboard):

- `v`: token version.
- `host`: normalized route host.
- `action`: route/path scope the clearance applies to.
- `nonce`: random, single-use, stored with TTL.
- `iat`, `exp`: short issuance window (for example 2 minutes).
- `difficulty`: leading-zero bits for the PoW.
- `method`: `sha256-pow`.
- Signature: HMAC-SHA256 with a key from `file:`/`env:` for the first version;
  Ed25519 if a separate verifier appears. Never put the key in the page.

Clearance cookie:

- Name: `sb_clearance`.
- Host-only (no `Domain`), `Path=/`, `HttpOnly`, `Secure` on TLS,
  `SameSite=Lax`.
- Short TTL (15–30 minutes), renewable on successful verification.
- Signed payload with host, action, expiry and a random ID; do not bind to IP
  by default (mobile networks would break), but document the trade-off.
- Never include the challenge nonce or any secret in the cookie.

Nonce store:

- Bounded TTL map or SQLite table; single-use; purge on expiry and at capacity.
- Rate-limit issuance and failed verification per IP/host.
- A restart may invalidate outstanding challenges; that is acceptable if
  documented. Do not let the store grow without bound.

PoW:

- SHA-256 with N leading zero bits; `difficulty` is capped (for example 24 bits)
  to avoid punishing low-power clients.
- Run in a Web Worker where available; no external scripts; no `eval`.
- Provide a visible progress state and a timeout/fallback path.
- Difficulty may adapt to local rate-limit pressure, but never silently to a
  level a normal device cannot finish.

## Configuration

Per-route, off by default:

```json
{
  "host": "app.example.com",
  "target": "http://127.0.0.1:9000",
  "challenge": {
    "mode": "managed",
    "difficulty": 20,
    "clearance_ttl_seconds": 1800
  }
}
```

- `off`: no challenge (default).
- `managed`: issue a challenge when local signals warrant it (for example a
  rate-limit threshold or a first visit without clearance); never describe it
  as an invisible global risk score.
- `always`: challenge every new visitor; use for a deliberate “under attack”
  mode, not as a default.

Global defaults may exist for difficulty and TTL, but a route must opt in.

## Pipeline placement and exemptions

The stage belongs to `shield`, after `ratelimit` and before `identify`:

```text
trust -> ratelimit -> shield(challenge) -> identify -> authorize -> route -> forward
```

- Challenge only document requests (`Accept: text/html`) or explicit entry
  paths. Static assets, health checks, `/.well-known/*`, the OIDC callback and
  logout must be handled deliberately; the callback in particular must not
  lose its query or state.
- APIs receive a JSON `403` with a challenge endpoint and `Retry-After`; they
  are not forced through a browser page.
- CORS preflight, `OPTIONS`, and non-idempotent POST bodies need explicit
  treatment. Prefer challenging a GET entry page and letting the clearance
  cookie cover subsequent requests; do not buffer large request bodies just to
  issue a challenge.
- The admin listener stays loopback-only and is never challenged by the public
  path.

## Events, metrics and audit

Emit stable events:

- `shield.challenge_issued`
- `shield.challenge_passed`
- `shield.challenge_failed` with a code (`expired`, `bad_signature`,
  `replayed_nonce`, `wrong_host`, `insufficient_work`, `rate_limited`)
- `shield.challenge_exempt` with the reason

Never log the nonce, token, cookie or signing key. Metrics: issuance, pass
rate, solve time, difficulty, failures by code, fallback usage. If audit is
enabled, challenge outcomes for protected routes go through the durable audit
contract, not only the in-memory Broker.

## Failure semantics

- If the challenge subsystem cannot initialize, `always` routes must fail
  closed; `managed` routes need an explicit decision (fail open with an alert
  or fail closed) recorded in config and docs.
- If the nonce store is full, stop issuing new challenges and apply the same
  route policy; do not evict an unexpired nonce silently.
- If the signer key is missing or unreadable, fail startup for routes that
  require a challenge.
- Do not let a challenge failure turn into an open redirect: validate the
  return URL against the route host, as the OIDC gate already does.

## Accessibility and usability

- No-JS fallback: an operator-managed allowlist or a manual approval path.
  Do not pretend a CSS-only puzzle is accessible.
- Time budget and a clear error message; allow retry.
- Keyboard-only operation, screen-reader labels, focus management and
  `prefers-reduced-motion` respect.
- Document that low-power devices may take longer; do not set difficulty so
  high that a phone cannot finish.
- Test with axe/Lighthouse and at least one screen reader; record the results.

## Tests and acceptance

- Unit: token signing/verification, host and action binding, expiry, replay,
  difficulty validation, nonce capacity, cookie attributes.
- Integration: browser pass, no-JS fallback, curl/headless rejection, wrong
  host, tampered token, expired token, repeated solution, rate-limit pressure.
- Load: issuance and verification under concurrency; CPU and memory bounds;
  a PoW difficulty sweep on a low-power device.
- Accessibility: keyboard, screen reader, reduced motion, fallback.
- Operations: log-only mode first; measure false positives and solve times
  before enabling blocking on a real route.
- Audit: challenge events appear in the activity feed and, when enabled, in
  the durable audit log with no secrets.

## Phases

1. **Design and threat model** (this document): accessibility, failure modes,
   config shape, claim wording.
2. **Core**: signed challenge, nonce store, clearance cookie, per-route config,
   pipeline stage, events, tests. No polished UI.
3. **UI**: embedded page, WebCrypto worker, adaptive difficulty, metrics,
   browser E2E.
4. **Under attack**: operator-controlled `always` mode with a documented
   rollback.
5. **Optional later**: a `siteverify`-style API for apps outside Switchboard.
   Separate product decision; not part of `SHD-003`.

## Prior art

Cloudflare Turnstile (managed, global signals), Cap (open-source PoW CAPTCHA),
mCaptcha (self-hosted PoW), Anubis (PoW challenge for AI crawlers). Study their
token/replay handling, accessibility and UX; do not copy a widget and call it
equivalent.

## Release wording

- Do not call it “Turnstile” or “CAPTCHA” in user-facing claims.
- Call it a **proof-of-work challenge** or **Shield challenge**.
- Start in log-only mode; only claim “filters unsophisticated automation” after
  measured results. Never claim human verification or DDoS protection.

## Open questions

- Which routes opt in first: login endpoints, public marketing pages, or a
  disposable test route?
- Is the nonce store in memory or SQLite for restart behavior?
- What is the fail-open/fail-closed default for `managed`?
- Do we challenge POSTs or only GET entry pages?
- What is the accessibility fallback the owner can operate without support?
- Is a separate `siteverify` API ever worth the support surface?
