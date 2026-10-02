package idb

import (
	"context"
	"sync"
	"time"
)

// IdleClose is how long a companion with no leases stays alive, so a burst of
// verbs (tap, tap, type) reuses one process instead of respawning each time.
const IdleClose = 30 * time.Second

// Pool is a refcounted set of companions, one per UDID. The stream hub and the
// driver verbs share DefaultPool so a stream plus a tap on the same simulator
// never run two companions side by side.
type Pool struct {
	mu      sync.Mutex
	entries map[string]*poolEntry
	idle    time.Duration
	start   func(ctx context.Context, udid string) (*Session, error)
}

type poolEntry struct {
	sess  *Session
	refs  int
	timer *time.Timer
	ready chan struct{} // closed once sess/err are set
	err   error
}

// Lease is one holder's claim on a pooled session. Release is idempotent.
type Lease struct {
	Session *Session
	pool    *Pool
	udid    string
	entry   *poolEntry
	once    sync.Once
}

// DefaultPool is the process-wide pool.
var DefaultPool = NewPool()

// NewPool builds an empty pool that starts real companions.
func NewPool() *Pool {
	return &Pool{entries: map[string]*poolEntry{}, idle: IdleClose, start: Start}
}

// Acquire returns a lease on the companion for udid, starting it if needed.
// A companion that died since last use is replaced.
func (p *Pool) Acquire(ctx context.Context, udid string) (*Lease, error) {
	for {
		p.mu.Lock()
		e, ok := p.entries[udid]
		if ok && e.sess != nil && e.sess.process != nil && exited(e.sess.process) {
			// Dead companion: drop it so the next pass starts a fresh one.
			delete(p.entries, udid)
			if e.timer != nil {
				e.timer.Stop()
			}
			go e.sess.Close()
			ok = false
		}
		if !ok {
			e = &poolEntry{refs: 1, ready: make(chan struct{})}
			p.entries[udid] = e
			p.mu.Unlock()
			sess, err := p.start(ctx, udid)
			p.mu.Lock()
			e.sess, e.err = sess, err
			if err != nil && p.entries[udid] == e {
				delete(p.entries, udid)
			}
			close(e.ready)
			p.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return &Lease{Session: sess, pool: p, udid: udid, entry: e}, nil
		}
		e.refs++
		if e.timer != nil {
			e.timer.Stop()
			e.timer = nil
		}
		p.mu.Unlock()

		select {
		case <-e.ready:
		case <-ctx.Done():
			p.release(udid, e)
			return nil, ctx.Err()
		}
		if e.err != nil {
			// The starter failed; its entry is gone. Retry with our own start.
			continue
		}
		return &Lease{Session: e.sess, pool: p, udid: udid, entry: e}, nil
	}
}

// Release gives the claim back; the last release arms the idle close.
func (l *Lease) Release() {
	l.once.Do(func() { l.pool.release(l.udid, l.entry) })
}

func (p *Pool) release(udid string, e *poolEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e.refs > 0 {
		e.refs--
	}
	if e.refs > 0 || e.sess == nil || p.entries[udid] != e {
		return
	}
	if e.timer != nil {
		e.timer.Stop()
	}
	e.timer = time.AfterFunc(p.idle, func() {
		p.mu.Lock()
		if e.refs > 0 || p.entries[udid] != e {
			p.mu.Unlock()
			return
		}
		delete(p.entries, udid)
		p.mu.Unlock()
		e.sess.Close()
	})
}

// Close tears down every pooled companion immediately, leased or not.
func (p *Pool) Close() {
	p.mu.Lock()
	entries := p.entries
	p.entries = map[string]*poolEntry{}
	p.mu.Unlock()
	for _, e := range entries {
		if e.timer != nil {
			e.timer.Stop()
		}
		<-e.ready
		if e.sess != nil {
			e.sess.Close()
		}
	}
}

// CloseAll closes the process-wide pool.
func CloseAll() { DefaultPool.Close() }
