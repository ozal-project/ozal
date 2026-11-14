# Native login: passkeys, sessions and step-up

Related: [identity integration](../engineering.md#identity-and-applications) (how an
application trusts the gateway), [design](design.md) (the `identify` stage),
[tunnel visitor contract](../engineering.md#tunnels), [stack](stack.md)
(`go-webauthn/webauthn`).

## What this decides

How a visitor proves who they are **to Ozal itself** with a native passkey and
how that proof is kept safe for the rest of the session. An installation can
also use a configured upstream OIDC provider. This document does not decide
how an application trusts the result (see the
identity integration contract), or define Ozal as an OIDC issuer (see
[the provider boundary](#oidc-provider-boundary)).

## Principles

1. **Phishing-resistant by construction:** passkeys only. No passwords, SMS
   or TOTP. Email codes, if ever, only for onboarding.
2. **Nothing secret at rest:** the database holds public keys and hashes of
   session IDs and invitation tokens. A stolen `state.db` logs nobody in.
3. **The login is not the end:** the session is the next target (cookie
   theft), so it is short, rotated and re-proven for sensitive routes.
4. **The operator's shell is the root of trust:** recovery happens only
   through the CLI on the host (`ozal user invite -recover`). Whoever has that
   shell already owns the deployment; no weaker path (email, support questions,
   the admin bearer) can recover an account. "On the host" means the admin unix
   socket (`admin.socket`, mode `0600`): the CLI sends the request there, so the
   running process applies the recovery and publishes `user.invited` with code
   `recover` and actor `local`. The offline `-db` path stays as break-glass and
   prints the event on stderr. An administrator can invite a new person from
   the console: that creates an account and cannot take one over. The admin API
   refuses `recover`, because its bearer also travels to the machines that
   publish sites.
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
  own namespaces (`oidc:<base64url(issuer)>.<base64url(sub)>`; a future
  `atproto:<did>`), so a policy can never confuse two people who share an email
  address at different providers.
- **Email** is asserted by the operator at invitation time and marked
  verified on that basis; existing `allowed_emails` policies keep working.
- **RP ID** (the domain a passkey belongs to) is the parent domain, so one
  passkey works on every protected subdomain. The login pages live on one
  host, and the session cookie uses the configured `cookie_domain`. Protected
  hosts outside it get host-only sessions from that login
  ([host sessions](host-sessions.md)).

Migration 012 ends legacy OIDC sessions, which held only a raw `sub`, rather
than guessing their issuer. It preserves native credentials and sessions that
record the passkey method; legacy sessions without a method also require another
login. Both gates check the login method; OIDC sessions must match the configured
issuer-qualified namespace. Accounts are never linked by matching email.
Native membership changes end previous sessions transactionally, and the gate
refreshes authoritative membership on every request. See the current
[operator and visitor guide](../operations/first-application.md).

## Flow 1 — first enrolment (and recovery)

```
Operator (host shell)          Ozal                           Visitor's browser
  │ ozal user invite             │                               │
  │   --email ana@… --group ops  │                               │
  │─────────────────────────────▶│ create user (if new)          │
  │                              │ token = 32 random bytes       │
  │                              │ store SHA-256(token), 15 min  │
  │◀───── https://auth…/enrol#<token> (printed once) ───────────│
  │  sends the link to Ana out of band                           │
  │                              │                               │
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
- **Recovery** is the same flow with `-recover`: completing it replaces the
  passkeys, in one transaction with the new one the old ones are revoked,
  every session ends and other recovery links are retired. It is an explicit
  alternative authority: whoever runs it on the host can replace the user's
  credentials. Adding a second device without losing the first is Flow 3.
- **More passkeys** (a second device, a YubiKey) can be added from an existing
  session after a fresh step-up (Flow 3), never from an old session.
- Every new passkey sends an alert. A passkey you did not add is the one
  signal that an enrolment link or a session was stolen.

### Console administrators: first setup and access requests

Administrators of the console are a separate directory from visitors. The
sign-in page shows one primary action for the state of the installation:

- **No administrator yet:** a numbered setup. The first administrator is created
  with a one-use link that only the host's shell or the installer output can
  read (`console enrol`), or with `admin.login_host` a code that only the
  host's terminal shows and the person types on the login host, so nobody can
  get ahead of the operator. Once any
  administrator exists the setup path is closed and `bootstrap` is refused.
- **Administrators exist:** passkey sign-in. A host login stays as a secondary
  path for whoever runs the server.
- **Access requests (optional, off by default, stored as `admin_settings`):**
  an administrator with the administrators tool can turn on a form that lets a
  person ask for access. The public endpoints accept only same-origin posts,
  validate name and email, and are bounded (at most 10 requests per 10 minutes
  and 32 waiting). A request is held in memory and grants nothing. An email that
  already is an administrator gets the same answer as any other, so the form
  cannot be used to learn who is one. Approval is an operator-session action
  only (not the admin token or the host socket), needs a passkey proof from the
  last five minutes, and cannot grant a tool the approver's session lacks. It
  creates the same one-use `invite` enrolment a manual invitation does, but
  shortens it to 15 minutes and hands it only to the requester's browser, which
  holds a random secret created with the request (only its hash is kept).
  Turning the switch off, a restart, a denial or expiry end the request.
  Every step emits an event (`admin.access_requested`, `admin.access_approved`,
  `admin.access_denied`, `admin.access_requests_on|off`).

## Flow 2 — login

```
Browser                          Ozal (edge)
  │── GET https://app…/ ────────▶│ no valid session
  │◀─ 302 auth…/login?rd=<id> ───│ rd: server-side record of the requested
  │                              │ URL (allowed hosts only), not a raw URL
  │── GET /_/auth/login ────────▶│ page with passkey autofill + button
  │── POST …/passkey/begin ─────▶│ challenge (32 B, single use, 5 min),
  │                              │ stored with this pre-login cookie
  │◀─ request options ───────────│ rpId, userVerification=required,
  │                              │ allowCredentials=[] (discoverable)
  │  navigator.credentials.get(): the device shows the passkeys for
  │  the RP domain only; biometric/PIN stays on the device; it signs
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

- `max_auth_age_minutes`: if the session's last passkey proof is older, the
  visitor repeats Flow 2. The new login replaces the session, so its ID
  **rotates** and `auth_time` is the new proof's. Any enrolled passkey of any
  user can answer: the result is that user's session, re-authorized from
  scratch, so restricting `allowCredentials` to the previous user adds nothing.
  The proxy asks only after the policy allowed the request, and only with
  `auth.passkeys`. The admin console is a separate loopback listener with its
  own token and does not use it yet.
- `require_device_bound`: only a session whose passkey's authenticator did not
  set the backup-eligible flag (a YubiKey, a device-bound platform key)
  satisfies it. The flag is read at enrolment and at every login and stored
  with the session. A synced passkey is sent to the login page with
  `reauth=device`, which says which passkey is needed.
- A refusal says *why* (`policy explain` shows it) without revealing who else
  is allowed.

## Sessions

| Setting | Default | Why |
|---|---|---|
| Idle timeout | 12 h | A working day without re-login |
| Absolute lifetime | 7 days | Bounds a stolen cookie even if it is used |
| Rotation | On login, step-up, privilege change | A pre-login cookie never becomes a session |
| Cookie | `sb_session`, `Secure`, `HttpOnly`, `SameSite=Lax`, `cookie_domain` | Never forwarded upstream |
| Revocation | Disabling a user or revoking a passkey deletes its sessions at once | Not "when they expire" |

## Threats

| Threat | Defense in this design | Residual |
|---|---|---|
| Phishing page | The browser signs only for the real origin; a look-alike domain gets no passkey | None for passkeys; social engineering of the operator is out of band |
| Stolen database | Public keys and SHA-256 hashes only | Metadata (names, emails) is exposed |
| Stolen session cookie (infostealer) | Short lifetime, step-up on sensitive routes, revocation | A cookie remains usable until it expires or is revoked |
| Replay of a login response | Single-use, short-lived challenge bound to the pre-login cookie | — |
| Cloned authenticator | Sign-count check + alert | Synced passkeys report 0 and cannot be checked this way |
| Intercepted enrolment link | 15 min, single use, fragment + POST, alert on every new passkey | A link stolen inside 15 min is a real risk: send it over a channel you trust |
| Login CSRF / open redirect | POST + Origin check; `return_to` is checked against configured hosts | — |
| Account recovery abuse | Only the operator's shell can recover | Losing every device and the host means rebuilding the user |
| Brute force / challenge flooding | Existing `/_/auth/*` rate limit; capped challenge store | — |
| Compromised host | Out of scope: root on the host owns everything | Stated explicitly |

## Identity boundaries

Ozal authenticates a person with a passkey or a configured upstream OIDC
provider. Routes can constrain accepted methods through `access.methods`.
Applications trust identity headers only on requests that arrive through the
Ozal proxy. This contract covers authentication to Ozal and its sessions; an
application-facing OIDC issuer has a separate protocol and trust boundary.

## Implementation details

Code: `internal/passkey` (gate, ceremonies, pages), `internal/store/sqlite`
(`identity.go`, migrations `004_create_passkey_users` through
`016_passkey_label_and_session_client`), `ozal user`, config `auth.passkeys`. Where it
differs from the text above:

- **Return address.** The login keeps the existing `return_to` parameter,
  checked against the configured hosts (`auth.SafeReturnTo`, shared with the
  OIDC gate), instead of a server-side record. Same protection against open
  redirects, less state.
- **Session lifetime.** `session_ttl_minutes` defaults to 10080 (7 days
  absolute) and `session_idle_minutes` to 720 (12 hours without use). Step-up
  and the session columns (`auth_time`, `amr`, `device_bound`, `user_verified`,
  `device_first_seen`) are in migrations 006–009, and the policies that read
  them are implemented.
- **Methods.** OIDC can run beside passkeys; its callback lives on the login
  host and each route sets `access.methods`. `auth.passkeys` requires `-db`:
  users, passkeys and sessions share one database, so revoking a passkey and
  ending its sessions is one transaction.
- **Devices.** A signed-in person can list, add, rename and revoke their own
  passkeys from `/_/auth/devices`; adding one alerts through the configured
  notifier. Each passkey is named after the browser it was made in until its
  owner renames it, and the one that signed the current session in is marked.
  Each session records the browser it came from and the passkey that signed
  it (migration 016). These are hints for the owner, never policy inputs.
- **Two accounts, one email.** A visitor and a console administrator with the
  same email are separate accounts with separate passkeys and sessions; Ozal
  never merges them. The portal tells the visitor when their email is also an
  administrator, and People shows the link, with the console account's
  details only to callers who hold the administrators tool.
- **Revocation** deletes the user's sessions, and every authenticated request
  also checks that the user is still enabled.
- **Ceremonies** (challenges) live in memory, single use, 5 minutes, capped at
  10 000; a restart costs a visitor one retry.

## Security invariants

- An invited user enrols with a passkey; the link cannot be reused, works only
  within 15 minutes and survives a link preview unused.
- Login works with autofill and with the button; a look-alike origin, a
  replayed response, a revoked passkey and a disabled user are refused.
- `state.db` contains no secret that logs anyone in.
- Revoking a passkey ends its sessions immediately.
- A protected connector route reaches an internal app with the identity
  headers of the passkey user, with no external IdP.
- Every step appears in the activity feed; no token, challenge or cookie is
  logged.

## Enrollment and authentication flow

1. An operator creates an invitation. Ozal stores only a hash of its one-time
   token and places the token in the URL fragment, where servers and link
   previews do not receive it.
2. The visitor starts enrollment. The edge checks that the invitation is unused
   and within 15 minutes, stores the passkey's public key, consumes the token
   and starts a session.
3. The visitor returns to the configured `return_to` host, or sees explicit
   links to protected applications.
4. At sign-in, the visitor chooses a configured authentication method. The edge
   verifies a passkey response for the current origin, rejects replays, and
   provides verified identity to the proxied application.
5. A route that requires recent proof triggers step-up. `max_auth_age_minutes`
   and `require_device_bound` are evaluated by `auth.Fresh`.
6. A signed-in person can add, rename or revoke their passkeys at
   `/_/auth/devices`.
   Recovery after losing every device is performed by the operator through the
   host CLI and ends the previous sessions.

Session `Max-Age` ends at the earlier of 12 hours without use and 7 days after
login.

## OIDC provider boundary

This contract covers identity proof presented to Ozal. Upstream OIDC providers
can authenticate a person to Ozal, while applications behind the proxy receive
verified identity headers. An OIDC issuer for applications has a separate
protocol, key-management and trust contract; proxy identity headers must not be
treated as a substitute for that protocol on direct application connections.

## The portal

`/_/auth/me` is the page a person who uses the applications lands on after
signing in with no other destination. It states who is signed in and lists the
applications their identity may open. The list is not stored: each request
evaluates every protected route of the installed configuration with
`auth.Explain`, the decision the proxy makes, so a policy change shows at once
and a refused route is never named. It shows no policy, no group of anyone else
and no route that is not theirs. A route whose policy can ask the person to sign
in again (max auth age, device-bound passkey, allowed methods) carries a note
saying so. Like every login API, its data endpoint is a POST that checks the `Origin`.
