package idb

import (
	"context"
	"fmt"
	"sync"

	gen "github.com/Adelodunpeter25/sim-go/internal/idb/gen"
)

// VideoStream is one live H.264 pipe: Annex-B payload bytes arrive on Frames
// (feed them to scrcpy.ToAVCC — same packing as the Android path).
type VideoStream struct {
	Frames <-chan []byte

	stream gen.CompanionService_VideoStreamClient
	frames chan []byte
	stop   context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// StartVideo opens the device video pipe: H264 at the given fps and scale
// (1.0 = native). Proven live: valid Annex-B with SPS/PPS/IDR.
func (s *Session) StartVideo(fps uint64, scale float64) (*VideoStream, error) {
	if fps == 0 {
		fps = 30
	}
	if scale <= 0 {
		scale = 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := s.client.VideoStream(ctx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("idb video_stream: %w", err)
	}
	vs := &VideoStream{stream: stream, frames: make(chan []byte, 32), done: make(chan struct{})}
	vs.Frames = vs.frames
	vs.stop = cancel
	if err := stream.Send(&gen.VideoStreamRequest{
		Control: &gen.VideoStreamRequest_Start_{Start: &gen.VideoStreamRequest_Start{
			Format:      gen.VideoStreamRequest_H264,
			Fps:         fps,
			ScaleFactor: scale,
		}},
	}); err != nil {
		vs.Close()
		return nil, fmt.Errorf("idb video start: %w", err)
	}
	go vs.pump()
	return vs, nil
}

func (vs *VideoStream) pump() {
	defer close(vs.done)
	for {
		resp, err := vs.stream.Recv()
		if err != nil {
			return
		}
		data := resp.GetPayload().GetData()
		if len(data) == 0 {
			continue // log_output or empty heartbeat
		}
		select {
		case vs.frames <- data:
		default:
			// Slow viewer: drop, never block the device pipe.
		}
	}
}

// Stop asks the device to end the stream, then closes locally.
func (vs *VideoStream) Stop() {
	vs.once.Do(func() {
		_ = vs.stream.Send(&gen.VideoStreamRequest{
			Control: &gen.VideoStreamRequest_Stop_{Stop: &gen.VideoStreamRequest_Stop{}},
		})
		_ = vs.stream.CloseSend()
	})
	vs.Close()
}

// Close releases locally without asking (idempotent).
func (vs *VideoStream) Close() {
	vs.once.Do(func() { _ = vs.stream.CloseSend() })
	if vs.stop != nil {
		vs.stop()
	}
	<-vs.done
}
