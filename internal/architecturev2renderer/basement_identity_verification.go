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

const basementIdentityVerificationPolicyToken = "@@POLICY@@"

const basementIdentityVerificationPolicyTemplate = `{"apiVersion":"stackkit.basement-identity-trust-policy/v1","kind":"BasementIdentityTrustPolicy","contract":{"credentialIssuanceRuntime":"not-owned","credentialMaterial":"not-included","enrollment":"not-owned","externalTrust":"not-applicable","jwksBytes":"not-included","privateKeys":"not-included","runtimeEndpoints":"not-included","runtimeEnforcement":"unverified","scope":"generation-only","signingAuthority":"not-owned"},"policy":@@POLICY@@}
`

var basementIdentityVerificationPlanInputRefs = []string{"kit", "stackId"}
var basementIdentityVerificationPublicInputRefs = []string{"basement-verification-policy"}

type basementIdentityVerificationRenderer struct {
	template []byte
	contract RendererContract
}

func newBasementIdentityTrustPolicyRenderer() basementIdentityVerificationRenderer {
	sum := sha256.Sum256([]byte(basementIdentityVerificationPolicyTemplate))
	return basementIdentityVerificationRenderer{
		template: []byte(basementIdentityVerificationPolicyTemplate),
		contract: RendererContract{
			Kind: "native-config", RendererRef: identityTrustPolicyRendererRef,
			TemplateRef: basementIdentityTrustPolicyTemplateRef, Version: identityTrustPolicyVersion,
			ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
		},
	}
}

func BasementIdentityTrustPolicyRendererContract() RendererContract {
	return newBasementIdentityTrustPolicyRenderer().contract
}

func (r basementIdentityVerificationRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	policy, err := validateBasementIdentityVerificationUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(policy)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.basement-identity-verification.policy", "marshal typed Basement verifier policy", err)
	}
	sum := sha256.Sum256(r.template)
	if "sha256:"+hex.EncodeToString(sum[:]) != r.contract.ContractHash || bytes.Count(r.template, []byte(basementIdentityVerificationPolicyToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.basement-identity-verification.template", "embedded Basement verifier policy does not match its registered contract")
	}
	return []UnitOutput{{
		Ref:   basementIdentityTrustPolicyOutputRef,
		Bytes: bytes.Replace(r.template, []byte(basementIdentityVerificationPolicyToken), canonical, 1),
	}}, nil
}

type basementIdentityVerificationPlanInputs struct {
	StackID string           `json:"stackId"`
	Kit     localAutonomyKit `json:"kit"`
}

type basementIdentityVerificationValues struct {
	Policy BasementIdentityVerificationInput `json:"basement-verification-policy"`
}

type BasementIdentityVerificationInput struct {
	SiteRef   string                          `json:"siteRef"`
	Verifiers []BasementIdentityTrustVerifier `json:"verifiers"`
}

