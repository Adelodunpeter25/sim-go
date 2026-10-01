# Android live streaming — integration guide

How to embed sim-go's Android screen + input streaming in another
application (a desktop webview, a browser page, or a fully native client).
This document is framework agnostic: it describes wire behavior, lifecycle,
and responsibilities. There are no code samples and no per-language notes,
except where browser behavior specifically matters.

## 1. Architecture

Three parties, each with one job:

- **Device** (emulator or USB device): runs a pushed helper, scrcpy-server
  v2.7, which captures the screen as H.264 video and accepts input (touch,
  scroll, key, text) back. The helper is fetched once by sim-go and cached;
  the application never handles the binary itself.
- **sim-go server**: owns every device-side detail — pushing the helper,
  opening the tunnel, parsing video, translating input. It exposes one
  websocket route per device and converts everything into viewer-friendly
  framing (Section 5). Multiple viewers of the same device share one
  server-side session.
- **Viewer** (your UI): renders video frames and sends input messages. It
  knows nothing about adb, the helper, or tunnels. Any number of viewers —
  web, webview, or native — can attach to the same device, including viewers
  on a different operating system than the host.

There is intentionally no screenshot or polling path in the browser flow.
Video flows as it is encoded; input travels on the same socket.

## 2. Host and device requirements

- Host: a working `adb`, an emulator binary (discovered via `ANDROID_HOME`
  or well-known SDK locations), and loopback networking. The scrcpy helper
  binary is downloaded once from the pinned release and cached; set
  `SIM_GO_SCRCPY_SERVER` to use a pre-provisioned copy on machines without
  network access.
- Device: Android 11 or newer is the supported baseline (the pinned helper's
  floor). Verified against a Pixel_7 emulator image; other images are
  expected to work but report what you find.
- Encoder health: the device's H.264 encoder must produce frames. If a
  session starts but no video ever arrives, suspect the host GPU path — on
  some hosts (observed: Intel macOS with host GPU rendering) the encoder is
  starved and yields zero frames. The fallback is software rendering
  (`SIM_GO_ANDROID_GPU=swiftshader_indirect`). A healthy encoder can be
  confirmed independently with the platform's own screen recorder: if it
  also yields an empty file, the problem is the environment, not the stream.
- The server binds loopback only and has no authentication. Anything running
  as the same user can drive devices, exactly as if it invoked `adb`
  directly. Never expose the port to a network.

## 3. Session lifecycle

- A session starts on the **first viewer attach** for a device and is shared
  by every later viewer of that device. Starting takes seconds (push if
  missing, tunnel, helper launch, handshake).
- With **zero viewers for 30 seconds** the session closes itself: helper
  killed, tunnel removed, encoder freed. Connect on visible, disconnect on
  hide — the idle timer is the application's friend, not its enemy.
- A viewer that joins mid-stream is brought up to speed automatically: it
  receives the stream description first, then the server requests a fresh
  key frame, so decoding can start immediately.
- If the emulator shuts down, its session is torn down; viewers are closed
  and must re-attach after boot. Treat any unexpected close as "re-resolve
  the device, then re-attach."
- The stream is capped at 1280 px on the long edge and 60 fps at 6 Mbps.
  These are server policy, not negotiation: viewers adapt to the announced
  size (Section 6).

## 4. Connecting

One websocket per viewer:

- Route: `/api/stream` with query parameters `platform=android` and
  `id=` set to either the emulator serial (e.g. `emulator-5554`) or the AVD
  name (e.g. `Pixel_7`, resolved to its live serial by the server).
- A standard websocket handshake. Anything else on this route is an error:
  requesting a non-Android platform fails fast; requesting an AVD with no
  live serial fails with "boot it first"; a session that cannot start fails
  at connect time with the device-side reason attached.
- Keep the socket open for the life of the view. There is no heartbeat in
  the protocol; a dead TCP connection reads as a stalled stream (Section 8).

## 5. Message framing

Two directions, two shapes.

**Server → viewer, text messages (JSON).** Exactly one kind today:

- Stream description: an object with `type` `"meta"`, the stream `width`
  and `height` in pixels, the device `name`, and the video `codec` string
  (e.g. `avc1.42E01E`). The description is sent on attach and re-sent
  whenever the codec configuration is (re)parsed. Note: on a fresh session
  the first description may carry an empty codec — wait for the re-announce
  (or the first description packet below) before configuring a decoder.

**Server → viewer, binary messages.** One flag byte followed by payload:

- Flag `1`: stream description bytes (AVC decoder configuration). Feed to
  the decoder as its configuration, not as a frame.
- Flag `2`: key frame (complete picture). A decoder must start from one of
  these; deltas before the first key frame are undecodable by definition.
