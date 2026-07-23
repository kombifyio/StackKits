package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

const (
	homeAccessPolicyModuleID    = "stackkits-home-access-policy-manifest"
	homeAccessPolicyUnitID      = "policy-bundle"
	homeAccessPolicyRendererRef = "stackkit"
	homeAccessPolicyTemplateRef = "builtin://home/access/v1.json"
	homeAccessPolicyVersion     = "1.0.0"
	homeAccessPolicyOutputRef   = "local/network/access-policy.json"
	homeAccessPolicyToken       = "@@POLICY@@"
)

const homeAccessPolicyTemplate = `{"apiVersion":"stackkit.home-access-policy/v1","kind":"HomeAccessPolicy","contract":{"capabilities":["lan-access-policy","local-ingress"],"defaultDecision":"deny","discovery":"not-included","ingressEnforcement":"unverified","runtimeEnforcement":"unverified","scope":"generation-only"},"policy":@@POLICY@@}
`

var (
	homeAccessPolicyPlanInputRefs   = []string{}
	homeAccessPolicyPublicInputRefs = []string{"home-access-policy"}
)

type homeAccessKitPlanValidator func(homeAccessPlanInputs, []byte, string) ([]string, error)

var homeAccessKitPlanValidators = map[string]homeAccessKitPlanValidator{}

func registerHomeAccessKitPlanValidator(kitSlug string, validator homeAccessKitPlanValidator) {
	if kitSlug == "" || validator == nil {
		panic("architecturev2renderer: home-access kit validator requires a slug and implementation")
	}
	if _, exists := homeAccessKitPlanValidators[kitSlug]; exists {
		panic("architecturev2renderer: duplicate home-access kit validator for " + kitSlug)
	}
	homeAccessKitPlanValidators[kitSlug] = validator
}

func HomeAccessPolicyRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(homeAccessPolicyTemplate))
	return RendererContract{
		Kind: "native-config", RendererRef: homeAccessPolicyRendererRef,
		TemplateRef: homeAccessPolicyTemplateRef, Version: homeAccessPolicyVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type homeAccessPolicyRenderer struct {
	template []byte
	contract RendererContract
}

func newHomeAccessPolicyRenderer() homeAccessPolicyRenderer {
	return homeAccessPolicyRenderer{template: []byte(homeAccessPolicyTemplate), contract: HomeAccessPolicyRendererContract()}
}

//nolint:dupl // Policy renderers intentionally share the immutable-template lowering sequence.
func (r homeAccessPolicyRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	policy, err := validateHomeAccessPolicyUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	if homeAccessTemplateHash(r.template) != r.contract.ContractHash || bytes.Count(r.template, []byte(homeAccessPolicyToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.home-access-policy.template", "embedded policy manifest does not match its registered contract")
	}
	policyBytes, err := json.Marshal(policy)
	if err != nil {
		return nil, wrap(ErrInvalidPlan, "renderer.home-access-policy.policy", "marshal exact node-local Home access policy", err)
	}
	output := bytes.Replace(r.template, []byte(homeAccessPolicyToken), policyBytes, 1)
	return []UnitOutput{{Ref: homeAccessPolicyOutputRef, Bytes: output}}, nil
}

func homeAccessTemplateHash(template []byte) string {
	sum := sha256.Sum256(template)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type homeAccessPlanInputs struct {
	StackID           string                `json:"stackId"`
	Kit               localAutonomyKit      `json:"kit"`
	Sites             []localAutonomySite   `json:"sites"`
	Identity          localAutonomyIdentity `json:"identity"`
	LocalReachability homeLocalReachability `json:"localReachability"`
}

type homeLocalReachability struct {
	HomeSiteRefs []string                     `json:"homeSiteRefs"`
	Routes       []homeLocalReachabilityRoute `json:"routes"`
}

type homeLocalReachabilityRoute struct {
	ID               string                  `json:"id"`
	ServiceRef       string                  `json:"serviceRef"`
	ModuleRef        string                  `json:"moduleRef"`
	OriginSiteRef    string                  `json:"originSiteRef"`
	OriginNodeRefs   []string                `json:"originNodeRefs"`
	Exposure         string                  `json:"exposure"`
	Protocol         string                  `json:"protocol"`
	UpstreamProtocol string                  `json:"upstreamProtocol"`
	Port             int                     `json:"port"`
	TargetPort       int                     `json:"targetPort"`
	Host             string                  `json:"host,omitempty"`
	Path             string                  `json:"path,omitempty"`
	HealthGateRef    string                  `json:"healthGateRef"`
	Access           homeLocalAccessDecision `json:"access"`
	TLS              homeLocalTLSDecision    `json:"tls"`
}

type homeLocalAccessDecision struct {
	PolicyRef              string   `json:"policyRef"`
	PolicyExposure         string   `json:"policyExposure"`
	Authentication         string   `json:"authentication"`
	Privilege              string   `json:"privilege"`
	EnrolledDeviceRequired bool     `json:"enrolledDeviceRequired"`
	OwnerStepUpRequired    bool     `json:"ownerStepUpRequired"`
	LANStepDown            bool     `json:"lanStepDown"`
	AllowedSiteRefs        []string `json:"allowedSiteRefs"`
	AllowedMethods         []string `json:"allowedMethods,omitempty"`
	DefaultClosed          bool     `json:"defaultClosed"`
}

type homeLocalTLSDecision struct {
	Required   bool   `json:"required"`
	Mode       string `json:"mode"`
	MinVersion string `json:"minVersion,omitempty"`
}

// HomeAccessEnforcementPolicy is the closed, secret-free projection an
// authenticated Home access enforcer may consume. It deliberately excludes
// raw network configuration, addresses, credentials, discovery, sockets, and
// provider lifecycle authority.
type HomeAccessEnforcementPolicy struct {
	StackID string                       `json:"stackId"`
	SiteRef string                       `json:"siteRef"`
	NodeRef string                       `json:"nodeRef"`
	Routes  []HomeAccessEnforcementRoute `json:"routes"`
}

// HomeAccessEnforcementRoute preserves only the compiler-owned local route and
// access decision that the Home enforcer must apply and read back.
type HomeAccessEnforcementRoute struct {
	ID                     string   `json:"id"`
	ServiceRef             string   `json:"serviceRef"`
	ModuleRef              string   `json:"moduleRef"`
	OriginSiteRef          string   `json:"originSiteRef"`
	OriginNodeRefs         []string `json:"originNodeRefs"`
	Protocol               string   `json:"protocol"`
	UpstreamProtocol       string   `json:"upstreamProtocol"`
	Port                   int      `json:"port"`
	TargetPort             int      `json:"targetPort"`
	Host                   string   `json:"host,omitempty"`
	Path                   string   `json:"path,omitempty"`
	PolicyRef              string   `json:"policyRef"`
	PolicyExposure         string   `json:"policyExposure"`
	Authentication         string   `json:"authentication"`
	Privilege              string   `json:"privilege"`
	EnrolledDeviceRequired bool     `json:"enrolledDeviceRequired"`
	OwnerStepUpRequired    bool     `json:"ownerStepUpRequired"`
	LANStepDown            bool     `json:"lanStepDown"`
	AllowedSiteRefs        []string `json:"allowedSiteRefs"`
	AllowedMethods         []string `json:"allowedMethods,omitempty"`
	TLSRequired            bool     `json:"tlsRequired"`
	TLSMode                string   `json:"tlsMode"`
	TLSMinVersion          string   `json:"tlsMinVersion,omitempty"`
}

type homeAccessEnforcementValue struct {
	StackID string                       `json:"stackId"`
	Routes  []HomeAccessEnforcementRoute `json:"routes"`
}

type homeAccessPolicyValues struct {
	Policy json.RawMessage `json:"home-access-policy"`
}

type homeAccessPolicyArtifact struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Contract   struct {
		Capabilities       []string `json:"capabilities"`
		DefaultDecision    string   `json:"defaultDecision"`
		Discovery          string   `json:"discovery"`
		IngressEnforcement string   `json:"ingressEnforcement"`
		RuntimeEnforcement string   `json:"runtimeEnforcement"`
		Scope              string   `json:"scope"`
	} `json:"contract"`
	Policy json.RawMessage `json:"policy"`
}

// ValidateHomeAccessPolicyArtifact validates the exact generated policy bytes
// before they cross into a runtime adapter and returns a defensive projection.
// The generation-only markers are required: the artifact is policy input, not
// evidence that an enforcer already exists or ran.
func ValidateHomeAccessPolicyArtifact(raw []byte) (HomeAccessEnforcementPolicy, error) {
	var document homeAccessPolicyArtifact
	if err := decodeStrict(raw, &document); err != nil {
		return HomeAccessEnforcementPolicy{}, wrap(ErrInvalidPlan, "homeAccessPolicy", "decode exact Home access policy artifact", err)
	}
	if document.APIVersion != "stackkit.home-access-policy/v1" || document.Kind != "HomeAccessPolicy" ||
		!slices.Equal(document.Contract.Capabilities, []string{"lan-access-policy", "local-ingress"}) ||
		document.Contract.DefaultDecision != "deny" || document.Contract.Discovery != "not-included" ||
		document.Contract.IngressEnforcement != "unverified" || document.Contract.RuntimeEnforcement != "unverified" ||
		document.Contract.Scope != "generation-only" {
		return HomeAccessEnforcementPolicy{}, fail(ErrInvalidPlan, "homeAccessPolicy.contract", "artifact widens or fabricates the generation-only Home access contract")
	}
	var policy HomeAccessEnforcementPolicy
	if err := decodeStrict(document.Policy, &policy); err != nil {
		return HomeAccessEnforcementPolicy{}, wrap(ErrInvalidPlan, "homeAccessPolicy.policy", "decode exact node-local Home access policy", err)
	}
	if err := validateNodeLocalHomeAccessPolicy(policy, document.Policy, "homeAccessPolicy.policy"); err != nil {
		return HomeAccessEnforcementPolicy{}, err
	}
	return cloneHomeAccessEnforcementPolicy(policy), nil
}

//nolint:gocyclo // This boundary closes compiler-owned global policy onto one exact Site/node.
func validateHomeAccessPolicyUnit(unit RenderUnit, contract RendererContract) (HomeAccessEnforcementPolicy, error) {
	path := "resolvedPlan.modules." + homeAccessPolicyModuleID + ".renderUnits." + homeAccessPolicyUnitID
	if unit.ModuleID() != homeAccessPolicyModuleID || unit.ID() != homeAccessPolicyUnitID {
		return HomeAccessEnforcementPolicy{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", homeAccessPolicyModuleID, homeAccessPolicyUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return HomeAccessEnforcementPolicy{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Home access contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.RuntimeKind() != "native" || unit.RuntimeDelivery() != "stackkit" || unit.InstanceScope() != "node-local" ||
		!hasSite || !hasNode || unit.InstanceID() != homeAccessPolicyUnitID+"-node-"+nodeRef ||
		!containsExact(unit.LogicalSiteRefs(), siteRef) || !containsExact(unit.LogicalNodeRefs(), nodeRef) {
		return HomeAccessEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "Home access policy requires one exact governed native/stackkit Site/node instance")
	}
	if _, present := unit.RuntimeEngine(); present {
		return HomeAccessEnforcementPolicy{}, fail(ErrInvalidPlan, path+".runtime", "native Home access policy must not receive a runtime engine")
	}
	if _, present := unit.ContainerImageRef(); present {
		return HomeAccessEnforcementPolicy{}, fail(ErrInvalidPlan, path+".runtime", "native Home access policy must not receive a container image")
	}
	if _, present := unit.ContainerImageDigest(); present {
		return HomeAccessEnforcementPolicy{}, fail(ErrInvalidPlan, path+".runtime", "native Home access policy must not receive a container digest")
	}
	if _, present := unit.RuntimeEntryComponentRef(); present {
		return HomeAccessEnforcementPolicy{}, fail(ErrInvalidPlan, path+".runtime", "native Home access policy must not receive an entry component")
	}
	if !emptyJSONArray(unit.RuntimeComponentsJSON()) {
		return HomeAccessEnforcementPolicy{}, fail(ErrInvalidPlan, path+".runtime", "native Home access policy must not receive runtime components")
	}
	if _, present := unit.DaemonRef(); present {
		return HomeAccessEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "Home access policy must not receive daemon authority")
	}
	if _, present := unit.DaemonInstanceRef(); present {
		return HomeAccessEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "Home access policy must not receive a daemon instance")
	}
	if _, present := unit.DaemonEngine(); present {
		return HomeAccessEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "Home access policy must not receive a daemon engine")
	}
	if _, present := unit.DaemonSocketPath(); present {
		return HomeAccessEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "Home access policy must not receive a daemon socket")
	}
	if !exactStringList(unit.PublicInputRefs(), homeAccessPolicyPublicInputRefs) || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) ||
		!exactStringList(unit.PlanInputRefs(), homeAccessPolicyPlanInputRefs) || !emptyJSONObject(unit.PlanInputsJSON()) {
		return HomeAccessEnforcementPolicy{}, fail(ErrInvalidPlan, path+".inputs", "Home access policy accepts only its exact typed compiler-owned input")
	}
	if err := validateHomeAccessPolicyBindings(unit.InputBindingsJSON(), path+".inputBindings"); err != nil {
		return HomeAccessEnforcementPolicy{}, err
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) || !emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) {
		return HomeAccessEnforcementPolicy{}, fail(ErrInvalidPlan, path+".interfaces", "Home access policy has no service, network, interface, approval, or socket authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return HomeAccessEnforcementPolicy{}, fail(ErrInvalidPlan, path+".placement", "Home access policy requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != homeAccessPolicyOutputRef {
		return HomeAccessEnforcementPolicy{}, fail(ErrInvalidPlan, path+".outputs", "Home access policy requires exactly output %q", homeAccessPolicyOutputRef)
	}
	var values homeAccessPolicyValues
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return HomeAccessEnforcementPolicy{}, wrap(ErrInvalidPlan, path+".values", "decode exact Home access policy typed value", err)
	}
	var value homeAccessEnforcementValue
	if err := decodeStrict(values.Policy, &value); err != nil {
		return HomeAccessEnforcementPolicy{}, wrap(ErrInvalidPlan, path+".values.home-access-policy", "decode exact compiler-owned Home access policy", err)
	}
	if err := requireContractID(value.StackID, path+".values.home-access-policy.stackId"); err != nil {
		return HomeAccessEnforcementPolicy{}, err
	}
	if err := validateHomeAccessEnforcementRoutes(value.Routes, values.Policy, path+".values.home-access-policy"); err != nil {
		return HomeAccessEnforcementPolicy{}, err
	}
	localRoutes := make([]HomeAccessEnforcementRoute, 0, len(value.Routes))
	for _, route := range value.Routes {
		if containsExact(route.OriginNodeRefs, nodeRef) && route.OriginSiteRef != siteRef {
			return HomeAccessEnforcementPolicy{}, fail(ErrInvalidPlan, path+".values.home-access-policy.routes", "node is claimed by a route outside its exact Site")
		}
		if route.OriginSiteRef != siteRef || !containsExact(route.OriginNodeRefs, nodeRef) {
			continue
		}
		local := cloneHomeAccessEnforcementRoute(route)
		local.OriginNodeRefs = []string{nodeRef}
		localRoutes = append(localRoutes, local)
	}
	return HomeAccessEnforcementPolicy{StackID: value.StackID, SiteRef: siteRef, NodeRef: nodeRef, Routes: localRoutes}, nil
}

