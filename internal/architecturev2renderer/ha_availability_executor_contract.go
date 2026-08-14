package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/productkits"
)

const (
	haAvailabilityUnitID      = "executor-contract"
	haAvailabilityRendererRef = "stackkit"
	haAvailabilityTemplateRef = "builtin://availability/ha/executor-contract/v1.json"
	haAvailabilityVersion     = "1.0.0"
	haAvailabilityToken       = "@@PLAN_INPUTS@@"
)

// HAAvailabilityOutputRef returns the governed output path for one concrete
// kit/mode HA runtime module.
func HAAvailabilityOutputRef(moduleID string) string {
	return "availability/ha/" + moduleID + "/executor-contract.json"
}

const haAvailabilityTemplate = `{"apiVersion":"stackkit.ha-availability-executor-contract/v1","kind":"HAAvailabilityExecutorContract","contract":{"credentials":"external-owner","execution":"typed-local-operations","fencing":"compiler-bound","generation":"supported","operations":["apply-ha-availability","remove-obsolete-ha-availability","verify-ha-availability"],"providerLifecycle":"not-owned","runtimeEnforcement":"adapter-verified","scope":"control-plane-member","serverProviderAuthority":"not-owned","transportImplementation":"external-owner"},"planInputs":@@PLAN_INPUTS@@}`

var haAvailabilityModules = map[string]struct{}{
	"stackkits-ha-basement-warm-runtime": {}, "stackkits-ha-basement-quorum-runtime": {},
	"stackkits-ha-cloud-warm-runtime": {}, "stackkits-ha-cloud-quorum-runtime": {},
	"stackkits-ha-modern-warm-runtime": {}, "stackkits-ha-modern-quorum-runtime": {},
}

var haAvailabilityPlanInputRefs = []string{"availability", "controlPlane", "kit", "moduleCapabilities", "moduleTargets", "sites", "stackId"}

// HAAvailabilityExecutorContractRendererContract is shared only at renderer
// identity level. The generated plan remains bound to one exact catalog
// module, policy and control-plane member.
func HAAvailabilityExecutorContractRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(haAvailabilityTemplate))
	return RendererContract{Kind: "native-config", RendererRef: haAvailabilityRendererRef, TemplateRef: haAvailabilityTemplateRef, Version: haAvailabilityVersion, ContractHash: "sha256:" + hex.EncodeToString(sum[:])}
}

type haAvailabilityRenderer struct{ contract RendererContract }

func newHAAvailabilityRenderer() haAvailabilityRenderer {
	return haAvailabilityRenderer{contract: HAAvailabilityExecutorContractRendererContract()}
}

