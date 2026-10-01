package commands

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
)

const (
	// advancedMutationCommandDeadlineEnv lets the dispatcher name the time it
	// stops waiting for an Advanced mutation (RFC 3339).
	advancedMutationCommandDeadlineEnv = "STACKKIT_COMMAND_DEADLINE"
	// advancedMutationCommandBudget is the default whole-command budget when
	// no deadline is named. It stays inside Techstack's 30-minute Advanced
	// mutation command timeout with room for process start and the result.
	advancedMutationCommandBudget = 28 * time.Minute
	// advancedTargetMinimumBudget is the least target time worth starting; a
	// shorter budget fails before any target side effect.
	advancedTargetMinimumBudget = 5 * time.Minute
)

// advancedMutationCommandDeadline is the time by which an Advanced mutation,
// rollback included, must have returned its result.
func advancedMutationCommandDeadline(now time.Time) (time.Time, error) {
	raw := strings.TrimSpace(os.Getenv(advancedMutationCommandDeadlineEnv))
	if raw == "" {
		return now.Add(advancedMutationCommandBudget), nil
	}
	deadline, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be an RFC 3339 time: %w", advancedMutationCommandDeadlineEnv, err)
	}
	if !deadline.After(now) {
		return time.Time{}, fmt.Errorf("%s %s has already passed", advancedMutationCommandDeadlineEnv, raw)
	}
	return deadline, nil
}

// advancedTargetContext bounds the target phase so that a failed target still
// leaves the full recovery budget before the command deadline: the target
// ends by min(now+longOperation, commandDeadline-RecoveryBudget). A target
// that cannot get advancedTargetMinimumBudget fails closed before it starts.
func advancedTargetContext(ctx context.Context, now time.Time, longOperation time.Duration) (context.Context, context.CancelFunc, error) {
	deadline := now.Add(longOperation)
	if command, ok := lifecyclemutation.CommandDeadline(ctx); ok {
		if latest := command.Add(-lifecyclemutation.RecoveryBudget); latest.Before(deadline) {
			deadline = latest
		}
	}
	if remaining := deadline.Sub(now); remaining < advancedTargetMinimumBudget {
		return nil, nil, fmt.Errorf(
			"Advanced target needs at least %s before its rollback reserve, %s remain before the command deadline",
			advancedTargetMinimumBudget, remaining.Truncate(time.Second),
		)
	}
	targetCtx, cancel := context.WithDeadline(ctx, deadline)
	return targetCtx, cancel, nil
}
