#!/usr/bin/env bash
# Compatibility entrypoint; OIDC, upload and confirmation are in Go.
set -euo pipefail
[[ $# == 2 ]] || { echo 'usage: publish.sh ARCHIVE NEW_RECEIPT' >&2; exit 2; }
exec "${OZALCTL_BIN:-ozalctl}" site deploy -archive "$1" -result "$2" -apply
