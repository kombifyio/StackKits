package architecturev2renderer

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

type localRuntimeExecutorPlan struct {
	StackID                          string                      `json:"stackId"`
	Kit                              executorBundleKit           `json:"kit"`
	Sites                            []executorBundleSite        `json:"sites"`
	ModuleTargets                    []executorBundleTarget      `json:"moduleTargets"`
	ModuleCapabilities               []executorBundleCapability  `json:"moduleCapabilities"`
	ControlPlane                     executorBundleControlPlane  `json:"controlPlane"`
	StoragePolicy                    executorBundleStoragePolicy `json:"storagePolicy"`
	LocalNetworkPolicy               executorBundleNetworkPolicy `json:"localNetworkPolicy"`
	Data                             executorBundleData          `json:"data"`
	FailurePolicy                    executorBundleFailurePolicy `json:"failurePolicy"`
	LocalReachability                homeLocalReachability       `json:"localReachability"`
	HomeAccessRequirements           json.RawMessage             `json:"homeAccessRequirements,omitempty"`
	ExternalHomeAccessBindings       json.RawMessage             `json:"externalHomeAccessBindings,omitempty"`
	HomeBackupTargetRequirements     json.RawMessage             `json:"homeBackupTargetRequirements,omitempty"`
	ExternalHomeBackupTargetBindings json.RawMessage             `json:"externalHomeBackupTargetBindings,omitempty"`
}

func (localRuntimeExecutorPlan) executorContractPlanMarker() {}

type homeOffsiteBackupProjection struct {
	Requirements json.RawMessage `json:"requirements"`
	Bindings     json.RawMessage `json:"bindings"`
}

type homeAccessHandoffProjection struct {
	Requirements json.RawMessage `json:"requirements"`
	Bindings     json.RawMessage `json:"bindings"`
}

type homeAccessExecutorPlan struct {
	StackID            string                      `json:"stackId"`
	Kit                executorBundleKit           `json:"kit"`
	Sites              []executorBundleSite        `json:"sites"`
	ModuleTargets      []executorBundleTarget      `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability  `json:"moduleCapabilities"`
	ControlPlane       executorBundleControlPlane  `json:"controlPlane"`
	HomeAccessHandoff  homeAccessHandoffProjection `json:"homeAccessHandoff"`
}

func (homeAccessExecutorPlan) executorContractPlanMarker() {}

type basementComposeExecutorPlan struct {
	StackID            string                     `json:"stackId"`
	Kit                executorBundleKit          `json:"kit"`
	Sites              []executorBundleSite       `json:"sites"`
	ModuleTargets      []executorBundleTarget     `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability `json:"moduleCapabilities"`
	ControlPlane       executorBundleControlPlane `json:"controlPlane"`
}

func (basementComposeExecutorPlan) executorContractPlanMarker() {}

type homeOffsiteBackupExecutorPlan struct {
	StackID            string                      `json:"stackId"`
	Kit                executorBundleKit           `json:"kit"`
	Sites              []executorBundleSite        `json:"sites"`
	ModuleTargets      []executorBundleTarget      `json:"moduleTargets"`
	ModuleCapabilities []executorBundleCapability  `json:"moduleCapabilities"`
	ControlPlane       executorBundleControlPlane  `json:"controlPlane"`
	HomeOffsiteBackup  homeOffsiteBackupProjection `json:"homeOffsiteBackup"`
}

func (homeOffsiteBackupExecutorPlan) executorContractPlanMarker() {}