- Flag `3`: delta frame (change since the previous frame).
- Payloads are H.264 in AVCC packing (length-prefixed units), converted
  server-side from the device's native packing. Unknown flags must be
  ignored, not treated as fatal — the flag space may grow.

**Viewer → server, text messages (JSON).** One object per gesture:

- Touch: `type` `"touch"`, `action` one of `"down"`, `"move"`, `"up"`, and
  `x`/`y` in **stream pixels** (see Section 6). A tap is down immediately
  followed by up; a drag is down, moves, up.
- Scroll: `type` `"scroll"`, position `x`/`y` in stream pixels, `dx`/`dy`
  scroll amounts.
- Key: `type` `"key"`, `code` an Android keycode number (e.g. 3 home,
  4 back, 26 power, 24/25 volume, 66 enter, 67 backspace, 82 menu).
- Text: `type` `"text"`, `text` the string (truncated server-side at
  300 bytes).
- Button: `type` `"button"`, `button` one of `home`, `back`, `menu`,
  `power`, `volume-up`, `volume-down` (convenience over raw keycodes).
- Reset: `type` `"reset"`, asks the encoder for a fresh description plus
  key frame (what late joiners trigger automatically).
- Malformed messages are ignored, never fatal to the session.

## 6. Coordinates and sizing

- The announced width/height is the truth. Map client pointer positions
  into stream pixels before sending: multiply by stream-size divided by
  displayed-size, per axis. Never send display pixels.
- The stream keeps the device aspect ratio at or under the 1280 px cap
  (e.g. a 1080x2400 panel arrives as 576x1280). Size the drawing surface to
  the announced size on every description, not just on connect.
- Rotation is not currently handled: expect portrait panels from phone
  images. If the device rotates, treat the next description as the new
  truth (same rule as initial sizing).

## 7. Decoding requirements

- A decoder for H.264 Baseline (the announced codec string names the exact
  profile) is required. Apply the flag-`1` description bytes as the decoder
  configuration, then feed flag-`2`/`3` payloads as key/delta frames in
  arrival order.
- Frame timestamps in this protocol are a synthetic sequence for ordering,
  not wall-clock time. There is no audio track, so there is nothing to
  synchronize against — render each decoded frame immediately.
- If the decoder reports corrupt input once, request a reset (Section 5)
  rather than reconnecting: the usual cause is joining mid-stream without
  description + key frame, which reset repairs.

## 8. Failure modes and recovery

| Symptom | Likely cause | Recovery |
|---|---|---|
| Connect rejected, no live serial | Emulator not booted | Boot it, then attach |
| Connect accepted, meta arrives, no frames | Encoder starved (host GPU path) | Switch host to software rendering; verify with the platform recorder |
| Frames flow, then stop mid-stream | Emulator asleep/killed or tunnel dropped | Re-resolve the device and re-attach; treat closes as re-attach signals |
| Decoder errors on valid-looking bytes | Missed description/key frame | Send `reset`, wait for description + key frame |
| Input sent, nothing happens on screen | Coordinates outside the stream rect, or target app ignores synthetic input | Clamp to announced size; verify with a tap on a known control |

## 9. Embedding checklist

 keyworded by client kind, in integration order:

1. Resolve the device (serial or AVD name) and confirm it is booted.
2. Open one socket per visible view; close it when the view hides (feeds the 30 s idle close).
3. On description: size the surface, (re)configure the decoder.
4. Render key frames immediately; deltas in order; never buffer for smoothness — the encoder already paces.
5. Map all input to stream pixels; send one message per gesture phase.
6. On socket close or error: show device state (not a frozen last frame — a
   stale picture presented as live is the worst failure), then offer re-attach.
7. Never present a screenshot as a substitute for a dead stream. A still
   image cannot carry input and teaches viewers to mistrust the view.

## 10. Browser-specific notes

- Decoding uses the WebCodecs `VideoDecoder` interface where available
  (Chromium-based browsers and Safari with WebCodecs support — check for
  its presence and show an honest unsupported message otherwise; there is
  no fallback format on this route).
- `http://127.0.0.1` counts as a secure context, so WebCodecs works without
  TLS or special flags.
- There is no audio, so autoplay policies do not block rendering; drawing
  decoded frames to a 2D canvas needs no user gesture.
- For pointer input, disable the browser's touch behaviors on the drawing
  surface (panning/zooming would swallow drags) and mark wheel listeners
  non-passive where scroll must not move the page.
- Keyboard input from a page goes through the text/key messages; physical
  key events should be mapped explicitly (Enter, Backspace, Escape, arrows)
  with single printable characters sent as text.

## 11. Explicit non-goals

No audio capture or playback, no camera, no file transfer, no clipboard
sync, no display rotation handling, no multi-display, no recording. Each of
these is a separate protocol surface and is out of scope for this route.
Device management (boot, shutdown, slim, install, launch) lives on the
companion HTTP verbs, not here.
