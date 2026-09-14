package architecturev2renderer

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"sort"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// CloudPublicEdgePolicy is the exact provider-free route policy carried by a
// validated public-edge artifact. Runtime addresses, credentials, certificate
// material, DNS mutation and server lifecycle are deliberately absent.
type CloudPublicEdgePolicy struct {
	StackID         string
	KitSlug         string
	SiteRef         string
	NodeRef         string
	NetworkMode     string
	TransportSubnet string
	IPv6            bool
	TLSMinVersion   string
	Routes          []CloudPublicEdgeRoute
}

type cloudPublicEdgeExecutorDocument struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Module     struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	} `json:"module"`
	Contract struct {
		Apply                   string   `json:"apply"`
		CertificateIssuance     string   `json:"certificateIssuance"`
		Credentials             string   `json:"credentials"`
		DNSMutation             string   `json:"dnsMutation"`
		Generation              string   `json:"generation"`
		Operations              []string `json:"operations"`
		ProviderLifecycle       string   `json:"providerLifecycle"`
		RouteAuthority          string   `json:"routeAuthority"`
		RuntimeEnforcement      string   `json:"runtimeEnforcement"`
		Scope                   string   `json:"scope"`
		ServerProviderAuthority string   `json:"serverProviderAuthority"`
	} `json:"contract"`
	PlanInputs json.RawMessage `json:"planInputs"`
}

// ValidateCloudPublicEdgeExecutorArtifact verifies the complete artifact and
// selects one caller-bound Cloud edge node without discovering a target.
func ValidateCloudPublicEdgeExecutorArtifact(raw []byte, siteRef, nodeRef string) (CloudPublicEdgePolicy, error) {
	var document cloudPublicEdgeExecutorDocument
	if err := decodeStrict(raw, &document); err != nil {
		return CloudPublicEdgePolicy{}, wrap(ErrInvalidPlan, "cloudPublicEdgeArtifact", "decode exact Cloud public-edge artifact", err)
	}
	spec := executorContractBundleSpecs[6]
	if document.APIVersion != "stackkit.executor-contract-bundle/v1" || document.Kind != "ExecutorContractBundle" ||
		document.Module.ID != spec.moduleID || document.Module.Version != spec.moduleVersion ||
		document.Contract.Apply != "typed-local-operations" || document.Contract.CertificateIssuance != "not-owned" ||
		document.Contract.Credentials != "not-included" || document.Contract.DNSMutation != "not-owned" ||
		document.Contract.Generation != "supported" ||
		!exactStringList(document.Contract.Operations, []string{"apply-public-edge", "remove-obsolete-public-edge", "verify-public-edge", "commit-cloud-public-edge-evidence"}) ||
		document.Contract.ProviderLifecycle != "not-owned" || document.Contract.RouteAuthority != "compiler-owned-exact" ||
		document.Contract.RuntimeEnforcement != "adapter-verified" || document.Contract.Scope != "cloud-edge-node" ||
		document.Contract.ServerProviderAuthority != "not-owned" {
		return CloudPublicEdgePolicy{}, fail(ErrInvalidPlan, "cloudPublicEdgeArtifact.contract", "artifact widens or contradicts the typed Cloud public-edge authority")
	}
	decoded, err := decodeCloudRuntimeExecutorPlan(document.PlanInputs, "cloudPublicEdgeArtifact.planInputs", spec)
	if err != nil {
		return CloudPublicEdgePolicy{}, err
	}
	plan := decoded.(cloudPublicEdgeExecutorPlan)
	found := 0
	for _, target := range plan.ModuleTargets {
		if target.SiteRef == siteRef && target.ID == nodeRef {
			found++
		}
	}
	if found != 1 {
		return CloudPublicEdgePolicy{}, fail(ErrInvalidPlan, "cloudPublicEdgeArtifact.planInputs.moduleTargets", "must contain exactly one explicitly bound Cloud Site/node target")
	}
	routes := append([]CloudPublicEdgeRoute(nil), plan.PublicEdge.Routes...)
	return CloudPublicEdgePolicy{
		StackID: plan.StackID, KitSlug: plan.Kit.Slug, SiteRef: siteRef, NodeRef: nodeRef,
		NetworkMode: plan.PublicEdge.Network.Mode, TransportSubnet: plan.PublicEdge.Network.Transport.Subnet,
		IPv6: plan.PublicEdge.Network.Transport.IPv6, TLSMinVersion: plan.PublicEdge.Network.TLSMinVersion, Routes: routes,
	}, nil
}

// CloudOffsiteBackupPolicy is the exact provider-free target/custody policy
// selected for one caller-bound Cloud node.
type CloudOffsiteBackupPolicy struct {
	StackID                string
	KitSlug                string
	SiteRef                string
	NodeRef                string
	CapabilityRef          string
	ContractOwnerRef       string
	CapabilityContractHash string
	RequirementsHash       string
	BindingRef             string
	BindingHash            string
	BackupTargetRef        string
	CustodyAttestationRef  string
	StackKitsVersion       string
	CandidateDigest        string
	SpecHash               string
	IssuedAt               string
	ValidUntil             string
}

