package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
)

var openPublicUpgradeRecovery = func(
	workspace, operationID string,
) (publicUpgradeLifecycleSession, lifecyclemutation.Record, error) {
	return lifecyclemutation.OpenUpgradeRecovery(workspace, operationID)
}

func recoverPublicUpgradeOperation(
	ctx context.Context,
	workspace, requestedSpec, operationID string,
) (result publicUpgradeTransaction, returnErr error) {
	result = publicUpgradeTransaction{
		APIVersion:  publicUpgradeTransactionAPIVersion,
		OperationID: operationID,
		Status:      "recovering",
		FailedPhase: "explicit-recovery",
		Rollback: publicUpgradeRollback{
			Status: publicUpgradeRollbackFailed,
		},
	}
	mutation, record, err := openPublicUpgradeRecovery(workspace, operationID)
	if err != nil {
		result.Status = "failed"
		return result, err
	}
	defer func() { returnErr = errors.Join(returnErr, mutation.Close()) }()
	result.Rollback.RecoverySnapshotID = record.Checkpoint.ExecutorStateSnapshotID
	result.Rollback.PriorReleaseVersion = record.Prior.Version

	switch record.Status {
	case lifecyclemutation.StatusRecovered:
		result.Status = "recovered"
		result.Rollback.Status = publicUpgradeRollbackRestored
		result.Rollback.Verified = true
		return result, nil
	case lifecyclemutation.StatusSucceeded:
		result.Status = "succeeded"
		result.Rollback.Status = publicUpgradeRollbackNotRequired
		return result, nil
	}
	if record.Phase == lifecyclemutation.PhaseCommitSucceeded {
		if err := mutation.Complete(lifecyclemutation.StatusSucceeded); err != nil {
			result.Status = "failed"
			return result, fmt.Errorf("complete already committed lifecycle operation: %w", err)
		}
		result.Status = "succeeded"
		result.Rollback.Status = publicUpgradeRollbackNotRequired
		return result, nil
	}
	if !explicitRollbackStartPhase(record.Phase) {
		result.Status = "failed"
		return result, fmt.Errorf(
			"lifecycle operation %s stopped at ambiguous phase %s; refusing to replay a child command",
			operationID, record.Phase,
		)
	}

	checkpoint := publicUpgradeCheckpoint{
		OperationID:             operationID,
		KopiaAnchorID:           record.Checkpoint.KopiaAnchorID,
		ExecutorStateSnapshotID: record.Checkpoint.ExecutorStateSnapshotID,
	}
	if err := checkpoint.validate(); err != nil {
		result.Status = "failed"
		return result, fmt.Errorf("validate explicit recovery checkpoint: %w", err)
	}
	err = withPublicUpgradeTransactionLock(
		workspace,
		func(controlTransaction *confinedfs.Transaction) error {
			snapshot, loadErr := loadPublicUpgradeRecoveryCheckpoint(
				workspace, checkpoint.ExecutorStateSnapshotID,
			)
			if loadErr != nil {
				return loadErr
			}
			if err := validateExplicitRecoveryAuthority(record, snapshot); err != nil {
				return err
			}
			if record.Phase != lifecyclemutation.PhasePrepared {
				result.Rollback.DataStaged = true
			}
			return rollbackPublicUpgrade(
				ctx, workspace, requestedSpec, checkpoint, snapshot, mutation,
				controlTransaction, &result,
			)
		},
	)
	if err != nil {
		result.Status = "failed"
		return result, err
	}
	result.Status = "recovered"
	result.Rollback.Status = publicUpgradeRollbackRestored
	result.Rollback.Verified = true
	return result, nil
}

func explicitRollbackStartPhase(phase string) bool {
	switch phase {
	case lifecyclemutation.PhasePrepared,
		lifecyclemutation.PhaseTargetGenerateStarted,
		lifecyclemutation.PhaseTargetGenerateSucceeded,
		lifecyclemutation.PhaseTargetApplyStarted,
		lifecyclemutation.PhaseTargetApplySucceeded,
		lifecyclemutation.PhaseTargetVerifyStarted,
		lifecyclemutation.PhaseTargetVerifySucceeded,
		lifecyclemutation.PhaseCommitStarted,
		lifecyclemutation.PhaseRollbackStarted:
		return true
	default:
		return false
	}
}

func validateExplicitRecoveryAuthority(
	record lifecyclemutation.Record,
	snapshot upgradelifecycle.ExecutorStateSnapshot,
) error {
	if snapshot.ID != record.Checkpoint.ExecutorStateSnapshotID ||
		snapshot.OperationID != record.OperationID ||
		snapshot.KopiaSnapshotAnchor.ID != record.Checkpoint.KopiaAnchorID ||
		snapshot.OwnerRef != record.OwnerRef ||
		architectureV2ComponentVersion(snapshot.Release.Version) != record.Prior.Version ||
		snapshot.Release.ArchiveSHA256 != record.Prior.ArchiveSHA256 ||
		snapshot.Executable.Blob.SHA256 != record.Prior.ExecutableSHA256 {
		return errors.New(
			"explicit recovery checkpoint or prior executor differs from the signed lifecycle journal",
		)
	}
	return nil
}
