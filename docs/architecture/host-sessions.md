# Host sessions behind the central login (ID-015)

Stage C of the [autonomous publication proposal](publicacion-autonoma.md#102-sesiones-por-host-y-login-central).
A private application on a domain the login's cookie cannot reach
(`app.other.org` while the login is `auth.example.com`) gets its own
host-only session, issued after the central login vouches for the visitor.
The passkeys, the users and the decision of who may enter stay where they are:
the installation's login host and the per-request policy.

## Which hosts use it

A protected route's host is a **handoff host** when the passkey login is
configured and the host is neither the login host nor covered by
`auth.cookie_domain`. Every other protected host keeps today's shared cookie.
Installations with only an external OIDC provider keep the `cookie_domain`
requirement; the handoff runs inside the passkey gate (which may also offer
the OIDC button).

## Protocol

```
browser             app.other.org                 auth.example.com
   | GET /docs  ------>| no host session
   |                   | tx = {host, "/docs", H(binding)}
   |<-- 302 + Set-Cookie sb_handoff=binding (host-only, Path=/_/auth/handoff, Lax)
   | GET /_/auth/continue?tx=T  -------------------------->|
   |                   |      central session? fresh enough for the tx?
   |                   |      no  -> 302 /_/auth/login?return_to=continue?tx=T
   |                   |      yes -> take tx, code = {tx, central session}
   |<--------------------------------- 302 app.other.org/_/auth/handoff?code=C
   | GET /_/auth/handoff?code=C + cookie sb_handoff -->|
   |                   | take C; host == C.host; H(cookie) == C.binding
   |                   | child = CreateLinked(central, "app.other.org")
   |<-- 303 /docs + Set-Cookie sb_session=child (host-only)
```

- **Transaction** (`tx`): 256-bit random ID in the URL, kept in memory for
  ten minutes with the host, the path to return to (`RequestURI` on that host,
  never a URL) and the SHA-256 of the browser's binding secret.
- **Binding secret**: 256 bits in a host-only, `HttpOnly`, `Secure`,
  `SameSite=Lax` cookie limited to `/_/auth/handoff`, reused across tabs while
  it lives. It plays the role of PKCE's verifier: the code is worthless
  without it, and only the browser that started the transaction has it.
- **Code**: 256-bit random, single use (consumed by the first attempt,
  successful or not), sixty seconds, bound to the transaction's host and
  binding and to the central session that approved it. It never carries the
  central session or the principal.
- **Host session**: a new row whose principal is copied server side from the
  central session, with `audience = host` and a link to its parent. It
  expires no later than the parent's issued expiry. The cookie is the usual
  `sb_session` with no `Domain`, so it stays on that host.
- **Step-up**: a transaction created for a step-up (`max_auth_age`, device
  bound, method) is only approved by a central login newer than the
  transaction; otherwise `continue` sends the browser to the login page with
  the same reason.

## Checks on every request

- A session is valid only on its audience: a central session (`audience ""`)
  on the login host and `cookie_domain` hosts, a host session only on its own
  host. A cookie copied to another host is refused.
- A host session is valid only while its parent is: the parent's issued and
  idle expiry are checked on each lookup, and using the app counts as using
  the central login (both are touched together).
- The gate's per-request checks still apply: active user, current groups,
  route policy, step-up.

## Ending sessions

| Event | Effect |
|---|---|
| Logout on the login host | Central session and every host session it approved end. |
| Logout on a handoff host (`POST /_/auth/logout`) | The whole login ends: its parent and siblings too. |
| New login on the login host | The previous central session ends with its host sessions (existing rule); apps re-handoff silently on the next visit. |
| User disabled, membership change, passkey removed, "sign out everywhere" | Already delete by subject; host sessions copy it, so they go too. |
| Expiry or sweep | Host sessions whose parent is gone are swept. |

## Threats

| Threat | Control |
|---|---|
| Stolen code (logs, history, shoulder) | Single use, sixty seconds, useless without the binding cookie; `Referrer-Policy: no-referrer` and `no-store` on both redirects. |
| Login CSRF: attacker sends the victim a code from the attacker's own login | The victim's browser lacks the attacker's binding: refused. |
| Attacker lures a signed-in victim to `continue?tx=<attacker's tx>` | The code goes to the victim's browser, which does not hold the attacker's binding and consumes the code on the failed attempt. Residual: an attacker who also reads the victim's URL bar within sixty seconds; that same position can read the session cookie. |
| Open redirect | The app host comes from the stored transaction, created only for a configured handoff host; the return value is a path on that host. |
| Code or cookie replayed on another app | Code checked against the request host; session audience checked on every request. |
| Upstream app reads the session | The proxy strips `sb_session`; the binding cookie's path never reaches the upstream. |
| Restart or gate rebuild mid-login | Transactions and codes live in memory: the visitor retries once; no check is weakened. |

## Migration

No change for hosts covered by `cookie_domain`: their sessions keep
`audience ""` and the same cookie. Migration 015 adds `audience` and
`parent_hash` with defaults that describe existing rows. Passkeys keep their
RP ID; the login host does not move. The `cookie_domain` validation stays for
OIDC-only installations and for the login host itself.

Out of scope: authentication between different installations, Switchboard as
an OIDC provider for apps (`ID-004`), per-app logout that keeps the central
login.