func decodeLocalRuntimeExecutorPlan(raw []byte, path string, spec executorContractBundleSpec) (executorContractPlan, error) {
	if spec.moduleID == homeEncryptedOffsiteBackupModuleID {
		var exact homeOffsiteBackupExecutorPlan
		if err := decodeStrict(raw, &exact); err != nil {
			return nil, wrap(ErrInvalidPlan, path, "decode exact Home offsite-backup executor contract", err)
		}
		if err := validateExecutorContractPlanCommon(exact.StackID, exact.Kit, exact.Sites, exact.ModuleTargets, exact.ModuleCapabilities, exact.ControlPlane, spec, path); err != nil {
			return nil, err
		}
		validationPlan := localRuntimeExecutorPlan{
			StackID: exact.StackID, Kit: exact.Kit, Sites: exact.Sites,
			ModuleTargets: exact.ModuleTargets, ModuleCapabilities: exact.ModuleCapabilities,
			ControlPlane:                     exact.ControlPlane,
			HomeBackupTargetRequirements:     exact.HomeOffsiteBackup.Requirements,
			ExternalHomeBackupTargetBindings: exact.HomeOffsiteBackup.Bindings,
		}
		if err := validateHomeBackupTargetExecutorProjection(validationPlan, spec, path+".homeOffsiteBackup"); err != nil {
			return nil, err
		}
		return exact, nil
	}
	var plan localRuntimeExecutorPlan
	if err := decodeStrict(raw, &plan); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact Local executor contract", err)
	}
	if err := validateExecutorContractPlanCommon(plan.StackID, plan.Kit, plan.Sites, plan.ModuleTargets, plan.ModuleCapabilities, plan.ControlPlane, spec, path); err != nil {
		return nil, err
	}
	if err := validateStoragePolicy(plan.StoragePolicy, path+".storagePolicy"); err != nil {
		return nil, err
	}
	if err := validateExecutorBundleNetworkPolicy(plan.LocalNetworkPolicy, plan.Kit.Slug, false, path+".localNetworkPolicy"); err != nil {
		return nil, err
	}
	if err := validateExecutorBundleData(plan.Data, plan.Sites, path+".data"); err != nil {
		return nil, err
	}
	if err := validateExecutorBundleFailurePolicy(plan.FailurePolicy, path+".failurePolicy"); err != nil {
		return nil, err
	}
	siteKinds := executorBundleSiteKinds(plan.Sites)
	homeRefs := sortedExecutorBundleSites(plan.Sites, "home")
	if !exactStringList(plan.LocalReachability.HomeSiteRefs, homeRefs) {
		return nil, fail(ErrInvalidPlan, path+".localReachability.homeSiteRefs", "must exactly equal module Home Sites")
	}
	for index, route := range plan.LocalReachability.Routes {
		if err := validateHomeLocalRoute(route, siteKinds, fmt.Sprintf("%s.localReachability.routes[%d]", path, index)); err != nil {
			return nil, err
		}
	}
	if err := validateHomeAccessExecutorProjection(plan, spec, path); err != nil {
		return nil, err
	}
	if err := validateHomeBackupTargetExecutorProjection(plan, spec, path); err != nil {
		return nil, err
	}
	return plan, nil
}

func decodeHomeAccessExecutorPlan(raw []byte, path string, spec executorContractBundleSpec) (executorContractPlan, error) {
	var exact homeAccessExecutorPlan
	if err := decodeStrict(raw, &exact); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact Home access executor handoff", err)
	}
	if err := validateExecutorContractPlanCommon(
		exact.StackID,
		exact.Kit,
		exact.Sites,
		exact.ModuleTargets,
		exact.ModuleCapabilities,
		exact.ControlPlane,
		spec,
		path,
	); err != nil {
		return nil, err
	}
	validationPlan := localRuntimeExecutorPlan{
		StackID: exact.StackID, Kit: exact.Kit, Sites: exact.Sites,
		ModuleTargets: exact.ModuleTargets, ModuleCapabilities: exact.ModuleCapabilities,
		ControlPlane:               exact.ControlPlane,
		HomeAccessRequirements:     exact.HomeAccessHandoff.Requirements,
		ExternalHomeAccessBindings: exact.HomeAccessHandoff.Bindings,
	}
	if err := validateHomeAccessExecutorProjection(validationPlan, spec, path+".homeAccessHandoff"); err != nil {
		return nil, err
	}
	return exact, nil
}

func decodeBasementComposeExecutorPlan(raw []byte, path string, spec executorContractBundleSpec) (executorContractPlan, error) {
	var exact basementComposeExecutorPlan
	if err := decodeStrict(raw, &exact); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact Basement Compose selection handoff", err)
	}
	if err := validateExecutorContractPlanCommon(
		exact.StackID,
		exact.Kit,
		exact.Sites,
		exact.ModuleTargets,
		exact.ModuleCapabilities,
		exact.ControlPlane,
		spec,
		path,
	); err != nil {
		return nil, err
	}
	return exact, nil
}

