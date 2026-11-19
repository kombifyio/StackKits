package generationartifact

import "slices"

type ApplyWorkloadRequirement struct {
	ID              string   `json:"id"`
	ContractHash    string   `json:"contractHash"`
	ProviderRef     string   `json:"providerRef"`
	ModuleRef       string   `json:"moduleRef"`
	RuntimeKind     string   `json:"runtimeKind"`
	RuntimeDelivery string   `json:"runtimeDelivery"`
	ServiceRef      string   `json:"serviceRef"`
	HealthRef       string   `json:"healthRef"`
	SiteRefs        []string `json:"siteRefs"`
	NodeRefs        []string `json:"nodeRefs"`
	InstanceRefs    []string `json:"instanceRefs"`
	EvidenceRefs    []string `json:"evidenceRefs"`
}

type ApplySecretRequirement struct {
	ID             string   `json:"id"`
	SourceKind     string   `json:"sourceKind"`
	SourceRef      string   `json:"sourceRef"`
	SourceInputRef string   `json:"sourceInputRef"`
	OwnerKind      string   `json:"ownerKind"`
	OwnerRef       string   `json:"ownerRef"`
	ModuleRef      string   `json:"moduleRef,omitempty"`
	UnitRef        string   `json:"unitRef,omitempty"`
	InstanceRef    string   `json:"instanceRef,omitempty"`
	InputRef       string   `json:"inputRef"`
	SecretRef      string   `json:"secretRef"`
	SiteRefs       []string `json:"siteRefs"`
	NodeRefs       []string `json:"nodeRefs"`
}

type ApplyDaemonRequirement struct {
	DaemonRef   string `json:"daemonRef"`
	InstanceRef string `json:"instanceRef"`
	Engine      string `json:"engine"`
	SocketPath  string `json:"socketPath"`
}

type ApplyRuntimeRequirement struct {
	ID                      string                          `json:"id"`
	OwnerKind               string                          `json:"ownerKind"`
	OwnerRef                string                          `json:"ownerRef"`
	OwnerVersion            string                          `json:"ownerVersion,omitempty"`
	OwnerContractHash       string                          `json:"ownerContractHash"`
	ProviderRef             string                          `json:"providerRef"`
	ProviderContractHash    string                          `json:"providerContractHash"`
	ModuleRef               string                          `json:"moduleRef,omitempty"`
	ModuleContractHash      string                          `json:"moduleContractHash,omitempty"`
	UnitRef                 string                          `json:"unitRef,omitempty"`
	UnitContractHash        string                          `json:"unitContractHash,omitempty"`
	InstanceRef             string                          `json:"instanceRef"`
	WorkloadRef             string                          `json:"workloadRef,omitempty"`
	RuntimeKind             string                          `json:"runtimeKind"`
	RuntimeDelivery         string                          `json:"runtimeDelivery"`
	RuntimeEngine           string                          `json:"runtimeEngine,omitempty"`
	ImageRef                string                          `json:"imageRef,omitempty"`
	ImageDigest             string                          `json:"imageDigest,omitempty"`
	SiteRefs                []string                        `json:"siteRefs"`
	NodeRefs                []string                        `json:"nodeRefs"`
	HealthGateRefs          []string                        `json:"healthGateRefs,omitempty"`
	EvidenceGateRefs        []string                        `json:"evidenceGateRefs,omitempty"`
	DaemonBindings          []ApplyDaemonRequirement        `json:"daemonBindings"`
	ArtifactRefs            []string                        `json:"artifactRefs"`
	RuntimeAdapter          *ApplyRuntimeAdapterRequirement `json:"runtimeAdapter,omitempty"`
	AccessBindingRefs       []string                        `json:"accessBindingRefs,omitempty"`
	BackupTargetBindingRefs []string                        `json:"backupTargetBindingRefs,omitempty"`
}

type ApplyRuntimeAdapterAgentRequirement struct {
	ID                 string   `json:"id"`
	ModuleRef          string   `json:"moduleRef"`
	ModuleVersion      string   `json:"moduleVersion"`
	ModuleContractHash string   `json:"moduleContractHash"`
	ArtifactRefs       []string `json:"artifactRefs"`
}

