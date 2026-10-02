package main

// iOS live stream: one idb.Session per booted simulator, supervised by the
// hub and framed into exactly the same websocket contract the Android path
// uses (meta text + avcC desc + AVCC key/delta tags). Input goes in as HID:
// taps, swipes, keys, text, hardware buttons. No screenshots on this path.

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Adelodunpeter25/sim-go/internal/scrcpy"
)

const (
	iosFps            = 30
	iosScale          = 1.0 // native pixels; meta and input math assume this
	iosDragThreshold  = 8.0 // points of travel that turn a press into a drag
	iosSwipeDragSecs  = 0.2
	iosSwipeScrollSec = 0.1
	iosInputTimeout   = 10 * time.Second
)

// iosButtons maps the viewer's button names to HID buttons. Android-only
// buttons (back, menu, volume) have no iOS equivalent and are dropped.
var iosButtons = map[string]string{
	"home": "HOME", "power": "LOCK", "lock": "LOCK",
	"side": "SIDE_BUTTON", "siri": "SIRI",
}

// ---------- stream: Annex-B bytes in, tagged AVCC out ----------

// scTerminator is a synthetic start code: it proves the buffered NAL ended.
var scTerminator = []byte{0, 0, 0, 1}

// iosAU is one picture: its NALs with start codes stripped, ready for AVCC.
type iosAU struct {
	nals [][]byte
	key  bool
}

func (au iosAU) avcc() []byte {
	var out []byte
	var hdr [4]byte
	for _, nal := range au.nals {
		if len(nal) == 0 {
			continue
		}
		binary.BigEndian.PutUint32(hdr[:], uint32(len(nal)))
		out = append(out, hdr[:]...)
		out = append(out, nal...)
	}
	return out
}

// iosAssembler turns the companion's Annex-B byte stream into pictures.
// idb payload chunks are arbitrary: NALs split across chunks, many NALs per
// chunk, a NAL ends only where the next start code begins. So each picture
// is emitted once the following picture's start code proves it complete
// (~1 frame of extra latency, ~33ms at 30fps). Parameter sets ride along
// with the next picture and also drive the avcC description.
type iosAssembler struct {
	buf     []byte   // bytes of an unterminated NAL
	open    bool     // buf holds the start of a NAL
	pending [][]byte // non-VCL NALs (SPS/PPS/AUD/SEI) waiting for the next picture
	sps     []byte
	pps     []byte
	sentSPS []byte // params already turned into a broadcast avcC
	sentPPS []byte
}

// push ingests one payload chunk. It returns every picture that became
// complete, plus a description/codec pair when the SPS/PPS changed.
//
// Byte-stream framing means a NAL is only known to be finished once the next
// start code appears, so the newest picture (and a trailing SPS/PPS) is held
// back until the following chunk proves its end. In live video the next frame
// always arrives; at end of stream call flush.
func (a *iosAssembler) push(chunk []byte) (aus []iosAU, desc []byte, codec string) {
	b := append(a.buf, chunk...)
	nalStart := -1
	if a.open {
		nalStart = 0
	}
	var spans [][2]int
	for i := 0; i+2 < len(b); {
		n, ok := startCodeLen(b, i)
		if !ok {
			i++
			continue
		}
		if nalStart >= 0 && i > nalStart {
			spans = append(spans, [2]int{nalStart, i})
		}
		nalStart = i + n
		i = nalStart
	}
	for _, span := range spans {
		nal := b[span[0]:span[1]]
		if len(nal) == 0 {
			continue
		}
		switch typ := nal[0] & 0x1f; {
		case typ == 7: // SPS
			a.sps = append([]byte(nil), nal...)
			a.pending = append(a.pending, nal)
		case typ == 8: // PPS
			a.pps = append([]byte(nil), nal...)
			a.pending = append(a.pending, nal)
		case typ == 1 || typ == 5: // VCL: a picture is complete
			picture := make([][]byte, 0, len(a.pending)+1)
			picture = append(picture, a.pending...)
			picture = append(picture, nal)
			a.pending = a.pending[:0]
			aus = append(aus, iosAU{nals: picture, key: typ == 5})
		default: // AUD, SEI, filler: attach to the next picture
			a.pending = append(a.pending, nal)
		}
	}
	desc, codec = a.description()
	if len(a.pending) > 32 { // paranoia: no VCL for a long time
		a.pending = a.pending[len(a.pending)-32:]
	}
	if nalStart >= 0 {
		a.buf = b[nalStart:]
		a.open = true
	} else {
		// No start code in sight: between NALs this is junk, but a start code
		// may itself straddle the chunk boundary (00 00 | 01), so keep the
		// last few bytes and let the next chunk finish the job.
		if len(b) > 3 {
			b = b[len(b)-3:]
		}
		a.buf = append([]byte(nil), b...)
		a.open = false
	}
	return aus, desc, codec
}

