# Cloudflare coexistence and gradual independence

**Accepted by the owner: 2026-10-18.** Architecture decision, not evidence of
public deployment. Active work stays in [TO_IMPLEMENT.md](../TO_IMPLEMENT.md):
EDGE-006, EDGE-001, TUN-002 and SHD-001. Coordination stays in CROSS-006.

## Decision

Keep the public Portfolio on Cloudflare Pages. Use a separate preview hostname
to exercise the combined application path:

```text
Visitor -> Cloudflare proxy -> HTTPS Switchboard on VPS -> application
                                                       -> owned mTLS tunnel
                                                          -> homelab application
```

The public Portfolio remains independent of the VPS, home connection and
tunnel. Its static build remains useful as a Switchboard test fixture.
Cloudflare's HTTP proxy and Cloudflare Tunnel are different dependencies:
this design keeps the owned connector and does not require cloudflared.

Reduce vendor dependence as capabilities become proven. Cloudflare initially
provides exterior protection; Switchboard owns identity and gateway policy.
Applications retain their own authorization. Direct operation remains a
separate, deliberately tested mode.

## Responsibilities and evidence

| Area | Initial owner | Current evidence or limit |
|---|---|---|
| Public Portfolio hosting/delivery | Cloudflare Pages | Owner's hosting decision; account configuration not inventoried. Astro output is portable. |
| Exterior DDoS mitigation, WAF and bots | Cloudflare according to actual plan/settings | Inventory required; no claim that every available feature is enabled or that one VPS provides equivalent coverage. |
| Visitor and origin TLS | Cloudflare terminates visitor TLS; validated HTTPS to VPS | Target only. Origin issuance/renewal must be proven; private tunnel certificates do not establish public HTTPS. |
| Identity, policies and gateway decisions | Switchboard | Existing OIDC/policy core; tunnel visitor identity/IP contract and config/admin integration remain TUN-002 work. Application authorization stays in the app. |
| Connector identity and transport | Switchboard | Private mTLS pilot, reconnect, revocation and native leaf renewal verified; this is not visitor WAF protection. |
| Rate limiting, security headers and Shield | Switchboard progressively | SHD-001 partial; SHD-002 and PAGES-003 open. New detection rules start in log-only mode. |
| Processes, ports, firewalls, backup and recovery | Server Ops | Host plane remains separate from trust plane. |
| Workers/provider-specific application logic | Inventory first; portable code by default | No actual Workers dependency established in this review. Record each use, data, replacement and migration cost before building an alternative. |

## Acceptance before a public combined pilot

Requirements below are planned controls, not deployment claims:

- Inventory actual DNS/proxy, TLS, WAF, bot, rate-limit, Access, Pages and
  Workers configuration. Record enabled/disabled/unknown and plan limits
  without secrets.
- Restrict public origin web access to the approved Cloudflare path. Network
  source filtering does not identify our specific zone: evaluate additional
  origin authentication bound to our deployment. Document range/credential
  updates and revocation.
- Test direct public IPv4/IPv6, alternate hostnames and forged forwarding or
  identity headers. A correct Host alone must not bypass the boundary.
  SSH/admin/private mTLS retain their VPN/loopback boundaries.
- Trust visitor IP metadata only from approved/authenticated proxy peers;
  verify the actual header mapping, limiter keys, redirects and OIDC callback
  behavior. Visitor and connector identities remain separate.
- Close applicable TUN-002/SHD-001 exposure gates. Prove origin HTTPS renewal,
  restart recovery and the actual application path.
- Measure legitimate and controlled abusive traffic: false positives,
  latency, errors, resources and decision evidence. Provider-blocked requests
  never reach Switchboard; their absence cannot prove Shield coverage.
  Use controlled replay or an isolated path for comparisons.

## Criterion for reducing a dependency

For each control, record owner, configuration, threat, test corpus, failures,
maintenance cost and rollback. Define traffic/load and acceptable latency,
error and false-positive budgets before testing. Replace one control at a time
only when the owned implementation meets those budgets, survives recovery and
produces explainable decision evidence. Active tasks belong in the tracker.

WAF/bot results are claims about the measured corpus, not universal parity.
Global CDN and volumetric DDoS absorption remain outside a single VPS's scope;
upstream protection can remain necessary after application controls become
portable. Essential application functions, identity and policies should work
without Cloudflare, with lost protection/performance explicitly recorded.

Exercise removal on a separate controlled hostname/environment. Never
automatically fail open to the public origin. Record DNS/TLS changes,
alternative upstream mitigation, recovery objectives and rollback before a
public cutover.

## Operational consequence

Retain the private pilot and prepared HTTPS candidate as test assets. They do
not require moving the Portfolio apex or opening web ports now. Start EDGE-006
with the configuration inventory and acceptance contract, then implement the
origin boundary and remaining gates. Publication/control removal is a separate
explicit deployment action.

Implementation-time references:
[Pages headers](https://developers.cloudflare.com/pages/configuration/headers/),
[WAF](https://developers.cloudflare.com/waf/),
[DDoS protection](https://developers.cloudflare.com/ddos-protection/).