func validateHomeAccessPolicyBindings(raw []byte, path string) error {
	var bindings []rawModuleRenderInputBinding
	if err := decodeStrict(raw, &bindings); err != nil {
		return wrap(ErrInvalidPlan, path, "decode Home access input bindings", err)
	}
	expected := []rawModuleRenderInputBinding{{
		TargetRef: "home-access-policy", SourceRef: "access.homeEnforcement",
		ValueType: "home-access-enforcement-v1", Cardinality: "single", Required: true,
	}}
	if !reflect.DeepEqual(bindings, expected) {
		return fail(ErrInvalidPlan, path, "must exactly match the governed Home access binding")
	}
	return nil
}

func validateNodeLocalHomeAccessPolicy(policy HomeAccessEnforcementPolicy, raw []byte, path string) error {
	if err := requireContractID(policy.StackID, path+".stackId"); err != nil {
		return err
	}
	if err := requireContractID(policy.SiteRef, path+".siteRef"); err != nil {
		return err
	}
	if err := requireContractID(policy.NodeRef, path+".nodeRef"); err != nil {
		return err
	}
	if err := validateHomeAccessEnforcementRoutes(policy.Routes, raw, path); err != nil {
		return err
	}
	for index, route := range policy.Routes {
		if route.OriginSiteRef != policy.SiteRef || !slices.Equal(route.OriginNodeRefs, []string{policy.NodeRef}) {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s.routes[%d]", path, index), "artifact route must be closed to the exact artifact Site/node")
		}
	}
	return nil
}

