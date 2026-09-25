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
	"github.com/kombifyio/stackkits/internal/advancedrollback"
	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/terramatehost"
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
	// ChangeSetResult is the stackkit.change-set-result/v1 report of the
	// Terramate orchestration of a change-set apply. It is present whenever
	// the orchestration started, including when it failed.
	ChangeSetResult *terramatehost.Report `json:"changeSetResult,omitempty"`
	// RollbackResult is the stackkit.rollback-result/v1 report of the
	// coordinated rollback a failed change set ran for a Terramate-target
	// checkpoint.
	RollbackResult *advancedrollback.Report `json:"rollbackResult,omitempty"`
}

type verifiedAdvancedMutation struct {
	admission advancedChangeSetAdmission
	record    advancedchangeset.Record
	digest    string
	candidate architecturev2renderer.RenderResult
}

// advancedTerramateFailedPhase marks a change set whose post-apply Terramate
// orchestration failed; the rollback path runs exactly as for a failed
// target generate, apply or verify.
const advancedTerramateFailedPhase = "advanced-terramate"

// advancedChangeSetRolloutPrefix names the rollout events of the Terramate
// orchestration: advanced.change-set.materialize-host, .run-order, .converge.
const advancedChangeSetRolloutPrefix = "advanced.change-set."

// advancedTerramateOrchestration runs the Terramate step of a change-set
// apply (docs/ARCHITECTURE.md "Advanced change sets through Terramate
// (Stage 1)"). The skeleton stays generate, plan, apply, verify through the
// installed release; after apply and before verify the parent materializes
// the local host project, proves the Terramate run order equals the graph and
// runs a detailed-exitcode `tofu plan` through `terramate run` in every
// affected local stack root. Exit 2 means the apply did not converge: the
// change set fails with advanced_change_set_not_converged and rolls back.
type advancedTerramateOrchestration struct {
	tools    terramatehost.Tools
	report   *terramatehost.Report
	rollback *advancedrollback.Report
}

// coordinatedRollback wraps the coordinated rollback of a failed change set
// and keeps its stackkit.rollback-result/v1 report. A mutation without
// Terramate orchestration has none.
func (orchestration *advancedTerramateOrchestration) coordinatedRollback(
	run func() (*advancedrollback.Report, error),
) func() error {
	if orchestration == nil {
		return nil
	}
	return func() error {
		report, err := run()
		orchestration.rollback = report
		return err
	}
}

func (orchestration *advancedTerramateOrchestration) step(
	workspace string, verified verifiedAdvancedMutation,
) func(context.Context) error {
	if orchestration == nil {
		return nil
	}
	return func(ctx context.Context) error {
		binding := verified.admission.owner.Binding
		layout, err := terramatehost.PlanFromArtifacts(
			verified.candidate.Artifacts(), binding.SiteRef, binding.NodeRef,
		)
		if err != nil {
			return err
		}
		report, err := terramatehost.Converge(ctx, terramatehost.ConvergeRequest{
			WorkspaceRoot: workspace, ChangeSetID: verified.record.ChangeSetID, Layout: layout,
			ExpectedManifestSHA256: verified.record.TerramateHostManifestSHA256,
			AffectedStacks:         verified.record.AffectedStacks, Tools: orchestration.tools,
			Event: func(phase, status string, attributes map[string]string) {
				if attributes == nil {
					attributes = map[string]string{}
				}
				attributes["changeSetId"] = verified.record.ChangeSetID
				rolloutEvent(advancedChangeSetRolloutPrefix+phase, status, "advanced change-set "+phase+" "+status, attributes)
			},
		})
		orchestration.report = &report
		return err
	}
}

