package main

// Thin websocket adapter over sdk.Stream. The hub, backends and input
// handling live in the sdk; this only maps packets to the wire the page
// speaks: meta as a text message, then tagged binary 1 desc / 2 key / 3 delta.

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Adelodunpeter25/sim-go/sdk"
)

// Wire tags: 1 description (avcC), 2 keyframe, 3 delta.
const (
	tagDesc  = 1
	tagKey   = 2
	tagDelta = 3
)

type streamAPI struct{ c *sdk.Client }

func newStreamAPI(c *sdk.Client) *streamAPI { return &streamAPI{c: c} }

func writeJSONError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
}

func (a *streamAPI) attach(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	st, err := a.c.Stream(r.Context(), q.Get("platform"), q.Get("id"))
	if err != nil {
		code := http.StatusBadGateway
		switch {
		case errors.Is(err, sdk.ErrInvalidPlatform):
			code = http.StatusBadRequest
		case errors.Is(err, sdk.ErrDeviceNotFound):
			code = http.StatusNotFound
		}
		writeJSONError(w, err.Error(), code)
		return
	}
	conn, err := serveWS(w, r)
	if err != nil {
		_ = st.Close()
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	go readInput(conn, st)
	go writePackets(conn, st)
}

// writePackets forwards the stream until it ends, then closes the socket.
func writePackets(conn *wsConn, st *sdk.Stream) {
	defer conn.Close()
	defer st.Close()
	for p := range st.Packets() {
		var err error
		switch p.Kind {
		case sdk.PacketMeta:
			var meta []byte
			meta, err = json.Marshal(map[string]any{
				"type": "meta", "width": p.Meta.Width, "height": p.Meta.Height,
				"name": p.Meta.Name, "codec": p.Meta.Codec,
			})
			if err == nil {
				err = conn.WriteText(meta)
			}
		case sdk.PacketDescription:
			err = conn.WriteBinary(append([]byte{tagDesc}, p.Data...))
		case sdk.PacketKeyFrame:
			err = conn.WriteBinary(append([]byte{tagKey}, p.Data...))
		case sdk.PacketDelta:
			err = conn.WriteBinary(append([]byte{tagDelta}, p.Data...))
		}
		if err != nil {
			return
		}
	}
}

// readInput feeds viewer JSON into the stream. A rejected input must never
// take down the viewer's socket, so Input errors are ignored.
func readInput(conn *wsConn, st *sdk.Stream) {
	defer st.Close() // ends Packets, which closes the socket
	for {
		msg, err := conn.read()
		if err != nil {
			return
		}
		if msg.opcode != wsText {
			continue
		}
		var in sdk.Input
		if err := json.Unmarshal(msg.data, &in); err != nil {
			continue
		}
		_ = st.Input(in) // synchronous: touch down/move/up must stay ordered
	}
}
