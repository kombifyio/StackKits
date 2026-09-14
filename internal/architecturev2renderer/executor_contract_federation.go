package architecturev2renderer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

type federationRuntimeExecutorPlan struct {
	StackID                        string                     `json:"stackId"`
	Kit                            executorBundleKit          `json:"kit"`
	Sites                          []executorBundleSite       `json:"sites"`
	ModuleTargets                  []executorBundleTarget     `json:"moduleTargets"`
	ModuleCapabilities             []executorBundleCapability `json:"moduleCapabilities"`
	ControlPlane                   executorBundleControlPlane `json:"controlPlane"`
	FederationLinkPolicy           json.RawMessage            `json:"federationLinkPolicy,omitempty"`
	FederationControlActions       json.RawMessage            `json:"federationControlActions,omitempty"`
	FederationBackupPolicy         json.RawMessage            `json:"federationBackupPolicy,omitempty"`
	FederationObservability        json.RawMessage            `json:"federationObservability,omitempty"`
	FederationLinkRequirements     json.RawMessage            `json:"federationLinkRequirements,omitempty"`
	ExternalFederationLinkBindings json.RawMessage            `json:"externalFederationLinkBindings,omitempty"`
}

func (federationRuntimeExecutorPlan) executorContractPlanMarker() {}

type bridgePublicationExecutorPlan struct {
	StackID            string                     `json:"stackId"`
	Kit                executorBundleKit          `json:"kit"`
	Sites              []executorBundleSite       `json:"sites"`
	ModuleTargets      []executorBundleTarget     `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability `json:"moduleCapabilities"`
	ControlPlane       executorBundleControlPlane `json:"controlPlane"`
	BridgePublications []bridgePublication        `json:"bridgePublications"`
}

func (bridgePublicationExecutorPlan) executorContractPlanMarker() {}

type bridgePublication struct {
	ServiceRef         string                       `json:"serviceRef"`
	SourceSiteRef      string                       `json:"sourceSiteRef"`
	EdgeSiteRef        string                       `json:"edgeSiteRef"`
	Host               string                       `json:"host"`
	Protocol           string                       `json:"protocol"`
	Port               int                          `json:"port"`
	Path               string                       `json:"path"`
	DefaultClosed      bool                         `json:"defaultClosed"`
	TLS                bridgePublicationTLS         `json:"tls"`
	Auth               bridgePublicationAuth        `json:"auth"`
	Origin             bridgePublicationOrigin      `json:"origin"`
	RateLimit          bridgePublicationRateLimit   `json:"rateLimit"`
	ModuleRef          string                       `json:"moduleRef"`
	UnitRef            string                       `json:"unitRef"`
	OriginNodeRefs     []string                     `json:"originNodeRefs"`
	OriginInstanceRefs []string                     `json:"originInstanceRefs"`
	OriginTargets      []bridgeOriginMTLSTarget     `json:"originTargets"`
	UpstreamProtocol   string                       `json:"upstreamProtocol"`
	TargetPort         int                          `json:"targetPort"`
	HealthGateRef      string                       `json:"healthGateRef"`
	HealthProbe        *rawPublicRouteHealthProbeV3 `json:"healthProbe,omitempty"`
	DataBindingRef     string                       `json:"dataBindingRef,omitempty"`
	Access             bridgePublicationAccess      `json:"access"`
}

type bridgePublicationTLS struct {
	Required   bool   `json:"required"`
	Mode       string `json:"mode"`
	MinVersion string `json:"minVersion"`
}

type bridgePublicationAuth struct {
	Required  bool   `json:"required"`
	PolicyRef string `json:"policyRef"`
}

type bridgePublicationOrigin struct {
	IdentityRef  string `json:"identityRef"`
	MTLSRequired bool   `json:"mtlsRequired"`
}

type bridgePublicationRateLimit struct {
	Enabled       bool `json:"enabled"`
	Requests      int  `json:"requests"`
	WindowSeconds int  `json:"windowSeconds"`
}

type bridgePublicationAccess struct {
	Exposure               string   `json:"exposure"`
	PolicyExposure         string   `json:"policyExposure"`
	Authentication         string   `json:"authentication"`
	Privilege              string   `json:"privilege"`
	EnrolledDeviceRequired bool     `json:"enrolledDeviceRequired"`
	OwnerStepUpRequired    bool     `json:"ownerStepUpRequired"`
	LANStepDown            bool     `json:"lanStepDown"`
	AllowedMethods         []string `json:"allowedMethods,omitempty"`
	DefaultClosed          bool     `json:"defaultClosed"`
	PolicyRef              string   `json:"policyRef"`
}

type bridgeOriginMTLSExecutorPlan struct {
	StackID            string                     `json:"stackId"`
	Kit                executorBundleKit          `json:"kit"`
	Sites              []executorBundleSite       `json:"sites"`
	ModuleTargets      []executorBundleTarget     `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability `json:"moduleCapabilities"`
	ControlPlane       executorBundleControlPlane `json:"controlPlane"`
	BridgeOriginMTLS   bridgeOriginMTLSProjection `json:"bridgeOriginMTLS"`
}

type bridgeOriginMTLSProjection struct {
	Publications []bridgeOriginMTLSPublication `json:"publications"`
}

type bridgeOriginMTLSPublication struct {
	ServiceRef         string                           `json:"serviceRef"`
	IdentityRef        string                           `json:"identityRef"`
	SourceSiteRef      string                           `json:"sourceSiteRef"`
	EdgeSiteRef        string                           `json:"edgeSiteRef"`
	ModuleRef          string                           `json:"moduleRef"`
	UnitRef            string                           `json:"unitRef"`
	OriginNodeRefs     []string                         `json:"originNodeRefs"`
	OriginInstanceRefs []string                         `json:"originInstanceRefs"`
	OriginTargets      []bridgeOriginMTLSTarget         `json:"originTargets"`
	UpstreamProtocol   string                           `json:"upstreamProtocol"`
	TargetPort         int                              `json:"targetPort"`
	Transport          bridgeOriginMTLSTransport        `json:"transport"`
	WorkloadIdentity   bridgeOriginMTLSWorkloadIdentity `json:"workloadIdentity"`
	EdgeVerifier       bridgeOriginMTLSEdgeVerifier     `json:"edgeVerifier"`
}

type bridgeOriginMTLSTarget struct {
	NodeRef     string `json:"nodeRef"`
	InstanceRef string `json:"instanceRef"`
}

type bridgeOriginMTLSTransport struct {
	Mode              string `json:"mode"`
	MinimumTLSVersion string `json:"minimumTLSVersion"`
	ServerName        string `json:"serverName"`
	OutboundOnly      bool   `json:"outboundOnly"`
	GeneralLANAccess  bool   `json:"generalLANAccess"`
}

type bridgeOriginMTLSWorkloadIdentity struct {
	CredentialIssuerRef           string `json:"credentialIssuerRef"`
	Issuer                        string `json:"issuer"`
	Audience                      string `json:"audience"`
	VerificationKeySetRef         string `json:"verificationKeySetRef"`
	ProofOfPossessionRequired     bool   `json:"proofOfPossessionRequired"`
	CredentialTTLSeconds          int    `json:"credentialTTLSeconds"`
	RevocationMaxStalenessSeconds int    `json:"revocationMaxStalenessSeconds"`
}

type bridgeOriginMTLSEdgeVerifier struct {
	VerifierRef                string `json:"verifierRef"`
	DistributionRef            string `json:"distributionRef"`
	VerificationKeySetRef      string `json:"verificationKeySetRef"`
	MaxStalenessSeconds        int    `json:"maxStalenessSeconds"`
	IncludesPrivateKeyMaterial bool   `json:"includesPrivateKeyMaterial"`
	IncludesSigningAuthority   bool   `json:"includesSigningAuthority"`
	ReverseAllowed             bool   `json:"reverseAllowed"`
}

func (bridgeOriginMTLSExecutorPlan) executorContractPlanMarker() {}

