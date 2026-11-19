package web

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kinoforge/runnel-dashboard/internal/config"
)

const htmxSHA256 = "e209dda5c8235479f3166defc7750e1dbcd5a5c1808b7792fc2e6733768fb447"

func TestPagesNeedLogin(t *testing.T) {
	r := newRig(t)
	for _, p := range []string{"/", "/i/prod", "/i/prod/ns/shop", "/i/prod/ns/shop/jobs", "/i/prod/ns/shop/jobs/" + jobID} {
		resp, _ := r.do("GET", p, nil, nil)
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
			t.Errorf("%s without a session: %d %s", p, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	resp, _ := r.do("GET", "/", nil, map[string]string{"HX-Request": "true"})
	if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("HX-Redirect") != "/login" {
		t.Fatalf("htmx poll without a session: %d %q", resp.StatusCode, resp.Header.Get("HX-Redirect"))
	}
	if len(r.fake.hits) != 0 {
		t.Fatalf("runnel was called without a session: %v", r.fake.hits)
	}
}

func TestLoginAndLogout(t *testing.T) {
	r := newRig(t)
	resp, _ := r.do("POST", "/login", url.Values{"user": {"admin"}, "password": {"wrong password!"}}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password %d", resp.StatusCode)
	}
	csrf := r.login()
	for _, c := range resp.Cookies() {
		if c.Name == "runnel_dash" && c.Value != "" {
			t.Fatal("failed login set a session cookie")
		}
	}
	resp, _ = r.do("POST", "/logout", url.Values{"csrf": {"bad"}}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("logout with a bad csrf token %d", resp.StatusCode)
	}
	resp, _ = r.do("POST", "/logout", url.Values{"csrf": {csrf}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout %d", resp.StatusCode)
	}
	resp, _ = r.do("GET", "/", nil, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("still signed in after logout: %d", resp.StatusCode)
	}
	if !strings.Contains(r.logs.String(), "login failed") || !strings.Contains(r.logs.String(), "logout") {
		t.Fatalf("logins not logged: %s", r.logs)
	}
}

func TestLoginLimiter(t *testing.T) {
	r := newRig(t, func(c *config.Config) { c.Login.MaxAttempts = 2; c.ClientIPHeader = "X-Forwarded-For" })
	from := func(ip string) map[string]string { return map[string]string{"X-Forwarded-For": ip} }
	for range 2 {
		r.do("POST", "/login", url.Values{"user": {"admin"}, "password": {"nope nope nope"}}, from("203.0.113.1"))
	}
	resp, _ := r.do("POST", "/login", url.Values{"user": {"admin"}, "password": {password}}, from("203.0.113.1"))
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("right password while blocked %d", resp.StatusCode)
	}
	resp, _ = r.do("POST", "/login", url.Values{"user": {"admin"}, "password": {password}}, from("203.0.113.2"))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("one address locked the user out everywhere: %d", resp.StatusCode)
	}
	long := strings.Repeat("a", 64<<10)
	r.do("POST", "/login", url.Values{"user": {long}, "password": {"x"}}, from("203.0.113.3"))
	if strings.Contains(r.logs.String(), long[:1000]) {
		t.Fatal("an oversized user name was logged in full")
	}
}

func TestLoginLimiterPerUser(t *testing.T) {
	r := newRig(t, func(c *config.Config) { c.Login.UserMaxAttempts = 2; c.ClientIPHeader = "X-Forwarded-For" })
	for i := range 2 {
		r.do("POST", "/login", url.Values{"user": {"admin"}, "password": {"nope nope nope"}}, map[string]string{"X-Forwarded-For": fmt.Sprintf("203.0.113.%d", i+1)})
	}
	resp, _ := r.do("POST", "/login", url.Values{"user": {"admin"}, "password": {password}}, map[string]string{"X-Forwarded-For": "203.0.113.9"})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("user limit not applied across addresses: %d", resp.StatusCode)
	}
}

func TestClientIPFromLastHeaderLine(t *testing.T) {
	s := &Server{cfg: &config.Config{ClientIPHeader: "X-Forwarded-For"}}
	req := httptest.NewRequest("POST", "/login", nil)
	req.Header.Add("X-Forwarded-For", "198.51.100.7")
	req.Header.Add("X-Forwarded-For", "203.0.113.5, 192.0.2.1")
	if got := s.clientIP(req); got != "192.0.2.1" {
		t.Fatalf("client ip %q", got)
	}
}

func TestCrossOriginAndCSRF(t *testing.T) {
	r := newRig(t)
	csrf := r.login()
	path := "/i/prod/ns/shop/jobs/" + jobID + "/cancel"
	resp, _ := r.do("POST", path, nil, map[string]string{"X-CSRF-Token": csrf, "Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site post %d", resp.StatusCode)
	}
	resp, _ = r.do("POST", path, nil, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("post without csrf %d", resp.StatusCode)
	}
	resp, _ = r.do("POST", "/login", url.Values{"user": {"admin"}, "password": {password}}, map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site login %d", resp.StatusCode)
	}
	for _, h := range r.fake.hits {
		if strings.HasSuffix(h, "/cancel") {
			t.Fatal("a refused post reached runnel")
		}
	}
}

