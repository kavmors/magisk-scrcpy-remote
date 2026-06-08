#!/system/bin/sh

MODDIR=${0%/*}
STATE=/data/adb/magisk-scrcpy-remote

until [ "$(getprop sys.boot_completed)" = "1" ]; do
  sleep 2
done

mkdir -p "$STATE/logs"
chmod 700 "$STATE"
chmod 755 "$MODDIR/bin/msr-daemon-arm64"

if [ ! -f "$STATE/config.json" ]; then
  cp "$MODDIR/config.default.json" "$STATE/config.json"
  chmod 600 "$STATE/config.json"
fi

exec "$MODDIR/bin/msr-daemon-arm64" \
  --config "$STATE/config.json" \
  --module "$MODDIR" \
  --web "$MODDIR/web" \
  >> "$STATE/logs/daemon.log" 2>&1