func decodeBridgePublicationExecutorPlan(raw []byte, path string, spec executorContractBundleSpec) (executorContractPlan, error) {
	var plan bridgePublicationExecutorPlan
	if err := decodeStrict(raw, &plan); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact bridge publication handoff", err)
	}
	if err := validateExecutorContractPlanCommon(plan.StackID, plan.Kit, plan.Sites, plan.ModuleTargets, plan.ModuleCapabilities, plan.ControlPlane, spec, path); err != nil {
		return nil, err
	}
	if len(plan.BridgePublications) == 0 {
		return nil, fail(ErrInvalidPlan, path+".bridgePublications", "requires at least one compiler-owned service publication")
	}
	for index, publication := range plan.BridgePublications {
		if err := validateBridgePublication(publication, fmt.Sprintf("%s.bridgePublications[%d]", path, index)); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func validateBridgePublication(publication bridgePublication, path string) error {
	if publication.SourceSiteRef == publication.EdgeSiteRef || publication.Host == "" ||
		publication.Protocol != "https" || publication.Port != 443 || !strings.HasPrefix(publication.Path, "/") ||
		!publication.DefaultClosed || !publication.TLS.Required || publication.TLS.Mode != "terminate-at-edge" ||
		!containsExecutorBundleString([]string{"TLS1.2", "TLS1.3"}, publication.TLS.MinVersion) ||
		!publication.Auth.Required || publication.Auth.PolicyRef == "" ||
		publication.Origin.IdentityRef == "" || !publication.Origin.MTLSRequired ||
		!publication.RateLimit.Enabled || publication.RateLimit.Requests < 1 || publication.RateLimit.WindowSeconds < 1 ||
		publication.ModuleRef == "" || publication.UnitRef == "" ||
		!containsExecutorBundleString([]string{"http", "https", "tcp"}, publication.UpstreamProtocol) ||
		publication.TargetPort < 1 || publication.TargetPort > 65535 || publication.HealthGateRef == "" {
		return fail(ErrInvalidPlan, path, "service publication closure is incomplete or widened")
	}
	if publication.HealthProbe != nil {
		if err := validateBridgePublicationHealthProbe(*publication.HealthProbe, publication.UpstreamProtocol, publication.TargetPort, path+".healthProbe"); err != nil {
			return err
		}
	}
	for index, target := range publication.OriginTargets {
		targetPath := fmt.Sprintf("%s.originTargets[%d]", path, index)
		if err := requireContractID(target.NodeRef, targetPath+".nodeRef"); err != nil {
			return err
		}
		if err := requireContractID(target.InstanceRef, targetPath+".instanceRef"); err != nil {
			return err
		}
	}
	access := publication.Access
	if access.Exposure != "public" || access.PolicyExposure != "public" ||
		!containsExecutorBundleString([]string{"human", "device", "human+device", "workload"}, access.Authentication) ||
		!containsExecutorBundleString([]string{"user", "admin", "identity", "secrets", "vault", "recovery"}, access.Privilege) ||
		access.LANStepDown || !access.DefaultClosed || access.PolicyRef != publication.Auth.PolicyRef ||
		len(access.AllowedMethods) == 0 {
		return fail(ErrInvalidPlan, path+".access", "public access policy is incomplete or widened")
	}
	for _, method := range access.AllowedMethods {
		if !containsExecutorBundleString([]string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}, method) {
			return fail(ErrInvalidPlan, path+".access.allowedMethods", "unsupported HTTP method")
		}
	}
	return nil
}

func validateBridgePublicationHealthProbe(probe rawPublicRouteHealthProbeV3, upstreamProtocol string, targetPort int, path string) error {
	if probe.Protocol != upstreamProtocol || probe.Port != targetPort || probe.TimeoutSeconds < 1 || probe.TimeoutSeconds > 300 {
		return fail(ErrInvalidPlan, path, "backend Health probe does not match the exact publication upstream")
	}
	switch probe.Kind {
	case "http":
		if probe.Protocol != "http" || probe.Method != "GET" || probe.FollowRedirects == nil || *probe.FollowRedirects ||
			!strings.HasPrefix(probe.Path, "/") || len(probe.ExpectedStatuses) == 0 {
			return fail(ErrInvalidPlan, path, "HTTP backend Health must be an address-free GET with redirects disabled")
		}
		seen := make(map[int]struct{}, len(probe.ExpectedStatuses))
		for index, status := range probe.ExpectedStatuses {
			if status < 100 || status > 599 {
				return fail(ErrInvalidPlan, fmt.Sprintf("%s.expectedStatuses[%d]", path, index), "status is outside 100..599")
			}
			if _, duplicate := seen[status]; duplicate {
				return fail(ErrDuplicate, path+".expectedStatuses", "status is duplicated")
			}
			seen[status] = struct{}{}
		}
	case "tcp":
		if probe.Protocol != "tcp" || probe.Method != "" || probe.FollowRedirects != nil || probe.Path != "" || len(probe.ExpectedStatuses) != 0 {
			return fail(ErrInvalidPlan, path, "TCP backend Health cannot carry HTTP authority")
		}
	default:
		return fail(ErrInvalidPlan, path+".kind", "unsupported publication backend Health kind")
	}
	return nil
}

