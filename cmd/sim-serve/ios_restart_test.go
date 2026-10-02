package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Adelodunpeter25/sim-go/sdk"
)

// wsClient is a minimal client for the stream contract, so the live tests can
// exercise handshake, framing and control without a browser.
type wsClient struct {
	conn net.Conn
	br   *bufio.Reader
	t    *testing.T
}

func dialStream(t *testing.T, platform, id string) *wsClient {
	t.Helper()
	mux, err := newMux(sdk.New())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(srv.URL, "http://"), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	fmt.Fprintf(conn, "GET /api/stream?platform=%s&id=%s HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n", platform, id)
	c := &wsClient{conn: conn, br: bufio.NewReader(conn), t: t}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	status, _ := c.br.ReadString('\n')
	if !strings.Contains(status, "101") {
		t.Fatalf("status=%q", status)
	}
	for {
		line, _ := c.br.ReadString('\n')
		if strings.TrimSpace(line) == "" {
			break
		}
	}
	return c
}

func (c *wsClient) send(v any) {
	c.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		c.t.Fatal(err)
	}
	mask := []byte{0x11, 0x22, 0x33, 0x44}
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
	if _, err := c.conn.Write(append(hdr, masked...)); err != nil {
		c.t.Fatal(err)
	}
}

// next returns the next frame's tag and payload (tag 0 for text JSON).
func (c *wsClient) next(timeout time.Duration) (byte, []byte) {
	c.t.Helper()
	_ = c.conn.SetDeadline(time.Now().Add(timeout))
	var hdr [2]byte
	if _, err := io.ReadFull(c.br, hdr[:]); err != nil {
		c.t.Fatalf("frame header: %v", err)
	}
	if hdr[0]&0x0f == wsText {
		return 0, c.readPayload(int(hdr[1] & 0x7f))
	}
	n := int(hdr[1] & 0x7f)
	switch n {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			c.t.Fatal(err)
		}
		n = int(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			c.t.Fatal(err)
		}
		n = int(binary.BigEndian.Uint64(ext[:]))
	}
	payload := c.readPayload(n)
	if len(payload) == 0 {
		c.t.Fatal("empty binary frame")
	}
	return payload[0], payload[1:]
}

func (c *wsClient) readPayload(n int) []byte {
	c.t.Helper()
	if n < 0 || n > 32<<20 {
		c.t.Fatalf("bad length %d", n)
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(c.br, p); err != nil {
		c.t.Fatalf("payload: %v", err)
	}
	return p
}

type streamMeta struct {
	Type   string `json:"type"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Name   string `json:"name"`
	Codec  string `json:"codec"`
}

func (c *wsClient) meta() streamMeta {
	c.t.Helper()
	for {
		tag, payload := c.next(30 * time.Second)
		if tag != 0 {
			continue
		}
		var m streamMeta
		if err := json.Unmarshal(payload, &m); err != nil {
			c.t.Fatalf("meta json: %v", err)
		}
		return m
	}
}

// waitFirstPicture consumes frames until a description plus a keyframe have
// both arrived, proving the stream is decodable.
func (c *wsClient) waitFirstPicture(timeout time.Duration) (codec string, desc, key []byte) {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tag, payload := c.next(30 * time.Second)
		if tag == 0 {
			var m streamMeta
			if err := json.Unmarshal(payload, &m); err == nil && m.Codec != "" {
				codec = m.Codec
			}
			continue
		}
		switch tag {
		case tagDesc:
			desc = payload
		case tagKey:
			key = payload
		}
		if desc != nil && key != nil && codec != "" {
			return codec, desc, key
		}
	}
	c.t.Fatalf("no picture within %s (desc=%v key=%v codec=%q)", timeout, desc != nil, key != nil, codec)
	return
}

// bootedIOS returns a booted simulator's UDID, skipping when there is none.
func bootedIOS(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	devs, err := sdk.New().ListAll(ctx)
	if err != nil {
		t.Skipf("no devices: %v", err)
	}
	for _, d := range devs {
		if d.Platform == "ios" && d.State == "Booted" {
			return d.ID
		}
	}
	t.Skip("no booted iOS simulator")
	return ""
}

// A second viewer must get meta + description without disturbing the first,
// and both must keep receiving pictures afterwards.
func TestIOSLateJoinerGetsKeyframe(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	udid := bootedIOS(t)
	first := dialStream(t, "ios", udid)
	m1 := first.meta()
	if _, desc, key := first.waitFirstPicture(60 * time.Second); len(desc) == 0 || len(key) == 0 {
		t.Fatalf("first viewer got desc=%d key=%d", len(desc), len(key))
	}

	second := dialStream(t, "ios", udid)
	m2 := second.meta()
	if m2.Width != m1.Width || m2.Height != m1.Height {
		t.Fatalf("joiner meta %dx%d != %dx%d", m2.Width, m2.Height, m1.Width, m1.Height)
	}
	if _, desc, key := second.waitFirstPicture(60 * time.Second); len(desc) == 0 || len(key) == 0 {
		t.Fatalf("joiner got desc=%d key=%d", len(desc), len(key))
	}

	// The original viewer is still fed: the restart must not have killed it.
	if tag, _ := first.next(60 * time.Second); tag == 0 {
		t.Logf("first viewer saw a meta re-broadcast (fine)")
	}
}

func TestIOSRejectsBadRequests(t *testing.T) {
	mux, err := newMux(sdk.New())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cases := []struct {
		query string
		want  int
	}{
		{"platform=windows&id=x", http.StatusBadRequest},
		{"id=D6FFA457-63C1-4621-90D0-37719DED598F", http.StatusBadRequest},
		{"platform=android&id=emulator-does-not-exist", http.StatusNotFound},
		{"platform=ios&id=not-a-simulator", http.StatusNotFound},
	}
	for _, tc := range cases {
		resp, err := http.Get(srv.URL + "/api/stream?" + tc.query)
		if err != nil {
			t.Fatalf("%s: %v", tc.query, err)
		}
		code := resp.StatusCode
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if code != tc.want {
			t.Fatalf("%s: status %d want %d (body %q)", tc.query, code, tc.want, body)
		}
		t.Logf("%s -> %d %s", tc.query, code, strings.TrimSpace(string(body)))
	}
}
