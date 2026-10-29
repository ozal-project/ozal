#!/usr/bin/env bash
# Local gate: run before reporting any change as complete.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

# CI and release set OZAL_REQUIRE_FRONTEND=1 (REL-014): a frontend
# check that cannot run is then a failure, not a skipped step.
require_frontend="${OZAL_REQUIRE_FRONTEND:-}"
skipped() {
  if [[ -n "$require_frontend" ]]; then
    echo "$1 (required: OZAL_REQUIRE_FRONTEND is set)" >&2
    exit 1
  fi
  echo "  $1"
}

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
go build -o /dev/null ./cmd/ozal
go build -o /dev/null ./cmd/ozalctl

# Short fuzz pass: the seed corpus runs in the normal test step above; this
# keeps the fuzz targets alive without turning the gate into a long run.
echo "[5/7] fuzz (short)"
go test ./internal/config -run '^$' -fuzz '^FuzzParse$' -fuzztime=1s
go test ./internal/config -run '^$' -fuzz '^FuzzNormalizeHost$' -fuzztime=1s
go test ./internal/certs -run '^$' -fuzz '^FuzzVerifyTSIGResponse$' -fuzztime=1s
go test ./internal/auth -run '^$' -fuzz '^FuzzSafeReturnTo$' -fuzztime=1s
go test ./internal/proxy -run '^$' -fuzz '^FuzzStripCookie$' -fuzztime=1s
go test ./internal/pages -run '^$' -fuzz '^FuzzCheckEntry$' -fuzztime=1s
go test ./internal/pages -run '^$' -fuzz '^FuzzRules$' -fuzztime=1s
go test ./internal/policydemo -run '^$' -fuzz '^FuzzExplain$' -fuzztime=1s
go test ./internal/passkey -run '^$' -fuzz '^FuzzTheRefusalPageNeverGrowsMarkup$' -fuzztime=1s

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
  skipped "console toolchain not installed (cd web/console && npm ci); embedded build used as is"
fi

if command -v node >/dev/null 2>&1; then
  echo "[6c] passkey login page script"
  node scripts/test-passkey-page.mjs
  echo "[6d] console state logic"
  node --test web/console/src/lib/*.test.ts
  echo "[6e] site: tracker parser and demo logic"
  node --test site/src/lib/*.test.ts
else
  skipped "node not installed: passkey page script, console logic and site parser not exercised"
fi

# The project page reads the trackers at build time: a tracker edit that the
# parser or astro check rejects must fail here, not in the site workflow.
if [[ -d site/node_modules ]]; then
  echo "[6f] site: live demo engine, type check and build"
  scripts/build-site-engine.sh >/dev/null
  node scripts/test-site-engine.mjs
  (cd site && npm run build >/dev/null)
else
  skipped "site toolchain not installed (cd site && npm ci); site build not exercised"
fi

echo "[6g] Linux release installer"
if [[ "$(uname -s)" == Linux ]]; then scripts/release/test-install.sh; scripts/release/test-install-service.sh; fi

echo "[7/7] git diff --check"
git diff --check

echo "OK"
