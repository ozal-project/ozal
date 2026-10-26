#!/usr/bin/env bash
# Builds the policy engine for the project page's live demo: engine.wasm (the
# code the edge runs, compiled to WebAssembly) and the matching wasm_exec.js
# from the same Go toolchain. Both are build outputs, not committed.
set -euo pipefail
cd "$(dirname "$0")/.."

out=site/public
mkdir -p "$out"
commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)
GOOS=js GOARCH=wasm go build -trimpath -ldflags "-s -w -X main.commit=${commit}" -o "$out/engine.wasm" ./cmd/policywasm
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$out/wasm_exec.js"
echo "engine ${commit}: $(wc -c < "$out/engine.wasm") bytes"
