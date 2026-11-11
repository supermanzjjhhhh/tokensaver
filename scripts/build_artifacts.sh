#!/usr/bin/env bash
# Build host artifacts. Cross-compiling the C ABI plugin requires a matching C toolchain.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
mkdir -p dist

go build -trimpath -ldflags='-s -w' -o dist/tokensaver ./cmd/tokensaver
CGO_ENABLED=1 go build -buildmode=c-shared -trimpath -ldflags='-s -w' \
  -o dist/tokensaver-cpa.so ./cmd/cpaplugin

if command -v sha256sum >/dev/null 2>&1; then
  sha256sum dist/tokensaver dist/tokensaver-cpa.so dist/linux-amd64/* dist/linux-arm64/* > dist/SHA256SUMS
else
  shasum -a 256 dist/tokensaver dist/tokensaver-cpa.so dist/linux-amd64/* dist/linux-arm64/* > dist/SHA256SUMS
fi
cat dist/SHA256SUMS
