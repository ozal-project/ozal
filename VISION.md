# VISION.md — Where Ozal is going

> **Ozal publishes your private applications from a server you control, decides
> who gets in, and shows why.** One Go binary. No open ports at home, no
> passwords, and no SaaS in the access path.
>
> The long-range direction adds verifiable access decisions, more kinds of
> identity and infrastructure access. Only the first part exists today. This
> file keeps the two apart.

This document is the public product direction. Scheduled work lives in
[docs/TO_IMPLEMENT.md](docs/TO_IMPLEMENT.md). When the two disagree, the scheduled
plan wins until this file is updated on purpose. The "Long-range direction" section
is not a promise.

---

## The problem

A small team or a homelab wants its internal tools reachable from anywhere:
dashboards, previews, admin panels, a database console. Today that usually means
one of three bad options:

- open a port and hope the application's own login holds;
- put a SaaS in the access path, with its own accounts, pricing and outages;
- run a reverse proxy and build identity, certificates and audit around it by hand.

Ozal's answer is one binary that owns the front door. It publishes applications
on infrastructure you control, decides who gets in, and explains each decision.

## Who it is for

**The first user is a small team, or one person with a homelab,** who shares
internal tools and previews with a few people they know. They want:

- to publish an app or site on their own domain in minutes, not days;
- to know exactly who can open it, and to remove someone's access at once;
- no passwords to store, leak or reset;
- to understand a refusal without reading source code.

Enterprise PAM and SSE products solve the same problems for large organizations.
Ozal is for people who will never buy them.

## What exists today

