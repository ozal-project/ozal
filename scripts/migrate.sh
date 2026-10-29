#!/usr/bin/env bash
# Use the embedded Go migration runner; never apply SQL files directly.
#   scripts/migrate.sh up -db /absolute/path/state.db
#   scripts/migrate.sh status -db /absolute/path/state.db
# Legacy runner flags (including -status) remain supported.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

case "${1:-}" in
  up) shift ;;
  status) shift; set -- -status "$@" ;;
  -h|--help|help)
    cat <<'USAGE'
Usage: scripts/migrate.sh [up|status] [-db PATH]

up      Apply pending migrations (default).
status  Validate migration history and list applied/pending migrations.

Default database: ozal.db in the repository root (existing switchboard.db
is reused when ozal.db is absent). Relative paths are
resolved from that root, even when invoked from another directory.
Status can create the database and migration-history table on a fresh path;
it does not apply application migrations. Parent directories must exist.
Applied migrations are immutable; add a new migration to change the schema.
USAGE
    exit 0
    ;;
  ""|-*) ;;
  *) echo "Unknown migration action: $1 (use up or status)" >&2; exit 2 ;;
esac

exec go run ./cmd/ozal migrate "$@"
