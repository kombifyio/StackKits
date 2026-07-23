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

const cloudIdentityAuthorityPolicyToken = "@@POLICY@@"

const cloudIdentityAuthorityPolicyTemplate = `{"apiVersion":"stackkit.cloud-identity-trust-policy/v1","kind":"CloudIdentityTrustPolicy","contract":{"cloudDeviceEnrollment":"deny","cloudDeviceIssuance":"deny","credentialIssuanceRuntime":"unverified","credentialMaterial":"not-included","enrollmentAuthority":"not-owned","externalDeviceIssuer":"owner-bound","jwksBytes":"not-included","privateKeys":"not-included","runtimeEndpoints":"not-included","runtimeEnforcement":"unverified","scope":"generation-only","signingRuntime":"not-included"},"policy":@@POLICY@@}
`

var cloudIdentityAuthorityPlanInputRefs = []string{"kit", "stackId"}
var cloudIdentityAuthorityPublicInputRefs = []string{"cloud-identity-authority"}

type cloudIdentityAuthorityRenderer struct {
	template []byte
	contract RendererContract
}

func newCloudIdentityTrustPolicyRenderer() cloudIdentityAuthorityRenderer {
	sum := sha256.Sum256([]byte(cloudIdentityAuthorityPolicyTemplate))
	return cloudIdentityAuthorityRenderer{
		template: []byte(cloudIdentityAuthorityPolicyTemplate),
		contract: RendererContract{
			Kind: "native-config", RendererRef: identityTrustPolicyRendererRef,
			TemplateRef: cloudIdentityTrustPolicyTemplateRef, Version: identityTrustPolicyVersion,
			ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
		},
	}
}

func CloudIdentityTrustPolicyRendererContract() RendererContract {
	return newCloudIdentityTrustPolicyRenderer().contract
}

func (r cloudIdentityAuthorityRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	policy, err := validateCloudIdentityAuthorityUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(policy)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.cloud-identity-authority.policy", "marshal typed Cloud identity policy", err)
	}
	sum := sha256.Sum256(r.template)
	if "sha256:"+hex.EncodeToString(sum[:]) != r.contract.ContractHash || bytes.Count(r.template, []byte(cloudIdentityAuthorityPolicyToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.cloud-identity-authority.template", "embedded Cloud identity policy does not match its registered contract")
	}
	return []UnitOutput{{
		Ref:   cloudIdentityTrustPolicyOutputRef,
		Bytes: bytes.Replace(r.template, []byte(cloudIdentityAuthorityPolicyToken), canonical, 1),
	}}, nil
}

type cloudIdentityAuthorityPlanInputs struct {
	StackID string           `json:"stackId"`
	Kit     localAutonomyKit `json:"kit"`
}

type cloudIdentityAuthorityValues struct {
	Authority CloudIdentityAuthorityInput `json:"cloud-identity-authority"`
}

type CloudIdentityAuthorityInput struct {
	SiteRef   string                       `json:"siteRef"`
	Issuers   []CloudIdentityTrustIssuer   `json:"issuers"`
	Verifiers []CloudIdentityTrustVerifier `json:"verifiers"`
}

