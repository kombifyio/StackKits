//go:build !publisher

package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
)

const (
	publicUpgradeTransactionAPIVersion = "stackkit.upgrade-transaction/v1"
	publicUpgradeEventAPIVersion       = "stackkit.upgrade-event/v1"
	publicUpgradeTransactionRoot       = ".stackkit/upgrades/transactions"

	publicUpgradeRollbackNotRequired = "not-required,dataStaged"
	publicUpgradeRollbackRestored    = "runtime-restored,dataStaged"
	publicUpgradeRollbackFailed      = "rollback-failed,dataStaged"
)

var (
	newPublicUpgradeTransactionRunner = func() upgradelifecycle.Runner {
		return upgradelifecycle.ExecRunner{}
	}
	loadPublicUpgradeRecoveryCheckpoint = func(
		workspace, snapshotID string,
	) (upgradelifecycle.ExecutorStateSnapshot, error) {
		return (upgradelifecycle.ExecutorStateStore{}).Load(workspace, snapshotID)
	}
	stagePublicUpgradeRollbackData          = stagePublicUpgradeData
	revalidatePublicUpgradeCurrentAuthority = revalidatePublicUpgradeAuthority
	recoverPublicUpgradeExecutor            = func(
		ctx context.Context,
		workspace, snapshotID string,
		invoke upgradelifecycle.RecoveryCommand,
	) (upgradelifecycle.ExecutorStateRecoveryResult, error) {
		return (upgradelifecycle.ExecutorStateStore{}).Recover(
			ctx, workspace, snapshotID, invoke,
		)
	}
	withPublicUpgradeInstalledExecutable  = withVerifiedPublicUpgradeExecutable
	reverifyPublicUpgradeInstalledReceipt = verifyInstalledPublicUpgradeReceipt
	withPublicUpgradeTransactionLock      = withUpgradeTransactionLock
)

type publicUpgradeTransaction struct {
	APIVersion   string                         `json:"apiVersion"`
	OperationID  string                         `json:"operationId"`
	Status       string                         `json:"status"`
	FailedPhase  string                         `json:"failedPhase,omitempty"`
	Target       publicUpgradeExecution         `json:"target"`
	Rollback     publicUpgradeRollback          `json:"rollback"`
	CommittedAt  *time.Time                     `json:"committedAt,omitempty"`
	SuccessProof *publicUpgradeSuccessAuthority `json:"successProof,omitempty"`
}

type publicUpgradeExecution struct {
	ReleaseVersion     string `json:"releaseVersion"`
	PlanHash           string `json:"planHash,omitempty"`
	ManifestHash       string `json:"manifestHash,omitempty"`
	ApplyResultHash    string `json:"applyResultHash,omitempty"`
	EvidenceBundleHash string `json:"evidenceBundleHash,omitempty"`
	OwnerRef           string `json:"ownerRef,omitempty"`
	OwnerBindingHash   string `json:"ownerBindingHash,omitempty"`
	GenerateInvoked    bool   `json:"generateInvoked"`
	PlanVerified       bool   `json:"planVerified"`
	ApplyInvoked       bool   `json:"applyInvoked"`
	VerifyInvoked      bool   `json:"verifyInvoked"`
	Verified           bool   `json:"verified"`
}

type publicUpgradeRollback struct {
	Status                string `json:"status"`
	DataStaged            bool   `json:"dataStaged"`
	StagedRestoreResultID string `json:"stagedRestoreResultId,omitempty"`
	RecoverySnapshotID    string `json:"recoverySnapshotId"`
	PriorReleaseVersion   string `json:"priorReleaseVersion,omitempty"`
	PlanHash              string `json:"planHash,omitempty"`
	ApplyResultHash       string `json:"applyResultHash,omitempty"`
	Verified              bool   `json:"verified"`
}

type publicUpgradeSuccessAuthority struct {
	APIVersion         string    `json:"apiVersion"`
	OperationID        string    `json:"operationId"`
	TargetVersion      string    `json:"targetVersion"`
	TargetArchiveHash  string    `json:"targetArchiveHash"`
	PlanHash           string    `json:"planHash"`
	ManifestHash       string    `json:"manifestHash"`
	ApplyResultHash    string    `json:"applyResultHash"`
	EvidenceBundleHash string    `json:"evidenceBundleHash"`
	OwnerRef           string    `json:"ownerRef"`
	OwnerBindingHash   string    `json:"ownerBindingHash"`
	VerifiedAt         time.Time `json:"verifiedAt"`
}