// ApplyRuntimeAdapterRequirement binds the exact workload-scoped adapter
// selected by the ResolvedPlan. It carries only catalog authority and
// contract-handoff artifact identities; concrete endpoints, credentials,
// transport configuration, leases, and provider lifecycle remain external.
type ApplyRuntimeAdapterRequirement struct {
	ID                   string                                `json:"id"`
	ProviderRef          string                                `json:"providerRef"`
	ProviderVersion      string                                `json:"providerVersion"`
	ProviderContractHash string                                `json:"providerContractHash"`
	ModuleRef            string                                `json:"moduleRef"`
	ModuleVersion        string                                `json:"moduleVersion"`
	ModuleContractHash   string                                `json:"moduleContractHash"`
	ArtifactRefs         []string                              `json:"artifactRefs"`
	Agents               []ApplyRuntimeAdapterAgentRequirement `json:"agents,omitempty"`
	// Capabilities carries the catalog-declared adapter capabilities
	// (foundation/architecture_v2_catalog.cue runtimeAdapter.capabilities).
	// Callers outside the realization graph, such as native application
	// setup, admit a target by a declared capability here instead of by
	// comparing ID against one hardcoded adapter string.
	Capabilities []string `json:"capabilities,omitempty"`
}

// HasCapability reports whether the catalog declared the named capability for
// this exact adapter. It is the only admission seam callers outside the
// realization graph (native application setup) should use; it must never be
// replaced by an adapter ID comparison.
func (r *ApplyRuntimeAdapterRequirement) HasCapability(capability string) bool {
	if r == nil {
		return false
	}
	return slices.Contains(r.Capabilities, capability)
}

// ApplicationSetupLocalAPICapability is the catalog capability that admits
// native `stackkit setup` (application owner bootstrap and household user
// actions) for a runtime adapter. Declared today only for standalone-compose;
// platform adapters (coolify, komodo, dokploy) do not carry it yet.
const ApplicationSetupLocalAPICapability = "application-setup-local-api"

// ApplyAccessBindingRequirement is the exact provider-free projection of one
// externally realized Home access requirement. BindingHash is the upstream
// StackKits authority hash. The shared runtimeexecutor derives an additional
// projection hash when this requirement crosses the adapter boundary.
type ApplyAccessBindingRequirement struct {
	ID                     string   `json:"id"`
	RuntimeRequirementID   string   `json:"runtimeRequirementId"`
	StackID                string   `json:"stackId"`
	SiteRef                string   `json:"siteRef"`
	CapabilityRef          string   `json:"capabilityRef"`
	ContractOwnerRef       string   `json:"contractOwnerRef"`
	CapabilityContractHash string   `json:"capabilityContractHash"`
	TargetNodeRefs         []string `json:"targetNodeRefs"`
	RequirementsHash       string   `json:"requirementsHash"`
	BindingRef             string   `json:"bindingRef"`
	BindingHash            string   `json:"bindingHash"`
	AccessFabricRef        string   `json:"accessFabricRef"`
	StackKitsVersion       string   `json:"stackkitsVersion"`
	CandidateDigest        string   `json:"candidateDigest"`
	SpecHash               string   `json:"specHash"`
	IssuedAt               string   `json:"issuedAt"`
	ValidUntil             string   `json:"validUntil"`
}

// ApplyBackupTargetBindingRequirement is the exact provider-free projection of
// one externally realized Cloud backup-target requirement. It carries opaque
// target and custody refs, never provider or object-storage connection data.
type ApplyBackupTargetBindingRequirement struct {
	ID                     string   `json:"id"`
	RuntimeRequirementID   string   `json:"runtimeRequirementId"`
	StackID                string   `json:"stackId"`
	SiteRef                string   `json:"siteRef"`
	CapabilityRef          string   `json:"capabilityRef"`
	ContractOwnerRef       string   `json:"contractOwnerRef"`
	CapabilityContractHash string   `json:"capabilityContractHash"`
	TargetNodeRefs         []string `json:"targetNodeRefs"`
	RequirementsHash       string   `json:"requirementsHash"`
	BindingRef             string   `json:"bindingRef"`
	BindingHash            string   `json:"bindingHash"`
	BackupTargetRef        string   `json:"backupTargetRef"`
	CustodyAttestationRef  string   `json:"custodyAttestationRef"`
	StackKitsVersion       string   `json:"stackkitsVersion"`
	CandidateDigest        string   `json:"candidateDigest"`
	SpecHash               string   `json:"specHash"`
	IssuedAt               string   `json:"issuedAt"`
	ValidUntil             string   `json:"validUntil"`
}

// ApplyArtifactRequirement retains the exact CUE-owned render-instance
// identity for one generated artifact. Concrete artifact IDs remain
// plan-specific; executor capability selection binds the stable owner and
// contract identity instead of parsing those IDs.
type ApplyArtifactRequirement struct {
	ID                   string   `json:"id"`
	Kind                 string   `json:"kind"`
	Format               string   `json:"format"`
	Mode                 string   `json:"mode"`
	ExecutionClass       string   `json:"executionClass"`
	OwnerKind            string   `json:"ownerKind"`
	OwnerRef             string   `json:"ownerRef"`
	OwnerContractHash    string   `json:"ownerContractHash"`
	ProviderRef          string   `json:"providerRef,omitempty"`
	ProviderContractHash string   `json:"providerContractHash,omitempty"`
	ModuleRef            string   `json:"moduleRef,omitempty"`
	ModuleContractHash   string   `json:"moduleContractHash,omitempty"`
	UnitRef              string   `json:"unitRef,omitempty"`
	UnitContractHash     string   `json:"unitContractHash,omitempty"`
	InstanceRef          string   `json:"instanceRef,omitempty"`
	OutputRef            string   `json:"outputRef,omitempty"`
	SiteRefs             []string `json:"siteRefs"`
	NodeRefs             []string `json:"nodeRefs"`
}