type cloudOffsiteBackupExecutorDocument struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Module     struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	} `json:"module"`
	Contract struct {
		Apply                   string   `json:"apply"`
		BackupTargetAuthority   string   `json:"backupTargetAuthority"`
		Credentials             string   `json:"credentials"`
		Generation              string   `json:"generation"`
		Operations              []string `json:"operations"`
		ProviderLifecycle       string   `json:"providerLifecycle"`
		ProviderSelection       string   `json:"providerSelection"`
		RestoreVerification     string   `json:"restoreVerification"`
		RuntimeEnforcement      string   `json:"runtimeEnforcement"`
		Scope                   string   `json:"scope"`
		ServerProviderAuthority string   `json:"serverProviderAuthority"`
		TargetLifecycle         string   `json:"targetLifecycle"`
		TransportImplementation string   `json:"transportImplementation"`
	} `json:"contract"`
	PlanInputs json.RawMessage `json:"planInputs"`
}

// ValidateCloudOffsiteBackupExecutorArtifact verifies the complete artifact
// and selects one explicit Cloud node without resolving provider or target
// connection data.
func ValidateCloudOffsiteBackupExecutorArtifact(raw []byte, siteRef, nodeRef string) (CloudOffsiteBackupPolicy, error) {
	var document cloudOffsiteBackupExecutorDocument
	if err := decodeStrict(raw, &document); err != nil {
		return CloudOffsiteBackupPolicy{}, wrap(ErrInvalidPlan, "cloudOffsiteBackupArtifact", "decode exact Cloud offsite-backup artifact", err)
	}
	spec := executorContractBundleSpecs[7]
	if document.APIVersion != "stackkit.executor-contract-bundle/v1" || document.Kind != "ExecutorContractBundle" ||
		document.Module.ID != spec.moduleID || document.Module.Version != spec.moduleVersion ||
		document.Contract.Apply != "typed-local-operations" || document.Contract.BackupTargetAuthority != "external-binding-exact" ||
		document.Contract.Credentials != "external-owner" || document.Contract.Generation != "supported" ||
		!exactStringList(document.Contract.Operations, []string{"bind-offsite-backup-target", "remove-obsolete-offsite-backup-binding", "verify-offsite-backup-target", "commit-cloud-offsite-backup-evidence"}) ||
		document.Contract.ProviderLifecycle != "not-owned" || document.Contract.ProviderSelection != "external-owner" ||
		document.Contract.RestoreVerification != "required" || document.Contract.RuntimeEnforcement != "adapter-verified" ||
		document.Contract.Scope != "cloud-backup-node" || document.Contract.ServerProviderAuthority != "not-owned" ||
		document.Contract.TargetLifecycle != "not-owned" || document.Contract.TransportImplementation != "external-owner" {
		return CloudOffsiteBackupPolicy{}, fail(ErrInvalidPlan, "cloudOffsiteBackupArtifact.contract", "artifact widens or contradicts the typed Cloud offsite-backup authority")
	}
	decoded, err := decodeCloudRuntimeExecutorPlan(document.PlanInputs, "cloudOffsiteBackupArtifact.planInputs", spec)
	if err != nil {
		return CloudOffsiteBackupPolicy{}, err
	}
	plan := decoded.(cloudOffsiteBackupExecutorPlan)
	found := 0
	for _, target := range plan.ModuleTargets {
		if target.SiteRef == siteRef && target.ID == nodeRef {
			found++
		}
	}
	if found != 1 {
		return CloudOffsiteBackupPolicy{}, fail(ErrInvalidPlan, "cloudOffsiteBackupArtifact.planInputs.moduleTargets", "must contain exactly one explicitly bound Cloud Site/node target")
	}
	var requirements map[string]map[string]cloudBackupTargetRequirement
	if err := decodeStrict(plan.CloudOffsiteBackup.Requirements, &requirements); err != nil {
		return CloudOffsiteBackupPolicy{}, wrap(ErrInvalidPlan, "cloudOffsiteBackupArtifact.planInputs.cloudOffsiteBackup.requirements", "decode requirements", err)
	}
	var bindings map[string]map[string]cloudExternalBackupTargetBinding
	if err := decodeStrict(plan.CloudOffsiteBackup.Bindings, &bindings); err != nil {
		return CloudOffsiteBackupPolicy{}, wrap(ErrInvalidPlan, "cloudOffsiteBackupArtifact.planInputs.cloudOffsiteBackup.bindings", "decode bindings", err)
	}
	requirement, ok := requirements[siteRef][cloudBackupTargetCapability]
	if !ok || !slices.Contains(requirement.TargetNodeRefs, nodeRef) {
		return CloudOffsiteBackupPolicy{}, fail(ErrInvalidPlan, "cloudOffsiteBackupArtifact.planInputs.cloudOffsiteBackup.requirements", "does not bind the selected node")
	}
	binding, ok := bindings[siteRef][cloudBackupTargetCapability]
	if !ok {
		return CloudOffsiteBackupPolicy{}, fail(ErrInvalidPlan, "cloudOffsiteBackupArtifact.planInputs.cloudOffsiteBackup.bindings", "requires the exact external target binding")
	}
	return CloudOffsiteBackupPolicy{
		StackID: requirement.StackID, KitSlug: plan.Kit.Slug, SiteRef: siteRef, NodeRef: nodeRef,
		CapabilityRef: requirement.CapabilityRef, ContractOwnerRef: requirement.ContractOwnerRef,
		CapabilityContractHash: requirement.CapabilityContractHash, RequirementsHash: requirement.RequirementsHash,
		BindingRef: binding.BindingRef, BindingHash: binding.BindingHash, BackupTargetRef: binding.BackupTargetRef,
		CustodyAttestationRef: binding.CustodyAttestationRef, StackKitsVersion: binding.StackKitsVersion,
		CandidateDigest: binding.CandidateDigest, SpecHash: binding.SpecHash,
		IssuedAt: binding.IssuedAt, ValidUntil: binding.ValidUntil,
	}, nil
}

