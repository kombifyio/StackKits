package architecturev2renderer

import (
	"encoding/json"
	"fmt"
)

type modernFederationPolicyProjection struct {
	Overlay       modernFederationOverlayPolicy `json:"overlay"`
	Publications  []modernFederationPublication `json:"publications"`
	Policy        modernFederationPolicy        `json:"policy"`
	DataAuthority modernFederationData          `json:"dataAuthority"`
	Partition     modernFederationPartition     `json:"partition"`
}

type modernFederationOverlayPolicy struct {
	ContractRef             string   `json:"contractRef"`
	Implementation          string   `json:"implementation"`
	Initiation              string   `json:"initiation"`
	OutboundEstablished     bool     `json:"outboundEstablished"`
	TrafficMode             string   `json:"trafficMode"`
	AdvertisePrivateSubnets bool     `json:"advertisePrivateSubnets"`
	AdvertiseDefaultRoute   bool     `json:"advertiseDefaultRoute"`
	AllowBroadRoutes        bool     `json:"allowBroadRoutes"`
	PeerSiteRefs            []string `json:"peerSiteRefs"`
}

type modernFederationPartition struct {
	OnCloudLoss                     string `json:"onCloudLoss"`
	OnLinkLoss                      string `json:"onLinkLoss"`
	CloudEdge                       string `json:"cloudEdge"`
	LocalIdentityAuthorityAvailable bool   `json:"localIdentityAuthorityAvailable"`
	MaxStaleVerificationSeconds     int    `json:"maxStaleVerificationSeconds"`
	DenyNewCrossSiteSessions        bool   `json:"denyNewCrossSiteSessions"`
}

type federationLinkPolicyProjection struct {
	Overlay   modernFederationOverlayPolicy `json:"overlay"`
	Partition modernFederationPartition     `json:"partition"`
}

type federationControlActionsProjection struct {
	Enabled         bool                      `json:"enabled"`
	ModuleRef       string                    `json:"moduleRef"`
	ContractHash    string                    `json:"contractHash"`
	ActionAllowlist []string                  `json:"actionAllowlist"`
	Actions         []federationControlAction `json:"actions"`
	Partition       modernFederationPartition `json:"partition"`
}

