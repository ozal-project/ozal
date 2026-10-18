# Stack

Decisions about languages, libraries and tools. A new dependency is added here
*before* it lands in `go.mod`, with the reason. The default answer is the
standard library: writing the piece yourself is often the point of this
project.

**Last reviewed:** 2026-10-07

## Decided

| Area | Choice | Why |
|---|---|---|
| Language | Go (minimum `go 1.26`, developed on 1.27) | Single static binary, excellent `net/http` and `crypto/*`, the maintainer's chosen learning language |
| HTTP, TLS, proxy | stdlib `net/http`, `crypto/tls`, `httputil.ReverseProxy` (migrate to `Rewrite`) | Mature and enough; post-quantum hybrid key exchange (X25519MLKEM768) on by default |
| Public certificates | `golang.org/x/crypto/acme/autocert` today → **CertMagic** for DNS-01 wildcards | autocert has no DNS-01; CertMagic (Caddy's library) does, via libdns providers |
| Upstream OIDC login | `coreos/go-oidc` + `golang.org/x/oauth2` | Already in use and well reviewed |
| JOSE / JWT | `go-jose/go-jose/v4` | Already a dependency; reused for the OIDC provider |
| Config | JSON with `DisallowUnknownFields` | Already in use; strict and dependency-free |
| Logging | stdlib `log/slog` (JSON handler) | Structured logs without a dependency |
| Rate limiting | `golang.org/x/time/rate` or own token bucket | Own implementation is a good small learning piece; x/time/rate as fallback |
| Persistent state | SQLite via **`modernc.org/sqlite`** | Embedded: no extra service, so the gate cannot lose its database while it runs; pure Go (no cgo) keeps cross-compiling trivial; one file to back up. Kept behind consumer-declared ports so a Postgres adapter can be added later |
| Migrations | Embedded numbered `.sql` files + small own runner | Same discipline as DSHub: sequential and immutable once applied |
| Tunnels | **`github.com/hashicorp/yamux v0.1.2`** over mTLS (`TUN-001` local spike); `quic-go` evaluated later | Reuse reviewed stream multiplexing rather than invent framing/flow control. TLS authenticates both peers; one configured connector and upstream in the local spike. QUIC is a later learning step. |
| Passkeys | **`go-webauthn/webauthn`** | WebAuthn verification is subtle; learn it through a reviewed library |
| SSH (Q2–Q3) | `golang.org/x/crypto/ssh` | Certificates, server and client in one package |
| Dashboard | Q1: `html/template` + `embed.FS` + HTMX. Q2: Svelte 5 islands (custom elements built with Vite) for features that need rich client state. Q3+: SvelteKit `adapter-static` only if the dashboard becomes an app | Learn the Go side first (handlers, SSE, sessions, CSRF/CSP); Node is a build-time tool only, output embedded, so it stays one binary. Same-origin cookie auth, no tokens in `localStorage` |
| CLI | stdlib `flag` with a small subcommand dispatcher | Few subcommands; avoids a framework |
| Telegram | Bot API called directly with `net/http` | Small API surface; good HTTP-client practice |
| Tests | stdlib `testing`, `httptest`, native fuzzing, `-race` | No assertion library; matches existing tests |
| Verification | `scripts/verify.sh` (gofmt, vet, tests, build, diff check; staticcheck and govulncheck when installed) | One local gate, like DSHub's `npm run verify` |

## To decide (with target quarter)

| Area | Options | Decide by |
|---|---|---|
| VPS provider | Hetzner CX23, reachable over WireGuard at 10.8.0.1 | Private loopback-only Switchboard/Portfolio staging installed (2026-10-17 America/Asuncion); public EDGE-001 remains gated. Builds and application databases stay in the homelab. [TUN-001](tunnel-local-spike.md) remains the owned-transport lab; Cloudflare Tunnel is optional. |
| DNS provider for DNS-01 | Cloudflare DNS (already in use) | Deferred: only needed if Switchboard terminates TLS (`EDGE-002`) |
| Email for one-time codes | SMTP relay, Resend, Amazon SES | Q1 (blocks `ID-002`) |
| Private CA / ACME server | Own implementation on `crypto/x509` (preferred, learning) with Pebble as reference | Q2 |
| Policy language | Own JSON policies, Cedar (`cedar-go`), CEL | Q2 |
| Rules language | Own lexer/parser/evaluator (preferred, learning) | Q2 |
| WAF engine | `corazawaf/coraza/v3` + OWASP CRS, tested with `go-ftw` | Q3 |
| Transparency log | `golang.org/x/mod/sumdb/tlog` primitives + C2SP formats; Sunlight as reference | Q4 |
| Metrics / tracing | `prometheus/client_golang`, OpenTelemetry | Q2 |
| Local AI | Ollama HTTP API | Q4 |
| CI | GitHub Actions (Linux/macOS/Windows) | Q1 |
| Postgres adapter | Only if Switchboard runs on two or more edges (shared state, HA) | When multi-edge is planned |
| Go → TypeScript types for the admin API | `tygo` or hand-written | Q2 (with `UI-002`) |
| Releases | GoReleaser, checksums, SBOM, cosign | Q1 |

## Private pilot renewal

Private pilot leaf renewal is implemented in the existing Go binary with the
standard crypto/x509, ed25519 and JSON packages. Linux systemd schedules the
operator command; existing SSH provides authenticated host coordination.
No Python/OpenSSL runtime or new Go module is required for renewal. The CA
signer remains on the homelab; the daily timer and unit belong to Server Ops.

## Rejected

| Choice | Reason |
|---|---|
| A web framework (Gin, Echo, Fiber) | `net/http` routing (Go 1.22+) covers the need |
| An ORM | Few tables, explicit SQL is clearer and teaches more |
| cgo SQLite (`mattn/go-sqlite3`) | Breaks easy cross-compilation |
| Assertion libraries (testify) | Existing tests use the stdlib style |
| Next.js for the dashboard | Its value is a Node server at runtime (SSR, API routes): a second process beside the Go binary, or a static export that keeps only React. Svelte + Vite covers the rich-UI need with build-time Node only |
| Supabase (or any hosted DB) as primary state | The access gate would depend on a third party and a network hop for every session check; contradicts self-hosting and "verifiable even against the operator". As plain Postgres it remains a valid target for a future Postgres adapter |
