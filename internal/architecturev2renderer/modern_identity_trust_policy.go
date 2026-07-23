package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
)

const (
	modernHomeIdentityTrustPolicyModuleID     = "stackkits-modern-home-identity-trust-policy-manifest"
	modernCloudIdentityVerifierPolicyModuleID = "stackkits-modern-cloud-identity-verifier-policy-manifest"
	modernIdentityTrustPolicyTemplateRef      = "builtin://modern/home-identity-trust-policy/v1.json"
	modernCloudIdentityPolicyTemplateRef      = "builtin://modern/cloud-identity-verifier-policy/v1.json"
	modernIdentityTrustPolicyOutputRef        = "modern/identity/trust-policy.json"
	modernVerifierDistributionPolicyOutputRef = "modern/identity/verifier-distribution-policy.json"
	modernIdentityTrustProviderRef            = "stackkits-modern-identity-trust-policy"
	modernIdentityTrustContractRef            = "modern-identity-trust-policy-contract"
	modernIdentityPolicyToken                 = "@@POLICY@@"
)

const modernIdentityTrustPolicyTemplate = `{"apiVersion":"stackkit.modern-identity-trust-policy/v1","kind":"ModernIdentityTrustPolicy","contract":{"cloudEnrollment":"deny","cloudIssuance":"deny","credentialIssuanceRuntime":"unverified","credentialMaterial":"not-included","enrollmentAuthority":"home-only","generalLANReachability":"deny","jwksBytes":"not-included","privateKeys":"not-included","reverseDistribution":"deny","runtimeEndpoints":"not-included","runtimeEnforcement":"unverified","scope":"generation-only","signingRuntime":"not-included","transportRealization":"not-included"},"policy":@@POLICY@@}
`

const modernVerifierDistributionPolicyTemplate = `{"apiVersion":"stackkit.modern-verifier-distribution-policy/v1","kind":"ModernVerifierDistributionPolicy","contract":{"cloudEnrollment":"deny","cloudIssuance":"deny","credentialIssuanceRuntime":"unverified","credentialMaterial":"not-included","direction":"home-to-cloud-only","enrollmentAuthority":"not-included","generalLANReachability":"deny","jwksBytes":"not-included","privateKeys":"not-included","reverseDistribution":"deny","runtimeDistribution":"unverified","runtimeEndpoints":"not-included","runtimeEnforcement":"unverified","scope":"generation-only","signingRuntime":"not-included","transportRealization":"not-included"},"policy":@@POLICY@@}
`

var modernIdentityProjectionPlanInputRefs = []string{"kit", "stackId"}

type modernIdentityProjectionRenderer struct {
	role, moduleID, inputRef, sourceRef, valueType, outputRef string
	template                                                  []byte
	contract                                                  RendererContract
}

func init() {
	registerProductRegistryExtension(func(registry *Registry) error {
		home := newModernHomeIdentityTrustPolicyRenderer()
		if err := registry.Register(home.contract, home); err != nil {
			return err
		}
		cloud := newModernCloudIdentityVerifierPolicyRenderer()
		return registry.Register(cloud.contract, cloud)
	})
}

func newModernHomeIdentityTrustPolicyRenderer() modernIdentityProjectionRenderer {
	return newModernIdentityProjectionRenderer(
		"home", modernHomeIdentityTrustPolicyModuleID, "modern-home-identity-authority",
		"identityTrust.modernHomeAuthority", "modern-home-identity-authority-v1",
		modernIdentityTrustPolicyTemplateRef, modernIdentityTrustPolicyOutputRef, modernIdentityTrustPolicyTemplate,
	)
}

func newModernCloudIdentityVerifierPolicyRenderer() modernIdentityProjectionRenderer {
	return newModernIdentityProjectionRenderer(
		"cloud", modernCloudIdentityVerifierPolicyModuleID, "modern-cloud-identity-verification",
		"identityTrust.modernCloudVerification", "modern-cloud-identity-verification-v1",
		modernCloudIdentityPolicyTemplateRef, modernVerifierDistributionPolicyOutputRef, modernVerifierDistributionPolicyTemplate,
	)
}

