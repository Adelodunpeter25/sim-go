package sdk

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/Adelodunpeter25/sim-go/internal/scrcpy"
)

// buttonKeycodes maps viewer button names to Android keycodes.
var buttonKeycodes = map[string]int{
	"home": 3, "back": 4, "menu": 82, "power": 26, "app-switcher": 187,
	"volume-up": 24, "volume-down": 25,
}

// androidBackend drives a scrcpy session.
type androidBackend struct {
	sc *scrcpy.Session
	// size is the live stream size packed as w<<32|h. Touch coordinates are
	// in stream pixels, so a rotation must update it or taps land wrong.
	size atomic.Uint64
}

func packSize(w, h int) uint64 { return uint64(w)<<32 | uint64(uint32(h)) }

func (b *androidBackend) streamSize() (int, int) {
	v := b.size.Load()
	return int(v >> 32), int(uint32(v))
}

func openAndroid(ctx context.Context, serial string, q StreamQuality) (backend, error) {
	sc, err := scrcpy.StartWith(ctx, serial, scrcpy.Options{MaxSize: q.MaxSize, MaxFPS: q.MaxFPS, BitRate: q.BitRate})
	if err != nil {
		return nil, err
	}
	b := &androidBackend{sc: sc}
	b.size.Store(packSize(sc.Meta.Width, sc.Meta.Height))
	return b, nil
}

func (b *androidBackend) info() (int, int, string) {
	return b.sc.Meta.Width, b.sc.Meta.Height, b.sc.Meta.Name
}

// pump reads device frames: config rebuilds the avcC description, media
// frames go out as AVCC key/delta.
func (b *androidBackend) pump(ss *session) {
	for {
		select {
		case <-ss.done:
			return
		default:
		}
		f, err := b.sc.ReadFrame()
		if err != nil {
			return
		}
		if f.Config {
			_, _, sps, pps := scrcpy.ToAVCC(f.Payload)
			if len(sps) == 0 || len(pps) == 0 {
				continue
			}
			desc, err := scrcpy.AVCCDescription(sps, pps)
			if err != nil {
				continue
			}
			codec, err := scrcpy.CodecString(sps)
			if err != nil {
				continue
			}
			if w, h, err := scrcpy.SPSSize(sps); err == nil {
				if cw, ch := b.streamSize(); cw != w || ch != h {
					b.size.Store(packSize(w, h))
					ss.setSize(w, h)
				}
			}
			ss.publishDesc(desc, codec)
			continue
		}
		avcc, isKey, _, _ := scrcpy.ToAVCC(f.Payload)
		if len(avcc) == 0 {
			continue
		}
		ss.publishFrame(isKey || f.Key, avcc)
	}
}

func (b *androidBackend) input(in Input) error {
	w, h := b.streamSize()
	switch in.Type {
	case "touch":
		action := scrcpy.TouchMove
		switch strings.ToLower(in.Action) {
		case "down":
			action = scrcpy.TouchDown
		case "up":
			action = scrcpy.TouchUp
		}
		return b.sc.SendRaw(scrcpy.EncodeTouch(action, in.X, in.Y, w, h))
	case "scroll":
		return b.sc.SendRaw(scrcpy.EncodeScroll(in.X, in.Y, w, h, in.DX, in.DY))
	case "key":
		return b.sc.Key(in.Code)
	case "text":
		return b.sc.Text(in.Text)
	case "button":
		// Names with no Android mapping are dropped, like the iOS-only ones.
		if code, ok := buttonKeycodes[strings.ToLower(in.Button)]; ok {
			return b.sc.Key(code)
		}
		return nil
	case "reset":
		return b.sc.ResetVideo()
	}
	return fmt.Errorf("unknown input type %q", in.Type)
}

func (b *androidBackend) reset() { _ = b.sc.ResetVideo() }

func (b *androidBackend) close() { b.sc.Close() }
