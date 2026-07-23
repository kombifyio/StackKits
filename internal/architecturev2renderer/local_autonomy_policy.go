package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

const (
	localAutonomyPolicyModuleID    = "stackkits-local-autonomy-policy-manifest"
	localAutonomyPolicyUnitID      = "policy-bundle"
	localAutonomyPolicyRendererRef = "stackkit"
	localAutonomyPolicyTemplateRef = "builtin://home/local-autonomy/v1.json"
	localAutonomyPolicyVersion     = "1.0.0"
	localAutonomyPolicyOutputRef   = "local/autonomy/policy.json"
	localAutonomyPolicyToken       = "@@PLAN_INPUTS@@"
)

const localAutonomyPolicyTemplate = `{"apiVersion":"stackkit.local-autonomy-policy/v1","kind":"LocalAutonomyPolicy","contract":{"airGappedInstallation":"not-included","capability":"offline-autonomy","runtimeEnforcement":"unverified","scope":"generation-only"},"planInputs":@@PLAN_INPUTS@@}
`

var localAutonomyPolicyPlanInputRefs = []string{
	"controlPlane", "data", "failurePolicy", "identity", "kit", "sites", "stackId",
}

type localAutonomyKitPlanValidator func(localAutonomyPlanInputs, []byte, string) ([]string, error)

var localAutonomyKitPlanValidators = map[string]localAutonomyKitPlanValidator{}

func registerLocalAutonomyKitPlanValidator(kitSlug string, validator localAutonomyKitPlanValidator) {
	if kitSlug == "" || validator == nil {
		panic("architecturev2renderer: local-autonomy kit validator requires a slug and implementation")
	}
	if _, exists := localAutonomyKitPlanValidators[kitSlug]; exists {
		panic("architecturev2renderer: duplicate local-autonomy kit validator for " + kitSlug)
	}
	localAutonomyKitPlanValidators[kitSlug] = validator
}

