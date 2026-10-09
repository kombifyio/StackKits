package commands

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

var restoreActivationOperationPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{7,127}$`)

func runNativeV2RestoreActivationCommand(
	cmd *cobra.Command,
	restoreResultID, requestedOperationID string,
	ownerApproved bool,
	expectedPlanHash string,
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
	// Reject admission drift before opening application lifecycle records; the
	// activation service repeats this comparison under the mutation lock.
	if err := plan.RequireExpectedPlanHash(expectedPlanHash); err != nil {
		return err
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
	bootstrapRuntime, err := newRestoreActivationBootstrapRuntime(runtime, workspace)
	if err != nil {
		return err
	}
	if err := bootstrapRuntime.bind(plan, manifest, operationID); err != nil {
		return err
	}
	service, err := restoreactivation.NewService(
		bootstrapRuntime,
		nativeV2RestoreRecoveryResolver(ctx, workspace),
	)
	if err != nil {
		return err
	}
	backupService, err := newNativeV2BackupService(authority)
	if err != nil {
		return err
	}
	var lifecycleRuns []architectureV2ApplicationLifecycleRun
	var result restoreactivation.Result
	err = func() error {
		var activateErr error
		result, activateErr = service.Activate(ctx, restoreactivation.ActivateInput{
			WorkspaceRoot:    workspace,
			OperationID:      operationID,
			ExpectedPlanHash: expectedPlanHash,
			OwnerApproved:    ownerApproved,
			Plan:             plan,
			Manifest:         manifest,
			RestoreResult:    restoreResult,
			CurrentLineage:   authority.Lineage,
			RevalidateCurrentAuthority: func(readContext context.Context) (generationartifact.VerifiedPlan, backuplifecycle.AuthorityLineage, error) {
				current, err := inspectNativeV2BackupAuthorityForRequest(readContext, workspace, specFile)
				if err != nil {
					return generationartifact.VerifiedPlan{}, backuplifecycle.AuthorityLineage{}, err
				}
				if !sameNativeV2BackupAuthority(authority, current) {
					return generationartifact.VerifiedPlan{}, backuplifecycle.AuthorityLineage{}, errors.New("restore activation authority changed while acquiring the lifecycle lock")
				}
				return current.Plan, current.Lineage, nil
			},
			PrepareMutation: func(prepareContext context.Context) (func() error, error) {
				var err error
				lifecycleRuns, err = beginArchitectureV2ApplicationLifecyclesWithID(workspace, plan, "restore", "stackkit.restore", "", operationID, time.Now().UTC())
				if err != nil {
					return nil, err
				}
				return prepareGameServersHeld(prepareContext, workspace, true)
			},
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
		return activateErr
	}()
	if err != nil {
		var recovered *restoreactivation.ActivationRecoveredError
		if errors.As(err, &recovered) {
			return err
		}
		var notStarted *restoreactivation.ActivationNotStartedError
		if errors.As(err, &notStarted) {
			return failArchitectureV2ApplicationLifecycles(
				workspace, lifecycleRuns,
				"restore activation did not start; live application state is unchanged",
				time.Now().UTC(), err,
			)
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
	expectedPlanHash string,
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
	if handled, err := runOriginalReleaseRestoreRecovery(cmd, workspace, operationID, expectedPlanHash); handled {
		return err
	}
	runtime, err := restoreactivation.NewDockerRuntime(workspace)
	if err != nil {
		return err
	}
	bootstrapRuntime, err := newRestoreActivationBootstrapRuntime(runtime, workspace)
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
		authority, deriveErr := restoreactivation.DeriveAuthority(
			workspace, plan, manifest, recoveryRestoreResult, journal.OperationID,
		)
		if deriveErr != nil {
			return restoreactivation.Authority{}, deriveErr
		}
		if bindErr := bootstrapRuntime.bind(plan, manifest, journal.OperationID); bindErr != nil {
			return restoreactivation.Authority{}, bindErr
		}
		return authority, nil
	}
	service, err := restoreactivation.NewService(bootstrapRuntime, resolver)
	if err != nil {
		return err
	}
	result, err := service.Recover(ctx, restoreactivation.RecoverInput{
		WorkspaceRoot:    workspace,
		OperationID:      operationID,
		ExpectedPlanHash: expectedPlanHash,
		OwnerApproved:    ownerApproved,
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

type originalRecoveryReleaseProof struct {
	Version            string                `json:"version"`
	Platform           releaseindex.Platform `json:"platform"`
	ArchiveSHA256      string                `json:"archiveSha256"`
	ReleaseIndexSHA256 string                `json:"releaseIndexSha256"`
}

// Historical recovery runs through an attested private copy of A, while the
// current signed B remains installed. The existing original journal is the
// only mutation authority; cache preparation is not a new activation.
func runOriginalReleaseRestoreRecovery(cmd *cobra.Command, workspace, operationID, expectedPlanHash string) (bool, error) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	read := func(name string) string {
		if cmd.Flags().Lookup(name) == nil {
			return ""
		}
		value, _ := cmd.Flags().GetString(name)
		return strings.TrimSpace(value)
	}
	tag, archive, index := read("original-release"), read("original-archive-sha256"), read("original-index-sha256")
	cacheDirectory := read("original-release-cache")
	if tag == "" && archive == "" && index == "" && cacheDirectory == "" {
		return false, nil
	}
	hexDigest := regexp.MustCompile(`^[a-f0-9]{64}$`)
	current, err := releaseindex.ExactTagForBuildVersion(version)
	if err != nil || !semver.IsValid(tag) || !strings.HasPrefix(tag, "v") || semver.Compare(tag, "v0.52.5") < 0 || semver.Compare(tag, current) > 0 ||
		!hexDigest.MatchString(archive) || !hexDigest.MatchString(index) || !nativeV2BackupDigestPattern.MatchString(expectedPlanHash) {
		return true, errors.New("original recovery requires an exact prior release, archive/index digests and original admitted plan")
	}
	// Inspect under the existing lifecycle lock, then release it so A can open
	// and revalidate the same original journal under that lock before effects.
	session, record, err := lifecyclemutation.OpenRestoreActivationRecovery(workspace, operationID)
	if err != nil {
		return true, err
	}
	owner, ownerErr := localevidence.LoadOwnerCustody(workspace)
	bound := ownerErr == nil && record.RestoreActivation != nil &&
		record.RestoreActivation.Authority.OwnerRef == owner.OwnerRef &&
		record.RestoreActivation.Authority.PlanHash == expectedPlanHash
	closeErr := session.Close()
	if !bound || closeErr != nil {
		return true, errors.Join(errors.New("original recovery differs from the signed local Owner journal"), ownerErr, closeErr)
	}
	receipt, err := resolveOriginalRecoveryInstallation(ctx, workspace, tag, archive, index, cacheDirectory)
	if err != nil {
		return true, err
	}
	err = withVerifiedPublicUpgradeExecutable(ctx, receipt, func(binary string) error {
		args := append(publicUpgradeCommandPrefix(workspace, specFile), "backup", "restore", "recover", operationID,
			"--rollback", "--owner-approve", "--json", "--expected-plan-hash", expectedPlanHash)
		if _, err := newUpgradeInspectionRunner().Run(ctx, binary, args, workspace); err != nil {
			return fmt.Errorf("original verified release recovery: %w", err)
		}
		// Verify persisted Owner-signed evidence, never a success string from
		// the subprocess. A itself rechecks the original journal under its lock.
		result, err := restoreactivation.ReadResult(workspace, operationID)
		if err != nil {
			return err
		}
		original := record.RestoreActivation.Authority
		if result.OperationID != operationID || result.PlanHash != expectedPlanHash || result.Verification.OwnerRef != original.OwnerRef ||
			result.RestoreResultID != original.RestoreResultID || result.SafetySnapshotID != original.SafetySnapshotID || result.ManagedVolumeSetHash != original.ManagedVolumeSetHash {
			return errors.New("original release recovery result differs from its signed journal")
		}
		if backupOutputJSON {
			return writeCommandResult(cmd, cmd.CommandPath(), struct {
				restoreactivation.Result
				OriginalRecoveryRelease originalRecoveryReleaseProof `json:"originalRecoveryRelease"`
			}{result, originalRecoveryReleaseProof{receipt.Version, receipt.Platform, receipt.ArchiveSHA256, receipt.IndexSHA256}})
		}
		return emitRestoreActivationResult(cmd, result)
	})
	return true, err
}

func resolveOriginalRecoveryInstallation(ctx context.Context, workspace, tag, archive, index string, cacheDirectories ...string) (releaseindex.Receipt, error) {
	kit, err := loadWorkspaceKit(workspace)
	if err != nil {
		return releaseindex.Receipt{}, err
	}
	platform := currentReleasePlatform()
	directory, err := releaseindex.CacheInstallationDirectory(filepath.Join(workspace, ".stackkit", "releases"), kit, tag, platform)
	if err != nil {
		return releaseindex.Receipt{}, err
	}
	cacheDirectory := ""
	if len(cacheDirectories) > 0 {
		cacheDirectory = strings.TrimSpace(cacheDirectories[0])
	}
	if cacheDirectory != "" && !filepath.IsAbs(cacheDirectory) {
		return releaseindex.Receipt{}, errors.New("original release cache directory must be absolute")
	}
	installer := releaseindex.Installer{Source: newPublicReleaseSource(), Attestations: newPublicAttestationVerifier()}
	if _, err := os.Lstat(directory); errors.Is(err, os.ErrNotExist) {
		if cacheDirectory != "" {
			// A configured retained node cache is an offline-only authority.
			// Missing or invalid evidence never permits public reacquisition.
			directory, err = releaseindex.CacheInstallationDirectory(cacheDirectory, kit, tag, platform)
			if err != nil {
				return releaseindex.Receipt{}, err
			}
		} else {
			resolved, err := (releaseindex.Resolver{Source: installer.Source, Attestations: installer.Attestations}).Resolve(ctx,
				releaseindex.ResolveRequest{Kit: kit, Target: tag, OS: platform.OS, Arch: platform.Arch})
			if err != nil {
				return releaseindex.Receipt{}, err
			}
			if resolved.Asset.Archive.SHA256 != archive || fmt.Sprintf("%x", sha256.Sum256(resolved.RawIndex)) != index {
				return releaseindex.Receipt{}, errors.New("published original release differs from its immutable admission")
			}
			if _, err := installer.Install(ctx, resolved, workspace); err != nil {
				return releaseindex.Receipt{}, err
			}
		}
	} else if err != nil {
		return releaseindex.Receipt{}, err
	}
	var receipt releaseindex.Receipt
	err = installer.InspectInstalled(ctx, directory, func(proof releaseindex.VerifiedInstallation) error {
		return proof.Inspect(func(current releaseindex.Receipt, _ releaseindex.Asset, _ io.Reader) error {
			if err := validateExpectedCurrentReleaseReceipt(current, kit, tag, platform); err != nil {
				return err
			}
			if current.ArchiveSHA256 != archive || current.IndexSHA256 != index {
				return errors.New("cached original release differs from its immutable admission")
			}
			receipt = current
			return nil
		})
	})
	return receipt, err
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
			true,
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
