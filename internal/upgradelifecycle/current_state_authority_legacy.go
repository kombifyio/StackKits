package upgradelifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	legacyBeta4Version              = "v0.8.0-beta.4"
	legacyBeta4ArchiveSHA256        = "sha256:5a84fabc3229bd1c45620cbbebe31f6e3f1d67a3658f400ff7bd345336dab4c3"
	legacyBeta4IndexSHA256          = "sha256:06c547f5494ce7fd0e2e88b5ef2a82c41ab7f1d95169b99128889a28c002a49f"
	legacyBeta4AuthorityFingerprint = "sha256:575debed771be518ee5627a6d7c6b125813a10e4af366bc61a26e7cf7b90c80e"
	legacyBeta4CatalogHash          = "sha256:af19894dfda87bf364d2c753acb11000eb532c3c0c736935ea6414dfe2a9a3ae"
	legacyBeta4DefinitionHash       = "sha256:7bfee231cf6fb90c954fb2a4b01c8d475147df54e1d07c6879b7c12b2454ac48"
	legacyBeta4CompilerVersion      = "stackkits-resolver/0.8.0-beta.4"
	legacyBeta4RendererVersion      = "0.8.0-beta.4"
)

// LegacyCurrentStateAuthorityInput is intentionally limited to the single
// published beta.4 authority discontinuity. The caller must still supply the
// immutable installed-release proof, owner-signed Apply evidence, complete
// generated artifact closure, and the candidate-created Kopia snapshot.
type LegacyCurrentStateAuthorityInput struct {
	WorkspaceRoot     string
	Inspection        generationartifact.PlanInspection
	Manifest          generationartifact.ArtifactManifest
	GenerationReceipt generationartifact.GenerationReceipt
	ApplyResult       []byte
	ApplyReceipt      []byte
	Capture           ExecutorStateCaptureInput
}

