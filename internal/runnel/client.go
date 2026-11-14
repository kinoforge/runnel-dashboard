package runnel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var segment = regexp.MustCompile(`^[A-Za-z0-9._:~-]{1,200}$`)

var ErrBadName = errors.New("bad name")

type APIError struct {
	Status int
	Title  string
	Detail string
}

func (e *APIError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("runnel %d: %s", e.Status, e.Detail)
	}
	return fmt.Sprintf("runnel %d: %s", e.Status, e.Title)
}

func IsStatus(err error, status int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == status
}

type Client struct {
	Name     string
	base     string
	auth     func(*http.Request)
	http     *http.Client
	maxBytes int64
}

func New(name string, e Endpoint, timeout time.Duration, maxBytes int64) *Client {
	return &Client{Name: name, base: e.Base, auth: e.Authorize, maxBytes: maxBytes,
		http: &http.Client{Timeout: timeout, Transport: e.transport(), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func Path(parts ...string) (string, error) {
	var b strings.Builder
	for _, p := range parts {
		if strings.HasPrefix(p, "/") {
			b.WriteString(p)
			continue
		}
		if !segment.MatchString(p) || p == "." || p == ".." {
			return "", fmt.Errorf("%w: %q", ErrBadName, p)
		}
		b.WriteString("/" + url.PathEscape(p))
	}
	return b.String(), nil
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return err
	}
	c.auth(req)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("runnel %s: %w", c.Name, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > c.maxBytes {
		return fmt.Errorf("runnel %s: answer is larger than %d bytes", c.Name, c.maxBytes)
	}
	if resp.StatusCode >= 300 {
		ae := &APIError{Status: resp.StatusCode, Title: http.StatusText(resp.StatusCode)}
		var p struct{ Title, Detail string }
		if json.Unmarshal(raw, &p) == nil && p.Title != "" {
			ae.Title, ae.Detail = p.Title, p.Detail
		}
		return ae
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, q, nil, out)
}

func (c *Client) Ready(ctx context.Context) error { return c.get(ctx, "/readyz", nil, nil) }

func (c *Client) System(ctx context.Context) (System, error) {
	var s System
	return s, c.get(ctx, "/v1/system", nil, &s)
}

func (c *Client) Namespaces(ctx context.Context) ([]Namespace, error) {
	var out struct{ Items []Namespace }
	return out.Items, c.get(ctx, "/v1/namespaces", nil, &out)
}

func (c *Client) Namespace(ctx context.Context, ns string) (Namespace, error) {
	var n Namespace
	p, err := Path("/v1/namespaces", ns)
	if err != nil {
		return n, err
	}
	return n, c.get(ctx, p, nil, &n)
}

func (c *Client) Jobs(ctx context.Context, ns string, f Filter) (Page, error) {
	var out Page
	p, err := Path("/v1/ns", ns, "jobs")
	if err != nil {
		return out, err
	}
	return out, c.get(ctx, p, f.values(), &out)
}

func (c *Client) Job(ctx context.Context, ns, id string) (Job, error) {
	var j Job
	p, err := Path("/v1/ns", ns, "jobs", id)
	if err != nil {
		return j, err
	}
	return j, c.get(ctx, p, nil, &j)
}

func (c *Client) JobAction(ctx context.Context, ns, id, action string) error {
	if action != "cancel" && action != "retry" {
		return fmt.Errorf("%w: action %q", ErrBadName, action)
	}
	p, err := Path("/v1/ns", ns, "jobs", id, action)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, p, nil, nil, nil)
}

func (c *Client) Queues(ctx context.Context, ns string) (map[string]Queue, error) {
	return items[Queue](ctx, c, ns, "queues")
}

func (c *Client) Pools(ctx context.Context, ns string) (map[string]Pool, error) {
	return items[Pool](ctx, c, ns, "pools")
}

func (c *Client) Schedules(ctx context.Context, ns string) (map[string]Schedule, error) {
	return items[Schedule](ctx, c, ns, "schedules")
}

func items[T any](ctx context.Context, c *Client, ns, kind string) (map[string]T, error) {
	var out struct{ Items map[string]T }
	p, err := Path("/v1/ns", ns, kind)
	if err != nil {
		return nil, err
	}
	return out.Items, c.get(ctx, p, nil, &out)
}

func (c *Client) SetPaused(ctx context.Context, ns, kind, name string, paused bool) error {
	if kind != "queues" && kind != "pools" {
		return fmt.Errorf("%w: kind %q", ErrBadName, kind)
	}
	p, err := Path("/v1/ns", ns, kind, name)
	if err != nil {
		return err
	}
	cfg := map[string]any{}
	if err := c.get(ctx, p, nil, &cfg); err != nil {
		return err
	}
	if paused {
		cfg["paused"] = true
	} else {
		delete(cfg, "paused")
		delete(cfg, "paused_until")
	}
	return c.do(ctx, http.MethodPut, p, nil, cfg, nil)
}

func (c *Client) Events(ctx context.Context, ns string, after int64, limit int) ([]Event, error) {
	var out struct{ Items []Event }
	p, err := Path("/v1/ns", ns, "events")
	if err != nil {
		return nil, err
	}
	q := url.Values{"after": {fmt.Sprint(after)}, "limit": {fmt.Sprint(limit)}}
	return out.Items, c.get(ctx, p, q, &out)
}

func (c *Client) PoolUsage(ctx context.Context, ns string, scanLimit int) (map[string]int, bool, error) {
	if scanLimit <= 0 {
		return nil, false, nil
	}
	used := map[string]int{}
	f := Filter{State: "running", Limit: 500}
	for seen := 0; seen < scanLimit; {
		f.Limit = min(500, scanLimit-seen)
		page, err := c.Jobs(ctx, ns, f)
		if err != nil {
			return nil, false, err
		}
		for _, j := range page.Items {
			if j.Pool != "" {
				used[j.Pool]++
			}
		}
		seen += len(page.Items)
		if page.Next == "" || len(page.Items) == 0 {
			return used, false, nil
		}
		f.Cursor = page.Next
	}
	return used, true, nil
}
