package upgradelifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	currentStateApplyReceiptAPIVersion = "stackkit.apply-result-receipt/v1"
	currentStateApplyReceiptKind       = "OwnerSignedApplyResultReceipt"
	currentStateKopiaPolicyArtifactID  = "local-kopia-backup-source-policy-instance-source-policy-node-main"
	currentStateComposeArtifactID      = "basement-core-compose-instance-compose-node-main"
)

type verifiedCurrentApplyResult struct {
	canonical  []byte
	resultHash string
}

// CurrentSourceVerifier re-resolves the exact recovery StackSpec and Inventory
// through a fresh Architecture-v2 CurrentResolution.
type CurrentSourceVerifier struct {
	resolve func(architecturev2.ResolveInput) (architecturev2.Result, error)
}

func NewCurrentSourceVerifier(service *architecturev2.Service) (CurrentSourceVerifier, error) {
	if service == nil {
		return CurrentSourceVerifier{}, errors.New("current state authority: source verifier service is required")
	}
	return CurrentSourceVerifier{
		resolve: func(input architecturev2.ResolveInput) (architecturev2.Result, error) {
			current, err := service.ResolveCurrent(input)
			if err != nil {
				return architecturev2.Result{}, err
			}
			return current.Result()
		},
	}, nil
}

// CurrentApplyResultVerifier is an opaque adapter around the Architecture-v2
// Apply verifier. Callers cannot substitute a verifier function.
type CurrentApplyResultVerifier struct {
	verify func(architecturev2.ProductApplyResultVerificationInput) (verifiedCurrentApplyResult, error)
}

func NewCurrentApplyResultVerifier(service *architecturev2.Service) (CurrentApplyResultVerifier, error) {
	if service == nil {
		return CurrentApplyResultVerifier{}, errors.New("current state authority: Apply verifier service is required")
	}
	return CurrentApplyResultVerifier{
		verify: func(input architecturev2.ProductApplyResultVerificationInput) (verifiedCurrentApplyResult, error) {
			verified, err := service.VerifyProductApplyResult(input)
			if err != nil {
				return verifiedCurrentApplyResult{}, err
			}
			canonical, err := verified.Canonical()
			if err != nil {
				return verifiedCurrentApplyResult{}, err
			}
			return verifiedCurrentApplyResult{
				canonical: append([]byte(nil), canonical...), resultHash: verified.ResultHash(),
			}, nil
		},
	}, nil
}

// CurrentStateAuthorityInput contains the complete already-resolved current
// state. NewVerifiedExecutorStateCapture re-verifies every authority edge and
// returns the only handle accepted by ExecutorStateStore.
type CurrentStateAuthorityInput struct {
	WorkspaceRoot     string
	Plan              generationartifact.VerifiedPlan
	Manifest          generationartifact.ArtifactManifest
	GenerationReceipt generationartifact.GenerationReceipt
	Versions          generationartifact.ComponentVersions
	ApplyResult       []byte
	ApplyReceipt      []byte
	SourceVerifier    CurrentSourceVerifier
	ApplyVerifier     CurrentApplyResultVerifier
	Capture           ExecutorStateCaptureInput
}

type currentStateApplyReceipt struct {
	APIVersion string                                  `json:"apiVersion"`
	Kind       string                                  `json:"kind"`
	ResultHash string                                  `json:"resultHash"`
	Signature  localevidence.OwnerApplyResultSignature `json:"signature"`
}

