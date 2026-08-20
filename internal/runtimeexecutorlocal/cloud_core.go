package runtimeexecutorlocal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	cloudCoreProviderRef    = "stackkits-cloud-core"
	cloudCoreModuleRef      = "stackkits-cloud-core-runtime"
	cloudCoreUnitRef        = "compose"
	cloudCoreWorkloadRef    = "cloud-core"
	cloudCoreOutputRef      = "platform/cloud-core/compose.yaml"
	cloudCoreArtifactPrefix = "cloud-core-compose-instance-"
	cloudCoreImageRef       = "ghcr.io/coollabsio/coolify:4.1.2"
	cloudCoreImageDigest    = "sha256:3a27ba5f7f98ff7763a0a4d6715ec36e564f9622eea8f492c46f90716ea2525f"
)

type CloudCoreProject struct {
	ProjectRef, SiteRef, NodeRef, ExecutionChannelRef string
	ArtifactID, ArtifactDigest                        string
	Definition                                        []byte
	Services                                          []BasementCoreServiceExpectation
	Health                                            []BasementCoreHealthExpectation
}

type CloudCoreApplyObservation struct {
	ProjectRef, ArtifactDigest, Status string
}

type CloudCoreVerifyObservation struct {
	ProjectRef, ArtifactDigest, Status string
	Services                           []BasementCoreServiceObservation
	Probes                             []BasementCoreProbeObservation
}

type CloudCoreOperations interface {
	ApplyProject(context.Context, CloudCoreProject) (CloudCoreApplyObservation, error)
	VerifyProject(context.Context, CloudCoreProject) (CloudCoreVerifyObservation, error)
}

type CloudCoreAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHashes map[string]string
}

type CloudCoreExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalTargetBinding
	authority  CloudCoreAuthority
	operations CloudCoreOperations
}

func NewCloudCoreExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority CloudCoreAuthority, operations CloudCoreOperations) *CloudCoreExecutor {
	return &CloudCoreExecutor{identity: identity, binding: binding, authority: cloneCloudCoreAuthority(authority), operations: operations}
}

// VerifyAppliedCloudCore verifies the exact Cloud core child contract retained
// in the sealed Product Apply request without executing Apply a second time.
func VerifyAppliedCloudCore(ctx context.Context, request runtimeexecutor.ExecutionRequest, expectedBinding LocalTargetBinding, operations CloudCoreOperations) (CloudCoreVerifyObservation, error) {
	if ctx == nil || operations == nil {
		return CloudCoreVerifyObservation{}, errors.New("Cloud core verification requires a context and operations owner")
	}
	if err := request.Validate(); err != nil {
		return CloudCoreVerifyObservation{}, fmt.Errorf("validate applied Cloud runtime custody: %w", err)
	}
	child, binding, authority, err := appliedCloudCoreRequest(request)
	if err != nil {
		return CloudCoreVerifyObservation{}, err
	}
	if binding != expectedBinding || strings.TrimSpace(expectedBinding.SiteRef) == "" || strings.TrimSpace(expectedBinding.NodeRef) == "" || strings.TrimSpace(expectedBinding.ExecutionChannelRef) == "" {
		return CloudCoreVerifyObservation{}, errors.New("applied Cloud core target differs from local owner custody")
	}
	_, _, project, err := validateCloudCoreRequest(child, binding, authority)
	if err != nil {
		return CloudCoreVerifyObservation{}, err
	}
	observation, err := operations.VerifyProject(ctx, project)
	if err != nil {
		return CloudCoreVerifyObservation{}, err
	}
	if err := validateCloudCoreVerification(project, observation); err != nil {
		return CloudCoreVerifyObservation{}, err
	}
	return observation, nil
}

