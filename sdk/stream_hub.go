package sdk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// streamIdle is how long a session with no viewers stays up.
const streamIdle = 30 * time.Second

// backend is one platform's device session: it produces frames into the
// session and consumes input. Implementations: scrcpy (android), idb (ios).
type backend interface {
	// info is the video size and device name, known once started.
	info() (width, height int, name string)
	// pump reads device output and publishes it until the stream ends or
	// ss.done closes.
	pump(ss *session)
	input(in Input) error
	// reset makes the next frames start with a key frame (late joiner).
	reset()
	// close releases the device session. Idempotent.
	close()
}

type streamHub struct {
	mu       sync.Mutex
	sessions map[string]*session
	idle     time.Duration
	list     func(ctx context.Context) ([]Device, error)
	open     func(ctx context.Context, platform, id string) (backend, error)
}

func newStreamHub(list func(context.Context) ([]Device, error)) *streamHub {
	return &streamHub{
		sessions: map[string]*session{},
		idle:     streamIdle,
		list:     list,
		open:     openBackend,
	}
}

func openBackend(ctx context.Context, platform, id string) (backend, error) {
	switch platform {
	case "android":
		return openAndroid(ctx, id)
	case "ios":
		return nil, errors.New("ios streaming is not wired into the sdk yet")
	}
	return nil, ErrInvalidPlatform
}

// Stream attaches a viewer to the live stream of a booted device, starting
// the shared backend session if this is the first viewer. id may be a
// serial, an AVD name, a UDID or a simulator name.
//
// Errors: ErrInvalidPlatform, ErrDeviceNotFound (also not booted), or
// ErrBackendStart (check with errors.Is).
func (c *Client) Stream(ctx context.Context, platform, id string) (*Stream, error) {
	if platform != "android" && platform != "ios" {
		return nil, ErrInvalidPlatform
	}
	rid, err := c.hub.resolve(ctx, platform, id)
	if err != nil {
		return nil, err
	}
	// A session can end between lookup and attach; one retry starts a new one.
	for i := 0; i < 2; i++ {
		ss, err := c.hub.session(platform, rid)
		if err != nil {
			return nil, err
		}
		if st := ss.attach(); st != nil {
			return st, nil
		}
	}
	return nil, ErrStreamEnded
}

func notFound(format string, args ...any) error {
	return &tagged{err: fmt.Errorf(format, args...), kind: ErrDeviceNotFound}
}

// resolve turns whatever the caller sent into the identifier backends key on.
func (h *streamHub) resolve(ctx context.Context, platform, id string) (string, error) {
	devs, err := h.list(ctx)
	if err != nil {
		return "", &tagged{err: err, kind: ErrDeviceNotFound}
	}
	if platform == "android" {
		return resolveSerial(devs, id)
	}
	return resolveUDID(devs, id)
}

// resolveSerial accepts an emulator serial or an AVD name with a live
// serial. A serial-shaped id is still verified against the live list so a
// typo fails as "not found" instead of a confusing adb error.
func resolveSerial(devs []Device, id string) (string, error) {
	if strings.HasPrefix(id, "emulator-") {
		for _, d := range devs {
			if d.ID == id && d.State == "device" {
				return id, nil
			}
		}
		return "", notFound("emulator %s is not running (boot it first)", id)
	}
	for _, d := range devs {
		if d.Platform == "android" && d.Name == id && strings.HasPrefix(d.ID, "emulator-") {
			return d.ID, nil
		}
	}
	return "", notFound("no booted emulator for AVD %q (boot it first)", id)
}

// resolveUDID accepts a booted simulator by UDID or name. A shutdown sim is
// reported as such instead of failing later inside the companion.
func resolveUDID(devs []Device, id string) (string, error) {
	for _, d := range devs {
		if d.Platform != "ios" || (d.ID != id && d.Name != id) {
			continue
		}
		if d.State != "Booted" {
			return "", notFound("%s is %s (boot it first)", d.Name, strings.ToLower(d.State))
		}
		return d.ID, nil
	}
	return "", notFound("no iOS simulator %q", id)
}

// session returns the shared session for platform:id, starting it if needed.
func (h *streamHub) session(platform, id string) (*session, error) {
	key := platform + ":" + id
	h.mu.Lock()
	if ss, ok := h.sessions[key]; ok {
		h.mu.Unlock()
		return ss, nil
	}
	h.mu.Unlock()

	// The session outlives the caller's request: keep values, drop cancel.
	be, err := h.open(context.WithoutCancel(context.Background()), platform, id)
	if err != nil {
		return nil, &tagged{err: err, kind: ErrBackendStart}
	}
	w, ht, name := be.info()
	ss := &session{
		hub: h, key: key, platform: platform,
		width: w, height: ht, name: name,
		be: be, handles: map[*Stream]struct{}{}, done: make(chan struct{}),
	}
	h.mu.Lock()
	if dup, ok := h.sessions[key]; ok {
		h.mu.Unlock()
		be.close()
		return dup, nil
	}
	h.sessions[key] = ss
	h.mu.Unlock()
	go ss.run()
	return ss, nil
}

