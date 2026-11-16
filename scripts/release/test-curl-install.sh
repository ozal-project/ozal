#!/usr/bin/env bash
# Exercise the public installer with disposable signed releases and a local
# curl adapter. Real OpenSSL verification runs; no network or service is used.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
mkdir -p "$work/tools" "$work/pack" "$work/fixtures" "$work/caller"
export OZAL_INSTALL_FIXTURES="$work/fixtures"
export OZAL_INSTALL_CALLS="$work/calls"
export OZAL_INSTALL_FETCHES="$work/fetches"
export OZAL_INSTALL_SUDO="$work/sudo-calls"

# Only this temporary copy trusts the disposable signing key. Production's
# pinned key is checked separately by TestCurlInstallerPinsThePublishedReleaseKey.
openssl genpkey -algorithm ED25519 -out "$work/signing-private" 2>/dev/null
public_key=$(openssl pkey -in "$work/signing-private" -pubout -outform DER | tail -c 32 | base64 | tr -d '\n')
sed "s|^RELEASE_KEY=.*|RELEASE_KEY=\"$public_key\"|" "$root/site/public/install.sh" > "$work/install.sh"
sign() {
  openssl pkeyutl -sign -inkey "$work/signing-private" -rawin -in "$1" -out "$work/signature"
  base64 < "$work/signature" > "$2"
}

cat > "$work/pack/ozal" <<'BINARY'
#!/bin/sh
printf '%s\n' "$@" >> "$OZAL_INSTALL_CALLS"
case "$1" in
  version) echo 'ozal fixture' ;;
  install)
    [ "$2" = service ] || exit 90
    applied=0
    for arg do [ "$arg" != -apply ] || applied=1; done
    [ "$applied" = "${OZAL_INSTALL_EXPECT_APPLY:-0}" ] || exit 91
    echo 'Fixture service plan'
    exit "${OZAL_INSTALL_PLAN_STATUS:-0}"
    ;;
  *) exit 92 ;;
esac
BINARY
chmod +x "$work/pack/ozal"
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo 'unsupported installer test architecture' >&2; exit 1 ;;
esac
archive="ozal_0.1.0_linux_$arch.tar.gz"
tar -czf "$work/fixtures/$archive" -C "$work/pack" ozal
sum=$(sha256sum "$work/fixtures/$archive"); sum=${sum%% *}
printf '%s  %s\n' "$sum" "$archive" > "$work/fixtures/checksums.txt"
sign "$work/fixtures/checksums.txt" "$work/fixtures/checksums.txt.sig"
expires=$(date -u -d '+1 day' +%Y-%m-%dT%H:%M:%SZ)
for channel in stable preview; do
  cat > "$work/fixtures/$channel.json" <<CATALOG
{
  "channel": "$channel",
  "os": "linux",
  "arch": "$arch",
  "expires_at": "$expires",
  "version": "v0.1.0",
  "archive_url": "https://github.com/ozal-project/ozal/releases/download/v0.1.0/$archive",
  "archive_sha256": "$sum",
  "checksums_url": "https://github.com/ozal-project/ozal/releases/download/v0.1.0/checksums.txt",
  "signature_url": "https://github.com/ozal-project/ozal/releases/download/v0.1.0/checksums.txt.sig"
}
CATALOG
  sign "$work/fixtures/$channel.json" "$work/fixtures/$channel.sig"
done

cat > "$work/tools/curl" <<'CURL'
#!/bin/sh
set -eu
out=
url=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) out=$2; shift ;;
    https://*) url=$1 ;;
  esac
  shift
done
case "$url" in
  https://ozal.dev/api/v1/releases/stable/linux/*)
    [ "${OZAL_INSTALL_NO_STABLE:-0}" = 0 ] || exit 22
    case "$url" in *.sig) file=stable.sig ;; *) file=stable.json ;; esac ;;
  https://ozal.dev/api/v1/releases/preview/linux/*)
    case "$url" in *.sig) file=preview.sig ;; *) file=preview.json ;; esac ;;
  https://github.com/ozal-project/ozal/releases/download/v0.1.0/*) file=${url##*/} ;;
  *) echo 'unexpected fixture URL' >&2; exit 93 ;;
