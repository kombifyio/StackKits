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
	basementCoreProviderRef      = "stackkits-basement-core"
	basementCoreModuleRef        = "stackkits-basement-core-runtime"
	basementCoreUnitRef          = "compose"
	basementCoreWorkloadRef      = "basement-core"
	basementCoreOutputRef        = "platform/basement-core/compose.yaml"
	basementCoreArtifactPrefix   = "basement-core-compose-instance-"
	basementCoreImageRef         = "ghcr.io/coollabsio/coolify:4.1.2"
	basementCoreImageDigest      = "sha256:3a27ba5f7f98ff7763a0a4d6715ec36e564f9622eea8f492c46f90716ea2525f"
	basementCoreMaxArtifactBytes = 256 << 10
)

type BasementCoreServiceExpectation struct {
	Ref            string
	ImageRef       string
	ImageDigest    string
	HealthRequired bool
}

type BasementCoreHealthExpectation struct {
	RequirementID    string
	SourceRef        string
	Kind             string
	Port             int
	Path             string
	ExpectedStatuses []int
}

// BasementCoreProject is the closed, provider-free capability passed to the
// local Docker owner. It contains no executable, Docker endpoint, credential,
// or caller-selected filesystem path.
type BasementCoreProject struct {
	ProjectRef          string
	SiteRef             string
	NodeRef             string
	ExecutionChannelRef string
	ArtifactID          string
	ArtifactDigest      string
	Definition          []byte
	Services            []BasementCoreServiceExpectation
	Health              []BasementCoreHealthExpectation
}

type BasementCoreApplyObservation struct {
	ProjectRef         string `json:"projectRef"`
	ArtifactDigest     string `json:"artifactDigest"`
	OwnerRef           string `json:"ownerRef"`
	PocketIDSubject    string `json:"pocketIdSubject"`
	OwnerBindingDigest string `json:"ownerBindingDigest"`
	Status             string `json:"status"`
}

type BasementCoreServiceObservation struct {
	Ref         string `json:"ref"`
	ImageRef    string `json:"imageRef"`
	ImageDigest string `json:"imageDigest"`
	Status      string `json:"status"`
	Health      string `json:"health"`
}

type BasementCoreProbeObservation struct {
	RequirementID string `json:"requirementId"`
	Status        string `json:"status"`
}

type BasementCoreVerifyObservation struct {
	ProjectRef         string                           `json:"projectRef"`
	ArtifactDigest     string                           `json:"artifactDigest"`
	OwnerRef           string                           `json:"ownerRef"`
	PocketIDSubject    string                           `json:"pocketIdSubject"`
	OwnerBindingDigest string                           `json:"ownerBindingDigest"`
	Status             string                           `json:"status"`
	Services           []BasementCoreServiceObservation `json:"services"`
	Probes             []BasementCoreProbeObservation   `json:"probes"`
}

type BasementCoreOperations interface {
	ApplyProject(context.Context, BasementCoreProject) (BasementCoreApplyObservation, error)
	VerifyProject(context.Context, BasementCoreProject) (BasementCoreVerifyObservation, error)
}

type BasementCoreAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHashes map[string]string
}

type BasementCoreExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalTargetBinding
	authority  BasementCoreAuthority
	operations BasementCoreOperations
}

func NewBasementCoreExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority BasementCoreAuthority, operations BasementCoreOperations) *BasementCoreExecutor {
	return &BasementCoreExecutor{identity: identity, binding: binding, authority: cloneBasementCoreAuthority(authority), operations: operations}
}

