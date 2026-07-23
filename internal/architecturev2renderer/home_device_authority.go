package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
)

const homeDeviceAuthorityPolicyToken = "@@POLICY@@"

const homeDeviceAuthorityPolicyTemplate = `{"apiVersion":"stackkit.home-device-authority-policy/v1","kind":"HomeDeviceAuthorityPolicy","contract":{"credentialIssuanceRuntime":"unverified","credentialMaterial":"not-included","enrollment":"home-local","jwksBytes":"not-included","privateKeys":"not-included","runtimeEndpoints":"not-included","runtimeEnforcement":"unverified","scope":"generation-only","signingRuntime":"not-included"},"policy":@@POLICY@@}
`

var homeDeviceAuthorityPlanInputRefs = []string{"kit", "stackId"}
var homeDeviceAuthorityPublicInputRefs = []string{"home-device-authority"}

type homeDeviceAuthorityPolicyRenderer struct {
	template []byte
	contract RendererContract
}

func newHomeDeviceAuthorityPolicyRenderer() homeDeviceAuthorityPolicyRenderer {
	sum := sha256.Sum256([]byte(homeDeviceAuthorityPolicyTemplate))
	return homeDeviceAuthorityPolicyRenderer{
		template: []byte(homeDeviceAuthorityPolicyTemplate),
		contract: RendererContract{
			Kind:         "native-config",
			RendererRef:  identityTrustPolicyRendererRef,
			TemplateRef:  homeDeviceAuthorityPolicyTemplateRef,
			Version:      identityTrustPolicyVersion,
			ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
		},
	}
}

func HomeDeviceAuthorityPolicyRendererContract() RendererContract {
	return newHomeDeviceAuthorityPolicyRenderer().contract
}

func (r homeDeviceAuthorityPolicyRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	policy, err := validateHomeDeviceAuthorityUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(policy)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.home-device-authority.policy", "marshal typed Home authority policy", err)
	}
	sum := sha256.Sum256(r.template)
	if "sha256:"+hex.EncodeToString(sum[:]) != r.contract.ContractHash || bytes.Count(r.template, []byte(homeDeviceAuthorityPolicyToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.home-device-authority.template", "embedded Home authority policy does not match its registered contract")
	}
	return []UnitOutput{{
		Ref:   homeDeviceAuthorityPolicyOutputRef,
		Bytes: bytes.Replace(r.template, []byte(homeDeviceAuthorityPolicyToken), canonical, 1),
	}}, nil
}

type homeDeviceAuthorityPlanInputs struct {
	StackID string           `json:"stackId"`
	Kit     localAutonomyKit `json:"kit"`
}

type homeDeviceAuthorityValues struct {
	Authority HomeDeviceAuthorityInput `json:"home-device-authority"`
}

type HomeDeviceAuthorityInput struct {
	Authority HomeDeviceAuthorityIdentity `json:"authority"`
	Issuer    HomeDeviceCredentialIssuer  `json:"issuer"`
}

type HomeDeviceAuthorityIdentity struct {
	ID             string `json:"id"`
	TrustDomainRef string `json:"trustDomainRef"`
	SiteRef        string `json:"siteRef"`
}

type HomeDeviceEnrollment struct {
	Mode     string `json:"mode"`
	Exposure string `json:"exposure"`
}

type HomeDeviceCredentialIssuer struct {
	ID                            string               `json:"id"`
	AuthorityRef                  string               `json:"authorityRef"`
	Issuer                        string               `json:"issuer"`
	Audiences                     []string             `json:"audiences"`
	VerificationKeySetRef         string               `json:"verificationKeySetRef"`
	CredentialTTLSeconds          int                  `json:"lifetimeSeconds"`
	SessionTTLSeconds             int                  `json:"sessionTTLSeconds"`
	ProofOfPossessionRequired     bool                 `json:"proofOfPossessionRequired"`
	RevocationSupported           bool                 `json:"revocationSupported"`
	RevocationMaxStalenessSeconds int                  `json:"revocationMaxStalenessSeconds"`
	Enrollment                    HomeDeviceEnrollment `json:"enrollment"`
}