type homeBackupTargetRequirementProjection struct {
	APIVersion             string   `json:"apiVersion"`
	Kind                   string   `json:"kind"`
	StackID                string   `json:"stackId"`
	SiteRef                string   `json:"siteRef"`
	CapabilityRef          string   `json:"capabilityRef"`
	ContractOwnerRef       string   `json:"contractOwnerRef"`
	CapabilityContractHash string   `json:"capabilityContractHash"`
	TargetNodeRefs         []string `json:"targetNodeRefs"`
	Policy                 struct {
		Scope                       string `json:"scope"`
		EncryptionRequired          bool   `json:"encryptionRequired"`
		EncryptionAuthority         string `json:"encryptionAuthority"`
		PlaintextEgressAllowed      bool   `json:"plaintextEgressAllowed"`
		CredentialCustody           string `json:"credentialCustody"`
		TargetLifecycle             string `json:"targetLifecycle"`
		RestoreVerificationRequired bool   `json:"restoreVerificationRequired"`
		ProviderSelection           string `json:"providerSelection"`
	} `json:"policy"`
	SpecHash         string `json:"specHash"`
	RequirementsHash string `json:"requirementsHash"`
}

type externalHomeBackupTargetBindingProjection struct {
	APIVersion             string `json:"apiVersion"`
	Kind                   string `json:"kind"`
	BindingRef             string `json:"bindingRef"`
	BackupTargetRef        string `json:"backupTargetRef"`
	CustodyAttestationRef  string `json:"custodyAttestationRef"`
	StackID                string `json:"stackId"`
	SiteRef                string `json:"siteRef"`
	CapabilityRef          string `json:"capabilityRef"`
	ContractOwnerRef       string `json:"contractOwnerRef"`
	CapabilityContractHash string `json:"capabilityContractHash"`
	RequirementsHash       string `json:"requirementsHash"`
	StackKitsVersion       string `json:"stackkitsVersion"`
	CandidateDigest        string `json:"candidateDigest"`
	SpecHash               string `json:"specHash"`
	IssuedAt               string `json:"issuedAt"`
	ValidUntil             string `json:"validUntil"`
	BindingHash            string `json:"bindingHash"`
}

