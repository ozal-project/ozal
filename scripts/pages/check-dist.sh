#!/usr/bin/env bash
# Compatibility entrypoint; native scanner and archive validation are in Go.
set -euo pipefail
[[ $# == 1 ]] || { echo 'usage: check-dist.sh DIST' >&2; exit 2; }
exec "${OZALCTL_BIN:-ozalctl}" site check -dir "$1"
