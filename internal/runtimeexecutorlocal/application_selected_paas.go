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

// SelectedPaaSApplication names one closed application workload realized
// through the shared selected-PaaS seam. Each value is backed by the existing
// renderer parser for that workload; request data can never select it.
type SelectedPaaSApplication string

const (
	SelectedPaaSApplicationGitea         SelectedPaaSApplication = "gitea"
	SelectedPaaSApplicationPaperless     SelectedPaaSApplication = "paperless-ngx"
	SelectedPaaSApplicationJellyfin      SelectedPaaSApplication = "jellyfin"
	SelectedPaaSApplicationHomeAssistant SelectedPaaSApplication = "home-assistant"
)

const (
	selectedPaaSApplicationMaxBytes            = 128 << 10
	selectedPaaSApplicationProbeTimeoutSeconds = 10
)

// selectedPaaSApplicationIdentity is the typed parser projection the shared
// executor binds against the authorized runtime target.
type selectedPaaSApplicationIdentity struct {
	workloadRef, moduleRef, siteRef, nodeRef, instanceRef string
}

// selectedPaaSApplicationSpec adapts one existing closed parser. Every field is
// catalog- or renderer-owned; image, release, component graph, and route stay
// with the parser, which pins them to the compiled CUE projection.
type selectedPaaSApplicationSpec struct {
	name             string
	providerRef      string
	moduleRef        string
	unitRef          string
	workloadRef      string
	artifactRef      string
	outputRef        string
	healthRef        string
	expectedStatuses []int
	rendererContract func() architecturev2renderer.RendererContract
	parse            func([]byte) (selectedPaaSApplicationIdentity, error)
}

func selectedPaaSApplicationSpecFor(application SelectedPaaSApplication) (selectedPaaSApplicationSpec, bool) {
	switch application {
	case SelectedPaaSApplicationGitea:
		return selectedPaaSApplicationSpec{
			name: "Gitea", providerRef: "stackkits-gitea", moduleRef: "stackkits-gitea-runtime",
			unitRef: "gitea", workloadRef: "dev", artifactRef: "gitea-workload-bundle",
			outputRef: "workloads/gitea/bundle.json", healthRef: "gitea-http", expectedStatuses: []int{200},
			rendererContract: architecturev2renderer.GiteaWorkloadBundleRendererContract,
			parse: func(content []byte) (selectedPaaSApplicationIdentity, error) {
				descriptor, err := architecturev2renderer.ParseGiteaWorkloadBundle(content)
				return selectedPaaSApplicationIdentity{workloadRef: descriptor.WorkloadRef, moduleRef: descriptor.ModuleRef, siteRef: descriptor.SiteRef, nodeRef: descriptor.NodeRef, instanceRef: descriptor.InstanceRef}, err
			},
		}, true
	case SelectedPaaSApplicationPaperless:
		return selectedPaaSApplicationSpec{
			name: "Paperless-ngx", providerRef: "stackkits-paperless-ngx", moduleRef: "stackkits-paperless-runtime",
			unitRef: "paperless", workloadRef: "documents", artifactRef: "paperless-workload-bundle",
			outputRef: "workloads/paperless-ngx/bundle.json", healthRef: "paperless-http", expectedStatuses: []int{200, 302},
			rendererContract: architecturev2renderer.PaperlessWorkloadBundleRendererContract,
			parse: func(content []byte) (selectedPaaSApplicationIdentity, error) {
				descriptor, err := architecturev2renderer.ParsePaperlessWorkloadBundle(content)
				return selectedPaaSApplicationIdentity{
					workloadRef: descriptor.WorkloadRef, moduleRef: descriptor.ModuleRef,
					siteRef: descriptor.SiteRef, nodeRef: descriptor.NodeRef, instanceRef: descriptor.InstanceRef,
				}, err
			},
		}, true

	case SelectedPaaSApplicationJellyfin:
		return selectedPaaSApplicationSpec{
			name: "Jellyfin", providerRef: "stackkits-jellyfin", moduleRef: jellyfinWorkloadModuleRef,
			unitRef: "jellyfin", workloadRef: "media", artifactRef: "jellyfin-workload-bundle",
			outputRef: "workloads/jellyfin/bundle.json", healthRef: "jellyfin-http", expectedStatuses: []int{200},
			rendererContract: architecturev2renderer.JellyfinWorkloadBundleRendererContract,
			parse: func(content []byte) (selectedPaaSApplicationIdentity, error) {
				descriptor, err := architecturev2renderer.ParseJellyfinWorkloadBundle(content)
				return selectedPaaSApplicationIdentity{
					workloadRef: descriptor.WorkloadRef, moduleRef: descriptor.ModuleRef,
					siteRef: descriptor.SiteRef, nodeRef: descriptor.NodeRef, instanceRef: descriptor.InstanceRef,
				}, err
			},
		}, true
	case SelectedPaaSApplicationHomeAssistant:
		return selectedPaaSApplicationSpec{
			name: "Home Assistant", providerRef: "stackkits-home-assistant", moduleRef: homeAssistantWorkloadModuleRef,
			unitRef: "home-assistant", workloadRef: "smart-home", artifactRef: "home-assistant-workload-bundle",
			outputRef: "workloads/home-assistant/bundle.json", healthRef: "home-assistant-http",
			// Mirrors CUE home-assistant-http: a fresh install redirects / to
			// onboarding (302) until native owner setup; that is reachable, and
			// usability stays with the separate setup evidence.
			expectedStatuses: []int{200, 302},
			rendererContract: architecturev2renderer.HomeAssistantWorkloadBundleRendererContract,
			parse: func(content []byte) (selectedPaaSApplicationIdentity, error) {
				descriptor, err := architecturev2renderer.ParseHomeAssistantWorkloadBundle(content)
				return selectedPaaSApplicationIdentity{
					workloadRef: descriptor.WorkloadRef, moduleRef: descriptor.ModuleRef,
					siteRef: descriptor.SiteRef, nodeRef: descriptor.NodeRef, instanceRef: descriptor.InstanceRef,
				}, err
			},
		}, true
	default:
		return selectedPaaSApplicationSpec{}, false
	}
}

