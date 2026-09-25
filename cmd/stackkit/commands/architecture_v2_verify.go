package commands

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutoropentofu"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
	"github.com/kombifyio/stackkits/internal/runtimeobservation"
	"github.com/kombifyio/stackkits/internal/upgradelifecycle"
)

const (
	maxArchitectureV2ApplyResults     = 256
	ownerApplyResultReceiptAPIVersion = "stackkit.apply-result-receipt/v1"
	ownerApplyResultReceiptKind       = "OwnerSignedApplyResultReceipt"
	architectureV2ApplyEvidenceRoot   = ".stackkit/evidence/apply"
)

type ownerApplyResultReceipt struct {
	APIVersion string                                  `json:"apiVersion"`
	Kind       string                                  `json:"kind"`
	ResultHash string                                  `json:"resultHash"`
	Signature  localevidence.OwnerApplyResultSignature `json:"signature"`
}

func newOwnerApplyResultReceipt(workspaceRoot string, result architecturev2.VerifiedApplyResult) (ownerApplyResultReceipt, []byte, error) {
	canonicalResult, err := result.Canonical()
	if err != nil {
		return ownerApplyResultReceipt{}, nil, err
	}
	return newOwnerApplyResultReceiptForCanonical(workspaceRoot, canonicalResult, result.ResultHash())
}

func newOwnerApplyResultReceiptForCanonical(workspaceRoot string, canonicalResult []byte, resultHash string) (ownerApplyResultReceipt, []byte, error) {
	signature, err := localevidence.SignOwnerApplyResult(workspaceRoot, canonicalResult)
	if err != nil {
		return ownerApplyResultReceipt{}, nil, fmt.Errorf("sign canonical Architecture v2 Apply result: %w", err)
	}
	receipt := ownerApplyResultReceipt{
		APIVersion: ownerApplyResultReceiptAPIVersion, Kind: ownerApplyResultReceiptKind,
		ResultHash: resultHash, Signature: signature,
	}
	canonicalReceipt, err := resolvedplan.CanonicalJSON(receipt)
	if err != nil {
		return ownerApplyResultReceipt{}, nil, err
	}
	return receipt, canonicalReceipt, nil
}

func verifyOwnerApplyResultReceipt(workspaceRoot string, canonicalResult, canonicalReceipt []byte, resultHash string) error {
	var receipt ownerApplyResultReceipt
	decoder := json.NewDecoder(bytes.NewReader(canonicalReceipt))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return fmt.Errorf("decode owner-signed Apply result receipt: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("owner-signed Apply result receipt contains multiple JSON values")
	}
	want, err := resolvedplan.CanonicalJSON(receipt)
	if err != nil || !bytes.Equal(want, canonicalReceipt) ||
		receipt.APIVersion != ownerApplyResultReceiptAPIVersion ||
		receipt.Kind != ownerApplyResultReceiptKind || receipt.ResultHash != resultHash {
		return errors.New("owner-signed Apply result receipt is malformed or not canonical")
	}
	if err := localevidence.VerifyOwnerApplyResult(workspaceRoot, canonicalResult, receipt.Signature); err != nil {
		return fmt.Errorf("verify owner-signed Apply result receipt: %w", err)
	}
	return nil
}

type architectureV2VerifyReport struct {
	SchemaVersion string                              `json:"schemaVersion"`
	Offline       bool                                `json:"offline"`
	PlanHash      string                              `json:"planHash"`
	Apply         architecturev2.ApplyResultSummary   `json:"apply"`
	Owner         architectureV2OwnerVerifySummary    `json:"owner"`
	Runtime       *architectureV2RuntimeVerifySummary `json:"runtime,omitempty"`
	Observations  []runtimeobservation.Observation    `json:"observations"`
	Releases      []releaseindex.Receipt              `json:"releases"`
}

type architectureV2OwnerVerifySummary struct {
	OwnerRef           string `json:"ownerRef"`
	KeyID              string `json:"keyId"`
	PocketIDSubject    string `json:"pocketIdSubject"`
	OwnerBindingDigest string `json:"ownerBindingDigest"`
}

type architectureV2RuntimeVerifySummary struct {
	ExecutionMode string `json:"executionMode"`
	Live          bool   `json:"live"`
	ProjectRef    string `json:"projectRef,omitempty"`
	Status        string `json:"status"`
	ServiceCount  int    `json:"serviceCount"`
	ProbeCount    int    `json:"probeCount"`
	cloud         *runtimeexecutorlocal.CloudCoreVerifyObservation
}