func newModernIdentityProjectionRenderer(role, moduleID, inputRef, sourceRef, valueType, templateRef, outputRef, template string) modernIdentityProjectionRenderer {
	sum := sha256.Sum256([]byte(template))
	return modernIdentityProjectionRenderer{
		role: role, moduleID: moduleID, inputRef: inputRef, sourceRef: sourceRef, valueType: valueType, outputRef: outputRef,
		template: []byte(template),
		contract: RendererContract{Kind: "native-config", RendererRef: identityTrustPolicyRendererRef, TemplateRef: templateRef, Version: identityTrustPolicyVersion, ContractHash: "sha256:" + hex.EncodeToString(sum[:])},
	}
}

func ModernHomeIdentityTrustPolicyRendererContract() RendererContract {
	return newModernHomeIdentityTrustPolicyRenderer().contract
}

func ModernCloudIdentityVerifierPolicyRendererContract() RendererContract {
	return newModernCloudIdentityVerifierPolicyRenderer().contract
}

type ModernIdentityTrustEnforcementPolicy struct {
	StackID         string                            `json:"stackId"`
	KitSlug         string                            `json:"kitSlug"`
	HomeSiteRefs    []string                          `json:"homeSiteRefs"`
	CloudSiteRefs   []string                          `json:"cloudSiteRefs"`
	MaxStaleSeconds int                               `json:"maxStaleSeconds"`
	Issuers         []ModernIdentityTrustIssuer       `json:"issuers,omitempty"`
	Verifiers       []ModernIdentityTrustVerifier     `json:"verifiers"`
	Distributions   []ModernIdentityTrustDistribution `json:"distributions"`
}

type ModernIdentityTrustIssuer struct {
	ID                            string   `json:"id"`
	AuthorityRef                  string   `json:"authorityRef"`
	Principal                     string   `json:"principal"`
	Issuer                        string   `json:"issuer"`
	Audiences                     []string `json:"audiences"`
	VerificationKeySetRef         string   `json:"verificationKeySetRef"`
	ProofOfPossessionRequired     bool     `json:"proofOfPossessionRequired"`
	RevocationMaxStalenessSeconds int      `json:"revocationMaxStalenessSeconds"`
	CredentialTTLSeconds          int      `json:"lifetimeSeconds"`
	SessionTTLSeconds             int      `json:"sessionTTLSeconds"`
	EnrollmentMode                string   `json:"enrollmentMode"`
	EnrollmentExposure            string   `json:"enrollmentExposure"`
}

type ModernIdentityTrustVerifier struct {
	ID                            string   `json:"id"`
	Principal                     string   `json:"principal"`
	CredentialIssuerRef           string   `json:"issuerRef"`
	Issuer                        string   `json:"issuer"`
	Audiences                     []string `json:"audiences"`
	VerificationKeySetRef         string   `json:"verificationKeySetRef"`
	SiteRefs                      []string `json:"siteRefs,omitempty"`
	SiteKind                      string   `json:"siteKind,omitempty"`
	ProofOfPossessionRequired     bool     `json:"proofOfPossessionRequired"`
	RevocationMaxStalenessSeconds int      `json:"revocationMaxStalenessSeconds"`
}

type ModernIdentityTrustDistribution struct {
	ID                  string   `json:"id"`
	Principal           string   `json:"principal"`
	CredentialIssuerRef string   `json:"issuerRef"`
	Issuer              string   `json:"issuer"`
	FromSiteRefs        []string `json:"fromSiteRefs,omitempty"`
	ToSiteRefs          []string `json:"toSiteRefs,omitempty"`
	Materials           []string `json:"materials"`
	MaxStalenessSeconds int      `json:"maxStalenessSeconds"`
}

type modernIdentityPartition struct {
	OnCloudLoss                     string `json:"onCloudLoss,omitempty"`
	OnLinkLoss                      string `json:"onLinkLoss,omitempty"`
	CloudEdge                       string `json:"cloudEdge,omitempty"`
	LocalIdentityAuthorityAvailable bool   `json:"localIdentityAuthorityAvailable,omitempty"`
	MaxStaleVerificationSeconds     int    `json:"maxStaleVerificationSeconds"`
	DenyNewCrossSiteSessions        bool   `json:"denyNewCrossSiteSessions"`
}

