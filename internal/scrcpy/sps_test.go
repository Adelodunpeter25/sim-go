package scrcpy

import "testing"

type bitWriter struct {
	buf []byte
	n   int
}

func (w *bitWriter) bit(b uint) {
	if w.n%8 == 0 {
		w.buf = append(w.buf, 0)
	}
	if b != 0 {
		w.buf[len(w.buf)-1] |= 1 << (7 - uint(w.n%8))
	}
	w.n++
}

func (w *bitWriter) bits(v uint, n int) {
	for i := n - 1; i >= 0; i-- {
		w.bit(v >> uint(i) & 1)
	}
}

func (w *bitWriter) ue(v uint) {
	v++
	n := 0
	for t := v; t > 1; t >>= 1 {
		n++
	}
	w.bits(0, n)
	w.bits(v, n+1)
}

// sps builds a frame_mbs_only SPS for the given profile and crop.
func sps(profile uint, wMbs, hMbs uint, crop [4]uint) []byte {
	w := &bitWriter{}
	w.bits(profile, 8)
	w.bits(0, 8)  // constraints
	w.bits(31, 8) // level
	w.ue(0)       // sps id
	if profile == 100 {
		w.ue(1)  // chroma 4:2:0
		w.ue(0)  // luma depth
		w.ue(0)  // chroma depth
		w.bit(0) // bypass
		w.bit(0) // no scaling matrix
	}
	w.ue(0) // log2_max_frame_num_minus4
	w.ue(2) // poc type 2
	w.ue(1) // refs
	w.bit(0)
	w.ue(wMbs - 1)
	w.ue(hMbs - 1)
	w.bit(1) // frame_mbs_only
	w.bit(1) // direct_8x8
	if crop == [4]uint{} {
		w.bit(0)
	} else {
		w.bit(1)
		for _, c := range crop {
			w.ue(c)
		}
	}
	w.bit(1) // stop bit
	return append([]byte{0x67}, w.buf...)
}

func TestSPSSize(t *testing.T) {
	cases := []struct {
		name    string
		profile uint
		wMbs    uint
		hMbs    uint
		crop    [4]uint
		w, h    int
	}{
		{"baseline 1280x720", 66, 80, 45, [4]uint{}, 1280, 720},
		{"high 1080x2400 cropped", 100, 68, 150, [4]uint{0, 4, 0, 0}, 1080, 2400},
		{"high rotated 2400x1080", 100, 150, 68, [4]uint{0, 0, 0, 4}, 2400, 1080},
	}
	for _, c := range cases {
		gw, gh, err := SPSSize(sps(c.profile, c.wMbs, c.hMbs, c.crop))
		if err != nil || gw != c.w || gh != c.h {
			t.Errorf("%s: got %dx%d err=%v, want %dx%d", c.name, gw, gh, err, c.w, c.h)
		}
	}
	if _, _, err := SPSSize([]byte{0x67, 0x42}); err == nil {
		t.Error("short SPS must error")
	}
}