// NewVerifiedExecutorStateCapture is the production authority constructor for
// one immutable current-state recovery closure. It performs no persistence.
func NewVerifiedExecutorStateCapture(input CurrentStateAuthorityInput) (VerifiedExecutorStateCapture, error) {
	cloned, err := cloneCurrentStateAuthorityInput(input)
	if err != nil {
		return VerifiedExecutorStateCapture{}, err
	}
	input = cloned
	if strings.TrimSpace(input.WorkspaceRoot) == "" ||
		input.SourceVerifier.resolve == nil ||
		input.ApplyVerifier.verify == nil {
		return VerifiedExecutorStateCapture{}, errors.New("current state authority: workspace, source verifier, and Apply verifier are required")
	}
	var inventory []byte
	if input.Capture.Inventory != nil {
		inventory = append([]byte(nil), input.Capture.Inventory.Data...)
	}
	current, err := input.SourceVerifier.resolve(architecturev2.ResolveInput{
		StackSpec: append([]byte(nil), input.Capture.StackSpec.Data...),
		Inventory: inventory,
	})
	if err != nil {
		return VerifiedExecutorStateCapture{}, fmt.Errorf("current state authority: freshly resolve recovery sources: %w", err)
	}
	if current.PlanHash != input.Plan.Binding().PlanHash ||
		!bytes.Equal(current.CanonicalPlan, input.Plan.Canonical()) {
		return VerifiedExecutorStateCapture{}, errors.New("current state authority: StackSpec or Inventory resolves to a different current Plan")
	}
	if err := generationartifact.VerifyExecution(generationartifact.ExecutionGateInput{
		CurrentCanonical: current.CanonicalPlan,
		Plan:             input.Plan,
		Phase:            generationartifact.ExecutionPhaseApply,
		Versions:         input.Versions,
		Root:             input.WorkspaceRoot,
		Manifest:         input.Manifest,
		Receipt:          input.GenerationReceipt,
	}); err != nil {
		return VerifiedExecutorStateCapture{}, fmt.Errorf("current state authority: verify current generation closure: %w", err)
	}

	verifiedApply, err := input.ApplyVerifier.verify(architecturev2.ProductApplyResultVerificationInput{
		Plan: input.Plan, Manifest: input.Manifest, Receipt: input.GenerationReceipt,
		Versions: input.Versions, Result: append([]byte(nil), input.ApplyResult...),
	})
	if err != nil {
		return VerifiedExecutorStateCapture{}, fmt.Errorf("current state authority: verify current Apply result: %w", err)
	}
	if verifiedApply.resultHash == "" || !bytes.Equal(verifiedApply.canonical, input.ApplyResult) ||
		executorStateDigest(input.ApplyResult) != verifiedApply.resultHash {
		return VerifiedExecutorStateCapture{}, errors.New("current state authority: verified Apply result differs from exact canonical bytes")
	}
	applyReceiptHash, err := verifyCurrentStateApplyReceipt(
		input.WorkspaceRoot, verifiedApply.canonical, verifiedApply.resultHash, input.ApplyReceipt,
	)
	if err != nil {
		return VerifiedExecutorStateCapture{}, err
	}

	manifestHash, err := input.Manifest.Hash()
	if err != nil {
		return VerifiedExecutorStateCapture{}, fmt.Errorf("current state authority: hash manifest: %w", err)
	}
	generationReceiptHash, err := input.GenerationReceipt.Hash()
	if err != nil {
		return VerifiedExecutorStateCapture{}, fmt.Errorf("current state authority: hash generation receipt: %w", err)
	}
	lineage := input.Capture.Lineage
	if lineage.Binding != input.Plan.Binding() ||
		lineage.ManifestHash != manifestHash ||
		lineage.GenerationReceiptHash != generationReceiptHash ||
		lineage.ApplyResultHash != verifiedApply.resultHash ||
		lineage.ApplyReceiptHash != applyReceiptHash {
		return VerifiedExecutorStateCapture{}, errors.New("current state authority: capture lineage differs from Plan, Generation, or Apply authority")
	}

	owner, err := localevidence.LoadOwnerCustody(input.WorkspaceRoot)
	if err != nil {
		return VerifiedExecutorStateCapture{}, fmt.Errorf("current state authority: verify Owner custody: %w", err)
	}
	runtimeBinding, err := localevidence.LoadOwnerRuntimeBinding(input.WorkspaceRoot)
	if err != nil {
		return VerifiedExecutorStateCapture{}, fmt.Errorf("current state authority: verify Owner runtime binding: %w", err)
	}
	if runtimeBinding.OwnerRef != owner.OwnerRef ||
		runtimeBinding.PocketIDSubject != lineage.PocketIDSubject ||
		localevidence.OwnerRuntimeBindingDigest(runtimeBinding) != lineage.OwnerBindingDigest {
		return VerifiedExecutorStateCapture{}, errors.New("current state authority: PocketID Owner binding differs from capture lineage")
	}
	runtimeBindingBytes, err := json.MarshalIndent(runtimeBinding, "", "  ")
	if err != nil {
		return VerifiedExecutorStateCapture{}, fmt.Errorf("current state authority: encode verified Owner runtime binding: %w", err)
	}

	policyDigest, err := verifyCurrentStateArtifacts(input, owner)
	if err != nil {
		return VerifiedExecutorStateCapture{}, err
	}
	if input.Capture.KopiaSnapshotAnchor.PolicyArtifactDigest != policyDigest ||
		input.Capture.KopiaSnapshotAnchor.OperationID != "backup-"+input.Capture.OperationID {
		return VerifiedExecutorStateCapture{}, errors.New("current state authority: snapshot anchor differs from current policy or upgrade operation")
	}
	if err := verifyExecutorStateSnapshotAnchorWithAuthority(
		input.WorkspaceRoot, owner.OwnerRef, lineage, input.Capture.KopiaSnapshotAnchor,
		owner, runtimeBinding,
	); err != nil {
		return VerifiedExecutorStateCapture{}, err
	}
	release, err := verifyExecutorStateReleaseProof(
		input.Capture.Release, input.Capture.Executable.Blob.Data,
	)
	if err != nil {
		return VerifiedExecutorStateCapture{}, err
	}
	if err := appendCurrentStateControlBlobs(&input, runtimeBindingBytes); err != nil {
		return VerifiedExecutorStateCapture{}, err
	}
	input.Capture.Release = releaseindex.VerifiedInstallation{}
	return VerifiedExecutorStateCapture{
		token:   &verifiedExecutorStateCaptureToken{},
		input:   executorStateCaptureInput(input.Capture),
		release: release,
	}, nil
}