func projectBridgePublicationForInstance(decoded executorContractPlan, unit RenderUnit, path string) (executorContractPlan, error) {
	plan, ok := decoded.(bridgePublicationExecutorPlan)
	if !ok {
		return nil, fail(ErrInvalidPlan, path+".planInputs", "publication decoder returned an unexpected plan type")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if !hasSite || !hasNode {
		return nil, fail(ErrInvalidPlan, path+".instances", "publication runtime requires one exact Cloud Site/node instance")
	}
	var target *executorBundleTarget
	for index := range plan.ModuleTargets {
		if plan.ModuleTargets[index].ID == nodeRef && plan.ModuleTargets[index].SiteRef == siteRef {
			if target != nil {
				return nil, fail(ErrDuplicate, path+".moduleTargets", "publication instance target is duplicated")
			}
			copy := plan.ModuleTargets[index]
			target = &copy
		}
	}
	if target == nil {
		return nil, fail(ErrInvalidPlan, path+".moduleTargets", "publication instance is outside compiler-owned targets")
	}
	publications := make([]bridgePublication, 0, len(plan.BridgePublications))
	for _, publication := range plan.BridgePublications {
		if publication.EdgeSiteRef == siteRef {
			publications = append(publications, publication)
		}
	}
	if len(publications) == 0 {
		return nil, fail(ErrInvalidPlan, path+".bridgePublications", "Cloud edge instance has no compiler-owned publication")
	}
	plan.ModuleTargets = []executorBundleTarget{*target}
	plan.BridgePublications = publications
	return plan, nil
}

func decodeBridgeOriginMTLSExecutorPlan(raw []byte, path string, spec executorContractBundleSpec) (executorContractPlan, error) {
	var plan bridgeOriginMTLSExecutorPlan
	if err := decodeStrict(raw, &plan); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact bridge origin mTLS handoff", err)
	}
	if err := validateExecutorContractPlanCommon(plan.StackID, plan.Kit, plan.Sites, plan.ModuleTargets, plan.ModuleCapabilities, plan.ControlPlane, spec, path); err != nil {
		return nil, err
	}
	if len(plan.BridgeOriginMTLS.Publications) == 0 {
		return nil, fail(ErrInvalidPlan, path+".bridgeOriginMTLS.publications", "requires at least one exact publication")
	}
	targetRefs := make([]string, len(plan.ModuleTargets))
	for index := range plan.ModuleTargets {
		targetRefs[index] = plan.ModuleTargets[index].ID
	}
	originRefs := map[string]struct{}{}
	previousService := ""
	for index, publication := range plan.BridgeOriginMTLS.Publications {
		itemPath := fmt.Sprintf("%s.bridgeOriginMTLS.publications[%d]", path, index)
		if previousService != "" && publication.ServiceRef <= previousService {
			return nil, fail(ErrDuplicate, itemPath+".serviceRef", "publications must be unique and sorted")
		}
		previousService = publication.ServiceRef
		if publication.IdentityRef != publication.ServiceRef+"-origin" || publication.SourceSiteRef == publication.EdgeSiteRef ||
			publication.ModuleRef == "" || publication.UnitRef == "" || !sortedUniqueNonEmpty(publication.OriginNodeRefs) ||
			!sortedUniqueNonEmpty(publication.OriginInstanceRefs) ||
			len(publication.OriginNodeRefs) != len(publication.OriginInstanceRefs) ||
			len(publication.OriginTargets) != len(publication.OriginNodeRefs) ||
			publication.TargetPort < 1 || publication.TargetPort > 65535 ||
			!containsExecutorBundleString([]string{"http", "https", "tcp"}, publication.UpstreamProtocol) {
			return nil, fail(ErrInvalidPlan, itemPath, "publication origin closure is incomplete or widened")
		}
		targetNodes := make([]string, 0, len(publication.OriginTargets))
		targetInstances := make([]string, 0, len(publication.OriginTargets))
		seenTargetPairs := make(map[string]struct{}, len(publication.OriginTargets))
		for targetIndex, target := range publication.OriginTargets {
			targetPath := fmt.Sprintf("%s.originTargets[%d]", itemPath, targetIndex)
			pair := target.NodeRef + "\x00" + target.InstanceRef
			if _, duplicate := seenTargetPairs[pair]; duplicate {
				return nil, fail(ErrDuplicate, targetPath, "origin target pair is duplicated")
			}
			seenTargetPairs[pair] = struct{}{}
			targetNodes = append(targetNodes, target.NodeRef)
			targetInstances = append(targetInstances, target.InstanceRef)
		}
		sort.Strings(targetNodes)
		sort.Strings(targetInstances)
		if !exactStringList(targetNodes, publication.OriginNodeRefs) || !exactStringList(targetInstances, publication.OriginInstanceRefs) {
			return nil, fail(ErrInvalidPlan, itemPath+".originTargets", "origin target pairs must exactly bind the governed node and instance sets")
		}
		for _, nodeRef := range publication.OriginNodeRefs {
			originRefs[nodeRef] = struct{}{}
		}
		transport := publication.Transport
		if transport.Mode != "mtls-origin-proxy" || transport.MinimumTLSVersion != "TLS1.3" ||
			transport.ServerName != publication.ServiceRef+".origin.stackkit.internal" ||
			!transport.OutboundOnly || transport.GeneralLANAccess {
			return nil, fail(ErrInvalidPlan, itemPath+".transport", "origin transport must be outbound-only TLS1.3 mTLS without LAN authority")
		}
		identity := publication.WorkloadIdentity
		if identity.CredentialIssuerRef != "home-workload-credential-issuer" ||
			!strings.HasPrefix(identity.Issuer, "urn:stackkit:") ||
			!strings.HasSuffix(identity.Audience, ":audience:stackkit-workload") ||
			!strings.HasSuffix(identity.VerificationKeySetRef, ":keyset:home-workload-verification-keys") ||
			!identity.ProofOfPossessionRequired || identity.CredentialTTLSeconds < 300 ||
			identity.CredentialTTLSeconds > 86400 || identity.RevocationMaxStalenessSeconds < 0 ||
			identity.RevocationMaxStalenessSeconds > identity.CredentialTTLSeconds {
			return nil, fail(ErrInvalidPlan, itemPath+".workloadIdentity", "origin must bind the exact possession-bound Home workload identity")
		}
		verifier := publication.EdgeVerifier
		if verifier.VerifierRef != "modern-cloud-workload-verifier" ||
			verifier.DistributionRef != "modern-workload-verifier-distribution" ||
			verifier.VerificationKeySetRef != identity.VerificationKeySetRef ||
			verifier.MaxStalenessSeconds != 300 || verifier.IncludesPrivateKeyMaterial ||
			verifier.IncludesSigningAuthority || verifier.ReverseAllowed {
			return nil, fail(ErrInvalidPlan, itemPath+".edgeVerifier", "Cloud edge verifier must receive only fresh one-way verification authority")
		}
	}
	originNodeRefs := make([]string, 0, len(originRefs))
	for nodeRef := range originRefs {
		originNodeRefs = append(originNodeRefs, nodeRef)
	}
	sort.Strings(originNodeRefs)
	for _, nodeRef := range originNodeRefs {
		if !containsExecutorBundleString(targetRefs, nodeRef) {
			return nil, fail(ErrInvalidPlan, path+".bridgeOriginMTLS.publications", "origin node is outside module targets")
		}
	}
	return plan, nil
}

func projectBridgeOriginMTLSForInstance(decoded executorContractPlan, unit RenderUnit, path string) (executorContractPlan, error) {
	plan, ok := decoded.(bridgeOriginMTLSExecutorPlan)
	if !ok {
		return nil, fail(ErrInvalidPlan, path+".planInputs", "origin mTLS decoder returned an unexpected plan type")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if !hasSite || !hasNode {
		return nil, fail(ErrInvalidPlan, path+".instances", "origin mTLS requires one exact Site/node instance")
	}
	originNodeSet := make(map[string]struct{}, len(plan.ModuleTargets))
	for _, publication := range plan.BridgeOriginMTLS.Publications {
		for _, target := range publication.OriginTargets {
			originNodeSet[target.NodeRef] = struct{}{}
		}
	}
	originNodes := make([]string, 0, len(originNodeSet))
	for nodeRef := range originNodeSet {
		originNodes = append(originNodes, nodeRef)
	}
	sort.Strings(originNodes)
	moduleTargetRefs := make([]string, len(plan.ModuleTargets))
	for index, target := range plan.ModuleTargets {
		moduleTargetRefs[index] = target.ID
	}
	if !exactStringList(originNodes, moduleTargetRefs) {
		return nil, fail(ErrInvalidPlan, path+".bridgeOriginMTLS.publications", "unprojected origin targets must exactly cover module targets")
	}
	targets := make([]executorBundleTarget, 0, 1)
	for _, target := range plan.ModuleTargets {
		if target.ID == nodeRef && target.SiteRef == siteRef {
			targets = append(targets, target)
		}
	}
	if len(targets) != 1 {
		return nil, fail(ErrInvalidPlan, path+".moduleTargets", "origin mTLS instance must match exactly one compiler-owned target")
	}
	publications := make([]bridgeOriginMTLSPublication, 0, len(plan.BridgeOriginMTLS.Publications))
	for index, publication := range plan.BridgeOriginMTLS.Publications {
		for _, target := range publication.OriginTargets {
			if target.NodeRef != nodeRef {
				continue
			}
			if publication.SourceSiteRef != siteRef {
				return nil, fail(ErrInvalidPlan, fmt.Sprintf("%s.bridgeOriginMTLS.publications[%d].sourceSiteRef", path, index), "origin publication Site does not match its exact instance")
			}
			projected := publication
			projected.OriginNodeRefs = []string{nodeRef}
			projected.OriginInstanceRefs = []string{target.InstanceRef}
			projected.OriginTargets = []bridgeOriginMTLSTarget{target}
			publications = append(publications, projected)
		}
	}
	if len(publications) == 0 {
		return nil, fail(ErrInvalidPlan, path+".bridgeOriginMTLS.publications", "origin mTLS instance has no compiler-owned publication")
	}
	plan.BridgeOriginMTLS.Publications = publications
	return plan, nil
}

type federationLinkExecutorDocument struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Module     struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	} `json:"module"`
	Contract struct {
		Apply                   string   `json:"apply"`
		Credentials             string   `json:"credentials"`
		EndpointDiscovery       string   `json:"endpointDiscovery"`
		FabricLifecycle         string   `json:"fabricLifecycle"`
		Generation              string   `json:"generation"`
		Operations              []string `json:"operations"`
		ProviderLifecycle       string   `json:"providerLifecycle"`
		RouteAuthority          string   `json:"routeAuthority"`
		RuntimeEnforcement      string   `json:"runtimeEnforcement"`
		Scope                   string   `json:"scope"`
		ServerProviderAuthority string   `json:"serverProviderAuthority"`
		TransportImplementation string   `json:"transportImplementation"`
	} `json:"contract"`
	PlanInputs json.RawMessage `json:"planInputs"`
}