func (e *BasementCoreExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *BasementCoreExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Basement core executor requires a context")
	}
	if e == nil || e.operations == nil || strings.TrimSpace(e.binding.SiteRef) == "" ||
		strings.TrimSpace(e.binding.NodeRef) == "" || strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) ||
		!validCoreHostBootstrapDigest(e.authority.ModuleContractHash) || len(e.authority.HealthContractHashes) != 7 {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Basement core executor requires one explicit authenticated local authority")
	}
	target, health, project, err := validateBasementCoreRequest(request, e.binding, e.authority)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	applied, err := e.operations.ApplyProject(ctx, defensiveBasementCoreProject(project))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("apply exact Basement core project: %w", err)
	}
	if applied.ProjectRef != project.ProjectRef || applied.ArtifactDigest != project.ArtifactDigest ||
		applied.OwnerRef == "" || applied.PocketIDSubject == "" ||
		!validCoreHostBootstrapDigest(applied.OwnerBindingDigest) || applied.Status != "applied" {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Basement core Apply observation does not prove the exact project, artifact, and owner binding")
	}
	verified, err := e.operations.VerifyProject(ctx, defensiveBasementCoreProject(project))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify exact Basement core project: %w", err)
	}
	if err := validateBasementCoreVerification(project, verified); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	if applied.OwnerRef != verified.OwnerRef ||
		applied.PocketIDSubject != verified.PocketIDSubject ||
		applied.OwnerBindingDigest != verified.OwnerBindingDigest {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Basement core verification does not prove the applied owner binding")
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string                        `json:"schemaVersion"`
		Apply         BasementCoreApplyObservation  `json:"apply"`
		Verify        BasementCoreVerifyObservation `json:"verify"`
	}{SchemaVersion: "stackkit.basement-core-apply-evidence/v1", Apply: applied, Verify: verified})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal Basement core observation: %w", err)
	}
	sum := sha256.Sum256(evidence)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	outcome := runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{
			RequirementID: target.RequirementID, InstanceRef: target.InstanceRef,
			Status:            runtimeexecutor.RuntimeStatusApplied,
			ObservationRef:    "runtime-observation://basement-core/" + target.InstanceRef,
			ObservationDigest: digest,
		}},
		Health: make([]runtimeexecutor.HealthOutcome, len(health)),
	}
	for index, item := range health {
		outcome.Health[index] = runtimeexecutor.HealthOutcome{
			RequirementID: item.RequirementID, TargetRef: item.TargetRef,
			Status:            runtimeexecutor.HealthStatusHealthy,
			ObservationRef:    "health-observation://basement-core/" + item.RequirementID,
			ObservationDigest: digest,
		}
	}
	sort.Slice(outcome.Health, func(i, j int) bool {
		return outcome.Health[i].RequirementID < outcome.Health[j].RequirementID
	})
	return outcome, nil
}

func cloneBasementCoreAuthority(authority BasementCoreAuthority) BasementCoreAuthority {
	result := authority
	result.HealthContractHashes = make(map[string]string, len(authority.HealthContractHashes))
	for key, value := range authority.HealthContractHashes {
		result.HealthContractHashes[key] = value
	}
	return result
}

func defensiveBasementCoreProject(project BasementCoreProject) BasementCoreProject {
	project.Definition = append([]byte(nil), project.Definition...)
	project.Services = append([]BasementCoreServiceExpectation(nil), project.Services...)
	project.Health = append([]BasementCoreHealthExpectation(nil), project.Health...)
	for index := range project.Health {
		project.Health[index].ExpectedStatuses = append([]int(nil), project.Health[index].ExpectedStatuses...)
	}
	return project
}

func validateBasementCoreRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority BasementCoreAuthority) (runtimeexecutor.RuntimeTarget, []runtimeexecutor.HealthTarget, BasementCoreProject, error) {
	if len(request.RuntimeTargets) != 1 || len(request.AccessBindings) != 0 || len(request.HealthTargets) != 7 {
		return runtimeexecutor.RuntimeTarget{}, nil, BasementCoreProject{}, errors.New("Basement core requires exactly one runtime, seven health targets, and no access binding")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.BasementCoreComposeRendererContract()
	instanceRef := basementCoreUnitRef + "-node-" + binding.NodeRef
	artifactID := basementCoreArtifactPrefix + instanceRef
	if target.OwnerKind != "module" || target.OwnerRef != basementCoreModuleRef || target.OwnerVersion != "" ||
		target.ProviderRef != basementCoreProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != basementCoreModuleRef || target.OwnerContractHash != authority.ModuleContractHash ||
		target.ModuleContractHash != authority.ModuleContractHash || target.UnitRef != basementCoreUnitRef ||
		target.UnitContractHash != contract.ContractHash || target.RuntimeKind != "container" ||
		target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "docker" ||
		target.InstanceRef != instanceRef || target.WorkloadRef != basementCoreWorkloadRef ||
		target.ImageRef != basementCoreImageRef || target.ImageDigest != basementCoreImageDigest ||
		target.ExecutionChannelRef != binding.ExecutionChannelRef ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) ||
		!slices.Equal(target.NodeRefs, []string{binding.NodeRef}) ||
		len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 ||
		len(target.AccessBindingRefs) != 0 || len(target.BackupTargetCapabilities) != 0 ||
		len(target.BackupTargetBindingRefs) != 0 || target.RuntimeAdapter != nil ||
		!slices.Equal(target.ArtifactRefs, []string{artifactID}) {
		return runtimeexecutor.RuntimeTarget{}, nil, BasementCoreProject{}, errors.New("runtime target is not the exact locally bound Basement core Compose contract")
	}
	artifact, err := exactBasementCoreArtifact(request.Artifacts, target, artifactID, contract.ContractHash)
	if err != nil {
		return runtimeexecutor.RuntimeTarget{}, nil, BasementCoreProject{}, err
	}
	health, expectations, err := exactBasementCoreHealth(request.HealthTargets, target, authority)
	if err != nil {
		return runtimeexecutor.RuntimeTarget{}, nil, BasementCoreProject{}, err
	}
	services := architecturev2renderer.BasementCoreServiceContracts()
	serviceExpectations := make([]BasementCoreServiceExpectation, len(services))
	for index, service := range services {
		serviceExpectations[index] = BasementCoreServiceExpectation(service)
	}
	return target, health, BasementCoreProject{
		ProjectRef: instanceRef, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef,
		ExecutionChannelRef: binding.ExecutionChannelRef, ArtifactID: artifact.ID,
		ArtifactDigest: artifact.Digest, Definition: append([]byte(nil), artifact.Content...),
		Services: serviceExpectations, Health: expectations,
	}, nil
}