// NewVerifiedLegacyExecutorStateCapture is the only cross-release constructor.
// It does not run the current CUE contract: the attested beta.4 binary already
// supplied the exact PlanInspection and offline Verify proof. Everything that
// survives into rollback remains independently content-, release-, Owner-, and
// snapshot-bound here.
func NewVerifiedLegacyExecutorStateCapture(
	input LegacyCurrentStateAuthorityInput,
) (VerifiedExecutorStateCapture, error) {
	capture, err := cloneExecutorStateCaptureInput(input.Capture)
	if err != nil {
		return VerifiedExecutorStateCapture{}, err
	}
	input.Capture = capture
	input.ApplyResult = append([]byte(nil), input.ApplyResult...)
	input.ApplyReceipt = append([]byte(nil), input.ApplyReceipt...)
	input.Manifest.Artifacts = append(
		[]generationartifact.RenderedArtifact(nil),
		input.Manifest.Artifacts...,
	)
	if strings.TrimSpace(input.WorkspaceRoot) == "" {
		return VerifiedExecutorStateCapture{}, errors.New(
			"legacy current state authority: workspace is required",
		)
	}
	if err := verifyExactBeta4LegacyInspection(input.Inspection); err != nil {
		return VerifiedExecutorStateCapture{}, err
	}
	manifestHash, err := input.Manifest.Hash()
	if err != nil ||
		input.Manifest.APIVersion != generationartifact.ArtifactManifestAPIVersion ||
		input.Manifest.Kind != generationartifact.ArtifactManifestKind ||
		input.Manifest.Binding != input.Inspection.Binding ||
		manifestHash != input.Inspection.Manifest.Hash ||
		!equalLegacyArtifacts(
			input.Manifest.Artifacts, input.Inspection.Manifest.Artifacts,
		) {
		return VerifiedExecutorStateCapture{}, errors.New(
			"legacy current state authority: manifest differs from attested beta.4 plan proof",
		)
	}
	generationReceiptHash, err := input.GenerationReceipt.Hash()
	if err != nil ||
		input.GenerationReceipt.APIVersion != generationartifact.GenerationReceiptAPIVersion ||
		input.GenerationReceipt.Kind != generationartifact.GenerationReceiptKind ||
		input.GenerationReceipt.Binding != input.Inspection.Binding ||
		input.GenerationReceipt.ManifestHash != manifestHash {
		return VerifiedExecutorStateCapture{}, errors.New(
			"legacy current state authority: generation receipt differs from attested beta.4 generation",
		)
	}
	var applyDocument any
	if err := decodeExactJSON(input.ApplyResult, &applyDocument); err != nil {
		return VerifiedExecutorStateCapture{}, fmt.Errorf(
			"legacy current state authority: decode canonical Apply result: %w", err,
		)
	}
	canonicalApply, err := resolvedplan.CanonicalJSON(applyDocument)
	if err != nil || !bytes.Equal(canonicalApply, input.ApplyResult) {
		return VerifiedExecutorStateCapture{}, errors.New(
			"legacy current state authority: Apply result is not canonical",
		)
	}
	applyResultHash := executorStateDigest(input.ApplyResult)
	applyReceiptHash, err := verifyCurrentStateApplyReceipt(
		input.WorkspaceRoot, input.ApplyResult, applyResultHash, input.ApplyReceipt,
	)
	if err != nil {
		return VerifiedExecutorStateCapture{}, err
	}
	lineage := input.Capture.Lineage
	if lineage.Binding != input.Inspection.Binding ||
		lineage.ManifestHash != manifestHash ||
		lineage.GenerationReceiptHash != generationReceiptHash ||
		lineage.ApplyResultHash != applyResultHash ||
		lineage.ApplyReceiptHash != applyReceiptHash {
		return VerifiedExecutorStateCapture{}, errors.New(
			"legacy current state authority: capture lineage differs from beta.4 Plan, Generation, or Apply evidence",
		)
	}

	owner, err := localevidence.LoadOwnerCustody(input.WorkspaceRoot)
	if err != nil {
		return VerifiedExecutorStateCapture{}, fmt.Errorf(
			"legacy current state authority: verify Owner custody: %w", err,
		)
	}
	runtimeBinding, err := localevidence.LoadOwnerRuntimeBinding(input.WorkspaceRoot)
	if err != nil {
		return VerifiedExecutorStateCapture{}, fmt.Errorf(
			"legacy current state authority: verify Owner runtime binding: %w", err,
		)
	}
	if runtimeBinding.OwnerRef != owner.OwnerRef ||
		runtimeBinding.PocketIDSubject != lineage.PocketIDSubject ||
		localevidence.OwnerRuntimeBindingDigest(runtimeBinding) != lineage.OwnerBindingDigest {
		return VerifiedExecutorStateCapture{}, errors.New(
			"legacy current state authority: PocketID Owner binding differs from capture lineage",
		)
	}
	runtimeBindingBytes, err := json.MarshalIndent(runtimeBinding, "", "  ")
	if err != nil {
		return VerifiedExecutorStateCapture{}, err
	}
	artifactInput := CurrentStateAuthorityInput{
		Manifest: input.Manifest, Capture: input.Capture,
	}
	policyDigest, err := verifyCurrentStateArtifacts(artifactInput, owner)
	if err != nil {
		return VerifiedExecutorStateCapture{}, err
	}
	if input.Capture.KopiaSnapshotAnchor.PolicyArtifactDigest != policyDigest ||
		input.Capture.KopiaSnapshotAnchor.OperationID !=
			"backup-"+input.Capture.OperationID {
		return VerifiedExecutorStateCapture{}, errors.New(
			"legacy current state authority: snapshot anchor differs from current policy or upgrade operation",
		)
	}
	if err := verifyExecutorStateSnapshotAnchorWithAuthority(
		input.WorkspaceRoot, owner.OwnerRef, lineage,
		input.Capture.KopiaSnapshotAnchor, owner, runtimeBinding,
	); err != nil {
		return VerifiedExecutorStateCapture{}, err
	}
	release, err := verifyExecutorStateReleaseProof(
		input.Capture.Release, input.Capture.Executable.Blob.Data,
	)
	if err != nil {
		return VerifiedExecutorStateCapture{}, err
	}
	if release.Kit != "basement-kit" ||
		release.Version != legacyBeta4Version ||
		release.Channel != releaseindex.ChannelBeta ||
		release.ArchiveSHA256 != legacyBeta4ArchiveSHA256 ||
		release.IndexSHA256 != legacyBeta4IndexSHA256 {
		return VerifiedExecutorStateCapture{}, errors.New(
			"legacy current state authority: recovery release is not the exact attested beta.4 distribution",
		)
	}
	if err := appendLegacyCurrentStateControlBlobs(
		&input, runtimeBindingBytes,
	); err != nil {
		return VerifiedExecutorStateCapture{}, err
	}
	input.Capture.Release = releaseindex.VerifiedInstallation{}
	return VerifiedExecutorStateCapture{
		token:   &verifiedExecutorStateCaptureToken{},
		input:   executorStateCaptureInput(input.Capture),
		release: release,
	}, nil
}