type ApplyProviderOwnerRequirement struct {
	ID               string   `json:"id"`
	Ref              string   `json:"ref"`
	Version          string   `json:"version"`
	Kind             string   `json:"kind"`
	ContractHash     string   `json:"contractHash"`
	SiteRefs         []string `json:"siteRefs"`
	NodeRefs         []string `json:"nodeRefs"`
	EvidenceRefs     []string `json:"evidenceRefs"`
	HealthGateRefs   []string `json:"healthGateRefs"`
	EvidenceGateRefs []string `json:"evidenceGateRefs"`
}

type ApplyHostRequirement struct {
	NodeRef             string `json:"nodeRef"`
	SiteRef             string `json:"siteRef"`
	FailureDomain       string `json:"failureDomain"`
	External            bool   `json:"external"`
	BindingRef          string `json:"bindingRef,omitempty"`
	BindingHash         string `json:"bindingHash,omitempty"`
	ExecutionChannelRef string `json:"executionChannelRef,omitempty"`
}

type ApplyEvidenceRequirement struct {
	ID             string   `json:"id"`
	OwnerKind      string   `json:"ownerKind"`
	OwnerRef       string   `json:"ownerRef"`
	Ref            string   `json:"ref,omitempty"`
	GateRef        string   `json:"gateRef,omitempty"`
	Phase          string   `json:"phase,omitempty"`
	Producer       string   `json:"producer,omitempty"`
	Scenario       string   `json:"scenario,omitempty"`
	HealthGateRefs []string `json:"healthGateRefs"`
	ArtifactRefs   []string `json:"artifactRefs"`
}

type ApplyHealthRequirement struct {
	ID                   string            `json:"id"`
	RuntimeRequirementID string            `json:"runtimeRequirementId,omitempty"`
	SourceRef            string            `json:"sourceRef"`
	ContractHash         string            `json:"contractHash"`
	Phase                string            `json:"phase"`
	Kind                 string            `json:"kind"`
	TargetKind           string            `json:"targetKind"`
	TargetRef            string            `json:"targetRef"`
	RouteRef             string            `json:"routeRef,omitempty"`
	BackendPoolRef       string            `json:"backendPoolRef,omitempty"`
	Probe                *ApplyHealthProbe `json:"probe,omitempty"`
	SiteRefs             []string          `json:"siteRefs"`
	NodeRefs             []string          `json:"nodeRefs"`
}

type ApplyHealthProbe struct {
	Protocol         string `json:"protocol"`
	Port             int    `json:"port"`
	TimeoutSeconds   int    `json:"timeoutSeconds"`
	Method           string `json:"method,omitempty"`
	FollowRedirects  bool   `json:"followRedirects,omitempty"`
	Path             string `json:"path,omitempty"`
	ExpectedStatuses []int  `json:"expectedStatuses,omitempty"`
}