//nolint:gocyclo // The complete closed renderer boundary is validated together.
func validateCloudIdentityAuthorityUnit(unit RenderUnit, contract RendererContract) (CloudIdentityTrustEnforcementPolicy, error) {
	path := "resolvedPlan.modules." + cloudIdentityTrustPolicyModuleID + ".renderUnits." + identityTrustPolicyUnitID
	if unit.ModuleID() != cloudIdentityTrustPolicyModuleID || unit.ID() != identityTrustPolicyUnitID {
		return CloudIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", cloudIdentityTrustPolicyModuleID, identityTrustPolicyUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef ||
		unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return CloudIdentityTrustEnforcementPolicy{}, fail(ErrOutputChanged, path, "render-unit identity differs from the registered Cloud identity contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.RuntimeKind() != "native" || unit.RuntimeDelivery() != "stackkit" || unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		unit.InstanceID() != identityTrustPolicyUnitID+"-node-"+nodeRef {
		return CloudIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "requires one exact node-local native/stackkit target")
	}
	if _, present := unit.RuntimeEngine(); present {
		return CloudIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".runtime.engine", "runtime-engine authority is forbidden")
	}
	if _, present := unit.DaemonRef(); present {
		return CloudIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "daemon authority is forbidden")
	}
	if !exactStringList(unit.PublicInputRefs(), cloudIdentityAuthorityPublicInputRefs) || len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) {
		return CloudIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".inputs", "accepts only the exact typed Cloud identity input")
	}
	if !exactStringList(unit.PlanInputRefs(), cloudIdentityAuthorityPlanInputRefs) {
		return CloudIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".planInputRefs", "must contain only stack and governed Kit identity")
	}
	if err := validateCloudIdentityAuthorityBinding(unit.InputBindingsJSON(), path+".inputBindings"); err != nil {
		return CloudIdentityTrustEnforcementPolicy{}, err
	}
	if !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return CloudIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".interfaces", "service, network, endpoint, socket and privileged-interface authority is forbidden")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return CloudIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != cloudIdentityTrustPolicyOutputRef {
		return CloudIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", cloudIdentityTrustPolicyOutputRef)
	}
	var inputs cloudIdentityAuthorityPlanInputs
	if err := decodeStrict(unit.PlanInputsJSON(), &inputs); err != nil {
		return CloudIdentityTrustEnforcementPolicy{}, wrap(ErrInvalidPlan, path+".planInputs", "decode bounded Cloud identity plan", err)
	}
	if err := requireContractID(inputs.StackID, path+".planInputs.stackId"); err != nil {
		return CloudIdentityTrustEnforcementPolicy{}, err
	}
	if inputs.Kit.Slug != "cloud-kit" || stringsTrim(inputs.Kit.Version) == "" || !validSHA256(inputs.Kit.DefinitionHash) {
		return CloudIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".planInputs.kit", "requires exact governed Cloud Kit identity")
	}
	var values cloudIdentityAuthorityValues
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return CloudIdentityTrustEnforcementPolicy{}, wrap(ErrInvalidPlan, path+".values", "decode exact Cloud identity input", err)
	}
	authority, err := validateCloudIdentityAuthorityInput(values.Authority, inputs.StackID, path+".values.cloud-identity-authority")
	if err != nil {
		return CloudIdentityTrustEnforcementPolicy{}, err
	}
	if authority.SiteRef != siteRef || !exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !containsExact(unit.LogicalNodeRefs(), nodeRef) {
		return CloudIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, path+".instances", "runtime instance must bind the exact projected Cloud Site and eligible node")
	}
	return CloudIdentityTrustEnforcementPolicy{
		StackID: inputs.StackID, KitSlug: inputs.Kit.Slug, SiteRef: authority.SiteRef,
		Issuers: authority.Issuers, Verifiers: authority.Verifiers,
	}, nil
}

func validateCloudIdentityAuthorityBinding(raw []byte, path string) error {
	var bindings []rawModuleRenderInputBinding
	if err := decodeStrict(raw, &bindings); err != nil {
		return wrap(ErrInvalidPlan, path, "decode Cloud identity input binding", err)
	}
	expected := []rawModuleRenderInputBinding{{
		TargetRef: "cloud-identity-authority", SourceRef: "identityTrust.cloudAuthority",
		ValueType: "cloud-identity-authority-v1", Cardinality: "single", Required: true,
	}}
	if !reflect.DeepEqual(bindings, expected) {
		return fail(ErrInvalidPlan, path, "must exactly match the governed Cloud identity binding")
	}
	return nil
}

