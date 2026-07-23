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
	internalPKIModuleID    = "stackkits-internal-pki-contract"
	internalPKIUnitID      = "executor-contract"
	internalPKITemplateRef = "builtin://home/tls/internal-pki-executor-contract/v1.json"
	internalPKIOutputRef   = "home/tls/internal-pki-executor-contract.json"
	internalPKIToken       = "@@PLAN_INPUTS@@"
)

const internalPKITemplate = `{"apiVersion":"stackkit.internal-pki-executor-contract/v1","kind":"InternalPKIExecutorContract","module":{"id":"stackkits-internal-pki-contract","version":"1.0.0"},"contract":{"caAuthority":"single-explicit-home-node","certificateMaterial":"owner-held","credentials":"not-included","execution":"unverified","generation":"supported","leafIssuance":"unbound","providerLifecycle":"not-owned","runtimeEnforcement":"unverified","scope":"home-pki-contract","serverProviderAuthority":"not-owned","trustDistribution":"public-root-only"},"planInputs":@@PLAN_INPUTS@@}
`

var internalPKIPlanInputRefs = []string{"internalPKI", "kit", "stackId"}

func InternalPKIExecutorContractRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(internalPKITemplate))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit", TemplateRef: internalPKITemplateRef,
		Version: "1.0.0", ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type internalPKIRenderer struct {
	contract RendererContract
}

func newInternalPKIRenderer() internalPKIRenderer {
	return internalPKIRenderer{contract: InternalPKIExecutorContractRendererContract()}
}

type internalPKIPlan struct {
	StackID     string                `json:"stackId"`
	Kit         publicTLSExecutorKit  `json:"kit"`
	InternalPKI internalPKIProjection `json:"internalPKI"`
}

type internalPKIProjection struct {
	CapabilityRef     string                       `json:"capabilityRef"`
	ProviderRef       string                       `json:"providerRef"`
	Authority         internalPKIAuthority         `json:"authority"`
	TrustDistribution internalPKITrustDistribution `json:"trustDistribution"`
	LeafIssuance      internalPKILeafIssuance      `json:"leafIssuance"`
	Profile           publicTLSExecutorProfile     `json:"profile"`
	Issuer            publicTLSExecutorIssuer      `json:"issuer"`
}

type internalPKIAuthority struct {
	ID               string `json:"id"`
	Role             string `json:"role"`
	SiteRef          string `json:"siteRef"`
	NodeRef          string `json:"nodeRef"`
	TrustDomainRef   string `json:"trustDomainRef"`
	SubjectRef       string `json:"subjectRef"`
	KeyAlgorithm     string `json:"keyAlgorithm"`
	BasicConstraints struct {
		CA      bool `json:"ca"`
		PathLen int  `json:"pathLen"`
	} `json:"basicConstraints"`
	KeyUsage []string `json:"keyUsage"`
}

type internalPKITrustDistribution struct {
	Targets      []internalPKITrustTarget      `json:"targets"`
	MaterialSlot publicTLSExecutorMaterialSlot `json:"materialSlot"`
}

type internalPKITrustTarget struct {
	SiteRef string `json:"siteRef"`
	NodeRef string `json:"nodeRef"`
}

type internalPKILeafIssuance struct {
	Status                    string   `json:"status"`
	SubjectAuthority          string   `json:"subjectAuthority"`
	SANAuthority              string   `json:"sanAuthority"`
	CA                        bool     `json:"ca"`
	KeyAlgorithm              string   `json:"keyAlgorithm"`
	KeyUsage                  []string `json:"keyUsage"`
	ExtendedKeyUsage          []string `json:"extendedKeyUsage"`
	RequiredObservationFields []string `json:"requiredObservationFields"`
}

