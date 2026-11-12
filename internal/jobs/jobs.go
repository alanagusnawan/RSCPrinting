// Package jobs menyediakan registry in-memory untuk melacak status job cetak
// terakhir (dari WebSocket maupun endpoint HTTP lokal).
package jobs

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Status string

const (
	StatusQueued   Status = "queued"
	StatusRunning  Status = "running"
	StatusSuccess  Status = "success"
	StatusError    Status = "error"
	StatusCanceled Status = "canceled"
)

// Job merepresentasikan satu job cetak beserta statusnya.
type Job struct {
	ID         string     `json:"id"`
	Printer    string     `json:"printer"`
	Source     string     `json:"source,omitempty"`
	Status     Status     `json:"status"`
	Error      string     `json:"error,omitempty"`
	ErrorCode  string     `json:"errorCode,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`

	cancel func()
}

var ErrNotFound = errors.New("job not found")

// Registry menyimpan job terakhir dengan batas jumlah (ring buffer).
type Registry struct {
	mu    sync.Mutex
	order []string
	items map[string]*Job
	max   int
}

func NewRegistry(max int) *Registry {
	if max <= 0 {
		max = 200
	}
	return &Registry{items: make(map[string]*Job), max: max}
}

// Create mendaftarkan job baru (status queued). Bila id sudah ada, dikembalikan.
func (r *Registry) Create(id, printer, source string) *Job {
	if id == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if j, ok := r.items[id]; ok {
		return j
	}
	j := &Job{ID: id, Printer: printer, Source: source, Status: StatusQueued, CreatedAt: time.Now()}
	r.items[id] = j
	r.order = append(r.order, id)
	r.evictLocked()
	return j
}

// SetCancel menautkan cancel func agar job bisa dibatalkan via Delete.
func (r *Registry) SetCancel(id string, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if j, ok := r.items[id]; ok {
		j.cancel = cancel
	}
}

func (r *Registry) Start(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if j, ok := r.items[id]; ok {
		now := time.Now()
		j.Status = StatusRunning
		j.StartedAt = &now
	}
}

func (r *Registry) Finish(id string, err error, errorCode string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.items[id]
	if !ok {
		return
	}
	now := time.Now()
	j.FinishedAt = &now
	if err != nil {
		j.Status = StatusError
		j.Error = err.Error()
		j.ErrorCode = errorCode
	} else {
		j.Status = StatusSuccess
	}
}

func (r *Registry) Get(id string) (Job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if j, ok := r.items[id]; ok {
		return *j, true
	}
	return Job{}, false
}

// List mengembalikan job terbaru lebih dulu.
func (r *Registry) List() []Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Job, 0, len(r.order))
	for i := len(r.order) - 1; i >= 0; i-- {
		if j, ok := r.items[r.order[i]]; ok {
			out = append(out, *j)
		}
	}
	return out
}

// Delete membatalkan (bila masih berjalan) dan menghapus job dari registry.
func (r *Registry) Delete(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.items[id]
	if !ok {
		return false
	}
	if j.cancel != nil && (j.Status == StatusQueued || j.Status == StatusRunning) {
		j.cancel()
	}
	delete(r.items, id)
	for i, x := range r.order {
		if x == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return true
}

func (r *Registry) evictLocked() {
	for len(r.order) > r.max {
		id := r.order[0]
		r.order = r.order[1:]
		delete(r.items, id)
	}
}