func appliedCloudCoreRequest(root runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionRequest, LocalTargetBinding, CloudCoreAuthority, error) {
	var targets []runtimeexecutor.RuntimeTarget
	for _, target := range root.RuntimeTargets {
		if target.OwnerKind == "module" && target.OwnerRef == cloudCoreModuleRef && target.ProviderRef == cloudCoreProviderRef &&
			target.ModuleRef == cloudCoreModuleRef && target.UnitRef == cloudCoreUnitRef && target.WorkloadRef == cloudCoreWorkloadRef {
			targets = append(targets, target)
		}
	}
	if len(targets) != 1 {
		return runtimeexecutor.ExecutionRequest{}, LocalTargetBinding{}, CloudCoreAuthority{}, errors.New("applied runtime custody requires exactly one Cloud core target")
	}
	target := targets[0]
	if len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" {
		return runtimeexecutor.ExecutionRequest{}, LocalTargetBinding{}, CloudCoreAuthority{}, errors.New("applied Cloud core target lacks one exact Site, node, and execution channel")
	}
	artifactRefs := make(map[string]struct{}, len(target.ArtifactRefs))
	for _, ref := range target.ArtifactRefs {
		artifactRefs[ref] = struct{}{}
	}
	artifacts := make([]runtimeexecutor.Artifact, 0, len(artifactRefs))
	for _, artifact := range root.Artifacts {
		if _, selected := artifactRefs[artifact.ID]; selected {
			artifacts = append(artifacts, artifact)
		}
	}
	allowedHealth := make(map[string]struct{}, len(cloudCoreHealthSpecs))
	for _, spec := range cloudCoreHealthSpecs {
		allowedHealth[spec.source] = struct{}{}
	}
	health := make([]runtimeexecutor.HealthTarget, 0, len(cloudCoreHealthSpecs))
	healthHashes := make(map[string]string, len(cloudCoreHealthSpecs))
	for _, item := range root.HealthTargets {
		belongs := item.RuntimeRequirementID == target.RequirementID
		if item.RuntimeRequirementID == "" {
			belongs = item.TargetKind == "module" && item.TargetRef == cloudCoreModuleRef &&
				slices.Equal(item.SiteRefs, target.SiteRefs) && slices.Equal(item.NodeRefs, target.NodeRefs)
		}
		if !belongs {
			continue
		}
		if _, selected := allowedHealth[item.SourceRef]; !selected {
			return runtimeexecutor.ExecutionRequest{}, LocalTargetBinding{}, CloudCoreAuthority{}, errors.New("applied Cloud core target contains an unknown health contract")
		}
		health = append(health, item)
		healthHashes[item.SourceRef] = item.ContractHash
	}
	child := runtimeexecutor.ExecutionRequest{
		RuntimeTargets: []runtimeexecutor.RuntimeTarget{target}, Artifacts: artifacts, HealthTargets: health,
	}
	binding := LocalTargetBinding{SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef}
	authority := CloudCoreAuthority{
		ProviderContractHash: target.ProviderContractHash, ModuleContractHash: target.ModuleContractHash, HealthContractHashes: healthHashes,
	}
	return child, binding, authority, nil
}

func (e *CloudCoreExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *CloudCoreExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud core executor requires a context")
	}
	if e == nil || e.operations == nil || strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" ||
		strings.TrimSpace(e.binding.ExecutionChannelRef) == "" || !validCoreHostBootstrapDigest(e.authority.ProviderContractHash) ||
		!validCoreHostBootstrapDigest(e.authority.ModuleContractHash) || len(e.authority.HealthContractHashes) == 0 {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud core executor requires one explicit authenticated host authority")
	}
	target, health, project, err := validateCloudCoreRequest(request, e.binding, e.authority)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	applied, err := e.operations.ApplyProject(ctx, cloneCloudCoreProject(project))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("apply exact Cloud core project: %w", err)
	}
	if applied.ProjectRef != project.ProjectRef || applied.ArtifactDigest != project.ArtifactDigest || applied.Status != "applied" {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud core Apply observation does not prove the exact project and artifact")
	}
	verified, err := e.operations.VerifyProject(ctx, cloneCloudCoreProject(project))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify exact Cloud core project: %w", err)
	}
	if err := validateCloudCoreVerification(project, verified); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string                     `json:"schemaVersion"`
		Apply         CloudCoreApplyObservation  `json:"apply"`
		Verify        CloudCoreVerifyObservation `json:"verify"`
	}{"stackkit.cloud-core-apply-evidence/v1", applied, verified})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	sum := sha256.Sum256(evidence)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	outcome := runtimeexecutor.ExecutionOutcome{Runtime: []runtimeexecutor.RuntimeOutcome{{
		RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied,
		ObservationRef: "runtime-observation://cloud-core/" + target.InstanceRef, ObservationDigest: digest,
	}}, Health: make([]runtimeexecutor.HealthOutcome, len(health))}
	for index, item := range health {
		outcome.Health[index] = runtimeexecutor.HealthOutcome{RequirementID: item.RequirementID, TargetRef: item.TargetRef,
			Status: runtimeexecutor.HealthStatusHealthy, ObservationRef: "health-observation://cloud-core/" + item.RequirementID, ObservationDigest: digest}
	}
	sort.Slice(outcome.Health, func(i, j int) bool { return outcome.Health[i].RequirementID < outcome.Health[j].RequirementID })
	return outcome, nil
}

