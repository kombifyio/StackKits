package lifecyclemutation

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrUpgradeDataActivationRequired prevents a prior runtime from opening data
// that a newer runtime may already have changed. Isolated backup staging alone
// is not proof that the live data belongs to the prior runtime.
var ErrUpgradeDataActivationRequired = errors.New("lifecycle mutation: prior-runtime rollback requires verified live data activation after target Apply or an ambiguous recovery phase")

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
// proves target Apply was never admitted. A Record alone cannot prove this.
func (session *Session) RequirePreApplyUpgradeRollback() error {
	if session == nil || session.transaction == nil {
		return ErrUpgradeDataActivationRequired
	}
	if err := session.record.RequirePreApplyUpgradeRollback(); err == nil {
		return nil
	}
	if session.claimLock == nil || session.record.Kind != KindUpgrade || session.record.Status != StatusActive ||
		(session.record.Phase != PhaseRollbackStarted && session.record.Phase != PhaseRollbackGenerateStarted && session.record.Phase != PhaseRollbackGenerateDone) {
		return ErrUpgradeDataActivationRequired
	}
	latest, digest, err := latestHistoryRecord(session.workspace, session.transaction, session.record.OperationID)
	if err != nil {
		return err
	}
	if latest.Sequence != session.record.Sequence || digest != session.recordDigest {
		return ErrUpgradeDataActivationRequired
	}
	entries, err := session.transaction.Walk(operationsRoot + "/" + session.record.OperationID)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Info.IsDir() || strings.HasPrefix(filepath.Base(filepath.FromSlash(entry.Path)), ".stackkit-tmp-") {
			continue
		}
		data, _, err := session.transaction.ReadStable(entry.Path)
		if err != nil {
			return err
		}
		record, _, err := decodeRecord(session.workspace, data)
		if err != nil {
			return err
		}
		switch record.Phase {
		case PhasePrepared, PhaseTargetGenerateStarted, PhaseTargetGenerateSucceeded, PhaseRollbackStarted, PhaseRollbackGenerateStarted, PhaseRollbackGenerateDone:
		default:
			return ErrUpgradeDataActivationRequired
		}
	}
	if joinPhase(session.record.Phase) {
		claimed, _, err := session.transaction.Exists(joinClaimPath(session.record))
		if err != nil {
			return err
		}
		if claimed {
			return ErrUpgradeDataActivationRequired
		}
	}
	return nil
}
