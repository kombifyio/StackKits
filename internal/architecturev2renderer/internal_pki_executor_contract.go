package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	internalPKIModuleID    = "stackkits-internal-pki-contract"
	internalPKIUnitID      = "executor-contract"
	internalPKITemplateRef = "builtin://home/tls/internal-pki-executor-contract/v1.json"
	internalPKIOutputRef   = "home/tls/internal-pki-executor-contract.json"
	internalPKIToken       = "@@PLAN_INPUTS@@"
)

const internalPKITemplate = `{"apiVersion":"stackkit.internal-pki-executor-contract/v1","kind":"InternalPKIExecutorContract","module":{"id":"stackkits-internal-pki-contract","version":"1.0.0"},"contract":{"caAuthority":"single-explicit-home-node","certificateMaterial":"owner-held","credentials":"not-included","execution":"typed-local-operations","generation":"supported","leafIssuance":"compiler-bound","operations":["ensure-root-authority","issue-compiler-bound-leaves","distribute-public-trust-root","verify-internal-pki"],"providerLifecycle":"not-owned","runtimeEnforcement":"adapter-verified","scope":"home-pki-authority-node","serverProviderAuthority":"not-owned","trustDistribution":"public-root-only"},"planInputs":@@PLAN_INPUTS@@}
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
	Status                    string                           `json:"status"`
	SubjectAuthority          string                           `json:"subjectAuthority"`
	SANAuthority              string                           `json:"sanAuthority"`
	CA                        bool                             `json:"ca"`
	KeyAlgorithm              string                           `json:"keyAlgorithm"`
	KeyUsage                  []string                         `json:"keyUsage"`
	ExtendedKeyUsage          []string                         `json:"extendedKeyUsage"`
	RequiredObservationFields []string                         `json:"requiredObservationFields"`
	Identities                []InternalPKIRuntimeLeafIdentity `json:"identities"`
}

type InternalPKIRuntimeLeafIdentity struct {
	ID         string   `json:"id"`
	RouteRef   string   `json:"routeRef"`
	ServiceRef string   `json:"serviceRef"`
	ModuleRef  string   `json:"moduleRef"`
	SiteRef    string   `json:"siteRef"`
	NodeRef    string   `json:"nodeRef"`
	SubjectRef string   `json:"subjectRef"`
	DNSSANs    []string `json:"dnsSANs"`
	IPSANs     []string `json:"ipSANs"`
}

type InternalPKIRuntimeAuthority struct {
	ID             string   `json:"id"`
	SiteRef        string   `json:"siteRef"`
	NodeRef        string   `json:"nodeRef"`
	TrustDomainRef string   `json:"trustDomainRef"`
	SubjectRef     string   `json:"subjectRef"`
	KeyAlgorithm   string   `json:"keyAlgorithm"`
	KeyUsage       []string `json:"keyUsage"`
}

type InternalPKIRuntimeTrustTarget struct {
	SiteRef string `json:"siteRef"`
	NodeRef string `json:"nodeRef"`
}

type InternalPKIExecutorArtifact struct {
	StackID            string
	Authority          InternalPKIRuntimeAuthority
	TrustTargets       []InternalPKIRuntimeTrustTarget
	LeafIdentities     []InternalPKIRuntimeLeafIdentity
	ValiditySeconds    int
	RenewBeforeSeconds int
}

func (r internalPKIRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := validateGenerationOnlyPolicyUnit(unit, generationOnlyPolicyUnitSpec{
		moduleID: internalPKIModuleID, unitID: internalPKIUnitID, outputRef: internalPKIOutputRef,
		policyName: "internal PKI executor contract", contract: r.contract,
		placementScope: "node-local", placementCardinality: "one-per-node",
		planInputRefs: internalPKIPlanInputRefs, validatePlanInput: validateInternalPKIPlanInputs,
	})
	if err != nil {
		return nil, err
	}
	var plan internalPKIPlan
	if err := decodeStrict(raw, &plan); err != nil {
		return nil, wrap(ErrInvalidPlan, "renderer.internal-pki.planInputs", "decode exact internal PKI inputs", err)
	}
	if !exactStringList(unit.LogicalNodeRefs(), []string{plan.InternalPKI.Authority.NodeRef}) ||
		!exactStringList(unit.LogicalSiteRefs(), []string{plan.InternalPKI.Authority.SiteRef}) {
		return nil, fail(ErrInvalidPlan, "renderer.internal-pki.placement", "execution must bind only the single internal PKI authority node")
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
	if leaf.Status != "bound" || leaf.SubjectAuthority != "compiler-derived-service" ||
		leaf.SANAuthority != "compiler-derived-route" || leaf.CA ||
		leaf.KeyAlgorithm != "ecdsa-p256" ||
		!exactStringList(leaf.KeyUsage, []string{"digital-signature", "key-agreement"}) ||
		!exactStringList(leaf.ExtendedKeyUsage, []string{"server-auth", "client-auth"}) ||
		!exactStringList(leaf.RequiredObservationFields, []string{
			"certificate-fingerprint", "public-key-fingerprint", "trust-root-fingerprint",
			"serial", "not-before", "not-after", "observed-at",
		}) {
		return nil, fail(ErrInvalidPlan, path+".internalPKI.leafIssuance", "leaf issuance must be compiler-derived and runtime-bound")
	}
	previousIdentity := ""
	for index, identity := range leaf.Identities {
		identityPath := fmt.Sprintf("%s.internalPKI.leafIssuance.identities[%d]", path, index)
		for field, value := range map[string]string{
			"id": identity.ID, "routeRef": identity.RouteRef, "serviceRef": identity.ServiceRef,
			"moduleRef": identity.ModuleRef, "siteRef": identity.SiteRef, "nodeRef": identity.NodeRef,
			"subjectRef": identity.SubjectRef,
		} {
			if err := requireContractID(value, identityPath+"."+field); err != nil {
				return nil, err
			}
		}
		if previousIdentity != "" && identity.ID <= previousIdentity {
			return nil, fail(ErrDuplicate, identityPath+".id", "leaf identities must be unique and sorted")
		}
		if len(identity.DNSSANs) == 0 || !sortedUniqueNonEmpty(identity.DNSSANs) || len(identity.IPSANs) != 0 {
			return nil, fail(ErrInvalidPlan, identityPath, "leaf identity requires exact DNS SANs and no inferred IP SANs")
		}
		for _, dnsSAN := range identity.DNSSANs {
			if !validExecutorBundleHost(dnsSAN) {
				return nil, fail(ErrInvalidPlan, identityPath+".dnsSANs", "leaf DNS SAN is invalid")
			}
		}
		previousIdentity = identity.ID
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
	return []string{authority.SiteRef}, nil
}

// ValidateInternalPKIExecutorArtifact validates the immutable generated policy
// and binds it to the one authenticated authority target. It returns logical
// material requirements only; certificate and private-key bytes are excluded.
func ValidateInternalPKIExecutorArtifact(raw []byte, siteRef, nodeRef string) (InternalPKIExecutorArtifact, error) {
	const path = "internalPKIExecutorArtifact"
	var document struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Module     struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		} `json:"module"`
		Contract struct {
			CAAuthority             string   `json:"caAuthority"`
			CertificateMaterial     string   `json:"certificateMaterial"`
			Credentials             string   `json:"credentials"`
			Execution               string   `json:"execution"`
			Generation              string   `json:"generation"`
			LeafIssuance            string   `json:"leafIssuance"`
			Operations              []string `json:"operations"`
			ProviderLifecycle       string   `json:"providerLifecycle"`
			RuntimeEnforcement      string   `json:"runtimeEnforcement"`
			Scope                   string   `json:"scope"`
			ServerProviderAuthority string   `json:"serverProviderAuthority"`
			TrustDistribution       string   `json:"trustDistribution"`
		} `json:"contract"`
		PlanInputs internalPKIPlan `json:"planInputs"`
	}
	if err := decodeStrict(raw, &document); err != nil {
		return InternalPKIExecutorArtifact{}, wrap(ErrInvalidPlan, path, "decode exact internal PKI executor artifact", err)
	}
	if document.APIVersion != "stackkit.internal-pki-executor-contract/v1" ||
		document.Kind != "InternalPKIExecutorContract" ||
		document.Module.ID != internalPKIModuleID || document.Module.Version != "1.0.0" ||
		document.Contract.CAAuthority != "single-explicit-home-node" ||
		document.Contract.CertificateMaterial != "owner-held" ||
		document.Contract.Credentials != "not-included" ||
		document.Contract.Execution != "typed-local-operations" ||
		document.Contract.Generation != "supported" ||
		document.Contract.LeafIssuance != "compiler-bound" ||
		!exactStringList(document.Contract.Operations, []string{"ensure-root-authority", "issue-compiler-bound-leaves", "distribute-public-trust-root", "verify-internal-pki"}) ||
		document.Contract.ProviderLifecycle != "not-owned" ||
		document.Contract.RuntimeEnforcement != "adapter-verified" ||
		document.Contract.Scope != "home-pki-authority-node" ||
		document.Contract.ServerProviderAuthority != "not-owned" ||
		document.Contract.TrustDistribution != "public-root-only" {
		return InternalPKIExecutorArtifact{}, fail(ErrInvalidPlan, path+".contract", "artifact shell widens the authenticated internal PKI owner")
	}
	planRaw, err := json.Marshal(document.PlanInputs)
	if err != nil {
		return InternalPKIExecutorArtifact{}, wrap(ErrInvalidPlan, path+".planInputs", "marshal internal PKI projection", err)
	}
	if _, err := validateInternalPKIPlanInputs(planRaw, path+".planInputs"); err != nil {
		return InternalPKIExecutorArtifact{}, err
	}
	pki := document.PlanInputs.InternalPKI
	if pki.Authority.SiteRef != siteRef || pki.Authority.NodeRef != nodeRef {
		return InternalPKIExecutorArtifact{}, fail(ErrInvalidPlan, path+".authority", "artifact does not bind the authenticated authority target")
	}
	targets := make([]InternalPKIRuntimeTrustTarget, len(pki.TrustDistribution.Targets))
	for index, target := range pki.TrustDistribution.Targets {
		targets[index] = InternalPKIRuntimeTrustTarget{SiteRef: target.SiteRef, NodeRef: target.NodeRef}
	}
	return InternalPKIExecutorArtifact{
		StackID: document.PlanInputs.StackID,
		Authority: InternalPKIRuntimeAuthority{
			ID: pki.Authority.ID, SiteRef: pki.Authority.SiteRef, NodeRef: pki.Authority.NodeRef,
			TrustDomainRef: pki.Authority.TrustDomainRef, SubjectRef: pki.Authority.SubjectRef,
			KeyAlgorithm: pki.Authority.KeyAlgorithm, KeyUsage: append([]string(nil), pki.Authority.KeyUsage...),
		},
		TrustTargets: targets, LeafIdentities: append([]InternalPKIRuntimeLeafIdentity(nil), pki.LeafIssuance.Identities...),
		ValiditySeconds: pki.Issuer.ValiditySeconds, RenewBeforeSeconds: pki.Issuer.Renewal.RenewBeforeSeconds,
	}, nil
}
