#!/usr/bin/env bash
# Compatibility entrypoint; all installation logic lives in Go.
set -euo pipefail
[[ $# == 3 ]] || { echo 'usage: install.sh ARCHIVE TRUSTED_SHA256 NEW_DESTINATION' >&2; exit 2; }
: "${OZAL_BIN:?set OZAL_BIN to the path of a trusted current Ozal binary}"
# An old edge treats an unknown root command as server startup. Inspect the
# executable's capability marker without executing it before dispatch.
if ! LC_ALL=C grep -aFq -- 'usage: ozal install binary' "$OZAL_BIN"; then
  echo 'OZAL_BIN does not support native install; rebuild or use a current release' >&2
  exit 3
fi
exec "$OZAL_BIN" install binary -archive "$1" -sha256 "$2" -destination "$3" -apply