// LocalAutonomyPolicyRendererContract returns the exact built-in identity for
// the generation-only Home-site policy manifest. The hash binds the immutable
// JSON shell, including its explicit non-claim for runtime and air-gap
// enforcement; the exact compiler-owned plan projection is inserted later.
func LocalAutonomyPolicyRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(localAutonomyPolicyTemplate))
	return RendererContract{
		Kind: "native-config", RendererRef: localAutonomyPolicyRendererRef,
		TemplateRef: localAutonomyPolicyTemplateRef, Version: localAutonomyPolicyVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type localAutonomyPolicyRenderer struct {
	template []byte
	contract RendererContract
}

func newLocalAutonomyPolicyRenderer() localAutonomyPolicyRenderer {
	return localAutonomyPolicyRenderer{
		template: []byte(localAutonomyPolicyTemplate),
		contract: LocalAutonomyPolicyRendererContract(),
	}
}

//nolint:dupl // Policy renderers intentionally share the same small immutable-template lowering sequence.
func (r localAutonomyPolicyRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	planInputs, err := validateLocalAutonomyPolicyUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	if localAutonomyTemplateHash(r.template) != r.contract.ContractHash || bytes.Count(r.template, []byte(localAutonomyPolicyToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.local-autonomy-policy.template", "embedded policy manifest does not match its registered contract")
	}
	output := bytes.Replace(r.template, []byte(localAutonomyPolicyToken), planInputs, 1)
	return []UnitOutput{{Ref: localAutonomyPolicyOutputRef, Bytes: output}}, nil
}

func localAutonomyTemplateHash(template []byte) string {
	sum := sha256.Sum256(template)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type localAutonomyPlanInputs struct {
	StackID       string                     `json:"stackId"`
	Kit           localAutonomyKit           `json:"kit"`
	Sites         []localAutonomySite        `json:"sites"`
	ControlPlane  localAutonomyControlPlane  `json:"controlPlane"`
	Identity      localAutonomyIdentity      `json:"identity"`
	Data          localAutonomyData          `json:"data"`
	FailurePolicy localAutonomyFailurePolicy `json:"failurePolicy"`
}

type localAutonomyKit struct {
	Slug           string `json:"slug"`
	Version        string `json:"version"`
	DefinitionHash string `json:"definitionHash"`
}

type localAutonomySite struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	FailureDomain string `json:"failureDomain"`
}

type localAutonomyControlPlane struct {
	Mode             string   `json:"mode"`
	AuthoritySiteRef string   `json:"authoritySiteRef"`
	Members          []string `json:"members"`
}

type localAutonomyIdentity struct {
	HumanAuthoritySiteRef   string                        `json:"humanAuthoritySiteRef"`
	DeviceAuthoritySiteRef  string                        `json:"deviceAuthoritySiteRef"`
	EdgeVerifierSiteRefs    []string                      `json:"edgeVerifierSiteRefs,omitempty"`
	DeviceEnrollment        localAutonomyDeviceEnrollment `json:"deviceEnrollment"`
	PossessionBoundSessions bool                          `json:"possessionBoundSessions"`
	LANLocationIsIdentity   bool                          `json:"lanLocationIsIdentity"`
}

type localAutonomyDeviceEnrollment struct {
	Mode                      string `json:"mode"`
	AuthoritySiteRef          string `json:"authoritySiteRef"`
	EndpointExposure          string `json:"endpointExposure"`
	RemoteEnrollment          bool   `json:"remoteEnrollment"`
	RequireOwnerStepUp        bool   `json:"requireOwnerStepUp"`
	RequireLocalPairingProof  bool   `json:"requireLocalPairingProof"`
	RequireDeviceGeneratedKey bool   `json:"requireDeviceGeneratedKey"`
	RequirePossessionProof    bool   `json:"requirePossessionProof"`
	HardwareBackedKey         string `json:"hardwareBackedKey"`
	RevocationSupported       bool   `json:"revocationSupported"`
	CredentialTTLSeconds      int    `json:"credentialTTLSeconds"`
}

type localAutonomyData struct {
	DefaultAuthority string                              `json:"defaultAuthority"`
	Bindings         map[string]localAutonomyDataBinding `json:"bindings,omitempty"`
}

type localAutonomyDataBinding struct {
	Classes          []string                      `json:"classes"`
	PrimarySiteRef   string                        `json:"primarySiteRef"`
	ReplicaSiteRefs  []string                      `json:"replicaSiteRefs"`
	CloudCopyAllowed bool                          `json:"cloudCopyAllowed"`
	CloudCopyPolicy  *localAutonomyCloudCopyPolicy `json:"cloudCopyPolicy,omitempty"`
}

type localAutonomyCloudCopyPolicy struct {
	PolicyRef      string   `json:"policyRef"`
	AllowedClasses []string `json:"allowedClasses"`
	AllowPrimary   bool     `json:"allowPrimary"`
	AllowReplicas  bool     `json:"allowReplicas"`
}

type localAutonomyFailurePolicy struct {
	OnCloudLoss                     string `json:"onCloudLoss"`
	OnLinkLoss                      string `json:"onLinkLoss"`
	CloudEdge                       string `json:"cloudEdge"`
	LocalIdentityAuthorityAvailable bool   `json:"localIdentityAuthorityAvailable"`
	MaxStaleVerificationSeconds     int    `json:"maxStaleVerificationSeconds"`
	DenyNewCrossSiteSessions        bool   `json:"denyNewCrossSiteSessions"`
}

// LocalAutonomyEnforcementPolicy is the closed policy projection available to
// the distinct runtime enforcer. It retains Home/Cloud failure semantics and
// data placement authority without exposing endpoints, credentials, network
// configuration, provider lifecycle, or general LAN reachability.
type LocalAutonomyEnforcementPolicy struct {
	StackID                         string
	KitSlug                         string
	HomeSiteRefs                    []string
	CloudSiteRefs                   []string
	ControlMode                     string
	AuthoritySiteRef                string
	ControlMembers                  []string
	HumanAuthoritySiteRef           string
	DeviceAuthoritySiteRef          string
	EdgeVerifierSiteRefs            []string
	DataDefaultAuthority            string
	DataBindings                    []LocalAutonomyEnforcementDataBinding
	OnCloudLoss                     string
	OnLinkLoss                      string
	CloudEdge                       string
	LocalIdentityAuthorityAvailable bool
	MaxStaleVerificationSeconds     int
	DenyNewCrossSiteSessions        bool
}

type LocalAutonomyEnforcementDataBinding struct {
	Ref                string
	Classes            []string
	PrimarySiteRef     string
	ReplicaSiteRefs    []string
	CloudCopyAllowed   bool
	CloudCopyPolicyRef string
	AllowedClasses     []string
	AllowPrimary       bool
	AllowReplicas      bool
}

type localAutonomyPolicyArtifact struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Contract   struct {
		AirGappedInstallation string `json:"airGappedInstallation"`
		Capability            string `json:"capability"`
		RuntimeEnforcement    string `json:"runtimeEnforcement"`
		Scope                 string `json:"scope"`
	} `json:"contract"`
	PlanInputs json.RawMessage `json:"planInputs"`
}

// ValidateLocalAutonomyPolicyArtifact validates the exact generation-only
// artifact before returning a defensive runtime projection. The unverified
// marker is required because these bytes are policy input, never execution
// evidence by themselves.
func ValidateLocalAutonomyPolicyArtifact(raw []byte) (LocalAutonomyEnforcementPolicy, error) {
	var document localAutonomyPolicyArtifact
	if err := decodeStrict(raw, &document); err != nil {
		return LocalAutonomyEnforcementPolicy{}, wrap(ErrInvalidPlan, "localAutonomyPolicy", "decode exact local-autonomy policy artifact", err)
	}
	if document.APIVersion != "stackkit.local-autonomy-policy/v1" || document.Kind != "LocalAutonomyPolicy" ||
		document.Contract.AirGappedInstallation != "not-included" || document.Contract.Capability != "offline-autonomy" ||
		document.Contract.RuntimeEnforcement != "unverified" || document.Contract.Scope != "generation-only" {
		return LocalAutonomyEnforcementPolicy{}, fail(ErrInvalidPlan, "localAutonomyPolicy.contract", "artifact widens or fabricates the generation-only local-autonomy contract")
	}
	var inputs localAutonomyPlanInputs
	if err := decodeStrict(document.PlanInputs, &inputs); err != nil {
		return LocalAutonomyEnforcementPolicy{}, wrap(ErrInvalidPlan, "localAutonomyPolicy.planInputs", "decode exact local-autonomy policy inputs", err)
	}
	homeSiteRefs, err := validateLocalAutonomyPlanInputs(document.PlanInputs, "localAutonomyPolicy.planInputs")
	if err != nil {
		return LocalAutonomyEnforcementPolicy{}, err
	}
	cloudSiteRefs := make([]string, 0, len(inputs.Sites))
	for _, site := range inputs.Sites {
		if site.Kind == "cloud" {
			cloudSiteRefs = append(cloudSiteRefs, site.ID)
		}
	}
	sort.Strings(cloudSiteRefs)
	policy := LocalAutonomyEnforcementPolicy{
		StackID: inputs.StackID, KitSlug: inputs.Kit.Slug,
		HomeSiteRefs: append([]string(nil), homeSiteRefs...), CloudSiteRefs: cloudSiteRefs,
		ControlMode: inputs.ControlPlane.Mode, AuthoritySiteRef: inputs.ControlPlane.AuthoritySiteRef,
		ControlMembers:        append([]string(nil), inputs.ControlPlane.Members...),
		HumanAuthoritySiteRef: inputs.Identity.HumanAuthoritySiteRef, DeviceAuthoritySiteRef: inputs.Identity.DeviceAuthoritySiteRef,
		EdgeVerifierSiteRefs: append([]string(nil), inputs.Identity.EdgeVerifierSiteRefs...), DataDefaultAuthority: inputs.Data.DefaultAuthority,
		OnCloudLoss: inputs.FailurePolicy.OnCloudLoss, OnLinkLoss: inputs.FailurePolicy.OnLinkLoss, CloudEdge: inputs.FailurePolicy.CloudEdge,
		LocalIdentityAuthorityAvailable: inputs.FailurePolicy.LocalIdentityAuthorityAvailable,
		MaxStaleVerificationSeconds:     inputs.FailurePolicy.MaxStaleVerificationSeconds,
		DenyNewCrossSiteSessions:        inputs.FailurePolicy.DenyNewCrossSiteSessions,
	}
	bindingRefs := make([]string, 0, len(inputs.Data.Bindings))
	for ref := range inputs.Data.Bindings {
		bindingRefs = append(bindingRefs, ref)
	}
	sort.Strings(bindingRefs)
	policy.DataBindings = make([]LocalAutonomyEnforcementDataBinding, 0, len(bindingRefs))
	for _, ref := range bindingRefs {
		binding := inputs.Data.Bindings[ref]
		projected := LocalAutonomyEnforcementDataBinding{
			Ref: ref, Classes: append([]string(nil), binding.Classes...), PrimarySiteRef: binding.PrimarySiteRef,
			ReplicaSiteRefs: append([]string(nil), binding.ReplicaSiteRefs...), CloudCopyAllowed: binding.CloudCopyAllowed,
		}
		if binding.CloudCopyPolicy != nil {
			projected.CloudCopyPolicyRef = binding.CloudCopyPolicy.PolicyRef
			projected.AllowedClasses = append([]string(nil), binding.CloudCopyPolicy.AllowedClasses...)
			projected.AllowPrimary = binding.CloudCopyPolicy.AllowPrimary
			projected.AllowReplicas = binding.CloudCopyPolicy.AllowReplicas
		}
		policy.DataBindings = append(policy.DataBindings, projected)
	}
	return policy, nil
}

func validateLocalAutonomyPolicyUnit(unit RenderUnit, contract RendererContract) ([]byte, error) {
	return validateGenerationOnlyPolicyUnit(unit, generationOnlyPolicyUnitSpec{
		moduleID: localAutonomyPolicyModuleID, unitID: localAutonomyPolicyUnitID,
		outputRef: localAutonomyPolicyOutputRef, policyName: "local-autonomy policy",
		placementScope: "node-local", placementCardinality: "one-per-node",
		contract: contract, planInputRefs: localAutonomyPolicyPlanInputRefs,
		validatePlanInput: validateLocalAutonomyPlanInputs,
	})
}

func validateLocalAutonomyPlanInputs(raw []byte, path string) ([]string, error) {
	var inputs localAutonomyPlanInputs
	if err := decodeStrict(raw, &inputs); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact local-autonomy plan inputs", err)
	}
	if err := requireContractID(inputs.StackID, path+".stackId"); err != nil {
		return nil, err
	}
	if inputs.Kit.Version == "" || !validSHA256(inputs.Kit.DefinitionHash) {
		return nil, fail(ErrInvalidPlan, path+".kit", "local-autonomy policy requires an exact governed kit identity")
	}
	if inputs.Kit.Slug == "basement-kit" {
		return validateBasementLocalAutonomyPlanInputs(inputs, raw, path)
	}
	if validator, exists := localAutonomyKitPlanValidators[inputs.Kit.Slug]; exists {
		return validator(inputs, raw, path)
	}
	return nil, fail(ErrInvalidPlan, path+".kit.slug", "kit has no registered local-autonomy policy validator")
}

