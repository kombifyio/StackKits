package commands

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localbackupschedule"
	"github.com/spf13/cobra"
)

const nativeV2BackupPolicyArtifactID = "local-kopia-backup-source-policy-instance-source-policy-node-main"

type nativeV2BackupOperation string

const (
	nativeV2BackupConfigure nativeV2BackupOperation = "configure"
	nativeV2BackupStatus    nativeV2BackupOperation = "status"
	nativeV2BackupRun       nativeV2BackupOperation = "run"
	nativeV2BackupRestore   nativeV2BackupOperation = "restore"
	nativeV2BackupAbandon   nativeV2BackupOperation = "restore-abandon"
)

type nativeV2BackupAuthority struct {
	OwnerRef         string
	AuthorityRef     string
	WorkspaceRoot    string
	OutputRoot       string
	Plan             generationartifact.VerifiedPlan
	Lineage          backuplifecycle.AuthorityLineage
	PolicyDigest     string
	PolicyArtifact   []byte
	Policy           localbackuppolicy.Policy
	AppliedAuthority *nativeV2AppliedAuthority
	LegacyBeta4      *publicUpgradeBridge
	HistoricalStable *publicUpgradeBridge
}

type nativeV2BackupContinuation func(
	context.Context,
	nativeV2BackupOperation,
	nativeV2BackupAuthority,
	nativeV2BackupRequest,
) (any, error)

type nativeV2BackupRequest struct {
	OperationID      string
	SnapshotAnchorID string
	OwnerApproved    bool
	Scheduled        bool
}

var (
	continueNativeV2Backup                   nativeV2BackupContinuation = continueNativeV2BackupProduction
	inspectNativeV2BackupAuthorityForRequest                            = inspectNativeV2BackupAuthorityForCommand
)

var nativeV2BackupOperationIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var nativeV2BackupDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func runNativeV2BackupCommand(cmd *cobra.Command, operation nativeV2BackupOperation, requestedOperationID string) error {
	return runNativeV2BackupRequest(cmd, operation, nativeV2BackupRequest{OperationID: requestedOperationID})
}

func runNativeV2BackupRestoreCommand(
	cmd *cobra.Command,
	snapshotAnchorID string,
	requestedOperationID string,
	ownerApproved bool,
) error {
	return runNativeV2BackupRequest(cmd, nativeV2BackupRestore, nativeV2BackupRequest{
		OperationID:      requestedOperationID,
		SnapshotAnchorID: strings.TrimSpace(snapshotAnchorID),
		OwnerApproved:    ownerApproved,
	})
}

func runNativeV2BackupRestoreAbandonCommand(
	cmd *cobra.Command,
	operationID string,
	ownerApproved bool,
) error {
	return runNativeV2BackupRequest(cmd, nativeV2BackupAbandon, nativeV2BackupRequest{
		OperationID:   strings.TrimSpace(operationID),
		OwnerApproved: ownerApproved,
	})
}