type haAvailabilityPlan struct {
	StackID            string                     `json:"stackId"`
	Kit                executorBundleKit          `json:"kit"`
	Sites              []executorBundleSite       `json:"sites"`
	ModuleTargets      []executorBundleTarget     `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability `json:"moduleCapabilities"`
	ControlPlane       executorBundleControlPlane `json:"controlPlane"`
	Availability       haAvailabilityProjection   `json:"availability"`
}

type haAvailabilityProjection struct {
	Policy       HAAvailabilityPolicy   `json:"policy"`
	FailureModel HAFailureModel         `json:"failureModel"`
	Members      []HAAvailabilityMember `json:"members"`
}

// HAAvailabilityPolicy is the exact compiler-owned availability decision
// exposed to the provider-free local runtime owner.
type HAAvailabilityPolicy struct {
	Mode                string `json:"mode"`
	PolicyRef           string `json:"policyRef"`
	RealizationRef      string `json:"realizationRef"`
	ModuleRef           string `json:"moduleRef"`
	Selector            string `json:"selector"`
	RPOSeconds          int    `json:"rpoSeconds"`
	RTOSeconds          int    `json:"rtoSeconds"`
	FailureDomainSpread int    `json:"failureDomainSpread"`
	Fencing             string `json:"fencing"`
}

// HAFailureModel closes the member scope and partition behavior which the
// runtime owner must prove during readback.
type HAFailureModel struct {
	Basis             string `json:"basis"`
	MemberSiteScope   string `json:"memberSiteScope"`
	PartitionBehavior string `json:"partitionBehavior"`
}

// HAAvailabilityMember binds one control-plane node to its Site and failure
// domain without carrying provider, credential, endpoint, or transport data.
type HAAvailabilityMember struct {
	NodeRef       string `json:"nodeRef"`
	SiteRef       string `json:"siteRef"`
	FailureDomain string `json:"failureDomain"`
}

// HAAvailabilityExecutorArtifact is the material-free policy handed to the
// service-owned HA operations adapter for one exact member.
type HAAvailabilityExecutorArtifact struct {
	StackID      string
	KitSlug      string
	ModuleID     string
	Policy       HAAvailabilityPolicy
	FailureModel HAFailureModel
	Members      []HAAvailabilityMember
}

func (r haAvailabilityRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := "resolvedPlan.modules." + unit.ModuleID() + ".renderUnits." + haAvailabilityUnitID
	if _, ok := haAvailabilityModules[unit.ModuleID()]; !ok || unit.ID() != haAvailabilityUnitID ||
		unit.Kind() != r.contract.Kind || unit.RendererRef() != r.contract.RendererRef || unit.TemplateRef() != r.contract.TemplateRef ||
		unit.Version() != r.contract.Version || unit.ContractHash() != r.contract.ContractHash || unit.RuntimeKind() != "host" || unit.RuntimeDelivery() != "stackkit" ||
		!exactStringList(unit.PlanInputRefs(), haAvailabilityPlanInputRefs) || !emptyJSONObject(unit.ValuesJSON()) || !emptyJSONObject(unit.SecretRefsJSON()) || !emptyJSONArray(unit.InputBindingsJSON()) ||
		unit.InstanceScope() != "node-local" || !exactStringList(unit.DeclaredOutputs(), []string{HAAvailabilityOutputRef(unit.ModuleID())}) {
		return nil, fail(ErrInvalidPlan, path, "requires the exact HA member executor contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if !hasSite || !hasNode || unit.InstanceID() != haAvailabilityUnitID+"-node-"+nodeRef || !slices.Contains(unit.LogicalSiteRefs(), siteRef) || !slices.Contains(unit.LogicalNodeRefs(), nodeRef) {
		return nil, fail(ErrInvalidPlan, path+".instances", "requires one exact control-plane member instance")
	}
	var plan haAvailabilityPlan
	if err := decodeStrict(unit.PlanInputsJSON(), &plan); err != nil {
		return nil, wrap(ErrInvalidPlan, path+".planInputs", "decode HA availability projection", err)
	}
	if err := validateHAAvailabilityPlan(plan, unit.ModuleID(), siteRef, nodeRef, path+".planInputs"); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(plan)
	if err != nil {
		return nil, wrap(ErrRendererFailure, path+".planInputs", "marshal HA availability projection", err)
	}
	if err := rejectExecutorContractProjectionLeaks(canonical, path+".planInputs"); err != nil {
		return nil, err
	}
	if bytes.Count([]byte(haAvailabilityTemplate), []byte(haAvailabilityToken)) != 1 {
		return nil, fail(ErrOutputChanged, path+".template", "HA template token drifted")
	}
	return []UnitOutput{{Ref: HAAvailabilityOutputRef(unit.ModuleID()), Bytes: bytes.Replace([]byte(haAvailabilityTemplate), []byte(haAvailabilityToken), canonical, 1)}}, nil
}

func validateHAAvailabilityPlan(plan haAvailabilityPlan, moduleID, siteRef, nodeRef, path string) error {
	if err := requireContractID(plan.StackID, path+".stackId"); err != nil {
		return err
	}
	if !productkits.IsActive(plan.Kit.Slug) || strings.TrimSpace(plan.Kit.Version) == "" || !validSHA256(plan.Kit.DefinitionHash) {
		return fail(ErrInvalidPlan, path+".kit", "requires exact governed Kit identity")
	}
	p := plan.Availability.Policy
	if p.ModuleRef != moduleID || p.Selector != "control-plane-members" || !containsExecutorBundleString([]string{"warm-standby", "quorum"}, p.Mode) ||
		strings.TrimSpace(p.PolicyRef) == "" || strings.TrimSpace(p.RealizationRef) == "" || p.RPOSeconds < 0 || p.RTOSeconds <= 0 || p.FailureDomainSpread < 2 ||
		!containsExecutorBundleString([]string{"manual", "automatic"}, p.Fencing) {
		return fail(ErrInvalidPlan, path+".availability.policy", "does not bind an exact HA policy")
	}
	if p.Mode == "quorum" && p.Fencing != "automatic" {
		return fail(ErrInvalidPlan, path+".availability.policy.fencing", "quorum must remain automatically fenced")
	}
	if plan.ControlPlane.Mode != p.Mode || len(plan.Availability.Members) < 2 || len(plan.ModuleTargets) != len(plan.Availability.Members) {
		return fail(ErrInvalidPlan, path, "HA policy must exactly match the selected control-plane members")
	}
	found := 0
	targets := make(map[string]string, len(plan.ModuleTargets))
	for _, target := range plan.ModuleTargets {
		targets[target.ID] = target.SiteRef
	}
	seenNodes, seenDomains := map[string]struct{}{}, map[string]struct{}{}
	for index, member := range plan.Availability.Members {
		memberPath := fmt.Sprintf("%s.availability.members[%d]", path, index)
		if err := requireContractID(member.NodeRef, memberPath+".nodeRef"); err != nil {
			return err
		}
		if err := requireContractID(member.SiteRef, memberPath+".siteRef"); err != nil {
			return err
		}
		if err := requireContractID(member.FailureDomain, memberPath+".failureDomain"); err != nil {
			return err
		}
		if _, duplicate := seenNodes[member.NodeRef]; duplicate || targets[member.NodeRef] != member.SiteRef {
			return fail(ErrInvalidPlan, memberPath, "member is not an exact module target")
		}
		seenNodes[member.NodeRef] = struct{}{}
		seenDomains[member.FailureDomain] = struct{}{}
		if member.SiteRef == siteRef && member.NodeRef == nodeRef {
			found++
		}
	}
	if found != 1 || len(seenDomains) < p.FailureDomainSpread {
		return fail(ErrInvalidPlan, path+".availability.members", "member instance or required failure-domain spread is not exact")
	}
	if moduleID == "stackkits-ha-modern-warm-runtime" || moduleID == "stackkits-ha-modern-quorum-runtime" {
		if plan.ControlPlane.AuthoritySiteRef != siteRef || plan.Availability.FailureModel.MemberSiteScope != "authority-site-control-members" || !strings.Contains(plan.Availability.FailureModel.PartitionBehavior, "home-authority") {
			return fail(ErrInvalidPlan, path+".availability", "Modern HA must remain at the Home control authority and fail closed across the WAN")
		}
	}
	return nil
}

// ValidateHAAvailabilityExecutorArtifact validates one generated policy before
// the local owner hands it to any operations implementation.
func ValidateHAAvailabilityExecutorArtifact(raw []byte, moduleID, siteRef, nodeRef string) (HAAvailabilityExecutorArtifact, error) {
	var document struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Contract   struct {
			Credentials             string   `json:"credentials"`
			Execution               string   `json:"execution"`
			Fencing                 string   `json:"fencing"`
			Generation              string   `json:"generation"`
			Operations              []string `json:"operations"`
			ProviderLifecycle       string   `json:"providerLifecycle"`
			RuntimeEnforcement      string   `json:"runtimeEnforcement"`
			Scope                   string   `json:"scope"`
			ServerProviderAuthority string   `json:"serverProviderAuthority"`
			TransportImplementation string   `json:"transportImplementation"`
		} `json:"contract"`
		PlanInputs haAvailabilityPlan `json:"planInputs"`
	}
	if err := decodeStrict(raw, &document); err != nil {
		return HAAvailabilityExecutorArtifact{}, wrap(ErrInvalidPlan, "haAvailabilityArtifact", "decode exact HA artifact", err)
	}
	if document.APIVersion != "stackkit.ha-availability-executor-contract/v1" || document.Kind != "HAAvailabilityExecutorContract" {
		return HAAvailabilityExecutorArtifact{}, fail(ErrInvalidPlan, "haAvailabilityArtifact", "artifact is not an HA availability contract")
	}
	if document.Contract.Credentials != "external-owner" ||
		document.Contract.Execution != "typed-local-operations" ||
		document.Contract.Fencing != "compiler-bound" ||
		document.Contract.Generation != "supported" ||
		!slices.Equal(document.Contract.Operations, []string{"apply-ha-availability", "remove-obsolete-ha-availability", "verify-ha-availability"}) ||
		document.Contract.ProviderLifecycle != "not-owned" ||
		document.Contract.RuntimeEnforcement != "adapter-verified" ||
		document.Contract.Scope != "control-plane-member" ||
		document.Contract.ServerProviderAuthority != "not-owned" ||
		document.Contract.TransportImplementation != "external-owner" {
		return HAAvailabilityExecutorArtifact{}, fail(ErrInvalidPlan, "haAvailabilityArtifact.contract", "artifact does not carry the exact provider-free HA execution boundary")
	}
	if err := validateHAAvailabilityPlan(document.PlanInputs, moduleID, siteRef, nodeRef, "haAvailabilityArtifact.planInputs"); err != nil {
		return HAAvailabilityExecutorArtifact{}, err
	}
	return HAAvailabilityExecutorArtifact{StackID: document.PlanInputs.StackID, KitSlug: document.PlanInputs.Kit.Slug, ModuleID: moduleID, Policy: document.PlanInputs.Availability.Policy, FailureModel: document.PlanInputs.Availability.FailureModel, Members: append([]HAAvailabilityMember(nil), document.PlanInputs.Availability.Members...)}, nil
}
