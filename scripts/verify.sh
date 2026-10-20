#!/usr/bin/env bash
# Local gate: run before reporting any change as complete.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "[1/7] gofmt"
unformatted="$(gofmt -l .)"
if [[ -n "$unformatted" ]]; then
  echo "Files need gofmt:" >&2
  echo "$unformatted" >&2
  exit 1
fi

echo "[2/7] go vet"
go vet ./...

echo "[3/7] go test -race"
go test -race -count=1 ./...

echo "[4/7] go build"
go build -o /dev/null ./cmd/switchboard

# Short fuzz pass: the seed corpus runs in the normal test step above; this
# keeps the fuzz targets alive without turning the gate into a long run.
echo "[5/7] fuzz (short)"
go test ./internal/config -run '^$' -fuzz '^FuzzParse$' -fuzztime=1s
go test ./internal/config -run '^$' -fuzz '^FuzzNormalizeHost$' -fuzztime=1s
go test ./internal/auth -run '^$' -fuzz '^FuzzSafeReturnTo$' -fuzztime=1s
go test ./internal/proxy -run '^$' -fuzz '^FuzzStripCookie$' -fuzztime=1s
go test ./internal/pages -run '^$' -fuzz '^FuzzCheckEntry$' -fuzztime=1s
go test ./internal/pages -run '^$' -fuzz '^FuzzRules$' -fuzztime=1s

echo "[6/7] optional linters"
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

# The console is committed as built output. When its toolchain is installed,
# rebuild it and fail if the embedded copy was stale.
if [[ -d web/console/node_modules ]]; then
  echo "[6b] console: type check and rebuild"
  before="$(find internal/admin/console -type f -exec sha256sum {} + | sort | sha256sum)"
  scripts/build-console.sh >/dev/null
  after="$(find internal/admin/console -type f -exec sha256sum {} + | sort | sha256sum)"
  if [[ "$before" != "$after" ]]; then
    echo "internal/admin/console was stale: rebuilt it, review and commit the result" >&2
    exit 1
  fi
else
  echo "  console toolchain not installed (scripts/build-console.sh); embedded build used as is"
fi

echo "[7/7] git diff --check"
git diff --check

echo "OK"
