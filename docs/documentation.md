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

## 4. SDK API reference (exhaustive)

Import:

```go
import simgo "github.com/Adelodunpeter25/sim-go/sdk"
```

Everything below is the public surface. Internal packages (`internal/ios`,
`internal/android`, `internal/scrcpy`, `internal/idb`, `internal/driver`,
`internal/slim`) are not importable by consumers.

### Types and constructors

```go
type Device = driver.Device   // alias; never import internal/driver yourself
type Driver = driver.Driver   // alias; the per-platform toolchain interface
```

```go
type Device struct {
    Platform string // "ios" or "android"
    ID       string // UDID (ios) or serial/AVD (android)
    Name     string
    State    string // "Booted"/"Shutdown" (ios), "device"/"offline"/"avd" (android)
    OS       string // optional
}
```

```go
type Client struct { /* opaque */ }
func New() *Client          // Client with the built-in ios+android drivers
func (c *Client) Close() error  // tear down pooled idb companions; defer it
```

`Driver` is the per-platform toolchain interface
(`Name, Available, List, Boot, Shutdown, Slim, Restore, Launch, Terminate,
Install, Uninstall, IsInstalled, Press, SetAppearance, Normalize, Tap, Swipe,
Type, Key, OpenURL, Screenshot`). You normally don't touch it — but you can
fetch one and call it directly:

```go
d, err := c.Driver("android")   // "ios"|"android" or an error
```

### Device lifecycle

```go
func (c *Client) ListAll(ctx context.Context) ([]Device, error)
func (c *Client) IsBooted(ctx context.Context, platform, id string) (bool, error)
func (c *Client) Boot(ctx context.Context, platform, id string) error
func (c *Client) Shutdown(ctx context.Context, platform, id string) error
func (c *Client) Slim(ctx context.Context, platform, id string) error
func (c *Client) Restore(ctx context.Context, platform, id string) error
func (c *Client) Normalize(ctx context.Context, platform, id string) error
```

- `ListAll` merges ios+android, sorted by platform then name.
- `IsBooted` resolves id exactly (UDID, serial, or device name — never the
  `all` alias) and maps iOS `"Booted"` / Android `"device"` to true.
- `Slim`/`Restore` use the fixed profile in `internal/slim` (no options by
  design). `Normalize` makes screenshots deterministic (fixed iOS status
  bar, zeroed Android animation scales); appearance/content untouched.

### Apps

```go
func (c *Client) Launch(ctx context.Context, platform, id, app string) (string, error)
func (c *Client) Terminate(ctx context.Context, platform, id, app string) error
func (c *Client) Install(ctx context.Context, platform, id, appPath string) error
func (c *Client) Uninstall(ctx context.Context, platform, id, app string) error
func (c *Client) IsInstalled(ctx context.Context, platform, id, app string) (bool, error)
```

`app` is a bundle ID (iOS) or package name (Android). `appPath` is a `.app`
directory (iOS) or `.apk` file (Android). `Launch` returns the platform
tool's stdout line (pid etc.).

### Input / device verbs

```go
func (c *Client) Tap(ctx context.Context, platform, id string, x, y int) error
func (c *Client) Swipe(ctx context.Context, platform, id string, x1, y1, x2, y2, ms int) error
func (c *Client) Type(ctx context.Context, platform, id, text string) error
func (c *Client) Key(ctx context.Context, platform, id, code string) error
func (c *Client) Press(ctx context.Context, platform, id, button string) error
func (c *Client) SetAppearance(ctx context.Context, platform, id, mode string) error
func (c *Client) OpenURL(ctx context.Context, platform, id, url string) error
func (c *Client) Screenshot(ctx context.Context, platform, id, outPath string) error
```

- iOS coordinates are points; Android coordinates are pixels — each driver maps.
- `Key` accepts an Android `KEYCODE_*` name/number; on iOS only `3, 4, 66, 67`
  are honored, others error.
- `Press` button: `home|back|lock|power|volume-up|volume-down|menu|app-switcher`
  (+ iOS `side|siri`). Unsupported combinations return an explicit error.
