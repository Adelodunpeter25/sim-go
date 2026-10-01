package scrcpy

import (
	"encoding/binary"
	"testing"
)

// Byte layouts mirror reference/simfleet/src/android-stream.ts exactly.

func TestEncodeTouch(t *testing.T) {
	b := EncodeTouch(TouchDown, 100, 200, 1080, 2400)
	if len(b) != 32 {
		t.Fatalf("len=%d want 32", len(b))
	}
	if b[0] != msgInjectTouch || b[1] != TouchDown {
		t.Fatalf("header=%v", b[:2])
	}
	if binary.BigEndian.Uint64(b[2:10]) != ^uint64(1) {
		t.Fatalf("pointer id=%x", b[2:10])
	}
	if binary.BigEndian.Uint32(b[10:14]) != 100 || binary.BigEndian.Uint32(b[14:18]) != 200 {
		t.Fatalf("coords=%d,%d", binary.BigEndian.Uint32(b[10:14]), binary.BigEndian.Uint32(b[14:18]))
	}
	if binary.BigEndian.Uint16(b[18:20]) != 1080 || binary.BigEndian.Uint16(b[20:22]) != 2400 {
		t.Fatalf("size wrong")
	}
	if binary.BigEndian.Uint16(b[22:24]) != 0xffff {
		t.Fatalf("down pressure=%x", b[22:24])
	}
	up := EncodeTouch(TouchUp, 0, 0, 1080, 2400)
	if binary.BigEndian.Uint16(up[22:24]) != 0 {
		t.Fatalf("up pressure=%x", up[22:24])
	}
}

func TestEncodeKeycode(t *testing.T) {
	b := EncodeKeycode(true, 4)
	if len(b) != 14 || b[0] != msgInjectKeycode || b[1] != 0 {
		t.Fatalf("down=%v", b)
	}
	if binary.BigEndian.Uint32(b[2:6]) != 4 {
		t.Fatalf("keycode wrong")
	}
	u := EncodeKeycode(false, 4)
	if u[1] != 1 {
		t.Fatalf("up action=%d", u[1])
	}
}

func TestEncodeScroll(t *testing.T) {
	b := EncodeScroll(10, 20, 1080, 2400, 0, -16)
	if len(b) != 21 || b[0] != msgInjectScroll {
		t.Fatalf("scroll=%v", b[:2])
	}
}

func TestEncodeText(t *testing.T) {
	b := EncodeText("hi")
	if len(b) != 7 || b[0] != msgInjectText || binary.BigEndian.Uint32(b[1:5]) != 2 {
		t.Fatalf("text=%v", b)
	}
	long := make([]byte, 500)
	for i := range long {
		long[i] = 'a'
	}
	if got := len(EncodeText(string(long))); got != 305 {
		t.Fatalf("truncated len=%d want 305", got)
	}
}
