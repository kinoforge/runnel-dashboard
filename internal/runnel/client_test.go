package runnel

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestSetPausedKeepsOtherFields(t *testing.T) {
	var mu sync.Mutex
	stored := map[string]any{"name": "p", "slots": 5.0, "rate": 2.5, "paused": true, "paused_until": "2030-01-01T00:00:00Z"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/v1/ns/n/pools/p" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Method == http.MethodPut {
			stored = map[string]any{}
			_ = json.NewDecoder(r.Body).Decode(&stored)
		}
		_ = json.NewEncoder(w).Encode(stored)
	}))
	defer srv.Close()
	c := New("t", endpoint(t, srv.URL), time.Second, 1<<20)
	if err := c.SetPaused(context.Background(), "n", "pools", "p", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := stored["paused"]; ok || stored["paused_until"] != nil || stored["slots"] != 5.0 || stored["rate"] != 2.5 {
		t.Fatalf("resume stored %v", stored)
	}
	if err := c.SetPaused(context.Background(), "n", "pools", "p", true); err != nil || stored["paused"] != true || stored["slots"] != 5.0 {
		t.Fatalf("pause stored %v %v", stored, err)
	}
	if err := c.SetPaused(context.Background(), "n", "schedules", "p", true); !errors.Is(err, ErrBadName) {
		t.Fatalf("other kind %v", err)
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
