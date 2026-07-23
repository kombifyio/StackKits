package generationartifact

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	ApplyExecutionClassExecutable      = "executable"
	ApplyExecutionClassContractHandoff = "contract-handoff"
	ApplyExecutionClassPlan            = "plan"
)

// ApplyRequirements is the immutable, plan-owned input and postcondition set
// for Architecture v2 execution. ApplyEvidenceRequest projects only its
// pre-mutation Host, Secret, and explicit apply-phase evidence facts; runtime,
// workload, provider-owner, and Health requirements are verified from the exact
// executor result. It contains opaque secret references, never secret material.
// Runtime targets are derived from resolved render instances, never from
// generated files or ambient host discovery.
type ApplyRequirements struct {
	Binding              PlanBinding                     `json:"binding"`
	Workloads            []ApplyWorkloadRequirement      `json:"workloads"`
	Secrets              []ApplySecretRequirement        `json:"secrets"`
	RuntimeInstances     []ApplyRuntimeRequirement       `json:"runtimeInstances"`
	Artifacts            []ApplyArtifactRequirement      `json:"artifacts"`
	Hosts                []ApplyHostRequirement          `json:"hosts"`
	ProviderOwners       []ApplyProviderOwnerRequirement `json:"providerOwners"`
	AccessBindings       []ApplyAccessBindingRequirement `json:"accessBindings,omitempty"`
	EvidenceRequirements []ApplyEvidenceRequirement      `json:"evidenceRequirements"`
	HealthRequirements   []ApplyHealthRequirement        `json:"healthRequirements"`
}

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
	ID                   string                          `json:"id"`
	OwnerKind            string                          `json:"ownerKind"`
	OwnerRef             string                          `json:"ownerRef"`
	OwnerVersion         string                          `json:"ownerVersion,omitempty"`
	OwnerContractHash    string                          `json:"ownerContractHash"`
	ProviderRef          string                          `json:"providerRef"`
	ProviderContractHash string                          `json:"providerContractHash"`
	ModuleRef            string                          `json:"moduleRef,omitempty"`
	ModuleContractHash   string                          `json:"moduleContractHash,omitempty"`
	UnitRef              string                          `json:"unitRef,omitempty"`
	UnitContractHash     string                          `json:"unitContractHash,omitempty"`
	InstanceRef          string                          `json:"instanceRef"`
	WorkloadRef          string                          `json:"workloadRef,omitempty"`
	RuntimeKind          string                          `json:"runtimeKind"`
	RuntimeDelivery      string                          `json:"runtimeDelivery"`
	RuntimeEngine        string                          `json:"runtimeEngine,omitempty"`
	ImageRef             string                          `json:"imageRef,omitempty"`
	ImageDigest          string                          `json:"imageDigest,omitempty"`
	SiteRefs             []string                        `json:"siteRefs"`
	NodeRefs             []string                        `json:"nodeRefs"`
	HealthGateRefs       []string                        `json:"healthGateRefs,omitempty"`
	EvidenceGateRefs     []string                        `json:"evidenceGateRefs,omitempty"`
	DaemonBindings       []ApplyDaemonRequirement        `json:"daemonBindings"`
	ArtifactRefs         []string                        `json:"artifactRefs"`
	RuntimeAdapter       *ApplyRuntimeAdapterRequirement `json:"runtimeAdapter,omitempty"`
	AccessBindingRefs    []string                        `json:"accessBindingRefs,omitempty"`
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
}

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

// ApplyRequirements returns a defensive copy. A caller may inspect it to
// collect evidence, but cannot mutate the verified plan's authorization input.
func (p VerifiedPlan) ApplyRequirements() ApplyRequirements {
	return cloneApplyRequirements(p.applyRequirements)
}

