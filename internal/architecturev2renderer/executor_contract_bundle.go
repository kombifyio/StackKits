package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	executorContractBundleUnitID          = "executor-contract"
	executorContractBundleRendererRef     = "stackkit"
	executorContractBundleRendererVersion = "1.0.0"
	executorContractBundleToken           = "@@PLAN_INPUTS@@"

	coreRuntimeModuleID      = "stackkits-core-runtime"
	coreRuntimeModuleVersion = "2.0.0"
	coreRuntimeTemplateRef   = "builtin://foundation/runtime/executor-contract/v1.json"
	coreRuntimeOutputRef     = "foundation/runtime/executor-contract.json"

	localRuntimeModuleID      = "stackkits-local-runtime"
	localRuntimeModuleVersion = "2.0.0"
	localRuntimeTemplateRef   = "builtin://local/runtime/executor-contract/v1.json"
	localRuntimeOutputRef     = "local/runtime/executor-contract.json"

	homePrivateRemoteAccessModuleID       = "stackkits-home-private-remote-access-runtime"
	homePrivateRemoteAccessTemplateRef    = "builtin://home/remote-access/executor-contract/v1.json"
	homePrivateRemoteAccessOutputRef      = "home/remote-access/executor-contract.json"
	homePublicPublishEgressModuleID       = "stackkits-home-public-publish-egress-runtime"
	homePublicPublishEgressTemplateRef    = "builtin://home/publication/executor-contract/v1.json"
	homePublicPublishEgressOutputRef      = "home/publication/executor-contract.json"
	homeEncryptedOffsiteBackupModuleID    = "stackkits-home-encrypted-offsite-backup-runtime"
	homeEncryptedOffsiteBackupTemplateRef = "builtin://home/backup/offsite-executor-contract/v1.json"
	homeEncryptedOffsiteBackupOutputRef   = "home/backup/offsite-executor-contract.json"
	homeBackupCapability                  = "encrypted-offsite-backup"
	homeExtensionRuntimeModuleVersion     = "1.0.0"

	cloudPrivateAdminMeshModuleID      = "stackkits-cloud-private-admin-mesh-runtime"
	cloudPrivateAdminMeshModuleVersion = "1.0.0"
	cloudPrivateAdminMeshTemplateRef   = "builtin://cloud/admin-mesh/executor-contract/v1.json"
	cloudPrivateAdminMeshOutputRef     = "cloud/admin-mesh/executor-contract.json"

	basementComposeRuntimeModuleID      = "stackkits-basement-compose-runtime"
	basementComposeRuntimeModuleVersion = "1.0.0"
	basementComposeRuntimeTemplateRef   = "builtin://basement/runtime/executor-contract/v1.json"
	basementComposeRuntimeOutputRef     = "basement/runtime/executor-contract.json"

	modernHomeRuntimeModuleID      = "stackkits-modern-home-runtime"
	modernHomeRuntimeModuleVersion = "1.0.0"
	modernHomeRuntimeTemplateRef   = "builtin://modern/home-runtime/executor-contract/v1.json"
	modernHomeRuntimeOutputRef     = "modern/home-runtime/executor-contract.json"

	cloudHostSecurityModuleID      = "stackkits-cloud-host-security-runtime"
	cloudHostSecurityModuleVersion = "1.0.0"
	cloudHostSecurityTemplateRef   = "builtin://cloud/host-security/executor-contract/v1.json"
	cloudHostSecurityOutputRef     = "cloud/host-security/executor-contract.json"

	cloudPublicEdgeModuleID      = "stackkits-cloud-public-edge-runtime"
	cloudPublicEdgeModuleVersion = "1.0.0"
	cloudPublicEdgeTemplateRef   = "builtin://cloud/public-edge/executor-contract/v1.json"
	cloudPublicEdgeOutputRef     = "cloud/public-edge/executor-contract.json"

	cloudOffsiteBackupModuleID      = "stackkits-cloud-offsite-backup-runtime"
	cloudOffsiteBackupModuleVersion = "1.0.0"
	cloudOffsiteBackupTemplateRef   = "builtin://cloud/backup/executor-contract/v1.json"
	cloudOffsiteBackupOutputRef     = "cloud/backup/executor-contract.json"
	cloudBackupTargetCapability     = "offsite-object-backup"

	federationLinkModuleID             = "stackkits-federation-link-runtime"
	federationLinkModuleVersion        = "1.1.0"
	federationControlAgentModuleID     = "stackkits-federation-control-agent-runtime"
	federationBackupModuleID           = "stackkits-federation-backup-runtime"
	federationObservabilityModuleID    = "stackkits-federation-observability-runtime"
	federationRuntimeModuleVersion     = "1.0.0"
	federationLinkTemplateRef          = "builtin://modern/federation/link/executor-contract/v1.json"
	federationControlTemplateRef       = "builtin://modern/federation/control-agent/executor-contract/v1.json"
	federationBackupTemplateRef        = "builtin://modern/federation/backup/executor-contract/v1.json"
	federationObservabilityTemplateRef = "builtin://modern/federation/observability/executor-contract/v1.json"
	federationLinkOutputRef            = "modern/federation/link/executor-contract.json"
	federationLinkCapability           = "inter-site-link"
	federationControlOutputRef         = "modern/federation/control-agent/executor-contract.json"
	federationBackupOutputRef          = "modern/federation/backup/executor-contract.json"
	federationObservabilityOutputRef   = "modern/federation/observability/executor-contract.json"
	bridgePublicationModuleID          = "stackkits-bridge-publication-runtime"
	bridgePublicationModuleVersion     = "1.1.0"
	bridgePublicationTemplateRef       = "builtin://modern/federation/publication/executor-contract/v1.json"
	bridgePublicationOutputRef         = "modern/federation/publication/executor-contract.json"
	bridgeOriginMTLSModuleID           = "stackkits-bridge-origin-mtls-runtime"
	bridgeOriginMTLSModuleVersion      = "1.1.0"
	bridgeOriginMTLSTemplateRef        = "builtin://modern/federation/origin-mtls/executor-contract/v1.json"
	bridgeOriginMTLSOutputRef          = "modern/federation/origin-mtls/executor-contract.json"
)

const executorContractBundleContract = `"contract":{"apply":"not-implemented","credentials":"not-included","generation":"supported","providerLifecycle":"not-owned","runtimeEnforcement":"unverified","scope":"generation-only","serverProviderAuthority":"not-owned"}`
const federationLinkExecutorContract = `"contract":{"apply":"typed-local-operations","credentials":"external-owner","endpointDiscovery":"external-owner","fabricLifecycle":"not-owned","generation":"supported","operations":["establish-inter-site-link","remove-inter-site-link","verify-inter-site-link"],"providerLifecycle":"not-owned","routeAuthority":"compiler-owned-declared-flows-only","runtimeEnforcement":"adapter-verified","scope":"federated-site-node","serverProviderAuthority":"not-owned","transportImplementation":"external-owner"}`
const cloudPublicEdgeExecutorContract = `"contract":{"apply":"typed-local-operations","certificateIssuance":"not-owned","credentials":"not-included","dnsMutation":"not-owned","generation":"supported","operations":["apply-public-edge","remove-obsolete-public-edge","verify-public-edge"],"providerLifecycle":"not-owned","routeAuthority":"compiler-owned-exact","runtimeEnforcement":"adapter-verified","scope":"cloud-edge-node","serverProviderAuthority":"not-owned"}`
const bridgePublicationExecutorContract = `"contract":{"apply":"typed-local-operations","certificateIssuance":"not-owned","credentials":"not-included","dnsMutation":"not-owned","generation":"supported","operations":["apply-service-publication","remove-service-publication","verify-service-publication"],"providerLifecycle":"not-owned","publicationAuthority":"compiler-owned-exact","runtimeEnforcement":"adapter-verified","scope":"cloud-edge-node","serverProviderAuthority":"not-owned","transportImplementation":"external-owner"}`
const bridgeOriginMTLSExecutorContract = `"contract":{"apply":"typed-local-operations","credentials":"external-owner","generation":"supported","operations":["bind-origin-mtls-proxy","remove-origin-mtls-proxy","verify-origin-mtls"],"providerLifecycle":"not-owned","reverseTrust":"forbidden","runtimeEnforcement":"adapter-verified","scope":"home-origin-node","serverProviderAuthority":"not-owned","transportImplementation":"external-owner"}`

