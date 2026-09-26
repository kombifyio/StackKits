package upgradelifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/opentofu"
)

const (
	currentStateApplyReceiptAPIVersion = "stackkit.apply-result-receipt/v1"
	currentStateApplyReceiptKind       = "OwnerSignedApplyResultReceipt"
	currentStateKopiaPolicyArtifactID  = "local-kopia-backup-source-policy-instance-source-policy-node-main"
	currentStateComposeArtifactID      = "basement-core-compose-instance-compose-node-main"
)

type verifiedCurrentApplyResult struct {
	canonical      []byte
	resultHash     string
	runtimeCustody ExecutorStateBlobInput
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

func NewCurrentApplyResultVerifier(ctx context.Context, service *architecturev2.Service, journal *architecturev2.ProductApplyFileJournal) (CurrentApplyResultVerifier, error) {
	if ctx == nil || service == nil || journal == nil {
		return CurrentApplyResultVerifier{}, errors.New("current state authority: Apply verifier context, service, and runtime custody are required")
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
			custody, err := journal.LoadVerifiedAppliedRuntimeCustody(ctx, input.Plan, verified)
			if err != nil {
				return verifiedCurrentApplyResult{}, fmt.Errorf("current state authority: retain applied runtime custody: %w", err)
			}
			return verifiedCurrentApplyResult{
				canonical: append([]byte(nil), canonical...), resultHash: verified.ResultHash(),
				runtimeCustody: ExecutorStateBlobInput{
					ID: "applied-runtime-custody", Path: custody.Path(), Mode: "0600", Data: custody.Canonical(),
				},
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
	Legacy            *LegacyCurrentStateAuthorityInput
}

// CurrentStateCoreProfile is the exact Core profile and artifact pair used by
// the current Plan-owned recovery closure. The IDs come from ApplyRequirements;
// the stable profile identity and Compose output come from the existing local
// runtime profile registry.
//
// ComposeArtifactID names the governed artifact that carries the Core Compose
// payload and CoreArtifactOutputRef is its recovery path: the Compose artifact
// itself under the compose target, the Core OpenTofu root main.tf (which
// embeds the same payload byte for byte) under the opentofu and terramate
// targets. Every other field is identical across the three targets.
type CurrentStateCoreProfile struct {
	ModuleRef             string
	ComposeArtifactID     string
	ComposeOutputRef      string
	CoreArtifactOutputRef string
	PolicyArtifactID      string
	PolicyOutputRef       string
	// RuntimeComposePath is the native runtime Compose file the compose
	// target captures (Basement or Cloud standalone core runtime directory).
	RuntimeComposePath string
}

// CoreComposePayload returns the Core Compose payload the profile's carrier
// artifact holds: the artifact itself under the compose target, or the payload
// extracted from the Core OpenTofu root under the opentofu and terramate
// targets.
func (profile CurrentStateCoreProfile) CoreComposePayload(artifact []byte) ([]byte, error) {
	if profile.CoreArtifactOutputRef == "" || profile.CoreArtifactOutputRef == profile.ComposeOutputRef {
		return append([]byte(nil), artifact...), nil
	}
	payload, err := architecturev2renderer.ExtractComposePayload(artifact)
	if err != nil {
		return nil, fmt.Errorf("current state authority: derive the Core Compose payload: %w", err)
	}
	return payload, nil
}

// CurrentStateCoreProfileForPlan selects the one local Core runtime (Basement
// Full or Lite, or the Cloud standalone core) and its Compose/source-policy
// artifacts from the verified Apply requirements.
// It never derives a profile from an artifact filename or generated bytes.
func CurrentStateCoreProfileForPlan(
	plan generationartifact.VerifiedPlan,
	siteRef string,
	nodeRef string,
) (CurrentStateCoreProfile, error) {
	if strings.TrimSpace(siteRef) == "" || strings.TrimSpace(nodeRef) == "" {
		return CurrentStateCoreProfile{}, errors.New("current state authority: Core profile selection requires an Owner site and node")
	}
	target, err := GenerationTargetForPlan(plan)
	if err != nil {
		return CurrentStateCoreProfile{}, fmt.Errorf("current state authority: %w", err)
	}
	requirements := plan.ApplyRequirements()
	var runtime generationartifact.ApplyRuntimeRequirement
	var runtimeProfile nativehost.LocalCoreRecoveryProfile
	runtimeMatches := 0
	for _, candidate := range requirements.RuntimeInstances {
		profile, supported := nativehost.LocalCoreRecoveryProfileForModule(candidate.ModuleRef)
		unitRef := profile.UnitRef
		if target != executorStateTargetCompose {
			// The OpenTofu and Terramate twins are named after their target.
			unitRef = target
		}
		if !supported || candidate.OwnerKind != "module" || candidate.OwnerRef != candidate.ModuleRef ||
			candidate.ProviderRef != profile.ProviderRef || candidate.ModuleRef != profile.ModuleRef ||
			candidate.UnitRef != unitRef || candidate.WorkloadRef != profile.WorkloadRef ||
			candidate.RuntimeEngine != "docker" || candidate.RuntimeDelivery != "stackkit" ||
			candidate.RuntimeKind != "container" || len(candidate.SiteRefs) != 1 || candidate.SiteRefs[0] != siteRef ||
			len(candidate.NodeRefs) != 1 || candidate.NodeRefs[0] != nodeRef {
			continue
		}
		runtime = candidate
		runtimeProfile = profile
		runtimeMatches++
	}
	if runtimeMatches != 1 {
		return CurrentStateCoreProfile{}, errors.New("current state authority: Apply requirements must select exactly one local Core runtime with a Kopia source")
	}

	// The compose target carries the Core Compose artifact; the opentofu and
	// terramate targets carry the Core root main.tf that embeds it.
	coreKind, coreFormat, coreOutputRef := "compose", "yaml", runtimeProfile.OutputRef
	if target != executorStateTargetCompose {
		coreKind, coreFormat = target, "hcl"
		coreOutputRef = path.Join(path.Dir(runtimeProfile.OutputRef), opentofu.ConfigFile)
	}
	var compose, policy generationartifact.ApplyArtifactRequirement
	composeMatches, policyMatches := 0, 0
	for _, candidate := range requirements.Artifacts {
		if candidate.OwnerKind != "render-instance" || candidate.ModuleRef != runtime.ModuleRef ||
			candidate.ProviderRef != runtime.ProviderRef || candidate.ProviderContractHash != runtime.ProviderContractHash ||
			candidate.ModuleContractHash != runtime.ModuleContractHash || len(candidate.SiteRefs) != 1 || candidate.SiteRefs[0] != siteRef ||
			len(candidate.NodeRefs) != 1 || candidate.NodeRefs[0] != nodeRef {
			continue
		}
		switch {
		case candidate.UnitRef == runtime.UnitRef && candidate.InstanceRef == runtime.InstanceRef &&
			candidate.Kind == coreKind && candidate.Format == coreFormat && candidate.Mode == "0640" &&
			candidate.ExecutionClass == generationartifact.ApplyExecutionClassExecutable &&
			candidate.OutputRef == coreOutputRef && containsString(runtime.ArtifactRefs, candidate.ID):
			compose = candidate
			composeMatches++
		case candidate.UnitRef == "source-policy" && candidate.Kind == "native-config" && candidate.Format == "json" &&
			candidate.Mode == "0600" && candidate.ExecutionClass == generationartifact.ApplyExecutionClassArtifactOnly:
			policy = candidate
			policyMatches++
		}
	}
	if composeMatches != 1 || policyMatches != 1 || compose.ID == "" || policy.ID == "" || policy.OutputRef == "" {
		return CurrentStateCoreProfile{}, errors.New("current state authority: Core profile requires exactly one governed Compose and source-policy artifact")
	}
	return CurrentStateCoreProfile{
		ModuleRef: runtime.ModuleRef, ComposeArtifactID: compose.ID, ComposeOutputRef: runtimeProfile.OutputRef,
		CoreArtifactOutputRef: compose.OutputRef,
		PolicyArtifactID:      policy.ID, PolicyOutputRef: policy.OutputRef,
		RuntimeComposePath: runtimeProfile.RuntimeComposePath,
	}, nil
}

func currentStateCoreProfileForCapture(
	input ExecutorStateCaptureInput,
) (CurrentStateCoreProfile, error) {
	moduleRef := strings.TrimSpace(input.CoreModuleRef)
	if moduleRef == "" {
		// Snapshots produced before profile binding were Full-Core only. Keep
		// that persisted format readable while requiring explicit metadata for
		// every new CoreLite snapshot.
		moduleRef = localbackuppolicy.CoreModuleRef
	}
	runtimeProfile, supported := nativehost.LocalCoreRecoveryProfileForModule(moduleRef)
	if !supported {
		return CurrentStateCoreProfile{}, errors.New("current state authority: executor-state Core profile is unsupported")
	}
	if moduleRef != localbackuppolicy.CoreModuleRef &&
		(strings.TrimSpace(input.CoreComposeArtifactID) == "" || strings.TrimSpace(input.CorePolicyArtifactID) == "") {
		return CurrentStateCoreProfile{}, errors.New("current state authority: CoreLite and Cloud executor-state captures require explicit profile artifact identities")
	}
	return CurrentStateCoreProfile{
		ModuleRef:          moduleRef,
		ComposeArtifactID:  strings.TrimSpace(input.CoreComposeArtifactID),
		ComposeOutputRef:   runtimeProfile.OutputRef,
		PolicyArtifactID:   strings.TrimSpace(input.CorePolicyArtifactID),
		RuntimeComposePath: runtimeProfile.RuntimeComposePath,
	}, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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
	if target, err := GenerationTargetForPlan(input.Plan); err != nil || target != input.Capture.GenerationTarget {
		return VerifiedExecutorStateCapture{}, errors.New("current state authority: capture generation target differs from the current Plan")
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

	profile, policyDigest, err := verifyCurrentStateArtifactsForProfile(input, owner)
	if err != nil {
		return VerifiedExecutorStateCapture{}, err
	}
	if err := appendStandaloneComposeRuntimeCustody(&input); err != nil {
		return VerifiedExecutorStateCapture{}, err
	}
	runtimeGraph, err := currentStateRuntimeRecoveryGraph(input)
	if err != nil {
		return VerifiedExecutorStateCapture{}, err
	}
	input.Capture.CoreModuleRef = profile.ModuleRef
	input.Capture.CoreComposeArtifactID = profile.ComposeArtifactID
	input.Capture.CorePolicyArtifactID = profile.PolicyArtifactID
	if input.Capture.KopiaSnapshotAnchor.PolicyArtifactDigest != policyDigest ||
		input.Capture.KopiaSnapshotAnchor.OperationID != "backup-"+input.Capture.OperationID {
		return VerifiedExecutorStateCapture{}, errors.New("current state authority: snapshot anchor differs from current policy or upgrade operation")
	}
	if err := verifyExecutorStateSnapshotAnchorWithAuthority(
		input.WorkspaceRoot, owner.OwnerRef, profile.ModuleRef, lineage, input.Capture.KopiaSnapshotAnchor,
		owner, runtimeBinding,
	); err != nil {
		return VerifiedExecutorStateCapture{}, err
	}
	release, err := verifyExecutorStateCaptureRelease(
		input.Capture.Release, input.Capture.RunningRelease, input.Capture.Executable,
	)
	if err != nil {
		return VerifiedExecutorStateCapture{}, err
	}
	if err := appendCurrentStateControlBlobs(&input, runtimeBindingBytes, verifiedApply.runtimeCustody, runtimeGraph); err != nil {
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

func verifyCurrentStateArtifactsForProfile(
	input CurrentStateAuthorityInput,
	owner localevidence.OwnerCustody,
) (CurrentStateCoreProfile, string, error) {
	profile := CurrentStateCoreProfile{
		ModuleRef:         localbackuppolicy.CoreModuleRef,
		ComposeArtifactID: currentStateComposeArtifactID,
		ComposeOutputRef:  basementCoreComposeArtifactPath,
		PolicyArtifactID:  currentStateKopiaPolicyArtifactID,
		// The legacy fieldless path is Full-Core only.
		RuntimeComposePath: basementCoreRuntimeComposePath,
	}
	if len(input.Plan.Canonical()) != 0 {
		var err error
		profile, err = CurrentStateCoreProfileForPlan(
			input.Plan, owner.Binding.SiteRef, owner.Binding.NodeRef,
		)
		if err != nil {
			return CurrentStateCoreProfile{}, "", err
		}
	} else if input.Capture.CoreModuleRef != "" ||
		input.Capture.CoreComposeArtifactID != "" || input.Capture.CorePolicyArtifactID != "" {
		var err error
		profile, err = currentStateCoreProfileForCapture(input.Capture)
		if err != nil {
			return CurrentStateCoreProfile{}, "", err
		}
	}
	if len(input.Capture.Artifacts) != len(input.Manifest.Artifacts) {
		return CurrentStateCoreProfile{}, "", errors.New("current state authority: capture artifact set differs from complete manifest")
	}
	captured := make(map[string]ExecutorStateBlobInput, len(input.Capture.Artifacts))
	for _, artifact := range input.Capture.Artifacts {
		if _, duplicate := captured[artifact.ID]; duplicate {
			return CurrentStateCoreProfile{}, "", errors.New("current state authority: duplicate capture artifact ID")
		}
		captured[artifact.ID] = artifact
	}
	var policyManifest generationartifact.RenderedArtifact
	var policyBytes, coreArtifact []byte
	for _, manifestArtifact := range input.Manifest.Artifacts {
		artifact, exists := captured[manifestArtifact.ID]
		if !exists || artifact.Mode != manifestArtifact.Mode ||
			executorStateDigest(artifact.Data) != manifestArtifact.SHA256 {
			return CurrentStateCoreProfile{}, "", fmt.Errorf("current state authority: artifact %q differs from verified manifest", manifestArtifact.ID)
		}
		expectedPath := manifestArtifact.Path
		if manifestArtifact.ID == profile.ComposeArtifactID {
			expectedPath = profile.ComposeOutputRef
			if profile.CoreArtifactOutputRef != "" {
				expectedPath = profile.CoreArtifactOutputRef
			}
			coreArtifact = artifact.Data
		}
		if filepathToSlash(artifact.Path) != expectedPath {
			return CurrentStateCoreProfile{}, "", fmt.Errorf("current state authority: artifact %q recovery path differs from governed path", manifestArtifact.ID)
		}
		if manifestArtifact.ID == profile.PolicyArtifactID {
			policyManifest = manifestArtifact
			policyBytes = artifact.Data
		}
	}
	if policyManifest.ID == "" || len(policyBytes) == 0 {
		return CurrentStateCoreProfile{}, "", errors.New("current state authority: exact local Kopia policy artifact is required")
	}
	policyDigest, err := localbackuppolicy.Digest(policyBytes)
	if err != nil || policyDigest != policyManifest.SHA256 {
		return CurrentStateCoreProfile{}, "", errors.New("current state authority: local Kopia policy digest differs from manifest")
	}
	policy, err := localbackuppolicy.Decode(policyBytes)
	if err != nil {
		return CurrentStateCoreProfile{}, "", fmt.Errorf("current state authority: decode local Kopia policy: %w", err)
	}
	if profile.ModuleRef != localbackuppolicy.CoreModuleRef && policy.Source.CoreModuleRef != profile.ModuleRef {
		return CurrentStateCoreProfile{}, "", errors.New("current state authority: CoreLite or Cloud policy is not explicitly bound to its selected profile")
	}
	if policy.Source.CoreModuleRef != "" && policy.Source.CoreModuleRef != profile.ModuleRef {
		return CurrentStateCoreProfile{}, "", errors.New("current state authority: local Kopia policy differs from the selected Core profile")
	}
	if policy.Target.SiteRef != owner.Binding.SiteRef || policy.Target.NodeRef != owner.Binding.NodeRef {
		return CurrentStateCoreProfile{}, "", errors.New("current state authority: local Kopia policy target differs from Owner custody")
	}
	if len(coreArtifact) == 0 {
		return CurrentStateCoreProfile{}, "", errors.New("current state authority: runtime Compose differs from governed generation artifact")
	}
	composeBytes, err := profile.CoreComposePayload(coreArtifact)
	if err != nil {
		return CurrentStateCoreProfile{}, "", err
	}
	if executorStateTargetExecutesOpenTofu(input.Capture.GenerationTarget) {
		if err := verifyCurrentStateCoreOpenTofuRoot(input.Capture, profile, coreArtifact, composeBytes); err != nil {
			return CurrentStateCoreProfile{}, "", err
		}
		return profile, policyDigest, nil
	}
	if !bytes.Equal(composeBytes, input.Capture.RuntimeCompose.Data) ||
		input.Capture.RuntimeCompose.Path != profile.RuntimeComposePath {
		return CurrentStateCoreProfile{}, "", errors.New("current state authority: runtime Compose differs from governed generation artifact")
	}
	return profile, policyDigest, nil
}

// verifyCurrentStateCoreOpenTofuRoot binds the captured Core OpenTofu root of
// an opentofu or terramate install to the governed Core root artifact: its
// configuration is the artifact and the runtime Compose file it writes is the
// payload the artifact embeds, byte for byte.
func verifyCurrentStateCoreOpenTofuRoot(
	capture ExecutorStateCaptureInput,
	profile CurrentStateCoreProfile,
	coreArtifact []byte,
	composeBytes []byte,
) error {
	if capture.RuntimeCompose.ID != "" || capture.RuntimeCompose.Path != "" || len(capture.RuntimeCompose.Data) != 0 {
		return errors.New("current state authority: an OpenTofu install carries its runtime Compose in its Core OpenTofu root")
	}
	runtimeDir, _, ok := nativehost.NativeComposeProject(profile.ModuleRef)
	if !ok {
		return errors.New("current state authority: the selected Core module has no native runtime directory")
	}
	rootPath, err := opentofu.RootRelativePath(runtimeDir)
	if err != nil {
		return err
	}
	matches := 0
	for _, root := range capture.RuntimeOpenTofu {
		if root.Root != rootPath {
			continue
		}
		matches++
		if root.ModuleRef != profile.ModuleRef || !bytes.Equal(root.Config.Data, coreArtifact) ||
			root.Compose.Path != path.Join(path.Dir(rootPath), opentofu.ComposeFile) ||
			!bytes.Equal(root.Compose.Data, composeBytes) || len(root.State.Data) == 0 {
			return errors.New("current state authority: Core OpenTofu root differs from the governed Core root artifact and its Compose payload")
		}
	}
	if matches != 1 {
		return errors.New("current state authority: the selected Core module requires exactly one captured OpenTofu root")
	}
	return nil
}

func verifyCurrentStateArtifacts(
	input CurrentStateAuthorityInput,
	owner localevidence.OwnerCustody,
) (string, error) {
	_, policyDigest, err := verifyCurrentStateArtifactsForProfile(input, owner)
	return policyDigest, err
}

func appendStandaloneComposeRuntimeCustody(input *CurrentStateAuthorityInput) error {
	custody, err := restoreactivation.DeriveStandaloneComposeRuntimeCustody(
		input.WorkspaceRoot, input.Plan, input.Manifest, input.Capture.OperationID,
	)
	if err != nil {
		return fmt.Errorf("current state authority: derive standalone Application runtime custody: %w", err)
	}
	seenIDs := make(map[string]struct{}, len(input.Capture.Artifacts))
	seenPaths := make(map[string]struct{}, len(input.Capture.Artifacts))
	for _, artifact := range input.Capture.Artifacts {
		seenIDs[artifact.ID] = struct{}{}
		seenPaths[strings.ToLower(filepathToSlash(artifact.Path))] = struct{}{}
	}
	appendFile := func(id string, file restoreactivation.StandaloneComposeRuntimeFile) error {
		canonicalPath := filepathToSlash(file.Path)
		if !executorStateIDPattern.MatchString(id) {
			return errors.New("current state authority: standalone Application runtime custody ID is not portable")
		}
		if _, err := confinedfs.ValidatePortablePath(canonicalPath); err != nil {
			return fmt.Errorf("current state authority: standalone Application runtime custody path: %w", err)
		}
		if _, exists := seenIDs[id]; exists {
			return errors.New("current state authority: standalone Application runtime custody ID collides with recovery artifact")
		}
		pathKey := strings.ToLower(canonicalPath)
		for existing := range seenPaths {
			if executorStatePathsCollide(existing, pathKey) {
				return errors.New("current state authority: standalone Application runtime custody path collides with recovery artifact")
			}
		}
		if len(file.Data) == 0 && !executorStateStandaloneEnvironmentBlob(id, canonicalPath, file.Mode) {
			return errors.New("current state authority: only standalone Application environment custody may be empty")
		}
		seenIDs[id] = struct{}{}
		seenPaths[pathKey] = struct{}{}
		input.Capture.Artifacts = append(input.Capture.Artifacts, ExecutorStateBlobInput{
			ID: id, Path: canonicalPath, Mode: file.Mode, Data: append([]byte(nil), file.Data...),
		})
		return nil
	}
	openTofuRoots := make(map[string]ExecutorStateOpenTofuRootInput, len(input.Capture.RuntimeOpenTofu))
	for _, root := range input.Capture.RuntimeOpenTofu {
		openTofuRoots[root.Root] = root
	}
	for _, runtime := range custody {
		if executorStateTargetExecutesOpenTofu(input.Capture.GenerationTarget) {
			// The workload OpenTofu root already carries this project's
			// Compose file and .env; bind them instead of capturing twice.
			root, ok := openTofuRoots[path.Join(path.Dir(filepathToSlash(runtime.Compose.Path)), opentofu.RootDirName)]
			if !ok || filepathToSlash(root.Compose.Path) != filepathToSlash(runtime.Compose.Path) ||
				!bytes.Equal(root.Compose.Data, runtime.Compose.Data) ||
				filepathToSlash(root.Environment.Path) != filepathToSlash(runtime.Environment.Path) ||
				!bytes.Equal(root.Environment.Data, runtime.Environment.Data) {
				return errors.New("current state authority: standalone Application runtime differs from its captured OpenTofu root")
			}
		} else {
			if err := appendFile(executorStateStandaloneComposeID(runtime.Project), runtime.Compose); err != nil {
				return err
			}
			if err := appendFile(executorStateStandaloneEnvironmentID(runtime.Project), runtime.Environment); err != nil {
				return err
			}
		}
		for _, config := range runtime.ConfigFiles {
			if err := appendFile(executorStateStandaloneConfigID(runtime.Project, config.Path), config); err != nil {
				return err
			}
		}
	}
	return nil
}

func appendCurrentStateControlBlobs(
	input *CurrentStateAuthorityInput,
	runtimeBindingBytes []byte,
	runtimeCustody ExecutorStateBlobInput,
	runtimeGraph ExecutorStateBlobInput,
) error {
	if runtimeCustody.ID != "applied-runtime-custody" || runtimeCustody.Mode != "0600" || len(runtimeCustody.Data) == 0 {
		return errors.New("current state authority: verified applied runtime custody is required")
	}
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
		{ID: runtimeCustody.ID, Path: runtimeCustody.Path, Mode: runtimeCustody.Mode, Data: append([]byte(nil), runtimeCustody.Data...)},
		runtimeGraph,
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
	cloned.RuntimeOpenTofu = make([]ExecutorStateOpenTofuRootInput, len(input.RuntimeOpenTofu))
	for index, root := range input.RuntimeOpenTofu {
		cloned.RuntimeOpenTofu[index] = ExecutorStateOpenTofuRootInput{
			ModuleRef: root.ModuleRef, Root: root.Root,
			State: cloneBlob(root.State), Config: cloneBlob(root.Config), Compose: cloneBlob(root.Compose),
			Environment: cloneBlob(root.Environment),
		}
	}
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
