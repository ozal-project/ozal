#!/usr/bin/env bash
# Local gate: run before reporting any change as complete.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "[1/6] gofmt"
unformatted="$(gofmt -l .)"
if [[ -n "$unformatted" ]]; then
  echo "Files need gofmt:" >&2
  echo "$unformatted" >&2
  exit 1
fi

echo "[2/6] go vet"
go vet ./...

echo "[3/6] go test -race"
go test -race -count=1 ./...

echo "[4/6] go build"
go build -o /dev/null ./cmd/switchboard

echo "[5/6] optional linters"
if command -v staticcheck >/dev/null 2>&1; then
  staticcheck ./...
else
  echo "  staticcheck not installed (go install honnef.co/go/tools/cmd/staticcheck@latest)"
fi
if command -v govulncheck >/dev/null 2>&1; then
  govulncheck ./...
else
  echo "  govulncheck not installed (go install golang.org/x/vuln/cmd/govulncheck@latest)"
fi

echo "[6/6] git diff --check"
git diff --check

echo "OK"