func verifyArchitectureV2LocalState(
	ctx context.Context,
	workspaceRoot string,
	plan generationartifact.VerifiedPlan,
	manifest generationartifact.ArtifactManifest,
	offline bool,
	appliedRequests ...runtimeexecutor.ExecutionRequest,
) (architectureV2OwnerVerifySummary, *architectureV2RuntimeVerifySummary, error) {
	if len(appliedRequests) > 1 {
		return architectureV2OwnerVerifySummary{}, nil, errors.New("local Architecture v2 Verify accepts at most one applied runtime request")
	}
	var appliedRequest runtimeexecutor.ExecutionRequest
	if len(appliedRequests) == 1 {
		appliedRequest = appliedRequests[0]
	}
	ownerSummary, localBinding, err := verifyArchitectureV2OwnerCustody(workspaceRoot)
	if err != nil {
		return architectureV2OwnerVerifySummary{}, nil, err
	}
	kitSlug, address, err := architectureV2LocalVerifyIdentity(plan)
	if err != nil {
		return architectureV2OwnerVerifySummary{}, nil, err
	}
	if kitSlug == "cloud-kit" {
		return verifyArchitectureV2LocalCloudState(ctx, workspaceRoot, appliedRequest, ownerSummary, localBinding, address, offline)
	}
	if kitSlug != "basement-kit" {
		return architectureV2OwnerVerifySummary{}, nil, fmt.Errorf("local Architecture v2 Verify is not implemented for kit %q", kitSlug)
	}
	if _, err := localevidence.LoadBasementRuntimeCustody(workspaceRoot); err != nil {
		return architectureV2OwnerVerifySummary{}, nil, fmt.Errorf("verify local Basement runtime custody: %w", err)
	}
	binding, err := localevidence.LoadOwnerRuntimeBinding(workspaceRoot)
	if err != nil {
		return architectureV2OwnerVerifySummary{}, nil, fmt.Errorf("verify PocketID/step-ca owner binding: %w", err)
	}
	ownerSummary.PocketIDSubject = binding.PocketIDSubject
	ownerSummary.OwnerBindingDigest = localevidence.OwnerRuntimeBindingDigest(binding)
	if ownerSummary.OwnerRef != binding.OwnerRef {
		return architectureV2OwnerVerifySummary{}, nil, errors.New("PocketID owner binding differs from local owner custody")
	}
	if offline {
		return ownerSummary, nil, nil
	}
	observation, err := verifyBasementCoreWorkspace(ctx, workspaceRoot, plan, manifest, localBinding)
	if err != nil {
		return ownerSummary, nil, fmt.Errorf("verify live Basement core: %w", err)
	}
	if observation.OwnerRef != ownerSummary.OwnerRef ||
		observation.PocketIDSubject != ownerSummary.PocketIDSubject ||
		observation.OwnerBindingDigest != ownerSummary.OwnerBindingDigest {
		return architectureV2OwnerVerifySummary{}, nil, errors.New("live Basement owner observation differs from signed local binding")
	}
	return ownerSummary, &architectureV2RuntimeVerifySummary{
		ExecutionMode: "local-runtime", Live: true,
		ProjectRef: observation.ProjectRef, Status: observation.Status,
		ServiceCount: len(observation.Services), ProbeCount: len(observation.Probes),
	}, nil
}

func architectureV2LocalVerifyIdentity(plan generationartifact.VerifiedPlan) (string, localevidence.IdentityRuntimeAddress, error) {
	var projection struct {
		Kit struct {
			Slug string `json:"slug"`
		} `json:"kit"`
		Network struct {
			Configuration struct {
				Domain struct {
					Base            string `json:"base"`
					SubdomainPrefix string `json:"subdomainPrefix"`
				} `json:"domain"`
			} `json:"configuration"`
		} `json:"network"`
	}
	if err := json.Unmarshal(plan.Canonical(), &projection); err != nil {
		return "", localevidence.IdentityRuntimeAddress{}, fmt.Errorf("decode verified Architecture v2 local Verify identity: %w", err)
	}
	kitSlug := strings.TrimSpace(projection.Kit.Slug)
	domain := strings.TrimSpace(strings.ToLower(projection.Network.Configuration.Domain.Base))
	if kitSlug == "" || domain == "" {
		return "", localevidence.IdentityRuntimeAddress{}, errors.New("verified Architecture v2 plan lacks its local Verify kit or domain identity")
	}
	return kitSlug, localevidence.IdentityRuntimeAddress{
		Domain: domain, SubdomainPrefix: strings.TrimSpace(projection.Network.Configuration.Domain.SubdomainPrefix),
	}, nil
}

