# sim-go

Drive iOS simulators + Android emulators from Go. macOS + Linux.

Idea sources (cloned under `reference/` for study, not vendored):
- [simslim](https://github.com/MobAI-App/simslim) — slim iOS simulators by disabling background `launchd` daemons (~4x memory: 4.0 GB → 0.9 GB). `reference/simslim` is the Go + `simctl` reference.
- [simfleet](https://github.com/entropyconquers/simfleet) — fleet control plane: slim-by-default boots, drive actions (tap/swipe/type/key/open-url/screenshot), adb/emulator + scrcpy patterns. `reference/simfleet` is the TypeScript reference.

## What v1 does

Importable SDK + thin CLI. The core is stdlib only; `internal/idb` adds
grpc+protobuf (pinned companion protocol, no Python/Node at runtime) and
`sdk/streamws` adds gorilla/websocket:

```
sim-go list [-platform ios|android]
sim-go doctor [-json]
sim-go boot|shutdown|slim|restore|normalize <ios|android> <id>
sim-go launch <platform> <id> <bundle|package>
sim-go terminate|uninstall <platform> <id> <bundle|package>
sim-go install <platform> <id> <app.apk|.app>
sim-go press <platform> <id> <home|back|lock|power|volume-up|volume-down|menu|app-switcher>   # iOS: home, lock, power, side, siri
sim-go tap <platform> <id> <x> <y>
sim-go swipe <platform> <id> <x1> <y1> <x2> <y2> [ms]
sim-go type <platform> <id> <text...>
sim-go key <platform> <id> <code>            # android KEYCODE_*; iOS honors 3/4/66/67
sim-go open-url <platform> <id> <url>
sim-go screenshot <platform> <id> <out.png>
```

- iOS (`internal/ios`): `xcrun simctl list/boot/shutdown/io/openurl`. macOS only.
  Input verbs (`tap`, `swipe`, `type`, `key`, `press`) go through a pooled
  idb_companion (HID), not `simctl`, which has no input support. The
  companion is started on first use and closed 30 s after the last one.
- Android (`internal/android`): `adb devices/shell input/screencap`, `emulator -avd -no-boot-anim -gpu host -memory 4096`. mac + Linux.
- Live Android video+input (`internal/scrcpy`): pinned scrcpy-server v2.7
  (auto-fetched once, cached; Apache-2.0 Genymobile) pushed to the device and
  spoken to over an adb tunnel — H.264 in, touch/key/text control out. No
  scrcpy install needed anywhere. Proven live: `sim-go stream-probe android
  Pixel_7` → handshake 576x1280, 35KB keyframe, center tap injected.
- Slim is one **fixed** profile (`internal/slim/profile.go`), no flags by design:
  - iOS: ~130 `launchd` labels (search, iCloud, Siri, widgets, telemetry, photos-analysis, family, health, news/weather/maps, messaging). Keeps push (`apsd`), StoreKit, universal-links (`swcd`), `sharingd` running.
  - Android: ~40 bloat packages via `pm disable-user` (maps/photos/assistant/chrome/wellbeing/...). `restore` re-enables.

## Preview in a browser

`cmd/sim-serve` is a thin HTTP skin over the SDK (embedded single HTML
file, no build step; the websocket handler is `sdk/streamws`):

```
go run ./cmd/sim-serve            # http://127.0.0.1:8790
```

- **Live H.264 on both platforms** over `GET /api/stream?platform=android|ios&id=`
  (one `sdk.Stream` per device, shared by N viewers; meta → avcC description →
  tagged key/delta frames in, touch/scroll/key/text JSON back). Canvas gestures
  drive it; WebCodecs decodes. No screenshots anywhere on the browser path.
  - Android via scrcpy, iOS via a supervised `idb_companion` v1.1.8 (pinned,
    auto-fetched universal binary). Same wire contract either way: the server
    owns device truth, viewers only decode.
  - iOS HID has no move event, so a gesture is replayed on release: under 8pt of
    travel is a tap, beyond it a swipe. Wheel scroll becomes a short swipe along
    the delta. Late joiners get a fresh keyframe (the video pipe reopens, since
    the companion only emits SPS/PPS+IDR at stream start).
- Loopback only, no auth. Full wire and embedding guide: `docs/documentation.md`.

## Build / run

```
go build ./...
go vet ./...
go run ./cmd/sim-go list
go run ./cmd/sim-serve            # browser preview on :8790
```

Env: `ANDROID_HOME`, `SIM_GO_ANDROID_RAM_MB` (default 4096),
`SIM_GO_ANDROID_GPU` (`host` default, or `swiftshader_indirect` where host GL
starves the video encoder — observed on Intel mac),
`SIM_GO_SCRCPY_SERVER` (override pinned scrcpy-server binary path),
`SIM_GO_IDB_COMPANION` (override pinned idb_companion binary path).

## Layout

```
cmd/sim-go/main.go        CLI (thin consumer of sdk)
cmd/sim-serve/            browser preview (mux, verb endpoints, embedded page)
internal/driver/driver.go Driver interface + Device
sdk/                      the product: Client, Doctor, Normalize, Stream
                          (stream.go hub, stream_scrcpy.go, stream_idb.go
                          iOS backend + NAL assembler)
sdk/streamws/             websocket handler for sdk.Stream (gorilla/websocket,
                          the only dep outside internal/idb)
internal/slim/profile.go  fixed slim sets (from simslim categories + avdslim-style list)
internal/ios/              simctl driver: ios.go, devices.go, apps.go, slim.go, input.go (darwin only)
internal/android/          adb/emulator driver: android.go, discover.go, devices.go, apps.go, slim.go, input.go
internal/scrcpy/          live Android video+input: scrcpy.go, session.go, video.go, control.go
internal/idb/             live iOS HID+video: idb.go, session.go, hid.go,
                          video.go, keyboard.go, proto/ (pinned v1.1.8),
                          gen/ (generated gRPC; the one package with external
                          deps: grpc+protobuf)
reference/simslim         MobAI-App/simslim clone (study only, MIT)
reference/simfleet        entropyconquers/simfleet clone (study only, MIT) + scrcpy (Apache-2.0) protocol source
```

## v1 limits (honest)

- `simctl io` offers only enumerate/poll/recordVideo/screenshot (verified via
  `simctl io --help`), so iOS input and video use `internal/idb` instead:
  supervised idb_companion v1.1.8 (pinned universal binary, auto-fetched)
  gives HID tap/swipe/text/keys/buttons + H.264 over gRPC. Wired into the
  SDK (`Client.Stream`, the iOS driver verbs) and the browser preview.
- iOS has no HID move event: drags are replayed on release (see
  `docs/documentation.md`), and back/menu/volume buttons are unsupported.
- Android video needs working on-device encoding: `-gpu host` starves the
  encoder on Intel mac (screenrecord/scrcpy get zero frames); boot with
  `SIM_GO_ANDROID_GPU=swiftshader_indirect` there.
- iOS slim is live-session (`disable` + `bootout`, simslim `--no-reboot` path): persists across reboots on iOS 18.5+, session-only below.
- No server/dashboard/lanes/Metro (simfleet scope) — CLI + library only per v1 decision.
