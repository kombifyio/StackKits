package architecturev2renderer

import (
	"encoding/json"
	"fmt"
	"sort"
)

const (
	modernIdentityTrustPolicyModuleID         = "stackkits-modern-identity-trust-policy-manifest"
	modernIdentityTrustPolicyTemplateRef      = "builtin://modern/identity-trust-policy/v1.json"
	modernIdentityTrustPolicyOutputRef        = "modern/identity/trust-policy.json"
	modernVerifierDistributionPolicyOutputRef = "modern/identity/verifier-distribution-policy.json"
	modernIdentityTrustProviderRef            = "stackkits-modern-identity-trust-policy"
	modernIdentityTrustContractRef            = "modern-identity-trust-policy-contract"
)

const modernIdentityTrustPolicyTemplate = `{"apiVersion":"stackkit.modern-identity-trust-policy/v1","kind":"ModernIdentityTrustPolicy","contract":{"cloudEnrollment":"deny","cloudIssuance":"deny","credentialIssuanceRuntime":"unverified","credentialMaterial":"not-included","enrollmentAuthority":"home-only","generalLANReachability":"deny","jwksBytes":"not-included","privateKeys":"not-included","reverseDistribution":"deny","runtimeEndpoints":"not-included","runtimeEnforcement":"unverified","scope":"generation-only","signingAuthority":"home-only","transportRealization":"not-included"},"planInputs":@@PLAN_INPUTS@@}
`

const modernVerifierDistributionPolicyTemplate = `{"apiVersion":"stackkit.modern-verifier-distribution-policy/v1","kind":"ModernVerifierDistributionPolicy","contract":{"cloudEnrollment":"deny","cloudIssuance":"deny","credentialIssuanceRuntime":"unverified","credentialMaterial":"not-included","direction":"home-to-cloud-only","enrollmentAuthority":"not-included","generalLANReachability":"deny","jwksBytes":"not-included","privateKeys":"not-included","reverseDistribution":"deny","runtimeDistribution":"unverified","runtimeEndpoints":"not-included","runtimeEnforcement":"unverified","scope":"generation-only","signingAuthority":"not-included","transportRealization":"not-included"},"planInputs":@@PLAN_INPUTS@@}
`

var modernIdentityTrustPolicyPlanInputRefs = []string{"failurePolicy", "identityTrust", "kit", "sites", "stackId"}

type modernIdentityTrustPlanInputs struct {
	StackID       string                     `json:"stackId"`
	Kit           localAutonomyKit           `json:"kit"`
	Sites         []localAutonomySite        `json:"sites"`
	IdentityTrust identityTrustGraph         `json:"identityTrust"`
	FailurePolicy localAutonomyFailurePolicy `json:"failurePolicy"`
}

func init() {
	registerProductRegistryExtension(func(registry *Registry) error {
		renderer := newModernIdentityTrustPolicyRenderer()
		return registry.Register(renderer.contract, renderer)
	})
}

func newModernIdentityTrustPolicyRenderer() identityTrustPolicyRenderer {
	return newIdentityTrustPolicyRenderer(
		modernIdentityTrustPolicyModuleID, "Modern identity-federation policy", modernIdentityTrustPolicyTemplateRef,
		[]identityTrustPolicyOutputTemplate{
			{ref: modernIdentityTrustPolicyOutputRef, template: []byte(modernIdentityTrustPolicyTemplate)},
			{ref: modernVerifierDistributionPolicyOutputRef, template: []byte(modernVerifierDistributionPolicyTemplate)},
		},
		modernIdentityTrustPolicyPlanInputRefs, validateModernIdentityTrustPlanInputs,
	)
}

// ModernIdentityTrustPolicyRendererContract returns the exact private hybrid
// contract. Its hash jointly binds both generated outputs, so neither the
// trust manifest nor the one-way distribution manifest can drift alone.
func ModernIdentityTrustPolicyRendererContract() RendererContract {
	return newModernIdentityTrustPolicyRenderer().contract
}

