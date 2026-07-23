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
	immichWorkloadProviderRef    = "stackkits-immich"
	immichWorkloadModuleRef      = "stackkits-immich-runtime"
	immichWorkloadUnitRef        = "immich-server"
	immichWorkloadRef            = "photos"
	immichWorkloadInstancePrefix = "immich-server-node-"
	immichWorkloadArtifactPrefix = "immich-workload-bundle-instance-"
	immichWorkloadOutputRef      = "workloads/immich/bundle.json"
	immichWorkloadHealthID       = "module-stackkits-immich-runtime-immich-http"
	immichWorkloadHealthRef      = "immich-http"
	immichWorkloadImageRef       = "ghcr.io/immich-app/immich-server:v2.7.0"
	immichWorkloadImageDigest    = "sha256:ee60b98e7fcc836d61d7f5e7689514f3de7a9480f31ec6ca62d6221056b46ae1"
	immichWorkloadMaxBytes       = 512 << 10
)

// ImmichWorkloadAuthority is catalog authority fixed by service-owned adapter
// registration. None of these hashes can be supplied by runtime request data.
type ImmichWorkloadAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	UnitContractHash     string
	HealthContractHash   string
	RuntimeAdapter       SelectedPaaSRuntimeAdapterAuthority
}

type SelectedPaaSRuntimeAdapterAgentAuthority struct {
	ID                 string
	ModuleRef          string
	ModuleVersion      string
	ModuleContractHash string
}

// SelectedPaaSRuntimeAdapterAuthority is service-owned catalog authority for
// the one adapter implementation this executor is allowed to call.
type SelectedPaaSRuntimeAdapterAuthority struct {
	ID                   string
	ProviderRef          string
	ProviderVersion      string
	ProviderContractHash string
	ModuleRef            string
	ModuleVersion        string
	ModuleContractHash   string
	Agents               []SelectedPaaSRuntimeAdapterAgentAuthority
}

// SelectedPaaSWorkloadDeployment is a defensive, provider-neutral request to
// an already selected PaaS integration. Bundle contains only the validated
// workload graph and opaque secret references, never secret material.
type SelectedPaaSWorkloadDeployment struct {
	WorkloadRef         string
	ModuleRef           string
	UnitRef             string
	Release             string
	SiteRef             string
	NodeRef             string
	InstanceRef         string
	ExecutionChannelRef string
	ArtifactRef         string
	ArtifactDigest      string
	Bundle              []byte
	RuntimeAdapter      runtimeexecutor.RuntimeAdapterBinding
	AdapterArtifacts    []runtimeexecutor.Artifact
}

// SelectedPaaSApplyReceipt proves that the selected PaaS accepted the exact
// immutable workload artifact. It is not sufficient without readback.
type SelectedPaaSApplyReceipt struct {
	InstanceRef    string `json:"instanceRef"`
	ArtifactDigest string `json:"artifactDigest"`
	Status         string `json:"status"`
}

// SelectedPaaSComponentObservation is exact post-apply component readback.
type SelectedPaaSComponentObservation struct {
	ID          string `json:"id"`
	ImageDigest string `json:"imageDigest"`
	Status      string `json:"status"`
	Health      string `json:"health"`
}

