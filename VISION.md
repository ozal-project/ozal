# VISION.md — Where Switchboard is going

> **North star:** an open-source, self-hostable **transparent zero-trust
> access platform**. One Go binary that publishes apps and machines without
> opening ports, gives every human, machine and AI agent a short-lived
> cryptographic identity, decides access with a deterministic policy engine,
> and records **every trust decision in a verifiable log** — so not even the
> operator can quietly change who has access.
>
> Edge proxy, tunnels, passwordless identity, a private CA, infrastructure
> access, continuous evaluation and an AI-agent gateway: small enough to read,
> honest enough to verify.

This document is the long-range map. [docs/TO_IMPLEMENT.md](docs/TO_IMPLEMENT.md) is the
near-term, checkbox-level plan. When the two disagree, the near-term plan wins
until this file is updated on purpose.

---

## Why this project

Switchboard is a **year-long passion project** (October 2026 – September 2027)
optimized for two things, in this order:

1. **Learning in depth.** Each quarter forces real work in a different field:
   networking, OAuth/OIDC from the server side, WebAuthn, X.509 and ACME, SSH
   internals, transparency logs, security event protocols, and applied ML for
   detection.
2. **Open-source utility.** Its author runs it for real on
   `octaviosantacruz.com`. If it is not good enough for that, it is not done.

It is built by someone who operates enterprise PAM and SSE (CyberArk
PAM/PSM/SIA, Netskope, BigID) day to day. The goal is to bring the *ideas* of
those products — least privilege, just-in-time access, recorded privileged
sessions, continuous evaluation — to people who will never buy them, and to
add one thing they lack: verifiability.

Not a business. Success is measured in shipped releases, written lessons and
a platform its author actually trusts, not in stars or revenue.

## The thesis: zero trust that is itself verifiable

Zero-trust products ask you to trust *them*: their policy store, their CA,
their admins, their logs. Switchboard's differentiator is that the access
system's own state is auditable by anyone who holds a copy of the log:

- Every certificate the internal CA issues is appended to a public,
  Certificate-Transparency-style log.
- Every policy change, passkey registration, signing-key rotation, access
  grant and approval is an entry in the same kind of log.
- Agents, clients and auditors verify inclusion proofs and consistency proofs.
  A policy that was changed and changed back still leaves evidence.

This answers the classic PAM question — *who watches the administrator?* —
with cryptography instead of process.

## Landscape

| Project | Strength | Where Switchboard differs |
|---|---|---|
| Cloudflare Tunnel + Access | Tunnels, protected quick tunnels, email OTP | Self-hostable identity/policy and owned transport; optional exterior proxy protection |
| Pangolin | Self-hosted tunnels, WireGuard, RBAC | Own CA and transparency log; AI-agent principals |
| Octelium | Broad FOSS ZTNA, VPN, API/AI gateway | Smaller and readable; verifiability as the core thesis |
| Teleport, HashiCorp Boundary | Infra access, session recording | Homelab-to-small-team scale, one binary |
| step-ca | Private ACME CA | CA integrated with identity, policy and a public CT-style log |
| Pomerium, oauth2-proxy | Identity-aware proxy | Includes tunnels, its own identity and PKI |
| MCP gateways (Bifrost, ContextForge…) | Tool access for AI agents | Agents are just another principal under the same policy and audit |
| CyberArk, Netskope | Enterprise PAM / SSE | The same concepts, open source, for everyone else |

Feature-count races are a losing game; the thesis above is what makes the
project worth existing.

## Product and deployment direction — updated 2026-10-20

Initial offering: publish private applications on infrastructure the operator
controls, with identity, access policies and owned tunnels, while allowing a
choice of DNS and exterior protection providers. The initial user is a small
team sharing internal tools, panels and previews. Validate setup, revocation,
explainable decisions and recovery before expanding the feature surface.

Prove direct ingress and exterior-proxy ingress separately. Cloudflare can
continue to provide exterior protection while Switchboard owns gateway
identity and policy. DNS and ACME adapters make providers replaceable; a
worldwide CDN, volumetric DDoS network and distributed functions platform
remain outside the initial offering. Inventory actual Workers uses before
building substitutes. Broad feature maps below are learning directions,
not an implementation inventory or a commitment to provider-wide parity.

