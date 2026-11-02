# Administer an installation

To install or use the console, follow [installation](download-install.md) and
[first application](first-application.md). This guide covers operation, backups,
connectors and publishing. Available features depend on the installed version.

<a id="servicio-y-propiedad-del-estado"></a>

## Service and state ownership

The Linux service runs under its own account. The binary and host configuration
belong to root; the state belongs to the service account.

| Resource | Recommended path | Owner / permissions |
|---|---|---|
| Binary and previous versions | `/usr/local/lib/ozal/` | root, 755 |
| Host configuration | `/etc/ozal/config.json` | root, 644 if it only contains references |
| Credential files | `/etc/ozal/*.token` | root:ozal, 640 |
| SQLite, Pages, runtime and ACME | `/var/lib/ozal/` | ozal, directory 700 |
| Administrative socket | `/run/ozal/admin.sock` | ozal, 600 |

The [installer](download-install.md#3-install-the-linux-service)
prepares the service. The [example unit](ozal.service) documents its
permissions. Never let an unprivileged user replace the binary, configuration or
state, because that would grant access at the next restart.

With `runtime_file`, the console stores routes, projects and the GitHub App
reference in a service file. Listeners, TLS and authority stay in the host
configuration. Initialize it once before starting:

```sh
sudo -u ozal /usr/local/lib/ozal/ozal runtime init \
  -config /etc/ozal/config.json
```

The destination must be new. A missing or invalid runtime is then rejected; old
policies from the root file are not silently recovered. Include it in backups.
A failed reload keeps the previous valid snapshot. `config_sha256` in the
authenticated state identifies the effective configuration. Changing listeners,
tunnel identity or `runtime_file` requires a restart.

<a id="legacy-switchboard-names"></a>

## Legacy Switchboard names

Installations made with the Switchboard releases keep working with the legacy
names, which Ozal still accepts for compatibility:

- Variables: `SWITCHBOARD_CONFIG` and `SWITCHBOARD_ADMIN_TOKEN`. The `OZAL_*`
  variables take priority when both are set.
- Paths: `/etc/switchboard/config.json` and `switchboard.db`. `migrate` reuses
  `switchboard.db` when it exists and `ozal.db` does not.
- Hosts: `switchboard.localhost` keeps its existing passkeys.

Use the real `-config`, `-db` and `-unit` values of your installation with the
upgrade procedure below. Do not run the new-install installer over an existing
installation: it rejects the legacy layout to avoid creating two services.

Upgrading across the protocol change is **not backward compatible**:

- Update the edge and its connector together. Mixed versions do not negotiate the tunnel ALPN.
- OIDC login cookies issued before the change stop validating, so visitors sign in again.
- An audit log written before the change does not verify with the new binary, because the
  domain is part of each hash. Archive it with its previous binary and start a new log.
- Legacy `X-Switchboard-Visitor-*` headers are still stripped from client input.

Cookies `sb_*`, native `sb:` subjects and SQL migrations are unchanged.
For the release that carried these names, see the
[alpha.1 notes](../releases/v0.1.0-alpha.1.md).

## Upgrade and roll back

`ozal upgrade` runs from the **new binary**, verified and copied to a directory
owned by root. Substitute the SHA and paths with checked values. Never run as root
a file that another account can modify.

```sh
sudo /root-owned/path/ozal-new upgrade -unit ozal \
  -binary /usr/local/lib/ozal/ozal -sha256 EXPECTED_SHA256 \
  -config /etc/ozal/config.json \
  -db /var/lib/ozal/state.db -user ozal
```

It validates, stops the unit, backs up and migrates if there are migrations,
keeps the previous binary, and waits for the service to be active and the
administration API to respond. On failure it restores the binary and, if it had
migrated, the previous database. The change causes a brief interruption; high
availability is not promised. The hash is checked against the value you supply.
It does not by itself authenticate the download channel.

| Exit code | Result |
|---|---|
| 0 | New version active, or it was already installed |
| 2 | Rejected before the service was stopped |
| 3 | Failed, and the previous version responds again |
| 4 | The previous version was restored but does not respond: intervention needed |

Backups are not deleted automatically. A previous version may reject a new
schema, so reverting only the executable can be insufficient.

<a id="actualización-automática"></a>

### Automatic update

`ozal update` checks for the newest published version. With `-apply` it installs
on **one host**, using `ozal upgrade` and its rollback. It only accepts a release
whose `checksums.txt` is signed (Ed25519) with the key the host pins in
`/etc/ozal/release.pub`. It checks the archive hash before extracting the binary,
and never installs a version equal to or older than the installed one. Drafts do
not count; prereleases only with `-prerelease`.

```sh
sudo install -m 0644 release.pub /etc/ozal/release.pub
sudo /usr/local/lib/ozal/ozal update -public-key /etc/ozal/release.pub \
  -unit ozal -binary /usr/local/lib/ozal/ozal -config /etc/ozal/config.json \
  -db /var/lib/ozal/state.db -user ozal \
  -status /var/lib/ozal/update-status.json           # reports; -apply installs
sudo install -m 0644 docs/operations/ozal-update.service docs/operations/ozal-update.timer /etc/systemd/system/
sudo systemctl enable --now ozal-update.timer         # checks availability nightly
```

This command runs on the host and downloads from GitHub: it needs neither the
checkout nor SCP. Without `-apply`, it verifies the catalog and the signed
checksums, but no longer downloads the full archive. `-plan FILE` records the
review (version, platform, size, migrations and hash) without installing.
`-expect` and `-expect-sha256` bind an installation to the reviewed plan and
reject it if the catalog changed. A shared lock (`update.lock` next to the
binary by default) prevents the CLI and the executor from manipulating `.next`,
the migration or the rollback at the same time.

The download is written to disk and checked before it is extracted or run. The
updater checks free space before each phase: the compressed archive (or up to
512 MiB if the size is unknown), the extracted binary, the new and previous
copies, and a margin for the database and WAL snapshot, migration and restore.
Needs are added per filesystem, and 64 MiB is kept in reserve per device. This is
not a block reservation against other writers. If capacity cannot be checked, or
space is short, the update refuses before that phase. After starting, it requires
that the `MainPID` runs the verified image and answers health checks; the same
applies to confirming a rollback. This is not acceptance of a real application or
login.

The public key must come from a trusted channel and be protected from changes by
the service account. A published signed release and HTTPS connectivity are
required. A build sent by commit does not create that channel.

Ozal's own channel uses a signed JSON catalog per platform. The URL includes the
channel, OS and architecture. The client verifies the document's Ed25519
signature, expiry, platform, sequence and the checksums before downloading:

```sh
sudo /usr/local/lib/ozal/ozal update -public-key /etc/ozal/release.pub \
  -catalog https://ozal.dev/api/v1/releases/stable/linux/arm64 -channel stable \
  -state /var/lib/ozal/update-catalog-state.json \
  -plan /var/lib/ozal/update-plan.json \
  -unit ozal -binary /usr/local/lib/ozal/ozal -config /etc/ozal/config.json \
  -db /var/lib/ozal/state.db -user ozal -status /var/lib/ozal/update-status.json
```

The check stores the **highest verified sequence**, even if nothing is installed,
and compares the next catalog against it. This way an older signed catalog cannot
reintroduce an older version. A plan expires after ten minutes and is consumed
once. Do not change `-api` to `https://ozal.dev`: the GitHub adapter and the
catalog adapter use different formats.

The console can install the same plan if the host configures the local executor.
Without `admin.update_socket` the console only checks. With the socket, the
install path requires an administrator session with the separate `updates.install`
tool, a passkey confirmed within the last five minutes, and it rejects
`admin.token` and the host socket. The attempt is recorded in the administrative
audit and emits `update.started`. An API password does not replace the passkey,
because replacing the binary as root is a personal action.

The executor is a separate service, not a child of `ozal.service`. It runs as
root, reads a profile that only root can write, and only accepts requests to
review, install the reviewed plan and read the job. The profile fixes all paths
and the base URL. The executor also checks that the binary, its directory, the key
and the profile itself are not writable by group or others.

```json
{
  "socket": "/run/ozal-update/executor.sock",
  "socket_group": "ozal",
  "unit": "ozal",
  "binary": "/usr/local/lib/ozal/ozal",
  "config": "/etc/ozal/config.json",
  "db": "/var/lib/ozal/state.db",
  "user": "ozal",
  "public_key": "/etc/ozal/release.pub",
  "catalog_base": "https://ozal.dev/api/v1/releases",
  "channel": "stable",
  "status": "/var/lib/ozal/update-status.json",
  "state": "/var/lib/ozal/update-catalog-state.json",
  "jobs": "/var/lib/ozal/update-jobs",
  "lock": "/usr/local/lib/ozal/update.lock"
}
```

```sh
sudo install -m 0600 -o root -g root update-executor.json /etc/ozal/update-executor.json
sudo install -m 0644 docs/operations/ozal-update-executor.service /etc/systemd/system/
sudo systemctl enable --now ozal-update-executor.service
# En /etc/ozal/config.json: "admin": {"update_socket": "/run/ozal-update/executor.sock"}
sudo systemctl restart ozal
```

The group of the socket must include the service account (`Group=ozal` or
`SupplementaryGroups`). On `SIGTERM` the executor waits for a job in progress; do
not start it from `ozal.service` or with `KillMode=control-group`. A job
interrupted by a crash is marked `intervention_required` unless another updater
still holds the lock; in that case nothing is touched.

Catalog publication and edge installation are separate operations. Before
offering an update, validate the signed catalog, installation and recovery on a
clean VM; local tests alone do not prove that a host can update safely.
Installing from the console is not supported on the tunnel edge: coordinate
that update with `ozalctl edge pair-upgrade`.

A binary that is not a release (`ozal version` says `dev`, built by hand) **is
not replaced automatically**: the newest release could be older than what is
running. Move it to releases once with `-apply -force`, and only on an independent
host. The example timer **only checks**: it installs nothing by itself. On the
tunnel's ALPN edge, update it in coordination with the connector. Do not use the
per-host `-apply` there, because a mix of versions breaks the tunnel. `-status
FILE` records the result (`current`, `available`, `incompatible`, `updated`,
`failed`) as JSON readable by the service account, without keys or tokens. The
console can read the authenticated state at `GET /_/api/updates/status` with the
`updates` operator tool. If the host also configured the executor, `GET
/_/api/updates/plan` and `POST /_/api/updates/jobs` review and start a job under
the rules above. A job's state (`updated`, `rolled_back`, `failed`,
`intervention_required`) is read by ID. The displayed reason is reduced to a safe
message; details stay on the host. Exit code 3 of `ozal upgrade` confirms a
rollback; exit code 4 requires intervention.

