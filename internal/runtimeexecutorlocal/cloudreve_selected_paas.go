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
	cloudreveProviderRef    = "stackkits-cloudreve"
	cloudreveModuleRef      = "stackkits-cloudreve-runtime"
	cloudreveUnitRef        = "cloudreve"
	cloudreveWorkloadRef    = "files"
	cloudreveInstancePrefix = "cloudreve-node-"
	cloudreveArtifactPrefix = "cloudreve-workload-bundle-instance-"
	cloudreveOutputRef      = "workloads/cloudreve/bundle.json"
	cloudreveHealthID       = "module-stackkits-cloudreve-runtime-cloudreve-http"
	cloudreveHealthRef      = "cloudreve-http"
	cloudreveImageRef       = "docker.io/cloudreve/cloudreve:4.18.0"
	cloudreveImageDigest    = "sha256:f7a464100bf6325e9ba58cb2b0ee60f9a24c58fc2eb90647720bc4b8f3cddd9a"
	cloudreveMaxBytes       = 128 << 10
)

type CloudreveWorkloadAuthority = SelectedPaaSWorkloadAuthority

type CloudreveSelectedPaaSExecutor struct {
	core *selectedPaaSWorkloadExecutor
}

func NewCloudreveSelectedPaaSExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority CloudreveWorkloadAuthority, operations SelectedPaaSWorkloadOperations) *CloudreveSelectedPaaSExecutor {
	return &CloudreveSelectedPaaSExecutor{core: newSelectedPaaSWorkloadExecutor(
		"Cloudreve", identity, binding, authority, operations, validateCloudreveSelectedPaaSCoreRequest,
	)}
}

func (e *CloudreveSelectedPaaSExecutor) Identity() runtimeexecutor.ExecutorIdentity {
	if e == nil {
		return runtimeexecutor.ExecutorIdentity{}
	}
	return e.core.Identity()
}

func (e *CloudreveSelectedPaaSExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if e == nil || e.core == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloudreve selected-PaaS executor is not initialized")
	}
	return e.core.Execute(ctx, request)
}

func validateCloudreveSelectedPaaSCoreRequest(
	request runtimeexecutor.ExecutionRequest,
	binding LocalTargetBinding,
	authority SelectedPaaSWorkloadAuthority,
) (selectedPaaSValidatedRequest, error) {
	target, health, deployment, descriptor, err := validateCloudreveSelectedPaaSRequest(
		request, binding, authority,
	)
	if err != nil {
		return selectedPaaSValidatedRequest{}, err
	}
	return selectedPaaSValidatedRequest{
		target: target, health: health, deployment: deployment,
		validateObservation: func(observation SelectedPaaSWorkloadObservation) error {
			return validateCloudreveObservation(observation, deployment, descriptor)
		},
	}, nil
}

func validateCloudreveSelectedPaaSRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority CloudreveWorkloadAuthority) (runtimeexecutor.RuntimeTarget, []runtimeexecutor.HealthTarget, SelectedPaaSWorkloadDeployment, architecturev2renderer.CloudreveWorkloadBundleDescriptor, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, []runtimeexecutor.HealthTarget(nil)
	emptyDeployment := SelectedPaaSWorkloadDeployment{}
	emptyDescriptor := architecturev2renderer.CloudreveWorkloadBundleDescriptor{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) == 0 || len(request.AccessBindings) != 0 {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("Cloudreve selected-PaaS executor requires exactly one runtime, governed health targets, and no access binding")
	}
	target := request.RuntimeTargets[0]
	artifact, exists := runtimeExecutorArtifactByID(request.Artifacts, firstRuntimeArtifactRef(target.ArtifactRefs))
	if !exists {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("Cloudreve selected-PaaS workload artifact is absent")
	}
	if target.OwnerKind != "module" || target.OwnerRef != cloudreveModuleRef || target.OwnerContractHash != authority.ModuleContractHash ||
		target.ProviderRef != cloudreveProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != cloudreveModuleRef || target.ModuleContractHash != authority.ModuleContractHash ||
		target.UnitRef != cloudreveUnitRef || target.UnitContractHash != authority.UnitContractHash ||
		target.RuntimeKind != "container" || target.RuntimeDelivery != "selected-paas" || target.RuntimeEngine != "docker" ||
		target.WorkloadRef != cloudreveWorkloadRef || target.ImageRef != cloudreveImageRef || target.ImageDigest != cloudreveImageDigest ||
		target.InstanceRef != cloudreveInstancePrefix+binding.NodeRef || target.ExecutionChannelRef != binding.ExecutionChannelRef ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) ||
		len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		!slices.Equal(target.ArtifactRefs, []string{artifact.ID}) {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("runtime target is not the exact bound Cloudreve selected-PaaS contract")
	}
	moduleHealthIndex := -1
	for index, health := range request.HealthTargets {
		if health.TargetKind == "module" {
			if moduleHealthIndex >= 0 || health.RequirementID != cloudreveHealthID ||
				health.RuntimeRequirementID != "" || health.SourceRef != cloudreveHealthRef ||
				health.ContractHash != authority.HealthContractHash || health.Phase != "continuous" ||
				health.Kind != "http" || health.TargetRef != cloudreveModuleRef || health.Probe != nil ||
				health.RouteRef != "" || health.BackendPoolRef != "" ||
				!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
				return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("health target is not the exact Cloudreve HTTP postcondition")
			}
			moduleHealthIndex = index
			continue
		}
		if err := validateCloudreveRouteHealthTarget(health, target); err != nil {
			return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, err
		}
	}
	if moduleHealthIndex < 0 {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("Cloudreve request has no exact module health target")
	}
	adapterArtifacts, err := validateSelectedPaaSRuntimeAdapter(target.RuntimeAdapter, request.Artifacts, authority.RuntimeAdapter)
	if err != nil {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, err
	}
	if artifact.ID != cloudreveArtifactPrefix+target.InstanceRef || artifact.Kind != "native-config" ||
		artifact.Format != "json" || artifact.Mode != "0640" || artifact.OwnerKind != "render-instance" ||
		artifact.OwnerRef != target.InstanceRef || artifact.OwnerContractHash != authority.UnitContractHash ||
		artifact.ProviderRef != cloudreveProviderRef || artifact.ProviderContractHash != authority.ProviderContractHash ||
		artifact.ModuleRef != cloudreveModuleRef || artifact.ModuleContractHash != authority.ModuleContractHash ||
		artifact.UnitRef != cloudreveUnitRef || artifact.UnitContractHash != authority.UnitContractHash ||
		artifact.InstanceRef != target.InstanceRef || artifact.OutputRef != cloudreveOutputRef ||
		!slices.Equal(artifact.SiteRefs, target.SiteRefs) || !slices.Equal(artifact.NodeRefs, target.NodeRefs) ||
		len(artifact.Content) == 0 || len(artifact.Content) > cloudreveMaxBytes {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("artifact is not the exact target-bound Cloudreve workload bundle")
	}
	sum := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("Cloudreve artifact digest does not match immutable content")
	}
	descriptor, err := architecturev2renderer.ParseCloudreveWorkloadBundle(artifact.Content)
	if err != nil {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, fmt.Errorf("validate closed Cloudreve workload bundle: %w", err)
	}
	if descriptor.WorkloadRef != target.WorkloadRef || descriptor.ModuleRef != target.ModuleRef ||
		descriptor.SiteRef != binding.SiteRef || descriptor.NodeRef != binding.NodeRef ||
		descriptor.InstanceRef != target.InstanceRef {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("Cloudreve workload bundle target differs from authorized runtime target")
	}
	deployment := SelectedPaaSWorkloadDeployment{
		WorkloadRef: descriptor.WorkloadRef, ModuleRef: descriptor.ModuleRef, UnitRef: target.UnitRef,
		Release: descriptor.Release, SiteRef: descriptor.SiteRef, NodeRef: descriptor.NodeRef,
		InstanceRef: descriptor.InstanceRef, ExecutionChannelRef: binding.ExecutionChannelRef,
		ArtifactRef: artifact.ID, ArtifactDigest: artifact.Digest, Bundle: append([]byte(nil), artifact.Content...),
		Route:          descriptor.Route,
		RuntimeAdapter: *target.RuntimeAdapter, AdapterArtifacts: adapterArtifacts,
	}
	return target, append([]runtimeexecutor.HealthTarget(nil), request.HealthTargets...), deployment, descriptor, nil
}

func validateCloudreveRouteHealthTarget(health runtimeexecutor.HealthTarget, target runtimeexecutor.RuntimeTarget) error {
	probe := health.Probe
	if health.TargetKind != "route" || health.RuntimeRequirementID != target.RequirementID ||
		health.TargetRef == "" || health.RouteRef != health.TargetRef || health.BackendPoolRef == "" ||
		health.SourceRef != cloudreveHealthID || health.Phase != "post-apply" || health.Kind != "http" ||
		probe == nil || probe.Protocol != "http" || probe.Port != 5212 || probe.TimeoutSeconds != 10 ||
		probe.Method != "GET" || probe.FollowRedirects || probe.Path != "/" ||
		!slices.Equal(probe.ExpectedStatuses, []int{200, 302}) ||
		!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return errors.New("route health target is not the exact runtime-owned Cloudreve backend probe")
	}
	return nil
}

func validateCloudreveObservation(observation SelectedPaaSWorkloadObservation, deployment SelectedPaaSWorkloadDeployment, descriptor architecturev2renderer.CloudreveWorkloadBundleDescriptor) error {
	if observation.WorkloadRef != deployment.WorkloadRef || observation.Release != deployment.Release ||
		observation.InstanceRef != deployment.InstanceRef || observation.ArtifactDigest != deployment.ArtifactDigest ||
		observation.Status != "running" || !exactApplicationDeliveryRouteObservation(observation.Route, descriptor.Route) ||
		observation.Route.Method != "GET" || observation.Route.Path != "/" ||
		observation.Route.Status != "healthy" ||
		(observation.Route.HTTPStatus != 200 && observation.Route.HTTPStatus != 302) ||
		len(observation.Components) != len(descriptor.Components) {
		return errors.New("selected-PaaS observation does not prove the exact running Cloudreve workload and route")
	}
	for index, expected := range descriptor.Components {
		actual := observation.Components[index]
		if actual.ID != expected.ID || actual.ImageDigest != expected.ImageDigest ||
			actual.Status != "running" || actual.Health != "healthy" {
			return fmt.Errorf("selected-PaaS observation does not prove exact component %q", expected.ID)
		}
	}
	return nil
}