type federationControlAction struct {
	ID                             string `json:"id"`
	ContractRef                    string `json:"contractRef"`
	CapabilityRef                  string `json:"capabilityRef"`
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

type federationBackupPolicyProjection struct {
	DataAuthority modernFederationData      `json:"dataAuthority"`
	Partition     modernFederationPartition `json:"partition"`
}

type federationObservabilityProjection struct {
	EvidenceOnly bool                               `json:"evidenceOnly"`
	Publications []federationPublicationObservation `json:"publications"`
	Flows        []modernFederationFlow             `json:"flows"`
	Partition    modernFederationPartition          `json:"partition"`
}

type federationPublicationObservation struct {
	ServiceRef    string `json:"serviceRef"`
	SourceSiteRef string `json:"sourceSiteRef"`
	EdgeSiteRef   string `json:"edgeSiteRef"`
	HealthGateRef string `json:"healthGateRef"`
	DefaultClosed bool   `json:"defaultClosed"`
}

func validateFederationPolicyProjection(projection modernFederationPolicyProjection, siteKinds map[string]string, path string) error {
	peers, err := validateFederationOverlayPolicy(projection.Overlay, siteKinds, path+".overlay")
	if err != nil {
		return err
	}
	if err := validateFederationPartition(projection.Partition, path+".partition"); err != nil {
		return err
	}
	data, err := validateTypedFederationData(projection.DataAuthority, siteKinds, path+".dataAuthority")
	if err != nil {
		return err
	}
	if !projection.Policy.DefaultDeny || projection.Policy.AllowRFC1918Transit ||
		projection.Policy.CloudMayEnrollDevices || projection.Policy.CloudMayIssueDeviceCredentials {
		return fail(ErrInvalidPlan, path+".policy", "Federation policy must remain default-deny, non-transitive, and Home-authoritative")
	}
	if projection.Overlay.TrafficMode == "management-only" &&
		(len(projection.Publications) != 0 || len(projection.Policy.AllowedFlows) != 0) {
		return fail(ErrInvalidPlan, path+".overlay.trafficMode", "management-only overlays cannot carry publications or service flows")
	}
	for index, publication := range projection.Publications {
		if err := validateModernFederationPublication(publication, peers, siteKinds, data, fmt.Sprintf("%s.publications[%d]", path, index)); err != nil {
			return err
		}
	}
	for index, flow := range projection.Policy.AllowedFlows {
		if err := validateModernFederationFlow(flow, peers, data, fmt.Sprintf("%s.policy.allowedFlows[%d]", path, index)); err != nil {
			return err
		}
	}
	for index, publication := range projection.Publications {
		matches := 0
		for _, flow := range projection.Policy.AllowedFlows {
			if modernFederationFlowMatchesPublication(flow, publication) {
				matches++
			}
		}
		if matches == 0 {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s.publications[%d]", path, index), "publication has no exact edge-to-origin service flow")
		}
	}
	return nil
}

func validateTypedFederationProjection(
	moduleID string,
	raw json.RawMessage,
	sites []executorBundleSite,
	control executorBundleControlPlane,
	path string,
) error {
	modernSites := make([]modernFederationSite, 0, len(sites))
	for _, site := range sites {
		modernSites = append(modernSites, modernFederationSite{ID: site.ID, Kind: site.Kind, FailureDomain: site.FailureDomain})
	}
	_, siteKinds, err := validateModernFederationSites(modernSites, modernFederationControlPlane{
		Mode: control.Mode, AuthoritySiteRef: control.AuthoritySiteRef, Members: append([]string(nil), control.Members...),
	}, path)
	if err != nil {
		return err
	}
	switch moduleID {
	case federationLinkModuleID:
		var projection federationLinkPolicyProjection
		if err := decodeStrict(raw, &projection); err != nil {
			return wrap(ErrInvalidPlan, path+".federationLinkPolicy", "decode exact link policy", err)
		}
		if _, err := validateFederationOverlayPolicy(projection.Overlay, siteKinds, path+".federationLinkPolicy.overlay"); err != nil {
			return err
		}
		if err := validateFederationPartition(projection.Partition, path+".federationLinkPolicy.partition"); err != nil {
			return err
		}
	case federationControlAgentModuleID:
		var projection federationControlActionsProjection
		if err := decodeStrict(raw, &projection); err != nil {
			return wrap(ErrInvalidPlan, path+".federationControlActions", "decode exact control-action projection", err)
		}
		if err := validateFederationControlActions(projection, path+".federationControlActions"); err != nil {
			return err
		}
	case federationBackupModuleID:
		var projection federationBackupPolicyProjection
		if err := decodeStrict(raw, &projection); err != nil {
			return wrap(ErrInvalidPlan, path+".federationBackupPolicy", "decode exact backup policy", err)
		}
		if _, err := validateTypedFederationData(projection.DataAuthority, siteKinds, path+".federationBackupPolicy.dataAuthority"); err != nil {
			return err
		}
		if err := validateFederationPartition(projection.Partition, path+".federationBackupPolicy.partition"); err != nil {
			return err
		}
	case federationObservabilityModuleID:
		var projection federationObservabilityProjection
		if err := decodeStrict(raw, &projection); err != nil {
			return wrap(ErrInvalidPlan, path+".federationObservability", "decode exact observability projection", err)
		}
		if err := validateFederationObservability(projection, siteKinds, path+".federationObservability"); err != nil {
			return err
		}
	default:
		return fail(ErrInvalidPlan, path, "unknown Federation runtime module %q", moduleID)
	}
	if err := rejectModernFederationProjectionLeaks(raw, path); err != nil {
		return err
	}
	return nil
}

func validateFederationOverlayPolicy(
	overlay modernFederationOverlayPolicy,
	siteKinds map[string]string,
	path string,
) (map[string]struct{}, error) {
	if err := requireModernFederationID(overlay.ContractRef, path+".contractRef"); err != nil {
		return nil, err
	}
	if !stringListContains([]string{"wireguard", "headscale", "tailscale", "netbird", "pangolin"}, overlay.Implementation) ||
		overlay.Initiation != "local-outbound" || !overlay.OutboundEstablished ||
		!stringListContains([]string{"management-only", "policy-scoped"}, overlay.TrafficMode) ||
		overlay.AdvertisePrivateSubnets || overlay.AdvertiseDefaultRoute || overlay.AllowBroadRoutes ||
		len(overlay.PeerSiteRefs) < 2 {
		return nil, fail(ErrInvalidPlan, path, "link policy must be outbound-only, exact-flow scoped, and unable to advertise LAN/default/broad routes")
	}
	peers, err := uniqueModernFederationIDSet(overlay.PeerSiteRefs, path+".peerSiteRefs")
	if err != nil {
		return nil, err
	}
	home, cloud := false, false
	for peerRef := range peers {
		kind, exists := siteKinds[peerRef]
		if !exists {
			return nil, fail(ErrInvalidPlan, path+".peerSiteRefs", "peer %q is not a projected Site", peerRef)
		}
		home = home || kind == "home"
		cloud = cloud || kind == "cloud"
	}
	if !home || !cloud {
		return nil, fail(ErrInvalidPlan, path+".peerSiteRefs", "link policy requires explicit Home and Cloud peers")
	}
	return peers, nil
}

func validateFederationPartition(partition modernFederationPartition, path string) error {
	if partition.OnCloudLoss != "local-continues" || partition.OnLinkLoss != "local-continues" ||
		partition.CloudEdge != "fail-closed" || !partition.LocalIdentityAuthorityAvailable ||
		!partition.DenyNewCrossSiteSessions || partition.MaxStaleVerificationSeconds < 0 {
		return fail(ErrInvalidPlan, path, "partition policy must preserve local authority and fail new Cloud/cross-Site access closed")
	}
	return nil
}

func validateTypedFederationData(
	data modernFederationData,
	siteKinds map[string]string,
	path string,
) (modernFederationData, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return modernFederationData{}, wrap(ErrRendererFailure, path, "marshal typed data authority", err)
	}
	return validateModernFederationData(raw, siteKinds, path)
}