type publicUpgradeEvent struct {
	SchemaVersion string    `json:"schemaVersion"`
	Time          time.Time `json:"time"`
	OperationID   string    `json:"operationId"`
	Phase         string    `json:"phase"`
	Status        string    `json:"status"`
}

type publicUpgradeRawCommandResult struct {
	SchemaVersion string          `json:"schemaVersion"`
	Command       string          `json:"command"`
	Status        string          `json:"status"`
	Data          json.RawMessage `json:"data"`
}

func runPublicUpgradeTransaction(
	ctx context.Context,
	workspace string,
	requestedSpec string,
	targetReceipt releaseindex.Receipt,
	inspection upgradelifecycle.Inspection,
	checkpoint publicUpgradeCheckpoint,
) (result publicUpgradeTransaction, err error) {
	result = publicUpgradeTransaction{
		APIVersion:  publicUpgradeTransactionAPIVersion,
		OperationID: checkpoint.OperationID,
		Status:      "pending",
		Target: publicUpgradeExecution{
			ReleaseVersion: targetReceipt.Version,
		},
		Rollback: publicUpgradeRollback{
			Status:             "not-started",
			RecoverySnapshotID: checkpoint.ExecutorStateSnapshotID,
		},
	}
	defer func() {
		if err == nil {
			return
		}
		if result.Status != "rolled-back" {
			result.Status = "failed"
		}
		if result.FailedPhase == "" {
			result.FailedPhase = "transaction"
		}
	}()
	if err := validatePublicUpgradeTargetReceipt(targetReceipt, inspection); err != nil {
		result.FailedPhase = "target-authority"
		return result, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	operationCtx, cancel := context.WithTimeout(ctx, backupLongOperationTimeout)
	defer cancel()
	err = withPublicUpgradeTransactionLock(
		workspace,
		func(transaction *confinedfs.Transaction) error {
			snapshot, loadErr := loadPublicUpgradeRecoveryCheckpoint(
				workspace, checkpoint.ExecutorStateSnapshotID,
			)
			if loadErr != nil {
				result.FailedPhase = "checkpoint-verify"
				return fmt.Errorf("verify rollback checkpoint before target side effects: %w", loadErr)
			}
			if snapshot.ID != checkpoint.ExecutorStateSnapshotID ||
				snapshot.OperationID != checkpoint.OperationID ||
				snapshot.KopiaSnapshotAnchor.ID != checkpoint.KopiaAnchorID {
				result.FailedPhase = "checkpoint-verify"
				return errors.New("rollback checkpoint differs from the exact upgrade operation")
			}
			result.Rollback.PriorReleaseVersion = snapshot.Release.Version
			emitPublicUpgradeTransactionEvent(checkpoint.OperationID, "rollback-data-stage", "started")

			return withPublicUpgradeInstalledExecutable(
				operationCtx,
				targetReceipt,
				func(targetBinary string) error {
					staged, stageErr := stagePublicUpgradeRollbackData(
						operationCtx, workspace, requestedSpec, checkpoint, snapshot,
					)
					if stageErr != nil {
						result.FailedPhase = "rollback-data-stage"
						emitPublicUpgradeTransactionEvent(checkpoint.OperationID, "rollback-data-stage", "failed")
						return fmt.Errorf("stage verified rollback data before target side effects: %w", stageErr)
					}
					result.Rollback.DataStaged = true
					result.Rollback.StagedRestoreResultID = staged.ID
					result.Rollback.Status = publicUpgradeRollbackNotRequired
					emitPublicUpgradeTransactionEvent(checkpoint.OperationID, "rollback-data-stage", "succeeded")
					if authorityErr := revalidatePublicUpgradeCurrentAuthority(
						operationCtx, workspace, requestedSpec, snapshot,
					); authorityErr != nil {
						result.FailedPhase = "current-authority-reverify"
						return fmt.Errorf(
							"reverify exact current authority before target generate: %w",
							authorityErr,
						)
					}

					targetErr := executePublicUpgradeRelease(
						operationCtx,
						newPublicUpgradeTransactionRunner(),
						targetBinary,
						workspace,
						requestedSpec,
						inspection,
						targetReceipt,
						snapshot.OwnerRef,
						snapshot.Lineage.OwnerBindingDigest,
						&result.Target,
						checkpoint.OperationID,
					)
					if targetErr == nil {
						if verifyErr := reverifyPublicUpgradeInstalledReceipt(
							operationCtx, targetReceipt,
						); verifyErr != nil {
							targetErr = &publicUpgradePhaseError{
								phase: "target-release-reverify",
								cause: fmt.Errorf(
									"reverify exact installed target after live verification: %w",
									verifyErr,
								),
							}
						}
					}
					if targetErr == nil {
						now := time.Now().UTC()
						proof := publicUpgradeSuccessAuthority{
							APIVersion:         publicUpgradeTransactionAPIVersion,
							OperationID:        checkpoint.OperationID,
							TargetVersion:      targetReceipt.Version,
							TargetArchiveHash:  "sha256:" + targetReceipt.ArchiveSHA256,
							PlanHash:           result.Target.PlanHash,
							ManifestHash:       result.Target.ManifestHash,
							ApplyResultHash:    result.Target.ApplyResultHash,
							EvidenceBundleHash: result.Target.EvidenceBundleHash,
							OwnerRef:           result.Target.OwnerRef,
							OwnerBindingHash:   result.Target.OwnerBindingHash,
							VerifiedAt:         now,
						}
						if commitErr := commitPublicUpgradeSuccess(transaction, proof); commitErr != nil {
							result.FailedPhase = "commit"
							targetErr = fmt.Errorf("commit verified upgrade authority: %w", commitErr)
						} else {
							result.Status = "succeeded"
							result.CommittedAt = &now
							result.SuccessProof = &proof
							emitPublicUpgradeTransactionEvent(checkpoint.OperationID, "upgrade", "succeeded")
							return nil
						}
					}

					var phaseErr *publicUpgradePhaseError
					if errors.As(targetErr, &phaseErr) {
						result.FailedPhase = phaseErr.phase
					} else if result.FailedPhase == "" {
						result.FailedPhase = "target"
					}
					result.Rollback.Status = publicUpgradeRollbackFailed
					rollbackErr := rollbackPublicUpgrade(
						operationCtx,
						workspace,
						requestedSpec,
						checkpoint,
						snapshot,
						&result,
					)
					if rollbackErr != nil {
						emitPublicUpgradeTransactionEvent(checkpoint.OperationID, "rollback", "failed")
						return errors.Join(targetErr, fmt.Errorf("automatic runtime rollback failed: %w", rollbackErr))
					}
					result.Status = "rolled-back"
					result.Rollback.Status = publicUpgradeRollbackRestored
					result.Rollback.Verified = true
					emitPublicUpgradeTransactionEvent(checkpoint.OperationID, "rollback", "succeeded")
					return &publicUpgradeRolledBackError{
						phase: result.FailedPhase,
						cause: targetErr,
					}
				},
			)
		},
	)
	return result, err
}

func executePublicUpgradeRelease(
	ctx context.Context,
	runner upgradelifecycle.Runner,
	binary, workspace, requestedSpec string,
	inspection upgradelifecycle.Inspection,
	targetReceipt releaseindex.Receipt,
	expectedOwnerRef, expectedOwnerBindingHash string,
	result *publicUpgradeExecution,
	operationID string,
) error {
	if runner == nil || result == nil {
		return errors.New("target execution runner and result are required")
	}
	common := publicUpgradeCommandPrefix(workspace, requestedSpec)
	result.GenerateInvoked = true
	emitPublicUpgradeTransactionEvent(operationID, "target-generate", "started")
	if _, err := runner.Run(ctx, binary, append(common, "generate"), workspace); err != nil {
		emitPublicUpgradeTransactionEvent(operationID, "target-generate", "failed")
		return &publicUpgradePhaseError{phase: "target-generate", cause: err}
	}
	emitPublicUpgradeTransactionEvent(operationID, "target-generate", "succeeded")

	rawPlan, err := runner.Run(ctx, binary, append(common, "plan", "--json"), workspace)
	if err != nil {
		return &publicUpgradePhaseError{phase: "target-plan", cause: err}
	}
	var plan generationartifact.PlanInspection
	if err := decodeUpgradeExactJSON(rawPlan, &plan); err != nil {
		return &publicUpgradePhaseError{phase: "target-plan", cause: fmt.Errorf("decode exact target plan: %w", err)}
	}
	if err := validateExecutedUpgradePlan(plan, inspection); err != nil {
		return &publicUpgradePhaseError{phase: "target-plan", cause: err}
	}
	result.PlanHash = plan.Binding.PlanHash
	result.ManifestHash = plan.Manifest.Hash
	result.PlanVerified = true
	emitPublicUpgradeTransactionEvent(operationID, "target-plan", "succeeded")

	result.ApplyInvoked = true
	emitPublicUpgradeTransactionEvent(operationID, "target-apply", "started")
	if _, err := runner.Run(ctx, binary, append(common, "apply", "--auto-approve"), workspace); err != nil {
		emitPublicUpgradeTransactionEvent(operationID, "target-apply", "failed")
		return &publicUpgradePhaseError{phase: "target-apply", cause: err}
	}
	emitPublicUpgradeTransactionEvent(operationID, "target-apply", "succeeded")

	result.VerifyInvoked = true
	emitPublicUpgradeTransactionEvent(operationID, "target-verify", "started")
	rawVerify, err := runner.Run(ctx, binary, append(common, "verify", "--json"), workspace)
	if err != nil {
		emitPublicUpgradeTransactionEvent(operationID, "target-verify", "failed")
		return &publicUpgradePhaseError{phase: "target-verify", cause: err}
	}
	report, err := decodeAndValidateUpgradeVerify(
		rawVerify, plan.Binding.PlanHash, targetReceipt,
		expectedOwnerRef, expectedOwnerBindingHash,
	)
	if err != nil {
		emitPublicUpgradeTransactionEvent(operationID, "target-verify", "failed")
		return &publicUpgradePhaseError{phase: "target-verify", cause: err}
	}
	result.ApplyResultHash = report.Apply.ResultHash
	result.EvidenceBundleHash = report.Apply.EvidenceBundleHash
	result.OwnerRef = report.Owner.OwnerRef
	result.OwnerBindingHash = report.Owner.OwnerBindingDigest
	result.Verified = true
	emitPublicUpgradeTransactionEvent(operationID, "target-verify", "succeeded")
	return nil
}

func rollbackPublicUpgrade(
	ctx context.Context,
	workspace, requestedSpec string,
	checkpoint publicUpgradeCheckpoint,
	snapshot upgradelifecycle.ExecutorStateSnapshot,
	transaction *publicUpgradeTransaction,
) error {
	transaction.FailedPhase = publicUpgradeFailurePhase(transaction.FailedPhase)
	emitPublicUpgradeTransactionEvent(checkpoint.OperationID, "rollback", "started")
	recovered, err := recoverPublicUpgradeExecutor(
		ctx,
		workspace,
		checkpoint.ExecutorStateSnapshotID,
		func(
			recoveryContext context.Context,
			priorBinary string,
			recoveredSnapshot upgradelifecycle.ExecutorStateSnapshot,
		) error {
			if recoveredSnapshot.ID != snapshot.ID ||
				!reflect.DeepEqual(recoveredSnapshot.Release, snapshot.Release) {
				return errors.New("recovered executor snapshot differs from the pre-target authority")
			}
			runner := newPublicUpgradeTransactionRunner()
			common := publicUpgradeCommandPrefix(workspace, requestedSpec)
			if _, runErr := runner.Run(
				recoveryContext, priorBinary, append(common, "generate"), workspace,
			); runErr != nil {
				return fmt.Errorf("prior release generate: %w", runErr)
			}
			rawPlan, runErr := runner.Run(
				recoveryContext, priorBinary, append(common, "plan", "--json"), workspace,
			)
			if runErr != nil {
				return fmt.Errorf("prior release plan: %w", runErr)
			}
			var plan generationartifact.PlanInspection
			if runErr := decodeUpgradeExactJSON(rawPlan, &plan); runErr != nil {
				return fmt.Errorf("decode prior release plan: %w", runErr)
			}
			if runErr := validateRecoveredUpgradePlan(plan, snapshot); runErr != nil {
				return runErr
			}
			if _, runErr := runner.Run(
				recoveryContext, priorBinary, append(common, "apply", "--auto-approve"), workspace,
			); runErr != nil {
				return fmt.Errorf("prior release apply: %w", runErr)
			}
			rawVerify, runErr := runner.Run(
				recoveryContext, priorBinary, append(common, "verify", "--json"), workspace,
			)
			if runErr != nil {
				return fmt.Errorf("prior release verify: %w", runErr)
			}
			priorReceipt := releaseindex.Receipt{
				SchemaVersion: releaseindex.ReceiptSchemaVersion,
				Kit:           snapshot.Release.Kit, Version: snapshot.Release.Version,
				Channel: snapshot.Release.Channel, Platform: snapshot.Release.Platform,
				ArchiveSHA256: strings.TrimPrefix(snapshot.Release.ArchiveSHA256, "sha256:"),
			}
			report, runErr := decodeAndValidateUpgradeVerify(
				rawVerify, snapshot.Lineage.Binding.PlanHash, priorReceipt,
				snapshot.OwnerRef, snapshot.Lineage.OwnerBindingDigest,
			)
			if runErr != nil {
				return fmt.Errorf("validate prior release verification: %w", runErr)
			}
			transaction.Rollback.PlanHash = report.PlanHash
			transaction.Rollback.ApplyResultHash = report.Apply.ResultHash
			return nil
		},
	)
	if err != nil {
		return err
	}
	if recovered.SnapshotID != checkpoint.ExecutorStateSnapshotID ||
		recovered.KopiaSnapshotAnchor.ID != checkpoint.KopiaAnchorID {
		return errors.New("completed executor recovery differs from the exact rollback checkpoint")
	}
	transaction.Rollback.PriorReleaseVersion = recovered.Release.Version
	return nil
}

func stagePublicUpgradeData(
	ctx context.Context,
	workspace, requestedSpec string,
	checkpoint publicUpgradeCheckpoint,
	snapshot upgradelifecycle.ExecutorStateSnapshot,
) (backuplifecycle.RestoreResult, error) {
	authority, err := inspectNativeV2BackupAuthority(ctx, workspace, requestedSpec)
	if err != nil {
		return backuplifecycle.RestoreResult{}, err
	}
	if err := validatePublicUpgradeCurrentAuthority(authority, snapshot); err != nil {
		return backuplifecycle.RestoreResult{}, err
	}
	if snapshot.KopiaSnapshotAnchor.ID != checkpoint.KopiaAnchorID {
		return backuplifecycle.RestoreResult{}, errors.New(
			"current Kopia anchor differs from the upgrade checkpoint",
		)
	}
	raw, err := continueNativeV2BackupProduction(
		ctx,
		nativeV2BackupRestore,
		authority,
		nativeV2BackupRequest{
			OperationID:      "restore-" + checkpoint.OperationID,
			SnapshotAnchorID: checkpoint.KopiaAnchorID,
			OwnerApproved:    true,
		},
	)
	if err != nil {
		return backuplifecycle.RestoreResult{}, err
	}
	result, ok := raw.(backuplifecycle.RestoreResult)
	if !ok ||
		result.SnapshotAnchorID != checkpoint.KopiaAnchorID ||
		result.RecoveryAnchor.Mode != "verified-staging-only" ||
		!result.Receipt.RepositoryContentVerified ||
		!result.Verification.ServicesVerified {
		return backuplifecycle.RestoreResult{}, errors.New(
			"rollback data staging did not return the exact verified staging-only result",
		)
	}
	return result, nil
}

func revalidatePublicUpgradeAuthority(
	ctx context.Context,
	workspace, requestedSpec string,
	snapshot upgradelifecycle.ExecutorStateSnapshot,
) error {
	authority, err := inspectNativeV2BackupAuthority(ctx, workspace, requestedSpec)
	if err != nil {
		return err
	}
	return validatePublicUpgradeCurrentAuthority(authority, snapshot)
}

func validatePublicUpgradeCurrentAuthority(
	authority nativeV2BackupAuthority,
	snapshot upgradelifecycle.ExecutorStateSnapshot,
) error {
	if authority.Lineage != snapshot.Lineage ||
		authority.OwnerRef != snapshot.OwnerRef {
		return errors.New(
			"current backup authority differs from the verified rollback checkpoint",
		)
	}
	return nil
}

func withVerifiedPublicUpgradeExecutable(
	ctx context.Context,
	receipt releaseindex.Receipt,
	invoke func(string) error,
) error {
	if invoke == nil {
		return errors.New("verified target executable callback is required")
	}
	return (releaseindex.Installer{
		Attestations: newPublicAttestationVerifier(),
	}).InspectInstalled(ctx, receipt.InstallDir, func(proof releaseindex.VerifiedInstallation) error {
		var verifiedReceipt releaseindex.Receipt
		if err := proof.Inspect(func(
			current releaseindex.Receipt,
			_ releaseindex.Asset,
			_ io.Reader,
		) error {
			verifiedReceipt = current
			return nil
		}); err != nil {
			return err
		}
		if !reflect.DeepEqual(verifiedReceipt, receipt) {
			return errors.New("verified installed target receipt changed before execution")
		}
		executable, err := upgradelifecycle.RecoveryExecutableFromVerifiedRelease(proof)
		if err != nil {
			return err
		}
		tempRoot, err := os.MkdirTemp("", "stackkit-upgrade-target-")
		if err != nil {
			return fmt.Errorf("create private target executable directory: %w", err)
		}
		defer os.RemoveAll(tempRoot)
		binary := filepath.Join(tempRoot, executorRecoveryBinaryPath(receipt.Platform))
		if err := os.WriteFile(binary, executable, 0o700); err != nil {
			return fmt.Errorf("materialize exact verified target executable: %w", err)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(binary, 0o700); err != nil {
				return fmt.Errorf("make exact verified target executable private: %w", err)
			}
		}
		if err := requireUpgradeTargetBinary(binary); err != nil {
			return err
		}
		return invoke(binary)
	})
}

func verifyInstalledPublicUpgradeReceipt(
	ctx context.Context,
	receipt releaseindex.Receipt,
) error {
	return (releaseindex.Installer{
		Attestations: newPublicAttestationVerifier(),
	}).InspectInstalled(ctx, receipt.InstallDir, func(proof releaseindex.VerifiedInstallation) error {
		return proof.Inspect(func(
			current releaseindex.Receipt,
			_ releaseindex.Asset,
			_ io.Reader,
		) error {
			if !reflect.DeepEqual(current, receipt) {
				return errors.New(
					"verified installed target receipt changed after execution",
				)
			}
			return nil
		})
	})
}

func validateExecutedUpgradePlan(
	plan generationartifact.PlanInspection,
	shadow upgradelifecycle.Inspection,
) error {
	if err := validateUpgradePlanInspection(plan, "executed target"); err != nil {
		return err
	}
	if plan.Binding.PlanHash != shadow.Plan.TargetPlanHash ||
		plan.Manifest.Hash != shadow.Plan.TargetManifestHash {
		return errors.New(
			"executed target plan or manifest differs from the exact verified shadow inspection",
		)
	}
	return nil
}

func validateUpgradePlanInspection(
	inspection generationartifact.PlanInspection,
	label string,
) error {
	if inspection.APIVersion != generationartifact.PlanInspectionAPIVersion ||
		inspection.Kind != generationartifact.PlanInspectionKind ||
		inspection.VerifiedPhase != generationartifact.ExecutionPhaseGeneration ||
		inspection.ExecutorInvoked ||
		inspection.InfrastructureDiff != generationartifact.InfrastructureDiffNotAvailable ||
		inspection.Renderer != inspection.Binding.Renderer ||
		inspection.Manifest.Hash == "" ||
		inspection.Readiness.Generation.Status != "ready" ||
		len(inspection.Readiness.Generation.Blockers) != 0 ||
		inspection.Readiness.Apply.Status != "ready" ||
		len(inspection.Readiness.Apply.Blockers) != 0 {
		return fmt.Errorf("%s plan inspection is invalid or not generation/apply ready", label)
	}
	manifest := generationartifact.ArtifactManifest{
		APIVersion: generationartifact.ArtifactManifestAPIVersion,
		Kind:       generationartifact.ArtifactManifestKind,
		Binding:    inspection.Binding,
		Artifacts:  inspection.Manifest.Artifacts,
	}
	hash, err := manifest.Hash()
	if err != nil || hash != inspection.Manifest.Hash {
		return fmt.Errorf("%s plan inspection has an invalid manifest projection", label)
	}
	return nil
}

func validateRecoveredUpgradePlan(
	plan generationartifact.PlanInspection,
	snapshot upgradelifecycle.ExecutorStateSnapshot,
) error {
	if err := validateUpgradePlanInspection(plan, "recovered prior target"); err != nil {
		return err
	}
	if plan.Binding.PlanHash != snapshot.Lineage.Binding.PlanHash ||
		plan.Manifest.Hash != snapshot.Lineage.ManifestHash {
		return errors.New(
			"recovered prior plan or manifest differs from the captured current-state authority",
		)
	}
	return nil
}

func decodeAndValidateUpgradeVerify(
	raw []byte,
	planHash string,
	receipt releaseindex.Receipt,
	expectedOwnerRef, expectedOwnerBindingHash string,
) (architectureV2VerifyReport, error) {
	var envelope publicUpgradeRawCommandResult
	if err := decodeUpgradeExactJSON(raw, &envelope); err != nil {
		return architectureV2VerifyReport{}, fmt.Errorf("decode command result: %w", err)
	}
	if envelope.SchemaVersion != commandResultSchemaVersion ||
		envelope.Status != "success" ||
		envelope.Command != "stackkit verify" {
		return architectureV2VerifyReport{}, errors.New(
			"target verify did not return a successful stackkit.command-result/v1",
		)
	}
	var report architectureV2VerifyReport
	if err := decodeUpgradeExactJSON(envelope.Data, &report); err != nil {
		return architectureV2VerifyReport{}, fmt.Errorf("decode verify authority: %w", err)
	}
	if report.SchemaVersion != "stackkit.verify-result/v1" ||
		report.Offline ||
		report.PlanHash != planHash ||
		!nativeV2BackupDigestPattern.MatchString(report.Apply.ResultHash) ||
		!nativeV2BackupDigestPattern.MatchString(report.Apply.EvidenceBundleHash) ||
		report.Apply.AppliedAt.IsZero() ||
		report.Apply.RuntimeCount == 0 ||
		report.Apply.HealthCount == 0 ||
		strings.TrimSpace(report.Owner.OwnerRef) == "" ||
		report.Owner.OwnerRef != expectedOwnerRef ||
		strings.TrimSpace(report.Owner.PocketIDSubject) == "" ||
		!nativeV2BackupDigestPattern.MatchString(report.Owner.OwnerBindingDigest) ||
		report.Owner.OwnerBindingDigest != expectedOwnerBindingHash ||
		report.Runtime == nil ||
		report.Runtime.Status != "ready" ||
		report.Runtime.ServiceCount == 0 ||
		report.Runtime.ProbeCount == 0 {
		return architectureV2VerifyReport{}, errors.New(
			"target verify result does not prove the exact live Plan, Apply, Owner, and runtime closure",
		)
	}
	releaseMatched := false
	for _, candidate := range report.Releases {
		if candidate.Kit == receipt.Kit &&
			candidate.Version == receipt.Version &&
			candidate.Channel == receipt.Channel &&
			candidate.Platform == receipt.Platform &&
			candidate.ArchiveSHA256 == receipt.ArchiveSHA256 {
			releaseMatched = true
		}
	}
	if !releaseMatched {
		return architectureV2VerifyReport{}, errors.New(
			"target verify result does not include the exact applied release receipt",
		)
	}
	return report, nil
}

func decodeUpgradeExactJSON(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func requireUpgradeTargetBinary(binary string) error {
	info, err := os.Lstat(binary)
	if err != nil {
		return fmt.Errorf("exact target binary is missing: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("exact target binary must be a regular non-symlink file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return errors.New("exact target binary is not executable")
	}
	return nil
}

func validatePublicUpgradeTargetReceipt(
	receipt releaseindex.Receipt,
	inspection upgradelifecycle.Inspection,
) error {
	if receipt.SchemaVersion != releaseindex.ReceiptSchemaVersion ||
		receipt.Kit != inspection.Target.Kit ||
		receipt.Version != inspection.Target.Version ||
		receipt.Channel != inspection.Target.Channel ||
		receipt.Platform != inspection.Target.Platform ||
		receipt.ArchiveSHA256 != inspection.Target.ArchiveSHA256 ||
		strings.TrimSpace(receipt.InstallDir) == "" {
		return errors.New(
			"installed target receipt differs from the exact verified upgrade inspection",
		)
	}
	return nil
}

func publicUpgradeCommandPrefix(workspace, requestedSpec string) []string {
	if strings.TrimSpace(requestedSpec) == "" {
		requestedSpec = "stack-spec.yaml"
	}
	return []string{
		"--chdir", workspace,
		"--spec", requestedSpec,
		"--no-log",
	}
}

func withUpgradeTransactionLock(
	workspace string,
	execute func(*confinedfs.Transaction) error,
) (returnErr error) {
	if execute == nil {
		return errors.New("upgrade transaction callback is required")
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, transaction.Close()) }()
	if err := transaction.MkdirAll(publicUpgradeTransactionRoot, 0o700); err != nil {
		return err
	}
	lock, err := transaction.TryAcquireOutputLock(publicUpgradeTransactionRoot)
	if err != nil {
		return fmt.Errorf("acquire exclusive standard upgrade transaction lock: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, lock.Release()) }()
	return execute(transaction)
}

func commitPublicUpgradeSuccess(
	transaction *confinedfs.Transaction,
	proof publicUpgradeSuccessAuthority,
) error {
	if transaction == nil ||
		!publicUpgradeOperationIDPattern.MatchString(proof.OperationID) ||
		!nativeV2BackupDigestPattern.MatchString(proof.TargetArchiveHash) ||
		!nativeV2BackupDigestPattern.MatchString(proof.PlanHash) ||
		!nativeV2BackupDigestPattern.MatchString(proof.ManifestHash) ||
		!nativeV2BackupDigestPattern.MatchString(proof.ApplyResultHash) ||
		!nativeV2BackupDigestPattern.MatchString(proof.EvidenceBundleHash) ||
		!nativeV2BackupDigestPattern.MatchString(proof.OwnerBindingHash) ||
		strings.TrimSpace(proof.OwnerRef) == "" ||
		proof.VerifiedAt.IsZero() {
		return errors.New("verified upgrade success authority is incomplete")
	}
	canonical, err := resolvedplan.CanonicalJSON(proof)
	if err != nil {
		return err
	}
	return transaction.WriteFileExclusive(
		publicUpgradeTransactionRoot+"/"+proof.OperationID+".json",
		canonical,
		0o600,
	)
}

func emitPublicUpgradeTransactionEvent(operationID, phase, status string) {
	target := strings.TrimSpace(progressJSONL)
	if target == "" {
		return
	}
	event := publicUpgradeEvent{
		SchemaVersion: publicUpgradeEventAPIVersion,
		Time:          time.Now().UTC(),
		OperationID:   operationID,
		Phase:         phase,
		Status:        status,
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	data = append(data, '\n')
	if target == "-" {
		_, _ = os.Stdout.Write(data)
		return
	}
	file, err := os.OpenFile(
		filepath.Clean(target),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o600,
	)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, _ = file.Write(data)
}

func publicUpgradeFailurePhase(current string) string {
	if current != "" {
		return current
	}
	return "target"
}

type publicUpgradePhaseError struct {
	phase string
	cause error
}

func (err *publicUpgradePhaseError) Error() string {
	return fmt.Sprintf("%s failed: %v", err.phase, err.cause)
}

func (err *publicUpgradePhaseError) Unwrap() error {
	return err.cause
}

type publicUpgradeRolledBackError struct {
	phase string
	cause error
}

func (err *publicUpgradeRolledBackError) Error() string {
	return fmt.Sprintf(
		"upgrade failed during %s; prior runtime was restored and verified while data remains staged: %v",
		err.phase,
		err.cause,
	)
}

func (err *publicUpgradeRolledBackError) Unwrap() error {
	return err.cause
}
