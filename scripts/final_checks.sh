#!/usr/bin/env bash
# Reproducible local checks. No production services are contacted.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

python3 scripts/check_delivery.py
scripts/check_configs.sh
go test ./... -count=1
go test ./... -count=1 -race -timeout=90s
go vet ./...

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT
CGO_ENABLED=1 go build -buildmode=c-shared -trimpath -ldflags='-s -w' \
  -o "$tmpdir/tokensaver-cpa.so" ./cmd/cpaplugin

arch="$(uname -m)"
case "$arch" in
  x86_64) artifact="dist/linux-amd64/tokensaver-cpa.so" ;;
  aarch64|arm64) artifact="dist/linux-arm64/tokensaver-cpa.so" ;;
  *) artifact="" ;;
esac
if [ -n "$artifact" ] && [ -f "$artifact" ]; then
  go run scripts/cpa_abi_probe.go "$artifact" >/tmp/tokensaver-abi-check.txt
  grep -q 'request_interceptor":true' /tmp/tokensaver-abi-check.txt
  grep -q 'management_api":true' /tmp/tokensaver-abi-check.txt
fi

echo "final checks OK"
