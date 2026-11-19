package architecturev2renderer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/kombifyio/stackkits/internal/accessgatetable"
	"github.com/kombifyio/stackkits/internal/householdapps"
	"github.com/kombifyio/stackkits/internal/runtimeprofile"
)

var (
	contractIDPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]*$`)
	dockerSocketPathPattern = regexp.MustCompile(`^/[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*[.]sock$`)
)

const dockerSocketDirectInterfaceKind = "docker-socket-direct-v1"
const dockerSocketPathSourceDaemonBinding = "daemon-binding"
const maxDockerSocketPathBytes = 107

type renderPlan struct {
	localNetwork           *runtimeprofile.LocalNetwork
	dataProfile            runtimeprofile.DataProfile
	offlineBundle          *runtimeprofile.OfflineBundleRef
	listenerAddresses      map[string]string
	outputRoot             string
	networkDomainBase      string
	networkSubdomainPrefix string
	modules                []renderModule
	artifacts              map[string]artifactContract
	bindings               map[instanceOutputKey]outputBinding
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
	runtimeListenersCanonical    []byte
	providedInterfacesCanonical  []byte
	requiredInterfacesCanonical  []byte
	privilegedApprovalsCanonical []byte
	serviceEndpoints             []serviceEndpointContract
	providedInterfaces           []implementationInterface
	requiredInterfaces           []implementationInterface
	outputs                      []string
	instances                    []renderUnitInstance
	accelerator                  *ModuleAccelerator
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
	ApplyMode           string                     `json:"applyMode"`
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
	RuntimeListeners    []json.RawMessage          `json:"runtimeListeners"`
	ProvidesInterfaces  []json.RawMessage          `json:"providesInterfaces"`
	RequiresInterfaces  []json.RawMessage          `json:"requiresInterfaces"`
	Instances           []rawRenderUnitInstance    `json:"instances"`
}

type rawModuleServiceEndpoint struct {
	ServiceRef              string          `json:"serviceRef"`
	UpstreamProtocol        string          `json:"upstreamProtocol"`
	TargetPort              int             `json:"targetPort"`
	RequiredPrivilege       string          `json:"requiredPrivilege"`
	IngressAuth             string          `json:"ingressAuth,omitempty"`
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

type rawModuleRuntimeListener struct {
	ID                string              `json:"id"`
	ComponentRef      string              `json:"componentRef"`
	Transport         string              `json:"transport"`
	BindAddress       string              `json:"bindAddress"`
	BindAddressSource string              `json:"bindAddressSource,omitempty"`
	Port              int                 `json:"port"`
	TargetPort        int                 `json:"targetPort"`
	Sharing           string              `json:"sharing"`
	ListenerGroupRef  optionalStringField `json:"listenerGroupRef,omitempty"`
	Exposure          string              `json:"exposure"`
	SourceServiceRefs []string            `json:"sourceServiceRefs"`
}

type rawModuleRuntime struct {
	Execution         string                 `json:"execution"`
	Kind              string                 `json:"kind"`
	Delivery          string                 `json:"delivery"`
	Engine            optionalStringField    `json:"engine,omitempty"`
	Image             *rawModuleRuntimeImage `json:"image,omitempty"`
	EntryComponentRef optionalStringField    `json:"entryComponentRef,omitempty"`
	Components        []json.RawMessage      `json:"components,omitempty"`
	// ResidentComponents are parts of a native module that run inside an
	// existing StackKits process; they render nothing.
	ResidentComponents []string                   `json:"residentComponents,omitempty"`
	Settings           map[string]json.RawMessage `json:"settings,omitempty"`
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
	CoreModuleRef         string                                `json:"coreModuleRef,omitempty"`
	IngressAuth           string                                `json:"ingressAuth"`
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
	Exposure               string                     `json:"exposure"`
	PolicyExposure         string                     `json:"policyExposure"`
	Authentication         string                     `json:"authentication"`
	Privilege              string                     `json:"privilege"`
	EnrolledDeviceRequired bool                       `json:"enrolledDeviceRequired"`
	OwnerStepUpRequired    bool                       `json:"ownerStepUpRequired"`
	OwnerRemoteOptIn       bool                       `json:"ownerRemoteOptIn"`
	AppAccessAway          bool                       `json:"appAccessAway"`
	Audience               *householdapps.Audience    `json:"audience,omitempty"`
	AnonymousRateLimit     *accessgatetable.RateLimit `json:"anonymousRateLimit,omitempty"`
	AllowedSiteRefs        []string                   `json:"allowedSiteRefs,omitempty"`
	AllowedMethods         []string                   `json:"allowedMethods,omitempty"`
	DefaultClosed          bool                       `json:"defaultClosed"`
	PolicyRef              string                     `json:"policyRef"`
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
	settingsCanonical                                                           []byte
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