func verifyArchitectureV2LocalCloudState(
	ctx context.Context,
	workspaceRoot string,
	appliedRequest runtimeexecutor.ExecutionRequest,
	ownerSummary architectureV2OwnerVerifySummary,
	localBinding localevidence.LocalBinding,
	address localevidence.IdentityRuntimeAddress,
	offline bool,
) (architectureV2OwnerVerifySummary, *architectureV2RuntimeVerifySummary, error) {
	custody, err := localevidence.LoadCloudRuntimeCustody(workspaceRoot)
	if err != nil {
		return architectureV2OwnerVerifySummary{}, nil, fmt.Errorf("verify local Cloud runtime custody: %w", err)
	}
	if custody.OwnerRef != ownerSummary.OwnerRef || custody.KeyID != ownerSummary.KeyID || custody.IdentityAddress() != address {
		return architectureV2OwnerVerifySummary{}, nil, errors.New("Cloud runtime custody differs from the verified plan or local owner")
	}
	binding, err := localevidence.LoadOwnerRuntimeBinding(workspaceRoot)
	if errors.Is(err, localevidence.ErrOwnerRuntimeBindingMissing) {
		return architectureV2OwnerVerifySummary{}, nil, errors.New("verify PocketID/step-ca owner binding: the Cloud PocketID owner is not bound yet; run stackkit apply to bind it")
	}
	if err != nil {
		return architectureV2OwnerVerifySummary{}, nil, fmt.Errorf("verify PocketID/step-ca owner binding: %w", err)
	}
	if binding.OwnerRef != ownerSummary.OwnerRef {
		return architectureV2OwnerVerifySummary{}, nil, errors.New("PocketID owner binding differs from local owner custody")
	}
	ownerSummary.PocketIDSubject = binding.PocketIDSubject
	ownerSummary.OwnerBindingDigest = localevidence.OwnerRuntimeBindingDigest(binding)
	if offline {
		return ownerSummary, nil, nil
	}
	newOperations := runtimeexecutorlocal.NewOSCloudCoreOperations
	for _, target := range appliedRequest.RuntimeTargets {
		if target.OwnerKind == "module" && target.ModuleRef == "stackkits-cloud-core-standalone-runtime" {
			newOperations = runtimeexecutorlocal.NewOSCloudStandaloneCoreOperations
		}
	}
	if err := verifyArchitectureV2CloudCoreOpenTofuRoot(workspaceRoot, appliedRequest); err != nil {
		return ownerSummary, nil, fmt.Errorf("verify live Cloud core: %w", err)
	}
	operations, err := newOperations(workspaceRoot)
	if err != nil {
		return ownerSummary, nil, err
	}
	observation, err := runtimeexecutorlocal.VerifyAppliedCloudCore(ctx, appliedRequest, runtimeexecutorlocal.LocalTargetBinding{
		SiteRef: localBinding.SiteRef, NodeRef: localBinding.NodeRef, ExecutionChannelRef: localBinding.ChannelRef,
	}, operations)
	if err != nil {
		return ownerSummary, nil, fmt.Errorf("verify live Cloud core: %w", err)
	}
	if observation.OwnerRef != ownerSummary.OwnerRef ||
		observation.PocketIDSubject != ownerSummary.PocketIDSubject ||
		observation.OwnerBindingDigest != ownerSummary.OwnerBindingDigest {
		return architectureV2OwnerVerifySummary{}, nil, errors.New("live Cloud owner observation differs from signed local binding")
	}
	return ownerSummary, &architectureV2RuntimeVerifySummary{
		ExecutionMode: "local-runtime", Live: true,
		ProjectRef: observation.ProjectRef, Status: observation.Status,
		ServiceCount: len(observation.Services), ProbeCount: len(observation.Probes),
		cloud: &observation,
	}, nil
}

