# sim-go implementation plan

Commit sequence: WIP fix → A → B → C → D → E. Each step must be build-green before the next.

## Pre-step: commit the WIP on its own

`cmd/sim-serve/web/index.html` needKey fix goes in first, as a standalone commit.

- It is a correct frontend fix. A late joiner receives the cached avcC before the fresh IDR, so the page drops deltas until it sees tag 2.
- It should not be buried in a refactor.

## Phase A: publish the SDK at `sdk/`

Behavior is unchanged.

1. `git mv internal/sdk/{client,doctor,normalize}.go sdk/`. The package name stays `sdk`.
2. Add type aliases so console can name the API's types:
   - `type Device = driver.Device`
   - `type Driver = driver.Driver`
   - Signatures switch to the aliases. Aliases are identical types, so there is zero behavior change.
3. Repoint in-module imports in:
   - `cmd/sim-go/main.go`
   - `cmd/sim-serve/main.go`
   - `cmd/sim-serve/stream.go`
   - `cmd/sim-serve/stream_live_test.go`
4. Doc touches:
   - README layout and the "thin consumer" wording.
   - `roadmap.md` import line becomes `import simgo "github.com/Adelodunpeter25/sim-go/sdk"`.

## Phase B: iOS driver verbs through idb (new)

5. **Companion pool** in `internal/idb`.
   - It is process-wide and refcounted, with one companion per UDID and a 30s idle close.
   - The stream hub and the driver verbs must share it. Otherwise Stream + Tap on the same sim would spawn two companions, and their coexistence is unverified.
6. **Rewire `internal/ios/input.go`:**
   - **Tap/Swipe:** convert pixels to device points via `Session.Points()`. This is the same conversion `ios.go` does, and it matches Android's pixel contract on `Driver`.
   - **Swipe duration:** taken from the `ms` arg. The `Driver` signature stays untouched.
   - **Type:** use `idb.Text` (the fb-idb keyboard table, already tested).
   - **Key:** reuse serve's `iosKey` map (3→HOME, 4→Esc, 66→Return, 67→Del), so CLI and stream behave identically.
   - **Press:**
     - `home` → HOME.
     - `lock` and `power` → LOCK.
     - `side` and `siri` are accepted as extras.
     - `back`, `menu`, `volume-up` and `volume-down` return explicit unsupported errors. The proto has no such buttons, so the loud-failure contract is preserved and nothing is guessed.
7. `Client.Close()` tears down pooled companions, and `cmd/sim-go` defers it. Otherwise every one-shot `sim-go tap ios` orphans an `idb_companion` process, because macOS has no parent-death signal.
8. Optional: `Doctor` gains an `idbCompanionAvailable` field (binary present, or fetched once over the network). Console can use it to preflight.

## Phase C: stream core into `sdk`

This is the biggest phase. It is fully two-backend (scrcpy and idb).

9. Move `cmd/sim-serve/{stream,ios}.go` (~740 lines) into `sdk/stream.go`, `sdk/stream_hub.go` and `sdk/stream_ios.go` as unexported internals.
   - The hub lives inside `sdk`, not `internal/stream`. This avoids the `internal/stream` ↔ `sdk` import cycle that the current `h.sdk.Client` dependency would create.
   - `Client` owns the session map, as `streamHub` does today.
10. Public API:

```go
c.Stream(ctx, platform, id) (*Stream, error)   // android AND ios
s.Meta() StreamMeta                             // width/height/name/codec
s.Packets() <-chan Packet                       // Kind: Meta|Description|KeyFrame|Delta
s.Input(Input) error                            // byte-identical to today's viewerMsg
s.Touch/Scroll/Key/Text/Button/Reset(...)       // sugar over Input
s.Close() error                                 // detach viewer; last close arms 30s idle
```

   - `Meta` is a packet kind so adapters can reproduce today's wire exactly (meta→text, 1/2/3→binary) with no side channel.
   - Late-joiner logic moves into handle creation: the cached desc, plus the backend-specific reset (`ResetVideo` for scrcpy, video-pipe restart for idb).
   - Slow viewer: if its channel is full, that viewer is dropped. The encoder never blocks.
11. **Session sharing** stays on `Client`.
    - There is one scrcpy/idb backend per `platform:id`, with N `Stream` handles, refcounted, and a 30s idle close. Semantics are identical to today.
    - The Phase B pool means verbs and streams never double-start a companion.
12. **Tests:** unit tests move with the code.
    - 7 assembler tests.
    - 7 gesture/control tests.
    - `TestViewerMessageJSON`.
    - Live tests stay in `cmd/sim-serve`, untouched.

## Phase D: gorilla WS layer

13. `go get github.com/gorilla/websocket`. Add a new package `sdk/streamws` with `Handler(c *sdk.Client) http.Handler`:
    - upgrade the connection
    - send meta → avcC → tagged frames out on one writer goroutine
    - read JSON in and pass it to `s.Input`
    - ping/pong
    - write deadlines
14. `sim-serve`:
    - Delete `ws.go` and `ws_test.go`. `stream.go` and `ios.go` are already gone after Phase C.
    - `newMux` mounts `streamws.Handler` at `/api/stream`.
    - The page is unchanged. The wire is byte-identical, and the live tests prove it.
15. README dependency line: "core stdlib; helpers isolated: internal/idb (grpc+protobuf), sdk/streamws (gorilla/websocket)".

## Phase E: docs and verification

16. `git mv docs/android-streaming.md docs/streaming.md`, then add iOS sections:
    - companion backend
    - gesture-on-release (8pt threshold)
    - wheel→swipe
    - restart-on-late-join
    - points mapping
    - `SIM_GO_IDB_COMPANION`

    Fix the README links.
17. README:
    - Update the layout.
    - Update v1-limits. The "serve wiring is the remaining piece" line is stale, because the wiring has landed.
    - Document the CLI `tap`, `press` and `type` on iOS, which now work.

    Roadmap: update the Phase 3/4 checkboxes and the Phase 1 iOS-input line.
18. **Verify:**
    - `gofmt -l .`
    - `go build ./...`
    - `go vet ./...`
    - `go test ./...` (~36 unit tests must pass; live tests skip without devices)
    - Manual checks:
      - `go run ./cmd/sim-serve`: live stream and taps on both platforms.
      - `sim-go stream-probe ios|android`
      - `sim-go tap ios <udid> 200 400`

## Risks

- The pool refactor (B) and viewer channelization (C) are the only genuinely new designs. Both have their current behavior as the reference.
- Everything else is a verbatim move.
- Live tests need booted devices, so final verification runs on the machine where the 16e sim and the Pixel_7 AVD already proved out.
