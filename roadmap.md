# sim-go roadmap

**sim-go is a Go SDK.** The console desktop app (`console/apps/server-go`)
embeds it as a library; the `sim-go` CLI is a thin consumer and reference
client, not the product. Every phase below ships SDK-first with tests against
real devices, then exposes it on the CLI.

Import shape (console side):

```go
import simgo "github.com/Adelodunpeter25/sim-go"
```

Core rules for every phase:

- SDK package never writes to stdout; progress via callbacks, results as values.
- Every blocking call takes `context.Context` with sane timeouts.
- `ios` driver is darwin-only; `android` is darwin+linux. Build tags, no panics.
- Core stays stdlib-only (no external deps); helpers (server, encoders) isolated.
- Each task ends with a real-device test + commit before the next begins.

## Phase 0 — Foundation ✅ (v0.1.0, done)

- [x] `Driver` interface (`List/Boot/Shutdown/Slim/Restore/Tap/Swipe/Type/Key/OpenURL/Screenshot`)
- [x] iOS driver via `xcrun simctl` (proven: 32s boot incl. compile, launch, screenshot)
- [x] Android driver via `adb` + `emulator` (headless, `-gpu host`, `-memory 2048`)
- [x] Fixed slim profile (iOS ~130 `launchd` labels, Android ~40 packages; keeps push/StoreKit/universal-links)
- [x] Thin stdlib CLI mirroring the SDK

## Phase 1 — Agent verbs (SDK core)

Smallest missing blocks. Each is one SDK method + CLI verb + real-device test.

- [x] `Launch(ctx, id, bundleID)` / `Terminate(ctx, id, bundleID)` (iOS: `simctl launch/terminate`; Android: `am start` / `am force-stop`)
- [x] `Install(ctx, id, appPath)` / `Uninstall` (iOS: `simctl install/uninstall`; Android: `adb install/uninstall`)
- [x] `AppState` / `IsBooted` helpers (resolve-before-act: exact UDID/serial, never aliases like `all`)
- [x] `Doctor(ctx)` → `Diagnostics{Xcode, Simctl, ADB, Emulator, DiskFree}` (mirrors console `device.rs` diagnostics; powers `sim-go doctor --json`)
- [x] `Press(ctx, id, Home|Back|Lock|Power|VolumeUp|Down)` (Android `keyevent` behind one verb; iOS returns explicit unsupported — `simctl io` has no button support, Phase 4 helper scope)
- [x] `Normalize(ctx, id)` — deterministic screenshots: disable animations, fixed clock/battery/locale (t3code `mobile-showcase.ts` pattern)

Done when: an agent can boot → install → launch → screenshot → terminate purely through SDK calls, tested on the iPhone 16e sim.

## Phase 2 — Embeddable service (console's missing backend)

Turn the SDK into the service console's `device.rs` already expects.

- [ ] `service` package: `List/Boot/Shutdown/ShutdownAll/OpenApp/Interact/Screenshot` matching console's `DeviceService` routes 1:1
- [ ] `DeviceDescriptor{ID, Name, Platform, State, Model, OSVersion, Available}` JSON (console `types/device.rs` shape)
- [ ] State events: snapshot-first, then changes (t3code `DeviceService:1168` WS-subscriber pattern; transport-agnostic channel first, WS later)
- [ ] Boot-ID remap: Android AVD name → `emulator-XXXX` after boot; iOS attach-after-boot hook (stream session binds post-`bootstatus`, never assumes boot == ready)
- [ ] Host abstraction: `Local` first; `SSH` shape reserved (t3code `SshDeviceHost` pattern, loopback-forwarded)

Done when: console `server-go` imports the service package and its existing `device.rs` calls succeed against a local host. No GUI work here.

## Phase 3 — Stills-first streaming

Prove the server-owns-truth shape with cheap frames before touching video.

- [ ] `Screenshot --watch` (1s poll: `simctl io screenshot` / `adb exec-out screencap`)
- [ ] MJPEG endpoint per device (seed frame + multipart stream)
- [ ] Input over the same channel: normalized 0..1 coords in, server translates to driver verbs
- [ ] Security: loopback-only, allowlisted routes (t3code `DeviceHubProxy` lesson: never a generic exec route)

Done when: a browser page shows a live sim and taps land via normalized coords.

## Phase 4 — The t3code feel (persistent H.264)

This phase buys the near-zero latency. Only starts after Phase 3 works.

- [ ] Per-device persistent capture session (host-frambuffer → hardware H.264, one encoder per device)
- [ ] WS multiplex: frames out + input tags back on one socket (iOS AVCC-style / Android scrcpy-style framing)
- [ ] WebCodecs-capable viewer contract (server encodes once; any client — desktop, web, iOS app — just decodes)
- [ ] Session lifecycle: survive app backgrounding, re-keyframe on reattach, clean kill on shutdown

Done when: swipe on a remote viewer feels instant; an iOS client can drive an Android emulator (server owns all device truth).

## Phase 5 — Fleet / remote

- [ ] Multi-host: local + SSH hosts behind one service
- [ ] Agent attribution: every action tagged with session ID (t3code `agentDeviceSession` pattern)
- [ ] Slim-by-default + opt-out per device (simfleet `autoSlim` pattern)
- [ ] Multi-client: N viewers per device, lane/device claims so agents don't fight

## Non-goals (explicit)

- No RN/Metro/Expo layer (simfleet scope, not ours).
- No dashboard design here — console owns the UI; we own frames + verbs.
- No configurable slim profiles in v1 (fixed set is a feature, not a gap).
- No Windows support (mac + linux only).