// startCodeLen reports the length of the Annex-B start code at b[i], if any.
func startCodeLen(b []byte, i int) (int, bool) {
	if i+2 >= len(b) || b[i] != 0 || b[i+1] != 0 {
		return 0, false
	}
	if b[i+2] == 1 {
		return 3, true
	}
	if i+3 < len(b) && b[i+2] == 0 && b[i+3] == 1 {
		return 4, true
	}
	return 0, false
}

// flush emits a trailing picture/parameter set that will never be proven
// complete because no more bytes follow. Only correct at end of stream.
func (a *iosAssembler) flush() (aus []iosAU, desc []byte, codec string) {
	if !a.open || len(a.buf) == 0 {
		return nil, nil, ""
	}
	return a.push(scTerminator)
}

// description rebuilds the avcC box when the parameter sets changed; nil
// means "nothing new to tell viewers".
func (a *iosAssembler) description() ([]byte, string) {
	if len(a.sps) == 0 || len(a.pps) == 0 {
		return nil, ""
	}
	if bytes.Equal(a.sps, a.sentSPS) && bytes.Equal(a.pps, a.sentPPS) {
		return nil, ""
	}
	desc, err := scrcpy.AVCCDescription(a.sps, a.pps)
	if err != nil {
		return nil, ""
	}
	codec, err := scrcpy.CodecString(a.sps)
	if err != nil {
		return nil, ""
	}
	a.sentSPS = a.sps
	a.sentPPS = a.pps
	return desc, codec
}

// pumpIOS reads the companion's H.264 pipe, reassembles pictures, and
// broadcasts the same tagged AVCC frames the Android path sends.
func (ss *streamSession) pumpIOS() {
	defer ss.hub.closeSession(ss)
	vs, err := ss.ib.StartVideo(iosFps, iosScale)
	if err != nil {
		logf("idb video: %v", err)
		return
	}
	// vs is reassigned on restart, so the final stop needs the closure.
	defer func() { vs.Stop() }()
	asm := &iosAssembler{}
	for {
		select {
		case <-ss.done:
			return
		case <-ss.restart:
			// A late joiner needs a keyframe it can decode from, and the
			// companion only emits SPS/PPS+IDR when the pipe opens: reopen.
			vs.Stop()
			if vs, err = ss.ib.StartVideo(iosFps, iosScale); err != nil {
				logf("idb video restart: %v", err)
				return
			}
			asm = &iosAssembler{}
		case chunk, ok := <-vs.Frames:
			if !ok {
				return
			}
			aus, desc, codec := asm.push(chunk)
			if desc != nil {
				ss.mu.Lock()
				ss.desc, ss.codec = desc, codec
				ss.mu.Unlock()
				ss.broadcast(append([]byte{tagDesc}, desc...))
				ss.broadcastText(ss.metaJSON()) // viewers learn the codec
			}
			for _, au := range aus {
				avcc := au.avcc()
				if len(avcc) == 0 {
					continue
				}
				tag := byte(tagDelta)
				if au.key {
					tag = tagKey
				}
				ss.broadcast(append([]byte{tag}, avcc...))
			}
		}
	}
}

// requestIOSRestart asks pumpIOS to reopen the video pipe. A session that
// just started is left alone: its first keyframe is already on the way.
func (ss *streamSession) requestIOSRestart() {
	if time.Since(ss.started) < 5*time.Second {
		return
	}
	select {
	case ss.restart <- struct{}{}:
	default: // one pending restart is enough
	}
}

