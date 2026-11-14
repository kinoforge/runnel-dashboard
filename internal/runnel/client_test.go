package runnel

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPathRejectsTraversal(t *testing.T) {
	for _, bad := range []string{"..", ".", "a/b", "", "a b", "a%2fb", strings.Repeat("x", 201)} {
		if _, err := Path("/v1/ns", bad); !errors.Is(err, ErrBadName) {
			t.Errorf("Path accepted %q", bad)
		}
	}
	if p, err := Path("/v1/ns", "a.b", "jobs", "0190-ab"); err != nil || p != "/v1/ns/a.b/jobs/0190-ab" {
		t.Fatalf("Path %q %v", p, err)
	}
}

func endpoint(t *testing.T, raw string) Endpoint {
	t.Helper()
	e, err := ParseURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestClientSendsCredentialsAndParsesProblems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "admin" || pass != "secret-pass" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"title":"not found","detail":"unknown namespace"}`))
	}))
	defer srv.Close()
	c := New("t", endpoint(t, "http://admin:secret-pass@"+strings.TrimPrefix(srv.URL, "http://")), time.Second, 1<<20)
	_, err := c.Namespace(context.Background(), "x")
	if !IsStatus(err, 404) || !strings.Contains(err.Error(), "unknown namespace") {
		t.Fatalf("error %v", err)
	}
	srv.Close()
	if _, err := c.Namespace(context.Background(), "x"); err == nil || strings.Contains(err.Error(), "secret-pass") {
		t.Fatalf("error leaks the password: %v", err)
	}
}

func TestClientTrustsGivenCA(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New("t", endpoint(t, srv.URL), time.Second, 1<<20).Namespaces(context.Background()); err == nil {
		t.Fatal("trusted an unknown CA")
	}
	if _, err := New("t", endpoint(t, srv.URL+"?ca="+ca), time.Second, 1<<20).Namespaces(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestClientCapsAnswerSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[` + strings.Repeat(`{"name":"n"},`, 100) + `{"name":"n"}]}`))
	}))
	defer srv.Close()
	if _, err := New("t", endpoint(t, srv.URL), time.Second, 64).Namespaces(context.Background()); err == nil {
		t.Fatal("oversized answer accepted")
	}
}

type sent struct {
	method, path, media, ifMatch, ifNone, body string
}

func recorder(t *testing.T, answer func(w http.ResponseWriter)) (*Client, *[]sent) {
	t.Helper()
	var mu sync.Mutex
	var got []sent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		b, _ := io.ReadAll(r.Body)
		got = append(got, sent{r.Method, r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("If-Match"), r.Header.Get("If-None-Match"), string(b)})
		answer(w)
	}))
	t.Cleanup(srv.Close)
	return New("t", endpoint(t, srv.URL), time.Second, 1<<20), &got
}

func TestSetPausedSendsOnePatch(t *testing.T) {
	c, got := recorder(t, func(w http.ResponseWriter) { _, _ = w.Write([]byte(`{}`)) })
	ctx := context.Background()
	for _, step := range []struct {
		kind   string
		paused bool
		body   string
	}{
		{"pools", true, `{"paused":true}`},
		{"pools", false, `{"paused":false,"paused_until":null}`},
		{"queues", true, `{"paused":true}`},
		{"queues", false, `{"paused":false}`},
	} {
		*got = nil
		if err := c.SetPaused(ctx, "n", step.kind, "p", step.paused); err != nil {
			t.Fatal(err)
		}
		want := sent{"PATCH", "/v1/ns/n/" + step.kind + "/p", "application/merge-patch+json", "", "", step.body}
		if len(*got) != 1 || (*got)[0] != want {
			t.Fatalf("%s paused=%v sent %+v", step.kind, step.paused, *got)
		}
	}
	if err := c.SetPaused(ctx, "n", "schedules", "p", true); !errors.Is(err, ErrBadName) {
		t.Fatalf("other kind %v", err)
	}
}

func TestSettingAndPreconditions(t *testing.T) {
	c, got := recorder(t, func(w http.ResponseWriter) {
		w.Header().Set("ETag", `"v7"`)
		_, _ = w.Write([]byte(`{"name":"p","slots":5,"rate":1.5}`))
	})
	ctx := context.Background()
	var p Pool
	tag, err := c.Setting(ctx, "n", "pools", "p", &p)
	if err != nil || tag != `"v7"` || p.Slots != 5 || p.Rate != 1.5 {
		t.Fatalf("setting %q %+v %v", tag, p, err)
	}
	if err := c.Patch(ctx, "n", "pools", "p", map[string]any{"rate": nil}, Match{ETag: tag}); err != nil {
		t.Fatal(err)
	}
	if err := c.Patch(ctx, "n", "queues", "q", map[string]any{"rate": 2}, Match{Absent: true}); err != nil {
		t.Fatal(err)
	}
	want := []sent{
		{"GET", "/v1/ns/n/pools/p", "", "", "", ""},
		{"PATCH", "/v1/ns/n/pools/p", "application/merge-patch+json", `"v7"`, "", `{"rate":null}`},
		{"PATCH", "/v1/ns/n/queues/q", "application/merge-patch+json", "", "*", `{"rate":2}`},
	}
	if !slices.Equal(*got, want) {
		t.Fatalf("sent %+v", *got)
	}
	bare, _ := recorder(t, func(w http.ResponseWriter) { _, _ = w.Write([]byte(`{}`)) })
	if _, err := bare.Setting(ctx, "n", "pools", "p", &p); err == nil || !strings.Contains(err.Error(), "0.0.3-beta.2") {
		t.Fatalf("missing ETag: %v", err)
	}
}

func TestPoolUsagePagesAndStops(t *testing.T) {
	const total = 1203
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != "running" {
			t.Errorf("query %s", r.URL.RawQuery)
		}
		var from int
		_, _ = fmt.Sscan(r.URL.Query().Get("cursor"), &from)
		var limit int
		_, _ = fmt.Sscan(r.URL.Query().Get("limit"), &limit)
		page := Page{}
		for i := from; i < min(total, from+limit); i++ {
			page.Items = append(page.Items, Job{ID: fmt.Sprint(i), Pool: []string{"a", "b", ""}[i%3]})
		}
		if from+limit < total {
			page.Next = fmt.Sprint(from + limit)
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer srv.Close()
	c := New("t", endpoint(t, srv.URL), time.Second, 1<<20)
	used, cut, err := c.PoolUsage(context.Background(), "n", 10000)
	if err != nil || cut || used["a"] != 401 || used["b"] != 401 || len(used) != 2 {
		t.Fatalf("usage %v cut %v err %v", used, cut, err)
	}
	used, cut, err = c.PoolUsage(context.Background(), "n", 600)
	if err != nil || !cut || used["a"]+used["b"] != 400 {
		t.Fatalf("capped usage %v cut %v err %v", used, cut, err)
	}
	if used, cut, err = c.PoolUsage(context.Background(), "n", 0); used != nil || cut || err != nil {
		t.Fatal("scan limit 0 still scanned")
	}
}
