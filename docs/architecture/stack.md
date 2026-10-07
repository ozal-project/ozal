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
| Persistent state | SQLite via **`modernc.org/sqlite`** | Pure Go (no cgo), so cross-compiling stays trivial; one file to back up |
| Migrations | Embedded numbered `.sql` files + small own runner | Same discipline as DSHub: sequential and immutable once applied |
| Tunnels | **`hashicorp/yamux`** over mTLS (Q1); `quic-go` evaluated later | yamux is small and proven; QUIC is a later learning step |
| Passkeys | **`go-webauthn/webauthn`** | WebAuthn verification is subtle; learn it through a reviewed library |
| SSH (Q2–Q3) | `golang.org/x/crypto/ssh` | Certificates, server and client in one package |
| Dashboard | `html/template` + `embed.FS`; HTMX when interactive | No JS build step; already the documented choice |
| CLI | stdlib `flag` with a small subcommand dispatcher | Few subcommands; avoids a framework |
| Telegram | Bot API called directly with `net/http` | Small API surface; good HTTP-client practice |
| Tests | stdlib `testing`, `httptest`, native fuzzing, `-race` | No assertion library; matches existing tests |
| Verification | `scripts/verify.sh` (gofmt, vet, tests, build, diff check; staticcheck and govulncheck when installed) | One local gate, like DSHub's `npm run verify` |

## To decide (with target quarter)

| Area | Options | Decide by |
|---|---|---|
| VPS provider | Hetzner, DigitalOcean, Oracle free tier | Q1 (blocks `EDGE-001`) |
| DNS provider for DNS-01 | Cloudflare DNS (API, free), registrar's own DNS | Q1 (blocks `EDGE-002`) |
| Email for one-time codes | SMTP relay, Resend, Amazon SES | Q1 (blocks `ID-002`) |
| Private CA / ACME server | Own implementation on `crypto/x509` (preferred, learning) with Pebble as reference | Q2 |
| Policy language | Own JSON policies, Cedar (`cedar-go`), CEL | Q2 |
| Rules language | Own lexer/parser/evaluator (preferred, learning) | Q2 |
| WAF engine | `corazawaf/coraza/v3` + OWASP CRS, tested with `go-ftw` | Q3 |
| Transparency log | `golang.org/x/mod/sumdb/tlog` primitives + C2SP formats; Sunlight as reference | Q4 |
| Metrics / tracing | `prometheus/client_golang`, OpenTelemetry | Q2 |
| Local AI | Ollama HTTP API | Q4 |
| CI | GitHub Actions (Linux/macOS/Windows) | Q1 |
| Releases | GoReleaser, checksums, SBOM, cosign | Q1 |

## Rejected

| Choice | Reason |
|---|---|
| A web framework (Gin, Echo, Fiber) | `net/http` routing (Go 1.22+) covers the need |
| An ORM | Few tables, explicit SQL is clearer and teaches more |
| cgo SQLite (`mattn/go-sqlite3`) | Breaks easy cross-compilation |
| Assertion libraries (testify) | Existing tests use the stdlib style |