func validateHomeAccessEnforcementRoutes(routes []HomeAccessEnforcementRoute, raw []byte, path string) error {
	previousID := ""
	for index, route := range routes {
		routePath := fmt.Sprintf("%s.routes[%d]", path, index)
		if err := validateHomeAccessEnforcementRoute(route, routePath); err != nil {
			return err
		}
		if previousID != "" && route.ID <= previousID {
			return fail(ErrInvalidPlan, routePath+".id", "routes must be unique and sorted by id")
		}
		previousID = route.ID
	}
	return rejectGenerationOnlyPolicyProjectionLeaks(raw, path, "home-access policy")
}

func validateHomeAccessEnforcementRoute(route HomeAccessEnforcementRoute, path string) error {
	for field, value := range map[string]string{
		"id": route.ID, "serviceRef": route.ServiceRef, "moduleRef": route.ModuleRef,
		"originSiteRef": route.OriginSiteRef, "policyRef": route.PolicyRef,
	} {
		if err := requireContractID(value, path+"."+field); err != nil {
			return err
		}
	}
	if route.Protocol == "" || route.UpstreamProtocol == "" || route.Port < 1 || route.Port > 65535 || route.TargetPort < 1 || route.TargetPort > 65535 {
		return fail(ErrInvalidPlan, path, "route requires protocols and valid listener/target ports")
	}
	if host := strings.ToLower(strings.TrimSuffix(route.Host, ".")); route.PolicyExposure == "lan" && (host == "localhost" || strings.HasSuffix(host, ".localhost")) {
		return fail(ErrInvalidPlan, path+".host", ".localhost is process-local and cannot be a Home LAN route")
	}
	if err := validateExactContractIDs(route.OriginNodeRefs, path+".originNodeRefs"); err != nil {
		return err
	}
	if err := validateExactContractIDs(route.AllowedSiteRefs, path+".allowedSiteRefs"); err != nil {
		return err
	}
	if !containsExact(route.AllowedSiteRefs, route.OriginSiteRef) {
		return fail(ErrInvalidPlan, path+".allowedSiteRefs", "route origin must be explicitly authorized")
	}
	if len(route.AllowedMethods) > 0 && !validExactStringValues(route.AllowedMethods) {
		return fail(ErrInvalidPlan, path+".allowedMethods", "allowed methods must be exact, unique, and sorted")
	}
	if route.PolicyExposure == "lan" {
		if !route.LANStepDown || !route.EnrolledDeviceRequired || (route.Authentication != "device" && route.Authentication != "human+device") {
			return fail(ErrInvalidPlan, path, "LAN step-down requires device-bound authentication and enrollment")
		}
	} else if route.PolicyExposure != "private" || route.LANStepDown {
		return fail(ErrInvalidPlan, path+".policyExposure", "policy exposure must be private or explicit LAN step-down")
	}
	if route.Privilege == "admin" || route.Privilege == "identity" || route.Privilege == "secrets" {
		if route.Authentication != "human+device" || !route.EnrolledDeviceRequired || !route.OwnerStepUpRequired {
			return fail(ErrInvalidPlan, path, "privileged access requires human+device, enrollment, and owner step-up")
		}
	}
	return validateHomeLocalTLS(homeLocalTLSDecision{Required: route.TLSRequired, Mode: route.TLSMode, MinVersion: route.TLSMinVersion}, route.Protocol, path+".tls")
}

