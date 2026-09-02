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
	basementCoreProviderRef        = "stackkits-basement-core"
	basementCoreModuleRef          = "stackkits-basement-core-runtime"
	basementCoreLiteModuleRef      = "stackkits-basement-core-lite-runtime"
	basementCoreUnitRef            = "compose"
	basementCoreWorkloadRef        = "basement-core"
	basementCoreOutputRef          = "platform/basement-core/compose.yaml"
	basementCoreArtifactPrefix     = "basement-core-compose-instance-"
	basementCoreImageRef           = "ghcr.io/coollabsio/coolify:4.1.2"
	basementCoreImageDigest        = "sha256:3a27ba5f7f98ff7763a0a4d6715ec36e564f9622eea8f492c46f90716ea2525f"
	basementCoreLiteOutputRef      = "platform/basement-core-lite/compose.yaml"
	basementCoreLiteArtifactPrefix = "basement-core-lite-compose-instance-"
	basementCoreLiteImageRef       = "docker.io/library/nginx:alpine"
	basementCoreLiteImageDigest    = "sha256:4a73073bd557c65b759505da037898b61f1be6cbcc3c2c3aeac22d2a470c1752"
	basementCoreMaxArtifactBytes   = 256 << 10
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

// BasementCoreHealthContract is the public, secret-free projection of one
// selected Core profile's post-apply health gate. Commands and local runtime
// owners consume this projection instead of maintaining a second Lite list.
type BasementCoreHealthContract struct {
	SourceRef        string
	Kind             string
	TargetKind       string
	TargetRef        string
	Port             int
	Path             string
	ExpectedStatuses []int
}

// BasementCoreRuntimeProfile is the finite profile identity shared by Apply,
// live Verify, and Restore post-verification. It carries no endpoints,
// credentials, or caller-controlled paths.
type BasementCoreRuntimeProfile struct {
	ProviderRef      string
	ModuleRef        string
	UnitRef          string
	WorkloadRef      string
	OutputRef        string
	ArtifactPrefix   string
	ImageRef         string
	ImageDigest      string
	MaxArtifactBytes int
	Services         []BasementCoreServiceExpectation
	Health           []BasementCoreHealthContract
}

// BasementCoreRuntimeProfileForModule returns the one known local Core
// profile selected by the verified plan. Unknown module identities are
// rejected so a caller cannot turn a generic verifier into a fallback.
func BasementCoreRuntimeProfileForModule(moduleRef string) (BasementCoreRuntimeProfile, bool) {
	profile, ok := basementClosedLocalCoreExecutionProfileForModule(moduleRef)
	if !ok {
		return BasementCoreRuntimeProfile{}, false
	}
	services := profile.serviceContracts()
	result := BasementCoreRuntimeProfile{
		ProviderRef: profile.providerRef, ModuleRef: profile.moduleRef,
		UnitRef: profile.unitRef, WorkloadRef: profile.workloadRef,
		OutputRef: profile.outputRef, ArtifactPrefix: profile.artifactPrefix,
		ImageRef: profile.imageRef, ImageDigest: profile.imageDigest,
		MaxArtifactBytes: profile.maxArtifactBytes,
		Services:         make([]BasementCoreServiceExpectation, len(services)),
		Health:           make([]BasementCoreHealthContract, len(profile.healthSpecs)),
	}
	for index, service := range services {
		result.Services[index] = BasementCoreServiceExpectation(service)
	}
	for index, spec := range profile.healthSpecs {
		result.Health[index] = BasementCoreHealthContract{
			SourceRef: spec.source, Kind: spec.kind, TargetKind: spec.targetKind,
			TargetRef: spec.targetRef, Port: spec.port, Path: spec.path,
			ExpectedStatuses: append([]int(nil), spec.statuses...),
		}
	}
	return result, true
}

func (profile BasementCoreRuntimeProfile) ValidateComposeArtifact(content []byte) bool {
	switch profile.ModuleRef {
	case basementCoreModuleRef:
		return architecturev2renderer.ValidateBasementCoreComposeArtifact(content)
	case basementCoreLiteModuleRef:
		return architecturev2renderer.ValidateBasementCoreLiteComposeArtifact(content)
	default:
		return false
	}
}

