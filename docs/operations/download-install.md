# Download, install and update

This guide covers verified installation and the Linux service. The public
release channel is controlled by the maintainer: a public site does not
guarantee access to the assets of a private repository.

<a id="elegir-una-version"></a>

## Choose a version

GoReleaser builds Linux, macOS and Windows artifacts for amd64 and arm64, with
SHA-256 checksums and an SBOM. Pick your operating system and architecture, and
read the notes for **that version**. The historical alpha.1 release does not
include features added later to `main`. A local build with uncommitted changes
is a candidate, not a published reproducible release.

The `ozal_VERSION_*` examples name the Ozal packages; they do not claim that an
Ozal release is already published. Earlier tags keep their `switchboard` files
and binaries: use the instructions and installer of that tag. To update an
existing installation, follow the [migration to Ozal](administration.md#migrar-de-switchboard-a-ozal).

Download the archive from the authorized release channel and obtain its checksum
from a trusted source. A checksum that came from a compromised source does not
prove authenticity, and this guide does not claim a release signature.

## Linux: install a new binary without privileges

Verify the download before running any release code. In a new directory, verify
the full archive and extract only the regular `ozal` binary. If the channel is
not trusted, inspect the listing first. Never run an extracted installer before
checking the archive's checksum.

```sh
sha256sum ./ozal_VERSION_linux_amd64.tar.gz  # compare with EXPECTED_SHA256
mkdir ./ozal-release
# After verifying the archive and its regular member:
tar -xzf ./ozal_VERSION_linux_amd64.tar.gz -C ./ozal-release ozal
./ozal-release/ozal version
```

With a **trusted, current** binary you can install another local download
without Bash, tar or sha256sum in the install flow:

```sh
mkdir -p "$HOME/.local/bin"
./ozal-release/ozal install binary -archive ./ozal_VERSION_linux_amd64.tar.gz \
  -sha256 EXPECTED_SHA256 -destination "$HOME/.local/bin/ozal"  # plan
# Add -apply to verify a private copy and install it.
```

The Go command requires a trusted checksum, a new destination and exactly one
regular `ozal` member. It rejects duplicates, links, corrupt archives and
exceeded limits. It neither overwrites nor runs the extracted binary. For an
existing service, use [upgrade](administration.md#servicio-y-propiedad-del-estado).
`scripts/release/install.sh` only translates arguments; it needs `OZAL_BIN`
pointing to the current trusted binary. It is not a root of trust.

## Linux: install the service in one step

`ozal install service` performs the installation described in the
[systemd guide](administration.md#servicio-y-propiedad-del-estado) in one step:
it creates the `ozal` account, installs the binary and configuration as `root`,
generates a random administration token (readable only by root and the service
account), migrates the state, initializes the operational file, enables the
service and waits for the console to respond. Everything stays on loopback; it
does not touch DNS, the firewall or public TLS, and it refuses to overwrite an
existing installation (use `upgrade` for that).

```sh
sudo ./ozal install service -binary ./ozal   # review the plan
sudo ./ozal install service -binary ./ozal -target http://127.0.0.1:3000 -apply
# or, with a download and its checksum from a trusted channel:
sudo ./ozal install service -archive ./ozal_VERSION_linux_amd64.tar.gz -sha256 EXPECTED_SHA256 -apply
```

The installer creates `/usr/local/bin/ozal` as a link to the installed binary,
so `ozal` runs from any directory and `ozal upgrade` keeps updating it.
Administration commands use `/etc/ozal/config.json` when there is no
`config.json` in the current directory.

When it finishes, it prints the next steps: an SSH tunnel to the console, a
one-time link (`sudo ozal console enrol`, which creates the first administrator
with a passkey), and adding the first application in the console.
`scripts/release/test-install-service.sh` tests it with the real binary in a
staging directory, without root. The native equivalent is
`ozal install service -binary ./ozal -root /tmp/ozal-stage -apply`: it creates
no accounts and does not call systemd. A real installation requires root and
systemd. If it fails after creating files, keep the state for inspection and do
not allow it to be overwritten.

## macOS and Windows

On macOS, verify the tar.gz with `shasum -a 256`, extract the release binary
into its own directory and check `ozal version`. The service installer refuses
to run on other systems, and there is no approved launchd installer.

On Windows, verify the ZIP with `Get-FileHash -Algorithm SHA256`, use
`Expand-Archive` into a new destination and run `ozal.exe version`. Configuration,
validation and the proxy are portable. Systemd management and the administrative
Unix socket have a different scope there.

## Install a service and keep the state

The [systemd guide](administration.md#servicio-y-propiedad-del-estado) defines
the root-owned binary and configuration, the service account, private state and
the administrative socket. Decide the permanent domain, HTTPS, provider or
passkeys, origin transport and backups before moving a real site. Then follow
the [first application guide](first-application.md).

`upgrade` takes a hash that the operator has already verified. It validates,
migrates with a backup, activates the new binary and checks the admin API, and
rolls back on failure. It does not discover or authenticate a channel by itself.
Migration 012 requires a new OIDC login: it keeps data and native credentials.
An old session without a recorded method also requires a login. Rehearse the
restore in a disposable location.

## When the channel is ready

Beyond installer tests and local packages, a person without repository access
must be able to obtain the announced version, verify it and install it by
following this guide. Until the channel is private or that rehearsal is done,
this installation path is not accepted. Do not change DNS, a release or the
repository visibility without the corresponding authorization.