var executorContractBundleSpecs = []executorContractBundleSpec{
	{
		moduleID: coreRuntimeModuleID, moduleVersion: coreRuntimeModuleVersion,
		templateRef: coreRuntimeTemplateRef, outputRef: coreRuntimeOutputRef,
		planInputRefs: []string{"controlPlane", "data", "failurePolicy", "hostRuntimePolicy", "identity", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId", "storagePolicy"},
		allowedKits:   []string{"basement-kit", "cloud-kit", "modern-homelab"},
		allowedCapabilities: []string{
			"backup-core", "lifecycle-update", "observability-evidence", "secrets-recovery",
		},
		requiredCapabilities: []string{
			"backup-core", "lifecycle-update", "observability-evidence", "secrets-recovery",
		},
		decodePlan: decodeCoreRuntimeExecutorPlan,
	},
	{
		moduleID: localRuntimeModuleID, moduleVersion: localRuntimeModuleVersion,
		templateRef: localRuntimeTemplateRef, outputRef: localRuntimeOutputRef,
		planInputRefs: []string{"controlPlane", "data", "failurePolicy", "kit", "localNetworkPolicy", "localReachability", "moduleCapabilities", "moduleTargets", "sites", "stackId", "storagePolicy"},
		allowedKits:   []string{"basement-kit", "modern-homelab"}, siteKind: "home",
		allowedCapabilities: []string{
			"encrypted-offsite-backup", "lan-dns",
			"private-remote-access", "public-publish-egress",
		},
		requiredCapabilities: []string{},
		decodePlan:           decodeLocalRuntimeExecutorPlan,
	},
	{
		moduleID: cloudPrivateAdminMeshModuleID, moduleVersion: cloudPrivateAdminMeshModuleVersion,
		templateRef: cloudPrivateAdminMeshTemplateRef, outputRef: cloudPrivateAdminMeshOutputRef,
		planInputRefs: []string{"cloudAdminMesh", "controlPlane", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId"},
		allowedKits:   []string{"cloud-kit", "modern-homelab"}, siteKind: "cloud",
		allowedCapabilities:  []string{"private-admin-mesh"},
		requiredCapabilities: []string{"private-admin-mesh"},
		decodePlan:           decodeCloudRuntimeExecutorPlan,
	},
	{
		moduleID: basementComposeRuntimeModuleID, moduleVersion: basementComposeRuntimeModuleVersion,
		templateRef: basementComposeRuntimeTemplateRef, outputRef: basementComposeRuntimeOutputRef,
		planInputRefs: []string{"controlPlane", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId"},
		allowedKits:   []string{"basement-kit"}, siteKind: "home",
		allowedCapabilities:  []string{"basement-compose-runtime"},
		requiredCapabilities: []string{"basement-compose-runtime"},
		decodePlan:           decodeBasementComposeExecutorPlan,
	},
	{
		moduleID: modernHomeRuntimeModuleID, moduleVersion: modernHomeRuntimeModuleVersion,
		templateRef: modernHomeRuntimeTemplateRef, outputRef: modernHomeRuntimeOutputRef,
		planInputRefs: []string{"controlPlane", "data", "failurePolicy", "kit", "localNetworkPolicy", "localReachability", "moduleCapabilities", "moduleTargets", "sites", "stackId", "storagePolicy"},
		allowedKits:   []string{"modern-homelab"}, siteKind: "home",
		allowedCapabilities:  []string{"modern-home-workload-runtime"},
		requiredCapabilities: []string{"modern-home-workload-runtime"},
		decodePlan:           decodeLocalRuntimeExecutorPlan,
	},
	{
		moduleID: cloudHostSecurityModuleID, moduleVersion: cloudHostSecurityModuleVersion,
		templateRef: cloudHostSecurityTemplateRef, outputRef: cloudHostSecurityOutputRef,
		planInputRefs: []string{"controlPlane", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId"},
		allowedKits:   []string{"cloud-kit", "modern-homelab"}, siteKind: "cloud",
		allowedCapabilities:  []string{"host-local-internet-firewall", "internet-host-hardening"},
		requiredCapabilities: []string{"host-local-internet-firewall", "internet-host-hardening"},
		decodePlan:           decodeCloudRuntimeExecutorPlan,
	},
	{
		moduleID: cloudPublicEdgeModuleID, moduleVersion: cloudPublicEdgeModuleVersion,
		templateRef: cloudPublicEdgeTemplateRef, outputRef: cloudPublicEdgeOutputRef,
		contractJSON:  cloudPublicEdgeExecutorContract,
		planInputRefs: []string{"controlPlane", "kit", "moduleCapabilities", "moduleTargets", "publicEdge", "sites", "stackId"},
		allowedKits:   []string{"cloud-kit", "modern-homelab"}, siteKind: "cloud",
		allowedCapabilities:  []string{"public-edge"},
		requiredCapabilities: []string{"public-edge"},
		decodePlan:           decodeCloudRuntimeExecutorPlan,
	},
	{
		moduleID: cloudOffsiteBackupModuleID, moduleVersion: cloudOffsiteBackupModuleVersion,
		templateRef: cloudOffsiteBackupTemplateRef, outputRef: cloudOffsiteBackupOutputRef,
		planInputRefs: []string{"cloudOffsiteBackup", "controlPlane", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId"},
		allowedKits:   []string{"cloud-kit", "modern-homelab"}, siteKind: "cloud",
		allowedCapabilities:  []string{"offsite-object-backup"},
		requiredCapabilities: []string{"offsite-object-backup"},
		decodePlan:           decodeCloudRuntimeExecutorPlan,
	},
	{
		moduleID: federationLinkModuleID, moduleVersion: federationLinkModuleVersion,
		templateRef: federationLinkTemplateRef, outputRef: federationLinkOutputRef,
		contractJSON:        federationLinkExecutorContract,
		planInputRefs:       []string{"controlPlane", "externalFederationLinkBindings", "federationLinkPolicy", "federationLinkRequirements", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId"},
		allowedKits:         []string{"modern-homelab"},
		allowedCapabilities: []string{"inter-site-link"}, requiredCapabilities: []string{"inter-site-link"},
		decodePlan: decodeFederationRuntimeExecutorPlan,
	},
	{
		moduleID: federationControlAgentModuleID, moduleVersion: federationRuntimeModuleVersion,
		templateRef: federationControlTemplateRef, outputRef: federationControlOutputRef,
		planInputRefs:       []string{"controlPlane", "federationControlActions", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId"},
		allowedKits:         []string{"modern-homelab"},
		allowedCapabilities: []string{"outbound-control-agent"}, requiredCapabilities: []string{"outbound-control-agent"},
		decodePlan: decodeFederationRuntimeExecutorPlan,
	},
	{
		moduleID: federationBackupModuleID, moduleVersion: federationRuntimeModuleVersion,
		templateRef: federationBackupTemplateRef, outputRef: federationBackupOutputRef,
		planInputRefs:       []string{"controlPlane", "federationBackupPolicy", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId"},
		allowedKits:         []string{"modern-homelab"},
		allowedCapabilities: []string{"cross-site-backup"}, requiredCapabilities: []string{"cross-site-backup"},
		decodePlan: decodeFederationRuntimeExecutorPlan,
	},
	{
		moduleID: federationObservabilityModuleID, moduleVersion: federationRuntimeModuleVersion,
		templateRef: federationObservabilityTemplateRef, outputRef: federationObservabilityOutputRef,
		planInputRefs:       []string{"controlPlane", "federationObservability", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId"},
		allowedKits:         []string{"modern-homelab"},
		allowedCapabilities: []string{"bridge-observability"}, requiredCapabilities: []string{"bridge-observability"},
		decodePlan: decodeFederationRuntimeExecutorPlan,
	},
	{
		moduleID: homePrivateRemoteAccessModuleID, moduleVersion: homeExtensionRuntimeModuleVersion,
		templateRef: homePrivateRemoteAccessTemplateRef, outputRef: homePrivateRemoteAccessOutputRef,
		planInputRefs: []string{"controlPlane", "homeAccessHandoff", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId"},
		allowedKits:   []string{"basement-kit", "modern-homelab"}, siteKind: "home",
		allowedCapabilities: []string{"private-remote-access"}, requiredCapabilities: []string{"private-remote-access"},
		decodePlan: decodeHomeAccessExecutorPlan,
	},
	{
		moduleID: homePublicPublishEgressModuleID, moduleVersion: homeExtensionRuntimeModuleVersion,
		templateRef: homePublicPublishEgressTemplateRef, outputRef: homePublicPublishEgressOutputRef,
		planInputRefs: []string{"controlPlane", "homeAccessHandoff", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId"},
		allowedKits:   []string{"basement-kit"}, siteKind: "home",
		allowedCapabilities: []string{"public-publish-egress"}, requiredCapabilities: []string{"public-publish-egress"},
		decodePlan: decodeHomeAccessExecutorPlan,
	},
	{
		moduleID: homeEncryptedOffsiteBackupModuleID, moduleVersion: homeExtensionRuntimeModuleVersion,
		templateRef: homeEncryptedOffsiteBackupTemplateRef, outputRef: homeEncryptedOffsiteBackupOutputRef,
		planInputRefs: []string{"controlPlane", "homeOffsiteBackup", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId"},
		allowedKits:   []string{"basement-kit"}, siteKind: "home",
		allowedCapabilities: []string{"encrypted-offsite-backup"}, requiredCapabilities: []string{"encrypted-offsite-backup"},
		decodePlan: decodeLocalRuntimeExecutorPlan,
	},
	{
		moduleID: bridgePublicationModuleID, moduleVersion: bridgePublicationModuleVersion,
		templateRef: bridgePublicationTemplateRef, outputRef: bridgePublicationOutputRef,
		contractJSON:  bridgePublicationExecutorContract,
		planInputRefs: []string{"bridgePublications", "controlPlane", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId"},
		allowedKits:   []string{"modern-homelab"}, siteKind: "cloud",
		allowedCapabilities: []string{"service-publication"}, requiredCapabilities: []string{"service-publication"},
		decodePlan: decodeBridgePublicationExecutorPlan,
	},
	{
		moduleID: bridgeOriginMTLSModuleID, moduleVersion: bridgeOriginMTLSModuleVersion,
		templateRef: bridgeOriginMTLSTemplateRef, outputRef: bridgeOriginMTLSOutputRef,
		contractJSON:  bridgeOriginMTLSExecutorContract,
		planInputRefs: []string{"bridgeOriginMTLS", "controlPlane", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId"},
		allowedKits:   []string{"modern-homelab"}, siteKind: "home",
		allowedCapabilities: []string{"service-publication"}, requiredCapabilities: []string{"service-publication"},
		decodePlan: decodeBridgeOriginMTLSExecutorPlan,
	},
}

type executorContractBundleSpec struct {
	moduleID             string
	moduleVersion        string
	templateRef          string
	outputRef            string
	contractJSON         string
	planInputRefs        []string
	allowedKits          []string
	siteKind             string
	allowedCapabilities  []string
	requiredCapabilities []string
	decodePlan           func([]byte, string, executorContractBundleSpec) (executorContractPlan, error)
}

type executorContractPlan interface {
	executorContractPlanMarker()
}

type executorContractBundleRenderer struct {
	spec     executorContractBundleSpec
	template []byte
	contract RendererContract
}

// CoreRuntimeExecutorBundleRendererContract returns the exact immutable
// renderer identity for the shared generation-only executor handoff.
func CoreRuntimeExecutorBundleRendererContract() RendererContract {
	return newExecutorContractBundleRenderer(executorContractBundleSpecs[0]).contract
}

// LocalRuntimeExecutorBundleRendererContract returns the exact immutable
// renderer identity for the Home-local generation-only executor handoff.
func LocalRuntimeExecutorBundleRendererContract() RendererContract {
	return newExecutorContractBundleRenderer(executorContractBundleSpecs[1]).contract
}

// CloudPrivateAdminMeshExecutorBundleRendererContract returns the exact
// generation-only private admin-mesh handoff identity.
func CloudPrivateAdminMeshExecutorBundleRendererContract() RendererContract {
	return newExecutorContractBundleRenderer(executorContractBundleSpecs[2]).contract
}

// BasementComposeRuntimeExecutorBundleRendererContract returns the exact
// generation-only Basement Compose handoff identity.
func BasementComposeRuntimeExecutorBundleRendererContract() RendererContract {
	return newExecutorContractBundleRenderer(executorContractBundleSpecs[3]).contract
}

// ModernHomeRuntimeExecutorBundleRendererContract returns the retired Modern
// Home umbrella identity for isolated migration diagnostics only. Product v2
// plans cannot select or render it; concrete workload modules own execution.
func ModernHomeRuntimeExecutorBundleRendererContract() RendererContract {
	return newExecutorContractBundleRenderer(executorContractBundleSpecs[4]).contract
}

// CloudHostSecurityExecutorBundleRendererContract returns the exact typed,
// provider-free Cloud host-security handoff identity.
func CloudHostSecurityExecutorBundleRendererContract() RendererContract {
	return newCloudHostSecurityRenderer().contract
}

// CloudPublicEdgeExecutorBundleRendererContract returns the exact
// generation-only Cloud public-edge handoff identity.
func CloudPublicEdgeExecutorBundleRendererContract() RendererContract {
	return newExecutorContractBundleRenderer(executorContractBundleSpecs[6]).contract
}

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
		!exactStringList(document.Contract.Operations, []string{"apply-public-edge", "remove-obsolete-public-edge", "verify-public-edge"}) ||
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

// CloudOffsiteBackupExecutorBundleRendererContract returns the exact
// generation-only Cloud offsite-backup target handoff identity.
func CloudOffsiteBackupExecutorBundleRendererContract() RendererContract {
	return newExecutorContractBundleRenderer(executorContractBundleSpecs[7]).contract
}

func FederationLinkExecutorBundleRendererContract() RendererContract {
	return newExecutorContractBundleRenderer(executorContractBundleSpecs[8]).contract
}

func FederationControlAgentExecutorBundleRendererContract() RendererContract {
	return newExecutorContractBundleRenderer(executorContractBundleSpecs[9]).contract
}

func FederationBackupExecutorBundleRendererContract() RendererContract {
	return newExecutorContractBundleRenderer(executorContractBundleSpecs[10]).contract
}

func FederationObservabilityExecutorBundleRendererContract() RendererContract {
	return newExecutorContractBundleRenderer(executorContractBundleSpecs[11]).contract
}

func HomePrivateRemoteAccessExecutorBundleRendererContract() RendererContract {
	return newExecutorContractBundleRenderer(executorContractBundleSpecs[12]).contract
}

func HomePublicPublishEgressExecutorBundleRendererContract() RendererContract {
	return newExecutorContractBundleRenderer(executorContractBundleSpecs[13]).contract
}

func HomeEncryptedOffsiteBackupExecutorBundleRendererContract() RendererContract {
	return newExecutorContractBundleRenderer(executorContractBundleSpecs[14]).contract
}

func BridgePublicationExecutorBundleRendererContract() RendererContract {
	return newExecutorContractBundleRenderer(executorContractBundleSpecs[15]).contract
}

func BridgeOriginMTLSExecutorBundleRendererContract() RendererContract {
	return newExecutorContractBundleRenderer(executorContractBundleSpecs[16]).contract
}

func registerExecutorContractBundleRenderers(registry *Registry) error {
	for _, spec := range executorContractBundleSpecs {
		// The shared Core runtime umbrella is no longer a catalog authority.
		// Keep its decoder temporarily for isolated migration diagnostics, but
		// never expose it through the product renderer registry.
		if spec.moduleID == coreRuntimeModuleID || spec.moduleID == localRuntimeModuleID || spec.moduleID == modernHomeRuntimeModuleID || spec.moduleID == cloudHostSecurityModuleID {
			continue
		}
		renderer := newExecutorContractBundleRenderer(spec)
		if err := registry.Register(renderer.contract, renderer); err != nil {
			return err
		}
	}
	cloudHostSecurity := newCloudHostSecurityRenderer()
	if err := registry.Register(cloudHostSecurity.contract, cloudHostSecurity); err != nil {
		return err
	}
	return nil
}

func newExecutorContractBundleRenderer(spec executorContractBundleSpec) executorContractBundleRenderer {
	contractJSON := spec.contractJSON
	if contractJSON == "" {
		contractJSON = executorContractBundleContract
	}
	template := []byte(fmt.Sprintf(
		`{"apiVersion":"stackkit.executor-contract-bundle/v1","kind":"ExecutorContractBundle","module":{"id":%q,"version":%q},%s,"planInputs":%s}`+"\n",
		spec.moduleID, spec.moduleVersion, contractJSON, executorContractBundleToken,
	))
	sum := sha256.Sum256(template)
	return executorContractBundleRenderer{
		spec: spec, template: template,
		contract: RendererContract{
			Kind: "native-config", RendererRef: executorContractBundleRendererRef,
			TemplateRef: spec.templateRef, Version: executorContractBundleRendererVersion,
			ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
		},
	}
}

func (r executorContractBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := "resolvedPlan.modules." + r.spec.moduleID + ".renderUnits." + executorContractBundleUnitID
	if err := validateExecutorContractBundleUnit(unit, r, path); err != nil {
		return nil, err
	}
	plan, err := r.spec.decodePlan(unit.PlanInputsJSON(), path+".planInputs", r.spec)
	if err != nil {
		return nil, err
	}
	if r.spec.moduleID == bridgePublicationModuleID {
		plan, err = projectBridgePublicationForInstance(plan, unit, path)
		if err != nil {
			return nil, err
		}
	}
	if r.spec.moduleID == bridgeOriginMTLSModuleID {
		plan, err = projectBridgeOriginMTLSForInstance(plan, unit, path)
		if err != nil {
			return nil, err
		}
	}
	if r.spec.moduleID == federationLinkModuleID {
		plan, err = projectFederationLinkForInstance(plan, unit, path)
		if err != nil {
			return nil, err
		}
	}
	canonical, err := json.Marshal(plan)
	if err != nil {
		return nil, wrap(ErrRendererFailure, path+".planInputs", "marshal typed executor contract", err)
	}
	if err := rejectExecutorContractProjectionLeaks(canonical, path+".planInputs"); err != nil {
		return nil, err
	}
	if executorContractBundleTemplateHash(r.template) != r.contract.ContractHash || bytes.Count(r.template, []byte(executorContractBundleToken)) != 1 {
		return nil, fail(ErrOutputChanged, path+".template", "embedded executor contract does not match its registered contract")
	}
	output := bytes.Replace(r.template, []byte(executorContractBundleToken), canonical, 1)
	return []UnitOutput{{Ref: r.spec.outputRef, Bytes: output}}, nil
}

func executorContractBundleTemplateHash(template []byte) string {
	sum := sha256.Sum256(template)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func rejectExecutorContractProjectionLeaks(raw []byte, path string) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return wrap(ErrInvalidPlan, path, "scan executor contract inputs", err)
	}
	forbiddenKeys := map[string]struct{}{
		"accountref": {}, "addresses": {}, "credentialref": {}, "credentialrefs": {},
		"daemonsocketpath": {}, "inventoryfacts": {}, "managementaddress": {}, "managementcidrs": {},
		"nodes": {}, "providerlifecycle": {}, "providerref": {}, "secretrefs": {},
		"servicecidrs": {}, "socketpath": {}, "storagecidrs": {},
	}
	var walk func(any, string) error
	walk = func(current any, currentPath string) error {
		switch typed := current.(type) {
		case map[string]any:
			for key, nested := range typed {
				lowerKey := strings.ToLower(key)
				if _, forbidden := forbiddenKeys[lowerKey]; forbidden {
					return fail(ErrInvalidPlan, currentPath+"."+key, "field is outside the closed executor contract projection")
				}
				if lowerKey == "management" && !strings.HasSuffix(currentPath, ".hostRuntimePolicy.install.platform") {
					return fail(ErrInvalidPlan, currentPath+"."+key, "management authority is outside the closed executor contract projection")
				}
				if err := walk(nested, currentPath+"."+key); err != nil {
					return err
				}
			}
		case []any:
			for index, nested := range typed {
				if err := walk(nested, fmt.Sprintf("%s[%d]", currentPath, index)); err != nil {
					return err
				}
			}
		case string:
			lower := strings.ToLower(typed)
			if validSecretReference(lower) || strings.Contains(lower, ".sock") && strings.Contains(lower, "/") {
				return fail(ErrInvalidPlan, currentPath, "secret references and daemon socket paths are forbidden from executor contract artifacts")
			}
		}
		return nil
	}
	return walk(value, path)
}

//nolint:gocyclo // The exact unit boundary intentionally checks every authority class in one place.
func validateExecutorContractBundleUnit(unit RenderUnit, renderer executorContractBundleRenderer, path string) error {
	contract := renderer.contract
	if unit.ModuleID() != renderer.spec.moduleID || unit.ID() != executorContractBundleUnitID {
		return fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", renderer.spec.moduleID, executorContractBundleUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered executor contract")
	}
	wantRuntimeKind := "host"
	if renderer.spec.moduleID == bridgePublicationModuleID || renderer.spec.moduleID == bridgeOriginMTLSModuleID {
		wantRuntimeKind = "native"
	}
	if unit.RuntimeKind() != wantRuntimeKind || unit.RuntimeDelivery() != "stackkit" {
		return fail(ErrInvalidPlan, path+".instances", "executor contract requires exact %s/stackkit ownership", wantRuntimeKind)
	}
	nodeLocal := renderer.spec.moduleID == cloudHostSecurityModuleID || renderer.spec.moduleID == federationLinkModuleID || renderer.spec.moduleID == bridgePublicationModuleID || renderer.spec.moduleID == bridgeOriginMTLSModuleID
	if nodeLocal {
		siteRef, hasSite := unit.SiteRef()
		nodeRef, hasNode := unit.NodeRef()
		if unit.InstanceScope() != "node-local" || !hasSite || !hasNode || unit.InstanceID() != executorContractBundleUnitID+"-node-"+nodeRef ||
			!stringListContains(unit.LogicalSiteRefs(), siteRef) || !stringListContains(unit.LogicalNodeRefs(), nodeRef) {
			return fail(ErrInvalidPlan, path+".instances", "node-local executor contract requires one exact governed Site/node instance")
		}
	} else {
		if unit.InstanceScope() != "module" || unit.InstanceID() != executorContractBundleUnitID+"-logical" {
			return fail(ErrInvalidPlan, path+".instances", "executor contract requires exact module-single ownership")
		}
		if _, present := unit.SiteRef(); present {
			return fail(ErrInvalidPlan, path+".instances", "module-scoped executor contract must not receive a site binding")
		}
		if _, present := unit.NodeRef(); present {
			return fail(ErrInvalidPlan, path+".instances", "module-scoped executor contract must not receive a node binding")
		}
	}
	if _, present := unit.DaemonRef(); present {
		return fail(ErrInvalidPlan, path+".instances", "executor contract must not receive daemon authority")
	}
	if _, present := unit.DaemonInstanceRef(); present {
		return fail(ErrInvalidPlan, path+".instances", "executor contract must not receive daemon instance authority")
	}
	if _, present := unit.DaemonEngine(); present {
		return fail(ErrInvalidPlan, path+".instances", "executor contract must not receive daemon engine authority")
	}
	if _, present := unit.DaemonSocketPath(); present {
		return fail(ErrInvalidPlan, path+".instances", "executor contract must not receive daemon socket authority")
	}
	if len(unit.PublicInputRefs()) != 0 || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.ValuesJSON()) || !emptyJSONObject(unit.SecretRefsJSON()) {
		return fail(ErrInvalidPlan, path+".inputs", "executor contract accepts only its closed compiler-owned projection")
	}
	if !exactStringList(unit.PlanInputRefs(), renderer.spec.planInputRefs) {
		return fail(ErrInvalidPlan, path+".planInputRefs", "must exactly match the registered executor contract projection")
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) || !emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return fail(ErrInvalidPlan, path+".interfaces", "generation-only executor contract must not receive service, network, interface, approval, or socket authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	wantScope, wantCardinality := "module", "single"
	if nodeLocal {
		wantScope, wantCardinality = "node-local", "one-per-node"
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != wantScope || placement.Cardinality != wantCardinality {
		return fail(ErrInvalidPlan, path+".placement", "executor contract requires exact %s/%s placement", wantScope, wantCardinality)
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != renderer.spec.outputRef {
		return fail(ErrInvalidPlan, path+".outputs", "executor contract requires exactly output %q", renderer.spec.outputRef)
	}
	return nil
}

type executorBundleKit struct {
	Slug           string `json:"slug"`
	Version        string `json:"version"`
	DefinitionHash string `json:"definitionHash"`
}

type executorBundleSite struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	FailureDomain string `json:"failureDomain"`
}

type executorBundleControlPlane struct {
	Mode             string   `json:"mode"`
	AuthoritySiteRef string   `json:"authoritySiteRef"`
	Members          []string `json:"members"`
}

type executorBundleCapability struct {
	ID           string `json:"id"`
	ContractHash string `json:"contractHash"`
}

type executorBundleHardware struct {
	Arch           string `json:"arch"`
	Profile        string `json:"profile"`
	Virtualization string `json:"virtualization,omitempty"`
	CPUCores       int    `json:"cpuCores,omitempty"`
	RAMGB          int    `json:"ramGB,omitempty"`
	StorageGB      int    `json:"storageGB,omitempty"`
}

type executorBundleTarget struct {
	ID               string                 `json:"id"`
	SiteRef          string                 `json:"siteRef"`
	Roles            []string               `json:"roles"`
	FailureDomain    string                 `json:"failureDomain"`
	DeclaredHardware executorBundleHardware `json:"declaredHardware"`
}

type executorBundleSetupPolicy struct {
	Platform           string `json:"platform"`
	ApplicationDefault string `json:"applicationDefault"`
}

type executorBundlePlatform struct {
	Management      string                    `json:"management"`
	FallbackAllowed bool                      `json:"fallbackAllowed"`
	SetupPolicy     executorBundleSetupPolicy `json:"setupPolicy"`
}

type executorBundleInstallPolicy struct {
	Mode     string                 `json:"mode"`
	Runtime  string                 `json:"runtime"`
	Platform executorBundlePlatform `json:"platform"`
}

type executorBundleHostPolicy struct {
	Timezone           string `json:"timezone"`
	Locale             string `json:"locale"`
	Swap               string `json:"swap"`
	SwapSizeMB         int    `json:"swapSizeMB,omitempty"`
	UnattendedUpgrades string `json:"unattendedUpgrades"`
}

type executorBundleContainerPolicy struct {
	Engine        string `json:"engine"`
	Rootless      bool   `json:"rootless"`
	LiveRestore   bool   `json:"liveRestore"`
	StorageDriver string `json:"storageDriver,omitempty"`
	DataRoot      string `json:"dataRoot"`
	LogDriver     string `json:"logDriver"`
}

type executorBundleHostRuntimePolicy struct {
	Install   executorBundleInstallPolicy    `json:"install"`
	Host      executorBundleHostPolicy       `json:"host"`
	Container *executorBundleContainerPolicy `json:"container,omitempty"`
}

type executorBundleExternalStorage struct {
	DeviceRef  string `json:"deviceRef,omitempty"`
	MountPoint string `json:"mountPoint"`
}

type executorBundleNFSStorage struct {
	Server string `json:"server"`
	Path   string `json:"path"`
}

type executorBundleStoragePolicy struct {
	DataRoot     string                         `json:"dataRoot"`
	BackupRoot   string                         `json:"backupRoot"`
	StacksRoot   string                         `json:"stacksRoot"`
	MediaRoot    string                         `json:"mediaRoot,omitempty"`
	VolumeDriver string                         `json:"volumeDriver"`
	External     *executorBundleExternalStorage `json:"external,omitempty"`
	NFS          *executorBundleNFSStorage      `json:"nfs,omitempty"`
}

type executorBundleDomainPolicy struct {
	Base            string `json:"base"`
	SubdomainPrefix string `json:"subdomainPrefix,omitempty"`
}

type executorBundleTransportPolicy struct {
	Subnet  string `json:"subnet"`
	Gateway string `json:"gateway,omitempty"`
	MTU     int    `json:"mtu"`
	IPv6    bool   `json:"ipv6"`
}

type executorBundleDNSPolicy struct {
	Servers       []string `json:"servers"`
	SearchDomains []string `json:"searchDomains"`
}

type executorBundleTLSPolicy struct {
	DefaultMode string `json:"defaultMode"`
	MinVersion  string `json:"minVersion"`
}

type executorBundleNetworkPolicy struct {
	Mode      string                        `json:"mode"`
	Domain    executorBundleDomainPolicy    `json:"domain"`
	Transport executorBundleTransportPolicy `json:"transport"`
	DNS       executorBundleDNSPolicy       `json:"dns"`
	TLS       executorBundleTLSPolicy       `json:"tls"`
}

type executorBundleIdentity struct {
	HumanAuthoritySiteRef   string                          `json:"humanAuthoritySiteRef"`
	DeviceAuthoritySiteRef  string                          `json:"deviceAuthoritySiteRef,omitempty"`
	EdgeVerifierSiteRefs    []string                        `json:"edgeVerifierSiteRefs,omitempty"`
	DeviceEnrollment        *executorBundleDeviceEnrollment `json:"deviceEnrollment,omitempty"`
	PossessionBoundSessions bool                            `json:"possessionBoundSessions"`
	LANLocationIsIdentity   bool                            `json:"lanLocationIsIdentity"`
}

type executorBundleDeviceEnrollment struct {
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

type executorBundleCloudCopyPolicy struct {
	PolicyRef      string   `json:"policyRef"`
	AllowedClasses []string `json:"allowedClasses"`
	AllowPrimary   bool     `json:"allowPrimary"`
	AllowReplicas  bool     `json:"allowReplicas"`
}

type executorBundleDataBinding struct {
	Classes          []string                       `json:"classes"`
	PrimarySiteRef   string                         `json:"primarySiteRef"`
	ReplicaSiteRefs  []string                       `json:"replicaSiteRefs,omitempty"`
	CloudCopyAllowed bool                           `json:"cloudCopyAllowed"`
	CloudCopyPolicy  *executorBundleCloudCopyPolicy `json:"cloudCopyPolicy,omitempty"`
}

type executorBundleData struct {
	DefaultAuthority string                               `json:"defaultAuthority"`
	Bindings         map[string]executorBundleDataBinding `json:"bindings,omitempty"`
}

type executorBundleFailurePolicy struct {
	OnCloudLoss                     string `json:"onCloudLoss"`
	OnLinkLoss                      string `json:"onLinkLoss"`
	CloudEdge                       string `json:"cloudEdge"`
	LocalIdentityAuthorityAvailable bool   `json:"localIdentityAuthorityAvailable"`
	MaxStaleVerificationSeconds     int    `json:"maxStaleVerificationSeconds"`
	DenyNewCrossSiteSessions        bool   `json:"denyNewCrossSiteSessions"`
}

type coreRuntimeExecutorPlan struct {
	StackID            string                          `json:"stackId"`
	Kit                executorBundleKit               `json:"kit"`
	Sites              []executorBundleSite            `json:"sites"`
	ModuleTargets      []executorBundleTarget          `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability      `json:"moduleCapabilities"`
	ControlPlane       executorBundleControlPlane      `json:"controlPlane"`
	HostRuntimePolicy  executorBundleHostRuntimePolicy `json:"hostRuntimePolicy"`
	StoragePolicy      executorBundleStoragePolicy     `json:"storagePolicy"`
	Identity           executorBundleIdentity          `json:"identity"`
	Data               executorBundleData              `json:"data"`
	FailurePolicy      executorBundleFailurePolicy     `json:"failurePolicy"`
}

func (coreRuntimeExecutorPlan) executorContractPlanMarker() {}

type localRuntimeExecutorPlan struct {
	StackID                          string                      `json:"stackId"`
	Kit                              executorBundleKit           `json:"kit"`
	Sites                            []executorBundleSite        `json:"sites"`
	ModuleTargets                    []executorBundleTarget      `json:"moduleTargets"`
	ModuleCapabilities               []executorBundleCapability  `json:"moduleCapabilities"`
	ControlPlane                     executorBundleControlPlane  `json:"controlPlane"`
	StoragePolicy                    executorBundleStoragePolicy `json:"storagePolicy"`
	LocalNetworkPolicy               executorBundleNetworkPolicy `json:"localNetworkPolicy"`
	Data                             executorBundleData          `json:"data"`
	FailurePolicy                    executorBundleFailurePolicy `json:"failurePolicy"`
	LocalReachability                homeLocalReachability       `json:"localReachability"`
	HomeAccessRequirements           json.RawMessage             `json:"homeAccessRequirements,omitempty"`
	ExternalHomeAccessBindings       json.RawMessage             `json:"externalHomeAccessBindings,omitempty"`
	HomeBackupTargetRequirements     json.RawMessage             `json:"homeBackupTargetRequirements,omitempty"`
	ExternalHomeBackupTargetBindings json.RawMessage             `json:"externalHomeBackupTargetBindings,omitempty"`
}

func (localRuntimeExecutorPlan) executorContractPlanMarker() {}

type homeOffsiteBackupProjection struct {
	Requirements json.RawMessage `json:"requirements"`
	Bindings     json.RawMessage `json:"bindings"`
}

type homeAccessHandoffProjection struct {
	Requirements json.RawMessage `json:"requirements"`
	Bindings     json.RawMessage `json:"bindings"`
}

type homeAccessExecutorPlan struct {
	StackID            string                      `json:"stackId"`
	Kit                executorBundleKit           `json:"kit"`
	Sites              []executorBundleSite        `json:"sites"`
	ModuleTargets      []executorBundleTarget      `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability  `json:"moduleCapabilities"`
	ControlPlane       executorBundleControlPlane  `json:"controlPlane"`
	HomeAccessHandoff  homeAccessHandoffProjection `json:"homeAccessHandoff"`
}

func (homeAccessExecutorPlan) executorContractPlanMarker() {}

type basementComposeExecutorPlan struct {
	StackID            string                     `json:"stackId"`
	Kit                executorBundleKit          `json:"kit"`
	Sites              []executorBundleSite       `json:"sites"`
	ModuleTargets      []executorBundleTarget     `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability `json:"moduleCapabilities"`
	ControlPlane       executorBundleControlPlane `json:"controlPlane"`
}

func (basementComposeExecutorPlan) executorContractPlanMarker() {}

type homeOffsiteBackupExecutorPlan struct {
	StackID            string                      `json:"stackId"`
	Kit                executorBundleKit           `json:"kit"`
	Sites              []executorBundleSite        `json:"sites"`
	ModuleTargets      []executorBundleTarget      `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability  `json:"moduleCapabilities"`
	ControlPlane       executorBundleControlPlane  `json:"controlPlane"`
	HomeOffsiteBackup  homeOffsiteBackupProjection `json:"homeOffsiteBackup"`
}

func (homeOffsiteBackupExecutorPlan) executorContractPlanMarker() {}

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
	ServiceRef         string                     `json:"serviceRef"`
	SourceSiteRef      string                     `json:"sourceSiteRef"`
	EdgeSiteRef        string                     `json:"edgeSiteRef"`
	Host               string                     `json:"host"`
	Protocol           string                     `json:"protocol"`
	Port               int                        `json:"port"`
	Path               string                     `json:"path"`
	DefaultClosed      bool                       `json:"defaultClosed"`
	TLS                bridgePublicationTLS       `json:"tls"`
	Auth               bridgePublicationAuth      `json:"auth"`
	Origin             bridgePublicationOrigin    `json:"origin"`
	RateLimit          bridgePublicationRateLimit `json:"rateLimit"`
	ModuleRef          string                     `json:"moduleRef"`
	UnitRef            string                     `json:"unitRef"`
	OriginNodeRefs     []string                   `json:"originNodeRefs"`
	OriginInstanceRefs []string                   `json:"originInstanceRefs"`
	OriginTargets      []bridgeOriginMTLSTarget   `json:"originTargets"`
	UpstreamProtocol   string                     `json:"upstreamProtocol"`
	TargetPort         int                        `json:"targetPort"`
	HealthGateRef      string                     `json:"healthGateRef"`
	DataBindingRef     string                     `json:"dataBindingRef,omitempty"`
	Access             bridgePublicationAccess    `json:"access"`
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

func decodeCoreRuntimeExecutorPlan(raw []byte, path string, spec executorContractBundleSpec) (executorContractPlan, error) {
	var plan coreRuntimeExecutorPlan
	if err := decodeStrict(raw, &plan); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact Core executor contract", err)
	}
	if err := validateExecutorContractPlanCommon(plan.StackID, plan.Kit, plan.Sites, plan.ModuleTargets, plan.ModuleCapabilities, plan.ControlPlane, spec, path); err != nil {
		return nil, err
	}
	if err := validateHostRuntimePolicy(plan.HostRuntimePolicy, path+".hostRuntimePolicy"); err != nil {
		return nil, err
	}
	if err := validateStoragePolicy(plan.StoragePolicy, path+".storagePolicy"); err != nil {
		return nil, err
	}
	if err := validateExecutorBundleIdentity(plan.Identity, plan.Sites, path+".identity"); err != nil {
		return nil, err
	}
	if err := validateExecutorBundleData(plan.Data, plan.Sites, path+".data"); err != nil {
		return nil, err
	}
	if err := validateExecutorBundleFailurePolicy(plan.FailurePolicy, path+".failurePolicy"); err != nil {
		return nil, err
	}
	return plan, nil
}

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
	previousService := ""
	for index, publication := range plan.BridgePublications {
		itemPath := fmt.Sprintf("%s.bridgePublications[%d]", path, index)
		if err := validateBridgePublication(publication, itemPath); err != nil {
			return nil, err
		}
		if previousService != "" && publication.ServiceRef <= previousService {
			return nil, fail(ErrDuplicate, itemPath+".serviceRef", "publications must be unique and sorted")
		}
		previousService = publication.ServiceRef
	}
	return plan, nil
}

func validateBridgePublication(publication bridgePublication, path string) error {
	if err := requireContractID(publication.ServiceRef, path+".serviceRef"); err != nil {
		return err
	}
	if publication.SourceSiteRef == publication.EdgeSiteRef || publication.Host == "" ||
		publication.Protocol != "https" || publication.Port != 443 || !strings.HasPrefix(publication.Path, "/") ||
		!publication.DefaultClosed || !publication.TLS.Required || publication.TLS.Mode != "terminate-at-edge" ||
		!containsExecutorBundleString([]string{"TLS1.2", "TLS1.3"}, publication.TLS.MinVersion) ||
		!publication.Auth.Required || publication.Auth.PolicyRef == "" ||
		publication.Origin.IdentityRef != publication.ServiceRef+"-origin" || !publication.Origin.MTLSRequired ||
		!publication.RateLimit.Enabled || publication.RateLimit.Requests < 1 || publication.RateLimit.WindowSeconds < 1 ||
		publication.ModuleRef == "" || publication.UnitRef == "" ||
		!sortedUniqueNonEmpty(publication.OriginNodeRefs) || !sortedUniqueNonEmpty(publication.OriginInstanceRefs) ||
		len(publication.OriginNodeRefs) != len(publication.OriginInstanceRefs) ||
		len(publication.OriginTargets) != len(publication.OriginNodeRefs) ||
		!containsExecutorBundleString([]string{"http", "https", "tcp"}, publication.UpstreamProtocol) ||
		publication.TargetPort < 1 || publication.TargetPort > 65535 || publication.HealthGateRef == "" {
		return fail(ErrInvalidPlan, path, "service publication closure is incomplete or widened")
	}
	targetNodes := make([]string, 0, len(publication.OriginTargets))
	targetInstances := make([]string, 0, len(publication.OriginTargets))
	seenTargetPairs := make(map[string]struct{}, len(publication.OriginTargets))
	for index, target := range publication.OriginTargets {
		targetPath := fmt.Sprintf("%s.originTargets[%d]", path, index)
		if err := requireContractID(target.NodeRef, targetPath+".nodeRef"); err != nil {
			return err
		}
		if err := requireContractID(target.InstanceRef, targetPath+".instanceRef"); err != nil {
			return err
		}
		pair := target.NodeRef + "\x00" + target.InstanceRef
		if _, duplicate := seenTargetPairs[pair]; duplicate {
			return fail(ErrDuplicate, targetPath, "origin target pair is duplicated")
		}
		seenTargetPairs[pair] = struct{}{}
		targetNodes = append(targetNodes, target.NodeRef)
		targetInstances = append(targetInstances, target.InstanceRef)
	}
	sort.Strings(targetNodes)
	sort.Strings(targetInstances)
	if !exactStringList(targetNodes, publication.OriginNodeRefs) || !exactStringList(targetInstances, publication.OriginInstanceRefs) {
		return fail(ErrInvalidPlan, path+".originTargets", "origin target pairs must exactly bind the governed node and instance sets")
	}
	access := publication.Access
	if access.Exposure != "public" || access.PolicyExposure != "public" ||
		!containsExecutorBundleString([]string{"human", "device", "human+device", "workload"}, access.Authentication) ||
		!containsExecutorBundleString([]string{"user", "admin", "identity", "secrets", "vault", "recovery"}, access.Privilege) ||
		access.LANStepDown || !access.DefaultClosed || access.PolicyRef != publication.Auth.PolicyRef ||
		!sortedUniqueNonEmpty(access.AllowedMethods) {
		return fail(ErrInvalidPlan, path+".access", "public access policy is incomplete or widened")
	}
	for _, method := range access.AllowedMethods {
		if !containsExecutorBundleString([]string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}, method) {
			return fail(ErrInvalidPlan, path+".access.allowedMethods", "unsupported HTTP method")
		}
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
		if err := requireContractID(publication.ServiceRef, itemPath+".serviceRef"); err != nil {
			return nil, err
		}
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
			if err := requireContractID(target.NodeRef, targetPath+".nodeRef"); err != nil {
				return nil, err
			}
			if err := requireContractID(target.InstanceRef, targetPath+".instanceRef"); err != nil {
				return nil, err
			}
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

func decodeLocalRuntimeExecutorPlan(raw []byte, path string, spec executorContractBundleSpec) (executorContractPlan, error) {
	if spec.moduleID == homeEncryptedOffsiteBackupModuleID {
		var exact homeOffsiteBackupExecutorPlan
		if err := decodeStrict(raw, &exact); err != nil {
			return nil, wrap(ErrInvalidPlan, path, "decode exact Home offsite-backup executor contract", err)
		}
		if err := validateExecutorContractPlanCommon(exact.StackID, exact.Kit, exact.Sites, exact.ModuleTargets, exact.ModuleCapabilities, exact.ControlPlane, spec, path); err != nil {
			return nil, err
		}
		validationPlan := localRuntimeExecutorPlan{
			StackID: exact.StackID, Kit: exact.Kit, Sites: exact.Sites,
			ModuleTargets: exact.ModuleTargets, ModuleCapabilities: exact.ModuleCapabilities,
			ControlPlane:                     exact.ControlPlane,
			HomeBackupTargetRequirements:     exact.HomeOffsiteBackup.Requirements,
			ExternalHomeBackupTargetBindings: exact.HomeOffsiteBackup.Bindings,
		}
		if err := validateHomeBackupTargetExecutorProjection(validationPlan, spec, path+".homeOffsiteBackup"); err != nil {
			return nil, err
		}
		return exact, nil
	}
	var plan localRuntimeExecutorPlan
	if err := decodeStrict(raw, &plan); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact Local executor contract", err)
	}
	if err := validateExecutorContractPlanCommon(plan.StackID, plan.Kit, plan.Sites, plan.ModuleTargets, plan.ModuleCapabilities, plan.ControlPlane, spec, path); err != nil {
		return nil, err
	}
	if err := validateStoragePolicy(plan.StoragePolicy, path+".storagePolicy"); err != nil {
		return nil, err
	}
	if err := validateExecutorBundleNetworkPolicy(plan.LocalNetworkPolicy, plan.Kit.Slug, false, path+".localNetworkPolicy"); err != nil {
		return nil, err
	}
	if err := validateExecutorBundleData(plan.Data, plan.Sites, path+".data"); err != nil {
		return nil, err
	}
	if err := validateExecutorBundleFailurePolicy(plan.FailurePolicy, path+".failurePolicy"); err != nil {
		return nil, err
	}
	siteKinds := executorBundleSiteKinds(plan.Sites)
	homeRefs := sortedExecutorBundleSites(plan.Sites, "home")
	if !exactStringList(plan.LocalReachability.HomeSiteRefs, homeRefs) {
		return nil, fail(ErrInvalidPlan, path+".localReachability.homeSiteRefs", "must exactly equal module Home Sites")
	}
	for index, route := range plan.LocalReachability.Routes {
		if err := validateHomeLocalRoute(route, siteKinds, fmt.Sprintf("%s.localReachability.routes[%d]", path, index)); err != nil {
			return nil, err
		}
	}
	if err := validateHomeAccessExecutorProjection(plan, spec, path); err != nil {
		return nil, err
	}
	if err := validateHomeBackupTargetExecutorProjection(plan, spec, path); err != nil {
		return nil, err
	}
	return plan, nil
}

func decodeHomeAccessExecutorPlan(raw []byte, path string, spec executorContractBundleSpec) (executorContractPlan, error) {
	var exact homeAccessExecutorPlan
	if err := decodeStrict(raw, &exact); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact Home access executor handoff", err)
	}
	if err := validateExecutorContractPlanCommon(
		exact.StackID,
		exact.Kit,
		exact.Sites,
		exact.ModuleTargets,
		exact.ModuleCapabilities,
		exact.ControlPlane,
		spec,
		path,
	); err != nil {
		return nil, err
	}
	validationPlan := localRuntimeExecutorPlan{
		StackID: exact.StackID, Kit: exact.Kit, Sites: exact.Sites,
		ModuleTargets: exact.ModuleTargets, ModuleCapabilities: exact.ModuleCapabilities,
		ControlPlane:               exact.ControlPlane,
		HomeAccessRequirements:     exact.HomeAccessHandoff.Requirements,
		ExternalHomeAccessBindings: exact.HomeAccessHandoff.Bindings,
	}
	if err := validateHomeAccessExecutorProjection(validationPlan, spec, path+".homeAccessHandoff"); err != nil {
		return nil, err
	}
	return exact, nil
}

func decodeBasementComposeExecutorPlan(raw []byte, path string, spec executorContractBundleSpec) (executorContractPlan, error) {
	var exact basementComposeExecutorPlan
	if err := decodeStrict(raw, &exact); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact Basement Compose selection handoff", err)
	}
	if err := validateExecutorContractPlanCommon(
		exact.StackID,
		exact.Kit,
		exact.Sites,
		exact.ModuleTargets,
		exact.ModuleCapabilities,
		exact.ControlPlane,
		spec,
		path,
	); err != nil {
		return nil, err
	}
	return exact, nil
}

type homeBackupTargetRequirementProjection struct {
	APIVersion             string   `json:"apiVersion"`
	Kind                   string   `json:"kind"`
	StackID                string   `json:"stackId"`
	SiteRef                string   `json:"siteRef"`
	CapabilityRef          string   `json:"capabilityRef"`
	ContractOwnerRef       string   `json:"contractOwnerRef"`
	CapabilityContractHash string   `json:"capabilityContractHash"`
	TargetNodeRefs         []string `json:"targetNodeRefs"`
	Policy                 struct {
		Scope                       string `json:"scope"`
		EncryptionRequired          bool   `json:"encryptionRequired"`
		EncryptionAuthority         string `json:"encryptionAuthority"`
		PlaintextEgressAllowed      bool   `json:"plaintextEgressAllowed"`
		CredentialCustody           string `json:"credentialCustody"`
		TargetLifecycle             string `json:"targetLifecycle"`
		RestoreVerificationRequired bool   `json:"restoreVerificationRequired"`
		ProviderSelection           string `json:"providerSelection"`
	} `json:"policy"`
	SpecHash         string `json:"specHash"`
	RequirementsHash string `json:"requirementsHash"`
}

type externalHomeBackupTargetBindingProjection struct {
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

func validateHomeBackupTargetExecutorProjection(plan localRuntimeExecutorPlan, spec executorContractBundleSpec, path string) error {
	isHomeBackup := containsExecutorBundleString(spec.requiredCapabilities, homeBackupCapability)
	if !isHomeBackup {
		if len(plan.HomeBackupTargetRequirements) != 0 || len(plan.ExternalHomeBackupTargetBindings) != 0 {
			return fail(ErrInvalidPlan, path, "non-backup Home module received a Home backup target projection")
		}
		return nil
	}
	if len(plan.HomeBackupTargetRequirements) == 0 || len(plan.ExternalHomeBackupTargetBindings) == 0 {
		return fail(ErrInvalidPlan, path, "Home encrypted offsite backup requires exact requirement and binding projections")
	}
	var requirements map[string]map[string]json.RawMessage
	if err := decodeStrict(plan.HomeBackupTargetRequirements, &requirements); err != nil {
		return wrap(ErrInvalidPlan, path+".homeBackupTargetRequirements", "decode closed Home backup requirements", err)
	}
	var bindings map[string]map[string]json.RawMessage
	if err := decodeStrict(plan.ExternalHomeBackupTargetBindings, &bindings); err != nil {
		return wrap(ErrInvalidPlan, path+".externalHomeBackupTargetBindings", "decode closed external Home backup bindings", err)
	}
	if len(requirements) != len(plan.Sites) {
		return fail(ErrInvalidPlan, path+".homeBackupTargetRequirements", "must contain exactly one requirement per module Home Site")
	}
	for _, site := range plan.Sites {
		targetNodeRefs := make([]string, 0, len(plan.ModuleTargets))
		for _, target := range plan.ModuleTargets {
			if target.SiteRef == site.ID {
				targetNodeRefs = append(targetNodeRefs, target.ID)
			}
		}
		sort.Strings(targetNodeRefs)
		byCapability, exists := requirements[site.ID]
		if !exists || len(byCapability) != 1 {
			return fail(ErrInvalidPlan, path+".homeBackupTargetRequirements."+site.ID, "must contain only the encrypted offsite backup capability")
		}
		rawRequirement, exists := byCapability[homeBackupCapability]
		if !exists {
			return fail(ErrInvalidPlan, path+".homeBackupTargetRequirements."+site.ID, "missing exact Home backup capability requirement")
		}
		var requirement homeBackupTargetRequirementProjection
		if err := decodeStrict(rawRequirement, &requirement); err != nil {
			return wrap(ErrInvalidPlan, path+".homeBackupTargetRequirements."+site.ID+"."+homeBackupCapability, "decode closed requirement", err)
		}
		if requirement.APIVersion != "stackkit.home-backup-target-requirement/v1" || requirement.Kind != "HomeBackupTargetRequirement" ||
			requirement.StackID != plan.StackID || requirement.SiteRef != site.ID || requirement.CapabilityRef != homeBackupCapability ||
			requirement.ContractOwnerRef == "" || !validSHA256(requirement.CapabilityContractHash) || !validSHA256(requirement.SpecHash) || !validSHA256(requirement.RequirementsHash) ||
			!exactStringList(requirement.TargetNodeRefs, targetNodeRefs) || requirement.Policy.Scope != "governed-home-data-only" || !requirement.Policy.EncryptionRequired ||
			requirement.Policy.EncryptionAuthority != "home" || requirement.Policy.PlaintextEgressAllowed || requirement.Policy.CredentialCustody != "external" ||
			requirement.Policy.TargetLifecycle != "external" || !requirement.Policy.RestoreVerificationRequired || requirement.Policy.ProviderSelection != "external" {
			return fail(ErrInvalidPlan, path+".homeBackupTargetRequirements."+site.ID+"."+homeBackupCapability, "requirement widens or contradicts the exact Home backup authority")
		}
		rawBody, _ := json.Marshal(requirement)
		var body map[string]any
		if err := decodeStrict(rawBody, &body); err != nil {
			return err
		}
		wantHash, err := resolvedplan.ComputeHomeBackupTargetRequirementHash(resolvedplan.HomeBackupTargetRequirement(body))
		if err != nil || requirement.RequirementsHash != wantHash {
			return fail(ErrInvalidPlan, path+".homeBackupTargetRequirements."+site.ID+"."+homeBackupCapability+".requirementsHash", "does not match the canonical requirement body")
		}
		bindingByCapability := bindings[site.ID]
		if len(bindingByCapability) == 0 {
			continue
		}
		if len(bindingByCapability) != 1 {
			return fail(ErrInvalidPlan, path+".externalHomeBackupTargetBindings."+site.ID, "must contain only the encrypted offsite backup capability")
		}
		rawBinding, exists := bindingByCapability[homeBackupCapability]
		if !exists {
			return fail(ErrInvalidPlan, path+".externalHomeBackupTargetBindings."+site.ID, "binding capability does not match the module")
		}
		var binding externalHomeBackupTargetBindingProjection
		if err := decodeStrict(rawBinding, &binding); err != nil {
			return wrap(ErrInvalidPlan, path+".externalHomeBackupTargetBindings."+site.ID+"."+homeBackupCapability, "decode closed binding", err)
		}
		if binding.APIVersion != "stackkit.external-home-backup-target-binding/v1" || binding.Kind != "ExternalHomeBackupTargetBinding" ||
			binding.StackID != requirement.StackID || binding.SiteRef != requirement.SiteRef || binding.CapabilityRef != requirement.CapabilityRef ||
			binding.ContractOwnerRef != requirement.ContractOwnerRef || binding.CapabilityContractHash != requirement.CapabilityContractHash ||
			binding.RequirementsHash != requirement.RequirementsHash || binding.SpecHash != requirement.SpecHash ||
			!validOpaqueSHA256Ref(binding.BindingRef, "home-backup-target-binding") || !validOpaqueSHA256Ref(binding.BackupTargetRef, "home-backup-target") ||
			!validOpaqueSHA256Ref(binding.CustodyAttestationRef, "home-backup-custody-attestation") {
			return fail(ErrInvalidPlan, path+".externalHomeBackupTargetBindings."+site.ID, "binding does not exactly match the provider-free Home backup requirement")
		}
		rawBody, _ = json.Marshal(binding)
		body = map[string]any{}
		if err := decodeStrict(rawBody, &body); err != nil {
			return err
		}
		wantBindingHash, err := resolvedplan.ComputeExternalHomeBackupTargetBindingHash(resolvedplan.ExternalHomeBackupTargetBinding(body))
		if err != nil || binding.BindingHash != wantBindingHash {
			return fail(ErrInvalidPlan, path+".externalHomeBackupTargetBindings."+site.ID+"."+homeBackupCapability+".bindingHash", "does not match the canonical binding body")
		}
	}
	for siteRef := range bindings {
		if _, ok := requirements[siteRef]; !ok {
			return fail(ErrInvalidPlan, path+".externalHomeBackupTargetBindings."+siteRef, "binding targets a Site outside the exact requirement")
		}
	}
	return nil
}

type homeAccessRequirementProjection struct {
	APIVersion             string   `json:"apiVersion"`
	Kind                   string   `json:"kind"`
	StackID                string   `json:"stackId"`
	SiteRef                string   `json:"siteRef"`
	CapabilityRef          string   `json:"capabilityRef"`
	ContractOwnerRef       string   `json:"contractOwnerRef"`
	CapabilityContractHash string   `json:"capabilityContractHash"`
	TargetNodeRefs         []string `json:"targetNodeRefs"`
	Policy                 struct {
		DefaultDeny       bool   `json:"defaultDeny"`
		Initiation        string `json:"initiation"`
		RouteScope        string `json:"routeScope"`
		AllowDefaultRoute bool   `json:"allowDefaultRoute"`
		AllowBroadLAN     bool   `json:"allowBroadLAN"`
		IdentityMode      string `json:"identityMode"`
		CredentialCustody string `json:"credentialCustody"`
		FabricLifecycle   string `json:"fabricLifecycle"`
	} `json:"policy"`
	SpecHash         string `json:"specHash"`
	RequirementsHash string `json:"requirementsHash"`
}

type externalHomeAccessBindingProjection struct {
	APIVersion             string `json:"apiVersion"`
	Kind                   string `json:"kind"`
	BindingRef             string `json:"bindingRef"`
	StackID                string `json:"stackId"`
	SiteRef                string `json:"siteRef"`
	CapabilityRef          string `json:"capabilityRef"`
	ContractOwnerRef       string `json:"contractOwnerRef"`
	CapabilityContractHash string `json:"capabilityContractHash"`
	RequirementsHash       string `json:"requirementsHash"`
	AccessFabricRef        string `json:"accessFabricRef"`
	StackKitsVersion       string `json:"stackkitsVersion"`
	CandidateDigest        string `json:"candidateDigest"`
	SpecHash               string `json:"specHash"`
	IssuedAt               string `json:"issuedAt"`
	ValidUntil             string `json:"validUntil"`
	BindingHash            string `json:"bindingHash"`
}

func validateHomeAccessExecutorProjection(plan localRuntimeExecutorPlan, spec executorContractBundleSpec, path string) error {
	capabilityRef := ""
	for _, candidate := range []string{"private-remote-access", "public-publish-egress"} {
		if containsExecutorBundleString(spec.requiredCapabilities, candidate) {
			capabilityRef = candidate
			break
		}
	}
	if capabilityRef == "" {
		if len(plan.HomeAccessRequirements) != 0 || len(plan.ExternalHomeAccessBindings) != 0 {
			return fail(ErrInvalidPlan, path, "non-access module received a Home access authority projection")
		}
		return nil
	}
	if len(plan.HomeAccessRequirements) == 0 || len(plan.ExternalHomeAccessBindings) == 0 {
		return fail(ErrInvalidPlan, path, "Home access module requires exact requirement and binding projections")
	}
	var requirements map[string]map[string]json.RawMessage
	if err := decodeStrict(plan.HomeAccessRequirements, &requirements); err != nil {
		return wrap(ErrInvalidPlan, path+".homeAccessRequirements", "decode closed Home access requirements", err)
	}
	var bindings map[string]map[string]json.RawMessage
	if err := decodeStrict(plan.ExternalHomeAccessBindings, &bindings); err != nil {
		return wrap(ErrInvalidPlan, path+".externalHomeAccessBindings", "decode closed external Home access bindings", err)
	}
	if len(requirements) != len(plan.Sites) {
		return fail(ErrInvalidPlan, path+".homeAccessRequirements", "must contain exactly one requirement per module Home Site")
	}
	for _, site := range plan.Sites {
		targetNodeRefs := make([]string, 0, len(plan.ModuleTargets))
		for _, target := range plan.ModuleTargets {
			if target.SiteRef == site.ID {
				targetNodeRefs = append(targetNodeRefs, target.ID)
			}
		}
		sort.Strings(targetNodeRefs)
		if len(targetNodeRefs) == 0 {
			return fail(ErrInvalidPlan, path+".moduleTargets", "Home access module has no target at Site %s", site.ID)
		}
		byCapability, exists := requirements[site.ID]
		if !exists || len(byCapability) != 1 {
			return fail(ErrInvalidPlan, path+".homeAccessRequirements."+site.ID, "must contain only the module access capability")
		}
		rawRequirement, exists := byCapability[capabilityRef]
		if !exists {
			return fail(ErrInvalidPlan, path+".homeAccessRequirements."+site.ID, "missing exact access capability requirement")
		}
		var requirement homeAccessRequirementProjection
		if err := decodeStrict(rawRequirement, &requirement); err != nil {
			return wrap(ErrInvalidPlan, path+".homeAccessRequirements."+site.ID+"."+capabilityRef, "decode closed requirement", err)
		}
		identityMode := "device-bound"
		if capabilityRef == "public-publish-egress" {
			identityMode = "service-identity"
		}
		if requirement.APIVersion != "stackkit.home-access-requirement/v1" || requirement.Kind != "HomeAccessRequirement" ||
			requirement.StackID != plan.StackID || requirement.SiteRef != site.ID || requirement.CapabilityRef != capabilityRef ||
			requirement.ContractOwnerRef == "" || !validSHA256(requirement.CapabilityContractHash) || !validSHA256(requirement.SpecHash) || !validSHA256(requirement.RequirementsHash) ||
			!exactStringList(requirement.TargetNodeRefs, targetNodeRefs) || !requirement.Policy.DefaultDeny || requirement.Policy.Initiation != "home-outbound" ||
			requirement.Policy.RouteScope != "declared-services-only" || requirement.Policy.AllowDefaultRoute || requirement.Policy.AllowBroadLAN ||
			requirement.Policy.IdentityMode != identityMode || requirement.Policy.CredentialCustody != "external" || requirement.Policy.FabricLifecycle != "external" {
			return fail(ErrInvalidPlan, path+".homeAccessRequirements."+site.ID+"."+capabilityRef, "requirement widens or contradicts the exact Home access authority")
		}
		bindingByCapability := bindings[site.ID]
		if len(bindingByCapability) == 0 {
			continue
		}
		if len(bindingByCapability) != 1 {
			return fail(ErrInvalidPlan, path+".externalHomeAccessBindings."+site.ID, "must contain only the module access capability")
		}
		rawBinding, exists := bindingByCapability[capabilityRef]
		if !exists {
			return fail(ErrInvalidPlan, path+".externalHomeAccessBindings."+site.ID, "binding capability does not match the module")
		}
		var binding externalHomeAccessBindingProjection
		if err := decodeStrict(rawBinding, &binding); err != nil {
			return wrap(ErrInvalidPlan, path+".externalHomeAccessBindings."+site.ID+"."+capabilityRef, "decode closed binding", err)
		}
		if binding.APIVersion != "stackkit.external-home-access-binding/v1" || binding.Kind != "ExternalHomeAccessBinding" ||
			binding.StackID != requirement.StackID || binding.SiteRef != requirement.SiteRef || binding.CapabilityRef != requirement.CapabilityRef ||
			binding.ContractOwnerRef != requirement.ContractOwnerRef || binding.CapabilityContractHash != requirement.CapabilityContractHash ||
			binding.RequirementsHash != requirement.RequirementsHash || binding.SpecHash != requirement.SpecHash ||
			!validOpaqueSHA256Ref(binding.BindingRef, "home-access-binding") || !validOpaqueSHA256Ref(binding.AccessFabricRef, "home-access-fabric") ||
			!validSHA256(binding.CandidateDigest) || !validSHA256(binding.BindingHash) || binding.StackKitsVersion == "" || binding.IssuedAt == "" || binding.ValidUntil == "" {
			return fail(ErrInvalidPlan, path+".externalHomeAccessBindings."+site.ID+"."+capabilityRef, "binding does not exactly match the Home access requirement")
		}
	}
	for siteRef := range bindings {
		if !containsExecutorBundleString(sortedExecutorBundleSites(plan.Sites, "home"), siteRef) {
			return fail(ErrInvalidPlan, path+".externalHomeAccessBindings."+siteRef, "binding targets a Site outside the module")
		}
	}
	return nil
}

func validOpaqueSHA256Ref(value, scheme string) bool {
	prefix := scheme + "://sha256/"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	for _, character := range value[len(prefix):] {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func decodeCloudRuntimeExecutorPlan(raw []byte, path string, spec executorContractBundleSpec) (executorContractPlan, error) {
	if spec.moduleID == cloudPrivateAdminMeshModuleID {
		var exact cloudAdminMeshExecutorPlan
		if err := decodeStrict(raw, &exact); err != nil {
			return nil, wrap(ErrInvalidPlan, path, "decode exact Cloud admin-mesh executor contract", err)
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
	previousRouteID := ""
	for index, route := range projection.Routes {
		routePath := fmt.Sprintf("%s.routes[%d]", path, index)
		if previousRouteID != "" && route.ID <= previousRouteID {
			return fail(ErrInvalidPlan, routePath+".id", "public-edge routes must be unique and sorted")
		}
		previousRouteID = route.ID
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

//nolint:gocyclo // Identity, topology, capability ownership, and placement are one fail-closed handoff boundary.
func validateExecutorContractPlanCommon(stackID string, kit executorBundleKit, sites []executorBundleSite, targets []executorBundleTarget, capabilities []executorBundleCapability, control executorBundleControlPlane, spec executorContractBundleSpec, path string) error {
	if err := requireContractID(stackID, path+".stackId"); err != nil {
		return err
	}
	if !containsExecutorBundleString(spec.allowedKits, kit.Slug) || kit.Version == "" || !validSHA256(kit.DefinitionHash) {
		return fail(ErrInvalidPlan, path+".kit", "kit is incompatible with the registered module contract")
	}
	if len(sites) == 0 || len(targets) == 0 {
		return fail(ErrInvalidPlan, path+".moduleTargets", "executor contract requires explicit sites and module targets")
	}
	siteKinds := map[string]string{}
	previousSite := ""
	for index, site := range sites {
		sitePath := fmt.Sprintf("%s.sites[%d]", path, index)
		if err := requireContractID(site.ID, sitePath+".id"); err != nil {
			return err
		}
		if site.Kind != "home" && site.Kind != "cloud" || strings.TrimSpace(site.FailureDomain) == "" {
			return fail(ErrInvalidPlan, sitePath, "site requires a governed kind and failure domain")
		}
		if previousSite != "" && site.ID <= previousSite {
			return fail(ErrDuplicate, sitePath+".id", "sites must be unique and sorted")
		}
		previousSite = site.ID
		siteKinds[site.ID] = site.Kind
	}
	if spec.siteKind != "" && spec.moduleID != bridgePublicationModuleID {
		for _, site := range sites {
			if site.Kind != spec.siteKind {
				return fail(ErrInvalidPlan, path+".sites", "%s executor contract accepts only %s Sites", spec.moduleID, spec.siteKind)
			}
		}
	}
	modernSitesInvalid := kit.Slug == "modern-homelab" &&
		(spec.moduleID == bridgePublicationModuleID && (!containsExecutorBundleSiteKind(siteKinds, "home") || !containsExecutorBundleSiteKind(siteKinds, "cloud")) ||
			spec.moduleID != bridgePublicationModuleID && (spec.siteKind == "" && (!containsExecutorBundleSiteKind(siteKinds, "home") || !containsExecutorBundleSiteKind(siteKinds, "cloud")) || spec.siteKind != "" && !exactExecutorBundleSiteKinds(siteKinds, spec.siteKind)))
	if kit.Slug == "basement-kit" && !exactExecutorBundleSiteKinds(siteKinds, "home") || kit.Slug == "cloud-kit" && !exactExecutorBundleSiteKinds(siteKinds, "cloud") || modernSitesInvalid {
		return fail(ErrInvalidPlan, path+".sites", "site kinds contradict the selected kit")
	}
	if err := validateExecutorBundleTargets(targets, siteKinds, path+".moduleTargets"); err != nil {
		return err
	}
	if spec.moduleID == bridgePublicationModuleID {
		for _, target := range targets {
			if siteKinds[target.SiteRef] != "cloud" {
				return fail(ErrInvalidPlan, path+".moduleTargets", "publication runtime targets must be Cloud edge nodes")
			}
		}
	}
	if err := validateExecutorBundleCapabilities(capabilities, spec, path+".moduleCapabilities"); err != nil {
		return err
	}
	if siteKinds[control.AuthoritySiteRef] == "" || len(control.Members) == 0 || !containsExecutorBundleString([]string{"single", "warm-standby", "quorum"}, control.Mode) {
		return fail(ErrInvalidPlan, path+".controlPlane", "control plane must bind an existing authority Site and explicit members")
	}
	targetIDs := make([]string, len(targets))
	for index := range targets {
		targetIDs[index] = targets[index].ID
	}
	seenMembers := map[string]struct{}{}
	for index, member := range control.Members {
		if spec.moduleID != bridgePublicationModuleID && spec.moduleID != federationLinkModuleID && !containsExecutorBundleString(targetIDs, member) {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s.controlPlane.members[%d]", path, index), "control member is outside module targets")
		}
		if _, duplicate := seenMembers[member]; duplicate {
			return fail(ErrDuplicate, fmt.Sprintf("%s.controlPlane.members[%d]", path, index), "control member is duplicated")
		}
		seenMembers[member] = struct{}{}
	}
	if control.Mode == "single" && len(control.Members) != 1 || control.Mode == "warm-standby" && len(control.Members) < 2 || control.Mode == "quorum" && len(control.Members) < 3 {
		return fail(ErrInvalidPlan, path+".controlPlane.members", "member count contradicts control-plane mode")
	}
	return nil
}

func validateExecutorBundleTargets(targets []executorBundleTarget, siteKinds map[string]string, path string) error {
	previous := ""
	for index, target := range targets {
		targetPath := fmt.Sprintf("%s[%d]", path, index)
		if err := requireContractID(target.ID, targetPath+".id"); err != nil {
			return err
		}
		if previous != "" && target.ID <= previous {
			return fail(ErrDuplicate, targetPath+".id", "module targets must be unique and sorted")
		}
		previous = target.ID
		if siteKinds[target.SiteRef] == "" || len(target.Roles) == 0 || strings.TrimSpace(target.FailureDomain) == "" {
			return fail(ErrInvalidPlan, targetPath, "target must bind an existing Site, roles, and failure domain")
		}
		if target.DeclaredHardware.Arch != "amd64" && target.DeclaredHardware.Arch != "arm64" || target.DeclaredHardware.Profile == "" {
			return fail(ErrInvalidPlan, targetPath+".declaredHardware", "target requires an architecture and profile")
		}
		if target.DeclaredHardware.CPUCores < 0 || target.DeclaredHardware.RAMGB < 0 || target.DeclaredHardware.StorageGB < 0 {
			return fail(ErrInvalidPlan, targetPath+".declaredHardware", "declared capacities cannot be negative")
		}
		if !sortedUniqueNonEmpty(target.Roles) {
			return fail(ErrInvalidPlan, targetPath+".roles", "roles must be non-empty, unique, and sorted")
		}
	}
	return nil
}

func validateExecutorBundleCapabilities(capabilities []executorBundleCapability, spec executorContractBundleSpec, path string) error {
	if len(capabilities) == 0 {
		return fail(ErrInvalidPlan, path, "module capability projection cannot be empty")
	}
	ids := make([]string, 0, len(capabilities))
	for index, capability := range capabilities {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		if err := requireContractID(capability.ID, itemPath+".id"); err != nil {
			return err
		}
		if !containsExecutorBundleString(spec.allowedCapabilities, capability.ID) || !validSHA256(capability.ContractHash) {
			return fail(ErrInvalidPlan, itemPath, "capability is outside the exact module contract")
		}
		ids = append(ids, capability.ID)
	}
	if !sortedUniqueNonEmpty(ids) {
		return fail(ErrInvalidPlan, path, "module capabilities must be unique and sorted")
	}
	for _, required := range spec.requiredCapabilities {
		if !containsExecutorBundleString(ids, required) {
			return fail(ErrInvalidPlan, path, "required module capability %q is missing", required)
		}
	}
	return nil
}

func validateHostRuntimePolicy(policy executorBundleHostRuntimePolicy, path string) error {
	if !containsExecutorBundleString([]string{"advanced", "bare", "bootstrapped"}, policy.Install.Mode) || !containsExecutorBundleString([]string{"docker", "native"}, policy.Install.Runtime) {
		return fail(ErrInvalidPlan, path, "host runtime mode or runtime is unsupported")
	}
	if !containsExecutorBundleString([]string{"native", "selected-provider", "standalone"}, policy.Install.Platform.Management) || !containsExecutorBundleString([]string{"automatic", "manual", "on-demand"}, policy.Install.Platform.SetupPolicy.Platform) || !containsExecutorBundleString([]string{"automatic", "manual", "on-demand"}, policy.Install.Platform.SetupPolicy.ApplicationDefault) {
		return fail(ErrInvalidPlan, path+".install.platform", "platform policy is invalid")
	}
	if policy.Install.Runtime == "native" && (policy.Install.Platform.Management != "native" || policy.Container != nil) {
		return fail(ErrInvalidPlan, path, "native runtime cannot carry container policy")
	}
	if policy.Install.Runtime == "docker" {
		if policy.Container == nil || !containsExecutorBundleString([]string{"docker", "podman"}, policy.Container.Engine) || !absoluteExecutorBundlePath(policy.Container.DataRoot) || policy.Container.LogDriver == "" {
			return fail(ErrInvalidPlan, path+".container", "container runtime policy is incomplete")
		}
	}
	if policy.Host.Timezone == "" || policy.Host.Locale == "" || policy.Host.Swap == "" || policy.Host.UnattendedUpgrades == "" {
		return fail(ErrInvalidPlan, path+".host", "host policy is incomplete")
	}
	return nil
}

func validateStoragePolicy(policy executorBundleStoragePolicy, path string) error {
	for field, value := range map[string]string{"dataRoot": policy.DataRoot, "backupRoot": policy.BackupRoot, "stacksRoot": policy.StacksRoot} {
		if !absoluteExecutorBundlePath(value) {
			return fail(ErrInvalidPlan, path+"."+field, "storage path must be absolute")
		}
	}
	if policy.MediaRoot != "" && !absoluteExecutorBundlePath(policy.MediaRoot) {
		return fail(ErrInvalidPlan, path+".mediaRoot", "storage path must be absolute")
	}
	if policy.VolumeDriver != "local" && policy.VolumeDriver != "nfs" {
		return fail(ErrInvalidPlan, path+".volumeDriver", "unsupported storage driver")
	}
	if policy.External != nil && !absoluteExecutorBundlePath(policy.External.MountPoint) {
		return fail(ErrInvalidPlan, path+".external.mountPoint", "external mount point must be absolute")
	}
	if policy.VolumeDriver == "nfs" && (policy.NFS == nil || !validExecutorBundleHost(policy.NFS.Server) || !absoluteExecutorBundlePath(policy.NFS.Path)) {
		return fail(ErrInvalidPlan, path+".nfs", "NFS storage requires server and absolute path")
	}
	if policy.VolumeDriver != "nfs" && policy.NFS != nil {
		return fail(ErrInvalidPlan, path+".nfs", "NFS settings require the NFS volume driver")
	}
	return nil
}

func validateExecutorBundleNetworkPolicy(policy executorBundleNetworkPolicy, kit string, cloud bool, path string) error {
	wantMode := "private"
	if cloud {
		wantMode = "public-capable"
	}
	if kit == "modern-homelab" {
		wantMode = "hybrid"
	}
	if policy.Mode != wantMode || policy.Domain.Base == "" || !validExecutorBundlePrefix(policy.Transport.Subnet) || policy.Transport.MTU < 576 || policy.Transport.MTU > 9216 || len(policy.DNS.Servers) == 0 {
		return fail(ErrInvalidPlan, path, "network policy is incomplete or contradicts the selected kit")
	}
	if policy.Transport.Gateway != "" && !validExecutorBundleAddress(policy.Transport.Gateway) {
		return fail(ErrInvalidPlan, path+".transport.gateway", "gateway must be an IP address without credentials or transport authority")
	}
	for index, server := range policy.DNS.Servers {
		if !validExecutorBundleAddress(server) {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s.dns.servers[%d]", path, index), "DNS server must be an IP address without credentials or resolver URL authority")
		}
	}
	for index, domain := range policy.DNS.SearchDomains {
		if !validExecutorBundleHost(domain) || validExecutorBundleAddress(domain) {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s.dns.searchDomains[%d]", path, index), "DNS search domain must be a hostname without credentials or resolver URL authority")
		}
	}
	if !containsExecutorBundleString([]string{"internal", "off", "public"}, policy.TLS.DefaultMode) || !containsExecutorBundleString([]string{"TLS1.2", "TLS1.3"}, policy.TLS.MinVersion) {
		return fail(ErrInvalidPlan, path+".tls", "TLS policy is invalid")
	}
	if cloud && policy.TLS.DefaultMode != "public" || !cloud && kit == "basement-kit" && policy.TLS.DefaultMode != "internal" {
		return fail(ErrInvalidPlan, path+".tls.defaultMode", "TLS mode contradicts the runtime profile")
	}
	return nil
}

func validExecutorBundlePrefix(value string) bool {
	prefix, err := netip.ParsePrefix(value)
	return err == nil && prefix.IsValid()
}

func validExecutorBundleAddress(value string) bool {
	address, err := netip.ParseAddr(value)
	return err == nil && address.IsValid()
}

func validExecutorBundleHost(value string) bool {
	if validExecutorBundleAddress(value) {
		return true
	}
	if value == "" || len(value) > 253 || strings.ContainsAny(value, "@/:\\") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if character != '-' && character != '_' && (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
				return false
			}
		}
	}
	return true
}

func validateExecutorBundleIdentity(identity executorBundleIdentity, sites []executorBundleSite, path string) error {
	siteKinds := executorBundleSiteKinds(sites)
	if siteKinds[identity.HumanAuthoritySiteRef] == "" || !identity.PossessionBoundSessions || identity.LANLocationIsIdentity {
		return fail(ErrInvalidPlan, path, "identity policy must retain an existing authority, possession binding, and no LAN identity")
	}
	if identity.DeviceAuthoritySiteRef != "" && siteKinds[identity.DeviceAuthoritySiteRef] == "" {
		return fail(ErrInvalidPlan, path+".deviceAuthoritySiteRef", "device authority Site does not exist")
	}
	if identity.DeviceEnrollment != nil {
		device := identity.DeviceEnrollment
		if device.Mode != "local-only" || device.EndpointExposure != "lan" || device.RemoteEnrollment || !device.RequireOwnerStepUp || !device.RequireLocalPairingProof || !device.RequireDeviceGeneratedKey || !device.RequirePossessionProof || !device.RevocationSupported || siteKinds[device.AuthoritySiteRef] != "home" {
			return fail(ErrInvalidPlan, path+".deviceEnrollment", "device enrollment widened beyond the local possession-bound contract")
		}
	}
	return nil
}

func validateExecutorBundleData(data executorBundleData, sites []executorBundleSite, path string) error {
	if !containsExecutorBundleString([]string{"cloud", "home", "per-workload"}, data.DefaultAuthority) {
		return fail(ErrInvalidPlan, path+".defaultAuthority", "unsupported data authority")
	}
	siteKinds := executorBundleSiteKinds(sites)
	for bindingID, binding := range data.Bindings {
		bindingPath := path + ".bindings." + bindingID
		if err := requireContractID(bindingID, bindingPath); err != nil {
			return err
		}
		if len(binding.Classes) == 0 || siteKinds[binding.PrimarySiteRef] == "" {
			return fail(ErrInvalidPlan, bindingPath, "data binding requires classes and an existing primary Site")
		}
		for _, replica := range binding.ReplicaSiteRefs {
			if siteKinds[replica] == "" || replica == binding.PrimarySiteRef {
				return fail(ErrInvalidPlan, bindingPath+".replicaSiteRefs", "replica must be a distinct existing Site")
			}
		}
		if binding.CloudCopyAllowed != (binding.CloudCopyPolicy != nil) {
			return fail(ErrInvalidPlan, bindingPath+".cloudCopyPolicy", "cloud-copy opt-in and policy must be present together")
		}
		if binding.CloudCopyPolicy != nil {
			if err := requireContractID(binding.CloudCopyPolicy.PolicyRef, bindingPath+".cloudCopyPolicy.policyRef"); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateExecutorBundleFailurePolicy(policy executorBundleFailurePolicy, path string) error {
	if !containsExecutorBundleString([]string{"fail-closed", "local-continues", "not-applicable"}, policy.OnCloudLoss) || !containsExecutorBundleString([]string{"cloud-continues", "fail-closed", "local-continues", "not-applicable"}, policy.OnLinkLoss) || !containsExecutorBundleString([]string{"fail-closed", "not-applicable"}, policy.CloudEdge) || !policy.DenyNewCrossSiteSessions || policy.MaxStaleVerificationSeconds < 0 {
		return fail(ErrInvalidPlan, path, "failure policy widens the generation-only contract")
	}
	return nil
}

func executorBundleSiteKinds(sites []executorBundleSite) map[string]string {
	result := make(map[string]string, len(sites))
	for _, site := range sites {
		result[site.ID] = site.Kind
	}
	return result
}

func sortedExecutorBundleSites(sites []executorBundleSite, kind string) []string {
	var refs []string
	for _, site := range sites {
		if site.Kind == kind {
			refs = append(refs, site.ID)
		}
	}
	sort.Strings(refs)
	return refs
}

func exactExecutorBundleSiteKinds(siteKinds map[string]string, kind string) bool {
	if len(siteKinds) == 0 {
		return false
	}
	for _, candidate := range siteKinds {
		if candidate != kind {
			return false
		}
	}
	return true
}

func containsExecutorBundleSiteKind(siteKinds map[string]string, kind string) bool {
	for _, candidate := range siteKinds {
		if candidate == kind {
			return true
		}
	}
	return false
}

func containsExecutorBundleString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sortedUniqueNonEmpty(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for index, value := range values {
		if value == "" || index > 0 && value <= values[index-1] {
			return false
		}
	}
	return true
}

func absoluteExecutorBundlePath(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.ContainsAny(value, "\r\n\x00")
}
