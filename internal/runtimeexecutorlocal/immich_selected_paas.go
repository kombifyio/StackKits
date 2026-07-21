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
// Product registration remains absent until a real authenticated operations
// implementation is configured by the owning control plane.
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
		!validCoreHostBootstrapDigest(e.authority.UnitContractHash) || !validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
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
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied, ObservationRef: "runtime-observation://selected-paas/" + target.InstanceRef, ObservationDigest: digest}},
		Health:  []runtimeexecutor.HealthOutcome{{RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy, ObservationRef: "health-observation://selected-paas/" + target.InstanceRef, ObservationDigest: digest}},
	}, nil
}

func validateImmichSelectedPaaSRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority ImmichWorkloadAuthority) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, SelectedPaaSWorkloadDeployment, architecturev2renderer.ImmichWorkloadBundleDescriptor, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	emptyDeployment, emptyDescriptor := SelectedPaaSWorkloadDeployment{}, architecturev2renderer.ImmichWorkloadBundleDescriptor{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.Artifacts) != 1 || len(request.AccessBindings) != 0 {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("Immich selected-PaaS executor requires exactly one runtime, one health target, one artifact, and no access binding")
	}
	target, health, artifact := request.RuntimeTargets[0], request.HealthTargets[0], request.Artifacts[0]
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
	if health.RequirementID != immichWorkloadHealthID || health.SourceRef != immichWorkloadHealthRef || health.ContractHash != authority.HealthContractHash ||
		health.Phase != "continuous" || health.Kind != "http" || health.TargetKind != "module" || health.TargetRef != immichWorkloadModuleRef ||
		health.RouteRef != "" || health.BackendPoolRef != "" || !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, emptyDeployment, emptyDescriptor, errors.New("health target is not the exact Immich HTTP postcondition")
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
	}
	return target, health, deployment, descriptor, nil
}

func validateImmichSelectedPaaSObservation(observation SelectedPaaSWorkloadObservation, deployment SelectedPaaSWorkloadDeployment, descriptor architecturev2renderer.ImmichWorkloadBundleDescriptor) error {
	if observation.WorkloadRef != deployment.WorkloadRef || observation.Release != deployment.Release || observation.InstanceRef != deployment.InstanceRef || observation.ArtifactDigest != deployment.ArtifactDigest || observation.Status != "running" ||
		observation.Route.ServiceRef != "photos" || observation.Route.Status != "healthy" || observation.Route.HTTPStatus != 200 || len(observation.Components) != len(descriptor.Components) {
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
	return input
}
