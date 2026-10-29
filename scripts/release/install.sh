#!/usr/bin/env bash
# Compatibility entrypoint; all installation logic lives in Go.
set -euo pipefail
[[ $# == 3 ]] || { echo 'usage: install.sh ARCHIVE TRUSTED_SHA256 NEW_DESTINATION' >&2; exit 2; }
exec "${OZAL_BIN:?set OZAL_BIN to a trusted current Ozal binary}" install binary -archive "$1" -sha256 "$2" -destination "$3" -apply
