#!/usr/bin/env bash
# Compatibility entrypoint; public acceptance is in Go.
set -euo pipefail
[[ $# == 2 ]] || { echo 'usage: accept.sh PUBLIC_ORIGIN DIST' >&2; exit 2; }
exec "${OZALCTL_BIN:-ozalctl}" site accept -url "$1" -dir "$2"