type modernHomeIdentityInput struct {
	HomeSiteRef   string                            `json:"homeSiteRef"`
	CloudSiteRefs []string                          `json:"cloudSiteRefs"`
	Partition     modernIdentityPartition           `json:"partition"`
	Issuers       []ModernIdentityTrustIssuer       `json:"issuers"`
	Verifiers     []ModernIdentityTrustVerifier     `json:"verifiers"`
	Distributions []ModernIdentityTrustDistribution `json:"distributions"`
}

type modernCloudIdentityInput struct {
	HomeSiteRef   string                            `json:"homeSiteRef"`
	CloudSiteRefs []string                          `json:"cloudSiteRefs"`
	Partition     modernIdentityPartition           `json:"partition"`
	Verifiers     []ModernIdentityTrustVerifier     `json:"verifiers"`
	Distributions []ModernIdentityTrustDistribution `json:"distributions"`
}

type modernIdentityPlanInputs struct {
	StackID string           `json:"stackId"`
	Kit     localAutonomyKit `json:"kit"`
}

func (r modernIdentityProjectionRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	policy, err := validateModernIdentityProjectionUnit(unit, r)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(policy)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.modern-identity.policy", "marshal Modern identity projection", err)
	}
	sum := sha256.Sum256(r.template)
	if "sha256:"+hex.EncodeToString(sum[:]) != r.contract.ContractHash || bytes.Count(r.template, []byte(modernIdentityPolicyToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.modern-identity.template", "embedded Modern identity policy changed")
	}
	return []UnitOutput{{Ref: r.outputRef, Bytes: bytes.Replace(r.template, []byte(modernIdentityPolicyToken), canonical, 1)}}, nil
}

//nolint:gocyclo // The complete closed renderer boundary is validated together.
func validateModernIdentityProjectionUnit(unit RenderUnit, renderer modernIdentityProjectionRenderer) (ModernIdentityTrustEnforcementPolicy, error) {
	path := "resolvedPlan.modules." + renderer.moduleID + ".renderUnits.policy-bundle"
	if unit.ModuleID() != renderer.moduleID || unit.ID() != identityTrustPolicyUnitID ||
		unit.Kind() != renderer.contract.Kind || unit.RendererRef() != renderer.contract.RendererRef ||
		unit.TemplateRef() != renderer.contract.TemplateRef || unit.Version() != renderer.contract.Version || unit.ContractHash() != renderer.contract.ContractHash {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrOutputChanged, path, "render-unit identity differs from the registered Modern identity contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.RuntimeKind() != "native" || unit.RuntimeDelivery() != "stackkit" || unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		unit.InstanceID() != identityTrustPolicyUnitID+"-node-"+nodeRef {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "requires one exact node-local native/stackkit target")
	}
	if _, present := unit.RuntimeEngine(); present {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".runtime.engine", "runtime-engine authority is forbidden")
	}
	if _, present := unit.DaemonRef(); present {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "daemon authority is forbidden")
	}
	if !exactStringList(unit.PublicInputRefs(), []string{renderer.inputRef}) || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) ||
		!exactStringList(unit.PlanInputRefs(), modernIdentityProjectionPlanInputRefs) {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".inputs", "accepts only the exact owner-specific Modern identity input")
	}
	if err := validateModernIdentityProjectionBinding(unit.InputBindingsJSON(), renderer, path+".inputBindings"); err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".interfaces", "network, endpoint, socket and privileged-interface authority is forbidden")
	}
	var placement struct {
		Scope, Cardinality string
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != renderer.outputRef {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".outputs", "requires exact owner-specific output")
	}
	var plan modernIdentityPlanInputs
	if err := decodeStrict(unit.PlanInputsJSON(), &plan); err != nil || plan.Kit.Slug != "modern-homelab" || stringsTrim(plan.Kit.Version) == "" || !validSHA256(plan.Kit.DefinitionHash) {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".planInputs", "requires exact governed Modern Home Lab identity")
	}
	if err := requireContractID(plan.StackID, path+".planInputs.stackId"); err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	var values map[string]json.RawMessage
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil || len(values) != 1 {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".values", "requires exactly one owner-specific projection")
	}
	raw, exists := values[renderer.inputRef]
	if !exists {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".values", "owner-specific projection is absent")
	}
	var (
		policy ModernIdentityTrustEnforcementPolicy
		err    error
	)
	if renderer.role == "home" {
		var input modernHomeIdentityInput
		if err := decodeStrict(raw, &input); err != nil {
			return policy, wrap(ErrInvalidPlan, path+".values."+renderer.inputRef, "decode Modern Home projection", err)
		}
		policy, err = validateModernHomeIdentityInput(input, plan.StackID, path+".values."+renderer.inputRef)
	} else {
		var input modernCloudIdentityInput
		if err := decodeStrict(raw, &input); err != nil {
			return policy, wrap(ErrInvalidPlan, path+".values."+renderer.inputRef, "decode Modern Cloud projection", err)
		}
		policy, err = validateModernCloudIdentityInput(input, plan.StackID, path+".values."+renderer.inputRef)
	}
	if err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	policy.StackID, policy.KitSlug = plan.StackID, plan.Kit.Slug
	allowedSites := policy.HomeSiteRefs
	if renderer.role == "cloud" {
		allowedSites = policy.CloudSiteRefs
	}
	if !containsExact(allowedSites, siteRef) || !exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !containsExact(unit.LogicalNodeRefs(), nodeRef) {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "runtime instance crossed the owner-specific Site projection")
	}
	return policy, nil
}