func TestPagesRenderAndNeverLeakToken(t *testing.T) {
	r := newRig(t)
	r.login()
	want := map[string][]string{
		"/":                                {"v1.2.3", "50.0 MiB of 100.0 MiB", "hybrid"},
		"/i/prod":                          {"shop", "log_bytes"},
		"/i/prod/ns/shop":                  {"emails", "max running 4"},
		"/i/prod/ns/shop?tab=pools":        {`value="2"`, "2 / 5", "pools/batch/resume", "pools/api/pause"},
		"/i/prod/ns/shop?tab=events":       {jobID},
		"/i/prod/ns/shop?tab=keys":         {"k1"},
		"/i/prod/ns/shop?tab=schedules":    {"No schedules"},
		"/i/prod/ns/shop/jobs?state=bogus": {jobID},
		"/i/prod/ns/shop/jobs/" + jobID:    {"&lt;script&gt;alert(1)&lt;/script&gt;", "Cancel"},
	}
	for p, parts := range want {
		resp, body := r.do("GET", p, nil, nil)
		if resp.StatusCode != 200 {
			t.Errorf("%s: %d %s", p, resp.StatusCode, body)
			continue
		}
		for _, s := range parts {
			if !strings.Contains(body, s) {
				t.Errorf("%s: missing %q", p, s)
			}
		}
		if strings.Contains(body, runnelToken) || strings.Contains(body, "<script>alert") {
			t.Errorf("%s: leaks the token or unescaped payload", p)
		}
		if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src 'self'") || resp.Header.Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: security headers %v", p, resp.Header)
		}
	}
}

func TestBadPathsAndInstances(t *testing.T) {
	r := newRig(t)
	r.login()
	if resp, _ := r.do("GET", "/i/nope", nil, nil); resp.StatusCode != 404 {
		t.Fatalf("unknown instance %d", resp.StatusCode)
	}
	if resp, _ := r.do("GET", "/i/prod/ns/..%2f..%2fv1/jobs", nil, nil); resp.StatusCode != 400 && resp.StatusCode != 404 {
		t.Fatalf("traversal %d", resp.StatusCode)
	}
	if resp, body := r.do("GET", "/i/prod/ns/missing", nil, nil); resp.StatusCode != 404 || !strings.Contains(body, "no such thing") {
		t.Fatalf("missing namespace %d", resp.StatusCode)
	}
}

func TestActions(t *testing.T) {
	r := newRig(t)
	csrf := r.login()
	h := map[string]string{"X-CSRF-Token": csrf, "HX-Request": "true"}
	resp, _ := r.do("POST", "/i/prod/ns/shop/jobs/"+jobID+"/cancel", nil, h)
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("HX-Refresh") != "true" {
		t.Fatalf("cancel %d", resp.StatusCode)
	}
	resp, _ = r.do("POST", "/i/prod/ns/shop/pools/api/pause", nil, h)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("pause %d", resp.StatusCode)
	}
	last := r.fake.patches[len(r.fake.patches)-1]
	if last.path != "/v1/ns/shop/pools/api" || last.ifMatch != "" || len(last.body) != 1 || last.body["paused"] != true {
		t.Fatalf("pause sent %+v", last)
	}
	if got := r.fake.settings["/v1/ns/shop/pools/api"]; got["paused"] != true || got["slots"] != 5.0 || got["rate"] != 1.5 {
		t.Fatalf("pool after pause %v", got)
	}
	_, body := r.do("POST", "/i/prod/ns/shop/jobs/"+jobID+"/retry", nil, h)
	if !strings.Contains(body, "Could not retry") {
		t.Fatalf("failed retry not shown: %s", body)
	}
	if resp, _ := r.do("POST", "/i/prod/ns/shop/jobs/"+jobID+"/delete", nil, h); resp.StatusCode == http.StatusNoContent {
		t.Fatal("unknown action accepted")
	}
	if resp, _ := r.do("POST", "/i/prod/ns/shop/schedules/x/pause", nil, h); resp.StatusCode == http.StatusNoContent {
		t.Fatal("pausing a schedule accepted")
	}
	if !strings.Contains(r.logs.String(), "action=\"cancel job "+jobID+"\"") {
		t.Fatalf("action not logged: %s", r.logs)
	}
}

func TestVendoredHTMX(t *testing.T) {
	b, err := assets.ReadFile("static/htmx.min.js")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != htmxSHA256 {
		t.Fatal("static/htmx.min.js is not the vendored htmx 2.0.4")
	}
}