// FederationLinkPolicy is the material-free, caller-bound projection for one
// authenticated Home or Cloud node. The opaque binding proves external fabric
// custody without exposing endpoints, credentials, keys, provider resources,
// transport implementation, or general LAN routes.
type FederationLinkPolicy struct {
	StackID       string
	SiteRef       string
	NodeRef       string
	SiteKind      string
	HomeSiteRefs  []string
	CloudSiteRefs []string
	Overlay       FederationLinkOverlayPolicy
	Partition     FederationLinkPartitionPolicy
	Binding       FederationLinkBindingPolicy
}

type FederationLinkOverlayPolicy struct {
	ContractRef             string
	Implementation          string
	Initiation              string
	OutboundEstablished     bool
	TrafficMode             string
	AdvertisePrivateSubnets bool
	AdvertiseDefaultRoute   bool
	AllowBroadRoutes        bool
	PeerSiteRefs            []string
}

type FederationLinkPartitionPolicy struct {
	OnCloudLoss                     string
	OnLinkLoss                      string
	CloudEdge                       string
	LocalIdentityAuthorityAvailable bool
	MaxStaleVerificationSeconds     int
	DenyNewCrossSiteSessions        bool
}

type FederationLinkBindingPolicy struct {
	BindingRef            string
	FabricRef             string
	CustodyAttestationRef string
	RequirementsHash      string
	BindingHash           string
	BridgeContractHash    string
	IssuedAt              string
	ValidUntil            string
}

// ValidateFederationLinkExecutorArtifact verifies the executable contract,
// exact compiler/custody hashes, caller-bound Site/node and binding freshness
// at one trusted UTC instant immediately before runtime mutation.
func ValidateFederationLinkExecutorArtifact(raw []byte, siteRef, nodeRef string, evaluatedAt time.Time) (FederationLinkPolicy, error) {
	var document federationLinkExecutorDocument
	if err := decodeStrict(raw, &document); err != nil {
		return FederationLinkPolicy{}, wrap(ErrInvalidPlan, "federationLinkArtifact", "decode exact federation-link artifact", err)
	}
	spec := executorContractBundleSpecs[8]
	if document.APIVersion != "stackkit.executor-contract-bundle/v1" || document.Kind != "ExecutorContractBundle" ||
		document.Module.ID != spec.moduleID || document.Module.Version != spec.moduleVersion ||
		document.Contract.Apply != "typed-local-operations" || document.Contract.Credentials != "external-owner" ||
		document.Contract.EndpointDiscovery != "external-owner" || document.Contract.FabricLifecycle != "not-owned" ||
		document.Contract.Generation != "supported" ||
		!exactStringList(document.Contract.Operations, []string{"establish-inter-site-link", "remove-inter-site-link", "verify-inter-site-link"}) ||
		document.Contract.ProviderLifecycle != "not-owned" || document.Contract.RouteAuthority != "compiler-owned-declared-flows-only" ||
		document.Contract.RuntimeEnforcement != "adapter-verified" || document.Contract.Scope != "federated-site-node" ||
		document.Contract.ServerProviderAuthority != "not-owned" || document.Contract.TransportImplementation != "external-owner" {
		return FederationLinkPolicy{}, fail(ErrInvalidPlan, "federationLinkArtifact.contract", "artifact widens or contradicts the typed federation-link authority")
	}
	decoded, err := decodeFederationRuntimeExecutorPlan(document.PlanInputs, "federationLinkArtifact.planInputs", spec)
	if err != nil {
		return FederationLinkPolicy{}, err
	}
	plan := decoded.(federationRuntimeExecutorPlan)
	if len(plan.ModuleTargets) != 1 || plan.ModuleTargets[0].ID != nodeRef || plan.ModuleTargets[0].SiteRef != siteRef {
		return FederationLinkPolicy{}, fail(ErrInvalidPlan, "federationLinkArtifact.planInputs.moduleTargets", "artifact must contain exactly the caller-bound federation Site/node")
	}
	siteKind := ""
	for _, site := range plan.Sites {
		if site.ID == siteRef {
			if siteKind != "" {
				return FederationLinkPolicy{}, fail(ErrDuplicate, "federationLinkArtifact.planInputs.sites", "caller-bound federation Site is duplicated")
			}
			siteKind = site.Kind
		}
	}
	if siteKind != "home" && siteKind != "cloud" {
		return FederationLinkPolicy{}, fail(ErrInvalidPlan, "federationLinkArtifact.planInputs.sites", "caller-bound federation Site is absent or unsupported")
	}
	var projection federationLinkPolicyProjection
	if err := decodeStrict(plan.FederationLinkPolicy, &projection); err != nil {
		return FederationLinkPolicy{}, wrap(ErrInvalidPlan, "federationLinkArtifact.planInputs.federationLinkPolicy", "decode exact link policy", err)
	}
	var requirements map[string]map[string]any
	if err := json.Unmarshal(plan.FederationLinkRequirements, &requirements); err != nil {
		return FederationLinkPolicy{}, wrap(ErrInvalidPlan, "federationLinkArtifact.planInputs.federationLinkRequirements", "decode exact requirement", err)
	}
	requirement := requirements[federationLinkCapability]
	var bindings map[string]map[string]any
	if err := json.Unmarshal(plan.ExternalFederationLinkBindings, &bindings); err != nil {
		return FederationLinkPolicy{}, wrap(ErrInvalidPlan, "federationLinkArtifact.planInputs.externalFederationLinkBindings", "decode exact binding", err)
	}
	binding, present := bindings[federationLinkCapability]
	if !present {
		return FederationLinkPolicy{}, fail(ErrInvalidPlan, "federationLinkArtifact.planInputs.externalFederationLinkBindings", "runtime requires one exact external federation-link binding")
	}
	bindingTimes := struct {
		IssuedAt   string `json:"issuedAt"`
		ValidUntil string `json:"validUntil"`
	}{}
	bindingRaw, err := json.Marshal(binding)
	if err != nil || json.Unmarshal(bindingRaw, &bindingTimes) != nil {
		return FederationLinkPolicy{}, fail(ErrInvalidPlan, "federationLinkArtifact.planInputs.externalFederationLinkBindings.inter-site-link", "binding timestamps are unreadable")
	}
	issuedAt, issuedErr := time.Parse(time.RFC3339Nano, bindingTimes.IssuedAt)
	validUntil, validErr := time.Parse(time.RFC3339Nano, bindingTimes.ValidUntil)
	evaluatedAt = evaluatedAt.UTC()
	if evaluatedAt.IsZero() || issuedErr != nil || validErr != nil || evaluatedAt.Before(issuedAt.UTC()) || !evaluatedAt.Before(validUntil.UTC()) {
		return FederationLinkPolicy{}, fail(ErrInvalidPlan, "federationLinkArtifact.planInputs.externalFederationLinkBindings.inter-site-link.validUntil", "binding is outside its runtime validity window")
	}
	get := func(body map[string]any, field string) string {
		value, _ := body[field].(string)
		return value
	}
	stringSlice := func(value any) []string {
		rawValues, _ := value.([]any)
		values := make([]string, len(rawValues))
		for index, rawValue := range rawValues {
			values[index], _ = rawValue.(string)
		}
		return values
	}
	return FederationLinkPolicy{
		StackID: plan.StackID, SiteRef: siteRef, NodeRef: nodeRef, SiteKind: siteKind,
		HomeSiteRefs:  append([]string(nil), stringSlice(requirement["homeSiteRefs"])...),
		CloudSiteRefs: append([]string(nil), stringSlice(requirement["cloudSiteRefs"])...),
		Overlay: FederationLinkOverlayPolicy{
			ContractRef: projection.Overlay.ContractRef, Implementation: projection.Overlay.Implementation,
			Initiation: projection.Overlay.Initiation, OutboundEstablished: projection.Overlay.OutboundEstablished,
			TrafficMode: projection.Overlay.TrafficMode, AdvertisePrivateSubnets: projection.Overlay.AdvertisePrivateSubnets,
			AdvertiseDefaultRoute: projection.Overlay.AdvertiseDefaultRoute, AllowBroadRoutes: projection.Overlay.AllowBroadRoutes,
			PeerSiteRefs: append([]string(nil), projection.Overlay.PeerSiteRefs...),
		},
		Partition: FederationLinkPartitionPolicy{
			OnCloudLoss: projection.Partition.OnCloudLoss, OnLinkLoss: projection.Partition.OnLinkLoss,
			CloudEdge: projection.Partition.CloudEdge, LocalIdentityAuthorityAvailable: projection.Partition.LocalIdentityAuthorityAvailable,
			MaxStaleVerificationSeconds: projection.Partition.MaxStaleVerificationSeconds,
			DenyNewCrossSiteSessions:    projection.Partition.DenyNewCrossSiteSessions,
		},
		Binding: FederationLinkBindingPolicy{
			BindingRef: get(binding, "bindingRef"), FabricRef: get(binding, "fabricRef"),
			CustodyAttestationRef: get(binding, "custodyAttestationRef"), RequirementsHash: get(binding, "requirementsHash"),
			BindingHash: get(binding, "bindingHash"), BridgeContractHash: get(binding, "bridgeContractHash"),
			IssuedAt: bindingTimes.IssuedAt, ValidUntil: bindingTimes.ValidUntil,
		},
	}, nil
}

