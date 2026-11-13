package sdk

// iOS live stream backend: one pooled idb companion per simulator, framed
// into the same packets the Android path produces. The companion's Annex-B
// H.264 is reassembled into AVCC pictures; input goes in as HID taps,
// swipes, keys, text and hardware buttons. No screenshots on this path.

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/Adelodunpeter25/sim-go/internal/idb"
	"github.com/Adelodunpeter25/sim-go/internal/scrcpy"
)

const (
	iosFps            = 30
	iosScale          = 1.0 // native pixels
	iosDragThreshold  = 8.0 // points of travel that turn a press into a drag
	iosSwipeDragSecs  = 0.2
	iosSwipeScrollSec = 0.1
	iosInputTimeout   = 10 * time.Second
	// iosRestartGrace: a session this young already has its first keyframe
	// on the way, so a late joiner needs no video-pipe restart.
	iosRestartGrace = 5 * time.Second
)

// iosButtons maps the viewer's button names to HID buttons. Android-only
// buttons (back, menu, volume) have no iOS equivalent and are dropped.
var iosButtons = map[string]string{
	"home": "HOME", "power": "LOCK", "lock": "LOCK",
	"side": "SIDE_BUTTON", "siri": "SIRI",
}

// ---------- stream: Annex-B bytes in, AVCC pictures out ----------

// scTerminator is a synthetic start code: it proves the buffered NAL ended.
var scTerminator = []byte{0, 0, 0, 1}

// iosAU is one picture: its NALs with start codes stripped, ready for AVCC.
type iosAU struct {
	nals [][]byte
	key  bool
}

func (au iosAU) avcc() []byte {
	n := 0
	for _, nal := range au.nals {
		if len(nal) > 0 {
			n += 4 + len(nal)
		}
	}
	out := make([]byte, 0, n)
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
	cur     *iosAU   // picture still collecting slices
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
	// Nothing carried over: scan the chunk in place. The cap is clamped so a
	// later append can never write into memory the caller still owns.
	b := chunk[:len(chunk):len(chunk)]
	if len(a.buf) > 0 {
		b = append(a.buf, chunk...)
	}
	nalStart := -1
	if a.open {
		nalStart = 0
	}
	var spans [][2]int
	for pos := 0; ; {
		j := bytes.Index(b[pos:], startCode3)
		if j < 0 {
			break
		}
		i, n := pos+j, 3
		// A leading zero_byte makes it a 4-byte start code, unless that
		// zero already belongs to the previous start code.
		if i > 0 && b[i-1] == 0 && (nalStart < 0 || i-1 >= nalStart) {
			i, n = i-1, 4
		}
		if nalStart >= 0 && i > nalStart {
			spans = append(spans, [2]int{nalStart, i})
		}
		nalStart = i + n
		pos = nalStart
	}
	for _, span := range spans {
		nal := b[span[0]:span[1]]
		if len(nal) == 0 {
			continue
		}
		switch typ := nal[0] & 0x1f; {
		case typ == 7: // SPS
			a.emitCur(&aus)
			a.sps = append([]byte(nil), nal...)
			a.pending = append(a.pending, nal)
		case typ == 8: // PPS
			a.emitCur(&aus)
			a.pps = append([]byte(nil), nal...)
			a.pending = append(a.pending, nal)
		case typ == 1 || typ == 5: // VCL slice
			if startsPicture(nal) || a.cur == nil {
				a.emitCur(&aus)
				picture := make([][]byte, 0, len(a.pending)+1)
				picture = append(picture, a.pending...)
				a.pending = a.pending[:0]
				a.cur = &iosAU{nals: append(picture, nal), key: typ == 5}
			} else {
				// Later slice of the same picture: WebCodecs wants one
				// chunk per picture, not per slice.
				a.cur.nals = append(a.cur.nals, nal)
			}
		default: // AUD, SEI, filler: attach to the next picture
			a.emitCur(&aus)
			a.pending = append(a.pending, nal)
		}
	}
	// The open NAL's first bytes already say whether it starts a new
	// picture; deciding now avoids holding the current one a frame longer.
	if a.cur != nil && nalStart >= 0 && len(b)-nalStart >= 2 && startsPicture(b[nalStart:]) {
		a.emitCur(&aus)
	}
	desc, codec = a.description()
	if len(a.pending) > 32 { // paranoia: no VCL for a long time
		a.pending = a.pending[len(a.pending)-32:]
	}
	if nalStart >= 0 {
		a.buf = b[nalStart:len(b):len(b)]
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

var startCode3 = []byte{0, 0, 1}

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
		a.emitCur(&aus)
		return aus, nil, ""
	}
	aus, desc, codec = a.push(scTerminator)
	a.emitCur(&aus)
	return aus, desc, codec
}

