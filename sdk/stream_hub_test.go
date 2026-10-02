package sdk

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeBackend struct {
	resets  int32
	closed  int32
	inputs  chan Input
	release chan struct{}
}

func newFake() *fakeBackend {
	return &fakeBackend{inputs: make(chan Input, 16), release: make(chan struct{})}
}
func (f *fakeBackend) info() (int, int, string) { return 100, 200, "fake" }
func (f *fakeBackend) pump(ss *session) {
	select {
	case <-ss.done:
	case <-f.release:
	}
}
func (f *fakeBackend) input(in Input) error { f.inputs <- in; return nil }
func (f *fakeBackend) reset()               { atomic.AddInt32(&f.resets, 1) }
func (f *fakeBackend) close()               { atomic.AddInt32(&f.closed, 1) }

var testDevs = []Device{
	{Platform: "android", ID: "emulator-5554", Name: "Pixel_7", State: "device"},
	{Platform: "android", ID: "emulator-5556", Name: "Off", State: "offline"},
	{Platform: "ios", ID: "U1", Name: "iPhone", State: "Booted"},
	{Platform: "ios", ID: "U2", Name: "Sleepy", State: "Shutdown"},
}

func testHub(be *fakeBackend, starts *int32) *streamHub {
	h := newStreamHub(func(context.Context) ([]Device, error) { return testDevs, nil })
	h.open = func(ctx context.Context, platform, id string) (backend, error) {
		atomic.AddInt32(starts, 1)
		return be, nil
	}
	return h
}