func validateModernIdentityProjectionBinding(raw []byte, renderer modernIdentityProjectionRenderer, path string) error {
	var bindings []rawModuleRenderInputBinding
	if err := decodeStrict(raw, &bindings); err != nil {
		return wrap(ErrInvalidPlan, path, "decode Modern identity input binding", err)
	}
	expected := []rawModuleRenderInputBinding{{TargetRef: renderer.inputRef, SourceRef: renderer.sourceRef, ValueType: renderer.valueType, Cardinality: "single", Required: true}}
	if !reflect.DeepEqual(bindings, expected) {
		return fail(ErrInvalidPlan, path, "must exactly match the owner-specific Modern identity binding")
	}
	return nil
}

func validateModernHomeIdentityInput(input modernHomeIdentityInput, stackID, path string) (ModernIdentityTrustEnforcementPolicy, error) {
	if input.Partition.OnCloudLoss != "local-continues" || input.Partition.OnLinkLoss != "local-continues" || !input.Partition.LocalIdentityAuthorityAvailable ||
		!input.Partition.DenyNewCrossSiteSessions || input.Partition.CloudEdge != "" {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".partition", "Home partition must retain local authority without Cloud policy")
	}
	if err := validateModernSiteEnvelope(input.HomeSiteRef, input.CloudSiteRefs, input.Partition.MaxStaleVerificationSeconds, path); err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	if err := validateModernIssuers(input.Issuers, stackID, input.Partition.MaxStaleVerificationSeconds, path+".issuers"); err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	if err := validateModernVerifiers(input.Verifiers, stackID, 0, path+".verifiers"); err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	if err := validateModernDistributions(input.Distributions, stackID, input.Partition.MaxStaleVerificationSeconds, path+".distributions"); err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	verifiers, distributions := deriveModernIdentityPlacement(input.Verifiers, input.Distributions, "home", input.HomeSiteRef, input.CloudSiteRefs)
	return ModernIdentityTrustEnforcementPolicy{
		HomeSiteRefs: []string{input.HomeSiteRef}, CloudSiteRefs: append([]string(nil), input.CloudSiteRefs...),
		MaxStaleSeconds: input.Partition.MaxStaleVerificationSeconds, Issuers: input.Issuers, Verifiers: verifiers, Distributions: distributions,
	}, nil
}