func applyRequirementsFromPlan(plan resolvedplan.ResolvedPlan, binding PlanBinding) (ApplyRequirements, error) {
	result := ApplyRequirements{
		Binding: binding, Workloads: []ApplyWorkloadRequirement{}, Secrets: []ApplySecretRequirement{},
		RuntimeInstances: []ApplyRuntimeRequirement{}, Artifacts: []ApplyArtifactRequirement{}, Hosts: []ApplyHostRequirement{}, ProviderOwners: []ApplyProviderOwnerRequirement{},
		AccessBindings: []ApplyAccessBindingRequirement{}, EvidenceRequirements: []ApplyEvidenceRequirement{}, HealthRequirements: []ApplyHealthRequirement{},
	}
	providerContracts, err := parseApplyProviderContracts(plan)
	if err != nil {
		return ApplyRequirements{}, err
	}
	modules, err := parseApplyModules(plan, providerContracts)
	if err != nil {
		return ApplyRequirements{}, err
	}
	workloadByModule, runtimeAdapterByWorkloadModule, err := parseApplyWorkloads(plan, modules, &result)
	if err != nil {
		return ApplyRequirements{}, err
	}
	targetNodes := map[string]struct{}{}
	if err := appendApplyModuleRequirements(modules, workloadByModule, runtimeAdapterByWorkloadModule, targetNodes, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := appendApplyAccessBindingRequirements(plan, modules, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := appendApplyModuleSecretSources(modules, workloadByModule, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := appendApplyArtifactRequirements(plan, binding, modules, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := appendApplyIntentSecretRequirements(plan, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := appendApplyHealthRequirements(plan, modules, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := bindApplyWorkloadHealthRequirements(&result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := appendApplyProviderOwnerRequirements(plan, targetNodes, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := appendApplyHostRequirements(plan, targetNodes, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := appendApplyEvidenceGateRequirements(plan, &result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := validateApplySecretSourceClosure(result.Secrets); err != nil {
		return ApplyRequirements{}, err
	}
	sortApplyRequirements(&result)
	if err := validateApplyProviderOwnerClosure(result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := validateApplyArtifactRuntimeClosure(result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := validateApplyAccessBindingClosure(result); err != nil {
		return ApplyRequirements{}, err
	}
	if err := validateApplyRequirementIDs(result); err != nil {
		return ApplyRequirements{}, err
	}
	return result, nil
}

type applyModule struct {
	id, version, contractHash, providerRef, providerVersion, providerContractHash, executionClass, runtimeKind, runtimeDelivery, runtimeEngine, imageRef, imageDigest string
	runtimeAdapterID, runtimeAdapterAgentID, runtimeAdapterAgentRef                                                                                                   string
	runtimeAdapterAgentRefs                                                                                                                                           []string
	evidenceRefs, provides                                                                                                                                            []string
	siteRefs, nodeRefs                                                                                                                                                []string
	units                                                                                                                                                             []applyUnit
}

type applyProviderContract struct {
	version      string
	contractHash string
}

type applyUnit struct {
	id, contractHash string
	planInputRefs    []string
	secretRefs       map[string]string
	secretBindings   map[string]applySecretInputBinding
	instances        []applyInstance
	daemonBindings   map[string][]ApplyDaemonRequirement
}

type applySecretInputBinding struct {
	capabilityRef string
	key           string
}

type applyInstance struct {
	id, siteRef, nodeRef string
	outputs              []applyInstanceOutput
}

type applyInstanceOutput struct {
	artifactRef, outputRef string
}

func parseApplyProviderContracts(plan resolvedplan.ResolvedPlan) (map[string]applyProviderContract, error) {
	rawProviders, ok := plan["providers"].([]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.providers", "must be an array")
	}
	result := make(map[string]applyProviderContract, len(rawProviders))
	for index, raw := range rawProviders {
		path := fmt.Sprintf("resolvedPlan.providers[%d]", index)
		provider, ok := raw.(map[string]any)
		if !ok {
			return nil, fail(ErrInvalidPlan, path, "must be an object")
		}
		id, err := requiredString(provider, "id", path+".id")
		if err != nil {
			return nil, err
		}
		contractHash, err := requiredString(provider, "contractHash", path+".contractHash")
		if err != nil {
			return nil, err
		}
		version, err := requiredString(provider, "version", path+".version")
		if err != nil {
			return nil, err
		}
		if _, duplicate := result[id]; duplicate {
			return nil, fail(ErrInvalidPlan, path+".id", "duplicate provider %q", id)
		}
		result[id] = applyProviderContract{version: version, contractHash: contractHash}
	}
	return result, nil
}

func parseApplyModules(plan resolvedplan.ResolvedPlan, providerContracts map[string]applyProviderContract) (map[string]applyModule, error) {
	rawModules, ok := plan["modules"].([]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.modules", "must be an array")
	}
	result := make(map[string]applyModule, len(rawModules))
	for index, raw := range rawModules {
		path := fmt.Sprintf("resolvedPlan.modules[%d]", index)
		module, ok := raw.(map[string]any)
		if !ok {
			return nil, fail(ErrInvalidPlan, path, "must be an object")
		}
		id, err := requiredString(module, "id", path+".id")
		if err != nil {
			return nil, err
		}
		providerRef, err := requiredString(module, "providerRef", path+".providerRef")
		if err != nil {
			return nil, err
		}
		contractHash, err := requiredString(module, "contractHash", path+".contractHash")
		if err != nil {
			return nil, err
		}
		version, err := requiredString(module, "version", path+".version")
		if err != nil {
			return nil, err
		}
		runtimeContract, err := requiredObject(module, "runtime", path+".runtime")
		if err != nil {
			return nil, err
		}
		providerContract, exists := providerContracts[providerRef]
		if !exists {
			return nil, fail(ErrInvalidPlan, path+".providerRef", "provider %q is absent", providerRef)
		}
		parsed := applyModule{
			id: id, version: version, contractHash: contractHash, providerRef: providerRef,
			providerVersion: providerContract.version, providerContractHash: providerContract.contractHash,
		}
		parsed.executionClass = ApplyExecutionClassExecutable
		if rawExecution, exists := runtimeContract["execution"]; exists {
			execution, ok := rawExecution.(string)
			if !ok || execution != ApplyExecutionClassExecutable && execution != ApplyExecutionClassContractHandoff {
				return nil, fail(ErrInvalidPlan, path+".runtime.execution", "must be executable or contract-handoff")
			}
			parsed.executionClass = execution
		}
		if parsed.runtimeKind, err = requiredString(runtimeContract, "kind", path+".runtime.kind"); err != nil {
			return nil, err
		}
		if parsed.runtimeDelivery, err = requiredString(runtimeContract, "delivery", path+".runtime.delivery"); err != nil {
			return nil, err
		}
		parsed.runtimeEngine, _ = runtimeContract["engine"].(string)
		if runtimeAdapter, ok := module["runtimeAdapter"].(map[string]any); ok {
			if parsed.runtimeAdapterID, err = requiredString(runtimeAdapter, "id", path+".runtimeAdapter.id"); err != nil {
				return nil, err
			}
			if parsed.runtimeAdapterAgentRefs, err = applyStringList(runtimeAdapter, "agentRefs", path+".runtimeAdapter.agentRefs"); err != nil {
				return nil, err
			}
		}
		if runtimeAdapterAgent, ok := module["runtimeAdapterAgent"].(map[string]any); ok {
			if parsed.runtimeAdapterAgentID, err = requiredString(runtimeAdapterAgent, "id", path+".runtimeAdapterAgent.id"); err != nil {
				return nil, err
			}
			if parsed.runtimeAdapterAgentRef, err = requiredString(runtimeAdapterAgent, "adapterRef", path+".runtimeAdapterAgent.adapterRef"); err != nil {
				return nil, err
			}
		}
		if image, ok := runtimeContract["image"].(map[string]any); ok {
			parsed.imageRef, _ = image["ref"].(string)
			parsed.imageDigest, _ = image["digest"].(string)
		}
		parsed.siteRefs, err = applyStringList(module, "siteRefs", path+".siteRefs")
		if err != nil {
			return nil, err
		}
		parsed.nodeRefs, err = applyStringList(module, "nodeRefs", path+".nodeRefs")
		if err != nil {
			return nil, err
		}
		parsed.evidenceRefs, err = applyRealizationEvidenceRefs(module, path)
		if err != nil {
			return nil, err
		}
		parsed.provides, err = applyStringList(module, "provides", path+".provides")
		if err != nil {
			return nil, err
		}
		parsed.units, err = parseApplyUnits(module, path)
		if err != nil {
			return nil, err
		}
		if _, duplicate := result[id]; duplicate {
			return nil, fail(ErrInvalidPlan, path+".id", "duplicate module %q", id)
		}
		result[id] = parsed
	}
	return result, nil
}

func parseApplyUnits(module map[string]any, modulePath string) ([]applyUnit, error) {
	rawUnits, ok := module["renderUnits"].([]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, modulePath+".renderUnits", "must be an array")
	}
	units := make([]applyUnit, 0, len(rawUnits))
	for index, raw := range rawUnits {
		path := fmt.Sprintf("%s.renderUnits[%d]", modulePath, index)
		unit, ok := raw.(map[string]any)
		if !ok {
			return nil, fail(ErrInvalidPlan, path, "must be an object")
		}
		id, err := requiredString(unit, "id", path+".id")
		if err != nil {
			return nil, err
		}
		contractHash, err := requiredString(unit, "contractHash", path+".contractHash")
		if err != nil {
			return nil, err
		}
		secretRefs, err := applyStringMap(unit, "secretRefs", path+".secretRefs")
		if err != nil {
			return nil, err
		}
		secretBindings, err := parseApplySecretInputBindings(unit, secretRefs, path)
		if err != nil {
			return nil, err
		}
		planInputRefs, err := applyStringList(unit, "planInputRefs", path+".planInputRefs")
		if err != nil {
			return nil, err
		}
		instances, err := parseApplyInstances(unit, path)
		if err != nil {
			return nil, err
		}
		daemons, err := parseApplyDaemonBindings(unit, path)
		if err != nil {
			return nil, err
		}
		units = append(units, applyUnit{id: id, contractHash: contractHash, planInputRefs: planInputRefs, secretRefs: secretRefs, secretBindings: secretBindings, instances: instances, daemonBindings: daemons})
	}
	return units, nil
}

func parseApplySecretInputBindings(unit map[string]any, secretRefs map[string]string, path string) (map[string]applySecretInputBinding, error) {
	raw, ok := unit["secretInputBindings"].(map[string]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, path+".secretInputBindings", "must be an object")
	}
	result := make(map[string]applySecretInputBinding, len(raw))
	for targetRef, rawBinding := range raw {
		bindingPath := path + ".secretInputBindings." + targetRef
		if _, declared := secretRefs[targetRef]; !declared {
			return nil, fail(ErrInvalidPlan, bindingPath, "target is not a materialized secret input")
		}
		binding, ok := rawBinding.(map[string]any)
		if !ok {
			return nil, fail(ErrInvalidPlan, bindingPath, "must be an object")
		}
		source, err := requiredString(binding, "source", bindingPath+".source")
		if err != nil {
			return nil, err
		}
		if source != "capability-secret" {
			return nil, fail(ErrInvalidPlan, bindingPath+".source", "unsupported secret source %q", source)
		}
		capabilityRef, err := requiredString(binding, "capabilityRef", bindingPath+".capabilityRef")
		if err != nil {
			return nil, err
		}
		key, err := requiredString(binding, "key", bindingPath+".key")
		if err != nil {
			return nil, err
		}
		result[targetRef] = applySecretInputBinding{capabilityRef: capabilityRef, key: key}
	}
	return result, nil
}

func parseApplyInstances(unit map[string]any, unitPath string) ([]applyInstance, error) {
	rawInstances, ok := unit["instances"].([]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, unitPath+".instances", "must be an array")
	}
	instances := make([]applyInstance, 0, len(rawInstances))
	for index, raw := range rawInstances {
		path := fmt.Sprintf("%s.instances[%d]", unitPath, index)
		instance, ok := raw.(map[string]any)
		if !ok {
			return nil, fail(ErrInvalidPlan, path, "must be an object")
		}
		id, err := requiredString(instance, "id", path+".id")
		if err != nil {
			return nil, err
		}
		siteRef, _ := instance["siteRef"].(string)
		nodeRef, _ := instance["nodeRef"].(string)
		rawOutputs, ok := instance["outputs"].([]any)
		if !ok {
			return nil, fail(ErrInvalidPlan, path+".outputs", "must be an array")
		}
		outputs := make([]applyInstanceOutput, 0, len(rawOutputs))
		for outputIndex, rawOutput := range rawOutputs {
			outputPath := fmt.Sprintf("%s.outputs[%d]", path, outputIndex)
			output, ok := rawOutput.(map[string]any)
			if !ok {
				return nil, fail(ErrInvalidPlan, outputPath, "must be an object")
			}
			artifactRef, err := requiredString(output, "artifactRef", outputPath+".artifactRef")
			if err != nil {
				return nil, err
			}
			outputRef, err := requiredString(output, "ref", outputPath+".ref")
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, applyInstanceOutput{artifactRef: artifactRef, outputRef: outputRef})
		}
		instances = append(instances, applyInstance{id: id, siteRef: siteRef, nodeRef: nodeRef, outputs: outputs})
	}
	return instances, nil
}

func parseApplyDaemonBindings(unit map[string]any, unitPath string) (map[string][]ApplyDaemonRequirement, error) {
	result := map[string][]ApplyDaemonRequirement{}
	rawBindings, ok := unit["daemonBindings"].([]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, unitPath+".daemonBindings", "must be an array")
	}
	for index, raw := range rawBindings {
		path := fmt.Sprintf("%s.daemonBindings[%d]", unitPath, index)
		binding, ok := raw.(map[string]any)
		if !ok {
			return nil, fail(ErrInvalidPlan, path, "must be an object")
		}
		nodeRef, err := requiredString(binding, "nodeRef", path+".nodeRef")
		if err != nil {
			return nil, err
		}
		requirement := ApplyDaemonRequirement{}
		if requirement.DaemonRef, err = requiredString(binding, "daemonRef", path+".daemonRef"); err != nil {
			return nil, err
		}
		if requirement.InstanceRef, err = requiredString(binding, "instanceRef", path+".instanceRef"); err != nil {
			return nil, err
		}
		if requirement.Engine, err = requiredString(binding, "engine", path+".engine"); err != nil {
			return nil, err
		}
		if requirement.SocketPath, err = requiredString(binding, "socketPath", path+".socketPath"); err != nil {
			return nil, err
		}
		result[nodeRef] = append(result[nodeRef], requirement)
	}
	return result, nil
}

func parseApplyWorkloads(plan resolvedplan.ResolvedPlan, modules map[string]applyModule, result *ApplyRequirements) (map[string]string, map[string]*ApplyRuntimeAdapterRequirement, error) {
	rawWorkloads, ok := plan["workloads"].([]any)
	if !ok {
		return nil, nil, fail(ErrInvalidPlan, "resolvedPlan.workloads", "must be an array")
	}
	workloadByModule := make(map[string]string, len(rawWorkloads))
	runtimeAdapterByModule := make(map[string]*ApplyRuntimeAdapterRequirement, len(rawWorkloads))
	for index, raw := range rawWorkloads {
		path := fmt.Sprintf("resolvedPlan.workloads[%d]", index)
		workload, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, fail(ErrInvalidPlan, path, "must be an object")
		}
		id, err := requiredString(workload, "id", path+".id")
		if err != nil {
			return nil, nil, err
		}
		contractHash, err := requiredString(workload, "contractHash", path+".contractHash")
		if err != nil {
			return nil, nil, err
		}
		alternative, err := requiredObject(workload, "alternative", path+".alternative")
		if err != nil {
			return nil, nil, err
		}
		moduleRef, err := requiredString(alternative, "moduleRef", path+".alternative.moduleRef")
		if err != nil {
			return nil, nil, err
		}
		module, exists := modules[moduleRef]
		if !exists {
			return nil, nil, fail(ErrInvalidPlan, path+".alternative.moduleRef", "module %q is absent", moduleRef)
		}
		runtimeContract, err := requiredObject(alternative, "runtime", path+".alternative.runtime")
		if err != nil {
			return nil, nil, err
		}
		route, err := requiredObject(alternative, "route", path+".alternative.route")
		if err != nil {
			return nil, nil, err
		}
		requirement := ApplyWorkloadRequirement{ID: id, ContractHash: contractHash, ModuleRef: moduleRef, EvidenceRefs: append([]string(nil), module.evidenceRefs...)}
		if requirement.ProviderRef, err = requiredString(alternative, "providerRef", path+".alternative.providerRef"); err != nil {
			return nil, nil, err
		}
		if requirement.RuntimeKind, err = requiredString(runtimeContract, "kind", path+".alternative.runtime.kind"); err != nil {
			return nil, nil, err
		}
		if requirement.RuntimeDelivery, err = requiredString(runtimeContract, "delivery", path+".alternative.runtime.delivery"); err != nil {
			return nil, nil, err
		}
		adapter, err := parseApplyRuntimeAdapter(runtimeContract, path+".alternative.runtime", modules)
		if err != nil {
			return nil, nil, err
		}
		if requirement.RuntimeDelivery == "selected-paas" && adapter == nil {
			return nil, nil, fail(ErrInvalidPlan, path+".alternative.runtime.adapter", "selected-paas workload requires one exact runtime adapter")
		}
		if requirement.RuntimeDelivery != "selected-paas" && adapter != nil {
			return nil, nil, fail(ErrInvalidPlan, path+".alternative.runtime.adapter", "runtime adapter is valid only for selected-paas delivery")
		}
		if requirement.ServiceRef, err = requiredString(route, "serviceRef", path+".alternative.route.serviceRef"); err != nil {
			return nil, nil, err
		}
		if requirement.HealthRef, err = requiredString(route, "healthRef", path+".alternative.route.healthRef"); err != nil {
			return nil, nil, err
		}
		if requirement.SiteRefs, err = applyStringList(workload, "siteRefs", path+".siteRefs"); err != nil {
			return nil, nil, err
		}
		if requirement.NodeRefs, err = applyStringList(workload, "nodeRefs", path+".nodeRefs"); err != nil {
			return nil, nil, err
		}
		for _, unit := range module.units {
			for _, instance := range unit.instances {
				requirement.InstanceRefs = append(requirement.InstanceRefs, instance.id)
			}
		}
		if _, duplicate := workloadByModule[moduleRef]; duplicate {
			return nil, nil, fail(ErrInvalidPlan, path+".alternative.moduleRef", "workload module %q is selected more than once", moduleRef)
		}
		workloadByModule[moduleRef] = id
		runtimeAdapterByModule[moduleRef] = adapter
		result.Workloads = append(result.Workloads, requirement)
		for _, evidenceRef := range requirement.EvidenceRefs {
			result.EvidenceRequirements = append(result.EvidenceRequirements, ApplyEvidenceRequirement{
				ID: "workload/" + id + "/" + evidenceRef, OwnerKind: "workload", OwnerRef: id, Ref: evidenceRef,
			})
		}
	}
	return workloadByModule, runtimeAdapterByModule, nil
}

func parseApplyRuntimeAdapter(runtime map[string]any, path string, modules map[string]applyModule) (*ApplyRuntimeAdapterRequirement, error) {
	raw, exists := runtime["adapter"]
	if !exists {
		return nil, nil
	}
	adapter, ok := raw.(map[string]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, path+".adapter", "must be an object")
	}
	result := &ApplyRuntimeAdapterRequirement{}
	var err error
	if result.ID, err = requiredString(adapter, "id", path+".adapter.id"); err != nil {
		return nil, err
	}
	if result.ProviderRef, err = requiredString(adapter, "providerRef", path+".adapter.providerRef"); err != nil {
		return nil, err
	}
	if result.ProviderVersion, err = requiredString(adapter, "providerVersion", path+".adapter.providerVersion"); err != nil {
		return nil, err
	}
	if result.ProviderContractHash, err = requiredString(adapter, "providerContractHash", path+".adapter.providerContractHash"); err != nil {
		return nil, err
	}
	if result.ModuleRef, err = requiredString(adapter, "moduleRef", path+".adapter.moduleRef"); err != nil {
		return nil, err
	}
	if result.ModuleVersion, err = requiredString(adapter, "moduleVersion", path+".adapter.moduleVersion"); err != nil {
		return nil, err
	}
	if result.ModuleContractHash, err = requiredString(adapter, "moduleContractHash", path+".adapter.moduleContractHash"); err != nil {
		return nil, err
	}
	core, exists := modules[result.ModuleRef]
	if !exists || core.runtimeAdapterID != result.ID || core.providerRef != result.ProviderRef || core.providerVersion != result.ProviderVersion || core.version != result.ModuleVersion ||
		core.providerContractHash != result.ProviderContractHash || core.contractHash != result.ModuleContractHash || core.executionClass != ApplyExecutionClassContractHandoff {
		return nil, fail(ErrInvalidPlan, path+".adapter", "does not match one exact contract-handoff adapter module")
	}
	result.ArtifactRefs = applyModuleArtifactRefs(core)
	if len(result.ArtifactRefs) == 0 {
		return nil, fail(ErrInvalidPlan, path+".adapter.moduleRef", "adapter module has no contract-handoff artifact")
	}
	for _, agentID := range core.runtimeAdapterAgentRefs {
		var matched *applyModule
		for _, moduleID := range sortedApplyMapKeys(modules) {
			candidate := modules[moduleID]
			if candidate.runtimeAdapterAgentID != agentID || candidate.runtimeAdapterAgentRef != result.ID || candidate.providerRef != result.ProviderRef {
				continue
			}
			if matched != nil {
				return nil, fail(ErrInvalidPlan, path+".adapter", "agent %q resolves to multiple modules", agentID)
			}
			copy := candidate
			matched = &copy
		}
		if matched == nil || matched.executionClass != ApplyExecutionClassContractHandoff || matched.providerContractHash != result.ProviderContractHash {
			return nil, fail(ErrInvalidPlan, path+".adapter", "agent %q has no exact contract-handoff module", agentID)
		}
		artifactRefs := applyModuleArtifactRefs(*matched)
		if len(artifactRefs) == 0 {
			return nil, fail(ErrInvalidPlan, path+".adapter", "agent %q has no contract-handoff artifact", agentID)
		}
		result.Agents = append(result.Agents, ApplyRuntimeAdapterAgentRequirement{
			ID: agentID, ModuleRef: matched.id, ModuleVersion: matched.version, ModuleContractHash: matched.contractHash, ArtifactRefs: artifactRefs,
		})
	}
	return result, nil
}

func applyModuleArtifactRefs(module applyModule) []string {
	refs := []string{}
	for _, unit := range module.units {
		for _, instance := range unit.instances {
			refs = append(refs, applyInstanceArtifactRefs(instance.outputs)...)
		}
	}
	sort.Strings(refs)
	return refs
}

func appendApplyModuleRequirements(modules map[string]applyModule, workloadByModule map[string]string, runtimeAdapterByWorkloadModule map[string]*ApplyRuntimeAdapterRequirement, targetNodes map[string]struct{}, result *ApplyRequirements) error {
	for _, moduleID := range sortedApplyMapKeys(modules) {
		module := modules[moduleID]
		for _, nodeRef := range module.nodeRefs {
			targetNodes[nodeRef] = struct{}{}
		}
		if module.executionClass == ApplyExecutionClassExecutable {
			for _, evidenceRef := range module.evidenceRefs {
				result.EvidenceRequirements = append(result.EvidenceRequirements, ApplyEvidenceRequirement{ID: "module/" + moduleID + "/" + evidenceRef, OwnerKind: "module", OwnerRef: moduleID, Ref: evidenceRef})
			}
		}
		for _, unit := range module.units {
			for _, instance := range unit.instances {
				defaultSecretSourceKind, defaultSecretSourceRef := "module", moduleID
				if workloadRef := workloadByModule[moduleID]; workloadRef != "" {
					defaultSecretSourceKind, defaultSecretSourceRef = "workload", workloadRef
				}
				siteRefs := append([]string(nil), module.siteRefs...)
				nodeRefs := append([]string(nil), module.nodeRefs...)
				if instance.siteRef != "" {
					siteRefs = []string{instance.siteRef}
				}
				if instance.nodeRef != "" {
					nodeRefs = []string{instance.nodeRef}
				}
				if module.executionClass == ApplyExecutionClassExecutable {
					runtimeRequirement := ApplyRuntimeRequirement{
						ID: moduleID + "/" + unit.id + "/" + instance.id, OwnerKind: "module", OwnerRef: moduleID, OwnerContractHash: module.contractHash,
						ProviderRef: module.providerRef, ProviderContractHash: module.providerContractHash, ModuleRef: moduleID, ModuleContractHash: module.contractHash,
						UnitRef: unit.id, UnitContractHash: unit.contractHash, InstanceRef: instance.id, WorkloadRef: workloadByModule[moduleID], RuntimeKind: module.runtimeKind,
						RuntimeDelivery: module.runtimeDelivery, RuntimeEngine: module.runtimeEngine, ImageRef: module.imageRef, ImageDigest: module.imageDigest,
						SiteRefs: siteRefs, NodeRefs: nodeRefs, DaemonBindings: append([]ApplyDaemonRequirement(nil), unit.daemonBindings[instance.nodeRef]...),
						ArtifactRefs:   applyInstanceArtifactRefs(instance.outputs),
						RuntimeAdapter: cloneApplyRuntimeAdapterRequirement(runtimeAdapterByWorkloadModule[moduleID]),
					}
					result.RuntimeInstances = append(result.RuntimeInstances, runtimeRequirement)
				}
				if instance.nodeRef != "" {
					targetNodes[instance.nodeRef] = struct{}{}
				}
				for inputRef, secretRef := range unit.secretRefs {
					secretSourceKind, secretSourceRef, secretSourceInputRef := defaultSecretSourceKind, defaultSecretSourceRef, inputRef
					if binding, bound := unit.secretBindings[inputRef]; bound {
						secretSourceKind, secretSourceRef, secretSourceInputRef = "capability", binding.capabilityRef, binding.key
					}
					result.Secrets = append(result.Secrets, ApplySecretRequirement{
						ID: moduleID + "/" + unit.id + "/" + instance.id + "/" + inputRef, OwnerKind: "render-instance", OwnerRef: instance.id,
						SourceKind: secretSourceKind, SourceRef: secretSourceRef, SourceInputRef: secretSourceInputRef,
						ModuleRef: moduleID, UnitRef: unit.id, InstanceRef: instance.id, InputRef: inputRef, SecretRef: secretRef,
						SiteRefs: append([]string(nil), siteRefs...), NodeRefs: append([]string(nil), nodeRefs...),
					})
				}
			}
		}
	}
	return nil
}

func appendApplyModuleSecretSources(modules map[string]applyModule, workloadByModule map[string]string, result *ApplyRequirements) error {
	for _, moduleID := range sortedApplyMapKeys(modules) {
		if workloadByModule[moduleID] != "" {
			continue
		}
		sources := map[string]string{}
		for _, unit := range modules[moduleID].units {
			for inputRef, secretRef := range unit.secretRefs {
				if _, capabilityBound := unit.secretBindings[inputRef]; capabilityBound {
					continue
				}
				if previous, duplicate := sources[inputRef]; duplicate && previous != secretRef {
					return fail(ErrInvalidPlan, "resolvedPlan.modules."+moduleID+".renderUnits", "module secret input %q resolves inconsistently across render units", inputRef)
				}
				sources[inputRef] = secretRef
			}
		}
		for _, inputRef := range sortedApplyMapKeys(sources) {
			result.Secrets = append(result.Secrets, ApplySecretRequirement{
				ID: "source/module/" + moduleID + "/" + inputRef, SourceKind: "module", SourceRef: moduleID, SourceInputRef: inputRef,
				OwnerKind: "module-intent", OwnerRef: moduleID, InputRef: inputRef, SecretRef: sources[inputRef],
			})
		}
	}
	return nil
}

func applyInstanceArtifactRefs(outputs []applyInstanceOutput) []string {
	refs := make([]string, 0, len(outputs))
	for _, output := range outputs {
		refs = append(refs, output.artifactRef)
	}
	return refs
}

func appendApplyArtifactRequirements(plan resolvedplan.ResolvedPlan, binding PlanBinding, modules map[string]applyModule, result *ApplyRequirements) error {
	generation, err := requiredObject(plan, "generation", "resolvedPlan.generation")
	if err != nil {
		return err
	}
	rawArtifacts, ok := generation["artifacts"].([]any)
	if !ok {
		return fail(ErrInvalidPlan, "resolvedPlan.generation.artifacts", "must be an array")
	}
	for index, raw := range rawArtifacts {
		path := fmt.Sprintf("resolvedPlan.generation.artifacts[%d]", index)
		artifact, ok := raw.(map[string]any)
		if !ok {
			return fail(ErrInvalidPlan, path, "must be an object")
		}
		requirement := ApplyArtifactRequirement{ExecutionClass: ApplyExecutionClassPlan, SiteRefs: []string{}, NodeRefs: []string{}}
		if requirement.ID, err = requiredString(artifact, "id", path+".id"); err != nil {
			return err
		}
		if requirement.Kind, err = requiredString(artifact, "kind", path+".kind"); err != nil {
			return err
		}
		if requirement.Format, err = requiredString(artifact, "format", path+".format"); err != nil {
			return err
		}
		if requirement.Mode, err = requiredString(artifact, "mode", path+".mode"); err != nil {
			return err
		}
		owner, err := requiredObject(artifact, "owner", path+".owner")
		if err != nil {
			return err
		}
		if requirement.OwnerKind, err = requiredString(owner, "kind", path+".owner.kind"); err != nil {
			return err
		}
		switch requirement.OwnerKind {
		case "plan":
			requirement.OwnerRef = binding.PlanHash
			requirement.OwnerContractHash = binding.PlanHash
		case "render-instance":
			if requirement.ModuleRef, err = requiredString(owner, "moduleRef", path+".owner.moduleRef"); err != nil {
				return err
			}
			module, exists := modules[requirement.ModuleRef]
			if !exists {
				return fail(ErrInvalidPlan, path+".owner.moduleRef", "module %q is absent", requirement.ModuleRef)
			}
			requirement.ExecutionClass = module.executionClass
			requirement.ProviderRef = module.providerRef
			requirement.ProviderContractHash = module.providerContractHash
			requirement.ModuleContractHash = module.contractHash
			if requirement.UnitRef, err = requiredString(owner, "unitRef", path+".owner.unitRef"); err != nil {
				return err
			}
			if requirement.InstanceRef, err = requiredString(owner, "instanceRef", path+".owner.instanceRef"); err != nil {
				return err
			}
			requirement.OwnerRef = requirement.InstanceRef
			if requirement.OutputRef, err = requiredString(owner, "outputRef", path+".owner.outputRef"); err != nil {
				return err
			}
			unit, instance, output, found := findApplyArtifactOwner(module, requirement.UnitRef, requirement.InstanceRef, requirement.ID)
			if !found || output.outputRef != requirement.OutputRef {
				return fail(ErrInvalidPlan, path+".owner", "artifact owner does not match an exact module/unit/instance output")
			}
			requirement.UnitContractHash = unit.contractHash
			requirement.OwnerContractHash = unit.contractHash
			requirement.SiteRefs = append([]string(nil), module.siteRefs...)
			requirement.NodeRefs = append([]string(nil), module.nodeRefs...)
			if instance.siteRef != "" {
				requirement.SiteRefs = []string{instance.siteRef}
			}
			if instance.nodeRef != "" {
				requirement.NodeRefs = []string{instance.nodeRef}
			}
		default:
			return fail(ErrInvalidPlan, path+".owner.kind", "unsupported artifact owner kind %q", requirement.OwnerKind)
		}
		result.Artifacts = append(result.Artifacts, requirement)
	}
	return nil
}

func findApplyArtifactOwner(module applyModule, unitRef, instanceRef, artifactRef string) (applyUnit, applyInstance, applyInstanceOutput, bool) {
	for _, unit := range module.units {
		if unit.id != unitRef {
			continue
		}
		for _, instance := range unit.instances {
			if instance.id != instanceRef {
				continue
			}
			for _, output := range instance.outputs {
				if output.artifactRef == artifactRef {
					return unit, instance, output, true
				}
			}
		}
	}
	return applyUnit{}, applyInstance{}, applyInstanceOutput{}, false
}

func appendApplyIntentSecretRequirements(plan resolvedplan.ResolvedPlan, result *ApplyRequirements) error {
	for _, collection := range []struct {
		field string
		kind  string
	}{
		{field: "capabilities", kind: "capability"},
		{field: "workloads", kind: "workload"},
	} {
		rawValues, ok := plan[collection.field].([]any)
		if !ok {
			return fail(ErrInvalidPlan, "resolvedPlan."+collection.field, "must be an array")
		}
		for index, raw := range rawValues {
			path := fmt.Sprintf("resolvedPlan.%s[%d]", collection.field, index)
			value, ok := raw.(map[string]any)
			if !ok {
				return fail(ErrInvalidPlan, path, "must be an object")
			}
			id, err := requiredString(value, "id", path+".id")
			if err != nil {
				return err
			}
			if err := appendApplySecretSource(value, path, collection.kind, id, result); err != nil {
				return err
			}
		}
	}
	rawAddons, exists := plan["addons"]
	if !exists {
		return nil
	}
	addons, ok := rawAddons.(map[string]any)
	if !ok {
		return fail(ErrInvalidPlan, "resolvedPlan.addons", "must be an object")
	}
	for addonID, raw := range addons {
		path := "resolvedPlan.addons." + addonID
		addon, ok := raw.(map[string]any)
		if !ok {
			return fail(ErrInvalidPlan, path, "must be an object")
		}
		if err := appendApplySecretSource(addon, path, "addon", addonID, result); err != nil {
			return err
		}
	}
	return nil
}

func appendApplySecretSource(owner map[string]any, path, sourceKind, sourceRef string, result *ApplyRequirements) error {
	raw, exists := owner["secretRefs"]
	if !exists {
		return nil
	}
	secretRefs, err := applyStringMap(map[string]any{"secretRefs": raw}, "secretRefs", path+".secretRefs")
	if err != nil {
		return err
	}
	for inputRef, secretRef := range secretRefs {
		result.Secrets = append(result.Secrets, ApplySecretRequirement{
			ID: "source/" + sourceKind + "/" + sourceRef + "/" + inputRef, SourceKind: sourceKind, SourceRef: sourceRef, SourceInputRef: inputRef,
			OwnerKind: sourceKind + "-intent", OwnerRef: sourceRef, InputRef: inputRef, SecretRef: secretRef,
		})
	}
	return nil
}

func appendApplyProviderOwnerRequirements(plan resolvedplan.ResolvedPlan, targetNodes map[string]struct{}, result *ApplyRequirements) error {
	siteByNode := map[string]string{}
	rawNodes, ok := plan["nodes"].([]any)
	if !ok {
		return fail(ErrInvalidPlan, "resolvedPlan.nodes", "must be an array")
	}
	for index, raw := range rawNodes {
		path := fmt.Sprintf("resolvedPlan.nodes[%d]", index)
		node, ok := raw.(map[string]any)
		if !ok {
			return fail(ErrInvalidPlan, path, "must be an object")
		}
		nodeRef, err := requiredString(node, "id", path+".id")
		if err != nil {
			return err
		}
		siteRef, err := requiredString(node, "siteRef", path+".siteRef")
		if err != nil {
			return err
		}
		enabled, ok := node["enabled"].(bool)
		if !ok {
			return fail(ErrInvalidPlan, path+".enabled", "must be boolean")
		}
		if !enabled {
			continue
		}
		siteByNode[nodeRef] = siteRef
	}
	rawProviders, ok := plan["providers"].([]any)
	if !ok {
		return fail(ErrInvalidPlan, "resolvedPlan.providers", "must be an array")
	}
	for index, raw := range rawProviders {
		path := fmt.Sprintf("resolvedPlan.providers[%d]", index)
		provider, ok := raw.(map[string]any)
		if !ok {
			return fail(ErrInvalidPlan, path, "must be an object")
		}
		providerID, err := requiredString(provider, "id", path+".id")
		if err != nil {
			return err
		}
		providerContractHash, err := requiredString(provider, "contractHash", path+".contractHash")
		if err != nil {
			return err
		}
		providerSiteRefs, err := applyStringList(provider, "siteRefs", path+".siteRefs")
		if err != nil {
			return err
		}
		owner, exists := provider["owner"].(map[string]any)
		if !exists {
			continue
		}
		ownerKind, err := requiredString(owner, "kind", path+".owner.kind")
		if err != nil {
			return err
		}
		ownerRef, err := requiredString(owner, "ref", path+".owner.ref")
		if err != nil {
			return err
		}
		ownerVersion, err := requiredString(owner, "version", path+".owner.version")
		if err != nil {
			return err
		}
		ownerContractHash, err := requiredString(owner, "contractHash", path+".owner.contractHash")
		if err != nil {
			return err
		}
		healthGateRefs, err := applyStringList(owner, "healthGateRefs", path+".owner.healthGateRefs")
		if err != nil {
			return err
		}
		evidenceGateRefs, err := applyStringList(owner, "evidenceGateRefs", path+".owner.evidenceGateRefs")
		if err != nil {
			return err
		}
		evidenceRefs, err := applyRealizationEvidenceRefs(owner, path+".owner")
		if err != nil {
			return err
		}
		for _, evidenceRef := range evidenceRefs {
			result.EvidenceRequirements = append(result.EvidenceRequirements, ApplyEvidenceRequirement{ID: "provider-owner/" + providerID + "/" + evidenceRef, OwnerKind: "provider-owner", OwnerRef: ownerRef, Ref: evidenceRef})
		}
		ownerRequirement := ApplyProviderOwnerRequirement{
			ID: providerID, Ref: ownerRef, Version: ownerVersion, Kind: ownerKind, ContractHash: ownerContractHash,
			SiteRefs: append([]string(nil), providerSiteRefs...), EvidenceRefs: append([]string(nil), evidenceRefs...),
			HealthGateRefs: append([]string(nil), healthGateRefs...), EvidenceGateRefs: append([]string(nil), evidenceGateRefs...),
		}
		ownerNodeRefs, err := applyProviderOwnerNodeRefs(providerID, healthGateRefs, providerSiteRefs, siteByNode, result.HealthRequirements)
		if err != nil {
			return err
		}
		for _, nodeRef := range ownerNodeRefs {
			siteRef := siteByNode[nodeRef]
			ownerRequirement.NodeRefs = append(ownerRequirement.NodeRefs, nodeRef)
			targetNodes[nodeRef] = struct{}{}
			result.RuntimeInstances = append(result.RuntimeInstances, ApplyRuntimeRequirement{
				ID: "provider-owner/" + providerID + "/" + nodeRef, OwnerKind: "provider-owner", OwnerRef: ownerRef,
				OwnerVersion: ownerVersion, OwnerContractHash: ownerContractHash, ProviderRef: providerID, ProviderContractHash: providerContractHash, InstanceRef: nodeRef,
				RuntimeKind: ownerKind, RuntimeDelivery: "provider-owner", SiteRefs: []string{siteRef}, NodeRefs: []string{nodeRef},
				HealthGateRefs: append([]string(nil), healthGateRefs...), EvidenceGateRefs: append([]string(nil), evidenceGateRefs...),
				ArtifactRefs: []string{},
			})
		}
		result.ProviderOwners = append(result.ProviderOwners, ownerRequirement)
		inputs, err := requiredObject(owner, "inputs", path+".owner.inputs")
		if err != nil {
			return err
		}
		secretRefs, err := applyStringMap(inputs, "secretRefs", path+".owner.inputs.secretRefs")
		if err != nil {
			return err
		}
		realization, err := requiredObject(provider, "realization", path+".realization")
		if err != nil {
			return err
		}
		inputBindings, err := requiredObject(realization, "inputBindings", path+".realization.inputBindings")
		if err != nil {
			return err
		}
		for inputRef, secretRef := range secretRefs {
			bindingPath := path + ".realization.inputBindings." + inputRef
			rawBinding, exists := inputBindings[inputRef]
			if !exists {
				return fail(ErrInvalidPlan, bindingPath, "provider-owner secret input has no explicit capability-secret binding")
			}
			binding, ok := rawBinding.(map[string]any)
			if !ok {
				return fail(ErrInvalidPlan, bindingPath, "must be an object")
			}
			source, err := requiredString(binding, "source", bindingPath+".source")
			if err != nil {
				return err
			}
			if source != "capability-secret" {
				return fail(ErrInvalidPlan, bindingPath+".source", "provider-owner secret input must use capability-secret, got %q", source)
			}
			sourceRef, err := requiredString(binding, "capabilityRef", bindingPath+".capabilityRef")
			if err != nil {
				return err
			}
			sourceInputRef, err := requiredString(binding, "key", bindingPath+".key")
			if err != nil {
				return err
			}
			result.Secrets = append(result.Secrets, ApplySecretRequirement{ID: "provider-owner/" + providerID + "/" + inputRef, SourceKind: "capability", SourceRef: sourceRef, SourceInputRef: sourceInputRef, OwnerKind: "provider-owner", OwnerRef: ownerRef, InputRef: inputRef, SecretRef: secretRef, SiteRefs: append([]string(nil), providerSiteRefs...), NodeRefs: append([]string(nil), ownerRequirement.NodeRefs...)})
		}
	}
	return nil
}

func applyProviderOwnerNodeRefs(providerID string, healthGateRefs, providerSiteRefs []string, siteByNode map[string]string, healthRequirements []ApplyHealthRequirement) ([]string, error) {
	allowedSites := make(map[string]struct{}, len(providerSiteRefs))
	for _, siteRef := range providerSiteRefs {
		allowedSites[siteRef] = struct{}{}
	}
	healthByID := make(map[string]ApplyHealthRequirement, len(healthRequirements))
	for _, health := range healthRequirements {
		healthByID[health.ID] = health
	}
	nodeSet := map[string]struct{}{}
	for _, gateRef := range healthGateRefs {
		health, exists := healthByID[gateRef]
		if !exists || health.TargetKind != "provider" || health.TargetRef != providerID {
			return nil, fail(ErrInvalidPlan, "resolvedPlan.providers."+providerID+".owner.healthGateRefs", "gate %q is not an exact health requirement for this provider", gateRef)
		}
		if len(health.NodeRefs) == 0 {
			return nil, fail(ErrInvalidPlan, "resolvedPlan.gates.health."+gateRef+".nodeRefs", "provider-owner health gate must retain its exact target nodes")
		}
		for _, nodeRef := range health.NodeRefs {
			siteRef, exists := siteByNode[nodeRef]
			if !exists {
				return nil, fail(ErrInvalidPlan, "resolvedPlan.gates.health."+gateRef+".nodeRefs", "targets absent or disabled node %q", nodeRef)
			}
			if _, allowed := allowedSites[siteRef]; !allowed {
				return nil, fail(ErrInvalidPlan, "resolvedPlan.gates.health."+gateRef+".nodeRefs", "targets node %q outside provider sites", nodeRef)
			}
			nodeSet[nodeRef] = struct{}{}
		}
	}
	if len(nodeSet) == 0 {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.providers."+providerID+".owner.healthGateRefs", "provider owner has no exact target nodes")
	}
	return sortedApplyMapKeys(nodeSet), nil
}

func appendApplyHostRequirements(plan resolvedplan.ResolvedPlan, targetNodes map[string]struct{}, result *ApplyRequirements) error {
	rawNodes, ok := plan["nodes"].([]any)
	if !ok {
		return fail(ErrInvalidPlan, "resolvedPlan.nodes", "must be an array")
	}
	externalBindings, ok := plan["externalHostBindings"].(map[string]any)
	if !ok {
		return fail(ErrInvalidPlan, "resolvedPlan.externalHostBindings", "must be an object")
	}
	for index, raw := range rawNodes {
		path := fmt.Sprintf("resolvedPlan.nodes[%d]", index)
		node, ok := raw.(map[string]any)
		if !ok {
			return fail(ErrInvalidPlan, path, "must be an object")
		}
		nodeRef, err := requiredString(node, "id", path+".id")
		if err != nil {
			return err
		}
		if _, selected := targetNodes[nodeRef]; !selected {
			continue
		}
		requirement := ApplyHostRequirement{NodeRef: nodeRef}
		if requirement.SiteRef, err = requiredString(node, "siteRef", path+".siteRef"); err != nil {
			return err
		}
		if requirement.FailureDomain, err = requiredString(node, "failureDomain", path+".failureDomain"); err != nil {
			return err
		}
		if rawBinding, exists := externalBindings[nodeRef]; exists {
			binding, ok := rawBinding.(map[string]any)
			if !ok {
				return fail(ErrInvalidPlan, "resolvedPlan.externalHostBindings."+nodeRef, "must be an object")
			}
			requirement.External = true
			if requirement.BindingRef, err = requiredString(binding, "bindingRef", "resolvedPlan.externalHostBindings."+nodeRef+".bindingRef"); err != nil {
				return err
			}
			if requirement.BindingHash, err = requiredString(binding, "bindingHash", "resolvedPlan.externalHostBindings."+nodeRef+".bindingHash"); err != nil {
				return err
			}
			if requirement.ExecutionChannelRef, err = requiredString(binding, "executionChannelRef", "resolvedPlan.externalHostBindings."+nodeRef+".executionChannelRef"); err != nil {
				return err
			}
			bindingSecrets, err := applyStringMap(binding, "secretRefs", "resolvedPlan.externalHostBindings."+nodeRef+".secretRefs")
			if err != nil {
				return err
			}
			for inputRef, secretRef := range bindingSecrets {
				result.Secrets = append(result.Secrets, ApplySecretRequirement{
					ID: "external-host/" + nodeRef + "/" + inputRef, OwnerKind: "external-host-binding", OwnerRef: requirement.BindingRef,
					SourceKind: "external-host-binding", SourceRef: requirement.BindingRef, SourceInputRef: inputRef,
					InputRef: inputRef, SecretRef: secretRef, SiteRefs: []string{requirement.SiteRef}, NodeRefs: []string{nodeRef},
				})
			}
		}
		result.Hosts = append(result.Hosts, requirement)
	}
	return nil
}

func appendApplyHealthRequirements(plan resolvedplan.ResolvedPlan, modules map[string]applyModule, result *ApplyRequirements) error {
	gates, err := requiredObject(plan, "gates", "resolvedPlan.gates")
	if err != nil {
		return err
	}
	rawHealth, ok := gates["health"].([]any)
	if !ok {
		return fail(ErrInvalidPlan, "resolvedPlan.gates.health", "must be an array")
	}
	for index, raw := range rawHealth {
		path := fmt.Sprintf("resolvedPlan.gates.health[%d]", index)
		gate, ok := raw.(map[string]any)
		if !ok {
			return fail(ErrInvalidPlan, path, "must be an object")
		}
		requirement := ApplyHealthRequirement{}
		var err error
		if requirement.ID, err = requiredString(gate, "id", path+".id"); err != nil {
			return err
		}
		if requirement.ContractHash, err = requiredString(gate, "contractHash", path+".contractHash"); err != nil {
			return err
		}
		if requirement.Phase, err = requiredString(gate, "phase", path+".phase"); err != nil {
			return err
		}
		if requirement.Kind, err = requiredString(gate, "kind", path+".kind"); err != nil {
			return err
		}
		if requirement.TargetKind, err = requiredString(gate, "targetKind", path+".targetKind"); err != nil {
			return err
		}
		sourceField := "sourceRef"
		if requirement.TargetKind == "route" {
			sourceField = "sourceHealthGateRef"
		}
		if requirement.SourceRef, err = requiredString(gate, sourceField, path+"."+sourceField); err != nil {
			return err
		}
		if requirement.TargetRef, err = requiredString(gate, "targetRef", path+".targetRef"); err != nil {
			return err
		}
		if requirement.TargetKind == "module" {
			module, exists := modules[requirement.TargetRef]
			if exists && module.executionClass == ApplyExecutionClassContractHandoff {
				continue
			}
		}
		requirement.RouteRef, _ = gate["routeRef"].(string)
		requirement.BackendPoolRef, _ = gate["backendPoolRef"].(string)
		if requirement.SiteRefs, err = applyStringList(gate, "siteRefs", path+".siteRefs"); err != nil {
			return err
		}
		if rawNodeRefs, exists := gate["nodeRefs"]; exists {
			if requirement.NodeRefs, err = applyStringList(map[string]any{"nodeRefs": rawNodeRefs}, "nodeRefs", path+".nodeRefs"); err != nil {
				return err
			}
		}
		if requirement.TargetKind == "route" {
			execution, err := requiredString(gate, "execution", path+".execution")
			if err != nil {
				return err
			}
			if execution == "probe" {
				probe, err := applyRouteHealthProbe(gate, path)
				if err != nil {
					return err
				}
				owners, err := applyRouteHealthRuntimeOwners(plan, requirement, result.RuntimeInstances, path)
				if err != nil {
					return err
				}
				for _, owner := range owners {
					owned := requirement
					owned.ID = requirement.ID + "/runtime/" + owner.ID
					owned.RuntimeRequirementID = owner.ID
					owned.Probe = cloneApplyHealthProbe(probe)
					owned.SiteRefs = append([]string(nil), owner.SiteRefs...)
					owned.NodeRefs = append([]string(nil), owner.NodeRefs...)
					result.HealthRequirements = append(result.HealthRequirements, owned)
				}
				continue
			}
		}
		result.HealthRequirements = append(result.HealthRequirements, requirement)
	}
	return nil
}

func applyRouteHealthProbe(gate map[string]any, path string) (*ApplyHealthProbe, error) {
	protocol, err := requiredString(gate, "protocol", path+".protocol")
	if err != nil {
		return nil, err
	}
	port, err := applyInt(gate, "port", path+".port")
	if err != nil {
		return nil, err
	}
	timeout, err := applyInt(gate, "timeoutSeconds", path+".timeoutSeconds")
	if err != nil {
		return nil, err
	}
	probe := &ApplyHealthProbe{Protocol: protocol, Port: port, TimeoutSeconds: timeout}
	kind, _ := gate["kind"].(string)
	if kind == "http" {
		if probe.Method, err = requiredString(gate, "method", path+".method"); err != nil {
			return nil, err
		}
		if probe.Path, err = requiredString(gate, "path", path+".path"); err != nil {
			return nil, err
		}
		follow, ok := gate["followRedirects"].(bool)
		if !ok {
			return nil, fail(ErrInvalidPlan, path+".followRedirects", "must be a boolean")
		}
		probe.FollowRedirects = follow
		rawStatuses, ok := gate["expectedStatuses"].([]any)
		if !ok || len(rawStatuses) == 0 {
			return nil, fail(ErrInvalidPlan, path+".expectedStatuses", "must be a non-empty array")
		}
		for index, raw := range rawStatuses {
			statusPath := fmt.Sprintf("%s.expectedStatuses[%d]", path, index)
			status, err := applyIntegerValue(raw, statusPath)
			if err != nil {
				return nil, err
			}
			probe.ExpectedStatuses = append(probe.ExpectedStatuses, status)
		}
	}
	return probe, nil
}

func applyRouteHealthRuntimeOwners(plan resolvedplan.ResolvedPlan, health ApplyHealthRequirement, runtimes []ApplyRuntimeRequirement, path string) ([]ApplyRuntimeRequirement, error) {
	network, err := requiredObject(plan, "network", "resolvedPlan.network")
	if err != nil {
		return nil, err
	}
	rawPools, ok := network["backendPools"].([]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.network.backendPools", "must be an array")
	}
	var pool map[string]any
	for _, raw := range rawPools {
		candidate, ok := raw.(map[string]any)
		if ok && candidate["id"] == health.BackendPoolRef {
			if pool != nil {
				return nil, fail(ErrInvalidPlan, path+".backendPoolRef", "matches more than one backend pool")
			}
			pool = candidate
		}
	}
	if pool == nil {
		return nil, fail(ErrInvalidPlan, path+".backendPoolRef", "does not resolve one backend pool")
	}
	rawMembers, ok := pool["members"].([]any)
	if !ok || len(rawMembers) == 0 {
		return nil, fail(ErrInvalidPlan, path+".backendPoolRef", "backend pool has no exact members")
	}
	owners := make([]ApplyRuntimeRequirement, 0, len(rawMembers))
	for index, raw := range rawMembers {
		member, ok := raw.(map[string]any)
		if !ok {
			return nil, fail(ErrInvalidPlan, fmt.Sprintf("%s.members[%d]", path, index), "must be an object")
		}
		instanceRef, err := requiredString(member, "instanceRef", fmt.Sprintf("%s.members[%d].instanceRef", path, index))
		if err != nil {
			return nil, err
		}
		siteRef, err := requiredString(member, "siteRef", fmt.Sprintf("%s.members[%d].siteRef", path, index))
		if err != nil {
			return nil, err
		}
		nodeRef, err := requiredString(member, "nodeRef", fmt.Sprintf("%s.members[%d].nodeRef", path, index))
		if err != nil {
			return nil, err
		}
		matches := make([]ApplyRuntimeRequirement, 0, 1)
		for _, runtime := range runtimes {
			if runtime.InstanceRef == instanceRef && len(runtime.SiteRefs) == 1 && runtime.SiteRefs[0] == siteRef && len(runtime.NodeRefs) == 1 && runtime.NodeRefs[0] == nodeRef {
				matches = append(matches, runtime)
			}
		}
		if len(matches) != 1 {
			return nil, fail(ErrInvalidPlan, fmt.Sprintf("%s.members[%d]", path, index), "must bind exactly one Apply runtime owner; got %d", len(matches))
		}
		owners = append(owners, matches[0])
	}
	sort.Slice(owners, func(i, j int) bool { return owners[i].ID < owners[j].ID })
	return owners, nil
}

func applyInt(object map[string]any, field, path string) (int, error) {
	value, ok := object[field]
	if !ok {
		return 0, fail(ErrInvalidPlan, path, "must be an integer")
	}
	return applyIntegerValue(value, path)
}

func applyIntegerValue(value any, path string) (int, error) {
	switch number := value.(type) {
	case int:
		return number, nil
	case json.Number:
		parsed, err := strconv.Atoi(number.String())
		if err == nil {
			return parsed, nil
		}
	}
	return 0, fail(ErrInvalidPlan, path, "must be an integer")
}

func cloneApplyHealthProbe(source *ApplyHealthProbe) *ApplyHealthProbe {
	if source == nil {
		return nil
	}
	result := *source
	result.ExpectedStatuses = append([]int(nil), source.ExpectedStatuses...)
	return &result
}

func bindApplyWorkloadHealthRequirements(result *ApplyRequirements) error {
	for _, workload := range result.Workloads {
		matches := make([]string, 0, 1)
		for _, health := range result.HealthRequirements {
			if health.SourceRef == workload.HealthRef && health.TargetKind == "module" && health.TargetRef == workload.ModuleRef {
				matches = append(matches, health.ID)
			}
		}
		if len(matches) != 1 {
			return fail(ErrInvalidPlan, "resolvedPlan.workloads."+workload.ID+".alternative.route.healthRef", "must bind exactly one materialized module health gate for logical source %q; got %d", workload.HealthRef, len(matches))
		}
		result.EvidenceRequirements = append(result.EvidenceRequirements, ApplyEvidenceRequirement{
			ID: "workload/" + workload.ID + "/health/" + matches[0], OwnerKind: "workload", OwnerRef: workload.ID,
			Ref: workload.HealthRef, HealthGateRefs: []string{matches[0]},
		})
	}
	return nil
}

func appendApplyEvidenceGateRequirements(plan resolvedplan.ResolvedPlan, result *ApplyRequirements) error {
	gates, err := requiredObject(plan, "gates", "resolvedPlan.gates")
	if err != nil {
		return err
	}
	rawEvidence, ok := gates["evidence"].([]any)
	if !ok {
		return fail(ErrInvalidPlan, "resolvedPlan.gates.evidence", "must be an array")
	}
	for index, raw := range rawEvidence {
		path := fmt.Sprintf("resolvedPlan.gates.evidence[%d]", index)
		gate, ok := raw.(map[string]any)
		if !ok {
			return fail(ErrInvalidPlan, path, "must be an object")
		}
		gateRef, err := requiredString(gate, "id", path+".id")
		if err != nil {
			return err
		}
		requirement := ApplyEvidenceRequirement{ID: "gate/" + gateRef, GateRef: gateRef, OwnerKind: "evidence-gate", OwnerRef: gateRef}
		if requirement.Phase, err = requiredString(gate, "phase", path+".phase"); err != nil {
			return err
		}
		if requirement.Producer, err = requiredString(gate, "producer", path+".producer"); err != nil {
			return err
		}
		if requirement.Scenario, err = requiredString(gate, "scenario", path+".scenario"); err != nil {
			return err
		}
		if required, ok := gate["required"].(bool); !ok || !required {
			return fail(ErrInvalidPlan, path+".required", "must be true")
		}
		if rawRefs, exists := gate["healthGateRefs"]; exists {
			requirement.HealthGateRefs, err = applyStringList(map[string]any{"refs": rawRefs}, "refs", path+".healthGateRefs")
			if err != nil {
				return err
			}
		}
		if rawRefs, exists := gate["artifactRefs"]; exists {
			requirement.ArtifactRefs, err = applyStringList(map[string]any{"refs": rawRefs}, "refs", path+".artifactRefs")
			if err != nil {
				return err
			}
		}
		result.EvidenceRequirements = append(result.EvidenceRequirements, requirement)
	}
	return nil
}

func applyRealizationEvidenceRefs(owner map[string]any, path string) ([]string, error) {
	support, err := requiredObject(owner, "realizationSupport", path+".realizationSupport")
	if err != nil {
		return nil, err
	}
	evidence, err := requiredObject(support, "evidence", path+".realizationSupport.evidence")
	if err != nil {
		return nil, err
	}
	return applyStringList(evidence, "requiredRefs", path+".realizationSupport.evidence.requiredRefs")
}

func applyStringList(object map[string]any, field, path string) ([]string, error) {
	raw, ok := object[field].([]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, path, "must be an array")
	}
	result := make([]string, 0, len(raw))
	for index, value := range raw {
		item, ok := value.(string)
		if !ok || item == "" {
			return nil, fail(ErrInvalidPlan, fmt.Sprintf("%s[%d]", path, index), "must be a non-empty string")
		}
		result = append(result, item)
	}
	return result, nil
}

func applyStringMap(object map[string]any, field, path string) (map[string]string, error) {
	raw, ok := object[field].(map[string]any)
	if !ok {
		return nil, fail(ErrInvalidPlan, path, "must be an object")
	}
	result := make(map[string]string, len(raw))
	for key, value := range raw {
		item, ok := value.(string)
		if !ok || item == "" {
			return nil, fail(ErrInvalidPlan, path+"."+key, "must be a non-empty string")
		}
		result[key] = item
	}
	return result, nil
}

func validateApplySecretSourceClosure(requirements []ApplySecretRequirement) error {
	for _, source := range requirements {
		if source.OwnerKind != "capability-intent" && source.OwnerKind != "workload-intent" && source.OwnerKind != "module-intent" && source.OwnerKind != "addon-intent" {
			continue
		}
		matched := false
		for _, consumer := range requirements {
			if consumer.OwnerKind == source.OwnerKind || consumer.SourceKind != source.SourceKind || consumer.SourceRef != source.SourceRef || consumer.SourceInputRef != source.SourceInputRef || consumer.SecretRef != source.SecretRef {
				continue
			}
			matched = true
			break
		}
		if !matched {
			return fail(ErrInvalidPlan, "resolvedPlan."+source.SourceKind+"s."+source.SourceRef+".secretRefs."+source.SourceInputRef, "secret intent has no exact Apply consumer")
		}
	}
	return nil
}

func validateApplyRequirementIDs(requirements ApplyRequirements) error {
	checks := []struct {
		path string
		ids  []string
	}{
		{path: "workloads", ids: applyRequirementIDs(requirements.Workloads, func(value ApplyWorkloadRequirement) string { return value.ID })},
		{path: "secrets", ids: applyRequirementIDs(requirements.Secrets, func(value ApplySecretRequirement) string { return value.ID })},
		{path: "runtimeInstances", ids: applyRequirementIDs(requirements.RuntimeInstances, func(value ApplyRuntimeRequirement) string { return value.ID })},
		{path: "artifacts", ids: applyRequirementIDs(requirements.Artifacts, func(value ApplyArtifactRequirement) string { return value.ID })},
		{path: "hosts", ids: applyRequirementIDs(requirements.Hosts, func(value ApplyHostRequirement) string { return value.NodeRef })},
		{path: "providerOwners", ids: applyRequirementIDs(requirements.ProviderOwners, func(value ApplyProviderOwnerRequirement) string { return value.ID })},
		{path: "accessBindings", ids: applyRequirementIDs(requirements.AccessBindings, func(value ApplyAccessBindingRequirement) string { return value.ID })},
		{path: "evidence", ids: applyRequirementIDs(requirements.EvidenceRequirements, func(value ApplyEvidenceRequirement) string { return value.ID })},
		{path: "health", ids: applyRequirementIDs(requirements.HealthRequirements, func(value ApplyHealthRequirement) string { return value.ID })},
	}
	for _, check := range checks {
		seen := make(map[string]struct{}, len(check.ids))
		for _, id := range check.ids {
			if _, duplicate := seen[id]; duplicate {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements."+check.path, "duplicate requirement ID %q", id)
			}
			seen[id] = struct{}{}
		}
	}
	return nil
}

func validateApplyProviderOwnerClosure(requirements ApplyRequirements) error {
	owners := make(map[string]ApplyProviderOwnerRequirement, len(requirements.ProviderOwners))
	for _, owner := range requirements.ProviderOwners {
		if _, duplicate := owners[owner.ID]; duplicate {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.providerOwners", "duplicate provider-owner requirement %q", owner.ID)
		}
		owners[owner.ID] = owner
	}
	healthByID := make(map[string][]ApplyHealthRequirement, len(requirements.HealthRequirements))
	for _, health := range requirements.HealthRequirements {
		healthByID[health.ID] = append(healthByID[health.ID], health)
	}
	evidenceByGateRef := make(map[string][]ApplyEvidenceRequirement, len(requirements.EvidenceRequirements))
	for _, evidence := range requirements.EvidenceRequirements {
		if evidence.GateRef != "" {
			evidenceByGateRef[evidence.GateRef] = append(evidenceByGateRef[evidence.GateRef], evidence)
		}
	}
	runtimeNodesByOwner := make(map[string]map[string]struct{}, len(owners))
	for _, runtime := range requirements.RuntimeInstances {
		if runtime.OwnerKind != "provider-owner" {
			if runtime.OwnerVersion != "" || len(runtime.HealthGateRefs) != 0 || len(runtime.EvidenceGateRefs) != 0 {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID, "non-provider runtime must not carry provider-owner identity or gates")
			}
			continue
		}
		owner, exists := owners[runtime.ProviderRef]
		if !exists {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".providerRef", "provider-owner runtime has no exact owner requirement")
		}
		if runtime.OwnerRef != owner.Ref || runtime.OwnerVersion != owner.Version || runtime.OwnerContractHash != owner.ContractHash ||
			runtime.RuntimeKind != owner.Kind || runtime.RuntimeDelivery != "provider-owner" ||
			!slices.Equal(runtime.HealthGateRefs, owner.HealthGateRefs) || !slices.Equal(runtime.EvidenceGateRefs, owner.EvidenceGateRefs) {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID, "provider-owner runtime does not retain the exact owner identity and gates")
		}
		if len(runtime.SiteRefs) != 1 || !slices.Contains(owner.SiteRefs, runtime.SiteRefs[0]) ||
			len(runtime.NodeRefs) != 1 || runtime.InstanceRef != runtime.NodeRefs[0] || !slices.Contains(owner.NodeRefs, runtime.NodeRefs[0]) {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".nodeRefs", "provider-owner runtime does not retain one exact owner target site and node")
		}
		if runtimeNodesByOwner[owner.ID] == nil {
			runtimeNodesByOwner[owner.ID] = map[string]struct{}{}
		}
		if _, duplicate := runtimeNodesByOwner[owner.ID][runtime.NodeRefs[0]]; duplicate {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances", "provider-owner %q has duplicate runtime target node %q", owner.ID, runtime.NodeRefs[0])
		}
		runtimeNodesByOwner[owner.ID][runtime.NodeRefs[0]] = struct{}{}
	}
	for _, owner := range requirements.ProviderOwners {
		seenHealth := make(map[string]struct{}, len(owner.HealthGateRefs))
		for _, gateRef := range owner.HealthGateRefs {
			if _, duplicate := seenHealth[gateRef]; duplicate {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.providerOwners."+owner.ID+".healthGateRefs", "duplicate health gate %q", gateRef)
			}
			seenHealth[gateRef] = struct{}{}
			matches := healthByID[gateRef]
			if len(matches) != 1 || matches[0].TargetKind != "provider" || matches[0].TargetRef != owner.ID {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.providerOwners."+owner.ID+".healthGateRefs", "gate %q is not one exact materialized health gate for this provider owner", gateRef)
			}
		}
		seenEvidence := make(map[string]struct{}, len(owner.EvidenceGateRefs))
		for _, gateRef := range owner.EvidenceGateRefs {
			if _, duplicate := seenEvidence[gateRef]; duplicate {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.providerOwners."+owner.ID+".evidenceGateRefs", "duplicate evidence gate %q", gateRef)
			}
			seenEvidence[gateRef] = struct{}{}
			if len(evidenceByGateRef[gateRef]) != 1 {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.providerOwners."+owner.ID+".evidenceGateRefs", "gate %q is not one exact materialized evidence gate", gateRef)
			}
		}
		if len(runtimeNodesByOwner[owner.ID]) != len(owner.NodeRefs) {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.providerOwners."+owner.ID+".nodeRefs", "provider owner does not have exactly one runtime target per owner node")
		}
	}
	return nil
}

