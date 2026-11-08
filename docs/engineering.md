# Engineering guide

Current contracts and verification commands in one place. Read
[AGENTS.md](../AGENTS.md), [design](architecture/design.md) and
[stack](architecture/stack.md) before changing code. Active work, priorities
and done criteria are maintained by the project maintainer; the public
[work status](TO_IMPLEMENT.md) note explains where they are.

<a id="mantenimiento-de-documentacion"></a>
## Documentation maintenance

User guides are [installation](operations/download-install.md),
[applications](operations/first-application.md),
[administration](operations/administration.md) and [security](security.md).
Update the existing section with the code; do not open a new report, plan or
delivery note for each conversation, test or release. Pending state goes to the
maintainer's tracker, and version limits go to the release notes.

`docs/architecture/design.md` and `stack.md` keep the design rules and
dependencies. Identity contracts live in
[identity-login.md](architecture/identity-login.md) and
[host-sessions.md](architecture/host-sessions.md); the other short contracts are
here. Formats also belong to code and its regressions, not to repeated
implementation stories.

Temporary drafts and captures belong in ignored `docs/local/` or
`.local/docs-archive/`; neither is a build dependency or required to understand
a clean checkout. Never store secrets there.

Public status notes point to the maintainer. Detailed status, priorities and
acceptance records are maintained outside this repository. Do not duplicate
that private state or hide files needed to reproduce the product.

<a id="publicacion-autonoma"></a>
## Autonomous publication

`runtime_file` is the authority for active routes. A configuration edit does
not imply a DNS or deployment change; the operator reviews and applies those
operations explicitly. A durable state machine is needed only when an external
operation requires it.

Cloudflare is an optional integration, not a requirement. DNS-01 has adapters
for Cloudflare, Hetzner, RFC 2136 and a `builtin` challenge responder. Verify
portability with issuance and renewal through a non-Cloudflare provider. Test
TLS, allowed and denied access, revocation and recovery from an independent
operator and network.

**Identity assertion.** `assertion.Build` and `assertion.Check` are pure: claims for
one authorized person, and the verifier's rules (issuer, audience, subject, ID,
window with skew, lifetime at most 60 s). The proxy signs right after
authorization, only for routes with `identity_assertion`, and refuses to forward
when signing fails. The trust stage strips client copies in every spelling. The
route manager loads the key once per `key_dir`. Regression tests cover a forged
header, underscore spellings, another audience, routes without the field, a
missing signer and a failing signer.

<a id="identidad-y-aplicaciones"></a>
## Identity and applications

Native login, OIDC, expiry, groups and recovery are specified in
[identity-login.md](architecture/identity-login.md). The cross-domain handoff,
audiences, one-use codes, parent relationship and logout are in
[host-sessions.md](architecture/host-sessions.md). These contracts define
separate identity and session boundaries.

`Authorize` decides identity and groups; `Fresh` decides method, age and device.
A required group means AND with the other rules. Email allowlists need a
verified email. Unknown context keeps its source and freshness and fails closed
when the rule needs that evidence.

`policy explain` uses the same engine with a hypothetical identity and context:

```sh
ozal policy explain -config /path/config.json -host app.example.com \
  -email ana@example.com -verified -groups engineering -expect allowed
```

See `policy explain -h` for options; it does not prove OIDC, TLS, a real session,
health or transport. `policy replay` evaluates a candidate rule without
mutating the policy; `who-can` and `review` must not turn missing history into
"never used".

The app may keep its own login. Gateway headers are accepted only from its
authenticated path; the app keeps its internal authorization. Signed claims that
the app can verify, token exchange and Ozal as an OIDC provider are distinct
scopes and are not granted by passing headers. Token exchange and Ozal as an
OIDC provider are separate capabilities; this handoff does not implement them.

<a id="tuneles"></a>
## Tunnels

