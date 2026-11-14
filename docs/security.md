# Security and trust boundaries

Ozal enforces access at the gateway; each application keeps its own internal
authorization. This guide describes the design limits and the deployment
conditions. It is not an ASVS certification or a complete audit of an installation.

## Deployment rules

- The console and the administrative API stay on loopback or a private socket.
  Reach them through a private transport; never publish them as the upstream of a route.
  Only enrolling a passkey by code happens on the public login host: the code is
  shown only in the host's terminal, works once for five minutes, and the terminal
  reports which browser used it.
- Keep private applications on loopback or a controlled network: an alternative
  address that bypasses the gateway can also bypass its policies.
- Use a valid public TLS certificate; do not disable verification to work
  around certificate problems, staging tests or connections to the origin.
- Keep private keys on their host. Configure secrets through `file:`/`env:`
  references and minimal permissions. Never paste them into chats, URLs or repositories.
- Distinguish visitors, CI publishers, connectors and administrators. A valid
  identity in one scope grants no authority in the others.
- Back up and rehearse recovery of keys, configuration and data. An old copy can
  reactivate revoked authorizations. Follow [recovery](operations/administration.md#backup-and-recovery).

## System boundaries

| Boundary | Control | External condition |
|---|---|---|
| Client → edge | Configured hosts, sanitized identity/IP headers, policy and limits | DNS, firewall, capacity and network protection |
| Browser → login | OIDC or WebAuthn validation, state/challenges, sessions and revocation | Correctly configured IdP, RP/domain and authenticators |
| Edge → application | Identity generated after authorization; session cookie removed; optional signed assertion per request | The app trusts only the authorized gateway (or verifies the assertion with a pinned key) and keeps its own permissions |
| Edge → connector | mTLS, enrolled fingerprint, fixed resources and revocation | CA distribution, local keys and permitted transport |
| Operator → administration | Host/socket, or a session with the required tool | Unix permissions, browser security and host access |
| CI → publishing | Dedicated handler, repo/ref/workflow/audience/event and replay protection | Protecting the workflow, its credentials and build artifacts |
| Process → state | Validation, migrations, snapshots and private permissions | Trusted backups, disk, ACLs and host |
| Audit → external evidence | Signed chain and checkpoints | Copy and public key pinned outside host control |

A client cannot create a trusted identity with `X-Auth-Request-*`,
`X-Ozal-Visitor-*` (nor the former `X-Switchboard-Visitor-*`), or variants with
underscores. The tunnel carries assertions authenticated by mTLS; they are not
JWTs that any app can verify. For that, a route can opt into a signed
[identity assertion](reference.md#identity-assertion): a 60-second EdDSA JWS for that
application's audience, which it verifies with a pinned key and a replay check. Keeping an application's own login is coexistence,
not automatic SSO.
See [identity](architecture/identity-login.md) and
[host sessions](architecture/host-sessions.md).

<a id="entrada-directa-o-detras-de-un-proxy"></a>
<a id="direct-ingress-or-behind-a-proxy"></a>

## Direct ingress or behind a proxy

These are different trust profiles. With direct ingress, the IP comes from the
TCP peer. Behind Cloudflare, `trusted_proxies` accepts `CF-Connecting-IP` only
from configured peers and only when the value is unambiguous. Do not trust
visitor ranges or an arbitrary `X-Forwarded-For` chain.

Origin authentication with `tls.origin_client_ca_file` requires the client
certificate of the chosen proxy path. Limiting by IP does not by itself identify
a specific zone. Check direct origin access over IPv4/IPv6 and alternative names.
When migrating to direct ingress, prepare an explicit profile with its own
firewall, TLS and IP; the proxy failing must never open the origin automatically.

DNS and HTTP-01 do not depend intrinsically on Cloudflare. The current DNS-01
automation uses its API. The generic public tunnel and full provider independence
have separate acceptance; a local test does not demonstrate them.

<a id="auditoria-y-privacidad"></a>
<a id="audit-and-privacy"></a>

## Audit and privacy

Activity is a bounded in-memory feed and can lose events; it does not replace the
durable journal. Routes with required audit wait for persistence before forwarding;
a failure must deny the request, not downgrade the mode. An administrative intent
shows an authorized attempt, not a commit of the operation.
See the [technical contract](engineering.md#audit).

A local chain detects inconsistencies, but whoever controls all the files can
rewrite it. Checkpoints only help if they survive outside that authority, together
with the pinned public key. The verifier cannot prove where the copy was kept.
There is no promise of transparency against root or an independent public log.

Email, IP, subject and decisions can be personal data. Limit readers, retention
and copies. Do not log cookies, tokens, bodies, keys or raw provider errors.
TLS and the hash chain do not encrypt files at rest.

<a id="que-hay-que-comprobar-en-cada-instalacion"></a>
<a id="what-to-verify-on-each-installation"></a>

## What to verify on each installation

Test an allowed person and a denied one, forged headers, direct access to the
origin, revocation and reconnection, restart, TLS renewal and restore on a clean
host. A health check or a SQLite integrity check is partial. Browser compatibility,
the real provider and capacity under load need their own rehearsals.

A VPS is not a global CDN and does not absorb attacks that saturate its link.
The host and root remain the authority. High availability, isolation between
independent clients, volumetric DDoS protection and ASVS conformance are not
promised, and unit tests do not establish them.

For development and reproductions, use fixtures, fictitious credentials and
disposable state. Do not test abuse or availability against production. Pending
security and acceptance items are tracked by the maintainer; see the
[work status](TO_IMPLEMENT.md) note.
