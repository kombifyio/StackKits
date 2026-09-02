package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// nativeV2AppliedAuthority is the existing exact-generation and signed-Apply
// admission shared by local lifecycle operations. It does not select runtime
// implementations or create a second plan/custody authority.
type nativeV2AppliedAuthority struct {
	OwnerRef, AuthorityRef, WorkspaceRoot, OutputRoot string
	Plan                                              generationartifact.VerifiedPlan
	Manifest                                          generationartifact.ArtifactManifest
	Receipt                                           generationartifact.GenerationReceipt
	Owner                                             localevidence.OwnerCustody
	Lineage                                           backuplifecycle.AuthorityLineage
	AppliedWorkloads                                  []architecturev2.AppliedWorkloadIdentity
}

func inspectNativeV2AppliedAuthority(ctx context.Context, workspace, requestedSpec string) (nativeV2AppliedAuthority, error) {
	heldRoot, err := confinedfs.Open(workspace)
	if err != nil {
		return nativeV2AppliedAuthority{}, err
	}
	defer func() { _ = heldRoot.Close() }()
	workspace = heldRoot.Name()
	transaction, err := heldRoot.BeginTransaction()
	if err != nil {
		return nativeV2AppliedAuthority{}, err
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
		return nativeV2AppliedAuthority{}, err
	}
	if !handled || inspection.APIVersion != generationartifact.PlanInspectionAPIVersion ||
		inspection.Kind != generationartifact.PlanInspectionKind ||
		inspection.VerifiedPhase != generationartifact.ExecutionPhaseGeneration ||
		inspection.Readiness.Generation.Status != "ready" ||
		inspection.Readiness.Apply.Status != "ready" ||
		len(inspection.Readiness.Generation.Blockers) != 0 ||
		len(inspection.Readiness.Apply.Blockers) != 0 ||
		inspection.ExecutorInvoked {
		return nativeV2AppliedAuthority{}, errors.New("native v2 local lifecycle requires the exact generation- and Apply-ready Architecture v2 closure")
	}

	planPath := filepath.Join(workspace, filepath.FromSlash(inspection.OutputRoot), ".stackkit", "resolved-plan.json")
	authority, err := gate.newAuthority()
	if err != nil {
		return nativeV2AppliedAuthority{}, err
	}
	if closer, ok := authority.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}
	plan, err := authority.ReadCanonicalPlan(planPath)
	if err != nil {
		return nativeV2AppliedAuthority{}, err
	}
	if plan.Binding() != inspection.Binding || plan.OutputRoot() != inspection.OutputRoot {
		return nativeV2AppliedAuthority{}, errors.New("native v2 local lifecycle plan differs from the verified inspection")
	}
	_, manifestPath, receiptPath := plan.MetadataPaths(workspace)
	manifest, err := generationartifact.ReadManifest(manifestPath)
	if err != nil {
		return nativeV2AppliedAuthority{}, err
	}
	manifestRaw, _, err := transaction.ReadStable(workspaceRelativeBackupPath(workspace, manifestPath))
	if err != nil {
		return nativeV2AppliedAuthority{}, err
	}
	canonicalManifest, err := manifest.MarshalCanonical()
	if err != nil || !bytes.Equal(manifestRaw, canonicalManifest) {
		return nativeV2AppliedAuthority{}, errors.New("native v2 local lifecycle manifest changed during stable inspection")
	}
	receipt, err := generationartifact.ReadReceipt(receiptPath)
	if err != nil {
		return nativeV2AppliedAuthority{}, err
	}
	receiptRaw, _, err := transaction.ReadStable(workspaceRelativeBackupPath(workspace, receiptPath))
	if err != nil {
		return nativeV2AppliedAuthority{}, err
	}
	canonicalReceipt, err := receipt.MarshalCanonical()
	if err != nil || !bytes.Equal(receiptRaw, canonicalReceipt) {
		return nativeV2AppliedAuthority{}, errors.New("native v2 local lifecycle generation receipt changed during stable inspection")
	}
	manifestHash, err := manifest.Hash()
	if err != nil || manifestHash != inspection.Manifest.Hash {
		return nativeV2AppliedAuthority{}, errors.New("native v2 local lifecycle manifest differs from the verified inspection")
	}
	generationReceiptHash, err := receipt.Hash()
	if err != nil {
		return nativeV2AppliedAuthority{}, err
	}

	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return nativeV2AppliedAuthority{}, fmt.Errorf("verify local owner custody: %w", err)
	}
	verifyRaw, err := newArchitectureV2ProductVerifyAuthority(workspace, architectureV2ExecutionCLIOptions{})
	if err != nil {
		return nativeV2AppliedAuthority{}, err
	}
	if closer, ok := verifyRaw.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}
	verifyAuthority, ok := verifyRaw.(architectureV2ProductVerifyAuthority)
	if !ok {
		return nativeV2AppliedAuthority{}, errors.New("native v2 local lifecycle Apply verifier is unavailable")
	}
	verifiedApply, err := readCurrentArchitectureV2ApplyResult(workspace, plan.Binding(), func(data []byte) (architecturev2.VerifiedApplyResult, error) {
		return verifyAuthority.VerifyProductApplyResult(architecturev2.ProductApplyResultVerificationInput{
			Plan: plan, Manifest: manifest, Receipt: receipt, Versions: gate.versions, Result: data,
		})
	})
	if err != nil {
		return nativeV2AppliedAuthority{}, err
	}
	ownerSummary, _, err := verifyArchitectureV2LocalState(ctx, workspace, plan, manifest, true)
	if err != nil {
		return nativeV2AppliedAuthority{}, err
	}
	if ownerSummary.OwnerRef != owner.OwnerRef {
		return nativeV2AppliedAuthority{}, errors.New("offline owner verification differs from local custody")
	}
	applyReceiptHash, err := currentApplyReceiptHash(workspace, transaction, verifiedApply)
	if err != nil {
		return nativeV2AppliedAuthority{}, err
	}
	return nativeV2AppliedAuthority{
		OwnerRef: owner.OwnerRef, AuthorityRef: owner.Trust.HumanAuthorityRef, WorkspaceRoot: workspace,
		OutputRoot: inspection.OutputRoot, Plan: plan,
		Lineage: backuplifecycle.AuthorityLineage{
			Binding: inspection.Binding, ManifestHash: manifestHash,
			GenerationReceiptHash: generationReceiptHash,
			ApplyResultHash:       verifiedApply.ResultHash(), ApplyReceiptHash: applyReceiptHash,
			OwnerBindingDigest: ownerSummary.OwnerBindingDigest,
			PocketIDSubject:    ownerSummary.PocketIDSubject,
		},
		Manifest: manifest, Receipt: receipt, Owner: owner,
		AppliedWorkloads: verifiedApply.Summary().AppliedWorkloads,
	}, nil
}
