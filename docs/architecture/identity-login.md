# Native login: passkeys, sessions and step-up

**Status:** accepted 2026-10-21 (the owner approved every recommendation in
the table below). Phase 1 is implemented; see "Phase 1 as built". Tracker:
`ID-003` (passkeys), `ID-005` (phase 2); later phases touch `ID-004`,
`NTF-001` and `ID-001`. Not a release promise.

Related: [identity integration](identity-integration.md) (`ARCH-009`, how an
application trusts the gateway), [design](design.md) (the `identify` stage),
[tunnel visitor contract](tunnel-persistent.md), [stack](stack.md)
(`go-webauthn/webauthn`).

## What this decides

How a visitor proves who they are **to Switchboard itself**, without Google or
another external provider, and how that proof is kept safe for the rest of the
session. It does not decide how an application trusts the result (that is
`ARCH-009`) and it does not make Switchboard an OIDC provider for other apps
(that stays `ID-004`, later).

**Tension to resolve first.** `ARCH-009` lists "a user directory" as a
non-goal. Native login needs a minimal one: users, their passkeys, nothing
else (no passwords, no profiles, no self sign-up). Approving this document
narrows that non-goal to "no general-purpose directory or IdP"; rejecting it
keeps Switchboard on external providers only.

## Principles

1. **Phishing-resistant by construction:** passkeys only. No passwords, SMS
   or TOTP. Email codes, if ever, only for onboarding (`ID-001`).
2. **Nothing secret at rest:** the database holds public keys and hashes of
   session IDs and invitation tokens. A stolen `state.db` logs nobody in.
3. **The login is not the end:** the session is the next target (cookie
   theft), so it is short, rotated, re-proven for sensitive routes and, where
   the browser supports it, bound to the device (DBSC).
4. **The operator's shell is the root of trust:** recovery happens only
   through the CLI on the host (`switchboard user invite -recover`). Whoever
   has that shell already owns the deployment; no weaker path (email,
   support questions, the admin bearer) can recover an account. Since
   `CLI-001` (2026-10-22) "on the host" means the admin unix socket
   (`admin.socket`, mode `0600`): the CLI sends the request there, so the
   recovery is applied by the running process and published as
   `user.invited` with code `recover` and actor `local`. Offline `-db` stays
   as break-glass and prints the event on stderr. Inviting a new
   person is also possible from the admin console since `UI-003`: it creates
   an account, it cannot take one over. The admin API refuses `recover`
   (2026-10-21): its bearer also travels to the machines that publish sites.
5. **Every decision is explainable and audited:** login, refusal, new
   passkey, step-up and revocation are events, without secrets.

## Model

```
users        id (random, stable)  · name · email (operator-asserted) · groups
             · disabled_at · created_at
passkeys     credential_id · user_id · public_key (COSE) · sign_count
             · aaguid · transports · backup_eligible · backed_up
             · created_at · last_used_at · revoked_at · first_seen_pending
invitations  token_hash · user_id · purpose (enrol|recover) · expires_at
             · used_at
sessions     (existing table, new columns) auth_time · amr · device_bound
             · user_verified · device_first_seen (nullable; unknown for
               older/OIDC sessions)
```

`first_seen_pending` is set only for passkeys added after migration 009. The
first successful assertion consumes it in the same SQLite update that records
the credential, so concurrent logins cannot both claim to be first. Older
credentials migrate as already known; the raw credential ID is not copied into
session context or decision events. The enrolment session records the new
credential as first-seen, while later sessions record false.

- **Subject.** A passkey user is `sb:<user id>`. Upstream providers keep their
  own namespaces (`oidc:<base64url(issuer)>.<base64url(sub)>` since 2026-10-26;
  later `atproto:<did>`), so a policy
  can never confuse two people with the same email at different providers.
- **Email** is asserted by the operator at invitation time and marked
  verified on that basis; existing `allowed_emails` policies keep working.
- **RP ID** (the domain a passkey belongs to) is the parent domain,
  `octaviosantacruz.com`, so one passkey works on every protected subdomain.
  The login pages live on one host, `auth.octaviosantacruz.com`, and the
  session cookie uses the existing `cookie_domain`.

Migration 012 ends legacy OIDC sessions, which held only raw `sub`, rather
than guessing their issuer. It preserves native credentials and sessions that record the passkey method;
legacy sessions without a method also require another login.
Both gates check the login method; OIDC sessions must match the configured
issuer-qualified namespace. Accounts are never linked by matching email.
Native membership changes end previous sessions transactionally, and the gate
refreshes authoritative membership on every request. See the current
[operator/visitor guide](../operations/first-application.md).

## Flow 1 — first enrolment (and recovery)

