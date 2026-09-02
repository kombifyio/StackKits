package lifecyclemutation

import (
	"context"
	"time"
)

// RecoveryContext gives an already authorized compensating operation its own
// bounded phase. Cancellation of the failed mutation must not also cancel its
// recovery. Callers must retain the mutation lock and verify recovery authority.
func RecoveryContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), 10*time.Minute)
}