//nolint:gocyclo // The complete closed renderer boundary is validated together.
func validateBasementIdentityVerificationUnit(unit RenderUnit, contract RendererContract) (BasementIdentityTrustEnforcementPolicy, error) {
	path := "resolvedPlan.modules." + basementIdentityTrustPolicyModuleID + ".renderUnits." + identityTrustPolicyUnitID
	if unit.ModuleID() != basementIdentityTrustPolicyModuleID || unit.ID() != identityTrustPolicyUnitID {
		return BasementIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", basementIdentityTrustPolicyModuleID, identityTrustPolicyUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef ||
		unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return BasementIdentityTrustEnforcementPolicy{}, fail(ErrOutputChanged, path, "render-unit identity differs from the registered Basement verifier contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.RuntimeKind() != "native" || unit.RuntimeDelivery() != "stackkit" || unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		unit.InstanceID() != identityTrustPolicyUnitID+"-node-"+nodeRef {
		return BasementIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "requires one exact node-local native/stackkit target")
	}
	if _, present := unit.RuntimeEngine(); present {
		return BasementIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".runtime.engine", "runtime-engine authority is forbidden")
	}
	if _, present := unit.DaemonRef(); present {
		return BasementIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "daemon authority is forbidden")
	}
	if !exactStringList(unit.PublicInputRefs(), basementIdentityVerificationPublicInputRefs) || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) {
		return BasementIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".inputs", "accepts only the exact typed Basement verifier input")
	}
	if !exactStringList(unit.PlanInputRefs(), basementIdentityVerificationPlanInputRefs) {
		return BasementIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".planInputRefs", "must contain only stack and governed Kit identity")
	}
	if err := validateBasementIdentityVerificationBinding(unit.InputBindingsJSON(), path+".inputBindings"); err != nil {
		return BasementIdentityTrustEnforcementPolicy{}, err
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return BasementIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".interfaces", "service, network, endpoint, socket and privileged-interface authority is forbidden")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return BasementIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != basementIdentityTrustPolicyOutputRef {
		return BasementIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", basementIdentityTrustPolicyOutputRef)
	}
	var inputs basementIdentityVerificationPlanInputs
	if err := decodeStrict(unit.PlanInputsJSON(), &inputs); err != nil {
		return BasementIdentityTrustEnforcementPolicy{}, wrap(ErrInvalidPlan, path+".planInputs", "decode bounded Basement verifier plan identity", err)
	}
	if err := requireContractID(inputs.StackID, path+".planInputs.stackId"); err != nil {
		return BasementIdentityTrustEnforcementPolicy{}, err
	}
	if inputs.Kit.Slug != "basement-kit" || stringsTrim(inputs.Kit.Version) == "" || !validSHA256(inputs.Kit.DefinitionHash) {
		return BasementIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".planInputs.kit", "requires exact governed Basement Kit identity")
	}
	var values basementIdentityVerificationValues
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return BasementIdentityTrustEnforcementPolicy{}, wrap(ErrInvalidPlan, path+".values", "decode exact Basement verifier input", err)
	}
	policy, err := validateBasementIdentityVerificationInput(values.Policy, inputs.StackID, path+".values.basement-verification-policy")
	if err != nil {
		return BasementIdentityTrustEnforcementPolicy{}, err
	}
	if policy.SiteRef != siteRef || !exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !containsExact(unit.LogicalNodeRefs(), nodeRef) {
		return BasementIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "runtime instance must bind the exact projected Home Site and eligible node")
	}
	return BasementIdentityTrustEnforcementPolicy{
		StackID: inputs.StackID, KitSlug: inputs.Kit.Slug, SiteRef: policy.SiteRef, Verifiers: policy.Verifiers,
	}, nil
}

func validateBasementIdentityVerificationBinding(raw []byte, path string) error {
	var bindings []rawModuleRenderInputBinding
	if err := decodeStrict(raw, &bindings); err != nil {
		return wrap(ErrInvalidPlan, path, "decode Basement verifier input binding", err)
	}
	expected := []rawModuleRenderInputBinding{{
		TargetRef: "basement-verification-policy", SourceRef: "identityTrust.basementVerification",
		ValueType: "basement-identity-verification-v1", Cardinality: "single", Required: true,
	}}
	if !reflect.DeepEqual(bindings, expected) {
		return fail(ErrInvalidPlan, path, "must exactly match the governed Basement verifier binding")
	}
	return nil
}