// BasementCoreProject is the closed, provider-free capability passed to the
// local Docker owner. It contains no executable, Docker endpoint, credential,
// or caller-selected filesystem path.
type BasementCoreProject struct {
	ModuleRef           string
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

type closedLocalCoreExecutionProfile struct {
	displayName             string
	providerRef             string
	moduleRef               string
	unitRef                 string
	workloadRef             string
	outputRef               string
	artifactPrefix          string
	imageRef                string
	imageDigest             string
	maxArtifactBytes        int
	healthSpecs             []basementCoreHealthSpec
	rendererContract        func() architecturev2renderer.RendererContract
	serviceContracts        func() []architecturev2renderer.BasementCoreServiceContract
	validateComposeArtifact func([]byte) bool
}

func basementClosedLocalCoreExecutionProfile() closedLocalCoreExecutionProfile {
	return closedLocalCoreExecutionProfile{
		displayName: "Basement core", providerRef: basementCoreProviderRef,
		moduleRef: basementCoreModuleRef, unitRef: basementCoreUnitRef,
		workloadRef: basementCoreWorkloadRef, outputRef: basementCoreOutputRef,
		artifactPrefix: basementCoreArtifactPrefix, imageRef: basementCoreImageRef,
		imageDigest: basementCoreImageDigest, maxArtifactBytes: basementCoreMaxArtifactBytes,
		healthSpecs:             basementCoreHealthSpecs,
		rendererContract:        architecturev2renderer.BasementCoreComposeRendererContract,
		serviceContracts:        architecturev2renderer.BasementCoreServiceContracts,
		validateComposeArtifact: architecturev2renderer.ValidateBasementCoreComposeArtifact,
	}
}

func basementClosedLocalCoreLiteExecutionProfile() closedLocalCoreExecutionProfile {
	return closedLocalCoreExecutionProfile{
		displayName: "Basement core lite", providerRef: basementCoreProviderRef,
		moduleRef: basementCoreLiteModuleRef, unitRef: basementCoreUnitRef,
		workloadRef: basementCoreWorkloadRef, outputRef: basementCoreLiteOutputRef,
		artifactPrefix: basementCoreLiteArtifactPrefix, imageRef: basementCoreLiteImageRef,
		imageDigest: basementCoreLiteImageDigest, maxArtifactBytes: basementCoreMaxArtifactBytes,
		healthSpecs:             basementCoreLiteHealthSpecs,
		rendererContract:        architecturev2renderer.BasementCoreLiteComposeRendererContract,
		serviceContracts:        architecturev2renderer.BasementCoreLiteServiceContracts,
		validateComposeArtifact: architecturev2renderer.ValidateBasementCoreLiteComposeArtifact,
	}
}

func basementClosedLocalCoreExecutionProfileForModule(moduleRef string) (closedLocalCoreExecutionProfile, bool) {
	switch moduleRef {
	case basementCoreModuleRef:
		return basementClosedLocalCoreExecutionProfile(), true
	case basementCoreLiteModuleRef:
		return basementClosedLocalCoreLiteExecutionProfile(), true
	default:
		return closedLocalCoreExecutionProfile{}, false
	}
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
		!validCoreHostBootstrapDigest(e.authority.ModuleContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Basement core executor requires one explicit authenticated local authority")
	}
	if len(request.RuntimeTargets) != 1 {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Basement core executor requires one exact runtime target")
	}
	profile, supported := basementClosedLocalCoreExecutionProfileForModule(request.RuntimeTargets[0].ModuleRef)
	if !supported || len(e.authority.HealthContractHashes) != len(profile.healthSpecs) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Basement core executor does not support the selected local profile")
	}
	target, health, project, err := validateClosedLocalCoreRequest(request, e.binding, e.authority, profile)
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

func validateClosedLocalCoreRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority BasementCoreAuthority, profile closedLocalCoreExecutionProfile) (runtimeexecutor.RuntimeTarget, []runtimeexecutor.HealthTarget, BasementCoreProject, error) {
	if len(request.RuntimeTargets) != 1 || len(request.AccessBindings) != 0 || len(request.HealthTargets) != len(profile.healthSpecs) {
		return runtimeexecutor.RuntimeTarget{}, nil, BasementCoreProject{}, fmt.Errorf("%s requires exactly one runtime, %d health targets, and no access binding", profile.displayName, len(profile.healthSpecs))
	}
	target := request.RuntimeTargets[0]
	contract := profile.rendererContract()
	instanceRef := profile.unitRef + "-node-" + binding.NodeRef
	artifactID := profile.artifactPrefix + instanceRef
	if target.OwnerKind != "module" || target.OwnerRef != profile.moduleRef || target.OwnerVersion != "" ||
		target.ProviderRef != profile.providerRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != profile.moduleRef || target.OwnerContractHash != authority.ModuleContractHash ||
		target.ModuleContractHash != authority.ModuleContractHash || target.UnitRef != profile.unitRef ||
		target.UnitContractHash != contract.ContractHash || target.RuntimeKind != "container" ||
		target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "docker" ||
		target.InstanceRef != instanceRef || target.WorkloadRef != profile.workloadRef ||
		target.ImageRef != profile.imageRef || target.ImageDigest != profile.imageDigest ||
		target.ExecutionChannelRef != binding.ExecutionChannelRef ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) ||
		!slices.Equal(target.NodeRefs, []string{binding.NodeRef}) ||
		len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 ||
		len(target.AccessBindingRefs) != 0 || len(target.BackupTargetCapabilities) != 0 ||
		len(target.BackupTargetBindingRefs) != 0 || target.RuntimeAdapter != nil ||
		!slices.Equal(target.ArtifactRefs, []string{artifactID}) {
		return runtimeexecutor.RuntimeTarget{}, nil, BasementCoreProject{}, fmt.Errorf("runtime target is not the exact locally bound %s Compose contract", profile.displayName)
	}
	artifact, err := exactClosedLocalCoreArtifact(request.Artifacts, target, artifactID, contract.ContractHash, profile)
	if err != nil {
		return runtimeexecutor.RuntimeTarget{}, nil, BasementCoreProject{}, err
	}
	health, expectations, err := exactClosedLocalCoreHealth(request.HealthTargets, target, authority, profile)
	if err != nil {
		return runtimeexecutor.RuntimeTarget{}, nil, BasementCoreProject{}, err
	}
	services := profile.serviceContracts()
	serviceExpectations := make([]BasementCoreServiceExpectation, len(services))
	for index, service := range services {
		serviceExpectations[index] = BasementCoreServiceExpectation(service)
	}
	return target, health, BasementCoreProject{
		ModuleRef: profile.moduleRef, ProjectRef: instanceRef, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef,
		ExecutionChannelRef: binding.ExecutionChannelRef, ArtifactID: artifact.ID,
		ArtifactDigest: artifact.Digest, Definition: append([]byte(nil), artifact.Content...),
		Services: serviceExpectations, Health: expectations,
	}, nil
}