// SelectedPaaSRouteObservation is the bounded Immich service readback.
type SelectedPaaSRouteObservation struct {
	ServiceRef string `json:"serviceRef"`
	Protocol   string `json:"protocol"`
	Port       int    `json:"port"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     string `json:"status"`
	HTTPStatus int    `json:"httpStatus"`
}

// SelectedPaaSWorkloadObservation is the complete runtime postcondition.
type SelectedPaaSWorkloadObservation struct {
	WorkloadRef    string                             `json:"workloadRef"`
	Release        string                             `json:"release"`
	InstanceRef    string                             `json:"instanceRef"`
	ArtifactDigest string                             `json:"artifactDigest"`
	Status         string                             `json:"status"`
	Components     []SelectedPaaSComponentObservation `json:"components"`
	Route          SelectedPaaSRouteObservation       `json:"route"`
}

// SelectedPaaSWorkloadOperations is implemented by the selected PaaS owner.
// It intentionally has no provider/server lifecycle, lease, generation,
// endpoint selection, credential, generic command, or filesystem method.
type SelectedPaaSWorkloadOperations interface {
	ApplyWorkload(context.Context, SelectedPaaSWorkloadDeployment) (SelectedPaaSApplyReceipt, error)
	ObserveWorkload(context.Context, SelectedPaaSWorkloadDeployment) (SelectedPaaSWorkloadObservation, error)
}

// ImmichSelectedPaaSExecutor consumes only the exact generated Immich bundle.
// Product registration is available only through an explicitly supplied,
// authenticated operations implementation owned by the selected PaaS control
// plane; this adapter never discovers or constructs one.
type ImmichSelectedPaaSExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalTargetBinding
	authority  ImmichWorkloadAuthority
	operations SelectedPaaSWorkloadOperations
}

func NewImmichSelectedPaaSExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority ImmichWorkloadAuthority, operations SelectedPaaSWorkloadOperations) *ImmichSelectedPaaSExecutor {
	return &ImmichSelectedPaaSExecutor{identity: identity, binding: binding, authority: authority, operations: operations}
}

func (e *ImmichSelectedPaaSExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *ImmichSelectedPaaSExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Immich selected-PaaS executor requires a context")
	}
	if e == nil || e.operations == nil || strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" || strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) || !validCoreHostBootstrapDigest(e.authority.ModuleContractHash) ||
		!validCoreHostBootstrapDigest(e.authority.UnitContractHash) || !validCoreHostBootstrapDigest(e.authority.HealthContractHash) || !validSelectedPaaSRuntimeAdapterAuthority(e.authority.RuntimeAdapter) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Immich selected-PaaS executor requires one explicit authenticated target and exact catalog authority")
	}
	target, health, deployment, descriptor, err := validateImmichSelectedPaaSRequest(request, e.binding, e.authority)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	receipt, err := e.operations.ApplyWorkload(ctx, defensiveSelectedPaaSDeployment(deployment))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("apply exact Immich workload bundle: %w", err)
	}
	if receipt.InstanceRef != deployment.InstanceRef || receipt.ArtifactDigest != deployment.ArtifactDigest || receipt.Status != "applied" {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("selected-PaaS apply receipt does not bind the exact Immich target and artifact")
	}
	observation, err := e.operations.ObserveWorkload(ctx, defensiveSelectedPaaSDeployment(deployment))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("observe exact Immich workload bundle: %w", err)
	}
	observation.Components = append([]SelectedPaaSComponentObservation(nil), observation.Components...)
	sort.Slice(observation.Components, func(i, j int) bool { return observation.Components[i].ID < observation.Components[j].ID })
	if err := validateImmichSelectedPaaSObservation(observation, deployment, descriptor); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evidence, err := json.Marshal(struct {
		Apply       SelectedPaaSApplyReceipt        `json:"apply"`
		Observation SelectedPaaSWorkloadObservation `json:"observation"`
	}{Apply: receipt, Observation: observation})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal Immich selected-PaaS evidence: %w", err)
	}
	sum := sha256.Sum256(evidence)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	healthOutcomes := make([]runtimeexecutor.HealthOutcome, len(health))
	for index, requirement := range health {
		healthOutcomes[index] = runtimeexecutor.HealthOutcome{
			RequirementID: requirement.RequirementID, TargetRef: requirement.TargetRef, Status: runtimeexecutor.HealthStatusHealthy,
			ObservationRef: "health-observation://selected-paas/" + target.InstanceRef + "/" + requirement.RequirementID, ObservationDigest: digest,
		}
	}
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied, ObservationRef: "runtime-observation://selected-paas/" + target.InstanceRef, ObservationDigest: digest}},
		Health:  healthOutcomes,
	}, nil
}

func validateImmichSelectedPaaSRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority ImmichWorkloadAuthority) (runtimeexecutor.RuntimeTarget, []runtimeexecutor.HealthTarget, SelectedPaaSWorkloadDeployment, architecturev2renderer.ImmichWorkloadBundleDescriptor, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, []runtimeexecutor.HealthTarget(nil)
	emptyDeployment, emptyDescriptor := SelectedPaaSWorkloadDeployment{}, architecturev2renderer.ImmichWorkloadBundleDescriptor{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) == 0 || len(request.AccessBindings) != 0 {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("Immich selected-PaaS executor requires exactly one runtime, governed health targets, and no access binding")
	}
	target := request.RuntimeTargets[0]
	artifact, exists := runtimeExecutorArtifactByID(request.Artifacts, firstRuntimeArtifactRef(target.ArtifactRefs))
	if !exists {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("Immich selected-PaaS workload artifact is absent")
	}
	if target.OwnerKind != "module" || target.OwnerRef != immichWorkloadModuleRef || target.OwnerContractHash != authority.ModuleContractHash ||
		target.ProviderRef != immichWorkloadProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != immichWorkloadModuleRef || target.ModuleContractHash != authority.ModuleContractHash || target.UnitRef != immichWorkloadUnitRef || target.UnitContractHash != authority.UnitContractHash ||
		target.RuntimeKind != "container" || target.RuntimeDelivery != "selected-paas" || target.RuntimeEngine != "docker" ||
		target.WorkloadRef != immichWorkloadRef || target.ImageRef != immichWorkloadImageRef || target.ImageDigest != immichWorkloadImageDigest ||
		target.InstanceRef != immichWorkloadInstancePrefix+binding.NodeRef || target.ExecutionChannelRef != binding.ExecutionChannelRef ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) || len(target.DaemonBindings) != 0 ||
		len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 || !slices.Equal(target.ArtifactRefs, []string{artifact.ID}) {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("runtime target is not the exact bound Immich selected-PaaS contract")
	}
	moduleHealthIndex := -1
	for index, health := range request.HealthTargets {
		if health.TargetKind == "module" {
			if moduleHealthIndex >= 0 || health.RequirementID != immichWorkloadHealthID || health.RuntimeRequirementID != "" || health.SourceRef != immichWorkloadHealthRef || health.ContractHash != authority.HealthContractHash ||
				health.Phase != "continuous" || health.Kind != "http" || health.TargetRef != immichWorkloadModuleRef || health.Probe != nil ||
				health.RouteRef != "" || health.BackendPoolRef != "" || !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
				return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("health target is not the exact Immich HTTP postcondition")
			}
			moduleHealthIndex = index
			continue
		}
		if err := validateImmichRouteHealthTarget(health, target); err != nil {
			return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, err
		}
	}
	if moduleHealthIndex < 0 {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("Immich selected-PaaS request has no exact module health target")
	}
	adapterArtifacts, err := validateSelectedPaaSRuntimeAdapter(target.RuntimeAdapter, request.Artifacts, authority.RuntimeAdapter)
	if err != nil {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, err
	}
	if artifact.ID != immichWorkloadArtifactPrefix+target.InstanceRef || artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0640" ||
		artifact.OwnerKind != "render-instance" || artifact.OwnerRef != target.InstanceRef || artifact.OwnerContractHash != authority.UnitContractHash ||
		artifact.ProviderRef != immichWorkloadProviderRef || artifact.ProviderContractHash != authority.ProviderContractHash || artifact.ModuleRef != immichWorkloadModuleRef || artifact.ModuleContractHash != authority.ModuleContractHash ||
		artifact.UnitRef != immichWorkloadUnitRef || artifact.UnitContractHash != authority.UnitContractHash || artifact.InstanceRef != target.InstanceRef || artifact.OutputRef != immichWorkloadOutputRef ||
		!slices.Equal(artifact.SiteRefs, target.SiteRefs) || !slices.Equal(artifact.NodeRefs, target.NodeRefs) || len(artifact.Content) == 0 || len(artifact.Content) > immichWorkloadMaxBytes {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("artifact is not the exact target-bound Immich workload bundle")
	}
	sum := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("Immich workload artifact digest does not match its immutable content")
	}
	descriptor, err := architecturev2renderer.ParseImmichWorkloadBundle(artifact.Content)
	if err != nil {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, fmt.Errorf("validate closed Immich workload bundle: %w", err)
	}
	if descriptor.WorkloadRef != target.WorkloadRef || descriptor.ModuleRef != target.ModuleRef || descriptor.SiteRef != binding.SiteRef || descriptor.NodeRef != binding.NodeRef || descriptor.InstanceRef != target.InstanceRef {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("Immich workload bundle target differs from the authorized runtime target")
	}
	deployment := SelectedPaaSWorkloadDeployment{
		WorkloadRef: descriptor.WorkloadRef, ModuleRef: descriptor.ModuleRef, UnitRef: target.UnitRef, Release: descriptor.Release,
		SiteRef: descriptor.SiteRef, NodeRef: descriptor.NodeRef, InstanceRef: descriptor.InstanceRef, ExecutionChannelRef: binding.ExecutionChannelRef,
		ArtifactRef: artifact.ID, ArtifactDigest: artifact.Digest, Bundle: append([]byte(nil), artifact.Content...),
		RuntimeAdapter: *target.RuntimeAdapter, AdapterArtifacts: adapterArtifacts,
	}
	return target, append([]runtimeexecutor.HealthTarget(nil), request.HealthTargets...), deployment, descriptor, nil
}

func validateImmichRouteHealthTarget(health runtimeexecutor.HealthTarget, target runtimeexecutor.RuntimeTarget) error {
	probe := health.Probe
	if health.TargetKind != "route" || health.RuntimeRequirementID != target.RequirementID || health.TargetRef == "" || health.RouteRef != health.TargetRef || health.BackendPoolRef == "" ||
		health.SourceRef != immichWorkloadHealthID || health.Phase != "post-apply" || health.Kind != "http" || probe == nil ||
		probe.Protocol != "http" || probe.Port != 2283 || probe.TimeoutSeconds != 10 || probe.Method != "GET" || probe.FollowRedirects || probe.Path != "/api/server/ping" ||
		!slices.Equal(probe.ExpectedStatuses, []int{200}) || !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return errors.New("route health target is not the exact runtime-owned Immich backend probe")
	}
	return nil
}

func validSelectedPaaSRuntimeAdapterAuthority(authority SelectedPaaSRuntimeAdapterAuthority) bool {
	if strings.TrimSpace(authority.ID) == "" || strings.TrimSpace(authority.ProviderRef) == "" || strings.TrimSpace(authority.ProviderVersion) == "" ||
		strings.TrimSpace(authority.ModuleRef) == "" || strings.TrimSpace(authority.ModuleVersion) == "" || !validCoreHostBootstrapDigest(authority.ProviderContractHash) ||
		!validCoreHostBootstrapDigest(authority.ModuleContractHash) {
		return false
	}
	seen := map[string]struct{}{}
	for _, agent := range authority.Agents {
		if strings.TrimSpace(agent.ID) == "" || strings.TrimSpace(agent.ModuleRef) == "" || strings.TrimSpace(agent.ModuleVersion) == "" || !validCoreHostBootstrapDigest(agent.ModuleContractHash) {
			return false
		}
		if _, duplicate := seen[agent.ID]; duplicate {
			return false
		}
		seen[agent.ID] = struct{}{}
	}
	return true
}

func validateSelectedPaaSRuntimeAdapter(binding *runtimeexecutor.RuntimeAdapterBinding, artifacts []runtimeexecutor.Artifact, authority SelectedPaaSRuntimeAdapterAuthority) ([]runtimeexecutor.Artifact, error) {
	if binding == nil || binding.ID != authority.ID || binding.ProviderRef != authority.ProviderRef || binding.ProviderVersion != authority.ProviderVersion ||
		binding.ProviderContractHash != authority.ProviderContractHash || binding.ModuleRef != authority.ModuleRef || binding.ModuleVersion != authority.ModuleVersion ||
		binding.ModuleContractHash != authority.ModuleContractHash || len(binding.Agents) != len(authority.Agents) {
		return nil, errors.New("runtime adapter does not match the service-owned selected-PaaS authority")
	}
	result := make([]runtimeexecutor.Artifact, 0, len(binding.ArtifactRefs)+len(binding.Agents))
	if err := appendSelectedPaaSAdapterArtifacts(&result, artifacts, binding.ProviderRef, binding.ProviderContractHash, binding.ModuleRef, binding.ModuleContractHash, binding.ArtifactRefs); err != nil {
		return nil, err
	}
	for index, agent := range binding.Agents {
		expected := authority.Agents[index]
		if agent.ID != expected.ID || agent.ModuleRef != expected.ModuleRef || agent.ModuleVersion != expected.ModuleVersion || agent.ModuleContractHash != expected.ModuleContractHash {
			return nil, errors.New("runtime adapter agent does not match the service-owned selected-PaaS authority")
		}
		if err := appendSelectedPaaSAdapterArtifacts(&result, artifacts, binding.ProviderRef, binding.ProviderContractHash, agent.ModuleRef, agent.ModuleContractHash, agent.ArtifactRefs); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func appendSelectedPaaSAdapterArtifacts(result *[]runtimeexecutor.Artifact, artifacts []runtimeexecutor.Artifact, providerRef, providerHash, moduleRef, moduleHash string, refs []string) error {
	for _, ref := range refs {
		artifact, exists := runtimeExecutorArtifactByID(artifacts, ref)
		if !exists || artifact.ExecutionClass != runtimeexecutor.ArtifactExecutionClassContractHandoff || artifact.ProviderRef != providerRef ||
			artifact.ProviderContractHash != providerHash || artifact.ModuleRef != moduleRef || artifact.ModuleContractHash != moduleHash {
			return fmt.Errorf("selected-PaaS adapter artifact %q does not match its exact module authority", ref)
		}
		artifact.SiteRefs = append([]string(nil), artifact.SiteRefs...)
		artifact.NodeRefs = append([]string(nil), artifact.NodeRefs...)
		artifact.Content = append([]byte(nil), artifact.Content...)
		*result = append(*result, artifact)
	}
	return nil
}

func runtimeExecutorArtifactByID(artifacts []runtimeexecutor.Artifact, id string) (runtimeexecutor.Artifact, bool) {
	for _, artifact := range artifacts {
		if artifact.ID == id {
			return artifact, true
		}
	}
	return runtimeexecutor.Artifact{}, false
}

func firstRuntimeArtifactRef(refs []string) string {
	if len(refs) != 1 {
		return ""
	}
	return refs[0]
}

func validateImmichSelectedPaaSObservation(observation SelectedPaaSWorkloadObservation, deployment SelectedPaaSWorkloadDeployment, descriptor architecturev2renderer.ImmichWorkloadBundleDescriptor) error {
	if observation.WorkloadRef != deployment.WorkloadRef || observation.Release != deployment.Release || observation.InstanceRef != deployment.InstanceRef || observation.ArtifactDigest != deployment.ArtifactDigest || observation.Status != "running" ||
		observation.Route.ServiceRef != "photos" || observation.Route.Protocol != "http" || observation.Route.Port != 2283 || observation.Route.Method != "GET" || observation.Route.Path != "/api/server/ping" ||
		observation.Route.Status != "healthy" || observation.Route.HTTPStatus != 200 || len(observation.Components) != len(descriptor.Components) {
		return errors.New("selected-PaaS observation does not prove the exact running Immich workload and route")
	}
	for index, expected := range descriptor.Components {
		actual := observation.Components[index]
		wantStatus, wantHealth := "running", "healthy"
		if expected.Lifecycle == "one-shot" {
			wantStatus, wantHealth = "completed", "completed"
		}
		if actual.ID != expected.ID || actual.ImageDigest != expected.ImageDigest || actual.Status != wantStatus || actual.Health != wantHealth {
			return fmt.Errorf("selected-PaaS observation does not prove exact component %q", expected.ID)
		}
	}
	return nil
}

func defensiveSelectedPaaSDeployment(input SelectedPaaSWorkloadDeployment) SelectedPaaSWorkloadDeployment {
	input.Bundle = append([]byte(nil), input.Bundle...)
	request := runtimeexecutor.CloneExecutionRequest(runtimeexecutor.ExecutionRequest{
		RuntimeTargets: []runtimeexecutor.RuntimeTarget{{RuntimeAdapter: &input.RuntimeAdapter}}, Artifacts: input.AdapterArtifacts,
	})
	input.RuntimeAdapter = *request.RuntimeTargets[0].RuntimeAdapter
	input.AdapterArtifacts = request.Artifacts
	return input
}
