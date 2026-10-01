package scrcpy

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

const (
	deviceNameLen = 64
	codecMetaLen  = 12
	frameHeadLen  = 12

	packetFlagConfig   = uint64(1) << 63
	packetFlagKeyFrame = uint64(1) << 62
)

// Frame is one H.264 access unit from the device encoder.
type Frame struct {
	Config  bool // codec config (SPS/PPS): send before the first frame to a new viewer
	Key     bool
	Payload []byte // Annex-B H.264
}

// readHandshake consumes the dummy byte (already read by dialVideo), the
// 64-byte device name, and the 12-byte codec metadata
// (4-byte codec id "h264" + u32 width + u32 height).
func readHandshake(conn net.Conn) (Meta, error) {
	var m Meta
	name := make([]byte, deviceNameLen)
	if _, err := readFull(conn, name); err != nil {
		return m, fmt.Errorf("device name: %w", err)
	}
	m.Name = strings.TrimRight(string(name), "\x00")
	codec := make([]byte, codecMetaLen)
	if _, err := readFull(conn, codec); err != nil {
		return m, fmt.Errorf("codec meta: %w", err)
	}
	if string(codec[:4]) != "h264" {
		return m, fmt.Errorf("unexpected codec %q (want h264)", codec[:4])
	}
	m.Width = int(binary.BigEndian.Uint32(codec[4:8]))
	m.Height = int(binary.BigEndian.Uint32(codec[8:12]))
	if m.Width == 0 || m.Height == 0 {
		return m, fmt.Errorf("bad stream size %dx%d", m.Width, m.Height)
	}
	return m, nil
}

// ReadFrame blocks for the next frame: 12-byte header
// (u64 pts+flags, u32 size) followed by the payload.
func (s *Session) ReadFrame() (Frame, error) {
	var f Frame
	head := make([]byte, frameHeadLen)
	if _, err := readFull(s.video, head); err != nil {
		return f, fmt.Errorf("frame header: %w", err)
	}
	flags := binary.BigEndian.Uint64(head[0:8])
	size := binary.BigEndian.Uint32(head[8:12])
	if size == 0 || size > 20<<20 {
		return f, fmt.Errorf("bad frame size %d", size)
	}
	payload := make([]byte, size)
	if _, err := readFull(s.video, payload); err != nil {
		return f, fmt.Errorf("frame payload: %w", err)
	}
	f.Config = flags&packetFlagConfig != 0
	f.Key = flags&packetFlagKeyFrame != 0
	f.Payload = payload
	return f, nil
}

// WaitKeyframe reads until a key frame (or config + key) arrives.
func (s *Session) WaitKeyframe(timeout time.Duration) (Frame, error) {
	deadline := time.Now().Add(timeout)
	var sawConfig bool
	for time.Now().Before(deadline) {
		_ = s.video.SetReadDeadline(time.Now().Add(5 * time.Second))
		f, err := s.ReadFrame()
		_ = s.video.SetReadDeadline(time.Time{})
		if err != nil {
			if isTimeout(err) {
				continue
			}
			return f, err
		}
		if f.Config {
			sawConfig = true
			continue
		}
		if f.Key {
			_ = sawConfig
			return f, nil
		}
	}
	return Frame{}, fmt.Errorf("no key frame within %s", timeout)
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	return io.ReadFull(conn, buf)
}

func isTimeout(err error) bool {
	if ne, ok := err.(net.Error); ok {
		return ne.Timeout()
	}
	return false
}

func drain(conn net.Conn) {
	buf := make([]byte, 4096)
	for {
		if _, err := conn.Read(buf); err != nil {
			return
		}
	}
}
