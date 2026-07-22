package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	identityTrustPolicyUnitID      = "policy-bundle"
	identityTrustPolicyRendererRef = "stackkit"
	identityTrustPolicyVersion     = "1.0.0"
	identityTrustPolicyToken       = "@@PLAN_INPUTS@@"

	homeDeviceAuthorityPolicyModuleID    = "stackkits-home-device-authority-policy-manifest"
	homeDeviceAuthorityPolicyTemplateRef = "builtin://home/device-authority-policy/v1.json"
	homeDeviceAuthorityPolicyOutputRef   = "local/identity/device-authority-policy.json"
	homeDeviceAuthorityProviderRef       = "stackkits-home-device-authority"
	homeDeviceAuthorityContractRef       = "home-device-authority-policy-contract"

	basementIdentityTrustPolicyModuleID    = "stackkits-basement-identity-trust-policy-manifest"
	basementIdentityTrustPolicyTemplateRef = "builtin://basement/identity-trust-policy/v1.json"
	basementIdentityTrustPolicyOutputRef   = "local/identity/trust-policy.json"
	basementIdentityTrustProviderRef       = "stackkits-basement-identity-trust-policy"
	basementIdentityTrustContractRef       = "basement-identity-trust-policy-contract"

	cloudIdentityTrustPolicyModuleID    = "stackkits-cloud-identity-trust-policy-manifest"
	cloudIdentityTrustPolicyTemplateRef = "builtin://cloud/identity-trust-policy/v1.json"
	cloudIdentityTrustPolicyOutputRef   = "cloud/identity/trust-policy.json"
	cloudIdentityTrustProviderRef       = "stackkits-cloud-identity-trust-policy"
	cloudIdentityTrustContractRef       = "cloud-identity-trust-policy-contract"

	externalDeviceAuthorityContractRef = "owner-bound-device-authority"
)

const homeDeviceAuthorityPolicyTemplate = `{"apiVersion":"stackkit.home-device-authority-policy/v1","kind":"HomeDeviceAuthorityPolicy","contract":{"credentialIssuanceRuntime":"unverified","credentialMaterial":"not-included","enrollment":"home-local","jwksBytes":"not-included","privateKeys":"not-included","runtimeEndpoints":"not-included","runtimeEnforcement":"unverified","scope":"generation-only","signingRuntime":"not-included"},"planInputs":@@PLAN_INPUTS@@}
`

const basementIdentityTrustPolicyTemplate = `{"apiVersion":"stackkit.basement-identity-trust-policy/v1","kind":"BasementIdentityTrustPolicy","contract":{"credentialIssuanceRuntime":"unverified","credentialMaterial":"not-included","externalTrust":"not-applicable","jwksBytes":"not-included","privateKeys":"not-included","runtimeEndpoints":"not-included","runtimeEnforcement":"unverified","scope":"generation-only","signingAuthority":"not-owned"},"planInputs":@@PLAN_INPUTS@@}
`

const cloudIdentityTrustPolicyTemplate = `{"apiVersion":"stackkit.cloud-identity-trust-policy/v1","kind":"CloudIdentityTrustPolicy","contract":{"cloudDeviceEnrollment":"deny","cloudDeviceIssuance":"deny","credentialIssuanceRuntime":"unverified","credentialMaterial":"not-included","enrollmentAuthority":"not-owned","externalDeviceIssuer":"owner-bound","jwksBytes":"not-included","privateKeys":"not-included","runtimeEndpoints":"not-included","runtimeEnforcement":"unverified","scope":"generation-only","signingAuthority":"not-owned"},"planInputs":@@PLAN_INPUTS@@}
`

var identityTrustPolicyPlanInputRefs = []string{"identityTrust", "kit", "sites", "stackId"}

type identityTrustPolicyOutputTemplate struct {
	ref      string
	template []byte
}

type identityTrustPolicyRenderer struct {
	moduleID      string
	policyName    string
	contract      RendererContract
	planInputRefs []string
	outputs       []identityTrustPolicyOutputTemplate
	validate      func([]byte, string) ([]string, error)
}

func newIdentityTrustPolicyRenderer(moduleID, policyName, templateRef string, outputs []identityTrustPolicyOutputTemplate, refs []string, validate func([]byte, string) ([]string, error)) identityTrustPolicyRenderer {
	contract := RendererContract{
		Kind: "native-config", RendererRef: identityTrustPolicyRendererRef,
		TemplateRef: templateRef, Version: identityTrustPolicyVersion,
		ContractHash: identityTrustTemplatesHash(outputs),
	}
	return identityTrustPolicyRenderer{
		moduleID: moduleID, policyName: policyName, contract: contract,
		planInputRefs: append([]string(nil), refs...), outputs: cloneIdentityTrustTemplates(outputs), validate: validate,
	}
}

func newHomeDeviceAuthorityPolicyRenderer() identityTrustPolicyRenderer {
	return newIdentityTrustPolicyRenderer(
		homeDeviceAuthorityPolicyModuleID, "Home device-authority policy", homeDeviceAuthorityPolicyTemplateRef,
		[]identityTrustPolicyOutputTemplate{{ref: homeDeviceAuthorityPolicyOutputRef, template: []byte(homeDeviceAuthorityPolicyTemplate)}},
		identityTrustPolicyPlanInputRefs, validateHomeDeviceAuthorityPlanInputs,
	)
}

func newBasementIdentityTrustPolicyRenderer() identityTrustPolicyRenderer {
	return newIdentityTrustPolicyRenderer(
		basementIdentityTrustPolicyModuleID, "Basement identity-trust policy", basementIdentityTrustPolicyTemplateRef,
		[]identityTrustPolicyOutputTemplate{{ref: basementIdentityTrustPolicyOutputRef, template: []byte(basementIdentityTrustPolicyTemplate)}},
		identityTrustPolicyPlanInputRefs, validateBasementIdentityTrustPlanInputs,
	)
}

func newCloudIdentityTrustPolicyRenderer() identityTrustPolicyRenderer {
	return newIdentityTrustPolicyRenderer(
		cloudIdentityTrustPolicyModuleID, "Cloud identity-trust policy", cloudIdentityTrustPolicyTemplateRef,
		[]identityTrustPolicyOutputTemplate{{ref: cloudIdentityTrustPolicyOutputRef, template: []byte(cloudIdentityTrustPolicyTemplate)}},
		identityTrustPolicyPlanInputRefs, validateCloudIdentityTrustPlanInputs,
	)
}

func HomeDeviceAuthorityPolicyRendererContract() RendererContract {
	return newHomeDeviceAuthorityPolicyRenderer().contract
}

func BasementIdentityTrustPolicyRendererContract() RendererContract {
	return newBasementIdentityTrustPolicyRenderer().contract
}

func CloudIdentityTrustPolicyRendererContract() RendererContract {
	return newCloudIdentityTrustPolicyRenderer().contract
}

type HomeDeviceAuthorityEnforcementPolicy struct {
	StackID  string
	SiteRefs []string
	Issuer   HomeDeviceCredentialIssuer
}

type HomeDeviceCredentialIssuer struct {
	ID                            string
	Issuer                        string
	Audiences                     []string
	VerificationKeySetRef         string
	SiteRefs                      []string
	CredentialTTLSeconds          int
	SessionTTLSeconds             int
	ProofOfPossessionRequired     bool
	RevocationMaxStalenessSeconds int
	EnrollmentMode                string
	EnrollmentExposure            string
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
	PlanInputs json.RawMessage `json:"planInputs"`
}

