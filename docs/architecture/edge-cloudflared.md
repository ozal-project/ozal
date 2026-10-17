# Edge behind Cloudflare Tunnel

Status: **optional alternative, not the selected development path** (2026-10-17).
The owner now prioritizes the [owned local tunnel](tunnel-local-spike.md), with
public deployment deferred until an external host is available. The following
records the earlier Cloudflare-based proposal; its tunnel credentials are not
a blocker for developing Switchboard.

Historical decision record for the first public edge
of `octaviosantacruz.com`. Supersedes the "VPS + DNS-01" path of `EDGE-001` and
`EDGE-002` for the first release; the VPS path stays valid for later.

## Decision

Cloudflare owns DNS and public TLS. A `cloudflared` tunnel on the existing
host carries traffic out to Cloudflare, so no VPS and no inbound port are
needed. Switchboard stays the reverse proxy: it routes by `Host`, applies
authentication and strips the edge session before the upstream.

```
client ──TLS──▶ Cloudflare ──tunnel (outbound)──▶ cloudflared ──▶ Switchboard :8888 ──▶ apps (loopback)
```

Why this shape:

- No VPS cost and no open port; the origin cannot be reached directly.
- Switchboard keeps the product role (routing, identity, Pages); Cloudflare is
  only transport and certificates.
- `cloudflared` is a native binary, which fits the host rule of no Docker.

## Trade-offs accepted

| Topic | Consequence | Mitigation |
|---|---|---|
| Cloudflare terminates TLS | Cloudflare sees plaintext. Acceptable for public sites; a conscious decision for Custodia and SIGEA. | Portfolio first. Sensitive apps only behind Switchboard login, and only after the decision is recorded here. |
| Universal SSL is one level | `*.octaviosantacruz.com` is covered; `*.share.` / `*.apps.` are not (Advanced Certificate Manager is paid). | Flat names per project (below). |
| Overlap with `TUN-*` | Switchboard's own tunnels do the same job for shares. | cloudflared is the public path for fixed apps; `TUN-*` stays for ephemeral shares and is judged separately. |
| Host uptime | Site is up only while this host and its link are. | Demo/portfolio grade, not production. |

## Naming

Flat subdomains, one per project:

| Host | Route | Exposure |
|---|---|---|
| `octaviosantacruz.com` (apex) | Cloudflare Pages (already live, not through the tunnel) | public; stays on Pages |
| `preview.octaviosantacruz.com` | `portfolio/dist` (Switchboard Pages) | public smoke target for the tunnel and `PAGES-002`; never replaces the live apex |
| `custodia.octaviosantacruz.com` | panel `:5174` | behind Switchboard login, after the gates |
| `sigea.octaviosantacruz.com` | panel `:5173` | behind Switchboard login, after the gates |

The apex `octaviosantacruz.com` serves the portfolio (decision 2026-10-17; no
separate link hub) and, per the operator, the portfolio is already deployed on
Cloudflare Pages (not verified from this host). It stays there: Pages does not
depend on this host's uptime, so moving it behind the tunnel would only lower
its availability. The portfolio's «Projects» section links only real demos
and labels each as prototype, in development or available; apps behind login
are not presented as public. The tunnel hostnames are separate records and do
not touch the Pages apex. A new project is one route in Switchboard plus one hostname in
the tunnel; nothing else changes.

## Responsibilities

| Piece | Owner | Notes |
|---|---|---|
| DNS records, tunnel, credentials | Cloudflare account / operator | Credentials file stays local, mode 0600, never committed or pasted into chats. |
| `cloudflared` unit, ports registry, backup of its config | Server Ops | Unit joins `server-ops.target`; config (without credentials) is part of `CROSS-008`. |
| Routes, auth, header handling, Pages | Switchboard | `EDGE-005` below. |
| App authorization (RBAC) | Each app | Switchboard does not replace it. |

## Ingress rules (non-negotiable)

1. The tunnel config points **only** to `http://127.0.0.1:8888`, with a final
   `http_status:404` catch-all.
2. Never route `:9090` (Switchboard admin), the Server Ops panel (`:8095`) or
   any app port directly.
3. Switchboard's `dev_errors` stays `false` on the public config; unknown
   hosts must not list routes.
