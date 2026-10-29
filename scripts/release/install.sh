#!/usr/bin/env bash
# Install a downloaded, versioned Linux release. No network, root, service or
# config changes. Use a separate trusted channel for the expected checksum.
set -euo pipefail
if [[ $# -ne 3 ]]; then
  echo 'usage: install.sh ARCHIVE.tar.gz EXPECTED_SHA256 NEW_BINARY_PATH' >&2
  exit 2
fi
if [[ "$(uname -s)" != Linux ]]; then
  echo "This exact-target installer supports Linux. Use native installation instructions on macOS/Windows." >&2
  exit 2
fi
archive=$1
expected=$2
destination=$3
if [[ ! "$expected" =~ ^[[:xdigit:]]{64}$ ]]; then
  echo 'expected SHA-256 must contain exactly 64 hexadecimal characters' >&2
  exit 2
fi
if [[ ! -f "$archive" || -L "$archive" || -e "$destination" || -L "$destination" ]]; then
  echo 'archive must be a regular file; destination must not exist (use upgrade for an installed service)' >&2
  exit 2
fi
parent=$(dirname -- "$destination")
if [[ ! -d "$parent" ]]; then
  echo 'create the destination parent directory before installing' >&2
  exit 2
fi
# Work on a private copy so concurrent source changes cannot swap the archive
# between verification and extraction. Only one fixed member is ever extracted.
work=$(mktemp -d "$parent/.ozal-install-XXXXXX")
trap 'rm -rf -- "$work"' EXIT
cp -- "$archive" "$work/release.tar.gz"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$work/release.tar.gz")
else
  actual=$(shasum -a 256 "$work/release.tar.gz")
fi
actual=${actual%% *}
actual=$(printf %s "$actual" | tr A-F a-f)
expected=$(printf %s "$expected" | tr A-F a-f)
if [[ "$actual" != "$expected" ]]; then
  echo 'release checksum mismatch; nothing installed' >&2
  exit 1
fi
# Reject duplicate binary members and links; never unpack arbitrary paths,
# install scripts, config or state from the archive.
count=$(tar -tzf "$work/release.tar.gz" | awk '$0 == "ozal" { n++ } END { print n+0 }')
kind=$(tar -tvzf "$work/release.tar.gz" ozal | cut -c1)
if [[ "$count" -ne 1 || "$kind" != '-' ]]; then
  echo 'release needs exactly one regular ozal binary at the archive root' >&2
  exit 1
fi
tar -xOzf "$work/release.tar.gz" ozal > "$work/ozal"
if [[ ! -s "$work/ozal" ]]; then
  echo 'release binary is empty' >&2
  exit 1
fi
chmod 755 "$work/ozal"
# A same-filesystem hard link atomically refuses a concurrently created target.
ln -T -- "$work/ozal" "$destination"
echo "Installed $destination. Run its version command, then setup in a new private directory."