func verifyCurrentStateApplyReceipt(
	workspaceRoot string,
	canonicalResult []byte,
	resultHash string,
	canonicalReceipt []byte,
) (string, error) {
	var receipt currentStateApplyReceipt
	if err := decodeExactJSON(canonicalReceipt, &receipt); err != nil {
		return "", fmt.Errorf("current state authority: decode Owner Apply receipt: %w", err)
	}
	reencoded, err := resolvedplan.CanonicalJSON(receipt)
	if err != nil || !bytes.Equal(reencoded, canonicalReceipt) {
		return "", errors.New("current state authority: Owner Apply receipt is not canonical")
	}
	if receipt.APIVersion != currentStateApplyReceiptAPIVersion ||
		receipt.Kind != currentStateApplyReceiptKind ||
		receipt.ResultHash != resultHash {
		return "", errors.New("current state authority: Owner Apply receipt differs from verified Apply result")
	}
	if err := localevidence.VerifyOwnerApplyResult(workspaceRoot, canonicalResult, receipt.Signature); err != nil {
		return "", fmt.Errorf("current state authority: verify Owner Apply receipt: %w", err)
	}
	return executorStateDigest(canonicalReceipt), nil
}

func verifyCurrentStateArtifacts(
	input CurrentStateAuthorityInput,
	owner localevidence.OwnerCustody,
) (string, error) {
	if len(input.Capture.Artifacts) != len(input.Manifest.Artifacts) {
		return "", errors.New("current state authority: capture artifact set differs from complete manifest")
	}
	captured := make(map[string]ExecutorStateBlobInput, len(input.Capture.Artifacts))
	for _, artifact := range input.Capture.Artifacts {
		if _, duplicate := captured[artifact.ID]; duplicate {
			return "", errors.New("current state authority: duplicate capture artifact ID")
		}
		captured[artifact.ID] = artifact
	}
	var policyManifest generationartifact.RenderedArtifact
	var policyBytes, composeBytes []byte
	for _, manifestArtifact := range input.Manifest.Artifacts {
		artifact, exists := captured[manifestArtifact.ID]
		if !exists || artifact.Mode != manifestArtifact.Mode ||
			executorStateDigest(artifact.Data) != manifestArtifact.SHA256 {
			return "", fmt.Errorf("current state authority: artifact %q differs from verified manifest", manifestArtifact.ID)
		}
		expectedPath := manifestArtifact.Path
		if manifestArtifact.ID == currentStateComposeArtifactID {
			expectedPath = basementCoreComposeArtifactPath
			composeBytes = artifact.Data
		}
		if filepathToSlash(artifact.Path) != expectedPath {
			return "", fmt.Errorf("current state authority: artifact %q recovery path differs from governed path", manifestArtifact.ID)
		}
		if manifestArtifact.ID == currentStateKopiaPolicyArtifactID {
			policyManifest = manifestArtifact
			policyBytes = artifact.Data
		}
	}
	if policyManifest.ID == "" || len(policyBytes) == 0 {
		return "", errors.New("current state authority: exact local Kopia policy artifact is required")
	}
	policyDigest, err := localbackuppolicy.Digest(policyBytes)
	if err != nil || policyDigest != policyManifest.SHA256 {
		return "", errors.New("current state authority: local Kopia policy digest differs from manifest")
	}
	policy, err := localbackuppolicy.Decode(policyBytes)
	if err != nil {
		return "", fmt.Errorf("current state authority: decode local Kopia policy: %w", err)
	}
	if policy.Target.SiteRef != owner.Binding.SiteRef || policy.Target.NodeRef != owner.Binding.NodeRef {
		return "", errors.New("current state authority: local Kopia policy target differs from Owner custody")
	}
	if len(composeBytes) == 0 || !bytes.Equal(composeBytes, input.Capture.RuntimeCompose.Data) ||
		input.Capture.RuntimeCompose.Path != basementCoreRuntimeComposePath {
		return "", errors.New("current state authority: runtime Compose differs from governed generation artifact")
	}
	return policyDigest, nil
}