type cloudRuntimeExecutorPlan struct {
	StackID                      string                      `json:"stackId"`
	Kit                          executorBundleKit           `json:"kit"`
	Sites                        []executorBundleSite        `json:"sites"`
	ModuleTargets                []executorBundleTarget      `json:"moduleTargets"`
	ModuleCapabilities           []executorBundleCapability  `json:"moduleCapabilities"`
	ControlPlane                 executorBundleControlPlane  `json:"controlPlane"`
	StoragePolicy                executorBundleStoragePolicy `json:"storagePolicy"`
	CloudNetworkPolicy           executorBundleNetworkPolicy `json:"cloudNetworkPolicy"`
	PublicEdge                   *CloudPublicEdgeProjection  `json:"publicEdge,omitempty"`
	BackupTargetRequirements     json.RawMessage             `json:"backupTargetRequirements,omitempty"`
	ExternalBackupTargetBindings json.RawMessage             `json:"externalBackupTargetBindings,omitempty"`
	Data                         executorBundleData          `json:"data"`
	FailurePolicy                executorBundleFailurePolicy `json:"failurePolicy"`
	CloudAdminMesh               *CloudAdminMeshProjection   `json:"cloudAdminMesh,omitempty"`
}

type CloudAdminMeshProjection struct {
	CapabilityRef string                 `json:"capabilityRef"`
	SiteRefs      []string               `json:"siteRefs"`
	NodeRefs      []string               `json:"nodeRefs"`
	Network       CloudNetworkPosture    `json:"network"`
	Routes        []CloudPublicEdgeRoute `json:"routes"`
}

type CloudNetworkPosture struct {
	Mode      string `json:"mode"`
	Transport struct {
		Subnet string `json:"subnet"`
		IPv6   bool   `json:"ipv6"`
	} `json:"transport"`
	TLSMinVersion string `json:"tlsMinVersion"`
}

type cloudAdminMeshExecutorPlan struct {
	StackID            string                     `json:"stackId"`
	Kit                executorBundleKit          `json:"kit"`
	Sites              []executorBundleSite       `json:"sites"`
	ModuleTargets      []executorBundleTarget     `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability `json:"moduleCapabilities"`
	ControlPlane       executorBundleControlPlane `json:"controlPlane"`
	CloudAdminMesh     CloudAdminMeshProjection   `json:"cloudAdminMesh"`
}

func (cloudAdminMeshExecutorPlan) executorContractPlanMarker() {}

type CloudPublicEdgeProjection struct {
	CapabilityRef string                 `json:"capabilityRef"`
	Network       CloudNetworkPosture    `json:"network"`
	Routes        []CloudPublicEdgeRoute `json:"routes"`
}

type cloudPublicEdgeExecutorPlan struct {
	StackID            string                     `json:"stackId"`
	Kit                executorBundleKit          `json:"kit"`
	Sites              []executorBundleSite       `json:"sites"`
	ModuleTargets      []executorBundleTarget     `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability `json:"moduleCapabilities"`
	ControlPlane       executorBundleControlPlane `json:"controlPlane"`
	PublicEdge         CloudPublicEdgeProjection  `json:"publicEdge"`
}

func (cloudPublicEdgeExecutorPlan) executorContractPlanMarker() {}

type CloudPublicEdgeRoute struct {
	IngressAuth           string                               `json:"ingressAuth"`
	ID                    string                               `json:"id"`
	ServiceRef            string                               `json:"serviceRef"`
	ModuleRef             string                               `json:"moduleRef"`
	OriginSiteRef         string                               `json:"originSiteRef"`
	OriginSiteRefs        []string                             `json:"originSiteRefs"`
	OriginNodeRefs        []string                             `json:"originNodeRefs"`
	OriginSelector        string                               `json:"originSelector"`
	OriginSelection       *rawServiceEndpointOriginSelectionV2 `json:"originSelection,omitempty"`
	BackendPoolRef        string                               `json:"backendPoolRef"`
	BackendPool           CloudPublicEdgeBackendPool           `json:"backendPool"`
	Exposure              string                               `json:"exposure"`
	Protocol              string                               `json:"protocol"`
	UpstreamProtocol      string                               `json:"upstreamProtocol"`
	Port                  int                                  `json:"port"`
	TargetPort            int                                  `json:"targetPort"`
	Host                  string                               `json:"host"`
	Path                  string                               `json:"path"`
	Access                CloudPublicEdgeAccess                `json:"access"`
	TLS                   CloudPublicEdgeTLS                   `json:"tls"`
	HealthGateRef         string                               `json:"healthGateRef"`
	HealthProbe           CloudPublicEdgeHealthProbe           `json:"healthProbe"`
	CapabilityAuthorities []CloudPublicEdgeCapabilityAuthority `json:"capabilityAuthorities"`
}