func runNativeV2BackupRequest(
	cmd *cobra.Command,
	operation nativeV2BackupOperation,
	request nativeV2BackupRequest,
) error {
	if cmd == nil {
		return errors.New("native v2 backup command is required")
	}
	if operation == nativeV2BackupConfigure && cmd.Flags().Lookup("repo") != nil {
		return errors.New("native v2 backup repository paths are CUE-owned; --repo is not accepted")
	}
	if request.Scheduled && (operation != nativeV2BackupRun || request.OperationID != "" || request.SnapshotAnchorID != "") {
		return errors.New("scheduled backup must use the ordinary run operation with a schedule-owned operation ID")
	}
	if !request.Scheduled {
		operationID, err := normalizeNativeV2BackupOperationID(operation, request.OperationID)
		if err != nil {
			return err
		}
		request.OperationID = operationID
	}
	if operation == nativeV2BackupRestore || operation == nativeV2BackupAbandon {
		if !request.OwnerApproved {
			if operation == nativeV2BackupAbandon {
				return errors.New("native v2 restore abandonment requires --owner-approve")
			}
			return errors.New("native v2 restore requires --owner-approve")
		}
		if operation == nativeV2BackupRestore && !nativeV2BackupDigestPattern.MatchString(request.SnapshotAnchorID) {
			return errors.New("native v2 restore requires a sha256 snapshot-anchor ID")
		}
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	workspace := getWorkDir()
	initial, err := inspectNativeV2BackupAuthorityForRequest(ctx, workspace, specFile)
	if err != nil {
		return fmt.Errorf("backup %s authority: %w", operation, err)
	}
	var result any
	execute := func() error {
		return withArchitectureV2OutputLock(workspace, initial.OutputRoot, func(_ *confinedfs.Transaction, _ *confinedfs.OutputLock) error {
			current, inspectErr := inspectNativeV2BackupAuthorityForRequest(ctx, workspace, specFile)
			if inspectErr != nil {
				return fmt.Errorf("backup %s locked authority: %w", operation, inspectErr)
			}
			if !sameNativeV2BackupAuthority(initial, current) {
				return errors.New("native v2 backup authority changed while acquiring the output lock")
			}
			var scheduleAuthorization localbackupschedule.Authorization
			if request.Scheduled {
				var alreadyHandled bool
				scheduleAuthorization, alreadyHandled, inspectErr = authorizeScheduledNativeBackup(ctx, current)
				if inspectErr != nil {
					return inspectErr
				}
				if alreadyHandled {
					result = nativeBackupScheduleNoop{APIVersion: "stackkit.local-backup-scheduled-dispatch/v1", State: "no-snapshot-due", LastExecution: scheduleAuthorization.Execution}
					return nil
				}
				if scheduleAuthorization.Execution == nil || scheduleAuthorization.Execution.OperationID == "" {
					return errors.New("scheduled backup has no admitted pending operation")
				}
				request.OperationID = scheduleAuthorization.Execution.OperationID
			}
			var (
				lifecycleRuns  []architectureV2ApplicationLifecycleRun
				lifecycleStage string
				lifecycleRef   string
			)
			switch operation {
			case nativeV2BackupRun:
				lifecycleStage, lifecycleRef = "backup", "stackkit.backup"
			}
			startedAt := time.Now().UTC()
			if lifecycleStage != "" && len(current.Plan.Canonical()) != 0 {
				lifecycleRuns, inspectErr = beginArchitectureV2ApplicationLifecycles(
					workspace, current.Plan, lifecycleStage, lifecycleRef, "", startedAt,
				)
				if inspectErr != nil {
					return inspectErr
				}
			}
			result, inspectErr = continueNativeV2Backup(ctx, operation, current, request)
			if inspectErr == nil && request.Scheduled {
				inspectErr = completeScheduledNativeBackup(current, scheduleAuthorization, result)
			}
			if inspectErr != nil {
				return failArchitectureV2ApplicationLifecycles(
					workspace, lifecycleRuns,
					"native backup lifecycle failed before owner evidence was completed",
					time.Now().UTC(), inspectErr,
				)
			}
			if len(lifecycleRuns) == 0 {
				return nil
			}
			evidence, recoveryRef, evidenceErr := nativeV2BackupApplicationLifecycleEvidence(
				operation, request, result,
			)
			if evidenceErr != nil {
				return requireArchitectureV2ApplicationLifecycleRecovery(
					workspace, lifecycleRuns,
					"native backup lifecycle completed but its owner evidence could not be bound",
					recoveryRef, time.Now().UTC(), evidenceErr,
				)
			}
			return succeedArchitectureV2ApplicationLifecycles(
				workspace, lifecycleRuns, evidence, time.Now().UTC(),
			)
		})
	}
	if operation == nativeV2BackupStatus {
		err = execute()
	} else {
		err = withLifecycleMutation(workspace, "backup "+string(operation), execute)
	}
	if err != nil {
		return err
	}
	return emitNativeV2BackupResult(cmd, operation, result)
}

func nativeV2BackupApplicationLifecycleEvidence(
	operation nativeV2BackupOperation,
	request nativeV2BackupRequest,
	result any,
) ([]applicationlifecycle.Evidence, string, error) {
	switch operation {
	case nativeV2BackupRun:
		anchor, ok := result.(backuplifecycle.SnapshotAnchor)
		if !ok {
			return nil, "urn:stackkit:backup-operation:" + request.OperationID,
				errors.New("native backup runtime returned no snapshot anchor")
		}
		ref, err := backuplifecycle.SnapshotAnchorEvidenceRef(anchor.ID)
		if err != nil {
			return nil, "urn:stackkit:snapshot-anchor:" + anchor.ID, err
		}
		return []applicationlifecycle.Evidence{{
			Kind: "snapshot-anchor", Ref: ref, Digest: anchor.ID,
		}}, ref, nil
	case nativeV2BackupRestore:
		restore, ok := result.(backuplifecycle.RestoreResult)
		if !ok {
			return nil, "urn:stackkit:restore-operation:" + request.OperationID,
				errors.New("native restore runtime returned no restore result")
		}
		snapshotRef, err := backuplifecycle.SnapshotAnchorEvidenceRef(request.SnapshotAnchorID)
		if err != nil {
			return nil, "urn:stackkit:snapshot-anchor:" + request.SnapshotAnchorID, err
		}
		resultRef, err := backuplifecycle.RestoreResultEvidenceRef(restore.ID)
		if err != nil {
			return nil, "urn:stackkit:restore-result:" + restore.ID, err
		}
		return []applicationlifecycle.Evidence{
			{Kind: "snapshot-anchor", Ref: snapshotRef, Digest: request.SnapshotAnchorID},
			{Kind: "restore-result", Ref: resultRef, Digest: restore.ID},
			{Kind: "owner-observation", Ref: resultRef + "#verification", Digest: restore.ID},
		}, resultRef, nil
	default:
		return nil, "", nil
	}
}

func inspectNativeV2BackupAuthority(ctx context.Context, workspace, requestedSpec string) (nativeV2BackupAuthority, error) {
	applied, err := inspectNativeV2AppliedAuthority(ctx, workspace, requestedSpec)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	heldRoot, err := confinedfs.Open(applied.WorkspaceRoot)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	defer heldRoot.Close()
	transaction, err := heldRoot.BeginTransaction()
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	defer transaction.Close()
	coreModuleRef, policyArtifactRequirement, err := nativeV2BackupPolicyRequirement(
		applied.Plan, applied.Owner.Binding.SiteRef, applied.Owner.Binding.NodeRef,
	)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	policyArtifact, policyDigest, policy, err := readNativeV2BackupPolicyForArtifact(
		transaction, applied.Manifest, policyArtifactRequirement.ID,
	)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	if policy.Target.SiteRef != applied.Owner.Binding.SiteRef || policy.Target.NodeRef != applied.Owner.Binding.NodeRef {
		return nativeV2BackupAuthority{}, errors.New("local Kopia policy target differs from owner custody")
	}
	if policy.Source.CoreModuleRef != "" && policy.Source.CoreModuleRef != coreModuleRef {
		return nativeV2BackupAuthority{}, errors.New("local Kopia policy source differs from the applied Core profile")
	}
	if coreModuleRef == localbackuppolicy.CoreLiteModuleRef && policy.Source.CoreModuleRef != coreModuleRef {
		return nativeV2BackupAuthority{}, errors.New("CoreLite local Kopia policy must carry its explicit Core profile")
	}
	return nativeV2BackupAuthority{
		OwnerRef: applied.OwnerRef, AuthorityRef: applied.AuthorityRef, WorkspaceRoot: applied.WorkspaceRoot,
		OutputRoot: applied.OutputRoot, Plan: applied.Plan, Lineage: applied.Lineage,
		PolicyDigest: policyDigest, PolicyArtifact: policyArtifact, Policy: policy, AppliedAuthority: &applied,
	}, nil
}
func verifyNativeV2BackupRestore(
	ctx context.Context,
	expected nativeV2BackupAuthority,
	request backuplifecycle.RestoreVerificationRequest,
) (backuplifecycle.RestoreVerification, error) {
	if expected.LegacyBeta4 != nil {
		return verifyExactBeta4BackupRestore(ctx, expected, request)
	}
	if expected.HistoricalStable != nil {
		return verifyPublishedStableBackupRestore(ctx, expected, request)
	}
	current, err := inspectNativeV2BackupAuthority(ctx, expected.WorkspaceRoot, specFile)
	if err != nil {
		return backuplifecycle.RestoreVerification{}, err
	}
	if !sameNativeV2BackupAuthority(expected, current) ||
		request.OwnerRef != current.OwnerRef ||
		request.AuthorizationLineage != current.Lineage ||
		request.SnapshotAnchorID == "" ||
		request.OperationID == "" ||
		request.StagingPath != backuplifecycle.RestoreStagingPath(request.OperationID) {
		return backuplifecycle.RestoreVerification{}, errors.New("native v2 restore target authority changed before live post-verification")
	}

	gate := newArchitectureV2ExecutionGate()
	reader, err := gate.newAuthority()
	if err != nil {
		return backuplifecycle.RestoreVerification{}, err
	}
	if closer, ok := reader.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}
	planPath := filepath.Join(
		current.WorkspaceRoot,
		filepath.FromSlash(current.OutputRoot),
		".stackkit",
		"resolved-plan.json",
	)
	plan, err := reader.ReadCanonicalPlan(planPath)
	if err != nil {
		return backuplifecycle.RestoreVerification{}, err
	}
	if plan.Binding() != current.Lineage.Binding || plan.OutputRoot() != current.OutputRoot {
		return backuplifecycle.RestoreVerification{}, errors.New("native v2 restore post-verifier read a different canonical Plan")
	}
	_, manifestPath, _ := plan.MetadataPaths(current.WorkspaceRoot)
	manifest, err := generationartifact.ReadManifest(manifestPath)
	if err != nil {
		return backuplifecycle.RestoreVerification{}, err
	}
	owner, runtime, err := verifyArchitectureV2LocalState(
		ctx,
		current.WorkspaceRoot,
		plan,
		manifest,
		false,
	)
	if err != nil {
		return backuplifecycle.RestoreVerification{}, err
	}
	if runtime == nil || runtime.Status != "ready" ||
		runtime.ServiceCount == 0 || runtime.ProbeCount == 0 ||
		owner.OwnerRef != current.OwnerRef ||
		owner.OwnerBindingDigest != current.Lineage.OwnerBindingDigest ||
		owner.PocketIDSubject != current.Lineage.PocketIDSubject {
		return backuplifecycle.RestoreVerification{}, errors.New("native v2 restore post-verifier did not prove the current local service and Owner closure")
	}
	if err := verifyNativeV2BackupApplications(ctx, current); err != nil {
		return backuplifecycle.RestoreVerification{}, err
	}
	return backuplifecycle.RestoreVerification{
		APIVersion:         "stackkit.local-backup-restore-verification/v1",
		OwnerRef:           owner.OwnerRef,
		OwnerBindingDigest: owner.OwnerBindingDigest,
		PocketIDSubject:    owner.PocketIDSubject,
		PlanHash:           current.Lineage.Binding.PlanHash,
		ServicesVerified:   true,
		VerifiedAt:         time.Now().UTC(),
	}, nil
}