func validateHomeBackupTargetExecutorProjection(plan localRuntimeExecutorPlan, spec executorContractBundleSpec, path string) error {
	isHomeBackup := containsExecutorBundleString(spec.requiredCapabilities, homeBackupCapability)
	if !isHomeBackup {
		if len(plan.HomeBackupTargetRequirements) != 0 || len(plan.ExternalHomeBackupTargetBindings) != 0 {
			return fail(ErrInvalidPlan, path, "non-backup Home module received a Home backup target projection")
		}
		return nil
	}
	if len(plan.HomeBackupTargetRequirements) == 0 || len(plan.ExternalHomeBackupTargetBindings) == 0 {
		return fail(ErrInvalidPlan, path, "Home encrypted offsite backup requires exact requirement and binding projections")
	}
	var requirements map[string]map[string]json.RawMessage
	if err := decodeStrict(plan.HomeBackupTargetRequirements, &requirements); err != nil {
		return wrap(ErrInvalidPlan, path+".homeBackupTargetRequirements", "decode closed Home backup requirements", err)
	}
	var bindings map[string]map[string]json.RawMessage
	if err := decodeStrict(plan.ExternalHomeBackupTargetBindings, &bindings); err != nil {
		return wrap(ErrInvalidPlan, path+".externalHomeBackupTargetBindings", "decode closed external Home backup bindings", err)
	}
	targetSites := make(map[string]struct{}, len(plan.ModuleTargets))
	for _, target := range plan.ModuleTargets {
		targetSites[target.SiteRef] = struct{}{}
	}
	if len(requirements) != len(targetSites) {
		return fail(ErrInvalidPlan, path+".homeBackupTargetRequirements", "must contain exactly one requirement per module Home Site")
	}
	for _, site := range plan.Sites {
		if _, selected := targetSites[site.ID]; !selected {
			continue
		}
		targetNodeRefs := make([]string, 0, len(plan.ModuleTargets))
		for _, target := range plan.ModuleTargets {
			if target.SiteRef == site.ID {
				targetNodeRefs = append(targetNodeRefs, target.ID)
			}
		}
		sort.Strings(targetNodeRefs)
		byCapability, exists := requirements[site.ID]
		if !exists || len(byCapability) != 1 {
			return fail(ErrInvalidPlan, path+".homeBackupTargetRequirements."+site.ID, "must contain only the encrypted offsite backup capability")
		}
		rawRequirement, exists := byCapability[homeBackupCapability]
		if !exists {
			return fail(ErrInvalidPlan, path+".homeBackupTargetRequirements."+site.ID, "missing exact Home backup capability requirement")
		}
		var requirement homeBackupTargetRequirementProjection
		if err := decodeStrict(rawRequirement, &requirement); err != nil {
			return wrap(ErrInvalidPlan, path+".homeBackupTargetRequirements."+site.ID+"."+homeBackupCapability, "decode closed requirement", err)
		}
		if requirement.APIVersion != "stackkit.home-backup-target-requirement/v1" || requirement.Kind != "HomeBackupTargetRequirement" ||
			requirement.StackID != plan.StackID || requirement.SiteRef != site.ID || requirement.CapabilityRef != homeBackupCapability ||
			requirement.ContractOwnerRef == "" || !validSHA256(requirement.CapabilityContractHash) || !validSHA256(requirement.SpecHash) || !validSHA256(requirement.RequirementsHash) ||
			!exactStringList(requirement.TargetNodeRefs, targetNodeRefs) || requirement.Policy.Scope != "governed-home-data-only" || !requirement.Policy.EncryptionRequired ||
			requirement.Policy.EncryptionAuthority != "home" || requirement.Policy.PlaintextEgressAllowed || requirement.Policy.CredentialCustody != "external" ||
			requirement.Policy.TargetLifecycle != "external" || !requirement.Policy.RestoreVerificationRequired || requirement.Policy.ProviderSelection != "external" {
			return fail(ErrInvalidPlan, path+".homeBackupTargetRequirements."+site.ID+"."+homeBackupCapability, "requirement widens or contradicts the exact Home backup authority")
		}
		rawBody, _ := json.Marshal(requirement)
		var body map[string]any
		if err := decodeStrict(rawBody, &body); err != nil {
			return err
		}
		wantHash, err := resolvedplan.ComputeHomeBackupTargetRequirementHash(resolvedplan.HomeBackupTargetRequirement(body))
		if err != nil || requirement.RequirementsHash != wantHash {
			return fail(ErrInvalidPlan, path+".homeBackupTargetRequirements."+site.ID+"."+homeBackupCapability+".requirementsHash", "does not match the canonical requirement body")
		}
		bindingByCapability := bindings[site.ID]
		if len(bindingByCapability) == 0 {
			continue
		}
		if len(bindingByCapability) != 1 {
			return fail(ErrInvalidPlan, path+".externalHomeBackupTargetBindings."+site.ID, "must contain only the encrypted offsite backup capability")
		}
		rawBinding, exists := bindingByCapability[homeBackupCapability]
		if !exists {
			return fail(ErrInvalidPlan, path+".externalHomeBackupTargetBindings."+site.ID, "binding capability does not match the module")
		}
		var binding externalHomeBackupTargetBindingProjection
		if err := decodeStrict(rawBinding, &binding); err != nil {
			return wrap(ErrInvalidPlan, path+".externalHomeBackupTargetBindings."+site.ID+"."+homeBackupCapability, "decode closed binding", err)
		}
		if binding.APIVersion != "stackkit.external-home-backup-target-binding/v1" || binding.Kind != "ExternalHomeBackupTargetBinding" ||
			binding.StackID != requirement.StackID || binding.SiteRef != requirement.SiteRef || binding.CapabilityRef != requirement.CapabilityRef ||
			binding.ContractOwnerRef != requirement.ContractOwnerRef || binding.CapabilityContractHash != requirement.CapabilityContractHash ||
			binding.RequirementsHash != requirement.RequirementsHash || binding.SpecHash != requirement.SpecHash ||
			!validOpaqueSHA256Ref(binding.BindingRef, "home-backup-target-binding") || !validOpaqueSHA256Ref(binding.BackupTargetRef, "home-backup-target") ||
			!validOpaqueSHA256Ref(binding.CustodyAttestationRef, "home-backup-custody-attestation") {
			return fail(ErrInvalidPlan, path+".externalHomeBackupTargetBindings."+site.ID, "binding does not exactly match the provider-free Home backup requirement")
		}
		rawBody, _ = json.Marshal(binding)
		body = map[string]any{}
		if err := decodeStrict(rawBody, &body); err != nil {
			return err
		}
		wantBindingHash, err := resolvedplan.ComputeExternalHomeBackupTargetBindingHash(resolvedplan.ExternalHomeBackupTargetBinding(body))
		if err != nil || binding.BindingHash != wantBindingHash {
			return fail(ErrInvalidPlan, path+".externalHomeBackupTargetBindings."+site.ID+"."+homeBackupCapability+".bindingHash", "does not match the canonical binding body")
		}
	}
	for siteRef := range bindings {
		if _, ok := requirements[siteRef]; !ok {
			return fail(ErrInvalidPlan, path+".externalHomeBackupTargetBindings."+siteRef, "binding targets a Site outside the exact requirement")
		}
	}
	return nil
}