Publishing: `ozal release keygen -key release.key` creates the key and prints the
public part. Store the private key as the `OZAL_RELEASE_KEY` secret of the
`release` workflow, then delete the file. Without that secret the workflow fails
instead of producing an unsigned release. A release remains a draft until someone
publishes it, and only then do edges see it. **Whoever holds that key can install
code on every edge that pins it.** Rotate it by publishing the new public key to
the edges before signing with it.

<a id="actualización-coordinada-del-edge-y-el-conector"></a>

### Coordinated update of the edge and the connector

For a tunnel, treat both ends as **one version pair**. `ozalctl edge upgrade`
updates only the edge: it does not install or roll back the connector, does not
pause its renewal, and does not check applications. Its exit code 0 proves
startup and admin health. Verify tunnel traffic and protocol compatibility
across both ends; do not chain two independent upgrades on the assumption that
ALPN stays compatible.

The target may include a `connector` with its SSH host, service account, user
unit, executable, staging path and lock. In that case `ozalctl edge upgrade` is
refused for that target; use `ozalctl edge pair-upgrade`. That command builds a
signed Linux/amd64 release specified with `-ref TAG` and `-public-key FILE`, takes
both locks, verifies installed and uploaded hashes, saves the previous executable
on each host and a consistent SQLite copy on the edge, stops the connector and the
edge, installs the same binary, starts the edge and the connector, and checks the
tunnel through the local socket. The edge upgrade migrates SQLite as its service
account, but leaves the unit stopped until the connector is installed. If a failure
happens after the cutover has started, it tries to restore both executables and the
edge database; if it cannot confirm recovery, it reports intervention required.
Progress is reported through the local socket and is available at `GET
/_/api/updates/status` for sessions with the `updates` tool. `GET
/_/api/connectors/{name}/ready` is a host-only probe used by the coordinator. The
progress POST is tolerated as a 404 or 405 only before the candidate edge starts,
because older admin muxes may route an unknown POST through a GET-only fallback.
Once the candidate starts, only 202 is accepted; a missing or method-rejected
status endpoint triggers paired rollback. The final phases are saved when the
edge returns. A 503 or any other error stops the job before the cutover or
triggers its rollback. `ozalctl edge pair-upgrade`
without `-apply` only downloads and verifies the artifact: it does not contact the
hosts. With `-rollback-drill`, an applied update restores both previous versions
and their checkpointed edge database on purpose once the candidate is verified,
then checks that both are ready again. Use it to rehearse the rollback on the real
pair; edge state written while the candidate ran is set aside, not kept, and a
normal `-apply` afterwards performs the update. Before applying, run:

```sh
ozalctl edge pair-preflight [-targets FILE] [-target NAME]
```

This command does connect over SSH. It verifies the current versions and hashes,
the active units, the effective systemd user and group, the service's access to
configuration and SQLite, the admin socket, staging space and free locks. It
creates no files or locks, stops no services, and requires a terminal for the
read-only `sudo` queries on the edge. Its result is an observation, not a
reservation: `pair-upgrade -apply` repeats the inspection under both locks.
`edge.service_group` in the target fixes the expected group; if it is omitted, it
is discovered from the unit and used when restoring SQLite. It does not deploy or
migrate a VPS by itself.

For unattended pair updates, an edge can use the restricted pair helper instead
of interactive root commands. Build the Ozal binary containing the helper, create
an edge profile at `/etc/ozal/pair-helper.json` from the edge's unit, service
identity, installed paths, socket, staging path and connector name, then install
it with `scripts/install-pair-helper.sh`, passing the pinned release public key.
The helper downloads the requested tag from the fixed Ozal GitHub release API,
verifies its detached Ed25519 signature and archive checksum, and confirms the
binary hash matches the coordinator before running it as root. The installer
verifies the helper binary hash and runs the read-only preflight before granting
access. It creates the locked
`ozal-pair` account, installs one Ed25519 key with SSH forwarding and PTY
disabled, and grants passwordless sudo for that helper executable with no
arguments. The key can run commands as the unprivileged `ozal-pair` account for
inspection and staging; it cannot get a root shell or run other sudo commands.

