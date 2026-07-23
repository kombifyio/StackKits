package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	modernFederationPolicyModuleID    = "stackkits-modern-federation-policy-manifest"
	modernFederationPolicyUnitID      = "policy-bundle"
	modernFederationPolicyRendererRef = "stackkit"
	modernFederationPolicyTemplateRef = "builtin://modern/federation-policy/v1.json"
	modernFederationPolicyVersion     = "1.0.0"
	modernFederationPolicyOutputRef   = "modern/federation/policy.json"
	modernFederationPolicyToken       = "@@PLAN_INPUTS@@"
)

const modernFederationPolicyTemplate = `{"apiVersion":"stackkit.federation-policy/v1","kind":"FederationPolicyManifest","contract":{"capabilities":{"bridgePolicy":"manifested","partitionPolicy":"manifested"},"runtime":{"controlAgent":"unverified","deviceVerifier":"unverified","link":"unverified","partitionEnforcement":"unverified","policyEnforcement":"unverified","publicationRuntime":"unverified","transportRealization":"not-included"},"scope":"generation-only"},"planInputs":@@PLAN_INPUTS@@}
`

var modernFederationPolicyPlanInputRefs = []string{
	"bridge", "controlPlane", "data", "failurePolicy", "identity", "kit", "sites", "stackId",
}

var modernFederationArchitectureV2IDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func init() {
	registerHomeAccessKitPlanValidator("modern-homelab", func(inputs homeAccessPlanInputs, raw []byte, path string) ([]string, error) {
		homeSites, cloudSites := 0, 0
		for _, site := range inputs.Sites {
			if site.Kind == "home" {
				homeSites++
			}
			if site.Kind == "cloud" {
				cloudSites++
			}
		}
		if homeSites == 0 || cloudSites == 0 {
			return nil, fail(ErrInvalidPlan, path+".sites", "private hybrid home-access composition requires explicit Home and Cloud Sites")
		}
		return validateHomeAccessPlanInputsForKit(inputs, raw, path, true)
	})
	registerHomeLANDiscoveryKitPlanValidator("modern-homelab", func(inputs homeLANDiscoveryPlanInputs, raw []byte, path string) ([]string, error) {
		homeSites, cloudSites := 0, 0
		for _, site := range inputs.Sites {
			if site.Kind == "home" {
				homeSites++
			}
			if site.Kind == "cloud" {
				cloudSites++
			}
		}
		if homeSites == 0 || cloudSites == 0 {
			return nil, fail(ErrInvalidPlan, path+".sites", "private hybrid LAN-discovery composition requires explicit Home and Cloud Sites")
		}
		return validateHomeLANDiscoveryPlanInputsForKit(inputs, raw, path, true)
	})
	registerProductRegistryExtension(func(registry *Registry) error {
		renderer := newModernFederationPolicyRenderer()
		return registry.Register(renderer.contract, renderer)
	})
}

