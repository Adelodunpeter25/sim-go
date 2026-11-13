package scrcpy

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// AVCC helpers: the device speaks Annex-B H.264, browsers (WebCodecs) want
// AVCC (length-prefixed NALs) plus an avcC description built from SPS/PPS.
// Conversion happens once on the server so every viewer stays dumb.

func splitNALs(annexb []byte) [][]byte {
	nals := make([][]byte, 0, 4)
	start := -1
	pos := 0
	for {
		j := bytes.Index(annexb[pos:], startCode3)
		if j < 0 {
			break
		}
		j += pos
		end := j
		if end > 0 && annexb[end-1] == 0 { // 4-byte start code
			end--
		}
		if start >= 0 && end > start {
			nals = append(nals, annexb[start:end])
		}
		start = j + 3
		pos = start
	}
	if start >= 0 && len(annexb) > start {
		nals = append(nals, annexb[start:])
	}
	return nals
}

var startCode3 = []byte{0, 0, 1}

// ToAVCC converts one Annex-B access unit to AVCC. It returns the converted
// bytes, whether an IDR (type 5) is present, and any SPS/PPS NALs found.
func ToAVCC(annexb []byte) (avcc []byte, isKey bool, sps, pps []byte) {
	nals := splitNALs(annexb)
	avcc = make([]byte, 0, len(annexb)+4*len(nals))
	for _, nal := range nals {
		if len(nal) == 0 {
			continue
		}
		typ := nal[0] & 0x1f
		switch typ {
		case 5:
			isKey = true
		case 7:
			if sps == nil {
				sps = nal
			}
		case 8:
			if pps == nil {
				pps = nal
			}
		}
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(nal)))
		avcc = append(avcc, hdr[:]...)
		avcc = append(avcc, nal...)
	}
	return avcc, isKey, sps, pps
}

// CodecString builds the WebCodecs codec string (e.g. avc1.42E01E) from raw SPS.
func CodecString(sps []byte) (string, error) {
	if len(sps) < 4 {
		return "", fmt.Errorf("SPS too short (%d bytes)", len(sps))
	}
	return "avc1." + strings.ToUpper(hex.EncodeToString(sps[1:4])), nil
}

// AVCCDescription builds the avcC box payload from raw SPS/PPS NALs.
func AVCCDescription(sps, pps []byte) ([]byte, error) {
	if len(sps) == 0 || len(pps) == 0 {
		return nil, fmt.Errorf("need SPS+PPS for avcC description")
	}
	out := []byte{0x01, sps[1], sps[2], sps[3], 0xFF, 0xE1}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(sps)))
	out = append(out, hdr[:]...)
	out = append(out, sps...)
	out = append(out, 0x01)
	binary.BigEndian.PutUint16(hdr[:], uint16(len(pps)))
	out = append(out, hdr[:]...)
	out = append(out, pps...)
	return out, nil
}
