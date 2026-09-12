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
	privateAIProviderRef    = "stackkits-private-ai"
	privateAIModuleRef      = "stackkits-private-ai-runtime"
	privateAIUnitRef        = "private-ai"
	privateAIWorkloadRef    = "ai"
	privateAIInstancePrefix = "private-ai-node-"
	privateAIArtifactPrefix = "private-ai-workload-bundle-instance-"
	privateAIOutputRef      = "workloads/private-ai/bundle.json"
	privateAIHealthID       = "module-stackkits-private-ai-runtime-private-ai-http"
	privateAIHealthRef      = "private-ai-http"
	privateAIMaxBytes       = 128 << 10
)

type PrivateAIWorkloadAuthority = SelectedPaaSWorkloadAuthority

type PrivateAISelectedPaaSExecutor struct {
	core *selectedPaaSWorkloadExecutor
}

func NewPrivateAISelectedPaaSExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority PrivateAIWorkloadAuthority, operations SelectedPaaSWorkloadOperations) *PrivateAISelectedPaaSExecutor {
	return &PrivateAISelectedPaaSExecutor{core: newSelectedPaaSWorkloadExecutor(
		"PrivateAI", identity, binding, authority, operations, validatePrivateAISelectedPaaSCoreRequest,
	)}
}

func (e *PrivateAISelectedPaaSExecutor) Identity() runtimeexecutor.ExecutorIdentity {
	if e == nil {
		return runtimeexecutor.ExecutorIdentity{}
	}
	return e.core.Identity()
}

func (e *PrivateAISelectedPaaSExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if e == nil || e.core == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("PrivateAI selected-PaaS executor is not initialized")
	}
	return e.core.Execute(ctx, request)
}

func validatePrivateAISelectedPaaSCoreRequest(
	request runtimeexecutor.ExecutionRequest,
	binding LocalTargetBinding,
	authority SelectedPaaSWorkloadAuthority,
) (selectedPaaSValidatedRequest, error) {
	target, health, deployment, descriptor, err := validatePrivateAISelectedPaaSRequest(request, binding, authority)
	if err != nil {
		return selectedPaaSValidatedRequest{}, err
	}
	return selectedPaaSValidatedRequest{
		target: target, health: health, deployment: deployment,
		validateObservation: func(observation SelectedPaaSWorkloadObservation) error {
			return validatePrivateAIObservation(observation, deployment, descriptor)
		},
	}, nil
}

func validatePrivateAISelectedPaaSRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority PrivateAIWorkloadAuthority) (runtimeexecutor.RuntimeTarget, []runtimeexecutor.HealthTarget, SelectedPaaSWorkloadDeployment, architecturev2renderer.PrivateAIWorkloadBundleDescriptor, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, []runtimeexecutor.HealthTarget(nil)
	emptyDeployment := SelectedPaaSWorkloadDeployment{}
	emptyDescriptor := architecturev2renderer.PrivateAIWorkloadBundleDescriptor{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) == 0 || len(request.AccessBindings) != 0 {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("PrivateAI selected-PaaS executor requires exactly one runtime, governed health targets, and no access binding")
	}
	target := request.RuntimeTargets[0]
	artifact, exists := runtimeExecutorArtifactByID(request.Artifacts, firstRuntimeArtifactRef(target.ArtifactRefs))
	if !exists {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("PrivateAI selected-PaaS workload artifact is absent")
	}
	if target.OwnerKind != "module" || target.OwnerRef != privateAIModuleRef || target.OwnerContractHash != authority.ModuleContractHash ||
		target.ProviderRef != privateAIProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != privateAIModuleRef || target.ModuleContractHash != authority.ModuleContractHash ||
		target.UnitRef != privateAIUnitRef || target.UnitContractHash != authority.UnitContractHash ||
		target.RuntimeKind != "container" || target.RuntimeDelivery != "selected-paas" || target.RuntimeEngine != "docker" ||
		target.WorkloadRef != privateAIWorkloadRef || (target.ImageRef != architecturev2renderer.PrivateAIImageRef || target.ImageDigest != architecturev2renderer.PrivateAIImageDigest) ||
		target.InstanceRef != privateAIInstancePrefix+binding.NodeRef || target.ExecutionChannelRef != binding.ExecutionChannelRef ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) ||
		len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		!slices.Equal(target.ArtifactRefs, []string{artifact.ID}) {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("runtime target is not the exact bound PrivateAI selected-PaaS contract")
	}
	moduleHealthIndex := -1
	for index, health := range request.HealthTargets {
		if health.TargetKind == "module" {
			if moduleHealthIndex >= 0 || health.RequirementID != privateAIHealthID ||
				health.RuntimeRequirementID != "" || health.SourceRef != privateAIHealthRef ||
				health.ContractHash != authority.HealthContractHash || health.Phase != "continuous" ||
				health.Kind != "http" || health.TargetRef != privateAIModuleRef || health.Probe != nil ||
				health.RouteRef != "" || health.BackendPoolRef != "" ||
				!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
				return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("health target is not the exact PrivateAI HTTP postcondition")
			}
			moduleHealthIndex = index
			continue
		}
		if err := validatePrivateAIRouteHealthTarget(health, target); err != nil {
			return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, err
		}
	}
	if moduleHealthIndex < 0 {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("PrivateAI request has no exact module health target")
	}
	adapterArtifacts, err := validateSelectedPaaSRuntimeAdapter(target.RuntimeAdapter, request.Artifacts, authority.RuntimeAdapter)
	if err != nil {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, err
	}
	if artifact.ID != privateAIArtifactPrefix+target.InstanceRef || artifact.Kind != "native-config" ||
		artifact.Format != "json" || artifact.Mode != "0640" || artifact.OwnerKind != "render-instance" ||
		artifact.OwnerRef != target.InstanceRef || artifact.OwnerContractHash != authority.UnitContractHash ||
		artifact.ProviderRef != privateAIProviderRef || artifact.ProviderContractHash != authority.ProviderContractHash ||
		artifact.ModuleRef != privateAIModuleRef || artifact.ModuleContractHash != authority.ModuleContractHash ||
		artifact.UnitRef != privateAIUnitRef || artifact.UnitContractHash != authority.UnitContractHash ||
		artifact.InstanceRef != target.InstanceRef || artifact.OutputRef != privateAIOutputRef ||
		!slices.Equal(artifact.SiteRefs, target.SiteRefs) || !slices.Equal(artifact.NodeRefs, target.NodeRefs) ||
		len(artifact.Content) == 0 || len(artifact.Content) > privateAIMaxBytes {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("artifact is not the exact target-bound PrivateAI workload bundle")
	}
	sum := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("PrivateAI artifact digest does not match immutable content")
	}
	descriptor, err := architecturev2renderer.ParsePrivateAIWorkloadBundle(artifact.Content)
	if err != nil {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, fmt.Errorf("validate closed PrivateAI workload bundle: %w", err)
	}
	if descriptor.WorkloadRef != target.WorkloadRef || descriptor.ModuleRef != target.ModuleRef ||
		descriptor.SiteRef != binding.SiteRef || descriptor.NodeRef != binding.NodeRef ||
		descriptor.InstanceRef != target.InstanceRef {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("PrivateAI workload bundle target differs from authorized runtime target")
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

func validatePrivateAIRouteHealthTarget(health runtimeexecutor.HealthTarget, target runtimeexecutor.RuntimeTarget) error {
	probe := health.Probe
	if health.TargetKind != "route" || health.RuntimeRequirementID != target.RequirementID ||
		health.TargetRef == "" || health.RouteRef != health.TargetRef || health.BackendPoolRef == "" ||
		health.SourceRef != privateAIHealthID || health.Phase != "post-apply" || health.Kind != "http" ||
		probe == nil || probe.Protocol != "http" || probe.Port != 8080 || probe.TimeoutSeconds != 10 ||
		probe.Method != "GET" || probe.FollowRedirects || probe.Path != "/health" ||
		!slices.Equal(probe.ExpectedStatuses, []int{200}) ||
		!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return errors.New("route health target is not the exact runtime-owned PrivateAI backend probe")
	}
	return nil
}

func validatePrivateAIObservation(observation SelectedPaaSWorkloadObservation, deployment SelectedPaaSWorkloadDeployment, descriptor architecturev2renderer.PrivateAIWorkloadBundleDescriptor) error {
	bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(deployment.Bundle)
	if err != nil {
		return err
	}
	return validateStandaloneApplicationObservation(observation, deployment, bundle, []int{200})
}