// ModernFederationPolicyRendererContract returns the exact built-in identity
// for the generation-only policy manifest. The hash covers the immutable JSON
// shell and its explicit statement that transport/runtime enforcement is not
// part of this renderer.
func ModernFederationPolicyRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(modernFederationPolicyTemplate))
	return RendererContract{
		Kind: "native-config", RendererRef: modernFederationPolicyRendererRef,
		TemplateRef: modernFederationPolicyTemplateRef, Version: modernFederationPolicyVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type modernFederationPolicyRenderer struct {
	template []byte
	contract RendererContract
}

func newModernFederationPolicyRenderer() modernFederationPolicyRenderer {
	return modernFederationPolicyRenderer{
		template: []byte(modernFederationPolicyTemplate),
		contract: ModernFederationPolicyRendererContract(),
	}
}

//nolint:dupl // Policy renderers intentionally share the same small immutable-template lowering sequence.
func (r modernFederationPolicyRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	planInputs, err := validateModernFederationPolicyUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	if modernFederationTemplateHash(r.template) != r.contract.ContractHash || bytes.Count(r.template, []byte(modernFederationPolicyToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.modern-federation-policy.template", "embedded policy manifest does not match its registered contract")
	}
	output := bytes.Replace(r.template, []byte(modernFederationPolicyToken), planInputs, 1)
	return []UnitOutput{{Ref: modernFederationPolicyOutputRef, Bytes: output}}, nil
}

func modernFederationTemplateHash(template []byte) string {
	sum := sha256.Sum256(template)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type modernFederationPlanInputs struct {
	StackID       string                        `json:"stackId"`
	Kit           modernFederationKit           `json:"kit"`
	Sites         []modernFederationSite        `json:"sites"`
	ControlPlane  modernFederationControlPlane  `json:"controlPlane"`
	Bridge        json.RawMessage               `json:"bridge"`
	Identity      modernFederationIdentity      `json:"identity"`
	Data          json.RawMessage               `json:"data"`
	FailurePolicy modernFederationFailurePolicy `json:"failurePolicy"`
}

type modernFederationKit struct {
	Slug           string `json:"slug"`
	Version        string `json:"version"`
	DefinitionHash string `json:"definitionHash"`
}

type modernFederationSite struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	FailureDomain string `json:"failureDomain"`
}

type modernFederationControlPlane struct {
	Mode             string   `json:"mode"`
	AuthoritySiteRef string   `json:"authoritySiteRef"`
	Members          []string `json:"members"`
}

type modernFederationIdentity struct {
	HumanAuthoritySiteRef   string                           `json:"humanAuthoritySiteRef"`
	DeviceAuthoritySiteRef  string                           `json:"deviceAuthoritySiteRef"`
	EdgeVerifierSiteRefs    []string                         `json:"edgeVerifierSiteRefs"`
	DeviceEnrollment        modernFederationDeviceEnrollment `json:"deviceEnrollment"`
	PossessionBoundSessions bool                             `json:"possessionBoundSessions"`
	LANLocationIsIdentity   bool                             `json:"lanLocationIsIdentity"`
}

type modernFederationDeviceEnrollment struct {
	Mode                      string `json:"mode"`
	AuthoritySiteRef          string `json:"authoritySiteRef"`
	EndpointExposure          string `json:"endpointExposure"`
	RemoteEnrollment          bool   `json:"remoteEnrollment"`
	RequireOwnerStepUp        bool   `json:"requireOwnerStepUp"`
	RequireLocalPairingProof  bool   `json:"requireLocalPairingProof"`
	RequireDeviceGeneratedKey bool   `json:"requireDeviceGeneratedKey"`
	RequirePossessionProof    bool   `json:"requirePossessionProof"`
	HardwareBackedKey         string `json:"hardwareBackedKey"`
	RevocationSupported       bool   `json:"revocationSupported"`
	CredentialTTLSeconds      int    `json:"credentialTTLSeconds"`
}

type modernFederationFailurePolicy struct {
	OnCloudLoss                     string `json:"onCloudLoss"`
	OnLinkLoss                      string `json:"onLinkLoss"`
	CloudEdge                       string `json:"cloudEdge"`
	LocalIdentityAuthorityAvailable bool   `json:"localIdentityAuthorityAvailable"`
	MaxStaleVerificationSeconds     int    `json:"maxStaleVerificationSeconds"`
	DenyNewCrossSiteSessions        bool   `json:"denyNewCrossSiteSessions"`
}

type modernFederationBridge struct {
	Overlay      modernFederationOverlay       `json:"overlay"`
	Publications []modernFederationPublication `json:"publications"`
	Policy       modernFederationPolicy        `json:"policy"`
	ControlAgent modernFederationControlAgent  `json:"controlAgent"`
}

type modernFederationOverlay struct {
	ContractRef             string   `json:"contractRef"`
	ProviderRef             string   `json:"providerRef"`
	ProviderContractHash    string   `json:"providerContractHash"`
	ModuleRef               string   `json:"moduleRef"`
	Implementation          string   `json:"implementation"`
	Initiation              string   `json:"initiation"`
	OutboundEstablished     bool     `json:"outboundEstablished"`
	TrafficMode             string   `json:"trafficMode"`
	AdvertisePrivateSubnets bool     `json:"advertisePrivateSubnets"`
	AdvertiseDefaultRoute   bool     `json:"advertiseDefaultRoute"`
	AllowBroadRoutes        bool     `json:"allowBroadRoutes"`
	PeerSiteRefs            []string `json:"peerSiteRefs"`
}

type modernFederationPolicy struct {
	DefaultDeny                    bool                   `json:"defaultDeny"`
	AllowedFlows                   []modernFederationFlow `json:"allowedFlows"`
	AllowRFC1918Transit            bool                   `json:"allowRFC1918Transit"`
	CloudMayEnrollDevices          bool                   `json:"cloudMayEnrollDevices"`
	CloudMayIssueDeviceCredentials bool                   `json:"cloudMayIssueDeviceCredentials"`
}

type modernFederationFlow struct {
	FromSiteRef             string   `json:"fromSiteRef"`
	ToSiteRef               string   `json:"toSiteRef"`
	ServiceRef              string   `json:"serviceRef"`
	Protocol                string   `json:"protocol"`
	Ports                   []int    `json:"ports"`
	Methods                 []string `json:"methods,omitempty"`
	DataClasses             []string `json:"dataClasses"`
	ServiceIdentityRequired bool     `json:"serviceIdentityRequired"`
}

type modernFederationControlAgent struct {
	Enabled              bool                           `json:"enabled"`
	ProviderRef          string                         `json:"providerRef"`
	ProviderContractHash string                         `json:"providerContractHash"`
	ModuleRef            string                         `json:"moduleRef"`
	ActionAllowlist      []string                       `json:"actionAllowlist"`
	Actions              []modernFederationRemoteAction `json:"actions"`
}

type modernFederationRemoteAction struct {
	ID                             string `json:"id"`
	ContractRef                    string `json:"contractRef"`
	CapabilityRef                  string `json:"capabilityRef"`
	ProviderRef                    string `json:"providerRef"`
	ProviderContractHash           string `json:"providerContractHash"`
	ModuleRef                      string `json:"moduleRef"`
	Transport                      string `json:"transport"`
	IssuerRef                      string `json:"issuerRef"`
	Audience                       string `json:"audience"`
	MaxTTLSeconds                  int    `json:"maxTTLSeconds"`
	Destructive                    bool   `json:"destructive"`
	ApprovalClass                  string `json:"approvalClass"`
	ApprovalReceiptRequired        bool   `json:"approvalReceiptRequired"`
	RequiresSignedActions          bool   `json:"requiresSignedActions"`
	RequiresNonce                  bool   `json:"requiresNonce"`
	RequiresResolvedPlanHash       bool   `json:"requiresResolvedPlanHash"`
	RequiresIdempotencyKey         bool   `json:"requiresIdempotencyKey"`
	CapabilityScopedActions        bool   `json:"capabilityScopedActions"`
	ReplayProtection               bool   `json:"replayProtection"`
	RequiresApprovalForDestructive bool   `json:"requiresApprovalForDestructive"`
}

type modernFederationPublicationAuth struct {
	Required  bool   `json:"required"`
	PolicyRef string `json:"policyRef"`
}

type modernFederationPublicationAccess struct {
	PolicyRef              string   `json:"policyRef"`
	Exposure               string   `json:"exposure"`
	PolicyExposure         string   `json:"policyExposure"`
	Authentication         string   `json:"authentication"`
	Privilege              string   `json:"privilege"`
	EnrolledDeviceRequired bool     `json:"enrolledDeviceRequired"`
	OwnerStepUpRequired    bool     `json:"ownerStepUpRequired"`
	LANStepDown            bool     `json:"lanStepDown"`
	AllowedMethods         []string `json:"allowedMethods"`
	DefaultClosed          bool     `json:"defaultClosed"`
}

type modernFederationPublication struct {
	ServiceRef         string                               `json:"serviceRef"`
	SourceSiteRef      string                               `json:"sourceSiteRef"`
	EdgeSiteRef        string                               `json:"edgeSiteRef"`
	Host               string                               `json:"host"`
	Protocol           string                               `json:"protocol"`
	Port               int                                  `json:"port"`
	Path               string                               `json:"path"`
	DefaultClosed      bool                                 `json:"defaultClosed"`
	TLS                modernFederationPublicationTLS       `json:"tls"`
	Auth               modernFederationPublicationAuth      `json:"auth"`
	Origin             modernFederationPublicationOrigin    `json:"origin"`
	RateLimit          modernFederationPublicationRateLimit `json:"rateLimit"`
	ModuleRef          string                               `json:"moduleRef"`
	UnitRef            string                               `json:"unitRef"`
	OriginNodeRefs     []string                             `json:"originNodeRefs"`
	OriginInstanceRefs []string                             `json:"originInstanceRefs"`
	UpstreamProtocol   string                               `json:"upstreamProtocol"`
	TargetPort         int                                  `json:"targetPort"`
	HealthGateRef      string                               `json:"healthGateRef"`
	DataBindingRef     string                               `json:"dataBindingRef"`
	Access             modernFederationPublicationAccess    `json:"access"`
}

type modernFederationPublicationTLS struct {
	Required   bool   `json:"required"`
	Mode       string `json:"mode"`
	MinVersion string `json:"minVersion"`
}

type modernFederationPublicationOrigin struct {
	IdentityRef  string `json:"identityRef"`
	MTLSRequired bool   `json:"mtlsRequired"`
}

type modernFederationPublicationRateLimit struct {
	Enabled       bool `json:"enabled"`
	Requests      int  `json:"requests"`
	WindowSeconds int  `json:"windowSeconds"`
}

type modernFederationData struct {
	DefaultAuthority string                                 `json:"defaultAuthority"`
	Bindings         map[string]modernFederationDataBinding `json:"bindings,omitempty"`
}

type modernFederationDataBinding struct {
	Classes          []string                         `json:"classes"`
	PrimarySiteRef   string                           `json:"primarySiteRef"`
	ReplicaSiteRefs  []string                         `json:"replicaSiteRefs,omitempty"`
	CloudCopyAllowed bool                             `json:"cloudCopyAllowed"`
	CloudCopyPolicy  *modernFederationCloudCopyPolicy `json:"cloudCopyPolicy,omitempty"`
}

type modernFederationCloudCopyPolicy struct {
	PolicyRef      string   `json:"policyRef"`
	AllowedClasses []string `json:"allowedClasses"`
	AllowPrimary   bool     `json:"allowPrimary"`
	AllowReplicas  bool     `json:"allowReplicas"`
}

func validateModernFederationPolicyUnit(unit RenderUnit, contract RendererContract) ([]byte, error) {
	return validateGenerationOnlyPolicyUnit(unit, generationOnlyPolicyUnitSpec{
		moduleID: modernFederationPolicyModuleID, unitID: modernFederationPolicyUnitID,
		outputRef: modernFederationPolicyOutputRef, policyName: "federation policy",
		contract: contract, planInputRefs: modernFederationPolicyPlanInputRefs,
		validatePlanInput: validateModernFederationPlanInputs,
	})
}

//nolint:gocyclo // The complete generation-only projection is validated atomically so no partially checked federation policy can be rendered.
func validateModernFederationPlanInputs(raw []byte, path string) ([]string, error) {
	var inputs modernFederationPlanInputs
	if err := decodeStrict(raw, &inputs); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact federation plan inputs", err)
	}
	if err := requireModernFederationID(inputs.StackID, path+".stackId"); err != nil {
		return nil, err
	}
	if inputs.Kit.Slug != "modern-homelab" || inputs.Kit.Version == "" || !validSHA256(inputs.Kit.DefinitionHash) {
		return nil, fail(ErrInvalidPlan, path+".kit", "policy manifest requires an exact Modern Homelab kit identity")
	}
	siteRefs, siteKinds, err := validateModernFederationSites(inputs.Sites, inputs.ControlPlane, path)
	if err != nil {
		return nil, err
	}
	data, err := validateModernFederationData(inputs.Data, siteKinds, path+".data")
	if err != nil {
		return nil, err
	}
	if err := validateModernFederationBridge(inputs.Bridge, siteKinds, data, path+".bridge"); err != nil {
		return nil, err
	}
	if inputs.Identity.HumanAuthoritySiteRef != inputs.ControlPlane.AuthoritySiteRef || inputs.Identity.DeviceAuthoritySiteRef != inputs.ControlPlane.AuthoritySiteRef ||
		!inputs.Identity.PossessionBoundSessions || inputs.Identity.LANLocationIsIdentity || inputs.Identity.DeviceEnrollment.Mode != "local-only" ||
		inputs.Identity.DeviceEnrollment.AuthoritySiteRef != inputs.ControlPlane.AuthoritySiteRef || inputs.Identity.DeviceEnrollment.EndpointExposure != "lan" ||
		inputs.Identity.DeviceEnrollment.RemoteEnrollment || !inputs.Identity.DeviceEnrollment.RequireOwnerStepUp || !inputs.Identity.DeviceEnrollment.RequireLocalPairingProof ||
		!inputs.Identity.DeviceEnrollment.RequireDeviceGeneratedKey || !inputs.Identity.DeviceEnrollment.RequirePossessionProof || !inputs.Identity.DeviceEnrollment.RevocationSupported {
		return nil, fail(ErrInvalidPlan, path+".identity", "Modern identity must remain home-authoritative, device-bound, locally enrolled, and possession-proven")
	}
	if len(inputs.Identity.EdgeVerifierSiteRefs) == 0 {
		return nil, fail(ErrInvalidPlan, path+".identity.edgeVerifierSiteRefs", "at least one cloud edge verifier is required")
	}
	if _, err := uniqueModernFederationIDSet(inputs.Identity.EdgeVerifierSiteRefs, path+".identity.edgeVerifierSiteRefs"); err != nil {
		return nil, err
	}
	if inputs.Identity.DeviceEnrollment.HardwareBackedKey != "preferred" && inputs.Identity.DeviceEnrollment.HardwareBackedKey != "required" ||
		inputs.Identity.DeviceEnrollment.CredentialTTLSeconds < 300 || inputs.Identity.DeviceEnrollment.CredentialTTLSeconds > 86400 {
		return nil, fail(ErrInvalidPlan, path+".identity.deviceEnrollment", "device credentials require bounded lifetime and preferred-or-required hardware-backed keys")
	}
	cloudSiteRefs := make([]string, 0, len(siteKinds))
	for siteRef, kind := range siteKinds {
		if kind == "cloud" {
			cloudSiteRefs = append(cloudSiteRefs, siteRef)
		}
	}
	edgeVerifierRefs := append([]string(nil), inputs.Identity.EdgeVerifierSiteRefs...)
	sort.Strings(cloudSiteRefs)
	sort.Strings(edgeVerifierRefs)
	if !exactStringList(edgeVerifierRefs, cloudSiteRefs) {
		return nil, fail(ErrInvalidPlan, path+".identity.edgeVerifierSiteRefs", "edge verifiers must exactly cover every projected cloud site and exclude the home authority")
	}
	if inputs.FailurePolicy.OnCloudLoss != "local-continues" || inputs.FailurePolicy.OnLinkLoss != "local-continues" || inputs.FailurePolicy.CloudEdge != "fail-closed" ||
		!inputs.FailurePolicy.LocalIdentityAuthorityAvailable || !inputs.FailurePolicy.DenyNewCrossSiteSessions || inputs.FailurePolicy.MaxStaleVerificationSeconds < 0 {
		return nil, fail(ErrInvalidPlan, path+".failurePolicy", "partition policy must preserve local authority and fail the cloud edge closed")
	}
	if err := rejectModernFederationProjectionLeaks(raw, path); err != nil {
		return nil, err
	}
	return siteRefs, nil
}

//nolint:gocyclo // Site and control-plane membership form one fail-closed Modern Home Lab authority boundary.
func validateModernFederationSites(sites []modernFederationSite, control modernFederationControlPlane, path string) ([]string, map[string]string, error) {
	if len(sites) < 2 || control.AuthoritySiteRef == "" || len(control.Members) == 0 {
		return nil, nil, fail(ErrInvalidPlan, path+".sites", "Modern federation requires home and cloud sites plus a control authority")
	}
	seen := make(map[string]struct{}, len(sites))
	siteKinds := make(map[string]string, len(sites))
	siteRefs := make([]string, 0, len(sites))
	home, cloud, authorityHome := false, false, false
	for index, site := range sites {
		if err := requireModernFederationID(site.ID, fmt.Sprintf("%s.sites[%d].id", path, index)); err != nil {
			return nil, nil, err
		}
		if site.FailureDomain == "" || (site.Kind != "home" && site.Kind != "cloud") {
			return nil, nil, fail(ErrInvalidPlan, fmt.Sprintf("%s.sites[%d]", path, index), "site projection is not an exact id/kind/failureDomain view")
		}
		if _, duplicate := seen[site.ID]; duplicate {
			return nil, nil, fail(ErrDuplicate, fmt.Sprintf("%s.sites[%d].id", path, index), "duplicate site %q", site.ID)
		}
		seen[site.ID] = struct{}{}
		siteKinds[site.ID] = site.Kind
		siteRefs = append(siteRefs, site.ID)
		home = home || site.Kind == "home"
		cloud = cloud || site.Kind == "cloud"
		authorityHome = authorityHome || site.ID == control.AuthoritySiteRef && site.Kind == "home"
	}
	if !home || !cloud || !authorityHome {
		return nil, nil, fail(ErrInvalidPlan, path+".sites", "Modern federation requires both site kinds and a home control authority")
	}
	if control.Mode != "single" && control.Mode != "warm-standby" && control.Mode != "quorum" {
		return nil, nil, fail(ErrInvalidPlan, path+".controlPlane.mode", "must be a supported control-plane mode")
	}
	if _, err := uniqueModernFederationIDSet(control.Members, path+".controlPlane.members"); err != nil {
		return nil, nil, err
	}
	if control.Mode == "single" && len(control.Members) != 1 || control.Mode == "warm-standby" && len(control.Members) < 2 || control.Mode == "quorum" && len(control.Members) != 3 && len(control.Members) != 5 && len(control.Members) != 7 {
		return nil, nil, fail(ErrInvalidPlan, path+".controlPlane.members", "member cardinality does not match the control-plane mode")
	}
	sort.Strings(siteRefs)
	return siteRefs, siteKinds, nil
}

//nolint:gocyclo // Bridge validation keeps overlay, publication, flow, identity, and data constraints in one auditable acceptance boundary.
func validateModernFederationBridge(raw []byte, siteKinds map[string]string, data modernFederationData, path string) error {
	var bridge modernFederationBridge
	if err := decodeStrict(raw, &bridge); err != nil {
		return wrap(ErrInvalidPlan, path, "decode exact resolved bridge policy", err)
	}
	if requireModernFederationID(bridge.Overlay.ContractRef, path+".overlay.contractRef") != nil ||
		requireModernFederationID(bridge.Overlay.ProviderRef, path+".overlay.providerRef") != nil ||
		requireModernFederationID(bridge.Overlay.ModuleRef, path+".overlay.moduleRef") != nil || !validSHA256(bridge.Overlay.ProviderContractHash) ||
		!stringListContains([]string{"wireguard", "headscale", "tailscale", "netbird", "pangolin"}, bridge.Overlay.Implementation) ||
		bridge.Overlay.Initiation != "local-outbound" || !bridge.Overlay.OutboundEstablished ||
		!stringListContains([]string{"management-only", "policy-scoped"}, bridge.Overlay.TrafficMode) ||
		bridge.Overlay.AdvertisePrivateSubnets || bridge.Overlay.AdvertiseDefaultRoute || bridge.Overlay.AllowBroadRoutes || len(bridge.Overlay.PeerSiteRefs) < 2 {
		return fail(ErrInvalidPlan, path+".overlay", "overlay must stay local-outbound and may not advertise private/default/broad routes")
	}
	peerSet, err := uniqueModernFederationIDSet(bridge.Overlay.PeerSiteRefs, path+".overlay.peerSiteRefs")
	if err != nil {
		return err
	}
	for peerRef := range peerSet {
		if _, exists := siteKinds[peerRef]; !exists {
			return fail(ErrInvalidPlan, path+".overlay.peerSiteRefs", "peer %q is not present in the projected sites", peerRef)
		}
	}
	if !bridge.Policy.DefaultDeny || bridge.Policy.AllowRFC1918Transit || bridge.Policy.CloudMayEnrollDevices || bridge.Policy.CloudMayIssueDeviceCredentials {
		return fail(ErrInvalidPlan, path+".policy", "bridge policy must remain default-deny, non-transitive, and local-authority-only")
	}
	if bridge.Overlay.TrafficMode == "management-only" && (len(bridge.Publications) != 0 || len(bridge.Policy.AllowedFlows) != 0) {
		return fail(ErrInvalidPlan, path+".overlay.trafficMode", "management-only overlays cannot carry service publications or policy flows")
	}
	if !bridge.ControlAgent.Enabled || requireModernFederationID(bridge.ControlAgent.ProviderRef, path+".controlAgent.providerRef") != nil ||
		requireModernFederationID(bridge.ControlAgent.ModuleRef, path+".controlAgent.moduleRef") != nil || !validSHA256(bridge.ControlAgent.ProviderContractHash) {
		return fail(ErrInvalidPlan, path+".controlAgent", "outbound control authority must be bound to one catalog provider and module")
	}
	actionSet, err := uniqueModernFederationIDSet(bridge.ControlAgent.ActionAllowlist, path+".controlAgent.actionAllowlist")
	if err != nil || len(bridge.ControlAgent.ActionAllowlist) == 0 {
		if err != nil {
			return err
		}
		return fail(ErrInvalidPlan, path+".controlAgent.actionAllowlist", "at least one scoped action is required")
	}
	if len(bridge.ControlAgent.Actions) != len(actionSet) {
		return fail(ErrInvalidPlan, path+".controlAgent.actions", "resolved actions must exactly close the action allowlist")
	}
	seenActions := make(map[string]struct{}, len(bridge.ControlAgent.Actions))
	for index, action := range bridge.ControlAgent.Actions {
		actionPath := fmt.Sprintf("%s.controlAgent.actions[%d]", path, index)
		if _, allowed := actionSet[action.ID]; !allowed || action.ContractRef != action.ID {
			return fail(ErrInvalidPlan, actionPath, "resolved action is not an exact allowlist contract")
		}
		if _, duplicate := seenActions[action.ID]; duplicate {
			return fail(ErrInvalidPlan, actionPath+".id", "duplicate remote action %q", action.ID)
		}
		seenActions[action.ID] = struct{}{}
		if action.CapabilityRef != "outbound-control-agent" || action.ProviderRef != bridge.ControlAgent.ProviderRef ||
			action.ProviderContractHash != bridge.ControlAgent.ProviderContractHash || action.ModuleRef != bridge.ControlAgent.ModuleRef ||
			!stringListContains([]string{"managed-agent", "mtls-agent"}, action.Transport) || action.MaxTTLSeconds < 1 || action.MaxTTLSeconds > 300 ||
			!action.RequiresSignedActions || !action.RequiresNonce || !action.RequiresResolvedPlanHash || !action.RequiresIdempotencyKey ||
			!action.CapabilityScopedActions || !action.ReplayProtection || !action.RequiresApprovalForDestructive ||
			((action.Destructive || action.ApprovalClass != "none") && !action.ApprovalReceiptRequired) ||
			(action.Destructive && action.ApprovalClass == "none") ||
			!stringListContains([]string{"none", "owner-step-up", "break-glass"}, action.ApprovalClass) {
			return fail(ErrInvalidPlan, actionPath, "remote action must retain exact provider ownership, bounded TTL, replay protection, and destructive approval")
		}
		if err := requireModernFederationID(action.Audience, actionPath+".audience"); err != nil {
			return err
		}
		if err := requireModernFederationID(action.IssuerRef, actionPath+".issuerRef"); err != nil {
			return err
		}
	}
	for index, publication := range bridge.Publications {
		if err := validateModernFederationPublication(publication, peerSet, siteKinds, data, fmt.Sprintf("%s.publications[%d]", path, index)); err != nil {
			return err
		}
	}
	for index, flow := range bridge.Policy.AllowedFlows {
		if err := validateModernFederationFlow(flow, peerSet, data, fmt.Sprintf("%s.policy.allowedFlows[%d]", path, index)); err != nil {
			return err
		}
	}
	for publicationIndex, publication := range bridge.Publications {
		matches := 0
		for _, flow := range bridge.Policy.AllowedFlows {
			if modernFederationFlowMatchesPublication(flow, publication) {
				matches++
			}
		}
		if matches == 0 {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s.publications[%d]", path, publicationIndex), "publication has no exact edge-to-origin service flow")
		}
	}
	return nil
}

//nolint:gocyclo // Publications are accepted only after the entire closed edge-to-origin tuple is checked together.
func validateModernFederationPublication(publication modernFederationPublication, peers map[string]struct{}, siteKinds map[string]string, data modernFederationData, path string) error {
	for field, value := range map[string]string{
		"serviceRef": publication.ServiceRef, "sourceSiteRef": publication.SourceSiteRef, "edgeSiteRef": publication.EdgeSiteRef,
		"moduleRef": publication.ModuleRef, "unitRef": publication.UnitRef, "healthGateRef": publication.HealthGateRef,
		"dataBindingRef": publication.DataBindingRef, "auth.policyRef": publication.Auth.PolicyRef, "origin.identityRef": publication.Origin.IdentityRef,
	} {
		if err := requireModernFederationID(value, path+"."+field); err != nil {
			return err
		}
	}
	if publication.SourceSiteRef == publication.EdgeSiteRef || !setContains(peers, publication.SourceSiteRef) || !setContains(peers, publication.EdgeSiteRef) {
		return fail(ErrInvalidPlan, path, "publication source and edge must be distinct projected bridge peers")
	}
	if siteKinds[publication.SourceSiteRef] != "home" || siteKinds[publication.EdgeSiteRef] != "cloud" {
		return fail(ErrInvalidPlan, path, "Modern publications require a home source and a cloud edge verifier site")
	}
	if publication.Host == "" || !strings.HasPrefix(publication.Path, "/") || publication.Protocol != "https" || publication.Port < 1 || publication.Port > 65535 || !publication.DefaultClosed {
		return fail(ErrInvalidPlan, path, "public publication requires an HTTPS host/path/port and must fail closed")
	}
	if !publication.TLS.Required || publication.TLS.Mode != "terminate-at-edge" || publication.TLS.MinVersion != "TLS1.2" && publication.TLS.MinVersion != "TLS1.3" {
		return fail(ErrInvalidPlan, path+".tls", "public edge must terminate TLS at version 1.2 or newer")
	}
	if !publication.Auth.Required || publication.Auth.PolicyRef == "" {
		return fail(ErrInvalidPlan, path+".auth", "public publication requires an explicit access policy")
	}
	if publication.Origin.IdentityRef == "" || !publication.Origin.MTLSRequired {
		return fail(ErrInvalidPlan, path+".origin", "publication origin requires a bound mTLS workload identity")
	}
	if !publication.RateLimit.Enabled || publication.RateLimit.Requests < 1 || publication.RateLimit.WindowSeconds < 1 {
		return fail(ErrInvalidPlan, path+".rateLimit", "public edge requires an enabled positive rate limit")
	}
	if _, err := uniqueModernFederationIDSet(publication.OriginNodeRefs, path+".originNodeRefs"); err != nil || len(publication.OriginNodeRefs) == 0 {
		if err != nil {
			return err
		}
		return fail(ErrInvalidPlan, path+".originNodeRefs", "at least one governed origin node is required")
	}
	if _, err := uniqueModernFederationIDSet(publication.OriginInstanceRefs, path+".originInstanceRefs"); err != nil || len(publication.OriginInstanceRefs) == 0 {
		if err != nil {
			return err
		}
		return fail(ErrInvalidPlan, path+".originInstanceRefs", "at least one governed origin instance is required")
	}
	if !stringListContains([]string{"tcp", "udp", "http", "https"}, publication.UpstreamProtocol) || publication.TargetPort < 1 || publication.TargetPort > 65535 {
		return fail(ErrInvalidPlan, path+".upstreamProtocol", "publication requires an exact supported origin protocol and port")
	}
	access := publication.Access
	if access.PolicyRef != publication.Auth.PolicyRef || access.Exposure != "public" || access.PolicyExposure != "public" || access.LANStepDown || access.Authentication != "human+device" ||
		!stringListContains([]string{"user", "admin", "identity", "secrets"}, access.Privilege) || !access.EnrolledDeviceRequired || !access.DefaultClosed || len(access.AllowedMethods) == 0 {
		return fail(ErrInvalidPlan, path+".access", "public bridge publication requires human+device, enrolled-device, explicit-method, default-closed access")
	}
	if access.Privilege != "user" && !access.OwnerStepUpRequired {
		return fail(ErrInvalidPlan, path+".access.ownerStepUpRequired", "privileged public routes require owner step-up")
	}
	if err := validateModernFederationMethods(access.AllowedMethods, path+".access.allowedMethods", true); err != nil {
		return err
	}
	binding, exists := data.Bindings[publication.DataBindingRef]
	if !exists || binding.PrimarySiteRef != publication.SourceSiteRef || binding.CloudCopyAllowed {
		return fail(ErrInvalidPlan, path+".dataBindingRef", "published data must remain primary at the source site with cloud copy disabled")
	}
	return nil
}

//nolint:gocyclo // A policy flow is one indivisible authorization tuple whose identity, direction, protocol, port, and data scope must all match.
func validateModernFederationFlow(flow modernFederationFlow, peers map[string]struct{}, data modernFederationData, path string) error {
	for field, value := range map[string]string{"fromSiteRef": flow.FromSiteRef, "toSiteRef": flow.ToSiteRef, "serviceRef": flow.ServiceRef} {
		if err := requireModernFederationID(value, path+"."+field); err != nil {
			return err
		}
	}
	if flow.FromSiteRef == flow.ToSiteRef || !setContains(peers, flow.FromSiteRef) || !setContains(peers, flow.ToSiteRef) || !flow.ServiceIdentityRequired {
		return fail(ErrInvalidPlan, path, "cross-site flows require distinct bridge peers and workload identity")
	}
	if !stringListContains([]string{"tcp", "udp", "http", "https"}, flow.Protocol) {
		return fail(ErrInvalidPlan, path+".protocol", "unsupported cross-site protocol")
	}
	seenPorts := make(map[int]struct{}, len(flow.Ports))
	for index, port := range flow.Ports {
		if port < 1 || port > 65535 {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s.ports[%d]", path, index), "port is outside the supported range")
		}
		if _, duplicate := seenPorts[port]; duplicate {
			return fail(ErrDuplicate, fmt.Sprintf("%s.ports[%d]", path, index), "duplicate port %d", port)
		}
		seenPorts[port] = struct{}{}
	}
	if len(seenPorts) == 0 {
		return fail(ErrInvalidPlan, path+".ports", "at least one exact transport port is required")
	}
	if err := validateModernFederationMethods(flow.Methods, path+".methods", flow.Protocol == "http" || flow.Protocol == "https"); err != nil {
		return err
	}
	if flow.Protocol == "tcp" || flow.Protocol == "udp" {
		if len(flow.Methods) != 0 {
			return fail(ErrInvalidPlan, path+".methods", "TCP and UDP flows cannot carry HTTP methods")
		}
	}
	binding, exists := data.Bindings[flow.ServiceRef]
	if !exists {
		return fail(ErrInvalidPlan, path+".serviceRef", "flow service has no governed data binding")
	}
	if err := validateModernFederationDataClasses(flow.DataClasses, path+".dataClasses"); err != nil {
		return err
	}
	for _, dataClass := range flow.DataClasses {
		if !stringListContains(binding.Classes, dataClass) {
			return fail(ErrInvalidPlan, path+".dataClasses", "flow data class %q is not governed by its service binding", dataClass)
		}
	}
	return nil
}

func modernFederationFlowMatchesPublication(flow modernFederationFlow, publication modernFederationPublication) bool {
	if flow.FromSiteRef != publication.EdgeSiteRef || flow.ToSiteRef != publication.SourceSiteRef || flow.ServiceRef != publication.ServiceRef || flow.Protocol != publication.UpstreamProtocol || len(flow.Ports) != 1 || flow.Ports[0] != publication.TargetPort {
		return false
	}
	if flow.Protocol == "tcp" || flow.Protocol == "udp" {
		return true
	}
	left := append([]string(nil), flow.Methods...)
	right := append([]string(nil), publication.Access.AllowedMethods...)
	sort.Strings(left)
	sort.Strings(right)
	return exactStringList(left, right)
}

//nolint:gocyclo // Data placement, replicas, synchronization, and conflict policy are one fail-closed locality contract.
func validateModernFederationData(raw []byte, siteKinds map[string]string, path string) (modernFederationData, error) {
	var data modernFederationData
	if err := decodeStrict(raw, &data); err != nil {
		return modernFederationData{}, wrap(ErrInvalidPlan, path, "decode exact data authority projection", err)
	}
	if data.DefaultAuthority != "home" {
		return modernFederationData{}, fail(ErrInvalidPlan, path+".defaultAuthority", "Modern federation keeps home as the default data authority")
	}
	for bindingRef, binding := range data.Bindings {
		bindingPath := path + ".bindings." + bindingRef
		if err := requireModernFederationID(bindingRef, bindingPath); err != nil {
			return modernFederationData{}, err
		}
		if _, exists := siteKinds[binding.PrimarySiteRef]; !exists {
			return modernFederationData{}, fail(ErrInvalidPlan, bindingPath+".primarySiteRef", "primary site is not projected")
		}
		if err := validateModernFederationDataClasses(binding.Classes, bindingPath+".classes"); err != nil {
			return modernFederationData{}, err
		}
		replicaSet, err := uniqueModernFederationIDSet(binding.ReplicaSiteRefs, bindingPath+".replicaSiteRefs")
		if err != nil {
			return modernFederationData{}, err
		}
		for replicaRef := range replicaSet {
			if _, exists := siteKinds[replicaRef]; !exists || replicaRef == binding.PrimarySiteRef {
				return modernFederationData{}, fail(ErrInvalidPlan, bindingPath+".replicaSiteRefs", "replicas must be distinct projected sites")
			}
		}
		if !binding.CloudCopyAllowed {
			if binding.CloudCopyPolicy != nil || siteKinds[binding.PrimarySiteRef] == "cloud" || anyCloudSite(replicaSet, siteKinds) {
				return modernFederationData{}, fail(ErrInvalidPlan, bindingPath, "cloud placement requires an explicit cloud-copy policy")
			}
			continue
		}
		policy := binding.CloudCopyPolicy
		if policy == nil {
			return modernFederationData{}, fail(ErrInvalidPlan, bindingPath+".cloudCopyPolicy", "cloud-copy opt-in requires an explicit policy")
		}
		if err := requireModernFederationID(policy.PolicyRef, bindingPath+".cloudCopyPolicy.policyRef"); err != nil {
			return modernFederationData{}, err
		}
		if err := validateModernFederationDataClasses(policy.AllowedClasses, bindingPath+".cloudCopyPolicy.allowedClasses"); err != nil {
			return modernFederationData{}, err
		}
		for _, dataClass := range binding.Classes {
			if !stringListContains(policy.AllowedClasses, dataClass) {
				return modernFederationData{}, fail(ErrInvalidPlan, bindingPath+".cloudCopyPolicy.allowedClasses", "policy does not cover bound data class %q", dataClass)
			}
		}
		if siteKinds[binding.PrimarySiteRef] == "cloud" && !policy.AllowPrimary || anyCloudSite(replicaSet, siteKinds) && !policy.AllowReplicas {
			return modernFederationData{}, fail(ErrInvalidPlan, bindingPath+".cloudCopyPolicy", "policy does not authorize the requested cloud placement")
		}
	}
	return data, nil
}

func validateModernFederationMethods(methods []string, path string, required bool) error {
	if required && len(methods) == 0 {
		return fail(ErrInvalidPlan, path, "HTTP flows require at least one explicit method")
	}
	seen := make(map[string]struct{}, len(methods))
	for index, method := range methods {
		if !stringListContains([]string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}, method) {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s[%d]", path, index), "unsupported HTTP method %q", method)
		}
		if _, duplicate := seen[method]; duplicate {
			return fail(ErrDuplicate, fmt.Sprintf("%s[%d]", path, index), "duplicate HTTP method %q", method)
		}
		seen[method] = struct{}{}
	}
	return nil
}

