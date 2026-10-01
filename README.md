# sim-go

Drive iOS simulators + Android emulators from Go. macOS + Linux.

Idea sources (cloned under `reference/` for study, not vendored):
- [simslim](https://github.com/MobAI-App/simslim) — slim iOS simulators by disabling background `launchd` daemons (~4x memory: 4.0 GB → 0.9 GB). `reference/simslim` is the Go + `simctl` reference.
- [simfleet](https://github.com/entropyconquers/simfleet) — fleet control plane: slim-by-default boots, drive actions (tap/swipe/type/key/open-url/screenshot), adb/emulator + scrcpy patterns. `reference/simfleet` is the TypeScript reference.

## What v1 does

Importable drivers + thin CLI, stdlib only (no external deps):

```
sim-go list [-platform ios|android]
sim-go boot|shutdown|slim|restore <ios|android> <id>
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
cmd/sim-go/main.go        CLI (stdlib flag only)
internal/driver/driver.go Driver interface + Device
internal/slim/profile.go  fixed slim sets (from simslim categories + avdslim-style list)
internal/ios/ios.go       simctl driver (darwin only)
internal/android/android.go adb/emulator driver (darwin+linux)
reference/simslim         MobAI-App/simslim clone (study only, MIT)
reference/simfleet        entropyconquers/simfleet clone (study only, MIT)
```

## v1 limits (honest)

- `ios type/key`: not implemented — `simctl` has no text/key injection; simfleet solves this with baguette/argent helpers. Use `open-url` deep links for now.
- iOS slim is live-session (`disable` + `bootout`, simslim `--no-reboot` path): persists across reboots on iOS 18.5+, session-only below.
- No server/dashboard/lanes/Metro (simfleet scope) — CLI + library only per v1 decision.
