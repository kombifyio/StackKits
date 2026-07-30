package runtimeexecutorlocal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

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

// ImmichWorkloadAuthority remains a source-compatible product alias while the
// execution boundary is the reusable selected-PaaS authority.
type ImmichWorkloadAuthority = SelectedPaaSWorkloadAuthority

// ImmichSelectedPaaSExecutor consumes only the exact generated Immich bundle.
// Product registration is available only through an explicitly supplied,
// authenticated operations implementation owned by the selected PaaS control
// plane; this adapter never discovers or constructs one.
type ImmichSelectedPaaSExecutor struct {
	core *selectedPaaSWorkloadExecutor
}

func NewImmichSelectedPaaSExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority ImmichWorkloadAuthority, operations SelectedPaaSWorkloadOperations) *ImmichSelectedPaaSExecutor {
	return &ImmichSelectedPaaSExecutor{core: newSelectedPaaSWorkloadExecutor(
		"Immich", identity, binding, authority, operations, validateImmichSelectedPaaSCoreRequest,
	)}
}

func (e *ImmichSelectedPaaSExecutor) Identity() runtimeexecutor.ExecutorIdentity {
	if e == nil {
		return runtimeexecutor.ExecutorIdentity{}
	}
	return e.core.Identity()
}

func (e *ImmichSelectedPaaSExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if e == nil || e.core == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Immich selected-PaaS executor is not initialized")
	}
	return e.core.Execute(ctx, request)
}

func validateImmichSelectedPaaSCoreRequest(
	request runtimeexecutor.ExecutionRequest,
	binding LocalTargetBinding,
	authority SelectedPaaSWorkloadAuthority,
) (selectedPaaSValidatedRequest, error) {
	target, health, deployment, descriptor, err := validateImmichSelectedPaaSRequest(
		request, binding, authority,
	)
	if err != nil {
		return selectedPaaSValidatedRequest{}, err
	}
	return selectedPaaSValidatedRequest{
		target: target, health: health, deployment: deployment,
		validateObservation: func(observation SelectedPaaSWorkloadObservation) error {
			return validateImmichSelectedPaaSObservation(observation, deployment, descriptor)
		},
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