func cloneApplyRequirements(source ApplyRequirements) ApplyRequirements {
	result := source
	result.Workloads = append([]ApplyWorkloadRequirement(nil), source.Workloads...)
	for index := range result.Workloads {
		result.Workloads[index].SiteRefs = append([]string(nil), source.Workloads[index].SiteRefs...)
		result.Workloads[index].NodeRefs = append([]string(nil), source.Workloads[index].NodeRefs...)
		result.Workloads[index].InstanceRefs = append([]string(nil), source.Workloads[index].InstanceRefs...)
		result.Workloads[index].EvidenceRefs = append([]string(nil), source.Workloads[index].EvidenceRefs...)
	}
	result.Secrets = append([]ApplySecretRequirement(nil), source.Secrets...)
	for index := range result.Secrets {
		result.Secrets[index].SiteRefs = append([]string(nil), source.Secrets[index].SiteRefs...)
		result.Secrets[index].NodeRefs = append([]string(nil), source.Secrets[index].NodeRefs...)
	}
	result.RuntimeInstances = append([]ApplyRuntimeRequirement(nil), source.RuntimeInstances...)
	for index := range result.RuntimeInstances {
		result.RuntimeInstances[index].SiteRefs = append([]string(nil), source.RuntimeInstances[index].SiteRefs...)
		result.RuntimeInstances[index].NodeRefs = append([]string(nil), source.RuntimeInstances[index].NodeRefs...)
		result.RuntimeInstances[index].HealthGateRefs = append([]string(nil), source.RuntimeInstances[index].HealthGateRefs...)
		result.RuntimeInstances[index].EvidenceGateRefs = append([]string(nil), source.RuntimeInstances[index].EvidenceGateRefs...)
		result.RuntimeInstances[index].DaemonBindings = append([]ApplyDaemonRequirement(nil), source.RuntimeInstances[index].DaemonBindings...)
		result.RuntimeInstances[index].ArtifactRefs = append([]string(nil), source.RuntimeInstances[index].ArtifactRefs...)
		result.RuntimeInstances[index].RuntimeAdapter = cloneApplyRuntimeAdapterRequirement(source.RuntimeInstances[index].RuntimeAdapter)
		result.RuntimeInstances[index].AccessBindingRefs = append([]string(nil), source.RuntimeInstances[index].AccessBindingRefs...)
		result.RuntimeInstances[index].BackupTargetBindingRefs = append([]string(nil), source.RuntimeInstances[index].BackupTargetBindingRefs...)
	}
	result.Artifacts = append([]ApplyArtifactRequirement(nil), source.Artifacts...)
	for index := range result.Artifacts {
		result.Artifacts[index].SiteRefs = append([]string(nil), source.Artifacts[index].SiteRefs...)
		result.Artifacts[index].NodeRefs = append([]string(nil), source.Artifacts[index].NodeRefs...)
	}
	result.Hosts = append([]ApplyHostRequirement(nil), source.Hosts...)
	result.ProviderOwners = append([]ApplyProviderOwnerRequirement(nil), source.ProviderOwners...)
	for index := range result.ProviderOwners {
		result.ProviderOwners[index].SiteRefs = append([]string(nil), source.ProviderOwners[index].SiteRefs...)
		result.ProviderOwners[index].NodeRefs = append([]string(nil), source.ProviderOwners[index].NodeRefs...)
		result.ProviderOwners[index].EvidenceRefs = append([]string(nil), source.ProviderOwners[index].EvidenceRefs...)
		result.ProviderOwners[index].HealthGateRefs = append([]string(nil), source.ProviderOwners[index].HealthGateRefs...)
		result.ProviderOwners[index].EvidenceGateRefs = append([]string(nil), source.ProviderOwners[index].EvidenceGateRefs...)
	}
	result.AccessBindings = append([]ApplyAccessBindingRequirement(nil), source.AccessBindings...)
	for index := range result.AccessBindings {
		result.AccessBindings[index].TargetNodeRefs = append([]string(nil), source.AccessBindings[index].TargetNodeRefs...)
	}
	result.BackupTargetBindings = append([]ApplyBackupTargetBindingRequirement(nil), source.BackupTargetBindings...)
	for index := range result.BackupTargetBindings {
		result.BackupTargetBindings[index].TargetNodeRefs = append([]string(nil), source.BackupTargetBindings[index].TargetNodeRefs...)
	}
	result.EvidenceRequirements = append([]ApplyEvidenceRequirement(nil), source.EvidenceRequirements...)
	for index := range result.EvidenceRequirements {
		result.EvidenceRequirements[index].HealthGateRefs = append([]string(nil), source.EvidenceRequirements[index].HealthGateRefs...)
		result.EvidenceRequirements[index].ArtifactRefs = append([]string(nil), source.EvidenceRequirements[index].ArtifactRefs...)
	}
	result.HealthRequirements = append([]ApplyHealthRequirement(nil), source.HealthRequirements...)
	for index := range result.HealthRequirements {
		result.HealthRequirements[index].SiteRefs = append([]string(nil), source.HealthRequirements[index].SiteRefs...)
		result.HealthRequirements[index].NodeRefs = append([]string(nil), source.HealthRequirements[index].NodeRefs...)
		result.HealthRequirements[index].Probe = cloneApplyHealthProbe(source.HealthRequirements[index].Probe)
	}
	result.ExecutionScope = source.ExecutionScope.clone()
	return result
}

func cloneApplyRuntimeAdapterRequirement(source *ApplyRuntimeAdapterRequirement) *ApplyRuntimeAdapterRequirement {
	if source == nil {
		return nil
	}
	result := *source
	result.ArtifactRefs = append([]string(nil), source.ArtifactRefs...)
	result.Capabilities = append([]string(nil), source.Capabilities...)
	result.Agents = append([]ApplyRuntimeAdapterAgentRequirement(nil), source.Agents...)
	for index := range result.Agents {
		result.Agents[index].ArtifactRefs = append([]string(nil), source.Agents[index].ArtifactRefs...)
	}
	return &result
}
