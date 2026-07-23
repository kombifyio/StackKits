package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"reflect"
	"sort"
)

const cloudHostSecurityToken = "@@POLICY@@"

const cloudHostSecurityTemplate = `{"apiVersion":"stackkit.cloud-host-security-policy/v1","kind":"CloudHostSecurityPolicy","contract":{"apply":"typed-local-operations","credentials":"not-included","firewallPolicy":"default-deny-declared-services-only","generation":"supported","hardeningProfile":"internet-host-baseline-v1","operations":["apply-cloud-host-firewall","apply-cloud-host-hardening","verify-cloud-host-security"],"providerLifecycle":"not-owned","runtimeEnforcement":"adapter-verified","scope":"cloud-host-node","serverProviderAuthority":"not-owned"},"policy":@@POLICY@@}
`

var cloudHostSecurityPlanInputRefs = []string{
	"controlPlane", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId",
}

var cloudHostSecurityPublicInputRefs = []string{"host-security-network"}

type cloudHostSecurityRenderer struct {
	template []byte
	contract RendererContract
}

func newCloudHostSecurityRenderer() cloudHostSecurityRenderer {
	sum := sha256.Sum256([]byte(cloudHostSecurityTemplate))
	return cloudHostSecurityRenderer{
		template: []byte(cloudHostSecurityTemplate),
		contract: RendererContract{
			Kind: "native-config", RendererRef: executorContractBundleRendererRef,
			TemplateRef: cloudHostSecurityTemplateRef, Version: cloudHostSecurityModuleVersion,
			ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
		},
	}
}

func (r cloudHostSecurityRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	policy, err := validateCloudHostSecurityUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(policy)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.cloud-host-security.policy", "marshal typed node policy", err)
	}
	if executorContractBundleTemplateHash(r.template) != r.contract.ContractHash || bytes.Count(r.template, []byte(cloudHostSecurityToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.cloud-host-security.template", "embedded host-security policy does not match its registered contract")
	}
	return []UnitOutput{{
		Ref:   cloudHostSecurityOutputRef,
		Bytes: bytes.Replace(r.template, []byte(cloudHostSecurityToken), canonical, 1),
	}}, nil
}