func validateCloudIdentityAuthorityInput(value CloudIdentityAuthorityInput, stackID, path string) (CloudIdentityAuthorityInput, error) {
	if err := requireContractID(value.SiteRef, path+".siteRef"); err != nil {
		return value, err
	}
	if len(value.Issuers) != 2 || len(value.Verifiers) != 3 {
		return value, fail(ErrInvalidPlan, path, "requires exactly two owned issuers and three verifiers")
	}
	issuerCounts, issuerIDs := map[string]int{}, map[string]struct{}{}
	for index := range value.Issuers {
		issuer := &value.Issuers[index]
		itemPath := path + ".issuers[" + strconv.Itoa(index) + "]"
		if err := requireContractID(issuer.ID, itemPath+".id"); err != nil {
			return value, err
		}
		if err := requireContractID(issuer.AuthorityRef, itemPath+".authorityRef"); err != nil {
			return value, err
		}
		if _, duplicate := issuerIDs[issuer.ID]; duplicate {
			return value, fail(ErrInvalidPlan, itemPath+".id", "issuer ID is duplicated")
		}
		issuerIDs[issuer.ID] = struct{}{}
		if issuer.Principal != "human" && issuer.Principal != "workload" {
			return value, fail(ErrInvalidPlan, itemPath+".principal", "Cloud may issue only human and workload identity")
		}
		issuerCounts[issuer.Principal]++
		if err := requireStackInstanceURN(issuer.Issuer, stackID, "issuer", itemPath+".issuer"); err != nil {
			return value, err
		}
		if err := requireStackInstanceURN(issuer.VerificationKeySetRef, stackID, "keyset", itemPath+".verificationKeySetRef"); err != nil {
			return value, err
		}
		if err := requireSortedStackInstanceURNs(issuer.Audiences, stackID, "audience", itemPath+".audiences"); err != nil {
			return value, err
		}
		if issuer.CredentialTTLSeconds < 300 || issuer.CredentialTTLSeconds > 86400 ||
			issuer.SessionTTLSeconds < 60 || issuer.SessionTTLSeconds > 86400 ||
			issuer.RevocationMaxStalenessSeconds < 0 || issuer.RevocationMaxStalenessSeconds > issuer.CredentialTTLSeconds {
			return value, fail(ErrInvalidPlan, itemPath, "issuer lifetime, session, and revocation policy are outside closed bounds")
		}
	}
	for _, principal := range []string{"human", "workload"} {
		if issuerCounts[principal] != 1 {
			return value, fail(ErrInvalidPlan, path+".issuers", "requires exactly one %s issuer", principal)
		}
	}
	verifierCounts, verifierIDs := map[string]int{}, map[string]struct{}{}
	for index := range value.Verifiers {
		verifier := &value.Verifiers[index]
		itemPath := path + ".verifiers[" + strconv.Itoa(index) + "]"
		if err := requireContractID(verifier.ID, itemPath+".id"); err != nil {
			return value, err
		}
		if err := requireContractID(verifier.CredentialIssuerRef, itemPath+".issuerRef"); err != nil {
			return value, err
		}
		if _, duplicate := verifierIDs[verifier.ID]; duplicate {
			return value, fail(ErrInvalidPlan, itemPath+".id", "verifier ID is duplicated")
		}
		verifierIDs[verifier.ID] = struct{}{}
		if verifier.Principal != "device" && verifier.Principal != "human" && verifier.Principal != "workload" {
			return value, fail(ErrInvalidPlan, itemPath+".principal", "unsupported Cloud verifier principal")
		}
		verifierCounts[verifier.Principal]++
		if err := requireStackInstanceURN(verifier.Issuer, stackID, "issuer", itemPath+".issuer"); err != nil {
			return value, err
		}
		if err := requireStackInstanceURN(verifier.VerificationKeySetRef, stackID, "keyset", itemPath+".verificationKeySetRef"); err != nil {
			return value, err
		}
		if err := requireSortedStackInstanceURNs(verifier.Audiences, stackID, "audience", itemPath+".audiences"); err != nil {
			return value, err
		}
		if verifier.RevocationMaxStalenessSeconds < 0 || verifier.RevocationMaxStalenessSeconds > 86400 {
			return value, fail(ErrInvalidPlan, itemPath+".revocationMaxStalenessSeconds", "requires bounded revocation staleness")
		}
	}
	for _, principal := range []string{"device", "human", "workload"} {
		if verifierCounts[principal] != 1 {
			return value, fail(ErrInvalidPlan, path+".verifiers", "requires exactly one %s verifier", principal)
		}
	}
	sort.Slice(value.Issuers, func(i, j int) bool { return value.Issuers[i].ID < value.Issuers[j].ID })
	sort.Slice(value.Verifiers, func(i, j int) bool { return value.Verifiers[i].ID < value.Verifiers[j].ID })
	return value, nil
}

