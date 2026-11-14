package auth

import (
	"sync"
	"time"
)

type Limiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu sync.Mutex
	m  map[string]*bucket
}

type bucket struct {
	fails int
	reset time.Time
}

func NewLimiter(maxFails int, window time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{max: maxFails, window: window, now: now, m: map[string]*bucket{}}
}

func (l *Limiter) Allow(keys ...string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		if b, ok := l.m[k]; ok && now.Before(b.reset) && b.fails >= l.max {
			return false
		}
	}
	return true
}

func (l *Limiter) Fail(keys ...string) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, b := range l.m {
		if !now.Before(b.reset) {
			delete(l.m, k)
		}
	}
	for _, k := range keys {
		b, ok := l.m[k]
		if !ok {
			b = &bucket{reset: now.Add(l.window)}
			l.m[k] = b
		}
		b.fails++
	}
}

func (l *Limiter) Reset(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		delete(l.m, k)
	}
}