esac
printf '%s\n' "$url" >> "$OZAL_INSTALL_FETCHES"
cp "$OZAL_INSTALL_FIXTURES/$file" "$out"
CURL
cat > "$work/tools/id" <<'ID'
#!/bin/sh
[ "$*" = '-u' ] || exit 93
printf '%s\n' "${OZAL_INSTALL_UID:-1000}"
ID
# sudo only records how it was used: -v must come before any download, and the
# privileged steps run through it. The installed updates command is not
# present in the fixture, so its call is only recorded.
cat > "$work/tools/sudo" <<'SUDO'
#!/bin/sh
printf '%s\n' "$*" >> "$OZAL_INSTALL_SUDO"
if [ "$1" = -v ]; then
  [ "${OZAL_INSTALL_SUDO_REFUSE:-0}" = 0 ] || exit 1
  [ ! -s "$OZAL_INSTALL_FETCHES" ] || exit 94
  exit 0
fi
[ "$1" != /usr/local/lib/ozal/ozal ] || exit 0
if [ "$1" = cp ] && [ "${OZAL_INSTALL_SUDO_TAMPER:-0}" = 1 ]; then
  "$@" && printf 'tampered' >> "$3"; exit
fi
exec "$@"
SUDO
chmod +x "$work/tools/curl" "$work/tools/id" "$work/tools/sudo"
export PATH="$work/tools:$PATH"

run() {
  : > "$OZAL_INSTALL_CALLS"
  : > "$OZAL_INSTALL_FETCHES"
  : > "$OZAL_INSTALL_SUDO"
  status=0
  # stdin matches curl | sh, including preservation of arguments after --.
  (cd "$work/caller" && sh -s -- "$@" < "$work/install.sh") > "$work/output" 2>&1 || status=$?
}
require_status() {
  if [[ $status != "$1" ]]; then
    cat "$work/output" >&2
    echo "expected installer exit $1, got $status" >&2
    exit 1
  fi
}
require_refusal() {
  [[ $status != 0 ]]
  grep -Fq "$1" "$work/output"
  [[ ! -s "$OZAL_INSTALL_CALLS" ]]
}

# --plan verifies and shows the plan without root and without installing.
run --plan -- -target 'http://127.0.0.1:3000/path with spaces'
require_status 0
grep -Fq 'Nothing was installed. To install the service:' "$work/output"
grep -Fxq 'http://127.0.0.1:3000/path with spaces' "$OZAL_INSTALL_CALLS"
[[ ! -s "$OZAL_INSTALL_SUDO" ]]

# The script owns installation mode. Reject every spelling of the inner apply
# flag before downloading or executing a release, in either mode.
for flag in -apply --apply -apply=true --apply=true -apply=1 --apply=TRUE -apply=false --apply=false; do
  for mode in plan install; do
    if [[ $mode == plan ]]; then
      run --plan -- -target http://127.0.0.1:3000 "$flag"
    else
      run -- -target http://127.0.0.1:3000 "$flag"
    fi
    require_status 1
    require_refusal 'installation mode cannot be set in forwarded service arguments'
    [[ ! -s "$OZAL_INSTALL_FETCHES" ]]
    [[ ! -s "$OZAL_INSTALL_SUDO" ]]
  done
done

# The bare command installs: an ordinary user is asked for sudo before any
# download, verification runs unprivileged, and only the service and updates
# installation run through sudo. The binary records arguments only.
OZAL_INSTALL_EXPECT_APPLY=1 run -- -target http://127.0.0.1:3000
require_status 0
grep -Fxq -- '-apply' "$OZAL_INSTALL_CALLS"
[[ $(head -n 1 "$OZAL_INSTALL_SUDO") == -v ]]
# The executable run as root is the one re-verified in a sudo-created directory.
[[ $(sed -n 2p "$OZAL_INSTALL_SUDO") == 'mktemp -d' ]]
[[ $(sed -n 3p "$OZAL_INSTALL_SUDO") == cp\ * ]]
grep -q '^/.*/bin/ozal install service -archive /.* -apply$' "$OZAL_INSTALL_SUDO"
if grep -q '^\./bin/ozal' "$OZAL_INSTALL_SUDO"; then
  echo 'root ran the binary from the user workspace' >&2; exit 1