//nolint:gocyclo // Basement identity, data, and failure authority are one fail-closed product boundary.
func validateBasementLocalAutonomyPlanInputs(inputs localAutonomyPlanInputs, raw []byte, path string) ([]string, error) {
	siteKinds, homeSiteRefs, cloudSiteRefs, err := validateLocalAutonomySites(inputs.Sites, path)
	if err != nil {
		return nil, err
	}
	if len(inputs.Sites) != 1 || len(homeSiteRefs) != 1 || len(cloudSiteRefs) != 0 {
		return nil, fail(ErrInvalidPlan, path+".sites", "Basement local autonomy requires exactly one Home Site")
	}
	authoritySiteRef, err := validateLocalAutonomyControlPlane(inputs.ControlPlane, siteKinds, path)
	if err != nil {
		return nil, err
	}
	if err := validateLocalAutonomyIdentity(inputs.Identity, authoritySiteRef, path+".identity"); err != nil {
		return nil, err
	}
	if len(inputs.Identity.EdgeVerifierSiteRefs) != 0 {
		return nil, fail(ErrInvalidPlan, path+".identity.edgeVerifierSiteRefs", "Basement has no Cloud edge verifiers")
	}
	if err := validateLocalAutonomyData(inputs.Data, siteKinds, authoritySiteRef, false, path+".data"); err != nil {
		return nil, err
	}
	failure := inputs.FailurePolicy
	if failure.OnCloudLoss != "not-applicable" || failure.OnLinkLoss != "local-continues" || failure.CloudEdge != "not-applicable" ||
		failure.MaxStaleVerificationSeconds != 0 || !failure.LocalIdentityAuthorityAvailable || !failure.DenyNewCrossSiteSessions {
		return nil, fail(ErrInvalidPlan, path+".failurePolicy", "Basement must continue locally without stale or cross-site authority")
	}
	if err := rejectLocalAutonomyProjectionLeaks(raw, path); err != nil {
		return nil, err
	}
	return homeSiteRefs, nil
}