// emitCur hands the picture under construction to the output.
func (a *iosAssembler) emitCur(aus *[]iosAU) {
	if a.cur != nil {
		*aus = append(*aus, *a.cur)
		a.cur = nil
	}
}

// startsPicture reports whether a NAL (header first) begins a new picture:
// any non-VCL NAL, or a slice with first_mb_in_slice == 0 (ue(v) "1" bit).
func startsPicture(nal []byte) bool {
	if len(nal) < 2 {
		return true
	}
	if typ := nal[0] & 0x1f; typ != 1 && typ != 5 {
		return true
	}
	return nal[1]&0x80 != 0
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

// iosBackend drives one pooled idb companion.
type iosBackend struct {
	lease   *idb.Lease
	ib      *idb.Session
	started time.Time
	touch   iosTouch
	liveMu  sync.Mutex
	live    *idb.TouchStream

	scrollMu      sync.Mutex
	scrollPending *Input
	scrollBusy    bool
	restart       chan struct{} // buffered(1): reopen the video pipe
	fps           uint64
	scale         float64
	bitrate       int

	mu sync.Mutex
	ss *session
	vs *idb.VideoStream
}

func openIOS(ctx context.Context, udid string, q StreamQuality) (backend, error) {
	lease, err := idb.DefaultPool.Acquire(ctx, udid)
	if err != nil {
		return nil, err
	}
	return &iosBackend{
		lease: lease, ib: lease.Session,
		started: time.Now(), restart: make(chan struct{}, 1),
		fps: q.iosFPS(), scale: q.iosScale(), bitrate: q.iosBitRate(),
	}, nil
}

func (b *iosBackend) info() (int, int, string) {
	desc := b.ib.Desc.GetTargetDescription()
	dims := desc.GetScreenDimensions()
	return int(dims.GetWidth()), int(dims.GetHeight()), desc.GetName()
}

// pump reads the companion's H.264 pipe, reassembles pictures, and publishes
// them. A restart request reopens the pipe if dead or no active viewers.
func (b *iosBackend) pump(ss *session) {
	b.mu.Lock()
	b.ss = ss
	b.mu.Unlock()

	vs, err := b.ib.StartVideo(b.fps, b.scale, b.bitrate)
	if err != nil {
		return
	}
	b.mu.Lock()
	b.vs = vs
	b.mu.Unlock()
	// vs is reassigned on restart, so stop whatever is currently active.
	defer func() {
		b.mu.Lock()
		cur := b.vs
		b.vs = nil
		b.mu.Unlock()
		if cur != nil {
			cur.Stop()
		}
	}()
	asm := &iosAssembler{}
	for {
		select {
		case <-ss.done:
			return
		case <-b.restart:
			// Do NOT stop the running vs if there are already active viewers enjoying the stream.
			// Only restart the video pipe if the pipe is dead or has no active viewers.
			if b.hasActiveViewers() && b.isPipeAlive() {
				continue
			}
			vs.Stop()
			if vs, err = b.ib.StartVideo(b.fps, b.scale, b.bitrate); err != nil {
				return
			}
			b.mu.Lock()
			b.vs = vs
			b.mu.Unlock()
			asm = &iosAssembler{}
		case chunk, ok := <-vs.Frames:
			if !ok {
				select {
				case <-ss.done:
					return
				default:
				}
				// The pipe died while the session is still active; try to reopen it.
				vs.Stop()
				if vs, err = b.ib.StartVideo(b.fps, b.scale, b.bitrate); err != nil {
					return
				}
				b.mu.Lock()
				b.vs = vs
				b.mu.Unlock()
				asm = &iosAssembler{}
				continue
			}
			aus, desc, codec := asm.push(chunk)
			if desc != nil {
				ss.publishDesc(desc, codec)
			}
			for _, au := range aus {
				if avcc := au.avcc(); len(avcc) > 0 {
					ss.publishFrame(au.key, avcc)
				}
			}
		}
	}
}

// reset asks pump to reopen the video pipe if dead or no active viewers.
// A session that just started is left alone: its first keyframe is already on the way.
// If active viewers are already watching the stream, the pipe is not interrupted.
func (b *iosBackend) reset() {
	if time.Since(b.started) < iosRestartGrace {
		return
	}
	if b.hasActiveViewers() && b.isPipeAlive() {
		return
	}
	select {
	case b.restart <- struct{}{}:
	default: // one pending restart is enough
	}
}

func (b *iosBackend) hasActiveViewers() bool {
	b.mu.Lock()
	ss := b.ss
	b.mu.Unlock()
	if ss == nil {
		return false
	}
	return ss.activeViewers() > 1
}

func (b *iosBackend) isPipeAlive() bool {
	b.mu.Lock()
	vs := b.vs
	b.mu.Unlock()
	if vs == nil {
		return false
	}
	return vs.Alive()
}

func (b *iosBackend) close() { b.lease.Release() }

// ---------- control: Input in, HID out ----------

// do runs one HID action under a bounded context.
func (b *iosBackend) do(fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), iosInputTimeout)
	defer cancel()
	return fn(ctx)
}