type CloudPublicEdgeBackendPool struct {
	UpstreamProtocol string                         `json:"upstreamProtocol"`
	TargetPort       int                            `json:"targetPort"`
	Members          []CloudPublicEdgeBackendMember `json:"members"`
}

type CloudPublicEdgeBackendMember struct {
	SiteRef     string `json:"siteRef"`
	NodeRef     string `json:"nodeRef"`
	InstanceRef string `json:"instanceRef"`
}

type CloudPublicEdgeAccess struct {
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

type CloudPublicEdgeTLS struct {
	Required           bool   `json:"required"`
	Mode               string `json:"mode"`
	MinVersion         string `json:"minVersion,omitempty"`
	ProfileRef         string `json:"profileRef,omitempty"`
	IssuerRef          string `json:"issuerRef,omitempty"`
	OwnerCapabilityRef string `json:"ownerCapabilityRef,omitempty"`
}

type CloudPublicEdgeHealthProbe struct {
	Kind             string `json:"kind"`
	Protocol         string `json:"protocol"`
	Port             int    `json:"port"`
	TimeoutSeconds   int    `json:"timeoutSeconds"`
	Method           string `json:"method,omitempty"`
	FollowRedirects  *bool  `json:"followRedirects,omitempty"`
	Path             string `json:"path,omitempty"`
	ExpectedStatuses []int  `json:"expectedStatuses,omitempty"`
}

type CloudPublicEdgeCapabilityAuthority struct {
	CapabilityRef string `json:"capabilityRef"`
	Role          string `json:"role"`
}

func (cloudRuntimeExecutorPlan) executorContractPlanMarker() {}

type cloudOffsiteBackupProjection struct {
	Requirements json.RawMessage `json:"requirements"`
	Bindings     json.RawMessage `json:"bindings"`
}

type cloudOffsiteBackupExecutorPlan struct {
	StackID            string                       `json:"stackId"`
	Kit                executorBundleKit            `json:"kit"`
	Sites              []executorBundleSite         `json:"sites"`
	ModuleTargets      []executorBundleTarget       `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability   `json:"moduleCapabilities"`
	ControlPlane       executorBundleControlPlane   `json:"controlPlane"`
	CloudOffsiteBackup cloudOffsiteBackupProjection `json:"cloudOffsiteBackup"`
}

func (cloudOffsiteBackupExecutorPlan) executorContractPlanMarker() {}

func normalizeCloudRouteIngressAuth(routes []CloudPublicEdgeRoute, path string) error {
	for index := range routes {
		if routes[index].IngressAuth == "" {
			routes[index].IngressAuth = "native"
		}
		if !oneOf(routes[index].IngressAuth, "none", "native", "forward-auth") {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s.routes[%d].ingressAuth", path, index), "unsupported ingress auth mode %q", routes[index].IngressAuth)
		}
	}
	return nil
}

func decodeCloudRuntimeExecutorPlan(raw []byte, path string, spec executorContractBundleSpec) (executorContractPlan, error) {
	if spec.moduleID == cloudPrivateAdminMeshModuleID {
		var exact cloudAdminMeshExecutorPlan
		if err := decodeStrict(raw, &exact); err != nil {
			return nil, wrap(ErrInvalidPlan, path, "decode exact Cloud admin-mesh executor contract", err)
		}
		if err := normalizeCloudRouteIngressAuth(exact.CloudAdminMesh.Routes, path+".cloudAdminMesh"); err != nil {
			return nil, err
		}
		if err := validateExecutorContractPlanCommon(exact.StackID, exact.Kit, exact.Sites, exact.ModuleTargets, exact.ModuleCapabilities, exact.ControlPlane, spec, path); err != nil {
			return nil, err
		}
		validationPlan := cloudRuntimeExecutorPlan{
			StackID: exact.StackID, Kit: exact.Kit, Sites: exact.Sites,
			ModuleTargets: exact.ModuleTargets, ModuleCapabilities: exact.ModuleCapabilities,
			ControlPlane: exact.ControlPlane, CloudAdminMesh: &exact.CloudAdminMesh,
		}
		if err := validateCloudAdminMeshProjection(exact.CloudAdminMesh, validationPlan, path+".cloudAdminMesh"); err != nil {
			return nil, err
		}
		return exact, nil
	}
	if spec.moduleID == cloudPublicEdgeModuleID {
		var exact cloudPublicEdgeExecutorPlan
		if err := decodeStrict(raw, &exact); err != nil {
			return nil, wrap(ErrInvalidPlan, path, "decode exact Cloud public-edge executor contract", err)
		}
		if err := normalizeCloudRouteIngressAuth(exact.PublicEdge.Routes, path+".publicEdge"); err != nil {
			return nil, err
		}
		if err := validateExecutorContractPlanCommon(exact.StackID, exact.Kit, exact.Sites, exact.ModuleTargets, exact.ModuleCapabilities, exact.ControlPlane, spec, path); err != nil {
			return nil, err
		}
		validationPlan := cloudRuntimeExecutorPlan{
			StackID: exact.StackID, Kit: exact.Kit, Sites: exact.Sites,
			ModuleTargets: exact.ModuleTargets, ModuleCapabilities: exact.ModuleCapabilities,
			ControlPlane: exact.ControlPlane, PublicEdge: &exact.PublicEdge,
		}
		if err := validateCloudPublicEdgeProjection(exact.PublicEdge, validationPlan, path+".publicEdge"); err != nil {
			return nil, err
		}
		return exact, nil
	}
	if spec.moduleID == cloudOffsiteBackupModuleID {
		var exact cloudOffsiteBackupExecutorPlan
		if err := decodeStrict(raw, &exact); err != nil {
			return nil, wrap(ErrInvalidPlan, path, "decode exact Cloud offsite-backup executor contract", err)
		}
		if err := validateExecutorContractPlanCommon(exact.StackID, exact.Kit, exact.Sites, exact.ModuleTargets, exact.ModuleCapabilities, exact.ControlPlane, spec, path); err != nil {
			return nil, err
		}
		validationPlan := cloudRuntimeExecutorPlan{
			StackID: exact.StackID, Kit: exact.Kit, Sites: exact.Sites,
			ModuleTargets: exact.ModuleTargets, ModuleCapabilities: exact.ModuleCapabilities,
			ControlPlane:                 exact.ControlPlane,
			BackupTargetRequirements:     exact.CloudOffsiteBackup.Requirements,
			ExternalBackupTargetBindings: exact.CloudOffsiteBackup.Bindings,
		}
		if err := validateCloudBackupTargetProjection(validationPlan, path+".cloudOffsiteBackup"); err != nil {
			return nil, err
		}
		return exact, nil
	}
	var plan cloudRuntimeExecutorPlan
	if err := decodeStrict(raw, &plan); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact Cloud executor contract", err)
	}
	if err := validateExecutorContractPlanCommon(plan.StackID, plan.Kit, plan.Sites, plan.ModuleTargets, plan.ModuleCapabilities, plan.ControlPlane, spec, path); err != nil {
		return nil, err
	}
	if plan.CloudAdminMesh != nil {
		return nil, fail(ErrInvalidPlan, path+".cloudAdminMesh", "Cloud admin-mesh authority is forbidden for this module")
	}
	if plan.PublicEdge != nil {
		return nil, fail(ErrInvalidPlan, path+".publicEdge", "public-edge route authority is forbidden for this Cloud module")
	}
	if err := validateStoragePolicy(plan.StoragePolicy, path+".storagePolicy"); err != nil {
		return nil, err
	}
	if err := validateExecutorBundleNetworkPolicy(plan.CloudNetworkPolicy, plan.Kit.Slug, true, path+".cloudNetworkPolicy"); err != nil {
		return nil, err
	}
	if spec.moduleID == cloudOffsiteBackupModuleID {
		if len(plan.BackupTargetRequirements) == 0 || len(plan.ExternalBackupTargetBindings) == 0 {
			return nil, fail(ErrInvalidPlan, path, "Cloud offsite backup requires both target requirement and external binding projections")
		}
		if err := validateCloudBackupTargetProjection(plan, path); err != nil {
			return nil, err
		}
	} else if len(plan.BackupTargetRequirements) != 0 || len(plan.ExternalBackupTargetBindings) != 0 {
		return nil, fail(ErrInvalidPlan, path, "backup target authority is forbidden for this Cloud module")
	}
	if err := validateExecutorBundleData(plan.Data, plan.Sites, path+".data"); err != nil {
		return nil, err
	}
	if err := validateExecutorBundleFailurePolicy(plan.FailurePolicy, path+".failurePolicy"); err != nil {
		return nil, err
	}
	return plan, nil
}

func validateCloudAdminMeshProjection(projection CloudAdminMeshProjection, plan cloudRuntimeExecutorPlan, path string) error {
	if projection.CapabilityRef != "private-admin-mesh" || len(projection.SiteRefs) == 0 || len(projection.NodeRefs) == 0 || len(projection.Routes) == 0 {
		return fail(ErrInvalidPlan, path, "projection requires exact capability, Site, node, and route authority")
	}
	if !exactStringList(projection.SiteRefs, sortedExecutorBundleSites(plan.Sites, "cloud")) {
		return fail(ErrInvalidPlan, path+".siteRefs", "must exactly equal the module Cloud Site projection")
	}
	wantNodes := make([]string, 0, len(plan.ModuleTargets))
	nodeSites := make(map[string]string, len(plan.ModuleTargets))
	for _, target := range plan.ModuleTargets {
		wantNodes = append(wantNodes, target.ID)
		nodeSites[target.ID] = target.SiteRef
	}
	sort.Strings(wantNodes)
	if !exactStringList(projection.NodeRefs, wantNodes) {
		return fail(ErrInvalidPlan, path+".nodeRefs", "must exactly equal the module target nodes")
	}
	if err := validateCloudNetworkPosture(projection.Network, path+".network"); err != nil {
		return err
	}
	previousRouteID := ""
	for index, route := range projection.Routes {
		routePath := fmt.Sprintf("%s.routes[%d]", path, index)
		if previousRouteID != "" && route.ID <= previousRouteID {
			return fail(ErrInvalidPlan, routePath+".id", "routes must be unique and sorted")
		}
		previousRouteID = route.ID
		if route.Exposure != "private" || route.Access.PolicyExposure != "private" ||
			route.Access.Authentication != "human+device" || !route.Access.EnrolledDeviceRequired ||
			route.Access.LANStepDown || !route.Access.DefaultClosed {
			return fail(ErrInvalidPlan, routePath, "admin route must remain private, device-bound, and default-closed")
		}
		authorityCount := 0
		for _, authority := range route.CapabilityAuthorities {
			if authority.CapabilityRef == projection.CapabilityRef && authority.Role == "access" {
				authorityCount++
			}
		}
		if authorityCount != 1 {
			return fail(ErrInvalidPlan, routePath+".capabilityAuthorities", "requires exactly one private-admin-mesh access authority")
		}
		if !containsExecutorBundleString(projection.SiteRefs, route.OriginSiteRef) {
			return fail(ErrInvalidPlan, routePath+".originSiteRef", "route origin is outside the exact Cloud Sites")
		}
		for allowedIndex, siteRef := range route.Access.AllowedSiteRefs {
			if !containsExecutorBundleString(projection.SiteRefs, siteRef) {
				return fail(ErrInvalidPlan, fmt.Sprintf("%s.access.allowedSiteRefs[%d]", routePath, allowedIndex), "allowed Site is outside the exact Cloud Sites")
			}
		}
		for nodeIndex, nodeRef := range route.OriginNodeRefs {
			if nodeSites[nodeRef] != route.OriginSiteRef {
				return fail(ErrInvalidPlan, fmt.Sprintf("%s.originNodeRefs[%d]", routePath, nodeIndex), "origin node is outside the exact module target Site")
			}
		}
		for memberIndex, member := range route.BackendPool.Members {
			if nodeSites[member.NodeRef] != member.SiteRef || !containsExecutorBundleString(projection.SiteRefs, member.SiteRef) {
				return fail(ErrInvalidPlan, fmt.Sprintf("%s.backendPool.members[%d]", routePath, memberIndex), "backend member is outside the exact module targets")
			}
		}
	}
	return nil
}

type cloudBackupTargetRequirement struct {
	APIVersion             string                  `json:"apiVersion"`
	Kind                   string                  `json:"kind"`
	StackID                string                  `json:"stackId"`
	SiteRef                string                  `json:"siteRef"`
	CapabilityRef          string                  `json:"capabilityRef"`
	ContractOwnerRef       string                  `json:"contractOwnerRef"`
	CapabilityContractHash string                  `json:"capabilityContractHash"`
	TargetNodeRefs         []string                `json:"targetNodeRefs"`
	Policy                 cloudBackupTargetPolicy `json:"policy"`
	SpecHash               string                  `json:"specHash"`
	RequirementsHash       string                  `json:"requirementsHash"`
}

type cloudBackupTargetPolicy struct {
	Scope                       string `json:"scope"`
	EncryptionRequired          bool   `json:"encryptionRequired"`
	CredentialCustody           string `json:"credentialCustody"`
	TargetLifecycle             string `json:"targetLifecycle"`
	RestoreVerificationRequired bool   `json:"restoreVerificationRequired"`
	ProviderSelection           string `json:"providerSelection"`
}

type cloudExternalBackupTargetBinding struct {
	APIVersion             string `json:"apiVersion"`
	Kind                   string `json:"kind"`
	BindingRef             string `json:"bindingRef"`
	BackupTargetRef        string `json:"backupTargetRef"`
	CustodyAttestationRef  string `json:"custodyAttestationRef"`
	StackID                string `json:"stackId"`
	SiteRef                string `json:"siteRef"`
	CapabilityRef          string `json:"capabilityRef"`
	ContractOwnerRef       string `json:"contractOwnerRef"`
	CapabilityContractHash string `json:"capabilityContractHash"`
	RequirementsHash       string `json:"requirementsHash"`
	StackKitsVersion       string `json:"stackkitsVersion"`
	CandidateDigest        string `json:"candidateDigest"`
	SpecHash               string `json:"specHash"`
	IssuedAt               string `json:"issuedAt"`
	ValidUntil             string `json:"validUntil"`
	BindingHash            string `json:"bindingHash"`
}

func validateCloudBackupTargetProjection(plan cloudRuntimeExecutorPlan, path string) error {
	var requirements map[string]map[string]cloudBackupTargetRequirement
	if err := decodeStrict(plan.BackupTargetRequirements, &requirements); err != nil {
		return wrap(ErrInvalidPlan, path+".backupTargetRequirements", "decode closed backup target requirements", err)
	}
	var bindings map[string]map[string]cloudExternalBackupTargetBinding
	if err := decodeStrict(plan.ExternalBackupTargetBindings, &bindings); err != nil {
		return wrap(ErrInvalidPlan, path+".externalBackupTargetBindings", "decode closed external backup target bindings", err)
	}
	cloudSites := sortedExecutorBundleSites(plan.Sites, "cloud")
	targetNodes := make(map[string]string, len(plan.ModuleTargets))
	for _, target := range plan.ModuleTargets {
		targetNodes[target.ID] = target.SiteRef
	}
	if len(requirements) == 0 {
		return fail(ErrInvalidPlan, path+".backupTargetRequirements", "must contain the compiler-owned Cloud backup target requirement")
	}
	for siteRef, capabilityRequirements := range requirements {
		if !containsExecutorBundleString(cloudSites, siteRef) || len(capabilityRequirements) != 1 {
			return fail(ErrInvalidPlan, path+".backupTargetRequirements."+siteRef, "must contain one requirement for a module Cloud Site")
		}
		requirement, ok := capabilityRequirements[cloudBackupTargetCapability]
		if !ok || requirement.APIVersion != "stackkit.backup-target-requirement/v1" || requirement.Kind != "BackupTargetRequirement" ||
			requirement.StackID != plan.StackID || requirement.SiteRef != siteRef || requirement.CapabilityRef != cloudBackupTargetCapability ||
			requirement.Policy.Scope != "governed-data-only" || !requirement.Policy.EncryptionRequired || requirement.Policy.CredentialCustody != "external" ||
			requirement.Policy.TargetLifecycle != "external" || !requirement.Policy.RestoreVerificationRequired || requirement.Policy.ProviderSelection != "external" {
			return fail(ErrInvalidPlan, path+".backupTargetRequirements."+siteRef, "widens or mismatches the closed provider-free backup target requirement")
		}
		if len(requirement.TargetNodeRefs) == 0 {
			return fail(ErrInvalidPlan, path+".backupTargetRequirements."+siteRef+".targetNodeRefs", "must select at least one module target")
		}
		for _, nodeRef := range requirement.TargetNodeRefs {
			if targetNodes[nodeRef] != siteRef {
				return fail(ErrInvalidPlan, path+".backupTargetRequirements."+siteRef+".targetNodeRefs", "contains a node outside the exact module Site")
			}
		}
		rawRequirement, err := json.Marshal(requirement)
		if err != nil {
			return wrap(ErrInvalidPlan, path+".backupTargetRequirements."+siteRef, "marshal requirement", err)
		}
		var requirementBody map[string]any
		if err := decodeStrict(rawRequirement, &requirementBody); err != nil {
			return wrap(ErrInvalidPlan, path+".backupTargetRequirements."+siteRef, "decode requirement body", err)
		}
		wantRequirementHash, err := resolvedplan.ComputeBackupTargetRequirementHash(resolvedplan.BackupTargetRequirement(requirementBody))
		if err != nil || requirement.RequirementsHash != wantRequirementHash {
			return fail(ErrInvalidPlan, path+".backupTargetRequirements."+siteRef+".requirementsHash", "does not match the canonical requirement body")
		}
		capabilityBindings := bindings[siteRef]
		if len(capabilityBindings) == 0 {
			continue
		}
		if len(capabilityBindings) != 1 {
			return fail(ErrInvalidPlan, path+".externalBackupTargetBindings."+siteRef, "must contain only the exact backup capability")
		}
		binding, ok := capabilityBindings[cloudBackupTargetCapability]
		if !ok || binding.APIVersion != "stackkit.external-backup-target-binding/v1" || binding.Kind != "ExternalBackupTargetBinding" ||
			binding.StackID != requirement.StackID || binding.SiteRef != requirement.SiteRef || binding.CapabilityRef != requirement.CapabilityRef ||
			binding.ContractOwnerRef != requirement.ContractOwnerRef || binding.CapabilityContractHash != requirement.CapabilityContractHash ||
			binding.RequirementsHash != requirement.RequirementsHash || binding.SpecHash != requirement.SpecHash ||
			!validOpaqueSHA256Ref(binding.BindingRef, "backup-target-binding") || !validOpaqueSHA256Ref(binding.BackupTargetRef, "backup-target") ||
			!validOpaqueSHA256Ref(binding.CustodyAttestationRef, "backup-custody-attestation") {
			return fail(ErrInvalidPlan, path+".externalBackupTargetBindings."+siteRef, "does not exactly match the provider-free backup target requirement")
		}
		rawBinding, err := json.Marshal(binding)
		if err != nil {
			return wrap(ErrInvalidPlan, path+".externalBackupTargetBindings."+siteRef, "marshal binding", err)
		}
		var bindingBody map[string]any
		if err := decodeStrict(rawBinding, &bindingBody); err != nil {
			return wrap(ErrInvalidPlan, path+".externalBackupTargetBindings."+siteRef, "decode binding body", err)
		}
		wantBindingHash, err := resolvedplan.ComputeExternalBackupTargetBindingHash(resolvedplan.ExternalBackupTargetBinding(bindingBody))
		if err != nil || binding.BindingHash != wantBindingHash {
			return fail(ErrInvalidPlan, path+".externalBackupTargetBindings."+siteRef+".bindingHash", "does not match the canonical binding body")
		}
	}
	for siteRef := range bindings {
		if _, ok := requirements[siteRef]; !ok {
			return fail(ErrInvalidPlan, path+".externalBackupTargetBindings."+siteRef, "binding targets a Site outside the exact requirement")
		}
	}
	return nil
}

func validateCloudPublicEdgeProjection(projection CloudPublicEdgeProjection, plan cloudRuntimeExecutorPlan, path string) error {
	if projection.CapabilityRef != "public-edge" || projection.Routes == nil {
		return fail(ErrInvalidPlan, path, "projection must carry only the exact public-edge capability and a closed route list")
	}
	rawRoutes, err := json.Marshal(projection.Routes)
	if err != nil {
		return wrap(ErrInvalidPlan, path+".routes", "marshal public-edge routes", err)
	}
	if err := validatePublicServiceRouteListV4(rawRoutes, path+".routes"); err != nil {
		return err
	}
	if err := validateCloudNetworkPosture(projection.Network, path+".network"); err != nil {
		return err
	}
	siteKinds := executorBundleSiteKinds(plan.Sites)
	targetSites := make(map[string]string, len(plan.ModuleTargets))
	for _, target := range plan.ModuleTargets {
		targetSites[target.ID] = target.SiteRef
	}
	// Route order is decided by the canonical plan form, which sorts
	// set-semantic lists by their serialized bytes rather than by id. Requiring
	// ascending ids here would reject every plan the canonicalizer produces, so
	// the enforced invariant is uniqueness.
	seenRouteIDs := make(map[string]struct{}, len(projection.Routes))
	for index, route := range projection.Routes {
		routePath := fmt.Sprintf("%s.routes[%d]", path, index)
		if _, duplicate := seenRouteIDs[route.ID]; duplicate {
			return fail(ErrInvalidPlan, routePath+".id", "public-edge routes must be unique")
		}
		seenRouteIDs[route.ID] = struct{}{}
		if route.Exposure != "public" || route.TLS.Mode != "terminate-at-edge" || !route.TLS.Required || !route.Access.DefaultClosed {
			return fail(ErrInvalidPlan, routePath, "public-edge route must be public, default-closed, and terminate required TLS at the edge")
		}
		edgeAuthorityCount := 0
		for _, authority := range route.CapabilityAuthorities {
			if authority.CapabilityRef == "public-edge" && authority.Role == "edge" {
				edgeAuthorityCount++
			}
		}
		if edgeAuthorityCount != 1 {
			return fail(ErrInvalidPlan, routePath+".capabilityAuthorities", "route must bind exactly one public-edge edge authority")
		}
		if route.OriginSiteRef != "" && siteKinds[route.OriginSiteRef] != "cloud" {
			return fail(ErrInvalidPlan, routePath+".originSiteRef", "public-edge origin must be an exact projected Cloud Site")
		}
		for siteIndex, siteRef := range route.OriginSiteRefs {
			if siteKinds[siteRef] != "cloud" {
				return fail(ErrInvalidPlan, fmt.Sprintf("%s.originSiteRefs[%d]", routePath, siteIndex), "public-edge origin must be an exact projected Cloud Site")
			}
		}
		for nodeIndex, nodeRef := range route.OriginNodeRefs {
			if !containsExecutorBundleString(route.OriginSiteRefs, targetSites[nodeRef]) {
				return fail(ErrInvalidPlan, fmt.Sprintf("%s.originNodeRefs[%d]", routePath, nodeIndex), "origin node is outside the exact Cloud module targets")
			}
		}
		for memberIndex, member := range route.BackendPool.Members {
			if siteKinds[member.SiteRef] != "cloud" || targetSites[member.NodeRef] != member.SiteRef {
				return fail(ErrInvalidPlan, fmt.Sprintf("%s.backendPool.members[%d]", routePath, memberIndex), "backend member is outside the exact Cloud module targets")
			}
		}
	}
	return nil
}

func validateCloudNetworkPosture(posture CloudNetworkPosture, path string) error {
	transportPrefix, err := netip.ParsePrefix(posture.Transport.Subnet)
	if !containsExecutorBundleString([]string{"public-capable", "private", "hybrid"}, posture.Mode) ||
		err != nil || transportPrefix.String() != posture.Transport.Subnet ||
		transportPrefix.Addr().Is6() != posture.Transport.IPv6 ||
		!containsExecutorBundleString([]string{"TLS1.2", "TLS1.3"}, posture.TLSMinVersion) {
		return fail(ErrInvalidPlan, path, "contains an unsupported bounded network posture")
	}
	return nil
}
