package backupcontroller

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAuditLog_AppendWritesToStoreAndSlog verifies the dual-sink
// design: AuditLog.Append must (a) persist via the Store and (b)
// mirror the same event onto the configured slog handler. The slog
// mirror is the operational read path that SIEM tails consume; if it
// regresses, the audit log silently disappears from those pipelines
// even though the Store row is intact.
func TestAuditLog_AppendWritesToStoreAndSlog(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	a := &AuditLog{Store: store, Logger: slog.New(handler)}

	require.NoError(t, a.Append(ctx, &AuditEntry{
		TenantID: "t1",
		Actor:    "operator:apikey",
		Action:   "tenant.create",
		Resource: "tenant:t1",
	}))

	// Store side.
	entries, err := store.ListAuditByTenant(ctx, "t1", 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "tenant.create", entries[0].Action)

	// slog side — the line is JSON, but we don't need to parse it; we
	// only need to verify the structured fields made it through.
	out := buf.String()
	assert.Contains(t, out, `"action":"tenant.create"`)
	assert.Contains(t, out, `"actor":"operator:apikey"`)
	assert.Contains(t, out, `"resource":"tenant:t1"`)
	// Single line per Append (slog default).
	assert.Equal(t, 1, strings.Count(strings.TrimSpace(out), "\n")+1)
}

// TestAuditLog_StoreErrorPropagates is the security-relevant case:
// when the Store fails (DB down, disk full), the caller MUST see the
// error so the in-flight HTTP request can fail closed. A silent
// success would mean state-changing operations happen without an
// audit row, which is the exact scenario the audit log is meant to
// prevent.
func TestAuditLog_StoreErrorPropagates(t *testing.T) {
	a := &AuditLog{Store: &failingStore{}}
	err := a.Append(context.Background(), &AuditEntry{Action: "test"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, errInjected))
}

// TestAuditLog_NilLoggerFallsBack confirms the AuditLog behaves
// sensibly without an explicit logger — uses slog.Default() rather
// than panicking. A nil-logger panic on first audit write would be a
// bad first-day-on-the-job experience.
func TestAuditLog_NilLoggerFallsBack(t *testing.T) {
	store := NewMemoryStore()
	a := &AuditLog{Store: store /* Logger intentionally nil */}
	require.NotPanics(t, func() {
		_ = a.Append(context.Background(), &AuditEntry{TenantID: "t", Action: "x", Resource: "y", Actor: "z"})
	})
}

// failingStore is a Store whose every method returns errInjected.
// Used to exercise error paths without touching real persistence.
type failingStore struct{}

var errInjected = errors.New("injected store failure")

func (failingStore) CreateTenant(ctx context.Context, t *Tenant) error  { return errInjected }
func (failingStore) GetTenant(context.Context, string) (*Tenant, error) { return nil, errInjected }
func (failingStore) ListTenants(context.Context) ([]*Tenant, error)     { return nil, errInjected }
func (failingStore) CreateFleet(context.Context, *Fleet) error          { return errInjected }
func (failingStore) ListFleetsByTenant(context.Context, string) ([]*Fleet, error) {
	return nil, errInjected
}
func (failingStore) CreateHost(context.Context, *Host) error               { return errInjected }
func (failingStore) GetHost(context.Context, string) (*Host, error)        { return nil, errInjected }
func (failingStore) GetHostByToken(context.Context, string) (*Host, error) { return nil, errInjected }
func (failingStore) UpdateHostLastSeen(context.Context, string, time.Time) error {
	return errInjected
}
func (failingStore) ListHostsByFleet(context.Context, string) ([]*Host, error) {
	return nil, errInjected
}
func (failingStore) CreateRepo(context.Context, *Repo) error        { return errInjected }
func (failingStore) GetRepo(context.Context, string) (*Repo, error) { return nil, errInjected }
func (failingStore) ListReposByTenant(context.Context, string) ([]*Repo, error) {
	return nil, errInjected
}
func (failingStore) CreateJob(context.Context, *Job) error        { return errInjected }
func (failingStore) GetJob(context.Context, string) (*Job, error) { return nil, errInjected }
func (failingStore) ListJobsByTenant(context.Context, string) ([]*Job, error) {
	return nil, errInjected
}
func (failingStore) ListAllJobs(context.Context) ([]*Job, error) { return nil, errInjected }
func (failingStore) UpdateJobStatus(context.Context, string, JobStatus, *time.Time) error {
	return errInjected
}
func (failingStore) AppendAudit(context.Context, *AuditEntry) error { return errInjected }
func (failingStore) ListAuditByTenant(context.Context, string, int) ([]*AuditEntry, error) {
	return nil, errInjected
}