// ValidateHomeDeviceAuthorityPolicyArtifact validates the exact generated Home
// authority policy and returns only device enrollment/issuer/revocation
// configuration. Key bytes, credentials, endpoints, transport, and provider
// lifecycle are structurally absent.
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
	siteRefs, err := validateHomeDeviceAuthorityPlanInputs(document.PlanInputs, "homeDeviceAuthorityPolicy.planInputs")
	if err != nil {
		return HomeDeviceAuthorityEnforcementPolicy{}, err
	}
	validated, err := decodeIdentityTrustPlanInputs(document.PlanInputs, "homeDeviceAuthorityPolicy.planInputs")
	if err != nil {
		return HomeDeviceAuthorityEnforcementPolicy{}, err
	}
	deviceIssuers := make([]identityTrustCredentialIssuer, 0, 1)
	for _, issuer := range validated.inputs.IdentityTrust.CredentialIssuers {
		if issuer.Principal == "device" {
			deviceIssuers = append(deviceIssuers, issuer)
		}
	}
	if len(deviceIssuers) != 1 {
		return HomeDeviceAuthorityEnforcementPolicy{}, fail(ErrInvalidPlan, "homeDeviceAuthorityPolicy.planInputs.identityTrust.credentialIssuers", "Home enforcement requires exactly one device credential issuer")
	}
	issuer := deviceIssuers[0]
	return HomeDeviceAuthorityEnforcementPolicy{
		StackID: validated.inputs.StackID, SiteRefs: append([]string(nil), siteRefs...),
		Issuer: HomeDeviceCredentialIssuer{
			ID: issuer.ID, Issuer: issuer.Issuer, Audiences: append([]string(nil), issuer.Audiences...), VerificationKeySetRef: issuer.VerificationKeySetRef,
			SiteRefs: append([]string(nil), issuer.Placement.SiteRefs...), CredentialTTLSeconds: issuer.CredentialTTLSeconds, SessionTTLSeconds: issuer.SessionTTLSeconds,
			ProofOfPossessionRequired: issuer.ProofOfPossessionRequired, RevocationMaxStalenessSeconds: issuer.RevocationMaxStalenessSeconds,
			EnrollmentMode: issuer.Enrollment.Mode, EnrollmentExposure: issuer.Enrollment.Exposure,
		},
	}, nil
}

// BasementIdentityTrustEnforcementPolicy is the finite secret-free verifier
// projection consumed by the Basement trust enforcer. It carries references
// and policy only; key bytes, credentials, endpoints, enrollment, issuance,
// provider lifecycle, and signing authority are intentionally absent.
type BasementIdentityTrustEnforcementPolicy struct {
	StackID   string
	SiteRefs  []string
	Verifiers []BasementIdentityTrustVerifier
}

type BasementIdentityTrustVerifier struct {
	ID                            string
	Principal                     string
	CredentialIssuerRef           string
	Issuer                        string
	Audiences                     []string
	VerificationKeySetRef         string
	SiteRefs                      []string
	ProofOfPossessionRequired     bool
	RevocationMaxStalenessSeconds int
	CredentialTTLSeconds          int
	SessionTTLSeconds             int
}

type basementIdentityTrustPolicyArtifact struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Contract   struct {
		CredentialIssuanceRuntime string `json:"credentialIssuanceRuntime"`
		CredentialMaterial        string `json:"credentialMaterial"`
		ExternalTrust             string `json:"externalTrust"`
		JWKSBytes                 string `json:"jwksBytes"`
		PrivateKeys               string `json:"privateKeys"`
		RuntimeEndpoints          string `json:"runtimeEndpoints"`
		RuntimeEnforcement        string `json:"runtimeEnforcement"`
		Scope                     string `json:"scope"`
		SigningAuthority          string `json:"signingAuthority"`
	} `json:"contract"`
	PlanInputs json.RawMessage `json:"planInputs"`
}

// ValidateBasementIdentityTrustPolicyArtifact validates the exact generated
// policy and returns only the verifier configuration a future authenticated
// enforcer may consume. All three principal classes are required because the
// CUE enforcement requirement owns device, human, and workload verification.
func ValidateBasementIdentityTrustPolicyArtifact(raw []byte) (BasementIdentityTrustEnforcementPolicy, error) {
	var document basementIdentityTrustPolicyArtifact
	if err := decodeStrict(raw, &document); err != nil {
		return BasementIdentityTrustEnforcementPolicy{}, wrap(ErrInvalidPlan, "basementIdentityTrustPolicy", "decode exact Basement identity-trust artifact", err)
	}
	contract := document.Contract
	if document.APIVersion != "stackkit.basement-identity-trust-policy/v1" || document.Kind != "BasementIdentityTrustPolicy" ||
		contract.CredentialIssuanceRuntime != "unverified" || contract.CredentialMaterial != "not-included" || contract.ExternalTrust != "not-applicable" ||
		contract.JWKSBytes != "not-included" || contract.PrivateKeys != "not-included" || contract.RuntimeEndpoints != "not-included" ||
		contract.RuntimeEnforcement != "unverified" || contract.Scope != "generation-only" || contract.SigningAuthority != "not-owned" {
		return BasementIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, "basementIdentityTrustPolicy.contract", "artifact widens or fabricates the generation-only Basement verifier contract")
	}
	siteRefs, err := validateBasementIdentityTrustPlanInputs(document.PlanInputs, "basementIdentityTrustPolicy.planInputs")
	if err != nil {
		return BasementIdentityTrustEnforcementPolicy{}, err
	}
	validated, err := decodeIdentityTrustPlanInputs(document.PlanInputs, "basementIdentityTrustPolicy.planInputs")
	if err != nil {
		return BasementIdentityTrustEnforcementPolicy{}, err
	}
	policy := BasementIdentityTrustEnforcementPolicy{StackID: validated.inputs.StackID, SiteRefs: append([]string(nil), siteRefs...)}
	policy.Verifiers = make([]BasementIdentityTrustVerifier, 0, len(validated.inputs.IdentityTrust.VerifierPlacements))
	principalCounts := map[string]int{"device": 0, "human": 0, "workload": 0}
	for _, verifier := range validated.inputs.IdentityTrust.VerifierPlacements {
		issuer := validated.issuers[verifier.CredentialIssuerRef]
		policy.Verifiers = append(policy.Verifiers, BasementIdentityTrustVerifier{
			ID: verifier.ID, Principal: verifier.Principal, CredentialIssuerRef: verifier.CredentialIssuerRef, Issuer: verifier.Issuer,
			Audiences: append([]string(nil), verifier.Audiences...), VerificationKeySetRef: verifier.VerificationKeySetRef,
			SiteRefs: append([]string(nil), verifier.Placement.SiteRefs...), ProofOfPossessionRequired: verifier.ProofOfPossessionRequired,
			RevocationMaxStalenessSeconds: verifier.RevocationMaxStalenessSeconds,
			CredentialTTLSeconds:          issuer.CredentialTTLSeconds, SessionTTLSeconds: issuer.SessionTTLSeconds,
		})
		principalCounts[verifier.Principal]++
	}
	for principal, count := range principalCounts {
		if count == 0 {
			return BasementIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, "basementIdentityTrustPolicy.planInputs.identityTrust.verifierPlacements", "Basement enforcement requires at least one %s verifier", principal)
		}
	}
	sort.Slice(policy.Verifiers, func(left, right int) bool { return policy.Verifiers[left].ID < policy.Verifiers[right].ID })
	return policy, nil
}

