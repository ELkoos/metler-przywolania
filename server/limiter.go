package main

import (
	"net"
	"sync"
	"time"
)

// Limiter blokuje klucz (np. pracownika albo adres IP) po zbyt wielu nieudanych próbach.
type Limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	block  time.Duration
	m      map[string]*attempts
	now    func() time.Time
}

type attempts struct {
	first, until time.Time
	n            int
}

func NewLimiter(max int, window, block time.Duration) *Limiter {
	return &Limiter{max: max, window: window, block: block, m: map[string]*attempts{}, now: time.Now}
}

// Blocked zwraca czas do końca blokady (0 = brak blokady).
func (l *Limiter) Blocked(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.m[key]
	if a == nil {
		return 0
	}
	if d := a.until.Sub(l.now()); d > 0 {
		return d
	}
	return 0
}

// Fail zapisuje nieudaną próbę; zwraca true, gdy klucz właśnie został zablokowany.
func (l *Limiter) Fail(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	a := l.m[key]
	if a == nil || now.Sub(a.first) > l.window {
		a = &attempts{first: now}
		l.m[key] = a
	}
	a.n++
	if a.n >= l.max {
		a.until = now.Add(l.block)
		a.n = 0
		a.first = now
		return true
	}
	// sprzątanie starych wpisów
	if len(l.m) > 10000 {
		for k, v := range l.m {
			if now.Sub(v.first) > l.window && now.After(v.until) {
				delete(l.m, k)
			}
		}
	}
	return false
}

// Success kasuje licznik nieudanych prób.
func (l *Limiter) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if a := l.m[key]; a != nil && l.now().After(a.until) {
		delete(l.m, key)
	}
}

func hostOf(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}
