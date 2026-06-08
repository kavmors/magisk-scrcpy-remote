#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/magisk/bin/scrcpy-server-v4.0"

mkdir -p "$(dirname "$OUT")"
if [ -s "$OUT" ]; then
  echo "$OUT"
  exit 0
fi
curl -L \
  https://github.com/Genymobile/scrcpy/releases/download/v4.0/scrcpy-server-v4.0 \
  -o "$OUT"
chmod 0644 "$OUT"

echo "$OUT"
