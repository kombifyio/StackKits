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

// RetiredOutputGCInspection is the explicit, one-action cleanup report for a
// completed output-transaction tombstone.
type RetiredOutputGCInspection = execution.RetiredOutputGCInspection

// RetiredOutputGCAction names the only bounded retired-tombstone mutation.
type RetiredOutputGCAction = execution.RetiredOutputGCAction

const (
	RetiredOutputGCRemoveStage   = execution.RetiredOutputGCRemoveStage
	RetiredOutputGCRemoveJournal = execution.RetiredOutputGCRemoveJournal
)

func InspectRetiredOutputGC(workspace *confinedfs.Transaction, transactionID string) (RetiredOutputGCInspection, error) {
	return execution.InspectRetiredOutputGC(workspace, transactionID)
}

func ApplyRetiredOutputGC(workspace *confinedfs.Transaction, expected RetiredOutputGCInspection) error {
	return execution.ApplyRetiredOutputGC(workspace, expected)
}
