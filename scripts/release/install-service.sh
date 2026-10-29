#!/usr/bin/env bash
# Compatibility argument translation only. Prefer: ozal install service.
set -euo pipefail
args=() apply=1
while (($#)); do
  case "$1" in
    --binary|--archive|--sha256|--target|--group)
      [[ $# -ge 2 ]] || exit 2
      args+=("-${1#--}" "$2"); shift 2 ;;
    --dry-run) apply=0; shift ;;
    *) echo 'use ozal install service -h' >&2; exit 2 ;;
  esac
done
[[ -z ${OZAL_INSTALL_PREFIX:-} ]] || args+=(-root "$OZAL_INSTALL_PREFIX")
((apply == 0)) || args+=(-apply)
: "${OZAL_BIN:?set OZAL_BIN to the path of a trusted current Ozal binary}"
# An old edge treats an unknown root command as server startup. Inspect the
# executable's capability marker without executing it before dispatch.
if ! LC_ALL=C grep -aFq -- 'usage: ozal install binary' "$OZAL_BIN"; then
  echo 'OZAL_BIN does not support native install; rebuild or use a current release' >&2
  exit 3
fi
exec "$OZAL_BIN" install service "${args[@]}"