```
Operator (host shell)          Switchboard                    Visitor's browser
  │ switchboard user invite      │                               │
  │   --email ana@… --group ops  │                               │
  │─────────────────────────────▶│ create user (if new)          │
  │                              │ token = 32 random bytes       │
  │                              │ store SHA-256(token), 15 min  │
  │◀───── https://auth…/enrol#<token> (printed once) ───────────│
  │  sends the link to Ana out of band                           │
  │                                                              │
  │                              │◀──── GET /_/auth/enrol ───────│ (page only; the
  │                              │                               │  token is in the
  │                              │                               │  #fragment, never
  │                              │                               │  sent by previews)
  │                              │◀──── POST /_/auth/enrol/begin │ {token}
  │                              │ token valid, unused, unexpired│
  │                              │ challenge (32 B, single use,  │
  │                              │ 5 min, bound to this browser) │
  │                              │──── creation options ────────▶│ navigator.credentials
  │                              │                               │ .create(): Face ID /
  │                              │                               │ fingerprint / PIN
  │                              │◀──── POST …/enrol/finish ─────│ attestation response
  │                              │ verify (see checks below)     │
  │                              │ store public key, mark token  │
  │                              │ used, start session           │
  │                              │ event passkey.registered ─────┼──▶ Telegram alert
```

- The token travels in the URL **fragment** and is redeemed by a POST, so a
  link preview (WhatsApp, Slack) that fetches the URL cannot consume it.
- Registration options: discoverable credential required, user verification
  required, attestation `none` (synced passkeys are not attested anyway).
- **Recovery** is the same flow with `--recover`: a new passkey is added; old
  ones stay until the operator revokes them (`switchboard user passkeys`,
  `… revoke`).
- **More passkeys** (a second device, a YubiKey) can be added from an
  existing session after a fresh step-up (Flow 3), never from an old session.
- Every new passkey sends an alert. A passkey you did not add is the one
  signal that an enrolment link or a session was stolen.

## Flow 2 — login

```
Browser                          Switchboard (edge)
  │── GET https://app…/ ────────▶│ no valid session
  │◀─ 302 auth…/login?rd=<id> ───│ rd: server-side record of the requested
  │                              │ URL (allowed hosts only), not a raw URL
  │── GET /_/auth/login ────────▶│ page with passkey autofill + button
  │── POST …/passkey/begin ─────▶│ challenge (32 B, single use, 5 min),
  │                              │ stored with this pre-login cookie
  │◀─ request options ───────────│ rpId, userVerification=required,
  │                              │ allowCredentials=[] (discoverable)
  │  navigator.credentials.get(): the device shows "octaviosantacruz.com"
  │  passkeys only; biometric/PIN stays on the device; it signs
  │  challenge + origin
  │── POST …/passkey/finish ────▶│ checks:
  │                              │  · challenge known, unexpired, unused
  │                              │  · origin is an allowed https origin
  │                              │  · rpIdHash matches; flags UP and UV set
  │                              │  · signature valid for the stored key
  │                              │  · credential and user not revoked/disabled
  │                              │  · sign count increases (if non-zero);
  │                              │    otherwise refuse + alert (cloned key)
  │                              │ new session: 32 random bytes; store
  │                              │ SHA-256; record auth_time, amr,
  │                              │ credential, device_bound (BE=0)
  │◀─ Set-Cookie sb_session; 302 to rd ─│ event auth.login
  │── GET https://app…/ ────────▶│ session → Principal → Authorize → forward
```

- No username field, so nothing to enumerate: the browser offers the
  passkeys it has for this domain.
- `begin`/`finish` require `POST`, a JSON body and a matching `Origin`; the
  existing strict rate limit on `/_/auth/*` applies.
