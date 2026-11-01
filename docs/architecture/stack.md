# Stack

Dependency decisions. Before adding one, record its reason and operational cost
here. Exact versions live in [go.mod](../../go.mod) and the lockfiles of the
[console](../../web/console/package.json) and [site](../../site/package.json).
Tool versions live in `.node-version` and the workflows. Do not duplicate
versions or keep a task status table in this file.

## Decided

| Area | Choice | Reason |
|---|---|---|
| Core | Go; minimum declared in go.mod | Networking, TLS, concurrency and single-binary distribution |
| HTTP and proxy | stdlib net/http, crypto/tls, httputil.ReverseProxy | No additional HTTP framework |
| Public certificates | x/crypto/acme/autocert and an in-house DNS-01 manager on x/crypto/acme | Reuse the protocol; keep storage and renewal bounded |
| DNS-01: Cloudflare | Cloudflare API with net/http | Default adapter; no new modules |
| DNS-01: `builtin` | Minimal in-house authoritative DNS server (`internal/acmedns`) on `golang.org/x/net/dns/dnsmessage` and `net`; reuses `internal/ratelimit` | Issuance without a third-party account or API (acme-dns pattern inside the same process). No new modules. The wire format is checked against `dig` in a test that is skipped when `dig` is not installed |
| DNS-01: Hetzner | Hetzner Cloud DNS API (RRSets, `add_records`/`remove_records` by name and type, Actions) with net/http | Second managed provider, no new modules. Schema taken from the official OpenAPI spec; **the TXT quoting is not stated in it**, and it is isolated in `hetznerTXTValue` until tested against a real zone |
| DNS-01: RFC 2136 | Dynamic UPDATE signed with TSIG (RFC 8945) over TCP; `golang.org/x/net/dns/dnsmessage` and `crypto/hmac` | Independence from the DNS provider (BIND, PowerDNS, Knot). `x/net` was already an indirect dependency in go.mod and becomes direct: **zero new modules**. The in-house TSIG is checked against BIND's `nsupdate` in a test that is skipped when it is not installed; SHA-2 algorithms only |
| OIDC client | coreos/go-oidc and x/oauth2 | Issuer verification, tokens and flow; reused for CI |
| JOSE | go-jose/v4 | Signatures and JWTs with an existing library |
| Passkeys | go-webauthn/webauthn | Do not implement WebAuthn or its primitives from scratch |
| Persistence | modernc.org/sqlite, explicit SQL and embedded migrations | Local state without an external service or cgo |
| Tunnels | hashicorp/yamux over mTLS | Reused multiplexing; identity and permissions are Ozal's own |
| Configuration | Strict JSON, `file:`/`env:` secret references | Validation and persistence without resolving secrets to disk |
| Console | Svelte 5, TypeScript, Vite, Tailwind and bits-ui | Static SPA embedded in the binary; Node only at build time |
| Public site | Static Astro, Tailwind and an optional Go/WASM engine | Accessible content without a Node server |
| Typography | Self-hosted Geist Sans/Mono, OFL | Local assets and a CSP without font providers |
| CLI | Standard `flag`; separate `ozalctl` for SSH operation | Installation and automation; routine work from the console |
| Telegram / GitHub App | net/http adapters | Optional, referenced locally, minimal permissions |
| Audit | JSONL, SHA-256 and Ed25519 checkpoints | Local contract with explicit evidence limits |
| Tests | Native testing, httptest, race and fuzz | Regressions in decisions, adapters and boundaries |
| Release | GoReleaser, SHA-256 and SBOM with syft | Build tools; not runtime dependencies |

The console source is `web/console`; `scripts/build-console.sh` builds it into
`internal/admin/console`, which the Go binary embeds with `embed.FS`. Commit the
built output with its source. Vite generates external scripts compatible with
the project's CSP. The
administrative token lives in the tab's memory; after a reload, the edge returns
it from an `HttpOnly` cookie limited to its resume endpoint. SvelteKit or SSR is
not needed for this console. React with Vite would be technically possible, but
no demonstrated need justifies the migration.

The Linux CI and release jobs prepare dependencies with lockfiles and a pinned
Node version, and use `OZAL_REQUIRE_FRONTEND=1` to fail when frontend checks
are skipped. Having the job configured does not prove it ran successfully;
runner acceptance is tracked separately.

<a id="public-origin-certificate-contract"></a>

## Public certificate contract

The composition root connects `GetCertificate` to the listener. The ACME account
is separate per directory and environment. Complete cert/key generations are
validated and persisted before activation. There is one writer; renewal is
serial with timeouts and backoff; cleanup removes only our own TXT records by
ID and recovers its state. A valid certificate is kept if renewal fails; without
one, TLS fails closed.

A wildcard covers one label level; the apex needs an additional name. The
manager accepts the bounded set of names supported by the configuration. Do not
turn it into an arbitrary platform without a new decision. Details are in the
[engineering guide](../engineering.md#certificates). CertMagic remains an
alternative if maintaining the in-house manager costs more than it saves; it is
not a current dependency.

## Private renewal

The pilot uses Go crypto/x509 and Ed25519, SSH coordination and a systemd timer.
Keys are per host, CSRs are public, and rollback respects revocation. It needs
no Python or OpenSSL at runtime, and it does not yet provide renewal for any
fleet. The CA and its rollover are separate from the leaf certificates. See the
[contract](../engineering.md#tunnels).

## Decide when there is a need

Postgres or multi-edge state, QUIC, a WAF engine, a private CA or SSH, a mail
provider, metrics and tracing, and transparency-log primitives keep their
tracking in the internal trackers and the vision document. None is added merely
because a proposal mentions it. PowerDNS and a shared URL service are outside
the chosen standalone MVP.

We keep the preference for stdlib, explicit SQL and cgo-free dependencies. The
edge's access path must not depend on a mandatory hosted database. The
consolidated review recommends keeping Go, Svelte and SQLite, and prioritizing
measurement, recovery and external acceptance. It does not prove the absence of
vulnerabilities.
