#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

go mod tidy

GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build \
  -trimpath \
  -ldflags="-s -w" \
  -o magisk/bin/msr-daemon-arm64 \
  ./cmd/msr-daemon

chmod 0755 magisk/bin/msr-daemon-arm64