func verifyExactBeta4LegacyInspection(
	inspection generationartifact.PlanInspection,
) error {
	if err := validatePlanInspection(inspection, "legacy beta.4 current"); err != nil {
		return err
	}
	authority := inspection.Binding.Authority
	if inspection.Binding.DefinitionHash != legacyBeta4DefinitionHash ||
		inspection.Binding.CompilerVersion != legacyBeta4CompilerVersion ||
		inspection.Binding.Renderer.ID != "stackkit" ||
		inspection.Binding.Renderer.Version != legacyBeta4RendererVersion ||
		authority.Class != "product" ||
		authority.Document != "catalog" ||
		!authority.GraduationEligible ||
		authority.Issuer != "stackkits-product-authority/v1" ||
		authority.AuthorityFingerprint != legacyBeta4AuthorityFingerprint ||
		authority.CatalogHash != legacyBeta4CatalogHash {
		return errors.New(
			"legacy current state authority: plan is outside the exact beta.4 authority allowlist",
		)
	}
	return nil
}

func equalLegacyArtifacts(
	left, right []generationartifact.RenderedArtifact,
) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func appendLegacyCurrentStateControlBlobs(
	input *LegacyCurrentStateAuthorityInput,
	runtimeBindingBytes []byte,
) error {
	manifestBytes, err := input.Manifest.MarshalCanonical()
	if err != nil {
		return err
	}
	receiptBytes, err := input.GenerationReceipt.MarshalCanonical()
	if err != nil {
		return err
	}
	metadataRoot := filepath.ToSlash(filepath.Join(
		input.Inspection.OutputRoot, ".stackkit",
	))
	controls := []ExecutorStateBlobInput{
		{ID: "generation-manifest", Path: metadataRoot + "/" + generationartifact.ArtifactManifestFileName, Mode: "0600", Data: manifestBytes},
		{ID: "generation-receipt", Path: metadataRoot + "/" + generationartifact.GenerationReceiptFileName, Mode: "0600", Data: receiptBytes},
		{ID: "apply-result", Path: ".stackkit/evidence/apply/results/" + strings.TrimPrefix(executorStateDigest(input.ApplyResult), "sha256:") + ".json", Mode: "0600", Data: append([]byte(nil), input.ApplyResult...)},
		{ID: "apply-result-receipt", Path: ".stackkit/evidence/apply/receipts/" + strings.TrimPrefix(executorStateDigest(input.ApplyResult), "sha256:") + ".json", Mode: "0600", Data: append([]byte(nil), input.ApplyReceipt...)},
		{ID: "owner-runtime-binding", Path: ".stackkit/evidence/owner-runtime-binding.json", Mode: "0600", Data: runtimeBindingBytes},
	}
	for _, control := range controls {
		for _, artifact := range input.Capture.Artifacts {
			if artifact.ID == control.ID ||
				strings.EqualFold(filepathToSlash(artifact.Path), control.Path) {
				return errors.New(
					"legacy current state authority: control evidence collides with recovery artifact",
				)
			}
		}
		input.Capture.Artifacts = append(input.Capture.Artifacts, control)
	}
	return nil
}