Verified against this checkout; see the [README status table](README.md#status)
and the [alpha.1 notes](docs/releases/v0.1.0-alpha.1.md) for what each release
includes.

- A host-based reverse proxy with health checks, HTTPS with automatic certificates,
  and atomic routing snapshots.
- Per-route access: allow people, groups or any signed-in person; step-up with a
  recent passkey; device-bound passkeys.
- Native passkey sign-in with invitations and recovery, or an external OIDC provider.
- Static sites with deployment history and rollback, including publishing from GitHub.
- An explanation of each policy decision, an activity feed, and an optional durable
  audit trail for access and admin changes.
- A console embedded in the binary, private on loopback or a local socket.

Owned tunnels to private connectors exist as an experiment, off by default.

## Principles for product decisions

1. **Your server, your domain, your identity.** No SaaS in the access path. An
   external provider or DNS service can be used, but never required.
2. **Passwordless by default.** Passkeys first; no password storage.
3. **Explain every refusal.** A denied person and their operator should both be
   able to see why, in plain words.
4. **Protect the origin too.** The gateway only helps if applications cannot be
   reached around it.
5. **Make recovery real before adding features.** Revocation, backups and rollback
   are part of the product, not extras.
6. **Claims match evidence.** A feature is "available" only when the current
   checkout and its tests show it. "Verifiable" or "tamper-evident" is not claimed
   until the audit has an external witness.

## Long-range direction

Everything in this section is a direction, not a commitment, and is not in the
current checkout unless the README status table says so.

The aim is to make the access system's own decisions checkable by someone other
than the operator: an append-only, externally witnessed log of certificates, policy
changes and access grants, with signed checkpoints. Today the audit is local and
hash-chained; the verifier reports `tamper_evident=false` because it cannot prove a
checkpoint was kept outside the host. The detailed scope is in
[the audit contract](docs/engineering.md#auditoria).

Later work also covers private certificates for internal services, infrastructure
access with approval and session recording, and AI agents as identities under the
same policy. Security-filter ideas are measured before they ship.

The detailed plan, including its schedule, is maintained privately.

## Landscape

| Project | Strength | Where Ozal differs |
|---|---|---|
| Cloudflare Tunnel + Access | Tunnels, protected quick tunnels, email OTP | Self-hostable identity/policy and owned transport; optional exterior proxy protection |
| Pangolin | Self-hosted tunnels, WireGuard, RBAC | Own CA and transparency log; AI-agent principals |
| Octelium | Broad FOSS ZTNA, VPN, API/AI gateway | Smaller and readable; verifiability as the core thesis |
| Teleport, HashiCorp Boundary | Infra access, session recording | Homelab-to-small-team scale, one binary |
| step-ca | Private ACME CA | CA integrated with identity, policy and a public CT-style log |
| Pomerium, oauth2-proxy | Identity-aware proxy | Includes tunnels, its own identity and PKI |
| MCP gateways (Bifrost, ContextForge…) | Tool access for AI agents | Agents are just another principal under the same policy and audit |
| CyberArk, Netskope | Enterprise PAM / SSE | The same concepts, open source, for everyone else |

Feature-count races are a losing game. What matters is whether the access decision
is simple to read and, over time, checkable.

## Product and deployment direction

Initial offering: publish private applications on infrastructure the operator
controls, with identity, access policies and owned tunnels, while allowing a choice
of DNS and exterior protection providers. Validate setup, revocation, explainable
decisions and recovery before expanding the feature surface.

Prove direct ingress and exterior-proxy ingress separately. Cloudflare can continue
to provide exterior protection while Ozal owns gateway identity and policy. DNS and
ACME adapters keep providers replaceable. A worldwide CDN, volumetric DDoS network
and distributed functions platform are outside the initial offering.

The [positioning and portability contract](docs/engineering.md#publicacion-autonoma)
and the [coexistence decision](docs/security.md#direct-ingress-or-behind-a-proxy)
define the boundaries and the acceptance evidence.

## Out of scope

- **Passwords.** Identity is passwordless or delegated.
- **A publicly trusted CA.** Public names use Let's Encrypt.
- **Being a CDN or absorbing volumetric DDoS.** One VPS cannot; the gateway protects
  applications, not network capacity.
- **Browser isolation and payments.**
- **Autonomous AI decisions.** AI may produce signals and explanations, never an allow.
- **Multi-tenant SaaS with billing.**

## Rules for releases

1. **A release promises only what has evidence.** Release notes record the promise,
   the evidence, the non-goals, rollback and recovery, and residual risks. A claim
   stronger than the evidence ("verifiable", "transparent", "production-ready") is a
   release blocker.
2. **Security findings come first.** A public access platform with known holes is
   worse than none.
3. **Every release ends with a written record** of what it did and did not prove.
4. **Cut scope, not quality.** When a planned item slips, it moves out of the release
   rather than shipping half-tested.
5. **Engineering labs are not release commitments.** Performance, failure drills,
   threat models and conformance work are tracked as `LAB-*` and do not gate a release
   unless the owner promotes one.
6. **Re-read this file periodically** and change it on purpose.

## References

- [Cloudflare — Protected quick tunnels](https://blog.cloudflare.com/protected-quick-tunnels/)
- [Let's Encrypt — 6-day and IP address certificates](https://letsencrypt.org/2026/01/15/6day-and-ip-general-availability)
- [RFC 8555 — ACME](https://www.rfc-editor.org/rfc/rfc8555)
- [Smallstep — private ACME server](https://smallstep.com/blog/private-acme-server/)
- [Filippo Valsorda — You should run a CT log](https://words.filippo.io/run-sunlight/)
- [Filippo Valsorda — Building a transparent keyserver](https://words.filippo.io/keyserver-tlog/)
- [RFC 8693 — OAuth 2.0 Token Exchange](https://www.rfc-editor.org/rfc/rfc8693)
- [OpenPubkey SSH (opkssh)](https://github.com/openpubkey/opkssh)
- [Pangolin](https://github.com/fosrl/pangolin) · [Octelium](https://octelium.com/)
- [C2SP tlog-witness](https://c2sp.org/tlog-witness@v1.0.0) · [tlog-checkpoint](https://c2sp.org/tlog-checkpoint@v1.0.0)
- [Telegram Bot API](https://core.telegram.org/bots/api)