//nolint:gocyclo // Home signing, Cloud verification, one-way distribution, and partition staleness are one hybrid trust boundary.
func validateModernIdentityTrustPlanInputs(raw []byte, path string) ([]string, error) {
	var inputs modernIdentityTrustPlanInputs
	if err := decodeStrict(raw, &inputs); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact Modern identity-trust plan inputs", err)
	}
	baseInputs := identityTrustPlanInputs{
		StackID: inputs.StackID, Kit: inputs.Kit, Sites: inputs.Sites, IdentityTrust: inputs.IdentityTrust,
	}
	baseRaw, err := json.Marshal(baseInputs)
	if err != nil {
		return nil, wrap(ErrInvalidPlan, path, "canonicalize Modern identity-trust projection", err)
	}
	validated, err := validateIdentityTrustEnvelope(baseInputs, baseRaw, path)
	if err != nil {
		return nil, err
	}
	if inputs.Kit.Slug != "modern-homelab" || len(validated.homeSiteRefs) == 0 || len(validated.cloudSiteRefs) == 0 {
		return nil, fail(ErrInvalidPlan, path+".sites", "Modern identity federation requires explicit Home and Cloud Sites")
	}
	if inputs.FailurePolicy.MaxStaleVerificationSeconds < 0 || !inputs.FailurePolicy.LocalIdentityAuthorityAvailable || !inputs.FailurePolicy.DenyNewCrossSiteSessions ||
		inputs.FailurePolicy.OnCloudLoss != "local-continues" || inputs.FailurePolicy.OnLinkLoss != "local-continues" || inputs.FailurePolicy.CloudEdge != "fail-closed" {
		return nil, fail(ErrInvalidPlan, path+".failurePolicy", "Modern identity federation requires local authority continuity and fail-closed Cloud verification")
	}
	deviceAuthority, err := validateHomeAuthorityAndIssuance(inputs.IdentityTrust, validated.siteKinds, path)
	if err != nil {
		return nil, err
	}
	if !deviceAuthority || !hasPrincipalIssuer(validated, "device") {
		return nil, fail(ErrInvalidPlan, path+".identityTrust", "Modern requires an exact Home device authority and credential issuer")
	}
	for index, issuer := range inputs.IdentityTrust.CredentialIssuers {
		issuerPath := fmt.Sprintf("%s.identityTrust.credentialIssuers[%d]", path, index)
		if !issuer.IssuanceWithinStackKit || !issuer.RevocationSupported || issuer.RevocationMaxStalenessSeconds != inputs.FailurePolicy.MaxStaleVerificationSeconds {
			return nil, fail(ErrInvalidPlan, issuerPath, "Modern Home issuers must be in-stack, possession-bound, revocable, and match partition staleness")
		}
	}
	coveredCloudSites := make(map[string]struct{}, len(validated.cloudSiteRefs))
	for index, verifier := range inputs.IdentityTrust.VerifierPlacements {
		verifierPath := fmt.Sprintf("%s.identityTrust.verifierPlacements[%d]", path, index)
		if verifier.Placement.Kind != "sites" || !allSiteRefsHaveKind(verifier.Placement.SiteRefs, validated.siteKinds, "cloud") || verifier.RevocationMaxStalenessSeconds != inputs.FailurePolicy.MaxStaleVerificationSeconds {
			return nil, fail(ErrInvalidPlan, verifierPath, "Modern verifiers must be Cloud-only and match partition staleness")
		}
		if err := requireCatalogOwner(verifier.Owner, modernIdentityTrustProviderRef, modernIdentityTrustPolicyModuleID, verifierPath+".owner"); err != nil {
			return nil, err
		}
		for _, siteRef := range verifier.Placement.SiteRefs {
			coveredCloudSites[siteRef] = struct{}{}
		}
	}
	coveredSiteRefs := make([]string, 0, len(coveredCloudSites))
	for siteRef := range coveredCloudSites {
		coveredSiteRefs = append(coveredSiteRefs, siteRef)
	}
	sort.Strings(coveredSiteRefs)
	if !exactStringList(coveredSiteRefs, validated.cloudSiteRefs) || len(inputs.IdentityTrust.VerifierPlacements) == 0 {
		return nil, fail(ErrInvalidPlan, path+".identityTrust.verifierPlacements", "Modern verifier placements must exactly cover every Cloud Site")
	}
	distributedCloudSites := make(map[string]struct{}, len(validated.cloudSiteRefs))
	for index, distribution := range inputs.IdentityTrust.VerifierDistributions {
		distributionPath := fmt.Sprintf("%s.identityTrust.verifierDistributions[%d]", path, index)
		if err := requireCatalogOwner(distribution.Owner, modernIdentityTrustProviderRef, modernIdentityTrustPolicyModuleID, distributionPath+".owner"); err != nil {
			return nil, err
		}
		if !allSiteRefsHaveKind(distribution.From.SiteRefs, validated.siteKinds, "home") || !allSiteRefsHaveKind(distribution.To.SiteRefs, validated.siteKinds, "cloud") ||
			distribution.MaxStalenessSeconds != inputs.FailurePolicy.MaxStaleVerificationSeconds {
			return nil, fail(ErrInvalidPlan, distributionPath, "Modern distribution must be one-way Home-to-Cloud and match partition staleness")
		}
		for _, siteRef := range distribution.To.SiteRefs {
			distributedCloudSites[siteRef] = struct{}{}
		}
	}
	distributedSiteRefs := make([]string, 0, len(distributedCloudSites))
	for siteRef := range distributedCloudSites {
		distributedSiteRefs = append(distributedSiteRefs, siteRef)
	}
	sort.Strings(distributedSiteRefs)
	if !exactStringList(distributedSiteRefs, validated.cloudSiteRefs) {
		return nil, fail(ErrInvalidPlan, path+".identityTrust.verifierDistributions", "one-way distributions must exactly cover every Cloud Site")
	}
	if err := rejectIdentityTrustProjectionLeaks(raw, path); err != nil {
		return nil, err
	}
	governedSites := append(append([]string(nil), validated.homeSiteRefs...), validated.cloudSiteRefs...)
	sort.Strings(governedSites)
	return governedSites, nil
}
