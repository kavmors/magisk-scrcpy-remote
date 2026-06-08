#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STATE=/data/adb/magisk-scrcpy-remote
REMOTE=/data/local/tmp/msr-test

"$ROOT/scripts/fetch-scrcpy-server.sh" >/dev/null
"$ROOT/scripts/build.sh"

adb shell "rm -rf '$REMOTE' && mkdir -p '$REMOTE/web' '$STATE/logs'"
adb push "$ROOT/magisk/bin/msr-daemon-arm64" "$REMOTE/msr-daemon-arm64" >/dev/null
adb push "$ROOT/magisk/bin/scrcpy-server-v4.0" "$REMOTE/scrcpy-server-v4.0" >/dev/null
adb push "$ROOT/web/." "$REMOTE/web/" >/dev/null

TMP_CONFIG="$(mktemp)"
trap 'rm -f "$TMP_CONFIG"' EXIT
cat >"$TMP_CONFIG" <<JSON
{
  "listen": "0.0.0.0:13014",
  "stateDir": "$STATE",
  "scrcpyServerPath": "$REMOTE/scrcpy-server-v4.0",
  "deviceScrcpyServerPath": "/data/local/tmp/msr-scrcpy-server.jar",
  "video": {"maxSize": 1280, "maxFps": 30, "bitRate": 4000000},
  "audio": {"enabled": true, "bitRate": 128000, "source": "output"}
}
JSON
adb push "$TMP_CONFIG" "$REMOTE/config.json" >/dev/null
adb shell "printf '%s\n' 'test-token' > '$REMOTE/token' && chmod 600 '$REMOTE/token'"

adb shell "chmod 0755 '$REMOTE/msr-daemon-arm64'; pid=\$(pidof msr-daemon-arm64 2>/dev/null); [ -z \"\$pid\" ] || kill \$pid"
adb shell "cd '$REMOTE'; nohup ./msr-daemon-arm64 --config '$REMOTE/config.json' --module '$REMOTE' --web '$REMOTE/web' < /dev/null > '$STATE/logs/test-daemon.log' 2>&1 & echo started"

sleep 1
adb shell "cat '$REMOTE/token' 2>/dev/null || true"
adb shell "ip -f inet addr show wlan0 2>/dev/null | awk '/inet / {print \$2}' || ip route get 8.8.8.8 | awk '{print \$7; exit}'"
