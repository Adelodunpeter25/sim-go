# sim-go

Drive iOS simulators + Android emulators from Go. macOS + Linux.

Idea sources (cloned under `reference/` for study, not vendored):
- [simslim](https://github.com/MobAI-App/simslim) — slim iOS simulators by disabling background `launchd` daemons (~4x memory: 4.0 GB → 0.9 GB). `reference/simslim` is the Go + `simctl` reference.
- [simfleet](https://github.com/entropyconquers/simfleet) — fleet control plane: slim-by-default boots, drive actions (tap/swipe/type/key/open-url/screenshot), adb/emulator + scrcpy patterns. `reference/simfleet` is the TypeScript reference.

## What v1 does

Importable drivers + thin CLI, stdlib only (no external deps):

```
sim-go list [-platform ios|android]
sim-go doctor [-json]
sim-go boot|shutdown|slim|restore|normalize <ios|android> <id>
sim-go launch <platform> <id> <bundle|package>
sim-go terminate|uninstall <platform> <id> <bundle|package>
sim-go install <platform> <id> <app.apk|.app>
sim-go press <platform> <id> <home|back|lock|power|volume-up|volume-down|menu>
sim-go tap <platform> <id> <x> <y>
sim-go swipe <platform> <id> <x1> <y1> <x2> <y2> [ms]
sim-go type <platform> <id> <text...>        # android only in v1
sim-go key <platform> <id> <code>            # android KEYCODE_* in v1
sim-go open-url <platform> <id> <url>
sim-go screenshot <platform> <id> <out.png>
```

- iOS (`internal/ios`): `xcrun simctl list/boot/shutdown/io/openurl`. macOS only.
- Android (`internal/android`): `adb devices/shell input/screencap`, `emulator -avd -no-boot-anim -gpu host -memory 2048`. mac + Linux.
- Slim is one **fixed** profile (`internal/slim/profile.go`), no flags by design:
  - iOS: ~130 `launchd` labels (search, iCloud, Siri, widgets, telemetry, photos-analysis, family, health, news/weather/maps, messaging). Keeps push (`apsd`), StoreKit, universal-links (`swcd`), `sharingd` running.
  - Android: ~40 bloat packages via `pm disable-user` (maps/photos/assistant/chrome/wellbeing/...). `restore` re-enables.

## Build / run

```
go build ./...
go vet ./...
go run ./cmd/sim-go list
go run ./cmd/sim-go list -platform android
```

Env: `ANDROID_HOME`, `SIM_GO_ANDROID_RAM_MB` (default 2048).

## Layout

```
cmd/sim-go/main.go        CLI (thin consumer of internal/sdk)
internal/driver/driver.go Driver interface + Device
internal/sdk/             embeddable facade (Client, Doctor, Normalize) — the product
internal/slim/profile.go  fixed slim sets (from simslim categories + avdslim-style list)
internal/ios/ios.go       simctl driver (darwin only)
internal/android/android.go adb/emulator driver (darwin+linux)
reference/simslim         MobAI-App/simslim clone (study only, MIT)
reference/simfleet        entropyconquers/simfleet clone (study only, MIT)
```

## v1 limits (honest)

- `ios tap/swipe/press/type/key`: not supported by `simctl` on this host — `simctl io`
  offers only enumerate/poll/recordVideo/screenshot (verified via `simctl io --help`).
  t3code solves this with an agent-device/baguette helper (Phase 4 scope). The SDK
  fails loudly with guidance instead of passing cryptic simctl errors. Use
  `launch`/`open-url` for now.
- Android paths are compile-tested only here (no `emulator` binary on this host;
  `doctor` reports this honestly).
- iOS slim is live-session (`disable` + `bootout`, simslim `--no-reboot` path): persists across reboots on iOS 18.5+, session-only below.
- No server/dashboard/lanes/Metro (simfleet scope) — CLI + library only per v1 decision.
