package architecturev2renderer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/generationartifact"
)

var (
	contractIDPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]*$`)
	dockerSocketPathPattern = regexp.MustCompile(`^/[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*[.]sock$`)
)

const dockerSocketDirectInterfaceKind = "docker-socket-direct-v1"
const dockerSocketPathSourceDaemonBinding = "daemon-binding"
const maxDockerSocketPathBytes = 107

type renderPlan struct {
	outputRoot string
	modules    []renderModule
	artifacts  map[string]artifactContract
	bindings   map[instanceOutputKey]outputBinding
}

type renderModule struct {
	id    string
	units []renderUnitContract
}

type renderUnitContract struct {
	id                           string
	kind                         string
	rendererRef                  string
	templateRef                  string
	version                      string
	contractHash                 string
	runtime                      moduleRuntimeContract
	publicInputRefs              []string
	secretInputRefs              []string
	planInputRefs                []string
	inputBindingsCanonical       []byte
	siteRefs                     []string
	nodeRefs                     []string
	valuesCanonical              []byte
	secretsCanonical             []byte
	planInputsCanonical          []byte
	placementCanonical           []byte
	serviceEndpointsCanonical    []byte
	providedInterfacesCanonical  []byte
	requiredInterfacesCanonical  []byte
	privilegedApprovalsCanonical []byte
	serviceEndpoints             []serviceEndpointContract
	providedInterfaces           []implementationInterface
	requiredInterfaces           []implementationInterface
	outputs                      []string
	instances                    []renderUnitInstance
}

type serviceEndpointContract struct {
	serviceRef string
}

type renderUnitInstance struct {
	id                string
	scope             string
	siteRef           string
	nodeRef           string
	daemonRef         string
	daemonInstanceRef string
	daemonEngine      string
	daemonSocketPath  string
	networkBindings   []runtimeNetworkBinding
	networkCanonical  []byte
	outputs           []renderInstanceOutput
}

type runtimeNetworkOwner struct {
	moduleRef, unitRef, instanceRef, interfaceRef string
}

type runtimeNetworkMember struct {
	role, moduleRef, unitRef, instanceRef, interfaceRef string
}

type runtimeNetwork struct {
	id, networkRef, siteRef, nodeRef, daemonRef, daemonInstanceRef string
	owner                                                          runtimeNetworkOwner
	members                                                        []runtimeNetworkMember
}

type runtimeNetworkBinding struct {
	networkInstanceRef, networkRef, role, interfaceRef string
	siteRef, nodeRef, daemonRef, daemonInstanceRef     string
	owner                                              runtimeNetworkOwner
}

type implementationInterface struct {
	id, kind, daemonRef, networkRef, policyProfile, endpointRef, socketPath, socketPathSource string
	providerBindings                                                                          []implementationProviderBinding
}

type implementationProviderBinding struct {
	interfaceRef, moduleRef, unitRef, providerInstanceRef, consumerInstanceRef string
	siteRef, nodeRef, daemonRef, daemonInstanceRef                             string
	networkRef, networkInstanceRef, policyProfile, endpointRef                 string
}

type renderInstanceIdentity struct {
	moduleRef, unitRef, instanceRef string
}

type interfaceIdentity struct {
	instance renderInstanceIdentity
	role     string
	ref      string
}

type networkMembershipIdentity struct {
	networkRef string
	member     runtimeNetworkMember
}

type renderInstanceContext struct {
	unit     renderUnitContract
	instance renderUnitInstance
}

type renderInstanceOutput struct {
	ref        string
	artifactID string
	path       string
}

type artifactContract struct {
	id       string
	path     string
	kind     string
	format   string
	mode     string
	required bool
	owner    artifactOwner
}

type artifactOwner struct {
	kind        string
	moduleRef   string
	unitRef     string
	instanceRef string
	outputRef   string
}

type outputBinding struct {
	moduleID    string
	unitRef     string
	instanceRef string
	artifactID  string
	outputRef   string
}

type instanceOutputKey struct {
	moduleID   string
	unitID     string
	instanceID string
	output     string
}

type unitOutputKey struct {
	moduleID string
	unitID   string
	output   string
}

type rawRenderUnit struct {
	ID                  string                     `json:"id"`
	Kind                string                     `json:"kind"`
	RendererRef         string                     `json:"rendererRef"`
	CompatibleTargets   []string                   `json:"compatibleTargets"`
	TemplateRef         string                     `json:"templateRef"`
	Version             string                     `json:"version"`
	ContractHash        string                     `json:"contractHash"`
	PublicInputRefs     []string                   `json:"publicInputRefs"`
	SecretInputRefs     []string                   `json:"secretInputRefs"`
	PlanInputRefs       []string                   `json:"planInputRefs"`
	InputBindings       []json.RawMessage          `json:"inputBindings"`
	SecretInputBindings map[string]json.RawMessage `json:"secretInputBindings"`
	Values              map[string]json.RawMessage `json:"values"`
	SecretRefs          map[string]json.RawMessage `json:"secretRefs"`
	PlanInputs          map[string]json.RawMessage `json:"planInputs"`
	Outputs             []string                   `json:"outputs"`
	SiteRefs            []string                   `json:"siteRefs"`
	NodeRefs            []string                   `json:"nodeRefs"`
	Placement           json.RawMessage            `json:"placement"`
	DaemonBindings      []json.RawMessage          `json:"daemonBindings"`
	ServiceEndpoints    []json.RawMessage          `json:"serviceEndpoints"`
	ProvidesInterfaces  []json.RawMessage          `json:"providesInterfaces"`
	RequiresInterfaces  []json.RawMessage          `json:"requiresInterfaces"`
	Instances           []rawRenderUnitInstance    `json:"instances"`
}

type rawModuleServiceEndpoint struct {
	ServiceRef              string          `json:"serviceRef"`
	UpstreamProtocol        string          `json:"upstreamProtocol"`
	TargetPort              int             `json:"targetPort"`
	AllowedIngressProtocols []string        `json:"allowedIngressProtocols"`
	AllowedExposures        []string        `json:"allowedExposures"`
	OriginSelector          string          `json:"originSelector"`
	OriginSelection         json.RawMessage `json:"originSelection,omitempty"`
	HealthRef               string          `json:"healthRef"`
	Data                    json.RawMessage `json:"data,omitempty"`
}

type rawModuleServiceEndpointData struct {
	BindingRef      string   `json:"bindingRef"`
	RequiredClasses []string `json:"requiredClasses"`
	Locality        string   `json:"locality"`
}

type rawModuleRuntime struct {
	Execution         string                     `json:"execution"`
	Kind              string                     `json:"kind"`
	Delivery          string                     `json:"delivery"`
	Engine            optionalStringField        `json:"engine,omitempty"`
	Image             *rawModuleRuntimeImage     `json:"image,omitempty"`
	EntryComponentRef optionalStringField        `json:"entryComponentRef,omitempty"`
	Components        []json.RawMessage          `json:"components,omitempty"`
	Settings          map[string]json.RawMessage `json:"settings,omitempty"`
}

type rawModuleRenderInputBinding struct {
	TargetRef    string          `json:"targetRef"`
	SourceRef    string          `json:"sourceRef"`
	ValueType    string          `json:"valueType"`
	Cardinality  string          `json:"cardinality"`
	Required     bool            `json:"required"`
	DefaultValue json.RawMessage `json:"defaultValue,omitempty"`
}

type rawPublicServiceRouteV2 struct {
	ID               string                      `json:"id"`
	ServiceRef       string                      `json:"serviceRef"`
	ModuleRef        string                      `json:"moduleRef"`
	OriginSiteRef    string                      `json:"originSiteRef"`
	OriginNodeRefs   []string                    `json:"originNodeRefs"`
	BackendPoolRef   string                      `json:"backendPoolRef"`
	BackendPool      rawPublicRouteBackendPoolV2 `json:"backendPool"`
	Exposure         string                      `json:"exposure"`
	Protocol         string                      `json:"protocol"`
	UpstreamProtocol string                      `json:"upstreamProtocol"`
	Port             int                         `json:"port"`
	TargetPort       int                         `json:"targetPort"`
	Host             string                      `json:"host,omitempty"`
	Path             string                      `json:"path,omitempty"`
	Access           rawPublicRouteAccessV2      `json:"access"`
	TLS              rawPublicRouteTLSV2         `json:"tls"`
	HealthGateRef    string                      `json:"healthGateRef"`
}

type rawPublicServiceRouteV3 struct {
	rawPublicServiceRouteV2
	HealthProbe rawPublicRouteHealthProbeV3 `json:"healthProbe"`
}

type rawServiceEndpointOriginSelectionV2 struct {
	SiteKinds               []string `json:"siteKinds"`
	MinSites                int      `json:"minSites"`
	SiteFailureDomainSpread int      `json:"siteFailureDomainSpread"`
	NodeFailureDomainSpread int      `json:"nodeFailureDomainSpread"`
	RequiredRoles           []string `json:"requiredRoles"`
	SiteFailureDomains      []string `json:"siteFailureDomains,omitempty"`
	NodeFailureDomains      []struct {
		SiteRef       string `json:"siteRef"`
		FailureDomain string `json:"failureDomain"`
	} `json:"nodeFailureDomains,omitempty"`
}

type rawPublicServiceRouteV4 struct {
	ID                    string                                `json:"id"`
	ServiceRef            string                                `json:"serviceRef"`
	ModuleRef             string                                `json:"moduleRef"`
	OriginSiteRef         string                                `json:"originSiteRef,omitempty"`
	OriginSiteRefs        []string                              `json:"originSiteRefs"`
	OriginNodeRefs        []string                              `json:"originNodeRefs"`
	OriginSelector        string                                `json:"originSelector"`
	OriginSelection       *rawServiceEndpointOriginSelectionV2  `json:"originSelection,omitempty"`
	BackendPoolRef        string                                `json:"backendPoolRef"`
	BackendPool           rawPublicRouteBackendPoolV2           `json:"backendPool"`
	Exposure              string                                `json:"exposure"`
	Protocol              string                                `json:"protocol"`
	UpstreamProtocol      string                                `json:"upstreamProtocol"`
	Port                  int                                   `json:"port"`
	TargetPort            int                                   `json:"targetPort"`
	Host                  string                                `json:"host,omitempty"`
	Path                  string                                `json:"path,omitempty"`
	Access                rawPublicRouteAccessV2                `json:"access"`
	TLS                   rawPublicRouteTLSV2                   `json:"tls"`
	HealthGateRef         string                                `json:"healthGateRef"`
	HealthProbe           rawPublicRouteHealthProbeV3           `json:"healthProbe"`
	CapabilityAuthorities []rawPublicRouteCapabilityAuthorityV4 `json:"capabilityAuthorities"`
}

type rawPublicRouteCapabilityAuthorityV4 struct {
	CapabilityRef string `json:"capabilityRef"`
	Role          string `json:"role"`
}

type rawPublicRouteHealthProbeV3 struct {
	Kind             string `json:"kind"`
	Protocol         string `json:"protocol"`
	Port             int    `json:"port"`
	TimeoutSeconds   int    `json:"timeoutSeconds"`
	Method           string `json:"method,omitempty"`
	FollowRedirects  *bool  `json:"followRedirects,omitempty"`
	Path             string `json:"path,omitempty"`
	ExpectedStatuses []int  `json:"expectedStatuses,omitempty"`
}

type rawPublicRouteBackendPoolV2 struct {
	UpstreamProtocol string                          `json:"upstreamProtocol"`
	TargetPort       int                             `json:"targetPort"`
	Members          []rawPublicRouteBackendMemberV2 `json:"members"`
}

type rawPublicRouteBackendMemberV2 struct {
	SiteRef     string `json:"siteRef"`
	NodeRef     string `json:"nodeRef"`
	InstanceRef string `json:"instanceRef"`
}

type rawPublicRouteAccessV2 struct {
	Exposure               string   `json:"exposure"`
	PolicyExposure         string   `json:"policyExposure"`
	Authentication         string   `json:"authentication"`
	Privilege              string   `json:"privilege"`
	EnrolledDeviceRequired bool     `json:"enrolledDeviceRequired"`
	OwnerStepUpRequired    bool     `json:"ownerStepUpRequired"`
	LANStepDown            bool     `json:"lanStepDown"`
	AllowedSiteRefs        []string `json:"allowedSiteRefs,omitempty"`
	AllowedMethods         []string `json:"allowedMethods,omitempty"`
	DefaultClosed          bool     `json:"defaultClosed"`
	PolicyRef              string   `json:"policyRef"`
}

type rawPublicRouteTLSV2 struct {
	Required           bool   `json:"required"`
	Mode               string `json:"mode"`
	MinVersion         string `json:"minVersion,omitempty"`
	ProfileRef         string `json:"profileRef,omitempty"`
	IssuerRef          string `json:"issuerRef,omitempty"`
	OwnerCapabilityRef string `json:"ownerCapabilityRef,omitempty"`
}

type rawModuleRuntimeImage struct {
	Ref    string              `json:"ref"`
	Digest optionalStringField `json:"digest,omitempty"`
}

type moduleRuntimeContract struct {
	execution, kind, delivery, engine, imageRef, imageDigest, entryComponentRef string
	componentsCanonical                                                         []byte
}

type rawRenderUnitInstance struct {
	ID                string                     `json:"id"`
	Scope             string                     `json:"scope"`
	SiteRef           optionalStringField        `json:"siteRef,omitempty"`
	NodeRef           optionalStringField        `json:"nodeRef,omitempty"`
	DaemonRef         optionalStringField        `json:"daemonRef,omitempty"`
	DaemonInstanceRef optionalStringField        `json:"daemonInstanceRef,omitempty"`
	DaemonEngine      optionalStringField        `json:"daemonEngine,omitempty"`
	DaemonSocketPath  optionalStringField        `json:"daemonSocketPath,omitempty"`
	Outputs           []rawRenderInstanceOutput  `json:"outputs"`
	NetworkBindings   []rawRuntimeNetworkBinding `json:"networkBindings"`
}

type rawRuntimeNetworkOwner struct {
	ModuleRef    string `json:"moduleRef"`
	UnitRef      string `json:"unitRef"`
	InstanceRef  string `json:"instanceRef"`
	InterfaceRef string `json:"interfaceRef"`
}

type rawRuntimeNetworkMember struct {
	Role         string `json:"role"`
	ModuleRef    string `json:"moduleRef"`
	UnitRef      string `json:"unitRef"`
	InstanceRef  string `json:"instanceRef"`
	InterfaceRef string `json:"interfaceRef"`
}

type rawRuntimeNetwork struct {
	ID                string                    `json:"id"`
	NetworkRef        string                    `json:"networkRef"`
	SiteRef           string                    `json:"siteRef"`
	NodeRef           string                    `json:"nodeRef"`
	DaemonRef         string                    `json:"daemonRef"`
	DaemonInstanceRef string                    `json:"daemonInstanceRef"`
	Owner             rawRuntimeNetworkOwner    `json:"owner"`
	Members           []rawRuntimeNetworkMember `json:"members"`
}

type rawRuntimeNetworkBinding struct {
	NetworkInstanceRef string                 `json:"networkInstanceRef"`
	NetworkRef         string                 `json:"networkRef"`
	Role               string                 `json:"role"`
	InterfaceRef       string                 `json:"interfaceRef"`
	SiteRef            string                 `json:"siteRef"`
	NodeRef            string                 `json:"nodeRef"`
	DaemonRef          string                 `json:"daemonRef"`
	DaemonInstanceRef  string                 `json:"daemonInstanceRef"`
	Owner              rawRuntimeNetworkOwner `json:"owner"`
}

type rawImplementationProviderBinding struct {
	InterfaceRef        string `json:"interfaceRef"`
	ModuleRef           string `json:"moduleRef"`
	UnitRef             string `json:"unitRef"`
	ProviderInstanceRef string `json:"providerInstanceRef"`
	ConsumerInstanceRef string `json:"consumerInstanceRef"`
	SiteRef             string `json:"siteRef"`
	NodeRef             string `json:"nodeRef"`
	DaemonRef           string `json:"daemonRef"`
	DaemonInstanceRef   string `json:"daemonInstanceRef"`
	NetworkRef          string `json:"networkRef"`
	NetworkInstanceRef  string `json:"networkInstanceRef"`
	PolicyProfile       string `json:"policyProfile"`
	EndpointRef         string `json:"endpointRef"`
}

type rawRenderInstanceOutput struct {
	Ref         string `json:"ref"`
	ArtifactRef string `json:"artifactRef"`
	Path        string `json:"path"`
}

type rawRenderUnitPlacement struct {
	Scope       string              `json:"scope"`
	Cardinality string              `json:"cardinality"`
	DaemonRef   optionalStringField `json:"daemonRef,omitempty"`
}

type rawRuntimeDaemonBinding struct {
	SiteRef     string `json:"siteRef"`
	NodeRef     string `json:"nodeRef"`
	DaemonRef   string `json:"daemonRef"`
	InstanceRef string `json:"instanceRef"`
	Engine      string `json:"engine"`
	SocketPath  string `json:"socketPath"`
}

type rawOutputBinding struct {
	ArtifactRef string `json:"artifactRef"`
	UnitRef     string `json:"unitRef"`
	OutputRef   string `json:"outputRef"`
}

type rawArtifact struct {
	ID       string          `json:"id"`
	Path     string          `json:"path"`
	Kind     string          `json:"kind"`
	Format   string          `json:"format"`
	Mode     string          `json:"mode"`
	Required *bool           `json:"required"`
	Owner    json.RawMessage `json:"owner"`
}

type rawArtifactOwner struct {
	Kind        string              `json:"kind"`
	ModuleRef   optionalStringField `json:"moduleRef,omitempty"`
	UnitRef     optionalStringField `json:"unitRef,omitempty"`
	InstanceRef optionalStringField `json:"instanceRef,omitempty"`
	OutputRef   optionalStringField `json:"outputRef,omitempty"`
}

type rawPrivilegedInterfaceApproval struct {
	ID              string   `json:"id"`
	Kind            string   `json:"kind"`
	ModuleRef       string   `json:"moduleRef"`
	UnitRef         string   `json:"unitRef"`
	ProviderRef     string   `json:"providerRef"`
	DaemonRef       string   `json:"daemonRef"`
	PolicyProfile   string   `json:"policyProfile"`
	ReasonCode      string   `json:"reasonCode"`
	EvidenceRef     string   `json:"evidenceRef"`
	SiteRefs        []string `json:"siteRefs"`
	NodeRefs        []string `json:"nodeRefs"`
	EvidenceGateRef string   `json:"evidenceGateRef"`
}

type privilegedInterfaceApproval struct {
	raw       json.RawMessage
	contract  rawPrivilegedInterfaceApproval
	boundUnit bool
}

type optionalStringField struct {
	present bool
	value   string
}

func (field *optionalStringField) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("must be a string, not null")
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	field.present = true
	field.value = value
	return nil
}

func parseVerifiedPlan(plan generationartifact.VerifiedPlan) (renderPlan, error) {
	canonical := plan.Canonical()
	projection, err := parsePlanCanonical(canonical)
	if err != nil {
		return renderPlan{}, err
	}
	if projection.outputRoot != plan.OutputRoot() {
		return renderPlan{}, fail(ErrInvalidPlan, "resolvedPlan.generation.outputRoot", "projection does not match the verified plan output root")
	}
	return projection, nil
}

// parsePlanCanonical is deliberately unexported. Production callers can only
// enter through parseVerifiedPlan with generationartifact.VerifiedPlan; unit
// tests use this byte-level seam to exercise fail-closed lowering before a
// generation-ready authority fixture exists.
func parsePlanCanonical(canonical []byte) (renderPlan, error) {
	top, err := decodeRawObject(canonical, "resolvedPlan")
	if err != nil {
		return renderPlan{}, err
	}
	generation, err := requiredRawObject(top, "generation", "resolvedPlan.generation")
	if err != nil {
		return renderPlan{}, err
	}
	outputRoot, err := requiredRawString(generation, "outputRoot", "resolvedPlan.generation.outputRoot")
	if err != nil {
		return renderPlan{}, err
	}
	if outputRoot != "." {
		if _, err := validatePortablePath(outputRoot); err != nil {
			return renderPlan{}, wrap(ErrInvalidPath, "resolvedPlan.generation.outputRoot", "invalid managed output root", err)
		}
	}

	artifacts, err := parseArtifacts(generation, outputRoot)
	if err != nil {
		return renderPlan{}, err
	}
	nodeSites, err := parseNodeSiteIndex(top)
	if err != nil {
		return renderPlan{}, err
	}
	runtimeNetworks, err := parseRuntimeNetworks(top, nodeSites)
	if err != nil {
		return renderPlan{}, err
	}
	rawModules, err := requiredRawArray(top, "modules", "resolvedPlan.modules")
	if err != nil {
		return renderPlan{}, err
	}
	if len(rawModules) == 0 {
		return renderPlan{}, fail(ErrInvalidPlan, "resolvedPlan.modules", "at least one renderable module is required")
	}

	result := renderPlan{
		outputRoot: outputRoot,
		artifacts:  artifacts,
		bindings:   make(map[instanceOutputKey]outputBinding),
	}
	moduleIDs := make(map[string]struct{}, len(rawModules))
	boundArtifacts := make(map[string]instanceOutputKey)
	for moduleIndex, rawModule := range rawModules {
		modulePath := fmt.Sprintf("resolvedPlan.modules[%d]", moduleIndex)
		module, bindings, err := parseModule(rawModule, modulePath, artifacts, outputRoot)
		if err != nil {
			return renderPlan{}, err
		}
		if err := validateModuleInstanceNodeSites(module, nodeSites, modulePath); err != nil {
			return renderPlan{}, err
		}
		if _, exists := moduleIDs[module.id]; exists {
			return renderPlan{}, fail(ErrDuplicate, modulePath+".id", "duplicate module ID %q", module.id)
		}
		moduleIDs[module.id] = struct{}{}
		for _, binding := range bindings {
			key := instanceOutputKey{moduleID: module.id, unitID: binding.unitRef, instanceID: binding.instanceRef, output: binding.outputRef}
			if _, exists := result.bindings[key]; exists {
				return renderPlan{}, fail(ErrDuplicate, modulePath+".renderUnits.instances.outputs", "instance output %s/%s/%s is bound more than once", binding.unitRef, binding.instanceRef, binding.outputRef)
			}
			if previous, exists := boundArtifacts[binding.artifactID]; exists {
				return renderPlan{}, fail(ErrDuplicate, modulePath+".renderUnits.instances.outputs", "artifact %q is already owned by %s/%s/%s/%s", binding.artifactID, previous.moduleID, previous.unitID, previous.instanceID, previous.output)
			}
			boundArtifacts[binding.artifactID] = key
			result.bindings[key] = binding
		}
		result.modules = append(result.modules, module)
	}

	return finalizeRenderPlan(top, result, artifacts, boundArtifacts, runtimeNetworks)
}

func finalizeRenderPlan(top map[string]json.RawMessage, result renderPlan, artifacts map[string]artifactContract, boundArtifacts map[string]instanceOutputKey, runtimeNetworks []runtimeNetwork) (renderPlan, error) {
	if err := validateBoundArtifacts(artifacts, boundArtifacts); err != nil {
		return renderPlan{}, err
	}
	if err := validateRuntimeNetworkGraph(result.modules, runtimeNetworks); err != nil {
		return renderPlan{}, err
	}
	if err := bindPlanPrivilegedInterfaceApprovals(top, result.modules); err != nil {
		return renderPlan{}, err
	}
	sort.Slice(result.modules, func(i, j int) bool { return result.modules[i].id < result.modules[j].id })
	return result, nil
}

func bindPlanPrivilegedInterfaceApprovals(top map[string]json.RawMessage, modules []renderModule) error {
	approvals, present, err := parsePrivilegedInterfaceApprovals(top)
	if err != nil {
		return err
	}
	if !present {
		bindEmptyPrivilegedInterfaceApprovals(modules)
		return nil
	}
	return bindPrivilegedInterfaceApprovals(modules, approvals)
}

func parsePrivilegedInterfaceApprovals(top map[string]json.RawMessage) ([]privilegedInterfaceApproval, bool, error) {
	raw, present := top["privilegedInterfaceApprovals"]
	if !present {
		return nil, false, nil
	}
	var values []json.RawMessage
	if err := decodeJSON(raw, &values, false); err != nil || values == nil {
		if err == nil {
			err = fmt.Errorf("must be an array")
		}
		return nil, true, wrap(ErrInvalidPlan, "resolvedPlan.privilegedInterfaceApprovals", "decode approvals", err)
	}
	result := make([]privilegedInterfaceApproval, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		approvalPath := fmt.Sprintf("resolvedPlan.privilegedInterfaceApprovals[%d]", index)
		var decoded rawPrivilegedInterfaceApproval
		if err := decodeStrict(value, &decoded); err != nil {
			return nil, true, wrap(ErrInvalidPlan, approvalPath, "decode approval", err)
		}
		for _, field := range []struct{ name, value string }{
			{"id", decoded.ID}, {"moduleRef", decoded.ModuleRef}, {"unitRef", decoded.UnitRef},
			{"providerRef", decoded.ProviderRef}, {"daemonRef", decoded.DaemonRef},
			{"policyProfile", decoded.PolicyProfile}, {"evidenceGateRef", decoded.EvidenceGateRef},
		} {
			if err := requireContractID(field.value, approvalPath+"."+field.name); err != nil {
				return nil, true, err
			}
		}
		if decoded.Kind != dockerSocketDirectInterfaceKind {
			return nil, true, fail(ErrInvalidPlan, approvalPath+".kind", "must be %q", dockerSocketDirectInterfaceKind)
		}
		if decoded.ReasonCode != "provider-backing" && decoded.ReasonCode != "lifecycle-owner" {
			return nil, true, fail(ErrInvalidPlan, approvalPath+".reasonCode", "unsupported direct-socket approval reason")
		}
		if strings.TrimSpace(decoded.EvidenceRef) == "" {
			return nil, true, fail(ErrInvalidPlan, approvalPath+".evidenceRef", "must be non-empty")
		}
		if len(decoded.SiteRefs) == 0 || len(decoded.NodeRefs) == 0 {
			return nil, true, fail(ErrInvalidPlan, approvalPath, "siteRefs and nodeRefs must contain exact approved placement")
		}
		if _, err := uniqueContractIDSet(decoded.SiteRefs, approvalPath+".siteRefs"); err != nil {
			return nil, true, err
		}
		if _, err := uniqueContractIDSet(decoded.NodeRefs, approvalPath+".nodeRefs"); err != nil {
			return nil, true, err
		}
		if _, duplicate := seen[decoded.ID]; duplicate {
			return nil, true, fail(ErrDuplicate, approvalPath+".id", "duplicate privileged approval %q", decoded.ID)
		}
		seen[decoded.ID] = struct{}{}
		result = append(result, privilegedInterfaceApproval{raw: append([]byte(nil), value...), contract: decoded})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].contract.ID < result[j].contract.ID })
	return result, true, nil
}

func bindPrivilegedInterfaceApprovals(modules []renderModule, approvals []privilegedInterfaceApproval) error {
	for moduleIndex := range modules {
		for unitIndex := range modules[moduleIndex].units {
			unit := &modules[moduleIndex].units[unitIndex]
			bound := make([]json.RawMessage, 0)
			for approvalIndex := range approvals {
				approval := &approvals[approvalIndex]
				if approval.contract.ModuleRef != modules[moduleIndex].id || approval.contract.UnitRef != unit.id {
					continue
				}
				approval.boundUnit = true
				bound = append(bound, approval.raw)
			}
			canonical, err := json.Marshal(bound)
			if err != nil {
				return wrap(ErrInvalidPlan, "resolvedPlan.privilegedInterfaceApprovals", "canonicalize unit approvals", err)
			}
			unit.privilegedApprovalsCanonical = canonical
		}
	}
	for _, approval := range approvals {
		if !approval.boundUnit {
			return fail(ErrInvalidPlan, "resolvedPlan.privilegedInterfaceApprovals."+approval.contract.ID, "approval references an unknown render unit")
		}
	}
	return nil
}

func bindEmptyPrivilegedInterfaceApprovals(modules []renderModule) {
	for moduleIndex := range modules {
		for unitIndex := range modules[moduleIndex].units {
			modules[moduleIndex].units[unitIndex].privilegedApprovalsCanonical = []byte("[]")
		}
	}
}

func validateBoundArtifacts(artifacts map[string]artifactContract, boundArtifacts map[string]instanceOutputKey) error {
	for artifactID, artifact := range artifacts {
		if artifact.owner.kind == "plan" {
			if artifactID != "resolved-plan" {
				return fail(ErrInvalidPlan, "resolvedPlan.generation.artifacts", "plan-owned artifact %q has no governed renderer producer", artifactID)
			}
			continue
		}
		if _, exists := boundArtifacts[artifactID]; !exists {
			return fail(ErrInvalidPlan, "resolvedPlan.generation.artifacts", "render-instance artifact %q has no exact instance output binding", artifactID)
		}
	}
	return nil
}

func parseNodeSiteIndex(top map[string]json.RawMessage) (map[string]string, error) {
	rawNodes, err := requiredRawArray(top, "nodes", "resolvedPlan.nodes")
	if err != nil {
		return nil, err
	}
	if len(rawNodes) == 0 {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.nodes", "at least one resolved node is required")
	}
	nodeSites := make(map[string]string, len(rawNodes))
	for index, rawNode := range rawNodes {
		nodePath := fmt.Sprintf("resolvedPlan.nodes[%d]", index)
		node, err := decodeRawObject(rawNode, nodePath)
		if err != nil {
			return nil, err
		}
		nodeRef, err := requiredRawString(node, "id", nodePath+".id")
		if err != nil {
			return nil, err
		}
		if err := requireContractID(nodeRef, nodePath+".id"); err != nil {
			return nil, err
		}
		siteRef, err := requiredRawString(node, "siteRef", nodePath+".siteRef")
		if err != nil {
			return nil, err
		}
		if err := requireContractID(siteRef, nodePath+".siteRef"); err != nil {
			return nil, err
		}
		if _, exists := nodeSites[nodeRef]; exists {
			return nil, fail(ErrDuplicate, nodePath+".id", "duplicate top-level node ID %q", nodeRef)
		}
		nodeSites[nodeRef] = siteRef
	}
	return nodeSites, nil
}

func validateModuleInstanceNodeSites(module renderModule, nodeSites map[string]string, modulePath string) error {
	for _, unit := range module.units {
		for _, instance := range unit.instances {
			if instance.scope != "node-local" {
				continue
			}
			instancePath := fmt.Sprintf("%s.renderUnits.%s.instances.%s", modulePath, unit.id, instance.id)
			siteRef, exists := nodeSites[instance.nodeRef]
			if !exists {
				return fail(ErrInvalidPlan, instancePath+".nodeRef", "references node %q outside top-level resolved nodes", instance.nodeRef)
			}
			if instance.siteRef != siteRef {
				return fail(ErrInvalidPlan, instancePath+".siteRef", "site %q does not match top-level node %q siteRef %q", instance.siteRef, instance.nodeRef, siteRef)
			}
		}
	}
	return nil
}

func parseRuntimeNetworks(top map[string]json.RawMessage, nodeSites map[string]string) ([]runtimeNetwork, error) {
	rawNetworks, err := requiredRawArray(top, "runtimeNetworks", "resolvedPlan.runtimeNetworks")
	if err != nil {
		return nil, err
	}
	result := make([]runtimeNetwork, 0, len(rawNetworks))
	seenIDs := make(map[string]struct{}, len(rawNetworks))
	for index, raw := range rawNetworks {
		networkPath := fmt.Sprintf("resolvedPlan.runtimeNetworks[%d]", index)
		network, err := parseRuntimeNetwork(raw, networkPath, nodeSites)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seenIDs[network.id]; duplicate {
			return nil, fail(ErrDuplicate, networkPath+".id", "duplicate runtime network ID %q", network.id)
		}
		seenIDs[network.id] = struct{}{}
		result = append(result, network)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result, nil
}

func parseRuntimeNetwork(raw json.RawMessage, networkPath string, nodeSites map[string]string) (runtimeNetwork, error) {
	var decoded rawRuntimeNetwork
	if err := decodeStrict(raw, &decoded); err != nil {
		return runtimeNetwork{}, wrap(ErrInvalidPlan, networkPath, "decode runtime network", err)
	}
	fields := []struct{ name, value string }{
		{name: "id", value: decoded.ID}, {name: "networkRef", value: decoded.NetworkRef},
		{name: "siteRef", value: decoded.SiteRef}, {name: "nodeRef", value: decoded.NodeRef},
		{name: "daemonRef", value: decoded.DaemonRef}, {name: "daemonInstanceRef", value: decoded.DaemonInstanceRef},
	}
	for _, field := range fields {
		if err := requireContractID(field.value, networkPath+"."+field.name); err != nil {
			return runtimeNetwork{}, err
		}
	}
	if siteRef, exists := nodeSites[decoded.NodeRef]; !exists || siteRef != decoded.SiteRef {
		return runtimeNetwork{}, fail(ErrInvalidPlan, networkPath+".nodeRef", "runtime network node and site must match exact top-level node locality")
	}
	owner, err := validateRuntimeNetworkOwner(decoded.Owner, networkPath+".owner")
	if err != nil {
		return runtimeNetwork{}, err
	}
	wantID := owner.instanceRef + "-network-" + decoded.NetworkRef + "-interface-" + owner.interfaceRef
	if decoded.ID != wantID {
		return runtimeNetwork{}, fail(ErrInvalidPlan, networkPath+".id", "must use exact provider-owned ID %q", wantID)
	}
	members, err := parseRuntimeNetworkMembers(decoded.Members, owner, networkPath+".members")
	if err != nil {
		return runtimeNetwork{}, err
	}
	return runtimeNetwork{
		id: decoded.ID, networkRef: decoded.NetworkRef, siteRef: decoded.SiteRef,
		nodeRef: decoded.NodeRef, daemonRef: decoded.DaemonRef, daemonInstanceRef: decoded.DaemonInstanceRef,
		owner: owner, members: members,
	}, nil
}

func parseRuntimeNetworkMembers(raw []rawRuntimeNetworkMember, owner runtimeNetworkOwner, valuePath string) ([]runtimeNetworkMember, error) {
	if len(raw) == 0 {
		return nil, fail(ErrInvalidPlan, valuePath, "must contain the provider owner member")
	}
	members := make([]runtimeNetworkMember, 0, len(raw))
	seen := make(map[runtimeNetworkMember]struct{}, len(raw))
	providerCount, ownerFound := 0, false
	for index, decoded := range raw {
		memberPath := fmt.Sprintf("%s[%d]", valuePath, index)
		member, err := parseRuntimeNetworkMember(decoded, memberPath)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[member]; duplicate {
			return nil, fail(ErrDuplicate, memberPath, "runtime network member is duplicated")
		}
		seen[member] = struct{}{}
		if member.role == "provider" {
			providerCount++
			ownerFound = ownerFound || ownerMatchesMember(owner, member)
		}
		members = append(members, member)
	}
	if providerCount != 1 || !ownerFound {
		return nil, fail(ErrInvalidPlan, valuePath, "owner must exactly identify the sole provider member")
	}
	sort.Slice(members, func(i, j int) bool { return runtimeNetworkMemberLess(members[i], members[j]) })
	return members, nil
}

func parseRuntimeNetworkMember(raw rawRuntimeNetworkMember, valuePath string) (runtimeNetworkMember, error) {
	if raw.Role != "provider" && raw.Role != "consumer" {
		return runtimeNetworkMember{}, fail(ErrInvalidPlan, valuePath+".role", "must be provider or consumer")
	}
	fields := []struct{ name, value string }{
		{name: "moduleRef", value: raw.ModuleRef}, {name: "unitRef", value: raw.UnitRef},
		{name: "instanceRef", value: raw.InstanceRef}, {name: "interfaceRef", value: raw.InterfaceRef},
	}
	for _, field := range fields {
		if err := requireContractID(field.value, valuePath+"."+field.name); err != nil {
			return runtimeNetworkMember{}, err
		}
	}
	return runtimeNetworkMember{
		role: raw.Role, moduleRef: raw.ModuleRef, unitRef: raw.UnitRef,
		instanceRef: raw.InstanceRef, interfaceRef: raw.InterfaceRef,
	}, nil
}

func validateRuntimeNetworkGraph(modules []renderModule, networks []runtimeNetwork) error {
	instances, interfaces := indexRenderInstancesAndInterfaces(modules)
	networkIndex, memberIndex, err := indexAndValidateRuntimeNetworkMembers(networks, instances, interfaces)
	if err != nil {
		return err
	}
	if err := validateRuntimeNetworkProviderCompleteness(instances, interfaces, networkIndex); err != nil {
		return err
	}
	if err := validateReciprocalRuntimeNetworkBindings(instances, networkIndex, memberIndex); err != nil {
		return err
	}
	return validateImplementationProviderBindingGraph(modules, instances, interfaces, networkIndex, memberIndex)
}

func indexRenderInstancesAndInterfaces(modules []renderModule) (map[renderInstanceIdentity]renderInstanceContext, map[interfaceIdentity]implementationInterface) {
	instances := make(map[renderInstanceIdentity]renderInstanceContext)
	interfaces := make(map[interfaceIdentity]implementationInterface)
	for _, module := range modules {
		for _, unit := range module.units {
			for _, instance := range unit.instances {
				identity := renderInstanceIdentity{moduleRef: module.id, unitRef: unit.id, instanceRef: instance.id}
				instances[identity] = renderInstanceContext{unit: unit, instance: instance}
				for _, contract := range unit.providedInterfaces {
					interfaces[interfaceIdentity{instance: identity, role: "provider", ref: contract.id}] = contract
				}
				for _, contract := range unit.requiredInterfaces {
					interfaces[interfaceIdentity{instance: identity, role: "consumer", ref: contract.id}] = contract
				}
			}
		}
	}
	return instances, interfaces
}

func indexAndValidateRuntimeNetworkMembers(networks []runtimeNetwork, instances map[renderInstanceIdentity]renderInstanceContext, interfaces map[interfaceIdentity]implementationInterface) (map[string]runtimeNetwork, map[networkMembershipIdentity]struct{}, error) {
	networkIndex := make(map[string]runtimeNetwork, len(networks))
	memberIndex := make(map[networkMembershipIdentity]struct{})
	for _, network := range networks {
		networkIndex[network.id] = network
		for _, member := range network.members {
			memberIndex[networkMembershipIdentity{networkRef: network.id, member: member}] = struct{}{}
			context, exists := instances[memberInstanceIdentity(member)]
			if !exists {
				return nil, nil, fail(ErrInvalidPlan, "resolvedPlan.runtimeNetworks."+network.id+".members", "member references an unknown render instance")
			}
			contract, exists := interfaces[interfaceIdentity{instance: memberInstanceIdentity(member), role: member.role, ref: member.interfaceRef}]
			if !exists {
				return nil, nil, fail(ErrInvalidPlan, "resolvedPlan.runtimeNetworks."+network.id+".members", "member references an undeclared %s interface", member.role)
			}
			if err := validateNetworkMemberLocality(network, member, context.instance, contract); err != nil {
				return nil, nil, err
			}
		}
	}
	return networkIndex, memberIndex, nil
}

func validateRuntimeNetworkProviderCompleteness(instances map[renderInstanceIdentity]renderInstanceContext, interfaces map[interfaceIdentity]implementationInterface, networks map[string]runtimeNetwork) error {
	providers := make([]interfaceIdentity, 0)
	for identity := range interfaces {
		if identity.role == "provider" {
			providers = append(providers, identity)
		}
	}
	sort.Slice(providers, func(i, j int) bool {
		left := providers[i].instance.moduleRef + "\x00" + providers[i].instance.unitRef + "\x00" + providers[i].instance.instanceRef + "\x00" + providers[i].ref
		right := providers[j].instance.moduleRef + "\x00" + providers[j].instance.unitRef + "\x00" + providers[j].instance.instanceRef + "\x00" + providers[j].ref
		return left < right
	})

	for _, identity := range providers {
		context := instances[identity.instance]
		contract := interfaces[identity]
		owner := runtimeNetworkOwner{
			moduleRef: identity.instance.moduleRef, unitRef: identity.instance.unitRef,
			instanceRef: identity.instance.instanceRef, interfaceRef: identity.ref,
		}
		matches := 0
		if context.instance.scope == "node-local" {
			for _, network := range networks {
				if network.owner == owner && network.networkRef == contract.networkRef &&
					network.siteRef == context.instance.siteRef && network.nodeRef == context.instance.nodeRef &&
					network.daemonRef == context.instance.daemonRef && network.daemonRef == contract.daemonRef &&
					network.daemonInstanceRef == context.instance.daemonInstanceRef {
					matches++
				}
			}
		}
		if matches != 1 {
			path := "resolvedPlan.modules." + identity.instance.moduleRef + ".renderUnits." + identity.instance.unitRef + ".instances." + identity.instance.instanceRef + ".networkBindings"
			return fail(ErrInvalidPlan, path, "provider interface %q must own exactly one exact runtime network; found %d", identity.ref, matches)
		}
	}
	return nil
}

func validateReciprocalRuntimeNetworkBindings(instances map[renderInstanceIdentity]renderInstanceContext, networkIndex map[string]runtimeNetwork, memberIndex map[networkMembershipIdentity]struct{}) error {
	reciprocal := make(map[networkMembershipIdentity]struct{})
	for identity, context := range instances {
		for _, binding := range context.instance.networkBindings {
			network, exists := networkIndex[binding.networkInstanceRef]
			if !exists {
				return fail(ErrInvalidPlan, "resolvedPlan.modules."+identity.moduleRef+".renderUnits."+identity.unitRef+".instances."+identity.instanceRef+".networkBindings", "references unknown runtime network %q", binding.networkInstanceRef)
			}
			member := runtimeNetworkMember{
				role: binding.role, moduleRef: identity.moduleRef, unitRef: identity.unitRef,
				instanceRef: identity.instanceRef, interfaceRef: binding.interfaceRef,
			}
			membership := networkMembershipIdentity{networkRef: network.id, member: member}
			if _, exists := memberIndex[membership]; !exists {
				return fail(ErrInvalidPlan, "resolvedPlan.modules."+identity.moduleRef+".renderUnits."+identity.unitRef+".instances."+identity.instanceRef+".networkBindings", "has no exact reciprocal runtime network member")
			}
			if _, duplicate := reciprocal[membership]; duplicate {
				return fail(ErrDuplicate, "resolvedPlan.modules."+identity.moduleRef+".renderUnits."+identity.unitRef+".instances."+identity.instanceRef+".networkBindings", "runtime network member is bound more than once")
			}
			if binding.networkRef != network.networkRef || binding.siteRef != network.siteRef || binding.nodeRef != network.nodeRef ||
				binding.daemonRef != network.daemonRef || binding.daemonInstanceRef != network.daemonInstanceRef || binding.owner != network.owner {
				return fail(ErrInvalidPlan, "resolvedPlan.modules."+identity.moduleRef+".renderUnits."+identity.unitRef+".instances."+identity.instanceRef+".networkBindings", "binding attributes or owner do not exactly match runtime network %q", network.id)
			}
			reciprocal[membership] = struct{}{}
		}
	}
	for membership := range memberIndex {
		if _, exists := reciprocal[membership]; !exists {
			return fail(ErrInvalidPlan, "resolvedPlan.runtimeNetworks."+membership.networkRef+".members", "member has no exact reciprocal instance network binding")
		}
	}
	return nil
}

func validateNetworkMemberLocality(network runtimeNetwork, member runtimeNetworkMember, instance renderUnitInstance, contract implementationInterface) error {
	memberPath := "resolvedPlan.runtimeNetworks." + network.id + ".members"
	if instance.scope != "node-local" || instance.siteRef != network.siteRef || instance.nodeRef != network.nodeRef {
		return fail(ErrInvalidPlan, memberPath, "member render instance does not exactly share runtime network site and node")
	}
	if contract.networkRef != network.networkRef || contract.daemonRef != network.daemonRef {
		return fail(ErrInvalidPlan, memberPath, "member interface does not exactly match runtime network and daemon")
	}
	if member.role == "provider" {
		if instance.daemonRef != network.daemonRef || instance.daemonInstanceRef != network.daemonInstanceRef || !ownerMatchesMember(network.owner, member) {
			return fail(ErrInvalidPlan, memberPath, "provider instance does not exactly own runtime network daemon locality")
		}
	} else if instance.daemonRef != "" && (instance.daemonRef != network.daemonRef || instance.daemonInstanceRef != network.daemonInstanceRef) {
		return fail(ErrInvalidPlan, memberPath, "daemon-bound consumer does not share provider daemon instance")
	}
	return nil
}

func validateImplementationProviderBindingGraph(modules []renderModule, instances map[renderInstanceIdentity]renderInstanceContext, interfaces map[interfaceIdentity]implementationInterface, networks map[string]runtimeNetwork, members map[networkMembershipIdentity]struct{}) error {
	coveredConsumers := make(map[networkMembershipIdentity]struct{})
	for _, module := range modules {
		for _, unit := range module.units {
			for _, required := range unit.requiredInterfaces {
				boundConsumerInstances := make(map[string]struct{}, len(required.providerBindings))
				for _, binding := range required.providerBindings {
					bindingPath := "resolvedPlan.modules." + module.id + ".renderUnits." + unit.id + ".requiresInterfaces." + required.id + ".providerBindings"
					if _, duplicate := boundConsumerInstances[binding.consumerInstanceRef]; duplicate {
						return fail(ErrDuplicate, bindingPath, "consumer instance %q has more than one provider network binding", binding.consumerInstanceRef)
					}
					boundConsumerInstances[binding.consumerInstanceRef] = struct{}{}
					consumerMembership, err := validateImplementationProviderBinding(
						module.id, unit.id, required, binding, bindingPath,
						instances, interfaces, networks, members,
					)
					if err != nil {
						return err
					}
					if _, duplicate := coveredConsumers[consumerMembership]; duplicate {
						return fail(ErrDuplicate, bindingPath, "consumer network member has more than one provider binding")
					}
					coveredConsumers[consumerMembership] = struct{}{}
				}
				if required.kind == "docker-http-readonly-v1" {
					if len(boundConsumerInstances) != len(unit.instances) {
						return fail(ErrInvalidPlan, "resolvedPlan.modules."+module.id+".renderUnits."+unit.id+".requiresInterfaces."+required.id+".providerBindings", "must bind every exact consumer render instance exactly once")
					}
					for _, instance := range unit.instances {
						if _, exists := boundConsumerInstances[instance.id]; !exists {
							return fail(ErrInvalidPlan, "resolvedPlan.modules."+module.id+".renderUnits."+unit.id+".requiresInterfaces."+required.id+".providerBindings", "consumer instance %q has no exact provider network binding", instance.id)
						}
					}
				}
			}
		}
	}
	for membership := range members {
		if membership.member.role == "consumer" {
			if _, exists := coveredConsumers[membership]; !exists {
				return fail(ErrInvalidPlan, "resolvedPlan.runtimeNetworks."+membership.networkRef+".members", "consumer member has no exact required-interface provider binding")
			}
		}
	}
	return nil
}

func validateImplementationProviderBinding(moduleID, unitID string, required implementationInterface, binding implementationProviderBinding, bindingPath string, instances map[renderInstanceIdentity]renderInstanceContext, interfaces map[interfaceIdentity]implementationInterface, networks map[string]runtimeNetwork, members map[networkMembershipIdentity]struct{}) (networkMembershipIdentity, error) {
	consumerID := renderInstanceIdentity{moduleRef: moduleID, unitRef: unitID, instanceRef: binding.consumerInstanceRef}
	consumer, exists := instances[consumerID]
	if !exists {
		return networkMembershipIdentity{}, fail(ErrInvalidPlan, bindingPath, "consumerInstanceRef does not identify an instance of the required interface unit")
	}
	providerID := renderInstanceIdentity{moduleRef: binding.moduleRef, unitRef: binding.unitRef, instanceRef: binding.providerInstanceRef}
	provider, exists := instances[providerID]
	if !exists {
		return networkMembershipIdentity{}, fail(ErrInvalidPlan, bindingPath, "provider instance does not exist")
	}
	provided, exists := interfaces[interfaceIdentity{instance: providerID, role: "provider", ref: binding.interfaceRef}]
	if !exists {
		return networkMembershipIdentity{}, fail(ErrInvalidPlan, bindingPath, "provider interface does not exist on exact provider instance")
	}
	network, exists := networks[binding.networkInstanceRef]
	if !exists {
		return networkMembershipIdentity{}, fail(ErrInvalidPlan, bindingPath, "networkInstanceRef does not identify an exact runtime network")
	}
	owner := runtimeNetworkOwner{moduleRef: binding.moduleRef, unitRef: binding.unitRef, instanceRef: binding.providerInstanceRef, interfaceRef: binding.interfaceRef}
	if network.owner != owner || !providerBindingMatchesNetwork(binding, network) {
		return networkMembershipIdentity{}, fail(ErrInvalidPlan, bindingPath, "provider binding attributes do not exactly match provider-owned runtime network")
	}
	if !providerAndConsumerMatchBinding(provider.instance, consumer.instance, binding) {
		return networkMembershipIdentity{}, fail(ErrInvalidPlan, bindingPath, "provider and consumer instances do not share exact network locality")
	}
	if !providerAndConsumerInterfacesMatchBinding(provided, required, binding) {
		return networkMembershipIdentity{}, fail(ErrInvalidPlan, bindingPath, "provider binding does not match exact provider and consumer interface contracts")
	}
	providerMember := runtimeNetworkMember{role: "provider", moduleRef: binding.moduleRef, unitRef: binding.unitRef, instanceRef: binding.providerInstanceRef, interfaceRef: binding.interfaceRef}
	if _, exists := members[networkMembershipIdentity{networkRef: network.id, member: providerMember}]; !exists {
		return networkMembershipIdentity{}, fail(ErrInvalidPlan, bindingPath, "runtime network lacks exact provider member")
	}
	consumerMember := runtimeNetworkMember{role: "consumer", moduleRef: moduleID, unitRef: unitID, instanceRef: binding.consumerInstanceRef, interfaceRef: required.id}
	consumerMembership := networkMembershipIdentity{networkRef: network.id, member: consumerMember}
	if _, exists := members[consumerMembership]; !exists {
		return networkMembershipIdentity{}, fail(ErrInvalidPlan, bindingPath, "runtime network lacks exact consumer member")
	}
	return consumerMembership, nil
}

func providerBindingMatchesNetwork(binding implementationProviderBinding, network runtimeNetwork) bool {
	return binding.networkRef == network.networkRef && binding.siteRef == network.siteRef &&
		binding.nodeRef == network.nodeRef && binding.daemonRef == network.daemonRef &&
		binding.daemonInstanceRef == network.daemonInstanceRef
}

func providerAndConsumerMatchBinding(provider, consumer renderUnitInstance, binding implementationProviderBinding) bool {
	return provider.siteRef == binding.siteRef && provider.nodeRef == binding.nodeRef &&
		provider.daemonRef == binding.daemonRef && provider.daemonInstanceRef == binding.daemonInstanceRef &&
		consumer.siteRef == binding.siteRef && consumer.nodeRef == binding.nodeRef
}

func providerAndConsumerInterfacesMatchBinding(provided, required implementationInterface, binding implementationProviderBinding) bool {
	return provided.kind == "docker-http-readonly-v1" && required.kind == provided.kind &&
		provided.networkRef == binding.networkRef && provided.daemonRef == binding.daemonRef &&
		provided.policyProfile == binding.policyProfile && provided.endpointRef == binding.endpointRef &&
		required.networkRef == binding.networkRef && required.daemonRef == binding.daemonRef &&
		required.policyProfile == binding.policyProfile && required.endpointRef == binding.endpointRef
}

func memberInstanceIdentity(member runtimeNetworkMember) renderInstanceIdentity {
	return renderInstanceIdentity{moduleRef: member.moduleRef, unitRef: member.unitRef, instanceRef: member.instanceRef}
}

func ownerMatchesMember(owner runtimeNetworkOwner, member runtimeNetworkMember) bool {
	return owner.moduleRef == member.moduleRef && owner.unitRef == member.unitRef &&
		owner.instanceRef == member.instanceRef && owner.interfaceRef == member.interfaceRef
}

func runtimeNetworkMemberLess(left, right runtimeNetworkMember) bool {
	leftKey := left.role + "\x00" + left.moduleRef + "\x00" + left.unitRef + "\x00" + left.instanceRef + "\x00" + left.interfaceRef
	rightKey := right.role + "\x00" + right.moduleRef + "\x00" + right.unitRef + "\x00" + right.instanceRef + "\x00" + right.interfaceRef
	return leftKey < rightKey
}

func parseArtifacts(generation map[string]json.RawMessage, outputRoot string) (map[string]artifactContract, error) {
	rawArtifacts, err := requiredRawArray(generation, "artifacts", "resolvedPlan.generation.artifacts")
	if err != nil {
		return nil, err
	}
	if len(rawArtifacts) == 0 {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.generation.artifacts", "at least one governed artifact is required")
	}
	result := make(map[string]artifactContract, len(rawArtifacts))
	paths := make(map[string]string, len(rawArtifacts))
	resolvedPlanCount := 0
	for index, raw := range rawArtifacts {
		artifactPath := fmt.Sprintf("resolvedPlan.generation.artifacts[%d]", index)
		artifact, err := parseArtifactContract(raw, artifactPath)
		if err != nil {
			return nil, err
		}
		if _, exists := result[artifact.id]; exists {
			return nil, fail(ErrDuplicate, artifactPath+".id", "duplicate artifact ID %q", artifact.id)
		}
		pathKey := portableKey(artifact.path)
		if previous, exists := paths[pathKey]; exists {
			return nil, fail(ErrDuplicate, artifactPath+".path", "artifact path %q duplicates artifact %q", artifact.path, previous)
		}
		paths[pathKey] = artifact.id
		result[artifact.id] = artifact
		isResolvedPlan, err := validateArtifactGovernance(artifact, artifactPath, outputRoot)
		if err != nil {
			return nil, err
		}
		if isResolvedPlan {
			resolvedPlanCount++
		}
	}
	if resolvedPlanCount != 1 {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.generation.artifacts", "exactly one resolved-plan artifact is required")
	}
	return result, nil
}

func parseArtifactContract(raw json.RawMessage, artifactPath string) (artifactContract, error) {
	var decoded rawArtifact
	if err := decodeStrict(raw, &decoded); err != nil {
		return artifactContract{}, wrap(ErrInvalidPlan, artifactPath, "decode artifact contract", err)
	}
	if err := requireContractID(decoded.ID, artifactPath+".id"); err != nil {
		return artifactContract{}, err
	}
	if _, err := validatePortablePath(decoded.Path); err != nil {
		return artifactContract{}, wrap(ErrInvalidPath, artifactPath+".path", "invalid artifact path", err)
	}
	if decoded.Kind == "" || decoded.Format == "" || decoded.Mode == "" || decoded.Required == nil || decoded.Owner == nil {
		return artifactContract{}, fail(ErrInvalidPlan, artifactPath, "kind, format, mode, required, and owner are mandatory")
	}
	owner, err := parseArtifactOwner(decoded.Owner, artifactPath+".owner")
	if err != nil {
		return artifactContract{}, err
	}
	return artifactContract{
		id: decoded.ID, path: decoded.Path, kind: decoded.Kind, format: decoded.Format,
		mode: decoded.Mode, required: *decoded.Required, owner: owner,
	}, nil
}

func validateArtifactGovernance(artifact artifactContract, artifactPath, outputRoot string) (bool, error) {
	if artifact.id == "resolved-plan" {
		want := joinOutputPath(outputRoot, ".stackkit/resolved-plan.json")
		if !artifact.required || artifact.path != want || artifact.owner.kind != "plan" {
			return true, fail(ErrInvalidPlan, artifactPath, "resolved-plan must be a required plan-owned artifact at %q", want)
		}
		return true, nil
	}
	if artifact.owner.kind != "render-instance" {
		return false, fail(ErrInvalidPlan, artifactPath+".owner.kind", "non-plan artifact must be owned by one render instance")
	}
	return false, nil
}

func parseArtifactOwner(raw json.RawMessage, ownerPath string) (artifactOwner, error) {
	var decoded rawArtifactOwner
	if err := decodeStrict(raw, &decoded); err != nil {
		return artifactOwner{}, wrap(ErrInvalidPlan, ownerPath, "decode artifact owner", err)
	}
	switch decoded.Kind {
	case "plan":
		if decoded.ModuleRef.present || decoded.UnitRef.present || decoded.InstanceRef.present || decoded.OutputRef.present {
			return artifactOwner{}, fail(ErrInvalidPlan, ownerPath, "plan owner must not carry render-instance identity")
		}
		return artifactOwner{kind: decoded.Kind}, nil
	case "render-instance":
		fields := []struct {
			name  string
			value optionalStringField
		}{
			{name: "moduleRef", value: decoded.ModuleRef},
			{name: "unitRef", value: decoded.UnitRef},
			{name: "instanceRef", value: decoded.InstanceRef},
		}
		for _, field := range fields {
			if !field.value.present {
				return artifactOwner{}, fail(ErrInvalidPlan, ownerPath+"."+field.name, "is required for a render-instance owner")
			}
			if err := requireContractID(field.value.value, ownerPath+"."+field.name); err != nil {
				return artifactOwner{}, err
			}
		}
		if !decoded.OutputRef.present {
			return artifactOwner{}, fail(ErrInvalidPlan, ownerPath+".outputRef", "is required for a render-instance owner")
		}
		if _, err := validatePortablePath(decoded.OutputRef.value); err != nil {
			return artifactOwner{}, wrap(ErrInvalidPath, ownerPath+".outputRef", "invalid logical output reference", err)
		}
		return artifactOwner{
			kind: decoded.Kind, moduleRef: decoded.ModuleRef.value, unitRef: decoded.UnitRef.value,
			instanceRef: decoded.InstanceRef.value, outputRef: decoded.OutputRef.value,
		}, nil
	default:
		return artifactOwner{}, fail(ErrInvalidPlan, ownerPath+".kind", "must be plan or render-instance")
	}
}

type moduleOutputOwner struct{ unitID, outputRef string }

func parseModule(raw json.RawMessage, modulePath string, artifacts map[string]artifactContract, outputRoot string) (renderModule, []outputBinding, error) {
	object, err := decodeRawObject(raw, modulePath)
	if err != nil {
		return renderModule{}, nil, err
	}
	moduleID, err := requiredRawString(object, "id", modulePath+".id")
	if err != nil {
		return renderModule{}, nil, err
	}
	if err := requireContractID(moduleID, modulePath+".id"); err != nil {
		return renderModule{}, nil, err
	}
	runtime, err := parseModuleRuntime(object, modulePath)
	if err != nil {
		return renderModule{}, nil, err
	}
	module, unitIDs, outputs, err := parseModuleUnits(object, moduleID, modulePath)
	if err != nil {
		return renderModule{}, nil, err
	}
	for index := range module.units {
		module.units[index].runtime = runtime
	}
	logicalBindings, err := parseModuleBindings(object, module, unitIDs, outputs, modulePath)
	if err != nil {
		return renderModule{}, nil, err
	}
	bindings, err := bindModuleInstances(module, logicalBindings, artifacts, outputRoot, modulePath)
	return module, bindings, err
}

func parseModuleRuntime(object map[string]json.RawMessage, modulePath string) (moduleRuntimeContract, error) {
	raw, exists := object["runtime"]
	if !exists {
		return moduleRuntimeContract{}, fail(ErrInvalidPlan, modulePath+".runtime", "is required")
	}
	var decoded rawModuleRuntime
	if err := decodeStrict(raw, &decoded); err != nil {
		return moduleRuntimeContract{}, wrap(ErrInvalidPlan, modulePath+".runtime", "decode module runtime", err)
	}
	if !containsStringValue([]string{"container", "native", "host", "external", "control-plane"}, decoded.Kind) {
		return moduleRuntimeContract{}, fail(ErrInvalidPlan, modulePath+".runtime.kind", "unsupported module runtime kind %q", decoded.Kind)
	}
	if !containsStringValue([]string{"stackkit", "selected-paas", "external-control-plane"}, decoded.Delivery) {
		return moduleRuntimeContract{}, fail(ErrInvalidPlan, modulePath+".runtime.delivery", "unsupported module delivery %q", decoded.Delivery)
	}
	if !containsStringValue([]string{"executable", "contract-handoff"}, decoded.Execution) {
		return moduleRuntimeContract{}, fail(ErrInvalidPlan, modulePath+".runtime.execution", "unsupported module execution class %q", decoded.Execution)
	}
	runtime := moduleRuntimeContract{execution: decoded.Execution, kind: decoded.Kind, delivery: decoded.Delivery, componentsCanonical: []byte("[]")}
	if decoded.Engine.present {
		if !containsStringValue([]string{"docker", "podman", "systemd", "binary", "api"}, decoded.Engine.value) {
			return moduleRuntimeContract{}, fail(ErrInvalidPlan, modulePath+".runtime.engine", "unsupported module runtime engine %q", decoded.Engine.value)
		}
		runtime.engine = decoded.Engine.value
	}
	if decoded.Image != nil {
		if strings.TrimSpace(decoded.Image.Ref) == "" || strings.ContainsAny(decoded.Image.Ref, "\r\n\t ") {
			return moduleRuntimeContract{}, fail(ErrInvalidPlan, modulePath+".runtime.image.ref", "must be a non-empty image reference")
		}
		runtime.imageRef = decoded.Image.Ref
		if decoded.Image.Digest.present {
			if !validSHA256(decoded.Image.Digest.value) {
				return moduleRuntimeContract{}, fail(ErrInvalidPlan, modulePath+".runtime.image.digest", "must be a lowercase sha256 digest")
			}
			runtime.imageDigest = decoded.Image.Digest.value
		}
	}
	if decoded.EntryComponentRef.present {
		if err := requireContractID(decoded.EntryComponentRef.value, modulePath+".runtime.entryComponentRef"); err != nil {
			return moduleRuntimeContract{}, err
		}
		runtime.entryComponentRef = decoded.EntryComponentRef.value
	}
	if decoded.Components != nil {
		canonical, err := json.Marshal(decoded.Components)
		if err != nil {
			return moduleRuntimeContract{}, wrap(ErrInvalidPlan, modulePath+".runtime.components", "canonicalize component graph", err)
		}
		runtime.componentsCanonical = canonical
	}
	if runtime.kind == "container" {
		if runtime.engine != "docker" && runtime.engine != "podman" {
			return moduleRuntimeContract{}, fail(ErrInvalidPlan, modulePath+".runtime.engine", "container runtime requires docker or podman")
		}
		if runtime.imageRef == "" {
			return moduleRuntimeContract{}, fail(ErrInvalidPlan, modulePath+".runtime.image", "container runtime requires an image")
		}
	}
	return runtime, nil
}

func containsStringValue(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func parseModuleUnits(object map[string]json.RawMessage, moduleID, modulePath string) (renderModule, map[string]struct{}, map[string]moduleOutputOwner, error) {
	rawUnits, err := requiredRawArray(object, "renderUnits", modulePath+".renderUnits")
	if err != nil {
		return renderModule{}, nil, nil, err
	}
	if len(rawUnits) == 0 {
		return renderModule{}, nil, nil, fail(ErrInvalidPlan, modulePath+".renderUnits", "at least one render unit is required")
	}
	module := renderModule{id: moduleID, units: make([]renderUnitContract, 0, len(rawUnits))}
	unitIDs := make(map[string]struct{}, len(rawUnits))
	outputs := make(map[string]moduleOutputOwner)
	serviceOwners := make(map[string]string)
	for index, rawUnit := range rawUnits {
		unitPath := fmt.Sprintf("%s.renderUnits[%d]", modulePath, index)
		unit, err := parseRenderUnit(rawUnit, unitPath)
		if err != nil {
			return renderModule{}, nil, nil, err
		}
		if _, exists := unitIDs[unit.id]; exists {
			return renderModule{}, nil, nil, fail(ErrDuplicate, unitPath+".id", "duplicate render-unit ID %q", unit.id)
		}
		unitIDs[unit.id] = struct{}{}
		for _, endpoint := range unit.serviceEndpoints {
			if previous, exists := serviceOwners[endpoint.serviceRef]; exists {
				return renderModule{}, nil, nil, fail(ErrDuplicate, unitPath+".serviceEndpoints", "service %q is already exported by unit %q", endpoint.serviceRef, previous)
			}
			serviceOwners[endpoint.serviceRef] = unit.id
		}
		for _, output := range unit.outputs {
			if previous, exists := outputs[portableKey(output)]; exists {
				return renderModule{}, nil, nil, fail(ErrDuplicate, unitPath+".outputs", "output %q is already owned by unit %q", output, previous.unitID)
			}
			outputs[portableKey(output)] = moduleOutputOwner{unitID: unit.id, outputRef: output}
		}
		module.units = append(module.units, unit)
	}
	sort.Slice(module.units, func(i, j int) bool { return module.units[i].id < module.units[j].id })
	return module, unitIDs, outputs, nil
}

func parseModuleBindings(object map[string]json.RawMessage, module renderModule, unitIDs map[string]struct{}, outputs map[string]moduleOutputOwner, modulePath string) ([]outputBinding, error) {
	artifactSupport, err := moduleArtifactSupport(object, modulePath)
	if err != nil {
		return nil, err
	}
	requiredRefs, err := requiredStringArray(artifactSupport, "requiredRefs", modulePath+".realizationSupport.artifacts.requiredRefs")
	if err != nil {
		return nil, err
	}
	requiredSet, err := uniqueContractIDSet(requiredRefs, modulePath+".realizationSupport.artifacts.requiredRefs")
	if err != nil {
		return nil, err
	}
	rawBindings, err := requiredRawArray(artifactSupport, "outputBindings", modulePath+".realizationSupport.artifacts.outputBindings")
	if err != nil {
		return nil, err
	}
	if len(rawBindings) == 0 {
		return nil, fail(ErrInvalidPlan, modulePath+".realizationSupport.artifacts.outputBindings", "render units require explicit output bindings")
	}
	bindings := make([]outputBinding, 0, len(rawBindings))
	boundOutputs := make(map[unitOutputKey]struct{}, len(rawBindings))
	boundArtifacts := make(map[string]struct{}, len(rawBindings))
	for index, rawBinding := range rawBindings {
		bindingPath := fmt.Sprintf("%s.realizationSupport.artifacts.outputBindings[%d]", modulePath, index)
		binding, err := parseOutputBinding(rawBinding, module.id, unitIDs, outputs, requiredSet, bindingPath)
		if err != nil {
			return nil, err
		}
		key := unitOutputKey{moduleID: module.id, unitID: binding.unitRef, output: binding.outputRef}
		if _, exists := boundOutputs[key]; exists {
			return nil, fail(ErrDuplicate, bindingPath, "unit output is bound more than once")
		}
		if _, exists := boundArtifacts[binding.artifactID]; exists {
			return nil, fail(ErrDuplicate, bindingPath, "artifact is bound more than once")
		}
		boundOutputs[key], boundArtifacts[binding.artifactID] = struct{}{}, struct{}{}
		bindings = append(bindings, binding)
	}
	if err := requireCompleteModuleBindings(module, requiredSet, boundOutputs, boundArtifacts, modulePath); err != nil {
		return nil, err
	}
	return bindings, nil
}

func moduleArtifactSupport(object map[string]json.RawMessage, modulePath string) (map[string]json.RawMessage, error) {
	realization, err := requiredRawObject(object, "realizationSupport", modulePath+".realizationSupport")
	if err != nil {
		return nil, err
	}
	return requiredRawObject(realization, "artifacts", modulePath+".realizationSupport.artifacts")
}

func parseOutputBinding(raw json.RawMessage, moduleID string, unitIDs map[string]struct{}, outputs map[string]moduleOutputOwner, requiredSet map[string]struct{}, bindingPath string) (outputBinding, error) {
	var decoded rawOutputBinding
	if err := decodeStrict(raw, &decoded); err != nil {
		return outputBinding{}, wrap(ErrInvalidPlan, bindingPath, "decode output binding", err)
	}
	if err := requireContractID(decoded.UnitRef, bindingPath+".unitRef"); err != nil {
		return outputBinding{}, err
	}
	if err := requireContractID(decoded.ArtifactRef, bindingPath+".artifactRef"); err != nil {
		return outputBinding{}, err
	}
	if _, err := validatePortablePath(decoded.OutputRef); err != nil {
		return outputBinding{}, wrap(ErrInvalidPath, bindingPath+".outputRef", "invalid output reference", err)
	}
	if _, exists := unitIDs[decoded.UnitRef]; !exists {
		return outputBinding{}, fail(ErrInvalidPlan, bindingPath+".unitRef", "references undeclared render unit %q", decoded.UnitRef)
	}
	owner, exists := outputs[portableKey(decoded.OutputRef)]
	if !exists || owner.unitID != decoded.UnitRef || owner.outputRef != decoded.OutputRef {
		return outputBinding{}, fail(ErrInvalidPlan, bindingPath+".outputRef", "output %q is not declared by unit %q", decoded.OutputRef, decoded.UnitRef)
	}
	if _, exists := requiredSet[decoded.ArtifactRef]; !exists {
		return outputBinding{}, fail(ErrInvalidPlan, bindingPath+".artifactRef", "artifact %q is not declared in this module's requiredRefs", decoded.ArtifactRef)
	}
	return outputBinding{moduleID: moduleID, unitRef: decoded.UnitRef, artifactID: decoded.ArtifactRef, outputRef: decoded.OutputRef}, nil
}

func requireCompleteModuleBindings(module renderModule, requiredSet map[string]struct{}, boundOutputs map[unitOutputKey]struct{}, boundArtifacts map[string]struct{}, modulePath string) error {
	if len(boundArtifacts) != len(requiredSet) {
		return fail(ErrInvalidPlan, modulePath+".realizationSupport.artifacts", "requiredRefs and outputBindings artifactRefs must be identical")
	}
	for _, unit := range module.units {
		for _, output := range unit.outputs {
			if _, exists := boundOutputs[unitOutputKey{moduleID: module.id, unitID: unit.id, output: output}]; !exists {
				return fail(ErrInvalidPlan, modulePath+".renderUnits", "unit output %s/%s has no output binding", unit.id, output)
			}
		}
	}
	return nil
}

func bindModuleInstances(module renderModule, logicalBindings []outputBinding, artifacts map[string]artifactContract, outputRoot, modulePath string) ([]outputBinding, error) {
	logicalArtifacts := make(map[unitOutputKey]string, len(logicalBindings))
	for _, binding := range logicalBindings {
		logicalArtifacts[unitOutputKey{moduleID: module.id, unitID: binding.unitRef, output: binding.outputRef}] = binding.artifactID
	}
	bindings := make([]outputBinding, 0, len(logicalBindings))
	for _, unit := range module.units {
		for _, instance := range unit.instances {
			for _, output := range instance.outputs {
				logicalKey := unitOutputKey{moduleID: module.id, unitID: unit.id, output: output.ref}
				logicalArtifactID, exists := logicalArtifacts[logicalKey]
				if !exists {
					return nil, fail(ErrInvalidPlan, modulePath+".renderUnits."+unit.id+".instances."+instance.id, "output %q has no logical output binding", output.ref)
				}
				artifact, exists := artifacts[output.artifactID]
				if !exists || !artifact.required {
					return nil, fail(ErrInvalidPlan, modulePath+".renderUnits."+unit.id+".instances."+instance.id, "references undeclared or non-required artifact %q", output.artifactID)
				}
				materializedPath := joinOutputPath(outputRoot, output.path)
				if artifact.path != materializedPath {
					return nil, fail(ErrInvalidPlan, modulePath+".renderUnits."+unit.id+".instances."+instance.id, "artifact %q path %q does not match materialized instance path %q", output.artifactID, artifact.path, materializedPath)
				}
				owner := artifact.owner
				if owner.kind != "render-instance" || owner.moduleRef != module.id || owner.unitRef != unit.id || owner.instanceRef != instance.id || owner.outputRef != output.ref {
					return nil, fail(ErrInvalidPlan, modulePath+".renderUnits."+unit.id+".instances."+instance.id, "artifact %q owner does not exactly match instance output %s/%s/%s/%s", output.artifactID, module.id, unit.id, instance.id, output.ref)
				}
				if instance.scope == "module" && output.artifactID != logicalArtifactID {
					return nil, fail(ErrInvalidPlan, modulePath+".renderUnits."+unit.id+".instances."+instance.id, "module instance output %q must retain logical artifact %q", output.ref, logicalArtifactID)
				}
				bindings = append(bindings, outputBinding{
					moduleID: module.id, unitRef: unit.id, instanceRef: instance.id,
					artifactID: output.artifactID, outputRef: output.ref,
				})
			}
		}
	}
	return bindings, nil
}

func parseRenderUnit(raw json.RawMessage, unitPath string) (renderUnitContract, error) {
	var decoded rawRenderUnit
	if err := decodeStrict(raw, &decoded); err != nil {
		return renderUnitContract{}, wrap(ErrInvalidPlan, unitPath, "decode render unit", err)
	}
	if err := validateRenderUnitIdentity(decoded, unitPath); err != nil {
		return renderUnitContract{}, err
	}
	valuesCanonical, secretsCanonical, err := validateRenderUnitInputs(decoded, unitPath)
	if err != nil {
		return renderUnitContract{}, err
	}
	planInputRefs, planInputsCanonical, err := validateRenderUnitPlanInputs(decoded, unitPath)
	if err != nil {
		return renderUnitContract{}, err
	}
	inputBindingsCanonical, err := validateRenderUnitInputBindings(decoded, unitPath)
	if err != nil {
		return renderUnitContract{}, err
	}
	outputs, err := validateRenderUnitOutputs(decoded.Outputs, unitPath)
	if err != nil {
		return renderUnitContract{}, err
	}
	placement, providedInterfaces, requiredInterfaces, placementContract, err := validateRenderUnitImplementation(decoded, unitPath)
	if err != nil {
		return renderUnitContract{}, err
	}
	serviceEndpoints, serviceEndpointsCanonical, err := parseServiceEndpoints(decoded.ServiceEndpoints, placementContract, unitPath+".serviceEndpoints")
	if err != nil {
		return renderUnitContract{}, err
	}
	providedContracts, err := parseImplementationInterfaces(decoded.ProvidesInterfaces, "provider", unitPath+".providesInterfaces")
	if err != nil {
		return renderUnitContract{}, err
	}
	requiredContracts, err := parseImplementationInterfaces(decoded.RequiresInterfaces, "consumer", unitPath+".requiresInterfaces")
	if err != nil {
		return renderUnitContract{}, err
	}
	instances, err := validateRenderUnitInstances(decoded, outputs, placementContract, unitPath)
	if err != nil {
		return renderUnitContract{}, err
	}
	if err := validateDirectSocketRequirements(requiredContracts, instances, placementContract, unitPath); err != nil {
		return renderUnitContract{}, err
	}
	return renderUnitContract{
		id: decoded.ID, kind: decoded.Kind, rendererRef: decoded.RendererRef,
		templateRef: decoded.TemplateRef, version: decoded.Version, contractHash: decoded.ContractHash,
		publicInputRefs: append([]string(nil), decoded.PublicInputRefs...), secretInputRefs: append([]string(nil), decoded.SecretInputRefs...),
		planInputRefs: append([]string(nil), planInputRefs...),
		siteRefs:      append([]string(nil), decoded.SiteRefs...), nodeRefs: append([]string(nil), decoded.NodeRefs...),
		valuesCanonical: valuesCanonical, secretsCanonical: secretsCanonical, planInputsCanonical: planInputsCanonical,
		inputBindingsCanonical: inputBindingsCanonical, placementCanonical: placement,
		serviceEndpointsCanonical:   serviceEndpointsCanonical,
		providedInterfacesCanonical: providedInterfaces, requiredInterfacesCanonical: requiredInterfaces,
		serviceEndpoints:   serviceEndpoints,
		providedInterfaces: providedContracts, requiredInterfaces: requiredContracts,
		outputs: outputs, instances: instances,
	}, nil
}

//nolint:gocyclo // Closed binding validation intentionally checks every source/type/cardinality/value branch together.
func validateRenderUnitInputBindings(unit rawRenderUnit, unitPath string) ([]byte, error) {
	if unit.InputBindings == nil {
		return nil, fail(ErrInvalidPlan, unitPath+".inputBindings", "inputBindings is mandatory")
	}
	if len(unit.SecretInputBindings) != 0 {
		return nil, fail(ErrInvalidPlan, unitPath+".secretInputBindings", "renderer model does not accept unresolved secret input binding authority")
	}
	publicSet, err := uniqueContractIDSet(unit.PublicInputRefs, unitPath+".publicInputRefs")
	if err != nil {
		return nil, err
	}
	secretSet, err := uniqueContractIDSet(unit.SecretInputRefs, unitPath+".secretInputRefs")
	if err != nil {
		return nil, err
	}
	planSet := make(map[string]struct{}, len(unit.PlanInputRefs))
	for _, ref := range unit.PlanInputRefs {
		planSet[ref] = struct{}{}
	}
	bindings := make([]rawModuleRenderInputBinding, 0, len(unit.InputBindings))
	seen := make(map[string]struct{}, len(unit.InputBindings))
	for index, raw := range unit.InputBindings {
		path := fmt.Sprintf("%s.inputBindings[%d]", unitPath, index)
		var binding rawModuleRenderInputBinding
		if err := decodeStrict(raw, &binding); err != nil {
			return nil, wrap(ErrInvalidPlan, path, "decode input binding", err)
		}
		if err := requireContractID(binding.TargetRef, path+".targetRef"); err != nil {
			return nil, err
		}
		if _, exists := publicSet[binding.TargetRef]; !exists {
			return nil, fail(ErrInvalidPlan, path+".targetRef", "bound target is not a declared public input")
		}
		if _, exists := secretSet[binding.TargetRef]; exists {
			return nil, fail(ErrInvalidPlan, path+".targetRef", "bound target aliases a secret input")
		}
		if _, exists := planSet[binding.TargetRef]; exists {
			return nil, fail(ErrInvalidPlan, path+".targetRef", "bound target aliases a compiler plan input")
		}
		if _, duplicate := seen[binding.TargetRef]; duplicate {
			return nil, fail(ErrDuplicate, path+".targetRef", "duplicate bound target")
		}
		seen[binding.TargetRef] = struct{}{}
		hasDefault := len(binding.DefaultValue) != 0 && string(binding.DefaultValue) != "null"
		if binding.Required == hasDefault {
			return nil, fail(ErrInvalidPlan, path+".defaultValue", "required bindings forbid defaults and optional bindings require one")
		}
		switch binding.SourceRef {
		case "identity.deviceEnrollment":
			if binding.ValueType != "device-enrollment-public-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "identity.deviceEnrollment has an invalid type or cardinality")
			}
		case "identityTrust.homeDeviceAuthority":
			if binding.ValueType != "home-device-authority-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "identityTrust.homeDeviceAuthority has an invalid type or cardinality")
			}
			value, exists := unit.Values[binding.TargetRef]
			if !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound Home device-authority value is missing")
			}
			if _, err := decodeHomeDeviceAuthorityInput(value, unitPath+".values."+binding.TargetRef); err != nil {
				return nil, err
			}
		case "identityTrust.basementVerification":
			if binding.ValueType != "basement-identity-verification-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "identityTrust.basementVerification has an invalid type or cardinality")
			}
			if _, exists := unit.Values[binding.TargetRef]; !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound Basement identity-verification value is missing")
			}
		case "identityTrust.cloudAuthority":
			if binding.ValueType != "cloud-identity-authority-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "identityTrust.cloudAuthority has an invalid type or cardinality")
			}
			if _, exists := unit.Values[binding.TargetRef]; !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound Cloud identity-authority value is missing")
			}
		case "access.homeEnforcement":
			if binding.ValueType != "home-access-enforcement-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "access.homeEnforcement has an invalid type or cardinality")
			}
			if _, exists := unit.Values[binding.TargetRef]; !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound Home access-enforcement value is missing")
			}
		case "localAutonomy.policy":
			if binding.ValueType != "local-autonomy-policy-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "localAutonomy.policy has an invalid type or cardinality")
			}
			if _, exists := unit.Values[binding.TargetRef]; !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound local-autonomy policy value is missing")
			}
		case "network.routes":
			if binding.ValueType != "authority-bound-service-route-list-v4" || binding.Cardinality != "list" {
				return nil, fail(ErrInvalidPlan, path, "network.routes has an invalid type or cardinality")
			}
			value, exists := unit.Values[binding.TargetRef]
			if !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound network.routes value is missing")
			}
			if err := validatePublicServiceRouteListV4(value, unitPath+".values."+binding.TargetRef); err != nil {
				return nil, err
			}
		case "network.cloudHostSecurity":
			if binding.ValueType != "cloud-host-security-policy-v2" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "network.cloudHostSecurity has an invalid type or cardinality")
			}
			value, exists := unit.Values[binding.TargetRef]
			if !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound network.cloudHostSecurity value is missing")
			}
			if _, err := decodeCloudHostSecurityNetworkInput(value, unitPath+".values."+binding.TargetRef); err != nil {
				return nil, err
			}
		case "host.bootstrapRuntime":
			if binding.ValueType != "host-bootstrap-runtime-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "host.bootstrapRuntime has an invalid type or cardinality")
			}
			value, exists := unit.Values[binding.TargetRef]
			if !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound host.bootstrapRuntime value is missing")
			}
			if _, err := decodeCoreHostBootstrapRuntime(value, unitPath+".values."+binding.TargetRef); err != nil {
				return nil, err
			}
		case "storage.hostRoots":
			if binding.ValueType != "host-storage-roots-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "storage.hostRoots has an invalid type or cardinality")
			}
			value, exists := unit.Values[binding.TargetRef]
			if !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound storage.hostRoots value is missing")
			}
			if _, err := decodeCoreHostStorageRoots(value, unitPath+".values."+binding.TargetRef); err != nil {
				return nil, err
			}
		case "storage.backupRoot":
			if binding.ValueType != "local-backup-root-v1" || binding.Cardinality != "single" {
				return nil, fail(ErrInvalidPlan, path, "storage.backupRoot has an invalid type or cardinality")
			}
			value, exists := unit.Values[binding.TargetRef]
			if !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".values."+binding.TargetRef, "bound storage.backupRoot value is missing")
			}
			if _, err := decodeHomeBackupRoot(value, unitPath+".values."+binding.TargetRef); err != nil {
				return nil, err
			}
		default:
			return nil, fail(ErrInvalidPlan, path+".sourceRef", "unsupported resolved-plan input source")
		}
		bindings = append(bindings, binding)
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].TargetRef < bindings[j].TargetRef })
	canonical, err := json.Marshal(bindings)
	if err != nil {
		return nil, wrap(ErrInvalidPlan, unitPath+".inputBindings", "canonicalize input bindings", err)
	}
	return canonical, nil
}

func validatePublicRouteTLSAuthorityV4(route rawPublicServiceRouteV4, roles map[string]string, path string) error {
	tls := route.TLS
	if !tls.Required {
		if tls.Mode != "off" || tls.MinVersion != "" || tls.OwnerCapabilityRef != "" || tls.ProfileRef != "" || tls.IssuerRef != "" || route.Exposure == "public" || roles["edge"] != "" || roles["egress"] != "" {
			return fail(ErrInvalidPlan, path+".tls", "TLS-off route cannot carry public edge, egress, profile, issuer, or custody authority")
		}
		return nil
	}
	if tls.MinVersion == "" {
		return fail(ErrInvalidPlan, path+".tls", "authority-bound route requires an explicit TLS minimum")
	}
	switch tls.Mode {
	case "external":
		if route.Exposure != "public" || tls.OwnerCapabilityRef == "" || roles["egress"] != tls.OwnerCapabilityRef || roles["edge"] != "" || tls.ProfileRef != "" || tls.IssuerRef != "" {
			return fail(ErrInvalidPlan, path+".tls", "external TLS requires the exact egress owner and forbids edge/profile/issuer authority")
		}
	case "terminate-at-edge":
		if route.Exposure != "public" || roles["edge"] == "" || roles["egress"] != "" || tls.OwnerCapabilityRef != "" || tls.ProfileRef == "" || tls.IssuerRef == "" {
			return fail(ErrInvalidPlan, path+".tls", "edge TLS requires exact edge/profile/issuer authority and forbids egress custody")
		}
	case "internal":
		if tls.OwnerCapabilityRef != "" || tls.ProfileRef == "" || tls.IssuerRef == "" || roles["edge"] != "" || roles["egress"] != "" {
			return fail(ErrInvalidPlan, path+".tls", "internal TLS forbids public edge or egress authority")
		}
	default:
		return fail(ErrInvalidPlan, path+".tls.mode", "unsupported required TLS mode %q", tls.Mode)
	}
	return nil
}

func validatePublicRouteCapabilityAuthoritiesV4(route rawPublicServiceRouteV4, path string) error {
	if route.CapabilityAuthorities == nil {
		return fail(ErrInvalidPlan, path+".capabilityAuthorities", "authority list is mandatory")
	}
	capabilities := map[string]struct{}{}
	roles := map[string]string{}
	for authorityIndex, authority := range route.CapabilityAuthorities {
		authorityPath := fmt.Sprintf("%s.capabilityAuthorities[%d]", path, authorityIndex)
		if err := requireContractID(authority.CapabilityRef, authorityPath+".capabilityRef"); err != nil {
			return err
		}
		if !oneOf(authority.Role, "access", "transport", "edge", "egress") {
			return fail(ErrInvalidPlan, authorityPath+".role", "unsupported route capability role %q", authority.Role)
		}
		if _, duplicate := capabilities[authority.CapabilityRef]; duplicate {
			return fail(ErrDuplicate, authorityPath+".capabilityRef", "duplicate route capability authority")
		}
		capabilities[authority.CapabilityRef] = struct{}{}
		if existing, duplicate := roles[authority.Role]; duplicate {
			return fail(ErrDuplicate, authorityPath+".role", "route role %q is already owned by %q", authority.Role, existing)
		}
		roles[authority.Role] = authority.CapabilityRef
	}
	if route.Exposure == "local" && len(route.CapabilityAuthorities) != 0 {
		return fail(ErrInvalidPlan, path+".capabilityAuthorities", "local route cannot carry remote reachability authority")
	}
	if route.Exposure != "local" && len(route.CapabilityAuthorities) == 0 {
		return fail(ErrInvalidPlan, path+".capabilityAuthorities", "non-local route requires exact reachability authority")
	}
	return validatePublicRouteTLSAuthorityV4(route, roles, path)
}

func validatePublicServiceRouteListV3(raw json.RawMessage, valuePath string) error {
	var values []json.RawMessage
	if err := decodeJSON(raw, &values, false); err != nil || values == nil {
		return fail(ErrInvalidPlan, valuePath, "expected public service route v3 list")
	}
	seenRoutes := make(map[string]struct{}, len(values))
	for index, value := range values {
		path := fmt.Sprintf("%s[%d]", valuePath, index)
		var route rawPublicServiceRouteV3
		if err := decodeStrict(value, &route); err != nil {
			return wrap(ErrInvalidPlan, path, "decode public service route v3", err)
		}
		if _, duplicate := seenRoutes[route.ID]; duplicate {
			return fail(ErrDuplicate, path+".id", "duplicate public route %q", route.ID)
		}
		seenRoutes[route.ID] = struct{}{}
		base, err := json.Marshal([]rawPublicServiceRouteV2{route.rawPublicServiceRouteV2})
		if err != nil {
			return wrap(ErrInvalidPlan, path, "encode public service route v3 base", err)
		}
		if err := validatePublicServiceRouteListV2(base, valuePath); err != nil {
			return err
		}
		probe := route.HealthProbe
		if probe.Port != route.TargetPort || probe.Protocol != route.UpstreamProtocol || probe.TimeoutSeconds < 1 || probe.TimeoutSeconds > 300 {
			return fail(ErrInvalidPlan, path+".healthProbe", "probe protocol, port, and timeout must match the route contract")
		}
		switch probe.Kind {
		case "http":
			if !oneOf(probe.Protocol, "http", "https") || probe.Method != "GET" || probe.FollowRedirects == nil || *probe.FollowRedirects || !strings.HasPrefix(probe.Path, "/") || len(probe.ExpectedStatuses) == 0 {
				return fail(ErrInvalidPlan, path+".healthProbe", "invalid closed HTTP probe")
			}
			seenStatuses := map[int]struct{}{}
			for _, status := range probe.ExpectedStatuses {
				if status < 100 || status > 599 {
					return fail(ErrInvalidPlan, path+".healthProbe.expectedStatuses", "HTTP status is outside 100..599")
				}
				if _, duplicate := seenStatuses[status]; duplicate {
					return fail(ErrDuplicate, path+".healthProbe.expectedStatuses", "duplicate HTTP status")
				}
				seenStatuses[status] = struct{}{}
			}
		case "tcp":
			if probe.Protocol != "tcp" || probe.Method != "" || probe.FollowRedirects != nil || probe.Path != "" || probe.ExpectedStatuses != nil {
				return fail(ErrInvalidPlan, path+".healthProbe", "TCP probes forbid HTTP-only fields")
			}
		default:
			return fail(ErrInvalidPlan, path+".healthProbe.kind", "unsupported probe kind %q", probe.Kind)
		}
	}
	return nil
}

func validatePublicServiceRouteListV4(raw json.RawMessage, valuePath string) error {
	var values []json.RawMessage
	if err := decodeJSON(raw, &values, false); err != nil || values == nil {
		return fail(ErrInvalidPlan, valuePath, "expected public service route v4 list")
	}
	seenRoutes := make(map[string]struct{}, len(values))
	for index, value := range values {
		path := fmt.Sprintf("%s[%d]", valuePath, index)
		var route rawPublicServiceRouteV4
		if err := decodeStrict(value, &route); err != nil {
			return wrap(ErrInvalidPlan, path, "decode public service route v4", err)
		}
		for _, field := range []struct{ name, value string }{
			{"id", route.ID}, {"serviceRef", route.ServiceRef}, {"moduleRef", route.ModuleRef},
			{"backendPoolRef", route.BackendPoolRef}, {"healthGateRef", route.HealthGateRef},
		} {
			if err := requireContractID(field.value, path+"."+field.name); err != nil {
				return err
			}
		}
		if _, duplicate := seenRoutes[route.ID]; duplicate {
			return fail(ErrDuplicate, path+".id", "duplicate public route %q", route.ID)
		}
		seenRoutes[route.ID] = struct{}{}
		sites, err := uniqueContractIDSet(route.OriginSiteRefs, path+".originSiteRefs")
		if err != nil || len(sites) == 0 {
			return fail(ErrInvalidPlan, path+".originSiteRefs", "at least one unique origin Site is required")
		}
		nodes, err := uniqueContractIDSet(route.OriginNodeRefs, path+".originNodeRefs")
		if err != nil || len(nodes) == 0 {
			return fail(ErrInvalidPlan, path+".originNodeRefs", "at least one unique origin node is required")
		}
		switch route.OriginSelector {
		case "single-site", "control-authority-site":
			if len(route.OriginSiteRefs) != 1 || route.OriginSiteRef != route.OriginSiteRefs[0] || route.OriginSelection != nil {
				return fail(ErrInvalidPlan, path, "single-Site selector requires one matching originSiteRef and forbids originSelection")
			}
		case "multi-zone", "edge-pool":
			if route.OriginSiteRef != "" || route.OriginSelection == nil {
				return fail(ErrInvalidPlan, path, "multi-Site selector forbids originSiteRef and requires originSelection")
			}
			if err := validateEndpointOriginSelection(*route.OriginSelection, route.OriginSelector, path+".originSelection", true); err != nil {
				return err
			}
			if len(route.OriginSiteRefs) < route.OriginSelection.MinSites {
				return fail(ErrInvalidPlan, path+".originSiteRefs", "origin Site count is below minSites")
			}
			for domainIndex, domain := range route.OriginSelection.NodeFailureDomains {
				if _, exists := sites[domain.SiteRef]; !exists {
					return fail(ErrInvalidPlan, fmt.Sprintf("%s.originSelection.nodeFailureDomains[%d].siteRef", path, domainIndex), "node failure domain is outside the origin Site set")
				}
			}
		default:
			return fail(ErrInvalidPlan, path+".originSelector", "unsupported selector %q", route.OriginSelector)
		}
		if route.BackendPool.UpstreamProtocol != route.UpstreamProtocol || route.BackendPool.TargetPort != route.TargetPort || len(route.BackendPool.Members) == 0 {
			return fail(ErrInvalidPlan, path+".backendPool", "backend pool must exactly match the route upstream and contain members")
		}
		seenInstances, memberSites, memberNodes := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
		for memberIndex, member := range route.BackendPool.Members {
			memberPath := fmt.Sprintf("%s.backendPool.members[%d]", path, memberIndex)
			for _, field := range []struct{ name, value string }{{"siteRef", member.SiteRef}, {"nodeRef", member.NodeRef}, {"instanceRef", member.InstanceRef}} {
				if err := requireContractID(field.value, memberPath+"."+field.name); err != nil {
					return err
				}
			}
			if _, exists := sites[member.SiteRef]; !exists {
				return fail(ErrInvalidPlan, memberPath+".siteRef", "backend member is outside the origin Site set")
			}
			if _, exists := nodes[member.NodeRef]; !exists {
				return fail(ErrInvalidPlan, memberPath+".nodeRef", "backend member is outside the origin node set")
			}
			if _, duplicate := seenInstances[member.InstanceRef]; duplicate {
				return fail(ErrDuplicate, memberPath+".instanceRef", "duplicate backend instance")
			}
			seenInstances[member.InstanceRef], memberSites[member.SiteRef], memberNodes[member.NodeRef] = struct{}{}, struct{}{}, struct{}{}
		}
		if !reflect.DeepEqual(sites, memberSites) || !reflect.DeepEqual(nodes, memberNodes) {
			return fail(ErrInvalidPlan, path+".backendPool.members", "backend member Site and node sets must exactly equal the route origin sets")
		}
		probe := route.HealthProbe
		if probe.Port != route.TargetPort || probe.Protocol != route.UpstreamProtocol || probe.TimeoutSeconds < 1 || probe.TimeoutSeconds > 300 {
			return fail(ErrInvalidPlan, path+".healthProbe", "probe protocol, port, and timeout must match the route contract")
		}
		switch probe.Kind {
		case "http":
			if !oneOf(probe.Protocol, "http", "https") || probe.Method != "GET" || probe.FollowRedirects == nil || *probe.FollowRedirects || !strings.HasPrefix(probe.Path, "/") || len(probe.ExpectedStatuses) == 0 {
				return fail(ErrInvalidPlan, path+".healthProbe", "invalid closed HTTP probe")
			}
		case "tcp":
			if probe.Protocol != "tcp" || probe.Method != "" || probe.FollowRedirects != nil || probe.Path != "" || probe.ExpectedStatuses != nil {
				return fail(ErrInvalidPlan, path+".healthProbe", "TCP probes forbid HTTP-only fields")
			}
		default:
			return fail(ErrInvalidPlan, path+".healthProbe.kind", "unsupported probe kind %q", probe.Kind)
		}
		if err := validatePublicRouteCapabilityAuthoritiesV4(route, path); err != nil {
			return err
		}
	}
	return nil
}

func validatePublicServiceRouteListV2(raw json.RawMessage, valuePath string) error {
	var values []json.RawMessage
	if err := decodeJSON(raw, &values, false); err != nil || values == nil {
		return fail(ErrInvalidPlan, valuePath, "expected public service route list")
	}
	seenRoutes := make(map[string]struct{}, len(values))
	for index, value := range values {
		path := fmt.Sprintf("%s[%d]", valuePath, index)
		var route rawPublicServiceRouteV2
		if err := decodeStrict(value, &route); err != nil {
			return wrap(ErrInvalidPlan, path, "decode public service route v2", err)
		}
		for _, field := range []struct{ name, value string }{
			{"id", route.ID}, {"serviceRef", route.ServiceRef}, {"moduleRef", route.ModuleRef},
			{"originSiteRef", route.OriginSiteRef}, {"backendPoolRef", route.BackendPoolRef}, {"healthGateRef", route.HealthGateRef},
		} {
			if err := requireContractID(field.value, path+"."+field.name); err != nil {
				return err
			}
		}
		if _, duplicate := seenRoutes[route.ID]; duplicate {
			return fail(ErrDuplicate, path+".id", "duplicate public route %q", route.ID)
		}
		seenRoutes[route.ID] = struct{}{}
		if route.BackendPool.UpstreamProtocol != route.UpstreamProtocol || route.BackendPool.TargetPort != route.TargetPort {
			return fail(ErrInvalidPlan, path+".backendPool", "backend protocol and port must exactly match the route upstream")
		}
		if len(route.BackendPool.Members) == 0 {
			return fail(ErrInvalidPlan, path+".backendPool.members", "at least one exact backend member is required")
		}
		nodes, err := uniqueContractIDSet(route.OriginNodeRefs, path+".originNodeRefs")
		if err != nil || len(nodes) == 0 {
			return fail(ErrInvalidPlan, path+".originNodeRefs", "at least one unique origin node is required")
		}
		seenInstances := make(map[string]struct{}, len(route.BackendPool.Members))
		for memberIndex, member := range route.BackendPool.Members {
			memberPath := fmt.Sprintf("%s.backendPool.members[%d]", path, memberIndex)
			for _, field := range []struct{ name, value string }{{"siteRef", member.SiteRef}, {"nodeRef", member.NodeRef}, {"instanceRef", member.InstanceRef}} {
				if err := requireContractID(field.value, memberPath+"."+field.name); err != nil {
					return err
				}
			}
			if member.SiteRef != route.OriginSiteRef {
				return fail(ErrInvalidPlan, memberPath+".siteRef", "backend member is outside the route origin site")
			}
			if _, exists := nodes[member.NodeRef]; !exists {
				return fail(ErrInvalidPlan, memberPath+".nodeRef", "backend member is outside the route origin nodes")
			}
			if _, duplicate := seenInstances[member.InstanceRef]; duplicate {
				return fail(ErrDuplicate, memberPath+".instanceRef", "duplicate backend instance")
			}
			seenInstances[member.InstanceRef] = struct{}{}
		}
	}
	return nil
}

func validateRenderUnitImplementation(unit rawRenderUnit, unitPath string) ([]byte, []byte, []byte, rawRenderUnitPlacement, error) {
	if unit.SiteRefs == nil || unit.NodeRefs == nil || unit.Placement == nil || unit.DaemonBindings == nil || unit.ServiceEndpoints == nil || unit.ProvidesInterfaces == nil || unit.RequiresInterfaces == nil {
		return nil, nil, nil, rawRenderUnitPlacement{}, fail(ErrInvalidPlan, unitPath, "siteRefs, nodeRefs, placement, daemonBindings, serviceEndpoints, providesInterfaces, and requiresInterfaces are mandatory")
	}
	if len(unit.SiteRefs) == 0 || len(unit.NodeRefs) == 0 {
		return nil, nil, nil, rawRenderUnitPlacement{}, fail(ErrInvalidPlan, unitPath, "siteRefs and nodeRefs must contain resolved placement")
	}
	if _, err := uniqueContractIDSet(unit.SiteRefs, unitPath+".siteRefs"); err != nil {
		return nil, nil, nil, rawRenderUnitPlacement{}, err
	}
	if _, err := uniqueContractIDSet(unit.NodeRefs, unitPath+".nodeRefs"); err != nil {
		return nil, nil, nil, rawRenderUnitPlacement{}, err
	}
	placementObject, err := decodeRawObject(unit.Placement, unitPath+".placement")
	if err != nil {
		return nil, nil, nil, rawRenderUnitPlacement{}, err
	}
	var placementContract rawRenderUnitPlacement
	if err := decodeStrict(unit.Placement, &placementContract); err != nil {
		return nil, nil, nil, rawRenderUnitPlacement{}, wrap(ErrInvalidPlan, unitPath+".placement", "decode placement contract", err)
	}
	if err := validatePlacementContract(placementContract, unitPath+".placement"); err != nil {
		return nil, nil, nil, rawRenderUnitPlacement{}, err
	}
	placement, err := canonicalObject(placementObject)
	if err != nil {
		return nil, nil, nil, rawRenderUnitPlacement{}, wrap(ErrInvalidPlan, unitPath+".placement", "canonicalize render-unit placement", err)
	}
	provided, err := canonicalImplementationInterfaces(unit.ProvidesInterfaces, unitPath+".providesInterfaces")
	if err != nil {
		return nil, nil, nil, rawRenderUnitPlacement{}, err
	}
	required, err := canonicalImplementationInterfaces(unit.RequiresInterfaces, unitPath+".requiresInterfaces")
	if err != nil {
		return nil, nil, nil, rawRenderUnitPlacement{}, err
	}
	return placement, provided, required, placementContract, nil
}

//nolint:gocyclo // Endpoint parsing validates the complete closed schema and security policy before admitting any routable contract.
func parseServiceEndpoints(values []json.RawMessage, placement rawRenderUnitPlacement, valuePath string) ([]serviceEndpointContract, []byte, error) {
	if len(values) > 0 && placement.Scope != "node-local" {
		return nil, nil, fail(ErrInvalidPlan, valuePath, "routable service endpoints require node-local placement")
	}
	result := make([]serviceEndpointContract, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, raw := range values {
		endpointPath := fmt.Sprintf("%s[%d]", valuePath, index)
		var endpoint rawModuleServiceEndpoint
		if err := decodeStrict(raw, &endpoint); err != nil {
			return nil, nil, wrap(ErrInvalidPlan, endpointPath, "decode service endpoint", err)
		}
		for _, field := range []struct{ name, value string }{
			{"serviceRef", endpoint.ServiceRef},
			{"healthRef", endpoint.HealthRef},
		} {
			if err := requireContractID(field.value, endpointPath+"."+field.name); err != nil {
				return nil, nil, err
			}
		}
		if _, duplicate := seen[endpoint.ServiceRef]; duplicate {
			return nil, nil, fail(ErrDuplicate, endpointPath+".serviceRef", "duplicate service endpoint %q", endpoint.ServiceRef)
		}
		seen[endpoint.ServiceRef] = struct{}{}
		if !oneOf(endpoint.UpstreamProtocol, "tcp", "udp", "http", "https") {
			return nil, nil, fail(ErrInvalidPlan, endpointPath+".upstreamProtocol", "unsupported network protocol %q", endpoint.UpstreamProtocol)
		}
		if endpoint.TargetPort < 1 || endpoint.TargetPort > 65535 {
			return nil, nil, fail(ErrInvalidPlan, endpointPath+".targetPort", "must be between 1 and 65535")
		}
		if err := validateUniqueEnumList(endpoint.AllowedIngressProtocols, []string{"tcp", "udp", "http", "https"}, endpointPath+".allowedIngressProtocols"); err != nil {
			return nil, nil, err
		}
		if err := validateUniqueEnumList(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}, endpointPath+".allowedExposures"); err != nil {
			return nil, nil, err
		}
		if !oneOf(endpoint.OriginSelector, "single-site", "control-authority-site", "multi-zone", "edge-pool") {
			return nil, nil, fail(ErrInvalidPlan, endpointPath+".originSelector", "unsupported origin selector %q", endpoint.OriginSelector)
		}
		hasOriginSelection := len(endpoint.OriginSelection) != 0 && string(endpoint.OriginSelection) != "null"
		if endpoint.OriginSelector == "single-site" || endpoint.OriginSelector == "control-authority-site" {
			if hasOriginSelection {
				return nil, nil, fail(ErrInvalidPlan, endpointPath+".originSelection", "single-Site selectors forbid an origin selection policy")
			}
		} else {
			if !hasOriginSelection {
				return nil, nil, fail(ErrInvalidPlan, endpointPath+".originSelection", "selector %q requires an explicit origin selection policy", endpoint.OriginSelector)
			}
			var selection rawServiceEndpointOriginSelectionV2
			if err := decodeStrict(endpoint.OriginSelection, &selection); err != nil {
				return nil, nil, wrap(ErrInvalidPlan, endpointPath+".originSelection", "decode origin selection policy", err)
			}
			if err := validateEndpointOriginSelection(selection, endpoint.OriginSelector, endpointPath+".originSelection", false); err != nil {
				return nil, nil, err
			}
		}
		if endpoint.Data != nil {
			if bytes.Equal(bytes.TrimSpace(endpoint.Data), []byte("null")) {
				return nil, nil, fail(ErrInvalidPlan, endpointPath+".data", "must be an object, not null")
			}
			var data rawModuleServiceEndpointData
			if err := decodeStrict(endpoint.Data, &data); err != nil {
				return nil, nil, wrap(ErrInvalidPlan, endpointPath+".data", "decode service data contract", err)
			}
			if err := requireContractID(data.BindingRef, endpointPath+".data.bindingRef"); err != nil {
				return nil, nil, err
			}
			if err := validateUniqueEnumList(data.RequiredClasses, []string{"public", "internal", "personal", "sensitive", "secret"}, endpointPath+".data.requiredClasses"); err != nil {
				return nil, nil, err
			}
			if !oneOf(data.Locality, "primary-site", "primary-or-replica") {
				return nil, nil, fail(ErrInvalidPlan, endpointPath+".data.locality", "unsupported data locality %q", data.Locality)
			}
		}
		result = append(result, serviceEndpointContract{serviceRef: endpoint.ServiceRef})
	}
	canonical, err := json.Marshal(values)
	if err != nil {
		return nil, nil, wrap(ErrInvalidPlan, valuePath, "canonicalize service endpoints", err)
	}
	return result, canonical, nil
}

func validateEndpointOriginSelection(selection rawServiceEndpointOriginSelectionV2, selector, path string, resolved bool) error {
	if err := validateUniqueEnumList(selection.SiteKinds, []string{"home", "cloud"}, path+".siteKinds"); err != nil {
		return err
	}
	if selection.MinSites < 1 || selection.SiteFailureDomainSpread < 1 || selection.NodeFailureDomainSpread < 1 {
		return fail(ErrInvalidPlan, path, "selection thresholds must be positive")
	}
	if err := validateUniqueEnumList(selection.RequiredRoles, []string{"controller", "worker", "storage", "edge"}, path+".requiredRoles"); err != nil && len(selection.RequiredRoles) > 0 {
		return err
	}
	if selector == "multi-zone" && (selection.NodeFailureDomainSpread < 2 || len(selection.RequiredRoles) != 0) {
		return fail(ErrInvalidPlan, path, "multi-zone requires node failure-domain spread >= 2 and no role filter")
	}
	if selector == "edge-pool" && (len(selection.RequiredRoles) != 1 || selection.RequiredRoles[0] != "edge") {
		return fail(ErrInvalidPlan, path+".requiredRoles", "edge-pool requires exactly the edge role")
	}
	if !resolved {
		if selection.SiteFailureDomains != nil || selection.NodeFailureDomains != nil {
			return fail(ErrInvalidPlan, path, "catalog selection policies cannot contain compiler-resolved failure domains")
		}
		return nil
	}
	if len(selection.SiteFailureDomains) < selection.SiteFailureDomainSpread || len(selection.NodeFailureDomains) < selection.NodeFailureDomainSpread {
		return fail(ErrInvalidPlan, path, "resolved failure-domain evidence is below the declared spread")
	}
	seenSiteDomains := map[string]struct{}{}
	for index, domain := range selection.SiteFailureDomains {
		if domain == "" {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s.siteFailureDomains[%d]", path, index), "failure domain is empty")
		}
		if _, duplicate := seenSiteDomains[domain]; duplicate {
			return fail(ErrDuplicate, path+".siteFailureDomains", "duplicate Site failure domain")
		}
		seenSiteDomains[domain] = struct{}{}
	}
	seenNodeDomains := map[string]struct{}{}
	for index, domain := range selection.NodeFailureDomains {
		if err := requireContractID(domain.SiteRef, fmt.Sprintf("%s.nodeFailureDomains[%d].siteRef", path, index)); err != nil {
			return err
		}
		if domain.FailureDomain == "" {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s.nodeFailureDomains[%d].failureDomain", path, index), "failure domain is empty")
		}
		key := domain.SiteRef + "\x00" + domain.FailureDomain
		if _, duplicate := seenNodeDomains[key]; duplicate {
			return fail(ErrDuplicate, path+".nodeFailureDomains", "duplicate site-scoped node failure domain")
		}
		seenNodeDomains[key] = struct{}{}
	}
	return nil
}

func validateUniqueEnumList(values, allowed []string, valuePath string) error {
	if len(values) == 0 {
		return fail(ErrInvalidPlan, valuePath, "must contain at least one value")
	}
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		itemPath := fmt.Sprintf("%s[%d]", valuePath, index)
		if !oneOf(value, allowed...) {
			return fail(ErrInvalidPlan, itemPath, "unsupported value %q", value)
		}
		if _, duplicate := seen[value]; duplicate {
			return fail(ErrDuplicate, itemPath, "duplicate value %q", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func validatePlacementContract(placement rawRenderUnitPlacement, placementPath string) error {
	switch placement.Scope {
	case "module":
		if placement.Cardinality != "single" || placement.DaemonRef.present {
			return fail(ErrInvalidPlan, placementPath, "module placement requires single cardinality and no daemonRef")
		}
	case "node-local":
		switch placement.Cardinality {
		case "single", "one-per-node":
			if placement.DaemonRef.present {
				return fail(ErrInvalidPlan, placementPath+".daemonRef", "is only allowed for one-per-daemon placement")
			}
		case "one-per-daemon":
			if !placement.DaemonRef.present {
				return fail(ErrInvalidPlan, placementPath+".daemonRef", "is required for one-per-daemon placement")
			}
			if err := requireContractID(placement.DaemonRef.value, placementPath+".daemonRef"); err != nil {
				return err
			}
		default:
			return fail(ErrInvalidPlan, placementPath+".cardinality", "unsupported node-local cardinality %q", placement.Cardinality)
		}
	default:
		return fail(ErrInvalidPlan, placementPath+".scope", "must be module or node-local")
	}
	return nil
}

func validateRenderUnitInstances(unit rawRenderUnit, logicalOutputs []string, placement rawRenderUnitPlacement, unitPath string) ([]renderUnitInstance, error) {
	if len(unit.Instances) == 0 {
		return nil, fail(ErrInvalidPlan, unitPath+".instances", "at least one explicit render instance is required")
	}
	wantCount := renderUnitInstanceCount(unit, placement)
	if len(unit.Instances) != wantCount {
		return nil, fail(ErrInvalidPlan, unitPath+".instances", "contains %d instances, want exactly %d for %s/%s placement", len(unit.Instances), wantCount, placement.Scope, placement.Cardinality)
	}
	logicalSet := make(map[string]struct{}, len(logicalOutputs))
	for _, output := range logicalOutputs {
		logicalSet[output] = struct{}{}
	}
	siteSet, _ := uniqueContractIDSet(unit.SiteRefs, unitPath+".siteRefs")
	nodeSet, _ := uniqueContractIDSet(unit.NodeRefs, unitPath+".nodeRefs")
	daemonBindings, err := indexRuntimeDaemonBindings(unit.DaemonBindings, unitPath+".daemonBindings")
	if err != nil {
		return nil, err
	}
	if err := validateRenderUnitDaemonBindings(placement, daemonBindings, len(unit.NodeRefs), unitPath); err != nil {
		return nil, err
	}
	instances := make([]renderUnitInstance, 0, len(unit.Instances))
	seenIDs := make(map[string]struct{}, len(unit.Instances))
	seenNodes := make(map[string]struct{}, len(unit.Instances))
	for index, raw := range unit.Instances {
		instancePath := fmt.Sprintf("%s.instances[%d]", unitPath, index)
		if err := requireContractID(raw.ID, instancePath+".id"); err != nil {
			return nil, err
		}
		if _, exists := seenIDs[raw.ID]; exists {
			return nil, fail(ErrDuplicate, instancePath+".id", "duplicate render instance %q", raw.ID)
		}
		seenIDs[raw.ID] = struct{}{}
		instance, err := validateRenderUnitInstanceIdentity(unit, raw, placement, siteSet, nodeSet, daemonBindings, instancePath)
		if err != nil {
			return nil, err
		}
		if err := trackRenderInstanceNode(instance, seenNodes, instancePath); err != nil {
			return nil, err
		}
		outputs, err := validateRenderInstanceOutputs(raw.Outputs, logicalSet, instancePath)
		if err != nil {
			return nil, err
		}
		networkBindings, networkCanonical, err := validateRuntimeNetworkBindings(raw.NetworkBindings, instance, instancePath)
		if err != nil {
			return nil, err
		}
		if instance.scope == "module" && len(networkBindings) != 0 {
			return nil, fail(ErrInvalidPlan, instancePath+".networkBindings", "module-scoped render instances cannot join runtime networks")
		}
		instance.outputs = outputs
		instance.networkBindings = networkBindings
		instance.networkCanonical = networkCanonical
		instances = append(instances, instance)
	}
	if placement.Scope == "node-local" {
		for _, nodeRef := range unit.NodeRefs {
			if _, exists := seenNodes[nodeRef]; !exists {
				return nil, fail(ErrInvalidPlan, unitPath+".instances", "eligible node %q has no exact render instance", nodeRef)
			}
		}
	}
	sort.Slice(instances, func(i, j int) bool { return instances[i].id < instances[j].id })
	return instances, nil
}

func renderUnitInstanceCount(unit rawRenderUnit, placement rawRenderUnitPlacement) int {
	if placement.Scope == "node-local" && placement.Cardinality != "single" {
		return len(unit.NodeRefs)
	}
	return 1
}

func validateRenderUnitDaemonBindings(placement rawRenderUnitPlacement, bindings map[string]rawRuntimeDaemonBinding, nodeCount int, unitPath string) error {
	if placement.Cardinality == "one-per-daemon" {
		if len(bindings) != nodeCount {
			return fail(ErrInvalidPlan, unitPath+".daemonBindings", "must contain exactly one daemon binding per eligible node")
		}
		return nil
	}
	if len(bindings) != 0 {
		return fail(ErrInvalidPlan, unitPath+".daemonBindings", "is only allowed for one-per-daemon placement")
	}
	return nil
}

func trackRenderInstanceNode(instance renderUnitInstance, seenNodes map[string]struct{}, instancePath string) error {
	if instance.nodeRef == "" {
		return nil
	}
	if _, exists := seenNodes[instance.nodeRef]; exists {
		return fail(ErrDuplicate, instancePath+".nodeRef", "node %q has more than one instance for this logical render unit", instance.nodeRef)
	}
	seenNodes[instance.nodeRef] = struct{}{}
	return nil
}

func validateRenderUnitInstanceIdentity(unit rawRenderUnit, raw rawRenderUnitInstance, placement rawRenderUnitPlacement, siteSet, nodeSet map[string]struct{}, daemonBindings map[string]rawRuntimeDaemonBinding, instancePath string) (renderUnitInstance, error) {
	if raw.Scope != placement.Scope {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath+".scope", "instance scope %q does not match logical placement scope %q", raw.Scope, placement.Scope)
	}
	if placement.Scope == "module" {
		return validateModuleRenderUnitInstanceIdentity(unit, raw, instancePath)
	}
	instance, err := validateNodeLocalRenderUnitInstanceIdentity(raw, siteSet, nodeSet, instancePath)
	if err != nil {
		return renderUnitInstance{}, err
	}
	if placement.Cardinality != "one-per-daemon" {
		return validateNonDaemonRenderUnitInstanceIdentity(unit, raw, instance, instancePath)
	}
	return validateDaemonRenderUnitInstanceIdentity(unit, raw, instance, placement, daemonBindings, instancePath)
}

func validateModuleRenderUnitInstanceIdentity(unit rawRenderUnit, raw rawRenderUnitInstance, instancePath string) (renderUnitInstance, error) {
	if raw.ID != unit.ID+"-logical" {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath+".id", "module instance must use exact ID %q", unit.ID+"-logical")
	}
	if raw.SiteRef.present || raw.NodeRef.present || raw.DaemonRef.present || raw.DaemonInstanceRef.present || raw.DaemonEngine.present || raw.DaemonSocketPath.present {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath, "module instance must not carry site, node, or daemon locality")
	}
	return renderUnitInstance{id: raw.ID, scope: raw.Scope}, nil
}

func validateNodeLocalRenderUnitInstanceIdentity(raw rawRenderUnitInstance, siteSet, nodeSet map[string]struct{}, instancePath string) (renderUnitInstance, error) {
	if !raw.SiteRef.present || !raw.NodeRef.present {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath, "node-local instance requires exact siteRef and nodeRef")
	}
	if err := requireContractID(raw.SiteRef.value, instancePath+".siteRef"); err != nil {
		return renderUnitInstance{}, err
	}
	if err := requireContractID(raw.NodeRef.value, instancePath+".nodeRef"); err != nil {
		return renderUnitInstance{}, err
	}
	if _, exists := siteSet[raw.SiteRef.value]; !exists {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath+".siteRef", "references site %q outside the logical render unit", raw.SiteRef.value)
	}
	if _, exists := nodeSet[raw.NodeRef.value]; !exists {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath+".nodeRef", "references node %q outside the logical render unit", raw.NodeRef.value)
	}
	return renderUnitInstance{
		id: raw.ID, scope: raw.Scope, siteRef: raw.SiteRef.value, nodeRef: raw.NodeRef.value,
	}, nil
}

func validateNonDaemonRenderUnitInstanceIdentity(unit rawRenderUnit, raw rawRenderUnitInstance, instance renderUnitInstance, instancePath string) (renderUnitInstance, error) {
	wantID := unit.ID + "-node-" + instance.nodeRef
	if raw.ID != wantID {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath+".id", "node-local instance must use exact ID %q", wantID)
	}
	if raw.DaemonRef.present || raw.DaemonInstanceRef.present || raw.DaemonEngine.present || raw.DaemonSocketPath.present {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath, "non-daemon instance must not carry daemon identity or socket metadata")
	}
	return instance, nil
}

func validateDaemonRenderUnitInstanceIdentity(unit rawRenderUnit, raw rawRenderUnitInstance, instance renderUnitInstance, placement rawRenderUnitPlacement, daemonBindings map[string]rawRuntimeDaemonBinding, instancePath string) (renderUnitInstance, error) {
	if !raw.DaemonRef.present || !raw.DaemonInstanceRef.present || !raw.DaemonEngine.present || !raw.DaemonSocketPath.present {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath, "one-per-daemon instance requires daemonRef, daemonInstanceRef, daemonEngine, and daemonSocketPath")
	}
	if err := requireContractID(raw.DaemonRef.value, instancePath+".daemonRef"); err != nil {
		return renderUnitInstance{}, err
	}
	if err := requireContractID(raw.DaemonInstanceRef.value, instancePath+".daemonInstanceRef"); err != nil {
		return renderUnitInstance{}, err
	}
	if raw.DaemonEngine.value != "docker" {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath+".daemonEngine", "must be docker")
	}
	if err := validateDockerSocketPath(raw.DaemonSocketPath.value); err != nil {
		return renderUnitInstance{}, wrap(ErrInvalidPlan, instancePath+".daemonSocketPath", "invalid Docker socket path", err)
	}
	if !placement.DaemonRef.present || raw.DaemonRef.value != placement.DaemonRef.value {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath+".daemonRef", "does not match logical placement daemonRef")
	}
	binding, exists := daemonBindings[instance.nodeRef]
	if !exists || binding.SiteRef != instance.siteRef || binding.DaemonRef != raw.DaemonRef.value || binding.InstanceRef != raw.DaemonInstanceRef.value || binding.Engine != raw.DaemonEngine.value || binding.SocketPath != raw.DaemonSocketPath.value {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath, "daemon identity does not exactly match the node-scoped resolved daemon binding")
	}
	wantID := unit.ID + "-node-" + instance.nodeRef + "-daemon-" + raw.DaemonInstanceRef.value
	if raw.ID != wantID {
		return renderUnitInstance{}, fail(ErrInvalidPlan, instancePath+".id", "daemon instance must use exact ID %q", wantID)
	}
	instance.daemonRef, instance.daemonInstanceRef = raw.DaemonRef.value, raw.DaemonInstanceRef.value
	instance.daemonEngine, instance.daemonSocketPath = raw.DaemonEngine.value, raw.DaemonSocketPath.value
	return instance, nil
}

func validateRenderInstanceOutputs(rawOutputs []rawRenderInstanceOutput, logicalSet map[string]struct{}, instancePath string) ([]renderInstanceOutput, error) {
	if rawOutputs == nil || len(rawOutputs) != len(logicalSet) {
		return nil, fail(ErrInvalidPlan, instancePath+".outputs", "must materialize every logical output exactly once")
	}
	outputs := make([]renderInstanceOutput, 0, len(rawOutputs))
	seenRefs := make(map[string]struct{}, len(rawOutputs))
	seenArtifacts := make(map[string]struct{}, len(rawOutputs))
	seenPaths := make(map[string]struct{}, len(rawOutputs))
	for index, raw := range rawOutputs {
		outputPath := fmt.Sprintf("%s.outputs[%d]", instancePath, index)
		if _, exists := logicalSet[raw.Ref]; !exists {
			return nil, fail(ErrInvalidPlan, outputPath+".ref", "references undeclared logical output %q", raw.Ref)
		}
		if _, exists := seenRefs[raw.Ref]; exists {
			return nil, fail(ErrDuplicate, outputPath+".ref", "logical output %q is materialized more than once", raw.Ref)
		}
		if err := requireContractID(raw.ArtifactRef, outputPath+".artifactRef"); err != nil {
			return nil, err
		}
		if _, err := validatePortablePath(raw.Path); err != nil {
			return nil, wrap(ErrInvalidPath, outputPath+".path", "invalid materialized output path", err)
		}
		if _, exists := seenArtifacts[raw.ArtifactRef]; exists {
			return nil, fail(ErrDuplicate, outputPath+".artifactRef", "artifact %q is materialized more than once in the instance", raw.ArtifactRef)
		}
		pathKey := portableKey(raw.Path)
		if _, exists := seenPaths[pathKey]; exists {
			return nil, fail(ErrDuplicate, outputPath+".path", "path %q is materialized more than once in the instance", raw.Path)
		}
		seenRefs[raw.Ref], seenArtifacts[raw.ArtifactRef], seenPaths[pathKey] = struct{}{}, struct{}{}, struct{}{}
		outputs = append(outputs, renderInstanceOutput{ref: raw.Ref, artifactID: raw.ArtifactRef, path: raw.Path})
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].ref < outputs[j].ref })
	return outputs, nil
}

func indexRuntimeDaemonBindings(rawBindings []json.RawMessage, bindingPath string) (map[string]rawRuntimeDaemonBinding, error) {
	result := make(map[string]rawRuntimeDaemonBinding, len(rawBindings))
	for index, raw := range rawBindings {
		var binding rawRuntimeDaemonBinding
		path := fmt.Sprintf("%s[%d]", bindingPath, index)
		if err := decodeStrict(raw, &binding); err != nil {
			return nil, wrap(ErrInvalidPlan, path, "decode runtime daemon binding", err)
		}
		for _, field := range []struct{ name, value string }{
			{name: "siteRef", value: binding.SiteRef}, {name: "nodeRef", value: binding.NodeRef},
			{name: "daemonRef", value: binding.DaemonRef}, {name: "instanceRef", value: binding.InstanceRef},
		} {
			if err := requireContractID(field.value, path+"."+field.name); err != nil {
				return nil, err
			}
		}
		if binding.Engine != "docker" {
			return nil, fail(ErrInvalidPlan, path+".engine", "runtime daemon binding engine must be docker")
		}
		if err := validateDockerSocketPath(binding.SocketPath); err != nil {
			return nil, wrap(ErrInvalidPlan, path+".socketPath", "invalid runtime daemon socket path", err)
		}
		if _, exists := result[binding.NodeRef]; exists {
			return nil, fail(ErrDuplicate, path+".nodeRef", "node %q has more than one daemon binding", binding.NodeRef)
		}
		result[binding.NodeRef] = binding
	}
	return result, nil
}

func validateDirectSocketRequirements(requirements []implementationInterface, instances []renderUnitInstance, placement rawRenderUnitPlacement, unitPath string) error {
	for _, requirement := range requirements {
		if requirement.kind != dockerSocketDirectInterfaceKind {
			continue
		}
		requirementPath := unitPath + ".requiresInterfaces." + requirement.id
		if placement.Scope != "node-local" || placement.Cardinality != "one-per-daemon" {
			return fail(ErrInvalidPlan, requirementPath, "direct Docker socket access requires one-per-daemon placement")
		}
		for _, instance := range instances {
			if instance.daemonEngine != "docker" || instance.daemonRef != requirement.daemonRef {
				return fail(ErrInvalidPlan, requirementPath, "must select the exact Docker daemon binding for every render instance")
			}
			if requirement.socketPathSource == "" && instance.daemonSocketPath != requirement.socketPath {
				return fail(ErrInvalidPlan, requirementPath+".endpoint.path", "must exactly match every node-scoped Docker daemon binding for the render unit")
			}
		}
	}
	return nil
}

func validateDockerSocketPath(value string) error {
	// len(string) is the UTF-8 byte length in Go, which is the Linux ABI bound.
	if byteLength := len(value); byteLength > maxDockerSocketPathBytes {
		return fmt.Errorf("is %d UTF-8 bytes; Linux AF_UNIX paths permit at most %d bytes before the terminating NUL", byteLength, maxDockerSocketPathBytes)
	}
	if !dockerSocketPathPattern.MatchString(value) || !path.IsAbs(value) || path.Clean(value) != value {
		return fmt.Errorf("must be an absolute canonical path with portable ASCII segments and a .sock suffix")
	}
	for _, segment := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("must not contain dot or parent-directory segments")
		}
	}
	return nil
}

func canonicalImplementationInterfaces(values []json.RawMessage, valuePath string) ([]byte, error) {
	canonical := make([]json.RawMessage, 0, len(values))
	for index, raw := range values {
		object, err := decodeRawObject(raw, fmt.Sprintf("%s[%d]", valuePath, index))
		if err != nil {
			return nil, err
		}
		value, err := canonicalObject(object)
		if err != nil {
			return nil, wrap(ErrInvalidPlan, fmt.Sprintf("%s[%d]", valuePath, index), "canonicalize implementation interface", err)
		}
		canonical = append(canonical, value)
	}
	return json.Marshal(canonical)
}

func parseImplementationInterfaces(values []json.RawMessage, role, valuePath string) ([]implementationInterface, error) {
	result := make([]implementationInterface, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, raw := range values {
		interfacePath := fmt.Sprintf("%s[%d]", valuePath, index)
		contract, err := parseImplementationInterface(raw, role, interfacePath)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[contract.id]; duplicate {
			return nil, fail(ErrDuplicate, interfacePath+".id", "duplicate %s interface %q", role, contract.id)
		}
		seen[contract.id] = struct{}{}
		result = append(result, contract)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result, nil
}

//nolint:gocyclo // Interface parsing exhaustively enforces mutually exclusive socket and network endpoint forms at the trust boundary.
func parseImplementationInterface(raw json.RawMessage, role, interfacePath string) (implementationInterface, error) {
	object, err := decodeRawObject(raw, interfacePath)
	if err != nil {
		return implementationInterface{}, err
	}
	id, err := requiredRawString(object, "id", interfacePath+".id")
	if err != nil {
		return implementationInterface{}, err
	}
	if err := requireContractID(id, interfacePath+".id"); err != nil {
		return implementationInterface{}, err
	}
	kind, err := requiredRawString(object, "kind", interfacePath+".kind")
	if err != nil {
		return implementationInterface{}, err
	}
	daemonRef, err := requiredRawString(object, "daemonRef", interfacePath+".daemonRef")
	if err != nil {
		return implementationInterface{}, err
	}
	policyProfile, err := requiredRawString(object, "policyProfile", interfacePath+".policyProfile")
	if err != nil {
		return implementationInterface{}, err
	}
	for _, field := range []struct{ name, value string }{{"daemonRef", daemonRef}, {"policyProfile", policyProfile}} {
		if err := requireContractID(field.value, interfacePath+"."+field.name); err != nil {
			return implementationInterface{}, err
		}
	}
	endpoint, err := requiredRawObject(object, "endpoint", interfacePath+".endpoint")
	if err != nil {
		return implementationInterface{}, err
	}
	networkRef, networkPresent, err := optionalRawContractID(endpoint, "networkRef", interfacePath+".endpoint.networkRef")
	if err != nil {
		return implementationInterface{}, err
	}
	endpointRef, endpointPresent, err := optionalRawContractID(endpoint, "ref", interfacePath+".endpoint.ref")
	if err != nil {
		return implementationInterface{}, err
	}
	if networkPresent != endpointPresent {
		return implementationInterface{}, fail(ErrInvalidPlan, interfacePath+".endpoint", "networkRef and ref must be present together")
	}
	var socketPath, socketPathSource string
	if kind == dockerSocketDirectInterfaceKind {
		if networkPresent || endpointPresent {
			return implementationInterface{}, fail(ErrInvalidPlan, interfacePath+".endpoint", "direct Docker socket endpoints cannot carry networkRef or ref")
		}
		_, hasPath := endpoint["path"]
		_, hasPathSource := endpoint["pathSource"]
		if hasPath == hasPathSource {
			return implementationInterface{}, fail(ErrInvalidPlan, interfacePath+".endpoint", "exactly one of path or pathSource is required")
		}
		if hasPath {
			socketPath, err = requiredRawString(endpoint, "path", interfacePath+".endpoint.path")
			if err != nil {
				return implementationInterface{}, err
			}
			if err := validateDockerSocketPath(socketPath); err != nil {
				return implementationInterface{}, wrap(ErrInvalidPlan, interfacePath+".endpoint.path", "invalid direct Docker socket path", err)
			}
		} else {
			socketPathSource, err = requiredRawString(endpoint, "pathSource", interfacePath+".endpoint.pathSource")
			if err != nil {
				return implementationInterface{}, err
			}
			if socketPathSource != dockerSocketPathSourceDaemonBinding {
				return implementationInterface{}, fail(ErrInvalidPlan, interfacePath+".endpoint.pathSource", "must be %q", dockerSocketPathSourceDaemonBinding)
			}
		}
	}
	contract := implementationInterface{
		id: id, kind: kind, daemonRef: daemonRef, networkRef: networkRef,
		policyProfile: policyProfile, endpointRef: endpointRef, socketPath: socketPath, socketPathSource: socketPathSource,
	}
	providerRaw, hasProviderBindings := object["providerBindings"]
	if role == "provider" && hasProviderBindings {
		return implementationInterface{}, fail(ErrInvalidPlan, interfacePath+".providerBindings", "provider interfaces cannot carry provider bindings")
	}
	if role == "consumer" && kind == "docker-http-readonly-v1" && !hasProviderBindings {
		return implementationInterface{}, fail(ErrInvalidPlan, interfacePath+".providerBindings", "networked required interface must carry exact provider bindings")
	}
	if !hasProviderBindings {
		return contract, nil
	}
	bindings, err := decodeImplementationProviderBindings(providerRaw, kind, interfacePath+".providerBindings")
	if err != nil {
		return implementationInterface{}, err
	}
	contract.providerBindings = bindings
	return contract, nil
}

func decodeImplementationProviderBindings(raw json.RawMessage, kind, valuePath string) ([]implementationProviderBinding, error) {
	var values []json.RawMessage
	if err := decodeJSON(raw, &values, false); err != nil || values == nil {
		if err == nil {
			err = fmt.Errorf("must be an array")
		}
		return nil, wrap(ErrInvalidPlan, valuePath, "decode provider bindings", err)
	}
	if kind == "docker-http-readonly-v1" && len(values) == 0 {
		return nil, fail(ErrInvalidPlan, valuePath, "networked required interface needs at least one provider binding")
	}
	return parseImplementationProviderBindings(values, valuePath)
}

func optionalRawContractID(object map[string]json.RawMessage, name, valuePath string) (string, bool, error) {
	raw, exists := object[name]
	if !exists {
		return "", false, nil
	}
	var value string
	if err := decodeJSON(raw, &value, false); err != nil {
		return "", false, wrap(ErrInvalidPlan, valuePath, "decode contract ID", err)
	}
	if err := requireContractID(value, valuePath); err != nil {
		return "", false, err
	}
	return value, true, nil
}

func parseImplementationProviderBindings(values []json.RawMessage, valuePath string) ([]implementationProviderBinding, error) {
	result := make([]implementationProviderBinding, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, raw := range values {
		bindingPath := fmt.Sprintf("%s[%d]", valuePath, index)
		var decoded rawImplementationProviderBinding
		if err := decodeStrict(raw, &decoded); err != nil {
			return nil, wrap(ErrInvalidPlan, bindingPath, "decode implementation provider binding", err)
		}
		fields := map[string]string{
			"interfaceRef": decoded.InterfaceRef, "moduleRef": decoded.ModuleRef, "unitRef": decoded.UnitRef,
			"providerInstanceRef": decoded.ProviderInstanceRef, "consumerInstanceRef": decoded.ConsumerInstanceRef,
			"siteRef": decoded.SiteRef, "nodeRef": decoded.NodeRef, "daemonRef": decoded.DaemonRef,
			"daemonInstanceRef": decoded.DaemonInstanceRef, "networkRef": decoded.NetworkRef,
			"networkInstanceRef": decoded.NetworkInstanceRef, "policyProfile": decoded.PolicyProfile,
			"endpointRef": decoded.EndpointRef,
		}
		for name, value := range fields {
			if err := requireContractID(value, bindingPath+"."+name); err != nil {
				return nil, err
			}
		}
		key := decoded.ConsumerInstanceRef
		if _, duplicate := seen[key]; duplicate {
			return nil, fail(ErrDuplicate, bindingPath, "consumer instance has more than one provider network binding")
		}
		seen[key] = struct{}{}
		result = append(result, implementationProviderBinding{
			interfaceRef: decoded.InterfaceRef, moduleRef: decoded.ModuleRef, unitRef: decoded.UnitRef,
			providerInstanceRef: decoded.ProviderInstanceRef, consumerInstanceRef: decoded.ConsumerInstanceRef,
			siteRef: decoded.SiteRef, nodeRef: decoded.NodeRef, daemonRef: decoded.DaemonRef,
			daemonInstanceRef: decoded.DaemonInstanceRef, networkRef: decoded.NetworkRef,
			networkInstanceRef: decoded.NetworkInstanceRef, policyProfile: decoded.PolicyProfile,
			endpointRef: decoded.EndpointRef,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].consumerInstanceRef != result[j].consumerInstanceRef {
			return result[i].consumerInstanceRef < result[j].consumerInstanceRef
		}
		return result[i].networkInstanceRef < result[j].networkInstanceRef
	})
	return result, nil
}

func validateRuntimeNetworkBindings(raw []rawRuntimeNetworkBinding, instance renderUnitInstance, valuePath string) ([]runtimeNetworkBinding, []byte, error) {
	if raw == nil {
		return nil, nil, fail(ErrInvalidPlan, valuePath+".networkBindings", "is required, even when empty")
	}
	bindings := make([]runtimeNetworkBinding, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for index, decoded := range raw {
		bindingPath := fmt.Sprintf("%s.networkBindings[%d]", valuePath, index)
		fields := map[string]string{
			"networkInstanceRef": decoded.NetworkInstanceRef, "networkRef": decoded.NetworkRef,
			"interfaceRef": decoded.InterfaceRef, "siteRef": decoded.SiteRef, "nodeRef": decoded.NodeRef,
			"daemonRef": decoded.DaemonRef, "daemonInstanceRef": decoded.DaemonInstanceRef,
		}
		for name, value := range fields {
			if err := requireContractID(value, bindingPath+"."+name); err != nil {
				return nil, nil, err
			}
		}
		if decoded.Role != "provider" && decoded.Role != "consumer" {
			return nil, nil, fail(ErrInvalidPlan, bindingPath+".role", "must be provider or consumer")
		}
		owner, err := validateRuntimeNetworkOwner(decoded.Owner, bindingPath+".owner")
		if err != nil {
			return nil, nil, err
		}
		key := decoded.NetworkInstanceRef + "\x00" + decoded.Role + "\x00" + decoded.InterfaceRef
		if _, duplicate := seen[key]; duplicate {
			return nil, nil, fail(ErrDuplicate, bindingPath, "runtime network binding is duplicated")
		}
		seen[key] = struct{}{}
		bindings = append(bindings, runtimeNetworkBinding{
			networkInstanceRef: decoded.NetworkInstanceRef, networkRef: decoded.NetworkRef,
			role: decoded.Role, interfaceRef: decoded.InterfaceRef, siteRef: decoded.SiteRef,
			nodeRef: decoded.NodeRef, daemonRef: decoded.DaemonRef, daemonInstanceRef: decoded.DaemonInstanceRef,
			owner: owner,
		})
	}
	sort.Slice(raw, func(i, j int) bool {
		if raw[i].NetworkInstanceRef != raw[j].NetworkInstanceRef {
			return raw[i].NetworkInstanceRef < raw[j].NetworkInstanceRef
		}
		if raw[i].Role != raw[j].Role {
			return raw[i].Role < raw[j].Role
		}
		return raw[i].InterfaceRef < raw[j].InterfaceRef
	})
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].networkInstanceRef != bindings[j].networkInstanceRef {
			return bindings[i].networkInstanceRef < bindings[j].networkInstanceRef
		}
		if bindings[i].role != bindings[j].role {
			return bindings[i].role < bindings[j].role
		}
		return bindings[i].interfaceRef < bindings[j].interfaceRef
	})
	canonical, err := json.Marshal(raw)
	if err != nil {
		return nil, nil, wrap(ErrInvalidPlan, valuePath+".networkBindings", "canonicalize runtime network bindings", err)
	}
	return bindings, canonical, nil
}

func validateRuntimeNetworkOwner(raw rawRuntimeNetworkOwner, valuePath string) (runtimeNetworkOwner, error) {
	fields := map[string]string{
		"moduleRef": raw.ModuleRef, "unitRef": raw.UnitRef,
		"instanceRef": raw.InstanceRef, "interfaceRef": raw.InterfaceRef,
	}
	for name, value := range fields {
		if err := requireContractID(value, valuePath+"."+name); err != nil {
			return runtimeNetworkOwner{}, err
		}
	}
	return runtimeNetworkOwner{
		moduleRef: raw.ModuleRef, unitRef: raw.UnitRef,
		instanceRef: raw.InstanceRef, interfaceRef: raw.InterfaceRef,
	}, nil
}

func validateRenderUnitIdentity(unit rawRenderUnit, unitPath string) error {
	for field, value := range map[string]string{"id": unit.ID, "kind": unit.Kind, "rendererRef": unit.RendererRef} {
		if err := requireContractID(value, unitPath+"."+field); err != nil {
			return err
		}
	}
	if strings.TrimSpace(unit.TemplateRef) == "" || strings.ContainsAny(unit.TemplateRef, "\r\n\t") {
		return fail(ErrInvalidPlan, unitPath+".templateRef", "a non-empty template reference is required")
	}
	if strings.TrimSpace(unit.Version) == "" {
		return fail(ErrInvalidPlan, unitPath+".version", "an exact render-unit version is required")
	}
	if !validSHA256(unit.ContractHash) {
		return fail(ErrInvalidPlan, unitPath+".contractHash", "must be a lowercase sha256 digest")
	}
	if len(unit.CompatibleTargets) != 1 || !oneOf(unit.CompatibleTargets[0], "compose", "opentofu") {
		return fail(ErrInvalidPlan, unitPath+".compatibleTargets", "resolved render unit must carry exactly one selected generation target")
	}
	return nil
}

func validateRenderUnitInputs(unit rawRenderUnit, unitPath string) ([]byte, []byte, error) {
	if unit.PublicInputRefs == nil || unit.SecretInputRefs == nil || unit.Values == nil || unit.SecretRefs == nil {
		return nil, nil, fail(ErrInvalidPlan, unitPath, "publicInputRefs, secretInputRefs, values, and secretRefs are mandatory")
	}
	publicSet, err := uniqueContractIDSet(unit.PublicInputRefs, unitPath+".publicInputRefs")
	if err != nil {
		return nil, nil, err
	}
	secretSet, err := uniqueContractIDSet(unit.SecretInputRefs, unitPath+".secretInputRefs")
	if err != nil {
		return nil, nil, err
	}
	if err := requireObjectKeysSubset(unit.Values, publicSet, unitPath+".values"); err != nil {
		return nil, nil, err
	}
	if err := requireCompleteSecretRefs(unit.SecretRefs, secretSet, unitPath+".secretRefs"); err != nil {
		return nil, nil, err
	}
	values, err := canonicalObject(unit.Values)
	if err != nil {
		return nil, nil, wrap(ErrInvalidPlan, unitPath+".values", "canonicalize public inputs", err)
	}
	secrets, err := canonicalObject(unit.SecretRefs)
	if err != nil {
		return nil, nil, wrap(ErrInvalidPlan, unitPath+".secretRefs", "canonicalize secret references", err)
	}
	return values, secrets, nil
}

func validateRenderUnitPlanInputs(unit rawRenderUnit, unitPath string) ([]string, []byte, error) {
	if unit.PlanInputRefs == nil || unit.PlanInputs == nil {
		return nil, nil, fail(ErrInvalidPlan, unitPath, "planInputRefs and planInputs are mandatory")
	}
	declared := make(map[string]struct{}, len(unit.PlanInputRefs))
	refs := append([]string(nil), unit.PlanInputRefs...)
	for index, ref := range refs {
		if _, allowed := allowedRendererPlanInputRefs[ref]; !allowed {
			return nil, nil, fail(ErrInvalidPlan, fmt.Sprintf("%s.planInputRefs[%d]", unitPath, index), "unsupported compiler plan input %q", ref)
		}
		if _, duplicate := declared[ref]; duplicate {
			return nil, nil, fail(ErrDuplicate, fmt.Sprintf("%s.planInputRefs[%d]", unitPath, index), "duplicate plan input %q", ref)
		}
		declared[ref] = struct{}{}
	}
	for _, ref := range append(append([]string(nil), unit.PublicInputRefs...), unit.SecretInputRefs...) {
		if _, conflict := declared[ref]; conflict {
			return nil, nil, fail(ErrInvalidPlan, unitPath+".planInputRefs", "compiler plan input %q aliases a public or secret input", ref)
		}
	}
	if err := requireObjectKeysSubset(unit.PlanInputs, declared, unitPath+".planInputs"); err != nil {
		return nil, nil, err
	}
	if len(unit.PlanInputs) != len(declared) {
		return nil, nil, fail(ErrInvalidPlan, unitPath+".planInputs", "must be the exact 1:1 planInputRefs projection")
	}
	for ref := range declared {
		if _, exists := unit.PlanInputs[ref]; !exists {
			return nil, nil, fail(ErrInvalidPlan, unitPath+".planInputs", "declared plan input %q is missing", ref)
		}
	}
	canonical, err := canonicalObject(unit.PlanInputs)
	if err != nil {
		return nil, nil, wrap(ErrInvalidPlan, unitPath+".planInputs", "canonicalize compiler plan inputs", err)
	}
	sort.Strings(refs)
	return refs, canonical, nil
}

var allowedRendererPlanInputRefs = map[string]struct{}{
	"stackId": {}, "kit": {}, "sites": {}, "controlPlane": {},
	"bridge": {}, "identity": {}, "data": {}, "failurePolicy": {},
	"localReachability": {}, "identityTrust": {}, "homeLANDiscovery": {},
	"moduleTargets": {}, "moduleCapabilities": {}, "hostRuntimePolicy": {},
	"storagePolicy": {}, "localNetworkPolicy": {}, "cloudNetworkPolicy": {}, "publicEdge": {}, "publicTLS": {},
	"cloudOffsiteBackup": {},
}

func requireCompleteSecretRefs(refs map[string]json.RawMessage, declared map[string]struct{}, valuePath string) error {
	if err := requireObjectKeysSubset(refs, declared, valuePath); err != nil {
		return err
	}
	if len(refs) != len(declared) {
		return fail(ErrInvalidPlan, valuePath, "every declared secret input requires an opaque secret reference")
	}
	for key := range declared {
		if _, exists := refs[key]; !exists {
			return fail(ErrInvalidPlan, valuePath, "declared secret input %q has no opaque reference", key)
		}
	}
	return requireOpaqueSecretReferences(refs, valuePath)
}

func validateRenderUnitOutputs(declared []string, unitPath string) ([]string, error) {
	if len(declared) == 0 {
		return nil, fail(ErrInvalidPlan, unitPath+".outputs", "at least one output is required")
	}
	outputs := append([]string(nil), declared...)
	seen := make(map[string]struct{}, len(outputs))
	for index, output := range outputs {
		if _, err := validatePortablePath(output); err != nil {
			return nil, wrap(ErrInvalidPath, fmt.Sprintf("%s.outputs[%d]", unitPath, index), "invalid render output", err)
		}
		key := portableKey(output)
		if _, exists := seen[key]; exists {
			return nil, fail(ErrDuplicate, fmt.Sprintf("%s.outputs[%d]", unitPath, index), "duplicate output %q", output)
		}
		seen[key] = struct{}{}
	}
	sort.Strings(outputs)
	return outputs, nil
}

func requireObjectKeysSubset(values map[string]json.RawMessage, expected map[string]struct{}, valuePath string) error {
	for key, raw := range values {
		if _, exists := expected[key]; !exists {
			return fail(ErrInvalidPlan, valuePath+"."+key, "input is not declared")
		}
		if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fail(ErrInvalidPlan, valuePath+"."+key, "input value must not be null")
		}
	}
	return nil
}

func requireOpaqueSecretReferences(values map[string]json.RawMessage, valuePath string) error {
	for key, raw := range values {
		var reference string
		if err := decodeJSON(raw, &reference, false); err != nil || !validSecretReference(reference) {
			if err == nil {
				err = fmt.Errorf("must be an opaque secret reference URI")
			}
			return wrap(ErrInvalidPlan, valuePath+"."+key, "secret material must remain an opaque reference", err)
		}
	}
	return nil
}

func validSecretReference(value string) bool {
	for _, prefix := range []string{"secret://", "vault://", "doppler://", "techstack://"} {
		if strings.HasPrefix(value, prefix) && len(value) > len(prefix) && !strings.ContainsAny(value, " \t\r\n") {
			return true
		}
	}
	return false
}

func uniqueContractIDSet(values []string, valuePath string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(values))
	for index, value := range values {
		if err := requireContractID(value, fmt.Sprintf("%s[%d]", valuePath, index)); err != nil {
			return nil, err
		}
		key := value
		if _, exists := result[key]; exists {
			return nil, fail(ErrDuplicate, fmt.Sprintf("%s[%d]", valuePath, index), "duplicate reference %q", value)
		}
		result[key] = struct{}{}
	}
	return result, nil
}

func decodeRawObject(data []byte, valuePath string) (map[string]json.RawMessage, error) {
	var result map[string]json.RawMessage
	if err := decodeJSON(data, &result, false); err != nil {
		return nil, wrap(ErrInvalidPlan, valuePath, "decode object", err)
	}
	if result == nil {
		return nil, fail(ErrInvalidPlan, valuePath, "must be an object")
	}
	return result, nil
}

func requiredRawObject(object map[string]json.RawMessage, name, valuePath string) (map[string]json.RawMessage, error) {
	raw, exists := object[name]
	if !exists {
		return nil, fail(ErrInvalidPlan, valuePath, "is required")
	}
	return decodeRawObject(raw, valuePath)
}

func requiredRawArray(object map[string]json.RawMessage, name, valuePath string) ([]json.RawMessage, error) {
	raw, exists := object[name]
	if !exists {
		return nil, fail(ErrInvalidPlan, valuePath, "is required")
	}
	var result []json.RawMessage
	if err := decodeJSON(raw, &result, false); err != nil || result == nil {
		if err == nil {
			err = fmt.Errorf("must be an array")
		}
		return nil, wrap(ErrInvalidPlan, valuePath, "decode array", err)
	}
	return result, nil
}

func requiredRawString(object map[string]json.RawMessage, name, valuePath string) (string, error) {
	raw, exists := object[name]
	if !exists {
		return "", fail(ErrInvalidPlan, valuePath, "is required")
	}
	var result string
	if err := decodeJSON(raw, &result, false); err != nil || strings.TrimSpace(result) == "" {
		if err == nil {
			err = fmt.Errorf("must be a non-empty string")
		}
		return "", wrap(ErrInvalidPlan, valuePath, "decode string", err)
	}
	return result, nil
}

func requiredStringArray(object map[string]json.RawMessage, name, valuePath string) ([]string, error) {
	raw, exists := object[name]
	if !exists {
		return nil, fail(ErrInvalidPlan, valuePath, "is required")
	}
	var result []string
	if err := decodeJSON(raw, &result, false); err != nil || result == nil {
		if err == nil {
			err = fmt.Errorf("must be an array")
		}
		return nil, wrap(ErrInvalidPlan, valuePath, "decode string array", err)
	}
	return result, nil
}

func decodeStrict(data []byte, target any) error { return decodeJSON(data, target, true) }

func decodeJSON(data []byte, target any, strict bool) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return fmt.Errorf("multiple JSON values")
	} else if err != io.EOF {
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return nil
}

func canonicalObject(value map[string]json.RawMessage) ([]byte, error) {
	// encoding/json sorts string map keys and RawMessage preserves the already
	// canonical scalar/object representation carried by VerifiedPlan.
	return json.Marshal(value)
}

func requireContractID(value, valuePath string) error {
	if !contractIDPattern.MatchString(value) {
		return fail(ErrInvalidPlan, valuePath, "must be a non-empty portable contract ID")
	}
	return nil
}

func joinOutputPath(outputRoot, output string) string {
	if outputRoot == "." {
		return output
	}
	return path.Join(outputRoot, output)
}

func validatePortablePath(value string) (string, error) {
	if value == "" || strings.ContainsRune(value, '\x00') || strings.Contains(value, `\`) || strings.ContainsAny(value, `<>:"|?*`) {
		return "", fmt.Errorf("must be a non-empty portable slash-separated relative path")
	}
	if strings.HasPrefix(value, "/") || (len(value) >= 2 && value[1] == ':') {
		return "", fmt.Errorf("absolute, drive-relative, and UNC paths are forbidden")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != value {
		return "", fmt.Errorf("path must be canonical and remain beneath its root")
	}
	for _, segment := range strings.Split(clean, "/") {
		if strings.TrimRight(segment, ". ") != segment || windowsReservedSegment(segment) {
			return "", fmt.Errorf("path is not portable to Windows")
		}
	}
	return clean, nil
}

func windowsReservedSegment(segment string) bool {
	base := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}

func portableKey(value string) string {
	// Architecture v2 output paths are portable contracts, not host-native
	// paths. Fold case on every host so one canonical plan cannot be accepted
	// on a case-sensitive builder and rejected later on Windows.
	return strings.ToLower(value)
}

func validSHA256(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
