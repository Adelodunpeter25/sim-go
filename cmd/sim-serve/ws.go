package main

// Minimal WebSocket server on stdlib only (no new dependencies).
//
// Supports exactly what the preview page needs: the opening handshake,
// server→client text/binary frames, client→server masked text frames,
// ping/pong, and close. Fragmented control frames are rejected; fragmented
// data frames are re-assembled. Anything else closes the connection.

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const wsMagic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	wsText   = 1
	wsBinary = 2
	wsClose  = 8
	wsPing   = 9
	wsPong   = 10
)

type wsConn struct {
	conn net.Conn
	br   *bufio.Reader
	wmu  sync.Mutex
}

func serveWS(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, fmt.Errorf("not a websocket upgrade")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, fmt.Errorf("missing Sec-WebSocket-Key")
	}
	sum := sha1.Sum([]byte(key + wsMagic))
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, fmt.Errorf("server does not support hijacking")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n"
	if _, err := rw.WriteString(resp); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &wsConn{conn: conn, br: bufio.NewReader(conn)}, nil
}

type wsMessage struct {
	opcode int
	data   []byte
}

func (c *wsConn) read() (wsMessage, error) {
	var data []byte
	var opcode int = -1
	for {
		fin, op, payload, err := c.readFrame()
		if err != nil {
			return wsMessage{}, err
		}
		switch op {
		case wsPing:
			_ = c.writeFrame(true, wsPong, payload)
			continue
		case wsPong:
			continue
		case wsClose:
			_ = c.writeFrame(true, wsClose, payload)
			return wsMessage{}, io.EOF
		case wsText, wsBinary:
			if opcode != -1 {
				return wsMessage{}, fmt.Errorf("interleaved fragment")
			}
			opcode, data = op, payload
		case 0: // continuation
			if opcode == -1 {
				return wsMessage{}, fmt.Errorf("stray continuation")
			}
			data = append(data, payload...)
		default:
			return wsMessage{}, fmt.Errorf("bad opcode %d", op)
		}
		if fin {
			return wsMessage{opcode: opcode, data: data}, nil
		}
	}
}

func (c *wsConn) readFrame() (fin bool, opcode int, payload []byte, err error) {
	var hdr [2]byte
	if _, err = io.ReadFull(c.br, hdr[:]); err != nil {
		return false, 0, nil, err
	}
	fin = hdr[0]&0x80 != 0
	opcode = int(hdr[0] & 0x0f)
	masked := hdr[1]&0x80 != 0
	length := int64(hdr[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		if _, err = io.ReadFull(c.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = int64(ext[0])<<8 | int64(ext[1])
	case 127:
		var ext [8]byte
		if _, err = io.ReadFull(c.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = 0
		for _, b := range ext {
			length = length<<8 | int64(b)
		}
		if length < 0 {
			return false, 0, nil, fmt.Errorf("negative length")
		}
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(c.br, mask[:]); err != nil {
			return false, 0, nil, err
		}
	}
	if length > 32<<20 {
		return false, 0, nil, fmt.Errorf("frame too large (%d)", length)
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(c.br, payload); err != nil {
		return false, 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return fin, opcode, payload, nil
}

func (c *wsConn) writeFrame(fin bool, opcode int, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	var hdr []byte
	b0 := byte(opcode)
	if fin {
		b0 |= 0x80
	}
	hdr = append(hdr, b0)
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n < 65536:
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 127, 0, 0, 0, 0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	if _, err := c.conn.Write(hdr); err != nil {
		return err
	}
	_, err := c.conn.Write(payload)
	return err
}

// WriteText sends one unfragmented text message.
func (c *wsConn) WriteText(data []byte) error { return c.writeFrame(true, wsText, data) }

// WriteBinary sends one unfragmented binary message.
func (c *wsConn) WriteBinary(data []byte) error { return c.writeFrame(true, wsBinary, data) }

// Close sends a close frame and closes the TCP connection.
func (c *wsConn) Close() error {
	_ = c.writeFrame(true, wsClose, nil)
	return c.conn.Close()
}