The helper profile must be root-owned, mode `0600`, and use
`/var/lib/ozal-pair/stage` as its staging directory. The target's edge SSH entry
must use `ozal-pair@HOST` and the matching private key; set `edge.pair_helper` to
`true`, `edge.stage` to `/var/lib/ozal-pair/stage`, and `edge.lock` beneath
`/var/lib/ozal-pair/locks`. The connector target remains its own SSH account.
The helper accepts only bounded JSON actions, ignores the user-writable staged
executable, and binds service names and filesystem paths to the root-owned
profile. Each successful edge install also replaces the helper with the same
signed candidate, keeping its protocol version current.

Before the maintenance window, fill in this record with observed data, without
secret values, and keep the operational evidence in a private location:

| Item | Edge | Connector |
|---|---|---|
| Previous version | Commit/build and SHA-256 of the installed executable | Commit/build and installed SHA-256 |
| Common candidate | Committed ref, OS/architecture and artifact SHA-256 | Same ref, artifact for its architecture and SHA-256 |
| Real service | Host, system/user unit, account, executable, config, DB, socket | Host, system/user unit, account, executable, arguments and fixed paths |
| State | Root config, effective runtime, SQLite, Pages, TLS and audit | Local identity, public CA, configuration and renewal |
| Recovery | Previous pair kept, private checkpoint and a new restore destination | Previous executable kept and previous configuration |

Some installations have units and renewal agents with historical names. Do not
rename them during the cutover. Also identify any separate `tunnel edge` process:
each edge that receives a given connector needs a compatible pair. Do not infer
the topology from a port reservation or a service name. Record the effective
`config_sha256` through the authenticated socket. With `runtime_file`, the root
config alone does not describe the routes and publishers.

<a id="backup-y-recuperacion"></a>

## Backup and recovery

Back up **SQLite and its external files** through a private mechanism:

- Host configuration and `runtime_file`.
- A consistent SQLite snapshot; do not copy an active file while ignoring its WAL.
- The contents of `pages.dir`, or the original artifacts to republish.
- TLS cache and state, the ACME account and DNS credentials, to reissue if needed.
- Tunnel identity, trust bundles and CA keys, kept outside the edge.
- External secret files and sources (`file:`/`env:`), and paired chats if used.
- Audit logs and checkpoints, and their pinned public key, with an independent copy.

The database backup uses `VACUUM INTO`, verifies integrity and publishes to a new
file with private permissions. Test it on a disposable directory:

```sh
ozal db backup -db /var/lib/ozal/state.db -out ./state.backup.db
ozal db restore -from ./state.backup.db -to ./state.restored.db
ozal doctor -config /etc/ozal/config.json -db ./state.restored.db
ozal migrate -db ./state.restored.db status
```

Older builds may not include `db backup`. In that case, use SQLite's online
backup API or a build that provides the command, and check the result with
`PRAGMA integrity_check`. Always verify a restore at a new path with the binary
that will read it. A database backup does not include Pages, runtime, TLS/ACME,
tunnel identity or tokens: copy those in the same checkpoint and confirm that the
`deployments` and `site_live` rows point to directories that exist.

`restore` does not overwrite existing destinations and invalidates sessions by
default. `-keep-sessions` can reactivate old cookies. An old copy can also
reactivate users, passkeys, invitations or connectors revoked after it was taken:
reconcile those revocations before returning the service to use.

For a lost host: install a verified version, restore files and owners with the
service stopped, restore the database to a new path, run doctor and explicit
migrations with a backup, start the service, and check login, permissions, sites
and tunnels. An integrity check does not prove that keys, DNS or connectors are
available. Measure data loss and recovery time in that rehearsal.

<a id="incorporar-un-conector"></a>

## Add a connector

Use an existing CA and distribute its public certificate through an
authenticated channel. The placeholders are paths chosen by the operator.

```sh
# Connector machine: new directory, local key and public CSR.
ozal connector prepare -dir ./connector-identity -name homelab
# Authorized CA host: it only receives the CSR.
ozal connector sign -csr ./connector.csr \
  -ca-dir /path/to/existing-ca -out ./connector.crt
# Return the public certificate to its machine; never transfer the key.
ozal connector check -dir ./connector-identity -ca ./ca.crt
# Edge: enrollment through the local socket's authority.
ozal connector enroll -config /etc/ozal/config.json \
  -name homelab -cert ./connector.crt
```

