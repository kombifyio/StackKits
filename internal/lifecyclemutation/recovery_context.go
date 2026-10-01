package lifecyclemutation

import (
	"context"
	"time"
)

// RecoveryBudget is the longest a compensating operation may run. A mutation
// that must leave room for its own recovery reserves this much of the command
// deadline.
const RecoveryBudget = 10 * time.Minute

type commandDeadlineKey struct{}

// WithCommandDeadline records the time by which the whole command, recovery
// included, must have returned. Its caller (for example Techstack's agent)
// kills the process at that point, so a recovery that runs past it is lost.
func WithCommandDeadline(ctx context.Context, deadline time.Time) context.Context {
	return context.WithValue(ctx, commandDeadlineKey{}, deadline)
}

// CommandDeadline reports the deadline recorded by WithCommandDeadline. It
// survives context.WithoutCancel, unlike the context's own deadline.
func CommandDeadline(ctx context.Context) (time.Time, bool) {
	deadline, ok := ctx.Value(commandDeadlineKey{}).(time.Time)
	return deadline, ok
}

// RecoveryContext gives an already authorized compensating operation its own
// bounded phase. Cancellation of the failed mutation must not also cancel its
// recovery, but the recovery still ends by the command deadline so it reports
// its outcome instead of being killed mid-way. Callers must retain the mutation
// lock and verify recovery authority.
func RecoveryContext(parent context.Context) (context.Context, context.CancelFunc) {
	deadline := time.Now().Add(RecoveryBudget)
	if command, ok := CommandDeadline(parent); ok && command.Before(deadline) {
		deadline = command
	}
	return context.WithDeadline(context.WithoutCancel(parent), deadline)
}
