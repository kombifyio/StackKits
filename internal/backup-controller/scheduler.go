package backupcontroller

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// Scheduler walks the Job table on a tick, identifies due jobs based on
// their cron schedule, and publishes a JobMessage for each onto the
// queue. Agents pull from the queue and execute.
//
// Cron parsing is intentionally minimal: we accept the standard 5-field
// crontab format ("min hour dom mon dow") that the addon already uses
// in #Config.schedule. We do NOT use a full external cron library yet
// because the surface this scaffold needs is tiny and an extra
// dependency for a half-finished feature is wrong.
//
// The Postgres-backed Phase-4-final scheduler will replace this loop
// with `SELECT … FOR UPDATE SKIP LOCKED` so multiple controller
// replicas can share work. The contract — "due jobs land on the queue,
// exactly once" — does not change.
type Scheduler struct {
	Store    Store
	Queue    JobQueue
	Tick     time.Duration
	Now      func() time.Time
	Deadline time.Duration
	Logger   *slog.Logger
}

// Run blocks until ctx is canceled. Errors during a single tick are
// logged but never abort the loop — a transient queue blip should not
// take the whole controller offline.
func (s *Scheduler) Run(ctx context.Context) error {
	tick := s.Tick
	if tick <= 0 {
		tick = 60 * time.Second
	}
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := s.Now
	if now == nil {
		now = time.Now
	}

	t := time.NewTicker(tick)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			s.tickOnce(ctx, now(), logger)
		}
	}
}

// tickOnce is exported for tests. The exported wrapper Run is what the
// production binary calls.
func (s *Scheduler) tickOnce(ctx context.Context, at time.Time, logger *slog.Logger) {
	jobs, err := s.Store.ListAllJobs(ctx)
	if err != nil {
		logger.Error("scheduler: list jobs", "err", err)
		return
	}
	deadline := s.Deadline
	if deadline <= 0 {
		deadline = 1 * time.Hour
	}

	for _, j := range jobs {
		if !shouldRun(j, at) {
			continue
		}
		msg := JobMessage{
			JobID:    j.ID,
			HostID:   j.HostID,
			Deadline: at.Add(deadline),
		}
		if err := s.Queue.Publish(ctx, msg); err != nil {
			logger.Error("scheduler: publish", "job", j.ID, "err", err)
			continue
		}
		// Mark as pending → running transition happens when the agent
		// picks it up; here we just record the fact that we dispatched.
		runAt := at
		_ = s.Store.UpdateJobStatus(ctx, j.ID, JobStatusPending, &runAt)
	}
}

// shouldRun is the minimal cron decision: a job is due when its
// cron expression's minute and hour fields match `at`'s minute/hour
// AND its LastRun is not within the same minute. Day-of-month, month,
// and day-of-week are honored as wildcards or exact matches; we do
// not support ranges, lists, or step values yet — those go through
// the real cron lib in the Postgres-backed scheduler.
//
// The intent is to make this code shippable and testable today without
// pulling in a dep we'll throw away in two PRs.
func shouldRun(j *Job, at time.Time) bool {
	parts := strings.Fields(j.Schedule)
	if len(parts) != 5 {
		return false
	}
	if !cronFieldMatch(parts[0], at.Minute()) {
		return false
	}
	if !cronFieldMatch(parts[1], at.Hour()) {
		return false
	}
	if !cronFieldMatch(parts[2], at.Day()) {
		return false
	}
	if !cronFieldMatch(parts[3], int(at.Month())) {
		return false
	}
	if !cronFieldMatch(parts[4], int(at.Weekday())) {
		return false
	}
	if j.LastRun != nil && j.LastRun.Truncate(time.Minute).Equal(at.Truncate(time.Minute)) {
		// Already dispatched this minute.
		return false
	}
	return true
}

func cronFieldMatch(field string, val int) bool {
	if field == "*" {
		return true
	}
	if field == "" {
		// An empty field is malformed. The shipping scaffold's
		// parseInt happily returns 0 for "", which would silently
		// match any zero-valued cron position (minute 0, hour 0, …).
		// Reject explicitly so a malformed schedule never accidentally
		// fires.
		return false
	}
	// Single integer only for this scaffold.
	var n int
	if _, err := parseInt(field, &n); err != nil {
		return false
	}
	return n == val
}

// parseInt is a tiny helper to avoid importing strconv just for this.
// Returns the number of bytes consumed.
func parseInt(s string, out *int) (int, error) {
	n := 0
	consumed := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return consumed, errBadCronField
		}
		n = n*10 + int(r-'0')
		consumed++
	}
	*out = n
	return consumed, nil
}

var errBadCronField = errBadCron{}

type errBadCron struct{}

func (errBadCron) Error() string { return "bad cron field" }