// CloudIdentityTrustEnforcementPolicy is the closed secret-free policy for the
// Cloud trust enforcer. Issuers contains only StackKit-owned human/workload
// issuers; the external device issuer remains a verifier reference and can
// never become Cloud issuance or enrollment authority.
type CloudIdentityTrustEnforcementPolicy struct {
	StackID   string
	SiteRefs  []string
	Issuers   []CloudIdentityTrustIssuer
	Verifiers []CloudIdentityTrustVerifier
}

type CloudIdentityTrustIssuer struct {
	ID                            string
	Principal                     string
	Issuer                        string
	Audiences                     []string
	VerificationKeySetRef         string
	SiteRefs                      []string
	ProofOfPossessionRequired     bool
	RevocationMaxStalenessSeconds int
	CredentialTTLSeconds          int
	SessionTTLSeconds             int
}

type CloudIdentityTrustVerifier struct {
	ID                            string
	Principal                     string
	CredentialIssuerRef           string
	Issuer                        string
	Audiences                     []string
	VerificationKeySetRef         string
	SiteRefs                      []string
	ProofOfPossessionRequired     bool
	RevocationMaxStalenessSeconds int
}

type cloudIdentityTrustPolicyArtifact struct {
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
		SigningAuthority          string `json:"signingAuthority"`
	} `json:"contract"`
	PlanInputs json.RawMessage `json:"planInputs"`
}

// ValidateCloudIdentityTrustPolicyArtifact validates the exact generation-only
// Cloud policy and projects only the authority named by its future enforcer.
func ValidateCloudIdentityTrustPolicyArtifact(raw []byte) (CloudIdentityTrustEnforcementPolicy, error) {
	var document cloudIdentityTrustPolicyArtifact
	if err := decodeStrict(raw, &document); err != nil {
		return CloudIdentityTrustEnforcementPolicy{}, wrap(ErrInvalidPlan, "cloudIdentityTrustPolicy", "decode exact Cloud identity-trust artifact", err)
	}
	contract := document.Contract
	if document.APIVersion != "stackkit.cloud-identity-trust-policy/v1" || document.Kind != "CloudIdentityTrustPolicy" ||
		contract.CloudDeviceEnrollment != "deny" || contract.CloudDeviceIssuance != "deny" || contract.CredentialIssuanceRuntime != "unverified" ||
		contract.CredentialMaterial != "not-included" || contract.EnrollmentAuthority != "not-owned" || contract.ExternalDeviceIssuer != "owner-bound" ||
		contract.JWKSBytes != "not-included" || contract.PrivateKeys != "not-included" || contract.RuntimeEndpoints != "not-included" ||
		contract.RuntimeEnforcement != "unverified" || contract.Scope != "generation-only" || contract.SigningAuthority != "not-owned" {
		return CloudIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, "cloudIdentityTrustPolicy.contract", "artifact widens or fabricates the generation-only Cloud trust contract")
	}
	siteRefs, err := validateCloudIdentityTrustPlanInputs(document.PlanInputs, "cloudIdentityTrustPolicy.planInputs")
	if err != nil {
		return CloudIdentityTrustEnforcementPolicy{}, err
	}
	validated, err := decodeIdentityTrustPlanInputs(document.PlanInputs, "cloudIdentityTrustPolicy.planInputs")
	if err != nil {
		return CloudIdentityTrustEnforcementPolicy{}, err
	}
	policy := CloudIdentityTrustEnforcementPolicy{StackID: validated.inputs.StackID, SiteRefs: append([]string(nil), siteRefs...)}
	issuerCounts := map[string]int{"human": 0, "workload": 0}
	for _, issuer := range validated.inputs.IdentityTrust.CredentialIssuers {
		if issuer.Principal == "device" {
			continue
		}
		policy.Issuers = append(policy.Issuers, CloudIdentityTrustIssuer{
			ID: issuer.ID, Principal: issuer.Principal, Issuer: issuer.Issuer, Audiences: append([]string(nil), issuer.Audiences...),
			VerificationKeySetRef: issuer.VerificationKeySetRef, SiteRefs: append([]string(nil), issuer.Placement.SiteRefs...),
			ProofOfPossessionRequired: issuer.ProofOfPossessionRequired, RevocationMaxStalenessSeconds: issuer.RevocationMaxStalenessSeconds,
			CredentialTTLSeconds: issuer.CredentialTTLSeconds, SessionTTLSeconds: issuer.SessionTTLSeconds,
		})
		issuerCounts[issuer.Principal]++
	}
	verifierCounts := map[string]int{"device": 0, "human": 0, "workload": 0}
	for _, verifier := range validated.inputs.IdentityTrust.VerifierPlacements {
		policy.Verifiers = append(policy.Verifiers, CloudIdentityTrustVerifier{
			ID: verifier.ID, Principal: verifier.Principal, CredentialIssuerRef: verifier.CredentialIssuerRef, Issuer: verifier.Issuer,
			Audiences: append([]string(nil), verifier.Audiences...), VerificationKeySetRef: verifier.VerificationKeySetRef,
			SiteRefs: append([]string(nil), verifier.Placement.SiteRefs...), ProofOfPossessionRequired: verifier.ProofOfPossessionRequired,
			RevocationMaxStalenessSeconds: verifier.RevocationMaxStalenessSeconds,
		})
		verifierCounts[verifier.Principal]++
	}
	for principal, count := range issuerCounts {
		if count == 0 {
			return CloudIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, "cloudIdentityTrustPolicy.planInputs.identityTrust.credentialIssuers", "Cloud enforcement requires at least one %s issuer", principal)
		}
	}
	for principal, count := range verifierCounts {
		if count == 0 {
			return CloudIdentityTrustEnforcementPolicy{}, fail(ErrInvalidPlan, "cloudIdentityTrustPolicy.planInputs.identityTrust.verifierPlacements", "Cloud enforcement requires at least one %s verifier", principal)
		}
	}
	sort.Slice(policy.Issuers, func(left, right int) bool { return policy.Issuers[left].ID < policy.Issuers[right].ID })
	sort.Slice(policy.Verifiers, func(left, right int) bool { return policy.Verifiers[left].ID < policy.Verifiers[right].ID })
	return policy, nil
}

func (r identityTrustPolicyRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	outputRefs := make([]string, len(r.outputs))
	for index := range r.outputs {
		outputRefs[index] = r.outputs[index].ref
	}
	planInputs, err := validateGenerationOnlyPolicyUnit(unit, generationOnlyPolicyUnitSpec{
		moduleID: r.moduleID, unitID: identityTrustPolicyUnitID, outputRefs: outputRefs,
		policyName: r.policyName, contract: r.contract, planInputRefs: r.planInputRefs,
		validatePlanInput: r.validate,
	})
	if err != nil {
		return nil, err
	}
	if identityTrustTemplatesHash(r.outputs) != r.contract.ContractHash {
		return nil, fail(ErrOutputChanged, "renderer."+r.moduleID+".template", "embedded policy manifest does not match its registered contract")
	}
	outputs := make([]UnitOutput, 0, len(r.outputs))
	for _, output := range r.outputs {
		if bytes.Count(output.template, []byte(identityTrustPolicyToken)) != 1 {
			return nil, fail(ErrOutputChanged, "renderer."+r.moduleID+".template", "embedded policy manifest has an invalid projection token")
		}
		outputs = append(outputs, UnitOutput{Ref: output.ref, Bytes: bytes.Replace(output.template, []byte(identityTrustPolicyToken), planInputs, 1)})
	}
	return outputs, nil
}