func cloneCloudCoreAuthority(authority CloudCoreAuthority) CloudCoreAuthority {
	result := authority
	result.HealthContractHashes = make(map[string]string, len(authority.HealthContractHashes))
	for key, value := range authority.HealthContractHashes {
		result.HealthContractHashes[key] = value
	}
	return result
}

func cloneCloudCoreProject(project CloudCoreProject) CloudCoreProject {
	project.Definition = append([]byte(nil), project.Definition...)
	project.Services = append([]BasementCoreServiceExpectation(nil), project.Services...)
	project.Health = append([]BasementCoreHealthExpectation(nil), project.Health...)
	for index := range project.Health {
		project.Health[index].ExpectedStatuses = append([]int(nil), project.Health[index].ExpectedStatuses...)
	}
	return project
}

func validateCloudCoreRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority CloudCoreAuthority) (runtimeexecutor.RuntimeTarget, []runtimeexecutor.HealthTarget, CloudCoreProject, error) {
	if len(request.RuntimeTargets) != 1 || len(request.AccessBindings) != 0 || len(request.HealthTargets) == 0 {
		return runtimeexecutor.RuntimeTarget{}, nil, CloudCoreProject{}, errors.New("Cloud core requires one runtime, at least one health target, and no access binding")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.CloudCoreComposeRendererContract()
	instanceRef := cloudCoreUnitRef + "-node-" + binding.NodeRef
	artifactID := cloudCoreArtifactPrefix + instanceRef
	if target.OwnerKind != "module" || target.OwnerRef != cloudCoreModuleRef || target.OwnerVersion != "" || target.ProviderRef != cloudCoreProviderRef ||
		target.ProviderContractHash != authority.ProviderContractHash || target.ModuleRef != cloudCoreModuleRef || target.OwnerContractHash != authority.ModuleContractHash ||
		target.ModuleContractHash != authority.ModuleContractHash || target.UnitRef != cloudCoreUnitRef || target.UnitContractHash != contract.ContractHash ||
		target.RuntimeKind != "container" || target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "docker" || target.InstanceRef != instanceRef ||
		target.WorkloadRef != cloudCoreWorkloadRef || target.ImageRef != cloudCoreImageRef || target.ImageDigest != cloudCoreImageDigest ||
		target.ExecutionChannelRef != binding.ExecutionChannelRef || !slices.Equal(target.SiteRefs, []string{binding.SiteRef}) ||
		!slices.Equal(target.NodeRefs, []string{binding.NodeRef}) || len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 ||
		len(target.AccessBindingRefs) != 0 || len(target.BackupTargetCapabilities) != 0 || len(target.BackupTargetBindingRefs) != 0 ||
		target.RuntimeAdapter != nil || !slices.Equal(target.ArtifactRefs, []string{artifactID}) {
		return runtimeexecutor.RuntimeTarget{}, nil, CloudCoreProject{}, errors.New("runtime target is not the exact bound Cloud core Compose contract")
	}
	if len(request.Artifacts) != 1 {
		return runtimeexecutor.RuntimeTarget{}, nil, CloudCoreProject{}, errors.New("Cloud core requires one exact generated artifact")
	}
	artifact := request.Artifacts[0]
	if artifact.ID != artifactID || artifact.Kind != "compose" || artifact.Format != "yaml" || artifact.Mode != "0640" ||
		artifact.OwnerKind != "render-instance" || artifact.OwnerRef != instanceRef || artifact.OwnerContractHash != contract.ContractHash ||
		artifact.ProviderRef != cloudCoreProviderRef || artifact.ProviderContractHash != authority.ProviderContractHash || artifact.ModuleRef != cloudCoreModuleRef ||
		artifact.ModuleContractHash != authority.ModuleContractHash || artifact.UnitRef != cloudCoreUnitRef || artifact.UnitContractHash != contract.ContractHash ||
		artifact.InstanceRef != instanceRef || artifact.OutputRef != cloudCoreOutputRef || !slices.Equal(artifact.SiteRefs, target.SiteRefs) ||
		!slices.Equal(artifact.NodeRefs, target.NodeRefs) || len(artifact.Content) == 0 || len(artifact.Content) > basementCoreMaxArtifactBytes ||
		!architecturev2renderer.ValidateCloudCoreComposeArtifact(artifact.Content) {
		return runtimeexecutor.RuntimeTarget{}, nil, CloudCoreProject{}, errors.New("artifact is not the exact generated Cloud core Compose definition")
	}
	sum := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return runtimeexecutor.RuntimeTarget{}, nil, CloudCoreProject{}, errors.New("Cloud core artifact digest does not match its immutable content")
	}
	health, expectations, err := exactCloudCoreHealth(request.HealthTargets, target, authority)
	if err != nil {
		return runtimeexecutor.RuntimeTarget{}, nil, CloudCoreProject{}, err
	}
	contracts := architecturev2renderer.CloudCoreServiceContracts()
	services := make([]BasementCoreServiceExpectation, len(contracts))
	for index, contract := range contracts {
		services[index] = BasementCoreServiceExpectation(contract)
	}
	return target, health, CloudCoreProject{ProjectRef: target.InstanceRef, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef,
		ExecutionChannelRef: binding.ExecutionChannelRef, ArtifactID: artifact.ID, ArtifactDigest: artifact.Digest,
		Definition: append([]byte(nil), artifact.Content...), Services: services, Health: expectations}, nil
}

