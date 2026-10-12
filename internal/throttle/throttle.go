// Package throttle limits repeated failed logins per client address.
package throttle

import (
	"net"
	"sync"
	"time"
)

type entry struct {
	failures int
	first    time.Time // start of the current counting window
	until    time.Time // blocked until (zero = not blocked)
}

// Limiter blocks an address for Ban after MaxFailures failures within Window.
type Limiter struct {
	MaxFailures int
	Window      time.Duration
	Ban         time.Duration

	mu  sync.Mutex
	now func() time.Time
	m   map[string]*entry
}

// New returns a limiter. maxFailures <= 0 disables it.
func New(maxFailures int, window, ban time.Duration) *Limiter {
	return &Limiter{MaxFailures: maxFailures, Window: window, Ban: ban, now: time.Now, m: map[string]*entry{}}
}

// Key extracts the client IP from a host:port or bare address.
func Key(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// Blocked reports whether the address is currently banned, and for how long.
func (l *Limiter) Blocked(key string) (bool, time.Duration) {
	if l == nil || l.MaxFailures <= 0 {
		return false, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.m[key]
	if e == nil {
		return false, 0
	}
	if now := l.now(); now.Before(e.until) {
		return true, e.until.Sub(now)
	}
	return false, 0
}

// Fail records a failed attempt.
func (l *Limiter) Fail(key string) {
	if l == nil || l.MaxFailures <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.gc(now)
	e := l.m[key]
	if e == nil || now.Sub(e.first) > l.Window {
		e = &entry{first: now}
		l.m[key] = e
	}
	e.failures++
	if e.failures >= l.MaxFailures {
		e.until = now.Add(l.Ban)
		e.failures = 0
		e.first = now
	}
}

// Succeed clears the failure history of the address.
func (l *Limiter) Succeed(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.m, key)
	l.mu.Unlock()
}

// gc drops expired entries so the map cannot grow without bound.
func (l *Limiter) gc(now time.Time) {
	if len(l.m) < 1024 {
		return
	}
	for k, e := range l.m {
		if now.After(e.until) && now.Sub(e.first) > l.Window {
			delete(l.m, k)
		}
	}
}