The owner now intends to move Portfolio out of Cloudflare Pages. This
supersedes the 18/10 requirement to retain that hosting; migration and the
operator's cutover remain pending, with no DNS or deployment action implied.
The [positioning and portability contract](docs/engineering.md#publicacion-autonoma)
and [coexistence decision](docs/security.md#entrada-directa-o-detras-de-un-proxy)
define boundaries, demonstration and acceptance. Active work stays in
EDGE-006/EDGE-001, the existing PAGES-* tasks and workspace CROSS-006.

## What exists today

A host-based reverse proxy with health checks, automatic TLS (ACME client),
OIDC login against an external provider, per-route claim authorization,
atomic routing snapshots, and a loopback control plane with persistence and
audit lines. See [README.md](README.md). This is the seed of the **edge**.

---

## The pillars

### 1. Edge and tunnels

Publish anything without inbound ports, from a laptop, a homelab or a server
behind CGNAT.

```bash
switchboard edge --config edge.json                      # on the VPS

sb share http://localhost:3000 --allow ana@client.com    # ephemeral, protected
# → https://k7x2.share.octaviosantacruz.com   (only Ana gets in)

sb connect --name grafana http://localhost:3000          # permanent, named
sb connect --tcp ssh=localhost:22 --name homelab         # raw TCP
```

- Outbound multiplexed connection (yamux over mTLS first, QUIC later),
  reconnect with backoff, heartbeats.
- Wildcard TLS via ACME DNS-01 for public hosts.
- Protected shares in the style of Cloudflare's protected quick tunnels: the
  guest list can stay on the agent; the edge signs an identity assertion and
  the agent enforces it. Shares die when the agent stops.

### 2. Passwordless identity

- Email one-time codes / magic links and **passkeys (WebAuthn)**. No passwords,
  ever — which removes the most dangerous parts of an IdP.
- Switchboard as an **OIDC provider**: "Sign in with octaviosantacruz" for any
  app, with signing-key rotation, JWKS and discovery.
- External providers (Google, GitHub, any OIDC issuer) remain options.
- Step-up: sensitive actions require a fresh passkey assertion.

### 3. Private PKI ("our own Let's Encrypt", privately)

A publicly trusted CA is out of reach for an individual (root-program audits,
CA/Browser Forum Baseline Requirements, HSM ceremonies) and unnecessary — the
edge uses Let's Encrypt for public names. A **private CA** is where the
learning is:

- **ACME server (RFC 8555)** usable by certbot, Caddy, cert-manager.
- **Developer certificates:** trusted HTTPS for `*.localhost` installed into the
  OS trust store, like mkcert, but renewed automatically.
- **mTLS everywhere:** edge ↔ agent, agent ↔ service.
- **SSH CA:** certificates valid for minutes, issued after login; no
  `authorized_keys` sprawl.
- **Workload identity**, SPIFFE-compatible SVIDs for services.
- **Short-lived by default:** hours, not months. Revocation becomes "wait".
- Name constraints, a root kept offline, intermediate on the edge, optional
  hardware key (YubiKey/PKCS#11).
- Experimental **post-quantum** track: hybrid ML-KEM is already Go's TLS default;
  ML-DSA certificates as an opt-in experiment when Go supports them.

### 4. Infrastructure access (PSM/SIA ideas, open source)

- `sb ssh homelab`, `sb db prod-postgres` from anywhere, through tunnels.
- **Just-in-time access:** request, approve (web/email/chat), auto-expire.
- **Session recording:** SSH and web-terminal sessions as asciicast, replayable
  from the dashboard; database query logs.
- **Credential injection:** the user never sees the database password or the
  server key; the edge brokers it from a small built-in vault.
- Browser-based terminal and DB console for emergency access.

### 5. Transparency log (the thesis; long-range target)

The transparency log described here is not implemented yet. Current code has
an optional local access-decision audit with a hash chain and manual signed
checkpoints; the verifier reports `tamper_evident=false` because it cannot
prove that a checkpoint was retained outside the host. See the partial scope
in [the audit contract](docs/engineering.md#auditoria) and tracker
entries `AUD-001`/`AUD-002`.

- Tiled, static, append-only Merkle log in the style of the Static CT API /
  Sunlight: cheap to serve, cacheable, easy to mirror.
- Entry types: issued certificates, policy versions, identity/key events,
  access grants, approvals, session metadata.
- Signed checkpoints; witnesses (other people's instances) can co-sign.
- Clients and agents verify inclusion before trusting a certificate or policy.
- Future `switchboard audit verify` and log clients should check inclusion and
  consistency against independently retained checkpoints; local verification
  alone cannot prove that an operator did not rewrite the full log.
- Raw data stays private where needed: only hashes or commitments go in public
  entries.

### 6. Continuous evaluation and AI-assisted decisions

**Rule: the policy engine decides; AI advises.** Access decisions stay
deterministic, explainable and testable. AI produces *signals* and
*explanations*, never a silent allow.

- **Signals:** time, location, device certificate, impossible travel, new
  device, unusual resource — scored by a risk model trained on the platform's
  own event history.
- **Actions from signals:** re-authenticate with a passkey, shorten a session,
  require approval, revoke.
- **CAEP / OpenID Shared Signals:** transmit and receive security events so a
  revoked account or risky session is cut everywhere in seconds. Open-source
  implementations are scarce; a complete transmitter *and* receiver is a real
  contribution.
- **Explainability:** "why was Ana denied?" answered in plain language from
  the decision trace.
- **Policy copilot:** draft a policy from natural language, then dry-run it
  against the access graph and show exactly who gains or loses access.
- **Session summaries:** "this SSH session edited `/etc/sudoers` and restarted
  nginx".
- **Access reviews:** recommend removing grants nobody has used in 90 days.

### 7. AI agents as principals

Agents are a third kind of identity next to humans and machines:

- **Agent identity:** each agent gets its own short-lived credentials, bound to
  the human or service it acts for.
- **Delegation without credential exposure:** OAuth token exchange (RFC 8693);
  the agent never holds upstream secrets — the gateway exchanges them per call.
- **MCP gateway:** which agent may call which tool, with which arguments, on
  whose behalf; every call logged and appended to the transparency log.
- **Human-in-the-loop JIT:** risky tool calls (delete, pay, deploy) pause for
  approval, exactly like privileged access requests for humans.

### 8. Shield: a WAF and edge security, built to learn

Every request already passes through the edge, so it is the natural place to
learn web application security from the defender's side. Shield grows a little
every quarter instead of being one big block:

- **Rate limiting:** token buckets per IP, identity, route and login endpoint.
  The cheapest high-value control and required for a public edge.
- **Rules engine:** an own expression language in the spirit of Cloudflare's
  Rules language (wirefilter), e.g.
  `http.request.uri.path matches "^/admin" and not identity.groups contains "ops"`.
  Lexer, parser, type checker, compiler to a fast evaluator — a compiler course
  in disguise. Custom rules, redirects and header transforms all use it.
- **Managed rules:** OWASP Core Rule Set via Coraza (a Go, ModSecurity-compatible
  engine) rather than reimplementing SecLang. Learning comes from tuning,
  false-positive handling, paranoia levels, and testing with CRS's own
  regression suite — the same approach ngrok took for its WAF.
- **Own detections next to CRS:** small detectors (SQLi, XSS, path traversal,
  SSRF) written by hand and measured against the CRS test corpus to understand
  *why* the managed rules look the way they do.
- **Bot defense:** a privacy-friendly proof-of-work challenge (in the spirit of
  Turnstile and Anubis), an "under attack" mode, TLS client fingerprints (JA4)
  as a signal.
- **Signed agents:** verify Web Bot Auth (HTTP Message Signatures, RFC 9421) so
  well-behaved AI agents can prove who they are, and sign Switchboard's own
  outbound agent traffic.
- **API protection:** OpenAPI schema validation (positive security model) and
  endpoint discovery from observed traffic.
- **Leaked credentials:** check passwords submitted to upstream login forms
  against breached-password data with k-anonymity (HIBP range API), and flag
  the request to the app instead of blocking blindly.
- **Client-side protection:** CSP and Reporting API endpoint, an inventory of
  third-party scripts seen by browsers, alerts on new ones (Page Shield ideas).
- **Firewall for AI:** for LLM endpoints behind the edge — PII detection in
  prompts and responses, prompt-injection heuristics, token-based rate limits.
- **Crawler tarpit:** a maze of generated links for crawlers that ignore
  `robots.txt` (AI Labyrinth ideas), opt-in.
- **Security analytics:** every Shield decision is an event with the rule that
  fired, viewable in the dashboard and usable by the risk model.

Shield starts in **log-only mode** for every new rule; blocking is an explicit
step. A WAF that breaks the apps behind it gets turned off.

### 9. Notifications and approvals (ChatOps)

Access decisions often need a human *now*, on a phone. Switchboard gets one
`Notifier` / `Approver` interface with several channels, Telegram first:

- **Telegram bot** (free, push on every phone, inline keyboards):
  - JIT access requests arrive as a card with **[✅ Approve 1h] [❌ Deny]**;
    the message is rewritten in place with the decision, who made it and when.
  - AI-agent human-in-the-loop: "agent `deploy-bot` wants to call
    `db.drop_table` on behalf of Ana — approve?"
  - Login alerts and push approval ("is this you signing in from Madrid?"),
    in the style of Duo push.
  - Shield alerts: attack spikes, new third-party script, rule in log-only
    mode that would have blocked real traffic.
  - Bot commands for the operator: `/status`, `/shares`, `/revoke <share>`,
    `/lockdown` (under attack mode), each requiring a recent passkey session.
- **Security of the channel itself:** chats are linked to identities through a
  one-time pairing code; callback data is signed and single-use; approvals for
  high-risk actions require a passkey step-up in the browser, so a stolen phone
  or chat is not enough. Every approval goes to the transparency log.
- **Other channels behind the same interface:** ntfy and Gotify (self-hosted
  push), Discord, Slack, Matrix, email, and generic signed webhooks.
- Telegram can also deliver one-time login codes as an alternative to email.

### Stretch: private networking

WireGuard mesh between agents with NAT traversal and relay fallback, so
services reach each other privately under the same identity and policy.

---

## Cloudflare feature map

Cloudflare is the main source of inspiration. This table maps its features to
what Switchboard would build, so ideas are tracked in one place. "Quarter" is
the target release; "Stretch" means only if time allows; "No" is a deliberate
non-goal.

| Cloudflare feature | What it does | Switchboard version | Quarter |
|---|---|---|---|
| Tunnel, quick tunnels | Publish local services without open ports | `sb connect`, `sb share` | Q1 |
| Access, protected quick tunnels | Identity in front of apps; email-gated shares | Passwordless identity + `--allow` | Q1 |
| Rate limiting | Limit request rates per key | Token buckets per IP/identity/route | Q1 |
| Load balancing, health checks | Spread traffic, avoid dead origins | Multiple upstreams, active + passive health | Q1 |
| Origin CA, authenticated origin pulls | Trust between edge and origin | Private CA + mTLS to upstreams | Q2 |
| Rules language (wirefilter), custom rules | Expression-based request filtering | Own expression language and compiler | Q2 |
| Transform rules, redirects | Rewrite headers, URLs, redirects | Same rules engine | Q2 |
| Turnstile, Under Attack Mode | Human/bot challenge without CAPTCHAs | Proof-of-work challenge, attack mode | Q2 |
| Keyless SSL | Private keys stay off the edge | Remote signer for the CA/edge keys | Stretch |
| WAF managed rules | OWASP-class attack detection | Coraza + OWASP CRS, own detectors to compare | Q3 |
| API Shield | Schema validation, API discovery | OpenAPI validation, discovery from traffic | Q3 |
| Leaked credentials detection | Flag breached passwords at login | HIBP k-anonymity check, header to app | Q3 |
| Page Shield | Monitor third-party scripts in browsers | CSP/Reporting endpoint + script inventory | Q3 |
| Access for Infrastructure | SSH/RDP/DB access with identity | `sb ssh`, `sb db`, JIT, recording | Q3 |
| Bot management, JA4 | Score automation, fingerprint clients | JA4 + behavior signals into risk model | Q3 |
| Device posture (WARP) | Gate access on device state | Device certificates + posture signals | Q4 |
| Web Bot Auth / signed agents | Cryptographic identity for agents | Verify RFC 9421 signatures; sign own agents | Q4 |
| Firewall for AI | Protect LLM apps: PII, prompt abuse | Inline LLM inspection for routes behind the edge | Q4 |
| AI Gateway | Proxy, log, rate-limit LLM calls | Paired with the MCP/agent gateway | Q4 |
| Inline DLP | Detect sensitive data in transit | Response inspection on protected routes | Q4 |
| AI Labyrinth | Trap crawlers that ignore rules | Opt-in crawler tarpit | Stretch |
| Gateway DNS filtering | Filtering resolver for devices | DoH resolver with blocklists for enrolled devices | Stretch |
| WARP / Magic WAN | Private networking | WireGuard mesh | Stretch |
| Workers | Run code at the edge | WASM plugins | Stretch |
| Waiting Room | Queue visitors under load | Simple virtual queue | Stretch |
| CDN cache | Global caching | — | No |
| Volumetric DDoS protection, anycast | Absorb attacks at network scale | — (impossible on one VPS) | No |
| Browser Isolation | Render pages remotely | — (too heavy for the payoff) | No |
| Pay per crawl | Charge crawlers via HTTP 402 | — (no payments) | No |

---

## Integration map

Open-source projects and standards Switchboard can integrate with, embed, or
learn from. Building everything from scratch is not the goal; the rule is
**build the core, integrate the commodity** — write what teaches the most,
plug in what is solved.

| Area | Project / standard | How it fits | Mode |
|---|---|---|---|
| **Identity** | Google, GitHub, Keycloak, Authentik, Pocket ID, Okta | Upstream OIDC providers for organizations that already have one | Integrate |
| | SCIM 2.0 | Provision and deprovision users/groups from an IdP | Implement (Q3) |
| | OpenPubkey / opkssh | Reference for OIDC-bound SSH; compare with own SSH CA | Learn from |
| **Policy** | Cedar (cedar-go) | Candidate policy language: readable, analyzable, fast | Evaluate (Q2) |
| | OpenFGA (Zanzibar) | Relationship-based access ("Ana is editor of project X") | Evaluate later |
| | CEL | Lightweight expressions inside rules | Evaluate (Q2) |
| **PKI** | step-ca, Pebble, Boulder | Reference ACME implementations; Pebble for tests | Learn from / test |
| | cert-manager, Caddy, certbot | ACME clients that must work against the private CA | Compatibility tests |
| | YubiKey / PKCS#11 | Hardware-held root and signing keys | Integrate (Q2) |
| **Transparency** | C2SP tlog-checkpoint, tlog-tiles, tlog-witness | Standard formats so others' tools can verify the log | Implement (Q4) |
| | transparency.dev witnesses, omniwitness | External witnesses cosign checkpoints | Integrate (Q4) |
| | Sigstore (cosign, Rekor) | Sign Switchboard's own releases; reference design | Integrate (releases) |
| **Shield** | OWASP Coraza + CRS | Managed WAF rules | Embed (Q3) |
| | CrowdSec | Community blocklists and shared signals; Switchboard as a bouncer | Integrate (Q3) |
| | Anubis | Reference for proof-of-work bot challenges | Learn from |
| | MaxMind GeoLite2 / IP2Location | Country/ASN signals for rules and risk | Integrate (Q2) |
| | Have I Been Pwned (range API) | Leaked-password checks with k-anonymity | Integrate (Q3) |
| | AbuseIPDB, Spamhaus DROP | IP reputation feeds | Integrate (Q3) |
| **Signals** | OpenID Shared Signals (CAEP/RISC) | Exchange revocations and risk with IdPs and SaaS | Implement (Q3) |
| | osquery, Fleet | Device posture data from enrolled machines | Integrate (Q4) |
| **AI** | Ollama, llama.cpp | Local models for explanations and summaries; no data leaves the box | Integrate (Q4) |
| | Anthropic, OpenAI-compatible APIs | Optional hosted models, opt-in | Integrate (Q4) |
| | Model Context Protocol | Agent gateway speaks MCP natively | Implement (Q4) |
| **Secrets** | OpenBao / HashiCorp Vault, SOPS, age | External secret sources for config and credential injection | Integrate (Q3) |
| **Sessions** | asciinema format | Recording and replay of terminal sessions | Implement (Q3) |
| | Apache Guacamole (guacd) | RDP/VNC in the browser | Integrate (stretch) |
| **Networking** | wireguard-go, Headscale, NetBird | Reference and possible base for the mesh | Learn from (stretch) |
| | Docker labels, Kubernetes Gateway API | Auto-discover routes from containers, Traefik-style | Implement (Q2) |
| **Observability** | OpenTelemetry, Prometheus, Grafana, Loki | Traces, metrics, dashboards, logs | Integrate (Q1–Q2) |
| | Uptime Kuma | External monitoring of the edge itself | Integrate |
| **Notifications** | Telegram Bot API, ntfy, Gotify, Discord, Slack, Matrix | Alerts and approvals | Integrate (Q1–Q3) |
| | Apprise | Many channels behind one library, if useful | Evaluate |
| **Operations** | OpenTofu / Terraform provider | Manage routes and policies as code | Implement (stretch) |
| | GoReleaser, SBOM, SLSA provenance | Trustworthy releases | Integrate (Q1) |
| | Home Assistant | Smart-home use case: protected remote access and alerts | Example config |

---

## The year plan

Each quarter ends with a tagged release, release notes, real use on
`octaviosantacruz.com`, and a written lesson (blog post or portfolio entry).

| Quarter | Release | Main scope | Shield track | Learning focus |
|---|---|---|---|---|
| **Q1 · Oct–Dec 2026** | `v0.1` Edge | Security fixes, VPS edge with wildcard TLS, tunnels (`sb connect`, `sb share`), protected shares, passwordless identity, OIDC provider, Telegram alerts | Rate limiting, security headers | Networking, multiplexing, OIDC server side, WebAuthn |
| **Q2 · Jan–Mar 2027** | `v0.2` PKI | ACME server, internal CA, mTLS edge↔agent and edge↔origin, developer certs, SSH CA, workload identity | Rules language + compiler, custom rules, PoW challenge | X.509, ACME, key management, compilers |
| **Q3 · Apr–Jun 2027** | `v0.3` Access | SSH/DB over tunnels, JIT with Telegram approvals, session recording, credential injection, CAEP/SSF, SCIM | Coraza + CRS, own detectors, API schema validation, leaked credentials, CSP reporting | SSH internals, L4 proxying, security events, web attacks |
| **Q4 · Jul–Sep 2027** | `v0.4` Trust | Transparency log for everything, risk signals and AI explanations, AI-agent gateway | Web Bot Auth, Firewall for AI, DLP | Merkle logs, token exchange, applied ML |
| **Stretch** | — | WireGuard mesh, post-quantum certificate experiments, WASM plugins | Crawler tarpit, DNS filtering, waiting room | P2P networking, frontier crypto |

The Shield track is deliberately small each quarter so it never blocks the
main release; if a quarter slips, Shield items move first.

## Dogfooding on octaviosantacruz.com

| Host | Purpose |
|---|---|
| `auth.octaviosantacruz.com` | Login, passkeys, OIDC provider |
| `ca.octaviosantacruz.com` | Private ACME directory (reachable only by enrolled agents) |
| `log.octaviosantacruz.com` | Public transparency log tiles and checkpoints |
| `*.share.octaviosantacruz.com` | Ephemeral protected shares |
| `*.apps.octaviosantacruz.com` | Named tunnels |
| admin | Never public: only through a tunnel with a recorded session |

---

## Architecture principles to adopt now

1. **Streams, not just HTTP.** The core moves bytes between an
   identity-checked entry point and a destination. HTTP routing is one handler;
   TCP, SSH, database and MCP traffic are others.
2. **Every actor is a Principal.** Humans, machines and AI agents share one
   identity model; authorization stays a pure function of principal, resource,
   context and policy.
3. **The policy engine decides; AI advises.** No model output is ever an allow.
4. **Everything is an event.** Logins, decisions, config changes, issuance and
   sessions emit structured events from day one; logs, metrics, the dashboard,
   risk scoring and the transparency log are consumers.
5. **Short-lived everything.** Sessions, certificates and credentials expire
   fast enough that revocation is rarely needed.
6. **The operator is not trusted blindly.** Trust-relevant state changes are
   logged verifiably.
7. **Embedded state, raw config.** Runtime state (sessions, tunnels, passkeys,
   keys, approvals) lives in an embedded store (SQLite). The human-edited config
   file holds static intent; secrets come from env/files and are never written
   back.
8. **One binary, subcommands.** `switchboard edge`, `sb connect`, `sb share`,
   `sb ssh`, `switchboard ca`, `switchboard audit verify`.
9. **Secure defaults, explained.** Every default has a sentence in the docs
   saying why.

## Out of scope

- **Passwords.** Identity is passwordless or delegated.
- **A publicly trusted CA.** Public names use Let's Encrypt.
- **Being a CDN or absorbing volumetric DDoS.** One VPS cannot; the WAF
  protects applications, not network capacity.
- **Browser isolation and payments** (pay per crawl).
- **Autonomous AI decisions.** AI produces signals and explanations only.
- **Multi-tenant SaaS with billing.**

## Rules that keep a year-long project alive

1. **A quarter starts only when the previous release is in real use.**
2. **Security findings jump the queue.** A public access platform with known
   holes is worse than none.
3. **Every release ends with a written lesson.** The writing is part of the
   learning.
4. **Cut scope, never quality.** If a quarter slips, its last items move to the
   stretch list.
5. **Re-read this file every quarter** and change it on purpose.
6. **A release promises only what has evidence.** Release notes record the
   promise, evidence, non-goals, rollback/recovery and residual risks. A claim
   stronger than the evidence (“verifiable”, “transparent”, “production-ready”)
   is a release blocker.
7. **Engineering labs are learning, not release commitments.** Performance,
   failure drills, threat models, accessibility, conformance and formal methods
   live in the tracker as `LAB-*`; they do not gate a release unless the owner
   explicitly promotes one.

## Open questions

- Tunnel transport: yamux over mTLS first, QUIC later — or QUIC from the start?
- Policy language: keep JSON policies, or adopt CEL once conditions grow?
- Email delivery for one-time codes: SMTP relay, Resend or Amazon SES?
- Should the CA and the OIDC provider run as separate processes sharing a
  library, to keep the public edge's attack surface smaller?
- Risk model: start with rules and statistics; when does a learned model earn
  its complexity?

## References

- [Cloudflare — Protected quick tunnels](https://blog.cloudflare.com/protected-quick-tunnels/)
- [Let's Encrypt — 6-day and IP address certificates](https://letsencrypt.org/2026/01/15/6day-and-ip-general-availability)
- [RFC 8555 — ACME](https://www.rfc-editor.org/rfc/rfc8555)
- [Smallstep — private ACME server](https://smallstep.com/blog/private-acme-server/)
- [Filippo Valsorda — You should run a CT log](https://words.filippo.io/run-sunlight/)
- [Filippo Valsorda — Building a transparent keyserver](https://words.filippo.io/keyserver-tlog/)
- [OpenID Shared Signals Working Group](https://openid.net/wg/sharedsignals/)
- [RFC 8693 — OAuth 2.0 Token Exchange](https://www.rfc-editor.org/rfc/rfc8693)
- [SPIFFE](https://spiffe.io/)
- [OpenPubkey SSH (opkssh)](https://github.com/openpubkey/opkssh)
- [Pangolin](https://github.com/fosrl/pangolin) · [Octelium](https://octelium.com/)
- [OWASP Coraza](https://github.com/corazawaf/coraza) · [OWASP CRS](https://coreruleset.org/)
- [ngrok — Building a WAF with OWASP CRS and Coraza](https://ngrok.com/blog/ngrok-waf-owasp-crs-coraza)
- [Cloudflare — Signed agents (Web Bot Auth)](https://blog.cloudflare.com/signed-agents/)
- [RFC 9421 — HTTP Message Signatures](https://www.rfc-editor.org/rfc/rfc9421)
- [Cloudflare — Firewall for AI](https://blog.cloudflare.com/firewall-for-ai/)
- [Cloudflare — Free threat detections, leaked credentials](https://blog.cloudflare.com/a-safer-internet-with-cloudflare/)
- [Cloudflare — Inline DLP](https://blog.cloudflare.com/inline-dlp-ga/)
- [C2SP tlog-witness](https://c2sp.org/tlog-witness@v1.0.0) · [tlog-checkpoint](https://c2sp.org/tlog-checkpoint@v1.0.0)
- [cedar-go](https://github.com/cedar-policy/cedar-go) · [OpenFGA](https://github.com/openfga)
- [CrowdSec WAF behind a reverse proxy](https://docs.crowdsec.net/u/user_guides/waf_rp_howto/)
- [Telegram Bot API](https://core.telegram.org/bots/api)
