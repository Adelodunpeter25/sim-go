# sim-go — Integration Documentation

`sim-go` drives iOS simulators and Android emulators from Go or over HTTP.
This document is the integration reference for any application that wants to
embed it: a desktop app, a CI agent, a web UI, or a test runner.

It covers:

1. What sim-go provides and the two ways to consume it (Go SDK, HTTP server)
2. Installation and requirements
3. Go SDK usage with code samples
4. HTTP API reference with request/response examples
5. CLI reference
6. Live streaming protocol (websocket) with client-side pseudocode
7. Environment variables
8. Error handling contract
9. Security model
10. Limitations

---

## 1. What you get

| Capability | Go SDK | HTTP (`cmd/sim-serve`) | CLI |
|---|---|---|---|
| List/doctor/devices | ✅ | ✅ | ✅ |
| Boot / shutdown / slim / restore / normalize | ✅ | ✅ | ✅ |
| Launch / terminate / install / uninstall / installed? | ✅ | ✅ | ✅ |
| Tap / swipe / type / key / press / open-url | ✅ | ✅ | ✅ |
| Screenshot | ✅ | ✅ (file path) | ✅ |
| Live H.264 streaming + remote input over WS | ✅ (`Client.Stream`) | ✅ (`/api/stream`) | probe only |

Platforms: iOS (macOS only, via `xcrun simctl` + pinned `idb_companion`),
Android (macOS + Linux, via `adb`/`emulator` + pinned `scrcpy-server`).

---

## 2. Installation

```bash
git clone <this repo> && cd sim-go
go build ./...
```

As a library in your own module:

```go
import simgo "github.com/Adelodunpeter25/sim-go/sdk"
```

Requirements per platform:

- **iOS**: macOS, Xcode installed, a booted simulator. First iOS input/stream
  use auto-downloads the pinned `idb_companion` v1.1.8 (universal binary).
- **Android**: Android SDK (`ANDROID_HOME`), an AVD, a booted emulator
  (Android 11+ baseline). First stream use downloads pinned `scrcpy-server` v2.7.

Both binaries are fetched once and cached; set the `SIM_GO_*` override
variables (Section 7) on offline machines.

---

## 3. Go SDK

Basic rules (same for every consumer):

- Package `sdk` **never writes to stdout**; results are values, progress via
  callbacks.
- Every call takes `context.Context`; drivers bound their own waits.
- `defer client.Close()` — mandatory on macOS so pooled `idb_companion`
  processes don't outlive your process.

### List devices and run doctor

```go
package main

import (
    "context"
    "fmt"

    simgo "github.com/Adelodunpeter25/sim-go/sdk"
)

func main() {
    ctx := context.Background()
    c := simgo.New()
    defer c.Close()

    devs, err := c.ListAll(ctx)
    if err != nil { panic(err) }
    for _, d := range devs {
        fmt.Printf("%s %s %s %s\n", d.Platform, d.ID, d.Name, d.State)
    }

    diag := c.Doctor(ctx)
    fmt.Printf("iosUsable=%v androidUsable=%v diskFree=%d\n",
        diag.IOSUsable, diag.AndroidUsable, diag.DiskFreeBytes)
}
```

### Boot → install → launch → screenshot → terminate

```go
const (
    platform = "android"
    id       = "emulator-5554" // or AVD name, resolved to its live serial
    app      = "com.example.app"
)

if err := c.Boot(ctx, platform, id); err != nil { panic(err) }
if err := c.Install(ctx, platform, id, "app/release.apk"); err != nil { panic(err) }
out, err := c.Launch(ctx, platform, id, app)
if err != nil { panic(err) }
fmt.Println(out)

if err := c.Screenshot(ctx, platform, id, "shot.png"); err != nil { panic(err) }
if err := c.Terminate(ctx, platform, id, app); err != nil { panic(err) }
if err := c.Shutdown(ctx, platform, id); err != nil { panic(err) }
```

### Interact

```go
// tap center of a 1080x1920-ish stream area
_ = c.Tap(ctx, platform, id, 540, 960)
_ = c.Swipe(ctx, platform, id, 540, 1600, 540, 400, 300) // x1,y1,x2,y2,ms
_ = c.Type(ctx, platform, id, "hello")
_ = c.Key(ctx, platform, id, "66")          // Android KEYCODE; iOS honors 3/4/66/67
_ = c.Press(ctx, platform, id, "home")      // home|back|lock|power|volume-up|volume-down|menu|app-switcher
_ = c.OpenURL(ctx, platform, id, "https://example.com")
```

