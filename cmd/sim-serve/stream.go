package main

// Live stream hub: one session per device, shared by every browser viewer.
// Android uses scrcpy, iOS uses idb's HID + H.264 companion; both hand
// viewers the same contract — H.264 (AVCC) out, JSON control messages in.
// No screenshots anywhere on this path.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Adelodunpeter25/sim-go/internal/idb"
	"github.com/Adelodunpeter25/sim-go/internal/scrcpy"
	"github.com/Adelodunpeter25/sim-go/sdk"
)

// Wire tags mirror simfleet/t3code: 1 description (avcC), 2 keyframe, 3 delta.
const (
	tagDesc  = 1
	tagKey   = 2
	tagDelta = 3
)

type viewerMsg struct {
	Type   string  `json:"type"`
	Action string  `json:"action,omitempty"`
	X      int     `json:"x,omitempty"`
	Y      int     `json:"y,omitempty"`
	DX     float64 `json:"dx,omitempty"`
	DY     float64 `json:"dy,omitempty"`
	Code   int     `json:"code,omitempty"`
	Text   string  `json:"text,omitempty"`
	Button string  `json:"button,omitempty"`
}

var buttonKeycodes = map[string]int{
	"home": 3, "back": 4, "menu": 82, "power": 26,
	"volume-up": 24, "volume-down": 25,
}

type viewer struct {
	conn   *wsConn
	closed bool
	mu     sync.Mutex
}

func (v *viewer) send(data []byte, binary bool) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return fmt.Errorf("viewer gone")
	}
	if binary {
		return v.conn.WriteBinary(data)
	}
	return v.conn.WriteText(data)
}

func (v *viewer) close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.closed {
		v.closed = true
		_ = v.conn.Close()
	}
}

// streamSession is one device's live stream, whichever backend drives it.
// Exactly one of sc (android) or ib (ios) is set.
type streamSession struct {
	key      string // hub key: "<platform>:<serial|udid>"
	platform string
	width    int
	height   int
	name     string
	started  time.Time

	sc      *scrcpy.Session
	ib      *idb.Session
	ibLease *idb.Lease
	touch   iosTouch
	restart chan struct{} // buffered(1): reopen the iOS video pipe

	codec   string
	desc    []byte
	viewers map[*viewer]bool
	mu      sync.Mutex
	done    chan struct{}
	idle    *time.Timer
	hub     *streamHub
}

type streamHub struct {
	mu       sync.Mutex
	sessions map[string]*streamSession
	sdk      *sdk.Client
}

func newStreamHub(c *sdk.Client) *streamHub {
	return &streamHub{sessions: map[string]*streamSession{}, sdk: c}
}

// attach resolves the device, starts (or reuses) the session, and upgrades.
func (h *streamHub) attach(w http.ResponseWriter, r *http.Request) {
	platform := r.URL.Query().Get("platform")
	if platform != "android" && platform != "ios" {
		http.Error(w, `{"ok":false,"error":"platform must be android or ios"}`, http.StatusBadRequest)
		return
	}
	id, err := h.resolveID(r.Context(), platform, r.URL.Query().Get("id"))
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusNotFound)
		return
	}
	ss, err := h.sessionFor(platform, id)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusBadGateway)
		return
	}
	conn, err := serveWS(w, r)
	if err != nil {
		h.dropIfIdle(ss)
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	v := &viewer{conn: conn}
	ss.addViewer(v)
	go ss.readLoop(v)
}

func writeJSONError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
}

// resolveID turns whatever the viewer sent (a serial, an AVD name, a UDID or
// a simulator name) into the identifier the backends key on.
func (h *streamHub) resolveID(ctx context.Context, platform, id string) (string, error) {
	if platform == "android" {
		return h.resolveSerial(ctx, id)
	}
	return h.resolveUDID(ctx, id)
}

// resolveSerial accepts an emulator serial or an AVD name with a live serial.
// A serial-shaped id is still verified against the live device list so a typo
// fails as "not found" instead of a confusing adb error from the backend.
func (h *streamHub) resolveSerial(ctx context.Context, id string) (string, error) {
	if strings.HasPrefix(id, "emulator-") {
		devs, err := h.sdk.ListAll(ctx)
		if err != nil {
			return "", err
		}
		for _, d := range devs {
			if d.ID == id && d.State == "device" {
				return id, nil
			}
		}
		return "", fmt.Errorf("emulator %s is not running (boot it first)", id)
	}
	devs, err := h.sdk.ListAll(ctx)
	if err != nil {
		return "", err
	}
	for _, d := range devs {
		if d.Platform == "android" && d.Name == id && strings.HasPrefix(d.ID, "emulator-") {
			return d.ID, nil
		}
	}
	return "", fmt.Errorf("no booted emulator for AVD %q (boot it first)", id)
}

