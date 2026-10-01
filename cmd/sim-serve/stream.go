package main

// Android live stream: one scrcpy.Session per emulator serial, shared by
// every browser viewer. Viewers get H.264 (AVCC) over the socket and send
// JSON control messages back. No screenshots anywhere on this path.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Adelodunpeter25/sim-go/internal/scrcpy"
	"github.com/Adelodunpeter25/sim-go/internal/sdk"
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

type streamSession struct {
	serial  string
	sc      *scrcpy.Session
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

// attach resolves the serial, starts (or reuses) the session, and upgrades.
func (h *streamHub) attach(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("platform") != "android" {
		http.Error(w, `{"ok":false,"error":"live stream is android-only in v1 (iOS needs the Phase 4 helper)"}`, http.StatusBadRequest)
		return
	}
	serial, err := h.resolveSerial(r.Context(), r.URL.Query().Get("id"))
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusNotFound)
		return
	}
	ss, err := h.sessionFor(serial)
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

// resolveSerial accepts an emulator serial or an AVD name with a live serial.
func (h *streamHub) resolveSerial(ctx context.Context, id string) (string, error) {
	if strings.HasPrefix(id, "emulator-") {
		return id, nil
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

func (h *streamHub) sessionFor(serial string) (*streamSession, error) {
	h.mu.Lock()
	if ss, ok := h.sessions[serial]; ok {
		h.mu.Unlock()
		return ss, nil
	}
	h.mu.Unlock()

	sc, err := scrcpy.Start(context.Background(), serial)
	if err != nil {
		return nil, err
	}
	ss := &streamSession{
		serial:  serial,
		sc:      sc,
		viewers: map[*viewer]bool{},
		done:    make(chan struct{}),
		hub:     h,
	}
	h.mu.Lock()
	if dup, ok := h.sessions[serial]; ok {
		h.mu.Unlock()
		sc.Close()
		return dup, nil
	}
	h.sessions[serial] = ss
	h.mu.Unlock()
	go ss.pump()
	return ss, nil
}

func (h *streamHub) remove(ss *streamSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions[ss.serial] == ss {
		delete(h.sessions, ss.serial)
	}
}

func (ss *streamSession) metaJSON() []byte {
	meta, _ := json.Marshal(map[string]any{
		"type": "meta", "width": ss.sc.Meta.Width, "height": ss.sc.Meta.Height,
		"name": ss.sc.Meta.Name, "codec": ss.codec,
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
		_ = ss.sc.ResetVideo() // fresh keyframe for the late joiner
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
	ss.sc.Close()
}

// pump reads device frames forever: config rebuilds the avcC description,
// media frames go out as AVCC tagged key/delta.
func (ss *streamSession) pump() {
	defer ss.hub.closeSession(ss)
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