// Cloud module health contracts use container target ports; the local owner
// probes the exact host listeners published by the Cloud Compose artifact.
var cloudCoreHealthSpecs = []basementCoreHealthSpec{
	{source: "cloud-coolify-http", kind: "http", targetKind: "module", targetRef: cloudCoreModuleRef, path: "/", port: 8080, probePort: 8000, timeout: 30, statuses: []int{200, 302}},
	{source: "cloud-hub-http", kind: "http", targetKind: "module", targetRef: cloudCoreModuleRef, path: "/healthz", port: 80, probePort: 8081, timeout: 30, statuses: []int{200}},
	{source: "cloud-pocketid-http", kind: "http", targetKind: "module", targetRef: cloudCoreModuleRef, path: "/", port: 1411, probePort: 1411, timeout: 30, statuses: []int{200, 302}},
	{source: "cloud-router-http", kind: "http", targetKind: "module", targetRef: cloudCoreModuleRef, path: "/ping", port: 8080, probePort: 8080, timeout: 30, statuses: []int{200}},
	{source: "cloud-tinyauth-http", kind: "http", targetKind: "module", targetRef: cloudCoreModuleRef, path: "/", port: 3000, probePort: 4000, timeout: 30, statuses: []int{200, 302}},
}

func exactCloudCoreHealth(input []runtimeexecutor.HealthTarget, target runtimeexecutor.RuntimeTarget, authority CloudCoreAuthority) ([]runtimeexecutor.HealthTarget, []BasementCoreHealthExpectation, error) {
	bySource := make(map[string]runtimeexecutor.HealthTarget, len(input))
	for _, item := range input {
		if _, duplicate := bySource[item.SourceRef]; duplicate {
			return nil, nil, errors.New("Cloud core health targets contain a duplicate source")
		}
		bySource[item.SourceRef] = item
	}
	health := make([]runtimeexecutor.HealthTarget, 0, len(cloudCoreHealthSpecs))
	expectations := make([]BasementCoreHealthExpectation, 0, len(cloudCoreHealthSpecs))
	for _, spec := range cloudCoreHealthSpecs {
		item, ok := bySource[spec.source]
		if !ok {
			// The kit decides which core services it selects; a spec this plan
			// does not carry is simply not part of this rollout.
			continue
		}
		hash, trusted := authority.HealthContractHashes[spec.source]
		if !trusted || !validCoreHostBootstrapDigest(hash) || item.ContractHash != hash || item.Phase != "post-apply" ||
			item.Kind != spec.kind || item.TargetKind != spec.targetKind || item.TargetRef != spec.targetRef ||
			!slices.Equal(item.SiteRefs, target.SiteRefs) || !slices.Equal(item.NodeRefs, target.NodeRefs) {
			return nil, nil, errors.New("Cloud core health authority does not match its CUE-owned contract")
		}
		if item.Probe != nil && (item.Probe.Protocol != spec.kind || item.Probe.Port != spec.port ||
			item.Probe.TimeoutSeconds != spec.timeout || item.Probe.Path != spec.path || item.Probe.Method != "GET" ||
			!slices.Equal(item.Probe.ExpectedStatuses, spec.statuses)) {
			return nil, nil, errors.New("Cloud core health probe differs from the CUE-owned contract")
		}
		health = append(health, item)
		expectations = append(expectations, BasementCoreHealthExpectation{RequirementID: item.RequirementID, SourceRef: item.SourceRef,
			Kind: item.Kind, Port: spec.probePort, Path: spec.path, ExpectedStatuses: append([]int(nil), spec.statuses...)})
	}
	return health, expectations, nil
}

