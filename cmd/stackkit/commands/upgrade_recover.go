package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
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
	forward bool,
	retargetTo string,
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
		if record.Phase == lifecyclemutation.PhasePrepared {
			result.Rollback.Status = publicUpgradeRollbackReleasedBeforeTarget
			return result, nil
		}
		result.Rollback.Status = publicUpgradeRollbackRestored
		result.Rollback.Verified = true
		return result, nil
	case lifecyclemutation.StatusSucceeded:
		result.Status = "succeeded"
		result.Rollback.Status = publicUpgradeRollbackNotRequired
		return result, nil
	}
	if record.Phase == lifecyclemutation.PhaseCommitSucceeded {
		if err := backupcustody.CommitS3TargetMove(workspace); err != nil {
			result.Status = "failed"
			return result, fmt.Errorf("commit the backup target custody move: %w", err)
		}
		if err := mutation.Complete(lifecyclemutation.StatusSucceeded); err != nil {
			result.Status = "failed"
			return result, fmt.Errorf("complete already committed lifecycle operation: %w", err)
		}
		result.Status = "succeeded"
		result.Rollback.Status = publicUpgradeRollbackNotRequired
		return result, nil
	}
	if forward {
		admission, ok := mutation.(interface{ ForwardRecoveryAdmitted() (bool, error) })
		if !ok {
			result.Status = "failed"
			return result, errors.New("forward recovery requires an explicit lifecycle recovery session")
		}
		if admitted, err := admission.ForwardRecoveryAdmitted(); err != nil || !admitted {
			result.Status = "failed"
			return result, errors.Join(fmt.Errorf(
				"lifecycle operation %s stopped at phase %s and its signed history never admitted target Apply; recover it without --forward",
				operationID, record.Phase,
			), err)
		}
		if err := completePublicUpgradeForward(
			ctx, workspace, requestedSpec, record, mutation, retargetTo, &result,
		); err != nil {
			result.Status = "failed"
			return result, err
		}
		result.Status = "succeeded"
		result.FailedPhase = ""
		result.Rollback.Status = publicUpgradeRollbackNotRequired
		return result, nil
	}
	if !forward {
		released, err := releasePreparedPublicUpgrade(
			workspace, operationID, record, mutation, &result,
		)
		if err != nil {
			result.Status = "failed"
			return result, err
		}
		if released {
			return result, nil
		}
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
			// A change set that died after moving custody to its candidate
			// authority returns custody to the authority it recorded.
			if err := backupcustody.SettleS3TargetMove(workspace); err != nil {
				return fmt.Errorf("restore the backup target custody: %w", err)
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
		lifecyclemutation.PhaseRollbackStarted,
		lifecyclemutation.PhaseRollbackGenerateStarted,
		lifecyclemutation.PhaseRollbackGenerateDone:
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
		priorReleaseAuthority(snapshot) != record.Prior {
		return errors.New(
			"explicit recovery checkpoint or prior executor differs from the signed lifecycle journal",
		)
	}
	return nil
}

// releasePreparedPublicUpgrade settles an upgrade whose signed history is
// exactly the prepared phase: no target Generate, Apply or Verify and no
// rollback phase was ever recorded, so the prior release has nothing to be
// restored to and its Generate, Plan, Apply and Verify must not be replayed.
// It returns false, leaving today's rollback path in force, whenever the
// history is not exactly prepared; it refuses when the checkpoint differs
// from the signed prior authority or the workspace shows a target install or
// Apply. The recovering CLI may be any release that carries this code.
func releasePreparedPublicUpgrade(
	workspace, operationID string,
	record lifecyclemutation.Record,
	mutation publicUpgradeLifecycleSession,
	result *publicUpgradeTransaction,
) (bool, error) {
	if record.Phase != lifecyclemutation.PhasePrepared {
		return false, nil
	}
	release, ok := mutation.(interface {
		PreparedReleaseAdmitted() (bool, error)
		CompletePreparedRelease() error
	})
	if !ok {
		return false, nil
	}
	admitted, err := release.PreparedReleaseAdmitted()
	if err != nil {
		return false, fmt.Errorf("inspect prepared lifecycle history: %w", err)
	}
	if !admitted {
		return false, nil
	}
	checkpoint := publicUpgradeCheckpoint{
		OperationID:             operationID,
		KopiaAnchorID:           record.Checkpoint.KopiaAnchorID,
		ExecutorStateSnapshotID: record.Checkpoint.ExecutorStateSnapshotID,
	}
	if err := checkpoint.validate(); err != nil {
		return false, fmt.Errorf("validate explicit recovery checkpoint: %w", err)
	}
	err = withPublicUpgradeTransactionLock(
		workspace,
		func(_ *confinedfs.Transaction) error {
			snapshot, loadErr := loadPublicUpgradeRecoveryCheckpoint(
				workspace, checkpoint.ExecutorStateSnapshotID,
			)
			if loadErr != nil {
				return loadErr
			}
			if err := validateExplicitRecoveryAuthority(record, snapshot); err != nil {
				return err
			}
			// Nothing of the target may be installed or applied; any doubt
			// refuses and never replays the prior release.
			if err := requirePreparedTargetNeverApplied(workspace, record, snapshot); err != nil {
				return err
			}
			if err := backupcustody.SettleS3TargetMove(workspace); err != nil {
				return fmt.Errorf("restore the backup target custody: %w", err)
			}
			if err := release.CompletePreparedRelease(); err != nil {
				return fmt.Errorf("release the prepared lifecycle operation: %w", err)
			}
			return nil
		},
	)
	if err != nil {
		return false, err
	}
	result.Status = "recovered"
	result.Rollback.Status = publicUpgradeRollbackReleasedBeforeTarget
	result.Rollback.Verified = false
	return true, nil
}

// requirePreparedTargetNeverApplied proves from workspace state, not from the
// running process, that the target of a prepared-only upgrade was never
// installed or applied: (1) the content-addressed Apply result the checkpoint
// sealed still exists and was produced by the signed prior release, (2) no
// Apply result at all was recorded after the checkpoint was captured (the
// latest applied executor identity is therefore still the prior release), and
// (3) a different target release has no installed release-cache directory.
// Unreadable or ambiguous state is a refusal.
func requirePreparedTargetNeverApplied(
	workspace string,
	record lifecyclemutation.Record,
	snapshot upgradelifecycle.ExecutorStateSnapshot,
) (returnErr error) {
	sealed := strings.TrimPrefix(snapshot.Lineage.ApplyResultHash, "sha256:")
	if sealed == "" {
		return errors.New("the checkpoint does not name its sealed Apply result; refusing to release the prepared operation")
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return fmt.Errorf("open workspace to prove the target was never applied: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return fmt.Errorf("begin target-never-applied proof: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, transaction.Close()) }()
	directory := path.Join(architectureV2ApplyEvidenceRoot, "results")
	entries, err := transaction.Walk(directory)
	if err != nil {
		return fmt.Errorf("read the Apply results to prove the target was never applied: %w", err)
	}
	sealedFound := false
	for _, entry := range entries {
		if entry.Info.IsDir() {
			continue
		}
		if !entry.Info.Mode().IsRegular() || path.Dir(entry.Path) != directory ||
			!strings.HasSuffix(path.Base(entry.Path), ".json") {
			return fmt.Errorf("Apply result directory contains an unsupported entry %q", entry.Path)
		}
		raw, _, err := transaction.ReadStable(entry.Path)
		if err != nil {
			return err
		}
		var probe struct {
			Executor  generationartifact.ApplyExecutorIdentity `json:"executor"`
			AppliedAt string                                   `json:"appliedAt"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return fmt.Errorf("decode Apply result %q: %w", entry.Path, err)
		}
		appliedAt, err := time.Parse(time.RFC3339Nano, probe.AppliedAt)
		if err != nil {
			return fmt.Errorf("Apply result %q has no readable Apply time: %w", entry.Path, err)
		}
		if appliedAt.After(snapshot.CapturedAt) {
			return fmt.Errorf(
				"Apply result %q was recorded after the checkpoint; the target may have been applied, refusing to release operation %s",
				entry.Path, record.OperationID,
			)
		}
		if path.Base(entry.Path) == sealed+".json" {
			sealedFound = true
			if architectureV2ComponentVersion(probe.Executor.Version) != record.Prior.Version {
				return fmt.Errorf(
					"the sealed Apply result was produced by %s, not the signed prior release %s",
					probe.Executor.Version, record.Prior.Version,
				)
			}
		}
	}
	if !sealedFound {
		return errors.New("the checkpoint's sealed Apply result is missing; refusing to release the prepared operation")
	}
	if record.Target.Version != record.Prior.Version && record.Target.Authority == "" {
		kit, err := loadWorkspaceKit(workspace)
		if err != nil {
			return fmt.Errorf("load StackKit identity: %w", err)
		}
		_, installDir, err := appliedPublicUpgradeReleasePath(workspace, kit, record.Target.Version, currentReleasePlatform())
		if err != nil {
			return err
		}
		if _, statErr := os.Lstat(installDir); !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("the target release %s is installed under the workspace release cache or its state is unreadable; refusing to release operation %s", record.Target.Version, record.OperationID)
		}
	}
	return nil
}
