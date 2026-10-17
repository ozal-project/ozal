#!/usr/bin/env bash
# Create the next numbered migration without modifying existing files.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ $# -ne 1 || ! "$1" =~ ^[a-z0-9]+(_[a-z0-9]+)*$ ]]; then
  echo "Usage: scripts/new-migration.sh name_in_snake_case" >&2
  exit 2
fi

migration_dir="$ROOT_DIR/internal/store/sqlite/migrations"
shopt -s nullglob
migration_files=("$migration_dir"/*.sql)
next_version=1
for migration_path in "${migration_files[@]}"; do
  migration_name="${migration_path##*/}"
  if [[ ! "$migration_name" =~ ^([0-9]{3})_[a-z0-9]+(_[a-z0-9]+)*\.sql$ ]]; then
    echo "Invalid migration filename: $migration_name" >&2
    exit 1
  fi
  if (( 10#${BASH_REMATCH[1]} != next_version )); then
    echo "Migration versions must be contiguous from 001" >&2
    exit 1
  fi
  next_version=$((next_version + 1))
done
if (( next_version > 999 )); then
  echo "Migration numbering exhausted (maximum 999)" >&2
  exit 1
fi
printf -v migration_name '%03d_%s.sql' "$next_version" "$1"
migration_path="$migration_dir/$migration_name"
# noclobber also protects against another invocation creating the same file.
(set -o noclobber; cat > "$migration_path" <<HEADER
-- $migration_name
-- Applied migrations are immutable. Add a new numbered migration for changes.
-- The runner wraps this file in a transaction; do not add BEGIN or COMMIT.
HEADER
)
printf 'Created %s\nAdd the SQL before running scripts/migrate.sh up.\n' "$migration_path"
