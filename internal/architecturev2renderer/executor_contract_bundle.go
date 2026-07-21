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

	federationLinkModuleID             = "stackkits-federation-link-runtime"
	federationControlAgentModuleID     = "stackkits-federation-control-agent-runtime"
	federationBackupModuleID           = "stackkits-federation-backup-runtime"
	federationObservabilityModuleID    = "stackkits-federation-observability-runtime"
	federationRuntimeModuleVersion     = "1.0.0"
	federationLinkTemplateRef          = "builtin://modern/federation/link/executor-contract/v1.json"
	federationControlTemplateRef       = "builtin://modern/federation/control-agent/executor-contract/v1.json"
	federationBackupTemplateRef        = "builtin://modern/federation/backup/executor-contract/v1.json"
	federationObservabilityTemplateRef = "builtin://modern/federation/observability/executor-contract/v1.json"
	federationLinkOutputRef            = "modern/federation/link/executor-contract.json"
	federationControlOutputRef         = "modern/federation/control-agent/executor-contract.json"
	federationBackupOutputRef          = "modern/federation/backup/executor-contract.json"
	federationObservabilityOutputRef   = "modern/federation/observability/executor-contract.json"
)

const executorContractBundleContract = `"contract":{"apply":"not-implemented","credentials":"not-included","generation":"supported","providerLifecycle":"not-owned","runtimeEnforcement":"unverified","scope":"generation-only","serverProviderAuthority":"not-owned"}`
const cloudHostSecurityExecutorContract = `"contract":{"apply":"typed-local-operations","credentials":"not-included","firewallPolicy":"default-deny-declared-services-only","generation":"supported","hardeningProfile":"internet-host-baseline-v1","operations":["apply-cloud-host-firewall","apply-cloud-host-hardening","verify-cloud-host-security"],"providerLifecycle":"not-owned","runtimeEnforcement":"adapter-verified","scope":"cloud-host-node","serverProviderAuthority":"not-owned"}`

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
		planInputRefs: []string{"cloudNetworkPolicy", "controlPlane", "data", "failurePolicy", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId", "storagePolicy"},
		allowedKits:   []string{"cloud-kit", "modern-homelab"}, siteKind: "cloud",
		allowedCapabilities:  []string{"private-admin-mesh"},
		requiredCapabilities: []string{"private-admin-mesh"},
		decodePlan:           decodeCloudRuntimeExecutorPlan,
	},
	{
		moduleID: basementComposeRuntimeModuleID, moduleVersion: basementComposeRuntimeModuleVersion,
		templateRef: basementComposeRuntimeTemplateRef, outputRef: basementComposeRuntimeOutputRef,
		planInputRefs: []string{"controlPlane", "data", "failurePolicy", "kit", "localNetworkPolicy", "localReachability", "moduleCapabilities", "moduleTargets", "sites", "stackId", "storagePolicy"},
		allowedKits:   []string{"basement-kit"}, siteKind: "home",
		allowedCapabilities:  []string{"basement-compose-runtime"},
		requiredCapabilities: []string{"basement-compose-runtime"},
		decodePlan:           decodeLocalRuntimeExecutorPlan,
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
		contractJSON:  cloudHostSecurityExecutorContract,
		planInputRefs: []string{"cloudNetworkPolicy", "controlPlane", "data", "failurePolicy", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId", "storagePolicy"},
		allowedKits:   []string{"cloud-kit", "modern-homelab"}, siteKind: "cloud",
		allowedCapabilities:  []string{"host-local-internet-firewall", "internet-host-hardening"},
		requiredCapabilities: []string{"host-local-internet-firewall", "internet-host-hardening"},
		decodePlan:           decodeCloudRuntimeExecutorPlan,
	},
	{
		moduleID: cloudPublicEdgeModuleID, moduleVersion: cloudPublicEdgeModuleVersion,
		templateRef: cloudPublicEdgeTemplateRef, outputRef: cloudPublicEdgeOutputRef,
		planInputRefs: []string{"cloudNetworkPolicy", "controlPlane", "data", "failurePolicy", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId", "storagePolicy"},
		allowedKits:   []string{"cloud-kit", "modern-homelab"}, siteKind: "cloud",
		allowedCapabilities:  []string{"public-edge"},
		requiredCapabilities: []string{"public-edge"},
		decodePlan:           decodeCloudRuntimeExecutorPlan,
	},
	{
		moduleID: cloudOffsiteBackupModuleID, moduleVersion: cloudOffsiteBackupModuleVersion,
		templateRef: cloudOffsiteBackupTemplateRef, outputRef: cloudOffsiteBackupOutputRef,
		planInputRefs: []string{"cloudNetworkPolicy", "controlPlane", "data", "failurePolicy", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId", "storagePolicy"},
		allowedKits:   []string{"cloud-kit", "modern-homelab"}, siteKind: "cloud",
		allowedCapabilities:  []string{"offsite-object-backup"},
		requiredCapabilities: []string{"offsite-object-backup"},
		decodePlan:           decodeCloudRuntimeExecutorPlan,
	},
	{
		moduleID: federationLinkModuleID, moduleVersion: federationRuntimeModuleVersion,
		templateRef: federationLinkTemplateRef, outputRef: federationLinkOutputRef,
		planInputRefs:       []string{"bridge", "controlPlane", "data", "failurePolicy", "identity", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId"},
		allowedKits:         []string{"modern-homelab"},
		allowedCapabilities: []string{"inter-site-link"}, requiredCapabilities: []string{"inter-site-link"},
		decodePlan: decodeFederationRuntimeExecutorPlan,
	},
	{
		moduleID: federationControlAgentModuleID, moduleVersion: federationRuntimeModuleVersion,
		templateRef: federationControlTemplateRef, outputRef: federationControlOutputRef,
		planInputRefs:       []string{"bridge", "controlPlane", "data", "failurePolicy", "identity", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId"},
		allowedKits:         []string{"modern-homelab"},
		allowedCapabilities: []string{"outbound-control-agent"}, requiredCapabilities: []string{"outbound-control-agent"},
		decodePlan: decodeFederationRuntimeExecutorPlan,
	},
	{
		moduleID: federationBackupModuleID, moduleVersion: federationRuntimeModuleVersion,
		templateRef: federationBackupTemplateRef, outputRef: federationBackupOutputRef,
		planInputRefs:       []string{"bridge", "controlPlane", "data", "failurePolicy", "identity", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId"},
		allowedKits:         []string{"modern-homelab"},
		allowedCapabilities: []string{"cross-site-backup"}, requiredCapabilities: []string{"cross-site-backup"},
		decodePlan: decodeFederationRuntimeExecutorPlan,
	},
	{
		moduleID: federationObservabilityModuleID, moduleVersion: federationRuntimeModuleVersion,
		templateRef: federationObservabilityTemplateRef, outputRef: federationObservabilityOutputRef,
		planInputRefs:       []string{"bridge", "controlPlane", "data", "failurePolicy", "identity", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId"},
		allowedKits:         []string{"modern-homelab"},
		allowedCapabilities: []string{"bridge-observability"}, requiredCapabilities: []string{"bridge-observability"},
		decodePlan: decodeFederationRuntimeExecutorPlan,
	},
	{
		moduleID: homePrivateRemoteAccessModuleID, moduleVersion: homeExtensionRuntimeModuleVersion,
		templateRef: homePrivateRemoteAccessTemplateRef, outputRef: homePrivateRemoteAccessOutputRef,
		planInputRefs: []string{"controlPlane", "data", "externalHomeAccessBindings", "failurePolicy", "homeAccessRequirements", "kit", "localNetworkPolicy", "localReachability", "moduleCapabilities", "moduleTargets", "sites", "stackId", "storagePolicy"},
		allowedKits:   []string{"basement-kit", "modern-homelab"}, siteKind: "home",
		allowedCapabilities: []string{"private-remote-access"}, requiredCapabilities: []string{"private-remote-access"},
		decodePlan: decodeLocalRuntimeExecutorPlan,
	},
	{
		moduleID: homePublicPublishEgressModuleID, moduleVersion: homeExtensionRuntimeModuleVersion,
		templateRef: homePublicPublishEgressTemplateRef, outputRef: homePublicPublishEgressOutputRef,
		planInputRefs: []string{"controlPlane", "data", "externalHomeAccessBindings", "failurePolicy", "homeAccessRequirements", "kit", "localNetworkPolicy", "localReachability", "moduleCapabilities", "moduleTargets", "sites", "stackId", "storagePolicy"},
		allowedKits:   []string{"basement-kit"}, siteKind: "home",
		allowedCapabilities: []string{"public-publish-egress"}, requiredCapabilities: []string{"public-publish-egress"},
		decodePlan: decodeLocalRuntimeExecutorPlan,
	},
	{
		moduleID: homeEncryptedOffsiteBackupModuleID, moduleVersion: homeExtensionRuntimeModuleVersion,
		templateRef: homeEncryptedOffsiteBackupTemplateRef, outputRef: homeEncryptedOffsiteBackupOutputRef,
		planInputRefs: []string{"controlPlane", "data", "failurePolicy", "kit", "localNetworkPolicy", "localReachability", "moduleCapabilities", "moduleTargets", "sites", "stackId", "storagePolicy"},
		allowedKits:   []string{"basement-kit"}, siteKind: "home",
		allowedCapabilities: []string{"encrypted-offsite-backup"}, requiredCapabilities: []string{"encrypted-offsite-backup"},
		decodePlan: decodeLocalRuntimeExecutorPlan,
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
	return newExecutorContractBundleRenderer(executorContractBundleSpecs[5]).contract
}

// CloudHostSecurityPolicy is the closed, provider-free host policy carried by
// one validated Cloud host-security executor artifact. It deliberately has no
// endpoint, credential, provider-resource, lifecycle, or generic-command
// authority.
type CloudHostSecurityPolicy struct {
	StackID         string
	KitSlug         string
	SiteRef         string
	NodeRef         string
	Roles           []string
	NetworkMode     string
	TransportSubnet string
	IPv6            bool
	TLSMinVersion   string
}

type cloudHostSecurityExecutorDocument struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Module     struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	} `json:"module"`
	Contract struct {
		Apply                   string   `json:"apply"`
		Credentials             string   `json:"credentials"`
		FirewallPolicy          string   `json:"firewallPolicy"`
		Generation              string   `json:"generation"`
		HardeningProfile        string   `json:"hardeningProfile"`
		Operations              []string `json:"operations"`
		ProviderLifecycle       string   `json:"providerLifecycle"`
		RuntimeEnforcement      string   `json:"runtimeEnforcement"`
		Scope                   string   `json:"scope"`
		ServerProviderAuthority string   `json:"serverProviderAuthority"`
	} `json:"contract"`
	PlanInputs json.RawMessage `json:"planInputs"`
}

// ValidateCloudHostSecurityExecutorArtifact verifies the complete generated
// contract and returns only the policy for the explicitly selected Cloud
// Site/node. Selection is caller-owned; this function never discovers or
// chooses a host.
func ValidateCloudHostSecurityExecutorArtifact(raw []byte, siteRef, nodeRef string) (CloudHostSecurityPolicy, error) {
	var document cloudHostSecurityExecutorDocument
	if err := decodeStrict(raw, &document); err != nil {
		return CloudHostSecurityPolicy{}, wrap(ErrInvalidPlan, "cloudHostSecurityArtifact", "decode exact Cloud host-security artifact", err)
	}
	spec := executorContractBundleSpecs[5]
	if document.APIVersion != "stackkit.executor-contract-bundle/v1" || document.Kind != "ExecutorContractBundle" ||
		document.Module.ID != spec.moduleID || document.Module.Version != spec.moduleVersion ||
		document.Contract.Apply != "typed-local-operations" || document.Contract.Credentials != "not-included" ||
		document.Contract.FirewallPolicy != "default-deny-declared-services-only" || document.Contract.Generation != "supported" ||
		document.Contract.HardeningProfile != "internet-host-baseline-v1" ||
		!exactStringList(document.Contract.Operations, []string{"apply-cloud-host-firewall", "apply-cloud-host-hardening", "verify-cloud-host-security"}) ||
		document.Contract.ProviderLifecycle != "not-owned" || document.Contract.RuntimeEnforcement != "adapter-verified" ||
		document.Contract.Scope != "cloud-host-node" || document.Contract.ServerProviderAuthority != "not-owned" {
		return CloudHostSecurityPolicy{}, fail(ErrInvalidPlan, "cloudHostSecurityArtifact.contract", "artifact widens or contradicts the typed Cloud host-security authority")
	}
	decoded, err := decodeCloudRuntimeExecutorPlan(document.PlanInputs, "cloudHostSecurityArtifact.planInputs", spec)
	if err != nil {
		return CloudHostSecurityPolicy{}, err
	}
	plan := decoded.(cloudRuntimeExecutorPlan)
	var selected executorBundleTarget
	found := 0
	for _, target := range plan.ModuleTargets {
		if target.SiteRef == siteRef && target.ID == nodeRef {
			selected = target
			found++
		}
	}
	if found != 1 {
		return CloudHostSecurityPolicy{}, fail(ErrInvalidPlan, "cloudHostSecurityArtifact.planInputs.moduleTargets", "must contain exactly one explicitly bound Cloud Site/node target")
	}
	return CloudHostSecurityPolicy{
		StackID: plan.StackID, KitSlug: plan.Kit.Slug, SiteRef: selected.SiteRef, NodeRef: selected.ID,
		Roles: append([]string(nil), selected.Roles...), NetworkMode: plan.CloudNetworkPolicy.Mode,
		TransportSubnet: plan.CloudNetworkPolicy.Transport.Subnet, IPv6: plan.CloudNetworkPolicy.Transport.IPv6,
		TLSMinVersion: plan.CloudNetworkPolicy.TLS.MinVersion,
	}, nil
}

// CloudPublicEdgeExecutorBundleRendererContract returns the exact
// generation-only Cloud public-edge handoff identity.
func CloudPublicEdgeExecutorBundleRendererContract() RendererContract {
	return newExecutorContractBundleRenderer(executorContractBundleSpecs[6]).contract
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

func registerExecutorContractBundleRenderers(registry *Registry) error {
	for _, spec := range executorContractBundleSpecs {
		// The shared Core runtime umbrella is no longer a catalog authority.
		// Keep its decoder temporarily for isolated migration diagnostics, but
		// never expose it through the product renderer registry.
		if spec.moduleID == coreRuntimeModuleID || spec.moduleID == localRuntimeModuleID || spec.moduleID == modernHomeRuntimeModuleID {
			continue
		}
		renderer := newExecutorContractBundleRenderer(spec)
		if err := registry.Register(renderer.contract, renderer); err != nil {
			return err
		}
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
	if unit.RuntimeKind() != "host" || unit.RuntimeDelivery() != "stackkit" || unit.InstanceScope() != "module" || unit.InstanceID() != executorContractBundleUnitID+"-logical" {
		return fail(ErrInvalidPlan, path+".instances", "executor contract requires exact host/stackkit module-single ownership")
	}
	if _, present := unit.SiteRef(); present {
		return fail(ErrInvalidPlan, path+".instances", "module-scoped executor contract must not receive a site binding")
	}
	if _, present := unit.NodeRef(); present {
		return fail(ErrInvalidPlan, path+".instances", "module-scoped executor contract must not receive a node binding")
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
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "module" || placement.Cardinality != "single" {
		return fail(ErrInvalidPlan, path+".placement", "executor contract requires exact module/single placement")
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
	StackID                    string                      `json:"stackId"`
	Kit                        executorBundleKit           `json:"kit"`
	Sites                      []executorBundleSite        `json:"sites"`
	ModuleTargets              []executorBundleTarget      `json:"moduleTargets"`
	ModuleCapabilities         []executorBundleCapability  `json:"moduleCapabilities"`
	ControlPlane               executorBundleControlPlane  `json:"controlPlane"`
	StoragePolicy              executorBundleStoragePolicy `json:"storagePolicy"`
	LocalNetworkPolicy         executorBundleNetworkPolicy `json:"localNetworkPolicy"`
	Data                       executorBundleData          `json:"data"`
	FailurePolicy              executorBundleFailurePolicy `json:"failurePolicy"`
	LocalReachability          homeLocalReachability       `json:"localReachability"`
	HomeAccessRequirements     json.RawMessage             `json:"homeAccessRequirements,omitempty"`
	ExternalHomeAccessBindings json.RawMessage             `json:"externalHomeAccessBindings,omitempty"`
}

func (localRuntimeExecutorPlan) executorContractPlanMarker() {}

type cloudRuntimeExecutorPlan struct {
	StackID            string                      `json:"stackId"`
	Kit                executorBundleKit           `json:"kit"`
	Sites              []executorBundleSite        `json:"sites"`
	ModuleTargets      []executorBundleTarget      `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability  `json:"moduleCapabilities"`
	ControlPlane       executorBundleControlPlane  `json:"controlPlane"`
	StoragePolicy      executorBundleStoragePolicy `json:"storagePolicy"`
	CloudNetworkPolicy executorBundleNetworkPolicy `json:"cloudNetworkPolicy"`
	Data               executorBundleData          `json:"data"`
	FailurePolicy      executorBundleFailurePolicy `json:"failurePolicy"`
}

func (cloudRuntimeExecutorPlan) executorContractPlanMarker() {}

type federationRuntimeExecutorPlan struct {
	StackID            string                        `json:"stackId"`
	Kit                executorBundleKit             `json:"kit"`
	Sites              []executorBundleSite          `json:"sites"`
	ModuleTargets      []executorBundleTarget        `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability    `json:"moduleCapabilities"`
	ControlPlane       executorBundleControlPlane    `json:"controlPlane"`
	Bridge             json.RawMessage               `json:"bridge"`
	Identity           modernFederationIdentity      `json:"identity"`
	Data               json.RawMessage               `json:"data"`
	FailurePolicy      modernFederationFailurePolicy `json:"failurePolicy"`
}

func (federationRuntimeExecutorPlan) executorContractPlanMarker() {}

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

func decodeLocalRuntimeExecutorPlan(raw []byte, path string, spec executorContractBundleSpec) (executorContractPlan, error) {
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
	return plan, nil
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
	var plan cloudRuntimeExecutorPlan
	if err := decodeStrict(raw, &plan); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact Cloud executor contract", err)
	}
	if err := validateExecutorContractPlanCommon(plan.StackID, plan.Kit, plan.Sites, plan.ModuleTargets, plan.ModuleCapabilities, plan.ControlPlane, spec, path); err != nil {
		return nil, err
	}
	if err := validateStoragePolicy(plan.StoragePolicy, path+".storagePolicy"); err != nil {
		return nil, err
	}
	if err := validateExecutorBundleNetworkPolicy(plan.CloudNetworkPolicy, plan.Kit.Slug, true, path+".cloudNetworkPolicy"); err != nil {
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

func decodeFederationRuntimeExecutorPlan(raw []byte, path string, spec executorContractBundleSpec) (executorContractPlan, error) {
	var plan federationRuntimeExecutorPlan
	if err := decodeStrict(raw, &plan); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact Federation executor contract", err)
	}
	if err := validateExecutorContractPlanCommon(plan.StackID, plan.Kit, plan.Sites, plan.ModuleTargets, plan.ModuleCapabilities, plan.ControlPlane, spec, path); err != nil {
		return nil, err
	}
	policyInputs := modernFederationPlanInputs{
		StackID:      plan.StackID,
		Kit:          modernFederationKit{Slug: plan.Kit.Slug, Version: plan.Kit.Version, DefinitionHash: plan.Kit.DefinitionHash},
		ControlPlane: modernFederationControlPlane{Mode: plan.ControlPlane.Mode, AuthoritySiteRef: plan.ControlPlane.AuthoritySiteRef, Members: append([]string(nil), plan.ControlPlane.Members...)},
		Bridge:       append(json.RawMessage(nil), plan.Bridge...), Identity: plan.Identity,
		Data: append(json.RawMessage(nil), plan.Data...), FailurePolicy: plan.FailurePolicy,
	}
	for _, site := range plan.Sites {
		policyInputs.Sites = append(policyInputs.Sites, modernFederationSite{ID: site.ID, Kind: site.Kind, FailureDomain: site.FailureDomain})
	}
	canonical, err := json.Marshal(policyInputs)
	if err != nil {
		return nil, wrap(ErrRendererFailure, path, "marshal Federation policy validation projection", err)
	}
	if _, err := validateModernFederationPlanInputs(canonical, path); err != nil {
		return nil, err
	}
	return plan, nil
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
	if spec.siteKind != "" {
		for _, site := range sites {
			if site.Kind != spec.siteKind {
				return fail(ErrInvalidPlan, path+".sites", "%s executor contract accepts only %s Sites", spec.moduleID, spec.siteKind)
			}
		}
	}
	modernSitesInvalid := kit.Slug == "modern-homelab" && (spec.siteKind == "" && (!containsExecutorBundleSiteKind(siteKinds, "home") || !containsExecutorBundleSiteKind(siteKinds, "cloud")) || spec.siteKind != "" && !exactExecutorBundleSiteKinds(siteKinds, spec.siteKind))
	if kit.Slug == "basement-kit" && !exactExecutorBundleSiteKinds(siteKinds, "home") || kit.Slug == "cloud-kit" && !exactExecutorBundleSiteKinds(siteKinds, "cloud") || modernSitesInvalid {
		return fail(ErrInvalidPlan, path+".sites", "site kinds contradict the selected kit")
	}
	if err := validateExecutorBundleTargets(targets, siteKinds, path+".moduleTargets"); err != nil {
		return err
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
		if !containsExecutorBundleString(targetIDs, member) {
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
