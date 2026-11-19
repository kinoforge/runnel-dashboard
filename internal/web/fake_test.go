package web

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/kinoforge/runnel-dashboard/internal/config"
	"github.com/kinoforge/runnel-dashboard/internal/runnel"
)

const (
	runnelToken = "secret-admin-password-value"
	password    = "correct horse battery"
	jobID       = "01900000-0000-7000-8000-000000000001"
)

type patchCall struct {
	path, ifMatch, ifNone, media string
	body                         map[string]any
}

type fakeRunnel struct {
	mu       sync.Mutex
	settings map[string]map[string]any
	versions map[string]int
	patches  []patchCall
	hits     []string
}

func newFake() *fakeRunnel {
	return &fakeRunnel{
		settings: map[string]map[string]any{
			"/v1/ns/shop/queues/emails": {"name": "emails", "max_running": 4.0},
			"/v1/ns/shop/pools/api":     {"name": "api", "slots": 5.0, "rate": 1.5},
		},
		versions: map[string]int{},
	}
}

func (f *fakeRunnel) tag(path string) string { return fmt.Sprintf(`"v%d"`, f.versions[path]) }

func (f *fakeRunnel) setting(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	if !strings.HasPrefix(path, "/v1/ns/shop/queues/") && !strings.HasPrefix(path, "/v1/ns/shop/pools/") {
		return false
	}
	cur, found := f.settings[path]
	switch r.Method {
	case http.MethodGet:
		if !found {
			return false
		}
	case http.MethodPatch:
		call := patchCall{path: path, ifMatch: r.Header.Get("If-Match"), ifNone: r.Header.Get("If-None-Match"), media: r.Header.Get("Content-Type")}
		_ = json.NewDecoder(r.Body).Decode(&call.body)
		f.patches = append(f.patches, call)
		if (call.ifMatch != "" && (!found || call.ifMatch != f.tag(path))) || (call.ifNone == "*" && found) {
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = w.Write([]byte(`{"title":"precondition failed"}`))
			return true
		}
		if !found {
			cur = map[string]any{"name": path[strings.LastIndexByte(path, '/')+1:]}
			f.settings[path] = cur
		}
		for k, v := range call.body {
			if v == nil {
				delete(cur, k)
			} else {
				cur[k] = v
			}
		}
		f.versions[path]++
	default:
		return false
	}
	w.Header().Set("ETag", f.tag(path))
	_ = json.NewEncoder(w).Encode(cur)
	return true
}

func (f *fakeRunnel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits = append(f.hits, r.Method+" "+r.URL.Path)
	if r.URL.Path == "/readyz" {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return
	}
	if user, pass, ok := r.BasicAuth(); !ok || user != "admin" || pass != runnelToken {
		w.WriteHeader(401)
		return
	}
	body := map[string]string{
		"GET /v1/system":                             `{"version":"v1.2.3","go_version":"go1.27.4","started_at":"2026-11-14T10:00:00Z","memory":{"total_bytes":52428800,"heap_bytes":1048576,"limit_bytes":104857600},"storage":{"kind":"hybrid","bytes":2048,"details":{"log_bytes":1024}}}`,
		"GET /v1/namespaces":                         `{"items":[{"name":"shop","keys":[]}]}`,
		"GET /v1/namespaces/shop":                    `{"name":"shop","keys":[{"id":"k1","roles":["admin"]}],"stats":[{"queue":"emails","counts":{"waiting":3,"running":2}}]}`,
		"GET /v1/ns/shop/queues":                     `{"items":{"emails":{"name":"emails","max_running":4}}}`,
		"GET /v1/ns/shop/pools":                      `{"items":{"api":{"name":"api","slots":5},"batch":{"name":"batch","paused_until":"2999-01-01T00:00:00Z"}}}`,
		"GET /v1/ns/shop/schedules":                  `{"items":{}}`,
		"GET /v1/ns/shop/events":                     `{"items":[{"seq":1,"job_id":"` + jobID + `","queue":"emails","kind":"send","state":"waiting"}]}`,
		"GET /v1/ns/shop/jobs/" + jobID:              `{"id":"` + jobID + `","queue":"emails","kind":"send","state":"running","payload":{"html":"<script>alert(1)</script>"}}`,
		"POST /v1/ns/shop/jobs/" + jobID + "/cancel": `{}`,
	}
	key := r.Method + " " + r.URL.Path
	if key == "GET /v1/ns/shop/jobs" {
		if r.URL.Query().Get("state") == "running" {
			_, _ = w.Write([]byte(`{"items":[{"id":"a","pool":"api"},{"id":"b","pool":"api"}]}`))
		} else {
			_, _ = w.Write([]byte(`{"items":[{"id":"` + jobID + `","queue":"emails","kind":"send","state":"running"}],"next":""}`))
		}
		return
	}
	if f.setting(w, r) {
		return
	}
	if b, ok := body[key]; ok {
		_, _ = w.Write([]byte(b))
		return
	}
	w.WriteHeader(404)
	_, _ = w.Write([]byte(`{"title":"not found","detail":"no such thing"}`))
}

type rig struct {
	t      *testing.T
	fake   *fakeRunnel
	srv    *httptest.Server
	client *http.Client
	logs   *strings.Builder
}

func newRig(t *testing.T, mutate ...func(*config.Config)) *rig {
	t.Helper()
	fake := newFake()
	rs := httptest.NewServer(fake)
	t.Cleanup(rs.Close)
	h, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	cfg := config.Defaults
	no := false
	cfg.SecureCookies = &no
	cfg.Users = []config.User{{Name: "admin", PasswordHash: string(h)}}
	ep, err := runnel.ParseURL("http://admin:" + runnelToken + "@" + strings.TrimPrefix(rs.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Instances = []config.Instance{{Name: "prod", Endpoint: ep, Timeout: 2 * time.Second}}
	for _, m := range mutate {
		m(&cfg)
	}
	logs := &strings.Builder{}
	s, err := New(&cfg, slog.New(slog.NewTextHandler(logs, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &rig{t: t, fake: fake, srv: srv, client: c, logs: logs}
}

type reply struct {
	StatusCode int
	Header     http.Header
	cookies    []*http.Cookie
}

func (r reply) Cookies() []*http.Cookie { return r.cookies }

func (r *rig) do(method, path string, form url.Values, headers map[string]string) (reply, string) {
	r.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, r.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", r.srv.URL)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return reply{StatusCode: resp.StatusCode, Header: resp.Header, cookies: resp.Cookies()}, string(b)
}

func (r *rig) login() string {
	r.t.Helper()
	resp, _ := r.do("POST", "/login", url.Values{"user": {"admin"}, "password": {password}}, nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		r.t.Fatalf("login %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, page := r.do("GET", "/", nil, nil)
	i := strings.Index(page, `name="csrf" value="`)
	if i < 0 {
		r.t.Fatal("no csrf token on the page")
	}
	rest := page[i+len(`name="csrf" value="`):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		r.t.Fatal("csrf value not closed")
	}
	return rest[:end]
}