// HomeDeviceAuthorityEnforcementPolicy is the complete operation-shaped
// runtime custody document. Human/workload authorities, verifier placement,
// distribution, key bytes, endpoints, credentials, provider identity and
// lifecycle authority are structurally absent.
type HomeDeviceAuthorityEnforcementPolicy struct {
	StackID   string                      `json:"stackId"`
	KitSlug   string                      `json:"kitSlug"`
	Authority HomeDeviceAuthorityIdentity `json:"authority"`
	Issuer    HomeDeviceCredentialIssuer  `json:"issuer"`
}

//nolint:gocyclo // Every allowed field and authority boundary is checked together.
func validateHomeDeviceAuthorityUnit(unit RenderUnit, contract RendererContract) (HomeDeviceAuthorityEnforcementPolicy, error) {
	path := "resolvedPlan.modules." + homeDeviceAuthorityPolicyModuleID + ".renderUnits." + identityTrustPolicyUnitID
	if unit.ModuleID() != homeDeviceAuthorityPolicyModuleID || unit.ID() != identityTrustPolicyUnitID {
		return HomeDeviceAuthorityEnforcementPolicy{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", homeDeviceAuthorityPolicyModuleID, identityTrustPolicyUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef ||
		unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return HomeDeviceAuthorityEnforcementPolicy{}, fail(ErrOutputChanged, path, "render-unit identity differs from the registered Home device-authority contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.RuntimeKind() != "native" || unit.RuntimeDelivery() != "stackkit" || unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		unit.InstanceID() != identityTrustPolicyUnitID+"-node-"+nodeRef {
		return HomeDeviceAuthorityEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "requires one exact node-local native/stackkit target")
	}
	if _, present := unit.RuntimeEngine(); present {
		return HomeDeviceAuthorityEnforcementPolicy{}, fail(ErrInvalidPlan, path+".runtime.engine", "runtime-engine authority is forbidden")
	}
	if _, present := unit.DaemonRef(); present {
		return HomeDeviceAuthorityEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "daemon authority is forbidden")
	}
	if !exactStringList(unit.PublicInputRefs(), homeDeviceAuthorityPublicInputRefs) || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) {
		return HomeDeviceAuthorityEnforcementPolicy{}, fail(ErrInvalidPlan, path+".inputs", "accepts only the exact typed Home device-authority input")
	}
	if !exactStringList(unit.PlanInputRefs(), homeDeviceAuthorityPlanInputRefs) {
		return HomeDeviceAuthorityEnforcementPolicy{}, fail(ErrInvalidPlan, path+".planInputRefs", "must contain only stack and governed Kit identity")
	}
	if err := validateHomeDeviceAuthorityBinding(unit.InputBindingsJSON(), path+".inputBindings"); err != nil {
		return HomeDeviceAuthorityEnforcementPolicy{}, err
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return HomeDeviceAuthorityEnforcementPolicy{}, fail(ErrInvalidPlan, path+".interfaces", "service, network, endpoint, socket and privileged-interface authority is forbidden")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return HomeDeviceAuthorityEnforcementPolicy{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != homeDeviceAuthorityPolicyOutputRef {
		return HomeDeviceAuthorityEnforcementPolicy{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", homeDeviceAuthorityPolicyOutputRef)
	}
	var inputs homeDeviceAuthorityPlanInputs
	if err := decodeStrict(unit.PlanInputsJSON(), &inputs); err != nil {
		return HomeDeviceAuthorityEnforcementPolicy{}, wrap(ErrInvalidPlan, path+".planInputs", "decode bounded Home authority plan identity", err)
	}
	if err := requireContractID(inputs.StackID, path+".planInputs.stackId"); err != nil {
		return HomeDeviceAuthorityEnforcementPolicy{}, err
	}
	if inputs.Kit.Slug != "basement-kit" && inputs.Kit.Slug != "modern-homelab" {
		return HomeDeviceAuthorityEnforcementPolicy{}, fail(ErrInvalidPlan, path+".planInputs.kit.slug", "Home device authority is unavailable to kit %q", inputs.Kit.Slug)
	}
	if stringsTrim(inputs.Kit.Version) == "" || !validSHA256(inputs.Kit.DefinitionHash) {
		return HomeDeviceAuthorityEnforcementPolicy{}, fail(ErrInvalidPlan, path+".planInputs.kit", "requires exact governed Kit identity")
	}
	var values homeDeviceAuthorityValues
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return HomeDeviceAuthorityEnforcementPolicy{}, wrap(ErrInvalidPlan, path+".values", "decode exact Home device-authority input", err)
	}
	authority, err := validateHomeDeviceAuthorityInput(values.Authority, inputs.StackID, path+".values.home-device-authority")
	if err != nil {
		return HomeDeviceAuthorityEnforcementPolicy{}, err
	}
	if authority.Authority.SiteRef != siteRef || !exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!containsExact(unit.LogicalNodeRefs(), nodeRef) {
		return HomeDeviceAuthorityEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "runtime instance must bind the exact projected Home authority Site and eligible node")
	}
	return HomeDeviceAuthorityEnforcementPolicy{
		StackID: inputs.StackID, KitSlug: inputs.Kit.Slug, Authority: authority.Authority, Issuer: authority.Issuer,
	}, nil
}

