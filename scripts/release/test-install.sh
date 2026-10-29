#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
mkdir "$work/pack"
printf '%s\n' '#!/bin/sh' 'echo fixture-version' > "$work/pack/ozal"
tar -czf "$work/release.tar.gz" -C "$work/pack" ozal
expected=$(sha256sum "$work/release.tar.gz"); expected=${expected%% *}
"$root/scripts/release/install.sh" "$work/release.tar.gz" "$expected" "$work/installed" >/dev/null
[[ $("$work/installed") == fixture-version ]]
if "$root/scripts/release/install.sh" "$work/release.tar.gz" "$expected" "$work/installed" >/dev/null 2>&1; then exit 1; fi
if "$root/scripts/release/install.sh" "$work/release.tar.gz" "$(printf '%064d' 0)" "$work/bad" >/dev/null 2>&1; then exit 1; fi
[[ ! -e "$work/bad" ]]
mkdir "$work/directory-target"
ln -s "$work/directory-target" "$work/symlink-target"
for destination in "$work/directory-target" "$work/symlink-target"; do
  if "$root/scripts/release/install.sh" "$work/release.tar.gz" "$expected" "$destination" >/dev/null 2>&1; then exit 1; fi
  [[ ! -e "$work/directory-target/ozal" ]]
done
rm "$work/pack/ozal"
ln -s /bin/sh "$work/pack/ozal"
tar -czf "$work/link.tar.gz" -C "$work/pack" ozal
expected=$(sha256sum "$work/link.tar.gz"); expected=${expected%% *}
if "$root/scripts/release/install.sh" "$work/link.tar.gz" "$expected" "$work/bad" >/dev/null 2>&1; then exit 1; fi
[[ ! -e "$work/bad" ]]
echo 'release installer: verified fixture, checksum refusal, no overwrite and symlink refusal OK'
