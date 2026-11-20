package web

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"sync"

	"github.com/kinoforge/runnel-dashboard/internal/auth"
	"github.com/kinoforge/runnel-dashboard/internal/runnel"
)

const fanOut = 8

func (s *Server) page(r *http.Request, sess auth.Session, title string, live bool, crumbs ...crumb) view {
	return view{Title: title, User: sess.User, CSRF: sess.CSRF, Live: live, Self: r.URL.RequestURI(), Crumbs: crumbs}
}

func (s *Server) fail(w http.ResponseWriter, v view, err error) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, runnel.ErrBadName):
		status = http.StatusBadRequest
	case runnel.IsStatus(err, http.StatusNotFound):
		status = http.StatusNotFound
	}
	v.Error, v.Live = err.Error(), false
	s.render(w, status, "error", v)
}

type instanceCard struct {
	Name       string
	Ready      bool
	Error      string
	Note       string
	System     *runnel.System
	Namespaces int
}

func (s *Server) card(ctx context.Context, c *runnel.Client) instanceCard {
	card := instanceCard{Name: c.Name}
	if err := c.Ready(ctx); err != nil {
		card.Error = err.Error()
	} else {
		card.Ready = true
	}
	sys, err := c.System(ctx)
	switch {
	case err == nil:
		card.System = &sys
	case runnel.IsStatus(err, http.StatusNotFound):
		card.Note = "This Runnel has no /v1/system, so memory and storage are not shown. Upgrade it to see them."
	case card.Error == "":
		card.Error = err.Error()
	}
	if ns, err := c.Namespaces(ctx); err == nil {
		card.Namespaces = len(ns)
	}
	return card
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	cards := make([]instanceCard, len(s.order))
	parallel(len(s.order), func(i int) { cards[i] = s.card(r.Context(), s.clients[s.order[i]]) })
	v := s.page(r, sess, "Instances", true)
	v.Data = cards
	s.render(w, http.StatusOK, "overview", v)
}

type nsRow struct {
	runnel.Namespace
	Totals map[string]int64
	Error  string
}

func (s *Server) instance(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	c, ok := s.client(w, r)
	if !ok {
		return
	}
	v := s.page(r, sess, c.Name, true, crumb{c.Name, ""})
	card := s.card(r.Context(), c)
	list, err := c.Namespaces(r.Context())
	if err != nil {
		s.fail(w, v, err)
		return
	}
	rows := make([]nsRow, len(list))
	parallel(len(list), func(i int) {
		rows[i] = nsRow{Namespace: list[i], Totals: map[string]int64{}}
		n, err := c.Namespace(r.Context(), list[i].Name)
		if err != nil {
			rows[i].Error = err.Error()
			return
		}
		for _, st := range n.Stats {
			for k, n := range st.Counts {
				rows[i].Totals[k] += n
			}
		}
	})
	v.Data = map[string]any{"Card": card, "Namespaces": rows, "States": runnel.States}
	s.render(w, http.StatusOK, "instance", v)
}

type queueRow struct {
	Name   string
	Counts map[string]int64
	Config *runnel.Queue
}

type poolRow struct {
	runnel.Pool
	Used int
	Held bool
}

func (s *Server) namespace(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	c, ok := s.client(w, r)
	if !ok {
		return
	}
	ns, tab := r.PathValue("ns"), r.URL.Query().Get("tab")
	if !slices.Contains([]string{"queues", "pools", "schedules", "events", "keys"}, tab) {
		tab = "queues"
	}
	v := s.page(r, sess, ns, true, crumb{c.Name, "/i/" + c.Name}, crumb{ns, ""})
	data := map[string]any{"Inst": c.Name, "NS": ns, "Tab": tab, "States": runnel.States}
	var err error
	switch tab {
	case "queues":
		data["Queues"], err = s.queueRows(r.Context(), c, ns)
	case "pools":
		data["Pools"], data["Truncated"], err = s.poolRows(r.Context(), c, ns)
	case "schedules":
		var all map[string]runnel.Schedule
		all, err = c.Schedules(r.Context(), ns)
		data["Schedules"] = sortedValues(all)
	case "events":
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		after = max(after, 0)
		var evs []runnel.Event
		evs, err = c.Events(r.Context(), ns, after, s.cfg.PageSize)
		data["Events"], data["After"] = evs, after
		if len(evs) == s.cfg.PageSize {
			data["Next"] = evs[len(evs)-1].Seq
		}
	case "keys":
		var n runnel.Namespace
		n, err = c.Namespace(r.Context(), ns)
		data["Info"] = n
	}
	if err != nil {
		s.fail(w, v, err)
		return
	}
	v.Data = data
	s.render(w, http.StatusOK, "namespace", v)
}

