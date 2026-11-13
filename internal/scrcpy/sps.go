package scrcpy

import "fmt"

// SPSSize returns the displayed picture size from a raw H.264 SPS NAL
// (header byte first). scrcpy announces a rotation or resize by restarting
// its encoder, which shows up as a new SPS.
func SPSSize(sps []byte) (width, height int, err error) {
	if len(sps) < 5 {
		return 0, 0, fmt.Errorf("SPS too short (%d bytes)", len(sps))
	}
	r := &bitReader{data: unescapeRBSP(sps[1:])}
	profile := r.bits(8)
	r.bits(16) // constraint flags + level_idc
	r.ue()     // seq_parameter_set_id
	chroma := uint(1)
	switch profile {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		chroma = r.ue()
		if chroma == 3 {
			r.bits(1) // separate_colour_plane_flag
		}
		r.ue()              // bit_depth_luma_minus8
		r.ue()              // bit_depth_chroma_minus8
		r.bits(1)           // qpprime_y_zero_transform_bypass_flag
		if r.bits(1) == 1 { // seq_scaling_matrix_present_flag
			lists := 8
			if chroma == 3 {
				lists = 12
			}
			for i := 0; i < lists; i++ {
				if r.bits(1) == 1 {
					size := 16
					if i >= 6 {
						size = 64
					}
					last, next := 8, 8
					for j := 0; j < size && next != 0; j++ {
						delta := r.se()
						next = (last + delta + 256) % 256
						if next != 0 {
							last = next
						}
					}
				}
			}
		}
	}
	r.ue()          // log2_max_frame_num_minus4
	switch r.ue() { // pic_order_cnt_type
	case 0:
		r.ue()
	case 1:
		r.bits(1)
		r.se()
		r.se()
		for n := r.ue(); n > 0; n-- {
			r.se()
		}
	}
	r.ue()    // max_num_ref_frames
	r.bits(1) // gaps_in_frame_num_value_allowed_flag
	wMbs := int(r.ue()) + 1
	hMapUnits := int(r.ue()) + 1
	frameMbsOnly := r.bits(1)
	if frameMbsOnly == 0 {
		r.bits(1) // mb_adaptive_frame_field_flag
	}
	r.bits(1) // direct_8x8_inference_flag
	var cropL, cropR, cropT, cropB int
	if r.bits(1) == 1 {
		cropL, cropR, cropT, cropB = int(r.ue()), int(r.ue()), int(r.ue()), int(r.ue())
	}
	if r.err {
		return 0, 0, fmt.Errorf("SPS truncated")
	}
	cropX, cropY := 1, 2-int(frameMbsOnly)
	if chroma == 1 || chroma == 2 {
		cropX = 2
	}
	if chroma == 1 {
		cropY *= 2
	}
	width = wMbs*16 - (cropL+cropR)*cropX
	height = (2-int(frameMbsOnly))*hMapUnits*16 - (cropT+cropB)*cropY
	if width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("bad SPS size %dx%d", width, height)
	}
	return width, height, nil
}

// unescapeRBSP drops the 0x03 emulation-prevention bytes.
func unescapeRBSP(b []byte) []byte {
	out := make([]byte, 0, len(b))
	zeros := 0
	for _, c := range b {
		if zeros >= 2 && c == 3 {
			zeros = 0
			continue
		}
		if c == 0 {
			zeros++
		} else {
			zeros = 0
		}
		out = append(out, c)
	}
	return out
}

type bitReader struct {
	data []byte
	pos  int // bit position
	err  bool
}

func (r *bitReader) bit() uint {
	if r.pos>>3 >= len(r.data) {
		r.err = true
		return 0
	}
	v := uint(r.data[r.pos>>3]>>(7-uint(r.pos&7))) & 1
	r.pos++
	return v
}

func (r *bitReader) bits(n int) uint {
	var v uint
	for i := 0; i < n; i++ {
		v = v<<1 | r.bit()
	}
	return v
}

func (r *bitReader) ue() uint {
	zeros := 0
	for r.bit() == 0 {
		if r.err || zeros > 31 {
			r.err = true
			return 0
		}
		zeros++
	}
	return 1<<uint(zeros) - 1 + r.bits(zeros)
}

func (r *bitReader) se() int {
	k := r.ue()
	if k&1 == 1 {
		return int(k+1) / 2
	}
	return -int(k / 2)
}
