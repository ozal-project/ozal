# Install Ozal

Ozal ships as one binary. This guide covers verifying a download, installing the
binary, and running it as a Linux service.

Before you start, check which release you are installing: read the notes for that
version, not `main`. The [alpha.1 notes](../releases/v0.1.0-alpha.1.md) describe
the first tagged build and do not include features added later.

## Choose your path

| You want to… | Go to |
|---|---|
| Try Ozal on your own machine | [Try it locally](../../README.md#get-started), then [first application](first-application.md) |
| Run Ozal as a Linux service | [Install the service in one step](#3-install-the-linux-service) |
| Update an existing installation | [Upgrade and roll back](administration.md#upgrade-and-roll-back) |
| Keep an installation from the Switchboard era | [Legacy Switchboard names](administration.md#legacy-switchboard-names) |

## 1. Verify the download

Download the archive from the authorized release channel and get its checksum from
a trusted source. A checksum that came from the same compromised source does not
prove authenticity, and this guide does not claim a release signature.

GoReleaser builds Linux, macOS and Windows archives for amd64 and arm64, with
SHA-256 checksums and an SBOM. Pick your operating system and architecture.

```sh
sha256sum ./ozal_VERSION_linux_amd64.tar.gz  # compare with EXPECTED_SHA256
mkdir ./ozal-release
# After verifying the archive and its regular member:
tar -xzf ./ozal_VERSION_linux_amd64.tar.gz -C ./ozal-release ozal
./ozal-release/ozal version
```

If the channel is not trusted, list the archive before extracting it. Never run an
extracted installer before checking the archive's checksum.

The `ozal_VERSION_*` names are the Ozal package names. They do not mean an Ozal
release is already published; check the release channel first.

## 2. Install the binary without privileges

With a trusted, current binary you can install a verified download into your own
directory, without Bash, tar or sha256sum in the install flow:

```sh
mkdir -p "$HOME/.local/bin"
./ozal-release/ozal install binary -archive ./ozal_VERSION_linux_amd64.tar.gz \
  -sha256 EXPECTED_SHA256 -destination "$HOME/.local/bin/ozal"  # plan
# Add -apply to verify a private copy and install it.
```

The command requires a trusted checksum, a new destination and exactly one regular
`ozal` member. It rejects duplicates, links, corrupt archives and oversized entries.
It neither overwrites nor runs the extracted binary.

`scripts/release/install.sh` only translates arguments. It needs `OZAL_BIN` pointing
to a trusted binary and is not a root of trust.

<a id="linux-install-the-service-in-one-step"></a>

## 3. Install the Linux service

`ozal install service` runs the setup described in the
[service ownership guide](administration.md#service-and-state-ownership) in one step.
It:

- creates the `ozal` service account;
- installs the binary and configuration as `root`;
- generates an administration token, readable only by root and the service account;
- migrates the state and initializes the operational file;
- enables the service and waits for the console to answer.

Everything stays on loopback. It does not touch DNS, the firewall or public TLS. It
refuses to overwrite an existing installation; use `upgrade` for that.

```sh
sudo ./ozal install service -binary ./ozal   # review the plan
sudo ./ozal install service -binary ./ozal -target http://127.0.0.1:3000 -apply
# or, with a download and its checksum from a trusted channel:
sudo ./ozal install service -archive ./ozal_VERSION_linux_amd64.tar.gz -sha256 EXPECTED_SHA256 -apply
```

The installer creates `/usr/local/bin/ozal` as a link to the installed binary. This
keeps `ozal upgrade` working from any directory. Administration commands use
`/etc/ozal/config.json` unless a `config.json` exists in the current directory.

When it finishes, it prints the next steps:

1. Open an SSH tunnel to the console.
2. Create the first administrator with a one-time link: `sudo ozal console enrol`.
3. Add your first application in the console.

To test the installer without root or systemd, use the staging directory:
`scripts/release/test-install-service.sh` runs the real binary, and
`ozal install service -binary ./ozal -root /tmp/ozal-stage -apply` creates no
accounts. A real installation requires root and systemd. If it fails after creating
files, keep the state for inspection and do not overwrite it.

## macOS and Windows

On macOS, verify the archive with `shasum -a 256`, extract the binary into its own
directory and run `ozal version`. The service installer runs only on Linux; there is
no approved launchd installer.

On Windows, verify the ZIP with `Get-FileHash -Algorithm SHA256`, expand it with
`Expand-Archive` into a new directory and run `ozal.exe version`. Configuration,
validation and the proxy are portable. Systemd management and the administrative Unix
socket do not apply there.

## Before you move a real site

Decide the permanent domain, HTTPS, sign-in method (passkeys or your identity
provider), how traffic reaches the origin, and your backups before you point a
production name at Ozal. Then follow the
[first application guide](first-application.md).

Upgrades work the same way: `upgrade` takes a hash you have verified yourself,
validates, migrates with a backup, activates the new binary, checks the admin API,
and rolls back on failure. It does not discover or authenticate a download channel
on its own. Migration 012 requires a new OIDC login but keeps your data and native
credentials. Rehearse any restore in a disposable location first.