func validateHomeDeviceAuthorityBinding(raw []byte, path string) error {
	var bindings []rawModuleRenderInputBinding
	if err := decodeStrict(raw, &bindings); err != nil {
		return wrap(ErrInvalidPlan, path, "decode Home device-authority input binding", err)
	}
	expected := []rawModuleRenderInputBinding{{
		TargetRef: "home-device-authority", SourceRef: "identityTrust.homeDeviceAuthority",
		ValueType: "home-device-authority-v1", Cardinality: "single", Required: true,
	}}
	if !reflect.DeepEqual(bindings, expected) {
		return fail(ErrInvalidPlan, path, "must exactly match the governed Home device-authority binding")
	}
	return nil
}

func validateHomeDeviceAuthorityInput(value HomeDeviceAuthorityInput, stackID, path string) (HomeDeviceAuthorityInput, error) {
	if err := requireContractID(value.Authority.ID, path+".authority.id"); err != nil {
		return value, err
	}
	if err := requireContractID(value.Authority.TrustDomainRef, path+".authority.trustDomainRef"); err != nil {
		return value, err
	}
	if err := requireContractID(value.Authority.SiteRef, path+".authority.siteRef"); err != nil {
		return value, err
	}
	if err := requireContractID(value.Issuer.ID, path+".issuer.id"); err != nil {
		return value, err
	}
	if value.Issuer.AuthorityRef != value.Authority.ID {
		return value, fail(ErrInvalidPlan, path+".issuer.authorityRef", "issuer must bind the exact projected authority")
	}
	if err := requireStackInstanceURN(value.Issuer.Issuer, stackID, "issuer", path+".issuer.issuer"); err != nil {
		return value, err
	}
	if err := requireStackInstanceURN(value.Issuer.VerificationKeySetRef, stackID, "keyset", path+".issuer.verificationKeySetRef"); err != nil {
		return value, err
	}
	if err := requireSortedStackInstanceURNs(value.Issuer.Audiences, stackID, "audience", path+".issuer.audiences"); err != nil {
		return value, err
	}
	if value.Issuer.CredentialTTLSeconds < 300 || value.Issuer.CredentialTTLSeconds > 86400 ||
		value.Issuer.SessionTTLSeconds < 60 || value.Issuer.SessionTTLSeconds > 86400 ||
		value.Issuer.RevocationMaxStalenessSeconds < 0 || value.Issuer.RevocationMaxStalenessSeconds > value.Issuer.CredentialTTLSeconds {
		return value, fail(ErrInvalidPlan, path+".issuer", "credential TTL and revocation staleness must be bounded")
	}
	if !value.Issuer.ProofOfPossessionRequired || !value.Issuer.RevocationSupported ||
		value.Issuer.Enrollment.Mode != "local-only" || value.Issuer.Enrollment.Exposure != "lan" {
		return value, fail(ErrInvalidPlan, path+".issuer", "Home device issuer requires possession-bound revocable LAN-local enrollment")
	}
	return value, nil
}