func validateLocalAutonomySites(sites []localAutonomySite, path string) (map[string]string, []string, []string, error) {
	if len(sites) == 0 {
		return nil, nil, nil, fail(ErrInvalidPlan, path+".sites", "local autonomy requires at least one explicit Home Site")
	}
	siteKinds := make(map[string]string, len(sites))
	homeSiteRefs := make([]string, 0, len(sites))
	cloudSiteRefs := make([]string, 0, len(sites))
	for index, site := range sites {
		if err := requireContractID(site.ID, fmt.Sprintf("%s.sites[%d].id", path, index)); err != nil {
			return nil, nil, nil, err
		}
		if site.FailureDomain == "" || (site.Kind != "home" && site.Kind != "cloud") {
			return nil, nil, nil, fail(ErrInvalidPlan, fmt.Sprintf("%s.sites[%d]", path, index), "site projection must contain only id, home-or-cloud kind, and failureDomain")
		}
		if _, duplicate := siteKinds[site.ID]; duplicate {
			return nil, nil, nil, fail(ErrDuplicate, fmt.Sprintf("%s.sites[%d].id", path, index), "duplicate site %q", site.ID)
		}
		siteKinds[site.ID] = site.Kind
		if site.Kind == "home" {
			homeSiteRefs = append(homeSiteRefs, site.ID)
		} else {
			cloudSiteRefs = append(cloudSiteRefs, site.ID)
		}
	}
	sort.Strings(homeSiteRefs)
	sort.Strings(cloudSiteRefs)
	return siteKinds, homeSiteRefs, cloudSiteRefs, nil
}

