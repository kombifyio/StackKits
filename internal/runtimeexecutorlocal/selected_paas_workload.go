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

// SelectedPaaSWorkloadAuthority is catalog authority fixed by product-owned
// adapter registration. Workload request data can never supply these hashes.
type SelectedPaaSWorkloadAuthority struct {
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

// SelectedPaaSRuntimeAdapterAuthority identifies the one adapter
// implementation an Application Kit is allowed to call.
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
// an already selected PaaS integration. Bundle contains only a validated
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
	Route               architecturev2renderer.ApplicationDeliveryRouteDescriptor
	RuntimeAdapter      runtimeexecutor.RuntimeAdapterBinding
	AdapterArtifacts    []runtimeexecutor.Artifact
}

type SelectedPaaSApplyReceipt struct {
	InstanceRef    string `json:"instanceRef"`
	ArtifactDigest string `json:"artifactDigest"`
	Status         string `json:"status"`
}

type SelectedPaaSComponentObservation struct {
	ID          string `json:"id"`
	ImageDigest string `json:"imageDigest"`
	Status      string `json:"status"`
	Health      string `json:"health"`
}

// SelectedPaaSRouteObservation is the provider-neutral service readback.
type SelectedPaaSRouteObservation struct {
	RouteRef              string `json:"routeRef"`
	ServiceRef            string `json:"serviceRef"`
	ModuleRef             string `json:"moduleRef"`
	Exposure              string `json:"exposure"`
	Protocol              string `json:"protocol"`
	UpstreamProtocol      string `json:"upstreamProtocol"`
	HealthGateRef         string `json:"healthGateRef"`
	BackendPoolRef        string `json:"backendPoolRef"`
	Host                  string `json:"host,omitempty"`
	RoutePath             string `json:"routePath"`
	Port                  int    `json:"port"`
	TargetPort            int    `json:"targetPort"`
	TLSRequired           bool   `json:"tlsRequired"`
	TLSMode               string `json:"tlsMode"`
	TLSMinVersion         string `json:"tlsMinVersion,omitempty"`
	TLSProfileRef         string `json:"tlsProfileRef,omitempty"`
	TLSIssuerRef          string `json:"tlsIssuerRef,omitempty"`
	TLSOwnerCapabilityRef string `json:"tlsOwnerCapabilityRef,omitempty"`
	Method                string `json:"method"`
	Path                  string `json:"path"`
	Status                string `json:"status"`
	HTTPStatus            int    `json:"httpStatus"`
}

func exactApplicationDeliveryRouteObservation(
	actual SelectedPaaSRouteObservation,
	expected architecturev2renderer.ApplicationDeliveryRouteDescriptor,
) bool {
	return actual.RouteRef == expected.ID && actual.ServiceRef == expected.ServiceRef &&
		actual.ModuleRef == expected.ModuleRef &&
		actual.Exposure == expected.Exposure && actual.Protocol == expected.Protocol &&
		actual.UpstreamProtocol == expected.UpstreamProtocol &&
		actual.HealthGateRef == expected.HealthGateRef &&
		actual.BackendPoolRef == expected.BackendPoolRef &&
		actual.Host == expected.Host && actual.RoutePath == expected.Path &&
		actual.Port == expected.Port && actual.TargetPort == expected.TargetPort &&
		actual.TLSRequired == expected.TLSRequired && actual.TLSMode == expected.TLSMode &&
		actual.TLSMinVersion == expected.TLSMinVersion &&
		actual.TLSProfileRef == expected.TLSProfileRef &&
		actual.TLSIssuerRef == expected.TLSIssuerRef &&
		actual.TLSOwnerCapabilityRef == expected.TLSOwnerCapabilityRef
}

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

// SelectedPaaSWorkloadObservationValidator is an optional extension of an
// operations owner. Existing operations implementations remain source
// compatible while restore verification can require the selected owner's
// semantic status-code and component validation.
type SelectedPaaSWorkloadObservationValidator interface {
	ValidateWorkloadObservation(SelectedPaaSWorkloadDeployment, SelectedPaaSWorkloadObservation) error
}

