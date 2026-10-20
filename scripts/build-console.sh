#!/usr/bin/env bash
# Builds the Svelte admin console into internal/admin/console, which the Go
# binary embeds. Node is a build-time tool only; commit the output.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR/web/console"

if [[ ! -d node_modules ]]; then
  npm ci --no-audit --no-fund
fi
npm run check
npm run build