Configure `tunnel` on the edge and a route with `connector: "homelab"`. The current
transport allows loopback or an explicit RFC 1918/ULA subnet; it is not a generic
public listener. The connector validates the edge's name and certificate and
serves only its fixed destinations. For the exact options, use
`ozal tunnel connect -h`; the contracts are in the [engineering guide](../engineering.md#tunnels).

The certificate issued by this flow lasts 30 days. Plan and test rotation
before it expires. Ozal does not provide generic connector renewal; any
SSH-based renewal you build yourself is outside this guide. If the expiry
metadata is missing, the console reports it as unknown.

To retire a connector, review the affected applications and run
`ozal connector revoke -config /etc/ozal/config.json -name CONNECTOR_NAME`
on the edge, then stop the connector on its host. Periodic revalidation closes
revoked sessions within about five seconds; if the registry errors, existing
sessions may remain and new connections are refused. Do not delete the shared CA
or the application's data to revoke an identity.

<a id="github-y-publicacion"></a>

## GitHub and publishing

These are three separate functions: OIDC login for people, repository discovery
with a GitHub App, and workflow authorization to publish. One does not grant the
others.

The usual way is to connect from the console, in **Integrations → GitHub →
Connect GitHub**, or inside the wizard (**Applications → Add application → A
website built from GitHub → Connect GitHub**). When you return from GitHub, the
console brings you back to the page you started from. Ozal creates, in your
account (or in the organization you name), its own **private** App through a
manifest: `Metadata: read`, and `Contents`, `Workflows` and `Pull requests` with
write access, without webhooks or events. Approve it on GitHub. GitHub returns to
the console, Ozal stores the App key in `github-app-ID.pem` (mode 0600, next to
the `-db` state database), and then asks you to choose repositories. On return,
the installation is read from GitHub using the App key; it is never taken from the
URL. This requires the `publishing` tool and that Ozal starts with `-db`. The
configuration stores only the `file:` reference; the OAuth secret and the webhook
secret that GitHub returns are discarded.

Without `-db`, or if you prefer to create the App by hand, store its key as a
private file and configure only the reference from the host socket:

```sh
ozal github connect -config /etc/ozal/config.json \
  -app-id 123 -installation-id 456 -key file:/etc/ozal/github-app-key
```

With GitHub connected, the wizard lists the installed repositories, proposes the
default branch and the build (`npm ci && npm run build`, output `dist`). The
review shows the generated workflow. **One save** creates the address, makes that
repository, branch and workflow (`ozal-pages.yml`) the only publisher of the
site, and opens a PR that adds it; Ozal never merges it. The last step shows the
PR and waits for the first publication before showing the DNS record. If trust or
the PR fails, the address is kept and the step says so, and you finish in Sites.
A private site, or an edge without `pages.deploy`, saves the trust but does not
open a PR: the workflow is written and reviewed separately. The wizard rejects a
site name that already publishes from GitHub instead of replacing its publisher.

**Integrations** shows the connection: the App and account, which repositories it
can see, what it can do, whether the edge can open PRs (`pages.deploy`), and which
sites publish from GitHub. If the installation covers **all** repositories in the
account, it warns you and links to GitHub's page so you can select only the
repositories that publish sites. Ozal requests write access one repository at a
time, but the App key would reach all of them. The same page explains that login
is still Ozal's (passkeys or a configured OIDC provider): connecting GitHub or a
DNS provider never allows signing in with that account. Without `pages.deploy`,
the wizard ends by saying the workflow is added separately, instead of waiting for
a publication.

Disconnecting remains a host operation (`ozal github disconnect`). It deletes the
App on GitHub if you no longer use it, and removes its key file.

For an existing site, choose in Sites who may publish: repository, site, project,
full ref and workflow. The server rechecks access and the repository and owner
IDs. The build runs in CI, and the edge receives the finished artifact; it never
compiles the repository's code. Once the publisher is saved, **Prepare publishing
workflow** lets you choose the build directory, its output and a public hostname
for the project. **Preview workflow** shows the file without writing to GitHub.
**Review pull request** asks you to confirm the branch and file before creating
them. The App also needs `Contents: write`, `Workflows: write` and `Pull requests:
write`, only on the selected repositories. The token used to propose the PR is
limited to the selected repository; exploring still asks only for metadata.

The starter template uses `npm ci` and `npm run build`; review the PR for other
builds. It supports subdirectories and branches containing `/`, but requires a
branch ref, a public project hostname, and an OIDC audience equal to the HTTPS
origin of the deploy endpoint. Protected sites and custom audiences require a
separately reviewed workflow and acceptance; do not change their policy to use
this template. The build job cannot request OIDC; the deploy job does not run
repository code. The checker and publisher and the actions are pinned by commit.
Public acceptance restores the last accepted build from cache if one exists.
Without a cache, the operator must reconcile the live version and restore a
retained version from Sites.

Ozal adds a new file on a new branch, opens the PR and returns its link. It does
not replace existing workflows or branches, does not merge, and does not declare a
publication accepted. Creating the branch can trigger other push workflows in the
repository. If the response is interrupted or fails after a write, inspect the
`ozal/publish-…` branch and its PRs before retrying: the branch is kept so it can be
reconciled, and it is never forced or deleted automatically. Merging the PR enables
publishing on the chosen branch; verify the first build, the active OIDC policy,
acceptance and the real rollback.

The deploy endpoint has a dedicated HTTPS hostname and a reduced scope. The
workflow requests OIDC with the edge's audience, and the repository, owner, ref,
workflow and authorized event must match. Use pinned actions and `id-token: write`
only where you deploy. Use `ozalctl site check|pack|deploy|accept` (native Go) and
see the [publishing contract](../engineering.md#publishing).
Build secrets must not end up in `dist`; runtime secrets stay on the application
host.

Disconnecting the publisher removes future trust and keeps the content and
routes; an upload that was already authenticated can finish with its captured
snapshot. `github disconnect` removes API exploration, **not** the stored OIDC
rules; remove the publishers if you want to stop CI. Uninstalling the App does
not revoke those rules on the edge either.

<a id="operacion-remota-con-ozalctl"></a>

## Remote operation with ozalctl

`ozalctl` runs on the operator's machine, talks over SSH and shows a plan before
applying. Use a private targets file — [ozalctl.example.json](../../ozalctl.example.json) —
with destinations, paths, unit, account, socket, lock and the limited publishing
token file.

```sh
go build -o ./ozalctl ./cmd/ozalctl  # separate binary on the operator machine
ozalctl edge upgrade [-ref REF] [-apply]
ozalctl edge pair-upgrade -ref vX.Y.Z -public-key FILE [-force] [-rollback-drill] [-apply]
ozalctl edge config -base FILE [-cloudflare-ranges] [-apply]
ozalctl site publish -site NAME -dir DIST [-host HOST] [-apply]
ozalctl app alias -from HOST -to HOST [-manual-dns] [-wait 90s] [-apply]
ozalctl api [-d JSON|@FILE|@-] METHOD /_/api/PATH
ozalctl project trust -project NAME -repository-id ID -owner-id ID -workflow-ref WORKFLOW [-ref refs/heads/main] [-apply]
ozalctl project rename -project NAME -to NEW_NAME [-apply]
ozalctl site cutover -host HOST -to IP -cloudflare-token FILE [-dir DIST] [-apply]
ozalctl site cutover -host HOST -back EVIDENCE -cloudflare-token FILE [-apply]
```

`edge upgrade` first validates, as your SSH user, the part of the configuration
it can read. If the configuration directory is readable only by root and the
service account (`0750`), it says so and leaves full validation to `ozal upgrade`,
which runs it as root before stopping the unit. You do not need to open the directory.

<a id="operar-sin-sudo-grupo-del-socket"></a>

### Operate without `sudo`: socket group

By default `app alias` and `project trust` run on the edge as the service account
(`sudo -u`), so they ask for your password and a terminal. With
`admin.socket_group` the admin socket is set to mode `0660` for a dedicated group.
If your SSH user belongs to that group, it opens the socket, and commands run
without `sudo` or a TTY (they also work from scripts). The audit records
`local:<user>` with the uid reported by the kernel; the service account still
appears as `local`. Membership in the group is equivalent to host authority
(enrolling connectors, recovering administrators). Give it only to someone who
already has `sudo` on the edge.

```sh
sudo groupadd --system ozal-admin
sudo usermod -aG ozal-admin deploy        # your SSH user; sign in again
sudo systemctl edit ozal                  # drop-in:
#   [Service]
#   SupplementaryGroups=ozal-admin
#   RuntimeDirectoryMode=0711
# config: "admin": {"socket": "/run/ozal/admin.sock", "socket_group": "ozal-admin"}
sudo systemctl restart ozal
```

`RuntimeDirectoryMode=0711` lets users traverse `/run/ozal` without listing it;
the socket itself stays closed to anyone outside the group. The service does not
start if the group does not exist, if its account does not belong to it, or if the
socket directory is not traversable. A socket that looks shared but is not never
exists. In the targets file, add `"socket_group": "ozal-admin"` to the `edge`. If
your user still cannot open the socket, `ozalctl` stops before running anything
and shows the `usermod` command. Installing binaries, configuration and units
(`edge upgrade`, `edge config`, `pair-upgrade`) still requires root.

### The API as a second path: `ozalctl api`

Everything the admin API exposes (`/_/api/...`: zones, applications, people,
groups, sites, projects, connectors, policy) can be used from your machine with
`ozalctl api`, in addition to the CLI and the console. It needs `socket_group` in
the target: the edge socket is forwarded over SSH to a local socket inside a `0700`
directory (never to a TCP port, which any local user could use), and the request
is made by your SSH user, without `sudo` or a token on your machine.

```sh
ozalctl api GET /_/api/zones
ozalctl api -d '{"name":"example.org","expected":"<config_sha256>"}' POST /_/api/zones
ozalctl api POST /_/api/zones/example.org/check
ozalctl api -d @people.json PUT /_/api/people/ana@example.org/groups
```

It accepts only `/_/api/` paths, JSON bodies up to 1 MB, and the methods GET,
POST, PUT, PATCH and DELETE. It prints the response (indented JSON); an HTTP 4xx
or 5xx ends with status 1 after showing it. Before sending anything, it checks that
the edge responds on the socket: if your user is not in the group, it stops without
sending the request. A network error after sending leaves the result indeterminate:
check the state before repeating a change.

`upgrade` builds a committed ref and follows the procedure above. `config`
requires a base with `runtime_file` and no routes, projects or GitHub settings: it
keeps the existing operational projection, validates as the service, and checks the
digest after the change. If it initializes the runtime, it does not replace an
existing one. From then on, change routes from the console.

`publish` packages regular files, publishes through the private admin API, and
checks the content when `-host` is given. A failure rolls back to the previous
artifact.

`project trust` migrates the GitHub publisher of an existing project when its
repository or organization changes. It verifies the numeric IDs on GitHub and pins
the full ref and the claim `owner/repo/.github/workflows/site.yml@refs/heads/main`.
By default it shows the change; `-apply` keeps the previous metadata in a private
file `project-trust-NAME-DATE.json` next to the service database. The file holds the
IDs, ref and workflow needed to reconfigure trust, without tokens, keys or a full
copy of the runtime. It is never overwritten. The edge compares the reviewed digest,
saves through its API and confirms the active trust; no SIGHUP or external edits to
the runtime are needed. It keeps sites, routes and other publishers. It requires
exactly one previous GitHub publisher; if there are several, review their
configuration explicitly. This authority belongs to the host socket, never to a web
or publishing token. A network error can leave the result indeterminate: check the
plan again before retrying; to revert, use the `before` values from the backup in a
new reviewed command. Like `app alias`, it first requires an `edge upgrade` to a
binary that includes the subcommand; the guard refuses older binaries without
starting them.

`app alias` gives an existing public application (a static site or a route to an
upstream) a second hostname without touching the first. It rejects applications
with an access policy and redirect routes: the alias keeps exactly the origin's
public path. `ozalctl` runs `ozal app alias` on the edge against the local admin
socket (as the service account with `sudo`, or as your user with `socket_group`),
so no new token exists on your machine. The edge saves the route with the reviewed
digest, creates the proxied A record with its own credential (`dns.cloudflare.token`,
a Zone DNS Edit token; it can be the file already used by `tls.dns01`), and waits
until `dns`, `certificate` and `reach` are `ok`. It never replaces an existing
record: if one exists, it waits for it to match. Without `dns.cloudflare` it refuses
before changing anything; with `-manual-dns` it saves the route and shows the
missing record. It can be repeated safely. If the edge binary is older, the command
stops before running anything and asks for `edge upgrade`.

Cloudflare is optional and has two explicit operations, separate from DNS:

```sh
ozalctl cloudflare origin-pull -host NEW.example.com -like OLD.example.com -token-file TOKEN_FILE
ozalctl cloudflare deploy-rule -host deploy.example.com -token-file TOKEN_FILE
# Review the plan; add -apply to write and verify by reading back.
```

The token file must be private. Origin Pulls and Configuration Rules permissions
are different from DNS Edit. `origin-pull` copies only the enabled association of
an existing certificate in the same zone: it exports no keys, creates no
certificates and does not replace different associations. `deploy-rule` adjusts
only the managed rule for the exact hostname; it does not replace the ruleset, does
not change others' rules, and refuses ambiguous scope or extra parameters. Before
writing, it saves 0600 metadata in `~/.local/state/ozalctl/` (or `-backup
NEW_FILE`), reads back and verifies the result. If it fails after writing, check the
provider again before retrying or reverting; the backup contains no credentials and
not the whole zone configuration. The writes were tested against a local API. The
native plans also confirmed by reading back the Origin Pulls association of the
Ozal hostname and the existing deploy rule; no changes were made in Cloudflare.

Cutover is an integration **currently specific to Cloudflare**, with DNS changes
and evidence to restore. A domain associated with Pages may require manual removal
there. Check the plan and keep the evidence; rolling back content does not revert
DNS or databases automatically.

<a id="ante-un-fallo"></a>

## When something fails

Check separately: the process (`/_/healthz`), effective configuration, the
connector link, the application itself, DNS and HTTPS. A responding process does
not prove the full path works. To test limits or recovery, use a disposable
instance; do not fill a disk or cut the network on a shared host. Development
commands are in the [engineering guide](../engineering.md#verification).