// verifyArchitectureV2CloudCoreOpenTofuRoot checks the installed Core
// OpenTofu root of a Cloud core applied under the opentofu or terramate unit;
// a Cloud core applied under the compose unit has no root.
func verifyArchitectureV2CloudCoreOpenTofuRoot(workspaceRoot string, appliedRequest runtimeexecutor.ExecutionRequest) error {
	for _, target := range appliedRequest.RuntimeTargets {
		if target.OwnerKind != "module" || target.ProviderRef != "stackkits-cloud-core" ||
			(target.UnitRef != runtimeexecutoropentofu.UnitRef && target.UnitRef != runtimeexecutoropentofu.TerramateUnitRef) {
			continue
		}
		var mainTF []byte
		roots := 0
		for _, artifact := range appliedRequest.Artifacts {
			if slices.Contains(target.ArtifactRefs, artifact.ID) && path.Base(artifact.OutputRef) == runtimeexecutoropentofu.ConfigFile {
				mainTF = artifact.Content
				roots++
			}
		}
		if roots != 1 {
			return errors.New("applied Cloud core target does not own exactly one OpenTofu root")
		}
		if _, err := verifyArchitectureV2CoreOpenTofuRoot(workspaceRoot, target.ModuleRef, mainTF); err != nil {
			return fmt.Errorf("verify the Cloud core OpenTofu root: %w", err)
		}
	}
	return nil
}

func verifyArchitectureV2OwnerCustody(workspaceRoot string) (architectureV2OwnerVerifySummary, localevidence.LocalBinding, error) {
	owner, err := localevidence.LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return architectureV2OwnerVerifySummary{}, localevidence.LocalBinding{}, fmt.Errorf("verify local owner custody: %w", err)
	}
	return architectureV2OwnerVerifySummary{OwnerRef: owner.OwnerRef, KeyID: owner.KeyID}, owner.Binding, nil
}

