package cnki

import (
	"context"
	"os"
	"strconv"
	"sync"
	"time"
)

// Limiter is the only throttle in the system. It spaces request starts by a
// fixed interval (the polite rate CNKI tolerates) while allowing several
// requests in flight, so latency overlaps instead of adding to the interval.
// During human verification it holds every new request.
type Limiter struct {
	mu       sync.Mutex
	interval time.Duration
	backoff  float64
	next     time.Time
	slots    chan struct{}
	held     chan struct{} // non-nil while a person owns the browser
}

func NewLimiter(interval time.Duration, inflight int) *Limiter {
	return &Limiter{interval: interval, backoff: 1, slots: make(chan struct{}, max(1, inflight))}
}
func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// Acquire waits for a start slot and returns the function that ends the request.
func (l *Limiter) Acquire(ctx context.Context) (func(), error) {
	if err := l.waitOpen(ctx); err != nil {
		return nil, err
	}
	select {
	case l.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-l.slots }
	l.mu.Lock()
	at := l.next
	if now := time.Now(); at.Before(now) {
		at = now
	}
	l.next = at.Add(time.Duration(float64(l.interval) * l.backoff))
	l.mu.Unlock()
	if wait := time.Until(at); wait > 0 {
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			release()
			return nil, ctx.Err()
		}
	}
	if err := l.waitOpen(ctx); err != nil {
		release()
		return nil, err
	}
	return release, nil
}
func (l *Limiter) waitOpen(ctx context.Context) error {
	for {
		l.mu.Lock()
		held := l.held
		l.mu.Unlock()
		if held == nil {
			return nil
		}
		select {
		case <-held:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
func (l *Limiter) Hold() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held == nil {
		l.held = make(chan struct{})
	}
}
func (l *Limiter) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held != nil {
		close(l.held)
		l.held = nil
	}
	l.next = time.Now().Add(l.interval)
}

// Slow doubles spacing after a rate-limit or challenge; Fast decays it back.
func (l *Limiter) Slow() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.backoff = min(8, max(2, l.backoff*2))
}
func (l *Limiter) Fast() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.backoff = max(1, l.backoff*.9)
}