// resolveUDID accepts a booted simulator by UDID or name. A shutdown sim is
// reported as such instead of failing later inside the companion.
func (h *streamHub) resolveUDID(ctx context.Context, id string) (string, error) {
	devs, err := h.sdk.ListAll(ctx)
	if err != nil {
		return "", err
	}
	for _, d := range devs {
		if d.Platform != "ios" || (d.ID != id && d.Name != id) {
			continue
		}
		if d.State != "Booted" {
			return "", fmt.Errorf("%s is %s (boot it first)", d.Name, strings.ToLower(d.State))
		}
		return d.ID, nil
	}
	return "", fmt.Errorf("no iOS simulator %q", id)
}

func (h *streamHub) sessionFor(platform, id string) (*streamSession, error) {
	key := platform + ":" + id
	h.mu.Lock()
	if ss, ok := h.sessions[key]; ok {
		h.mu.Unlock()
		return ss, nil
	}
	h.mu.Unlock()

	ss := &streamSession{
		key:      key,
		platform: platform,
		started:  time.Now(),
		viewers:  map[*viewer]bool{},
		done:     make(chan struct{}),
		restart:  make(chan struct{}, 1),
		hub:      h,
	}
	var err error
	switch platform {
	case "android":
		ss.sc, err = scrcpy.Start(context.Background(), id)
		if err != nil {
			return nil, err
		}
		ss.width, ss.height, ss.name = ss.sc.Meta.Width, ss.sc.Meta.Height, ss.sc.Meta.Name
	case "ios":
		ss.ibLease, err = idb.DefaultPool.Acquire(context.Background(), id)
		if err != nil {
			return nil, err
		}
		ss.ib = ss.ibLease.Session
		dims := ss.ib.Desc.GetTargetDescription().GetScreenDimensions()
		ss.width, ss.height = int(dims.GetWidth()), int(dims.GetHeight())
		ss.name = ss.ib.Desc.GetTargetDescription().GetName()
	}
	h.mu.Lock()
	if dup, ok := h.sessions[key]; ok {
		h.mu.Unlock()
		ss.closeBackend()
		return dup, nil
	}
	h.sessions[key] = ss
	h.mu.Unlock()
	go ss.pump()
	return ss, nil
}

// closeBackend releases the device session. Idempotent.
func (ss *streamSession) closeBackend() {
	if ss.sc != nil {
		ss.sc.Close()
	}
	if ss.ibLease != nil {
		ss.ibLease.Release() // pooled: the companion outlives us until idle
	}
}

func (h *streamHub) remove(ss *streamSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions[ss.key] == ss {
		delete(h.sessions, ss.key)
	}
}

func (ss *streamSession) metaJSON() []byte {
	meta, _ := json.Marshal(map[string]any{
		"type": "meta", "width": ss.width, "height": ss.height,
		"name": ss.name, "codec": ss.codec,
	})
	return meta
}

func (ss *streamSession) addViewer(v *viewer) {
	_ = v.send(ss.metaJSON(), false)
	ss.mu.Lock()
	desc := ss.desc
	ss.viewers[v] = true
	if ss.idle != nil {
		ss.idle.Stop()
		ss.idle = nil
	}
	ss.mu.Unlock()
	if len(desc) > 0 {
		pkt := append([]byte{tagDesc}, desc...)
		_ = v.send(pkt, true)
		// Force a fresh keyframe for the late joiner: scrcpy re-syncs its
		// encoder in place, idb only emits SPS/PPS+IDR on a new pipe.
		switch {
		case ss.sc != nil:
			_ = ss.sc.ResetVideo()
		case ss.ib != nil:
			ss.requestIOSRestart()
		}
	}
}

func (ss *streamSession) drop(v *viewer) {
	v.close()
	ss.mu.Lock()
	delete(ss.viewers, v)
	empty := len(ss.viewers) == 0
	if empty && ss.idle == nil {
		ss.idle = time.AfterFunc(30*time.Second, func() { ss.hub.closeSession(ss) })
	}
	ss.mu.Unlock()
}

