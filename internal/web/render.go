package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

//go:embed templates static
var assets embed.FS

type crumb struct{ Name, URL string }

type view struct {
	Title   string
	User    string
	CSRF    string
	Refresh string
	Live    bool
	Self    string
	Crumbs  []crumb
	Error   string
	Data    any
}

type pages struct{ byName map[string]*template.Template }

var funcs = template.FuncMap{
	"bytes":  humanBytes,
	"ago":    ago,
	"when":   when,
	"uptime": uptime,
	"pretty": pretty,
	"count":  func(m map[string]int64, k string) int64 { return m[k] },
	"join":   strings.Join,
	"pct":    pct,
	"list":   func(v ...string) []string { return v },
}

func loadPages() (*pages, error) {
	base, err := template.New("").Funcs(funcs).ParseFS(assets, "templates/layout.html")
	if err != nil {
		return nil, err
	}
	names, err := fs.Glob(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	p := &pages{byName: map[string]*template.Template{}}
	for _, n := range names {
		name := strings.TrimSuffix(strings.TrimPrefix(n, "templates/"), ".html")
		if name == "layout" {
			continue
		}
		t, err := template.Must(base.Clone()).ParseFS(assets, n)
		if err != nil {
			return nil, err
		}
		p.byName[name] = t
	}
	return p, nil
}

func (s *Server) render(w http.ResponseWriter, status int, page string, v view) {
	t, ok := s.pages.byName[page]
	if !ok {
		http.Error(w, "unknown page", http.StatusInternalServerError)
		return
	}
	v.Refresh = s.cfg.Refresh.String()
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", v); err != nil {
		s.log.Error("render failed", "page", page, "err", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (s *Server) flash(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.pages.byName["flash"].ExecuteTemplate(w, "flash", msg); err != nil {
		s.log.Error("render failed", "page", "flash", "err", err)
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	if d < 0 {
		return "in " + short(-d)
	}
	return short(d) + " ago"
}

func uptime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return short(time.Since(t))
}

func short(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func when(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.UTC().Format("2006-01-02 15:04:05Z")
}

func pretty(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if json.Indent(&buf, raw, "", "  ") != nil {
		return string(raw)
	}
	return buf.String()
}

func pct(part, whole int64) int64 {
	if whole <= 0 {
		return 0
	}
	return min(100, part*100/whole)
}
