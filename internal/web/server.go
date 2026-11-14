package web

import (
	"context"
	"crypto/subtle"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/kinoforge/runnel-dashboard/internal/auth"
	"github.com/kinoforge/runnel-dashboard/internal/config"
	"github.com/kinoforge/runnel-dashboard/internal/runnel"
)

const csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

type ctxKey struct{}

type Server struct {
	cfg       *config.Config
	log       *slog.Logger
	users     *auth.Users
	sessions  *auth.Sessions
	limiter   *auth.Limiter
	clients   map[string]*runnel.Client
	order     []string
	pages     *pages
	mux       *http.ServeMux
	cookie    string
	crossSite *http.CrossOriginProtection
}

func New(cfg *config.Config, log *slog.Logger, now func() time.Time) (*Server, error) {
	hashes := map[string]string{}
	for _, u := range cfg.Users {
		hashes[u.Name] = u.PasswordHash
	}
	p, err := loadPages()
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, log: log, users: auth.NewUsers(hashes), pages: p, mux: http.NewServeMux(),
		sessions: auth.NewSessions(cfg.SessionTTL, cfg.IdleTimeout, now),
		limiter:  auth.NewLimiter(cfg.Login.MaxAttempts, cfg.Login.Window, now),
		clients:  map[string]*runnel.Client{}, cookie: "runnel_dash", crossSite: http.NewCrossOriginProtection()}
	if cfg.Secure() {
		s.cookie = "__Host-runnel_dash"
	}
	if cfg.PublicURL != "" {
		if err := s.crossSite.AddTrustedOrigin(strings.TrimSuffix(cfg.PublicURL, "/")); err != nil {
			return nil, err
		}
	}
	for _, in := range cfg.Instances {
		s.clients[in.Name] = runnel.New(in.Name, in.Endpoint, in.Timeout, cfg.MaxResponseBytes)
		s.order = append(s.order, in.Name)
	}
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	static, _ := fs.Sub(assets, "static")
	files := http.StripPrefix("/static/", http.FileServerFS(static))
	s.mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		files.ServeHTTP(w, r)
	}))
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	s.mux.HandleFunc("GET /login", s.loginPage)
	s.mux.HandleFunc("POST /login", s.login)
	s.mux.Handle("POST /logout", s.authed(s.logout))
	s.mux.Handle("GET /{$}", s.authed(s.overview))
	s.mux.Handle("GET /i/{inst}", s.authed(s.instance))
	s.mux.Handle("GET /i/{inst}/ns/{ns}", s.authed(s.namespace))
	s.mux.Handle("GET /i/{inst}/ns/{ns}/jobs", s.authed(s.jobs))
	s.mux.Handle("GET /i/{inst}/ns/{ns}/jobs/{id}", s.authed(s.job))
	s.mux.Handle("POST /i/{inst}/ns/{ns}/jobs/{id}/{action}", s.authed(s.jobAction))
	s.mux.Handle("POST /i/{inst}/ns/{ns}/{kind}/{name}/{op}", s.authed(s.pauseAction))
	for _, kind := range []string{"queues", "pools"} {
		s.mux.Handle("GET /i/{inst}/ns/{ns}/"+kind+"/{name}", s.authed(s.settings(kind)))
		s.mux.Handle("POST /i/{inst}/ns/{ns}/"+kind+"/{name}", s.authed(s.saveSettings(kind)))
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	h.Set("Cache-Control", "no-store")
	if s.cfg.Secure() {
		h.Set("Strict-Transport-Security", "max-age=31536000")
	}
	if err := s.crossSite.Check(r); err != nil {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.HTTP.MaxBodyBytes)
	s.mux.ServeHTTP(w, r)
}

type authedHandler func(w http.ResponseWriter, r *http.Request, sess auth.Session)

func (s *Server) authed(h authedHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(s.cookie)
		var sess auth.Session
		ok := err == nil
		if ok {
			sess, ok = s.sessions.Get(c.Value)
		}
		if !ok {
			if r.Header.Get("HX-Request") != "" {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if r.Method != http.MethodGet && !validCSRF(r, sess.CSRF) {
			http.Error(w, "missing or wrong CSRF token", http.StatusForbidden)
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, c.Value)), sess)
	})
}

func validCSRF(r *http.Request, want string) bool {
	got := r.Header.Get("X-CSRF-Token")
	if got == "" {
		got = r.PostFormValue("csrf")
	}
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.ClientIPHeader != "" {
		if v := r.Header.Get(s.cfg.ClientIPHeader); v != "" {
			parts := strings.Split(v, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) client(w http.ResponseWriter, r *http.Request) (*runnel.Client, bool) {
	c, ok := s.clients[r.PathValue("inst")]
	if !ok {
		http.NotFound(w, r)
	}
	return c, ok
}
