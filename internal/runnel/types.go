package runnel

import (
	"encoding/json"
	"net/url"
	"strconv"
	"time"
)

var States = []string{"blocked", "waiting", "running", "polling", "completed", "dead", "cancelled", "expired"}

type System struct {
	Version      string       `json:"version"`
	GoVersion    string       `json:"go_version"`
	StartedAt    time.Time    `json:"started_at"`
	Goroutines   int          `json:"goroutines"`
	CPUs         int          `json:"cpus"`
	Memory       Memory       `json:"memory"`
	Storage      *StorageInfo `json:"storage"`
	StorageError string       `json:"storage_error"`
}

type Memory struct {
	Total     int64 `json:"total_bytes"`
	Heap      int64 `json:"heap_bytes"`
	NextGC    int64 `json:"next_gc_bytes"`
	Limit     int64 `json:"limit_bytes"`
	Container int64 `json:"container_limit_bytes"`
	GCCycles  int64 `json:"gc_cycles"`
}

func (m Memory) Cap() int64 {
	switch {
	case m.Limit > 0:
		return m.Limit
	case m.Container > 0:
		return m.Container
	}
	return 0
}

type StorageInfo struct {
	Kind    string           `json:"kind"`
	Bytes   int64            `json:"bytes"`
	Details map[string]int64 `json:"details"`
}

type Namespace struct {
	Name      string       `json:"name"`
	HasSecret bool         `json:"has_callback_secret"`
	Keys      []Key        `json:"keys"`
	CreatedAt time.Time    `json:"created_at"`
	Stats     []QueueStats `json:"stats"`
}

type Key struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Roles     []string  `json:"roles"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type QueueStats struct {
	Queue  string           `json:"queue"`
	Counts map[string]int64 `json:"counts"`
}

type Queue struct {
	Name            string  `json:"name"`
	Paused          bool    `json:"paused"`
	MaxRunning      int     `json:"max_running"`
	GroupMaxRunning int     `json:"group_max_running"`
	MaxQueued       int     `json:"max_queued"`
	GroupMaxQueued  int     `json:"group_max_queued"`
	Rate            float64 `json:"rate"`
	Burst           int     `json:"burst"`
	MaxAttempts     int     `json:"max_attempts"`
	Timeout         string  `json:"timeout"`
	Retention       string  `json:"retention"`
}

type Pool struct {
	Name        string    `json:"name"`
	Slots       int       `json:"slots"`
	GroupSlots  int       `json:"group_slots"`
	Rate        float64   `json:"rate"`
	Burst       int       `json:"burst"`
	Paused      bool      `json:"paused"`
	PausedUntil time.Time `json:"paused_until"`
}

type Schedule struct {
	Name     string `json:"name"`
	Spec     string `json:"spec"`
	Timezone string `json:"timezone"`
	Paused   bool   `json:"paused"`
	Queue    string `json:"queue"`
	Kind     string `json:"kind"`
	Pool     string `json:"pool"`
}

type AttemptError struct {
	Attempt int       `json:"attempt"`
	Queue   string    `json:"queue"`
	Kind    string    `json:"kind"`
	At      time.Time `json:"at"`
	Error   string    `json:"error"`
}

type Job struct {
	ID          string            `json:"id"`
	Queue       string            `json:"queue"`
	Kind        string            `json:"kind"`
	Payload     json.RawMessage   `json:"payload"`
	Priority    int               `json:"priority"`
	Group       string            `json:"group"`
	Pool        string            `json:"pool"`
	State       string            `json:"state"`
	Attempt     int               `json:"attempt"`
	MaxAttempts int               `json:"max_attempts"`
	RunAt       time.Time         `json:"run_at"`
	Deadline    time.Time         `json:"deadline"`
	Worker      string            `json:"worker"`
	LeaseUntil  time.Time         `json:"lease_until"`
	Version     int64             `json:"version"`
	UniqueKey   string            `json:"unique_key"`
	DependsOn   []string          `json:"depends_on"`
	CallbackURL string            `json:"callback_url"`
	Meta        map[string]string `json:"meta"`
	Result      json.RawMessage   `json:"result"`
	Errors      []AttemptError    `json:"errors"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	StartedAt   time.Time         `json:"started_at"`
	FinishedAt  time.Time         `json:"finished_at"`
}

func (j Job) Final() bool {
	switch j.State {
	case "completed", "dead", "cancelled", "expired":
		return true
	}
	return false
}

type Page struct {
	Items []Job  `json:"items"`
	Next  string `json:"next"`
}

type Filter struct {
	Queue, Kind, Group, State, Cursor string
	Limit                             int
}

func (f Filter) values() url.Values {
	q := url.Values{}
	for k, v := range map[string]string{"queue": f.Queue, "kind": f.Kind, "group": f.Group, "state": f.State, "cursor": f.Cursor} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	return q
}

type Event struct {
	Seq   int64     `json:"seq"`
	JobID string    `json:"job_id"`
	Queue string    `json:"queue"`
	Kind  string    `json:"kind"`
	State string    `json:"state"`
	At    time.Time `json:"at"`
}