func validateLocalAutonomyControlPlane(control localAutonomyControlPlane, siteKinds map[string]string, path string) (string, error) {
	authoritySiteRef := control.AuthoritySiteRef
	if siteKinds[authoritySiteRef] != "home" || len(control.Members) == 0 ||
		(control.Mode != "single" && control.Mode != "warm-standby" && control.Mode != "quorum") {
		return "", fail(ErrInvalidPlan, path+".controlPlane", "control authority and members must remain Home-authoritative")
	}
	seenMembers := make(map[string]struct{}, len(control.Members))
	for index, member := range control.Members {
		if err := requireContractID(member, fmt.Sprintf("%s.controlPlane.members[%d]", path, index)); err != nil {
			return "", err
		}
		if _, duplicate := seenMembers[member]; duplicate {
			return "", fail(ErrDuplicate, fmt.Sprintf("%s.controlPlane.members[%d]", path, index), "duplicate control member %q", member)
		}
		seenMembers[member] = struct{}{}
	}
	if control.Mode == "single" && len(control.Members) != 1 || control.Mode == "warm-standby" && len(control.Members) < 2 ||
		control.Mode == "quorum" && len(control.Members) != 3 && len(control.Members) != 5 && len(control.Members) != 7 {
		return "", fail(ErrInvalidPlan, path+".controlPlane.members", "member cardinality does not match the control-plane mode")
	}
	return authoritySiteRef, nil
}

