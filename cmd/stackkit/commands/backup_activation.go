package commands

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/spf13/cobra"
)

var restoreActivationOperationPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{7,127}$`)

func runNativeV2RestoreActivationCommand(
	cmd *cobra.Command,
	restoreResultID, requestedOperationID string,
	ownerApproved bool,
) error {
	if cmd == nil {
		return errors.New("native v2 restore activation command is required")
	}
	if !ownerApproved {
		return errors.New("native v2 restore activation requires --owner-approve")
	}
	if !nativeV2BackupDigestPattern.MatchString(strings.TrimSpace(restoreResultID)) {
		return errors.New("native v2 restore activation requires a sha256 restore-result ID")
	}
	operationID, err := normalizeRestoreActivationOperationID(requestedOperationID)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	workspace := getWorkDir()
	authority, err := inspectNativeV2BackupAuthorityForRequest(
		ctx, workspace, specFile,
	)
	if err != nil {
		return fmt.Errorf("backup restore activation authority: %w", err)
	}
	plan, manifest, err := readNativeV2RestorePlanManifest(
		ctx, workspace, authority.OutputRoot,
	)
	if err != nil {
		return err
	}
	if plan.Binding() != authority.Lineage.Binding {
		return errors.New("backup restore activation Plan differs from current Apply authority")
	}
	restoreResult, err := backuplifecycle.LoadRestoreResult(
		workspace, strings.TrimSpace(restoreResultID),
	)
	if err != nil {
		return fmt.Errorf("load staged restore result: %w", err)
	}
	if restoreResult.AuthorizationLineage != authority.Lineage ||
		restoreResult.OwnerRef != authority.OwnerRef {
		return errors.New("staged restore result differs from the current local Owner and Apply authority")
	}
	runtime, err := restoreactivation.NewDockerRuntime(workspace)
	if err != nil {
		return err
	}
	service, err := restoreactivation.NewService(
		runtime,
		nativeV2RestoreRecoveryResolver(ctx, workspace),
	)
	if err != nil {
		return err
	}
	backupService, err := newNativeV2BackupService(authority)
	if err != nil {
		return err
	}
	lifecycleRuns, err := beginArchitectureV2ApplicationLifecyclesWithID(
		workspace,
		plan,
		"restore",
		"stackkit.restore",
		"",
		operationID,
		time.Now().UTC(),
	)
	if err != nil {
		return err
	}
	result, err := service.Activate(ctx, restoreactivation.ActivateInput{
		WorkspaceRoot:  workspace,
		OperationID:    operationID,
		OwnerApproved:  ownerApproved,
		Plan:           plan,
		Manifest:       manifest,
		RestoreResult:  restoreResult,
		CurrentLineage: authority.Lineage,
		CreateSafetySnapshot: func(
			snapshotContext context.Context,
			_ string,
		) (backuplifecycle.SnapshotAnchor, error) {
			return backupService.Run(snapshotContext, backuplifecycle.RunInput{
				OwnerRef: authority.OwnerRef, AuthorityRef: authority.AuthorityRef,
				Lineage: authority.Lineage,
				PolicyArtifact: append(
					[]byte(nil), authority.PolicyArtifact...,
				),
				OperationID:     safetySnapshotOperationID(operationID),
				ProtectRecovery: true,
			})
		},
		VerifyLive: nativeV2RestoreActivationVerifier(
			workspace, restoreResult,
		),
		FinalizeResult: func(_ context.Context, finalized restoreactivation.Result, _ error) error {
			evidence, evidenceRef, finalizeErr := restoreActivationApplicationLifecycleEvidence(workspace, finalized)
			if finalizeErr != nil {
				return finalizeErr
			}
			if finalized.Status == "recovered" {
				return recoverArchitectureV2ApplicationLifecycles(
					workspace, lifecycleRuns,
					"restore activation failed; the prior application state was restored automatically",
					evidenceRef, evidence, time.Now().UTC(), nil,
				)
			}
			return succeedArchitectureV2ApplicationLifecycles(
				workspace, lifecycleRuns, evidence, time.Now().UTC(),
			)
		},
	})
	if err != nil {
		var recovered *restoreactivation.ActivationRecoveredError
		if errors.As(err, &recovered) {
			return err
		}
		return requireArchitectureV2ApplicationLifecycleRecovery(
			workspace,
			lifecycleRuns,
			"restore activation failed and requires explicit recovery review",
			"urn:stackkit:restore-activation:"+operationID,
			time.Now().UTC(),
			err,
		)
	}
	return emitRestoreActivationResult(cmd, result)
}

func runNativeV2RestoreRecoveryCommand(
	cmd *cobra.Command,
	operationID string,
	ownerApproved bool,
) error {
	if cmd == nil {
		return errors.New("native v2 restore recovery command is required")
	}
	if !ownerApproved {
		return errors.New("native v2 restore recovery requires --owner-approve")
	}
	operationID = strings.TrimSpace(operationID)
	if !restoreActivationOperationPattern.MatchString(operationID) {
		return errors.New("native v2 restore recovery requires an exact activation operation ID")
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	workspace := getWorkDir()
	runtime, err := restoreactivation.NewDockerRuntime(workspace)
	if err != nil {
		return err
	}
	var (
		recoveryRestoreResult backuplifecycle.RestoreResult
		recoveryPlan          generationartifact.VerifiedPlan
	)
	resolver := func(
		resolveContext context.Context,
		journal lifecyclemutation.RestoreActivationAuthority,
	) (restoreactivation.Authority, error) {
		var resolveErr error
		recoveryRestoreResult, resolveErr = backuplifecycle.LoadRestoreResult(
			workspace, journal.RestoreResultID,
		)
		if resolveErr != nil {
			return restoreactivation.Authority{}, resolveErr
		}
		plan, manifest, resolveErr := readNativeV2RestorePlanManifest(
			resolveContext, workspace, "",
		)
		if resolveErr != nil {
			return restoreactivation.Authority{}, resolveErr
		}
		recoveryPlan = plan
		return restoreactivation.DeriveAuthority(
			workspace, plan, manifest, recoveryRestoreResult, journal.OperationID,
		)
	}
	service, err := restoreactivation.NewService(runtime, resolver)
	if err != nil {
		return err
	}
	result, err := service.Recover(ctx, restoreactivation.RecoverInput{
		WorkspaceRoot: workspace,
		OperationID:   operationID,
		OwnerApproved: ownerApproved,
		VerifyLive: func(verifyContext context.Context) (
			restoreactivation.LiveVerification,
			error,
		) {
			if recoveryRestoreResult.ID == "" {
				return restoreactivation.LiveVerification{},
					errors.New("restore recovery authority was not resolved")
			}
			return nativeV2RestoreActivationVerifier(
				workspace, recoveryRestoreResult,
			)(verifyContext)
		},
		FinalizeResult: func(_ context.Context, finalized restoreactivation.Result, _ error) error {
			evidence, _, finalizeErr := restoreActivationApplicationLifecycleEvidence(workspace, finalized)
			if finalizeErr != nil {
				return finalizeErr
			}
			terminalStatus := applicationlifecycle.StatusSucceeded
			if finalized.Status == "recovered" {
				terminalStatus = applicationlifecycle.StatusRecovered
			}
			return completeExistingApplicationLifecycles(
				workspace, recoveryPlan, operationID, terminalStatus, evidence, time.Now().UTC(),
			)
		},
	})
	if err != nil {
		return err
	}
	return emitRestoreActivationResult(cmd, result)
}

func restoreActivationApplicationLifecycleEvidence(
	workspace string,
	result restoreactivation.Result,
) ([]applicationlifecycle.Evidence, string, error) {
	resultRef, resultDigest, err := restoreactivation.ResultEvidence(workspace, result)
	if err != nil {
		return nil, "urn:stackkit:restore-activation:" + result.OperationID, err
	}
	snapshotRef, err := backuplifecycle.SnapshotAnchorEvidenceRef(result.SafetySnapshotID)
	if err != nil {
		return nil, resultRef, err
	}
	return []applicationlifecycle.Evidence{
		{Kind: "snapshot-anchor", Ref: snapshotRef, Digest: result.SafetySnapshotID},
		{Kind: "restore-result", Ref: resultRef, Digest: resultDigest},
		{
			Kind: "owner-observation", Ref: resultRef + "#verification",
			Digest: resultDigest,
		},
	}, resultRef, nil
}

func readNativeV2RestorePlanManifest(
	ctx context.Context,
	workspace, expectedOutputRoot string,
) (generationartifact.VerifiedPlan, generationartifact.ArtifactManifest, error) {
	var inspection generationartifact.PlanInspection
	gate := newArchitectureV2ExecutionGate()
	handled, err := gate.preflight(
		workspace, specFile, architectureV2Plan,
		architectureV2ExecutionCLIOptions{
			context: ctx,
			inspectionSink: func(value generationartifact.PlanInspection) error {
				inspection = value
				return nil
			},
		},
	)
	if err != nil {
		return generationartifact.VerifiedPlan{}, generationartifact.ArtifactManifest{}, err
	}
	if !handled ||
		(expectedOutputRoot != "" && inspection.OutputRoot != expectedOutputRoot) ||
		inspection.Readiness.Generation.Status != "ready" ||
		len(inspection.Readiness.Generation.Blockers) != 0 {
		return generationartifact.VerifiedPlan{}, generationartifact.ArtifactManifest{},
			errors.New("restore activation requires the exact generation-ready local Plan")
	}
	reader, err := gate.newAuthority()
	if err != nil {
		return generationartifact.VerifiedPlan{}, generationartifact.ArtifactManifest{}, err
	}
	if closer, ok := reader.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}
	planPath := filepath.Join(
		workspace, filepath.FromSlash(inspection.OutputRoot),
		".stackkit", "resolved-plan.json",
	)
	plan, err := reader.ReadCanonicalPlan(planPath)
	if err != nil {
		return generationartifact.VerifiedPlan{}, generationartifact.ArtifactManifest{}, err
	}
	if plan.Binding() != inspection.Binding {
		return generationartifact.VerifiedPlan{}, generationartifact.ArtifactManifest{},
			errors.New("restore activation Plan differs from the current CUE resolution")
	}
	_, manifestPath, _ := plan.MetadataPaths(workspace)
	manifest, err := generationartifact.ReadManifest(manifestPath)
	if err != nil {
		return generationartifact.VerifiedPlan{}, generationartifact.ArtifactManifest{}, err
	}
	if err := generationartifact.VerifyManifest(plan, workspace, manifest); err != nil {
		return generationartifact.VerifiedPlan{}, generationartifact.ArtifactManifest{},
			fmt.Errorf("verify restore activation manifest: %w", err)
	}
	return plan, manifest, nil
}

func nativeV2RestoreRecoveryResolver(
	parent context.Context,
	workspace string,
) restoreactivation.RecoveryAuthorityResolver {
	return func(
		ctx context.Context,
		journal lifecyclemutation.RestoreActivationAuthority,
	) (restoreactivation.Authority, error) {
		if ctx == nil {
			ctx = parent
		}
		restoreResult, err := backuplifecycle.LoadRestoreResult(
			workspace, journal.RestoreResultID,
		)
		if err != nil {
			return restoreactivation.Authority{}, err
		}
		plan, manifest, err := readNativeV2RestorePlanManifest(
			ctx, workspace, "",
		)
		if err != nil {
			return restoreactivation.Authority{}, err
		}
		return restoreactivation.DeriveAuthority(
			workspace, plan, manifest, restoreResult, journal.OperationID,
		)
	}
}

func nativeV2RestoreActivationVerifier(
	workspace string,
	restoreResult backuplifecycle.RestoreResult,
) func(context.Context) (restoreactivation.LiveVerification, error) {
	return func(ctx context.Context) (restoreactivation.LiveVerification, error) {
		current, err := inspectNativeV2BackupAuthorityForRequest(
			ctx, workspace, specFile,
		)
		if err != nil {
			return restoreactivation.LiveVerification{}, err
		}
		if current.OwnerRef != restoreResult.OwnerRef ||
			current.Lineage != restoreResult.AuthorizationLineage {
			return restoreactivation.LiveVerification{},
				errors.New("restored runtime differs from the signed activation authority")
		}
		return verifyNativeV2BackupRestore(
			ctx, current, backuplifecycle.RestoreVerificationRequest{
				OwnerRef:             restoreResult.OwnerRef,
				AuthorizationLineage: restoreResult.AuthorizationLineage,
				SnapshotAnchorID:     restoreResult.SnapshotAnchorID,
				OperationID:          restoreResult.OperationID,
				StagingPath:          restoreResult.Request.StagingPath,
			},
		)
	}
}

func normalizeRestoreActivationOperationID(requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		random := make([]byte, 8)
		if _, err := rand.Read(random); err != nil {
			return "", err
		}
		requested = "restore-activate-" +
			time.Now().UTC().Format("20060102t150405z") + "-" +
			hex.EncodeToString(random)
	}
	if !restoreActivationOperationPattern.MatchString(requested) {
		return "", errors.New("--operation-id must be 8-128 lowercase portable non-secret characters")
	}
	return requested, nil
}

func safetySnapshotOperationID(operationID string) string {
	sum := sha256.Sum256([]byte(operationID))
	return "restore-safety-" + hex.EncodeToString(sum[:12])
}

func emitRestoreActivationResult(
	cmd *cobra.Command,
	result restoreactivation.Result,
) error {
	if backupOutputJSON {
		return writeCommandResult(cmd, cmd.CommandPath(), result)
	}
	_, err := fmt.Fprintf(
		cmd.OutOrStdout(),
		"Native v2 restore activation %s: %s\n",
		result.OperationID, result.Status,
	)
	return err
}