type cloudHostSecurityPlanInputs struct {
	StackID            string                     `json:"stackId"`
	Kit                executorBundleKit          `json:"kit"`
	Sites              []executorBundleSite       `json:"sites"`
	ModuleTargets      []executorBundleTarget     `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability `json:"moduleCapabilities"`
	ControlPlane       executorBundleControlPlane `json:"controlPlane"`
}

type cloudHostSecurityNetworkInput struct {
	NetworkMode     string `json:"networkMode"`
	TransportSubnet string `json:"transportSubnet"`
	IPv6            bool   `json:"ipv6"`
	TLSMinVersion   string `json:"tlsMinVersion"`
}

type cloudHostSecurityValues struct {
	Network cloudHostSecurityNetworkInput `json:"host-security-network"`
}

// CloudHostSecurityPolicy is the complete operation-shaped runtime custody
// document. It deliberately excludes plan topology, hardware, DNS, storage,
// data, failure, endpoint, credential and provider lifecycle authority.
type CloudHostSecurityPolicy struct {
	StackID         string   `json:"stackId"`
	KitSlug         string   `json:"kitSlug"`
	SiteRef         string   `json:"siteRef"`
	NodeRef         string   `json:"nodeRef"`
	Roles           []string `json:"roles"`
	NetworkMode     string   `json:"networkMode"`
	TransportSubnet string   `json:"transportSubnet"`
	IPv6            bool     `json:"ipv6"`
	TLSMinVersion   string   `json:"tlsMinVersion"`
}

//nolint:gocyclo // Every allowed authority class is checked at the boundary.
func validateCloudHostSecurityUnit(unit RenderUnit, contract RendererContract) (CloudHostSecurityPolicy, error) {
	path := "resolvedPlan.modules." + cloudHostSecurityModuleID + ".renderUnits." + executorContractBundleUnitID
	if unit.ModuleID() != cloudHostSecurityModuleID || unit.ID() != executorContractBundleUnitID {
		return CloudHostSecurityPolicy{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", cloudHostSecurityModuleID, executorContractBundleUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef ||
		unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return CloudHostSecurityPolicy{}, fail(ErrOutputChanged, path, "render-unit identity differs from the registered Cloud host-security contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.RuntimeKind() != "host" || unit.RuntimeDelivery() != "stackkit" || unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		unit.InstanceID() != executorContractBundleUnitID+"-node-"+nodeRef {
		return CloudHostSecurityPolicy{}, fail(ErrInvalidPlan, path+".instances", "requires one exact node-local host/stackkit target")
	}
	if _, present := unit.RuntimeEngine(); present {
		return CloudHostSecurityPolicy{}, fail(ErrInvalidPlan, path+".runtime.engine", "runtime-engine authority is forbidden")
	}
	if _, present := unit.DaemonRef(); present {
		return CloudHostSecurityPolicy{}, fail(ErrInvalidPlan, path+".instances", "daemon authority is forbidden")
	}
	if !exactStringList(unit.PublicInputRefs(), cloudHostSecurityPublicInputRefs) || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) {
		return CloudHostSecurityPolicy{}, fail(ErrInvalidPlan, path+".inputs", "accepts only the exact typed network input")
	}
	if !exactStringList(unit.PlanInputRefs(), cloudHostSecurityPlanInputRefs) {
		return CloudHostSecurityPolicy{}, fail(ErrInvalidPlan, path+".planInputRefs", "must exactly match the bounded validation envelope")
	}
	if err := validateCloudHostSecurityBinding(unit.InputBindingsJSON(), path+".inputBindings"); err != nil {
		return CloudHostSecurityPolicy{}, err
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return CloudHostSecurityPolicy{}, fail(ErrInvalidPlan, path+".interfaces", "service, network, socket and privileged-interface authority is forbidden")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return CloudHostSecurityPolicy{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != cloudHostSecurityOutputRef {
		return CloudHostSecurityPolicy{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", cloudHostSecurityOutputRef)
	}
	var inputs cloudHostSecurityPlanInputs
	if err := decodeStrict(unit.PlanInputsJSON(), &inputs); err != nil {
		return CloudHostSecurityPolicy{}, wrap(ErrInvalidPlan, path+".planInputs", "decode bounded Cloud host-security inputs", err)
	}
	spec := executorContractBundleSpecs[5]
	if err := validateExecutorContractPlanCommon(inputs.StackID, inputs.Kit, inputs.Sites, inputs.ModuleTargets, inputs.ModuleCapabilities, inputs.ControlPlane, spec, path+".planInputs"); err != nil {
		return CloudHostSecurityPolicy{}, err
	}
	var values cloudHostSecurityValues
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return CloudHostSecurityPolicy{}, wrap(ErrInvalidPlan, path+".values", "decode exact Cloud host-security binding", err)
	}
	network, err := validateCloudHostSecurityNetworkInput(values.Network, inputs.Kit.Slug, path+".values.host-security-network")
	if err != nil {
		return CloudHostSecurityPolicy{}, err
	}
	var selected executorBundleTarget
	matches := 0
	for _, target := range inputs.ModuleTargets {
		if target.SiteRef == siteRef && target.ID == nodeRef {
			selected = target
			matches++
		}
	}
	if matches != 1 {
		return CloudHostSecurityPolicy{}, fail(ErrInvalidPlan, path+".planInputs.moduleTargets", "instance is not bound to exactly one declared module target")
	}
	roles := append([]string(nil), selected.Roles...)
	sort.Strings(roles)
	return CloudHostSecurityPolicy{
		StackID: inputs.StackID, KitSlug: inputs.Kit.Slug, SiteRef: siteRef, NodeRef: nodeRef, Roles: roles,
		NetworkMode: network.NetworkMode, TransportSubnet: network.TransportSubnet, IPv6: network.IPv6, TLSMinVersion: network.TLSMinVersion,
	}, nil
}

func validateCloudHostSecurityBinding(raw []byte, path string) error {
	var bindings []rawModuleRenderInputBinding
	if err := decodeStrict(raw, &bindings); err != nil {
		return wrap(ErrInvalidPlan, path, "decode Cloud host-security input binding", err)
	}
	expected := []rawModuleRenderInputBinding{{
		TargetRef: "host-security-network", SourceRef: "network.cloudHostSecurity",
		ValueType: "cloud-host-security-network-v1", Cardinality: "single", Required: true,
	}}
	if !reflect.DeepEqual(bindings, expected) {
		return fail(ErrInvalidPlan, path, "must exactly match the governed Cloud host-security network binding")
	}
	return nil
}

func decodeCloudHostSecurityNetworkInput(raw json.RawMessage, path string) (cloudHostSecurityNetworkInput, error) {
	var value cloudHostSecurityNetworkInput
	if err := decodeStrict(raw, &value); err != nil {
		return value, wrap(ErrInvalidPlan, path, "decode Cloud host-security network", err)
	}
	return validateCloudHostSecurityNetworkInput(value, "", path)
}

func validateCloudHostSecurityNetworkInput(value cloudHostSecurityNetworkInput, kitSlug, path string) (cloudHostSecurityNetworkInput, error) {
	if value.NetworkMode != "public-capable" && value.NetworkMode != "hybrid" {
		return value, fail(ErrInvalidPlan, path+".networkMode", "requires public-capable or hybrid")
	}
	if kitSlug != "" {
		expected := "public-capable"
		if kitSlug == "modern-homelab" {
			expected = "hybrid"
		} else if kitSlug != "cloud-kit" {
			return value, fail(ErrInvalidPlan, path+".networkMode", "kit %s has no Cloud host-security projection", kitSlug)
		}
		if value.NetworkMode != expected {
			return value, fail(ErrInvalidPlan, path+".networkMode", "kit %s requires %s", kitSlug, expected)
		}
	}
	prefix, err := netip.ParsePrefix(value.TransportSubnet)
	if err != nil || prefix.String() != value.TransportSubnet {
		return value, fail(ErrInvalidPlan, path+".transportSubnet", "requires a canonical CIDR")
	}
	if value.TLSMinVersion != "TLS1.2" && value.TLSMinVersion != "TLS1.3" {
		return value, fail(ErrInvalidPlan, path+".tlsMinVersion", "requires TLS1.2 or TLS1.3")
	}
	return value, nil
}

type cloudHostSecurityExecutorDocument struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Contract   struct {
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
	Policy CloudHostSecurityPolicy `json:"policy"`
}

// ValidateCloudHostSecurityExecutorArtifact accepts only the operation-shaped
// policy for the explicitly selected target. It never discovers or chooses a
// host and cannot recover discarded compiler authority from the artifact.
func ValidateCloudHostSecurityExecutorArtifact(raw []byte, siteRef, nodeRef string) (CloudHostSecurityPolicy, error) {
	var document cloudHostSecurityExecutorDocument
	if err := decodeStrict(raw, &document); err != nil {
		return CloudHostSecurityPolicy{}, wrap(ErrInvalidPlan, "cloudHostSecurityArtifact", "decode exact Cloud host-security artifact", err)
	}
	if document.APIVersion != "stackkit.cloud-host-security-policy/v1" || document.Kind != "CloudHostSecurityPolicy" ||
		document.Contract.Apply != "typed-local-operations" || document.Contract.Credentials != "not-included" ||
		document.Contract.FirewallPolicy != "default-deny-declared-services-only" || document.Contract.Generation != "supported" ||
		document.Contract.HardeningProfile != "internet-host-baseline-v1" ||
		!exactStringList(document.Contract.Operations, []string{"apply-cloud-host-firewall", "apply-cloud-host-hardening", "verify-cloud-host-security"}) ||
		document.Contract.ProviderLifecycle != "not-owned" || document.Contract.RuntimeEnforcement != "adapter-verified" ||
		document.Contract.Scope != "cloud-host-node" || document.Contract.ServerProviderAuthority != "not-owned" {
		return CloudHostSecurityPolicy{}, fail(ErrInvalidPlan, "cloudHostSecurityArtifact.contract", "artifact widens or contradicts the typed Cloud host-security authority")
	}
	if document.Policy.StackID == "" || document.Policy.SiteRef != siteRef || document.Policy.NodeRef != nodeRef || len(document.Policy.Roles) == 0 {
		return CloudHostSecurityPolicy{}, fail(ErrInvalidPlan, "cloudHostSecurityArtifact.policy", "policy is not bound to the exact requested target")
	}
	if !sort.StringsAreSorted(document.Policy.Roles) {
		return CloudHostSecurityPolicy{}, fail(ErrInvalidPlan, "cloudHostSecurityArtifact.policy.roles", "roles must be canonical")
	}
	if _, err := validateCloudHostSecurityNetworkInput(cloudHostSecurityNetworkInput{
		NetworkMode: document.Policy.NetworkMode, TransportSubnet: document.Policy.TransportSubnet,
		IPv6: document.Policy.IPv6, TLSMinVersion: document.Policy.TLSMinVersion,
	}, document.Policy.KitSlug, "cloudHostSecurityArtifact.policy"); err != nil {
		return CloudHostSecurityPolicy{}, err
	}
	return document.Policy, nil
}