func validateExactContractIDs(values []string, path string) error {
	if len(values) == 0 || !slices.IsSorted(values) {
		return fail(ErrInvalidPlan, path, "must be non-empty, unique, and sorted")
	}
	for index, value := range values {
		if err := requireContractID(value, fmt.Sprintf("%s[%d]", path, index)); err != nil {
			return err
		}
		if index > 0 && values[index-1] == value {
			return fail(ErrDuplicate, fmt.Sprintf("%s[%d]", path, index), "duplicate contract ref")
		}
	}
	return nil
}

func validExactStringValues(values []string) bool {
	if !slices.IsSorted(values) {
		return false
	}
	for index, value := range values {
		if value == "" || value != strings.TrimSpace(value) || index > 0 && values[index-1] == value {
			return false
		}
	}
	return true
}

func cloneHomeAccessEnforcementPolicy(policy HomeAccessEnforcementPolicy) HomeAccessEnforcementPolicy {
	policy.Routes = append([]HomeAccessEnforcementRoute(nil), policy.Routes...)
	for index := range policy.Routes {
		policy.Routes[index] = cloneHomeAccessEnforcementRoute(policy.Routes[index])
	}
	return policy
}

func cloneHomeAccessEnforcementRoute(route HomeAccessEnforcementRoute) HomeAccessEnforcementRoute {
	route.OriginNodeRefs = append([]string(nil), route.OriginNodeRefs...)
	route.AllowedSiteRefs = append([]string(nil), route.AllowedSiteRefs...)
	route.AllowedMethods = append([]string(nil), route.AllowedMethods...)
	return route
}