// SelectedPaaSApplicationRefs is the catalog selector identity of one named
// application, exported so service construction cannot drift from the
// executor's own request contract.
type SelectedPaaSApplicationRefs struct {
	Name        string
	ProviderRef string
	ModuleRef   string
	UnitRef     string
	WorkloadRef string
}

func (application SelectedPaaSApplication) Refs() (SelectedPaaSApplicationRefs, bool) {
	spec, known := selectedPaaSApplicationSpecFor(application)
	if !known {
		return SelectedPaaSApplicationRefs{}, false
	}
	return SelectedPaaSApplicationRefs{
		Name: spec.name, ProviderRef: spec.providerRef, ModuleRef: spec.moduleRef,
		UnitRef: spec.unitRef, WorkloadRef: spec.workloadRef,
	}, true
}

// SelectedPaaSApplicationExecutor realizes one closed application workload
// through the provider-neutral SelectedPaaSWorkloadOperations owner.
type SelectedPaaSApplicationExecutor struct {
	core *selectedPaaSWorkloadExecutor
}

// NewSelectedPaaSApplicationExecutor binds one named application to exact
// channel and catalog authority. An unknown application yields an executor
// that fails closed on every call.
func NewSelectedPaaSApplicationExecutor(
	application SelectedPaaSApplication,
	identity runtimeexecutor.ExecutorIdentity,
	binding LocalTargetBinding,
	authority SelectedPaaSWorkloadAuthority,
	operations SelectedPaaSWorkloadOperations,
) *SelectedPaaSApplicationExecutor {
	spec, known := selectedPaaSApplicationSpecFor(application)
	if !known {
		return &SelectedPaaSApplicationExecutor{}
	}
	return &SelectedPaaSApplicationExecutor{core: newSelectedPaaSWorkloadExecutor(
		spec.name, identity, binding, authority, operations, spec.validate,
	)}
}

func (e *SelectedPaaSApplicationExecutor) Identity() runtimeexecutor.ExecutorIdentity {
	if e == nil || e.core == nil {
		return runtimeexecutor.ExecutorIdentity{}
	}
	return e.core.Identity()
}

func (e *SelectedPaaSApplicationExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if e == nil || e.core == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("selected-PaaS application executor is not initialized")
	}
	return e.core.Execute(ctx, request)
}

