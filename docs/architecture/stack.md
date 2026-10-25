# Stack

Decisions about languages, libraries and tools. A new dependency is added here
*before* it lands in `go.mod`, with the reason. The default answer is the
standard library: writing the piece yourself is often the point of this
project.

**Last reviewed:** 2026-10-19

## Decided

| Area | Choice | Why |
|---|---|---|
| Language | Go (minimum `go 1.26`, recommended toolchain `go1.27.3`) | Single static binary, excellent `net/http` and `crypto/*`, the chosen implementation language |
| HTTP, TLS, proxy | stdlib `net/http`, `crypto/tls`, `httputil.ReverseProxy` (migrate to `Rewrite`) | Mature and enough; post-quantum hybrid key exchange (X25519MLKEM768) on by default |
| Public certificates | `golang.org/x/crypto/acme/autocert` today → **own bounded manager on the existing `golang.org/x/crypto/acme` + Cloudflare DNS API via stdlib `net/http`** (`EDGE-007`, revised 2026-10-18) | DNS-01 validates without inbound challenge connections and preserves a publicly trusted origin certificate behind Cloudflare. Initial scope is one name, `preview.octaviosantacruz.com`, with zero new Go modules. Learning and dependency control justify owning the lifecycle only with explicit storage, cleanup, retry and renewal acceptance; see [lifecycle contract](#public-origin-certificate-contract). CertMagic is a fallback if maintaining this bounded implementation proves too costly. |
| Upstream OIDC login and GitHub deployment identity | `coreos/go-oidc` + `golang.org/x/oauth2` | Existing verifier reused for Pages with pinned GitHub issuer/JWKS, RS256, audience and expiry checks; no new dependency |
| JOSE / JWT | `go-jose/go-jose/v4` | Already a dependency; reused for the OIDC provider |
| Config | JSON with `DisallowUnknownFields` | Already in use; strict and dependency-free |
| Logging | stdlib `log/slog` (JSON handler) | Structured logs without a dependency |
| Rate limiting | `golang.org/x/time/rate` or own token bucket | A bounded token bucket can remain local; x/time/rate is the library alternative |
| Persistent state | SQLite via **`modernc.org/sqlite`** | Embedded: no extra service, so the gate cannot lose its database while it runs; pure Go (no cgo) keeps cross-compiling trivial; one file to back up. Kept behind consumer-declared ports so a Postgres adapter can be added later |
| Migrations | Embedded numbered `.sql` files + small own runner | Same discipline as DSHub: sequential and immutable once applied |
| Tunnels | **`github.com/hashicorp/yamux v0.1.2`** over mTLS (`TUN-001` local spike); `quic-go` evaluated later | Reuse reviewed stream multiplexing rather than invent framing/flow control. TLS authenticates both peers; one configured connector and upstream in the local spike. QUIC is a later protocol option. |
| Passkeys | **`go-webauthn/webauthn`** v0.18.2 (in use since 2026-10-21, `ID-003`) | WebAuthn verification is subtle; learn it through a reviewed library. Brings 8 indirect modules (`fxamacker/cbor`, `go-webauthn/x`, `golang-jwt/jwt/v5` and `go-viper/mapstructure` for authenticator metadata, `google/go-tpm`, `tinylib/msgp`, `philhofer/fwd`, `x448/float16`); `govulncheck` clean at adoption |
| SSH (Q2–Q3) | `golang.org/x/crypto/ssh` | Certificates, server and client in one package |
| Dashboard | **Decided 2026-10-20 (owner):** the console is a Svelte 5 single-page app built with Vite 8, styled with Tailwind CSS 4 and shadcn-svelte components (bits-ui), icons from `@lucide/svelte`. Source in `web/console`, built by `scripts/build-console.sh` into `internal/admin/console`, committed and embedded with `embed.FS`. Plain Vite, not SvelteKit: SvelteKit's static output boots with an inline script, which the console CSP (`script-src 'self'`, no `unsafe-inline`) forbids. `/_/status` stays a server-rendered `html/template` route-debug view that works without JavaScript. | Node is a build-time tool only, so the binary stays self-contained and `go build` needs no Node. The owner preferred a styled component system over hand-written HTML/JS. Versions are pinned exactly in `package-lock.json` and were each at least two weeks old when chosen (TypeScript 6.0.3, not 7, because `svelte-check` needs the TypeScript API). `verify.sh` rebuilds the console when its toolchain is installed and fails if the embedded copy is stale. Same-origin only; the admin token is held in memory for the simulator, never in `localStorage` or a cookie |
| Project site | **Decided 2026-10-20:** Astro 7 static output in `site/`, Tailwind CSS 4, same fonts and tokens as the console. Served as files by Switchboard Pages (dogfooding) at `switchboard.octaviosantacruz.com`. **2026-10-25 (`SITE-003`):** Geist Sans and Geist Mono (`@fontsource/geist-sans` and `@fontsource/geist-mono` 5.3.0, OFL, self-hosted) replace Manrope and IBM Plex Mono, with a dark/violet palette; the console moves to the same tokens in `UI-005`. Published from GitHub Actions through the deploy endpoint (`.github/workflows/site.yml`) | Content page: static HTML, no client JavaScript, SEO-friendly. Next.js was considered (owner preference) but its value is a React runtime this page does not need; Astro matches the Portfolio stack |
| CLI | stdlib `flag` with a small subcommand dispatcher | Few subcommands; avoids a framework |
| Telegram | Bot API called directly with `net/http` | Small API surface; good HTTP-client practice |
| Tests | stdlib `testing`, `httptest`, native fuzzing, `-race` | No assertion library; matches existing tests |
| Verification | `scripts/verify.sh` (gofmt, vet, tests, build, diff check; staticcheck and govulncheck when installed) | One local gate, like DSHub's `npm run verify` |

## To decide (with target quarter)

| Area | Options | Decide by |
|---|---|---|
| VPS provider | Hetzner CX23, reachable over WireGuard at 10.8.0.1 | Private loopback-only Switchboard/Portfolio staging installed (2026-10-17 America/Asuncion); public EDGE-001 remains gated. Builds and application databases stay in the homelab. [TUN-001](tunnel-local-spike.md) remains the owned-transport lab; Cloudflare Tunnel is optional. |
| Wildcard DNS-01 scope | **Decided 2026-10-19 (owner): one wildcard certificate (`*.octaviosantacruz.com`) from the same own manager**, not CertMagic and not several independent names. The manager still serves one certificate *name*; `internal/certs/names.go` holds every wildcard rule (single-label coverage, apex excluded, base-domain challenge TXT, `_wildcard.` storage key). One manager means one ACME attempt and one challenge record at a time, so there is no concurrent-TXT case. The apex stays on Pages. Trade-off: the private key covers every subdomain, so each hostname still needs its own Cloudflare origin-pull credential and route; revisit if a second independent name or zone is needed | Implemented in `EDGE-002`; staging then production issuance acceptance pending |
| Durable audit / checkpoints | Stdlib JSONL + SHA-256 hash chain + Ed25519 checkpoints; optional AuditAnchor adapter; RFC 9162/Merkle later. No new module for the local log | Before `AUD-001`; [contract](audit-contract.md) |
| Shield challenge | Own proof-of-work + WebCrypto + signed clearance cookie; no third-party CAPTCHA. Evaluate Cap/Anubis/mCaptcha for UX and replay handling | Before `SHD-003`; [design](shield-challenge.md) |
| Identity integration with apps | Transport-only, coexistence, signed assertion (JWT/JWKS or mTLS-bound header), shared IdP/token exchange, or Switchboard as IdP (out of scope) | Before `ARCH-009` implementation; [options](identity-integration.md) |
| Learning-lab tooling | External tools only (`pprof`, `benchstat`, `hey`/`k6`, TLA+/Alloy, axe/Lighthouse, `h2spec`, `testssl.sh`); no runtime dependency without a `stack.md` entry | Per `LAB-*`; [tracker](../TO_IMPLEMENT.md) |
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
| Go → TypeScript types for the admin API | Hand-written in `web/console/src/lib/api.ts` for now; `tygo` if the API grows | Q2 (with `UI-002`) |
| Releases | GoReleaser (`.goreleaser.yaml`, tested with v2.3.2), SHA-256 checksums, SPDX SBOM by syft (v1.14.0); cosign later (`LAB-010`). Build tools, not module dependencies | Q1 (config in place 2026-10-20) |

## Public origin certificate contract

This is the accepted design for `EDGE-007`. The DNS-01 manager described here
is implemented in `internal/certs` (commit `2698e62`, 2026-10-18) with
simulated-ACME/DNS/storage tests; the autocert HTTP-01 path remains for
listeners without `tls.dns01`. Real staging and production issuance still need
an operator-supplied DNS token and are not claimed as verified. The
[renewal decision contract](certificate-renewal.md)
documents its pure decision; the full lifecycle is covered by the package's
tests, beyond that decision alone.

- **Bounded ownership:** one configured hostname and Cloudflare DNS, using the
  existing `x/crypto/acme` protocol client and a small `net/http` adapter. Do not
  write ACME/JWS or cryptographic primitives. Wildcards, multi-edge coordination,
  additional DNS providers and a general certificate platform are outside this
  task; `EDGE-002` extends the same manager to one wildcard name (2026-10-19).
- **Consumer port:** the listener declares the minimum interface,
  `GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error)`, and passes it
  to `tls.Config.GetCertificate`. `cmd/switchboard` wires the implementation;
  storage, DNS, scheduling and issuance stay out of the listener. A later
  implementation can replace the manager without changing this TLS contract.
- **Account and storage:** configure the ACME directory and registration email;
  persist the ACME account identity/key separately from the certificate key.
  Use private permissions, validate hostname, chain, lifetime and key matching
  against the system roots (or an explicit `certificate_roots_file` bundle for
  isolated staging),
  then durably store a complete certificate/key generation before one atomic
  activation. Recover the last complete generation after a crash; never pair
  files from different generations. Keep staging and production state separate.
  Token references use existing `file:`/`env:` resolution and a token scoped to
  the required zone; never log or persist resolved credentials in config.
- **DNS ownership:** save the exact record ID returned by Cloudflare along with
  its zone, name and value, and delete only the record owned by that issuance.
  Use bounded propagation polling and cancellable network requests before
  requesting validation. Cleanup also runs after issuance errors, with a fresh
  bounded cleanup context when the parent was cancelled. Persist cleanup intent
  and retry owned orphan cleanup on recovery; an ambiguous create timeout must
  be reconciled conservatively, never by deleting every TXT at the name or
  assuming that an identical value proves ownership. Test that unrelated TXT
  records survive. A local successful DNS lookup is not proof that all ACME
  validation vantage points have observed the value.
- **Renewal ownership:** inject time into the pure renewal decision; start with
  the last third of the actual certificate lifetime, independent of its length.
  Serialize issuance/renewal into one active attempt in one process, reject a
  second writer to the same state, and cancel/join workers on shutdown. Use
  bounded exponential backoff with jitter and honor server rate-limit retry
  times; do not spin on failed authorizations. Retain the old valid certificate
  after renewal failure. With no valid certificate, TLS must fail closed and
  readiness must not report success.
- **Visibility:** emit `cert.renewed` and `cert.renewal_failed` with safe hostname,
  expiry and error-code metadata, and show expiry, last renewal outcome and next
  attempt in the trust console. Never include keys, tokens or challenge values.
- **Acceptance order:** fixed-clock core tests and simulated ACME/DNS/storage
  failures first; then Let's Encrypt staging through a direct test path using
  explicit staging-root trust and a valid origin client certificate where
  required. Never disable TLS verification or weaken Cloudflare Full (strict)
  to test staging. Production issuance and Cloudflare end-to-end verification
  follow separately, respecting the existing exposure gates. Prove renewal,
  failure recovery and restart with persisted state, not only first issuance.
  DNS-01 mode needs neither port 80 nor an ACME TLS-ALPN exemption; remove that
  exemption for this mode while preserving `originauth` checks.

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
