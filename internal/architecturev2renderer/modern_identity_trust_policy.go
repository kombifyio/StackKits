package architecturev2renderer

import (
	"bytes"
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

type ModernIdentityTrustEnforcementPolicy struct {
	StackID         string
	HomeSiteRefs    []string
	CloudSiteRefs   []string
	MaxStaleSeconds int
	Verifiers       []ModernIdentityTrustVerifier
	Distributions   []ModernIdentityTrustDistribution
}

type ModernIdentityTrustVerifier struct {
	ID                            string
	Principal                     string
	CredentialIssuerRef           string
	Issuer                        string
	Audiences                     []string
	VerificationKeySetRef         string
	SiteRefs                      []string
	SiteKind                      string
	ProofOfPossessionRequired     bool
	RevocationMaxStalenessSeconds int
}

type ModernIdentityTrustDistribution struct {
	ID                  string
	Principal           string
	CredentialIssuerRef string
	Issuer              string
	FromSiteRefs        []string
	ToSiteRefs          []string
	Materials           []string
	MaxStalenessSeconds int
}

type modernIdentityTrustPolicyArtifact struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Contract   struct {
		CloudEnrollment           string `json:"cloudEnrollment"`
		CloudIssuance             string `json:"cloudIssuance"`
		CredentialIssuanceRuntime string `json:"credentialIssuanceRuntime"`
		CredentialMaterial        string `json:"credentialMaterial"`
		EnrollmentAuthority       string `json:"enrollmentAuthority"`
		GeneralLANReachability    string `json:"generalLANReachability"`
		JWKSBytes                 string `json:"jwksBytes"`
		PrivateKeys               string `json:"privateKeys"`
		ReverseDistribution       string `json:"reverseDistribution"`
		RuntimeEndpoints          string `json:"runtimeEndpoints"`
		RuntimeEnforcement        string `json:"runtimeEnforcement"`
		Scope                     string `json:"scope"`
		SigningAuthority          string `json:"signingAuthority"`
		TransportRealization      string `json:"transportRealization"`
	} `json:"contract"`
	PlanInputs json.RawMessage `json:"planInputs"`
}

type modernVerifierDistributionPolicyArtifact struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Contract   struct {
		CloudEnrollment           string `json:"cloudEnrollment"`
		CloudIssuance             string `json:"cloudIssuance"`
		CredentialIssuanceRuntime string `json:"credentialIssuanceRuntime"`
		CredentialMaterial        string `json:"credentialMaterial"`
		Direction                 string `json:"direction"`
		EnrollmentAuthority       string `json:"enrollmentAuthority"`
		GeneralLANReachability    string `json:"generalLANReachability"`
		JWKSBytes                 string `json:"jwksBytes"`
		PrivateKeys               string `json:"privateKeys"`
		ReverseDistribution       string `json:"reverseDistribution"`
		RuntimeDistribution       string `json:"runtimeDistribution"`
		RuntimeEndpoints          string `json:"runtimeEndpoints"`
		RuntimeEnforcement        string `json:"runtimeEnforcement"`
		Scope                     string `json:"scope"`
		SigningAuthority          string `json:"signingAuthority"`
		TransportRealization      string `json:"transportRealization"`
	} `json:"contract"`
	PlanInputs json.RawMessage `json:"planInputs"`
}

