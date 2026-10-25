# Selected OWASP ASVS 5.0.0 Level 2 subset mapping

**Status:** initial mapping for `LAB-003`, written 2026-10-25. OWASP ASVS
5.0.0 is the stable version used for this snapshot. A Level 2 scope includes
applicable Level 1 and Level 2 requirements; the row labels retain each
requirement's assigned level. This table is not an assessment against all
ASVS requirements and does not claim Level 2 conformance. It selects
requirements that touch Switchboard's trust boundaries and separates
source/test evidence from missing deployment or independent verification.

Source: [OWASP ASVS releases](https://github.com/OWASP/ASVS/releases)
and the [official English requirements for the `v5.0.0_release` tag](https://github.com/OWASP/ASVS/blob/v5.0.0_release/5.0/docs_en/OWASP_Application_Security_Verification_Standard_5.0.0_en.xml).
Requirement identifiers and level intent are from that version.

| ASVS requirement (level) | Switchboard evidence | Status and remaining evidence |
|---|---|---|
| `V4.1.3` (L2) — API and Web Service: intermediary identity/IP headers must not be end-user-overridable. | `internal/pipeline/client_ip.go`, `internal/pipeline/pipeline.go`; tests `TestTrustIsOptInAndRejectsAmbiguousAliases`, `TestVisitorAssertionIsNeverTrustedOnOrdinaryListeners`, and proxy raw-socket spoofing coverage. | **Source/test evidence exists.** Deployment proxy ranges and public-origin direct access still need the `EDGE-006` acceptance path. |
| `V5.2.3` (L2) — File Handling: bound archive expanded size and entry count before extraction. | `internal/pages/extract.go`, upload limits in `internal/admin/sites.go`; `TestExtractRefusesHostileArchives` and `TestAnUploadOverTheCapKeepsItsCause`. | **Source/test evidence exists** for implemented archive caps and hostile paths. Full ASVS verification would also inspect every supported archive/content form and deployment storage quotas. |
| `V6.3.1` (L1) — Authentication: credential-abuse controls follow documented policy. | `internal/pipeline/ratelimit.go`, passkey gate and configured login limiter. | **Partial.** Switchboard supports rate limits and passkey login; password guessing at an external IdP is outside this code. Measure the actual login endpoint and document thresholds, lockout behavior and alerting before claiming coverage. |
| `V7.2.4` (L1) — Session Management: rotate the session token on authentication and re-authentication. | `internal/auth/session.go`, `internal/auth/oidc.go`, `internal/passkey/gate.go`; tests `TestLoginRecordsAuthTimeAndRotatesTheSession`, `TestFullLoginCallbackFlow`, and `TestSessionIDsAreUnique`. | **Source/test evidence exists** for implemented login/session flows. Cross-host cookie attributes and real-browser behavior remain manual acceptance. |
| `V8.2.1` (L1) — Authorization: restrict function access to explicitly permitted consumers. | `internal/auth/authz.go`, `internal/config/access.go`, `internal/proxy/proxy.go`; `TestAuthorize`, proxy claim-denial and matching-claim tests. | **Source/test evidence exists** for route-level policy in this process. It does not verify authorization inside the upstream or prevent direct origin bypass. |
| `V10.4.6` (L2) — OAuth/OIDC authorization servers require PKCE for code grants. | Switchboard is an OIDC relying party, not the authorization server. `internal/auth/oidc.go` sends S256; tests `TestS256ChallengeIsDeterministic` and `TestFullLoginCallbackFlow` exercise the client-side flow. | **External prerequisite, not a Switchboard authorization-server pass.** Verify the configured provider enforces PKCE and review the relying-party flow against its applicable ASVS client requirements. |
| `V11.5.1` (L2) — Cryptography: generate non-guessable values using a CSPRNG with adequate entropy. | `auth.NewSessionID` uses 32 bytes from `crypto/rand`; OIDC state/verifier and audit IDs also use cryptographic randomness. | **Source evidence exists.** Maintain a full cryptographic inventory and verify key generation/rotation policy as `AUD-002`, `LAB-010` and PKI scope mature. |
| `V12.3.2` (L2) — Secure Communication: TLS clients validate server certificates. | Go TLS defaults in upstream/ACME clients; certificate chain verification in `internal/certs`; `EDGE-007` fixed-root and invalid-chain tests. | **Partial.** Tests cover local adapter paths; real external staging/production lifecycle and every outbound client need separate evidence. |
| `V13.2.1` (L2) — Backend Communication Configuration: authenticate backend communications that do not use the application session. | Admin TCP mutations require a bearer; the admin Unix socket relies on local OS access; connector enrollment and tunnel traffic use mTLS. The in-process SQLite adapter is not evidence of authenticated service-to-service communication. | **Partial.** Repository tests cover these specific paths. Host socket ownership, secret file access, network segmentation and any external databases require deployment evidence. |
| `V14.1.2` (L2) — Data Protection: document protection requirements for classified sensitive data. | `docs/architecture/audit-contract.md` describes event content; audit files use mode 0600/0700; access reports expose only per-run keyed fingerprints. | **Partial.** Define retention and privacy treatment for IPs, opaque subjects, hostnames and reasons before broadening durable event coverage. The separate higher-level retention-deletion requirement `V14.2.7` (L3) remains unimplemented. |
| `V15.1.2` (L2) — Secure Coding and Architecture: maintain a software-component inventory such as an SBOM and use trusted, maintained repositories. | `.goreleaser.yaml` generates an SPDX SBOM for each release archive using Syft; Go modules are pinned in `go.mod`/`go.sum`, and `govulncheck` runs in `scripts/verify.sh`. `REL-005` records release evidence. | **Partial.** SBOM generation is implemented, but this mapping does not verify the SBOM contents for a current release or establish the ongoing repository-trust and remediation policy. The broader supply-chain lab remains under `LAB-010`. |
| `V16.5.3` (L2) — Error Handling: fail securely rather than continue after a security-sensitive failure. | Required audit append rejects protected forwarding; config/auth checks fail closed; `internal/proxy`, `internal/auth`, and `internal/audit` failure regressions. | **Source/test evidence exists for selected failure paths.** `LAB-002` still needs slow/5xx, disk-full/read-only, clock-skew, expiry and measured recovery drills. |

## Reading this table

- **Source/test evidence exists** means the named code and repository tests
  exercise that slice. It does not mean an independent L2 audit has verified
  every condition in the requirement.
- **Partial** means the code provides some relevant behavior, while the
  remaining acceptance depends on deployment, broader coverage or a missing
  lifecycle.
- **Gap** means no current evidence satisfies that selected requirement.
- No row is a severity-rated security finding. Gaps are backlog items, not
  confirmed vulnerabilities.

The full standard has substantially more requirements, including browser
security, privacy, API behavior, software supply chain and operational
configuration. Continue the matrix only alongside a scoped test plan and
evidence; do not turn this sample into a compliance badge.

Switchboard's context controls are relevant to `V8.2.4`, but that requirement
is Level 3 in ASVS 5.0.0 and is intentionally excluded from this Level 2
subset. The feature's existence is not evidence of Level 3 coverage. `V10.4.6`
is included only to make the external OAuth authorization-server boundary
explicit; Switchboard is an OIDC relying party, so this row is not evidence
that Switchboard implements or passes the authorization-server requirement.