iOS unsupported buttons (`back`, `menu`, `volume-*`, `app-switcher`) return
an explicit unsupported error — never guessed.

### Slim / restore / normalize

```go
_ = c.Slim(ctx, platform, id)     // iOS: disable+bootout ~130 launchd daemons; Android: pm disable-user ~40 packages
_ = c.Restore(ctx, platform, id)  // re-enable everything slim disabled
_ = c.Normalize(ctx, platform, id) // deterministic screenshots: fixed iOS status bar, zeroed Android animation scales
```

`Slim` uses one fixed profile by design (no flags).

---

## 4. HTTP API (`cmd/sim-serve`)

Start it:

```bash
go run ./cmd/sim-serve            # http://127.0.0.1:8790 (loopback only, no auth)
go run ./cmd/sim-serve -addr 127.0.0.1:9000
```

All verb endpoints are `POST` with a JSON body; responses are JSON.
Success: `{"ok": true, ...}`. Failure: HTTP 500 `{"ok": false, "error": "..."}`.
`/api/devices` and `/api/stream` are GET.

| Endpoint | Body | Response |
|---|---|---|
| `GET /api/devices` | — | `[{platform,id,name,state,os}, ...]` |
| `GET /api/doctor` | — | `Diagnostics` object (see below) |
| `POST /api/boot` | `{platform,id}` | `{ok:true}` |
| `POST /api/shutdown` | `{platform,id}` | `{ok:true}` |
| `POST /api/slim` | `{platform,id}` | `{ok:true}` |
| `POST /api/restore` | `{platform,id}` | `{ok:true}` |
| `POST /api/normalize` | `{platform,id}` | `{ok:true}` |
| `POST /api/launch` | `{platform,id,app}` | `{ok:true, output}` |
| `POST /api/terminate` | `{platform,id,app}` | `{ok:true}` |
| `POST /api/install` | `{platform,id,path}` | `{ok:true}` (path is server-local) |
| `POST /api/uninstall` | `{platform,id,app}` | `{ok:true}` |
| `POST /api/tap` | `{platform,id,x,y}` | `{ok:true}` |
| `POST /api/swipe` | `{platform,id,x1,y1,x2,y2,ms}` | `{ok:true}` |
| `POST /api/type` | `{platform,id,text}` | `{ok:true}` |
| `POST /api/key` | `{platform,id,code}` | `{ok:true}` |
| `POST /api/press` | `{platform,id,button}` | `{ok:true}` |
| `POST /api/open-url` | `{platform,id,url}` | `{ok:true}` |
| `GET /api/stream?platform=&id=` | websocket upgrade | see Section 6 |

`platform` is `"ios"` or `"android"`. `id` is a UDID/simulator name (iOS) or
serial/AVD name (Android).

Examples:

```bash
curl -s http://127.0.0.1:8790/api/doctor
curl -s -X POST http://127.0.0.1:8790/api/boot   -d '{"platform":"android","id":"Pixel_7"}'
curl -s -X POST http://127.0.0.1:8790/api/launch -d '{"platform":"ios","id":"ABCD-1234","app":"com.apple.Preferences"}'
curl -s -X POST http://127.0.0.1:8790/api/tap    -d '{"platform":"android","id":"emulator-5554","x":540,"y":960}'
```

`Diagnostics` fields: `os`, `xcodeInstalled`, `simctlAvailable`,
`adbAvailable`, `emulatorAvailable`, `idbCompanionAvailable`,
`diskFreeBytes`, `hasEnoughDisk`, `iosUsable`, `androidUsable`.

---

## 5. CLI

```
sim-go list [-platform ios|android]
sim-go doctor [-json]
sim-go boot|shutdown|slim|restore|normalize <ios|android> <id>
sim-go launch <platform> <id> <bundle|package>
sim-go terminate|uninstall <platform> <id> <bundle|package>
sim-go install <platform> <id> <app.apk|.app>
sim-go press <platform> <id> <home|back|lock|power|volume-up|volume-down|menu|app-switcher>
sim-go tap <platform> <id> <x> <y>
sim-go swipe <platform> <id> <x1> <y1> <x2> <y2> [ms]
sim-go type <platform> <id> <text...>
sim-go key <platform> <id> <code>
sim-go open-url <platform> <id> <url>
sim-go screenshot <platform> <id> <out.png>
```