- `SetAppearance` mode: `dark` or `light`.

### Doctor

```go
type Diagnostics struct {
    OS, XcodeSelectPath, Detail                  string
    XcodeInstalled, SimctlAvailable, ADBAvailable,
    EmulatorAvail, IDBCompanionAvailable,
    HasEnoughDiskGB, IOSUsable, AndroidUsable    bool
    DiskFreeBytes                                uint64
}
func (c *Client) Doctor(ctx context.Context) Diagnostics
```

Never fails: missing tools read as `false` flags; `Detail` carries the
human summary. `IDBCompanionAvailable == false` only means the first iOS
input/stream will download the pinned companion once.

### Live streaming (`Client.Stream` and friends)

```go
func (c *Client) Stream(ctx context.Context, platform, id string) (*Stream, error)
```

One shared backend session per device; each call returns one viewer handle.

```go
type StreamMeta struct { Width, Height int; Name, Codec string }

type PacketKind int
const (
    PacketMeta        PacketKind = iota + 1 // Packet.Meta carries size/name/codec
    PacketDescription                       // avcC decoder configuration
    PacketKeyFrame                          // AVCC IDR picture
    PacketDelta                             // AVCC non-key picture
)

type Packet struct {
    Kind PacketKind
    Meta StreamMeta // only for PacketMeta
    Data []byte     // every other kind; shared read-only across viewers
}
```

```go
type Stream struct { /* opaque */ }
func (s *Stream) Meta() StreamMeta        // current video description
func (s *Stream) Packets() <-chan Packet  // closed on end; see Err
func (s *Stream) Err() error              // why Packets closed; nil while open
func (s *Stream) Input(in Input) error
func (s *Stream) Touch(action string, x, y int) error        // "down"|"move"|"up"
func (s *Stream) Scroll(x, y int, dx, dy float64) error
func (s *Stream) Key(code int) error                         // Android keycode
func (s *Stream) Text(text string) error
func (s *Stream) Button(name string) error                   // home|back|menu|power|...
func (s *Stream) Reset() error                               // fresh description+keyframe
func (s *Stream) Close() error                               // detach this viewer
```

```go
// Input is also the JSON wire shape for websocket viewers:
type Input struct {
    Type   string  `json:"type"`             // touch|scroll|key|text|button|reset
    Action string  `json:"action,omitempty"` // touch only
    X, Y   int     `json:"x,omitempty"`
    DX, DY float64 `json:"dx,omitempty"`     // scroll deltas, pixels
    Code   int     `json:"code,omitempty"`   // key: Android keycode
    Text   string  `json:"text,omitempty"`
    Button string  `json:"button,omitempty"`
}
```

Error sentinels (match with `errors.Is`):

```go
ErrInvalidPlatform  // platform is not ios or android
ErrDeviceNotFound   // no such device, or not booted
ErrBackendStart     // device exists but its stream backend failed to start
ErrStreamClosed     // Input on a closed/ended Stream
ErrStreamEnded      // device session ended under this viewer
ErrSlowViewer       // viewer fell behind (>120 buffered packets) and was dropped
```

Typical consumption loop:

```go
s, err := c.Stream(ctx, "android", "Pixel_7")
if err != nil { ... }
defer s.Close()
for p := range s.Packets() {
    switch p.Kind {
    case sdk.PacketMeta:        configureDecoder(p.Meta)
    case sdk.PacketDescription: decoderConfig(p.Data)
    case sdk.PacketKeyFrame, sdk.PacketDelta:
        feed(p.Kind == sdk.PacketKeyFrame, p.Data)
    }
}
if err := s.Err(); err != nil && !errors.Is(err, sdk.ErrSlowViewer) { ... }
s.Touch("down", x, y); s.Touch("up", x, y)
```

### Websocket adapter

```go
// sdk/streamws — the only package with an HTTP dependency:
func streamws.Handler(c *sdk.Client) http.Handler
```

Serves the exact wire protocol in Section 6 over `/api/stream?platform=&id=`.
Use it to embed streaming in any `net/http`-compatible server (it is what
`cmd/sim-serve` mounts).

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
