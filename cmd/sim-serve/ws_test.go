package main

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// RFC 6455 §1.3 opening-handshake example vector.
func TestWSHandshakeVector(t *testing.T) {
	var server *wsConn
	done := make(chan error, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := serveWS(w, r)
		if err != nil {
			done <- err
			return
		}
		server = c
		done <- nil
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go srv.Serve(ln)
	defer srv.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET /api/stream HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	br := bufio.NewReader(conn)
	status, _ := br.ReadString('\n')
	if !strings.Contains(status, "101") {
		t.Fatalf("status=%q", status)
	}
	accept := ""
	for {
		line, _ := br.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "sec-websocket-accept:") {
			accept = strings.TrimSpace(line[len("sec-websocket-accept:"):])
		}
	}
	if accept != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("accept=%q", accept)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_ = server.Close()
}

func TestWSFraming(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	server := &wsConn{conn: a, br: bufio.NewReader(a)}

	// Client → server: masked "hi" text frame.
	go func() {
		_, _ = b.Write([]byte{0x81, 0x82, 0x11, 0x22, 0x33, 0x44, 'h' ^ 0x11, 'i' ^ 0x22})
	}()
	msg, err := server.read()
	if err != nil {
		t.Fatal(err)
	}
	if msg.opcode != wsText || string(msg.data) != "hi" {
		t.Fatalf("msg=%d %q", msg.opcode, msg.data)
	}

	// Server → client: binary frame, unmasked, exact bytes.
	payload := make([]byte, 300)
	for i := range payload {
		payload[i] = byte(i)
	}
	go func() { _ = server.WriteBinary(payload) }()
	_ = b.SetReadDeadline(time.Now().Add(5 * time.Second))
	var hdr [4]byte
	if _, err := readFullBytes(b, hdr[:]); err != nil {
		t.Fatal(err)
	}
	if hdr[0] != 0x82 || hdr[1] != 126 || hdr[2] != 1 || hdr[3] != 44 {
		t.Fatalf("header=%x", hdr)
	}
	got := make([]byte, 300)
	if _, err := readFullBytes(b, got); err != nil {
		t.Fatal(err)
	}
	for i := range got {
		if got[i] != byte(i) {
			t.Fatalf("byte %d", i)
		}
	}

	// Ping → pong (read pumps the ping; the following read blocks, so run it aside).
	go func() {
		_, _ = b.Write([]byte{0x89, 0x80, 0x11, 0x22, 0x33, 0x44}) // masked, empty
	}()
	go func() { _, _ = server.read() }()
	_ = b.SetReadDeadline(time.Now().Add(5 * time.Second))
	var pong [2]byte
	if _, err := readFullBytes(b, pong[:]); err != nil {
		t.Fatal(err)
	}
	if pong[0] != 0x8A || pong[1] != 0x00 {
		t.Fatalf("pong=%x", pong)
	}
	// Client side first so the server close frame fails fast instead of
	// blocking on a pipe with no reader.
	_ = b.Close()
	_ = server.Close()
}

func readFullBytes(r net.Conn, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := r.Read(buf[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
