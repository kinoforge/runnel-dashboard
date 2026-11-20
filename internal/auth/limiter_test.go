package auth

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimiterTakeIsAtomic(t *testing.T) {
	l := NewLimiter(3, time.Minute, 100, nil)
	var ok atomic.Int32
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if l.Take("ip:1") {
				ok.Add(1)
			}
		})
	}
	wg.Wait()
	if ok.Load() != 3 {
		t.Fatalf("%d of 50 concurrent attempts got through, want 3", ok.Load())
	}
}

func TestLimiterTracksAtMostItsCapAndDropsTheOldest(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := NewLimiter(1, time.Hour, 3, c.now)
	for i := range 5 {
		l.Fail(fmt.Sprintf("ip:%d", i))
		c.t = c.t.Add(time.Second)
	}
	if n := len(l.m); n != 3 {
		t.Fatalf("tracking %d keys, want 3", n)
	}
	if !l.Allow("ip:0") || !l.Allow("ip:1") {
		t.Fatal("the oldest keys were not the ones dropped")
	}
	if l.Allow("ip:4") || l.Allow("ip:3") || l.Allow("ip:2") {
		t.Fatal("a recent key was dropped")
	}
}

func TestLimiterSweepsExpiredKeys(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := NewLimiter(1, time.Minute, 100, c.now)
	for i := range 10 {
		l.Fail(fmt.Sprintf("ip:%d", i))
	}
	c.t = c.t.Add(2 * time.Minute)
	l.Fail("ip:new")
	if n := len(l.m); n != 1 {
		t.Fatalf("%d keys after the window passed, want 1", n)
	}
}
