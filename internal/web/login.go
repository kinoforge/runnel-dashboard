package web

import (
	"net/http"

	"github.com/kinoforge/runnel-dashboard/internal/auth"
)

const (
	maxUserName = 64
	maxPassword = 72
)

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(s.cookie); err == nil {
		if _, ok := s.sessions.Get(c.Value); ok {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	}
	s.render(w, http.StatusOK, "login", view{Title: "Sign in"})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	name, password := r.PostFormValue("user"), r.PostFormValue("password")
	ip := s.clientIP(r)
	sane := len(name) <= maxUserName && len(password) <= maxPassword
	if !sane {
		name = name[:min(len(name), maxUserName)]
	}
	if !s.limiter.Allow("ip:"+ip) || (s.userLimit != nil && !s.userLimit.Allow(name)) {
		s.log.Warn("login blocked", "user", name, "ip", ip)
		s.render(w, http.StatusTooManyRequests, "login", view{Title: "Sign in", Error: "Too many failed attempts. Try again later."})
		return
	}
	if !sane || !s.users.Check(name, password) {
		s.limiter.Fail("ip:" + ip)
		if s.userLimit != nil && sane {
			s.userLimit.Fail(name)
		}
		s.log.Warn("login failed", "user", name, "ip", ip)
		s.render(w, http.StatusUnauthorized, "login", view{Title: "Sign in", Error: "Wrong user name or password."})
		return
	}
	s.limiter.Reset("ip:" + ip)
	if s.userLimit != nil {
		s.userLimit.Reset(name)
	}
	id, _ := s.sessions.Create(name)
	http.SetCookie(w, &http.Cookie{Name: s.cookie, Value: id, Path: "/", MaxAge: int(s.cfg.SessionTTL.Seconds()),
		HttpOnly: true, Secure: s.cfg.Secure(), SameSite: http.SameSiteStrictMode})
	s.log.Info("login", "user", name, "ip", ip)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	if id, ok := r.Context().Value(ctxKey{}).(string); ok {
		s.sessions.Delete(id)
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.cfg.Secure(), SameSite: http.SameSiteStrictMode})
	s.log.Info("logout", "user", sess.User, "ip", s.clientIP(r))
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
