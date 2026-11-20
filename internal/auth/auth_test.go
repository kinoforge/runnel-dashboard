package auth

import (
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func TestUsersCheck(t *testing.T) {
	h, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
	u := NewUsers(map[string]string{"admin": string(h)})
	if !u.Check("admin", "correct horse battery") {
		t.Fatal("right password refused")
	}
	if u.Check("admin", "wrong") || u.Check("nobody", "correct horse battery") || u.Check("", "") {
		t.Fatal("wrong login accepted")
	}
	if c, _ := bcrypt.Cost(u.dummy); c != bcrypt.MinCost {
		t.Fatalf("dummy hash cost %d, want the users' cost", c)
	}
}

func TestHash(t *testing.T) {
	if _, err := Hash("short"); !errors.Is(err, ErrPasswordLength) {
		t.Fatalf("short password: %v", err)
	}
	if _, err := Hash(string(make([]byte, 73))); !errors.Is(err, ErrPasswordLength) {
		t.Fatalf("long password: %v", err)
	}
}

func TestSessionsExpire(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s := NewSessions(time.Hour, 10*time.Minute, c.now)
	id, sess := s.Create("admin")
	if len(id) < 40 || sess.CSRF == "" || sess.CSRF == id {
		t.Fatalf("weak session id %q csrf %q", id, sess.CSRF)
	}
	if other, _ := s.Create("admin"); other == id {
		t.Fatal("session ids repeat")
	}
	c.t = c.t.Add(9 * time.Minute)
	if got, ok := s.Get(id); !ok || got.User != "admin" {
		t.Fatal("session lost before idle timeout")
	}
	c.t = c.t.Add(9 * time.Minute)
	if _, ok := s.Get(id); !ok {
		t.Fatal("use did not refresh the idle timer")
	}
	c.t = c.t.Add(11 * time.Minute)
	if _, ok := s.Get(id); ok {
		t.Fatal("idle session still valid")
	}

	id, _ = s.Create("admin")
	for range 7 {
		c.t = c.t.Add(9 * time.Minute)
		s.Get(id)
	}
	if _, ok := s.Get(id); ok {
		t.Fatal("session outlived its ttl")
	}

	id, _ = s.Create("admin")
	s.Delete(id)
	if _, ok := s.Get(id); ok {
		t.Fatal("deleted session still valid")
	}
	if _, ok := s.Get(""); ok {
		t.Fatal("empty id valid")
	}
}

func TestLimiter(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := NewLimiter(3, time.Minute, 100, c.now)
	for range 3 {
		if !l.Allow("ip:1", "user:a") {
			t.Fatal("blocked too early")
		}
		l.Fail("ip:1", "user:a")
	}
	if l.Allow("ip:1", "user:b") || l.Allow("ip:2", "user:a") {
		t.Fatal("not blocked after max failures")
	}
	if !l.Allow("ip:2", "user:b") {
		t.Fatal("unrelated keys blocked")
	}
	c.t = c.t.Add(time.Minute)
	if !l.Allow("ip:1", "user:a") {
		t.Fatal("still blocked after the window")
	}
	l.Fail("ip:3")
	l.Reset("ip:3")
	if len(l.m) != 0 {
		t.Fatalf("expired buckets kept: %d", len(l.m))
	}
}