The integrated process uses `tunnel` plus `connector` routes; it requires a
migrated SQLite database and an active enrolment. Route changes reload snapshots
and keep sessions; the listener, subnet and identity files require a restart.
Offline returns 502 without TCP fallback. The console distinguishes the
connector connection from the application actually working.
[Onboarding](operations/administration.md#add-a-connector).

`tunnel edge|connect` are independent commands; `tunnel edge` serves an
anonymous route. Visitor policies need the integrated process. Both require
`-cert`, `-key`, `-ca` and a host; `connect` also requires a literal `-edge` and
a verified `-server-name`. `-tunnel-network` accepts only one RFC1918/ULA
subnet; the default is loopback. See `-h` for the exact options.

The connector sets `-root` or `-upstream` and can add up to eight hosts with
`-route HOST=root:DIR|upstream:URL`; fixed HTTP destinations stay on loopback
without credentials, path or query. The edge never chooses an arbitrary origin.
Standalone tunnel commands use ephemeral identities and loopback ports; they do
not provide a public ingress product or replace persistent credentials.

IP and subject/email travel as `X-Ozal-Visitor-*` only inside mTLS.
`TrustAuthenticatedTunnel` validates the values and turns them into upstream
identity; ordinary listeners remove every client assertion and the session
cookie. They are assertions authenticated by the tunnel, not signatures the app
can verify.

The current code shares 32 in-flight requests and keeps a 16-slot handshake
reservation for the whole session. Separate the handshake/session/stream budgets
before expanding capacity; do not advertise scalability without measured load.
Enrolment revalidation runs every five seconds; a registry error denies new
connections but keeps established sessions.

The renewal flow is a Linux command with SSH coordination and keys generated
per role on their host. Thirty-day leaves, a seven-day threshold, a durable
journal and an exclusive lock. It keeps previous identities and rolls back only
when the current fingerprint still belongs to the transaction; operator
revocation or replacement wins over rollback. A lost SSH response is
reconciled. It exposes no public CA signer. CA rollover is a separate operation;
do not replace its root while renewing leaves.

<a id="certificados"></a>
## Certificates

HTTP-01/autocert serves configured names and follows the route table. DNS-01
uses the in-house manager over `x/crypto/acme`; the adapters are Cloudflare,
Hetzner, RFC 2136 and `builtin`. A wildcard covers one label level, not the
apex; alternative names must be validated and present in the leaf. Do not issue
for any SNI received.

Keep the ACME account and the certificate key separate, persist complete
generations before the swap, use a single-writer lock and recover the last valid
generation. Validate the chain, key/names and validity against system roots or
explicit staging roots. Never mix staging and production state.

Renew according to the real fraction of remaining life, not fixed days; one
active attempt, timeouts, jittered backoff and respect for the provider's retry
budget. On failure keep a still-valid certificate; without one, TLS fails
closed. The handshake never waits for issuance. TXT cleanup removes only our own
record by identity; persist the intent and reconcile ambiguous creations.

Origin client authentication and proxy trust are independent of server TLS.
The autocert ALPN exemption does not apply to the DNS-01 listener. Emit safe
events without tokens or challenges. Verify renewal, failure, restart and
external operations in addition to first issuance.

<a id="publicacion"></a>
## Publishing

Builds run outside the edge, normally in GitHub Actions. A project associates a
site, hostnames and publishers; the dedicated endpoint receives artifacts and
does not run builds. The admin plane stays private. A publisher can be OIDC
Actions or a project-scoped token; GitHub App discovery is independent.

`internal/pages/workflow.Build` is the pure core: it validates
refs, relative paths and HTTPS origins, generates YAML without expressions from
inputs, and binds the content by SHA-256 to a deterministic branch. The template
separates build (`contents: read`, no OIDC) from deploy (`id-token: write`, no
checkout or repository execution). Actions and `ozalctl` are pinned by SHA; the
publisher uses the published ref `900003e224c785f698ec015945b43fb2456753fd`.
Pin changes require reviewing the template and its regressions.

`githubapp.AppStatus` also reports whether the installation covers all
repositories and its settings page (only a `https://github.com` link); the
repository listing adds it on page 1 when the installation matches the
configuration, and omits it if GitHub does not answer. GitHub always returns to
`/applications`; the console stores per tab (`sessionStorage`, one known route)
whether the operator left Integrations, and the router returns there before
processing the code or installation.

The admin shell derives repo/owner/ref/workflow from the saved project, checks
the installation and digest again before a write, and requires a reviewed plan
and the `publishing` tool. Saving trust and the proposal check the session again
after repository discovery: a revocation or expiry during that query prevents
the write. It records audit intent before calling the adapter; a required-audit
failure prevents the write. `githubapp.OpenWorkflow` requests a one-repo token,
verifies its IDs/name again, fixes the base commit, requires the file and branch
not to exist, and uses create-ref + create-content without a replacement SHA +
create-PR. It does not follow redirects, filters provider errors and never
retries or cleans an uncertain write. `github.workflow_proposed` is emitted only
after a valid PR; it does not mean deploy or a configuration change. Disconnect
or a concurrent mutation after dispatch does not undo a request already sent to
GitHub.

Regressions for generation, scopes, repository removal or movement, permissions,
altered plan or stale config, required audit, existing file/branch and partial
failures live in `internal/pages/workflow`, `internal/githubapp` and
`internal/admin`. `OZAL_WORKFLOW_BROWSER=1 go test ./internal/admin -run
TestWorkflowBrowserAcceptance -count=1 -v` runs the embedded SPA and real
handlers on loopback with simulated GitHub I/O and deployment data. It requires
`agent-browser`, `CHROME` and the browser libraries; `OZAL_BROWSER_ARGS` lets you
configure local arguments. `OZAL_WORKFLOW_ARTIFACTS` stores private captures. It
does not prove a real App installation, PR or build.

Connecting from the console: `githubapp.Manifest` is pure (origin without path,
validated account, private App without webhooks or events and only the four
permissions). The manifest `state` is random, one-use, bound to the session actor
and expires in one hour; a code with another `state` does not reach GitHub. The
conversion checks `publishing` again after the network call, writes the key with
`O_EXCL` and 0600, stores only the `file:` reference and discards OAuth/webhook
secrets. `installation_id: 0` means an App created without an installation:
`installationToken` rejects it. The installation is obtained with the App JWT
(`GET /app`, `/app/installations`, exactly one); the client cannot name it. The
wizard preview and the PR use `publishingPlan`, so the reviewed digest is the one
applied. `OZAL_WORKFLOW_BROWSER=1 go test ./internal/admin -run
TestGitHubWizardBrowserAcceptance -count=1 -v` covers the connect panel, return
from the installation, repository choice, workflow review and a single save
with trust and PR, with simulated GitHub and axe at four widths.

Explicit migration of an existing publisher uses `config.PlanProjectTrust` (pure
core) and an endpoint exclusive to the admin socket. It does not expand web-token
or App-discovery authority. It requires exactly one GitHub publisher, keeps the
other deployers and applies through `Manager.PutProject` with the reviewed
digest: validation, persistence and snapshot go through the same edge lock as
console edits. The CLI first saves a new 0600 file with only the previous GitHub
metadata, then confirms active trust. An indeterminate network result requires
reconciliation; it does not trigger a blind rollback.

OIDC requires issuer/JWKS/RS256, audience, short life, repo/owner IDs, ref,
workflow and allowed events (`push`/`workflow_dispatch`). A
`pull_request_target` ref does not by itself authorize a deployment. Replay by
`jti` is stored durably; a status GET does not consume the upload permission.
See [deployauth](../internal/pages/deployauth) and
[deployapi](../internal/pages/deployapi) for exact codes and endpoints.

Artifact handling authenticates, rate-limits and records the required intent
**before consuming the body**. Regular files, count and size limits, no
links/traversal, internal or secret paths rejected, an immutable manifest and
atomic activation. Site rollback activates a retained artifact; it changes no
DNS or database. `_headers` and `_redirects` are validated and applied but are
not served as public files.

The operator reuses `internal/ctl/artifact`: it packs a bounded snapshot with
`pages.Pack`, validates with `pages.Extract`, rejects private names, links and
known credential patterns before requesting OIDC. `internal/ctl/publish.CI`
separates the `TokenSource` port from the Actions HTTPS adapter; it denies
authenticated redirects, bounds responses and never retries an uncertain upload.
Public acceptance compares stable asset bytes and rejects the SPA fallback on
unknown paths. The workflow uses Go commands; `scripts/pages` keeps only
compatibility wrappers.

`internal/install` contains plans and filesystem/systemd adapters; the unit is
embedded and matches the documented example. The staging test installs the real
ELF under a prefix without accounts, root permissions or a running service.
Release scripts only translate arguments to the trusted Go binary named by
`OZAL_BIN`. `internal/cloudflare` contains pure plans and `certs` the HTTP
adapter; mutations are per hostname/rule, with a private backup and a read
before/after. Tests use local providers and authorize no real infrastructure
change.

Trust snapshots of an upload in progress can outlive a disconnect; do not
pretend that canceling from the browser reverts the server. An intent without an
outcome means an unknown result, not success or a proven rollback. Secret
references are not resolved back into config. Previews and generated PRs are
separate from deployment; GitHub Actions builds and deploys from a reviewed
workflow.

<a id="release-service-and-configuration-contract"></a>
## Release service and configuration contract

The console uses the edge's private API for applications, identities and state.
The release client can query GitHub or a signed per-platform catalog and
restricts download hosts. The edge authorizes and journals update jobs; a
separately installed root-owned executor downloads and applies only a plan
reviewed by an operator. The web edge receives no service credentials and does
not execute installation commands.

A catalog record carries explicit OS/architecture, commit, artifact
hash/size, catalog sequence and expiry, release notes, migrations and
compatibility constraints. The catalog document is signed with the same Ed25519
key pinned in each installation, and its checksums are verified again; a
checksum signature alone does not prove that a channel is current or that its
compatibility rules hold. Key rotation, minimum client version and local state
against rollback are part of that format. An old date or version, an invalid
signature, the wrong platform and an untrusted clock have explicit outcomes.
[TUF](https://theupdateframework.io/docs/metadata/) is a reference for expiry,
roles and trust; this implementation is not TUF and does not claim full
resistance to key compromise. Catalog queries are anonymous and must not include
installation identifiers, configuration or the installed version. A catalog
host sees the connection's source IP; retain only the operational data needed
to serve and protect the public catalog.

Publication and trust: a commit is the source of a candidate, not an order to
update every installation. CI builds from a fixed ref, applies the gate,
generates per-platform files and signs before announcing them. The operator
chooses the `stable` or `preview` channel, with unique ascending semver
versions within the channel, full commit and immutable artifacts. Do not order
only by `+<commit>`: build metadata does not change semver precedence. Never
announce `preview` as stable or replace an artifact under the same version.
Changing channel or downgrading requires an explicit decision, distinct from the
automatic rollback of the current job.

The `ozal.dev` catalog can be static JSON generated by CI, without a database or
remote execution service. Files can live in GitHub Releases or managed storage
under the domain; always download with size, time and redirect limits to exact
allowed hosts. The public runtime serves only metadata/files and never receives
the signing key. The domain name provides a stable address; authenticity comes
from the public key pinned in each installation through a trusted channel. Do
not accept a new key only because the queried catalog delivered it.

<a id="actualizar-desde-la-consola-o-la-cli-sin-scp"></a>
### Updating from the console or the CLI

`ozal update` implements the contract in the current tree. `-catalog URL` plus
`-channel stable|preview` verifies a per-platform signed JSON catalog (detached
Ed25519 signature, expiry, channel, OS/architecture, sequence, commit, archive
hash and size) and `-state FILE` stores the **highest verified sequence and
version**, even when the check installs nothing. `-plan FILE` writes the review
with installed and candidate versions, platform, size, migrations, notes and
hash; `-expect` and `-expect-sha256` refuse to install if the catalog changed,
and a reviewed version that is already installed finishes without touching
anything. A check without `-apply` does not download the whole archive.

`-lock FILE` takes a non-blocking flock shared by the CLI, the executor and
`ozal upgrade`. The child receives the descriptor through `ExtraFiles`, checks
that it belongs to the same regular file and keeps the same open file
description: if the parent dies during installation, the child keeps the flock.
Each process closes its descriptor without `LOCK_UN`; exclusion ends when the
last one closes. The descriptor is not accidentally inherited by migrations or
`systemctl`. Invalid descriptors or descriptors from another file are rejected
before reading the configuration. The regression kills the parent, verifies that
a second process is still blocked and checks release when the child exits. A
second concurrent update fails explicitly instead of competing for `.next`,
migration or rollback. The lock is not a security boundary against root: it is
exclusion to avoid corrupting an installation.

The executor (`ozal update-executor`) is a subcommand of the same binary started
by `ozal-update-executor.service`, separate from `ozal.service`. It runs as root,
reads a root-owned profile and offers only three operations over a private Unix
socket: check the signed catalog, install the reviewed plan and read the durable
job. The profile fixes socket, group, unit, binary, configuration, database,
account, key, catalog base URL, channel, state, plan, jobs and lock. It rejects a
profile, key, binary or binary directory writable by group or other accounts
and, when running as root, requires those files and directories to be owned by
root; a compromised `ozal` must not become root by replacing the executable.
The executor retains `NoNewPrivileges` and grants only ambient `CAP_SETUID` so
the upgrader can start migrations as the service account. The UID transition
clears that capability before the migration binary executes; the migration
process remains unprivileged.

The executor routes are not a generic dispatcher: they accept no paths, commands,
units, shell arguments or client URLs. A plan expires after ten minutes, is
consumed once, and the updater rechecks catalog, version and hash under the lock
before stopping the service. Jobs live in a JSON journal per ID with phases
(`reviewing`, `staging`, `verifying`, `installing`, `complete`) and outcomes
(`updated`, `rolled_back`, `failed`, `intervention_required`). An executor
restart marks the job as requiring intervention, unless another updater still
holds the lock; in that case it does not overwrite it. The web service still runs
without root.

Console permissions: `updates` remains a read permission. Installing requires a
separate `updates.install` tool that existing sessions do not inherit: a host
login without a tool list receives the default set without it, and the first
administrator (host bootstrap) receives all. `POST /_/api/updates/jobs` accepts
only an administrator session opened with a passkey and a passkey proof less
than five minutes old; it rejects `admin.token`, the host socket and logins
without a passkey account. If the proof expired, the console calls
`POST /_/api/admin/step-up/begin|finish`, which verifies an assertion from the
same administrator and does not open a new session. Every accepted attempt goes
through `recordMutationIntent` with the actor and emits `update.started`; host
errors are not forwarded to the console.

Implemented sequence for both the button and the CLI:

```text
Check catalog → review version and consequences → confirm plan
  → installation lock → download to private staging → verify signature/hash
  → validate config and compatibility → prepare recovery → stop Ozal
  → migrate if needed → replace binary → start
  → check admin health and the MainPID executable hash → updated
                             failure → restore binary and previous DB if migrated
                                   → verify restoration
                                   → rolled back or intervention required
```

Staging writes 32 KiB blocks and computes SHA-256 during transfer; it does not
hold the whole archive in the Go heap. It extracts only after checking
authenticated size and hash; ordinary failures remove the partial file and the
incomplete binary. Limits: 512 MiB for the archive and the binary; extraction is
bounded to 512 MiB plus 16 MiB for headers and other files. Small metadata keeps
its own in-memory limits. The systemd executor sets `TMPDIR` to the installed
binary directory, which is root-owned, writable by the executor and executable
even when `/tmp` is mounted `noexec`. A direct `ozal update` command uses
`TMPDIR` or `/tmp`; it must point to a private writable directory on an
executable filesystem. Staging may be on tmpfs, and the budget counts its
filesystem, not total host memory. Before installing it also requires
`version -json` of the verified candidate to identify Ozal and match the
reviewed version (a `v` prefix is tolerated; build metadata must match).

The budget checks available space (`statfs`, user-available blocks) before
downloading, before extracting and before creating `.next` or stopping the
service. Download: announced size, or the 512 MiB limit if GitHub does not
report one. Extraction: binary size from the tar header. Installation: new size
plus previous size on the binary filesystem; three times the sum of database and
WAL on the database filesystem, for snapshot, migration and restoration. Needs
that share a device are added and 64 MiB are reserved per filesystem, not per
file. A capacity query failure or an insufficient budget is rejected explicitly.
This does not reserve blocks: quotas, other writers and migration growth can
still cause ENOSPC; it is not a full-disk or power-loss test.

After starting, admin health and active state are not enough: the updater reads
`MainPID` from systemd and compares the SHA-256 of `/proc/<pid>/exe` with the
candidate. It checks that PID and inode do not change during the read. This
proves the exact reviewed image, even with builds of the same version. Rollback
verifies the previous hash the same way; if only health returns but the image is
not proven, the result is intervention required. An already-current `-apply`
also verifies the process before reporting success without changes; a check
without `-apply` remains metadata only. There is no new version endpoint and no
process information exposed on the public listener. The updater that starts the
operation also applies the budget before invoking the candidate, captures the
previous hash and verifies installed/running images after success or rollback. It
does not trust only the exit code of an older `upgrade`: if the candidate claims
success or rollback but that proof fails, the durable result is
`intervention_required`, without claiming an unverified restoration. Acceptance
must verify login and access to both allowed and denied protected applications.

Keep the `/usr/local/bin/ozal` link to the installed path; replacement happens at
the verified destination, with a rename on the same filesystem, preserving owner
and permissions. Configuration, runtime, keys, certificates and Pages data are
not regenerated or replaced by release defaults. Record the checkpoint and the
database compatibility policy; reverting only the binary is unsafe after
migrations. Do not promise a later manual rollback over a database that already
received new writes: it needs its own data-loss and restoration policy. Disk
failures, rollback and lack of space are reported as such, preserving evidence
for recovery.

Console experience: the existing toast opens "Review update", a dialog with the
installed and candidate versions, channel, platform, size, notes, migration
warning and the "Update" button. If the passkey proof is missing, it requests it
and retries once. It shows the phase and ID, one stable text per phase and an
unambiguous result: updated, restored, failed or intervention required; there
are no invented percentages or success for submitting a request. During restart
it shows "Waiting for Ozal to return" and keeps polling the journal by ID;
closing the dialog does not cancel the job. If the session was lost, it asks the
operator to sign in again. Offline, it distinguishes "could not check" from
"already updated"; normal traffic continues.

The journal separates job state from the live installed version. Acceptance
must also cover reconciliation after `SIGKILL` or power loss. The single-host
flow does not apply to the tunnel edge.

Acceptance: two published versions from fixed commits, signed for Linux/arm64,
installation A on a clean VM and update to B from the console; repeat from A
through the CLI without a checkout or SCP. Test altered signature/hash, wrong
platform, old/expired catalog, dropped network, double confirmation,
incompatible config, insufficient permissions/space, failure to start B,
restoration and reconciliation after interruption. Verify persistent state, the
binary actually running, login and an allowed/denied app. Do not apply the
single-host flow to the tunnel edge: update the edge and connector as a version
pair and verify every supported platform before offering that flow.

<a id="auditoria"></a>
## Audit

Activity (`events.Broker`) is bounded and can lose events. The durable JSONL
journal has sequence/hash/prev, an exclusive lock, private permissions and
persistence according to mode. `required` waits for append+sync before forwarding
or mutating; failure is 503. `best_effort` accepts loss and reports it through an
independent channel.

Format v1: each record includes `v`, `log_id`, `seq`, `time`, `type`, safe fields
and `prev`/`hash`. Exact bytes are hashed, not a universal canonical JSON. Domain
`ozal-audit-v1\x00`, big-endian uint64 sequence, decoded prev, big-endian uint64
length and compact JSON without the final hash field. The code in
[audit](../internal/audit) and its tests fix the interoperable encoding. Do not
log cookies, secrets, bodies, challenges or raw errors.

An Ed25519 checkpoint signs payload v1 with domain
`ozal-audit-checkpoint-v1\x00` and order `v, log_id, seq, hash, time, key_id`.
A checkpoint and a public key pinned outside the host's authority are required;
the tool verifies signature/chain, not that independence by itself. Do not call
it public transparency or protection against a compromised root.

A durable administrative intent records an attempt, not a commit. Publishing
adds an outcome without a common transaction across database, files and JSONL;
if the result is stored but the append fails, do not undo content by pretending
atomicity. Tests cover login, certificate and tunnel events, as well as journal
rotation, verification cadence and retention; these properties are not inferred
from a list of desired events.

**Access history.** The console's "Who got in" reads
`internal/accesshistory`, not the activity broker. When `audit.include` has
`access` and the journal opens, `audit.Options.Observe` feeds it every record
the journal holds while `Open` validates them, then each new record only after
its batch is synced; a record whose append or sync failed is never shown. The
history then survives restarts and says "from the durable access record". Without
access audit, or if the journal does not open, it holds what this process saw
and says so. It keeps the newest 5,000 decisions in detail and running totals
(outcomes, refusal codes, person-application pairs up to 10,000, with a flag
when capped) for all of them; older detail stays in the journal for
`ozal policy replay`. It is rebuilt from the journal on every start, so its
memory cost is bounded but its startup cost grows with the journal until
retention is configured. `GET /_/api/access` is read-only and needs any
operator session, like the event stream.

An incomplete tail is not repaired at startup: stop the writer, run
`audit repair` in plan mode and then `-apply`, and check the chain. Interior
corruption or an invalid hash is not accepted as a repairable truncated tail.
Offline verification and checkpoints respect the lock.
[Security](security.md#audit-and-privacy).

<a id="development-and-stack-criteria"></a>
## Development and stack criteria

The console uses Ozal's Umbral/Marfil identity with the same typography and
palette as the site. The authenticated shell is a typographic bar on desktop and
a modal directory on mobile: it keeps tools, URLs and focus while navigating;
Escape closes the menu and Tab stays contained. The arch marks the active space
without inventing service state. Overview uses route names in HTML to stay
readable when resized, separates connector state from an app test and shows
history limited to retained events, with site/change lists for mobile. Without a
session only the access screen and its mark are downloaded; navigation,
applications and administrative data are not exposed.

### Console asset and CSP contract

The console is a static SPA embedded in the Go binary. Keep scripts external
and self-host fonts and images. Its Content Security Policy is
`default-src 'none'; script-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'; form-action https://github.com; base-uri 'none'; frame-ancestors 'none'`.
The narrow `style-src-attr` exception lets Radix position floating controls;
`style-src` remains self-only, so injected style elements stay blocked. Modal
confirmation uses the browser's native dialog and dropdown menus do not lock
page scrolling. Browser acceptance checks that these interactions produce no
CSP violations. Do not broaden the policy to `style-src 'unsafe-inline'`. Keep
Vite's `assetsInlineLimit: 0` so fonts remain same-origin, and do not inline
scripts. Go regression tests check the policy on console routes. The frontend
contract records the build, routing, caching and parity requirements.

The stack decision lives in [stack.md](architecture/stack.md). The Go binary
embeds a static console with no Node runtime. WebAuthn, OIDC, JOSE and yamux are
reused instead of reimplementing their primitives; local SQLite avoids another
service. Multi-edge shared state or measured contention may justify another
database. Capacity claims require representative load measurements. Keep
authorization, snapshots and revocation decisions covered by regressions, and
extract proxy, config or admin responsibilities when those packages are
touched.

<a id="verificacion"></a>
## Verification

`scripts/verify.sh` is the gate before reporting a change as complete. It runs
gofmt, `go vet`, race tests, both builds, short fuzz targets, optional
staticcheck/govulncheck, the console type check/rebuild consistency check,
passkey page tests and site tests when their toolchains are installed. Focused
checks:

```sh
go test -race ./...
go test -race ./internal/auth -run TestName
go test ./internal/config -fuzz FuzzName
```

Browser and integration fixtures used by the project:

```sh
node scripts/demo/product-acceptance.mjs
node scripts/demo/passkey-acceptance.mjs
OZAL_BIN=/path/binary node scripts/demo/update-acceptance.mjs
node scripts/demo/host-sessions-acceptance.mjs
node scripts/demo/visitor-passkeys-acceptance.mjs
CHROME=/path/chrome node scripts/demo/https-host-sessions-acceptance.mjs
node scripts/demo/site-check.mjs
python3 scripts/smoke-tunnel-configured.py --binary /path/test-binary
python3 scripts/rehearse-coordinated-upgrade.py \
  --previous /path/previous-binary --candidate /path/candidate-binary
```

`update-acceptance.mjs` checks the dialog on desktop and mobile, Escape and focus
return, axe and polling resumption when a pending job is reopened. It uses a
disposable edge and simulated update HTTP responses; it installs no artifacts and
proves neither MFA nor a real VM update. Authorization and the executor are
covered by their Go tests.

Fixtures need their own dependencies and browser; use `OZAL_BIN` and `CHROME`
where required and read the script for prerequisites. They run local fixtures;
localhost HTTP does not prove public HTTPS or other browsers. Do not install
extra tools implicitly for an example.

The coordinated upgrade rehearsal requires two trusted binaries on both sides of
the ALPN change. Build each ref separately from `git archive` with
`go build -buildvcs=false`, recording ref and SHA-256; it does not modify Git or
download releases. The chosen "previous" is a protocol reference, not evidence
of the version installed on any deployment. Use loopback and state generated in a
private temporary directory. Test previous pair, both mixtures (persistent 502),
candidate pair, publication before/after, SQLite restore to a new path, runtime
and Pages from the checkpoint, restored pair and a new update after rollback.
Check that a protected app asks for login, the login page responds and forged
headers/cookies never reach the origin. It does not test browser authentication,
systemd/SSH, real key backup, ACME or public publication. Measured time is fixture
recovery, not the server's RTO. The normal gate does not need historical
artifacts: run this rehearsal before a protocol change, in addition to
`scripts/verify.sh`.

The HTTPS host-session acceptance requires Linux, Go, `agent-browser`
with `--ca-cert` support, `certutil` (package `libnss3-tools`) and the
`playwright-core` already pinned in `scripts/demo`. It builds a test executable
from `cmd/ozal`, uses the production autocert configuration with preloaded local
leaves, real routing, WebAuthn and a migrated SQLite database. All listeners use
ephemeral loopback ports; the fixture administration exists only in `_test.go`.
Close stdin to stop it and remove temporary state/certificates. The CA is imported
only into that agent-browser session's temporary NSS store; it does not change
the user's trust and does not ignore TLS errors. The test checks that Chrome first
rejects the untrusted CA and, after trusting it, accepts login and two apps on
different sites: host-only `Secure`, `HttpOnly`, `SameSite=Lax` cookies, browser
binding, code audience, stolen and consumed code, denied policy, cookies removed
before the upstream, logout at both ends, disabled user, revocation and parent
expiry. Expiry is triggered only in fixture rows; production rules are not
shortened. It includes a mobile viewport with touch and keeps the shared cookie of
a host under `cookie_domain`. Mobile emulation does not prove a real phone. Two
tabs on the same host start handoffs at once and redeem their codes in reverse
order: each keeps its destination and clears only its binding cookie. The driver
aborts an intercepted navigation before redeeming the code, without creating
artificial documents or blocking favicons. It also checks that a binding copied
with `Path=/` does not reach the upstream. Go regressions reproduce the original
conflict and prove that one failed redemption does not stop the other, together
with cookie filtering on direct and connector routes.

The normal `TestDirectHTTPSProfileWithoutCloudflare` validates the direct
template and serves a cached certificate with verified TLS trust. These tests
**do not issue or renew public certificates**, prove DNS propagation or
firewall configuration, or exercise the full distribution binary. External
validation requires a public CA, another network, a phone, renewal, restart and
recovery; the public connector has a separate acceptance boundary.

For browser QA: 1440/1024/390/320 px, keyboard, focus/Escape, cancellation
without mutation, errors/retry, stale data and logout without residual data. Test
passkeys per device, loss/revocation, callbacks and cross-host handoff. On the
public site check without JS, a failed WASM load and Save-Data. Store captures in
a temporary/ignored directory, not in product documentation.

For failures: own state, ephemeral ports, fictitious secrets, restart/kill, slow
or 5xx origin, expiry, full/read-only disk, clock and recovery. Keep concise,
reproducible verification evidence with the relevant change. Fixture tests do
not prove real DNS/provider, release signatures or an external restore.

Example benchmark, on a quiet host with a constant environment:

```sh
GOMAXPROCS=1 go test -run '^$' -bench 'Benchmark(Authorize|Fresh|Proxy)' \
  -benchmem -benchtime=1s -count=5 ./internal/auth ./internal/proxy
go test -run '^$' -bench '^BenchmarkLimiter' -benchmem ./internal/ratelimit
go test -run '^$' -bench '^BenchmarkWriterAppendGrouped' -benchmem ./internal/audit
```

Proxy benchmarks use a synthetic transport. Required, serial audit persistence
costs far more per request than the proxy path without it; that cost is the
persistence, not public throughput or p99. Measure listener/TLS, SQLite, audit and tunnel together with
representative storage, saturation and sustained load before declaring capacity.

Property/fuzz and mutation tests must detect rule changes, not copy the
implementation. The differential harness requires Caddy/nginx installed; tests
that skip that dependency do not prove interoperability.

<a id="revisión-estática-de-seguridad-capacidad-y-código-sin-uso"></a>
