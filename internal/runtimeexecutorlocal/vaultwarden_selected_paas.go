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
	vaultwardenProviderRef    = "stackkits-vaultwarden"
	vaultwardenModuleRef      = "stackkits-vaultwarden-runtime"
	vaultwardenUnitRef        = "vaultwarden"
	vaultwardenWorkloadRef    = "vault"
	vaultwardenInstancePrefix = "vaultwarden-node-"
	vaultwardenArtifactPrefix = "vaultwarden-workload-bundle-instance-"
	vaultwardenOutputRef      = "workloads/vaultwarden/bundle.json"
	vaultwardenHealthID       = "module-stackkits-vaultwarden-runtime-vaultwarden-http"
	vaultwardenHealthRef      = "vaultwarden-http"
	vaultwardenImageRef       = "ghcr.io/dani-garcia/vaultwarden:1.35.4"
	vaultwardenImageDigest    = "sha256:43498a94b22f9563f2a94b53760ab3e710eefc0d0cac2efda4b12b9eb8690664"
	vaultwardenMaxBytes       = 128 << 10
)

type VaultwardenWorkloadAuthority = SelectedPaaSWorkloadAuthority

type VaultwardenSelectedPaaSExecutor struct {
	core *selectedPaaSWorkloadExecutor
}

func NewVaultwardenSelectedPaaSExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority VaultwardenWorkloadAuthority, operations SelectedPaaSWorkloadOperations) *VaultwardenSelectedPaaSExecutor {
	return &VaultwardenSelectedPaaSExecutor{core: newSelectedPaaSWorkloadExecutor(
		"Vaultwarden", identity, binding, authority, operations, validateVaultwardenSelectedPaaSCoreRequest,
	)}
}

func (e *VaultwardenSelectedPaaSExecutor) Identity() runtimeexecutor.ExecutorIdentity {
	if e == nil {
		return runtimeexecutor.ExecutorIdentity{}
	}
	return e.core.Identity()
}

func (e *VaultwardenSelectedPaaSExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if e == nil || e.core == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Vaultwarden selected-PaaS executor is not initialized")
	}
	return e.core.Execute(ctx, request)
}

func validateVaultwardenSelectedPaaSCoreRequest(
	request runtimeexecutor.ExecutionRequest,
	binding LocalTargetBinding,
	authority SelectedPaaSWorkloadAuthority,
) (selectedPaaSValidatedRequest, error) {
	target, health, deployment, descriptor, err := validateVaultwardenSelectedPaaSRequest(request, binding, authority)
	if err != nil {
		return selectedPaaSValidatedRequest{}, err
	}
	return selectedPaaSValidatedRequest{
		target: target, health: health, deployment: deployment,
		validateObservation: func(observation SelectedPaaSWorkloadObservation) error {
			return validateVaultwardenObservation(observation, deployment, descriptor)
		},
	}, nil
}

func validateVaultwardenSelectedPaaSRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority VaultwardenWorkloadAuthority) (runtimeexecutor.RuntimeTarget, []runtimeexecutor.HealthTarget, SelectedPaaSWorkloadDeployment, architecturev2renderer.VaultwardenWorkloadBundleDescriptor, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, []runtimeexecutor.HealthTarget(nil)
	emptyDeployment := SelectedPaaSWorkloadDeployment{}
	emptyDescriptor := architecturev2renderer.VaultwardenWorkloadBundleDescriptor{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) == 0 || len(request.AccessBindings) != 0 {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("Vaultwarden selected-PaaS executor requires exactly one runtime, governed health targets, and no access binding")
	}
	target := request.RuntimeTargets[0]
	artifact, exists := runtimeExecutorArtifactByID(request.Artifacts, firstRuntimeArtifactRef(target.ArtifactRefs))
	if !exists {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("Vaultwarden selected-PaaS workload artifact is absent")
	}
	if target.OwnerKind != "module" || target.OwnerRef != vaultwardenModuleRef || target.OwnerContractHash != authority.ModuleContractHash ||
		target.ProviderRef != vaultwardenProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != vaultwardenModuleRef || target.ModuleContractHash != authority.ModuleContractHash ||
		target.UnitRef != vaultwardenUnitRef || target.UnitContractHash != authority.UnitContractHash ||
		target.RuntimeKind != "container" || target.RuntimeDelivery != "selected-paas" || target.RuntimeEngine != "docker" ||
		target.WorkloadRef != vaultwardenWorkloadRef || target.ImageRef != vaultwardenImageRef || target.ImageDigest != vaultwardenImageDigest ||
		target.InstanceRef != vaultwardenInstancePrefix+binding.NodeRef || target.ExecutionChannelRef != binding.ExecutionChannelRef ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) ||
		len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		!slices.Equal(target.ArtifactRefs, []string{artifact.ID}) {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("runtime target is not the exact bound Vaultwarden selected-PaaS contract")
	}
	moduleHealthIndex := -1
	for index, health := range request.HealthTargets {
		if health.TargetKind == "module" {
			if moduleHealthIndex >= 0 || health.RequirementID != vaultwardenHealthID ||
				health.RuntimeRequirementID != "" || health.SourceRef != vaultwardenHealthRef ||
				health.ContractHash != authority.HealthContractHash || health.Phase != "continuous" ||
				health.Kind != "http" || health.TargetRef != vaultwardenModuleRef || health.Probe != nil ||
				health.RouteRef != "" || health.BackendPoolRef != "" ||
				!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
				return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("health target is not the exact Vaultwarden HTTP postcondition")
			}
			moduleHealthIndex = index
			continue
		}
		if err := validateVaultwardenRouteHealthTarget(health, target); err != nil {
			return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, err
		}
	}
	if moduleHealthIndex < 0 {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("Vaultwarden request has no exact module health target")
	}
	adapterArtifacts, err := validateSelectedPaaSRuntimeAdapter(target.RuntimeAdapter, request.Artifacts, authority.RuntimeAdapter)
	if err != nil {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, err
	}
	if artifact.ID != vaultwardenArtifactPrefix+target.InstanceRef || artifact.Kind != "native-config" ||
		artifact.Format != "json" || artifact.Mode != "0640" || artifact.OwnerKind != "render-instance" ||
		artifact.OwnerRef != target.InstanceRef || artifact.OwnerContractHash != authority.UnitContractHash ||
		artifact.ProviderRef != vaultwardenProviderRef || artifact.ProviderContractHash != authority.ProviderContractHash ||
		artifact.ModuleRef != vaultwardenModuleRef || artifact.ModuleContractHash != authority.ModuleContractHash ||
		artifact.UnitRef != vaultwardenUnitRef || artifact.UnitContractHash != authority.UnitContractHash ||
		artifact.InstanceRef != target.InstanceRef || artifact.OutputRef != vaultwardenOutputRef ||
		!slices.Equal(artifact.SiteRefs, target.SiteRefs) || !slices.Equal(artifact.NodeRefs, target.NodeRefs) ||
		len(artifact.Content) == 0 || len(artifact.Content) > vaultwardenMaxBytes {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("artifact is not the exact target-bound Vaultwarden workload bundle")
	}
	sum := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("Vaultwarden artifact digest does not match immutable content")
	}
	descriptor, err := architecturev2renderer.ParseVaultwardenWorkloadBundle(artifact.Content)
	if err != nil {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, fmt.Errorf("validate closed Vaultwarden workload bundle: %w", err)
	}
	if descriptor.WorkloadRef != target.WorkloadRef || descriptor.ModuleRef != target.ModuleRef ||
		descriptor.SiteRef != binding.SiteRef || descriptor.NodeRef != binding.NodeRef ||
		descriptor.InstanceRef != target.InstanceRef {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("Vaultwarden workload bundle target differs from authorized runtime target")
	}
	deployment := SelectedPaaSWorkloadDeployment{
		WorkloadRef: descriptor.WorkloadRef, ModuleRef: descriptor.ModuleRef, UnitRef: target.UnitRef,
		Release: descriptor.Release, SiteRef: descriptor.SiteRef, NodeRef: descriptor.NodeRef,
		InstanceRef: descriptor.InstanceRef, ExecutionChannelRef: binding.ExecutionChannelRef,
		ArtifactRef: artifact.ID, ArtifactDigest: artifact.Digest, Bundle: append([]byte(nil), artifact.Content...),
		RuntimeAdapter: *target.RuntimeAdapter, AdapterArtifacts: adapterArtifacts,
	}
	return target, append([]runtimeexecutor.HealthTarget(nil), request.HealthTargets...), deployment, descriptor, nil
}

func validateVaultwardenRouteHealthTarget(health runtimeexecutor.HealthTarget, target runtimeexecutor.RuntimeTarget) error {
	probe := health.Probe
	if health.TargetKind != "route" || health.RuntimeRequirementID != target.RequirementID ||
		health.TargetRef == "" || health.RouteRef != health.TargetRef || health.BackendPoolRef == "" ||
		health.SourceRef != vaultwardenHealthID || health.Phase != "post-apply" || health.Kind != "http" ||
		probe == nil || probe.Protocol != "http" || probe.Port != 80 || probe.TimeoutSeconds != 10 ||
		probe.Method != "GET" || probe.FollowRedirects || probe.Path != "/alive" ||
		!slices.Equal(probe.ExpectedStatuses, []int{200}) ||
		!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return errors.New("route health target is not the exact runtime-owned Vaultwarden backend probe")
	}
	return nil
}

func validateVaultwardenObservation(observation SelectedPaaSWorkloadObservation, deployment SelectedPaaSWorkloadDeployment, descriptor architecturev2renderer.VaultwardenWorkloadBundleDescriptor) error {
	if observation.WorkloadRef != deployment.WorkloadRef || observation.Release != deployment.Release ||
		observation.InstanceRef != deployment.InstanceRef || observation.ArtifactDigest != deployment.ArtifactDigest ||
		observation.Status != "running" || observation.Route.ServiceRef != "vault" ||
		observation.Route.Protocol != "http" || observation.Route.Port != 80 ||
		observation.Route.Method != "GET" || observation.Route.Path != "/alive" ||
		observation.Route.Status != "healthy" || observation.Route.HTTPStatus != 200 ||
		len(observation.Components) != len(descriptor.Components) {
		return errors.New("selected-PaaS observation does not prove the exact running Vaultwarden workload and route")
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
