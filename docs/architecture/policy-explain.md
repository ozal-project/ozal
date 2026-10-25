# Policy explain

**Status:** implemented 2026-10-20 (`cmd/switchboard/policy.go`,
`auth.Explain`); see `IMPLEMENTED.md`. This file is the original design record;
the current CLI also reports sourced context and supports hypothetical session
and request-method evidence for configured context rules. Tracker:
`ARCH-008` in [TO_IMPLEMENT.md](../TO_IMPLEMENT.md). This is not a release
promise or an end-to-end access test.

Related: [`auth.Authorize`](../../internal/auth/authz.go),
[`config.AccessPolicy`](../../internal/config/access.go),
[`config.Route`](../../internal/config/config.go), the
[`validate` command](../../cmd/switchboard/validate.go), and the later
`UI-002` policy simulator.

## Goal

A small CLI that answers: **given a route and an example authenticated
Principal, why would Switchboard allow or deny the request?**

It must use the existing pure `auth.Authorize`; no second policy engine. The
value is a stable, explainable decision before deployment or while debugging a
denial, without binding listeners or starting an OIDC flow.

## What it proves and what it does not

It evaluates the pure authorization function for supplied identity and
optional hypothetical context inputs: identity allowlists, verified email,
required groups, session evidence and request-method classification. It prints
the resulting stable code and reason.

It does **not** prove OIDC discovery, the login redirect, session creation or
lookup, cookie attributes, TLS, trusted-proxy IP resolution, rate limiting,
Shield, tunnel identity or the upstream response. The command must print that
scope explicitly. It is a diagnostic tool, not an end-to-end access test and
not a security proof.

## Semantics to preserve

- `Route.Access == nil` and `AuthRequired == false`: public route; no access
  policy.
- `Route.Access != nil` or `AuthRequired == true`: authentication required.
  `AuthRequired` is the legacy shorthand for an empty access policy.
- `AccessPolicy{}` (empty): any authenticated Principal may pass.
- `AllowedEmails` / `AllowedDomains`: OR; if either is set, the Principal's
  email must be verified. Email matching is case-insensitive. Domain matching
  is exact after the last `@`; it never uses a suffix, so `evilexample.com`
  does not match `example.com`.
- `RequiredGroups`: AND; every configured group must be present. Names are
  case-sensitive.
- `auth.Authorize` does not produce `authentication_required`; that is a Gate
  decision made before Authorize. The CLI must not fake it by passing an empty
  Principal.

## CLI

```bash
switchboard policy explain -config config.json \
  -host app.example.com \
  -email ana@example.com -verified -groups engineering,oncall
```

Flags:

| Flag | Meaning |
|---|---|
| `-config` | Routing config to load (default `config.json`). It never prints resolved secrets. |
| `-host` | Route host, normalized with `config.NormalizeHost` (case, trailing dot and port are handled the same as routing). Required. |
| `-email` | Principal email for an authenticated simulation. Required for a protected route unless `-no-session` is used; optional for a public route. |
| `-verified` | Sets `Principal.EmailVerified`. |
| `-groups` | Comma-separated groups; empty or omitted means none. |
| `-method` | Hypothetical login method: `oidc` or `passkey`; used by configured context rules. |
| `-device-bound` | Hypothetical non-syncable passkey evidence; requires `-method passkey`. |
| `-user-verified` | Hypothetical WebAuthn user-verification result (`true` or `false`); requires `-method passkey`. Omitted means unknown. |
| `-device-first-seen` | Hypothetical one-time new-passkey evidence (`true` or `false`); requires `-method passkey`. Omitted means unknown. |
| `-request-method` | Hypothetical supported HTTP method; supplies the input for `action_risky` context rules. |
| `-no-session` | Simulates an unauthenticated request at the Gate level; prints `authentication_required` for protected routes and never calls `Authorize`; for a public route it still reports the public decision. Mutually exclusive with principal, session and request evidence flags. |
| `-json` | Stable machine-readable output. |
| `-expect` | Optional `allowed`, `denied` or `step_up`; exits non-zero if the decision differs. |

Exit codes:

- `0`: evaluation completed (allowed or denied) or `-expect` matched.
- `1`: invalid input, unknown route, config error or `-expect` mismatch.

### Examples

Allowed:

