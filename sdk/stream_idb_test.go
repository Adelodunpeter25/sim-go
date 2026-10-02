package sdk

import (
	"encoding/binary"
	"testing"
)

// scTerminator-prefixed NALs, mirroring what the companion emits.
func sc(nals ...[]byte) []byte {
	var out []byte
	for _, n := range nals {
		out = append(out, 0, 0, 0, 1)
		out = append(out, n...)
	}
	return out
}

var (
	spsNAL = []byte{0x67, 0x42, 0xE0, 0x1E, 0xAB, 0xCD, 0xEF} // avc1.42E01E
	ppsNAL = []byte{0x68, 0xCE, 0x38, 0x80}
	idrNAL = append([]byte{0x65, 0x88}, filler(200)...) // first_mb_in_slice == 0
	slNAL  = append([]byte{0x41, 0x9A}, filler(80)...)
	seiNAL = []byte{0x06, 0x05, 0x01, 0x02, 0x03, 0x04, 0x80}
)

func filler(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

// avccNALLengths reads the length prefix of every NAL in an AVCC buffer.
func avccNALLengths(avcc []byte) []int {
	var out []int
	for len(avcc) >= 4 {
		n := int(binary.BigEndian.Uint32(avcc[:4]))
		if n < 1 || 4+n > len(avcc) {
			break
		}
		out = append(out, n)
		avcc = avcc[4+n:]
	}
	return out
}

func TestAssemblerKeyframeCarriesParameterSets(t *testing.T) {
	a := &iosAssembler{}
	aus, desc, codec := a.push(sc(spsNAL, ppsNAL, idrNAL))
	if len(desc) == 0 {
		t.Fatal("no description emitted on SPS/PPS")
	}
	if codec != "avc1.42E01E" {
		t.Fatalf("codec=%q want avc1.42E01E", codec)
	}
	// The IDR is only complete once the next start code proves it ended.
	if len(aus) != 0 {
		t.Fatalf("unterminated NAL completed a picture too early: %d", len(aus))
	}
	aus, desc, _ = a.push(sc(slNAL))
	if desc != nil {
		t.Fatal("description re-emitted without a parameter change")
	}
	if len(aus) != 1 {
		t.Fatalf("want 1 picture, got %d", len(aus))
	}
	key := aus[0]
	if !key.key {
		t.Fatal("first picture should be a keyframe")
	}
	if len(key.nals) != 3 {
		t.Fatalf("keyframe NALs = %d, want 3 (SPS+PPS+IDR)", len(key.nals))
	}
	sizes := avccNALLengths(key.avcc())
	if len(sizes) != 3 || sizes[0] != len(spsNAL) || sizes[1] != len(ppsNAL) || sizes[2] != len(idrNAL) {
		t.Fatalf("AVCC NAL sizes = %v want [%d %d %d]", sizes, len(spsNAL), len(ppsNAL), len(idrNAL))
	}
}

func TestAssemblerByteAtATime(t *testing.T) {
	a := &iosAssembler{}
	stream := append(sc(spsNAL, ppsNAL, idrNAL), sc(slNAL, slNAL)...)
	var aus []iosAU
	var sawDesc bool
	for i := range stream {
		got, desc, _ := a.push(stream[i : i+1])
		sawDesc = sawDesc || desc != nil
		aus = append(aus, got...)
	}
	if !sawDesc {
		t.Fatal("description never emitted")
	}
	// The last NAL has no successor to prove its end, so it is held back.
	if len(aus) != 2 {
		t.Fatalf("want 2 pictures before flush, got %d", len(aus))
	}
	if !aus[0].key || aus[1].key {
		t.Fatalf("key flags = %v %v, want true false", aus[0].key, aus[1].key)
	}
	if len(aus[0].nals) != 3 {
		t.Fatalf("keyframe should carry SPS+PPS+IDR, got %d NALs", len(aus[0].nals))
	}
	tail, _, _ := a.flush()
	if len(tail) != 1 || tail[0].key {
		t.Fatalf("flush should emit the final delta: %d pictures", len(tail))
	}
}

func TestAssemblerMixedChunkBoundaries(t *testing.T) {
	a := &iosAssembler{}
	// A chunk holding SPS + half a PPS: no picture, and no description yet
	// (a description needs both parameter sets complete).
	aus, desc, _ := a.push(sc(spsNAL, ppsNAL[:2]))
	if len(aus) != 0 || desc != nil {
		t.Fatalf("partial PPS: aus=%d desc=%v", len(aus), desc != nil)
	}
	// Next chunk finishes the PPS mid-NAL, then a start code opens the IDR,
	// and another opens a delta. This is how the wire really behaves.
	rest := ppsNAL[2:]
	rest = append(rest, sc(idrNAL)...)
	rest = append(rest, sc(slNAL)...)
	aus, desc, _ = a.push(rest)
	if desc == nil {
		t.Fatal("no description once the PPS completed")
	}
	if len(aus) != 1 || !aus[0].key {
		t.Fatalf("want 1 keyframe, got %d pictures", len(aus))
	}
	if len(aus[0].nals) != 3 {
		t.Fatalf("want SPS+PPS+IDR, got %d NALs", len(aus[0].nals))
	}
	aus, _, _ = a.push(sc(slNAL))
	if len(aus) != 1 || aus[0].key {
		t.Fatalf("want 1 delta, got %d", len(aus))
	}
}

func TestAssemblerRebuildsDescriptionOnParameterChange(t *testing.T) {
	a := &iosAssembler{}
	_, desc, _ := a.push(sc(spsNAL, ppsNAL, slNAL))
	if desc == nil {
		t.Fatal("first description missing")
	}
	// Same SPS/PPS again: no re-broadcast.
	_, desc, _ = a.push(sc(spsNAL, ppsNAL, slNAL))
	if desc != nil {
		t.Fatal("unchanged parameters should not re-emit a description")
	}
	newSPS := []byte{0x67, 0x64, 0x00, 0x28, 0x11, 0x22, 0x33} // avc1.640028
	_, desc, codec := a.push(sc(newSPS, ppsNAL, slNAL))
	if desc == nil {
		t.Fatal("changed SPS should re-emit a description")
	}
	if codec != "avc1.640028" {
		t.Fatalf("codec=%q want avc1.640028", codec)
	}
}

func TestAssemblerRecoversFromGarbage(t *testing.T) {
	a := &iosAssembler{}
	aus, desc, _ := a.push([]byte{0x00, 0x11, 0x22, 0x33, 0x44})
	if len(aus) != 0 || desc != nil {
		t.Fatalf("garbage produced aus=%d desc=%v", len(aus), desc != nil)
	}
	// The stream must recover on the next real chunk.
	aus, desc, _ = a.push(sc(spsNAL, ppsNAL, idrNAL, slNAL))
	if desc == nil {
		t.Fatal("no description after recovering from garbage")
	}
	if len(aus) != 1 || !aus[0].key {
		t.Fatalf("want recovered keyframe, got %d pictures", len(aus))
	}
}

func TestAssemblerBoundsPendingNALs(t *testing.T) {
	a := &iosAssembler{}
	// 200 SEI NALs with no VCL must not grow pending without bound.
	for i := 0; i < 200; i++ {
		a.push(sc(seiNAL))
	}
	if len(a.pending) > 32 {
		t.Fatalf("pending grew to %d NALs", len(a.pending))
	}
}

func TestAssemblerEmptyInput(t *testing.T) {
	a := &iosAssembler{}
	aus, desc, codec := a.push(nil)
	if len(aus) != 0 || desc != nil || codec != "" {
		t.Fatalf("empty push: aus=%d desc=%v codec=%q", len(aus), desc != nil, codec)
	}
	tail, _, _ := a.flush()
	if len(tail) != 0 {
		t.Fatalf("flush on an empty assembler emitted %d pictures", len(tail))
	}
}

func TestAssemblerGroupsSlicesIntoOnePicture(t *testing.T) {
	a := &iosAssembler{}
	slice2 := append([]byte{0x41, 0x40}, filler(60)...) // first_mb_in_slice > 0
	slice3 := append([]byte{0x41, 0x20}, filler(60)...)
	var aus []iosAU
	for _, chunk := range [][]byte{sc(spsNAL, ppsNAL, idrNAL), sc(slNAL, slice2, slice3), sc(slNAL)} {
		got, _, _ := a.push(chunk)
		aus = append(aus, got...)
	}
	if len(aus) != 2 {
		t.Fatalf("want 2 pictures, got %d", len(aus))
	}
	if n := len(aus[1].nals); n != 3 || aus[1].key {
		t.Fatalf("multi-slice picture: %d NALs key=%v, want 3 delta", n, aus[1].key)
	}
	tail, _, _ := a.flush()
	if len(tail) != 1 {
		t.Fatalf("flush: want 1 picture, got %d", len(tail))
	}
}