func (s *Server) queueRows(ctx context.Context, c *runnel.Client, ns string) ([]queueRow, error) {
	n, err := c.Namespace(ctx, ns)
	if err != nil {
		return nil, err
	}
	cfgs, err := c.Queues(ctx, ns)
	if err != nil {
		return nil, err
	}
	rows := map[string]*queueRow{}
	for _, st := range n.Stats {
		rows[st.Queue] = &queueRow{Name: st.Queue, Counts: st.Counts}
	}
	for name, q := range cfgs {
		if rows[name] == nil {
			rows[name] = &queueRow{Name: name, Counts: map[string]int64{}}
		}
		rows[name].Config = &q
	}
	out := make([]queueRow, 0, len(rows))
	for _, name := range slices.Sorted(maps.Keys(rows)) {
		out = append(out, *rows[name])
	}
	return out, nil
}

func (s *Server) poolRows(ctx context.Context, c *runnel.Client, ns string) ([]poolRow, bool, error) {
	cfgs, err := c.Pools(ctx, ns)
	if err != nil {
		return nil, false, err
	}
	used, truncated, err := c.PoolUsage(ctx, ns, s.cfg.RunningScanLimit)
	if err != nil {
		return nil, false, err
	}
	out := make([]poolRow, 0, len(cfgs))
	for _, p := range sortedValues(cfgs) {
		out = append(out, poolRow{Pool: p, Used: used[p.Name], Held: p.Paused || s.now().Before(p.PausedUntil)})
	}
	return out, truncated, nil
}

func (s *Server) jobs(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	c, ok := s.client(w, r)
	if !ok {
		return
	}
	ns, q := r.PathValue("ns"), r.URL.Query()
	f := runnel.Filter{Queue: q.Get("queue"), Kind: q.Get("kind"), Group: q.Get("group"), State: q.Get("state"), Cursor: q.Get("cursor"), Limit: s.cfg.PageSize}
	if f.State != "" && !slices.Contains(runnel.States, f.State) {
		f.State = ""
	}
	v := s.page(r, sess, "Jobs", f.Cursor == "", crumb{c.Name, "/i/" + c.Name}, crumb{ns, "/i/" + c.Name + "/ns/" + ns}, crumb{"jobs", ""})
	page, err := c.Jobs(r.Context(), ns, f)
	if err != nil {
		s.fail(w, v, err)
		return
	}
	v.Data = map[string]any{"Inst": c.Name, "NS": ns, "Filter": f, "Page": page, "States": runnel.States}
	s.render(w, http.StatusOK, "jobs", v)
}

func (s *Server) job(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	c, ok := s.client(w, r)
	if !ok {
		return
	}
	ns, id := r.PathValue("ns"), r.PathValue("id")
	v := s.page(r, sess, "Job", true, crumb{c.Name, "/i/" + c.Name}, crumb{ns, "/i/" + c.Name + "/ns/" + ns},
		crumb{"jobs", "/i/" + c.Name + "/ns/" + ns + "/jobs"}, crumb{id, ""})
	j, err := c.Job(r.Context(), ns, id)
	if err != nil {
		s.fail(w, v, err)
		return
	}
	v.Live = !j.Final()
	v.Data = map[string]any{"Inst": c.Name, "NS": ns, "Job": j}
	s.render(w, http.StatusOK, "job", v)
}

func sortedValues[T any](m map[string]T) []T {
	out := make([]T, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		out = append(out, m[k])
	}
	return out
}

func parallel(n int, fn func(i int)) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, fanOut)
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			fn(i)
		}()
	}
	wg.Wait()
}