func validateBasementIdentityVerificationInput(value BasementIdentityVerificationInput, stackID, path string) (BasementIdentityVerificationInput, error) {
	if err := requireContractID(value.SiteRef, path+".siteRef"); err != nil {
		return value, err
	}
	if len(value.Verifiers) != 3 {
		return value, fail(ErrInvalidPlan, path+".verifiers", "requires exactly device, human, and workload verifiers")
	}
	counts := map[string]int{}
	ids := map[string]struct{}{}
	for index := range value.Verifiers {
		verifier := &value.Verifiers[index]
		verifierPath := path + ".verifiers[" + strconv.Itoa(index) + "]"
		if err := requireContractID(verifier.ID, verifierPath+".id"); err != nil {
			return value, err
		}
		if _, duplicate := ids[verifier.ID]; duplicate {
			return value, fail(ErrInvalidPlan, verifierPath+".id", "verifier ID is duplicated")
		}
		ids[verifier.ID] = struct{}{}
		if verifier.Principal != "device" && verifier.Principal != "human" && verifier.Principal != "workload" {
			return value, fail(ErrInvalidPlan, verifierPath+".principal", "requires device, human, or workload")
		}
		counts[verifier.Principal]++
		if err := requireContractID(verifier.CredentialIssuerRef, verifierPath+".issuerRef"); err != nil {
			return value, err
		}
		if err := requireStackInstanceURN(verifier.Issuer, stackID, "issuer", verifierPath+".issuer"); err != nil {
			return value, err
		}
		if err := requireStackInstanceURN(verifier.VerificationKeySetRef, stackID, "keyset", verifierPath+".verificationKeySetRef"); err != nil {
			return value, err
		}
		if err := requireSortedStackInstanceURNs(verifier.Audiences, stackID, "audience", verifierPath+".audiences"); err != nil {
			return value, err
		}
		if verifier.CredentialTTLSeconds < 300 || verifier.CredentialTTLSeconds > 86400 ||
			verifier.SessionTTLSeconds < 60 || verifier.SessionTTLSeconds > 86400 ||
			verifier.RevocationMaxStalenessSeconds < 0 || verifier.RevocationMaxStalenessSeconds > verifier.CredentialTTLSeconds {
			return value, fail(ErrInvalidPlan, verifierPath, "lifetime, session, and revocation policy are outside closed bounds")
		}
	}
	for _, principal := range []string{"device", "human", "workload"} {
		if counts[principal] != 1 {
			return value, fail(ErrInvalidPlan, path+".verifiers", "requires exactly one %s verifier", principal)
		}
	}
	sort.Slice(value.Verifiers, func(i, j int) bool { return value.Verifiers[i].ID < value.Verifiers[j].ID })
	return value, nil
}

type basementIdentityVerificationArtifact struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Contract   struct {
		CredentialIssuanceRuntime string `json:"credentialIssuanceRuntime"`
		CredentialMaterial        string `json:"credentialMaterial"`
		Enrollment                string `json:"enrollment"`
		ExternalTrust             string `json:"externalTrust"`
		JWKSBytes                 string `json:"jwksBytes"`
		PrivateKeys               string `json:"privateKeys"`
		RuntimeEndpoints          string `json:"runtimeEndpoints"`
		RuntimeEnforcement        string `json:"runtimeEnforcement"`
		Scope                     string `json:"scope"`
		SigningAuthority          string `json:"signingAuthority"`
	} `json:"contract"`
	Policy BasementIdentityTrustEnforcementPolicy `json:"policy"`
}

func ValidateBasementIdentityTrustPolicyArtifact(raw []byte) (BasementIdentityTrustEnforcementPolicy, error) {
	var document basementIdentityVerificationArtifact
	if err := decodeStrict(raw, &document); err != nil {
		return BasementIdentityTrustEnforcementPolicy{}, wrap(ErrInvalidPlan, "basementIdentityTrustPolicy", "decode exact Basement verifier artifact", err)
	}
	contract := document.Contract
	if document.APIVersion != "stackkit.basement-identity-trust-policy/v1" || document.Kind != "BasementIdentityTrustPolicy" ||
		contract.CredentialIssuanceRuntime != "not-owned" || contract.CredentialMaterial != "not-included" || contract.Enrollment != "not-owned" ||
		contract.ExternalTrust != "not-applicable" || contract.JWKSBytes != "not-included" || contract.PrivateKeys != "not-included" ||
		contract.RuntimeEndpoints != "not-included" || contract.RuntimeEnforcement != "unverified" ||
		contract.Scope != "generation-only" || contract.SigningAuthority != "not-owned" {
		return BasementIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, "basementIdentityTrustPolicy.contract", "artifact widens or fabricates the generation-only Basement verifier contract")
	}
	if document.Policy.KitSlug != "basement-kit" {
		return BasementIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, "basementIdentityTrustPolicy.policy.kitSlug", "artifact has no Basement Kit authority")
	}
	input := BasementIdentityVerificationInput{SiteRef: document.Policy.SiteRef, Verifiers: document.Policy.Verifiers}
	validated, err := validateBasementIdentityVerificationInput(input, document.Policy.StackID, "basementIdentityTrustPolicy.policy")
	if err != nil {
		return BasementIdentityTrustEnforcementPolicy{}, err
	}
	document.Policy.Verifiers = validated.Verifiers
	return document.Policy, nil
}

var _ UnitRenderer = basementIdentityVerificationRenderer{}