func validateFederationControlActions(projection federationControlActionsProjection, path string) error {
	if !projection.Enabled || projection.ModuleRef != federationControlAgentModuleID || !validSHA256(projection.ContractHash) {
		return fail(ErrInvalidPlan, path, "control authority must be enabled and bound to the exact module contract")
	}
	allowlist, err := uniqueModernFederationIDSet(projection.ActionAllowlist, path+".actionAllowlist")
	if err != nil || len(allowlist) == 0 {
		if err != nil {
			return err
		}
		return fail(ErrInvalidPlan, path+".actionAllowlist", "at least one exact control action is required")
	}
	if len(projection.Actions) != len(allowlist) {
		return fail(ErrInvalidPlan, path+".actions", "actions must exactly close the allowlist")
	}
	seen := make(map[string]struct{}, len(projection.Actions))
	for index, action := range projection.Actions {
		actionPath := fmt.Sprintf("%s.actions[%d]", path, index)
		if _, allowed := allowlist[action.ID]; !allowed || action.ContractRef != action.ID ||
			action.CapabilityRef != "outbound-control-agent" || action.ModuleRef != projection.ModuleRef ||
			!stringListContains([]string{"managed-agent", "mtls-agent"}, action.Transport) ||
			action.MaxTTLSeconds < 1 || action.MaxTTLSeconds > 300 ||
			!action.RequiresSignedActions || !action.RequiresNonce || !action.RequiresResolvedPlanHash ||
			!action.RequiresIdempotencyKey || !action.CapabilityScopedActions || !action.ReplayProtection ||
			!action.RequiresApprovalForDestructive ||
			((action.Destructive || action.ApprovalClass != "none") && !action.ApprovalReceiptRequired) ||
			(action.Destructive && action.ApprovalClass == "none") ||
			!stringListContains([]string{"none", "owner-step-up", "break-glass"}, action.ApprovalClass) {
			return fail(ErrInvalidPlan, actionPath, "control action widens scope, freshness, replay, or approval policy")
		}
		if _, duplicate := seen[action.ID]; duplicate {
			return fail(ErrDuplicate, actionPath+".id", "duplicate action %q", action.ID)
		}
		seen[action.ID] = struct{}{}
		if err := requireModernFederationID(action.IssuerRef, actionPath+".issuerRef"); err != nil {
			return err
		}
		if err := requireModernFederationID(action.Audience, actionPath+".audience"); err != nil {
			return err
		}
	}
	return validateFederationPartition(projection.Partition, path+".partition")
}

