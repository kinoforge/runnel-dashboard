package config

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func hashOf(t *testing.T) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(h)
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func doc(t *testing.T, instances string) []byte {
	return []byte("users:\n  - name: admin\n    password_hash: \"" + hashOf(t) + "\"\ninstances:\n" + instances)
}

func TestParseAppliesDefaultsAndConnects(t *testing.T) {
	b := doc(t, "  - {name: prod, url: \"https://admin:${PROD_PASS}@r.example:8700\"}\n  - {name: dev, url: \"http://localhost:8700\", timeout: 3s}\n  - {name: whole, url: \"${WHOLE}\"}\n")
	c, err := Parse(b, env(map[string]string{"PROD_PASS": "p@ss:/word?#", "WHOLE": "https://ops:pw@w.example"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8710" || c.SessionTTL != 12*time.Hour || c.Refresh != 5*time.Second || !c.Secure() || c.Login.MaxAttempts != 5 {
		t.Fatalf("defaults not applied: %+v", c)
	}
	prod, dev, whole := c.Instances[0].Endpoint, c.Instances[1].Endpoint, c.Instances[2].Endpoint
	req, _ := http.NewRequest("GET", prod.Base, nil)
	prod.Authorize(req)
	if user, pass, ok := req.BasicAuth(); prod.Base != "https://r.example:8700" || !ok || user != "admin" || pass != "p@ss:/word?#" {
		t.Fatalf("prod %v %q %q", prod, user, pass)
	}
	if dev.Base != "http://localhost:8700" || dev.HasCredentials() || whole.Base != "https://w.example" || !whole.HasCredentials() {
		t.Fatalf("dev %v, whole %v", dev, whole)
	}
	if c.Instances[0].Timeout != DefaultInstanceTimeout || c.Instances[1].Timeout != 3*time.Second {
		t.Fatalf("timeouts %v %v", c.Instances[0].Timeout, c.Instances[1].Timeout)
	}
}

func TestParseKeepsExplicitInsecureCookies(t *testing.T) {
	b := append([]byte("secure_cookies: false\n"), doc(t, "  - {name: a, url: \"http://a:1\"}\n")...)
	c, err := Parse(b, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Secure() {
		t.Fatal("secure_cookies false was overridden")
	}
}

func TestErrorsHideThePassword(t *testing.T) {
	for _, url := range []string{"https://admin:hunter2-secret@a:port", "ftp://admin:hunter2-secret@a", "https://admin:hunter2-secret@a?bad=1"} {
		_, err := Parse(doc(t, "  - {name: a, url: \""+url+"\"}\n"), env(nil))
		if err == nil || strings.Contains(err.Error(), "hunter2-secret") {
			t.Errorf("%s: %v", url, err)
		}
	}
}

func TestParseRejects(t *testing.T) {
	good := hashOf(t)
	cases := map[string]string{
		"unknown field":   "nope: 1\nusers: [{name: a, password_hash: \"" + good + "\"}]\ninstances: [{name: a, url: \"http://a:1\"}]",
		"no users":        "instances: [{name: a, url: \"http://a:1\"}]",
		"plain password":  "users: [{name: a, password_hash: hunter2}]\ninstances: [{name: a, url: \"http://a:1\"}]",
		"duplicate user":  "users: [{name: a, password_hash: \"" + good + "\"}, {name: a, password_hash: \"" + good + "\"}]\ninstances: [{name: a, url: \"http://a:1\"}]",
		"no instances":    "users: [{name: a, password_hash: \"" + good + "\"}]",
		"bad url":         "users: [{name: a, password_hash: \"" + good + "\"}]\ninstances: [{name: a, url: \"ftp://a\"}]",
		"no url":          "users: [{name: a, password_hash: \"" + good + "\"}]\ninstances: [{name: a}]",
		"tls on http":     "users: [{name: a, password_hash: \"" + good + "\"}]\ninstances: [{name: a, url: \"http://a:1?ca=/c.pem\"}]",
		"empty variable":  "users: [{name: a, password_hash: \"" + good + "\"}]\ninstances: [{name: a, url: \"http://u:${EMPTY}@a:1\"}]",
		"missing file":    "users: [{name: a, password_hash: \"" + good + "\"}]\ninstances: [{name: a, url: \"https://a:1?ca=/missing.pem\"}]",
		"duplicate inst":  "users: [{name: a, password_hash: \"" + good + "\"}]\ninstances: [{name: a, url: \"http://a:1\"}, {name: a, url: \"http://b:1\"}]",
		"bad inst name":   "users: [{name: a, password_hash: \"" + good + "\"}]\ninstances: [{name: \"a/b\", url: \"http://a:1\"}]",
		"fast refresh":    "refresh: 100ms\nusers: [{name: a, password_hash: \"" + good + "\"}]\ninstances: [{name: a, url: \"http://a:1\"}]",
		"half tls":        "tls_cert: /c\nusers: [{name: a, password_hash: \"" + good + "\"}]\ninstances: [{name: a, url: \"http://a:1\"}]",
		"public url path": "public_url: https://d.example/x\nusers: [{name: a, password_hash: \"" + good + "\"}]\ninstances: [{name: a, url: \"http://a:1\"}]",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(body), env(nil))
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.HasPrefix(err.Error(), "config: ") {
				t.Fatalf("error %q lacks the config prefix", err)
			}
		})
	}
}