const (
	jellyfinWorkloadModuleRef      = "stackkits-jellyfin-runtime"
	homeAssistantWorkloadModuleRef = "stackkits-home-assistant-runtime"
)

// ValidateSelectedPaaSWorkloadObservation applies the product-owned semantic
// observation contract after an operations owner has read the runtime. The
// standalone Compose prober deliberately reports the observed HTTP status but
// does not decide whether that status satisfies the selected application
// contract; restore verification must use this boundary rather than trusting
// the generic Status field.
func ValidateSelectedPaaSWorkloadObservation(
	deployment SelectedPaaSWorkloadDeployment,
	observation SelectedPaaSWorkloadObservation,
) error {
	switch deployment.ModuleRef {
	case immichWorkloadModuleRef, immichLiteWorkloadModuleRef:
		descriptor, err := architecturev2renderer.ParseImmichWorkloadBundle(deployment.Bundle)
		if err != nil {
			return fmt.Errorf("validate Immich workload observation contract: %w", err)
		}
		return validateImmichSelectedPaaSObservation(observation, deployment, descriptor)
	case cloudreveModuleRef:
		descriptor, err := architecturev2renderer.ParseCloudreveWorkloadBundle(deployment.Bundle)
		if err != nil {
			return fmt.Errorf("validate Cloudreve workload observation contract: %w", err)
		}
		return validateCloudreveObservation(observation, deployment, descriptor)
	case vaultwardenModuleRef:
		descriptor, err := architecturev2renderer.ParseVaultwardenWorkloadBundle(deployment.Bundle)
		if err != nil {
			return fmt.Errorf("validate Vaultwarden workload observation contract: %w", err)
		}
		return validateVaultwardenObservation(observation, deployment, descriptor)
	case jellyfinWorkloadModuleRef:
		if _, err := architecturev2renderer.ParseJellyfinWorkloadBundle(deployment.Bundle); err != nil {
			return fmt.Errorf("validate Jellyfin workload observation contract: %w", err)
		}
		bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(deployment.Bundle)
		if err != nil {
			return fmt.Errorf("validate Jellyfin workload observation envelope: %w", err)
		}
		return validateStandaloneApplicationObservation(observation, deployment, bundle, 200)
	case homeAssistantWorkloadModuleRef:
		if _, err := architecturev2renderer.ParseHomeAssistantWorkloadBundle(deployment.Bundle); err != nil {
			return fmt.Errorf("validate Home Assistant workload observation contract: %w", err)
		}
		bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(deployment.Bundle)
		if err != nil {
			return fmt.Errorf("validate Home Assistant workload observation envelope: %w", err)
		}
		return validateStandaloneApplicationObservation(observation, deployment, bundle, 200)
	default:
		return errors.New("selected-PaaS workload has no product-owned observation validator")
	}
}