func cloneIdentityTrustTemplates(values []identityTrustPolicyOutputTemplate) []identityTrustPolicyOutputTemplate {
	result := make([]identityTrustPolicyOutputTemplate, len(values))
	for index, value := range values {
		result[index] = identityTrustPolicyOutputTemplate{ref: value.ref, template: append([]byte(nil), value.template...)}
	}
	return result
}

func identityTrustTemplatesHash(outputs []identityTrustPolicyOutputTemplate) string {
	hash := sha256.New()
	for _, output := range outputs {
		_, _ = hash.Write([]byte(output.ref))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(output.template)
		_, _ = hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

type identityTrustPlanInputs struct {
	StackID       string              `json:"stackId"`
	Kit           localAutonomyKit    `json:"kit"`
	Sites         []localAutonomySite `json:"sites"`
	IdentityTrust identityTrustGraph  `json:"identityTrust"`
}

type identityTrustGraph struct {
	Authorities           []identityTrustAuthority            `json:"authorities"`
	CredentialIssuers     []identityTrustCredentialIssuer     `json:"credentialIssuers"`
	VerifierPlacements    []identityTrustVerifierPlacement    `json:"verifierPlacements"`
	VerifierDistributions []identityTrustVerifierDistribution `json:"verifierDistributions"`
}

type identityTrustAuthority struct {
	ID             string                      `json:"id"`
	Principal      string                      `json:"principal"`
	TrustDomainRef string                      `json:"trustDomainRef"`
	Placement      identityTrustPlacement      `json:"placement"`
	Owner          identityTrustAuthorityOwner `json:"owner"`
}

type identityTrustPlacement struct {
	Kind        string   `json:"kind"`
	SiteRefs    []string `json:"siteRefs,omitempty"`
	ContractRef string   `json:"contractRef,omitempty"`
}

type identityTrustAuthorityOwner struct {
	Kind        string `json:"kind"`
	ProviderRef string `json:"providerRef,omitempty"`
	ModuleRef   string `json:"moduleRef,omitempty"`
	ContractRef string `json:"contractRef,omitempty"`
}

type identityTrustCredentialIssuer struct {
	ID                            string                      `json:"id"`
	AuthorityRef                  string                      `json:"authorityRef"`
	Principal                     string                      `json:"principal"`
	Issuer                        string                      `json:"issuer"`
	Audiences                     []string                    `json:"audiences"`
	VerificationKeySetRef         string                      `json:"verificationKeySetRef"`
	Placement                     identityTrustPlacement      `json:"placement"`
	Owner                         identityTrustAuthorityOwner `json:"owner"`
	IssuanceWithinStackKit        bool                        `json:"issuanceWithinStackKit"`
	CredentialTTLSeconds          int                         `json:"credentialTTLSeconds"`
	SessionTTLSeconds             int                         `json:"sessionTTLSeconds"`
	ProofOfPossessionRequired     bool                        `json:"proofOfPossessionRequired"`
	RevocationSupported           bool                        `json:"revocationSupported"`
	RevocationMaxStalenessSeconds int                         `json:"revocationMaxStalenessSeconds"`
	Enrollment                    identityTrustEnrollment     `json:"enrollment"`
}

type identityTrustEnrollment struct {
	Mode     string `json:"mode"`
	Exposure string `json:"exposure"`
}

type identityTrustVerifierPlacement struct {
	ID                            string                    `json:"id"`
	CredentialIssuerRef           string                    `json:"credentialIssuerRef"`
	Issuer                        string                    `json:"issuer"`
	Principal                     string                    `json:"principal"`
	Audiences                     []string                  `json:"audiences"`
	VerificationKeySetRef         string                    `json:"verificationKeySetRef"`
	Placement                     identityTrustPlacement    `json:"placement"`
	Owner                         identityTrustCatalogOwner `json:"owner"`
	ProofOfPossessionRequired     bool                      `json:"proofOfPossessionRequired"`
	RevocationMaxStalenessSeconds int                       `json:"revocationMaxStalenessSeconds"`
}

type identityTrustCatalogOwner struct {
	Kind        string `json:"kind"`
	ProviderRef string `json:"providerRef"`
	ModuleRef   string `json:"moduleRef"`
}

type identityTrustVerifierDistribution struct {
	ID                          string                    `json:"id"`
	CredentialIssuerRef         string                    `json:"credentialIssuerRef"`
	Issuer                      string                    `json:"issuer"`
	From                        identityTrustPlacement    `json:"from"`
	To                          identityTrustPlacement    `json:"to"`
	Materials                   []string                  `json:"materials"`
	IncludesSigningAuthority    bool                      `json:"includesSigningAuthority"`
	IncludesEnrollmentAuthority bool                      `json:"includesEnrollmentAuthority"`
	IncludesPrivateKeyMaterial  bool                      `json:"includesPrivateKeyMaterial"`
	IncludesCredentialMaterial  bool                      `json:"includesCredentialMaterial"`
	ReverseAllowed              bool                      `json:"reverseAllowed"`
	MaxStalenessSeconds         int                       `json:"maxStalenessSeconds"`
	Owner                       identityTrustCatalogOwner `json:"owner"`
}

type validatedIdentityTrust struct {
	inputs        identityTrustPlanInputs
	siteKinds     map[string]string
	homeSiteRefs  []string
	cloudSiteRefs []string
	authorities   map[string]identityTrustAuthority
	issuers       map[string]identityTrustCredentialIssuer
	issuersByURN  map[string]identityTrustCredentialIssuer
	verifiers     map[string]identityTrustVerifierPlacement
}

func decodeIdentityTrustPlanInputs(raw []byte, path string) (validatedIdentityTrust, error) {
	var inputs identityTrustPlanInputs
	if err := decodeStrict(raw, &inputs); err != nil {
		return validatedIdentityTrust{}, wrap(ErrInvalidPlan, path, "decode exact identity-trust plan inputs", err)
	}
	validated, err := validateIdentityTrustEnvelope(inputs, raw, path)
	if err != nil {
		return validatedIdentityTrust{}, err
	}
	return validated, nil
}

func validateIdentityTrustEnvelope(inputs identityTrustPlanInputs, raw []byte, path string) (validatedIdentityTrust, error) {
	if err := requireContractID(inputs.StackID, path+".stackId"); err != nil {
		return validatedIdentityTrust{}, err
	}
	if err := requireContractID(inputs.Kit.Slug, path+".kit.slug"); err != nil || strings.TrimSpace(inputs.Kit.Version) == "" || !validSHA256(inputs.Kit.DefinitionHash) {
		if err != nil {
			return validatedIdentityTrust{}, err
		}
		return validatedIdentityTrust{}, fail(ErrInvalidPlan, path+".kit", "identity trust requires an exact governed kit identity")
	}
	siteKinds, homeSiteRefs, cloudSiteRefs, err := validateLocalAutonomySites(inputs.Sites, path)
	if err != nil {
		return validatedIdentityTrust{}, err
	}
	if err := requireSortedSites(inputs.Sites, path+".sites"); err != nil {
		return validatedIdentityTrust{}, err
	}
	validated := validatedIdentityTrust{
		inputs: inputs, siteKinds: siteKinds, homeSiteRefs: homeSiteRefs, cloudSiteRefs: cloudSiteRefs,
		authorities: make(map[string]identityTrustAuthority), issuers: make(map[string]identityTrustCredentialIssuer),
		issuersByURN: make(map[string]identityTrustCredentialIssuer), verifiers: make(map[string]identityTrustVerifierPlacement),
	}
	if err := validateIdentityTrustGraph(&validated, path+".identityTrust"); err != nil {
		return validatedIdentityTrust{}, err
	}
	if err := rejectIdentityTrustProjectionLeaks(raw, path); err != nil {
		return validatedIdentityTrust{}, err
	}
	return validated, nil
}

//nolint:gocyclo // The closed graph and all of its cross-references are one fail-closed trust boundary.
func validateIdentityTrustGraph(validated *validatedIdentityTrust, path string) error {
	graph := validated.inputs.IdentityTrust
	if graph.Authorities == nil || graph.CredentialIssuers == nil || graph.VerifierPlacements == nil || graph.VerifierDistributions == nil {
		return fail(ErrInvalidPlan, path, "all identity-trust graph arrays are required, even when empty")
	}
	seenIDs := map[string]struct{}{}
	for index, authority := range graph.Authorities {
		authorityPath := fmt.Sprintf("%s.authorities[%d]", path, index)
		if err := requireUniqueIdentityID(authority.ID, seenIDs, authorityPath+".id"); err != nil {
			return err
		}
		if !validIdentityPrincipal(authority.Principal) {
			return fail(ErrInvalidPlan, authorityPath+".principal", "must be human, device, or workload")
		}
		if err := requireContractID(authority.TrustDomainRef, authorityPath+".trustDomainRef"); err != nil {
			return err
		}
		if err := validateIdentityTrustPlacement(authority.Placement, validated.siteKinds, authorityPath+".placement", true); err != nil {
			return err
		}
		if err := validateIdentityAuthorityOwnerShape(authority.Owner, authorityPath+".owner"); err != nil {
			return err
		}
		validated.authorities[authority.ID] = authority
	}
	seenIDs = map[string]struct{}{}
	for index, issuer := range graph.CredentialIssuers {
		issuerPath := fmt.Sprintf("%s.credentialIssuers[%d]", path, index)
		if err := requireUniqueIdentityID(issuer.ID, seenIDs, issuerPath+".id"); err != nil {
			return err
		}
		authority, exists := validated.authorities[issuer.AuthorityRef]
		if !exists || issuer.Principal != authority.Principal || !sameIdentityTrustPlacement(issuer.Placement, authority.Placement) || !sameIdentityTrustOwner(issuer.Owner, authority.Owner) {
			return fail(ErrInvalidPlan, issuerPath, "credential issuer must exactly bind its authority principal, placement, and owner")
		}
		if err := requireStackInstanceURN(issuer.Issuer, validated.inputs.StackID, "issuer", issuerPath+".issuer"); err != nil {
			return err
		}
		if err := requireStackInstanceURN(issuer.VerificationKeySetRef, validated.inputs.StackID, "keyset", issuerPath+".verificationKeySetRef"); err != nil {
			return err
		}
		if err := requireSortedStackInstanceURNs(issuer.Audiences, validated.inputs.StackID, "audience", issuerPath+".audiences"); err != nil {
			return err
		}
		if err := validateIdentityTrustPlacement(issuer.Placement, validated.siteKinds, issuerPath+".placement", true); err != nil {
			return err
		}
		if err := validateIdentityAuthorityOwnerShape(issuer.Owner, issuerPath+".owner"); err != nil {
			return err
		}
		if issuer.CredentialTTLSeconds < 300 || issuer.CredentialTTLSeconds > 86400 || issuer.SessionTTLSeconds < 60 || issuer.SessionTTLSeconds > 86400 ||
			issuer.RevocationMaxStalenessSeconds < 0 || !issuer.RevocationSupported || issuer.RevocationMaxStalenessSeconds > issuer.CredentialTTLSeconds {
			return fail(ErrInvalidPlan, issuerPath, "credential TTL and revocation staleness must be bounded and internally consistent")
		}
		if issuer.Principal != "human" && !issuer.ProofOfPossessionRequired {
			return fail(ErrInvalidPlan, issuerPath+".proofOfPossessionRequired", "device and workload credentials must be possession-bound")
		}
		if !validIdentityEnrollment(issuer.Enrollment) || issuer.Principal != "device" && issuer.Enrollment.Mode != "none" {
			return fail(ErrInvalidPlan, issuerPath+".enrollment", "enrollment mode and exposure are inconsistent with the principal")
		}
		if _, duplicate := validated.issuersByURN[issuer.Issuer]; duplicate {
			return fail(ErrDuplicate, issuerPath+".issuer", "credential issuer must be unique")
		}
		validated.issuers[issuer.ID] = issuer
		validated.issuersByURN[issuer.Issuer] = issuer
	}
	seenIDs = map[string]struct{}{}
	for index, verifier := range graph.VerifierPlacements {
		verifierPath := fmt.Sprintf("%s.verifierPlacements[%d]", path, index)
		if err := requireUniqueIdentityID(verifier.ID, seenIDs, verifierPath+".id"); err != nil {
			return err
		}
		issuer, exists := validated.issuers[verifier.CredentialIssuerRef]
		if !exists || verifier.Issuer != issuer.Issuer || verifier.Principal != issuer.Principal || verifier.VerificationKeySetRef != issuer.VerificationKeySetRef ||
			!exactStringList(verifier.Audiences, issuer.Audiences) || verifier.ProofOfPossessionRequired != issuer.ProofOfPossessionRequired {
			return fail(ErrInvalidPlan, verifierPath, "verifier must exactly bind one credential issuer's principal, audiences, and verification key reference")
		}
		if err := requireStackInstanceURN(verifier.Issuer, validated.inputs.StackID, "issuer", verifierPath+".issuer"); err != nil {
			return err
		}
		if err := requireStackInstanceURN(verifier.VerificationKeySetRef, validated.inputs.StackID, "keyset", verifierPath+".verificationKeySetRef"); err != nil {
			return err
		}
		if err := requireSortedStackInstanceURNs(verifier.Audiences, validated.inputs.StackID, "audience", verifierPath+".audiences"); err != nil {
			return err
		}
		if err := validateIdentityTrustPlacement(verifier.Placement, validated.siteKinds, verifierPath+".placement", false); err != nil {
			return err
		}
		if verifier.RevocationMaxStalenessSeconds < 0 || verifier.RevocationMaxStalenessSeconds > issuer.RevocationMaxStalenessSeconds {
			return fail(ErrInvalidPlan, verifierPath+".revocationMaxStalenessSeconds", "must not be weaker than the credential issuer")
		}
		if err := validateCatalogOwner(verifier.Owner, verifierPath+".owner"); err != nil {
			return err
		}
		validated.verifiers[verifier.ID] = verifier
	}
	seenIDs = map[string]struct{}{}
	for index, distribution := range graph.VerifierDistributions {
		distributionPath := fmt.Sprintf("%s.verifierDistributions[%d]", path, index)
		if err := requireUniqueIdentityID(distribution.ID, seenIDs, distributionPath+".id"); err != nil {
			return err
		}
		issuer, exists := validated.issuers[distribution.CredentialIssuerRef]
		if !exists || distribution.Issuer != issuer.Issuer {
			return fail(ErrInvalidPlan, distributionPath+".credentialIssuerRef", "distribution must bind an exact credential issuer ID and resolved issuer URN")
		}
		if err := requireStackInstanceURN(distribution.Issuer, validated.inputs.StackID, "issuer", distributionPath+".issuer"); err != nil {
			return err
		}
		if err := validateIdentityTrustPlacement(distribution.From, validated.siteKinds, distributionPath+".from", false); err != nil {
			return err
		}
		if err := validateIdentityTrustPlacement(distribution.To, validated.siteKinds, distributionPath+".to", false); err != nil {
			return err
		}
		if siteRefsOverlap(distribution.From.SiteRefs, distribution.To.SiteRefs) || !sameIdentityTrustPlacement(distribution.From, issuer.Placement) ||
			!exactStringList(distribution.Materials, []string{"revocation-state", "verification-key-reference"}) {
			return fail(ErrInvalidPlan, distributionPath, "distribution must be one-way and carry only revocation-state plus verification-key-reference")
		}
		if distribution.IncludesCredentialMaterial || distribution.IncludesPrivateKeyMaterial || distribution.IncludesSigningAuthority || distribution.IncludesEnrollmentAuthority || distribution.ReverseAllowed {
			return fail(ErrInvalidPlan, distributionPath, "distribution must not carry credentials, private keys, signing, or enrollment authority")
		}
		if distribution.MaxStalenessSeconds < 0 || distribution.MaxStalenessSeconds > issuer.RevocationMaxStalenessSeconds {
			return fail(ErrInvalidPlan, distributionPath+".maxStalenessSeconds", "must not be weaker than the credential issuer")
		}
		if err := validateCatalogOwner(distribution.Owner, distributionPath+".owner"); err != nil {
			return err
		}
		if !identityDistributionHasExactVerifierCoverage(distribution, validated.verifiers) {
			return fail(ErrInvalidPlan, distributionPath+".to", "distribution target Sites require exact verifier coverage for its credential issuer")
		}
	}
	return nil
}

func validateHomeDeviceAuthorityPlanInputs(raw []byte, path string) ([]string, error) {
	validated, err := decodeIdentityTrustPlanInputs(raw, path)
	if err != nil {
		return nil, err
	}
	if len(validated.homeSiteRefs) == 0 {
		return nil, fail(ErrInvalidPlan, path+".sites", "Home device authority requires at least one Home Site")
	}
	deviceIssuer, err := validateHomeAuthorityAndIssuance(validated.inputs.IdentityTrust, validated.siteKinds, path)
	if err != nil {
		return nil, err
	}
	if !deviceIssuer || !hasPrincipalIssuer(validated, "device") {
		return nil, fail(ErrInvalidPlan, path+".identityTrust", "Home authority graph requires an exact device authority and credential issuer")
	}
	return validated.homeSiteRefs, nil
}

//nolint:gocyclo // Local issuer ownership and local verifier trust form one Basement boundary.
func validateBasementIdentityTrustPlanInputs(raw []byte, path string) ([]string, error) {
	validated, err := decodeIdentityTrustPlanInputs(raw, path)
	if err != nil {
		return nil, err
	}
	if validated.inputs.Kit.Slug != "basement-kit" || len(validated.homeSiteRefs) != 1 || len(validated.cloudSiteRefs) != 0 {
		return nil, fail(ErrInvalidPlan, path+".sites", "Basement identity trust requires exactly one Home Site and no Cloud Site")
	}
	if len(validated.inputs.IdentityTrust.VerifierDistributions) != 0 || len(validated.inputs.IdentityTrust.VerifierPlacements) == 0 {
		return nil, fail(ErrInvalidPlan, path+".identityTrust", "Basement requires local verifiers and has no verifier distribution")
	}
	for index, authority := range validated.inputs.IdentityTrust.Authorities {
		authorityPath := fmt.Sprintf("%s.identityTrust.authorities[%d]", path, index)
		if authority.Placement.Kind != "sites" || !allSiteRefsHaveKind(authority.Placement.SiteRefs, validated.siteKinds, "home") {
			return nil, fail(ErrInvalidPlan, authorityPath+".placement", "Basement cannot reference an external or Cloud authority")
		}
		if err := requireAuthorityCatalogOwner(authority.Owner, homeDeviceAuthorityProviderRef, homeDeviceAuthorityPolicyModuleID, authorityPath+".owner"); err != nil {
			return nil, err
		}
	}
	for index, issuer := range validated.inputs.IdentityTrust.CredentialIssuers {
		issuerPath := fmt.Sprintf("%s.identityTrust.credentialIssuers[%d]", path, index)
		if issuer.Placement.Kind != "sites" || !allSiteRefsHaveKind(issuer.Placement.SiteRefs, validated.siteKinds, "home") || !issuer.IssuanceWithinStackKit {
			return nil, fail(ErrInvalidPlan, issuerPath, "Basement credential issuers must remain owned and active at Home")
		}
		if err := requireAuthorityCatalogOwner(issuer.Owner, homeDeviceAuthorityProviderRef, homeDeviceAuthorityPolicyModuleID, issuerPath+".owner"); err != nil {
			return nil, err
		}
	}
	for index, verifier := range validated.inputs.IdentityTrust.VerifierPlacements {
		verifierPath := fmt.Sprintf("%s.identityTrust.verifierPlacements[%d]", path, index)
		if verifier.Placement.Kind != "sites" || !allSiteRefsHaveKind(verifier.Placement.SiteRefs, validated.siteKinds, "home") {
			return nil, fail(ErrInvalidPlan, verifierPath+".placement", "Basement verifiers must remain at Home")
		}
		if err := requireCatalogOwner(verifier.Owner, basementIdentityTrustProviderRef, basementIdentityTrustPolicyModuleID, verifierPath+".owner"); err != nil {
			return nil, err
		}
	}
	if !hasPrincipalVerifier(validated, "device") {
		return nil, fail(ErrInvalidPlan, path+".identityTrust.verifierPlacements", "Basement requires a device credential verifier")
	}
	return validated.homeSiteRefs, nil
}

//nolint:gocyclo // External issuer denial and Cloud verifier ownership form one Cloud boundary.
func validateCloudIdentityTrustPlanInputs(raw []byte, path string) ([]string, error) {
	validated, err := decodeIdentityTrustPlanInputs(raw, path)
	if err != nil {
		return nil, err
	}
	if validated.inputs.Kit.Slug != "cloud-kit" || len(validated.homeSiteRefs) != 0 || len(validated.cloudSiteRefs) == 0 {
		return nil, fail(ErrInvalidPlan, path+".sites", "Cloud identity trust requires Cloud Sites only")
	}
	if len(validated.inputs.IdentityTrust.VerifierDistributions) != 0 || len(validated.inputs.IdentityTrust.VerifierPlacements) == 0 {
		return nil, fail(ErrInvalidPlan, path+".identityTrust", "Cloud trust requires verifiers and cannot claim private in-stack distribution")
	}
	for index, authority := range validated.inputs.IdentityTrust.Authorities {
		authorityPath := fmt.Sprintf("%s.identityTrust.authorities[%d]", path, index)
		if authority.Principal == "device" {
			if authority.Placement.Kind != "external" || authority.Placement.ContractRef != externalDeviceAuthorityContractRef {
				return nil, fail(ErrInvalidPlan, authorityPath, "Cloud device trust requires the exact external device authority contract")
			}
			if err := requireExternalAuthorityOwner(authority.Owner, authorityPath+".owner"); err != nil {
				return nil, err
			}
			continue
		}
		if authority.Placement.Kind != "sites" || !allSiteRefsHaveKind(authority.Placement.SiteRefs, validated.siteKinds, "cloud") {
			return nil, fail(ErrInvalidPlan, authorityPath+".placement", "Cloud human and workload authorities must remain at Cloud Sites")
		}
		if err := requireAuthorityCatalogOwner(authority.Owner, cloudIdentityTrustProviderRef, cloudIdentityTrustPolicyModuleID, authorityPath+".owner"); err != nil {
			return nil, err
		}
	}
	for index, issuer := range validated.inputs.IdentityTrust.CredentialIssuers {
		issuerPath := fmt.Sprintf("%s.identityTrust.credentialIssuers[%d]", path, index)
		if issuer.Enrollment.Mode != "none" || issuer.Enrollment.Exposure != "none" {
			return nil, fail(ErrInvalidPlan, issuerPath+".enrollment", "Cloud identity trust cannot claim device enrollment")
		}
		if issuer.Principal == "device" {
			if issuer.IssuanceWithinStackKit || issuer.Placement.Kind != "external" || issuer.Placement.ContractRef != externalDeviceAuthorityContractRef {
				return nil, fail(ErrInvalidPlan, issuerPath, "Cloud cannot claim external device credential issuance")
			}
			if err := requireExternalAuthorityOwner(issuer.Owner, issuerPath+".owner"); err != nil {
				return nil, err
			}
			continue
		}
		if !issuer.IssuanceWithinStackKit || issuer.Placement.Kind != "sites" || !allSiteRefsHaveKind(issuer.Placement.SiteRefs, validated.siteKinds, "cloud") {
			return nil, fail(ErrInvalidPlan, issuerPath, "Cloud human and workload issuers must remain StackKit-owned at Cloud Sites")
		}
		if err := requireAuthorityCatalogOwner(issuer.Owner, cloudIdentityTrustProviderRef, cloudIdentityTrustPolicyModuleID, issuerPath+".owner"); err != nil {
			return nil, err
		}
	}
	for index, verifier := range validated.inputs.IdentityTrust.VerifierPlacements {
		verifierPath := fmt.Sprintf("%s.identityTrust.verifierPlacements[%d]", path, index)
		if verifier.Placement.Kind != "sites" || !allSiteRefsHaveKind(verifier.Placement.SiteRefs, validated.siteKinds, "cloud") {
			return nil, fail(ErrInvalidPlan, verifierPath+".placement", "Cloud verifiers must remain at Cloud Sites")
		}
		if err := requireCatalogOwner(verifier.Owner, cloudIdentityTrustProviderRef, cloudIdentityTrustPolicyModuleID, verifierPath+".owner"); err != nil {
			return nil, err
		}
	}
	if !hasPrincipalIssuer(validated, "device") || !hasPrincipalVerifier(validated, "device") {
		return nil, fail(ErrInvalidPlan, path+".identityTrust", "Cloud trust requires an exact external device issuer and Cloud device verifier")
	}
	return validated.cloudSiteRefs, nil
}

func validateIdentityTrustPlacement(placement identityTrustPlacement, siteKinds map[string]string, path string, allowExternal bool) error {
	switch placement.Kind {
	case "sites":
		if placement.ContractRef != "" {
			return fail(ErrInvalidPlan, path+".contractRef", "Site placement cannot carry an external contract")
		}
		return requireSortedSiteRefs(placement.SiteRefs, siteKinds, path+".siteRefs", true)
	case "external":
		if !allowExternal || placement.SiteRefs != nil || placement.ContractRef == "" {
			return fail(ErrInvalidPlan, path, "external placement requires an exact contract and no StackKit Sites")
		}
		return requireContractID(placement.ContractRef, path+".contractRef")
	default:
		return fail(ErrInvalidPlan, path+".kind", "must be sites or external")
	}
}

func validateIdentityAuthorityOwnerShape(owner identityTrustAuthorityOwner, path string) error {
	switch owner.Kind {
	case "catalog":
		if owner.ContractRef != "" {
			return fail(ErrInvalidPlan, path+".contractRef", "catalog owner cannot carry an external contract")
		}
		for field, value := range map[string]string{"providerRef": owner.ProviderRef, "moduleRef": owner.ModuleRef} {
			if err := requireContractID(value, path+"."+field); err != nil {
				return err
			}
		}
		return nil
	case "external":
		if owner.ProviderRef != "" || owner.ModuleRef != "" {
			return fail(ErrInvalidPlan, path, "external owner structurally forbids providerRef and moduleRef")
		}
		return requireContractID(owner.ContractRef, path+".contractRef")
	default:
		return fail(ErrInvalidPlan, path+".kind", "must be catalog or external")
	}
}

func validateCatalogOwner(owner identityTrustCatalogOwner, path string) error {
	if owner.Kind != "catalog" {
		return fail(ErrInvalidPlan, path+".kind", "verifier and distribution owners must be catalog-owned")
	}
	if err := requireContractID(owner.ProviderRef, path+".providerRef"); err != nil {
		return err
	}
	return requireContractID(owner.ModuleRef, path+".moduleRef")
}

func requireAuthorityCatalogOwner(owner identityTrustAuthorityOwner, providerRef, moduleRef, path string) error {
	if owner.Kind != "catalog" || owner.ProviderRef != providerRef || owner.ModuleRef != moduleRef || owner.ContractRef != "" {
		return fail(ErrInvalidPlan, path, "authority must be owned by exact catalog provider/module %s/%s", providerRef, moduleRef)
	}
	return nil
}

func requireExternalAuthorityOwner(owner identityTrustAuthorityOwner, path string) error {
	if owner.Kind != "external" || owner.ProviderRef != "" || owner.ModuleRef != "" || owner.ContractRef != externalDeviceAuthorityContractRef {
		return fail(ErrInvalidPlan, path, "external authority must bind exact contract %q and no catalog owner", externalDeviceAuthorityContractRef)
	}
	return nil
}

func requireCatalogOwner(owner identityTrustCatalogOwner, providerRef, moduleRef, path string) error {
	if owner.Kind != "catalog" || owner.ProviderRef != providerRef || owner.ModuleRef != moduleRef {
		return fail(ErrInvalidPlan, path, "must be owned by exact catalog provider/module %s/%s", providerRef, moduleRef)
	}
	return nil
}

func requireSortedSites(sites []localAutonomySite, path string) error {
	previous := ""
	for index, site := range sites {
		if previous != "" && site.ID <= previous {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s[%d].id", path, index), "sites must be unique and sorted by id")
		}
		previous = site.ID
	}
	return nil
}

func requireUniqueIdentityID(value string, seen map[string]struct{}, path string) error {
	if err := requireContractID(value, path); err != nil {
		return err
	}
	if _, duplicate := seen[value]; duplicate {
		return fail(ErrDuplicate, path, "record id must be unique")
	}
	seen[value] = struct{}{}
	return nil
}

func requireSortedSiteRefs(values []string, siteKinds map[string]string, path string, nonEmpty bool) error {
	if err := requireSortedLogicalRefs(values, path, nonEmpty); err != nil {
		return err
	}
	for _, value := range values {
		if _, exists := siteKinds[value]; !exists {
			return fail(ErrInvalidPlan, path, "references an unknown site %q", value)
		}
	}
	return nil
}

func requireSortedLogicalRefs(values []string, path string, nonEmpty bool) error {
	if values == nil || nonEmpty && len(values) == 0 {
		return fail(ErrInvalidPlan, path, "must be a present, sorted, non-empty reference list")
	}
	previous := ""
	for index, value := range values {
		if err := requireLogicalIdentityRef(value, fmt.Sprintf("%s[%d]", path, index)); err != nil {
			return err
		}
		if previous != "" && value <= previous {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s[%d]", path, index), "references must be unique and sorted")
		}
		previous = value
	}
	return nil
}

func requireLogicalIdentityRef(value, path string) error {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	if trimmed == "" || trimmed != value || len(value) > 512 || strings.ContainsAny(value, "\t\r\n/\\") || strings.Contains(lower, "://") || validSecretReference(lower) {
		return fail(ErrInvalidPlan, path, "must be a logical identity reference, never a URL, path, network location, or secret reference")
	}
	return nil
}

func requireStackInstanceURN(value, stackID, urnType, path string) error {
	prefix := "urn:stackkit:" + stackID + ":" + urnType + ":"
	if !strings.HasPrefix(value, prefix) || len(value) == len(prefix) {
		return fail(ErrInvalidPlan, path, "must be a resolved StackInstance-bound %s URN with prefix %q", urnType, prefix)
	}
	return requireLogicalIdentityRef(value, path)
}

func requireSortedStackInstanceURNs(values []string, stackID, urnType, path string) error {
	if len(values) == 0 {
		return fail(ErrInvalidPlan, path, "must be a present, sorted, non-empty resolved URN list")
	}
	previous := ""
	for index, value := range values {
		valuePath := fmt.Sprintf("%s[%d]", path, index)
		if err := requireStackInstanceURN(value, stackID, urnType, valuePath); err != nil {
			return err
		}
		if previous != "" && value <= previous {
			return fail(ErrInvalidPlan, valuePath, "URNs must be unique and sorted")
		}
		previous = value
	}
	return nil
}

func validateHomeAuthorityAndIssuance(graph identityTrustGraph, siteKinds map[string]string, path string) (bool, error) {
	for index, authority := range graph.Authorities {
		authorityPath := fmt.Sprintf("%s.identityTrust.authorities[%d]", path, index)
		if authority.Placement.Kind != "sites" || !allSiteRefsHaveKind(authority.Placement.SiteRefs, siteKinds, "home") {
			return false, fail(ErrInvalidPlan, authorityPath+".placement", "signing authorities must remain at Home")
		}
		if err := requireAuthorityCatalogOwner(authority.Owner, homeDeviceAuthorityProviderRef, homeDeviceAuthorityPolicyModuleID, authorityPath+".owner"); err != nil {
			return false, err
		}
	}
	deviceIssuer := false
	for index, issuer := range graph.CredentialIssuers {
		issuerPath := fmt.Sprintf("%s.identityTrust.credentialIssuers[%d]", path, index)
		if issuer.Placement.Kind != "sites" || !allSiteRefsHaveKind(issuer.Placement.SiteRefs, siteKinds, "home") || !issuer.IssuanceWithinStackKit || !issuer.RevocationSupported {
			return false, fail(ErrInvalidPlan, issuerPath, "Home credential issuers must remain owned and active at Home")
		}
		if err := requireAuthorityCatalogOwner(issuer.Owner, homeDeviceAuthorityProviderRef, homeDeviceAuthorityPolicyModuleID, issuerPath+".owner"); err != nil {
			return false, err
		}
		if issuer.Principal == "device" {
			if !issuer.ProofOfPossessionRequired || issuer.Enrollment.Mode != "local-only" || issuer.Enrollment.Exposure != "lan" {
				return false, fail(ErrInvalidPlan, issuerPath, "device issuance requires possession proof and LAN-local enrollment")
			}
			deviceIssuer = true
		}
	}
	return deviceIssuer, nil
}

func validIdentityEnrollment(value identityTrustEnrollment) bool {
	return value.Mode == "local-only" && value.Exposure == "lan" || value.Mode == "none" && value.Exposure == "none"
}

func sameIdentityTrustPlacement(left, right identityTrustPlacement) bool {
	return left.Kind == right.Kind && left.ContractRef == right.ContractRef && exactStringList(left.SiteRefs, right.SiteRefs)
}

func sameIdentityTrustOwner(left, right identityTrustAuthorityOwner) bool {
	return left.Kind == right.Kind && left.ProviderRef == right.ProviderRef && left.ModuleRef == right.ModuleRef && left.ContractRef == right.ContractRef
}

func siteRefsOverlap(left, right []string) bool {
	seen := make(map[string]struct{}, len(left))
	for _, value := range left {
		seen[value] = struct{}{}
	}
	for _, value := range right {
		if _, exists := seen[value]; exists {
			return true
		}
	}
	return false
}

func identityDistributionHasExactVerifierCoverage(distribution identityTrustVerifierDistribution, verifiers map[string]identityTrustVerifierPlacement) bool {
	covered := make(map[string]struct{}, len(distribution.To.SiteRefs))
	for _, verifier := range verifiers {
		if verifier.CredentialIssuerRef != distribution.CredentialIssuerRef || verifier.Issuer != distribution.Issuer {
			continue
		}
		for _, siteRef := range verifier.Placement.SiteRefs {
			if stringListContains(distribution.To.SiteRefs, siteRef) {
				covered[siteRef] = struct{}{}
			}
		}
	}
	return len(covered) == len(distribution.To.SiteRefs)
}

func validIdentityPrincipal(value string) bool {
	return value == "human" || value == "device" || value == "workload"
}

func allSiteRefsHaveKind(refs []string, siteKinds map[string]string, kind string) bool {
	if len(refs) == 0 {
		return false
	}
	for _, ref := range refs {
		if siteKinds[ref] != kind {
			return false
		}
	}
	return true
}

func hasPrincipalIssuer(validated validatedIdentityTrust, principal string) bool {
	for _, issuer := range validated.issuers {
		if issuer.Principal == principal {
			return true
		}
	}
	return false
}

func hasPrincipalVerifier(validated validatedIdentityTrust, principal string) bool {
	for _, verifier := range validated.verifiers {
		if verifier.Principal == principal {
			return true
		}
	}
	return false
}

func rejectIdentityTrustProjectionLeaks(raw []byte, path string) error {
	if err := rejectGenerationOnlyPolicyProjectionLeaks(raw, path, "identity-trust policy"); err != nil {
		return err
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return wrap(ErrInvalidPlan, path, "scan identity-trust projection", err)
	}
	forbiddenKeys := map[string]struct{}{
		"jwks": {}, "jwksbytes": {}, "keybytes": {}, "privatekey": {}, "privatekeybytes": {},
		"publickey": {}, "publickeybytes": {}, "token": {}, "secret": {}, "url": {}, "uri": {},
		"endpoint": {}, "address": {}, "addresses": {}, "cidr": {}, "network": {}, "socket": {},
	}
	var walk func(any, string) error
	walk = func(current any, currentPath string) error {
		switch typed := current.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if _, forbidden := forbiddenKeys[strings.ToLower(key)]; forbidden {
					return fail(ErrInvalidPlan, currentPath+"."+key, "runtime key, credential material, URL, or network detail is outside the identity-trust projection")
				}
				if err := walk(typed[key], currentPath+"."+key); err != nil {
					return err
				}
			}
		case []any:
			for index, nested := range typed {
				if err := walk(nested, fmt.Sprintf("%s[%d]", currentPath, index)); err != nil {
					return err
				}
			}
		case string:
			lower := strings.ToLower(typed)
			if validSecretReference(lower) || strings.Contains(lower, "://") || strings.ContainsAny(typed, "\r\n") {
				return fail(ErrInvalidPlan, currentPath, "URLs, network locations, secret references, and credential material are forbidden")
			}
		}
		return nil
	}
	return walk(value, path)
}