// settleAdvancedTarget applies the rollback policy to a failed target: any
// failure of generate, plan, apply, the Terramate step or verify runs the
// existing public-upgrade rollback.
func settleAdvancedTarget(
	result *publicUpgradeTransaction, targetErr error, rollback func() error,
) error {
	if targetErr == nil {
		return nil
	}
	result.FailedPhase = "advanced-target"
	if _, terramateFailure := terramatehost.Reason(targetErr); terramateFailure {
		result.FailedPhase = advancedTerramateFailedPhase
	}
	result.Rollback.Status = publicUpgradeRollbackFailed
	if rollbackErr := rollback(); rollbackErr != nil {
		return errors.Join(targetErr, rollbackErr)
	}
	result.Status = "rolled-back"
	result.Rollback.Status = publicUpgradeRollbackRestored
	result.Rollback.Verified = true
	return &publicUpgradeRolledBackError{phase: result.FailedPhase, cause: targetErr}
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
	var orchestration *advancedTerramateOrchestration
	if request.Operation == advancedcapability.OperationTerramateChangeSetApply {
		// Missing packaged Terramate or OpenTofu fails closed before any
		// checkpoint or lifecycle side effect.
		tools, toolsErr := terramatehost.PackagedTools()
		if toolsErr != nil {
			return result, toolsErr
		}
		orchestration = &advancedTerramateOrchestration{tools: tools}
	}
	result.PlanHash = verified.admission.candidate.PlanHash
	candidatePlan, err := verified.admission.candidateService.VerifyCanonicalPlan(
		verified.admission.candidate.CanonicalPlan,
	)
	if err != nil {
		return result, fmt.Errorf("verify Advanced candidate lifecycle plan: %w", err)
	}
	lifecycleOperation, err := advancedApplicationLifecycleOperation(request.Operation)
	if err != nil {
		return result, err
	}
	applicationRuns, err := beginArchitectureV2ApplicationLifecycles(
		workspace,
		candidatePlan,
		"upgrade",
		lifecycleOperation,
		"",
		now,
	)
	if err != nil {
		return result, fmt.Errorf("begin Advanced application lifecycle: %w", err)
	}
	failApplications := func(message string, cause error) error {
		return failArchitectureV2ApplicationLifecycles(
			workspace, applicationRuns, message, time.Now().UTC(), cause,
		)
	}

	kit, receipt, resolution, err := currentAdvancedReleaseAuthority(cmd, workspace)
	if err != nil {
		return result, failApplications(
			"Advanced target release authority could not be established before mutation", err,
		)
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
		return result, failApplications(
			"mandatory Advanced rollback checkpoint could not be created before mutation", err,
		)
	}
	defer mutation.Close()
	result.Checkpoint = checkpoint

	transaction, transactionErr := executeAdvancedMutation(
		ctx, workspace, receipt, checkpoint, mutation, request, verified,
		absoluteCapability, absoluteCandidate, now, orchestration,
	)
	result.Transaction = transaction
	if orchestration != nil {
		result.ChangeSetResult = orchestration.report
		result.RollbackResult = orchestration.rollback
	}
	transactionErr = completeAdvancedApplicationLifecycles(
		workspace, applicationRuns, result, transactionErr,
	)
	return result, transactionErr
}

func advancedApplicationLifecycleOperation(operation string) (string, error) {
	switch operation {
	case advancedcapability.OperationTerramateChangeSetApply:
		return "stackkit.advanced.change-set.apply", nil
	case advancedcapability.OperationDriftReconcileAdvanced:
		return "stackkit.drift.reconcile.advanced", nil
	default:
		return "", &advancedcapability.Denial{
			Code:   advancedcapability.ReasonCapabilityOperationDenied,
			Field:  "operation",
			Detail: "operation has no bounded Application Lifecycle projection",
		}
	}
}

func completeAdvancedApplicationLifecycles(
	workspace string,
	runs []architectureV2ApplicationLifecycleRun,
	result advancedMutationResult,
	mutationErr error,
) error {
	if len(runs) == 0 {
		return mutationErr
	}
	resultRef, resultDigest, persistErr := persistAdvancedApplicationLifecycleResult(
		workspace, result,
	)
	if persistErr != nil {
		return requireArchitectureV2ApplicationLifecycleRecovery(
			workspace,
			runs,
			"Advanced mutation completed without durable application Owner evidence",
			"urn:stackkit:advanced-operation:"+result.Transaction.OperationID,
			time.Now().UTC(),
			errors.Join(mutationErr, persistErr),
		)
	}
	snapshotRef, snapshotErr := backuplifecycle.SnapshotAnchorEvidenceRef(
		result.Checkpoint.KopiaAnchorID,
	)
	if snapshotErr != nil {
		return requireArchitectureV2ApplicationLifecycleRecovery(
			workspace,
			runs,
			"Advanced rollback snapshot evidence could not be bound",
			resultRef,
			time.Now().UTC(),
			errors.Join(mutationErr, snapshotErr),
		)
	}
	evidence := []applicationlifecycle.Evidence{
		{
			Kind: "snapshot-anchor", Ref: snapshotRef,
			Digest: result.Checkpoint.KopiaAnchorID,
		},
		{Kind: "upgrade-result", Ref: resultRef, Digest: resultDigest},
		{
			Kind: "owner-observation", Ref: resultRef + "#advanced-authority",
			Digest: resultDigest,
		},
	}
	if mutationErr == nil {
		return succeedArchitectureV2ApplicationLifecycles(
			workspace, runs, evidence, time.Now().UTC(),
		)
	}
	if result.Transaction.Status == "rolled-back" &&
		result.Transaction.Rollback.Verified {
		evidence[2].Ref = resultRef + "#verified-rollback"
		return recoverArchitectureV2ApplicationLifecycles(
			workspace,
			runs,
			"Advanced target failed and the prior runtime was restored and verified",
			resultRef,
			evidence,
			time.Now().UTC(),
			mutationErr,
		)
	}
	return requireArchitectureV2ApplicationLifecycleRecovery(
		workspace,
		runs,
		"Advanced mutation failed without a verified automatic recovery",
		resultRef,
		time.Now().UTC(),
		mutationErr,
	)
}