func (b *iosBackend) input(in Input) error {
	// The wire speaks video pixels; HID wants device points.
	pxX, pxY := b.ib.Points()
	toPoint := func(x, y int) (float64, float64) {
		return float64(x) / pxX, float64(y) / pxY
	}
	switch in.Type {
	case "touch":
		x, y := toPoint(in.X, in.Y)
		return b.touchInput(strings.ToLower(in.Action), x, y)
	case "scroll":
		// A wheel burst would queue one blocking HID swipe each and stall
		// every later input. Merge pending deltas; one worker drains them.
		b.queueScroll(in)
		return nil
	case "key":
		// Unknown codes are a no-op rather than an error, like Android.
		return b.do(func(ctx context.Context) error {
			_, err := b.ib.AndroidKey(ctx, in.Code)
			return err
		})
	case "text":
		return b.do(func(ctx context.Context) error { return b.ib.Text(ctx, in.Text) })
	case "button":
		name, ok := iosButtons[strings.ToLower(in.Button)]
		if !ok {
			return nil
		}
		return b.do(func(ctx context.Context) error { return b.ib.Button(ctx, name) })
	case "reset":
		b.reset()
		return nil
	}
	return fmt.Errorf("unknown input type %q", in.Type)
}

// queueScroll merges in into the pending scroll (deltas add up, the latest
// pointer position wins) and starts the worker if none is running.
func (b *iosBackend) queueScroll(in Input) {
	b.scrollMu.Lock()
	if b.scrollPending != nil {
		in.DX += b.scrollPending.DX
		in.DY += b.scrollPending.DY
	}
	b.scrollPending = &in
	start := !b.scrollBusy
	b.scrollBusy = true
	b.scrollMu.Unlock()
	if start {
		go b.drainScroll()
	}
}

func (b *iosBackend) drainScroll() {
	pxX, pxY := b.ib.Points()
	for {
		b.scrollMu.Lock()
		in := b.scrollPending
		b.scrollPending = nil
		if in == nil {
			b.scrollBusy = false
			b.scrollMu.Unlock()
			return
		}
		b.scrollMu.Unlock()
		x, y := float64(in.X)/pxX, float64(in.Y)/pxY
		endX, endY := x+in.DX/pxX, y+in.DY/pxY
		_ = b.do(func(ctx context.Context) error {
			return b.ib.Swipe(ctx, x, y, endX, endY, iosSwipeScrollSec)
		})
	}
}

// touchInput drives one finger. A live HID stream gives real down/move/up;
// if it cannot be opened the press is replayed on release as a tap or swipe.
func (b *iosBackend) touchInput(action string, x, y float64) error {
	switch action {
	case "down":
		b.endLive(x, y) // a lost "up" must not leave a finger stuck down
		b.touch.mu.Lock()
		b.touch.active, b.touch.startX, b.touch.startY = true, x, y
		b.touch.mu.Unlock()
		ts, err := b.ib.BeginTouch(x, y)
		if err != nil {
			return nil // fallback replays on release
		}
		b.liveMu.Lock()
		b.live = ts
		b.liveMu.Unlock()
	case "move":
		b.liveMu.Lock()
		ts := b.live
		b.liveMu.Unlock()
		if ts != nil {
			if err := ts.Move(x, y); err != nil {
				b.dropLive()
			}
		}
	case "up":
		b.liveMu.Lock()
		ts := b.live
		b.live = nil
		b.liveMu.Unlock()
		if ts != nil {
			b.touch.mu.Lock()
			b.touch.active = false
			b.touch.mu.Unlock()
			return ts.End(x, y)
		}
		g := releaseGesture(&b.touch, x, y)
		return b.do(func(ctx context.Context) error {
			if g.swipe {
				return b.ib.Swipe(ctx, g.x1, g.y1, g.x2, g.y2, g.duration)
			}
			return b.ib.Tap(ctx, g.x1, g.y1)
		})
	}
	return nil
}

func (b *iosBackend) endLive(x, y float64) {
	b.liveMu.Lock()
	ts := b.live
	b.live = nil
	b.liveMu.Unlock()
	if ts != nil {
		_ = ts.End(x, y)
	}
}

// dropLive abandons a broken stream; the rest of the gesture is ignored.
func (b *iosBackend) dropLive() {
	b.liveMu.Lock()
	b.live = nil
	b.liveMu.Unlock()
}

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