func decodeHomeDeviceAuthorityInput(raw json.RawMessage, path string) (HomeDeviceAuthorityInput, error) {
	var value HomeDeviceAuthorityInput
	if err := decodeStrict(raw, &value); err != nil {
		return value, wrap(ErrInvalidPlan, path, "decode closed Home device-authority projection", err)
	}
	parts := strings.Split(value.Issuer.Issuer, ":")
	if len(parts) != 5 || parts[0] != "urn" || parts[1] != "stackkit" || parts[3] != "issuer" {
		return value, fail(ErrInvalidPlan, path+".issuer.issuer", "requires a canonical StackInstance issuer URN")
	}
	return validateHomeDeviceAuthorityInput(value, parts[2], path)
}

type homeDeviceAuthorityPolicyArtifact struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Contract   struct {
		CredentialIssuanceRuntime string `json:"credentialIssuanceRuntime"`
		CredentialMaterial        string `json:"credentialMaterial"`
		Enrollment                string `json:"enrollment"`
		JWKSBytes                 string `json:"jwksBytes"`
		PrivateKeys               string `json:"privateKeys"`
		RuntimeEndpoints          string `json:"runtimeEndpoints"`
		RuntimeEnforcement        string `json:"runtimeEnforcement"`
		Scope                     string `json:"scope"`
		SigningRuntime            string `json:"signingRuntime"`
	} `json:"contract"`
	Policy HomeDeviceAuthorityEnforcementPolicy `json:"policy"`
}

// ValidateHomeDeviceAuthorityPolicyArtifact validates the exact
// operation-shaped policy consumed by the local Home authority owner.
func ValidateHomeDeviceAuthorityPolicyArtifact(raw []byte) (HomeDeviceAuthorityEnforcementPolicy, error) {
	var document homeDeviceAuthorityPolicyArtifact
	if err := decodeStrict(raw, &document); err != nil {
		return HomeDeviceAuthorityEnforcementPolicy{}, wrap(ErrInvalidPlan, "homeDeviceAuthorityPolicy", "decode exact Home device-authority artifact", err)
	}
	contract := document.Contract
	if document.APIVersion != "stackkit.home-device-authority-policy/v1" || document.Kind != "HomeDeviceAuthorityPolicy" ||
		contract.CredentialIssuanceRuntime != "unverified" || contract.CredentialMaterial != "not-included" || contract.Enrollment != "home-local" ||
		contract.JWKSBytes != "not-included" || contract.PrivateKeys != "not-included" || contract.RuntimeEndpoints != "not-included" ||
		contract.RuntimeEnforcement != "unverified" || contract.Scope != "generation-only" || contract.SigningRuntime != "not-included" {
		return HomeDeviceAuthorityEnforcementPolicy{}, fail(ErrInvalidPlan, "homeDeviceAuthorityPolicy.contract", "artifact widens or fabricates the generation-only Home device-authority contract")
	}
	input := HomeDeviceAuthorityInput{Authority: document.Policy.Authority, Issuer: document.Policy.Issuer}
	if document.Policy.KitSlug != "basement-kit" && document.Policy.KitSlug != "modern-homelab" {
		return HomeDeviceAuthorityEnforcementPolicy{}, fail(ErrInvalidPlan, "homeDeviceAuthorityPolicy.policy.kitSlug", "artifact has no Home authority Kit")
	}
	if _, err := validateHomeDeviceAuthorityInput(input, document.Policy.StackID, "homeDeviceAuthorityPolicy.policy"); err != nil {
		return HomeDeviceAuthorityEnforcementPolicy{}, err
	}
	return document.Policy, nil
}

func stringsTrim(value string) string {
	for len(value) > 0 && (value[0] == ' ' || value[0] == '\t' || value[0] == '\r' || value[0] == '\n') {
		value = value[1:]
	}
	for len(value) > 0 {
		last := value[len(value)-1]
		if last != ' ' && last != '\t' && last != '\r' && last != '\n' {
			break
		}
		value = value[:len(value)-1]
	}
	return value
}

var _ UnitRenderer = homeDeviceAuthorityPolicyRenderer{}
