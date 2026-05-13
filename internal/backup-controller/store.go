package backupcontroller

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned by Store implementations when a requested row
// does not exist. Callers compare with errors.Is.
var ErrNotFound = errors.New("backup-controller: not found")

// Store is the persistence interface. The in-memory implementation in
// this file is the test double + the day-one shipping target. The
// Postgres-backed implementation is a follow-up PR; both must satisfy
// the same contract, which is why every method takes a context and
// returns an error.
//
// Method naming follows existing kombify-StackKits convention: short
// verbs (Create, Get, List, Update, Delete) without redundant type
// suffixes when the type is in the receiver chain.
type Store interface {
	// Tenants
	CreateTenant(ctx context.Context, t *Tenant) error
	GetTenant(ctx context.Context, id string) (*Tenant, error)
	ListTenants(ctx context.Context) ([]*Tenant, error)

	// Fleets
	CreateFleet(ctx context.Context, f *Fleet) error
	ListFleetsByTenant(ctx context.Context, tenantID string) ([]*Fleet, error)

	// Hosts
	CreateHost(ctx context.Context, h *Host) error
	GetHost(ctx context.Context, id string) (*Host, error)
	GetHostByToken(ctx context.Context, token string) (*Host, error)
	UpdateHostLastSeen(ctx context.Context, id string, t time.Time) error
	ListHostsByFleet(ctx context.Context, fleetID string) ([]*Host, error)

	// Repos
	CreateRepo(ctx context.Context, r *Repo) error
	GetRepo(ctx context.Context, id string) (*Repo, error)
	ListReposByTenant(ctx context.Context, tenantID string) ([]*Repo, error)

	// Jobs
	CreateJob(ctx context.Context, j *Job) error
	GetJob(ctx context.Context, id string) (*Job, error)
	ListJobsByTenant(ctx context.Context, tenantID string) ([]*Job, error)
	ListAllJobs(ctx context.Context) ([]*Job, error)
	UpdateJobStatus(ctx context.Context, id string, status JobStatus, lastRun *time.Time) error

	// Audit
	AppendAudit(ctx context.Context, e *AuditEntry) error
	ListAuditByTenant(ctx context.Context, tenantID string, limit int) ([]*AuditEntry, error)
}

// =============================================================================
// IN-MEMORY IMPLEMENTATION
//
// Used for tests and as the day-one shipping target until the Postgres
// driver lands. Trivially thread-safe via a single RWMutex; we trade
// fine-grained locking for code clarity at the scale Phase 4 starts at
// (a controller per kombify-Backup-SaaS region, not a global cluster).
// =============================================================================

// NewMemoryStore returns a new in-memory Store. The returned value is
// safe for concurrent use.
func NewMemoryStore() Store {
	return &memoryStore{
		tenants: map[string]*Tenant{},
		fleets:  map[string]*Fleet{},
		hosts:   map[string]*Host{},
		repos:   map[string]*Repo{},
		jobs:    map[string]*Job{},
	}
}

type memoryStore struct {
	mu      sync.RWMutex
	tenants map[string]*Tenant
	fleets  map[string]*Fleet
	hosts   map[string]*Host
	repos   map[string]*Repo
	jobs    map[string]*Job
	audit   []*AuditEntry
}

func (m *memoryStore) CreateTenant(ctx context.Context, t *Tenant) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	m.tenants[t.ID] = t
	return nil
}

func (m *memoryStore) GetTenant(ctx context.Context, id string) (*Tenant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tenants[id]
	if !ok {
		return nil, ErrNotFound
	}
	return t, nil
}

func (m *memoryStore) ListTenants(ctx context.Context) ([]*Tenant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Tenant, 0, len(m.tenants))
	for _, t := range m.tenants {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *memoryStore) CreateFleet(ctx context.Context, f *Fleet) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	m.fleets[f.ID] = f
	return nil
}

func (m *memoryStore) ListFleetsByTenant(ctx context.Context, tenantID string) ([]*Fleet, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []*Fleet{}
	for _, f := range m.fleets {
		if f.TenantID == tenantID {
			out = append(out, f)
		}
	}
	return out, nil
}

func (m *memoryStore) CreateHost(ctx context.Context, h *Host) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if h.ID == "" {
		h.ID = uuid.NewString()
	}
	if h.AgentToken == "" {
		h.AgentToken = uuid.NewString()
	}
	m.hosts[h.ID] = h
	return nil
}

func (m *memoryStore) GetHost(ctx context.Context, id string) (*Host, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	h, ok := m.hosts[id]
	if !ok {
		return nil, ErrNotFound
	}
	return h, nil
}

func (m *memoryStore) GetHostByToken(ctx context.Context, token string) (*Host, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, h := range m.hosts {
		if h.AgentToken == token {
			return h, nil
		}
	}
	return nil, ErrNotFound
}

func (m *memoryStore) UpdateHostLastSeen(ctx context.Context, id string, t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.hosts[id]
	if !ok {
		return ErrNotFound
	}
	h.LastSeen = t
	return nil
}

func (m *memoryStore) ListHostsByFleet(ctx context.Context, fleetID string) ([]*Host, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []*Host{}
	for _, h := range m.hosts {
		if h.FleetID == fleetID {
			out = append(out, h)
		}
	}
	return out, nil
}

func (m *memoryStore) CreateRepo(ctx context.Context, r *Repo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	m.repos[r.ID] = r
	return nil
}

func (m *memoryStore) GetRepo(ctx context.Context, id string) (*Repo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.repos[id]
	if !ok {
		return nil, ErrNotFound
	}
	return r, nil
}

func (m *memoryStore) ListReposByTenant(ctx context.Context, tenantID string) ([]*Repo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []*Repo{}
	for _, r := range m.repos {
		if r.TenantID == tenantID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memoryStore) CreateJob(ctx context.Context, j *Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j.ID == "" {
		j.ID = uuid.NewString()
	}
	if j.Status == "" {
		j.Status = JobStatusPending
	}
	m.jobs[j.ID] = j
	return nil
}

func (m *memoryStore) GetJob(ctx context.Context, id string) (*Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return j, nil
}

func (m *memoryStore) ListJobsByTenant(ctx context.Context, tenantID string) ([]*Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []*Job{}
	for _, j := range m.jobs {
		if j.TenantID == tenantID {
			out = append(out, j)
		}
	}
	return out, nil
}

func (m *memoryStore) ListAllJobs(ctx context.Context) ([]*Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, j)
	}
	return out, nil
}

func (m *memoryStore) UpdateJobStatus(ctx context.Context, id string, status JobStatus, lastRun *time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return ErrNotFound
	}
	j.Status = status
	if lastRun != nil {
		j.LastRun = lastRun
	}
	return nil
}

func (m *memoryStore) AppendAudit(ctx context.Context, e *AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	m.audit = append(m.audit, e)
	return nil
}

func (m *memoryStore) ListAuditByTenant(ctx context.Context, tenantID string, limit int) ([]*AuditEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []*AuditEntry{}
	// Newest first.
	for i := len(m.audit) - 1; i >= 0; i-- {
		if m.audit[i].TenantID != tenantID {
			continue
		}
		out = append(out, m.audit[i])
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}
