package nativehost

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
	immichLiteWorkloadModuleRef  = "stackkits-immich-lite-runtime"
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

	immichLiteWorkloadProviderRef    = "stackkits-immich-lite"
	immichLiteWorkloadArtifactPrefix = "immich-lite-workload-bundle-instance-"
	immichLiteWorkloadOutputRef      = "workloads/immich-lite/bundle.json"
	immichLiteWorkloadHealthID       = "module-stackkits-immich-lite-runtime-immich-http"
)

// immichWorkloadVariant is the exact catalog identity of one Photos Immich
// alternative. Standard and Lite share the Immich v2.7.0 server image, unit,
// route probe, and bundle schema; they differ only in their catalog provider,
// module, generated artifact, and module Health identities.
type immichWorkloadVariant struct {
	providerRef, moduleRef, artifactPrefix, outputRef, healthID string
}

var (
	immichStandardWorkloadVariant = immichWorkloadVariant{
		providerRef: immichWorkloadProviderRef, moduleRef: immichWorkloadModuleRef,
		artifactPrefix: immichWorkloadArtifactPrefix, outputRef: immichWorkloadOutputRef, healthID: immichWorkloadHealthID,
	}
	immichLiteWorkloadVariant = immichWorkloadVariant{
		providerRef: immichLiteWorkloadProviderRef, moduleRef: immichLiteWorkloadModuleRef,
		artifactPrefix: immichLiteWorkloadArtifactPrefix, outputRef: immichLiteWorkloadOutputRef, healthID: immichLiteWorkloadHealthID,
	}
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
	return newImmichVariantSelectedPaaSExecutor(immichStandardWorkloadVariant, identity, binding, authority, operations)
}

// NewImmichLiteSelectedPaaSExecutor consumes only the exact generated Immich
// Lite bundle (the machine-learning-free Photos alternative) under the same
// selected-PaaS custody as the standard Immich executor.
func NewImmichLiteSelectedPaaSExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority ImmichWorkloadAuthority, operations SelectedPaaSWorkloadOperations) *ImmichSelectedPaaSExecutor {
	return newImmichVariantSelectedPaaSExecutor(immichLiteWorkloadVariant, identity, binding, authority, operations)
}