type federationControlAgentExecutorDocument struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Module     struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	} `json:"module"`
	Contract struct {
		Apply                   string   `json:"apply"`
		Credentials             string   `json:"credentials"`
		Generation              string   `json:"generation"`
		InboundAuthority        string   `json:"inboundAuthority"`
		Operations              []string `json:"operations"`
		ProviderLifecycle       string   `json:"providerLifecycle"`
		RuntimeEnforcement      string   `json:"runtimeEnforcement"`
		Scope                   string   `json:"scope"`
		ServerProviderAuthority string   `json:"serverProviderAuthority"`
		TransportImplementation string   `json:"transportImplementation"`
	} `json:"contract"`
	PlanInputs json.RawMessage `json:"planInputs"`
}

// FederationControlAgentPolicy contains the exact, material-free policy for
// one Modern Site/node. Transport endpoints, credentials, tunnel mechanics
// and custody are intentionally absent.
type FederationControlAgentPolicy struct {
	StackID      string
	SiteRef      string
	NodeRef      string
	SiteKind     string
	ContractHash string
	Actions      []FederationControlAgentAction
	Partition    FederationControlAgentPartition
}

// ValidateFederationControlAgentExecutorArtifact verifies a complete
// node-local control-agent artifact. It does not discover a peer or transport:
// the caller is bound to exactly one compiler-selected Site/node.
func ValidateFederationControlAgentExecutorArtifact(raw []byte, siteRef, nodeRef string) (FederationControlAgentPolicy, error) {
	var document federationControlAgentExecutorDocument
	if err := decodeStrict(raw, &document); err != nil {
		return FederationControlAgentPolicy{}, wrap(ErrInvalidPlan, "federationControlAgentArtifact", "decode exact control-agent artifact", err)
	}
	spec := executorContractBundleSpecs[9]
	if document.APIVersion != "stackkit.executor-contract-bundle/v1" || document.Kind != "ExecutorContractBundle" ||
		document.Module.ID != spec.moduleID || document.Module.Version != spec.moduleVersion ||
		document.Contract.Apply != "typed-local-operations" || document.Contract.Credentials != "external-owner" ||
		document.Contract.Generation != "supported" || document.Contract.InboundAuthority != "forbidden" ||
		!exactStringList(document.Contract.Operations, []string{"bind-outbound-control-agent", "remove-outbound-control-agent", "verify-outbound-control-agent"}) ||
		document.Contract.ProviderLifecycle != "not-owned" || document.Contract.RuntimeEnforcement != "adapter-verified" ||
		document.Contract.Scope != "federated-site-node" || document.Contract.ServerProviderAuthority != "not-owned" ||
		document.Contract.TransportImplementation != "external-owner" {
		return FederationControlAgentPolicy{}, fail(ErrInvalidPlan, "federationControlAgentArtifact.contract", "artifact widens or contradicts the typed outbound control-agent authority")
	}
	decoded, err := decodeFederationRuntimeExecutorPlan(document.PlanInputs, "federationControlAgentArtifact.planInputs", spec)
	if err != nil {
		return FederationControlAgentPolicy{}, err
	}
	plan := decoded.(federationRuntimeExecutorPlan)
	if len(plan.ModuleTargets) != 1 || plan.ModuleTargets[0].ID != nodeRef || plan.ModuleTargets[0].SiteRef != siteRef {
		return FederationControlAgentPolicy{}, fail(ErrInvalidPlan, "federationControlAgentArtifact.planInputs.moduleTargets", "artifact must contain exactly the caller-bound federation Site/node")
	}
	siteKind := ""
	for _, site := range plan.Sites {
		if site.ID == siteRef {
			if siteKind != "" {
				return FederationControlAgentPolicy{}, fail(ErrDuplicate, "federationControlAgentArtifact.planInputs.sites", "caller-bound federation Site is duplicated")
			}
			siteKind = site.Kind
		}
	}
	if siteKind != "home" && siteKind != "cloud" {
		return FederationControlAgentPolicy{}, fail(ErrInvalidPlan, "federationControlAgentArtifact.planInputs.sites", "caller-bound federation Site is absent or unsupported")
	}
	var projection federationControlActionsProjection
	if err := decodeStrict(plan.FederationControlActions, &projection); err != nil {
		return FederationControlAgentPolicy{}, wrap(ErrInvalidPlan, "federationControlAgentArtifact.planInputs.federationControlActions", "decode exact control-action projection", err)
	}
	if err := validateFederationControlActions(projection, "federationControlAgentArtifact.planInputs.federationControlActions"); err != nil {
		return FederationControlAgentPolicy{}, err
	}
	return FederationControlAgentPolicy{
		StackID: plan.StackID, SiteRef: siteRef, NodeRef: nodeRef, SiteKind: siteKind,
		ContractHash: projection.ContractHash,
		Actions:      append([]FederationControlAgentAction(nil), projection.Actions...),
		Partition:    FederationControlAgentPartition(projection.Partition),
	}, nil
}

type bridgePublicationExecutorDocument struct {
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
		PublicationAuthority    string   `json:"publicationAuthority"`
		RuntimeEnforcement      string   `json:"runtimeEnforcement"`
		Scope                   string   `json:"scope"`
		ServerProviderAuthority string   `json:"serverProviderAuthority"`
		TransportImplementation string   `json:"transportImplementation"`
	} `json:"contract"`
	PlanInputs json.RawMessage `json:"planInputs"`
}

// BridgePublicationPolicy is the exact material-free policy for one Cloud
// edge node. DNS, certificates, credentials and transport remain external.
type BridgePublicationPolicy struct {
	StackID      string
	SiteRef      string
	NodeRef      string
	Publications []BridgePublicationRule
}

type BridgePublicationRule struct {
	ServiceRef             string
	SourceSiteRef          string
	EdgeSiteRef            string
	Host                   string
	Protocol               string
	Port                   int
	Path                   string
	TLSMinVersion          string
	AuthPolicyRef          string
	OriginIdentityRef      string
	RateLimitRequests      int
	RateLimitWindowSeconds int
	ModuleRef              string
	UnitRef                string
	OriginNodeRefs         []string
	OriginInstanceRefs     []string
	OriginTargets          []BridgePublicationOriginTarget
	UpstreamProtocol       string
	TargetPort             int
	HealthGateRef          string
	HealthProbe            *BridgePublicationHealthProbe
	DataBindingRef         string
	Authentication         string
	Privilege              string
	EnrolledDeviceRequired bool
	OwnerStepUpRequired    bool
	AllowedMethods         []string
}

type BridgePublicationOriginTarget struct {
	NodeRef     string
	InstanceRef string
}

type BridgePublicationHealthProbe struct {
	Kind             string
	Protocol         string
	Port             int
	TimeoutSeconds   int
	Method           string
	FollowRedirects  bool
	Path             string
	ExpectedStatuses []int
}