func validateStandaloneApplicationObservation(
	observation SelectedPaaSWorkloadObservation,
	deployment SelectedPaaSWorkloadDeployment,
	bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor,
	expectedHTTPStatus int,
) error {
	if deployment.WorkloadRef != bundle.WorkloadRef || deployment.ModuleRef != bundle.ModuleRef ||
		deployment.Release != bundle.Release || deployment.SiteRef != bundle.SiteRef ||
		deployment.NodeRef != bundle.NodeRef || deployment.InstanceRef != bundle.InstanceRef ||
		deployment.Route != bundle.Route || observation.WorkloadRef != deployment.WorkloadRef ||
		observation.Release != deployment.Release || observation.InstanceRef != deployment.InstanceRef ||
		observation.ArtifactDigest != deployment.ArtifactDigest || observation.Status != "running" ||
		!exactApplicationDeliveryRouteObservation(observation.Route, bundle.Route) ||
		observation.Route.Status != "healthy" || observation.Route.HTTPStatus != expectedHTTPStatus ||
		len(observation.Components) != len(bundle.Components) {
		return errors.New("selected-PaaS observation does not prove the exact running workload and route")
	}
	entry, found := standaloneApplicationComponent(bundle.Components, bundle.EntryComponent)
	if !found || entry.HealthKind != "http" || entry.HealthPath == "" || observation.Route.Method != "GET" || observation.Route.Path != entry.HealthPath {
		return errors.New("selected-PaaS observation does not prove the exact workload health route")
	}
	seen := make(map[string]struct{}, len(observation.Components))
	for _, actual := range observation.Components {
		if _, duplicate := seen[actual.ID]; duplicate {
			return errors.New("selected-PaaS observation repeats a workload component")
		}
		seen[actual.ID] = struct{}{}
		expected, found := standaloneApplicationComponent(bundle.Components, actual.ID)
		if !found || actual.ImageDigest != expected.ImageDigest {
			return errors.New("selected-PaaS observation does not prove the exact workload component image")
		}
		wantStatus, wantHealth := "running", "healthy"
		if expected.Lifecycle == "one-shot" {
			wantStatus, wantHealth = "completed", "completed"
		}
		if actual.Status != wantStatus || actual.Health != wantHealth {
			return errors.New("selected-PaaS observation does not prove the exact workload component state")
		}
	}
	return nil
}

func standaloneApplicationComponent(
	components []architecturev2renderer.ApplicationDeliveryComponentDescriptor,
	id string,
) (architecturev2renderer.ApplicationDeliveryComponentDescriptor, bool) {
	index := slices.IndexFunc(components, func(component architecturev2renderer.ApplicationDeliveryComponentDescriptor) bool {
		return component.ID == id
	})
	if index < 0 {
		return architecturev2renderer.ApplicationDeliveryComponentDescriptor{}, false
	}
	return components[index], true
}

type selectedPaaSValidatedRequest struct {
	target              runtimeexecutor.RuntimeTarget
	health              []runtimeexecutor.HealthTarget
	deployment          SelectedPaaSWorkloadDeployment
	validateObservation func(SelectedPaaSWorkloadObservation) error
}

type selectedPaaSRequestValidator func(
	runtimeexecutor.ExecutionRequest,
	LocalTargetBinding,
	SelectedPaaSWorkloadAuthority,
) (selectedPaaSValidatedRequest, error)

type selectedPaaSWorkloadExecutor struct {
	productName string
	identity    runtimeexecutor.ExecutorIdentity
	binding     LocalTargetBinding
	authority   SelectedPaaSWorkloadAuthority
	operations  SelectedPaaSWorkloadOperations
	validate    selectedPaaSRequestValidator
}

func newSelectedPaaSWorkloadExecutor(
	productName string,
	identity runtimeexecutor.ExecutorIdentity,
	binding LocalTargetBinding,
	authority SelectedPaaSWorkloadAuthority,
	operations SelectedPaaSWorkloadOperations,
	validate selectedPaaSRequestValidator,
) *selectedPaaSWorkloadExecutor {
	return &selectedPaaSWorkloadExecutor{
		productName: productName, identity: identity, binding: binding,
		authority: authority, operations: operations, validate: validate,
	}
}

func (executor *selectedPaaSWorkloadExecutor) Identity() runtimeexecutor.ExecutorIdentity {
	if executor == nil {
		return runtimeexecutor.ExecutorIdentity{}
	}
	return executor.identity
}

