package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

const (
	localAutonomyPolicyModuleID    = "stackkits-local-autonomy-policy-manifest"
	localAutonomyPolicyUnitID      = "policy-bundle"
	localAutonomyPolicyRendererRef = "stackkit"
	localAutonomyPolicyTemplateRef = "builtin://home/local-autonomy/v1.json"
	localAutonomyPolicyVersion     = "1.0.0"
	localAutonomyPolicyOutputRef   = "local/autonomy/policy.json"
	localAutonomyPolicyToken       = "@@POLICY@@"
)

const localAutonomyPolicyTemplate = `{"apiVersion":"stackkit.local-autonomy-policy/v1","kind":"LocalAutonomyPolicy","contract":{"airGappedInstallation":"not-included","capability":"offline-autonomy","providerLifecycle":"not-owned","runtimeEnforcement":"adapter-verified","scope":"home-control-node"},"policy":@@POLICY@@}
`

var localAutonomyPolicyPlanInputRefs = []string{}
var localAutonomyPolicyPublicInputRefs = []string{"local-autonomy-policy"}

// The shared types below remain the bounded input vocabulary for adjacent
// Home/Modern policy renderers. The local-autonomy renderer itself no longer
// receives these coarse plan objects.
type localAutonomyKit struct {
	Slug           string `json:"slug"`
	Version        string `json:"version"`
	DefinitionHash string `json:"definitionHash"`
}

type localAutonomySite struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	FailureDomain string `json:"failureDomain"`
}

type localAutonomyControlPlane struct {
	Mode             string   `json:"mode"`
	AuthoritySiteRef string   `json:"authoritySiteRef"`
	Members          []string `json:"members"`
}

type localAutonomyIdentity struct {
	HumanAuthoritySiteRef   string                        `json:"humanAuthoritySiteRef"`
	DeviceAuthoritySiteRef  string                        `json:"deviceAuthoritySiteRef"`
	EdgeVerifierSiteRefs    []string                      `json:"edgeVerifierSiteRefs,omitempty"`
	DeviceEnrollment        localAutonomyDeviceEnrollment `json:"deviceEnrollment"`
	PossessionBoundSessions bool                          `json:"possessionBoundSessions"`
	LANLocationIsIdentity   bool                          `json:"lanLocationIsIdentity"`
}

