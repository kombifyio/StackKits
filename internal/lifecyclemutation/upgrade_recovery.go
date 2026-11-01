package lifecyclemutation

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrUpgradeDataActivationRequired prevents a prior runtime from opening data
// that a newer runtime may already have changed. Isolated backup staging alone
// is not proof that the live data belongs to the prior runtime.
var ErrUpgradeDataActivationRequired = errors.New("lifecycle mutation: prior-runtime rollback requires verified live data activation after target Apply or an ambiguous recovery phase; after target Apply, stackkit upgrade --recover <operation-id> --forward completes the target release instead")

// RequirePreApplyUpgradeRollback admits executor-only recovery while the signed
// journal proves that no target Apply was admitted. Once Apply may have run,
// recovery must restore the complete prior data set before starting old images.
// A bare rollback-started record does not retain that proof and is denied too.
func (record Record) RequirePreApplyUpgradeRollback() error {
	if record.Kind != KindUpgrade || record.Status != StatusActive {
		return ErrUpgradeDataActivationRequired
	}
	switch record.Phase {
	case PhasePrepared, PhaseTargetGenerateStarted, PhaseTargetGenerateSucceeded:
		return nil
	default:
		return ErrUpgradeDataActivationRequired
	}
}

// RequirePreApplyUpgradeRollback can resume unclaimed or completed prior Generate
// while explicit recovery owns the claim race and the complete signed history
// proves target Apply was never admitted. A Record alone cannot prove this: a
// forward retarget signs target Generate again after target Apply was admitted,
// so even a target Generate phase is checked against the history.
func (session *Session) RequirePreApplyUpgradeRollback() error {
	if session == nil || session.transaction == nil {
		return ErrUpgradeDataActivationRequired
	}
	recordAdmits := session.record.RequirePreApplyUpgradeRollback() == nil
	if !recordAdmits &&
		(session.claimLock == nil || session.record.Kind != KindUpgrade || session.record.Status != StatusActive ||
			(session.record.Phase != PhaseRollbackStarted && session.record.Phase != PhaseRollbackGenerateStarted && session.record.Phase != PhaseRollbackGenerateDone)) {
		return ErrUpgradeDataActivationRequired
	}
	admitted, err := session.targetApplyAdmitted()
	if err != nil {
		return err
	}
	if admitted {
		return ErrUpgradeDataActivationRequired
	}
	if !recordAdmits && joinPhase(session.record.Phase) {
		claimed, _, err := session.transaction.Exists(joinClaimPath(session.record))
		if err != nil {
			return err
		}
		// A prior Generate whose claimant recovery proved gone wrote only
		// generated artifacts; the restart regenerates them.
		if claimed && !session.claimantGone {
			return ErrUpgradeDataActivationRequired
		}
	}
	return nil
}

// ForwardRecoveryAdmitted reports whether forward completion may resume from
// the current phase: after target Apply admission, or at the target Generate
// that a forward retarget signed after target Apply had been admitted.
func (session *Session) ForwardRecoveryAdmitted() (bool, error) {
	if session == nil || session.transaction == nil ||
		session.record.Kind != KindUpgrade || session.record.Status != StatusActive ||
		session.record.UpgradeDataActivation != nil {
		return false, nil
	}
	if ForwardRecoveryPhase(session.record.Phase) {
		return true, nil
	}
	if session.record.Phase != PhaseTargetGenerateStarted && session.record.Phase != PhaseTargetGenerateSucceeded {
		return false, nil
	}
	return session.targetApplyAdmitted()
}

// targetApplyAdmitted reads the operation's complete signed history and
// reports whether any phase beyond target Generate and the pre-Apply rollback
// was ever recorded, so target Apply may have run. A history that is not the
// session's exact current record fails closed.
func (session *Session) targetApplyAdmitted() (bool, error) {
	latest, digest, err := latestHistoryRecord(session.workspace, session.transaction, session.record.OperationID)
	if err != nil {
		return false, err
	}
	if latest.Sequence != session.record.Sequence || digest != session.recordDigest {
		return false, ErrUpgradeDataActivationRequired
	}
	entries, err := session.transaction.Walk(operationsRoot + "/" + session.record.OperationID)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Info.IsDir() || strings.HasPrefix(filepath.Base(filepath.FromSlash(entry.Path)), ".stackkit-tmp-") {
			continue
		}
		data, _, err := session.transaction.ReadStable(entry.Path)
		if err != nil {
			return false, err
		}
		record, _, err := decodeRecord(session.workspace, data)
		if err != nil {
			return false, err
		}
		switch record.Phase {
		case PhasePrepared, PhaseTargetGenerateStarted, PhaseTargetGenerateSucceeded, PhaseRollbackStarted, PhaseRollbackGenerateStarted, PhaseRollbackGenerateDone:
		default:
			return true, nil
		}
	}
	return false, nil
}