fi
grep -Fxq '/usr/local/lib/ozal/ozal install updates -channel stable -apply' "$OZAL_INSTALL_SUDO"
if grep -q 'version' "$OZAL_INSTALL_SUDO"; then
  echo 'verification ran as root' >&2; exit 1
fi

# --no-updates installs only the service; older --apply/--updates still work.
OZAL_INSTALL_EXPECT_APPLY=1 run --no-updates
require_status 0
if grep -Fq 'install updates' "$OZAL_INSTALL_SUDO"; then
  echo '--no-updates still installed updates' >&2; exit 1
fi
OZAL_INSTALL_EXPECT_APPLY=1 run --apply --updates
require_status 0
grep -Fq 'install updates' "$OZAL_INSTALL_SUDO"

# As root, nothing goes through sudo.
OZAL_INSTALL_UID=0 OZAL_INSTALL_EXPECT_APPLY=1 run --no-updates
require_status 0
grep -Fxq -- '-apply' "$OZAL_INSTALL_CALLS"
[[ ! -s "$OZAL_INSTALL_SUDO" ]]

# A file changed after verification is refused before root runs anything.
OZAL_INSTALL_SUDO_TAMPER=1 run
require_status 1
grep -Fq 'changed after verification' "$work/output"
if grep -q 'install service' "$OZAL_INSTALL_SUDO"; then
  echo 'root ran an altered archive' >&2; exit 1
fi

# A refused sudo stops an ordinary user before any download.
OZAL_INSTALL_SUDO_REFUSE=1 run
require_status 1
require_refusal 'sudo was refused'
[[ ! -s "$OZAL_INSTALL_FETCHES" ]]

export OZAL_INSTALL_PLAN_STATUS=42
run --plan
require_status 42
grep -Fq 'could not produce the service installation plan' "$work/output"
if grep -Fq 'To install the service:' "$work/output"; then
  echo 'failed plan printed installation success instructions' >&2; exit 1
fi
unset OZAL_INSTALL_PLAN_STATUS

OZAL_INSTALL_NO_STABLE=1 run --plan
require_status 0
grep -Fq 'Channel preview offers v0.1.0.' "$work/output"

run --plan --version v0.1.0
require_status 0

# Retained files must survive the installer's temporary-directory cleanup,
# relative to its caller rather than its download workspace.
for keep_dir in './downloads' './downloads with spaces' "$work/absolute" "$work/absolute with spaces" '../sibling' '.'; do
  run --plan --keep "$keep_dir"
  require_status 0
  case "$keep_dir" in
    /*) destination=$keep_dir ;;
    *) destination="$work/caller/$keep_dir" ;;
  esac
  for file in "$archive" checksums.txt checksums.txt.sig; do
    cmp "$work/fixtures/$file" "$destination/$file"
  done
  cmp "$work/pack/ozal" "$destination/ozal"
  [[ -x "$destination/ozal" ]]
done

# Alter signed bytes without signing them again: neither the version command
# nor the installation may execute on any verification failure. These runs use
# the default install mode.
cp "$work/fixtures/stable.json" "$work/catalog-good"
printf '\n' >> "$work/fixtures/stable.json"
run
require_refusal 'the release catalog is not signed by the release key'
cp "$work/catalog-good" "$work/fixtures/stable.json"

cp "$work/fixtures/checksums.txt" "$work/checksums-good"
printf '\n' >> "$work/fixtures/checksums.txt"
run
require_refusal 'is not signed by the release key'
cp "$work/checksums-good" "$work/fixtures/checksums.txt"

printf 'tampered' >> "$work/fixtures/$archive"
run
require_refusal 'does not match its signed checksum'

echo 'public installer: one-command install via sudo, plan mode, apply boundary, plan success/failure, retained files, preview fallback, pinned version and signature/checksum refusals OK'
