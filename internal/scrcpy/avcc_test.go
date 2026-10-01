package scrcpy

import (
	"encoding/binary"
	"testing"
)

func TestToAVCC(t *testing.T) {
	sps := []byte{0x67, 0x42, 0xE0, 0x1E, 0xAA, 0xBB}
	pps := []byte{0x68, 0xCE, 0x3C, 0x80}
	idr := []byte{0x65, 0x01, 0x02, 0x03}
	annexb := []byte{0, 0, 0, 1}
	annexb = append(annexb, sps...)
	annexb = append(annexb, 0, 0, 0, 1)
	annexb = append(annexb, pps...)
	annexb = append(annexb, 0, 0, 1)
	annexb = append(annexb, idr...)

	avcc, isKey, gotSPS, gotPPS := ToAVCC(annexb)
	if !isKey {
		t.Fatal("want key frame (IDR present)")
	}
	if string(gotSPS) != string(sps) || string(gotPPS) != string(pps) {
		t.Fatal("SPS/PPS not recovered")
	}
	// Walk AVCC: u32 length + NAL, three times.
	off := 0
	for i := 0; i < 3; i++ {
		if off+4 > len(avcc) {
			t.Fatalf("truncated at NAL %d", i)
		}
		n := int(binary.BigEndian.Uint32(avcc[off : off+4]))
		off += 4
		if off+n > len(avcc) {
			t.Fatalf("NAL %d overruns", i)
		}
		off += n
	}
	if off != len(avcc) {
		t.Fatalf("trailing bytes: %d", len(avcc)-off)
	}
}

func TestCodecStringAndDescription(t *testing.T) {
	sps := []byte{0x67, 0x42, 0xE0, 0x1E, 0xAA}
	pps := []byte{0x68, 0xCE, 0x3C}
	codec, err := CodecString(sps)
	if err != nil || codec != "avc1.42E01E" {
		t.Fatalf("codec=%q err=%v", codec, err)
	}
	desc, err := AVCCDescription(sps, pps)
	if err != nil {
		t.Fatal(err)
	}
	if desc[0] != 1 || desc[4] != 0xFF || desc[5] != 0xE1 {
		t.Fatalf("bad avcC header: %x", desc[:6])
	}
	if _, err := AVCCDescription(nil, pps); err == nil {
		t.Fatal("want error without SPS")
	}
}
