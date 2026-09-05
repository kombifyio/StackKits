package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
)

const (
	homeLANDNSPolicyModuleID    = "stackkits-home-lan-dns-manifest"
	homeLANDNSPolicyUnitID      = "policy-bundle"
	homeLANDNSPolicyRendererRef = "stackkit"
	homeLANDNSPolicyTemplateRef = "builtin://home/lan-dns/v1.json"
	homeLANDNSPolicyVersion     = "1.0.0"
	homeLANDNSPolicyOutputRef   = "local/network/lan-dns-policy.json"
	homeLANDNSPolicyDomainToken = "@@DOMAIN@@"
	homeLANDNSPolicyInputsToken = "@@PLAN_INPUTS@@"
)

// homeLANDNSPolicyTemplate is the immutable generation-only LAN DNS resolver
// policy. It declares the governed resolver contract (Unbound, pinned image,
// LAN-scoped port 53, mandatory DNSSEC) for the plan domain without claiming
// runtime enforcement: no container runs and no LAN client resolves until the
// slice-2 runtime owner lands. The image pin is amd64-only; arm/arm64 support
// is an explicit follow-up, never silent breakage.
const homeLANDNSPolicyTemplate = `{"apiVersion":"stackkit.home-lan-dns-policy/v1","kind":"HomeLANDNSPolicy","contract":{"capability":"lan-dns","resolver":"unbound","image":{"ref":"docker.io/mvance/unbound:1.22.0","digest":"sha256:76906da36d1806f3387338f15dcf8b357c51ce6897fb6450d6ce010460927e90"},"architectures":["amd64"],"listen":{"port":53,"protocols":["udp","tcp"],"scope":"lan"},"dnssec":{"required":true},"zone":"@@DOMAIN@@","runtimeEnforcement":"unverified","executor":"pending","scope":"generation-only"},"planInputs":@@PLAN_INPUTS@@}
`

var homeLANDNSPolicyPlanInputRefs = []string{"kit", "sites", "stackId"}

var homeLANDNSDomainPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*[A-Za-z0-9]$`)

type homeLANDNSKitPlanValidator func(homeLANDNSPlanInputs, []byte, string) ([]string, error)

var homeLANDNSKitPlanValidators = map[string]homeLANDNSKitPlanValidator{}

// registerHomeLANDNSKitPlanValidator lets a kit composition add its
// topology-specific validation without widening the shared projection.
func registerHomeLANDNSKitPlanValidator(kitSlug string, validator homeLANDNSKitPlanValidator) {
	if kitSlug == "" || validator == nil {
		panic("architecturev2renderer: home LAN-DNS kit validator requires a slug and implementation")
	}
	if _, exists := homeLANDNSKitPlanValidators[kitSlug]; exists {
		panic("architecturev2renderer: duplicate home LAN-DNS kit validator for " + kitSlug)
	}
	homeLANDNSKitPlanValidators[kitSlug] = validator
}

// HomeLANDNSPolicyRendererContract returns the exact built-in identity for
// the generation-only LAN DNS policy. Its immutable shell declares the
// resolver contract and explicitly marks runtime enforcement unverified with
// the executor pending.
func HomeLANDNSPolicyRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(homeLANDNSPolicyTemplate))
	return RendererContract{
		Kind: "native-config", RendererRef: homeLANDNSPolicyRendererRef,
		TemplateRef: homeLANDNSPolicyTemplateRef, Version: homeLANDNSPolicyVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type homeLANDNSPolicyRenderer struct {
	template []byte
	contract RendererContract
}

func newHomeLANDNSPolicyRenderer() homeLANDNSPolicyRenderer {
	return homeLANDNSPolicyRenderer{
		template: []byte(homeLANDNSPolicyTemplate),
		contract: HomeLANDNSPolicyRendererContract(),
	}
}

//nolint:dupl // Policy renderers intentionally share the immutable-template lowering sequence.
func (r homeLANDNSPolicyRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	planInputs, domain, err := validateHomeLANDNSPolicyUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	if homeLANDNSTemplateHash(r.template) != r.contract.ContractHash ||
		bytes.Count(r.template, []byte(homeLANDNSPolicyDomainToken)) != 1 ||
		bytes.Count(r.template, []byte(homeLANDNSPolicyInputsToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.home-lan-dns-policy.template", "embedded policy manifest does not match its registered contract")
	}
	output := bytes.Replace(r.template, []byte(homeLANDNSPolicyDomainToken), []byte(domain), 1)
	output = bytes.Replace(output, []byte(homeLANDNSPolicyInputsToken), planInputs, 1)
	return []UnitOutput{{Ref: homeLANDNSPolicyOutputRef, Bytes: output}}, nil
}

func homeLANDNSTemplateHash(template []byte) string {
	sum := sha256.Sum256(template)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type homeLANDNSPlanInputs struct {
	StackID string              `json:"stackId"`
	Kit     localAutonomyKit    `json:"kit"`
	Sites   []localAutonomySite `json:"sites"`
}

func validateHomeLANDNSPolicyUnit(unit RenderUnit, contract RendererContract) ([]byte, string, error) {
	planInputs, err := validateGenerationOnlyPolicyUnit(unit, generationOnlyPolicyUnitSpec{
		moduleID: homeLANDNSPolicyModuleID, unitID: homeLANDNSPolicyUnitID,
		outputRef: homeLANDNSPolicyOutputRef, policyName: "home LAN-DNS policy",
		contract: contract, planInputRefs: homeLANDNSPolicyPlanInputRefs,
		validatePlanInput: validateHomeLANDNSPlanInputs,
	})
	if err != nil {
		return nil, "", err
	}
	domain, hasDomain := unit.NetworkDomainBase()
	path := "resolvedPlan.modules." + homeLANDNSPolicyModuleID + ".renderUnits." + homeLANDNSPolicyUnitID
	if !hasDomain || !homeLANDNSDomainPattern.MatchString(domain) {
		return nil, "", fail(ErrInvalidPlan, path+".domain", "home LAN-DNS policy requires a canonical resolved network domain")
	}
	return planInputs, domain, nil
}

func validateHomeLANDNSPlanInputs(raw []byte, path string) ([]string, error) {
	var inputs homeLANDNSPlanInputs
	if err := decodeStrict(raw, &inputs); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact home LAN-DNS plan inputs", err)
	}
	if inputs.Kit.Version == "" || !validSHA256(inputs.Kit.DefinitionHash) {
		return nil, fail(ErrInvalidPlan, path+".kit", "home LAN DNS requires an exact governed kit identity")
	}
	switch inputs.Kit.Slug {
	case "basement-kit":
		return validateHomeLANDNSPlanInputsForKit(inputs, raw, path, false)
	default:
		validator, exists := homeLANDNSKitPlanValidators[inputs.Kit.Slug]
		if !exists {
			return nil, fail(ErrInvalidPlan, path+".kit.slug", "kit has no registered home LAN-DNS validator")
		}
		return validator(inputs, raw, path)
	}
}

//nolint:gocyclo // Site scope and leak rejection form one fail-closed projection boundary.
func validateHomeLANDNSPlanInputsForKit(inputs homeLANDNSPlanInputs, raw []byte, path string, allowCloudSites bool) ([]string, error) {
	_, homeSiteRefs, cloudSiteRefs, err := validateLocalAutonomySites(inputs.Sites, path)
	if err != nil {
		return nil, err
	}
	if len(homeSiteRefs) == 0 || !allowCloudSites && len(cloudSiteRefs) != 0 {
		return nil, fail(ErrInvalidPlan, path+".sites", "LAN DNS accepts Home Sites only unless private kit composition authorizes Cloud peers")
	}
	if !allowCloudSites && (len(inputs.Sites) != 1 || len(homeSiteRefs) != 1) {
		return nil, fail(ErrInvalidPlan, path+".sites", "Basement LAN DNS requires exactly one Home Site")
	}
	if err := rejectGenerationOnlyPolicyProjectionLeaks(raw, path, "home LAN-DNS policy"); err != nil {
		return nil, err
	}
	return homeSiteRefs, nil
}