func (r internalPKIRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := validateGenerationOnlyPolicyUnit(unit, generationOnlyPolicyUnitSpec{
		moduleID: internalPKIModuleID, unitID: internalPKIUnitID, outputRef: internalPKIOutputRef,
		policyName: "internal PKI executor contract", contract: r.contract,
		planInputRefs: internalPKIPlanInputRefs, validatePlanInput: validateInternalPKIPlanInputs,
	})
	if err != nil {
		return nil, err
	}
	var plan internalPKIPlan
	if err := decodeStrict(raw, &plan); err != nil {
		return nil, wrap(ErrInvalidPlan, "renderer.internal-pki.planInputs", "decode exact internal PKI inputs", err)
	}
	nodeRefs := make([]string, len(plan.InternalPKI.TrustDistribution.Targets))
	for index := range plan.InternalPKI.TrustDistribution.Targets {
		nodeRefs[index] = plan.InternalPKI.TrustDistribution.Targets[index].NodeRef
	}
	if !exactStringList(unit.LogicalNodeRefs(), nodeRefs) {
		return nil, fail(ErrInvalidPlan, "renderer.internal-pki.placement", "logical nodes must exactly equal internal PKI targets")
	}
	canonical, err := json.Marshal(plan)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.internal-pki.planInputs", "marshal typed internal PKI inputs", err)
	}
	template := []byte(internalPKITemplate)
	if publicTLSExecutorContractTemplateHash(template) != r.contract.ContractHash || bytes.Count(template, []byte(internalPKIToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.internal-pki.template", "embedded contract identity drifted")
	}
	return []UnitOutput{{Ref: internalPKIOutputRef, Bytes: bytes.Replace(template, []byte(internalPKIToken), canonical, 1)}}, nil
}

