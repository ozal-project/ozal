#!/usr/bin/env bash
# Apply pending migrations to the state database (or report them).
#
#   scripts/migrate.sh                       # migrate ./switchboard.db
#   scripts/migrate.sh -db /var/lib/switchboard/state.db
#   scripts/migrate.sh -status               # list applied/pending only
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

go run ./cmd/switchboard migrate "$@"
