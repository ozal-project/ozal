# Identity integration with protected applications

**Status:** proposed 2026-10-19. Decision record; no implementation. Tracker:
`ARCH-009` in [TO_IMPLEMENT.md](../TO_IMPLEMENT.md). Not a release promise.

Related: [design pattern](design.md), [OIDC gate](../../internal/auth/oidc.go),
[Cloudflare coexistence](cloudflare-coexistence.md), workspace `CROSS-006`
and `CROSS-010`.

## Context

Switchboard can authenticate a visitor with an external OIDC provider and
authorize the request by email, domain and groups. Applications behind it may
also have their own authentication: SIGEA uses its own JWT flow and Custodia
has local/OIDC modes. Putting both in series can produce two logins, and simply
forwarding traffic does not prove that the gateway's access policy is the thing
that actually protects the application.

The design question is not “how do we pass traffic?” It is: **who is the
authority for the application's identity, and what evidence does the
integration leave?**

## Options

| Option | What it proves | Cost / risk | Use now? |
|---|---|---|---|
| **A. Transport-only** — Switchboard does TLS, routing, health and audit; the app keeps its own auth | Routing/TLS/tunnel; not gateway access control | No double login if the app is the only gate; weak evidence for Switchboard's core | Good for static sites and apps with mature auth, not for the access-core demo |
| **B. Edge gate + app auth** — both authenticate; the upstream IdP session may make the second login silent | Coexistence; the app still decides its own authorization | Two sessions and confusing UX; policy may be redundant; not authoritative evidence | Useful for a first real app; must be labeled coexistence, not SSO |
| **C. Signed identity assertion** — Switchboard authenticates and sends a short-lived signed assertion (JWT/JWKS or mTLS-bound header) that the app verifies | Gateway identity/policy is authoritative; app receives a verifiable principal | App integration; header spoofing if direct access exists; key/audience/replay design; logout/revocation | Target for a disposable test app, then one real app |
| **D. Shared upstream IdP / token exchange** — both use the same IdP or RFC 8693 exchange; DPoP can bind tokens | Federated identity; no new IdP | Standards work; still two authorization decisions; app changes; token lifecycle | Later, if the apps already use the same IdP |
| **E. Switchboard as OIDC provider** — apps “Sign in with Switchboard” | Strongest integration | Becomes an IdP: users, keys, consent, recovery, security liability; separate product | Out of scope for this decision |

## Recommendation

1. **Do not claim SSO or “gateway is the auth authority” until C exists.** B is
   a coexistence demo; it proves transport and policy execution, not that the
   app trusts the gateway's identity.
2. **Start with a disposable test application** that has no auth and is
   reachable only through Switchboard. Use C with a signed assertion. Prove:
   - direct access to the app is blocked;
   - the edge strips client-supplied identity headers;
   - the assertion is audience-bound, short-lived and replay-protected;
   - logout/revocation has a defined effect;
   - the decision and assertion are audited without secrets.
3. **Only then integrate a real app.** Prefer an app whose auth can be
   disabled or put in a trusted-header mode behind a private network. Do not
   silently bypass an app's authorization; the app must still enforce its own
   fine-grained rules.
4. **Keep D/E separate.** Token exchange is a later standards exercise;
   becoming an IdP is a different product.

## Signed assertion shape (option C)

If C is chosen, the first version should be deliberately small:

- JWT signed with Ed25519; public key available to the app through a pinned
  JWKS file or a loopback endpoint, not a public discovery document.
- Claims: `iss` (Switchboard instance), `aud` (the specific app), `sub`
  (stable principal ID), `email`, `email_verified`, `groups`, `iat`, `exp`
  (short, for example 60 seconds), `jti`.
- Transport: a dedicated header such as `X-Switchboard-Identity` carrying the
  JWT, or an `Authorization: Bearer` only if the app has no conflicting bearer
  use. The edge must strip every client-supplied copy before injection.
- The app must verify signature, issuer, audience, expiry and `jti` replay
  window. It must never trust the header from a direct connection.
- The app remains responsible for its own authorization; the assertion conveys
  identity, not a blanket allow.
- Do not forward the Switchboard session cookie upstream. Do not put secrets in
  the JWT beyond the minimum claims.

This makes Switchboard an identity *asserter* for a specific app, not a general
IdP. If that distinction is lost, option E has arrived by accident.

## Threat model

| Risk | Mitigation |
|---|---|
| Client forges identity headers | Strip all identity headers at the edge; test with `-`/`_` spellings and duplicate values |
| App reachable directly, bypassing the edge | Firewall/private bind; app rejects unsigned requests; direct IPv4/IPv6 test |
| Audience confusion (token for app A used on app B) | `aud` check; per-app keys or explicit audience list |
| Replay | Short `exp`, `jti` cache at the app; TLS; no long-lived bearer |
| Key compromise | Rotation, `kid`, short overlap, revocation runbook |
| Logout/revocation | Define whether the app trusts session revocation; short assertion TTL is the backstop |
| Double login / confusing UX | Decide B vs C explicitly; do not mix silently |
| Clock skew | Bounded skew; app and edge both use UTC; test |
| Secret leakage in logs | Never log JWTs, cookies or keys; audit codes, not tokens |

## Acceptance criteria

- A disposable app is reachable only through Switchboard; direct access fails.
- A valid assertion is accepted; a missing, expired, wrong-audience, tampered
  or replayed assertion is rejected.
- Client-supplied identity headers never reach the app.
- Logout/revocation behavior is documented and tested.
- The decision and assertion issuance appear in the activity feed and, when
  enabled, the durable audit log, without tokens.
- The demo states clearly what it proves: transport, gateway identity, app
  authorization, or coexistence.

## Non-goals

- Building a full OIDC provider or user directory (option E). Narrowed
  2026-10-21 by the owner: Switchboard keeps a minimal directory of invited
  users and their passkeys for its own login
  ([identity-login.md](identity-login.md)); a general directory or IdP stays
  out of scope.
- Sharing a session cookie across unrelated apps.
- Replacing application authorization.
- Mixing Custodia/SIGEA data or identities without a contract.
- Claiming SSO before an app actually accepts the gateway's assertion.

## Open questions

- Which disposable app first: a tiny Go test app, Portfolio preview, or a
  SigeaWeb static host?
- JWT header vs `Authorization: Bearer` for the first integration?
- Where does the app get the public key: pinned file, loopback JWKS, or both?
- Does any real app support a trusted-header/auth-proxy mode without code
  changes?
- Is DPoP/token exchange worth a later lab, or is a short JWT enough for the
  target scale?
