package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Adelodunpeter25/sim-go/sdk"
)

// TestAndroidStreamLive drives the real /api/stream websocket against a
// booted emulator: handshake, meta, AVCC config + keyframe, control write.
// Skips (not fails) with no booted emulator, so `go test ./...` stays fast.
func TestAndroidStreamLive(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	c := sdk.New()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	devs, err := c.ListAll(ctx)
	if err != nil {
		t.Skipf("no devices: %v", err)
	}
	serial := ""
	for _, d := range devs {
		if d.Platform == "android" && strings.HasPrefix(d.ID, "emulator-") && d.State == "device" {
			serial = d.ID
			break
		}
	}
	if serial == "" {
		t.Skip("no booted emulator")
	}

	mux, err := newMux(c)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(srv.URL, "http://"), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET /api/stream?platform=android&id=%s HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n", serial)
	br := bufio.NewReader(conn)
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	status, _ := br.ReadString('\n')
	if !strings.Contains(status, "101") {
		t.Fatalf("status=%q", status)
	}
	for {
		line, _ := br.ReadString('\n')
		if strings.TrimSpace(line) == "" {
			break
		}
	}

	// Meta text message first.
	op, payload := readServerFrame(t, br)
	if op != wsText {
		t.Fatalf("first message opcode=%d want text", op)
	}
	var meta struct {
		Type   string `json:"type"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Codec  string `json:"codec"`
	}
	if err := json.Unmarshal(payload, &meta); err != nil || meta.Type != "meta" {
		t.Fatalf("meta=%q err=%v", payload, err)
	}
	if meta.Width == 0 || meta.Height == 0 {
		t.Fatalf("bad meta size %dx%d", meta.Width, meta.Height)
	}
	t.Logf("meta %dx%d codec=%q", meta.Width, meta.Height, meta.Codec)

	// Messages until config + keyframe (meta re-broadcasts carry the codec).
	var sawDesc, sawKey bool
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) && !(sawDesc && sawKey) {
		op, payload := readServerFrame(t, br)
		if op == wsText {
			var m struct {
				Type  string `json:"type"`
				Codec string `json:"codec"`
			}
			if err := json.Unmarshal(payload, &m); err != nil || m.Type != "meta" {
				t.Fatalf("text=%q", payload)
			}
			if m.Codec != "" {
				meta.Codec = m.Codec
			}
			continue
		}
		if op != wsBinary || len(payload) < 1 {
			t.Fatalf("opcode=%d len=%d", op, len(payload))
		}
		switch payload[0] {
		case tagDesc:
			sawDesc = true
			if len(payload) < 8 || payload[1] != 1 {
				t.Fatalf("bad avcC (%d bytes)", len(payload))
			}
		case tagKey:
			sawKey = true
			if len(payload) < 100 {
				t.Fatalf("keyframe suspiciously small (%d)", len(payload))
			}
		case tagDelta:
		default:
			t.Fatalf("unknown tag %d", payload[0])
		}
	}
	if !sawDesc || !sawKey {
		t.Fatalf("desc=%v key=%v", sawDesc, sawKey)
	}
	if meta.Codec == "" {
		t.Fatal("never learned the codec string")
	}

	// Control write: tap center must not kill the session.
	sendClientText(t, conn, fmt.Sprintf(`{"type":"touch","action":"down","x":%d,"y":%d}`, meta.Width/2, meta.Height/2))
	sendClientText(t, conn, fmt.Sprintf(`{"type":"touch","action":"up","x":%d,"y":%d}`, meta.Width/2, meta.Height/2))
	// Session must still be alive: next frame arrives.
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	op, _ = readServerFrame(t, br)
	if op != wsBinary {
		t.Fatalf("post-tap opcode=%d", op)
	}
}

func readServerFrame(t *testing.T, br *bufio.Reader) (int, []byte) {
	t.Helper()
	var hdr [2]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		t.Fatalf("frame header: %v", err)
	}
	op := int(hdr[0] & 0x0f)
	n := int64(hdr[1] & 0x7f)
	switch n {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(br, ext[:]); err != nil {
			t.Fatal(err)
		}
		n = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(br, ext[:]); err != nil {
			t.Fatal(err)
		}
		n = int64(binary.BigEndian.Uint64(ext[:]))
	}
	if n < 0 || n > 32<<20 {
		t.Fatalf("bad length %d", n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(br, payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	return op, payload
}

func sendClientText(t *testing.T, conn net.Conn, s string) {
	t.Helper()
	mask := []byte{0x11, 0x22, 0x33, 0x44}
	b := []byte(s)
	hdr := []byte{0x81}
	if len(b) < 126 {
		hdr = append(hdr, 0x80|byte(len(b)))
	} else {
		hdr = append(hdr, 0x80|126, byte(len(b)>>8), byte(len(b)))
	}
	hdr = append(hdr, mask...)
	masked := make([]byte, len(b))
	for i := range b {
		masked[i] = b[i] ^ mask[i%4]
	}
	if _, err := conn.Write(append(hdr, masked...)); err != nil {
		t.Fatal(err)
	}
}

// Opcodes of the raw frames the live tests parse by hand.
const (
	wsText   = 1
	wsBinary = 2
)

// The wire tags, restated here on purpose: these tests pin the contract the
// page depends on, independent of the names streamws uses.
const (
	tagDesc  = 1
	tagKey   = 2
	tagDelta = 3
)