type homeAccessRequirementProjection struct {
	APIVersion             string   `json:"apiVersion"`
	Kind                   string   `json:"kind"`
	StackID                string   `json:"stackId"`
	SiteRef                string   `json:"siteRef"`
	CapabilityRef          string   `json:"capabilityRef"`
	ContractOwnerRef       string   `json:"contractOwnerRef"`
	CapabilityContractHash string   `json:"capabilityContractHash"`
	TargetNodeRefs         []string `json:"targetNodeRefs"`
	Policy                 struct {
		DefaultDeny       bool   `json:"defaultDeny"`
		Initiation        string `json:"initiation"`
		RouteScope        string `json:"routeScope"`
		AllowDefaultRoute bool   `json:"allowDefaultRoute"`
		AllowBroadLAN     bool   `json:"allowBroadLAN"`
		IdentityMode      string `json:"identityMode"`
		CredentialCustody string `json:"credentialCustody"`
		FabricLifecycle   string `json:"fabricLifecycle"`
	} `json:"policy"`
	SpecHash         string `json:"specHash"`
	RequirementsHash string `json:"requirementsHash"`
}

type externalHomeAccessBindingProjection struct {
	APIVersion             string `json:"apiVersion"`
	Kind                   string `json:"kind"`
	BindingRef             string `json:"bindingRef"`
	StackID                string `json:"stackId"`
	SiteRef                string `json:"siteRef"`
	CapabilityRef          string `json:"capabilityRef"`
	ContractOwnerRef       string `json:"contractOwnerRef"`
	CapabilityContractHash string `json:"capabilityContractHash"`
	RequirementsHash       string `json:"requirementsHash"`
	AccessFabricRef        string `json:"accessFabricRef"`
	StackKitsVersion       string `json:"stackkitsVersion"`
	CandidateDigest        string `json:"candidateDigest"`
	SpecHash               string `json:"specHash"`
	IssuedAt               string `json:"issuedAt"`
	ValidUntil             string `json:"validUntil"`
	BindingHash            string `json:"bindingHash"`
}