```text
route: app.example.com (protected)
policy: allowed_domains=[example.com] required_groups=[engineering]
principal: ana@example.com verified=true groups=[engineering,oncall]
decision: allowed
code: allowed
reason: allowed
scope: authorization only; supplied session evidence is hypothetical, and OIDC, TLS and transport are not evaluated
```

Denied by group:

```text
route: app.example.com (protected)
policy: allowed_domains=[example.com] required_groups=[engineering]
principal: ana@example.com verified=true groups=[support]
decision: denied
code: missing_required_group
reason: account is missing required group engineering
scope: authorization only; OIDC, session and transport are not evaluated
```

Anonymous protected route:

```text
route: app.example.com (protected)
gate: authentication required before Authorize is called
scope: gate decision only; no OIDC flow was started
```

Machine output (`-json`) should expose stable fields, for example:

```json
{
  "route": "app.example.com",
  "protected": true,
  "policy": {
    "allowed_domains": ["example.com"],
    "required_groups": ["engineering"]
  },
  "principal": {
    "email": "ana@example.com",
    "email_verified": true,
    "groups": ["support"]
  },
  "decision": {
    "allowed": false,
    "code": "missing_required_group",
    "reason": "account is missing required group engineering"
  },
  "scope": "authorization_only"
}
```

The exact JSON field names become a small public contract; keep them stable
and version them if they change.

## Original implementation outline

This section records the first implementation plan. The code now also carries
typed context evidence and supports the flags above; follow the tracker and
README for the current behavior.

- Add `cmd/switchboard/policy.go` with `runPolicyExplain(ctx, args, out) error`
  and wire it into the existing subcommand dispatcher in `main.go`. Follow the
  style of `validate.go`.
- Load the config with `config.Load` (validation only; no listener, no secret
  resolution and no secret printing). Normalize the host and find the matching
  route. Config validation already rejects duplicate normalized hosts, so the
  lookup is unambiguous.
- Build an `auth.Principal` from the flags.
- If `-no-session` and the route requires auth, print the Gate-level
  `authentication_required` result without calling `auth.Authorize`. If the
  route is public, `-no-session` still reports the public decision.
- Otherwise call `auth.Authorize(principal, policy)` where `policy` is
  `Route.Access`, or the zero `config.AccessPolicy{}` when only
  `AuthRequired` is set. A public route can be reported as "public; no access
  policy" and still render the pure `allowed` decision.
- Render human text or JSON. Keep the scope line in both formats.
- Do not add a new policy package or duplicate allowlist/group logic. If a
  reusable function is needed later, it should live next to `Authorize` and be
  covered by the same tests.

## Tests

Table-driven tests at the command boundary:

- Public route: no authentication, no access policy.
- Protected route with empty policy and a Principal: allowed.
- Protected route with `-no-session`: `authentication_required` and no
  `Authorize` call.
- Allowed email, denied email, unverified email with an allowlist.
- Allowed domain, exact-domain rejection (`evilexample.com`), case handling.
- Required groups: all present, one missing, case mismatch.
- Multiple groups: AND, not OR.
- Host normalization: case, trailing dot, `host:port`, unknown host.
- JSON output fields and stable scope string.
- A config containing secret references: output never contains the reference
  or a resolved value.

The command should not bind listeners. A normal `go test ./cmd/switchboard`
and `scripts/verify.sh` are the gate.

## What this is not

- It is not a login simulator and does not start an OIDC flow.
- It is not a full gate test; session and transport need separate integration
  tests.
- It is not a policy-language implementation. If the policy model changes,
  this tool must call the same pure decision function, not fork the rules.
- It is not a replacement for `UI-002`; it is the small CLI that can exist
  first and later feed the UI.

## Acceptance criteria

- The command evaluates an existing config with all documented policy
  combinations and prints `Decision.Code` and `Decision.Reason`.
- It never prints resolved secrets and never binds a listener.
- `-no-session` is clearly separated from `Authorize`.
- JSON output is stable and tested.
- The help text states the scope limit: authorization only.
- `scripts/verify.sh` passes.

## Implementation boundaries

The CLI adapter owns flags, route lookup, `auth.Principal` construction and
output formatting. The security-sensitive rule is to call `auth.Authorize` exactly as the proxy does
and to keep `authentication_required` at the Gate level. A regression test
for the denied-group case must verify that behavior; the implementation must
not change `auth.Authorize` to make it pass.
