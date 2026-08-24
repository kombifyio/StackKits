package commands

import (
	"github.com/kombifyio/stackkits/internal/applyledger"
)

// persistApplyLedger writes the ledger into the current rollout run directory.
//
// It is best effort on purpose: failing to record the account must never turn a
// usable rollout into a failed one, and the ledger was already reported to the
// caller by the time this runs.
func persistApplyLedger(ledger applyledger.Ledger) string {
	if rolloutRecorder == nil {
		return ""
	}
	root := rolloutRecorder.Root()
	if root == "" {
		return ""
	}
	path, err := applyledger.Store(root, ledger)
	if err != nil {
		printVerbose("apply outcome ledger could not be persisted: %v", err)
		return ""
	}
	return path
}

// latestApplyLedger returns the most recent per-unit account in this workspace,
// or nil when no run recorded one. Status uses it to report what the last Apply
// actually did rather than only that one happened.
func latestApplyLedger(workspace string) *applyledger.Ledger {
	return applyledger.Latest(workspace)
}
