package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Adelodunpeter25/sim-go/sdk"
)

// TestIOSStreamLive drives the real /api/stream websocket against a booted
// simulator over the idb companion: meta, avcC description, keyframe, and a
// tap that must not kill the session. Skips (not fails) with no booted sim so
// `go test ./...` stays fast.
func TestIOSStreamLive(t *testing.T) {
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
	udid, name := "", ""
	for _, d := range devs {
		if d.Platform == "ios" && d.State == "Booted" {
			udid, name = d.ID, d.Name
			break
		}
	}
	if udid == "" {
		t.Skip("no booted iOS simulator")
	}
	t.Logf("driving %s (%s)", name, udid)

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
	fmt.Fprintf(conn, "GET /api/stream?platform=ios&id=%s HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n", udid)
	br := bufio.NewReader(conn)
	_ = conn.SetDeadline(time.Now().Add(90 * time.Second))
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

	// Meta first: the simulator's real screen size, not a guess.
	op, payload := readServerFrame(t, br)
	if op != wsText {
		t.Fatalf("first message opcode=%d want text", op)
	}
	var meta struct {
		Type   string `json:"type"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Name   string `json:"name"`
		Codec  string `json:"codec"`
	}
	if err := json.Unmarshal(payload, &meta); err != nil || meta.Type != "meta" {
		t.Fatalf("meta=%q err=%v", payload, err)
	}
	if meta.Width == 0 || meta.Height == 0 {
		t.Fatalf("bad meta size %dx%d", meta.Width, meta.Height)
	}
	t.Logf("meta %s %dx%d codec=%q", meta.Name, meta.Width, meta.Height, meta.Codec)

	var sawDesc, sawKey bool
	deadline := time.Now().Add(60 * time.Second)
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

	// Control: a tap at center must be replayed as HID without killing the
	// stream. The next frame proves the pipe survived.
	sendClientText(t, conn, fmt.Sprintf(`{"type":"touch","action":"down","x":%d,"y":%d}`, meta.Width/2, meta.Height/2))
	sendClientText(t, conn, fmt.Sprintf(`{"type":"touch","action":"up","x":%d,"y":%d}`, meta.Width/2, meta.Height/2))
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	op, _ = readServerFrame(t, br)
	if op != wsBinary {
		t.Fatalf("post-tap opcode=%d want binary", op)
	}
}
