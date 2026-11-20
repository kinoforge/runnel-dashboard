package web

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kinoforge/runnel-dashboard/internal/auth"
	"github.com/kinoforge/runnel-dashboard/internal/runnel"
)

func (s *Server) settings(kind string) authedHandler {
	return func(w http.ResponseWriter, r *http.Request, sess auth.Session) {
		c, ok := s.client(w, r)
		if !ok {
			return
		}
		ns, name := r.PathValue("ns"), r.PathValue("name")
		base := "/i/" + c.Name + "/ns/" + ns
		v := s.page(r, sess, name, false, crumb{c.Name, "/i/" + c.Name}, crumb{ns, base + "?tab=" + kind}, crumb{name, ""})
		data := map[string]any{"Inst": c.Name, "NS": ns, "Kind": kind, "Name": name}
		var cfg any = &runnel.Queue{Name: name}
		if kind == "pools" {
			cfg = &runnel.Pool{Name: name}
		}
		tag, err := c.Setting(r.Context(), ns, kind, name, cfg)
		switch {
		case runnel.IsStatus(err, http.StatusNotFound):
			data["New"] = true
		case err != nil:
			s.fail(w, v, err)
			return
		}
		data["ETag"], data["Config"] = tag, cfg
		v.Data = data
		s.render(w, http.StatusOK, "settings", v)
	}
}

func (s *Server) saveSettings(kind string) authedHandler {
	return func(w http.ResponseWriter, r *http.Request, sess auth.Session) {
		c, ok := s.client(w, r)
		if !ok {
			return
		}
		ns, name := r.PathValue("ns"), r.PathValue("name")
		fields, err := settingFields(r, kind, s.now())
		if err != nil {
			s.flash(w, err.Error())
			return
		}
		m := runnel.Match{ETag: r.PostFormValue("etag")}
		m.Absent = m.ETag == ""
		err = c.Patch(r.Context(), ns, kind, name, fields, m)
		if runnel.IsStatus(err, http.StatusPreconditionFailed) {
			s.flash(w, "Changed since you opened it. Reload and try again.")
			return
		}
		s.done(w, r, sess, "change "+kind+" "+name, err)
	}
}

func settingFields(r *http.Request, kind string, now time.Time) (map[string]any, error) {
	fields := map[string]any{"rate": nil, "burst": nil}
	if v := strings.TrimSpace(r.PostFormValue("rate")); v != "" {
		rate, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(rate) || rate < 0 || rate > 1e6 {
			return nil, errors.New("rate must be a number from 0 to 1000000")
		}
		fields["rate"] = rate
	}
	if v := strings.TrimSpace(r.PostFormValue("burst")); v != "" {
		burst, err := strconv.Atoi(v)
		if err != nil || burst < 0 {
			return nil, errors.New("burst must be a whole number of 0 or more")
		}
		fields["burst"] = burst
	}
	if v := strings.TrimSpace(r.PostFormValue("pause_for")); kind == "pools" && v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, errors.New("pause for must be a duration such as 30m or 2h")
		}
		fields["paused_until"] = now.Add(d).UTC().Format(time.RFC3339)
	}
	return fields, nil
}