// ValidateModernIdentityTrustPolicyArtifacts validates both jointly hashed
// Modern artifacts and returns only verifier/distribution policy. Home signing
// and enrollment, key bytes, credentials, transport, endpoints, provider
// lifecycle, and general LAN authority remain structurally absent.
func ValidateModernIdentityTrustPolicyArtifacts(trustRaw, distributionRaw []byte) (ModernIdentityTrustEnforcementPolicy, error) {
	var trust modernIdentityTrustPolicyArtifact
	if err := decodeStrict(trustRaw, &trust); err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, wrap(ErrInvalidPlan, "modernIdentityTrustPolicy", "decode exact Modern trust artifact", err)
	}
	var distribution modernVerifierDistributionPolicyArtifact
	if err := decodeStrict(distributionRaw, &distribution); err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, wrap(ErrInvalidPlan, "modernVerifierDistributionPolicy", "decode exact Modern distribution artifact", err)
	}
	tc, dc := trust.Contract, distribution.Contract
	if trust.APIVersion != "stackkit.modern-identity-trust-policy/v1" || trust.Kind != "ModernIdentityTrustPolicy" ||
		tc.CloudEnrollment != "deny" || tc.CloudIssuance != "deny" || tc.CredentialIssuanceRuntime != "unverified" || tc.CredentialMaterial != "not-included" ||
		tc.EnrollmentAuthority != "home-only" || tc.GeneralLANReachability != "deny" || tc.JWKSBytes != "not-included" || tc.PrivateKeys != "not-included" ||
		tc.ReverseDistribution != "deny" || tc.RuntimeEndpoints != "not-included" || tc.RuntimeEnforcement != "unverified" || tc.Scope != "generation-only" ||
		tc.SigningAuthority != "home-only" || tc.TransportRealization != "not-included" {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, "modernIdentityTrustPolicy.contract", "artifact widens or fabricates the Modern Home-authority contract")
	}
	if distribution.APIVersion != "stackkit.modern-verifier-distribution-policy/v1" || distribution.Kind != "ModernVerifierDistributionPolicy" ||
		dc.CloudEnrollment != "deny" || dc.CloudIssuance != "deny" || dc.CredentialIssuanceRuntime != "unverified" || dc.CredentialMaterial != "not-included" ||
		dc.Direction != "home-to-cloud-only" || dc.EnrollmentAuthority != "not-included" || dc.GeneralLANReachability != "deny" || dc.JWKSBytes != "not-included" || dc.PrivateKeys != "not-included" ||
		dc.ReverseDistribution != "deny" || dc.RuntimeDistribution != "unverified" || dc.RuntimeEndpoints != "not-included" || dc.RuntimeEnforcement != "unverified" ||
		dc.Scope != "generation-only" || dc.SigningAuthority != "not-included" || dc.TransportRealization != "not-included" {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, "modernVerifierDistributionPolicy.contract", "artifact widens or fabricates the one-way verifier distribution contract")
	}
	if !bytes.Equal(trust.PlanInputs, distribution.PlanInputs) {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, "modernIdentityTrustPolicy.planInputs", "joint Modern artifacts must carry byte-identical canonical plan inputs")
	}
	governedSites, err := validateModernIdentityTrustPlanInputs(trust.PlanInputs, "modernIdentityTrustPolicy.planInputs")
	if err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	var inputs modernIdentityTrustPlanInputs
	if err := decodeStrict(trust.PlanInputs, &inputs); err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, wrap(ErrInvalidPlan, "modernIdentityTrustPolicy.planInputs", "decode validated Modern policy", err)
	}
	_, homeSites, cloudSites, err := validateLocalAutonomySites(inputs.Sites, "modernIdentityTrustPolicy.planInputs")
	if err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	if len(governedSites) != len(homeSites)+len(cloudSites) {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, "modernIdentityTrustPolicy.planInputs.sites", "governed Site closure changed after validation")
	}
	issuers := make(map[string]identityTrustCredentialIssuer, len(inputs.IdentityTrust.CredentialIssuers))
	for _, issuer := range inputs.IdentityTrust.CredentialIssuers {
		issuers[issuer.ID] = issuer
	}
	policy := ModernIdentityTrustEnforcementPolicy{
		StackID: inputs.StackID, HomeSiteRefs: append([]string(nil), homeSites...), CloudSiteRefs: append([]string(nil), cloudSites...),
		MaxStaleSeconds: inputs.FailurePolicy.MaxStaleVerificationSeconds,
	}
	for _, verifier := range inputs.IdentityTrust.VerifierPlacements {
		siteKind := "home"
		if allSiteRefsHaveKind(verifier.Placement.SiteRefs, mapSiteKinds(inputs.Sites), "cloud") {
			siteKind = "cloud"
		}
		policy.Verifiers = append(policy.Verifiers, ModernIdentityTrustVerifier{
			ID: verifier.ID, Principal: verifier.Principal, CredentialIssuerRef: verifier.CredentialIssuerRef, Issuer: verifier.Issuer,
			Audiences: append([]string(nil), verifier.Audiences...), VerificationKeySetRef: verifier.VerificationKeySetRef,
			SiteRefs: append([]string(nil), verifier.Placement.SiteRefs...), SiteKind: siteKind,
			ProofOfPossessionRequired: verifier.ProofOfPossessionRequired, RevocationMaxStalenessSeconds: verifier.RevocationMaxStalenessSeconds,
		})
	}
	for _, item := range inputs.IdentityTrust.VerifierDistributions {
		policy.Distributions = append(policy.Distributions, ModernIdentityTrustDistribution{
			ID: item.ID, Principal: issuers[item.CredentialIssuerRef].Principal, CredentialIssuerRef: item.CredentialIssuerRef, Issuer: item.Issuer,
			FromSiteRefs: append([]string(nil), item.From.SiteRefs...), ToSiteRefs: append([]string(nil), item.To.SiteRefs...),
			Materials: append([]string(nil), item.Materials...), MaxStalenessSeconds: item.MaxStalenessSeconds,
		})
	}
	sort.Slice(policy.Verifiers, func(left, right int) bool { return policy.Verifiers[left].ID < policy.Verifiers[right].ID })
	sort.Slice(policy.Distributions, func(left, right int) bool { return policy.Distributions[left].ID < policy.Distributions[right].ID })
	return policy, nil
}