func appendCurrentStateControlBlobs(
	input *CurrentStateAuthorityInput,
	runtimeBindingBytes []byte,
) error {
	_, manifestPath, receiptPath := input.Plan.MetadataPaths(input.WorkspaceRoot)
	manifestBytes, err := input.Manifest.MarshalCanonical()
	if err != nil {
		return err
	}
	receiptBytes, err := input.GenerationReceipt.MarshalCanonical()
	if err != nil {
		return err
	}
	controls := []ExecutorStateBlobInput{
		{ID: "generation-manifest", Path: currentStateRelativePath(input.WorkspaceRoot, manifestPath), Mode: "0600", Data: manifestBytes},
		{ID: "generation-receipt", Path: currentStateRelativePath(input.WorkspaceRoot, receiptPath), Mode: "0600", Data: receiptBytes},
		{ID: "apply-result", Path: ".stackkit/evidence/apply/results/" + strings.TrimPrefix(executorStateDigest(input.ApplyResult), "sha256:") + ".json", Mode: "0600", Data: append([]byte(nil), input.ApplyResult...)},
		{ID: "apply-result-receipt", Path: ".stackkit/evidence/apply/receipts/" + strings.TrimPrefix(executorStateDigest(input.ApplyResult), "sha256:") + ".json", Mode: "0600", Data: append([]byte(nil), input.ApplyReceipt...)},
		{ID: "owner-runtime-binding", Path: ".stackkit/evidence/owner-runtime-binding.json", Mode: "0600", Data: runtimeBindingBytes},
	}
	for _, control := range controls {
		for _, artifact := range input.Capture.Artifacts {
			if artifact.ID == control.ID || strings.EqualFold(filepathToSlash(artifact.Path), control.Path) {
				return errors.New("current state authority: control evidence collides with recovery artifact")
			}
		}
		input.Capture.Artifacts = append(input.Capture.Artifacts, control)
	}
	return nil
}

func currentStateRelativePath(root, target string) string {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return filepathToSlash(target)
	}
	return filepathToSlash(relative)
}

func cloneExecutorStateCaptureInput(input ExecutorStateCaptureInput) (ExecutorStateCaptureInput, error) {
	cloneBlob := func(blob ExecutorStateBlobInput) ExecutorStateBlobInput {
		blob.Data = append([]byte(nil), blob.Data...)
		return blob
	}
	cloned := input
	cloned.Executable.Blob = cloneBlob(input.Executable.Blob)
	cloned.StackSpec = cloneBlob(input.StackSpec)
	if input.Inventory != nil {
		inventory := cloneBlob(*input.Inventory)
		cloned.Inventory = &inventory
	}
	cloned.Artifacts = make([]ExecutorStateBlobInput, len(input.Artifacts))
	for index, artifact := range input.Artifacts {
		cloned.Artifacts[index] = cloneBlob(artifact)
	}
	cloned.RuntimeCompose = cloneBlob(input.RuntimeCompose)
	canonicalAnchor, err := resolvedplan.CanonicalJSON(input.KopiaSnapshotAnchor)
	if err != nil {
		return ExecutorStateCaptureInput{}, fmt.Errorf("current state authority: clone snapshot anchor: %w", err)
	}
	if err := decodeExactJSON(canonicalAnchor, &cloned.KopiaSnapshotAnchor); err != nil {
		return ExecutorStateCaptureInput{}, fmt.Errorf("current state authority: clone snapshot anchor: %w", err)
	}
	return cloned, nil
}

func cloneCurrentStateAuthorityInput(input CurrentStateAuthorityInput) (CurrentStateAuthorityInput, error) {
	cloned := input
	cloned.Manifest.Artifacts = append(
		[]generationartifact.RenderedArtifact(nil),
		input.Manifest.Artifacts...,
	)
	cloned.ApplyResult = append([]byte(nil), input.ApplyResult...)
	cloned.ApplyReceipt = append([]byte(nil), input.ApplyReceipt...)
	capture, err := cloneExecutorStateCaptureInput(input.Capture)
	if err != nil {
		return CurrentStateAuthorityInput{}, err
	}
	cloned.Capture = capture
	return cloned, nil
}