func validateFederationObservability(
	projection federationObservabilityProjection,
	siteKinds map[string]string,
	path string,
) error {
	if !projection.EvidenceOnly {
		return fail(ErrInvalidPlan, path+".evidenceOnly", "observability projection cannot carry mutation authority")
	}
	if err := validateFederationPartition(projection.Partition, path+".partition"); err != nil {
		return err
	}
	for index, publication := range projection.Publications {
		itemPath := fmt.Sprintf("%s.publications[%d]", path, index)
		for field, value := range map[string]string{
			"serviceRef": publication.ServiceRef, "sourceSiteRef": publication.SourceSiteRef,
			"edgeSiteRef": publication.EdgeSiteRef, "healthGateRef": publication.HealthGateRef,
		} {
			if err := requireModernFederationID(value, itemPath+"."+field); err != nil {
				return err
			}
		}
		if publication.SourceSiteRef == publication.EdgeSiteRef ||
			siteKinds[publication.SourceSiteRef] != "home" || siteKinds[publication.EdgeSiteRef] != "cloud" ||
			!publication.DefaultClosed {
			return fail(ErrInvalidPlan, itemPath, "publication observation must be a default-closed Home-to-Cloud tuple")
		}
	}
	for index, flow := range projection.Flows {
		itemPath := fmt.Sprintf("%s.flows[%d]", path, index)
		if flow.FromSiteRef == flow.ToSiteRef || siteKinds[flow.FromSiteRef] == "" || siteKinds[flow.ToSiteRef] == "" ||
			!flow.ServiceIdentityRequired || !stringListContains([]string{"tcp", "udp", "http", "https"}, flow.Protocol) ||
			len(flow.Ports) == 0 {
			return fail(ErrInvalidPlan, itemPath, "flow observation is not an exact identity-bound cross-Site tuple")
		}
		if err := validateModernFederationMethods(flow.Methods, itemPath+".methods", flow.Protocol == "http" || flow.Protocol == "https"); err != nil {
			return err
		}
		if (flow.Protocol == "tcp" || flow.Protocol == "udp") && len(flow.Methods) != 0 {
			return fail(ErrInvalidPlan, itemPath+".methods", "TCP and UDP flow observations cannot carry HTTP methods")
		}
		if err := validateModernFederationDataClasses(flow.DataClasses, itemPath+".dataClasses"); err != nil {
			return err
		}
		seenPorts := make(map[int]struct{}, len(flow.Ports))
		for portIndex, port := range flow.Ports {
			if port < 1 || port > 65535 {
				return fail(ErrInvalidPlan, fmt.Sprintf("%s.ports[%d]", itemPath, portIndex), "port is outside the supported range")
			}
			if _, duplicate := seenPorts[port]; duplicate {
				return fail(ErrDuplicate, fmt.Sprintf("%s.ports[%d]", itemPath, portIndex), "duplicate observed port %d", port)
			}
			seenPorts[port] = struct{}{}
		}
	}
	return nil
}