// ValidateBridgePublicationExecutorArtifact verifies the executable contract
// and returns only the caller-bound Cloud edge projection.
func ValidateBridgePublicationExecutorArtifact(raw []byte, siteRef, nodeRef string) (BridgePublicationPolicy, error) {
	var document bridgePublicationExecutorDocument
	if err := decodeStrict(raw, &document); err != nil {
		return BridgePublicationPolicy{}, wrap(ErrInvalidPlan, "bridgePublicationArtifact", "decode exact publication artifact", err)
	}
	spec := executorContractBundleSpecs[15]
	if document.APIVersion != "stackkit.executor-contract-bundle/v1" || document.Kind != "ExecutorContractBundle" ||
		document.Module.ID != spec.moduleID || document.Module.Version != spec.moduleVersion ||
		document.Contract.Apply != "typed-local-operations" || document.Contract.CertificateIssuance != "not-owned" ||
		document.Contract.Credentials != "not-included" || document.Contract.DNSMutation != "not-owned" ||
		document.Contract.Generation != "supported" ||
		!exactStringList(document.Contract.Operations, []string{"apply-service-publication", "remove-service-publication", "verify-service-publication"}) ||
		document.Contract.ProviderLifecycle != "not-owned" || document.Contract.PublicationAuthority != "compiler-owned-exact" ||
		document.Contract.RuntimeEnforcement != "adapter-verified" || document.Contract.Scope != "cloud-edge-node" ||
		document.Contract.ServerProviderAuthority != "not-owned" || document.Contract.TransportImplementation != "external-owner" {
		return BridgePublicationPolicy{}, fail(ErrInvalidPlan, "bridgePublicationArtifact.contract", "artifact widens or contradicts the typed publication authority")
	}
	decoded, err := decodeBridgePublicationExecutorPlan(document.PlanInputs, "bridgePublicationArtifact.planInputs", spec)
	if err != nil {
		return BridgePublicationPolicy{}, err
	}
	plan := decoded.(bridgePublicationExecutorPlan)
	if len(plan.ModuleTargets) != 1 || plan.ModuleTargets[0].ID != nodeRef || plan.ModuleTargets[0].SiteRef != siteRef {
		return BridgePublicationPolicy{}, fail(ErrInvalidPlan, "bridgePublicationArtifact.planInputs.moduleTargets", "artifact must contain exactly the caller-bound Cloud Site/node")
	}
	rules := make([]BridgePublicationRule, len(plan.BridgePublications))
	for index, publication := range plan.BridgePublications {
		if publication.EdgeSiteRef != siteRef {
			return BridgePublicationPolicy{}, fail(ErrInvalidPlan, fmt.Sprintf("bridgePublicationArtifact.planInputs.bridgePublications[%d].edgeSiteRef", index), "publication is outside caller-bound Cloud Site")
		}
		rules[index] = BridgePublicationRule{
			ServiceRef: publication.ServiceRef, SourceSiteRef: publication.SourceSiteRef, EdgeSiteRef: publication.EdgeSiteRef,
			Host: publication.Host, Protocol: publication.Protocol, Port: publication.Port, Path: publication.Path,
			TLSMinVersion: publication.TLS.MinVersion, AuthPolicyRef: publication.Auth.PolicyRef,
			OriginIdentityRef: publication.Origin.IdentityRef, RateLimitRequests: publication.RateLimit.Requests,
			RateLimitWindowSeconds: publication.RateLimit.WindowSeconds, ModuleRef: publication.ModuleRef,
			UnitRef: publication.UnitRef, OriginNodeRefs: append([]string(nil), publication.OriginNodeRefs...),
			OriginInstanceRefs: append([]string(nil), publication.OriginInstanceRefs...),
			OriginTargets: func() []BridgePublicationOriginTarget {
				targets := make([]BridgePublicationOriginTarget, len(publication.OriginTargets))
				for targetIndex, target := range publication.OriginTargets {
					targets[targetIndex] = BridgePublicationOriginTarget{NodeRef: target.NodeRef, InstanceRef: target.InstanceRef}
				}
				return targets
			}(),
			UpstreamProtocol: publication.UpstreamProtocol,
			TargetPort:       publication.TargetPort, HealthGateRef: publication.HealthGateRef, DataBindingRef: publication.DataBindingRef,
			HealthProbe: func() *BridgePublicationHealthProbe {
				if publication.HealthProbe == nil {
					return nil
				}
				return &BridgePublicationHealthProbe{
					Kind: publication.HealthProbe.Kind, Protocol: publication.HealthProbe.Protocol,
					Port: publication.HealthProbe.Port, TimeoutSeconds: publication.HealthProbe.TimeoutSeconds,
					Method: publication.HealthProbe.Method, FollowRedirects: publication.HealthProbe.FollowRedirects != nil && *publication.HealthProbe.FollowRedirects,
					Path: publication.HealthProbe.Path, ExpectedStatuses: append([]int(nil), publication.HealthProbe.ExpectedStatuses...),
				}
			}(),
			Authentication: publication.Access.Authentication, Privilege: publication.Access.Privilege,
			EnrolledDeviceRequired: publication.Access.EnrolledDeviceRequired,
			OwnerStepUpRequired:    publication.Access.OwnerStepUpRequired,
			AllowedMethods:         append([]string(nil), publication.Access.AllowedMethods...),
		}
	}
	return BridgePublicationPolicy{StackID: plan.StackID, SiteRef: siteRef, NodeRef: nodeRef, Publications: rules}, nil
}

type bridgeOriginMTLSExecutorDocument struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Module     struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	} `json:"module"`
	Contract struct {
		Apply                   string   `json:"apply"`
		Credentials             string   `json:"credentials"`
		Generation              string   `json:"generation"`
		Operations              []string `json:"operations"`
		ProviderLifecycle       string   `json:"providerLifecycle"`
		ReverseTrust            string   `json:"reverseTrust"`
		RuntimeEnforcement      string   `json:"runtimeEnforcement"`
		Scope                   string   `json:"scope"`
		ServerProviderAuthority string   `json:"serverProviderAuthority"`
		TransportImplementation string   `json:"transportImplementation"`
	} `json:"contract"`
	PlanInputs json.RawMessage `json:"planInputs"`
}

// BridgeOriginMTLSPolicy is the exact material-free policy for one Home origin
// node. Credential and transport implementations remain outside StackKits.
type BridgeOriginMTLSPolicy struct {
	StackID      string
	SiteRef      string
	NodeRef      string
	Publications []BridgeOriginMTLSPublicationPolicy
}

type BridgeOriginMTLSPublicationPolicy struct {
	ServiceRef                    string
	IdentityRef                   string
	EdgeSiteRef                   string
	ModuleRef                     string
	UnitRef                       string
	OriginInstanceRef             string
	UpstreamProtocol              string
	TargetPort                    int
	ServerName                    string
	MinimumTLSVersion             string
	CredentialIssuerRef           string
	Issuer                        string
	Audience                      string
	VerificationKeySetRef         string
	CredentialTTLSeconds          int
	RevocationMaxStalenessSeconds int
	EdgeVerifierRef               string
	VerifierDistributionRef       string
	VerifierMaxStalenessSeconds   int
}