func validateModernFederationDataClasses(classes []string, path string) error {
	if len(classes) == 0 {
		return fail(ErrInvalidPlan, path, "at least one data class is required")
	}
	seen := make(map[string]struct{}, len(classes))
	for index, dataClass := range classes {
		if !stringListContains([]string{"public", "internal", "personal", "sensitive", "secret"}, dataClass) {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s[%d]", path, index), "unsupported data class %q", dataClass)
		}
		if _, duplicate := seen[dataClass]; duplicate {
			return fail(ErrDuplicate, fmt.Sprintf("%s[%d]", path, index), "duplicate data class %q", dataClass)
		}
		seen[dataClass] = struct{}{}
	}
	return nil
}

func rejectModernFederationProjectionLeaks(raw []byte, path string) error {
	return rejectGenerationOnlyPolicyProjectionLeaks(raw, path, "federation policy")
}

func anyCloudSite(refs map[string]struct{}, siteKinds map[string]string) bool {
	for ref := range refs {
		if siteKinds[ref] == "cloud" {
			return true
		}
	}
	return false
}

func setContains(values map[string]struct{}, wanted string) bool {
	_, exists := values[wanted]
	return exists
}

func requireModernFederationID(value, path string) error {
	if !modernFederationArchitectureV2IDPattern.MatchString(value) {
		return fail(ErrInvalidPlan, path, "must match the closed Architecture v2 ID grammar ^[a-z][a-z0-9-]*$")
	}
	return nil
}

func uniqueModernFederationIDSet(values []string, path string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(values))
	for index, value := range values {
		if err := requireModernFederationID(value, fmt.Sprintf("%s[%d]", path, index)); err != nil {
			return nil, err
		}
		if _, duplicate := result[value]; duplicate {
			return nil, fail(ErrDuplicate, fmt.Sprintf("%s[%d]", path, index), "duplicate reference %q", value)
		}
		result[value] = struct{}{}
	}
	return result, nil
}