func persistAdvancedApplicationLifecycleResult(
	workspace string,
	result advancedMutationResult,
) (string, string, error) {
	canonical, err := resolvedplan.CanonicalJSON(result)
	if err != nil {
		return "", "", fmt.Errorf("canonicalize Advanced lifecycle result: %w", err)
	}
	digest := architectureV2ApplicationLifecycleDigest(canonical)
	path := filepath.ToSlash(filepath.Join(
		".stackkit", "advanced", "results",
		strings.TrimPrefix(digest, "sha256:")+".json",
	))
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return "", "", fmt.Errorf("open Advanced lifecycle result workspace: %w", err)
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return "", "", fmt.Errorf("begin Advanced lifecycle result transaction: %w", err)
	}
	defer func() { _ = transaction.Close() }()
	if err := transaction.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", "", fmt.Errorf("create Advanced lifecycle result directory: %w", err)
	}
	if err := transaction.WriteFileExclusive(path, canonical, 0o600); err != nil {
		existing, info, readErr := transaction.ReadStable(path)
		if readErr != nil || !info.Mode().IsRegular() || !bytes.Equal(existing, canonical) {
			return "", "", fmt.Errorf("persist content-addressed Advanced lifecycle result: %w", err)
		}
	}
	absoluteRoot := filepath.Join(root.Name(), ".stackkit", "advanced")
	if err := backupcustody.ProtectPrivatePath(absoluteRoot, true); err != nil {
		return "", "", fmt.Errorf("protect Advanced lifecycle evidence root: %w", err)
	}
	if err := backupcustody.ProtectPrivatePath(
		filepath.Join(root.Name(), filepath.FromSlash(path)), false,
	); err != nil {
		return "", "", fmt.Errorf("protect Advanced lifecycle result: %w", err)
	}
	return path, digest, nil
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
	// The Terramate scope is re-derived from the fresh renders and the local
	// binding; a different stack set or host project is a stale change set.
	scope, err := advancedchangeset.DeriveTerramateScope(
		baseline.Artifacts(), candidate.Artifacts(), record.Changes,
		admission.owner.Binding.SiteRef, admission.owner.Binding.NodeRef,
	)
	if err != nil {
		return verifiedAdvancedMutation{}, err
	}
	if strings.Join(scope.AffectedStacks, ",") != strings.Join(record.AffectedStacks, ",") ||
		scope.HostManifestSHA256 != record.TerramateHostManifestSHA256 {
		return verifiedAdvancedMutation{}, &advancedchangeset.Error{
			Code:   advancedchangeset.ErrStale,
			Field:  "terramate",
			Detail: "affected stacks or the local Terramate host project differ from the Owner-approved change set",
		}
	}
	digest := sha256.Sum256(raw)
	return verifiedAdvancedMutation{
		admission: admission, record: record,
		digest:    "sha256:" + hex.EncodeToString(digest[:]),
		candidate: candidate,
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
	orchestration *advancedTerramateOrchestration,
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
					orchestration.step(workspace, revalidated),
				)
			},
		)
		// A checkpoint of a Terramate-target install rolls back per stack
		// (coordinated rollback); every other checkpoint keeps the existing
		// upgrade rollback.
		rollback := selectAdvancedChangeSetRollback(snapshot, func() error {
			return rollbackPublicUpgrade(
				operationCtx, workspace, specFile, checkpoint, snapshot,
				mutation, control, &result,
			)
		}, orchestration.coordinatedRollback(func() (*advancedrollback.Report, error) {
			return rollbackAdvancedChangeSetCoordinated(
				operationCtx, workspace, receipt, checkpoint, mutation, control,
				revalidated, orchestration.tools, &result,
			)
		}))
		if settled := settleAdvancedTarget(&result, targetErr, rollback); settled != nil {
			return settled
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
	orchestrate func(context.Context) error,
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
	// Terramate orchestration of a change set: between the applied target and
	// its verify, inside target-apply-succeeded, so a failure takes the same
	// rollback path as a failed verify.
	if orchestrate != nil {
		if err := orchestrate(ctx); err != nil {
			return err
		}
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