func (spec selectedPaaSApplicationSpec) validate(
	request runtimeexecutor.ExecutionRequest,
	binding LocalTargetBinding,
	authority SelectedPaaSWorkloadAuthority,
) (selectedPaaSValidatedRequest, error) {
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) == 0 || len(request.AccessBindings) != 0 {
		return selectedPaaSValidatedRequest{}, fmt.Errorf("%s selected-PaaS executor requires exactly one runtime, governed health targets, and no access binding", spec.name)
	}
	target := request.RuntimeTargets[0]
	artifact, exists := runtimeExecutorArtifactByID(request.Artifacts, firstRuntimeArtifactRef(target.ArtifactRefs))
	if !exists {
		return selectedPaaSValidatedRequest{}, fmt.Errorf("%s selected-PaaS workload artifact is absent", spec.name)
	}
	instanceRef := spec.unitRef + "-node-" + binding.NodeRef
	if target.OwnerKind != "module" || target.OwnerRef != spec.moduleRef || target.OwnerContractHash != authority.ModuleContractHash ||
		target.ProviderRef != spec.providerRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != spec.moduleRef || target.ModuleContractHash != authority.ModuleContractHash ||
		target.UnitRef != spec.unitRef || target.UnitContractHash != authority.UnitContractHash ||
		target.UnitContractHash != spec.rendererContract().ContractHash ||
		target.RuntimeKind != "container" || target.RuntimeDelivery != "selected-paas" || target.RuntimeEngine != "docker" ||
		target.WorkloadRef != spec.workloadRef || target.InstanceRef != instanceRef ||
		target.ExecutionChannelRef != binding.ExecutionChannelRef ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) ||
		len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		len(target.BackupTargetCapabilities) != 0 || len(target.BackupTargetBindingRefs) != 0 ||
		!slices.Equal(target.ArtifactRefs, []string{artifact.ID}) {
		return selectedPaaSValidatedRequest{}, fmt.Errorf("runtime target is not the exact bound %s selected-PaaS contract", spec.name)
	}
	adapterArtifacts, err := validateSelectedPaaSRuntimeAdapter(target.RuntimeAdapter, request.Artifacts, authority.RuntimeAdapter)
	if err != nil {
		return selectedPaaSValidatedRequest{}, err
	}
	if artifact.ID != spec.artifactRef+"-instance-"+instanceRef || artifact.Kind != "native-config" ||
		artifact.Format != "json" || artifact.Mode != "0640" || artifact.OwnerKind != "render-instance" ||
		artifact.OwnerRef != instanceRef || artifact.OwnerContractHash != authority.UnitContractHash ||
		artifact.ProviderRef != spec.providerRef || artifact.ProviderContractHash != authority.ProviderContractHash ||
		artifact.ModuleRef != spec.moduleRef || artifact.ModuleContractHash != authority.ModuleContractHash ||
		artifact.UnitRef != spec.unitRef || artifact.UnitContractHash != authority.UnitContractHash ||
		artifact.InstanceRef != instanceRef || artifact.OutputRef != spec.outputRef ||
		!slices.Equal(artifact.SiteRefs, target.SiteRefs) || !slices.Equal(artifact.NodeRefs, target.NodeRefs) ||
		len(artifact.Content) == 0 || len(artifact.Content) > selectedPaaSApplicationMaxBytes {
		return selectedPaaSValidatedRequest{}, fmt.Errorf("artifact is not the exact target-bound %s workload bundle", spec.name)
	}
	sum := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return selectedPaaSValidatedRequest{}, fmt.Errorf("%s artifact digest does not match immutable content", spec.name)
	}
	identity, err := spec.parse(artifact.Content)
	if err != nil {
		return selectedPaaSValidatedRequest{}, fmt.Errorf("validate closed %s workload bundle: %w", spec.name, err)
	}
	bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(artifact.Content)
	if err != nil {
		return selectedPaaSValidatedRequest{}, fmt.Errorf("validate %s workload delivery envelope: %w", spec.name, err)
	}
	entry, found := standaloneApplicationComponent(bundle.Components, bundle.EntryComponent)
	if identity.workloadRef != target.WorkloadRef || identity.moduleRef != target.ModuleRef ||
		identity.siteRef != binding.SiteRef || identity.nodeRef != binding.NodeRef || identity.instanceRef != instanceRef ||
		bundle.WorkloadRef != identity.workloadRef || bundle.ModuleRef != identity.moduleRef ||
		bundle.SiteRef != identity.siteRef || bundle.NodeRef != identity.nodeRef || bundle.InstanceRef != identity.instanceRef ||
		!found || entry.ID != spec.unitRef || entry.HealthKind != "http" ||
		target.ImageRef != entry.ImageRef || target.ImageDigest != entry.ImageDigest {
		return selectedPaaSValidatedRequest{}, fmt.Errorf("%s workload bundle differs from the authorized runtime target", spec.name)
	}
	if err := spec.validateHealth(request.HealthTargets, target, authority, bundle.Route, entry); err != nil {
		return selectedPaaSValidatedRequest{}, err
	}
	deployment := SelectedPaaSWorkloadDeployment{
		WorkloadRef: bundle.WorkloadRef, ModuleRef: bundle.ModuleRef, UnitRef: target.UnitRef,
		Release: bundle.Release, SiteRef: bundle.SiteRef, NodeRef: bundle.NodeRef,
		InstanceRef: bundle.InstanceRef, ExecutionChannelRef: binding.ExecutionChannelRef,
		ArtifactRef: artifact.ID, ArtifactDigest: artifact.Digest, Bundle: append([]byte(nil), artifact.Content...),
		Route:          bundle.Route,
		RuntimeAdapter: *target.RuntimeAdapter, AdapterArtifacts: adapterArtifacts,
	}
	return selectedPaaSValidatedRequest{
		target: target, health: append([]runtimeexecutor.HealthTarget(nil), request.HealthTargets...), deployment: deployment,
		validateObservation: func(observation SelectedPaaSWorkloadObservation) error {
			return ValidateSelectedPaaSWorkloadObservation(deployment, observation)
		},
	}, nil
}