// ---------- control: viewer JSON in, HID out ----------

// iosTouch keeps the in-flight pointer gesture. iOS HID has no "move" event,
// so a press is replayed on release as either a tap or a swipe.
type iosTouch struct {
	mu     sync.Mutex
	active bool
	startX float64 // device points
	startY float64
}

// iosGesture is the replay of one pointer gesture: HID has no move event, so
// the whole press is decided on release.
type iosGesture struct {
	swipe    bool
	tap      bool
	x1, y1   float64
	x2, y2   float64
	duration float64
}

// releaseGesture turns a completed press into a tap (barely moved) or a swipe
// (travelled far enough). It is a free function so the decision is testable
// without a simulator attached.
func releaseGesture(t *iosTouch, endX, endY float64) iosGesture {
	t.mu.Lock()
	active, startX, startY := t.active, t.startX, t.startY
	t.active = false
	t.mu.Unlock()
	if active && math.Hypot(endX-startX, endY-startY) > iosDragThreshold {
		return iosGesture{
			swipe: true, x1: startX, y1: startY, x2: endX, y2: endY,
			duration: iosSwipeDragSecs,
		}
	}
	// Release with no press seen still taps: swallowing the gesture would
	// look like a dropped click.
	return iosGesture{tap: true, x1: endX, y1: endY, x2: endX, y2: endY, duration: iosSwipeDragSecs}
}

func (ss *streamSession) handleIOS(vm viewerMsg) {
	if ss.ib == nil {
		return
	}
	// The wire speaks video pixels; HID wants device points.
	pxPerPointX, pxPerPointY := ss.ib.Points()
	toPoint := func(x, y int) (float64, float64) {
		return float64(x) / pxPerPointX, float64(y) / pxPerPointY
	}
	switch vm.Type {
	case "touch":
		x, y := toPoint(vm.X, vm.Y)
		switch strings.ToLower(vm.Action) {
		case "down":
			ss.touch.mu.Lock()
			ss.touch.active, ss.touch.startX, ss.touch.startY = true, x, y
			ss.touch.mu.Unlock()
		case "up":
			g := releaseGesture(&ss.touch, x, y)
			ss.iosDo("gesture", func(ctx context.Context) error {
				if g.swipe {
					return ss.ib.Swipe(ctx, g.x1, g.y1, g.x2, g.y2, g.duration)
				}
				return ss.ib.Tap(ctx, g.x1, g.y1)
			})
		}
	case "scroll":
		// The page sends Android-style deltas in stream pixels. A swipe from
		// the pointer along the delta is the natural-scroll equivalent.
		x, y := toPoint(vm.X, vm.Y)
		endX, endY := x+vm.DX/pxPerPointX, y+vm.DY/pxPerPointY
		ss.iosDo("scroll", func(ctx context.Context) error {
			return ss.ib.Swipe(ctx, x, y, endX, endY, iosSwipeScrollSec)
		})
	case "key":
		ss.iosDo("key", func(ctx context.Context) error { return ss.iosKey(ctx, vm.Code) })
	case "text":
		ss.iosDo("text", func(ctx context.Context) error { return ss.ib.Text(ctx, vm.Text) })
	case "button":
		name, ok := iosButtons[strings.ToLower(vm.Button)]
		if !ok {
			return
		}
		ss.iosDo("button", func(ctx context.Context) error { return ss.ib.Button(ctx, name) })
	case "reset":
		ss.requestIOSRestart()
	}
}

// iosKey maps the viewer's Android-style keycodes onto iOS equivalents.
// Unknown codes are a no-op rather than an error, like the Android path.
func (ss *streamSession) iosKey(ctx context.Context, code int) error {
	_, err := ss.ib.AndroidKey(ctx, code)
	return err
}

// iosDo runs one HID action under a bounded context. A rejected gesture must
// never take down the viewer's socket, so errors only get logged.
func (ss *streamSession) iosDo(what string, fn func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), iosInputTimeout)
	defer cancel()
	if err := fn(ctx); err != nil {
		logf("idb %s: %v", what, err)
	}
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "stream: "+format+"\n", args...)
}