// ValidateBridgeOriginMTLSExecutorArtifact verifies the exact executable
// contract and selects only the caller-bound Home node projection.
func ValidateBridgeOriginMTLSExecutorArtifact(raw []byte, siteRef, nodeRef string) (BridgeOriginMTLSPolicy, error) {
	var document bridgeOriginMTLSExecutorDocument
	if err := decodeStrict(raw, &document); err != nil {
		return BridgeOriginMTLSPolicy{}, wrap(ErrInvalidPlan, "bridgeOriginMTLSArtifact", "decode exact origin mTLS artifact", err)
	}
	spec := executorContractBundleSpecs[16]
	if document.APIVersion != "stackkit.executor-contract-bundle/v1" || document.Kind != "ExecutorContractBundle" ||
		document.Module.ID != spec.moduleID || document.Module.Version != spec.moduleVersion ||
		document.Contract.Apply != "typed-local-operations" || document.Contract.Credentials != "external-owner" ||
		document.Contract.Generation != "supported" ||
		!exactStringList(document.Contract.Operations, []string{"bind-origin-mtls-proxy", "remove-origin-mtls-proxy", "verify-origin-mtls"}) ||
		document.Contract.ProviderLifecycle != "not-owned" || document.Contract.ReverseTrust != "forbidden" ||
		document.Contract.RuntimeEnforcement != "adapter-verified" || document.Contract.Scope != "home-origin-node" ||
		document.Contract.ServerProviderAuthority != "not-owned" || document.Contract.TransportImplementation != "external-owner" {
		return BridgeOriginMTLSPolicy{}, fail(ErrInvalidPlan, "bridgeOriginMTLSArtifact.contract", "artifact widens or contradicts the typed origin mTLS authority")
	}
	decoded, err := decodeBridgeOriginMTLSExecutorPlan(document.PlanInputs, "bridgeOriginMTLSArtifact.planInputs", spec)
	if err != nil {
		return BridgeOriginMTLSPolicy{}, err
	}
	plan := decoded.(bridgeOriginMTLSExecutorPlan)
	matchingTargets := 0
	for _, target := range plan.ModuleTargets {
		if target.ID == nodeRef && target.SiteRef == siteRef {
			matchingTargets++
		}
	}
	if matchingTargets != 1 {
		return BridgeOriginMTLSPolicy{}, fail(ErrInvalidPlan, "bridgeOriginMTLSArtifact.planInputs.moduleTargets", "artifact topology must contain the caller-bound Home Site/node exactly once")
	}
	publications := make([]BridgeOriginMTLSPublicationPolicy, len(plan.BridgeOriginMTLS.Publications))
	for index, publication := range plan.BridgeOriginMTLS.Publications {
		if publication.SourceSiteRef != siteRef || len(publication.OriginNodeRefs) != 1 ||
			publication.OriginNodeRefs[0] != nodeRef || len(publication.OriginInstanceRefs) != 1 ||
			len(publication.OriginTargets) != 1 || publication.OriginTargets[0].NodeRef != nodeRef ||
			publication.OriginTargets[0].InstanceRef != publication.OriginInstanceRefs[0] {
			return BridgeOriginMTLSPolicy{}, fail(ErrInvalidPlan, fmt.Sprintf("bridgeOriginMTLSArtifact.planInputs.bridgeOriginMTLS.publications[%d]", index), "publication is not exact for the caller-bound Home node")
		}
		publications[index] = BridgeOriginMTLSPublicationPolicy{
			ServiceRef: publication.ServiceRef, IdentityRef: publication.IdentityRef,
			EdgeSiteRef: publication.EdgeSiteRef, ModuleRef: publication.ModuleRef, UnitRef: publication.UnitRef,
			OriginInstanceRef: publication.OriginInstanceRefs[0], UpstreamProtocol: publication.UpstreamProtocol,
			TargetPort: publication.TargetPort, ServerName: publication.Transport.ServerName,
			MinimumTLSVersion:   publication.Transport.MinimumTLSVersion,
			CredentialIssuerRef: publication.WorkloadIdentity.CredentialIssuerRef,
			Issuer:              publication.WorkloadIdentity.Issuer, Audience: publication.WorkloadIdentity.Audience,
			VerificationKeySetRef:         publication.WorkloadIdentity.VerificationKeySetRef,
			CredentialTTLSeconds:          publication.WorkloadIdentity.CredentialTTLSeconds,
			RevocationMaxStalenessSeconds: publication.WorkloadIdentity.RevocationMaxStalenessSeconds,
			EdgeVerifierRef:               publication.EdgeVerifier.VerifierRef,
			VerifierDistributionRef:       publication.EdgeVerifier.DistributionRef,
			VerifierMaxStalenessSeconds:   publication.EdgeVerifier.MaxStalenessSeconds,
		}
	}
	return BridgeOriginMTLSPolicy{StackID: plan.StackID, SiteRef: siteRef, NodeRef: nodeRef, Publications: publications}, nil
}

func decodeFederationRuntimeExecutorPlan(raw []byte, path string, spec executorContractBundleSpec) (executorContractPlan, error) {
	var plan federationRuntimeExecutorPlan
	if err := decodeStrict(raw, &plan); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact Federation executor contract", err)
	}
	if err := validateExecutorContractPlanCommon(plan.StackID, plan.Kit, plan.Sites, plan.ModuleTargets, plan.ModuleCapabilities, plan.ControlPlane, spec, path); err != nil {
		return nil, err
	}
	projections := map[string]json.RawMessage{
		federationLinkModuleID:          plan.FederationLinkPolicy,
		federationControlAgentModuleID:  plan.FederationControlActions,
		federationBackupModuleID:        plan.FederationBackupPolicy,
		federationObservabilityModuleID: plan.FederationObservability,
	}
	for moduleID, projection := range projections {
		if moduleID == spec.moduleID {
			if len(projection) == 0 {
				return nil, fail(ErrInvalidPlan, path, "Federation module %q lacks its exact typed projection", moduleID)
			}
			if err := validateTypedFederationProjection(moduleID, projection, plan.Sites, plan.ControlPlane, path); err != nil {
				return nil, err
			}
			continue
		}
		if len(projection) != 0 {
			return nil, fail(ErrInvalidPlan, path, "Federation module %q received authority owned by %q", spec.moduleID, moduleID)
		}
	}
	if spec.moduleID == federationLinkModuleID {
		if err := validateFederationLinkExecutorProjection(plan, path); err != nil {
			return nil, err
		}
	} else if len(plan.FederationLinkRequirements) != 0 || len(plan.ExternalFederationLinkBindings) != 0 {
		return nil, fail(ErrInvalidPlan, path, "non-link Federation module received the external link projection")
	}
	return plan, nil
}

