package idb

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func fakePool(idle time.Duration, n *int32) *Pool {
	return &Pool{entries: map[string]*poolEntry{}, idle: idle,
		start: func(ctx context.Context, udid string) (*Session, error) {
			atomic.AddInt32(n, 1)
			return &Session{UDID: udid}, nil
		}}
}

func TestPoolSharesOneSessionPerUDID(t *testing.T) {
	var n int32
	p := fakePool(time.Hour, &n)
	a, err := p.Acquire(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := p.Acquire(context.Background(), "u1")
	if a.Session != b.Session || n != 1 {
		t.Fatalf("want one shared session, starts=%d", n)
	}
	c, _ := p.Acquire(context.Background(), "u2")
	if c.Session == a.Session || n != 2 {
		t.Fatalf("distinct udids must not share, starts=%d", n)
	}
}

func TestPoolIdleCloseAfterLastRelease(t *testing.T) {
	var n int32
	p := fakePool(30*time.Millisecond, &n)
	a, _ := p.Acquire(context.Background(), "u1")
	b, _ := p.Acquire(context.Background(), "u1")
	a.Release()
	a.Release() // idempotent: must not drop b's ref
	time.Sleep(80 * time.Millisecond)
	p.mu.Lock()
	_, alive := p.entries["u1"]
	p.mu.Unlock()
	if !alive {
		t.Fatal("closed while still leased")
	}
	b.Release()
	time.Sleep(120 * time.Millisecond)
	p.mu.Lock()
	_, alive = p.entries["u1"]
	p.mu.Unlock()
	if alive {
		t.Fatal("idle companion not closed")
	}
}

func TestPoolReacquireCancelsIdleClose(t *testing.T) {
	var n int32
	p := fakePool(40*time.Millisecond, &n)
	a, _ := p.Acquire(context.Background(), "u1")
	a.Release()
	b, _ := p.Acquire(context.Background(), "u1")
	time.Sleep(120 * time.Millisecond)
	if n != 1 || b.Session != a.Session {
		t.Fatalf("reacquire should reuse, starts=%d", n)
	}
}

func TestPoolStartFailureNotCached(t *testing.T) {
	var calls int32
	p := &Pool{entries: map[string]*poolEntry{}, idle: time.Hour,
		start: func(ctx context.Context, udid string) (*Session, error) {
			if atomic.AddInt32(&calls, 1) == 1 {
				return nil, errors.New("boom")
			}
			return &Session{UDID: udid}, nil
		}}
	if _, err := p.Acquire(context.Background(), "u1"); err == nil {
		t.Fatal("want error")
	}
	if _, err := p.Acquire(context.Background(), "u1"); err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
}
