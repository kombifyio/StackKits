package backupcontroller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScheduler_TickOnceDispatchesDueJobs is the end-to-end check for
// the scheduler's only real responsibility: turn a Job whose schedule
// matches `at` into a JobMessage on the queue.
func TestScheduler_TickOnceDispatchesDueJobs(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	queue := NewMemoryQueue(8)
	defer func() { _ = queue.Close() }()

	// One job that is due, one that is not.
	due := &Job{TenantID: "t1", HostID: "h1", RepoID: "r1", Schedule: "0 2 * * *"}
	notDue := &Job{TenantID: "t1", HostID: "h2", RepoID: "r1", Schedule: "30 2 * * *"}
	require.NoError(t, store.CreateJob(ctx, due))
	require.NoError(t, store.CreateJob(ctx, notDue))

	at, err := time.Parse(time.RFC3339, "2026-05-01T02:00:00Z")
	require.NoError(t, err)

	s := &Scheduler{Store: store, Queue: queue, Deadline: 30 * time.Minute}
	s.tickOnce(ctx, at, nil)

	// Drain the queue with a short deadline; we expect exactly the
	// "due" job and nothing else.
	pullCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	msg, err := queue.Pull(pullCtx)
	require.NoError(t, err)
	assert.Equal(t, due.ID, msg.JobID)
	assert.Equal(t, due.HostID, msg.HostID)
	assert.True(t, msg.Deadline.After(at), "deadline should be after dispatch time")

	// A second pull must time out — confirms the not-due job was filtered.
	pullCtx2, cancel2 := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel2()
	_, err = queue.Pull(pullCtx2)
	assert.ErrorIs(t, err, context.DeadlineExceeded, "not-due job should not have been dispatched")
}

// TestScheduler_TickOnceMarksDispatched verifies the Store-side
// side-effect: after tickOnce dispatches a job, that job's LastRun is
// stamped to `at` so the next tick (within the same minute) does NOT
// re-dispatch it.
func TestScheduler_TickOnceMarksDispatched(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	queue := NewMemoryQueue(8)
	defer func() { _ = queue.Close() }()

	job := &Job{TenantID: "t1", HostID: "h1", RepoID: "r1", Schedule: "* * * * *"}
	require.NoError(t, store.CreateJob(ctx, job))

	at, err := time.Parse(time.RFC3339, "2026-05-01T02:00:00Z")
	require.NoError(t, err)

	s := &Scheduler{Store: store, Queue: queue}
	s.tickOnce(ctx, at, nil)

	got, err := store.GetJob(ctx, job.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastRun, "LastRun should be stamped after dispatch")
	assert.WithinDuration(t, at, *got.LastRun, time.Second)

	// Drain the queued message so the next tick is observed against an
	// empty queue.
	pullCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_, err = queue.Pull(pullCtx)
	require.NoError(t, err)

	// Re-tick at the same minute — no new dispatch.
	s.tickOnce(ctx, at, nil)
	pullCtx2, cancel2 := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel2()
	_, err = queue.Pull(pullCtx2)
	assert.ErrorIs(t, err, context.DeadlineExceeded, "second tick within same minute must not re-dispatch")
}

// TestScheduler_RunRespectsContext is the cooperative-cancellation
// guarantee for the long-running loop. The Run() method must return
// promptly when its context is canceled, otherwise a controller
// shutdown would hang on graceful-stop.
func TestScheduler_RunRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := NewMemoryStore()
	queue := NewMemoryQueue(1)
	defer func() { _ = queue.Close() }()

	s := &Scheduler{Store: store, Queue: queue, Tick: 50 * time.Millisecond}

	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	// Give the loop a tick or two to enter the select.
	time.Sleep(120 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(1 * time.Second):
		t.Fatal("Run did not return within 1s of context cancellation")
	}
}

// TestCronFieldMatch is a unit test for the minimal cron parser
// embedded in scheduler.go. The shipping scaffold accepts only "*" or
// a single integer per field; any other syntax (ranges, lists, steps)
// must return false rather than silently misinterpreting.
func TestCronFieldMatch(t *testing.T) {
	cases := []struct {
		field string
		val   int
		want  bool
	}{
		{"*", 0, true},
		{"*", 59, true},
		{"5", 5, true},
		{"5", 6, false},
		{"0", 0, true},
		{"00", 0, true}, // multi-digit zero-pad must still parse
		// Unsupported syntax — must be rejected, not best-guessed.
		{"1-5", 3, false},
		{"1,2,3", 2, false},
		{"*/15", 15, false},
		// Garbage.
		{"abc", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got := cronFieldMatch(c.field, c.val)
		assert.Equal(t, c.want, got, "cronFieldMatch(%q, %d)", c.field, c.val)
	}
}