func validateInternalPKIPlanInputs(raw []byte, path string) ([]string, error) {
	var plan internalPKIPlan
	if err := decodeStrict(raw, &plan); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact internal PKI executor inputs", err)
	}
	if err := requireContractID(plan.StackID, path+".stackId"); err != nil {
		return nil, err
	}
	if !containsExecutorBundleString([]string{"basement-kit", "modern-homelab"}, plan.Kit.Slug) ||
		strings.TrimSpace(plan.Kit.Version) == "" || !validSHA256(plan.Kit.DefinitionHash) {
		return nil, fail(ErrInvalidPlan, path+".kit", "internal PKI requires an exact Home-capable kit")
	}
	pki := plan.InternalPKI
	if pki.CapabilityRef != "internal-pki" || pki.ProviderRef != "stackkits-internal-pki" {
		return nil, fail(ErrInvalidPlan, path+".internalPKI", "projection must bind the exact internal PKI adapter")
	}
	authority := pki.Authority
	if authority.ID != "stackkits-home-root-ca" || authority.Role != "root-ca" ||
		authority.TrustDomainRef != plan.StackID || authority.SubjectRef != "stackkits-home-root-ca" ||
		authority.KeyAlgorithm != "ecdsa-p256" || !authority.BasicConstraints.CA ||
		authority.BasicConstraints.PathLen != 0 ||
		!exactStringList(authority.KeyUsage, []string{"cert-sign", "crl-sign"}) {
		return nil, fail(ErrInvalidPlan, path+".internalPKI.authority", "root CA authority is ambiguous or widened")
	}
	if err := requireContractID(authority.SiteRef, path+".internalPKI.authority.siteRef"); err != nil {
		return nil, err
	}
	if err := requireContractID(authority.NodeRef, path+".internalPKI.authority.nodeRef"); err != nil {
		return nil, err
	}
	targets := pki.TrustDistribution.Targets
	if len(targets) == 0 {
		return nil, fail(ErrInvalidPlan, path+".internalPKI.trustDistribution.targets", "trust distribution requires exact Home targets")
	}
	nodeRefs := make([]string, len(targets))
	siteSet := make(map[string]struct{}, len(targets))
	authorityTargets := 0
	previousNodeRef := ""
	for index, target := range targets {
		targetPath := fmt.Sprintf("%s.internalPKI.trustDistribution.targets[%d]", path, index)
		if err := requireContractID(target.SiteRef, targetPath+".siteRef"); err != nil {
			return nil, err
		}
		if err := requireContractID(target.NodeRef, targetPath+".nodeRef"); err != nil {
			return nil, err
		}
		if previousNodeRef != "" && target.NodeRef <= previousNodeRef {
			return nil, fail(ErrDuplicate, targetPath+".nodeRef", "trust targets must be unique and sorted")
		}
		if target.SiteRef == authority.SiteRef && target.NodeRef == authority.NodeRef {
			authorityTargets++
		}
		previousNodeRef = target.NodeRef
		nodeRefs[index] = target.NodeRef
		siteSet[target.SiteRef] = struct{}{}
	}
	if authorityTargets != 1 {
		return nil, fail(ErrInvalidPlan, path+".internalPKI.authority", "root CA authority must be exactly one trust target")
	}
	if slot := pki.TrustDistribution.MaterialSlot; slot != (publicTLSExecutorMaterialSlot{
		ID: "trust-root", Purpose: "trust-root", Sensitivity: "public",
	}) {
		return nil, fail(ErrInvalidPlan, path+".internalPKI.trustDistribution.materialSlot", "trust distribution may expose only the public trust root")
	}
	leaf := pki.LeafIssuance
	if leaf.Status != "unbound" || leaf.SubjectAuthority != "compiler-derived-service" ||
		leaf.SANAuthority != "compiler-derived-route" || leaf.CA ||
		leaf.KeyAlgorithm != "ecdsa-p256" ||
		!exactStringList(leaf.KeyUsage, []string{"digital-signature", "key-agreement"}) ||
		!exactStringList(leaf.ExtendedKeyUsage, []string{"server-auth", "client-auth"}) ||
		!exactStringList(leaf.RequiredObservationFields, []string{
			"certificate-fingerprint", "public-key-fingerprint", "trust-root-fingerprint",
			"serial", "not-before", "not-after", "observed-at",
		}) {
		return nil, fail(ErrInvalidPlan, path+".internalPKI.leafIssuance", "leaf issuance must remain compiler-derived and explicitly unbound")
	}
	if pki.Profile.ID != "stackkits-internal-pki-profile" || pki.Profile.CapabilityRef != "internal-pki" ||
		pki.Profile.Mode != "internal" || pki.Profile.TrustDomain != "private" ||
		!containsExecutorBundleString([]string{"TLS1.2", "TLS1.3"}, pki.Profile.MinimumVersion) ||
		!exactStringList(pki.Profile.AllowedIssuerKinds, []string{"internal-ca"}) {
		return nil, fail(ErrInvalidPlan, path+".internalPKI.profile", "profile is outside the private internal TLS contract")
	}
	issuer := pki.Issuer
	if issuer.ID != "stackkits-internal-ca" || issuer.CapabilityRef != "internal-pki" ||
		issuer.Kind != "internal-ca" || issuer.Challenge != "none" ||
		!exactStringList(issuer.SupportedSiteKinds, []string{"home"}) || issuer.ValiditySeconds != 7776000 ||
		len(issuer.RequiredInputSlotIDs) != 0 {
		return nil, fail(ErrInvalidPlan, path+".internalPKI.issuer", "issuer is outside the exact internal CA contract")
	}
	wantSlots := []publicTLSExecutorMaterialSlot{
		{ID: "root-certificate", Purpose: "certificate-chain", Sensitivity: "public"},
		{ID: "root-private-key", Purpose: "private-key", Sensitivity: "secret"},
		{ID: "trust-root", Purpose: "trust-root", Sensitivity: "public"},
	}
	if len(issuer.MaterialSlots) != len(wantSlots) {
		return nil, fail(ErrInvalidPlan, path+".internalPKI.issuer.materialSlots", "must expose exactly three logical slots")
	}
	for index := range wantSlots {
		if issuer.MaterialSlots[index] != wantSlots[index] {
			return nil, fail(ErrInvalidPlan, fmt.Sprintf("%s.internalPKI.issuer.materialSlots[%d]", path, index), "slot drifted")
		}
	}
	if !issuer.Renewal.Required || issuer.Renewal.HealthGateRef != "internal-pki-renewal-contract" ||
		issuer.Renewal.RenewBeforeSeconds != 2592000 || issuer.Renewal.RenewBeforeSeconds >= issuer.ValiditySeconds {
		return nil, fail(ErrInvalidPlan, path+".internalPKI.issuer.renewal", "renewal policy drifted")
	}
	if err := rejectPublicTLSExecutorContractLeaks(raw, path); err != nil {
		return nil, err
	}
	siteRefs := make([]string, 0, len(siteSet))
	for siteRef := range siteSet {
		siteRefs = append(siteRefs, siteRef)
	}
	sort.Strings(siteRefs)
	return siteRefs, nil
}
