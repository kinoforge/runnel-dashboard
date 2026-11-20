package auth

import (
	"sync"
	"time"
)

type Limiter struct {
	max     int
	window  time.Duration
	tracked int
	now     func() time.Time

	mu        sync.Mutex
	m         map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	fails int
	reset time.Time
}

func NewLimiter(maxFails int, window time.Duration, tracked int, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{max: maxFails, window: window, tracked: max(tracked, 1), now: now, m: map[string]*bucket{}, lastSweep: now()}
}

func (l *Limiter) Allow(keys ...string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.allowedLocked(now, keys)
}

func (l *Limiter) Take(keys ...string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.allowedLocked(now, keys) {
		return false
	}
	l.recordLocked(now, keys)
	return true
}

func (l *Limiter) Fail(keys ...string) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recordLocked(now, keys)
}

func (l *Limiter) allowedLocked(now time.Time, keys []string) bool {
	for _, k := range keys {
		if b, ok := l.m[k]; ok && now.Before(b.reset) && b.fails >= l.max {
			return false
		}
	}
	return true
}

func (l *Limiter) recordLocked(now time.Time, keys []string) {
	if len(l.m) >= l.tracked || !now.Before(l.lastSweep.Add(l.window)) {
		for k, b := range l.m {
			if !now.Before(b.reset) {
				delete(l.m, k)
			}
		}
		l.lastSweep = now
	}
	for _, k := range keys {
		b, ok := l.m[k]
		if !ok || !now.Before(b.reset) {
			if !ok && len(l.m) >= l.tracked {
				l.evictOldestLocked()
			}
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

func (l *Limiter) evictOldestLocked() {
	var oldest string
	var at time.Time
	for k, b := range l.m {
		if oldest == "" || b.reset.Before(at) {
			oldest, at = k, b.reset
		}
	}
	delete(l.m, oldest)
}