4. Each app that validates `Host` (Custodia's `TrustedHostMiddleware` returns
   400 for an unknown public host) needs its public host declared before
   exposure. Found in the `CROSS-006` pilot.

## Client IP behind the tunnel

`EDGE-005` is implemented locally: `trusted_proxies` is an explicit list of
peer IPs/CIDRs, disabled when absent. For this tunnel use only:

```json
"trusted_proxies": ["127.0.0.1/32", "::1/128"]
```

Only a trusted TCP peer with one valid `CF-Connecting-IP` supplies the client
address; missing/invalid/ambiguous headers fall back to the socket peer. Incoming
forwarding-IP headers and underscore aliases are stripped. The selected address
is stored in request context (`pipeline.ClientIP`) and becomes the single
outbound `X-Forwarded-For`; `RemoteAddr` is never rewritten. Config clone,
route mutations and atomic reload preserve the trust policy. See the ready-to-use
[`config.tunnel.example.json`](../../config.tunnel.example.json), which serves
only Portfolio and keeps both listeners on loopback with `dev_errors: false`.

This trust boundary permits local processes to assert a client address; it does
not authenticate `cloudflared` as a process. Keep the origin on loopback, do not
trust visitor networks, and do not allow untrusted host users/processes to reach
it. No `X-Forwarded-For` chain is accepted as an alternate source.

Cloudflare's [header reference](https://developers.cloudflare.com/fundamentals/reference/http-headers/)
notes that Pseudo IPv4 **Overwrite Headers** replaces `CF-Connecting-IP` with a
synthetic IPv4 and preserves the original in `CF-Connecting-IPv6`. Use Pseudo IPv4
off (or Add Header) for this deployment: Switchboard intentionally uses only the
validated `CF-Connecting-IP`, not a second fallback header. Worker subrequests
have separate Cloudflare header semantics and are outside this deployment plan.

## Workflow

Each step has its own evidence; do not mark the next until the previous holds.

1. **Local baseline.** `scripts/verify.sh` green; pilot config serves the
   target app on `<name>.localhost:8888` (see `CROSS-006` in
   `server-ops/docs/INFRAESTRUCTURA.md`).
2. **Create the tunnel** (operator). `cloudflared tunnel login`, `tunnel create`,
   one credentials file kept local. Route DNS per hostname or one proxied
   wildcard CNAME to the tunnel. Do not paste tokens into any chat or doc.
3. **Write the ingress config** following the rules above. Validate with
   `cloudflared tunnel ingress validate` and `ingress rule <url>`.
4. **Header handling** (`EDGE-005`, locally implemented). Enable the explicit
   loopback list and verify the trusted/untrusted peer tests before exposure.
5. **Unit and registry** (Server Ops). Add the `cloudflared` unit to
   `server-ops.target`, register its local metrics port in `ports.json`,
   run `bin/check-ports`, document in `INFRAESTRUCTURA.md`.
6. **Smoke through the tunnel.** The live portfolio stays on Cloudflare
   Pages. `npm run build`, route `preview.` to `dist`. Check `/` and `/es/`, no
   `sb_session` upstream, unknown host gives a bare 404, the real client IP
   arrives (`EDGE-005`), and the origin is unreachable except through the
   tunnel.
7. **Restart test.** Restart host services; the site returns without manual
   steps.
8. **Gate for protected apps.** Switchboard OIDC login working, `SEC-*`
   closed (done 2026-10-17), Custodia `PENTEST-003` decided, and the
   plaintext-at-Cloudflare decision recorded above. Then add `custodia.` and
   `sigea.` one at a time.
9. **Close.** Move the rows to `IMPLEMENTED.md` with date, evidence and
   commits; update the cross tracker (`CROSS-006`).

## Rollback

Disable the DNS record or stop the `cloudflared` unit: the origin has no
public path, so the site goes dark without touching Switchboard or the apps.
Local access on `*.localhost:8888` is unaffected.

## Open decisions

- Authentication for Custodia/SIGEA: Switchboard login only, or Cloudflare
  Access in front as an extra layer.
- Local credentials file versus dashboard-managed token (recommended: local
  file, so Server Ops can back up the config).