type localAutonomyDeviceEnrollment struct {
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

type localAutonomyData struct {
	DefaultAuthority string                              `json:"defaultAuthority"`
	Bindings         map[string]localAutonomyDataBinding `json:"bindings,omitempty"`
}

type localAutonomyDataBinding struct {
	Classes          []string                      `json:"classes"`
	PrimarySiteRef   string                        `json:"primarySiteRef"`
	ReplicaSiteRefs  []string                      `json:"replicaSiteRefs"`
	CloudCopyAllowed bool                          `json:"cloudCopyAllowed"`
	CloudCopyPolicy  *localAutonomyCloudCopyPolicy `json:"cloudCopyPolicy,omitempty"`
}

type localAutonomyCloudCopyPolicy struct {
	PolicyRef      string   `json:"policyRef"`
	AllowedClasses []string `json:"allowedClasses"`
	AllowPrimary   bool     `json:"allowPrimary"`
	AllowReplicas  bool     `json:"allowReplicas"`
}

type localAutonomyFailurePolicy struct {
	OnCloudLoss                     string `json:"onCloudLoss"`
	OnLinkLoss                      string `json:"onLinkLoss"`
	CloudEdge                       string `json:"cloudEdge"`
	LocalIdentityAuthorityAvailable bool   `json:"localIdentityAuthorityAvailable"`
	MaxStaleVerificationSeconds     int    `json:"maxStaleVerificationSeconds"`
	DenyNewCrossSiteSessions        bool   `json:"denyNewCrossSiteSessions"`
}

type localAutonomyPolicyInput struct {
	StackID  string `json:"stackId"`
	KitSlug  string `json:"kitSlug"`
	Topology struct {
		AuthorityHomeSiteRef string   `json:"authorityHomeSiteRef"`
		CloudSiteRefs        []string `json:"cloudSiteRefs"`
	} `json:"topology"`
	Control struct {
		Mode             string   `json:"mode"`
		AuthoritySiteRef string   `json:"authoritySiteRef"`
		MemberNodeRefs   []string `json:"memberNodeRefs"`
	} `json:"control"`
	Identity struct {
		AuthoritySiteRef         string   `json:"authoritySiteRef"`
		EnrollmentMode           string   `json:"enrollmentMode"`
		EdgeVerifierSiteRefs     []string `json:"edgeVerifierSiteRefs"`
		PossessionBoundSessions  bool     `json:"possessionBoundSessions"`
		LANLocationIsIdentity    bool     `json:"lanLocationIsIdentity"`
		AvailableDuringPartition bool     `json:"availableDuringPartition"`
	} `json:"identity"`
	Data struct {
		DefaultAuthoritySiteRef string                                `json:"defaultAuthoritySiteRef"`
		Bindings                []LocalAutonomyEnforcementDataBinding `json:"bindings"`
	} `json:"data"`
	Failure LocalAutonomyFailureDecision `json:"failure"`
}

type localAutonomyValues struct {
	Policy localAutonomyPolicyInput `json:"local-autonomy-policy"`
}

type LocalAutonomyFailureDecision struct {
	OnCloudLoss                 string `json:"onCloudLoss"`
	OnLinkLoss                  string `json:"onLinkLoss"`
	CloudEdge                   string `json:"cloudEdge"`
	MaxStaleVerificationSeconds int    `json:"maxStaleVerificationSeconds"`
	DenyNewCrossSiteSessions    bool   `json:"denyNewCrossSiteSessions"`
}

type LocalAutonomyEnforcementDataBinding struct {
	BindingRef         string   `json:"bindingRef"`
	PrimarySiteRef     string   `json:"primarySiteRef"`
	ReplicaSiteRefs    []string `json:"replicaSiteRefs"`
	CloudPlacement     string   `json:"cloudPlacement"`
	CloudCopyPolicyRef string   `json:"cloudCopyPolicyRef,omitempty"`
}

// LocalAutonomyEnforcementPolicy is the exact operation-shaped custody of one
// Home control node. It cannot name provider resources, endpoints, credentials,
// arbitrary LAN reachability, enrollment ceremony, or data classes.
type LocalAutonomyEnforcementPolicy struct {
	StackID                         string                                `json:"stackId"`
	KitSlug                         string                                `json:"kitSlug"`
	SiteRef                         string                                `json:"siteRef"`
	NodeRef                         string                                `json:"nodeRef"`
	CloudSiteRefs                   []string                              `json:"cloudSiteRefs"`
	ControlMode                     string                                `json:"controlMode"`
	ControlMembers                  []string                              `json:"controlMembers"`
	EdgeVerifierSiteRefs            []string                              `json:"edgeVerifierSiteRefs"`
	DataDefaultAuthority            string                                `json:"dataDefaultAuthority"`
	DataBindings                    []LocalAutonomyEnforcementDataBinding `json:"dataBindings"`
	OnCloudLoss                     string                                `json:"onCloudLoss"`
	OnLinkLoss                      string                                `json:"onLinkLoss"`
	CloudEdge                       string                                `json:"cloudEdge"`
	LocalIdentityAuthorityAvailable bool                                  `json:"localIdentityAuthorityAvailable"`
	MaxStaleVerificationSeconds     int                                   `json:"maxStaleVerificationSeconds"`
	DenyNewCrossSiteSessions        bool                                  `json:"denyNewCrossSiteSessions"`
}

type localAutonomyPolicyRenderer struct {
	template []byte
	contract RendererContract
}

func newLocalAutonomyPolicyRenderer() localAutonomyPolicyRenderer {
	return localAutonomyPolicyRenderer{template: []byte(localAutonomyPolicyTemplate), contract: LocalAutonomyPolicyRendererContract()}
}

func LocalAutonomyPolicyRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(localAutonomyPolicyTemplate))
	return RendererContract{
		Kind: "native-config", RendererRef: localAutonomyPolicyRendererRef,
		TemplateRef: localAutonomyPolicyTemplateRef, Version: localAutonomyPolicyVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

func (r localAutonomyPolicyRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	policy, err := validateLocalAutonomyPolicyUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(policy)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.local-autonomy-policy.policy", "marshal typed local-autonomy policy", err)
	}
	if localAutonomyTemplateHash(r.template) != r.contract.ContractHash || bytes.Count(r.template, []byte(localAutonomyPolicyToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.local-autonomy-policy.template", "embedded local-autonomy policy does not match its registered contract")
	}
	return []UnitOutput{{
		Ref:   localAutonomyPolicyOutputRef,
		Bytes: bytes.Replace(r.template, []byte(localAutonomyPolicyToken), canonical, 1),
	}}, nil
}

func localAutonomyTemplateHash(template []byte) string {
	sum := sha256.Sum256(template)
	return "sha256:" + hex.EncodeToString(sum[:])
}

//nolint:gocyclo // The closed topology, identity, placement and partition tuple is one authorization boundary.
func validateLocalAutonomyPolicyUnit(unit RenderUnit, contract RendererContract) (LocalAutonomyEnforcementPolicy, error) {
	path := "resolvedPlan.modules." + localAutonomyPolicyModuleID + ".renderUnits." + localAutonomyPolicyUnitID
	if unit.ModuleID() != localAutonomyPolicyModuleID || unit.ID() != localAutonomyPolicyUnitID {
		return LocalAutonomyEnforcementPolicy{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", localAutonomyPolicyModuleID, localAutonomyPolicyUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef ||
		unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return LocalAutonomyEnforcementPolicy{}, fail(ErrOutputChanged, path, "render-unit identity differs from the registered local-autonomy contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.RuntimeKind() != "native" || unit.RuntimeDelivery() != "stackkit" || unit.InstanceScope() != "node-local" ||
		!hasSite || !hasNode || unit.InstanceID() != localAutonomyPolicyUnitID+"-node-"+nodeRef {
		return LocalAutonomyEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "requires one exact node-local Home control target")
	}
	if _, present := unit.RuntimeEngine(); present {
		return LocalAutonomyEnforcementPolicy{}, fail(ErrInvalidPlan, path+".runtime.engine", "runtime-engine authority is forbidden")
	}
	if _, present := unit.DaemonRef(); present {
		return LocalAutonomyEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "daemon authority is forbidden")
	}
	if !exactStringList(unit.PublicInputRefs(), localAutonomyPolicyPublicInputRefs) || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) {
		return LocalAutonomyEnforcementPolicy{}, fail(ErrInvalidPlan, path+".inputs", "accepts only the exact typed local-autonomy input")
	}
	if !exactStringList(unit.PlanInputRefs(), localAutonomyPolicyPlanInputRefs) || !emptyJSONObject(unit.PlanInputsJSON()) {
		return LocalAutonomyEnforcementPolicy{}, fail(ErrInvalidPlan, path+".planInputs", "coarse plan inputs are forbidden")
	}
	if err := validateLocalAutonomyPolicyBinding(unit.InputBindingsJSON(), path+".inputBindings"); err != nil {
		return LocalAutonomyEnforcementPolicy{}, err
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return LocalAutonomyEnforcementPolicy{}, fail(ErrInvalidPlan, path+".interfaces", "endpoint, socket, network and privileged-interface authority is forbidden")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return LocalAutonomyEnforcementPolicy{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != localAutonomyPolicyOutputRef {
		return LocalAutonomyEnforcementPolicy{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", localAutonomyPolicyOutputRef)
	}
	var values localAutonomyValues
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return LocalAutonomyEnforcementPolicy{}, wrap(ErrInvalidPlan, path+".values", "decode exact local-autonomy input", err)
	}
	input, err := validateLocalAutonomyPolicyInput(values.Policy, path+".values.local-autonomy-policy")
	if err != nil {
		return LocalAutonomyEnforcementPolicy{}, err
	}
	if input.Topology.AuthorityHomeSiteRef != siteRef || input.Control.AuthoritySiteRef != siteRef ||
		input.Identity.AuthoritySiteRef != siteRef || input.Data.DefaultAuthoritySiteRef != siteRef ||
		!containsExact(input.Control.MemberNodeRefs, nodeRef) ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !containsExact(unit.LogicalNodeRefs(), nodeRef) {
		return LocalAutonomyEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "instance must bind the exact Home authority Site and one declared control member")
	}
	return localAutonomyOperationPolicy(input, siteRef, nodeRef), nil
}

func validateLocalAutonomyPolicyBinding(raw []byte, path string) error {
	var bindings []rawModuleRenderInputBinding
	if err := decodeStrict(raw, &bindings); err != nil {
		return wrap(ErrInvalidPlan, path, "decode local-autonomy input binding", err)
	}
	expected := []rawModuleRenderInputBinding{{
		TargetRef: "local-autonomy-policy", SourceRef: "localAutonomy.policy",
		ValueType: "local-autonomy-policy-v1", Cardinality: "single", Required: true,
	}}
	if !reflect.DeepEqual(bindings, expected) {
		return fail(ErrInvalidPlan, path, "must exactly match the governed local-autonomy binding")
	}
	return nil
}

//nolint:gocyclo // Basement and Modern partition invariants must be accepted or rejected atomically.
func validateLocalAutonomyPolicyInput(input localAutonomyPolicyInput, path string) (localAutonomyPolicyInput, error) {
	if err := requireContractID(input.StackID, path+".stackId"); err != nil {
		return input, err
	}
	if input.KitSlug != "basement-kit" && input.KitSlug != "modern-homelab" {
		return input, fail(ErrInvalidPlan, path+".kitSlug", "local autonomy is unavailable to kit %q", input.KitSlug)
	}
	authority := input.Topology.AuthorityHomeSiteRef
	if err := requireContractID(authority, path+".topology.authorityHomeSiteRef"); err != nil {
		return input, err
	}
	if input.Control.AuthoritySiteRef != authority || input.Identity.AuthoritySiteRef != authority ||
		input.Data.DefaultAuthoritySiteRef != authority {
		return input, fail(ErrInvalidPlan, path, "control, identity and data authority must equal the exact Home authority Site")
	}
	if input.Identity.EnrollmentMode != "local-only" || !input.Identity.PossessionBoundSessions ||
		input.Identity.LANLocationIsIdentity || !input.Identity.AvailableDuringPartition {
		return input, fail(ErrInvalidPlan, path+".identity", "identity must remain Home-local, possession-bound, LAN-independent and partition-available")
	}
	if err := requireSortedUniqueContractIDs(input.Topology.CloudSiteRefs, path+".topology.cloudSiteRefs", false); err != nil {
		return input, err
	}
	if err := requireSortedUniqueContractIDs(input.Control.MemberNodeRefs, path+".control.memberNodeRefs", true); err != nil {
		return input, err
	}
	if err := requireSortedUniqueContractIDs(input.Identity.EdgeVerifierSiteRefs, path+".identity.edgeVerifierSiteRefs", false); err != nil {
		return input, err
	}
	if err := validateLocalAutonomyMode(input.Control.Mode, len(input.Control.MemberNodeRefs), path+".control"); err != nil {
		return input, err
	}
	if input.Failure.OnLinkLoss != "local-continues" || !input.Failure.DenyNewCrossSiteSessions ||
		input.Failure.MaxStaleVerificationSeconds < 0 {
		return input, fail(ErrInvalidPlan, path+".failure", "link loss must preserve local authority and deny new cross-Site sessions")
	}
	if input.KitSlug == "basement-kit" {
		if len(input.Topology.CloudSiteRefs) != 0 || len(input.Identity.EdgeVerifierSiteRefs) != 0 ||
			input.Failure.OnCloudLoss != "not-applicable" || input.Failure.CloudEdge != "not-applicable" ||
			input.Failure.MaxStaleVerificationSeconds != 0 {
			return input, fail(ErrInvalidPlan, path, "Basement local autonomy cannot carry Cloud or stale-verification authority")
		}
	} else if len(input.Topology.CloudSiteRefs) == 0 ||
		!exactStringList(input.Identity.EdgeVerifierSiteRefs, input.Topology.CloudSiteRefs) ||
		input.Failure.OnCloudLoss != "local-continues" || input.Failure.CloudEdge != "fail-closed" {
		return input, fail(ErrInvalidPlan, path, "Modern local autonomy requires exact Cloud verifier coverage and a fail-closed Cloud edge")
	}
	previousBinding := ""
	for index, binding := range input.Data.Bindings {
		bindingPath := fmt.Sprintf("%s.data.bindings[%d]", path, index)
		if err := requireContractID(binding.BindingRef, bindingPath+".bindingRef"); err != nil {
			return input, err
		}
		if previousBinding != "" && binding.BindingRef <= previousBinding {
			return input, fail(ErrInvalidPlan, bindingPath+".bindingRef", "bindings must be unique and sorted")
		}
		previousBinding = binding.BindingRef
		if err := requireContractID(binding.PrimarySiteRef, bindingPath+".primarySiteRef"); err != nil {
			return input, err
		}
		if err := requireSortedUniqueContractIDs(binding.ReplicaSiteRefs, bindingPath+".replicaSiteRefs", false); err != nil {
			return input, err
		}
		if binding.CloudPlacement == "denied" {
			if binding.CloudCopyPolicyRef != "" {
				return input, fail(ErrInvalidPlan, bindingPath, "denied Cloud placement cannot carry a policy reference")
			}
			if binding.PrimarySiteRef != authority {
				return input, fail(ErrInvalidPlan, bindingPath+".primarySiteRef", "denied Cloud placement must remain Home-primary")
			}
			for _, replica := range binding.ReplicaSiteRefs {
				if containsExact(input.Topology.CloudSiteRefs, replica) {
					return input, fail(ErrInvalidPlan, bindingPath+".replicaSiteRefs", "denied Cloud placement cannot contain Cloud replicas")
				}
			}
		} else if binding.CloudPlacement != "policy-authorized" || binding.CloudCopyPolicyRef == "" {
			return input, fail(ErrInvalidPlan, bindingPath, "Cloud placement must be denied or carry one explicit policy reference")
		}
	}
	return input, nil
}

func localAutonomyOperationPolicy(input localAutonomyPolicyInput, siteRef, nodeRef string) LocalAutonomyEnforcementPolicy {
	return LocalAutonomyEnforcementPolicy{
		StackID: input.StackID, KitSlug: input.KitSlug, SiteRef: siteRef, NodeRef: nodeRef,
		CloudSiteRefs: append([]string(nil), input.Topology.CloudSiteRefs...),
		ControlMode:   input.Control.Mode, ControlMembers: append([]string(nil), input.Control.MemberNodeRefs...),
		EdgeVerifierSiteRefs: append([]string(nil), input.Identity.EdgeVerifierSiteRefs...),
		DataDefaultAuthority: input.Data.DefaultAuthoritySiteRef,
		DataBindings:         cloneLocalAutonomyDataBindings(input.Data.Bindings),
		OnCloudLoss:          input.Failure.OnCloudLoss, OnLinkLoss: input.Failure.OnLinkLoss, CloudEdge: input.Failure.CloudEdge,
		LocalIdentityAuthorityAvailable: input.Identity.AvailableDuringPartition,
		MaxStaleVerificationSeconds:     input.Failure.MaxStaleVerificationSeconds,
		DenyNewCrossSiteSessions:        input.Failure.DenyNewCrossSiteSessions,
	}
}

type localAutonomyPolicyArtifact struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Contract   struct {
		AirGappedInstallation string `json:"airGappedInstallation"`
		Capability            string `json:"capability"`
		ProviderLifecycle     string `json:"providerLifecycle"`
		RuntimeEnforcement    string `json:"runtimeEnforcement"`
		Scope                 string `json:"scope"`
	} `json:"contract"`
	Policy LocalAutonomyEnforcementPolicy `json:"policy"`
}

func ValidateLocalAutonomyPolicyArtifact(raw []byte) (LocalAutonomyEnforcementPolicy, error) {
	var document localAutonomyPolicyArtifact
	if err := decodeStrict(raw, &document); err != nil {
		return LocalAutonomyEnforcementPolicy{}, wrap(ErrInvalidPlan, "localAutonomyPolicy", "decode exact local-autonomy policy artifact", err)
	}
	if document.APIVersion != "stackkit.local-autonomy-policy/v1" || document.Kind != "LocalAutonomyPolicy" ||
		document.Contract.AirGappedInstallation != "not-included" || document.Contract.Capability != "offline-autonomy" ||
		document.Contract.ProviderLifecycle != "not-owned" || document.Contract.RuntimeEnforcement != "adapter-verified" ||
		document.Contract.Scope != "home-control-node" {
		return LocalAutonomyEnforcementPolicy{}, fail(ErrInvalidPlan, "localAutonomyPolicy.contract", "artifact widens or fabricates the local-autonomy contract")
	}
	policy := document.Policy
	if policy.SiteRef == "" || policy.NodeRef == "" || !containsExact(policy.ControlMembers, policy.NodeRef) ||
		policy.DataDefaultAuthority != policy.SiteRef || !policy.LocalIdentityAuthorityAvailable {
		return LocalAutonomyEnforcementPolicy{}, fail(ErrInvalidPlan, "localAutonomyPolicy.policy", "artifact is not bound to one exact Home control node")
	}
	input := localAutonomyPolicyInput{StackID: policy.StackID, KitSlug: policy.KitSlug}
	input.Topology.AuthorityHomeSiteRef = policy.SiteRef
	input.Topology.CloudSiteRefs = append([]string(nil), policy.CloudSiteRefs...)
	input.Control.Mode = policy.ControlMode
	input.Control.AuthoritySiteRef = policy.SiteRef
	input.Control.MemberNodeRefs = append([]string(nil), policy.ControlMembers...)
	input.Identity.AuthoritySiteRef = policy.SiteRef
	input.Identity.EnrollmentMode = "local-only"
	input.Identity.EdgeVerifierSiteRefs = append([]string(nil), policy.EdgeVerifierSiteRefs...)
	input.Identity.PossessionBoundSessions = true
	input.Identity.AvailableDuringPartition = policy.LocalIdentityAuthorityAvailable
	input.Data.DefaultAuthoritySiteRef = policy.DataDefaultAuthority
	input.Data.Bindings = cloneLocalAutonomyDataBindings(policy.DataBindings)
	input.Failure = LocalAutonomyFailureDecision{
		OnCloudLoss: policy.OnCloudLoss, OnLinkLoss: policy.OnLinkLoss, CloudEdge: policy.CloudEdge,
		MaxStaleVerificationSeconds: policy.MaxStaleVerificationSeconds,
		DenyNewCrossSiteSessions:    policy.DenyNewCrossSiteSessions,
	}
	if _, err := validateLocalAutonomyPolicyInput(input, "localAutonomyPolicy.policy"); err != nil {
		return LocalAutonomyEnforcementPolicy{}, err
	}
	return policy, nil
}

func validateLocalAutonomyMode(mode string, members int, path string) error {
	if mode == "single" && members == 1 || mode == "warm-standby" && members >= 2 ||
		mode == "quorum" && (members == 3 || members == 5 || members == 7) {
		return nil
	}
	return fail(ErrInvalidPlan, path, "member cardinality does not match control mode %q", mode)
}

func requireSortedUniqueContractIDs(values []string, path string, required bool) error {
	if required && len(values) == 0 {
		return fail(ErrInvalidPlan, path, "at least one reference is required")
	}
	if !sort.StringsAreSorted(values) {
		return fail(ErrInvalidPlan, path, "references must be sorted")
	}
	for index, value := range values {
		if err := requireContractID(value, fmt.Sprintf("%s[%d]", path, index)); err != nil {
			return err
		}
		if index > 0 && values[index-1] == value {
			return fail(ErrDuplicate, fmt.Sprintf("%s[%d]", path, index), "duplicate reference %q", value)
		}
	}
	return nil
}

func cloneLocalAutonomyDataBindings(bindings []LocalAutonomyEnforcementDataBinding) []LocalAutonomyEnforcementDataBinding {
	result := append([]LocalAutonomyEnforcementDataBinding(nil), bindings...)
	for index := range result {
		result[index].ReplicaSiteRefs = append([]string(nil), result[index].ReplicaSiteRefs...)
	}
	return result
}

// Shared validation helpers for the still-separate Modern federation and
// optional LAN-discovery renderers.
func validateLocalAutonomySites(sites []localAutonomySite, path string) (map[string]string, []string, []string, error) {
	if len(sites) == 0 {
		return nil, nil, nil, fail(ErrInvalidPlan, path+".sites", "requires at least one explicit Home Site")
	}
	siteKinds := make(map[string]string, len(sites))
	homeSiteRefs := make([]string, 0, len(sites))
	cloudSiteRefs := make([]string, 0, len(sites))
	for index, site := range sites {
		if err := requireContractID(site.ID, fmt.Sprintf("%s.sites[%d].id", path, index)); err != nil {
			return nil, nil, nil, err
		}
		if site.FailureDomain == "" || site.Kind != "home" && site.Kind != "cloud" {
			return nil, nil, nil, fail(ErrInvalidPlan, fmt.Sprintf("%s.sites[%d]", path, index), "Site projection must contain only id, kind and failureDomain")
		}
		if _, duplicate := siteKinds[site.ID]; duplicate {
			return nil, nil, nil, fail(ErrDuplicate, fmt.Sprintf("%s.sites[%d].id", path, index), "duplicate Site %q", site.ID)
		}
		siteKinds[site.ID] = site.Kind
		if site.Kind == "home" {
			homeSiteRefs = append(homeSiteRefs, site.ID)
		} else {
			cloudSiteRefs = append(cloudSiteRefs, site.ID)
		}
	}
	sort.Strings(homeSiteRefs)
	sort.Strings(cloudSiteRefs)
	return siteKinds, homeSiteRefs, cloudSiteRefs, nil
}

func validateLocalAutonomyControlPlane(control localAutonomyControlPlane, siteKinds map[string]string, path string) (string, error) {
	if siteKinds[control.AuthoritySiteRef] != "home" {
		return "", fail(ErrInvalidPlan, path+".controlPlane.authoritySiteRef", "control authority must remain Home-local")
	}
	if err := requireSortedUniqueContractIDs(control.Members, path+".controlPlane.members", true); err != nil {
		return "", err
	}
	if err := validateLocalAutonomyMode(control.Mode, len(control.Members), path+".controlPlane"); err != nil {
		return "", err
	}
	return control.AuthoritySiteRef, nil
}

func validateLocalAutonomyIdentity(identity localAutonomyIdentity, authoritySiteRef, path string) error {
	enrollment := identity.DeviceEnrollment
	if identity.HumanAuthoritySiteRef != authoritySiteRef || identity.DeviceAuthoritySiteRef != authoritySiteRef ||
		!identity.PossessionBoundSessions || identity.LANLocationIsIdentity || enrollment.Mode != "local-only" ||
		enrollment.AuthoritySiteRef != authoritySiteRef || enrollment.EndpointExposure != "lan" || enrollment.RemoteEnrollment ||
		!enrollment.RequireOwnerStepUp || !enrollment.RequireLocalPairingProof || !enrollment.RequireDeviceGeneratedKey ||
		!enrollment.RequirePossessionProof || !enrollment.RevocationSupported {
		return fail(ErrInvalidPlan, path, "identity must remain Home-authoritative, locally enrolled and possession-bound")
	}
	if _, err := uniqueLocalAutonomyIDSet(identity.EdgeVerifierSiteRefs, path+".edgeVerifierSiteRefs"); err != nil {
		return err
	}
	if enrollment.HardwareBackedKey != "preferred" && enrollment.HardwareBackedKey != "required" ||
		enrollment.CredentialTTLSeconds < 300 || enrollment.CredentialTTLSeconds > 86400 {
		return fail(ErrInvalidPlan, path+".deviceEnrollment", "device credentials require bounded lifetime and hardware-backed-key policy")
	}
	return nil
}

//nolint:gocyclo // Retained for the adjacent Modern federation projection until its own typed seam lands.
func validateLocalAutonomyData(data localAutonomyData, siteKinds map[string]string, authoritySiteRef string, allowCloudCopies bool, path string) error {
	if data.DefaultAuthority != authoritySiteRef {
		return fail(ErrInvalidPlan, path+".defaultAuthority", "default data authority must remain on the Home authority")
	}
	for ref, binding := range data.Bindings {
		bindingPath := path + ".bindings." + ref
		if err := requireContractID(ref, bindingPath); err != nil {
			return err
		}
		if err := validateLocalAutonomyDataClasses(binding.Classes, bindingPath+".classes"); err != nil {
			return err
		}
		if siteKinds[binding.PrimarySiteRef] == "" {
			return fail(ErrInvalidPlan, bindingPath+".primarySiteRef", "primary Site is not projected")
		}
		replicas, err := uniqueLocalAutonomyIDSet(binding.ReplicaSiteRefs, bindingPath+".replicaSiteRefs")
		if err != nil {
			return err
		}
		for replica := range replicas {
			if siteKinds[replica] == "" || replica == binding.PrimarySiteRef {
				return fail(ErrInvalidPlan, bindingPath+".replicaSiteRefs", "replicas must be distinct projected Sites")
			}
		}
		if !allowCloudCopies {
			if binding.PrimarySiteRef != authoritySiteRef || binding.CloudCopyAllowed || binding.CloudCopyPolicy != nil || anyLocalAutonomyCloudSite(replicas, siteKinds) {
				return fail(ErrInvalidPlan, bindingPath, "data must remain Home-primary without Cloud copies")
			}
			continue
		}
		if !binding.CloudCopyAllowed {
			if binding.CloudCopyPolicy != nil || siteKinds[binding.PrimarySiteRef] == "cloud" || anyLocalAutonomyCloudSite(replicas, siteKinds) {
				return fail(ErrInvalidPlan, bindingPath, "Cloud placement requires an explicit policy")
			}
			continue
		}
		policy := binding.CloudCopyPolicy
		if policy == nil {
			return fail(ErrInvalidPlan, bindingPath+".cloudCopyPolicy", "Cloud-copy opt-in requires a policy")
		}
		if err := requireContractID(policy.PolicyRef, bindingPath+".cloudCopyPolicy.policyRef"); err != nil {
			return err
		}
		if err := validateLocalAutonomyDataClasses(policy.AllowedClasses, bindingPath+".cloudCopyPolicy.allowedClasses"); err != nil {
			return err
		}
		for _, class := range binding.Classes {
			if !stringListContains(policy.AllowedClasses, class) {
				return fail(ErrInvalidPlan, bindingPath+".cloudCopyPolicy.allowedClasses", "policy does not cover class %q", class)
			}
		}
		if siteKinds[binding.PrimarySiteRef] == "cloud" && !policy.AllowPrimary || anyLocalAutonomyCloudSite(replicas, siteKinds) && !policy.AllowReplicas {
			return fail(ErrInvalidPlan, bindingPath+".cloudCopyPolicy", "policy does not authorize requested Cloud placement")
		}
	}
	return nil
}

func uniqueLocalAutonomyIDSet(values []string, path string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(values))
	for index, value := range values {
		if err := requireContractID(value, fmt.Sprintf("%s[%d]", path, index)); err != nil {
			return nil, err
		}
		if _, duplicate := result[value]; duplicate {
			return nil, fail(ErrDuplicate, fmt.Sprintf("%s[%d]", path, index), "duplicate reference %q", value)
		}
		result[value] = struct{}{}
	}
	return result, nil
}

func validateLocalAutonomyDataClasses(classes []string, path string) error {
	if len(classes) == 0 {
		return fail(ErrInvalidPlan, path, "at least one data class is required")
	}
	seen := map[string]struct{}{}
	for index, class := range classes {
		if !stringListContains([]string{"public", "internal", "personal", "sensitive", "secret"}, class) {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s[%d]", path, index), "unsupported data class")
		}
		if _, duplicate := seen[class]; duplicate {
			return fail(ErrDuplicate, fmt.Sprintf("%s[%d]", path, index), "duplicate data class")
		}
		seen[class] = struct{}{}
	}
	return nil
}

func sameSortedStrings(left, right []string) bool {
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	return exactStringList(left, right)
}

func anyLocalAutonomyCloudSite(refs map[string]struct{}, siteKinds map[string]string) bool {
	for ref := range refs {
		if siteKinds[ref] == "cloud" {
			return true
		}
	}
	return false
}

var _ UnitRenderer = localAutonomyPolicyRenderer{}
