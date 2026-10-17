# TUN-001: local outbound tunnel experiment

Status: local experiment on `spike/tun-001-local`, not a public deployment.
Date: 2026-10-17. No VPS, Cloudflare account, DNS change or inbound router
port is needed to develop and test this transport.

## Decision and boundary

The first owned transport is one connector, one configured HTTP/static route,
and one edge, using `github.com/hashicorp/yamux v0.1.2` over mutual TLS 1.3.
The same `switchboard` binary runs both roles. The connector initiates the TCP
connection; the edge opens logical streams inside it for visitor requests.
Yamux roles describe who opens streams, not who initiated TCP: the edge uses
`yamux.Client`, while the outbound connector uses `yamux.Server`.

```text
visitor HTTP -> edge / existing trust + host routing + forward pipeline
                         |
                 HTTP over yamux streams
                         |
               mutually authenticated TLS
          (TCP connection initiated by connector)
                         |
              connector / fixed configured route
                         |
                  static site or HTTP origin
```

The CLI only permits literal loopback addresses, including the edge ingress.
This deliberately prevents this experiment becoming an accidental public
listener. A future public edge belongs on an external host; deploying both
roles behind the residential router does not hide the residential IP.
Cloudflare Tunnel is an optional alternative, not a prerequisite for this work.
The live portfolio on Cloudflare Pages is unaffected until an explicit cutover.

## Local run

Both listeners default to ephemeral loopback ports, so omitting `-listen`
does not compete with the normal Switchboard service.

From the repository (Go must be in PATH):

```sh
go build -o /tmp/switchboard-tunnel ./cmd/switchboard
lab=$(mktemp -d /tmp/switchboard-tunnel-lab.XXXXXX)
/tmp/switchboard-tunnel tunnel-spike init -pki "$lab/pki"
/tmp/switchboard-tunnel tunnel-spike edge -pki "$lab/pki" \
  -listen 127.0.0.1:0 -tunnel-listen 127.0.0.1:0
```

The edge prints `HTTP http://127.0.0.1:<http-port> (Host: ...)` and `TUNNEL 127.0.0.1:<tunnel-port>`. In a second terminal,
substitute the directory and actual port shown by the first process:

```sh
/tmp/switchboard-tunnel tunnel-spike connect -pki /tmp/ACTUAL-LAB/pki \
  -edge 127.0.0.1:ACTUAL-TUNNEL-PORT \
  -root /home/saint/projects/portfolio/dist
```

Build the portfolio with `npm run build` in its own repository first. An
alternative to `-root` is `-upstream http://127.0.0.1:4321` when a local origin
is already running. Exactly one of the two flags is required. The upstream
must be a literal loopback HTTP origin with a port; URL credentials, external
addresses and path/query routing are rejected. The edge never supplies the
connector's filesystem root or origin address over the network.

```sh
curl --fail -H 'Host: portfolio.localhost' http://127.0.0.1:ACTUAL-HTTP-PORT/
curl --fail -H 'Host: portfolio.localhost' http://127.0.0.1:ACTUAL-HTTP-PORT/es/
```

Use the explicit Host header: matching a configured hostname without a port
to an incoming hostname with a port is still tracked by `EDGE-003`.
Stop both processes with Ctrl-C. Delete only your disposable lab directory
when finished. No systemd units are installed or restarted. `:8888` belongs to the running
`switchboard-dev.service`, so the lab uses ephemeral loopback ports for both
HTTP and the tunnel; it is not a new persistent service. Tests use ephemeral listeners too.

## Authentication and ownership

`init` creates a fresh private directory (0700) with an ephemeral CA certificate,
server/client leaf certificates and leaf keys (all 0600). Keys are generated
locally, never printed. The CA private key is not saved. The lab leaves expire
in 24 hours and have separate client/server EKUs. Existing directories are
rejected rather than overwritten. Both identities share a directory only for
this local lab; they must not be distributed together in a real deployment.

The connector verifies the CA and the edge DNS SAN `edge.localhost`. The edge
requires a valid client certificate and an exact `connector.localhost` DNS SAN.
ALPN must be `switchboard-tunnel-spike/1`; no insecure TLS toggle exists in the
CLI. Accepting any certificate from the CA without checking its authorized
identity would allow another enrolled connector to hijack this route.

Only one active connector is allowed. A duplicate authenticated connection is
rejected without replacing the current session. The edge clears a disconnected
session only if it is still that exact session, preserving a newer attachment.
Lifecycle events use stable codes; raw TLS errors/peer payloads are not logged.

## Resource and lifecycle findings

- Handshake concurrency is capped at 16 and handshakes time out after 5s.
- The edge accepts at most 32 concurrent HTTP requests through this handler.
  Yamux backlog is 64; stream opens/write stalls are bounded by 5s, with 5s
  transport keepalives. A missing connector returns 502; unknown hosts get 404.
- Each request uses a fresh HTTP stream. This avoids stale pooled connections
  when a connector is replaced. A cancelled request closes its own stream;
  the session remains available for other requests.
- `context.Context` cancellation closes listeners, sessions and active HTTP
  connections. The CLI joins both edge workers before returning.
- The normal proxy gains only an optional `http.RoundTripper` dependency.
  Existing trust, cookie stripping, authorization and routing stay in place.
  The spike itself exposes one public lab route, without an OIDC configuration.
- Connector forwarding performs trust checks again. This conservatively drops
  incoming identity/forwarding headers; original visitor IP/identity propagation
  across the authenticated tunnel is not yet a defined assertion contract.
  It must not be advertised as preserving client attribution for protected apps.

## Deferred work

`TUN-002` owns automatic reconnection/backoff, named routing, enrolment and
revocation, persistent credentials, operator status, and integration with normal
configuration/reload/admin. Its first slice (registry, revocation, `ConnectLoop`)
is implemented in `internal/tunnel`; what remains is listed in the tracker. `TUN-003` owns ephemeral protected shares. Production
TLS issuance, systemd deployment, rate limiting/exposure gates and a real remote
edge remain separate work. A local success does not prove WAN behavior, latency,
residential-IP privacy, DDoS protection or host restart recovery.

## Observed verification (2026-10-17)

- Targeted `go test -race -count=1 ./internal/tunnel ./internal/proxy
  ./cmd/switchboard` passed. Tests cover concurrent request/response bodies on
  one TLS session; identity/header/cookie stripping; offline/unknown routes;
  duplicate attachment; cancellation; manual reconnect; lab CLI restrictions.
- TLS rejection covers wrong connector SAN signed by the trusted CA, foreign CA,
  expired certificate, wrong EKU, missing certificate/ALPN, and wrong server SAN.
  A compiler overlay removing the exact identity check makes the wrong-identity
  regression fail, without changing shared source files.
- `go build` and `scripts/smoke-tunnel-local.py --binary <built-binary>
  --site-root /home/saint/projects/portfolio/dist` passed using separate real
  processes, ES/EN pages and hashed assets, duplicate/foreign-CA rejection,
  abrupt connector death, manual reconnection, shutdown and lifecycle events.
  Temporary processes, listeners and identity directories were removed.
- The first `scripts/verify.sh` run stopped at the concurrent unfinished
  `internal/ratelimit` exercise. That work was preserved and subsequently
  completed in parallel. A fresh full gate then passed: formatting, vet, all
  packages with race, build, all four fuzz checks and diff check.
- Optional `staticcheck` and `govulncheck` are not installed.

TUN-001 local criteria are verified; code is committed in `fd72b2c`. No push, public
deployment, application restart, live credential read or DNS change was performed
for this experiment. Persistent operation remains TUN-002.
