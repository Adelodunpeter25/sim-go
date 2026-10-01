package scrcpy

import (
	"encoding/binary"
	"fmt"
)

// Control message types (scrcpy v2.x wire format; see Version).
const (
	msgInjectKeycode = 0
	msgInjectText    = 1
	msgInjectTouch   = 2
	msgInjectScroll  = 3
	msgResetVideo    = 17
)

// Touch actions.
const (
	TouchDown   = 0
	TouchUp     = 1
	TouchMove   = 2
	TouchCancel = 3
)

// Pointer ID for a generic finger (-2 as two's complement u64).
const pointerGenericFinger = ^uint64(1)

// EncodeTouch builds a 32-byte inject-touch message. Coordinates are in
// device pixels; width/height are the current stream size (Meta) so the
// server can map them.
func EncodeTouch(action int, x, y, width, height int) []byte {
	buf := make([]byte, 32)
	buf[0] = msgInjectTouch
	buf[1] = byte(action)
	binary.BigEndian.PutUint64(buf[2:10], pointerGenericFinger)
	binary.BigEndian.PutUint32(buf[10:14], uint32(x))
	binary.BigEndian.PutUint32(buf[14:18], uint32(y))
	binary.BigEndian.PutUint16(buf[18:20], uint16(width))
	binary.BigEndian.PutUint16(buf[20:22], uint16(height))
	if action != TouchUp && action != TouchCancel {
		binary.BigEndian.PutUint16(buf[22:24], 0xffff) // pressure
	}
	return buf
}

// EncodeScroll builds a 21-byte inject-scroll message.
func EncodeScroll(x, y, width, height int, dx, dy float64) []byte {
	buf := make([]byte, 21)
	buf[0] = msgInjectScroll
	binary.BigEndian.PutUint32(buf[1:5], uint32(x))
	binary.BigEndian.PutUint32(buf[5:9], uint32(y))
	binary.BigEndian.PutUint16(buf[9:11], uint16(width))
	binary.BigEndian.PutUint16(buf[11:13], uint16(height))
	binary.BigEndian.PutUint16(buf[13:15], uint16(clampScroll(dx)))
	binary.BigEndian.PutUint16(buf[15:17], uint16(clampScroll(dy)))
	return buf
}

func clampScroll(v float64) int16 {
	// Server decodes i16 fixed point over [-1, 1], scaled by 16.
	n := v / 16
	if n > 1 {
		n = 1
	}
	if n < -1 {
		n = -1
	}
	return int16(n * 0x8000)
}

// EncodeKeycode builds a 14-byte inject-keycode message (down or up).
func EncodeKeycode(down bool, keycode int) []byte {
	buf := make([]byte, 14)
	buf[0] = msgInjectKeycode
	if !down {
		buf[1] = 1
	}
	binary.BigEndian.PutUint32(buf[2:6], uint32(keycode))
	return buf
}

// EncodeText builds an inject-text message (truncated to 300 bytes).
func EncodeText(text string) []byte {
	b := []byte(text)
	if len(b) > 300 {
		b = b[:300]
	}
	buf := make([]byte, 5+len(b))
	buf[0] = msgInjectText
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(b)))
	copy(buf[5:], b)
	return buf
}

func (s *Session) sendControl(msg []byte) error {
	s.ctrlMu.Lock()
	defer s.ctrlMu.Unlock()
	_, err := s.control.Write(msg)
	if err != nil {
		return fmt.Errorf("control write: %w", err)
	}
	return nil
}

// SendRaw writes a pre-encoded control message (used by the WS hub).
func (s *Session) SendRaw(msg []byte) error { return s.sendControl(msg) }

// Tap injects down+up at device pixels.
func (s *Session) Tap(x, y int) error {
	if err := s.sendControl(EncodeTouch(TouchDown, x, y, s.Meta.Width, s.Meta.Height)); err != nil {
		return err
	}
	return s.sendControl(EncodeTouch(TouchUp, x, y, s.Meta.Width, s.Meta.Height))
}

// Swipe injects down, moves, up across device pixels.
func (s *Session) Swipe(x1, y1, x2, y2, steps int) error {
	if steps < 1 {
		steps = 8
	}
	if err := s.sendControl(EncodeTouch(TouchDown, x1, y1, s.Meta.Width, s.Meta.Height)); err != nil {
		return err
	}
	for i := 1; i <= steps; i++ {
		x := x1 + (x2-x1)*i/steps
		y := y1 + (y2-y1)*i/steps
		if err := s.sendControl(EncodeTouch(TouchMove, x, y, s.Meta.Width, s.Meta.Height)); err != nil {
			return err
		}
	}
	return s.sendControl(EncodeTouch(TouchUp, x2, y2, s.Meta.Width, s.Meta.Height))
}

// Key injects a down+up pair for an Android keycode (e.g. 3=HOME, 4=BACK).
func (s *Session) Key(keycode int) error {
	if err := s.sendControl(EncodeKeycode(true, keycode)); err != nil {
		return err
	}
	return s.sendControl(EncodeKeycode(false, keycode))
}

// Text injects a string via the device IME.
func (s *Session) Text(text string) error {
	if text == "" {
		return nil
	}
	return s.sendControl(EncodeText(text))
}

// ResetVideo asks the encoder for fresh config + key frame (for late joiners).
func (s *Session) ResetVideo() error {
	return s.sendControl([]byte{msgResetVideo})
}