// nativeV2BackupPolicyRequirement selects the one source-policy artifact and
// its applied Core runtime from the already verified Plan-owned Apply
// requirements. It does not infer a profile from generated Compose or from a
// default compute tier.
func nativeV2BackupPolicyRequirement(
	plan generationartifact.VerifiedPlan,
	siteRef string,
	nodeRef string,
) (string, generationartifact.ApplyArtifactRequirement, error) {
	requirements := plan.ApplyRequirements()
	var (
		selected generationartifact.ApplyArtifactRequirement
		matches  int
	)
	for _, candidate := range requirements.Artifacts {
		if candidate.OwnerKind != "render-instance" ||
			candidate.UnitRef != "source-policy" ||
			candidate.Kind != "native-config" || candidate.Format != "json" ||
			candidate.Mode != "0600" ||
			candidate.ExecutionClass != generationartifact.ApplyExecutionClassArtifactOnly {
			continue
		}
		if candidate.ModuleRef != localbackuppolicy.CoreModuleRef &&
			candidate.ModuleRef != localbackuppolicy.CoreLiteModuleRef {
			continue
		}
		if len(candidate.SiteRefs) != 1 || candidate.SiteRefs[0] != siteRef ||
			len(candidate.NodeRefs) != 1 || candidate.NodeRefs[0] != nodeRef {
			continue
		}
		selected = candidate
		matches++
	}
	if matches != 1 || selected.ID == "" || selected.InstanceRef == "" || selected.OutputRef == "" {
		return "", generationartifact.ApplyArtifactRequirement{}, errors.New(
			"native v2 backup requires exactly one applied Full-Core or CoreLite source-policy artifact",
		)
	}
	runtimeMatches := 0
	for _, runtime := range requirements.RuntimeInstances {
		if runtime.OwnerKind == "module" && runtime.ModuleRef == selected.ModuleRef &&
			runtime.UnitRef == "compose" && runtime.InstanceRef != "" &&
			len(runtime.SiteRefs) == 1 && runtime.SiteRefs[0] == siteRef &&
			len(runtime.NodeRefs) == 1 && runtime.NodeRefs[0] == nodeRef {
			runtimeMatches++
		}
	}
	if runtimeMatches != 1 {
		return "", generationartifact.ApplyArtifactRequirement{}, errors.New(
			"native v2 backup requires exactly one applied Core runtime for its source-policy profile",
		)
	}
	return selected.ModuleRef, selected, nil
}

