package jobs

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/rizalahmaddd/raw-print-agent/internal/spool"
)

const (
	// A repeated job_id inside this window returns the first job instead of
	// printing again (double clicks, retried requests).
	DedupWindow = 10 * time.Minute
	keepFor     = 24 * time.Hour
	keepMax     = 500
)

type Job struct {
	ID        string         `json:"job_id"`
	Printer   string         `json:"printer"`
	DocName   string         `json:"doc_name"`
	Bytes     int            `json:"bytes"`
	Copies    int            `json:"copies"`
	SpoolID   uint32         `json:"spool_job_id,omitempty"`
	State     spool.JobState `json:"state"`
	Detail    string         `json:"detail,omitempty"`
	Origin    string         `json:"origin,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type Request struct {
	ID      string
	Printer string
	DocName string
	Data    []byte
	Copies  int
	Origin  string
}

type Manager struct {
	spool spool.Spooler
	now   func() time.Time
	mu    sync.Mutex
	jobs  map[string]*Job
	// serializes submissions so two jobs never interleave on the same printer
	submitMu sync.Mutex
}

func NewManager(s spool.Spooler) *Manager {
	return &Manager{spool: s, now: time.Now, jobs: map[string]*Job{}}
}

var ErrInvalid = errors.New("invalid job")

// Submit returns (job, duplicate, error). A failed job is recorded too, so the
// caller can show the reason when polling.
func (m *Manager) Submit(r Request) (Job, bool, error) {
	if r.ID == "" || len(r.ID) > 128 {
		return Job{}, false, errors.Join(ErrInvalid, errors.New("job_id is required (max 128 chars)"))
	}
	if len(r.Data) == 0 {
		return Job{}, false, errors.Join(ErrInvalid, errors.New("payload is empty"))
	}
	if r.Copies <= 0 {
		r.Copies = 1
	}
	if r.Copies > 10 {
		return Job{}, false, errors.Join(ErrInvalid, errors.New("copies must be 1..10"))
	}
	if r.DocName == "" {
		r.DocName = r.ID
	}

	m.submitMu.Lock()
	defer m.submitMu.Unlock()

	m.mu.Lock()
	m.pruneLocked()
	if j, ok := m.jobs[r.ID]; ok && m.now().Sub(j.CreatedAt) < DedupWindow && j.State != spool.StateError {
		cp := *j
		m.mu.Unlock()
		return cp, true, nil
	}
	now := m.now()
	j := &Job{ID: r.ID, Printer: r.Printer, DocName: r.DocName, Bytes: len(r.Data), Copies: r.Copies,
		State: spool.StateQueued, Origin: r.Origin, CreatedAt: now, UpdatedAt: now}
	m.jobs[r.ID] = j
	m.mu.Unlock()

	payload := r.Data
	if r.Copies > 1 {
		payload = make([]byte, 0, len(r.Data)*r.Copies)
		for i := 0; i < r.Copies; i++ {
			payload = append(payload, r.Data...)
		}
	}
	id, err := m.spool.Submit(r.Printer, r.DocName, payload)

	m.mu.Lock()
	defer m.mu.Unlock()
	j.SpoolID = id
	j.UpdatedAt = m.now()
	if err != nil {
		j.State = spool.StateError
		j.Detail = err.Error()
		return *j, false, err
	}
	return *j, false, nil
}

// Get refreshes the state from the spooler while the job is still pending.
func (m *Manager) Get(id string) (Job, bool) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return Job{}, false
	}
	pending := j.SpoolID != 0 && (j.State == spool.StateQueued || j.State == spool.StatePrinting)
	printer, spoolID := j.Printer, j.SpoolID
	m.mu.Unlock()

	if pending {
		info, err := m.spool.Job(printer, spoolID)
		m.mu.Lock()
		if err == nil {
			j.State, j.Detail = info.State, info.Detail
		} else {
			j.Detail = err.Error()
		}
		j.UpdatedAt = m.now()
		m.mu.Unlock()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return *j, true
}

func (m *Manager) Recent(limit int) []Job {
	m.mu.Lock()
	out := make([]Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, *j)
	}
	m.mu.Unlock()
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	for i, j := range out {
		if j.State == spool.StateQueued || j.State == spool.StatePrinting {
			if fresh, ok := m.Get(j.ID); ok {
				out[i] = fresh
			}
		}
	}
	return out
}

func (m *Manager) pruneLocked() {
	cutoff := m.now().Add(-keepFor)
	for id, j := range m.jobs {
		if j.CreatedAt.Before(cutoff) {
			delete(m.jobs, id)
		}
	}
	if len(m.jobs) <= keepMax {
		return
	}
	all := make([]*Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		all = append(all, j)
	}
	sort.Slice(all, func(a, b int) bool { return all[a].CreatedAt.Before(all[b].CreatedAt) })
	for _, j := range all[:len(all)-keepMax] {
		delete(m.jobs, j.ID)
	}
}