func (executor *selectedPaaSWorkloadExecutor) Execute(
	ctx context.Context,
	request runtimeexecutor.ExecutionRequest,
) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("selected-PaaS workload executor requires a context")
	}
	if executor == nil || executor.operations == nil || executor.validate == nil ||
		strings.TrimSpace(executor.productName) == "" ||
		strings.TrimSpace(executor.binding.SiteRef) == "" ||
		strings.TrimSpace(executor.binding.NodeRef) == "" ||
		strings.TrimSpace(executor.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(executor.authority.ProviderContractHash) ||
		!validCoreHostBootstrapDigest(executor.authority.ModuleContractHash) ||
		!validCoreHostBootstrapDigest(executor.authority.UnitContractHash) ||
		!validCoreHostBootstrapDigest(executor.authority.HealthContractHash) ||
		!validSelectedPaaSRuntimeAdapterAuthority(executor.authority.RuntimeAdapter) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New(
			"selected-PaaS workload executor requires one authenticated target and exact catalog authority",
		)
	}
	validated, err := executor.validate(request, executor.binding, executor.authority)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	receipt, err := executor.operations.ApplyWorkload(
		ctx, defensiveSelectedPaaSDeployment(validated.deployment),
	)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf(
			"apply exact %s selected-PaaS workload bundle: %w", executor.productName, err,
		)
	}
	if receipt.InstanceRef != validated.deployment.InstanceRef ||
		receipt.ArtifactDigest != validated.deployment.ArtifactDigest ||
		receipt.Status != "applied" {
		return runtimeexecutor.ExecutionOutcome{}, errors.New(
			"selected-PaaS apply receipt does not bind the exact target and artifact",
		)
	}
	observation, err := executor.operations.ObserveWorkload(
		ctx, defensiveSelectedPaaSDeployment(validated.deployment),
	)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf(
			"observe exact %s selected-PaaS workload bundle: %w", executor.productName, err,
		)
	}
	observation.Components = append([]SelectedPaaSComponentObservation(nil), observation.Components...)
	sort.Slice(observation.Components, func(i, j int) bool {
		return observation.Components[i].ID < observation.Components[j].ID
	})
	if err := validated.validateObservation(observation); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evidence, err := json.Marshal(struct {
		Apply       SelectedPaaSApplyReceipt        `json:"apply"`
		Observation SelectedPaaSWorkloadObservation `json:"observation"`
	}{Apply: receipt, Observation: observation})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf(
			"marshal %s selected-PaaS evidence: %w", executor.productName, err,
		)
	}
	digestBytes := sha256.Sum256(evidence)
	digest := "sha256:" + hex.EncodeToString(digestBytes[:])
	healthOutcomes := make([]runtimeexecutor.HealthOutcome, len(validated.health))
	for index, requirement := range validated.health {
		healthOutcomes[index] = runtimeexecutor.HealthOutcome{
			RequirementID: requirement.RequirementID,
			TargetRef:     requirement.TargetRef,
			Status:        runtimeexecutor.HealthStatusHealthy,
			ObservationRef: "health-observation://selected-paas/" +
				validated.target.InstanceRef + "/" + requirement.RequirementID,
			ObservationDigest: digest,
		}
	}
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{
			RequirementID: validated.target.RequirementID,
			InstanceRef:   validated.target.InstanceRef,
			Status:        runtimeexecutor.RuntimeStatusApplied,
			ObservationRef: "runtime-observation://selected-paas/" +
				validated.target.InstanceRef,
			ObservationDigest: digest,
		}},
		Health: healthOutcomes,
	}, nil
}

func validSelectedPaaSRuntimeAdapterAuthority(authority SelectedPaaSRuntimeAdapterAuthority) bool {
	if strings.TrimSpace(authority.ID) == "" || strings.TrimSpace(authority.ProviderRef) == "" ||
		strings.TrimSpace(authority.ProviderVersion) == "" || strings.TrimSpace(authority.ModuleRef) == "" ||
		strings.TrimSpace(authority.ModuleVersion) == "" ||
		!validCoreHostBootstrapDigest(authority.ProviderContractHash) ||
		!validCoreHostBootstrapDigest(authority.ModuleContractHash) {
		return false
	}
	seen := map[string]struct{}{}
	for _, agent := range authority.Agents {
		if strings.TrimSpace(agent.ID) == "" || strings.TrimSpace(agent.ModuleRef) == "" ||
			strings.TrimSpace(agent.ModuleVersion) == "" ||
			!validCoreHostBootstrapDigest(agent.ModuleContractHash) {
			return false
		}
		if _, duplicate := seen[agent.ID]; duplicate {
			return false
		}
		seen[agent.ID] = struct{}{}
	}
	return true
}