---

## 6. Live streaming protocol

One websocket per viewer at `/api/stream?platform=ios|android&id=<dev>`.
One shared server-side session per device: the first attach starts it (push
helper, tunnel, handshake — takes seconds), each extra viewer just joins,
and after zero viewers for 30 s the session is killed.

- A late joiner automatically gets description + a **fresh key frame** first,
  so it can decode immediately.
- The server sends WS pings every 54 s and drops silent peers; standard
  websocket libraries answer automatically.
- Stream is capped at 1280 px on the long edge, 60 fps, ~6 Mbps. Server
  policy, not negotiation.

### Server → viewer

Text message (JSON), one `meta` object:

```json
{"type":"meta","width":576,"height":1280,"name":"Pixel_7","codec":"avc1.42E01E"}
```

On a fresh session the first `meta` may carry an empty `codec` — wait for the
re-announce (or the first flag-`1` binary) before configuring your decoder.

Binary messages: one flag byte + H.264 AVCC payload.

| Flag | Meaning | Use |
|---|---|---|
| `1` | Decoder configuration (avcC) | Feed as codec config, **not** a frame |
| `2` | Key frame | Decoder must start from this |
| `3` | Delta frame | Feed in arrival order |

Unknown flags: ignore, don't fail. Payloads are H.264 in AVCC packing,
converted server-side from the device's native packing.

### Viewer → server

One JSON object per gesture:

```json
{"type":"touch","action":"down|move|up","x":123,"y":456}
{"type":"scroll","x":123,"y":456,"dx":0,"dy":-120}
{"type":"key","code":66}
{"type":"text","text":"hello"}
{"type":"button","button":"home"}
{"type":"reset"}
```

- Coordinates are **stream pixels**, not display pixels: multiply pointer
  positions by `streamSize / displayedSize` per axis.
- `key` code: Android keycode; on iOS only 3 (home), 4 (esc), 66 (return),
  67 (delete) act.
- `button`: `home|back|menu|power|volume-up|volume-down|app-switcher` on
  Android; on iOS `home|power|lock|side|siri` act, the rest are ignored.
- `text` is truncated server-side at 300 bytes. Malformed messages are
  ignored, never fatal.
- `reset` asks the encoder for a fresh description + key frame (what late
  joiners trigger automatically).

### Decoding

- Needs an H.264 decoder matching the announced profile (codec string names
  it; Baseline in practice).
- Apply flag-`1` as configuration, then decode `2`/`3` in order.
- Frame timestamps are a synthetic sequence for ordering — no audio, nothing
  to sync: render each decoded frame immediately, never buffer.
- On corrupt decode: send `reset` first; only re-attach if that doesn't cure
  it.

### Minimal browser client (WebCodecs)

```js
const ws = new WebSocket(
  `ws://127.0.0.1:8790/api/stream?platform=android&id=emulator-5554`);
ws.binaryType = 'arraybuffer';

let dec, W, H;
dec = new VideoDecoder({
  output: (f) => { paint(f, W, H); f.close(); },
  error: () => { ws.send(JSON.stringify({type:'reset'})); },
});

ws.onmessage = (e) => {
  if (typeof e.data === 'string') {
    const m = JSON.parse(e.data);
    if (m.type === 'meta') {
      W = m.width; H = m.height; resizeCanvas(W, H);
      if (m.codec) dec.configure({codec: m.codec, optimizeForLatency: true});
    }
    return;
  }
  const b = new Uint8Array(e.data), flag = b[0], NAL = b.slice(1);
  if (flag === 1) dec.configure({codec: codecString, description: NAL});
  else if (flag === 2 || flag === 3)
    dec.decode(new EncodedVideoChunk(
      {type: flag === 2 ? 'key' : 'delta', timestamp: seq++, data: NAL}));
};