// validateSelectedPaaSApplicationObservation applies the application's own
// closed parser and HTTP status set to a runtime readback.
func validateSelectedPaaSApplicationObservation(
	application SelectedPaaSApplication,
	deployment SelectedPaaSWorkloadDeployment,
	observation SelectedPaaSWorkloadObservation,
) error {
	spec, known := selectedPaaSApplicationSpecFor(application)
	if !known {
		return errors.New("selected-PaaS application has no product-owned observation validator")
	}
	if _, err := spec.parse(deployment.Bundle); err != nil {
		return fmt.Errorf("validate %s workload observation contract: %w", spec.name, err)
	}
	bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(deployment.Bundle)
	if err != nil {
		return fmt.Errorf("validate %s workload observation envelope: %w", spec.name, err)
	}
	return validateStandaloneApplicationObservation(observation, deployment, bundle, spec.expectedStatuses)
}

// validateHealth admits exactly the module postcondition and, only for a
// routed bundle, the runtime-owned backend probe of the entry component.
func (spec selectedPaaSApplicationSpec) validateHealth(
	health []runtimeexecutor.HealthTarget,
	target runtimeexecutor.RuntimeTarget,
	authority SelectedPaaSWorkloadAuthority,
	route architecturev2renderer.ApplicationDeliveryRouteDescriptor,
	entry architecturev2renderer.ApplicationDeliveryComponentDescriptor,
) error {
	moduleHealthID := "module-" + spec.moduleRef + "-" + spec.healthRef
	moduleHealth := 0
	for _, requirement := range health {
		if requirement.TargetKind == "module" {
			if requirement.RequirementID != moduleHealthID || requirement.RuntimeRequirementID != "" ||
				requirement.SourceRef != spec.healthRef || requirement.ContractHash != authority.HealthContractHash ||
				requirement.Phase != "continuous" || requirement.Kind != "http" || requirement.TargetRef != spec.moduleRef ||
				requirement.Probe != nil || requirement.RouteRef != "" || requirement.BackendPoolRef != "" ||
				!slices.Equal(requirement.SiteRefs, target.SiteRefs) || !slices.Equal(requirement.NodeRefs, target.NodeRefs) {
				return fmt.Errorf("health target is not the exact %s HTTP postcondition", spec.name)
			}
			moduleHealth++
			continue
		}
		probe := requirement.Probe
		if route.ID == "" || requirement.TargetKind != "route" || requirement.RuntimeRequirementID != target.RequirementID ||
			requirement.TargetRef != route.ID || requirement.RouteRef != route.ID ||
			requirement.BackendPoolRef == "" || requirement.BackendPoolRef != route.BackendPoolRef ||
			requirement.SourceRef != moduleHealthID || requirement.Phase != "post-apply" || requirement.Kind != "http" ||
			probe == nil || probe.Protocol != "http" || probe.Port != entry.HealthPort ||
			probe.TimeoutSeconds != selectedPaaSApplicationProbeTimeoutSeconds ||
			probe.Method != "GET" || probe.FollowRedirects || probe.Path != entry.HealthPath ||
			!slices.Equal(probe.ExpectedStatuses, spec.expectedStatuses) ||
			!slices.Equal(requirement.SiteRefs, target.SiteRefs) || !slices.Equal(requirement.NodeRefs, target.NodeRefs) {
			return fmt.Errorf("route health target is not the exact runtime-owned %s backend probe", spec.name)
		}
	}
	if moduleHealth != 1 {
		return fmt.Errorf("%s request requires exactly one module health target", spec.name)
	}
	return nil
}