func verifyBasementCoreWorkspace(
	ctx context.Context,
	workspaceRoot string,
	plan generationartifact.VerifiedPlan,
	manifest generationartifact.ArtifactManifest,
	localBinding localevidence.LocalBinding,
) (runtimeexecutorlocal.BasementCoreVerifyObservation, error) {
	if ctx == nil {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, errors.New("Basement workspace verification requires a context")
	}
	requirements := plan.ApplyRequirements()
	unitRef, err := architectureV2CoreRuntimeUnitRef(plan)
	if err != nil {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, err
	}
	var target generationartifact.ApplyRuntimeRequirement
	var profile runtimeexecutorlocal.BasementCoreRuntimeProfile
	targets := 0
	for _, candidate := range requirements.RuntimeInstances {
		candidateProfile, supported := runtimeexecutorlocal.BasementCoreRuntimeProfileForModule(candidate.ModuleRef)
		if supported && candidate.OwnerKind == "module" &&
			candidate.OwnerRef == candidateProfile.ModuleRef &&
			candidate.ProviderRef == candidateProfile.ProviderRef &&
			candidate.UnitRef == unitRef &&
			candidate.WorkloadRef == candidateProfile.WorkloadRef {
			target = candidate
			profile = candidateProfile
			targets++
		}
	}
	if targets != 1 {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, errors.New("verified plan requires exactly one Basement core runtime")
	}
	coreArtifact := architectureV2CoreArtifactShape(target.UnitRef, profile.OutputRef, profile.MaxArtifactBytes)
	if len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || len(target.ArtifactRefs) != coreArtifact.artifactRefs ||
		target.RuntimeKind != "container" || target.RuntimeDelivery != "stackkit" ||
		target.RuntimeEngine != "docker" {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, errors.New("verified Basement runtime is not the closed single-node Compose contract")
	}
	channelRef, err := verifiedLocalBasementExecutionChannel(requirements, target, localBinding)
	if err != nil {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, err
	}
	// The Core artifact is the Compose artifact under the compose target and
	// the Core OpenTofu root main.tf, which embeds the same Compose payload,
	// under the opentofu and terramate targets.
	var (
		artifactRequirement generationartifact.ApplyArtifactRequirement
		rendered            generationartifact.RenderedArtifact
		requirementCount    int
		renderedCount       int
	)
	for _, candidate := range requirements.Artifacts {
		if slices.Contains(target.ArtifactRefs, candidate.ID) && candidate.OutputRef == coreArtifact.outputRef {
			artifactRequirement = candidate
			requirementCount++
		}
	}
	for _, candidate := range manifest.Artifacts {
		if requirementCount == 1 && candidate.ID == artifactRequirement.ID {
			rendered = candidate
			renderedCount++
		}
	}
	if requirementCount != 1 || renderedCount != 1 ||
		artifactRequirement.Kind != coreArtifact.kind || artifactRequirement.Format != coreArtifact.format ||
		artifactRequirement.ExecutionClass != generationartifact.ApplyExecutionClassExecutable ||
		artifactRequirement.UnitRef != target.UnitRef || artifactRequirement.InstanceRef != target.InstanceRef ||
		rendered.Kind != artifactRequirement.Kind || rendered.Format != artifactRequirement.Format ||
		rendered.Mode != artifactRequirement.Mode {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, errors.New("verified generation lacks the exact executable Basement Compose artifact")
	}
	artifactPath := filepath.Join(workspaceRoot, filepath.FromSlash(rendered.Path))
	info, err := os.Lstat(artifactPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > int64(coreArtifact.maxBytes) {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, errors.New("Basement Compose artifact is not a bounded plain file")
	}
	content, err := os.ReadFile(artifactPath) //nolint:gosec // exact manifest path was already verified by the generation gate
	if err != nil {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, fmt.Errorf("read Basement Compose artifact: %w", err)
	}
	digest := sha256.Sum256(content)
	if "sha256:"+hex.EncodeToString(digest[:]) != rendered.SHA256 {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, errors.New("Basement Compose artifact differs from the CUE-owned standard")
	}
	definition := content
	if coreArtifact.openTofu {
		definition, err = verifyArchitectureV2CoreOpenTofuRoot(workspaceRoot, profile.ModuleRef, content)
		if err != nil {
			return runtimeexecutorlocal.BasementCoreVerifyObservation{}, fmt.Errorf("verify the Basement core OpenTofu root: %w", err)
		}
	}
	if !profile.ValidateComposeArtifact(definition) {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, errors.New("Basement Compose artifact differs from the CUE-owned standard")
	}
	services := append([]runtimeexecutorlocal.BasementCoreServiceExpectation(nil), profile.Services...)
	health, err := verifiedBasementCoreHealth(requirements, target)
	if err != nil {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, err
	}
	project := runtimeexecutorlocal.BasementCoreProject{
		ModuleRef: profile.ModuleRef, ProjectRef: target.InstanceRef, SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0],
		ExecutionChannelRef: channelRef, ArtifactID: rendered.ID, ArtifactDigest: rendered.SHA256,
		Definition: definition, Services: services, Health: health,
	}
	operations, err := newArchitectureV2BasementCoreVerifyOperations(workspaceRoot)
	if err != nil {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, err
	}
	observation, err := operations.VerifyProject(ctx, project)
	if err != nil {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, err
	}
	if observation.ProjectRef != project.ProjectRef || observation.ArtifactDigest != project.ArtifactDigest ||
		observation.Status != "ready" || len(observation.Services) != len(project.Services) ||
		len(observation.Probes) != len(project.Health) {
		return runtimeexecutorlocal.BasementCoreVerifyObservation{}, errors.New("live Basement observation does not prove the exact ready project")
	}
	return observation, nil
}

// newArchitectureV2BasementCoreVerifyOperations is the host observation owner
// of the live Basement core check; tests substitute it.
var newArchitectureV2BasementCoreVerifyOperations = runtimeexecutorlocal.NewOSBasementCoreOperations

// architectureV2CoreRuntimeUnitRef returns the render unit of the Core
// runtime, which is named after the plan generation target: compose,
// opentofu, or terramate.
func architectureV2CoreRuntimeUnitRef(plan generationartifact.VerifiedPlan) (string, error) {
	target, err := upgradelifecycle.GenerationTargetForPlan(plan)
	if err != nil {
		return "", fmt.Errorf("select the Core runtime unit: %w", err)
	}
	return target, nil
}

// architectureV2CoreArtifact is the governed Core artifact of one runtime
// unit: the Compose artifact, or the Core OpenTofu root main.tf that embeds
// it (plus, under terramate, the stack file beside it).
type architectureV2CoreArtifact struct {
	kind, format, outputRef string
	artifactRefs, maxBytes  int
	openTofu                bool
}