type cloudIdentityAuthorityArtifact struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Contract   struct {
		CloudDeviceEnrollment     string `json:"cloudDeviceEnrollment"`
		CloudDeviceIssuance       string `json:"cloudDeviceIssuance"`
		CredentialIssuanceRuntime string `json:"credentialIssuanceRuntime"`
		CredentialMaterial        string `json:"credentialMaterial"`
		EnrollmentAuthority       string `json:"enrollmentAuthority"`
		ExternalDeviceIssuer      string `json:"externalDeviceIssuer"`
		JWKSBytes                 string `json:"jwksBytes"`
		PrivateKeys               string `json:"privateKeys"`
		RuntimeEndpoints          string `json:"runtimeEndpoints"`
		RuntimeEnforcement        string `json:"runtimeEnforcement"`
		Scope                     string `json:"scope"`
		SigningRuntime            string `json:"signingRuntime"`
	} `json:"contract"`
	Policy CloudIdentityTrustEnforcementPolicy `json:"policy"`
}

func ValidateCloudIdentityTrustPolicyArtifact(raw []byte) (CloudIdentityTrustEnforcementPolicy, error) {
	var document cloudIdentityAuthorityArtifact
	if err := decodeStrict(raw, &document); err != nil {
		return CloudIdentityTrustEnforcementPolicy{}, wrap(ErrInvalidPlan, "cloudIdentityTrustPolicy", "decode exact Cloud identity artifact", err)
	}
	contract := document.Contract
	if document.APIVersion != "stackkit.cloud-identity-trust-policy/v1" || document.Kind != "CloudIdentityTrustPolicy" ||
		contract.CloudDeviceEnrollment != "deny" || contract.CloudDeviceIssuance != "deny" ||
		contract.CredentialIssuanceRuntime != "unverified" || contract.CredentialMaterial != "not-included" ||
		contract.EnrollmentAuthority != "not-owned" || contract.ExternalDeviceIssuer != "owner-bound" ||
		contract.JWKSBytes != "not-included" || contract.PrivateKeys != "not-included" ||
		contract.RuntimeEndpoints != "not-included" || contract.RuntimeEnforcement != "unverified" ||
		contract.Scope != "generation-only" || contract.SigningRuntime != "not-included" {
		return CloudIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, "cloudIdentityTrustPolicy.contract", "artifact widens or fabricates the generation-only Cloud identity contract")
	}
	if document.Policy.KitSlug != "cloud-kit" {
		return CloudIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, "cloudIdentityTrustPolicy.policy.kitSlug", "artifact has no Cloud Kit authority")
	}
	input := CloudIdentityAuthorityInput{SiteRef: document.Policy.SiteRef, Issuers: document.Policy.Issuers, Verifiers: document.Policy.Verifiers}
	validated, err := validateCloudIdentityAuthorityInput(input, document.Policy.StackID, "cloudIdentityTrustPolicy.policy")
	if err != nil {
		return CloudIdentityTrustEnforcementPolicy{}, err
	}
	document.Policy.Issuers, document.Policy.Verifiers = validated.Issuers, validated.Verifiers
	return document.Policy, nil
}

var _ UnitRenderer = cloudIdentityAuthorityRenderer{}
