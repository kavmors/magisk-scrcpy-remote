#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/dist/magisk-scrcpy-remote.zip"
STAGE="$ROOT/dist/stage"

"$ROOT/scripts/fetch-scrcpy-server.sh" >/dev/null
"$ROOT/scripts/build.sh"

rm -rf "$ROOT/dist"
mkdir -p "$STAGE"
cp -R "$ROOT/magisk/." "$STAGE/"
cp -R "$ROOT/web" "$STAGE/web"
rm -f "$STAGE/token"

(
  cd "$STAGE"
  zip -r "$OUT" . >/dev/null
)

rm -rf "$STAGE"

echo "$OUT"