func architectureV2CoreArtifactShape(unitRef, composeOutputRef string, maxComposeBytes int) architectureV2CoreArtifact {
	switch unitRef {
	case runtimeexecutoropentofu.UnitRef, runtimeexecutoropentofu.TerramateUnitRef:
		refs := 1
		if unitRef == runtimeexecutoropentofu.TerramateUnitRef {
			refs = 2
		}
		return architectureV2CoreArtifact{
			kind: unitRef, format: runtimeexecutoropentofu.ArtifactFormat,
			outputRef:    path.Join(path.Dir(composeOutputRef), runtimeexecutoropentofu.ConfigFile),
			artifactRefs: refs, maxBytes: 1 << 20, openTofu: true,
		}
	default:
		return architectureV2CoreArtifact{
			kind: "compose", format: "yaml", outputRef: composeOutputRef, artifactRefs: 1, maxBytes: maxComposeBytes,
		}
	}
}

// verifyArchitectureV2CoreOpenTofuRoot binds the installed Core OpenTofu root
// of an opentofu or terramate install to the governed root artifact with the
// rules the executor-state capture uses: the root configuration is the
// artifact, the runtime compose.yaml is the payload it embeds, and the root
// holds applied state. It returns the Compose payload.
func verifyArchitectureV2CoreOpenTofuRoot(workspaceRoot, moduleRef string, mainTF []byte) ([]byte, error) {
	payload, err := architecturev2renderer.ExtractComposePayload(mainTF)
	if err != nil {
		return nil, fmt.Errorf("derive the Compose payload: %w", err)
	}
	runtimeDir, _, ok := runtimeexecutorlocal.NativeComposeProject(moduleRef)
	if !ok {
		return nil, errors.New("the Core module has no native runtime directory")
	}
	rootPath, err := runtimeexecutoropentofu.RootRelativePath(runtimeDir)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(workspaceRoot, filepath.FromSlash(rootPath))
	readPlain := func(name string) ([]byte, error) {
		info, err := os.Lstat(name)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s is not a plain file", filepath.Base(name))
		}
		return os.ReadFile(name) //nolint:gosec // fixed runtime paths below the workspace root
	}
	config, err := readPlain(filepath.Join(root, runtimeexecutoropentofu.ConfigFile))
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(config, mainTF) {
		return nil, errors.New("the installed root configuration differs from the governed Core root artifact")
	}
	compose, err := readPlain(filepath.Join(filepath.Dir(root), runtimeexecutoropentofu.ComposeFile))
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(compose, payload) {
		return nil, errors.New("the runtime compose.yaml differs from the Compose payload of the governed Core root")
	}
	state, err := readPlain(filepath.Join(root, runtimeexecutoropentofu.StateFile))
	if err != nil || len(state) == 0 {
		return nil, errors.New("the Core OpenTofu root holds no applied state")
	}
	return payload, nil
}

func verifiedLocalBasementExecutionChannel(
	requirements generationartifact.ApplyRequirements,
	target generationartifact.ApplyRuntimeRequirement,
	localBinding localevidence.LocalBinding,
) (string, error) {
	if len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 ||
		localBinding.SiteRef != target.SiteRefs[0] || localBinding.NodeRef != target.NodeRefs[0] ||
		strings.TrimSpace(localBinding.ChannelRef) == "" {
		return "", errors.New("local owner custody does not bind the exact Basement Site/node/channel")
	}
	matches := 0
	for _, host := range requirements.Hosts {
		if host.SiteRef != target.SiteRefs[0] || host.NodeRef != target.NodeRefs[0] {
			continue
		}
		matches++
		if host.External {
			return "", errors.New("verified Basement host is external and cannot use local owner custody")
		}
		if host.ExecutionChannelRef != "" && host.ExecutionChannelRef != localBinding.ChannelRef {
			return "", errors.New("verified Basement host conflicts with the local owner execution channel")
		}
	}
	if matches != 1 {
		return "", errors.New("verified plan has no unique local Basement host")
	}
	return localBinding.ChannelRef, nil
}