func exactClosedLocalCoreArtifact(artifacts []runtimeexecutor.Artifact, target runtimeexecutor.RuntimeTarget, artifactID, unitHash string, profile closedLocalCoreExecutionProfile) (runtimeexecutor.Artifact, error) {
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
		artifact.OutputRef != profile.outputRef || !slices.Equal(artifact.SiteRefs, target.SiteRefs) ||
		!slices.Equal(artifact.NodeRefs, target.NodeRefs) || len(artifact.Content) == 0 ||
		len(artifact.Content) > profile.maxArtifactBytes ||
		!profile.validateComposeArtifact(artifact.Content) {
		return runtimeexecutor.Artifact{}, fmt.Errorf("artifact is not the exact CUE-owned %s Compose instance", profile.displayName)
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

var basementCoreLiteHealthSpecs = liteBasementCoreHealthSpecs()

func liteBasementCoreHealthSpecs() []basementCoreHealthSpec {
	result := make([]basementCoreHealthSpec, 0, len(basementCoreHealthSpecs)-1)
	for _, spec := range basementCoreHealthSpecs {
		if spec.source == "coolify-http" {
			continue
		}
		spec.targetRef = basementCoreLiteModuleRef
		spec.statuses = append([]int(nil), spec.statuses...)
		result = append(result, spec)
	}
	return result
}

func exactClosedLocalCoreHealth(input []runtimeexecutor.HealthTarget, target runtimeexecutor.RuntimeTarget, authority BasementCoreAuthority, profile closedLocalCoreExecutionProfile) ([]runtimeexecutor.HealthTarget, []BasementCoreHealthExpectation, error) {
	bySource := make(map[string]runtimeexecutor.HealthTarget, len(input))
	for _, item := range input {
		if _, duplicate := bySource[item.SourceRef]; duplicate {
			return nil, nil, errors.New("Basement core health targets contain a duplicate source")
		}
		bySource[item.SourceRef] = item
	}
	health := make([]runtimeexecutor.HealthTarget, 0, len(profile.healthSpecs))
	expectations := make([]BasementCoreHealthExpectation, 0, len(profile.healthSpecs))
	for _, spec := range profile.healthSpecs {
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
