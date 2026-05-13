package backupcontroller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStore_TenantLifecycle(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	tenant := &Tenant{Name: "acme", Plan: PlanPro}
	require.NoError(t, s.CreateTenant(ctx, tenant))
	assert.NotEmpty(t, tenant.ID, "Create should mint an ID")
	assert.False(t, tenant.CreatedAt.IsZero(), "Create should stamp CreatedAt")

	got, err := s.GetTenant(ctx, tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, "acme", got.Name)

	all, err := s.ListTenants(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

func TestMemoryStore_GetMissingReturnsErrNotFound(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	_, err := s.GetTenant(ctx, "does-not-exist")
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestMemoryStore_HostByToken(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	host := &Host{Hostname: "alpha", AgentToken: "tok-123", StackKitKind: HostKindBaseKit}
	require.NoError(t, s.CreateHost(ctx, host))

	got, err := s.GetHostByToken(ctx, "tok-123")
	require.NoError(t, err)
	assert.Equal(t, "alpha", got.Hostname)

	_, err = s.GetHostByToken(ctx, "wrong")
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestMemoryStore_AuditNewestFirst(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	for i := 0; i < 3; i++ {
		require.NoError(t, s.AppendAudit(ctx, &AuditEntry{
			TenantID: "t1",
			Actor:    "operator",
			Action:   "test",
			Resource: "test",
		}))
	}
	// Different tenant, must not bleed through.
	require.NoError(t, s.AppendAudit(ctx, &AuditEntry{
		TenantID: "t2",
		Actor:    "operator",
		Action:   "test",
		Resource: "test",
	}))

	out, err := s.ListAuditByTenant(ctx, "t1", 10)
	require.NoError(t, err)
	assert.Len(t, out, 3)

	out2, err := s.ListAuditByTenant(ctx, "t2", 10)
	require.NoError(t, err)
	assert.Len(t, out2, 1)
}

func TestMemoryStore_UpdateJobStatus(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	job := &Job{TenantID: "t1", HostID: "h1", RepoID: "r1", Schedule: "0 2 * * *"}
	require.NoError(t, s.CreateJob(ctx, job))
	assert.Equal(t, JobStatusPending, job.Status, "default status should be pending")

	now := time.Now().UTC()
	require.NoError(t, s.UpdateJobStatus(ctx, job.ID, JobStatusOK, &now))

	got, err := s.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, JobStatusOK, got.Status)
	require.NotNil(t, got.LastRun)
	assert.WithinDuration(t, now, *got.LastRun, time.Second)
}

func TestMemoryQueue_PublishPullRoundtrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	q := NewMemoryQueue(4)
	defer func() { _ = q.Close() }()

	in := JobMessage{JobID: "j1", HostID: "h1", Deadline: time.Now().Add(1 * time.Hour)}
	require.NoError(t, q.Publish(ctx, in))

	out, err := q.Pull(ctx)
	require.NoError(t, err)
	assert.Equal(t, in, out)
}

func TestScheduler_ShouldRun(t *testing.T) {
	at, err := time.Parse(time.RFC3339, "2026-05-01T02:00:00Z")
	require.NoError(t, err)

	cases := []struct {
		schedule string
		want     bool
	}{
		// 02:00 every day → matches.
		{"0 2 * * *", true},
		// 02:01 → does not match this minute.
		{"1 2 * * *", false},
		// every minute on the hour → matches at :00.
		{"0 * * * *", true},
		// any minute, any hour → matches.
		{"* * * * *", true},
		// invalid.
		{"definitely-not-cron", false},
	}
	for _, c := range cases {
		j := &Job{Schedule: c.schedule}
		assert.Equal(t, c.want, shouldRun(j, at), "schedule=%q at=%s", c.schedule, at)
	}
}
