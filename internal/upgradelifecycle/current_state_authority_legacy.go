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
	legacyV08Version                = "v0.8.0"
	legacyV08ArchiveSHA256          = "sha256:0d6840ccb789b14840f0ff4c0b6e21fef290a639a0fd47eb8c65ae43cbcb13de"
	legacyV08IndexSHA256            = "sha256:54815571ccb3ac58c9551dfce80d3a331b2a8660e0e1f1acfd0cad22004c9beb"
	legacyV08AuthorityFingerprint   = "sha256:082998f2b0843eb6d5dd9cccaede88412c9c73753cfbc201ced8ca5cc19c87ca"
	legacyV08CatalogHash            = "sha256:36e536598cc040ffa6a440d75eeeeb8ff067b813e0cc5549a10d3e41a971d2ab"
	legacyV08DefinitionHash         = "sha256:e605db9c5c0f60b7b571a11ec234f051ca65902402b88aca8b07f74169306cf3"
	legacyV08CompilerVersion        = "stackkits-resolver/0.8.0"
	legacyV08RendererVersion        = "0.8.0"
	legacyV09Version                = "v0.9.0"
	legacyV09ArchiveSHA256          = "sha256:6d969b7fbe672a596df4226db4e695d3d4f51a75d8edfc8434f1b37201fef423"
	legacyV09IndexSHA256            = "sha256:0dd20f30dfaad350cc5fb8077d68f99f2df8dcdb673817ee4aefdfe4aff5e587"
	legacyV09AuthorityFingerprint   = "sha256:fe62ce4fe53cef67f225b772beaeffdafadf859e74a1721c8e04249e0f10e765"
	legacyV09CatalogHash            = "sha256:36e536598cc040ffa6a440d75eeeeb8ff067b813e0cc5549a10d3e41a971d2ab"
	legacyV09DefinitionHash         = "sha256:63ccaa8a22cb01177ba97ed46f003134c09893ba62f2a786b7cefaf9fca88c3a"
	legacyV09CompilerVersion        = "stackkits-resolver/0.9.0"
	legacyV09RendererVersion        = "0.9.0"
)

type allowedLegacyRelease struct {
	version       string
	channel       releaseindex.Channel
	archiveSHA256 string
	indexSHA256   string
}

// LegacyCurrentStateAuthorityInput is intentionally limited to the exact
// published beta.4, v0.8.0, and v0.9.0 authority discontinuities. The caller
// must still supply the immutable installed-release proof, owner-signed Apply
// evidence, complete generated artifact closure, and the candidate-created
// Kopia snapshot.
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
// It does not run the current CUE contract: the attested historical binary already
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
	allowedRelease, err := verifyAllowedLegacyInspection(input.Inspection)
	if err != nil {
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
		release.Version != allowedRelease.version ||
		release.Channel != allowedRelease.channel ||
		release.ArchiveSHA256 != allowedRelease.archiveSHA256 ||
		release.IndexSHA256 != allowedRelease.indexSHA256 {
		return VerifiedExecutorStateCapture{}, errors.New(
			"legacy current state authority: recovery release does not match the exact attested historical distribution",
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

func verifyAllowedLegacyInspection(
	inspection generationartifact.PlanInspection,
) (allowedLegacyRelease, error) {
	if err := verifyExactBeta4LegacyInspection(inspection); err == nil {
		return allowedLegacyRelease{
			version: legacyBeta4Version, channel: releaseindex.ChannelBeta,
			archiveSHA256: legacyBeta4ArchiveSHA256,
			indexSHA256:   legacyBeta4IndexSHA256,
		}, nil
	}
	if err := validatePlanInspection(inspection, "legacy v0.8 stable current"); err != nil {
		return allowedLegacyRelease{}, err
	}
	if matchesHistoricalStableInspection(
		inspection,
		legacyV08DefinitionHash,
		legacyV08CompilerVersion,
		legacyV08RendererVersion,
		legacyV08AuthorityFingerprint,
		legacyV08CatalogHash,
	) {
		return allowedLegacyRelease{
			version: legacyV08Version, channel: releaseindex.ChannelStable,
			archiveSHA256: legacyV08ArchiveSHA256,
			indexSHA256:   legacyV08IndexSHA256,
		}, nil
	}
	if matchesHistoricalStableInspection(
		inspection,
		legacyV09DefinitionHash,
		legacyV09CompilerVersion,
		legacyV09RendererVersion,
		legacyV09AuthorityFingerprint,
		legacyV09CatalogHash,
	) {
		return allowedLegacyRelease{
			version: legacyV09Version, channel: releaseindex.ChannelStable,
			archiveSHA256: legacyV09ArchiveSHA256,
			indexSHA256:   legacyV09IndexSHA256,
		}, nil
	}
	return allowedLegacyRelease{}, errors.New(
		"legacy current state authority: plan is outside the exact historical authority allowlist",
	)
}

func matchesHistoricalStableInspection(
	inspection generationartifact.PlanInspection,
	definitionHash string,
	compilerVersion string,
	rendererVersion string,
	authorityFingerprint string,
	catalogHash string,
) bool {
	authority := inspection.Binding.Authority
	return inspection.Binding.DefinitionHash == definitionHash &&
		inspection.Binding.CompilerVersion == compilerVersion &&
		inspection.Binding.Renderer.ID == "stackkit" &&
		inspection.Binding.Renderer.Version == rendererVersion &&
		authority.Class == "product" &&
		authority.Document == "catalog" &&
		authority.GraduationEligible &&
		authority.Issuer == "stackkits-product-authority/v1" &&
		authority.AuthorityFingerprint == authorityFingerprint &&
		authority.CatalogHash == catalogHash
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
