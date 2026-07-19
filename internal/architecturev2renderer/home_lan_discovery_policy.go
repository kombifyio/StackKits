package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

const (
	homeLANDiscoveryPolicyModuleID    = "stackkits-home-lan-discovery-policy-manifest"
	homeLANDiscoveryPolicyUnitID      = "policy-bundle"
	homeLANDiscoveryPolicyRendererRef = "stackkit"
	homeLANDiscoveryPolicyTemplateRef = "builtin://home/lan-discovery/v1.json"
	homeLANDiscoveryPolicyVersion     = "1.0.0"
	homeLANDiscoveryPolicyOutputRef   = "local/network/discovery-policy.json"
	homeLANDiscoveryPolicyToken       = "@@PLAN_INPUTS@@"
)

const homeLANDiscoveryPolicyTemplate = `{"apiVersion":"stackkit.home-lan-discovery-policy/v1","kind":"HomeLANDiscoveryPolicy","contract":{"adapters":{"address":"not-included","dns":"not-included","interface":"not-included","runtime":"not-included"},"capability":"lan-discovery","defaultAdvertisement":"deny","runtimeEnforcement":"unverified","scope":"generation-only"},"planInputs":@@PLAN_INPUTS@@}
`

var homeLANDiscoveryPolicyPlanInputRefs = []string{"homeLANDiscovery", "kit", "sites", "stackId"}

var homeLANDiscoveryHostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*[A-Za-z0-9]$`)

type homeLANDiscoveryKitPlanValidator func(homeLANDiscoveryPlanInputs, []byte, string) ([]string, error)

var homeLANDiscoveryKitPlanValidators = map[string]homeLANDiscoveryKitPlanValidator{}

// registerHomeLANDiscoveryKitPlanValidator lets a kit composition add its
// topology-specific validation without widening the shared projection.
func registerHomeLANDiscoveryKitPlanValidator(kitSlug string, validator homeLANDiscoveryKitPlanValidator) {
	if kitSlug == "" || validator == nil {
		panic("architecturev2renderer: home LAN-discovery kit validator requires a slug and implementation")
	}
	if _, exists := homeLANDiscoveryKitPlanValidators[kitSlug]; exists {
		panic("architecturev2renderer: duplicate home LAN-discovery kit validator for " + kitSlug)
	}
	homeLANDiscoveryKitPlanValidators[kitSlug] = validator
}

// HomeLANDiscoveryPolicyRendererContract returns the exact built-in identity
// for the generation-only discovery policy. Its immutable shell explicitly
// denies implicit advertisements and makes no DNS, address, interface, or
// runtime-enforcement claim.
func HomeLANDiscoveryPolicyRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(homeLANDiscoveryPolicyTemplate))
	return RendererContract{
		Kind: "native-config", RendererRef: homeLANDiscoveryPolicyRendererRef,
		TemplateRef: homeLANDiscoveryPolicyTemplateRef, Version: homeLANDiscoveryPolicyVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type homeLANDiscoveryPolicyRenderer struct {
	template []byte
	contract RendererContract
}

func newHomeLANDiscoveryPolicyRenderer() homeLANDiscoveryPolicyRenderer {
	return homeLANDiscoveryPolicyRenderer{
		template: []byte(homeLANDiscoveryPolicyTemplate),
		contract: HomeLANDiscoveryPolicyRendererContract(),
	}
}

//nolint:dupl // Policy renderers intentionally share the immutable-template lowering sequence.
func (r homeLANDiscoveryPolicyRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	planInputs, err := validateHomeLANDiscoveryPolicyUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	if homeLANDiscoveryTemplateHash(r.template) != r.contract.ContractHash || bytes.Count(r.template, []byte(homeLANDiscoveryPolicyToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.home-lan-discovery-policy.template", "embedded policy manifest does not match its registered contract")
	}
	output := bytes.Replace(r.template, []byte(homeLANDiscoveryPolicyToken), planInputs, 1)
	return []UnitOutput{{Ref: homeLANDiscoveryPolicyOutputRef, Bytes: output}}, nil
}

func homeLANDiscoveryTemplateHash(template []byte) string {
	sum := sha256.Sum256(template)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type homeLANDiscoveryPlanInputs struct {
	StackID          string                     `json:"stackId"`
	Kit              localAutonomyKit           `json:"kit"`
	Sites            []localAutonomySite        `json:"sites"`
	HomeLANDiscovery homeLANDiscoveryProjection `json:"homeLANDiscovery"`
}

type homeLANDiscoveryProjection struct {
	HomeSiteRefs   []string                        `json:"homeSiteRefs"`
	Advertisements []homeLANDiscoveryAdvertisement `json:"advertisements"`
}

type homeLANDiscoveryAdvertisement struct {
	RouteRef       string                         `json:"routeRef"`
	ServiceRef     string                         `json:"serviceRef"`
	OriginSiteRef  string                         `json:"originSiteRef"`
	OriginNodeRefs []string                       `json:"originNodeRefs"`
	Protocol       string                         `json:"protocol"`
	Port           int                            `json:"port"`
	Host           string                         `json:"host"`
	Access         homeLANDiscoveryAccessDecision `json:"access"`
}

type homeLANDiscoveryAccessDecision struct {
	PolicyRef      string `json:"policyRef"`
	PolicyExposure string `json:"policyExposure"`
	DefaultClosed  bool   `json:"defaultClosed"`
}

func validateHomeLANDiscoveryPolicyUnit(unit RenderUnit, contract RendererContract) ([]byte, error) {
	return validateGenerationOnlyPolicyUnit(unit, generationOnlyPolicyUnitSpec{
		moduleID: homeLANDiscoveryPolicyModuleID, unitID: homeLANDiscoveryPolicyUnitID,
		outputRef: homeLANDiscoveryPolicyOutputRef, policyName: "home LAN-discovery policy",
		contract: contract, planInputRefs: homeLANDiscoveryPolicyPlanInputRefs,
		validatePlanInput: validateHomeLANDiscoveryPlanInputs,
	})
}

func validateHomeLANDiscoveryPlanInputs(raw []byte, path string) ([]string, error) {
	var inputs homeLANDiscoveryPlanInputs
	if err := decodeStrict(raw, &inputs); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact home LAN-discovery plan inputs", err)
	}
	if err := validateHomeLANDiscoveryEnvelopeIdentity(inputs, path); err != nil {
		return nil, err
	}
	switch inputs.Kit.Slug {
	case "basement-kit":
		return validateHomeLANDiscoveryPlanInputsForKit(inputs, raw, path, false)
	default:
		validator, exists := homeLANDiscoveryKitPlanValidators[inputs.Kit.Slug]
		if !exists {
			return nil, fail(ErrInvalidPlan, path+".kit.slug", "kit has no registered home LAN-discovery validator")
		}
		return validator(inputs, raw, path)
	}
}

func validateHomeLANDiscoveryEnvelopeIdentity(inputs homeLANDiscoveryPlanInputs, path string) error {
	if err := requireContractID(inputs.StackID, path+".stackId"); err != nil {
		return err
	}
	if inputs.Kit.Version == "" || !validSHA256(inputs.Kit.DefinitionHash) {
		return fail(ErrInvalidPlan, path+".kit", "home LAN discovery requires an exact governed kit identity")
	}
	return nil
}

//nolint:gocyclo // Topology, explicit record identity, LAN scope, and leak rejection form one fail-closed projection boundary.
func validateHomeLANDiscoveryPlanInputsForKit(inputs homeLANDiscoveryPlanInputs, raw []byte, path string, allowCloudSites bool) ([]string, error) {
	siteKinds, homeSiteRefs, cloudSiteRefs, err := validateLocalAutonomySites(inputs.Sites, path)
	if err != nil {
		return nil, err
	}
	if len(homeSiteRefs) == 0 || !allowCloudSites && len(cloudSiteRefs) != 0 {
		return nil, fail(ErrInvalidPlan, path+".sites", "LAN discovery accepts Home Sites only unless private kit composition authorizes Cloud peers")
	}
	if !allowCloudSites && (len(inputs.Sites) != 1 || len(homeSiteRefs) != 1) {
		return nil, fail(ErrInvalidPlan, path+".sites", "Basement LAN discovery requires exactly one Home Site")
	}
	projection := inputs.HomeLANDiscovery
	if !exactStringList(projection.HomeSiteRefs, homeSiteRefs) {
		return nil, fail(ErrInvalidPlan, path+".homeLANDiscovery.homeSiteRefs", "must exactly equal the sorted Home Site projection")
	}
	previousRouteRef := ""
	for index, advertisement := range projection.Advertisements {
		advertisementPath := fmt.Sprintf("%s.homeLANDiscovery.advertisements[%d]", path, index)
		if err := validateHomeLANDiscoveryAdvertisement(advertisement, siteKinds, advertisementPath); err != nil {
			return nil, err
		}
		if previousRouteRef != "" && advertisement.RouteRef <= previousRouteRef {
			return nil, fail(ErrInvalidPlan, advertisementPath+".routeRef", "advertisements must be unique and sorted by routeRef")
		}
		previousRouteRef = advertisement.RouteRef
	}
	if err := rejectGenerationOnlyPolicyProjectionLeaks(raw, path, "home LAN-discovery policy"); err != nil {
		return nil, err
	}
	return homeSiteRefs, nil
}

func validateHomeLANDiscoveryAdvertisement(advertisement homeLANDiscoveryAdvertisement, siteKinds map[string]string, path string) error {
	for field, value := range map[string]string{
		"routeRef": advertisement.RouteRef, "serviceRef": advertisement.ServiceRef,
		"originSiteRef": advertisement.OriginSiteRef, "policyRef": advertisement.Access.PolicyRef,
	} {
		if err := requireContractID(value, path+"."+field); err != nil {
			return err
		}
	}
	if siteKinds[advertisement.OriginSiteRef] != "home" {
		return fail(ErrInvalidPlan, path+".originSiteRef", "LAN advertisements must originate from an explicit Home Site")
	}
	if len(advertisement.OriginNodeRefs) == 0 {
		return fail(ErrInvalidPlan, path+".originNodeRefs", "LAN advertisements require explicit origin nodes")
	}
	previousNodeRef := ""
	for index, nodeRef := range advertisement.OriginNodeRefs {
		if err := requireContractID(nodeRef, fmt.Sprintf("%s.originNodeRefs[%d]", path, index)); err != nil {
			return err
		}
		if previousNodeRef != "" && nodeRef <= previousNodeRef {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s.originNodeRefs[%d]", path, index), "origin node refs must be unique and sorted")
		}
		previousNodeRef = nodeRef
	}
	if !stringListContains([]string{"tcp", "udp", "http", "https"}, advertisement.Protocol) {
		return fail(ErrInvalidPlan, path+".protocol", "LAN advertisement protocol is outside the governed network protocol set")
	}
	if advertisement.Port < 1 || advertisement.Port > 65535 {
		return fail(ErrInvalidPlan, path+".port", "LAN advertisement port must be valid")
	}
	host := strings.ToLower(advertisement.Host)
	if !homeLANDiscoveryHostPattern.MatchString(advertisement.Host) || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fail(ErrInvalidPlan, path+".host", "LAN discovery requires an explicit non-.localhost host")
	}
	if advertisement.Access.PolicyExposure != "lan" || !advertisement.Access.DefaultClosed {
		return fail(ErrInvalidPlan, path+".access", "LAN discovery requires an explicit default-closed LAN access decision")
	}
	return nil
}