func attach(t *testing.T, h *streamHub, platform, id string) *Stream {
	t.Helper()
	c := &Client{hub: h}
	st, err := c.Stream(context.Background(), platform, id)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func recv(t *testing.T, st *Stream) Packet {
	t.Helper()
	select {
	case p, ok := <-st.Packets():
		if !ok {
			t.Fatalf("packets closed: %v", st.Err())
		}
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for packet")
	}
	return Packet{}
}

func TestStreamSharesOneBackendAndFansOut(t *testing.T) {
	var starts int32
	be := newFake()
	h := testHub(be, &starts)
	a := attach(t, h, "android", "Pixel_7") // by AVD name
	b := attach(t, h, "android", "emulator-5554")
	if starts != 1 {
		t.Fatalf("backend starts=%d want 1", starts)
	}
	if p := recv(t, a); p.Kind != PacketMeta || p.Meta.Width != 100 || p.Meta.Name != "fake" {
		t.Fatalf("first packet %+v", p)
	}
	recv(t, b)
	ss := a.sess
	ss.publishDesc([]byte{1, 2}, "avc1.42")
	ss.publishFrame(true, []byte{9})
	ss.publishFrame(false, []byte{8})
	for _, st := range []*Stream{a, b} {
		if p := recv(t, st); p.Kind != PacketDescription {
			t.Fatalf("want desc, got %+v", p)
		}
		if p := recv(t, st); p.Kind != PacketMeta || p.Meta.Codec != "avc1.42" {
			t.Fatalf("want meta with codec, got %+v", p)
		}
		if p := recv(t, st); p.Kind != PacketKeyFrame {
			t.Fatalf("want key, got %+v", p)
		}
		if p := recv(t, st); p.Kind != PacketDelta {
			t.Fatalf("want delta, got %+v", p)
		}
	}
}

func TestLateJoinerGetsMetaDescThenReset(t *testing.T) {
	var starts int32
	be := newFake()
	h := testHub(be, &starts)
	a := attach(t, h, "android", "emulator-5554")
	recv(t, a)
	if be.resets != 0 {
		t.Fatal("first viewer must not force a reset")
	}
	a.sess.publishDesc([]byte{7}, "avc1.64")
	late := attach(t, h, "android", "emulator-5554")
	if p := recv(t, late); p.Kind != PacketMeta || p.Meta.Codec != "avc1.64" {
		t.Fatalf("late meta %+v", p)
	}
	if p := recv(t, late); p.Kind != PacketDescription || p.Data[0] != 7 {
		t.Fatalf("late desc %+v", p)
	}
	if be.resets != 1 {
		t.Fatalf("resets=%d want 1", be.resets)
	}
}

func TestSlowViewerDroppedWithoutBlocking(t *testing.T) {
	var starts int32
	h := testHub(newFake(), &starts)
	slow := attach(t, h, "android", "emulator-5554")
	fast := attach(t, h, "android", "emulator-5554")
	done := make(chan struct{})
	go func() {
		for i := 0; i < packetBuffer*3; i++ {
			slow.sess.publishFrame(false, []byte{1})
			for len(fast.ch) > 0 {
				<-fast.ch
			}
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publisher blocked on a slow viewer")
	}
	for range slow.Packets() {
	}
	if !errors.Is(slow.Err(), ErrSlowViewer) {
		t.Fatalf("slow err=%v", slow.Err())
	}
	if fast.Err() != nil {
		t.Fatalf("fast viewer dropped: %v", fast.Err())
	}
}

func TestIdleCloseAfterLastViewerAndReattachCancels(t *testing.T) {
	var starts int32
	be := newFake()
	h := testHub(be, &starts)
	h.idle = 60 * time.Millisecond
	a := attach(t, h, "android", "emulator-5554")
	a.Close()
	b := attach(t, h, "android", "emulator-5554") // cancels the timer
	time.Sleep(150 * time.Millisecond)
	if be.closed != 0 || starts != 1 {
		t.Fatalf("closed=%d starts=%d: reattach should keep session", be.closed, starts)
	}
	b.Close()
	time.Sleep(200 * time.Millisecond)
	if atomic.LoadInt32(&be.closed) != 1 {
		t.Fatalf("backend closed=%d want 1 after idle", be.closed)
	}
	c := attach(t, h, "android", "emulator-5554")
	defer c.Close()
	if starts != 2 {
		t.Fatalf("starts=%d want a fresh backend", starts)
	}
}

func TestInputRoutesAndClosedStreamRejects(t *testing.T) {
	var starts int32
	be := newFake()
	h := testHub(be, &starts)
	st := attach(t, h, "android", "emulator-5554")
	if err := st.Touch("down", 5, 6); err != nil {
		t.Fatal(err)
	}
	in := <-be.inputs
	if in.Type != "touch" || in.Action != "down" || in.X != 5 || in.Y != 6 {
		t.Fatalf("input %+v", in)
	}
	st.Key(66)
	if in := <-be.inputs; in.Code != 66 {
		t.Fatalf("key %+v", in)
	}
	st.Close()
	if err := st.Input(Input{Type: "reset"}); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("err=%v", err)
	}
	st.Close() // idempotent
}

func TestSessionEndClosesViewers(t *testing.T) {
	var starts int32
	be := newFake()
	h := testHub(be, &starts)
	st := attach(t, h, "android", "emulator-5554")
	close(be.release) // device stream dies
	for range st.Packets() {
	}
	if !errors.Is(st.Err(), ErrStreamEnded) {
		t.Fatalf("err=%v", st.Err())
	}
}

func TestStreamErrors(t *testing.T) {
	var starts int32
	h := testHub(newFake(), &starts)
	c := &Client{hub: h}
	ctx := context.Background()
	cases := []struct {
		platform, id string
		want         error
	}{
		{"windows", "x", ErrInvalidPlatform},
		{"android", "emulator-9999", ErrDeviceNotFound},
		{"android", "emulator-5556", ErrDeviceNotFound}, // offline
		{"android", "NoSuchAVD", ErrDeviceNotFound},
		{"ios", "Sleepy", ErrDeviceNotFound}, // shutdown
		{"ios", "nope", ErrDeviceNotFound},
	}
	for _, tc := range cases {
		if _, err := c.Stream(ctx, tc.platform, tc.id); !errors.Is(err, tc.want) {
			t.Fatalf("%s/%s: err=%v want %v", tc.platform, tc.id, err, tc.want)
		}
	}
	h.open = func(context.Context, string, string) (backend, error) { return nil, errors.New("adb exploded") }
	if _, err := c.Stream(ctx, "android", "emulator-5554"); !errors.Is(err, ErrBackendStart) {
		t.Fatalf("err=%v want ErrBackendStart", err)
	}
	if starts != 0 {
		t.Fatalf("unexpected starts=%d", starts)
	}
}

func TestConcurrentAttachDetach(t *testing.T) {
	var starts int32
	h := testHub(newFake(), &starts)
	h.idle = time.Millisecond
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := &Client{hub: h}
			for j := 0; j < 20; j++ {
				st, err := c.Stream(context.Background(), "android", "emulator-5554")
				if err != nil {
					t.Error(err)
					return
				}
				st.publishNoop()
				st.Close()
			}
		}()
	}
	wg.Wait()
	h.closeAll()
}

func (s *Stream) publishNoop() { s.sess.publishFrame(false, []byte{1}) }
