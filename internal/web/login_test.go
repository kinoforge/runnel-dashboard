package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kinoforge/runnel-dashboard/internal/config"
)

func TestConcurrentLoginsCannotPassTheLimit(t *testing.T) {
	r := newRig(t, func(c *config.Config) { c.Login.MaxAttempts = 2 })
	var checked, blocked atomic.Int32
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			resp, _ := r.do("POST", "/login", url.Values{"user": {"admin"}, "password": {"nope nope nope"}}, nil)
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				checked.Add(1)
			case http.StatusTooManyRequests:
				blocked.Add(1)
			}
		})
	}
	wg.Wait()
	if checked.Load() != 2 || blocked.Load() != 8 {
		t.Fatalf("%d passwords checked and %d blocked, want 2 and 8", checked.Load(), blocked.Load())
	}
}

func TestLoginEndsTheOldSession(t *testing.T) {
	r := newRig(t)
	r.login()
	u, _ := url.Parse(r.srv.URL)
	var old string
	for _, c := range r.client.Jar.Cookies(u) {
		if c.Name == "runnel_dash" {
			old = c.Value
		}
	}
	r.login()
	stale := &http.Client{CheckRedirect: r.client.CheckRedirect}
	req, _ := http.NewRequest("GET", r.srv.URL+"/", nil)
	req.AddCookie(&http.Cookie{Name: "runnel_dash", Value: old})
	resp, err := stale.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("the session from before the new login still works: %d", resp.StatusCode)
	}
}

func TestOversizedFormsAre413(t *testing.T) {
	r := newRig(t, func(c *config.Config) { c.HTTP.MaxBodyBytes = 1024 })
	big := url.Values{"user": {"admin"}, "password": {strings.Repeat("x", 4096)}}
	if resp, _ := r.do("POST", "/login", big, nil); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized login %d", resp.StatusCode)
	}
	csrf := r.login()
	path := "/i/prod/ns/shop/jobs/" + jobID + "/cancel"
	body := url.Values{"csrf": {csrf}, "pad": {strings.Repeat("x", 4096)}}
	if resp, _ := r.do("POST", path, body, nil); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized action %d", resp.StatusCode)
	}
}

func TestClientIPHeaderOnlyFromTrustedProxies(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	s := &Server{cfg: &config.Config{ClientIPHeader: "X-Forwarded-For", TrustedPrefixes: trusted}}
	cases := []struct {
		remote, header, want string
	}{
		{"10.1.2.3:4000", "203.0.113.5", "203.0.113.5"},
		{"198.51.100.9:4000", "203.0.113.5", "198.51.100.9"},
		{"10.1.2.3:4000", "not an address", "10.1.2.3"},
		{"10.1.2.3:4000", "::ffff:203.0.113.5", "203.0.113.5"},
	}
	for _, c := range cases {
		req := httptest.NewRequest("POST", "/login", nil)
		req.RemoteAddr = c.remote
		req.Header.Set("X-Forwarded-For", c.header)
		if got := s.clientIP(req); got != c.want {
			t.Errorf("from %s with %q: %q, want %q", c.remote, c.header, got, c.want)
		}
	}
}

func TestEventsPageClampsANegativeCursor(t *testing.T) {
	r := newRig(t)
	r.login()
	resp, page := r.do("GET", "/i/prod/ns/shop?tab=events&after=-5", nil, nil)
	if resp.StatusCode != http.StatusOK || strings.Contains(page, ">First</a>") {
		t.Fatalf("events with a negative cursor: %d", resp.StatusCode)
	}
}
