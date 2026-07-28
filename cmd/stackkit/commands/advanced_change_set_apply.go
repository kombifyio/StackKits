//go:build !publisher

package commands

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/advancedcapability"
	"github.com/kombifyio/stackkits/internal/advancedchangeset"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
	"github.com/spf13/cobra"
)

const advancedMutationResultSchema = "stackkit.advanced-mutation/v1"

type advancedMutationRequest struct {
	CapabilityPath string
	CandidatePath  string
	ChangeSetID    string
	ChangeSetSHA   string
	Operation      string
}

type advancedMutationResult struct {
	SchemaVersion string                   `json:"schemaVersion"`
	Mode          string                   `json:"mode"`
	Operation     string                   `json:"operation"`
	ChangeSetID   string                   `json:"changeSetId"`
	ChangeSetSHA  string                   `json:"changeSetSha256"`
	PlanHash      string                   `json:"candidatePlanHash"`
	Checkpoint    publicUpgradeCheckpoint  `json:"checkpoint"`
	Transaction   publicUpgradeTransaction `json:"transaction"`
}

type verifiedAdvancedMutation struct {
	admission advancedChangeSetAdmission
	record    advancedchangeset.Record
	digest    string
}

func runAdvancedChangeSetApply(cmd *cobra.Command, _ []string) error {
	result, err := runAdvancedMutation(cmd, advancedMutationRequest{
		CapabilityPath: strings.TrimSpace(advancedCapabilityPath),
		CandidatePath:  strings.TrimSpace(advancedCandidatePath),
		ChangeSetID:    strings.TrimSpace(advancedChangeSetID),
		ChangeSetSHA:   strings.TrimSpace(advancedChangeSetDigest),
		Operation:      advancedcapability.OperationTerramateChangeSetApply,
	})
	if advancedChangeSetApplyJSON {
		status := "success"
		if err != nil {
			status = "failed"
			if _, ok := advancedcapability.Reason(err); ok {
				status = "denied"
			} else if _, ok := advancedchangeset.Reason(err); ok {
				status = "denied"
			}
		}
		if writeErr := writeCommandResultStatus(cmd, cmd.CommandPath(), status, result); writeErr != nil {
			return errors.Join(err, writeErr)
		}
	} else if err == nil {
		_, _ = fmt.Fprintf(
			cmd.OutOrStdout(),
			"Advanced change set applied and verified: %s\nRollback anchor: %s (Kopia %s)\n",
			result.ChangeSetID,
			result.Checkpoint.ExecutorStateSnapshotID,
			result.Checkpoint.KopiaAnchorID,
		)
	}
	return err
}

