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
exec "${OZAL_BIN:?set OZAL_BIN to a trusted current Ozal binary}" install service "${args[@]}"
