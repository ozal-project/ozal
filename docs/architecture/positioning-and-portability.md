# Product positioning and provider portability

Owner-directed documentation: 2026-10-20 (America/Asuncion).
This records product direction and acceptance criteria, not a deployment or
a claim that all capabilities below are implemented. Active work stays in
[TO_IMPLEMENT.md](../TO_IMPLEMENT.md), primarily EDGE-006, EDGE-007, TUN-002,
SHD-001 and REL-008. The [coexistence decision](cloudflare-coexistence.md)
defines the exterior-proxy trust boundary.

## Offering and initial user

**Publish private applications on infrastructure you control, with identity,
access policies and owned tunnels, while choosing your DNS and exterior
protection providers.**

The initial user is a small team or operator sharing internal tools, panels
and previews. The useful outcome is one understandable path from a local
application to an authenticated visitor, with revocation and an explanation
of the access decision. Application authorization remains the application's
responsibility. An outbound connector avoids opening inbound application
ports at home; the public edge still requires reachable infrastructure.

The differentiation to validate is reduced setup and operating effort,
provider portability and explainable access decisions. Combining existing
features does not demonstrate that value by itself. The long-range identity,
PKI and transparency thesis in [VISION.md](../../VISION.md) remains a learning
direction; tamper evidence and protection against an administrator require
their own threat model, external witnesses/checkpoints and acceptance tests.

## Capability boundaries

| Capability | What Switchboard can own | External responsibility or limit |
|---|---|---|
| DNS | Integrations with providers; explicit hostname configuration | Authoritative DNS must remain available. A/AAAA/CNAME records locate services; they do not implement a proxy. Prefer existing DNS services/software over building a new authoritative server for the initial offering. |
| Public certificates | ACME issuance, storage, renewal and recovery | A public CA validates control of the domain. The current DNS-01 adapter uses Cloudflare's API; another DNS provider needs an adapter and acceptance. Public HTTPS does not intrinsically require Cloudflare's proxy. |
| Proxy and tunnels | Host routing, access policy, owned outbound transport, reconnect and revocation | Public edge hosting and connectivity remain required. Protected visitor-identity propagation through a connector must meet TUN-002 before it is offered as complete. |
| Application protection | Bounded requests, rate limits, security headers and measured Shield rules | Coverage, evasion and false-positive results apply to the tested corpus/load. No equivalence to all managed WAF or bot products follows from those tests. |
| Volumetric DDoS | Application resource budgets and integration with upstream mitigation | Traffic can saturate a network link before reaching the process. Network capacity, upstream filtering, transit and distributed mitigation require infrastructure and operation beyond one VPS. |
| Static hosting | Serve built output and provide tested deployment/rollback behavior | Moving files does not reproduce global CDN distribution or geographic failover. Pages distribution and production adoption have their own PAGES-* gates. |
| Workers-like behavior | Route to ordinary HTTP applications; reconsider bounded plugins only for a demonstrated need | A multi-tenant runtime needs isolation, scheduling, quotas, secrets and deployment recovery. Worldwide execution and provider-specific bindings add infrastructure and migration work. WASM plugins in VISION are a stretch goal, not Workers compatibility. |

Leaving Cloudflare is technically possible with other components and
providers. The practical boundary is the cost and evidence needed to deliver
equivalent service for a stated workload, rather than the number of features
implemented. DNS, CA, hosting and upstream mitigation dependencies should be
explicit and replaceable where practical.

## Deployment modes to prove

```text
Direct:          visitor -> Switchboard edge -> app / owned tunnel -> app
Exterior proxy:  visitor -> chosen proxy -> Switchboard edge -> app / tunnel
```

The direct mode uses browser-trusted origin TLS and a measured capacity and
upstream-protection budget. It does not inherit Cloudflare filtering.

The exterior-proxy mode authenticates the expected proxy/deployment and
accepts visitor metadata only from trusted peers. Direct access to that
protected origin must not bypass the boundary. In the current Cloudflare
deployment this includes network filtering and deployment-specific origin
client certificates; those credentials are separate from public server TLS
and the tunnel CA.

These are deliberate modes configured and tested separately. Never expose
the protected origin automatically when a provider or tunnel fails. A
provider change is an operator action with rollback, not a fail-open path.

## Demonstration and portability acceptance

Use a disposable application and controlled hostnames. The initial product
demonstration must show:

1. Enrol and connect an application through an outbound tunnel.
2. Associate a hostname, valid HTTPS and an explicit access policy.
3. Admit an allowed visitor, refuse an unauthorized one, and explain both.
4. Revoke access and prove it stops working; test forged identity/IP headers
   and attempts to bypass the expected ingress.
5. Survive restart, tunnel loss and certificate-renewal failure with explicit
   status and recovery, retaining a valid certificate when safe.
6. Repeat with direct ingress and an exterior proxy, preserving application
   data and policy semantics. Compare allow/deny outcomes, the verified
   principal and visitor address, redirects, callbacks and tunnel recovery.

Provider portability additionally needs a real alternate DNS/provider path:
prove a separate DNS-01 adapter, TXT cleanup, issuance/renewal and restart;
then document migration, TTL/propagation, origin trust changes and rollback.
Cloudflare DNS-only can test removal of its HTTP proxy, but does not prove
independence from its authoritative DNS or DNS API.

Record versions/configuration references without secrets, test commands,
outcomes, latency/error/resource budgets and residual risks. State what
protection and performance are lost in each mode. The current deployments
and local tests do not establish full provider portability or global parity.

## Scope and Portfolio direction

Concentrate the initial offering on the application-access circuit and its
adapters. A new authoritative DNS implementation, worldwide CDN, global DDoS
network and distributed functions platform are outside the initial scope.
Future features enter the existing tracker only after a concrete need and
acceptance criteria; this document creates no parallel backlog.

The owner stated on 2026-10-20 that Portfolio will leave Cloudflare Pages.
This supersedes the 18/10 requirement to keep it there permanently. The
migration remains pending: define deployment/rollback, preserve ES/EN and
assets, validate HTTPS, recovery and the chosen ingress, then schedule the
operator's cutover. This documentation request does not execute a deployment,
change DNS or assert that the public Portfolio has moved.

## References

- [Cloudflare DNS proxy status](https://developers.cloudflare.com/dns/proxy-status/)
- [Let's Encrypt and ACME](https://letsencrypt.org/how-it-works/)
- [Cloudflare DDoS components](https://developers.cloudflare.com/ddos-protection/about/components/)
- [Cloudflare Workers platform](https://developers.cloudflare.com/workers/)

The references explain service boundaries, not enabled features in the
owner's account. Account and deployment evidence stays in
[Server Ops](../../../server-ops/docs/CLOUDFLARE-INVENTORY.md).
