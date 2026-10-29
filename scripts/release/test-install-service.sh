#!/usr/bin/env bash
# Stages the service installer under a temporary prefix with the real binary:
# no root, account, unit or network is touched.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
bin=${OZAL_TEST_BINARY:-}
if [[ -z "$bin" ]]; then
  bin=$work/ozal
  (cd "$root" && go build -o "$bin" ./cmd/ozal)
fi
install=$root/scripts/release/install-service.sh

# Dry run changes nothing.
OZAL_INSTALL_PREFIX=$work/dry "$install" --binary "$bin" --dry-run >/dev/null
[[ ! -e "$work/dry" ]]

export OZAL_INSTALL_PREFIX=$work/stage
"$install" --binary "$bin" --target http://127.0.0.1:4000 --group staff >/dev/null
p=$work/stage
[[ -x $p/usr/local/lib/ozal/ozal ]]
# The command is a link to the installed binary, so upgrades keep it current.
[[ -L $p/usr/local/bin/ozal && $(readlink "$p/usr/local/bin/ozal") == /usr/local/lib/ozal/ozal ]]
[[ -s $p/etc/ozal/config.json && -s $p/etc/systemd/system/ozal.service ]]
grep -q '"required_groups": \["staff"\]' "$p/etc/ozal/config.json"
grep -q 'http://127.0.0.1:4000' "$p/etc/ozal/config.json"
# No secret in the config, and the token is private to its owner and group.
if grep -qE 'token": "[^f]' "$p/etc/ozal/config.json"; then echo 'config embeds a token' >&2; exit 1; fi
[[ $(stat -c %a "$p/etc/ozal/admin.token") == 640 ]]

# A second install never overwrites the first.
if "$install" --binary "$bin" >/dev/null 2>&1; then echo 'overwrote an installation' >&2; exit 1; fi
# A legacy installation must be upgraded in place, never shadowed by fresh state.
mkdir -p "$work/legacy/etc/switchboard"
printf '{}\n' > "$work/legacy/etc/switchboard/config.json"
if OZAL_INSTALL_PREFIX=$work/legacy "$install" --binary "$bin" >"$work/legacy-refusal" 2>&1; then
  echo 'installed beside legacy Switchboard state' >&2; exit 1
fi
grep -q 'Legacy Switchboard installation' "$work/legacy-refusal"
[[ ! -e "$work/legacy/etc/ozal" && ! -e "$work/legacy/var/lib/ozal" ]]
# Bad input is refused before anything is written.
for bad in "--group Bad;Group" "--target file:///etc/passwd" ; do
  if OZAL_INSTALL_PREFIX=$work/other "$install" --binary "$bin" $bad >/dev/null 2>&1; then echo "accepted: $bad" >&2; exit 1; fi
  [[ ! -e "$work/other" ]]
done
if OZAL_INSTALL_PREFIX=$work/other "$install" --archive x.tar.gz >/dev/null 2>&1; then echo 'archive without checksum accepted' >&2; exit 1; fi
echo 'service installer: staged install, dry run, no secret in config, no overwrite and input refusal OK'
