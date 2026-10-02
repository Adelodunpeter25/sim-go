package sdk

import (
	"errors"
	"sync"
)

// Live streaming: H.264 (AVCC) out, input events in, one shared backend
// session per device with N Stream handles on top.
//
//	s, err := c.Stream(ctx, "android", "Pixel_7")
//	defer s.Close()
//	for p := range s.Packets() { ... }   // Meta, Description, KeyFrame, Delta
//	s.Touch("down", x, y)

// PacketKind says what a Packet carries.
type PacketKind int

const (
	// PacketMeta carries Packet.Meta: size, device name, codec. Sent first,
	// and again whenever the codec becomes known or changes.
	PacketMeta PacketKind = iota + 1
	// PacketDescription is the avcC decoder configuration (WebCodecs
	// "description"). It always precedes the first key frame a viewer sees.
	PacketDescription
	// PacketKeyFrame is an AVCC-framed IDR picture.
	PacketKeyFrame
	// PacketDelta is an AVCC-framed non-key picture.
	PacketDelta
)

// StreamMeta describes the video the stream carries.
type StreamMeta struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Name   string `json:"name"`
	Codec  string `json:"codec"`
}

// Packet is one unit of output. Data is shared between viewers: read-only.
type Packet struct {
	Kind PacketKind
	Meta StreamMeta // PacketMeta only
	Data []byte     // every other kind
}

// Input is one control event from a viewer. The JSON shape is the viewer
// wire contract, so a websocket adapter can decode straight into it.
//
//	{"type":"touch","action":"down|move|up","x":..,"y":..}
//	{"type":"scroll","x":..,"y":..,"dx":..,"dy":..}
//	{"type":"key","code":66}   {"type":"text","text":"hi"}
//	{"type":"button","button":"home"}   {"type":"reset"}
//
// X, Y are stream pixels, the same space as StreamMeta.
type Input struct {
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

var (
	// ErrInvalidPlatform: platform is not ios or android.
	ErrInvalidPlatform = errors.New("platform must be android or ios")
	// ErrDeviceNotFound: no such device, or it is not booted.
	ErrDeviceNotFound = errors.New("device not found")
	// ErrBackendStart: the device exists but its stream backend failed to start.
	ErrBackendStart = errors.New("stream backend failed to start")
	// ErrStreamClosed: Input on a Stream that was closed or ended.
	ErrStreamClosed = errors.New("stream closed")
	// ErrStreamEnded is Err() when the device session ended under the viewer.
	ErrStreamEnded = errors.New("stream ended")
	// ErrSlowViewer is Err() when the viewer fell behind and was dropped.
	ErrSlowViewer = errors.New("viewer too slow, dropped")
)

// tagged keeps the original message while letting errors.Is match a kind.
type tagged struct {
	err  error
	kind error
}

func (t *tagged) Error() string        { return t.err.Error() }
func (t *tagged) Unwrap() error        { return t.err }
func (t *tagged) Is(target error) bool { return target == t.kind }

// packetBuffer is how many packets a viewer may lag behind (~2s at 60fps)
// before it is dropped. The encoder side never blocks on a viewer.
const packetBuffer = 120

// Stream is one viewer's handle on a shared device session.
type Stream struct {
	sess *session
	ch   chan Packet

	mu     sync.Mutex
	closed bool
	err    error
}

// Meta returns the current stream description.
func (s *Stream) Meta() StreamMeta { return s.sess.meta() }

// Packets delivers output in order. It is closed when the stream ends:
// after Close, when the device session dies (Err = ErrStreamEnded), or when
// this viewer is too slow (Err = ErrSlowViewer).
func (s *Stream) Packets() <-chan Packet { return s.ch }

// Err reports why Packets closed; nil while open or after a plain Close.
func (s *Stream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Input sends one control event to the device. It may block while the
// backend injects it (iOS HID is bounded at 10s).
func (s *Stream) Input(in Input) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return ErrStreamClosed
	}
	return s.sess.be.input(in)
}

// Touch sends a pointer event. action is "down", "move" or "up".
func (s *Stream) Touch(action string, x, y int) error {
	return s.Input(Input{Type: "touch", Action: action, X: x, Y: y})
}

// Scroll sends a wheel event at (x, y) with pixel deltas.
func (s *Stream) Scroll(x, y int, dx, dy float64) error {
	return s.Input(Input{Type: "scroll", X: x, Y: y, DX: dx, DY: dy})
}

// Key sends an Android-style keycode (e.g. 66 Enter, 67 Del, 3 Home).
func (s *Stream) Key(code int) error { return s.Input(Input{Type: "key", Code: code}) }

// Text types a string.
func (s *Stream) Text(text string) error { return s.Input(Input{Type: "text", Text: text}) }

// Button presses a named button: home, back, menu, power, volume-up, ...
func (s *Stream) Button(name string) error { return s.Input(Input{Type: "button", Button: name}) }

// Reset asks the backend for a fresh key frame.
func (s *Stream) Reset() error { return s.Input(Input{Type: "reset"}) }

// Close detaches this viewer. The last Close arms a 30s idle timer, after
// which the shared device session is torn down unless someone re-attaches.
func (s *Stream) Close() error {
	s.sess.detach(s, nil)
	return nil
}

// push delivers without ever blocking: a full channel drops this viewer.
func (s *Stream) push(p Packet) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	select {
	case s.ch <- p:
		s.mu.Unlock()
		return
	default:
	}
	s.mu.Unlock()
	s.sess.detach(s, ErrSlowViewer)
}

// finish closes the channel exactly once.
func (s *Stream) finish(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.err = err
	close(s.ch)
}