func validateLocalAutonomyIdentity(identity localAutonomyIdentity, authoritySiteRef, path string) error {
	enrollment := identity.DeviceEnrollment
	if identity.HumanAuthoritySiteRef != authoritySiteRef || identity.DeviceAuthoritySiteRef != authoritySiteRef ||
		!identity.PossessionBoundSessions || identity.LANLocationIsIdentity || enrollment.Mode != "local-only" || enrollment.AuthoritySiteRef != authoritySiteRef ||
		enrollment.EndpointExposure != "lan" || enrollment.RemoteEnrollment || !enrollment.RequireOwnerStepUp || !enrollment.RequireLocalPairingProof ||
		!enrollment.RequireDeviceGeneratedKey || !enrollment.RequirePossessionProof || !enrollment.RevocationSupported {
		return fail(ErrInvalidPlan, path, "identity must remain Home-authoritative, locally enrolled, device-bound, and possession-proven")
	}
	if _, err := uniqueLocalAutonomyIDSet(identity.EdgeVerifierSiteRefs, path+".edgeVerifierSiteRefs"); err != nil {
		return err
	}
	if (enrollment.HardwareBackedKey != "preferred" && enrollment.HardwareBackedKey != "required") || enrollment.CredentialTTLSeconds < 300 || enrollment.CredentialTTLSeconds > 86400 {
		return fail(ErrInvalidPlan, path+".deviceEnrollment", "device credentials require bounded lifetime and preferred-or-required hardware-backed keys")
	}
	return nil
}

