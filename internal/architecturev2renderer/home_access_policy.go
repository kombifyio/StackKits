package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	homeAccessPolicyModuleID    = "stackkits-home-access-policy-manifest"
	homeAccessPolicyUnitID      = "policy-bundle"
	homeAccessPolicyRendererRef = "stackkit"
	homeAccessPolicyTemplateRef = "builtin://home/access/v1.json"
	homeAccessPolicyVersion     = "1.0.0"
	homeAccessPolicyOutputRef   = "local/network/access-policy.json"
	homeAccessPolicyToken       = "@@PLAN_INPUTS@@"
)

const homeAccessPolicyTemplate = `{"apiVersion":"stackkit.home-access-policy/v1","kind":"HomeAccessPolicy","contract":{"capabilities":["lan-access-policy","local-ingress"],"defaultDecision":"deny","discovery":"not-included","ingressEnforcement":"unverified","runtimeEnforcement":"unverified","scope":"generation-only"},"planInputs":@@PLAN_INPUTS@@}
`

var homeAccessPolicyPlanInputRefs = []string{"identity", "kit", "localReachability", "sites", "stackId"}

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
	planInputs, err := validateHomeAccessPolicyUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	if homeAccessTemplateHash(r.template) != r.contract.ContractHash || bytes.Count(r.template, []byte(homeAccessPolicyToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.home-access-policy.template", "embedded policy manifest does not match its registered contract")
	}
	output := bytes.Replace(r.template, []byte(homeAccessPolicyToken), planInputs, 1)
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

func validateHomeAccessPolicyUnit(unit RenderUnit, contract RendererContract) ([]byte, error) {
	return validateGenerationOnlyPolicyUnit(unit, generationOnlyPolicyUnitSpec{
		moduleID: homeAccessPolicyModuleID, unitID: homeAccessPolicyUnitID,
		outputRef: homeAccessPolicyOutputRef, policyName: "home-access policy",
		contract: contract, planInputRefs: homeAccessPolicyPlanInputRefs,
		validatePlanInput: validateHomeAccessPlanInputs,
	})
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
	if tls.Mode != "internal" && tls.Mode != "terminate-at-edge" && tls.Mode != "passthrough" {
		return fail(ErrInvalidPlan, path+".mode", "TLS-required route needs a governed termination mode")
	}
	if tls.MinVersion != "TLS1.2" && tls.MinVersion != "TLS1.3" {
		return fail(ErrInvalidPlan, path+".minVersion", "TLS-required route needs an explicit minimum version")
	}
	return nil
}