func validateCloudCoreVerification(project CloudCoreProject, observation CloudCoreVerifyObservation) error {
	if observation.ProjectRef != project.ProjectRef || observation.ArtifactDigest != project.ArtifactDigest || observation.Status != "ready" ||
		len(observation.Services) != len(project.Services) || len(observation.Probes) != len(project.Health) {
		return errors.New("Cloud core verification does not prove the exact ready project")
	}
	wantServices := append([]BasementCoreServiceExpectation(nil), project.Services...)
	gotServices := append([]BasementCoreServiceObservation(nil), observation.Services...)
	sort.Slice(wantServices, func(i, j int) bool { return wantServices[i].Ref < wantServices[j].Ref })
	sort.Slice(gotServices, func(i, j int) bool { return gotServices[i].Ref < gotServices[j].Ref })
	for index, want := range wantServices {
		got := gotServices[index]
		if got.Ref != want.Ref || got.ImageRef != want.ImageRef || got.ImageDigest != want.ImageDigest || got.Status != "running" ||
			(want.HealthRequired && got.Health != "healthy") || (!want.HealthRequired && got.Health != "not-required") {
			return errors.New("Cloud core service verification differs from the pinned component graph")
		}
	}
	wantProbes := append([]BasementCoreHealthExpectation(nil), project.Health...)
	gotProbes := append([]BasementCoreProbeObservation(nil), observation.Probes...)
	sort.Slice(wantProbes, func(i, j int) bool { return wantProbes[i].RequirementID < wantProbes[j].RequirementID })
	sort.Slice(gotProbes, func(i, j int) bool { return gotProbes[i].RequirementID < gotProbes[j].RequirementID })
	for index, want := range wantProbes {
		got := gotProbes[index]
		if got.RequirementID != want.RequirementID || got.Status != "healthy" {
			return errors.New("Cloud core probe verification differs from the authorized postconditions")
		}
	}
	return nil
}

var _ runtimeexecutor.Executor = (*CloudCoreExecutor)(nil)