- Failures return one generic message; the event records the reason.
- `amr` follows RFC 8176: `["hwk","user"]` for a device-bound passkey,
  `["swk","user"]` for a synced one (the authenticator's backup flags).

## Flow 3 — step-up and route policy

A route's `access` gains two optional fields:

```json
"access": {
  "allowed_emails": ["ana@example.com"],
  "max_auth_age_minutes": 15,
  "require_device_bound": true
}
```

- `max_auth_age_minutes` (built 2026-10-21; named like the other minute
  settings): if the session's last passkey proof is older, the visitor
  repeats Flow 2; the new login replaces the session, so its ID **rotates**
  and `auth_time` is the new proof's. Any enrolled passkey of any user can
  answer: the result is that user's session, re-authorized from scratch, so
  restricting `allowCredentials` to the previous user adds nothing. The
  proxy asks only after the policy allowed, and only with `auth.passkeys`.
  The admin console is a separate loopback listener with its own token and
  does not use it yet.
- `require_device_bound` (built 2026-10-21): only a session whose passkey's
  authenticator did not set the backup-eligible flag (a YubiKey, a
  device-bound platform key) satisfies it; the flag is read at enrolment and
  at every login and stored with the session (migration 007). A synced
  passkey is sent to the login page with `reauth=device`, which says which
  passkey is needed.
- A refusal says *why* (`policy explain` already exists) without revealing
  who else is allowed.

## Sessions

| Setting | Proposed default | Why |
|---|---|---|
| Idle timeout | 12 h | A working day without re-login |
| Absolute lifetime | 7 days | Bounds a stolen cookie even if it is used |
| Rotation | On login, step-up, privilege change | A pre-login cookie never becomes a session |
| Cookie | `sb_session`, `Secure`, `HttpOnly`, `SameSite=Lax`, `cookie_domain` | Existing behavior; never forwarded upstream |
| Revocation | Disabling a user or revoking a passkey deletes its sessions at once | Not "when they expire" |
| DBSC (phase 2) | Offered to browsers that support it (Chrome on Windows today) | A copied cookie without the device key stops working within minutes |

DBSC is opportunistic: browsers without it keep a normal session. A route can
later require it, accepting that Firefox/Safari users cannot reach that route.

## Threats

| Threat | Defense in this design | Residual |
|---|---|---|
| Phishing page | The browser signs only for the real origin; a look-alike domain gets no passkey | None for passkeys; social engineering of the operator is out of band |
| Stolen database | Public keys and SHA-256 hashes only | Metadata (names, emails) is exposed |
| Stolen session cookie (infostealer) | Short lifetime, step-up on sensitive routes, DBSC where supported, revocation | Without DBSC a cookie is usable until it expires or is revoked |
| Replay of a login response | Single-use, short-lived challenge bound to the pre-login cookie | — |
| Cloned authenticator | Sign-count check + alert | Synced passkeys report 0 and cannot be checked this way |
| Intercepted enrolment link | 15 min, single use, fragment + POST, alert on every new passkey | A link stolen inside 15 min is a real risk: send it over a channel you trust |
| Login CSRF / open redirect | POST + Origin check; `rd` is a server-side record limited to configured hosts | — |
| Account recovery abuse | Only the operator's shell can recover | Losing every device and the host means rebuilding the user |
| Brute force / challenge flooding | Existing `/_/auth/*` rate limit; capped challenge store | — |
| Compromised host | Out of scope: root on the host owns everything | Stated explicitly |

## Phases

1. **Passkeys** (`ID-003`): model, CLI invite/recover/revoke, Flows 1–2,
   session columns, events and alerts, then the private acceptance of
   `TUN-002` with a protected connector route and no external provider.
2. **Policy and session hardening:** `max_auth_age`, `require_device_bound`,
   rotation, DBSC.
3. **Approvals** (`NTF-001`): Telegram number matching for admin changes and
   break-glass, always after a passkey session, never instead of one.
4. **Provider** (`ID-004`): Switchboard as OIDC provider with `acr`/`amr`,
   PKCE and DPoP-bound tokens; device flow for `sb login`.
5. **Upstreams and signals:** keep Google/OIDC; add atproto login; CAEP/SSF
   to revoke sessions on external signals.

## Decisions for the owner

| # | Decision | Recommended |
|---|---|---|
| 1 | Native user directory (narrow `ARCH-009`'s non-goal) | Yes, minimal: users + passkeys only |
| 2 | Who can join | Operator invitation only; no self sign-up |
| 3 | Synced passkeys | Allowed by default; device-bound required per route |
| 4 | Recovery | Host CLI only (admin unix socket since `CLI-001`) |
| 5 | Session lifetimes | 12 h idle, 7 days absolute, 15 min step-up for admin |
| 6 | RP ID and login host | `octaviosantacruz.com`, `auth.octaviosantacruz.com` |
| 7 | Alert on new passkey | Yes, Telegram when configured, always an event |
| 8 | DBSC | Phase 2, opportunistic |

## Phase 1 as built (2026-10-21)

Code: `internal/passkey` (gate, ceremonies, pages), `internal/store/sqlite`
(`identity.go`, migration `004_create_passkey_users`), `switchboard user`,
config `auth.passkeys`. Where it differs from the text above:

- **Return address.** The login keeps the existing `return_to` parameter,
  checked against the configured hosts (`auth.SafeReturnTo`, shared with the
  OIDC gate), instead of a server-side record. Same protection against open
  redirects, less state.
- **Session lifetime.** Phase 1 used the existing absolute lifetime
  (`session_ttl_minutes`, 8 h by default); `ID-005` replaced it on
  2026-10-21 (see the journey below). The 12 h idle / 7 day absolute
  split, step-up and the session columns (`auth_time`, `amr`,
  `device_bound`) move to phase 2 (`ID-005`), where the policies that read
  them are built.
- **One method at a time** in phase 1; `ID-007` (2026-10-21) allows an OIDC
  provider beside passkeys, with its callback on the login host and
  `access.methods` per route. `auth.passkeys` requires `-db`: users, passkeys and sessions share one
  database, so revoking a passkey and ending its sessions is one
  transaction.
- **Revocation** deletes the user's sessions, and every authenticated
  request also checks that the user is still enabled.
- **Ceremonies** (challenges) live in memory, single use, 5 minutes, capped
  at 10 000; a restart costs a visitor one retry.

## Acceptance (phase 1)

- An invited user enrols with a passkey; the link cannot be reused, works
  only within 15 minutes and survives a link preview unused.
- Login works with autofill and with the button; a look-alike origin, a
  replayed response, a revoked passkey and a disabled user are refused.
- `state.db` contains no secret that logs anyone in.
- Revoking a passkey ends its sessions immediately.
- A protected connector route reaches a homelab app with the identity
  headers of the passkey user (`TUN-002` acceptance), with no external IdP.
- Every step appears in the activity feed; no token, challenge or cookie is
  logged.

## The whole journey, today and planned (2026-10-21)

After the owner's iPhone acceptance. Phase 1 is live on the edge; the rest is
planned and keeps the same shape.

**Today (`ID-003`):**

1. The operator runs `user invite -email … -group … -return-to <protected URL>`
   on the host: it creates the user and a one-time token (only its hash is
   stored) and prints `…/_/auth/enrol?return_to=…#TOKEN`.
2. The operator sends the link by hand.
3. The visitor opens it. The token is in the fragment, so no server or link
   preview sees it, and nothing is consumed until the button is pressed.
4. *Create passkey* → the device makes a key pair behind Face ID/PIN; the edge
   checks the token (unused, within 15 minutes), stores only the public key,
   burns the token and starts a session.
5. The page returns to `return_to` if it is a protected host, otherwise it
   lists the protected sites as explicit links.
6. Later: no session → the login page; the visitor picks the passkey; the
   edge verifies the signature (this origin, user verified, no replay) and
   the app receives `X-Auth-Request-User`/`-Email`.

Weak points left on purpose for later phases: step 2 is manual, and in phase 1
the cookie had no expiry, so it ended with the browser session. `ID-005`
(first part, 2026-10-21) gives it `Max-Age` and ends sessions at the earlier
of 12 h without use and 7 days after login.

**Planned:**

- `ID-005` — sessions of 12 h idle / 7 days absolute (built), step-up (a
  fresh passkey proof) only for routes that ask for it (next).
- `ID-002` + `ID-001` — the invitation is emailed; the email carries a code
  (primary, finishes in the tab the visitor started in) and a link
  (fallback). Email bootstraps a passkey once; it never signs in on its own
  afterwards and never recovers an account.
- `ID-007` — the login page offers every configured method (passkey, an
  upstream OIDC provider such as Google or Okta) and never picks one on the
  visitor's behalf; a route can require a passkey.
- `ID-008` — from a session with a recent passkey proof, add or revoke one's
  own devices; every addition alerts.
- Recovery after losing every device stays `user invite -recover` on the host.

## What `ID-004` (provider) is, and what it is not

Being an OIDC provider means applications that speak OIDC (Grafana, Gitea, a
mobile app, the `sb` CLI) can offer "Sign in with Switchboard" instead of
trusting the proxy's headers. The protocol part is well specified and
bounded: discovery, JWKS with key rotation, authorization code + PKCE, token
and userinfo endpoints, `acr`/`amr` claims, and the device flow for CLIs.
Behind that endpoint the user still signs in with a passkey, so the tokens
are as phishing-resistant as the login.

What makes a product like Okta large is not OIDC: it is the catalog of
pre-built integrations (OIDC, SAML, SCIM provisioning), lifecycle management,
a risk engine, global availability and certifications, and an authenticator
app (push, device trust). Switchboard does not need its own phone app for the
phishing-resistant part: the platform passkey (Apple Passwords, Google
Password Manager, a security key) is the authenticator, and approvals for
sensitive actions can use the existing Telegram notifier (`NTF-001`). TOTP
apps (Google Authenticator) stay out of the login: their codes can be phished
through a proxy page, which is the attack passkeys stop.

## The portal (`ID-012`)

`/_/auth/me` is the page a person who uses the applications lands on after
signing in with no other destination. It states who is signed in and lists the
applications their identity may open. The list is not stored: each request
evaluates every protected route of the installed configuration with
`auth.Explain`, the decision the proxy makes, so a policy change shows at once
and a refused route is never named. It shows no policy, no group of anyone else
and no route that is not theirs. A route whose policy can ask the person to
sign in again (max auth age, device-bound passkey, allowed methods) carries a
note saying so. Like every login API, its data endpoint is a POST that checks
the `Origin`.