func verifiedBasementCoreHealth(
	requirements generationartifact.ApplyRequirements,
	target generationartifact.ApplyRuntimeRequirement,
) ([]runtimeexecutorlocal.BasementCoreHealthExpectation, error) {
	profile, ok := runtimeexecutorlocal.BasementCoreRuntimeProfileForModule(target.ModuleRef)
	if !ok {
		return nil, errors.New("verified Basement runtime has an unsupported local profile")
	}
	bySource := make(map[string]generationartifact.ApplyHealthRequirement, len(profile.Health))
	for _, item := range requirements.HealthRequirements {
		for _, want := range profile.Health {
			if item.SourceRef != want.SourceRef || item.Kind != want.Kind ||
				item.TargetKind != want.TargetKind || item.TargetRef != want.TargetRef {
				continue
			}
			if _, duplicate := bySource[item.SourceRef]; duplicate {
				return nil, errors.New("verified Basement health requirements contain a duplicate source")
			}
			bySource[item.SourceRef] = item
		}
	}
	health := make([]runtimeexecutorlocal.BasementCoreHealthExpectation, 0, len(profile.Health))
	for _, want := range profile.Health {
		item, ok := bySource[want.SourceRef]
		if !ok {
			return nil, errors.New("verified Basement runtime lacks one of its exact profile postconditions")
		}
		expectation := runtimeexecutorlocal.BasementCoreHealthExpectation{
			RequirementID: item.ID, SourceRef: item.SourceRef, Kind: item.Kind,
			Port: want.Port, Path: want.Path,
			ExpectedStatuses: append([]int(nil), want.ExpectedStatuses...),
		}
		health = append(health, expectation)
	}
	return health, nil
}

func printArchitectureV2VerifyReport(w io.Writer, report architectureV2VerifyReport) error {
	status := "success"
	if architectureV2HTTPProbeFailed(report.Observations) {
		status = "failed"
	}
	if _, err := fmt.Fprintf(w, "Architecture v2 verify: %s\nPlan: %s\nApply result: %s\nApply evidence: %s\nOwner: %s (%s)\n",
		status,
		report.PlanHash, report.Apply.ResultHash, report.Apply.EvidenceBundleHash,
		report.Owner.OwnerRef, report.Owner.PocketIDSubject); err != nil {
		return err
	}
	for _, receipt := range report.Releases {
		if _, err := fmt.Fprintf(w, "Release: %s %s (%s, %s/%s)\n",
			receipt.Kit, receipt.Version, receipt.Channel, receipt.Platform.OS, receipt.Platform.Arch); err != nil {
			return err
		}
	}
	if report.Offline {
		_, err := fmt.Fprintln(w, "Runtime probes: skipped (offline)")
		return err
	}
	if report.Runtime != nil {
		_, err := fmt.Fprintf(w, "Runtime: %s (%d services, %d probes)\n",
			report.Runtime.Status, report.Runtime.ServiceCount, report.Runtime.ProbeCount)
		return err
	}
	return nil
}

// loadArchitectureV2AppliedRuntimeRequest reads the sealed runtime request of
// the current verified Product Apply result from the read-only verify authority.
func loadArchitectureV2AppliedRuntimeRequest(
	ctx context.Context,
	workspaceRoot string,
	plan generationartifact.VerifiedPlan,
	manifest generationartifact.ArtifactManifest,
) (runtimeexecutor.ExecutionRequest, error) {
	_, _, receiptPath := plan.MetadataPaths(workspaceRoot)
	receipt, err := generationartifact.ReadReceipt(receiptPath)
	if err != nil {
		return runtimeexecutor.ExecutionRequest{}, err
	}
	gate := newArchitectureV2ExecutionGate()
	raw, err := gate.newVerifyAuthority(workspaceRoot, architectureV2ExecutionCLIOptions{})
	if err != nil {
		return runtimeexecutor.ExecutionRequest{}, err
	}
	if closer, ok := raw.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}
	verifyAuthority, verifies := raw.(architectureV2ProductVerifyAuthority)
	custody, holdsCustody := raw.(architectureV2AppliedRuntimeCustody)
	if !verifies || !holdsCustody {
		return runtimeexecutor.ExecutionRequest{}, errors.New("Architecture v2 verify authority has no applied runtime request custody")
	}
	result, err := readCurrentArchitectureV2ApplyResult(workspaceRoot, plan.Binding(), func(data []byte) (architecturev2.VerifiedApplyResult, error) {
		return verifyAuthority.VerifyProductApplyResult(architecturev2.ProductApplyResultVerificationInput{
			Plan: plan, Manifest: manifest, Receipt: receipt, Versions: gate.versions, Result: data,
		})
	})
	if err != nil {
		return runtimeexecutor.ExecutionRequest{}, err
	}
	requestDigest, err := architectureV2SharedRequestDigest(result)
	if err != nil {
		return runtimeexecutor.ExecutionRequest{}, err
	}
	if requestDigest == "" {
		return runtimeexecutor.ExecutionRequest{}, errors.New("verified Product Apply result has no applied runtime request custody")
	}
	return custody.LoadAppliedRuntimeRequest(ctx, requestDigest)
}

