// Package streamws serves sdk.Stream over a websocket.
//
// Wire (what the preview page speaks):
//
//	server -> client  text    {"type":"meta","width":..,"height":..,"name":..,"codec":..}
//	server -> client  binary  [1] avcC description | [2] key frame | [3] delta
//	client -> server  text    sdk.Input JSON (touch, scroll, key, text, button, reset)
//
// Query: ?platform=android|ios&id=<serial|avd|udid|name>. Failures before the
// upgrade are JSON {"ok":false,"error":..} with 400 (bad platform), 404
// (unknown or not booted device) or 502 (backend failed to start).
//
// This is the only sdk package with a websocket dependency
// (gorilla/websocket); the core stays stdlib.
package streamws

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Adelodunpeter25/sim-go/sdk"
	"github.com/gorilla/websocket"
)

// Binary frame tags.
const (
	TagDescription = 1
	TagKeyFrame    = 2
	TagDelta       = 3
)

const (
	writeWait    = 10 * time.Second
	pongWait     = 60 * time.Second
	pingInterval = pongWait * 9 / 10
	maxInputSize = 1 << 20 // input messages are tiny JSON; refuse anything big
)

// Handler returns the websocket endpoint for c's streams.
func Handler(c *sdk.Client) http.Handler {
	up := websocket.Upgrader{
		ReadBufferSize:  4 << 10,
		WriteBufferSize: 64 << 10,
		// Loopback tool, no auth: same-origin policy is not the boundary
		// here (see cmd/sim-serve), so don't reject non-browser clients.
		CheckOrigin: func(*http.Request) bool { return true },
		Error: func(w http.ResponseWriter, r *http.Request, status int, reason error) {
			writeError(w, reason.Error(), status)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		st, err := c.Stream(r.Context(), q.Get("platform"), q.Get("id"))
		if err != nil {
			writeError(w, err.Error(), statusFor(err))
			return
		}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil { // Upgrader already replied
			_ = st.Close()
			return
		}
		serve(conn, st)
	})
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, sdk.ErrInvalidPlatform):
		return http.StatusBadRequest
	case errors.Is(err, sdk.ErrDeviceNotFound):
		return http.StatusNotFound
	}
	return http.StatusBadGateway
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
}

// serve pumps one connection. gorilla allows one concurrent writer, so every
// write (packets and pings) happens in the writer goroutine below; the reader
// runs here and ends the stream when the peer goes away.
func serve(conn *websocket.Conn, st *sdk.Stream) {
	defer conn.Close()
	defer st.Close()

	go func() { // reader: viewer JSON -> Input
		defer st.Close() // ends Packets, which ends the writer
		conn.SetReadLimit(maxInputSize)
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(pongWait))
		})
		for {
			typ, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(pongWait))
			if typ != websocket.TextMessage {
				continue
			}
			var in sdk.Input
			if json.Unmarshal(data, &in) != nil {
				continue
			}
			// Synchronous: touch down/move/up must stay ordered. A rejected
			// input must never take down the viewer's socket.
			_ = st.Input(in)
		}
	}()

	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case p, ok := <-st.Packets():
			if !ok {
				_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
				_ = conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			if err := write(conn, p); err != nil {
				return
			}
		case <-ping.C:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func write(conn *websocket.Conn, p sdk.Packet) error {
	_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
	switch p.Kind {
	case sdk.PacketMeta:
		b, err := json.Marshal(map[string]any{
			"type": "meta", "width": p.Meta.Width, "height": p.Meta.Height,
			"name": p.Meta.Name, "codec": p.Meta.Codec,
		})
		if err != nil {
			return err
		}
		return conn.WriteMessage(websocket.TextMessage, b)
	case sdk.PacketDescription:
		return conn.WriteMessage(websocket.BinaryMessage, tagged(TagDescription, p.Data))
	case sdk.PacketKeyFrame:
		return conn.WriteMessage(websocket.BinaryMessage, tagged(TagKeyFrame, p.Data))
	case sdk.PacketDelta:
		return conn.WriteMessage(websocket.BinaryMessage, tagged(TagDelta, p.Data))
	}
	return nil
}

// tagged copies once; p.Data is shared between viewers and must not be touched.
func tagged(tag byte, data []byte) []byte {
	out := make([]byte, 1+len(data))
	out[0] = tag
	copy(out[1:], data)
	return out
}