func validateApplyArtifactRuntimeClosure(requirements ApplyRequirements) error {
	artifacts := make(map[string]ApplyArtifactRequirement, len(requirements.Artifacts))
	for _, artifact := range requirements.Artifacts {
		if _, duplicate := artifacts[artifact.ID]; duplicate {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.artifacts", "duplicate artifact requirement %q", artifact.ID)
		}
		artifacts[artifact.ID] = artifact
	}
	runtimeReferenced := make(map[string]int, len(artifacts))
	adapterReferenced := make(map[string]int, len(artifacts))
	for _, runtime := range requirements.RuntimeInstances {
		seen := make(map[string]struct{}, len(runtime.ArtifactRefs))
		for _, artifactRef := range runtime.ArtifactRefs {
			if _, duplicate := seen[artifactRef]; duplicate {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".artifactRefs", "duplicate artifact reference %q", artifactRef)
			}
			seen[artifactRef] = struct{}{}
			artifact, exists := artifacts[artifactRef]
			if !exists {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".artifactRefs", "artifact %q is absent", artifactRef)
			}
			if artifact.OwnerKind != "render-instance" || artifact.OwnerRef != runtime.InstanceRef || artifact.ProviderRef != runtime.ProviderRef ||
				artifact.ProviderContractHash != runtime.ProviderContractHash || artifact.ModuleRef != runtime.ModuleRef || artifact.ModuleContractHash != runtime.ModuleContractHash ||
				artifact.UnitRef != runtime.UnitRef || artifact.UnitContractHash != runtime.UnitContractHash || artifact.OwnerContractHash != runtime.UnitContractHash ||
				artifact.InstanceRef != runtime.InstanceRef || !slices.Equal(artifact.SiteRefs, runtime.SiteRefs) || !slices.Equal(artifact.NodeRefs, runtime.NodeRefs) {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".artifactRefs", "artifact %q is not owned by the exact runtime instance", artifactRef)
			}
			runtimeReferenced[artifactRef]++
		}
		if runtime.RuntimeDelivery == "selected-paas" && runtime.RuntimeAdapter == nil {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".runtimeAdapter", "selected-paas runtime requires one exact adapter")
		}
		if runtime.RuntimeAdapter != nil {
			if runtime.RuntimeDelivery != "selected-paas" || runtime.WorkloadRef == "" {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".runtimeAdapter", "is valid only for a selected-paas workload")
			}
			adapter := runtime.RuntimeAdapter
			if err := validateApplyRuntimeAdapterArtifacts(runtime.ID, "runtimeAdapter", adapter.ProviderRef, adapter.ProviderContractHash, adapter.ModuleRef, adapter.ModuleContractHash, adapter.ArtifactRefs, artifacts, adapterReferenced); err != nil {
				return err
			}
			seenAgents := map[string]struct{}{}
			for index, agent := range adapter.Agents {
				if _, duplicate := seenAgents[agent.ID]; duplicate {
					return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".runtimeAdapter.agents", "duplicate agent %q", agent.ID)
				}
				seenAgents[agent.ID] = struct{}{}
				field := fmt.Sprintf("runtimeAdapter.agents[%d]", index)
				if err := validateApplyRuntimeAdapterArtifacts(runtime.ID, field, adapter.ProviderRef, adapter.ProviderContractHash, agent.ModuleRef, agent.ModuleContractHash, agent.ArtifactRefs, artifacts, adapterReferenced); err != nil {
					return err
				}
			}
		}
	}
	for _, artifact := range requirements.Artifacts {
		switch artifact.OwnerKind {
		case "plan":
			if artifact.ExecutionClass != ApplyExecutionClassPlan {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.artifacts."+artifact.ID+".executionClass", "plan-owned metadata must use the plan execution class")
			}
			if runtimeReferenced[artifact.ID] != 0 || adapterReferenced[artifact.ID] != 0 {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.artifacts."+artifact.ID, "plan-owned metadata must not be assigned to a runtime instance")
			}
		case "render-instance":
			switch artifact.ExecutionClass {
			case ApplyExecutionClassExecutable:
				if runtimeReferenced[artifact.ID] != 1 || adapterReferenced[artifact.ID] != 0 {
					return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.artifacts."+artifact.ID, "executable render-instance artifact must be owned by exactly one runtime and no adapter; got runtime=%d adapter=%d", runtimeReferenced[artifact.ID], adapterReferenced[artifact.ID])
				}
			case ApplyExecutionClassContractHandoff:
				if runtimeReferenced[artifact.ID] != 0 {
					return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.artifacts."+artifact.ID, "contract-handoff artifact must not be assigned to an executable runtime")
				}
			default:
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.artifacts."+artifact.ID+".executionClass", "render-instance artifact requires an executable or contract-handoff execution class")
			}
		default:
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.artifacts."+artifact.ID+".ownerKind", "unsupported owner kind %q", artifact.OwnerKind)
		}
	}
	return nil
}