func runAdvancedMutation(cmd *cobra.Command, request advancedMutationRequest) (advancedMutationResult, error) {
	result := advancedMutationResult{
		SchemaVersion: advancedMutationResultSchema,
		Mode:          "advanced", Operation: request.Operation,
		ChangeSetID: request.ChangeSetID, ChangeSetSHA: request.ChangeSetSHA,
	}
	if request.CapabilityPath == "" || request.CandidatePath == "" ||
		!advancedSHA256Pattern.MatchString(request.ChangeSetID) ||
		!advancedSHA256Pattern.MatchString(request.ChangeSetSHA) {
		return result, &advancedcapability.Denial{
			Code:   advancedcapability.ReasonAdvancedChangeSetInvalid,
			Field:  "request",
			Detail: "--capability, --candidate-spec, --change-set sha256:<hex>, and --expect-sha256 sha256:<hex> are required",
		}
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	workspace := getWorkDir()
	now := time.Now().UTC().Truncate(time.Second)
	absoluteCapability := resolvePathFromWorkDir(workspace, request.CapabilityPath)
	absoluteCandidate := resolvePathFromWorkDir(workspace, request.CandidatePath)

	// This entire admission, including a fresh render and the exact stored byte
	// digest, happens before checkpoint creation or any other side effect.
	verified, err := verifyAdvancedMutation(
		ctx, workspace, absoluteCapability, absoluteCandidate,
		request.ChangeSetID, request.ChangeSetSHA, request.Operation, now,
	)
	if err != nil {
		return result, err
	}
	result.PlanHash = verified.admission.candidate.PlanHash

	kit, receipt, resolution, err := currentAdvancedReleaseAuthority(cmd, workspace)
	if err != nil {
		return result, err
	}
	var checkpoint publicUpgradeCheckpoint
	mutation, err := beginPublicUpgradeMutation(
		workspace,
		func() (lifecyclemutation.BeginRequest, error) {
			prepared, prepareErr := preparePublicUpgradeCheckpoint(
				ctx, workspace, kit, resolution,
			)
			if prepareErr != nil {
				return lifecyclemutation.BeginRequest{}, fmt.Errorf(
					"create mandatory Advanced rollback checkpoint: %w", prepareErr,
				)
			}
			snapshot, loadErr := loadPublicUpgradeRecoveryCheckpoint(
				workspace, prepared.ExecutorStateSnapshotID,
			)
			if loadErr != nil {
				return lifecyclemutation.BeginRequest{}, loadErr
			}
			var executableDigest string
			if executableErr := withPublicUpgradeInstalledExecutable(
				ctx, receipt, func(path string) error {
					var digestErr error
					executableDigest, digestErr = executableFileSHA256(path)
					return digestErr
				},
			); executableErr != nil {
				return lifecyclemutation.BeginRequest{}, executableErr
			}
			checkpoint = prepared
			return lifecyclemutation.BeginRequest{
				OperationID: prepared.OperationID,
				OwnerRef:    snapshot.OwnerRef,
				Checkpoint: lifecyclemutation.CheckpointAuthority{
					ExecutorStateSnapshotID: prepared.ExecutorStateSnapshotID,
					KopiaAnchorID:           prepared.KopiaAnchorID,
				},
				Target: lifecyclemutation.ReleaseAuthority{
					Version:          architectureV2ComponentVersion(receipt.Version),
					ArchiveSHA256:    "sha256:" + receipt.ArchiveSHA256,
					ExecutableSHA256: executableDigest,
				},
				Prior: lifecyclemutation.ReleaseAuthority{
					Version:          architectureV2ComponentVersion(snapshot.Release.Version),
					ArchiveSHA256:    snapshot.Release.ArchiveSHA256,
					ExecutableSHA256: snapshot.Executable.Blob.SHA256,
				},
			}, nil
		},
	)
	if err != nil {
		return result, err
	}
	defer mutation.Close()
	result.Checkpoint = checkpoint

	transaction, transactionErr := executeAdvancedMutation(
		ctx, workspace, receipt, checkpoint, mutation, request, verified,
		absoluteCapability, absoluteCandidate, now,
	)
	result.Transaction = transaction
	return result, transactionErr
}

func verifyAdvancedMutation(
	ctx context.Context,
	workspace, capabilityPath, candidatePath, changeSetID, expectedDigest, operation string,
	now time.Time,
) (verifiedAdvancedMutation, error) {
	admission, err := admitAdvancedChangeSetOperation(
		workspace, capabilityPath, candidatePath, now, operation,
	)
	if err != nil {
		return verifiedAdvancedMutation{}, err
	}
	capabilityDigest := sha256.Sum256(admission.capabilityRaw)
	verifyOwner := func(unsigned []byte, signature advancedchangeset.OwnerSignature) error {
		return localevidence.VerifyOwnerAdvancedChangeSet(
			workspace, unsigned, localevidence.OwnerAdvancedChangeSetSignature(signature),
		)
	}
	request := advancedchangeset.VerificationRequest{
		Now:                  now,
		CapabilityID:         admission.grant.CapabilityID,
		CapabilitySHA256:     "sha256:" + hex.EncodeToString(capabilityDigest[:]),
		KeyID:                admission.grant.KeyID,
		StackID:              admission.grant.StackID,
		OwnerRef:             admission.grant.OwnerRef,
		UIManagerRef:         admission.grant.UIManagerRef,
		RILRef:               admission.grant.RILRef,
		BaselinePlanHash:     admission.baseline.PlanHash,
		CandidatePlanHash:    admission.candidate.PlanHash,
		CapabilityExpiresAt:  admission.grant.ExpiresAt,
		VerifyOwnerSignature: verifyOwner,
	}
	record, raw, err := loadPinnedAdvancedChangeSet(workspace, changeSetID, expectedDigest, request)
	if err != nil {
		return verifiedAdvancedMutation{}, err
	}
	baseline, candidate, err := renderAdvancedAdmission(ctx, admission)
	if err != nil {
		return verifiedAdvancedMutation{}, err
	}
	baselineHash, err := advancedchangeset.RenderSHA256(baseline)
	if err != nil {
		return verifiedAdvancedMutation{}, err
	}
	candidateHash, err := advancedchangeset.RenderSHA256(candidate)
	if err != nil {
		return verifiedAdvancedMutation{}, err
	}
	if record.BaselineRenderSHA256 != baselineHash ||
		record.CandidateRenderSHA256 != candidateHash {
		return verifiedAdvancedMutation{}, &advancedchangeset.Error{
			Code:   advancedchangeset.ErrStale,
			Field:  "render",
			Detail: "fresh renderer output differs from the exact Owner-approved change set",
		}
	}
	digest := sha256.Sum256(raw)
	return verifiedAdvancedMutation{
		admission: admission, record: record,
		digest: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func loadPinnedAdvancedChangeSet(
	workspace, changeSetID, expectedDigest string,
	request advancedchangeset.VerificationRequest,
) (advancedchangeset.Record, []byte, error) {
	// Store.Load enforces confinement and owner-only ACLs before we inspect the
	// same content-addressed path. The second Verify closes the replacement
	// race and binds the exact returned bytes.
	if _, err := (advancedchangeset.Store{WorkspaceRoot: workspace}).Load(changeSetID, request); err != nil {
		return advancedchangeset.Record{}, nil, err
	}
	name := strings.TrimPrefix(changeSetID, "sha256:") + ".json"
	path := filepath.Join(workspace, ".stackkit", "advanced", "change-sets", name)
	if err := backupcustody.RequirePrivatePath(path, false); err != nil {
		return advancedchangeset.Record{}, nil, err
	}
	raw, err := readAdvancedRegular(path, maxAdvancedCandidateBytes, "Advanced change set")
	if err != nil {
		return advancedchangeset.Record{}, nil, err
	}
	sum := sha256.Sum256(raw)
	actual := "sha256:" + hex.EncodeToString(sum[:])
	if actual != expectedDigest {
		return advancedchangeset.Record{}, nil, &advancedchangeset.Error{
			Code:   advancedchangeset.ErrInvalid,
			Field:  "expect-sha256",
			Detail: "stored change-set bytes do not match the exact requested digest",
		}
	}
	record, err := advancedchangeset.Verify(raw, request)
	if err != nil {
		return advancedchangeset.Record{}, nil, err
	}
	if record.ChangeSetID != changeSetID {
		return advancedchangeset.Record{}, nil, errors.New("verified change-set content address changed")
	}
	if err := backupcustody.RequirePrivatePath(path, false); err != nil {
		return advancedchangeset.Record{}, nil, err
	}
	return record, raw, nil
}

func renderAdvancedAdmission(
	ctx context.Context,
	admission advancedChangeSetAdmission,
) (architecturev2renderer.RenderResult, architecturev2renderer.RenderResult, error) {
	tempRoot, err := os.MkdirTemp("", "stackkit-advanced-verify-*")
	if err != nil {
		return architecturev2renderer.RenderResult{}, architecturev2renderer.RenderResult{}, err
	}
	defer os.RemoveAll(tempRoot)
	if err := os.Chmod(tempRoot, 0o700); err != nil {
		return architecturev2renderer.RenderResult{}, architecturev2renderer.RenderResult{}, err
	}
	if err := backupcustody.ProtectPrivatePath(tempRoot, true); err != nil {
		return architecturev2renderer.RenderResult{}, architecturev2renderer.RenderResult{}, err
	}
	candidatePlan, err := admission.candidateService.VerifyCanonicalPlan(admission.candidate.CanonicalPlan)
	if err != nil {
		return architecturev2renderer.RenderResult{}, architecturev2renderer.RenderResult{}, err
	}
	candidatePlanPath, _, _ := candidatePlan.MetadataPaths(tempRoot)
	if _, err := admission.candidateService.PersistCanonicalPlan(candidatePlanPath, admission.candidate.CanonicalPlan); err != nil {
		return architecturev2renderer.RenderResult{}, architecturev2renderer.RenderResult{}, err
	}
	workspace, err := filepath.Abs(admission.workspace)
	if err != nil {
		return architecturev2renderer.RenderResult{}, architecturev2renderer.RenderResult{}, err
	}
	baselineAuth, err := admission.baselineService.AuthorizeGeneration(architecturev2.GenerationAuthorizationInput{
		Current: admission.baselineCurrent, WorkspaceRoot: filepath.Clean(workspace),
		Versions: advancedComponentVersions(),
	})
	if err != nil {
		return architecturev2renderer.RenderResult{}, architecturev2renderer.RenderResult{}, err
	}
	defer baselineAuth.Close()
	candidateAuth, err := admission.candidateService.AuthorizeGeneration(architecturev2.GenerationAuthorizationInput{
		Current: admission.candidateCurrent, WorkspaceRoot: filepath.Clean(tempRoot),
		Versions: advancedComponentVersions(),
	})
	if err != nil {
		return architecturev2renderer.RenderResult{}, architecturev2renderer.RenderResult{}, err
	}
	defer candidateAuth.Close()
	registry, err := architecturev2renderer.NewProductRegistry()
	if err != nil {
		return architecturev2renderer.RenderResult{}, architecturev2renderer.RenderResult{}, err
	}
	baseline, err := baselineAuth.Render(ctx, registry)
	if err != nil {
		return architecturev2renderer.RenderResult{}, architecturev2renderer.RenderResult{}, err
	}
	candidate, err := candidateAuth.Render(ctx, registry)
	return baseline, candidate, err
}

func currentAdvancedReleaseAuthority(
	cmd *cobra.Command, workspace string,
) (string, releaseindex.Receipt, releaseindex.Resolution, error) {
	kit, err := loadWorkspaceKit(workspace)
	if err != nil {
		return "", releaseindex.Receipt{}, releaseindex.Resolution{}, err
	}
	receipts, err := verifyWorkspaceReleaseReceipts(cmd, workspace)
	if err != nil {
		return "", releaseindex.Receipt{}, releaseindex.Resolution{}, err
	}
	receipt, err := currentDriftReconcileReceipt(receipts, kit)
	if err != nil {
		return "", releaseindex.Receipt{}, releaseindex.Resolution{}, err
	}
	resolution := releaseindex.Resolution{Asset: releaseindex.Asset{
		Kit: receipt.Kit, Version: receipt.Version, Channel: receipt.Channel,
		Platform: receipt.Platform, Archive: releaseindex.Blob{SHA256: receipt.ArchiveSHA256},
	}}
	return kit, receipt, resolution, nil
}

func executeAdvancedMutation(
	ctx context.Context,
	workspace string,
	receipt releaseindex.Receipt,
	checkpoint publicUpgradeCheckpoint,
	mutation publicUpgradeLifecycleSession,
	request advancedMutationRequest,
	initial verifiedAdvancedMutation,
	capabilityPath, candidatePath string,
	now time.Time,
) (result publicUpgradeTransaction, err error) {
	result = publicUpgradeTransaction{
		APIVersion:  publicUpgradeTransactionAPIVersion,
		OperationID: checkpoint.OperationID,
		Status:      "pending",
		Target:      publicUpgradeExecution{ReleaseVersion: receipt.Version},
		Rollback: publicUpgradeRollback{
			Status: "not-started", RecoverySnapshotID: checkpoint.ExecutorStateSnapshotID,
		},
	}
	operationCtx, cancel := context.WithTimeout(ctx, backupLongOperationTimeout)
	defer cancel()
	err = withPublicUpgradeTransactionLock(workspace, func(control *confinedfs.Transaction) error {
		snapshot, loadErr := loadPublicUpgradeRecoveryCheckpoint(
			workspace, checkpoint.ExecutorStateSnapshotID,
		)
		if loadErr != nil {
			return loadErr
		}
		result.Rollback.PriorReleaseVersion = snapshot.Release.Version
		staged, stageErr := stagePublicUpgradeRollbackData(
			operationCtx, workspace, specFile, checkpoint, snapshot,
		)
		if stageErr != nil {
			return stageErr
		}
		result.Rollback.DataStaged = true
		result.Rollback.StagedRestoreResultID = staged.ID
		result.Rollback.Status = publicUpgradeRollbackNotRequired
		if authorityErr := revalidatePublicUpgradeCurrentAuthority(
			operationCtx, workspace, specFile, snapshot,
		); authorityErr != nil {
			return authorityErr
		}

		// Offline capability, current Owner custody, candidate, signed record,
		// exact record bytes, and fresh renderer output are all revalidated
		// while the lifecycle lock is held and before target mutation.
		revalidated, verifyErr := verifyAdvancedMutation(
			operationCtx, workspace, capabilityPath, candidatePath,
			request.ChangeSetID, request.ChangeSetSHA, request.Operation, now,
		)
		if verifyErr != nil {
			return verifyErr
		}
		if !equalAdvancedAdmission(initial.admission, revalidated.admission) ||
			initial.record.ChangeSetID != revalidated.record.ChangeSetID ||
			initial.digest != revalidated.digest {
			return errors.New("Advanced authority changed after pre-side-effect admission")
		}

		targetErr := withPublicUpgradeInstalledExecutable(
			operationCtx, receipt, func(binary string) error {
				// Promoting intent is the first target side effect. The exact
				// prior StackSpec is already sealed in the rollback checkpoint.
				if writeErr := writeAdvancedCandidateIntent(
					workspace, specFile, revalidated.admission.candidateRaw,
				); writeErr != nil {
					return writeErr
				}
				return executeAdvancedTarget(
					operationCtx, binary, workspace, receipt, snapshot,
					revalidated.admission.candidate.PlanHash,
					mutation, checkpoint.OperationID, &result.Target,
				)
			},
		)
		if targetErr != nil {
			result.FailedPhase = "advanced-target"
			result.Rollback.Status = publicUpgradeRollbackFailed
			rollbackErr := rollbackPublicUpgrade(
				operationCtx, workspace, specFile, checkpoint, snapshot,
				mutation, control, &result,
			)
			if rollbackErr != nil {
				return errors.Join(targetErr, rollbackErr)
			}
			result.Status = "rolled-back"
			result.Rollback.Status = publicUpgradeRollbackRestored
			result.Rollback.Verified = true
			return &publicUpgradeRolledBackError{phase: result.FailedPhase, cause: targetErr}
		}
		if transitionErr := mutation.Transition(
			lifecyclemutation.PhaseTargetVerifySucceeded,
			lifecyclemutation.PhaseCommitStarted,
		); transitionErr != nil {
			return transitionErr
		}
		if transitionErr := mutation.Transition(
			lifecyclemutation.PhaseCommitStarted,
			lifecyclemutation.PhaseCommitSucceeded,
		); transitionErr != nil {
			return transitionErr
		}
		if completeErr := mutation.Complete(lifecyclemutation.StatusSucceeded); completeErr != nil {
			return completeErr
		}
		result.Status = "succeeded"
		committed := time.Now().UTC()
		result.CommittedAt = &committed
		return nil
	})
	if err != nil && result.Status != "rolled-back" {
		result.Status = "failed"
	}
	return result, err
}

func writeAdvancedCandidateIntent(workspace, requestedSpec string, raw []byte) (returnErr error) {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(workspace, resolvePathFromWorkDir(workspace, requestedSpec))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return errors.New("Advanced target StackSpec must remain beneath the workspace")
	}
	_, err = view.WriteAtomic0600(filepath.ToSlash(relative), bytes.Clone(raw))
	return err
}

func executeAdvancedTarget(
	ctx context.Context,
	binary, workspace string,
	receipt releaseindex.Receipt,
	snapshot upgradelifecycle.ExecutorStateSnapshot,
	candidatePlanHash string,
	mutation publicUpgradeLifecycleSession,
	operationID string,
	result *publicUpgradeExecution,
) error {
	runner := newPublicUpgradeTransactionRunner()
	common := publicUpgradeCommandPrefix(workspace, specFile)
	executableDigest, err := hashPublicUpgradeExecutable(binary)
	if err != nil {
		return err
	}
	generateNonce, err := mutation.BeginJoin(
		lifecyclemutation.PhasePrepared,
		lifecyclemutation.PhaseTargetGenerateStarted,
		"generate", architectureV2ComponentVersion(receipt.Version), executableDigest,
	)
	if err != nil {
		return err
	}
	result.GenerateInvoked = true
	if _, err := runner.Run(ctx, binary, append(
		append(common, lifecycleChildFlags(operationID, lifecyclemutation.PhaseTargetGenerateStarted, generateNonce)...),
		"generate",
	), workspace); err != nil {
		return err
	}
	if err := mutation.Transition(
		lifecyclemutation.PhaseTargetGenerateStarted,
		lifecyclemutation.PhaseTargetGenerateSucceeded,
	); err != nil {
		return err
	}
	rawPlan, err := runner.Run(ctx, binary, append(common, "plan", "--json"), workspace)
	if err != nil {
		return err
	}
	var plan generationartifact.PlanInspection
	if err := decodeUpgradeExactJSON(rawPlan, &plan); err != nil {
		return err
	}
	if plan.Binding.PlanHash != candidatePlanHash || strings.TrimSpace(plan.Manifest.Hash) == "" {
		return errors.New("generated Advanced target differs from the exact approved candidate")
	}
	result.PlanHash = plan.Binding.PlanHash
	result.ManifestHash = plan.Manifest.Hash
	result.PlanVerified = true

	applyNonce, err := mutation.BeginJoin(
		lifecyclemutation.PhaseTargetGenerateSucceeded,
		lifecyclemutation.PhaseTargetApplyStarted,
		"apply", architectureV2ComponentVersion(receipt.Version), executableDigest,
	)
	if err != nil {
		return err
	}
	result.ApplyInvoked = true
	if _, err := runner.Run(ctx, binary, append(
		append(common, lifecycleChildFlags(operationID, lifecyclemutation.PhaseTargetApplyStarted, applyNonce)...),
		"apply", "--auto-approve",
	), workspace); err != nil {
		return err
	}
	if err := mutation.Transition(
		lifecyclemutation.PhaseTargetApplyStarted,
		lifecyclemutation.PhaseTargetApplySucceeded,
	); err != nil {
		return err
	}
	verifyNonce, err := mutation.BeginJoin(
		lifecyclemutation.PhaseTargetApplySucceeded,
		lifecyclemutation.PhaseTargetVerifyStarted,
		"verify", architectureV2ComponentVersion(receipt.Version), executableDigest,
	)
	if err != nil {
		return err
	}
	result.VerifyInvoked = true
	rawVerify, err := runner.Run(ctx, binary, append(
		append(common, lifecycleChildFlags(operationID, lifecyclemutation.PhaseTargetVerifyStarted, verifyNonce)...),
		"verify", "--json",
	), workspace)
	if err != nil {
		return err
	}
	report, err := decodeAndValidateUpgradeVerify(
		rawVerify, candidatePlanHash, receipt,
		snapshot.OwnerRef, snapshot.Lineage.OwnerBindingDigest,
	)
	if err != nil {
		return err
	}
	result.ApplyResultHash = report.Apply.ResultHash
	result.EvidenceBundleHash = report.Apply.EvidenceBundleHash
	result.OwnerRef = report.Owner.OwnerRef
	result.OwnerBindingHash = report.Owner.OwnerBindingDigest
	result.Verified = true
	return mutation.Transition(
		lifecyclemutation.PhaseTargetVerifyStarted,
		lifecyclemutation.PhaseTargetVerifySucceeded,
	)
}
