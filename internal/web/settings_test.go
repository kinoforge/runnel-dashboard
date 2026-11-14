package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSettingsPage(t *testing.T) {
	r := newRig(t)
	csrf := r.login()
	resp, page := r.do("GET", "/i/prod/ns/shop/pools/api", nil, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, `name="etag" value="&#34;v0&#34;"`) || !strings.Contains(page, `value="1.5"`) || !strings.Contains(page, "set by the app that owns the pool") {
		t.Fatalf("pool settings %d: %s", resp.StatusCode, page)
	}
	if resp, page := r.do("GET", "/i/prod/ns/shop/queues/fresh", nil, nil); resp.StatusCode != http.StatusOK || !strings.Contains(page, "No settings yet") || !strings.Contains(page, `name="etag" value=""`) {
		t.Fatalf("new queue settings %d: %s", resp.StatusCode, page)
	}
	for path, want := range map[string]int{
		"/i/prod/ns/shop/schedules/x": http.StatusNotFound,
		"/i/prod/ns/shop/pools/a%20b": http.StatusBadRequest,
		"/i/nope/ns/shop/pools/api":   http.StatusNotFound,
	} {
		if resp, _ := r.do("GET", path, nil, nil); resp.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, resp.StatusCode, want)
		}
	}
	if resp, _ := r.do("POST", "/i/prod/ns/shop/pools/api", url.Values{"rate": {"3"}, "etag": {`"v0"`}}, map[string]string{"HX-Request": "true"}); resp.StatusCode != http.StatusForbidden || len(r.fake.patches) != 0 {
		t.Fatalf("save without csrf: %d", resp.StatusCode)
	}
	if resp, _ := r.do("POST", "/i/prod/ns/shop/pools/api", url.Values{"rate": {"3"}, "etag": {`"v0"`}}, map[string]string{"X-CSRF-Token": csrf, "Origin": "https://evil.example"}); resp.StatusCode != http.StatusForbidden || len(r.fake.patches) != 0 {
		t.Fatalf("cross-site save: %d", resp.StatusCode)
	}
}

func TestSettingsSave(t *testing.T) {
	r := newRig(t)
	h := map[string]string{"X-CSRF-Token": r.login(), "HX-Request": "true"}
	before := time.Now()
	resp, _ := r.do("POST", "/i/prod/ns/shop/pools/api", url.Values{"rate": {"3"}, "burst": {"2"}, "pause_for": {"30m"}, "etag": {`"v0"`}}, h)
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("HX-Refresh") != "true" {
		t.Fatalf("save %d", resp.StatusCode)
	}
	last := r.fake.patches[len(r.fake.patches)-1]
	until, _ := time.Parse(time.RFC3339, last.body["paused_until"].(string))
	if last.ifMatch != `"v0"` || last.media != "application/merge-patch+json" || last.body["rate"] != 3.0 || last.body["burst"] != 2.0 || until.Before(before.Add(29*time.Minute)) || until.After(time.Now().Add(31*time.Minute)) {
		t.Fatalf("save sent %+v", last)
	}
	if _, has := last.body["slots"]; has {
		t.Fatalf("save touched slots: %+v", last.body)
	}
	if got := r.fake.settings["/v1/ns/shop/pools/api"]; got["slots"] != 5.0 || got["rate"] != 3.0 {
		t.Fatalf("pool after save %v", got)
	}
	_, body := r.do("POST", "/i/prod/ns/shop/pools/api", url.Values{"rate": {"4"}, "etag": {`"v0"`}}, h)
	if !strings.Contains(body, "Changed since you opened it") {
		t.Fatalf("stale save: %s", body)
	}
	n := len(r.fake.patches)
	for field, value := range map[string]string{"rate": "abc", "burst": "-1", "pause_for": "soon"} {
		_, body := r.do("POST", "/i/prod/ns/shop/pools/api", url.Values{field: {value}, "etag": {`"v1"`}}, h)
		if !strings.Contains(body, "must be") || len(r.fake.patches) != n {
			t.Fatalf("bad %s accepted: %s", field, body)
		}
	}
	if resp, _ := r.do("POST", "/i/prod/ns/shop/pools/api", url.Values{"rate": {""}, "burst": {""}, "etag": {`"v1"`}}, h); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("clear %d", resp.StatusCode)
	}
	last = r.fake.patches[len(r.fake.patches)-1]
	if rate, has := last.body["rate"]; !has || rate != nil || len(last.body) != 2 {
		t.Fatalf("clear sent %+v", last.body)
	}
	if resp, _ := r.do("POST", "/i/prod/ns/shop/queues/fresh", url.Values{"rate": {"1"}, "etag": {""}}, h); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("create %d", resp.StatusCode)
	}
	if last := r.fake.patches[len(r.fake.patches)-1]; last.ifNone != "*" || last.ifMatch != "" {
		t.Fatalf("create sent %+v", last)
	}
	if !strings.Contains(r.logs.String(), `action="change pools api"`) {
		t.Fatalf("save not logged: %s", r.logs)
	}
}