func validateHomeAccessExecutorProjection(plan localRuntimeExecutorPlan, spec executorContractBundleSpec, path string) error {
	capabilityRef := ""
	for _, candidate := range []string{"private-remote-access", "public-publish-egress"} {
		if containsExecutorBundleString(spec.requiredCapabilities, candidate) {
			capabilityRef = candidate
			break
		}
	}
	if capabilityRef == "" {
		if len(plan.HomeAccessRequirements) != 0 || len(plan.ExternalHomeAccessBindings) != 0 {
			return fail(ErrInvalidPlan, path, "non-access module received a Home access authority projection")
		}
		return nil
	}
	if len(plan.HomeAccessRequirements) == 0 || len(plan.ExternalHomeAccessBindings) == 0 {
		return fail(ErrInvalidPlan, path, "Home access module requires exact requirement and binding projections")
	}
	var requirements map[string]map[string]json.RawMessage
	if err := decodeStrict(plan.HomeAccessRequirements, &requirements); err != nil {
		return wrap(ErrInvalidPlan, path+".homeAccessRequirements", "decode closed Home access requirements", err)
	}
	var bindings map[string]map[string]json.RawMessage
	if err := decodeStrict(plan.ExternalHomeAccessBindings, &bindings); err != nil {
		return wrap(ErrInvalidPlan, path+".externalHomeAccessBindings", "decode closed external Home access bindings", err)
	}
	targetSites := make(map[string]struct{}, len(plan.ModuleTargets))
	for _, target := range plan.ModuleTargets {
		targetSites[target.SiteRef] = struct{}{}
	}
	if len(requirements) != len(targetSites) {
		return fail(ErrInvalidPlan, path+".homeAccessRequirements", "must contain exactly one requirement per module Home Site")
	}
	for _, site := range plan.Sites {
		if _, selected := targetSites[site.ID]; !selected {
			continue
		}
		targetNodeRefs := make([]string, 0, len(plan.ModuleTargets))
		for _, target := range plan.ModuleTargets {
			if target.SiteRef == site.ID {
				targetNodeRefs = append(targetNodeRefs, target.ID)
			}
		}
		sort.Strings(targetNodeRefs)
		if len(targetNodeRefs) == 0 {
			return fail(ErrInvalidPlan, path+".moduleTargets", "Home access module has no target at Site %s", site.ID)
		}
		byCapability, exists := requirements[site.ID]
		if !exists || len(byCapability) != 1 {
			return fail(ErrInvalidPlan, path+".homeAccessRequirements."+site.ID, "must contain only the module access capability")
		}
		rawRequirement, exists := byCapability[capabilityRef]
		if !exists {
			return fail(ErrInvalidPlan, path+".homeAccessRequirements."+site.ID, "missing exact access capability requirement")
		}
		var requirement homeAccessRequirementProjection
		if err := decodeStrict(rawRequirement, &requirement); err != nil {
			return wrap(ErrInvalidPlan, path+".homeAccessRequirements."+site.ID+"."+capabilityRef, "decode closed requirement", err)
		}
		identityMode := "device-bound"
		if capabilityRef == "public-publish-egress" {
			identityMode = "service-identity"
		}
		if requirement.APIVersion != "stackkit.home-access-requirement/v1" || requirement.Kind != "HomeAccessRequirement" ||
			requirement.StackID != plan.StackID || requirement.SiteRef != site.ID || requirement.CapabilityRef != capabilityRef ||
			requirement.ContractOwnerRef == "" || !validSHA256(requirement.CapabilityContractHash) || !validSHA256(requirement.SpecHash) || !validSHA256(requirement.RequirementsHash) ||
			!exactStringList(requirement.TargetNodeRefs, targetNodeRefs) || !requirement.Policy.DefaultDeny || requirement.Policy.Initiation != "home-outbound" ||
			requirement.Policy.RouteScope != "declared-services-only" || requirement.Policy.AllowDefaultRoute || requirement.Policy.AllowBroadLAN ||
			requirement.Policy.IdentityMode != identityMode || requirement.Policy.CredentialCustody != "external" || requirement.Policy.FabricLifecycle != "external" {
			return fail(ErrInvalidPlan, path+".homeAccessRequirements."+site.ID+"."+capabilityRef, "requirement widens or contradicts the exact Home access authority")
		}
		bindingByCapability := bindings[site.ID]
		if len(bindingByCapability) == 0 {
			continue
		}
		if len(bindingByCapability) != 1 {
			return fail(ErrInvalidPlan, path+".externalHomeAccessBindings."+site.ID, "must contain only the module access capability")
		}
		rawBinding, exists := bindingByCapability[capabilityRef]
		if !exists {
			return fail(ErrInvalidPlan, path+".externalHomeAccessBindings."+site.ID, "binding capability does not match the module")
		}
		var binding externalHomeAccessBindingProjection
		if err := decodeStrict(rawBinding, &binding); err != nil {
			return wrap(ErrInvalidPlan, path+".externalHomeAccessBindings."+site.ID+"."+capabilityRef, "decode closed binding", err)
		}
		if binding.APIVersion != "stackkit.external-home-access-binding/v1" || binding.Kind != "ExternalHomeAccessBinding" ||
			binding.StackID != requirement.StackID || binding.SiteRef != requirement.SiteRef || binding.CapabilityRef != requirement.CapabilityRef ||
			binding.ContractOwnerRef != requirement.ContractOwnerRef || binding.CapabilityContractHash != requirement.CapabilityContractHash ||
			binding.RequirementsHash != requirement.RequirementsHash || binding.SpecHash != requirement.SpecHash ||
			!validOpaqueSHA256Ref(binding.BindingRef, "home-access-binding") || !validOpaqueSHA256Ref(binding.AccessFabricRef, "home-access-fabric") ||
			!validSHA256(binding.CandidateDigest) || !validSHA256(binding.BindingHash) || binding.StackKitsVersion == "" || binding.IssuedAt == "" || binding.ValidUntil == "" {
			return fail(ErrInvalidPlan, path+".externalHomeAccessBindings."+site.ID+"."+capabilityRef, "binding does not exactly match the Home access requirement")
		}
	}
	for siteRef := range bindings {
		if !containsExecutorBundleString(sortedExecutorBundleSites(plan.Sites, "home"), siteRef) {
			return fail(ErrInvalidPlan, path+".externalHomeAccessBindings."+siteRef, "binding targets a Site outside the module")
		}
	}
	return nil
}