func readCurrentArchitectureV2ApplyResult(
	workspaceRoot string,
	binding generationartifact.PlanBinding,
	verify func([]byte) (architecturev2.VerifiedApplyResult, error),
) (result architecturev2.VerifiedApplyResult, returnErr error) {
	if verify == nil {
		return architecturev2.VerifiedApplyResult{}, errors.New("Architecture v2 Apply result verifier is required")
	}
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return architecturev2.VerifiedApplyResult{}, fmt.Errorf("open workspace for Apply result verification: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return architecturev2.VerifiedApplyResult{}, fmt.Errorf("begin Apply result verification transaction: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, transaction.Close()) }()

	directory := path.Join(architectureV2ApplyEvidenceRoot, "results")
	entries, err := transaction.Walk(directory)
	if err != nil {
		return architecturev2.VerifiedApplyResult{}, fmt.Errorf("read Architecture v2 Apply results: %w", err)
	}
	if len(entries) < 2 || len(entries)-1 > maxArchitectureV2ApplyResults {
		return architecturev2.VerifiedApplyResult{}, fmt.Errorf("Architecture v2 verification requires 1-%d persisted Apply results", maxArchitectureV2ApplyResults)
	}
	var (
		selected   architecturev2.VerifiedApplyResult
		selectedAt time.Time
		found      int
	)
	for _, entry := range entries[1:] {
		if !entry.Info.Mode().IsRegular() || path.Dir(entry.Path) != directory ||
			!strings.HasSuffix(path.Base(entry.Path), ".json") {
			return architecturev2.VerifiedApplyResult{}, fmt.Errorf("Architecture v2 Apply result directory contains an unsupported entry %q", entry.Path)
		}
		raw, _, err := transaction.ReadStable(entry.Path)
		if err != nil {
			return architecturev2.VerifiedApplyResult{}, err
		}
		var probe struct {
			Binding generationartifact.PlanBinding `json:"binding"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return architecturev2.VerifiedApplyResult{}, fmt.Errorf("decode Apply result binding %q: %w", entry.Path, err)
		}
		if probe.Binding != binding {
			continue
		}
		verified, err := verify(raw)
		if err != nil {
			return architecturev2.VerifiedApplyResult{}, fmt.Errorf("verify current Apply result %q: %w", entry.Path, err)
		}
		wantName := strings.TrimPrefix(verified.ResultHash(), "sha256:") + ".json"
		if path.Base(entry.Path) != wantName {
			return architecturev2.VerifiedApplyResult{}, fmt.Errorf("Apply result %q is not stored at its content address", entry.Path)
		}
		receiptPath := path.Join(architectureV2ApplyEvidenceRoot, "receipts", wantName)
		canonicalReceipt, receiptInfo, err := transaction.ReadStable(receiptPath)
		if err != nil {
			return architecturev2.VerifiedApplyResult{}, fmt.Errorf("read owner-signed Apply result receipt %q: %w", receiptPath, err)
		}
		if !receiptInfo.Mode().IsRegular() {
			return architecturev2.VerifiedApplyResult{}, fmt.Errorf("owner-signed Apply result receipt %q is not a regular file", receiptPath)
		}
		if err := verifyOwnerApplyResultReceipt(workspaceRoot, raw, canonicalReceipt, verified.ResultHash()); err != nil {
			return architecturev2.VerifiedApplyResult{}, fmt.Errorf("verify Apply result receipt %q: %w", receiptPath, err)
		}
		summary := verified.Summary()
		if summary.AppliedAt.IsZero() {
			return architecturev2.VerifiedApplyResult{}, fmt.Errorf("Apply result %q has no verified Apply time", entry.Path)
		}
		found++
		if selectedAt.IsZero() || summary.AppliedAt.After(selectedAt) {
			selected, selectedAt = verified, summary.AppliedAt
		} else if summary.AppliedAt.Equal(selectedAt) && verified.ResultHash() != selected.ResultHash() {
			return architecturev2.VerifiedApplyResult{}, fmt.Errorf("multiple current Apply results share the latest Apply time")
		}
	}
	if found == 0 {
		return architecturev2.VerifiedApplyResult{}, errors.New("no persisted Apply result matches the current ResolvedPlan; run `stackkit apply` first")
	}
	return selected, nil
}