func validateHomeAccessPlanInputs(raw []byte, path string) ([]string, error) {
	var inputs homeAccessPlanInputs
	if err := decodeStrict(raw, &inputs); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact home-access plan inputs", err)
	}
	if err := requireContractID(inputs.StackID, path+".stackId"); err != nil {
		return nil, err
	}
	if inputs.Kit.Version == "" || !validSHA256(inputs.Kit.DefinitionHash) {
		return nil, fail(ErrInvalidPlan, path+".kit", "home-access policy requires an exact governed kit identity")
	}
	if inputs.Kit.Slug == "basement-kit" {
		return validateHomeAccessPlanInputsForKit(inputs, raw, path, false)
	}
	if validator, exists := homeAccessKitPlanValidators[inputs.Kit.Slug]; exists {
		return validator(inputs, raw, path)
	}
	return nil, fail(ErrInvalidPlan, path+".kit.slug", "kit has no registered home-access policy validator")
}

//nolint:gocyclo // Topology, device identity, route scope, access, and TLS form one fail-closed policy boundary.
func validateHomeAccessPlanInputsForKit(inputs homeAccessPlanInputs, raw []byte, path string, allowCloudSites bool) ([]string, error) {
	siteKinds, homeSiteRefs, cloudSiteRefs, err := validateLocalAutonomySites(inputs.Sites, path)
	if err != nil {
		return nil, err
	}
	if len(homeSiteRefs) == 0 || !allowCloudSites && len(cloudSiteRefs) != 0 {
		return nil, fail(ErrInvalidPlan, path+".sites", "home-access policy accepts Home Sites only unless the private kit composition authorizes Cloud peers")
	}
	if !allowCloudSites && (len(inputs.Sites) != 1 || len(homeSiteRefs) != 1) {
		return nil, fail(ErrInvalidPlan, path+".sites", "Basement home access requires exactly one Home Site")
	}
	if !exactStringList(inputs.LocalReachability.HomeSiteRefs, homeSiteRefs) {
		return nil, fail(ErrInvalidPlan, path+".localReachability.homeSiteRefs", "must exactly equal the sorted Home Site projection")
	}
	authoritySiteRef := inputs.Identity.HumanAuthoritySiteRef
	if siteKinds[authoritySiteRef] != "home" {
		return nil, fail(ErrInvalidPlan, path+".identity.humanAuthoritySiteRef", "Home access requires a Home identity authority")
	}
	if err := validateLocalAutonomyIdentity(inputs.Identity, authoritySiteRef, path+".identity"); err != nil {
		return nil, err
	}

	previousRouteID := ""
	for index, route := range inputs.LocalReachability.Routes {
		routePath := fmt.Sprintf("%s.localReachability.routes[%d]", path, index)
		if err := validateHomeLocalRoute(route, siteKinds, routePath); err != nil {
			return nil, err
		}
		if previousRouteID != "" && route.ID <= previousRouteID {
			return nil, fail(ErrInvalidPlan, routePath+".id", "local routes must be unique and sorted by id")
		}
		previousRouteID = route.ID
	}
	if err := rejectGenerationOnlyPolicyProjectionLeaks(raw, path, "home-access policy"); err != nil {
		return nil, err
	}
	return homeSiteRefs, nil
}

