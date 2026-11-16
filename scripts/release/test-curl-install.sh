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
chmod +x "$work/tools/curl" "$work/tools/id"
export PATH="$work/tools:$PATH"

run() {
  : > "$OZAL_INSTALL_CALLS"
  : > "$OZAL_INSTALL_FETCHES"
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

run -- -target 'http://127.0.0.1:3000/path with spaces'
require_status 0
grep -Fq 'Nothing was installed. To install the service:' "$work/output"
grep -Fxq 'http://127.0.0.1:3000/path with spaces' "$OZAL_INSTALL_CALLS"

# The outer installer owns application mode. Reject every spelling of the
# inner apply flag before downloading or executing a release, even if the
# caller also supplied the outer --apply flag.
for flag in -apply --apply -apply=true --apply=true -apply=1 --apply=TRUE -apply=false --apply=false; do
  for mode in plan apply; do
    if [[ $mode == apply ]]; then
      run --apply -- -target http://127.0.0.1:3000 "$flag"
    else
      run -- -target http://127.0.0.1:3000 "$flag"
    fi
    require_status 1
    require_refusal 'pass --apply before --'
    [[ ! -s "$OZAL_INSTALL_FETCHES" ]]
  done
done

# Simulate root only in the fixture; the extracted binary records arguments
# instead of installing a service. Outer --apply still reaches the child.
OZAL_INSTALL_UID=0 OZAL_INSTALL_EXPECT_APPLY=1 run --apply -- -target http://127.0.0.1:3000
require_status 0
grep -Fxq -- '-apply' "$OZAL_INSTALL_CALLS"

# Root validation must still stop an ordinary user before any download.
run --apply
require_status 1
require_refusal 'run it with sudo'
[[ ! -s "$OZAL_INSTALL_FETCHES" ]]

export OZAL_INSTALL_PLAN_STATUS=42
run
require_status 42
grep -Fq 'could not produce the service installation plan' "$work/output"
if grep -Fq 'To install the service:' "$work/output"; then
  echo 'failed plan printed installation success instructions' >&2; exit 1
fi
unset OZAL_INSTALL_PLAN_STATUS

OZAL_INSTALL_NO_STABLE=1 run
require_status 0
grep -Fq 'Channel preview offers v0.1.0.' "$work/output"

run --version v0.1.0
require_status 0

# Retained files must survive the installer's temporary-directory cleanup,
# relative to its caller rather than its download workspace.
for keep_dir in './downloads' './downloads with spaces' "$work/absolute" "$work/absolute with spaces" '../sibling' '.'; do
  run --keep "$keep_dir"
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
# nor the installation plan may execute on any verification failure.
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

echo 'public installer: apply boundary, plan success/failure, retained files, preview fallback, pinned version and signature/checksum refusals OK'