func exactBasementCoreArtifact(artifacts []runtimeexecutor.Artifact, target runtimeexecutor.RuntimeTarget, artifactID, unitHash string) (runtimeexecutor.Artifact, error) {
	var artifact runtimeexecutor.Artifact
	found := 0
	for _, candidate := range artifacts {
		if candidate.ID == artifactID {
			artifact = candidate
			found++
		}
	}
	if found != 1 || artifact.Kind != "compose" || artifact.Format != "yaml" || artifact.Mode != "0640" ||
		artifact.OwnerKind != "render-instance" || artifact.OwnerRef != target.InstanceRef ||
		artifact.OwnerContractHash != unitHash || artifact.ProviderRef != target.ProviderRef ||
		artifact.ProviderContractHash != target.ProviderContractHash || artifact.ModuleRef != target.ModuleRef ||
		artifact.ModuleContractHash != target.ModuleContractHash || artifact.UnitRef != target.UnitRef ||
		artifact.UnitContractHash != unitHash || artifact.InstanceRef != target.InstanceRef ||
		artifact.OutputRef != basementCoreOutputRef || !slices.Equal(artifact.SiteRefs, target.SiteRefs) ||
		!slices.Equal(artifact.NodeRefs, target.NodeRefs) || len(artifact.Content) == 0 ||
		len(artifact.Content) > basementCoreMaxArtifactBytes ||
		!architecturev2renderer.ValidateBasementCoreComposeArtifact(artifact.Content) {
		return runtimeexecutor.Artifact{}, errors.New("artifact is not the exact CUE-owned Basement core Compose instance")
	}
	sum := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return runtimeexecutor.Artifact{}, errors.New("Basement core artifact digest does not match immutable content")
	}
	return artifact, nil
}

type basementCoreHealthSpec struct {
	source, kind, targetKind, targetRef, path string
	port, probePort, timeout                  int
	statuses                                  []int
}

var basementCoreHealthSpecs = []basementCoreHealthSpec{
	{source: "basement-hub-http", kind: "http", targetKind: "module", targetRef: basementCoreModuleRef, path: "/healthz", port: 80, timeout: 30, statuses: []int{200}},
	{source: "basement-router-http", kind: "http", targetKind: "module", targetRef: basementCoreModuleRef, path: "/ping", port: 8080, timeout: 30, statuses: []int{200}},
	{source: "coolify-http", kind: "http", targetKind: "module", targetRef: basementCoreModuleRef, path: "/", port: 8000, timeout: 30, statuses: []int{200, 302}},
	{source: "local-kopia-runtime-container", kind: "container", targetKind: "module", targetRef: basementCoreModuleRef},
	{source: "pocketid-http", kind: "http", targetKind: "module", targetRef: basementCoreModuleRef, path: "/", port: 1411, timeout: 30, statuses: []int{200, 302}},
	{source: "step-ca-tcp", kind: "tcp", targetKind: "module", targetRef: basementCoreModuleRef, port: 9000, timeout: 30},
	{source: "tinyauth-http", kind: "http", targetKind: "module", targetRef: basementCoreModuleRef, path: "/", port: 4000, timeout: 30, statuses: []int{200, 302}},
}