func mapSiteKinds(sites []localAutonomySite) map[string]string {
	kinds := make(map[string]string, len(sites))
	for _, site := range sites {
		kinds[site.ID] = site.Kind
	}
	return kinds
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
	verifierCoverage := make(map[string]map[string]struct{}, 3)
	for index, verifier := range inputs.IdentityTrust.VerifierPlacements {
		verifierPath := fmt.Sprintf("%s.identityTrust.verifierPlacements[%d]", path, index)
		if verifier.Placement.Kind != "sites" {
			return nil, fail(ErrInvalidPlan, verifierPath, "Modern verifiers must remain on governed Home or Cloud Sites")
		}
		atHome := allSiteRefsHaveKind(verifier.Placement.SiteRefs, validated.siteKinds, "home")
		atCloud := allSiteRefsHaveKind(verifier.Placement.SiteRefs, validated.siteKinds, "cloud")
		if !atHome && !atCloud || atHome && verifier.RevocationMaxStalenessSeconds != 0 || atCloud && verifier.RevocationMaxStalenessSeconds != inputs.FailurePolicy.MaxStaleVerificationSeconds {
			return nil, fail(ErrInvalidPlan, verifierPath, "Home verifiers require zero-stale local state; Cloud verifiers must match partition staleness")
		}
		if err := requireCatalogOwner(verifier.Owner, modernIdentityTrustProviderRef, modernIdentityTrustPolicyModuleID, verifierPath+".owner"); err != nil {
			return nil, err
		}
		if verifierCoverage[verifier.Principal] == nil {
			verifierCoverage[verifier.Principal] = make(map[string]struct{})
		}
		for _, siteRef := range verifier.Placement.SiteRefs {
			verifierCoverage[verifier.Principal][siteRef] = struct{}{}
		}
	}
	governedSiteRefs := append(append([]string(nil), validated.homeSiteRefs...), validated.cloudSiteRefs...)
	sort.Strings(governedSiteRefs)
	for _, principal := range []string{"device", "human", "workload"} {
		covered := sortedMapKeys(verifierCoverage[principal])
		if !exactStringList(covered, governedSiteRefs) {
			return nil, fail(ErrInvalidPlan, path+".identityTrust.verifierPlacements", "Modern %s verifiers must exactly cover every Home and Cloud Site", principal)
		}
	}
	distributionCoverage := make(map[string]map[string]struct{}, 3)
	for index, distribution := range inputs.IdentityTrust.VerifierDistributions {
		distributionPath := fmt.Sprintf("%s.identityTrust.verifierDistributions[%d]", path, index)
		if err := requireCatalogOwner(distribution.Owner, modernIdentityTrustProviderRef, modernIdentityTrustPolicyModuleID, distributionPath+".owner"); err != nil {
			return nil, err
		}
		if !allSiteRefsHaveKind(distribution.From.SiteRefs, validated.siteKinds, "home") || !allSiteRefsHaveKind(distribution.To.SiteRefs, validated.siteKinds, "cloud") ||
			distribution.MaxStalenessSeconds != inputs.FailurePolicy.MaxStaleVerificationSeconds {
			return nil, fail(ErrInvalidPlan, distributionPath, "Modern distribution must be one-way Home-to-Cloud and match partition staleness")
		}
		principal := validated.issuers[distribution.CredentialIssuerRef].Principal
		if distributionCoverage[principal] == nil {
			distributionCoverage[principal] = make(map[string]struct{})
		}
		for _, siteRef := range distribution.To.SiteRefs {
			distributionCoverage[principal][siteRef] = struct{}{}
		}
	}
	for _, principal := range []string{"device", "human", "workload"} {
		if !exactStringList(sortedMapKeys(distributionCoverage[principal]), validated.cloudSiteRefs) {
			return nil, fail(ErrInvalidPlan, path+".identityTrust.verifierDistributions", "one-way %s distribution must exactly cover every Cloud Site", principal)
		}
	}
	if err := rejectIdentityTrustProjectionLeaks(raw, path); err != nil {
		return nil, err
	}
	governedSites := append(append([]string(nil), validated.homeSiteRefs...), validated.cloudSiteRefs...)
	sort.Strings(governedSites)
	return governedSites, nil
}

func sortedMapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