func (h *streamHub) dropIfIdle(ss *streamSession) {
	ss.mu.Lock()
	empty := len(ss.viewers) == 0
	ss.mu.Unlock()
	if empty {
		h.closeSession(ss)
	}
}

func (h *streamHub) closeSession(ss *streamSession) {
	h.remove(ss)
	ss.mu.Lock()
	viewers := make([]*viewer, 0, len(ss.viewers))
	for v := range ss.viewers {
		viewers = append(viewers, v)
	}
	ss.viewers = map[*viewer]bool{}
	if ss.idle != nil {
		ss.idle.Stop()
		ss.idle = nil
	}
	ss.mu.Unlock()
	for _, v := range viewers {
		v.close()
	}
	select {
	case <-ss.done:
	default:
		close(ss.done)
	}
	ss.closeBackend()
}

// pump reads device frames forever: config rebuilds the avcC description,
// media frames go out as AVCC tagged key/delta.
func (ss *streamSession) pump() {
	defer ss.hub.closeSession(ss)
	if ss.platform == "ios" {
		ss.pumpIOS()
		return
	}
	for {
		select {
		case <-ss.done:
			return
		default:
		}
		f, err := ss.sc.ReadFrame()
		if err != nil {
			return
		}
		if f.Config {
			_, _, sps, pps := scrcpy.ToAVCC(f.Payload)
			if len(sps) == 0 || len(pps) == 0 {
				continue
			}
			desc, err := scrcpy.AVCCDescription(sps, pps)
			if err != nil {
				continue
			}
			codec, err := scrcpy.CodecString(sps)
			if err != nil {
				continue
			}
			ss.mu.Lock()
			ss.desc, ss.codec = desc, codec
			ss.mu.Unlock()
			ss.broadcast(append([]byte{tagDesc}, desc...))
			ss.broadcastText(ss.metaJSON()) // late/meta-less viewers learn the codec
			continue
		}
		avcc, isKey, _, _ := scrcpy.ToAVCC(f.Payload)
		if len(avcc) == 0 {
			continue
		}
		tag := byte(tagDelta)
		if isKey || f.Key {
			tag = tagKey
		}
		ss.broadcast(append([]byte{tag}, avcc...))
	}
}

func (ss *streamSession) broadcast(pkt []byte) {
	ss.broadcastRaw(pkt, true)
}

func (ss *streamSession) broadcastText(pkt []byte) {
	ss.broadcastRaw(pkt, false)
}

func (ss *streamSession) broadcastRaw(pkt []byte, binary bool) {
	ss.mu.Lock()
	viewers := make([]*viewer, 0, len(ss.viewers))
	for v := range ss.viewers {
		viewers = append(viewers, v)
	}
	ss.mu.Unlock()
	for _, v := range viewers {
		if err := v.send(pkt, binary); err != nil {
			go ss.drop(v)
		}
	}
}

func (ss *streamSession) readLoop(v *viewer) {
	defer ss.drop(v)
	for {
		msg, err := v.conn.read()
		if err != nil {
			return
		}
		if msg.opcode != wsText {
			continue
		}
		var vm viewerMsg
		if err := json.Unmarshal(msg.data, &vm); err != nil {
			continue
		}
		ss.handle(vm)
	}
}

func (ss *streamSession) handle(vm viewerMsg) {
	if ss.platform == "ios" {
		ss.handleIOS(vm)
		return
	}
	w, h := ss.sc.Meta.Width, ss.sc.Meta.Height
	switch vm.Type {
	case "touch":
		action := scrcpy.TouchMove
		switch strings.ToLower(vm.Action) {
		case "down":
			action = scrcpy.TouchDown
		case "up":
			action = scrcpy.TouchUp
		}
		_ = ss.sendTouch(action, vm.X, vm.Y, w, h)
	case "scroll":
		_ = ss.sc.SendRaw(scrcpy.EncodeScroll(vm.X, vm.Y, w, h, vm.DX, vm.DY))
	case "key":
		_ = ss.sc.Key(vm.Code)
	case "text":
		_ = ss.sc.Text(vm.Text)
	case "button":
		if code, ok := buttonKeycodes[strings.ToLower(vm.Button)]; ok {
			_ = ss.sc.Key(code)
		}
	case "reset":
		_ = ss.sc.ResetVideo()
	}
}

func (ss *streamSession) sendTouch(action, x, y, w, h int) error {
	return ss.sc.SendRaw(scrcpy.EncodeTouch(action, x, y, w, h))
}
