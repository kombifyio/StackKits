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

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/spf13/cobra"
)

const nativeV2BackupPolicyArtifactID = "local-kopia-backup-source-policy-instance-source-policy-node-main"

type nativeV2BackupOperation string

const (
	nativeV2BackupConfigure nativeV2BackupOperation = "configure"
	nativeV2BackupStatus    nativeV2BackupOperation = "status"
	nativeV2BackupRun       nativeV2BackupOperation = "run"
	nativeV2BackupRestore   nativeV2BackupOperation = "restore"
)

type nativeV2BackupAuthority struct {
	OwnerRef       string
	AuthorityRef   string
	WorkspaceRoot  string
	OutputRoot     string
	Lineage        backuplifecycle.AuthorityLineage
	PolicyDigest   string
	PolicyArtifact []byte
	Policy         localbackuppolicy.Policy
	LegacyBeta4    *publicUpgradeBridge
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
	operationID, err := normalizeNativeV2BackupOperationID(operation, request.OperationID)
	if err != nil {
		return err
	}
	request.OperationID = operationID
	if operation == nativeV2BackupRestore {
		if !request.OwnerApproved {
			return errors.New("native v2 restore requires --owner-approve")
		}
		if !nativeV2BackupDigestPattern.MatchString(request.SnapshotAnchorID) {
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
			result, inspectErr = continueNativeV2Backup(ctx, operation, current, request)
			return inspectErr
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

func inspectNativeV2BackupAuthority(ctx context.Context, workspace, requestedSpec string) (nativeV2BackupAuthority, error) {
	heldRoot, err := confinedfs.Open(workspace)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	defer func() { _ = heldRoot.Close() }()
	workspace = heldRoot.Name()
	transaction, err := heldRoot.BeginTransaction()
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	defer func() { _ = transaction.Close() }()

	var inspection generationartifact.PlanInspection
	gate := newArchitectureV2ExecutionGate()
	handled, err := gate.preflight(workspace, requestedSpec, architectureV2Plan, architectureV2ExecutionCLIOptions{
		context: ctx,
		inspectionSink: func(value generationartifact.PlanInspection) error {
			inspection = value
			return nil
		},
	})
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	if !handled || inspection.APIVersion != generationartifact.PlanInspectionAPIVersion ||
		inspection.Kind != generationartifact.PlanInspectionKind ||
		inspection.VerifiedPhase != generationartifact.ExecutionPhaseGeneration ||
		inspection.Readiness.Generation.Status != "ready" ||
		inspection.Readiness.Apply.Status != "ready" ||
		len(inspection.Readiness.Generation.Blockers) != 0 ||
		len(inspection.Readiness.Apply.Blockers) != 0 ||
		inspection.ExecutorInvoked {
		return nativeV2BackupAuthority{}, errors.New("native v2 backup requires the exact generation- and Apply-ready Architecture v2 closure")
	}

	planPath := filepath.Join(workspace, filepath.FromSlash(inspection.OutputRoot), ".stackkit", "resolved-plan.json")
	authority, err := gate.newAuthority()
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	if closer, ok := authority.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}
	plan, err := authority.ReadCanonicalPlan(planPath)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	if plan.Binding() != inspection.Binding || plan.OutputRoot() != inspection.OutputRoot {
		return nativeV2BackupAuthority{}, errors.New("native v2 backup plan differs from the verified inspection")
	}
	_, manifestPath, receiptPath := plan.MetadataPaths(workspace)
	manifest, err := generationartifact.ReadManifest(manifestPath)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	manifestRaw, _, err := transaction.ReadStable(workspaceRelativeBackupPath(workspace, manifestPath))
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	canonicalManifest, err := manifest.MarshalCanonical()
	if err != nil || !bytes.Equal(manifestRaw, canonicalManifest) {
		return nativeV2BackupAuthority{}, errors.New("native v2 backup manifest changed during stable inspection")
	}
	receipt, err := generationartifact.ReadReceipt(receiptPath)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	receiptRaw, _, err := transaction.ReadStable(workspaceRelativeBackupPath(workspace, receiptPath))
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	canonicalReceipt, err := receipt.MarshalCanonical()
	if err != nil || !bytes.Equal(receiptRaw, canonicalReceipt) {
		return nativeV2BackupAuthority{}, errors.New("native v2 backup generation receipt changed during stable inspection")
	}
	manifestHash, err := manifest.Hash()
	if err != nil || manifestHash != inspection.Manifest.Hash {
		return nativeV2BackupAuthority{}, errors.New("native v2 backup manifest differs from the verified inspection")
	}
	generationReceiptHash, err := receipt.Hash()
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}

	policyArtifact, policyDigest, policy, err := readNativeV2BackupPolicy(transaction, manifest)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return nativeV2BackupAuthority{}, fmt.Errorf("verify local owner custody: %w", err)
	}
	if policy.Target.SiteRef != owner.Binding.SiteRef || policy.Target.NodeRef != owner.Binding.NodeRef {
		return nativeV2BackupAuthority{}, errors.New("local Kopia policy target differs from owner custody")
	}

	verifyRaw, err := newArchitectureV2ProductVerifyAuthority(workspace, architectureV2ExecutionCLIOptions{})
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	if closer, ok := verifyRaw.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}
	verifyAuthority, ok := verifyRaw.(architectureV2ProductVerifyAuthority)
	if !ok {
		return nativeV2BackupAuthority{}, errors.New("native v2 backup Apply verifier is unavailable")
	}
	verifiedApply, err := readCurrentArchitectureV2ApplyResult(workspace, plan.Binding(), func(data []byte) (architecturev2.VerifiedApplyResult, error) {
		return verifyAuthority.VerifyProductApplyResult(architecturev2.ProductApplyResultVerificationInput{
			Plan: plan, Manifest: manifest, Receipt: receipt, Versions: gate.versions, Result: data,
		})
	})
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	ownerSummary, _, err := verifyArchitectureV2LocalState(ctx, workspace, plan, manifest, true)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	if ownerSummary.OwnerRef != owner.OwnerRef {
		return nativeV2BackupAuthority{}, errors.New("offline owner verification differs from local custody")
	}
	applyReceiptHash, err := currentApplyReceiptHash(workspace, transaction, verifiedApply)
	if err != nil {
		return nativeV2BackupAuthority{}, err
	}
	return nativeV2BackupAuthority{
		OwnerRef: owner.OwnerRef, AuthorityRef: owner.Trust.HumanAuthorityRef, WorkspaceRoot: workspace,
		OutputRoot: inspection.OutputRoot,
		Lineage: backuplifecycle.AuthorityLineage{
			Binding: inspection.Binding, ManifestHash: manifestHash,
			GenerationReceiptHash: generationReceiptHash,
			ApplyResultHash:       verifiedApply.ResultHash(), ApplyReceiptHash: applyReceiptHash,
			OwnerBindingDigest: ownerSummary.OwnerBindingDigest,
			PocketIDSubject:    ownerSummary.PocketIDSubject,
		},
		PolicyDigest: policyDigest, PolicyArtifact: policyArtifact, Policy: policy,
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

func readNativeV2BackupPolicy(
	transaction *confinedfs.Transaction,
	manifest generationartifact.ArtifactManifest,
) ([]byte, string, localbackuppolicy.Policy, error) {
	var artifact generationartifact.RenderedArtifact
	count := 0
	for _, candidate := range manifest.Artifacts {
		if candidate.ID == nativeV2BackupPolicyArtifactID {
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
	if operation != nativeV2BackupRun && operation != nativeV2BackupRestore {
		return "", nil
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
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
		left.PolicyDigest == right.PolicyDigest &&
		left.Lineage == right.Lineage &&
		bytes.Equal(left.PolicyArtifact, right.PolicyArtifact) &&
		reflect.DeepEqual(left.Policy, right.Policy) &&
		reflect.DeepEqual(left.LegacyBeta4, right.LegacyBeta4)
}

func emitNativeV2BackupResult(cmd *cobra.Command, operation nativeV2BackupOperation, result any) error {
	if backupOutputJSON {
		return writeCommandResult(cmd, cmd.CommandPath(), result)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Native v2 backup %s completed\n", operation)
	return err
}
