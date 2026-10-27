#!/usr/bin/env bash
# Stages the service installer under a temporary prefix with the real binary:
# no root, account, unit or network is touched.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
bin=${SWITCHBOARD_TEST_BINARY:-}
if [[ -z "$bin" ]]; then
  bin=$work/switchboard
  (cd "$root" && go build -o "$bin" ./cmd/switchboard)
fi
install=$root/scripts/release/install-service.sh

# Dry run changes nothing.
SWITCHBOARD_INSTALL_PREFIX=$work/dry "$install" --binary "$bin" --dry-run >/dev/null
[[ ! -e "$work/dry" ]]

export SWITCHBOARD_INSTALL_PREFIX=$work/stage
"$install" --binary "$bin" --target http://127.0.0.1:4000 --group staff >/dev/null
p=$work/stage
[[ -x $p/usr/local/lib/switchboard/switchboard ]]
# The command is a link to the installed binary, so upgrades keep it current.
[[ -L $p/usr/local/bin/switchboard && $(readlink "$p/usr/local/bin/switchboard") == /usr/local/lib/switchboard/switchboard ]]
[[ -s $p/etc/switchboard/config.json && -s $p/etc/systemd/system/switchboard.service ]]
grep -q '"required_groups": \["staff"\]' "$p/etc/switchboard/config.json"
grep -q 'http://127.0.0.1:4000' "$p/etc/switchboard/config.json"
# No secret in the config, and the token is private to its owner and group.
if grep -qE 'token": "[^f]' "$p/etc/switchboard/config.json"; then echo 'config embeds a token' >&2; exit 1; fi
[[ $(stat -c %a "$p/etc/switchboard/admin.token") == 640 ]]

# A second install never overwrites the first.
if "$install" --binary "$bin" >/dev/null 2>&1; then echo 'overwrote an installation' >&2; exit 1; fi
# Bad input is refused before anything is written.
for bad in "--group Bad;Group" "--target file:///etc/passwd" ; do
  if SWITCHBOARD_INSTALL_PREFIX=$work/other "$install" --binary "$bin" $bad >/dev/null 2>&1; then echo "accepted: $bad" >&2; exit 1; fi
  [[ ! -e "$work/other" ]]
done
if SWITCHBOARD_INSTALL_PREFIX=$work/other "$install" --archive x.tar.gz >/dev/null 2>&1; then echo 'archive without checksum accepted' >&2; exit 1; fi
echo 'service installer: staged install, dry run, no secret in config, no overwrite and input refusal OK'