func validateApplyRuntimeAdapterArtifacts(runtimeID, field, providerRef, providerHash, moduleRef, moduleHash string, refs []string, artifacts map[string]ApplyArtifactRequirement, referenced map[string]int) error {
	if providerRef == "" || moduleRef == "" || !validSHA256(providerHash) || !validSHA256(moduleHash) || len(refs) == 0 {
		return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtimeID+"."+field, "adapter authority and artifact refs must be complete")
	}
	seen := map[string]struct{}{}
	for _, ref := range refs {
		if _, duplicate := seen[ref]; duplicate {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtimeID+"."+field+".artifactRefs", "duplicate artifact %q", ref)
		}
		seen[ref] = struct{}{}
		artifact, exists := artifacts[ref]
		if !exists || artifact.ExecutionClass != ApplyExecutionClassContractHandoff || artifact.ProviderRef != providerRef || artifact.ProviderContractHash != providerHash || artifact.ModuleRef != moduleRef || artifact.ModuleContractHash != moduleHash {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtimeID+"."+field+".artifactRefs", "artifact %q does not match the exact contract-handoff module", ref)
		}
		referenced[ref]++
	}
	return nil
}

func applyRequirementIDs[T any](values []T, id func(T) string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, id(value))
	}
	return result
}

func sortApplyRequirements(result *ApplyRequirements) {
	for index := range result.Workloads {
		sort.Strings(result.Workloads[index].SiteRefs)
		sort.Strings(result.Workloads[index].NodeRefs)
		sort.Strings(result.Workloads[index].InstanceRefs)
		sort.Strings(result.Workloads[index].EvidenceRefs)
	}
	for index := range result.RuntimeInstances {
		sort.Strings(result.RuntimeInstances[index].SiteRefs)
		sort.Strings(result.RuntimeInstances[index].NodeRefs)
		sort.Strings(result.RuntimeInstances[index].HealthGateRefs)
		sort.Strings(result.RuntimeInstances[index].EvidenceGateRefs)
		sort.Slice(result.RuntimeInstances[index].DaemonBindings, func(i, j int) bool {
			return result.RuntimeInstances[index].DaemonBindings[i].InstanceRef < result.RuntimeInstances[index].DaemonBindings[j].InstanceRef
		})
		sort.Strings(result.RuntimeInstances[index].ArtifactRefs)
		if result.RuntimeInstances[index].RuntimeAdapter != nil {
			sort.Strings(result.RuntimeInstances[index].RuntimeAdapter.ArtifactRefs)
			for agentIndex := range result.RuntimeInstances[index].RuntimeAdapter.Agents {
				sort.Strings(result.RuntimeInstances[index].RuntimeAdapter.Agents[agentIndex].ArtifactRefs)
			}
			sort.Slice(result.RuntimeInstances[index].RuntimeAdapter.Agents, func(left, right int) bool {
				return result.RuntimeInstances[index].RuntimeAdapter.Agents[left].ID < result.RuntimeInstances[index].RuntimeAdapter.Agents[right].ID
			})
		}
		sort.Strings(result.RuntimeInstances[index].AccessBindingRefs)
	}
	for index := range result.Artifacts {
		sort.Strings(result.Artifacts[index].SiteRefs)
		sort.Strings(result.Artifacts[index].NodeRefs)
	}
	for index := range result.Secrets {
		sort.Strings(result.Secrets[index].SiteRefs)
		sort.Strings(result.Secrets[index].NodeRefs)
	}
	for index := range result.ProviderOwners {
		sort.Strings(result.ProviderOwners[index].SiteRefs)
		sort.Strings(result.ProviderOwners[index].NodeRefs)
		sort.Strings(result.ProviderOwners[index].EvidenceRefs)
		sort.Strings(result.ProviderOwners[index].HealthGateRefs)
		sort.Strings(result.ProviderOwners[index].EvidenceGateRefs)
	}
	for index := range result.AccessBindings {
		sort.Strings(result.AccessBindings[index].TargetNodeRefs)
	}
	for index := range result.EvidenceRequirements {
		sort.Strings(result.EvidenceRequirements[index].HealthGateRefs)
		sort.Strings(result.EvidenceRequirements[index].ArtifactRefs)
	}
	for index := range result.HealthRequirements {
		sort.Strings(result.HealthRequirements[index].SiteRefs)
		sort.Strings(result.HealthRequirements[index].NodeRefs)
		if result.HealthRequirements[index].Probe != nil {
			sort.Ints(result.HealthRequirements[index].Probe.ExpectedStatuses)
		}
	}
	sort.Slice(result.Workloads, func(i, j int) bool { return result.Workloads[i].ID < result.Workloads[j].ID })
	sort.Slice(result.Secrets, func(i, j int) bool { return result.Secrets[i].ID < result.Secrets[j].ID })
	sort.Slice(result.RuntimeInstances, func(i, j int) bool { return result.RuntimeInstances[i].ID < result.RuntimeInstances[j].ID })
	sort.Slice(result.Artifacts, func(i, j int) bool { return result.Artifacts[i].ID < result.Artifacts[j].ID })
	sort.Slice(result.Hosts, func(i, j int) bool { return result.Hosts[i].NodeRef < result.Hosts[j].NodeRef })
	sort.Slice(result.ProviderOwners, func(i, j int) bool { return result.ProviderOwners[i].ID < result.ProviderOwners[j].ID })
	sort.Slice(result.AccessBindings, func(i, j int) bool { return result.AccessBindings[i].ID < result.AccessBindings[j].ID })
	sort.Slice(result.EvidenceRequirements, func(i, j int) bool { return result.EvidenceRequirements[i].ID < result.EvidenceRequirements[j].ID })
	sort.Slice(result.HealthRequirements, func(i, j int) bool { return result.HealthRequirements[i].ID < result.HealthRequirements[j].ID })
}

func sortedApplyMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
	return result
}

func cloneApplyRuntimeAdapterRequirement(source *ApplyRuntimeAdapterRequirement) *ApplyRuntimeAdapterRequirement {
	if source == nil {
		return nil
	}
	result := *source
	result.ArtifactRefs = append([]string(nil), source.ArtifactRefs...)
	result.Agents = append([]ApplyRuntimeAdapterAgentRequirement(nil), source.Agents...)
	for index := range result.Agents {
		result.Agents[index].ArtifactRefs = append([]string(nil), source.Agents[index].ArtifactRefs...)
	}
	return &result
}
