package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"sync"
	"time"
)

type Session struct {
	User    string
	CSRF    string
	Created time.Time
	Seen    time.Time
}

type Sessions struct {
	ttl, idle time.Duration
	now       func() time.Time

	mu sync.Mutex
	m  map[[32]byte]*Session
}

func NewSessions(ttl, idle time.Duration, now func() time.Time) *Sessions {
	if now == nil {
		now = time.Now
	}
	return &Sessions{ttl: ttl, idle: idle, now: now, m: map[[32]byte]*Session{}}
}

func token() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Sessions) Create(user string) (string, Session) {
	id, now := token(), s.now()
	sess := &Session{User: user, CSRF: token(), Created: now, Seen: now}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.m {
		if s.expired(v, now) {
			delete(s.m, k)
		}
	}
	s.m[sha256.Sum256([]byte(id))] = sess
	return id, *sess
}

func (s *Sessions) Get(id string) (Session, bool) {
	if id == "" {
		return Session{}, false
	}
	k, now := sha256.Sum256([]byte(id)), s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[k]
	if !ok {
		return Session{}, false
	}
	if s.expired(v, now) {
		delete(s.m, k)
		return Session{}, false
	}
	v.Seen = now
	return *v, true
}

func (s *Sessions) Delete(id string) {
	s.mu.Lock()
	delete(s.m, sha256.Sum256([]byte(id)))
	s.mu.Unlock()
}

func (s *Sessions) expired(v *Session, now time.Time) bool {
	return now.Sub(v.Created) >= s.ttl || now.Sub(v.Seen) >= s.idle
}