func validateModernCloudIdentityInput(input modernCloudIdentityInput, stackID, path string) (ModernIdentityTrustEnforcementPolicy, error) {
	if input.Partition.CloudEdge != "fail-closed" || !input.Partition.DenyNewCrossSiteSessions ||
		input.Partition.OnCloudLoss != "" || input.Partition.OnLinkLoss != "" || input.Partition.LocalIdentityAuthorityAvailable {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".partition", "Cloud partition must be verifier-only and fail closed")
	}
	if err := validateModernSiteEnvelope(input.HomeSiteRef, input.CloudSiteRefs, input.Partition.MaxStaleVerificationSeconds, path); err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	if err := validateModernVerifiers(input.Verifiers, stackID, input.Partition.MaxStaleVerificationSeconds, path+".verifiers"); err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	if err := validateModernDistributions(input.Distributions, stackID, input.Partition.MaxStaleVerificationSeconds, path+".distributions"); err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	verifiers, distributions := deriveModernIdentityPlacement(input.Verifiers, input.Distributions, "cloud", input.HomeSiteRef, input.CloudSiteRefs)
	return ModernIdentityTrustEnforcementPolicy{
		HomeSiteRefs: []string{input.HomeSiteRef}, CloudSiteRefs: append([]string(nil), input.CloudSiteRefs...),
		MaxStaleSeconds: input.Partition.MaxStaleVerificationSeconds, Verifiers: verifiers, Distributions: distributions,
	}, nil
}

func deriveModernIdentityPlacement(verifiers []ModernIdentityTrustVerifier, distributions []ModernIdentityTrustDistribution, role, homeSiteRef string, cloudSiteRefs []string) ([]ModernIdentityTrustVerifier, []ModernIdentityTrustDistribution) {
	derivedVerifiers := append([]ModernIdentityTrustVerifier(nil), verifiers...)
	for index := range derivedVerifiers {
		derivedVerifiers[index].SiteKind = role
		derivedVerifiers[index].SiteRefs = []string{homeSiteRef}
		if role == "cloud" {
			derivedVerifiers[index].SiteRefs = append([]string(nil), cloudSiteRefs...)
		}
	}
	derivedDistributions := append([]ModernIdentityTrustDistribution(nil), distributions...)
	for index := range derivedDistributions {
		derivedDistributions[index].FromSiteRefs = []string{homeSiteRef}
		derivedDistributions[index].ToSiteRefs = append([]string(nil), cloudSiteRefs...)
	}
	return derivedVerifiers, derivedDistributions
}

func validateModernSiteEnvelope(home string, clouds []string, maxStale int, path string) error {
	if err := requireContractID(home, path+".homeSiteRef"); err != nil {
		return err
	}
	if !sortedUniqueNonEmpty(clouds) || containsExact(clouds, home) || maxStale < 0 || maxStale > 86400 {
		return fail(ErrInvalidPlan, path, "Modern Site partition or staleness is invalid")
	}
	for index, cloud := range clouds {
		if err := requireContractID(cloud, path+".cloudSiteRefs["+strconv.Itoa(index)+"]"); err != nil {
			return err
		}
	}
	return nil
}

