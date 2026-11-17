// Package tasks runs bounded background operations without persisting credentials.
package tasks

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Task struct {
	ID        string    `json:"id"`
	Owner     string    `json:"owner"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	Progress  float64   `json:"progress"`
	Logs      []string  `json:"logs"`
	Result    any       `json:"result,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type entry struct {
	task   Task
	cancel context.CancelFunc
}
type Manager struct {
	mu      sync.Mutex
	entries map[string]*entry
	slots   chan struct{}
}
type Reporter struct {
	manager *Manager
	id      string
}
type publicError struct{ message string }

func (e publicError) Error() string    { return e.message }
func PublicError(message string) error { return publicError{message} }

var ownerPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

func ValidOwner(owner string) bool { return ownerPattern.MatchString(owner) }

var Default = New(2)

func New(concurrency int) *Manager {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Manager{entries: map[string]*entry{}, slots: make(chan struct{}, concurrency)}
}

func clone(t Task) Task { t.Logs = append([]string{}, t.Logs...); return t }
func terminal(state string) bool {
	return state == "succeeded" || state == "failed" || state == "cancelled"
}

func (m *Manager) Start(owner, kind, title string, run func(context.Context, *Reporter) (any, error)) Task {
	now := time.Now().UTC()
	t := Task{ID: uuid.NewString(), Owner: owner, Kind: kind, Title: title, State: "queued", Logs: []string{}, CreatedAt: now}
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	var active, owned int
	for _, e := range m.entries {
		if !terminal(e.task.State) {
			active++
			if e.task.Owner == owner {
				owned++
			}
		}
	}
	if !ValidOwner(owner) || run == nil {
		t.State = "failed"
		t.Error = "A valid browser identifier and operation are required."
	} else if active >= 64 || owned >= 16 {
		t.State = "failed"
		t.Error = "The operation queue is full. Wait for an earlier task to finish."
	}
	m.entries[t.ID] = &entry{task: t, cancel: cancel}
	// Retain recent completed tasks for inspection without allowing an unbounded log store.
	if len(m.entries) > 256 {
		var completed []Task
		for _, e := range m.entries {
			if terminal(e.task.State) && e.task.ID != t.ID {
				completed = append(completed, e.task)
			}
		}
		sort.Slice(completed, func(i, j int) bool { return completed[i].CreatedAt.Before(completed[j].CreatedAt) })
		for _, old := range completed {
			if len(m.entries) <= 256 {
				break
			}
			delete(m.entries, old.ID)
		}
	}
	m.mu.Unlock()
	if terminal(t.State) {
		cancel()
		return clone(t)
	}
	go m.execute(ctx, t.ID, run)
	return clone(t)
}

func (m *Manager) execute(ctx context.Context, id string, run func(context.Context, *Reporter) (any, error)) {
	defer func() {
		if recover() != nil {
			m.finish(id, nil, PublicError("The operation stopped unexpectedly. Retry with a valid input."), ctx)
		}
	}()
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	case <-ctx.Done():
		m.finish(id, nil, ctx.Err(), ctx)
		return
	}
	m.mu.Lock()
	e := m.entries[id]
	if e != nil {
		e.task.State = "running"
	}
	m.mu.Unlock()
	if ctx.Err() != nil {
		m.finish(id, nil, ctx.Err(), ctx)
		return
	}
	result, err := run(ctx, &Reporter{manager: m, id: id})
	m.finish(id, result, err, ctx)
}

func (m *Manager) finish(id string, result any, err error, ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[id]
	if e == nil {
		return
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		e.task.State = "cancelled"
		e.task.Result = nil
		e.task.Error = "Operation cancelled."
	} else if err != nil {
		e.task.State = "failed"
		e.task.Result = nil
		e.task.Error = "Operation failed. See the progress log for details."
		var safe publicError
		if errors.As(err, &safe) {
			e.task.Error = safe.message
		}
	} else {
		e.task.State = "succeeded"
		e.task.Progress = 100
		e.task.Result = result
	}
	e.cancel()
	e.cancel = nil
}

func (r *Reporter) Log(message string) {
	message = strings.Map(func(c rune) rune {
		if c < 32 && c != '\t' && c != '\n' {
			return -1
		}
		return c
	}, message)
	if len(message) > 2048 {
		message = message[:2048] + "…"
	}
	r.manager.mu.Lock()
	defer r.manager.mu.Unlock()
	e := r.manager.entries[r.id]
	if e == nil || terminal(e.task.State) {
		return
	}
	line := fmt.Sprintf("%s %s", time.Now().UTC().Format(time.RFC3339), message)
	e.task.Logs = append(e.task.Logs, line)
	if len(e.task.Logs) > 256 {
		e.task.Logs = e.task.Logs[len(e.task.Logs)-256:]
	}
}
func (r *Reporter) Progress(progress float64) {
	if math.IsNaN(progress) || math.IsInf(progress, 0) {
		return
	}
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	r.manager.mu.Lock()
	defer r.manager.mu.Unlock()
	if e := r.manager.entries[r.id]; e != nil && !terminal(e.task.State) {
		e.task.Progress = progress
	}
}
func (m *Manager) Get(owner, id string) (Task, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[id]
	if !ok || owner != e.task.Owner {
		return Task{}, false
	}
	return clone(e.task), true
}
func (m *Manager) List(owner string) []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := []Task{}
	for _, e := range m.entries {
		if e.task.Owner == owner {
			result = append(result, clone(e.task))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result
}
func (m *Manager) Cancel(owner, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[id]
	if !ok || e.task.Owner != owner {
		return false
	}
	if e.cancel != nil {
		e.cancel()
	}
	return true
}
