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

// StreamQuality tunes the encoder when a session starts. Zero fields keep
// the defaults (4 Mbps; Android 1280px/60fps, iOS native/30fps).
type StreamQuality struct {
	MaxSize int     // Android: longest edge in pixels
	MaxFPS  int     // frames per second
	BitRate int     // bits per second
	Scale   float64 // iOS: 1.0 = native pixels
}

func (q StreamQuality) iosFPS() uint64 {
	if q.MaxFPS > 0 {
		return uint64(q.MaxFPS)
	}
	return iosFps
}

func (q StreamQuality) iosBitRate() int {
	if q.BitRate > 0 {
		return q.BitRate
	}
	return defaultBitRate
}

// defaultBitRate is the encoder target when the viewer does not choose one.
const defaultBitRate = 4_000_000

func (q StreamQuality) iosScale() float64 {
	if q.Scale > 0 {
		return q.Scale
	}
	return iosScale
}

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

// packetBuffer is how many packets a viewer may buffer (~0.5s at 30fps) before
// congestion handling kicks in. Kept small on purpose: a deeper queue only
// adds seconds of latency to a screen mirror.
const packetBuffer = 16

// maxDrops is how many consecutive frames a congested viewer may drop before being disconnected.
const maxDrops = 240 // ~4s of deltas at 60fps without a keyframe getting through

// Stream is one viewer's handle on a shared device session.
type Stream struct {
	sess *session
	ch   chan Packet

	mu       sync.Mutex
	closed   bool
	err      error
	dropping bool
	drops    int
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

// push delivers without ever blocking: drops deltas during congestion, and only
// drops the viewer if it remains completely stalled.
func (s *Stream) push(p Packet) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}

	// A healthy viewer that drained below half clears the counter. While
	// dropping, only a key frame does: a flushed queue is empty by design.
	if !s.dropping && len(s.ch) < packetBuffer/2 {
		s.drops = 0
	}

	// While in dropping mode, continue dropping delta frames until a keyframe arrives.
	if s.dropping && p.Kind == PacketDelta {
		s.drops++
		if s.drops >= maxDrops {
			s.mu.Unlock()
			s.sess.detach(s, ErrSlowViewer)
			return
		}
		s.mu.Unlock()
		return
	}

	select {
	case s.ch <- p:
		if p.Kind == PacketKeyFrame {
			s.dropping = false
			s.drops = 0
		}
		s.mu.Unlock()
		return
	default:
	}

	// Channel is full: the queued frames are stale. Flush them (keeping the
	// decoder setup packets) and resync on the next key frame.
	s.flushFramesLocked()
	if p.Kind == PacketDelta {
		s.drops++
		wasDropping := s.dropping
		s.dropping = true
		if s.drops >= maxDrops {
			s.mu.Unlock()
			s.sess.detach(s, ErrSlowViewer)
			return
		}
		s.mu.Unlock()
		if !wasDropping {
			s.sess.be.reset()
		}
		return
	}

	s.ch <- p // flushed, so there is room
	if p.Kind == PacketKeyFrame {
		s.dropping = false
		s.drops = 0
	}
	s.mu.Unlock()
}

// flushFramesLocked discards queued pictures but keeps Meta and Description
// packets, which a decoder needs before it can use any later frame. s.mu held.
func (s *Stream) flushFramesLocked() {
	var keep []Packet
	for {
		select {
		case q := <-s.ch:
			if q.Kind == PacketMeta || q.Kind == PacketDescription {
				keep = append(keep, q)
			}
			continue
		default:
		}
		break
	}
	for _, q := range keep {
		s.ch <- q
	}
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