func projectFederationLinkForInstance(decoded executorContractPlan, unit RenderUnit, path string) (executorContractPlan, error) {
	plan, ok := decoded.(federationRuntimeExecutorPlan)
	if !ok {
		return nil, fail(ErrInvalidPlan, path+".planInputs", "federation-link decoder returned an unexpected plan type")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if !hasSite || !hasNode {
		return nil, fail(ErrInvalidPlan, path+".instances", "federation-link runtime requires one exact Site/node instance")
	}
	var target *executorBundleTarget
	for index := range plan.ModuleTargets {
		if plan.ModuleTargets[index].ID == nodeRef && plan.ModuleTargets[index].SiteRef == siteRef {
			if target != nil {
				return nil, fail(ErrDuplicate, path+".moduleTargets", "federation-link instance target is duplicated")
			}
			copy := plan.ModuleTargets[index]
			target = &copy
		}
	}
	if target == nil {
		return nil, fail(ErrInvalidPlan, path+".moduleTargets", "federation-link instance is outside compiler-owned targets")
	}
	plan.ModuleTargets = []executorBundleTarget{*target}
	return plan, nil
}

func validateFederationLinkExecutorProjection(plan federationRuntimeExecutorPlan, path string) error {
	if len(plan.FederationLinkRequirements) == 0 || len(plan.ExternalFederationLinkBindings) == 0 {
		return fail(ErrInvalidPlan, path, "Federation link executor requires both requirement and external binding projections")
	}
	var requirements map[string]map[string]any
	if err := json.Unmarshal(plan.FederationLinkRequirements, &requirements); err != nil {
		return wrap(ErrInvalidPlan, path+".federationLinkRequirements", "decode closed federation link requirements", err)
	}
	if len(requirements) != 1 {
		return fail(ErrInvalidPlan, path+".federationLinkRequirements", "must contain exactly the inter-site-link requirement")
	}
	requirement, ok := requirements[federationLinkCapability]
	if !ok {
		return fail(ErrInvalidPlan, path+".federationLinkRequirements", "must contain the inter-site-link requirement")
	}
	allowedRequirement := map[string]struct{}{
		"apiVersion": {}, "kind": {}, "stackId": {}, "capabilityRef": {}, "contractOwnerRef": {}, "capabilityContractHash": {},
		"homeSiteRefs": {}, "cloudSiteRefs": {}, "targetNodes": {}, "bridgeContractHash": {}, "policy": {}, "specHash": {}, "requirementsHash": {},
	}
	for key := range requirement {
		if _, allowed := allowedRequirement[key]; !allowed {
			return fail(ErrInvalidPlan, path+".federationLinkRequirements.inter-site-link."+key, "field is outside the closed federation link requirement")
		}
	}
	if requirement["apiVersion"] != "stackkit.federation-link-requirement/v1" || requirement["kind"] != "FederationLinkRequirement" || requirement["stackId"] != plan.StackID || requirement["capabilityRef"] != federationLinkCapability {
		return fail(ErrInvalidPlan, path+".federationLinkRequirements.inter-site-link", "identity does not match the exact executor contract")
	}
	capabilityContractHash := ""
	for _, capability := range plan.ModuleCapabilities {
		if capability.ID == federationLinkCapability {
			capabilityContractHash = capability.ContractHash
			break
		}
	}
	if requirement["contractOwnerRef"] != "stackkits-federation-link" || requirement["capabilityContractHash"] != capabilityContractHash {
		return fail(ErrInvalidPlan, path+".federationLinkRequirements.inter-site-link.contractOwnerRef", "requirement is outside the selected federation-link authority")
	}
	wantHash, err := resolvedplan.ComputeFederationLinkRequirementHash(resolvedplan.FederationLinkRequirement(requirement))
	if err != nil || requirement["requirementsHash"] != wantHash {
		return fail(ErrInvalidPlan, path+".federationLinkRequirements.inter-site-link.requirementsHash", "does not match the canonical requirement body")
	}
	wantHome, wantCloud := []string{}, []string{}
	for _, site := range plan.Sites {
		if site.Kind == "home" {
			wantHome = append(wantHome, site.ID)
		}
		if site.Kind == "cloud" {
			wantCloud = append(wantCloud, site.ID)
		}
	}
	wantTargets := make([]map[string]any, 0, len(plan.ModuleTargets))
	for _, target := range plan.ModuleTargets {
		wantTargets = append(wantTargets, map[string]any{"siteRef": target.SiteRef, "nodeRef": target.ID})
	}
	for field, want := range map[string]any{"homeSiteRefs": wantHome, "cloudSiteRefs": wantCloud} {
		haveJSON, _ := json.Marshal(requirement[field])
		wantJSON, _ := json.Marshal(want)
		if !bytes.Equal(haveJSON, wantJSON) {
			return fail(ErrInvalidPlan, path+".federationLinkRequirements.inter-site-link."+field, "widens or mismatches the exact module target scope")
		}
	}
	var linkPolicy federationLinkPolicyProjection
	if err := decodeStrict(plan.FederationLinkPolicy, &linkPolicy); err != nil {
		return wrap(ErrInvalidPlan, path+".federationLinkPolicy", "decode exact federation link policy", err)
	}
	wantPolicy := map[string]any{
		"defaultDeny": true, "initiation": "home-outbound", "trafficMode": linkPolicy.Overlay.TrafficMode,
		"routeScope": "declared-flows-only", "allowDefaultRoute": false, "allowBroadLAN": false,
		"credentialCustody": "external", "fabricLifecycle": "external",
	}
	havePolicyJSON, _ := json.Marshal(requirement["policy"])
	wantPolicyJSON, _ := json.Marshal(wantPolicy)
	if !bytes.Equal(havePolicyJSON, wantPolicyJSON) {
		return fail(ErrInvalidPlan, path+".federationLinkRequirements.inter-site-link.policy", "widens or contradicts the exact federation link policy")
	}
	requirementTargets, ok := requirement["targetNodes"].([]any)
	if !ok || len(requirementTargets) == 0 {
		return fail(ErrInvalidPlan, path+".federationLinkRequirements.inter-site-link.targetNodes", "must carry the compiler-owned Site/node target pairs")
	}
	requirementTargetIDs := make(map[string]struct{}, len(requirementTargets))
	for index, rawTarget := range requirementTargets {
		target, ok := rawTarget.(map[string]any)
		nodeRef, nodeOK := target["nodeRef"].(string)
		siteRef, siteOK := target["siteRef"].(string)
		if !ok || !nodeOK || !siteOK || siteRef == "" || nodeRef == "" {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s.federationLinkRequirements.inter-site-link.targetNodes[%d]", path, index), "must be an exact Site/node pair")
		}
		if _, duplicate := requirementTargetIDs[nodeRef]; duplicate {
			return fail(ErrDuplicate, fmt.Sprintf("%s.federationLinkRequirements.inter-site-link.targetNodes[%d].nodeRef", path, index), "federation target node is duplicated")
		}
		requirementTargetIDs[nodeRef] = struct{}{}
	}
	for index, member := range plan.ControlPlane.Members {
		if _, exists := requirementTargetIDs[member]; !exists {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s.controlPlane.members[%d]", path, index), "control member is outside the exact federation target closure")
		}
	}
	if len(plan.ModuleTargets) > 1 {
		haveJSON, _ := json.Marshal(requirementTargets)
		wantJSON, _ := json.Marshal(wantTargets)
		if !bytes.Equal(haveJSON, wantJSON) {
			return fail(ErrInvalidPlan, path+".federationLinkRequirements.inter-site-link.targetNodes", "widens or mismatches the exact module target scope")
		}
	} else {
		localJSON, _ := json.Marshal(wantTargets[0])
		localMatches := 0
		for _, rawTarget := range requirementTargets {
			targetJSON, _ := json.Marshal(rawTarget)
			if bytes.Equal(targetJSON, localJSON) {
				localMatches++
			}
		}
		if localMatches != 1 {
			return fail(ErrInvalidPlan, path+".federationLinkRequirements.inter-site-link.targetNodes", "does not contain the exact node-local instance target")
		}
	}
	var bindings map[string]map[string]any
	if err := json.Unmarshal(plan.ExternalFederationLinkBindings, &bindings); err != nil {
		return wrap(ErrInvalidPlan, path+".externalFederationLinkBindings", "decode closed external federation link bindings", err)
	}
	if len(bindings) == 0 {
		return nil
	}
	if len(bindings) != 1 {
		return fail(ErrInvalidPlan, path+".externalFederationLinkBindings", "must contain only the exact inter-site-link binding")
	}
	binding, ok := bindings[federationLinkCapability]
	if !ok {
		return fail(ErrInvalidPlan, path+".externalFederationLinkBindings", "contains no exact inter-site-link binding")
	}
	allowedBinding := map[string]struct{}{
		"apiVersion": {}, "kind": {}, "bindingRef": {}, "fabricRef": {}, "custodyAttestationRef": {}, "stackId": {}, "capabilityRef": {},
		"contractOwnerRef": {}, "capabilityContractHash": {}, "homeSiteRefs": {}, "cloudSiteRefs": {}, "targetNodes": {}, "bridgeContractHash": {},
		"requirementsHash": {}, "stackkitsVersion": {}, "candidateDigest": {}, "specHash": {}, "issuedAt": {}, "validUntil": {}, "bindingHash": {},
	}
	for key := range binding {
		if _, allowed := allowedBinding[key]; !allowed {
			return fail(ErrInvalidPlan, path+".externalFederationLinkBindings.inter-site-link."+key, "field is outside the closed external federation link binding")
		}
	}
	for _, field := range []string{"stackId", "capabilityRef", "contractOwnerRef", "capabilityContractHash", "homeSiteRefs", "cloudSiteRefs", "targetNodes", "bridgeContractHash", "requirementsHash", "specHash"} {
		haveJSON, _ := json.Marshal(binding[field])
		wantJSON, _ := json.Marshal(requirement[field])
		if !bytes.Equal(haveJSON, wantJSON) {
			return fail(ErrInvalidPlan, path+".externalFederationLinkBindings.inter-site-link."+field, "does not exactly match the provider-free requirement")
		}
	}
	wantBindingHash, err := resolvedplan.ComputeExternalFederationLinkBindingHash(resolvedplan.ExternalFederationLinkBinding(binding))
	if err != nil || binding["bindingHash"] != wantBindingHash {
		return fail(ErrInvalidPlan, path+".externalFederationLinkBindings.inter-site-link.bindingHash", "does not match the canonical binding body")
	}
	if err := resolvedplan.ValidateExternalFederationLinkBinding(
		resolvedplan.ExternalFederationLinkBinding(binding),
		resolvedplan.FederationLinkRequirement(requirement),
	); err != nil {
		return wrap(ErrInvalidPlan, path+".externalFederationLinkBindings.inter-site-link", "binding violates the closed external custody contract", err)
	}
	return nil
}