// input
canvas.addEventListener('pointerdown', e => sendTouch('down', e));
canvas.addEventListener('pointermove', e => sendTouch('move', e));
canvas.addEventListener('pointerup',   e => sendTouch('up',   e));
function sendTouch(action, e) {
  const r = canvas.getBoundingClientRect();
  const x = Math.round((e.clientX - r.left) * W / r.width);
  const y = Math.round((e.clientY - r.top ) * H / r.height);
  ws.send(JSON.stringify({type: 'touch', action, x, y}));
}
```

Browser notes: Chromium + Safari (WebCodecs support) only, no fallback
format exists on this route. `http://127.0.0.1` is a secure context, so no
TLS needed. No audio ⇒ no autoplay policy issues. Disable touch-action on the
canvas and use non-passive wheel listeners.

### iOS specifics

- One `idb_companion` per simulator UDID, shared process-wide (stream + SDK
  verbs) through a refcounted pool; pool closes it 30 s after last use.
- Companion emits Annex-B → server reassembles pictures and converts to AVCC,
  so clients need no iOS-specific code.
- HID works in device **points**; stream is in pixels. Server converts using
  the reported pixels-per-point scale.
- **Gesture replays on release**: down→up with travel ≤ 8 pt is a tap,
  anything further is a swipe from down to up. `move` is accepted and ignored,
  so drags show no motion until release.
- `scroll` becomes a short swipe (closest wheel equivalent).
- Late join / `reset`: server reopens the video pipe for a fresh
  SPS/PPS + IDR (older viewers see a brief discontinuity and recover).
- A viewer joining a session older than 5 s must expect this; plan for it.

### Failure modes

| Symptom | Likely cause | Recovery |
|---|---|---|
| Connect rejected 404 | device not booted | boot it, then attach |
| meta arrives, no frames | encoder starved (host GPU path, e.g. Intel mac `-gpu host`) | `SIM_GO_ANDROID_GPU=swiftshader_indirect`; verify with the platform recorder |
| Frames stop mid-stream | emulator killed / tunnel dropped | re-resolve device, re-attach |
| Decoder errors | missed description/key frame | send `reset` |
| Input does nothing | coords outside stream rect | clamp to announced size |

Non-goals of the stream route: no audio, camera, file transfer, clipboard,
rotation handling, multi-display, or recording.

---

## 7. Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `ANDROID_HOME` | — | Android SDK location (adb, emulator) |
| `SIM_GO_ANDROID_RAM_MB` | `4096` | emulator RAM |
| `SIM_GO_ANDROID_GPU` | `host` | set `swiftshader_indirect` where host GL starves the encoder (Intel mac) |
| `SIM_GO_SCRCPY_SERVER` | pinned v2.7 (cached) | override scrcpy-server binary path |
| `SIM_GO_IDB_COMPANION` | pinned v1.1.8 (cached) | override idb_companion binary path |

---

## 8. Error contract

- SDK calls return Go `error`s; iOS/Android drivers are darwin-only /
  darwin+linux respectively and return errors (never panic) where a toolchain
  is missing.
- HTTP verbs: 200 `{ok:true}` or 500 `{ok:false,error}`. Websocket handshake
  failures are pre-upgrade JSON: 400 unknown platform, 404 unknown/not-booted
  device, 502 session start failure.
- Unsupported platform features (e.g. iOS volume buttons) must return an
  explicit error, not silently no-op.

## 9. Security

- Loopback only, no auth — anyone running as the same user can drive devices.
  **Never** expose the port to a network.
- No generic exec route; fixed verb endpoints + one websocket route only.
- The stream route gives raw frames + input injection equivalent to adb
  input — treat the server as priviledged.

## 10. Limitations (v1)

- No drag-while-moving on iOS (gestures replay on release); no back/menu/
  volume buttons on iOS.
- Android video needs a working on-device encoder (see Section 6).
- iOS slim persistence depends on iOS version (session-only below iOS 18.5).
- No Windows support (macOS + Linux only).
- No server/dashboard/lanes/fleet yet (see `roadmap.md` Phase 5).

---

## 11. Where things live

```
cmd/sim-go/main.go        CLI (thin consumer of sdk)
cmd/sim-serve/            browser preview (mux, verb endpoints, embedded page)
internal/driver/driver.go Driver interface + Device
sdk/                      the product: Client, Doctor, Normalize, Stream
sdk/streamws/             websocket handler (gorilla/websocket)
internal/ios/             simctl driver (darwin only)
internal/android/         adb/emulator driver
internal/scrcpy/          Android live video+input (scrcpy-server v2.7)
internal/idb/             iOS live HID+video (idb_companion v1.1.8, gRPC)
internal/slim/profile.go  fixed slim sets
```