func validateHomeLocalRoute(route homeLocalReachabilityRoute, siteKinds map[string]string, path string) error {
	for field, value := range map[string]string{
		"id": route.ID, "serviceRef": route.ServiceRef, "moduleRef": route.ModuleRef,
		"originSiteRef": route.OriginSiteRef, "healthGateRef": route.HealthGateRef, "policyRef": route.Access.PolicyRef,
	} {
		if err := requireContractID(value, path+"."+field); err != nil {
			return err
		}
	}
	if route.Exposure != "local" || siteKinds[route.OriginSiteRef] != "home" || len(route.OriginNodeRefs) == 0 {
		return fail(ErrInvalidPlan, path, "home-access routes must be local, Home-originated, and bound to explicit logical nodes")
	}
	if route.Port < 1 || route.Port > 65535 || route.TargetPort < 1 || route.TargetPort > 65535 {
		return fail(ErrInvalidPlan, path+".port", "listener and target ports must be valid")
	}
	if route.Protocol == "" || route.UpstreamProtocol == "" {
		return fail(ErrInvalidPlan, path+".protocol", "listener and upstream protocols are required")
	}
	if host := strings.ToLower(strings.TrimSuffix(route.Host, ".")); route.Access.PolicyExposure == "lan" && (host == "localhost" || strings.HasSuffix(host, ".localhost")) {
		return fail(ErrInvalidPlan, path+".host", ".localhost is process-local and cannot be a Home LAN route")
	}
	seenNodes := map[string]struct{}{}
	for index, nodeRef := range route.OriginNodeRefs {
		if err := requireContractID(nodeRef, fmt.Sprintf("%s.originNodeRefs[%d]", path, index)); err != nil {
			return err
		}
		if _, duplicate := seenNodes[nodeRef]; duplicate {
			return fail(ErrDuplicate, fmt.Sprintf("%s.originNodeRefs[%d]", path, index), "duplicate logical node ref")
		}
		seenNodes[nodeRef] = struct{}{}
	}
	if err := validateHomeLocalAccess(route.Access, route.OriginSiteRef, siteKinds, path+".access"); err != nil {
		return err
	}
	return validateHomeLocalTLS(route.TLS, route.Protocol, path+".tls")
}

//nolint:gocyclo // Site scope, LAN step-down, and privileged access are one fail-closed decision.
func validateHomeLocalAccess(access homeLocalAccessDecision, originSiteRef string, siteKinds map[string]string, path string) error {
	if !access.DefaultClosed || len(access.AllowedSiteRefs) == 0 {
		return fail(ErrInvalidPlan, path, "local access must be default-closed and explicitly site-scoped")
	}
	allowedOrigin := false
	seen := map[string]struct{}{}
	for index, siteRef := range access.AllowedSiteRefs {
		if siteKinds[siteRef] != "home" {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s.allowedSiteRefs[%d]", path, index), "local access cannot authorize a non-Home Site")
		}
		if _, duplicate := seen[siteRef]; duplicate {
			return fail(ErrDuplicate, fmt.Sprintf("%s.allowedSiteRefs[%d]", path, index), "duplicate allowed site")
		}
		seen[siteRef] = struct{}{}
		allowedOrigin = allowedOrigin || siteRef == originSiteRef
	}
	if !allowedOrigin {
		return fail(ErrInvalidPlan, path+".allowedSiteRefs", "route origin must be explicitly authorized")
	}
	if access.PolicyExposure == "lan" {
		if !access.LANStepDown || !access.EnrolledDeviceRequired || (access.Authentication != "device" && access.Authentication != "human+device") {
			return fail(ErrInvalidPlan, path, "LAN step-down requires device-bound authentication and an enrolled device")
		}
	} else if access.PolicyExposure != "private" || access.LANStepDown {
		return fail(ErrInvalidPlan, path+".policyExposure", "local policy exposure must be private or an explicit LAN step-down")
	}
	if access.Privilege == "admin" || access.Privilege == "identity" || access.Privilege == "secrets" {
		if access.Authentication != "human+device" || !access.EnrolledDeviceRequired || !access.OwnerStepUpRequired {
			return fail(ErrInvalidPlan, path, "privileged local access requires human+device, enrollment, and owner step-up")
		}
	}
	return nil
}

func validateHomeLocalTLS(tls homeLocalTLSDecision, protocol, path string) error {
	if !tls.Required {
		if tls.Mode != "off" || tls.MinVersion != "" || protocol == "https" {
			return fail(ErrInvalidPlan, path, "TLS-off decision conflicts with the route protocol")
		}
		return nil
	}
	if tls.Mode != "internal" && tls.Mode != "terminate-at-edge" {
		return fail(ErrInvalidPlan, path+".mode", "TLS-required route needs a governed termination mode")
	}
	if tls.MinVersion != "TLS1.2" && tls.MinVersion != "TLS1.3" {
		return fail(ErrInvalidPlan, path+".minVersion", "TLS-required route needs an explicit minimum version")
	}
	return nil
}
