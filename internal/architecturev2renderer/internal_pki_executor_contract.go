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

const internalPKITemplate = `{"apiVersion":"stackkit.internal-pki-executor-contract/v1","kind":"InternalPKIExecutorContract","module":{"id":"stackkits-internal-pki-contract","version":"1.0.0"},"contract":{"certificateMaterial":"owner-held","credentials":"not-included","execution":"unverified","generation":"supported","providerLifecycle":"not-owned","runtimeEnforcement":"unverified","scope":"home-generation-handoff","serverProviderAuthority":"not-owned"},"planInputs":@@PLAN_INPUTS@@}
`

var internalPKIPlanInputRefs = []string{"internalPKI", "kit", "moduleTargets", "stackId"}

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
	StackID       string                 `json:"stackId"`
	Kit           publicTLSExecutorKit   `json:"kit"`
	ModuleTargets []executorBundleTarget `json:"moduleTargets"`
	InternalPKI   internalPKIProjection  `json:"internalPKI"`
}

type internalPKIProjection struct {
	CapabilityRef string                   `json:"capabilityRef"`
	ProviderRef   string                   `json:"providerRef"`
	Profile       publicTLSExecutorProfile `json:"profile"`
	Issuer        publicTLSExecutorIssuer  `json:"issuer"`
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
	nodeRefs := make([]string, len(plan.ModuleTargets))
	for index := range plan.ModuleTargets {
		nodeRefs[index] = plan.ModuleTargets[index].ID
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
	if len(plan.ModuleTargets) == 0 {
		return nil, fail(ErrInvalidPlan, path+".moduleTargets", "internal PKI requires governed Home targets")
	}
	nodeRefs := make([]string, len(plan.ModuleTargets))
	siteSet := map[string]struct{}{}
	previousNodeRef := ""
	for index, target := range plan.ModuleTargets {
		targetPath := fmt.Sprintf("%s.moduleTargets[%d]", path, index)
		if err := requireContractID(target.ID, targetPath+".id"); err != nil {
			return nil, err
		}
		if err := requireContractID(target.SiteRef, targetPath+".siteRef"); err != nil {
			return nil, err
		}
		if strings.TrimSpace(target.FailureDomain) == "" || !sortedUniqueNonEmpty(target.Roles) ||
			target.DeclaredHardware.Arch != "amd64" && target.DeclaredHardware.Arch != "arm64" ||
			target.DeclaredHardware.Profile == "" || target.DeclaredHardware.CPUCores < 0 ||
			target.DeclaredHardware.RAMGB < 0 || target.DeclaredHardware.StorageGB < 0 {
			return nil, fail(ErrInvalidPlan, targetPath, "internal PKI target is incomplete")
		}
		if previousNodeRef != "" && target.ID <= previousNodeRef {
			return nil, fail(ErrDuplicate, targetPath+".id", "targets must be unique and sorted")
		}
		previousNodeRef = target.ID
		nodeRefs[index] = target.ID
		siteSet[target.SiteRef] = struct{}{}
	}
	pki := plan.InternalPKI
	if pki.CapabilityRef != "internal-pki" || pki.ProviderRef != "stackkits-internal-pki" {
		return nil, fail(ErrInvalidPlan, path+".internalPKI", "projection must bind the exact internal PKI adapter")
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
		{ID: "certificate", Purpose: "certificate-chain", Sensitivity: "public"},
		{ID: "private-key", Purpose: "private-key", Sensitivity: "secret"},
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