//nolint:gocyclo // Data classes, placement, replicas, and optional Cloud-copy policy form one authority boundary.
func validateLocalAutonomyData(data localAutonomyData, siteKinds map[string]string, authoritySiteRef string, allowCloudCopies bool, path string) error {
	if data.DefaultAuthority != authoritySiteRef {
		return fail(ErrInvalidPlan, path+".defaultAuthority", "default data authority must remain on the control-authority Home Site")
	}
	for bindingRef, binding := range data.Bindings {
		bindingPath := path + ".bindings." + bindingRef
		if err := requireContractID(bindingRef, path+".bindings"); err != nil {
			return err
		}
		if err := validateLocalAutonomyDataClasses(binding.Classes, bindingPath+".classes"); err != nil {
			return err
		}
		if siteKinds[binding.PrimarySiteRef] == "" {
			return fail(ErrInvalidPlan, bindingPath, "data bindings require a projected primary Site")
		}
		replicaSet, err := uniqueLocalAutonomyIDSet(binding.ReplicaSiteRefs, bindingPath+".replicaSiteRefs")
		if err != nil {
			return err
		}
		for replicaSiteRef := range replicaSet {
			if siteKinds[replicaSiteRef] == "" || replicaSiteRef == binding.PrimarySiteRef {
				return fail(ErrInvalidPlan, bindingPath+".replicaSiteRefs", "replicas must be distinct projected Sites")
			}
		}
		if !allowCloudCopies {
			if binding.PrimarySiteRef != authoritySiteRef || binding.CloudCopyAllowed || binding.CloudCopyPolicy != nil || anyLocalAutonomyCloudSite(replicaSet, siteKinds) {
				return fail(ErrInvalidPlan, bindingPath, "data must remain Home-primary without Cloud copies")
			}
			continue
		}
		if !binding.CloudCopyAllowed {
			if binding.CloudCopyPolicy != nil || siteKinds[binding.PrimarySiteRef] == "cloud" || anyLocalAutonomyCloudSite(replicaSet, siteKinds) {
				return fail(ErrInvalidPlan, bindingPath, "Cloud placement requires an explicit cloud-copy policy")
			}
			continue
		}
		policy := binding.CloudCopyPolicy
		if policy == nil {
			return fail(ErrInvalidPlan, bindingPath+".cloudCopyPolicy", "Cloud-copy opt-in requires an explicit policy")
		}
		if err := requireContractID(policy.PolicyRef, bindingPath+".cloudCopyPolicy.policyRef"); err != nil {
			return err
		}
		if err := validateLocalAutonomyDataClasses(policy.AllowedClasses, bindingPath+".cloudCopyPolicy.allowedClasses"); err != nil {
			return err
		}
		for _, dataClass := range binding.Classes {
			if !stringListContains(policy.AllowedClasses, dataClass) {
				return fail(ErrInvalidPlan, bindingPath+".cloudCopyPolicy.allowedClasses", "policy does not cover bound data class %q", dataClass)
			}
		}
		if siteKinds[binding.PrimarySiteRef] == "cloud" && !policy.AllowPrimary || anyLocalAutonomyCloudSite(replicaSet, siteKinds) && !policy.AllowReplicas {
			return fail(ErrInvalidPlan, bindingPath+".cloudCopyPolicy", "policy does not authorize the requested Cloud placement")
		}
	}
	return nil
}

func uniqueLocalAutonomyIDSet(values []string, path string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(values))
	for index, value := range values {
		if err := requireContractID(value, fmt.Sprintf("%s[%d]", path, index)); err != nil {
			return nil, err
		}
		if _, duplicate := result[value]; duplicate {
			return nil, fail(ErrDuplicate, fmt.Sprintf("%s[%d]", path, index), "duplicate reference %q", value)
		}
		result[value] = struct{}{}
	}
	return result, nil
}

func validateLocalAutonomyDataClasses(classes []string, path string) error {
	if len(classes) == 0 {
		return fail(ErrInvalidPlan, path, "at least one data class is required")
	}
	seen := make(map[string]struct{}, len(classes))
	for index, dataClass := range classes {
		if !stringListContains([]string{"public", "internal", "personal", "sensitive", "secret"}, dataClass) {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s[%d]", path, index), "unsupported data class")
		}
		if _, duplicate := seen[dataClass]; duplicate {
			return fail(ErrDuplicate, fmt.Sprintf("%s[%d]", path, index), "duplicate data class")
		}
		seen[dataClass] = struct{}{}
	}
	return nil
}

func sameSortedStrings(left, right []string) bool {
	leftCopy := append([]string(nil), left...)
	rightCopy := append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	return exactStringList(leftCopy, rightCopy)
}

func anyLocalAutonomyCloudSite(refs map[string]struct{}, siteKinds map[string]string) bool {
	for ref := range refs {
		if siteKinds[ref] == "cloud" {
			return true
		}
	}
	return false
}

func rejectLocalAutonomyProjectionLeaks(raw []byte, path string) error {
	return rejectGenerationOnlyPolicyProjectionLeaks(raw, path, "local-autonomy policy")
}
