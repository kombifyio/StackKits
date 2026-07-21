package architecturev2

import (
	"github.com/kombifyio/stackkits/internal/architecturev2/internal/execution"
	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// RequireNoPendingOutputTransaction applies the Architecture v2 durable
// transaction admission guard through an already-held workspace transaction.
// CLI execution modes call it only while owning the matching output lock.
func RequireNoPendingOutputTransaction(workspace *confinedfs.Transaction, outputRoot string) error {
	return execution.RequireNoPendingOutputTransaction(workspace, outputRoot)
}
