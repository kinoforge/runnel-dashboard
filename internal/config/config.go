package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
	"golang.org/x/crypto/bcrypt"

	"github.com/kinoforge/runnel-dashboard/internal/runnel"
)

type Config struct {
	Listen           string        `yaml:"listen"`
	PublicURL        string        `yaml:"public_url"`
	SecureCookies    *bool         `yaml:"secure_cookies"`
	TLSCert          string        `yaml:"tls_cert"`
	TLSKey           string        `yaml:"tls_key"`
	ClientIPHeader   string        `yaml:"client_ip_header"`
	SessionTTL       time.Duration `yaml:"session_ttl"`
	IdleTimeout      time.Duration `yaml:"idle_timeout"`
	Refresh          time.Duration `yaml:"refresh"`
	RunningScanLimit int           `yaml:"running_scan_limit"`
	PageSize         int           `yaml:"page_size"`
	MaxResponseBytes int64         `yaml:"max_response_bytes"`
	Login            Login         `yaml:"login"`
	HTTP             HTTP          `yaml:"http"`
	Users            []User        `yaml:"users"`
	Instances        []Instance    `yaml:"instances"`
}

type Login struct {
	MaxAttempts     int           `yaml:"max_attempts"`
	UserMaxAttempts int           `yaml:"user_max_attempts"`
	Window          time.Duration `yaml:"window"`
}

type HTTP struct {
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout"`
	ReadTimeout       time.Duration `yaml:"read_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout"`
	IdleTimeout       time.Duration `yaml:"idle_timeout"`
	ShutdownTimeout   time.Duration `yaml:"shutdown_timeout"`
	MaxBodyBytes      int64         `yaml:"max_body_bytes"`
}

type User struct {
	Name         string `yaml:"name"`
	PasswordHash string `yaml:"password_hash"`
}

type Instance struct {
	Name     string          `yaml:"name"`
	URL      string          `yaml:"url"`
	Timeout  time.Duration   `yaml:"timeout"`
	Endpoint runnel.Endpoint `yaml:"-"`
}

var Defaults = Config{
	Listen: ":8710", SessionTTL: 12 * time.Hour, IdleTimeout: time.Hour, Refresh: 5 * time.Second,
	RunningScanLimit: 10000, PageSize: 50, MaxResponseBytes: 16 << 20,
	Login: Login{MaxAttempts: 5, Window: 15 * time.Minute},
	HTTP: HTTP{ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second,
		IdleTimeout: 2 * time.Minute, ShutdownTimeout: 15 * time.Second, MaxBodyBytes: 64 << 10},
}

const DefaultInstanceTimeout = 10 * time.Second

var namePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b, os.Getenv)
}

func Parse(b []byte, env func(string) string) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	for i := range c.Instances {
		if err := c.Instances[i].connect(env); err != nil {
			return nil, err
		}
	}
	return &c, nil
}

func (c *Config) Secure() bool { return *c.SecureCookies }

func (c *Config) applyDefaults() {
	d := Defaults
	pick(&c.Listen, d.Listen)
	pick(&c.SessionTTL, d.SessionTTL)
	pick(&c.IdleTimeout, d.IdleTimeout)
	pick(&c.Refresh, d.Refresh)
	pick(&c.RunningScanLimit, d.RunningScanLimit)
	pick(&c.PageSize, d.PageSize)
	pick(&c.MaxResponseBytes, d.MaxResponseBytes)
	pick(&c.Login.MaxAttempts, d.Login.MaxAttempts)
	pick(&c.Login.Window, d.Login.Window)
	pick(&c.HTTP.ReadHeaderTimeout, d.HTTP.ReadHeaderTimeout)
	pick(&c.HTTP.ReadTimeout, d.HTTP.ReadTimeout)
	pick(&c.HTTP.WriteTimeout, d.HTTP.WriteTimeout)
	pick(&c.HTTP.IdleTimeout, d.HTTP.IdleTimeout)
	pick(&c.HTTP.ShutdownTimeout, d.HTTP.ShutdownTimeout)
	pick(&c.HTTP.MaxBodyBytes, d.HTTP.MaxBodyBytes)
	if c.SecureCookies == nil {
		yes := true
		c.SecureCookies = &yes
	}
	for i := range c.Instances {
		pick(&c.Instances[i].Timeout, DefaultInstanceTimeout)
	}
}

func pick[T comparable](v *T, fallback T) {
	var zero T
	if *v == zero {
		*v = fallback
	}
}

func (c *Config) validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf("config: "+format, args...)) }
	if c.SessionTTL < 0 || c.IdleTimeout < 0 || c.Refresh < time.Second || c.Login.Window < 0 {
		bad("durations must be positive and refresh at least 1s")
	}
	if c.RunningScanLimit < 0 || c.PageSize < 1 || c.PageSize > 500 || c.Login.MaxAttempts < 1 || c.MaxResponseBytes < 1 {
		bad("running_scan_limit must be 0 or more, page_size 1 to 500, login.max_attempts and max_response_bytes at least 1")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		bad("tls_cert and tls_key go together")
	}
	if c.PublicURL != "" {
		if u, err := url.Parse(c.PublicURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" && u.Path != "/" {
			bad("public_url must be an origin like https://dash.example.com")
		}
	}
	if len(c.Users) == 0 {
		bad("at least one user is needed")
	}
	users := map[string]bool{}
	for _, u := range c.Users {
		switch {
		case !namePattern.MatchString(u.Name):
			bad("user %q: name must match %s", u.Name, namePattern)
		case users[u.Name]:
			bad("user %q is listed twice", u.Name)
		case !validHash(u.PasswordHash):
			bad("user %q: password_hash must be a bcrypt hash from `runnel-dashboard hash`", u.Name)
		}
		users[u.Name] = true
	}
	if len(c.Instances) == 0 {
		bad("at least one instance is needed")
	}
	names := map[string]bool{}
	for _, in := range c.Instances {
		if err := in.validate(); err != nil {
			errs = append(errs, err)
		}
		if names[in.Name] {
			bad("instance %q is listed twice", in.Name)
		}
		names[in.Name] = true
	}
	return errors.Join(errs...)
}

func (in Instance) validate() error {
	if !namePattern.MatchString(in.Name) {
		return fmt.Errorf("config: instance %q: name must match %s", in.Name, namePattern)
	}
	if in.URL == "" {
		return fmt.Errorf("config: instance %q: url is required", in.Name)
	}
	if in.Timeout < 0 {
		return fmt.Errorf("config: instance %q: timeout must be positive", in.Name)
	}
	return nil
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func (in *Instance) connect(env func(string) string) error {
	raw, err := expand(in.URL, env)
	if err != nil {
		return fmt.Errorf("config: instance %q: %w", in.Name, err)
	}
	if in.Endpoint, err = runnel.ParseURL(raw); err != nil {
		return fmt.Errorf("config: instance %q: %w", in.Name, err)
	}
	return nil
}

func expand(raw string, env func(string) string) (string, error) {
	if m := envRef.FindStringSubmatch(raw); m != nil && m[0] == raw {
		if v := env(m[1]); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("environment variable %s is empty", m[1])
	}
	var missing []string
	out := envRef.ReplaceAllStringFunc(raw, func(ref string) string {
		name := ref[2 : len(ref)-1]
		v := env(name)
		if v == "" {
			missing = append(missing, name)
		}
		return escape(v)
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("environment variable %s is empty", strings.Join(missing, ", "))
	}
	return out, nil
}

func escape(s string) string {
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("-._~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func validHash(h string) bool {
	_, err := bcrypt.Cost([]byte(h))
	return err == nil
}