// readNativeV2BackupPolicy retains the historical Full-Core artifact lookup
// used by pre-profile upgrade bridges. New native-v2 callers use the
// Plan-owned requirement above so CoreLite cannot inherit Full-Core output.
func readNativeV2BackupPolicy(
	transaction *confinedfs.Transaction,
	manifest generationartifact.ArtifactManifest,
) ([]byte, string, localbackuppolicy.Policy, error) {
	return readNativeV2BackupPolicyForArtifact(transaction, manifest, nativeV2BackupPolicyArtifactID)
}

func readNativeV2BackupPolicyForArtifact(
	transaction *confinedfs.Transaction,
	manifest generationartifact.ArtifactManifest,
	artifactID string,
) ([]byte, string, localbackuppolicy.Policy, error) {
	var artifact generationartifact.RenderedArtifact
	count := 0
	for _, candidate := range manifest.Artifacts {
		if candidate.ID == artifactID {
			artifact = candidate
			count++
		}
	}
	if count != 1 || artifact.Kind != "native-config" || artifact.Format != "json" ||
		artifact.Mode != "0600" || strings.TrimSpace(artifact.Path) == "" {
		return nil, "", localbackuppolicy.Policy{}, errors.New("native v2 backup requires exactly one governed local Kopia policy artifact")
	}
	raw, info, err := transaction.ReadStable(artifact.Path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 64<<10 {
		return nil, "", localbackuppolicy.Policy{}, errors.New("local Kopia policy artifact is not a bounded plain file")
	}
	digest, err := localbackuppolicy.Digest(raw)
	if err != nil {
		return nil, "", localbackuppolicy.Policy{}, err
	}
	if digest != artifact.SHA256 {
		return nil, "", localbackuppolicy.Policy{}, errors.New("local Kopia policy bytes differ from the verified manifest")
	}
	policy, err := localbackuppolicy.Decode(raw)
	if err != nil {
		return nil, "", localbackuppolicy.Policy{}, err
	}
	return append([]byte(nil), raw...), digest, policy, nil
}

func currentApplyReceiptHash(
	workspace string,
	transaction *confinedfs.Transaction,
	verifiedApply architecturev2.VerifiedApplyResult,
) (string, error) {
	resultHash := verifiedApply.ResultHash()
	name := strings.TrimPrefix(resultHash, "sha256:") + ".json"
	if len(name) != 69 {
		return "", errors.New("current Apply result has an invalid content address")
	}
	receiptPath := filepath.ToSlash(filepath.Join(architectureV2ApplyEvidenceRoot, "receipts", name))
	raw, info, err := transaction.ReadStable(receiptPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 64<<10 {
		return "", errors.New("owner-signed Apply receipt is not a bounded content-addressed file")
	}
	canonicalResult, err := verifiedApply.Canonical()
	if err != nil {
		return "", err
	}
	if err := verifyOwnerApplyResultReceipt(workspace, canonicalResult, raw, resultHash); err != nil {
		return "", fmt.Errorf("verify exact owner-signed Apply receipt: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func workspaceRelativeBackupPath(workspace, target string) string {
	relative, err := filepath.Rel(workspace, target)
	if err != nil {
		return target
	}
	return filepath.ToSlash(relative)
}

func normalizeNativeV2BackupOperationID(operation nativeV2BackupOperation, requested string) (string, error) {
	if operation != nativeV2BackupRun && operation != nativeV2BackupRestore && operation != nativeV2BackupAbandon {
		return "", nil
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		if operation == nativeV2BackupAbandon {
			return "", errors.New("restore abandonment requires an exact --operation-id")
		}
		random := make([]byte, 8)
		if _, err := rand.Read(random); err != nil {
			return "", fmt.Errorf("generate backup operation ID: %w", err)
		}
		prefix := "backup-"
		if operation == nativeV2BackupRestore {
			prefix = "restore-"
		}
		requested = prefix + time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(random)
	}
	if !nativeV2BackupOperationIDPattern.MatchString(requested) {
		return "", errors.New("--operation-id must be 1-128 portable non-secret characters")
	}
	return requested, nil
}

func sameNativeV2BackupAuthority(left, right nativeV2BackupAuthority) bool {
	return left.OwnerRef == right.OwnerRef &&
		left.AuthorityRef == right.AuthorityRef &&
		left.WorkspaceRoot == right.WorkspaceRoot &&
		left.OutputRoot == right.OutputRoot &&
		bytes.Equal(left.Plan.Canonical(), right.Plan.Canonical()) &&
		left.PolicyDigest == right.PolicyDigest &&
		left.Lineage == right.Lineage &&
		bytes.Equal(left.PolicyArtifact, right.PolicyArtifact) &&
		reflect.DeepEqual(left.Policy, right.Policy) &&
		reflect.DeepEqual(left.LegacyBeta4, right.LegacyBeta4) &&
		reflect.DeepEqual(left.HistoricalStable, right.HistoricalStable)
}

func emitNativeV2BackupResult(cmd *cobra.Command, operation nativeV2BackupOperation, result any) error {
	if backupOutputJSON {
		return writeCommandResult(cmd, cmd.CommandPath(), result)
	}
	if _, ok := result.(nativeBackupScheduleNoop); ok {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No new snapshot: the approved schedule has no pending UTC slot.")
		return err
	}
	if status, ok := result.(backuplifecycle.RepositoryStatus); operation == nativeV2BackupStatus && ok {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Repository ready: %t\nConsistency: %s\n", status.Ready, status.Consistency); err != nil {
			return err
		}
		if status.History != nil {
			if status.History.Issue != "" {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "History: %s\n", status.History.Issue); err != nil {
					return err
				}
			}
			for _, event := range []struct {
				label string
				age   backuplifecycle.EvidenceAge
			}{{"Snapshot receipt", status.History.Snapshot}, {"Staged restore receipt", status.History.StagedRestore}} {
				text := event.age.State
				if event.age.RecordedAt != nil {
					text += " at " + event.age.RecordedAt.Format(time.RFC3339)
					if !event.age.CurrentPlan {
						text += " [historical plan]"
					}
				}
				if event.age.AgeSeconds != nil {
					text += fmt.Sprintf(" (%s ago)", (time.Duration(*event.age.AgeSeconds) * time.Second).String())
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", event.label, text); err != nil {
					return err
				}
			}
			if availability := status.History.Availability; availability != nil {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Snapshot manifest: %s", availability.State); err != nil {
					return err
				}
				if availability.Reason != "" {
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), " (%s)", availability.Reason); err != nil {
						return err
					}
				}
				if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
					return err
				}
			}
			for _, objective := range status.History.RecoveryObjectives {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Recovery objective %s: snapshot age %s (limit %ds); application recovery %s (limit %ds)\n",
					objective.BindingRef, objective.DataLoss.State, objective.DataLoss.LimitSeconds,
					objective.RecoveryTime.State, objective.RecoveryTime.LimitSeconds); err != nil {
					return err
				}
			}
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "A recorded receipt or present manifest does not prove complete backup content or successful application recovery.")
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Native v2 backup %s completed\n", operation)
	return err
}