func validateModernIssuers(values []ModernIdentityTrustIssuer, stackID string, maxStale int, path string) error {
	if len(values) != 3 {
		return fail(ErrInvalidPlan, path, "requires exactly one issuer per principal")
	}
	counts, ids := map[string]int{}, map[string]struct{}{}
	for index := range values {
		value := &values[index]
		itemPath := path + "[" + strconv.Itoa(index) + "]"
		if err := requireContractID(value.ID, itemPath+".id"); err != nil {
			return err
		}
		if err := requireContractID(value.AuthorityRef, itemPath+".authorityRef"); err != nil {
			return err
		}
		if _, duplicate := ids[value.ID]; duplicate {
			return fail(ErrInvalidPlan, itemPath+".id", "issuer ID is duplicated")
		}
		ids[value.ID], counts[value.Principal] = struct{}{}, counts[value.Principal]+1
		if !modernIdentityPrincipal(value.Principal) || value.RevocationMaxStalenessSeconds != maxStale ||
			value.CredentialTTLSeconds < 300 || value.CredentialTTLSeconds > 86400 || value.SessionTTLSeconds < 60 || value.SessionTTLSeconds > 86400 ||
			value.RevocationMaxStalenessSeconds > value.CredentialTTLSeconds {
			return fail(ErrInvalidPlan, itemPath, "issuer principal, lifetime, or partition staleness changed")
		}
		mode, exposure := "none", "none"
		if value.Principal == "device" {
			mode, exposure = "local-only", "lan"
		}
		if value.EnrollmentMode != mode || value.EnrollmentExposure != exposure {
			return fail(ErrInvalidPlan, itemPath, "issuer enrollment crossed the Home-only authority")
		}
		if err := validateModernIdentityURNs(value.Issuer, value.VerificationKeySetRef, value.Audiences, stackID, itemPath); err != nil {
			return err
		}
	}
	if !exactModernPrincipalSet(counts) {
		return fail(ErrInvalidPlan, path, "requires exactly device, human, and workload issuers")
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	return nil
}

func validateModernVerifiers(values []ModernIdentityTrustVerifier, stackID string, stale int, path string) error {
	if len(values) != 3 {
		return fail(ErrInvalidPlan, path, "requires exactly one verifier per principal")
	}
	counts, ids := map[string]int{}, map[string]struct{}{}
	for index := range values {
		value := &values[index]
		itemPath := path + "[" + strconv.Itoa(index) + "]"
		if err := requireContractID(value.ID, itemPath+".id"); err != nil {
			return err
		}
		if err := requireContractID(value.CredentialIssuerRef, itemPath+".issuerRef"); err != nil {
			return err
		}
		if _, duplicate := ids[value.ID]; duplicate {
			return fail(ErrInvalidPlan, itemPath+".id", "verifier ID is duplicated")
		}
		ids[value.ID], counts[value.Principal] = struct{}{}, counts[value.Principal]+1
		if !modernIdentityPrincipal(value.Principal) || value.RevocationMaxStalenessSeconds != stale || value.SiteKind != "" || len(value.SiteRefs) != 0 {
			return fail(ErrInvalidPlan, itemPath, "verifier principal or partition staleness changed")
		}
		if err := validateModernIdentityURNs(value.Issuer, value.VerificationKeySetRef, value.Audiences, stackID, itemPath); err != nil {
			return err
		}
	}
	if !exactModernPrincipalSet(counts) {
		return fail(ErrInvalidPlan, path, "requires exactly device, human, and workload verifiers")
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	return nil
}

func validateModernDistributions(values []ModernIdentityTrustDistribution, stackID string, stale int, path string) error {
	if len(values) != 3 {
		return fail(ErrInvalidPlan, path, "requires exactly one distribution per principal")
	}
	counts, ids := map[string]int{}, map[string]struct{}{}
	for index := range values {
		value := &values[index]
		itemPath := path + "[" + strconv.Itoa(index) + "]"
		if err := requireContractID(value.ID, itemPath+".id"); err != nil {
			return err
		}
		if err := requireContractID(value.CredentialIssuerRef, itemPath+".issuerRef"); err != nil {
			return err
		}
		if _, duplicate := ids[value.ID]; duplicate {
			return fail(ErrInvalidPlan, itemPath+".id", "distribution ID is duplicated")
		}
		ids[value.ID], counts[value.Principal] = struct{}{}, counts[value.Principal]+1
		if !modernIdentityPrincipal(value.Principal) || value.MaxStalenessSeconds != stale || len(value.FromSiteRefs) != 0 || len(value.ToSiteRefs) != 0 ||
			!reflect.DeepEqual(value.Materials, []string{"revocation-state", "verification-key-reference"}) {
			return fail(ErrInvalidPlan, itemPath, "distribution widened direction, material, or staleness")
		}
		if err := requireStackInstanceURN(value.Issuer, stackID, "issuer", itemPath+".issuer"); err != nil {
			return err
		}
	}
	if !exactModernPrincipalSet(counts) {
		return fail(ErrInvalidPlan, path, "requires exactly device, human, and workload distributions")
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	return nil
}

func validateModernIdentityURNs(issuer, keyset string, audiences []string, stackID, path string) error {
	if err := requireStackInstanceURN(issuer, stackID, "issuer", path+".issuer"); err != nil {
		return err
	}
	if err := requireStackInstanceURN(keyset, stackID, "keyset", path+".verificationKeySetRef"); err != nil {
		return err
	}
	return requireSortedStackInstanceURNs(audiences, stackID, "audience", path+".audiences")
}

func modernIdentityPrincipal(value string) bool {
	return value == "device" || value == "human" || value == "workload"
}

func exactModernPrincipalSet(counts map[string]int) bool {
	return len(counts) == 3 && counts["device"] == 1 && counts["human"] == 1 && counts["workload"] == 1
}

type modernIdentityPolicyArtifact struct {
	APIVersion string                               `json:"apiVersion"`
	Kind       string                               `json:"kind"`
	Contract   map[string]string                    `json:"contract"`
	Policy     ModernIdentityTrustEnforcementPolicy `json:"policy"`
}

func ValidateModernHomeIdentityTrustPolicyArtifact(raw []byte) (ModernIdentityTrustEnforcementPolicy, error) {
	document, err := validateModernIdentityArtifactEnvelope(raw, "home")
	if err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	normalizedPolicy, err := normalizeModernDerivedArtifactPlacement(document.Policy, "home")
	if err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	verifiers, distributions := stripModernIdentityPlacement(normalizedPolicy.Verifiers, normalizedPolicy.Distributions)
	input := modernHomeIdentityInput{
		HomeSiteRef: normalizedPolicy.HomeSiteRefs[0], CloudSiteRefs: normalizedPolicy.CloudSiteRefs,
		Partition: modernIdentityPartition{
			OnCloudLoss: "local-continues", OnLinkLoss: "local-continues", LocalIdentityAuthorityAvailable: true,
			MaxStaleVerificationSeconds: normalizedPolicy.MaxStaleSeconds, DenyNewCrossSiteSessions: true,
		},
		Issuers: normalizedPolicy.Issuers, Verifiers: verifiers, Distributions: distributions,
	}
	validated, err := validateModernHomeIdentityInput(input, normalizedPolicy.StackID, "modernIdentityTrustPolicy.policy")
	if err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	validated.StackID, validated.KitSlug = normalizedPolicy.StackID, normalizedPolicy.KitSlug
	return validated, nil
}

func ValidateModernCloudIdentityVerifierPolicyArtifact(raw []byte) (ModernIdentityTrustEnforcementPolicy, error) {
	document, err := validateModernIdentityArtifactEnvelope(raw, "cloud")
	if err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	normalizedPolicy, err := normalizeModernDerivedArtifactPlacement(document.Policy, "cloud")
	if err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	verifiers, distributions := stripModernIdentityPlacement(normalizedPolicy.Verifiers, normalizedPolicy.Distributions)
	input := modernCloudIdentityInput{
		HomeSiteRef: normalizedPolicy.HomeSiteRefs[0], CloudSiteRefs: normalizedPolicy.CloudSiteRefs,
		Partition: modernIdentityPartition{CloudEdge: "fail-closed", MaxStaleVerificationSeconds: normalizedPolicy.MaxStaleSeconds, DenyNewCrossSiteSessions: true},
		Verifiers: verifiers, Distributions: distributions,
	}
	validated, err := validateModernCloudIdentityInput(input, normalizedPolicy.StackID, "modernVerifierDistributionPolicy.policy")
	if err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	validated.StackID, validated.KitSlug = normalizedPolicy.StackID, normalizedPolicy.KitSlug
	return validated, nil
}

func normalizeModernDerivedArtifactPlacement(policy ModernIdentityTrustEnforcementPolicy, role string) (ModernIdentityTrustEnforcementPolicy, error) {
	placementPresent := false
	for _, verifier := range policy.Verifiers {
		placementPresent = placementPresent || verifier.SiteKind != "" || len(verifier.SiteRefs) != 0
	}
	for _, distribution := range policy.Distributions {
		placementPresent = placementPresent || len(distribution.FromSiteRefs) != 0 || len(distribution.ToSiteRefs) != 0
	}
	if !placementPresent {
		policy.Verifiers, policy.Distributions = deriveModernIdentityPlacement(
			policy.Verifiers,
			policy.Distributions,
			role,
			policy.HomeSiteRefs[0],
			policy.CloudSiteRefs,
		)
		return policy, nil
	}
	for index, verifier := range policy.Verifiers {
		wantRefs := policy.HomeSiteRefs
		if role == "cloud" {
			wantRefs = policy.CloudSiteRefs
		}
		if verifier.SiteKind != role || !reflect.DeepEqual(verifier.SiteRefs, wantRefs) {
			return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, "modernIdentityPolicy.policy.verifiers["+strconv.Itoa(index)+"]", "derived verifier placement crossed its owner-specific Site partition")
		}
	}
	for index, distribution := range policy.Distributions {
		if !reflect.DeepEqual(distribution.FromSiteRefs, policy.HomeSiteRefs) || !reflect.DeepEqual(distribution.ToSiteRefs, policy.CloudSiteRefs) {
			return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, "modernIdentityPolicy.policy.distributions["+strconv.Itoa(index)+"]", "derived distribution placement is not exact Home-to-Cloud")
		}
	}
	return policy, nil
}

func stripModernIdentityPlacement(verifiers []ModernIdentityTrustVerifier, distributions []ModernIdentityTrustDistribution) ([]ModernIdentityTrustVerifier, []ModernIdentityTrustDistribution) {
	strippedVerifiers := append([]ModernIdentityTrustVerifier(nil), verifiers...)
	for index := range strippedVerifiers {
		strippedVerifiers[index].SiteKind = ""
		strippedVerifiers[index].SiteRefs = nil
	}
	strippedDistributions := append([]ModernIdentityTrustDistribution(nil), distributions...)
	for index := range strippedDistributions {
		strippedDistributions[index].FromSiteRefs = nil
		strippedDistributions[index].ToSiteRefs = nil
	}
	return strippedVerifiers, strippedDistributions
}

func validateModernIdentityArtifactEnvelope(raw []byte, role string) (modernIdentityPolicyArtifact, error) {
	var document modernIdentityPolicyArtifact
	if err := decodeStrict(raw, &document); err != nil {
		return document, wrap(ErrInvalidPlan, "modernIdentityPolicy", "decode exact Modern identity artifact", err)
	}
	expectedTemplate := modernIdentityTrustPolicyTemplate
	expectedAPI, expectedKind := "stackkit.modern-identity-trust-policy/v1", "ModernIdentityTrustPolicy"
	if role == "cloud" {
		expectedTemplate = modernVerifierDistributionPolicyTemplate
		expectedAPI, expectedKind = "stackkit.modern-verifier-distribution-policy/v1", "ModernVerifierDistributionPolicy"
	}
	var templateDocument modernIdentityPolicyArtifact
	skeleton := bytes.Replace([]byte(expectedTemplate), []byte(modernIdentityPolicyToken), []byte(`{"stackId":"x"}`), 1)
	if err := json.Unmarshal(skeleton, &templateDocument); err != nil {
		return document, wrap(ErrRendererFailure, "modernIdentityPolicy.contract", "decode embedded contract", err)
	}
	if document.APIVersion != expectedAPI || document.Kind != expectedKind || !reflect.DeepEqual(document.Contract, templateDocument.Contract) ||
		document.Policy.KitSlug != "modern-homelab" || len(document.Policy.HomeSiteRefs) != 1 {
		return document, fail(ErrInvalidPlan, "modernIdentityPolicy.contract", "artifact widened or crossed the owner-specific generation-only contract")
	}
	return document, nil
}

func ValidateModernIdentityTrustPolicyArtifacts(homeRaw, cloudRaw []byte) (ModernIdentityTrustEnforcementPolicy, error) {
	home, err := ValidateModernHomeIdentityTrustPolicyArtifact(homeRaw)
	if err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	cloud, err := ValidateModernCloudIdentityVerifierPolicyArtifact(cloudRaw)
	if err != nil {
		return ModernIdentityTrustEnforcementPolicy{}, err
	}
	if home.StackID != cloud.StackID || !reflect.DeepEqual(home.HomeSiteRefs, cloud.HomeSiteRefs) || !reflect.DeepEqual(home.CloudSiteRefs, cloud.CloudSiteRefs) ||
		home.MaxStaleSeconds != cloud.MaxStaleSeconds || !reflect.DeepEqual(home.Distributions, cloud.Distributions) {
		return ModernIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, "modernIdentityPolicy", "Home and Cloud projections do not share the exact one-way handoff")
	}
	return home, nil
}

var _ UnitRenderer = modernIdentityProjectionRenderer{}
