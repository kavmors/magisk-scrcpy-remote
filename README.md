# Magisk Scrcpy Remote

Android 14 arm64 Magisk module prototype for scrcpy over a single WebSocket.

The daemon runs on the phone, starts `scrcpy-server-v4.0`, connects to its
local abstract sockets, and serves a browser UI for video, audio, and control.
The browser, media stream, and control events all use the same HTTP(S)
reverse-proxy-compatible WebSocket path (`/ws-stream`).

## Build

```sh
./scripts/build.sh
```

The Android binary is written to:

```text
magisk/bin/msr-daemon-arm64
```

## Local Test On Connected Device

```sh
./scripts/device-test.sh
```

Then open:

```text
https://PHONE_IP:13014
```

The daemon waits until a non-empty token exists in the module directory. It
first reads `token`, then falls back to `token.local`. It does not generate a
random token.

For Magisk installs, the override path is:

```text
/data/adb/modules/magisk-scrcpy-remote/token
```

The packaged default token file is:

```text
/data/adb/modules/magisk-scrcpy-remote/token.local
```

Write your own token to `token` before or after boot if you want to override
`token.local`. If both files are missing or empty, the daemon keeps polling
every 2 seconds and does not listen on `13014` until a token appears:

```sh
adb shell su -c "printf '%s\n' 'your-token-here' > /data/adb/modules/magisk-scrcpy-remote/token"
```

`device-test.sh` also sets up the daemon for USB-forwarded HTTP access:

```sh
adb forward tcp:13014 tcp:13014
open http://127.0.0.1:13014
```

USB forwarding is useful for checking the page and stream. For LAN access, use
HTTPS with the phone Wi-Fi IP from a machine on the same reachable LAN:

```text
https://PHONE_IP:13014
```

The daemon also accepts plain HTTP on the same port so frp can forward to it.
When a LAN browser opens `http://PHONE_IP:13014`, the page redirects to
`https://PHONE_IP:13014` so Chrome enables WebCodecs. The certificate is
self-signed; accept the browser warning for this local device.

## frp HTTP Reverse Proxy

Only one HTTP(S) forwarding rule is required. No UDP, TURN, STUN, or extra media
ports are needed.

External:

```text
https://sc-mibox.gd.ddnsto.com:443
```

Forward to the phone:

```text
http://127.0.0.1:13014
```

The page uses `wss://sc-mibox.gd.ddnsto.com/ws-stream?...` automatically when
loaded over HTTPS. The browser must support WebCodecs; current desktop Chrome,
desktop Edge, and Android Chrome are the target.

WebCodecs requires a secure context. Use one of these:

```text
https://sc-mibox.gd.ddnsto.com
https://PHONE_IP:13014
http://127.0.0.1:13014
```

Chrome on iOS does not work because it uses Apple's WebKit engine rather than
Chromium's media stack.

## Magisk Module

Build a flashable module zip:

```sh
./scripts/package-magisk.sh
```

The output is:

```text
dist/magisk-scrcpy-remote.zip
```

The module is intended for Android 14 arm64 and bundles scrcpy server v4.0.

## Device Tools

The web UI includes a collapsible device tools panel. These APIs require the
same token used by the stream connection.

- APK install: uploads an `.apk` and runs `pm install -r -t` on the phone.
- File manager: lists absolute paths, uploads into the current directory, and
  downloads regular files.
- Shell: runs commands through `/system/bin/sh -c` with a 60 second timeout.

The daemon runs from a Magisk module, so shell and file operations run with the
module process privileges. Keep the token private when exposing the page through
frp.