func exactBasementCoreHealth(input []runtimeexecutor.HealthTarget, target runtimeexecutor.RuntimeTarget, authority BasementCoreAuthority) ([]runtimeexecutor.HealthTarget, []BasementCoreHealthExpectation, error) {
	bySource := make(map[string]runtimeexecutor.HealthTarget, len(input))
	for _, item := range input {
		if _, duplicate := bySource[item.SourceRef]; duplicate {
			return nil, nil, errors.New("Basement core health targets contain a duplicate source")
		}
		bySource[item.SourceRef] = item
	}
	health := make([]runtimeexecutor.HealthTarget, 0, len(basementCoreHealthSpecs))
	expectations := make([]BasementCoreHealthExpectation, 0, len(basementCoreHealthSpecs))
	for _, spec := range basementCoreHealthSpecs {
		item, ok := bySource[spec.source]
		hash, trusted := authority.HealthContractHashes[spec.source]
		if !ok || !trusted || !validCoreHostBootstrapDigest(hash) ||
			item.ContractHash != hash || item.Phase != "post-apply" || item.Kind != spec.kind ||
			item.TargetKind != spec.targetKind || item.TargetRef != spec.targetRef ||
			item.RouteRef != "" || item.BackendPoolRef != "" ||
			!slices.Equal(item.SiteRefs, target.SiteRefs) || !slices.Equal(item.NodeRefs, target.NodeRefs) {
			return nil, nil, errors.New("health target is not the exact Basement core postcondition")
		}
		if spec.kind == "contract" {
			if item.Probe != nil {
				return nil, nil, errors.New("Basement core provider contract must not carry a caller probe")
			}
		} else {
			// Direct module probes are executor-owned in runtimeexecutor/v1;
			// only route gates may carry a caller Probe DTO. If a future
			// contract supplies one, it must still match the CUE-owned spec.
			if item.Probe != nil && (item.Probe.Protocol != spec.kind || item.Probe.Port != spec.port ||
				item.Probe.TimeoutSeconds != spec.timeout || item.Probe.Path != spec.path ||
				(spec.kind == "http" && (item.Probe.Method != "GET" ||
					!slices.Equal(item.Probe.ExpectedStatuses, spec.statuses)))) {
				return nil, nil, errors.New("Basement core health probe differs from the CUE-owned contract")
			}
		}
		health = append(health, item)
		expectations = append(expectations, BasementCoreHealthExpectation{
			RequirementID: item.RequirementID, SourceRef: spec.source, Kind: spec.kind,
			Port: spec.port, Path: spec.path, ExpectedStatuses: append([]int(nil), spec.statuses...),
		})
	}
	return health, expectations, nil
}

func validateBasementCoreVerification(project BasementCoreProject, observation BasementCoreVerifyObservation) error {
	if observation.ProjectRef != project.ProjectRef || observation.ArtifactDigest != project.ArtifactDigest ||
		observation.OwnerRef == "" || observation.PocketIDSubject == "" ||
		!validCoreHostBootstrapDigest(observation.OwnerBindingDigest) ||
		observation.Status != "ready" || len(observation.Services) != len(project.Services) ||
		len(observation.Probes) != len(project.Health) {
		return errors.New("Basement core verification does not prove the exact ready project")
	}
	wantServices := append([]BasementCoreServiceExpectation(nil), project.Services...)
	gotServices := append([]BasementCoreServiceObservation(nil), observation.Services...)
	sort.Slice(wantServices, func(i, j int) bool { return wantServices[i].Ref < wantServices[j].Ref })
	sort.Slice(gotServices, func(i, j int) bool { return gotServices[i].Ref < gotServices[j].Ref })
	for index, want := range wantServices {
		got := gotServices[index]
		wantHealth := "not-configured"
		if want.HealthRequired {
			wantHealth = "healthy"
		}
		if got.Ref != want.Ref || got.ImageRef != want.ImageRef || got.ImageDigest != want.ImageDigest ||
			got.Status != "running" || got.Health != wantHealth {
			return errors.New("Basement core verification does not prove every pinned service")
		}
	}
	wantProbes := make(map[string]struct{}, len(project.Health))
	for _, item := range project.Health {
		wantProbes[item.RequirementID] = struct{}{}
	}
	for _, item := range observation.Probes {
		if _, ok := wantProbes[item.RequirementID]; !ok || item.Status != "healthy" {
			return errors.New("Basement core verification does not prove every governed probe")
		}
		delete(wantProbes, item.RequirementID)
	}
	if len(wantProbes) != 0 {
		return errors.New("Basement core verification omitted a governed probe")
	}
	return nil
}

var _ runtimeexecutor.Executor = (*BasementCoreExecutor)(nil)