func validateSelectedPaaSRuntimeAdapter(
	binding *runtimeexecutor.RuntimeAdapterBinding,
	artifacts []runtimeexecutor.Artifact,
	authority SelectedPaaSRuntimeAdapterAuthority,
) ([]runtimeexecutor.Artifact, error) {
	if binding == nil || binding.ID != authority.ID ||
		binding.ProviderRef != authority.ProviderRef ||
		binding.ProviderVersion != authority.ProviderVersion ||
		binding.ProviderContractHash != authority.ProviderContractHash ||
		binding.ModuleRef != authority.ModuleRef ||
		binding.ModuleVersion != authority.ModuleVersion ||
		binding.ModuleContractHash != authority.ModuleContractHash ||
		len(binding.Agents) != len(authority.Agents) {
		return nil, errors.New(
			"runtime adapter does not match the service-owned selected-PaaS authority",
		)
	}
	result := make([]runtimeexecutor.Artifact, 0, len(binding.ArtifactRefs)+len(binding.Agents))
	if err := appendSelectedPaaSAdapterArtifacts(
		&result, artifacts, binding.ProviderRef, binding.ProviderContractHash,
		binding.ModuleRef, binding.ModuleContractHash, binding.ArtifactRefs,
	); err != nil {
		return nil, err
	}
	for index, agent := range binding.Agents {
		expected := authority.Agents[index]
		if agent.ID != expected.ID || agent.ModuleRef != expected.ModuleRef ||
			agent.ModuleVersion != expected.ModuleVersion ||
			agent.ModuleContractHash != expected.ModuleContractHash {
			return nil, errors.New(
				"runtime adapter agent does not match the service-owned selected-PaaS authority",
			)
		}
		if err := appendSelectedPaaSAdapterArtifacts(
			&result, artifacts, binding.ProviderRef, binding.ProviderContractHash,
			agent.ModuleRef, agent.ModuleContractHash, agent.ArtifactRefs,
		); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func appendSelectedPaaSAdapterArtifacts(
	result *[]runtimeexecutor.Artifact,
	artifacts []runtimeexecutor.Artifact,
	providerRef, providerHash, moduleRef, moduleHash string,
	refs []string,
) error {
	for _, ref := range refs {
		artifact, exists := runtimeExecutorArtifactByID(artifacts, ref)
		if !exists ||
			artifact.ExecutionClass != runtimeexecutor.ArtifactExecutionClassContractHandoff ||
			artifact.ProviderRef != providerRef || artifact.ProviderContractHash != providerHash ||
			artifact.ModuleRef != moduleRef || artifact.ModuleContractHash != moduleHash {
			return fmt.Errorf(
				"selected-PaaS adapter artifact %q does not match its exact module authority", ref,
			)
		}
		artifact.SiteRefs = append([]string(nil), artifact.SiteRefs...)
		artifact.NodeRefs = append([]string(nil), artifact.NodeRefs...)
		artifact.Content = append([]byte(nil), artifact.Content...)
		*result = append(*result, artifact)
	}
	return nil
}

func runtimeExecutorArtifactByID(
	artifacts []runtimeexecutor.Artifact,
	id string,
) (runtimeexecutor.Artifact, bool) {
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

func defensiveSelectedPaaSDeployment(
	input SelectedPaaSWorkloadDeployment,
) SelectedPaaSWorkloadDeployment {
	input.Bundle = append([]byte(nil), input.Bundle...)
	request := runtimeexecutor.CloneExecutionRequest(runtimeexecutor.ExecutionRequest{
		RuntimeTargets: []runtimeexecutor.RuntimeTarget{{RuntimeAdapter: &input.RuntimeAdapter}},
		Artifacts:      input.AdapterArtifacts,
	})
	input.RuntimeAdapter = *request.RuntimeTargets[0].RuntimeAdapter
	input.AdapterArtifacts = request.Artifacts
	return input
}