func (h *streamHub) remove(ss *session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions[ss.key] == ss {
		delete(h.sessions, ss.key)
	}
}

// closeAll ends every session (Client.Close).
func (h *streamHub) closeAll() {
	h.mu.Lock()
	all := make([]*session, 0, len(h.sessions))
	for _, ss := range h.sessions {
		all = append(all, ss)
	}
	h.mu.Unlock()
	for _, ss := range all {
		h.closeSession(ss)
	}
}

// closeSession ends a session unconditionally: viewers are told, the
// backend is released. Safe to call more than once.
func (h *streamHub) closeSession(ss *session) {
	h.remove(ss)
	ss.mu.Lock()
	already := ss.closed
	ss.closed = true
	handles := make([]*Stream, 0, len(ss.handles))
	for st := range ss.handles {
		handles = append(handles, st)
	}
	ss.handles = map[*Stream]struct{}{}
	if ss.idle != nil {
		ss.idle.Stop()
		ss.idle = nil
	}
	ss.mu.Unlock()
	for _, st := range handles {
		st.finish(ErrStreamEnded)
	}
	if already {
		return
	}
	close(ss.done)
	ss.be.close()
}

// closeIfIdle is the idle timer's action: a viewer that attached just as
// the timer fired keeps the session alive.
func (h *streamHub) closeIfIdle(ss *session) {
	ss.mu.Lock()
	busy := len(ss.handles) > 0
	ss.idle = nil
	ss.mu.Unlock()
	if !busy {
		h.closeSession(ss)
	}
}

// session is one device's live stream, shared by every Stream handle.
type session struct {
	hub      *streamHub
	key      string // "<platform>:<serial|udid>"
	platform string
	width    int
	height   int
	name     string
	be       backend
	done     chan struct{}

	mu      sync.Mutex
	codec   string
	desc    []byte
	handles map[*Stream]struct{}
	idle    *time.Timer
	closed  bool
}

func (ss *session) run() {
	defer ss.hub.closeSession(ss)
	ss.be.pump(ss)
}

func (ss *session) meta() StreamMeta {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.metaLocked()
}

func (ss *session) metaLocked() StreamMeta {
	return StreamMeta{Width: ss.width, Height: ss.height, Name: ss.name, Codec: ss.codec}
}

// attach creates a viewer handle, or nil if the session already ended.
// A late joiner gets meta, then the cached avcC, then a forced key frame.
// Those initial packets are queued before the handle is registered, so no
// live frame can overtake them.
func (ss *session) attach() *Stream {
	st := &Stream{sess: ss, ch: make(chan Packet, packetBuffer)}
	ss.mu.Lock()
	if ss.closed {
		ss.mu.Unlock()
		return nil
	}
	st.ch <- Packet{Kind: PacketMeta, Meta: ss.metaLocked()}
	late := len(ss.desc) > 0
	if late {
		st.ch <- Packet{Kind: PacketDescription, Data: ss.desc}
	}
	ss.handles[st] = struct{}{}
	if ss.idle != nil {
		ss.idle.Stop()
		ss.idle = nil
	}
	ss.mu.Unlock()
	if late {
		// scrcpy re-syncs its encoder in place; idb only emits SPS/PPS+IDR
		// on a fresh video pipe.
		ss.be.reset()
	}
	return st
}

// detach removes a viewer; the last one arms the idle close.
func (ss *session) detach(st *Stream, err error) {
	ss.mu.Lock()
	_, was := ss.handles[st]
	delete(ss.handles, st)
	if was && len(ss.handles) == 0 && ss.idle == nil && !ss.closed {
		ss.idle = time.AfterFunc(ss.hub.idle, func() { ss.hub.closeIfIdle(ss) })
	}
	ss.mu.Unlock()
	st.finish(err)
}

// publishDesc records a new avcC and tells everyone, then re-announces meta
// so viewers learn the codec.
func (ss *session) publishDesc(desc []byte, codec string) {
	ss.mu.Lock()
	ss.desc, ss.codec = desc, codec
	meta := ss.metaLocked()
	ss.mu.Unlock()
	ss.broadcast(Packet{Kind: PacketDescription, Data: desc})
	ss.broadcast(Packet{Kind: PacketMeta, Meta: meta})
}

// publishFrame sends one AVCC picture.
func (ss *session) publishFrame(key bool, avcc []byte) {
	kind := PacketDelta
	if key {
		kind = PacketKeyFrame
	}
	ss.broadcast(Packet{Kind: kind, Data: avcc})
}

func (ss *session) broadcast(p Packet) {
	ss.mu.Lock()
	handles := make([]*Stream, 0, len(ss.handles))
	for st := range ss.handles {
		handles = append(handles, st)
	}
	ss.mu.Unlock()
	for _, st := range handles {
		st.push(p)
	}
}
