package web

import (
	"net/http"

	"github.com/kinoforge/runnel-dashboard/internal/auth"
)

func (s *Server) jobAction(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	c, ok := s.client(w, r)
	if !ok {
		return
	}
	ns, id, action := r.PathValue("ns"), r.PathValue("id"), r.PathValue("action")
	err := c.JobAction(r.Context(), ns, id, action)
	s.done(w, r, sess, action+" job "+id, err)
}

func (s *Server) pauseAction(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	c, ok := s.client(w, r)
	if !ok {
		return
	}
	ns, kind, name, op := r.PathValue("ns"), r.PathValue("kind"), r.PathValue("name"), r.PathValue("op")
	if op != "pause" && op != "resume" {
		http.NotFound(w, r)
		return
	}
	err := c.SetPaused(r.Context(), ns, kind, name, op == "pause")
	s.done(w, r, sess, op+" "+kind+" "+name, err)
}

func (s *Server) done(w http.ResponseWriter, r *http.Request, sess auth.Session, what string, err error) {
	attrs := []any{"user", sess.User, "instance", r.PathValue("inst"), "namespace", r.PathValue("ns"), "action", what}
	if err != nil {
		s.log.Warn("action failed", append(attrs, "err", err)...)
		s.flash(w, "Could not "+what+": "+err.Error())
		return
	}
	s.log.Info("action", attrs...)
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}