func newImmichVariantSelectedPaaSExecutor(variant immichWorkloadVariant, identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority ImmichWorkloadAuthority, operations SelectedPaaSWorkloadOperations) *ImmichSelectedPaaSExecutor {
	validate := func(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority SelectedPaaSWorkloadAuthority) (selectedPaaSValidatedRequest, error) {
		return validateImmichSelectedPaaSCoreRequest(variant, request, binding, authority)
	}
	return &ImmichSelectedPaaSExecutor{core: newSelectedPaaSWorkloadExecutor(
		"Immich", identity, binding, authority, operations, validate,
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
	variant immichWorkloadVariant,
	request runtimeexecutor.ExecutionRequest,
	binding LocalTargetBinding,
	authority SelectedPaaSWorkloadAuthority,
) (selectedPaaSValidatedRequest, error) {
	target, health, deployment, descriptor, err := validateImmichSelectedPaaSRequest(
		variant, request, binding, authority,
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

func validateImmichSelectedPaaSRequest(variant immichWorkloadVariant, request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority ImmichWorkloadAuthority) (runtimeexecutor.RuntimeTarget, []runtimeexecutor.HealthTarget, SelectedPaaSWorkloadDeployment, architecturev2renderer.ImmichWorkloadBundleDescriptor, error) {
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
	if target.OwnerKind != "module" || target.OwnerRef != variant.moduleRef || target.OwnerContractHash != authority.ModuleContractHash ||
		target.ProviderRef != variant.providerRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != variant.moduleRef || target.ModuleContractHash != authority.ModuleContractHash || target.UnitRef != immichWorkloadUnitRef || target.UnitContractHash != authority.UnitContractHash ||
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
			if moduleHealthIndex >= 0 || health.RequirementID != variant.healthID || health.RuntimeRequirementID != "" || health.SourceRef != immichWorkloadHealthRef || health.ContractHash != authority.HealthContractHash ||
				health.Phase != "continuous" || health.Kind != "http" || health.TargetRef != variant.moduleRef || health.Probe != nil ||
				health.RouteRef != "" || health.BackendPoolRef != "" || !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
				return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("health target is not the exact Immich HTTP postcondition")
			}
			moduleHealthIndex = index
			continue
		}
		if err := validateImmichRouteHealthTarget(variant, health, target); err != nil {
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
	if artifact.ID != variant.artifactPrefix+target.InstanceRef || artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0640" ||
		artifact.OwnerKind != "render-instance" || artifact.OwnerRef != target.InstanceRef || artifact.OwnerContractHash != authority.UnitContractHash ||
		artifact.ProviderRef != variant.providerRef || artifact.ProviderContractHash != authority.ProviderContractHash || artifact.ModuleRef != variant.moduleRef || artifact.ModuleContractHash != authority.ModuleContractHash ||
		artifact.UnitRef != immichWorkloadUnitRef || artifact.UnitContractHash != authority.UnitContractHash || artifact.InstanceRef != target.InstanceRef || artifact.OutputRef != variant.outputRef ||
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
		Route:          descriptor.Route,
		RuntimeAdapter: *target.RuntimeAdapter, AdapterArtifacts: adapterArtifacts,
	}
	return target, append([]runtimeexecutor.HealthTarget(nil), request.HealthTargets...), deployment, descriptor, nil
}

func validateImmichRouteHealthTarget(variant immichWorkloadVariant, health runtimeexecutor.HealthTarget, target runtimeexecutor.RuntimeTarget) error {
	probe := health.Probe
	if health.TargetKind != "route" || health.RuntimeRequirementID != target.RequirementID || health.TargetRef == "" || health.RouteRef != health.TargetRef || health.BackendPoolRef == "" ||
		health.SourceRef != variant.healthID || health.Phase != "post-apply" || health.Kind != "http" || probe == nil ||
		probe.Protocol != "http" || probe.Port != 2283 || probe.TimeoutSeconds != 10 || probe.Method != "GET" || probe.FollowRedirects || probe.Path != "/api/server/ping" ||
		!slices.Equal(probe.ExpectedStatuses, []int{200}) || !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return errors.New("route health target is not the exact runtime-owned Immich backend probe")
	}
	return nil
}

func validateImmichSelectedPaaSObservation(observation SelectedPaaSWorkloadObservation, deployment SelectedPaaSWorkloadDeployment, descriptor architecturev2renderer.ImmichWorkloadBundleDescriptor) error {
	if observation.WorkloadRef != deployment.WorkloadRef || observation.Release != deployment.Release || observation.InstanceRef != deployment.InstanceRef || observation.ArtifactDigest != deployment.ArtifactDigest ||
		!exactApplicationDeliveryRouteObservation(observation.Route, descriptor.Route) ||
		observation.Route.Method != "GET" || observation.Route.Path != "/api/server/ping" ||
		observation.Route.Status != "healthy" || observation.Route.HTTPStatus != 200 || len(observation.Components) != len(descriptor.Components) {
		return errors.New("selected-PaaS observation does not prove the exact running Immich workload and route")
	}
	degraded := false
	for index, expected := range descriptor.Components {
		actual := observation.Components[index]
		wantStatus, wantHealth := "running", "healthy"
		if expected.Lifecycle == "one-shot" {
			wantStatus, wantHealth = "completed", "completed"
		}
		if actual.ID != expected.ID || actual.ImageDigest != expected.ImageDigest {
			return fmt.Errorf("selected-PaaS observation does not prove exact component %q", expected.ID)
		}
		if actual.Status == wantStatus && actual.Health == wantHealth && actual.Reason == "" {
			continue
		}
		if expected.HealthFailure != "degraded" || actual.Status != "degraded" || actual.Reason == "" || actual.Health == "healthy" || actual.Health == "completed" {
			return fmt.Errorf("selected-PaaS observation does not prove exact component %q", expected.ID)
		}
		degraded = true
	}
	wantStatus := "running"
	if degraded {
		wantStatus = "degraded"
	}
	if observation.Status != wantStatus {
		return errors.New("selected-PaaS observation does not report the Immich workload health impact")
	}
	return nil
}
