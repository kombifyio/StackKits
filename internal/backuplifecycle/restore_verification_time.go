package backuplifecycle

import (
	"errors"
	"time"
)

func restoreApprovalCurrent(recovery RestoreRecoveryAnchor, at time.Time) bool {
	return !at.Before(recovery.ApprovedAt) && at.Before(recovery.ExpiresAt)
}

// A post-verifier must observe this invocation inside its original Owner
// approval window. An old successful observation cannot finish a new or
// resumed restore, and a slow verifier cannot extend the approval lifetime.
func validateFreshRestoreVerification(verification RestoreVerification, recovery RestoreRecoveryAnchor, startedAt, finishedAt time.Time) error {
	if !restoreApprovalCurrent(recovery, startedAt) || !restoreApprovalCurrent(recovery, finishedAt) ||
		finishedAt.Before(startedAt) || verification.VerifiedAt.Before(startedAt) || verification.VerifiedAt.After(finishedAt) {
		return errors.New("backuplifecycle: restore verification is not fresh within the current Owner approval")
	}
	return nil
}
