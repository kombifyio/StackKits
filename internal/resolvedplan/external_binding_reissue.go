package resolvedplan

import (
	"errors"
	"strings"
)

// ReissueExternalHomeBackupTargetBinding carries a Home backup target binding
// over to the requirement of a successor StackSpec. Home and federation
// bindings are issued from admission evidence, not by the node, so a re-issue
// widens nothing only when it adds no authority: the successor keeps the same
// target and custody attestation, the same release and Candidate, and the same
// validity window as the binding it succeeds (never renewed), and a
// requirement naming another Stack, Site, capability, contract owner or
// contract is refused. The same inputs always yield the same bytes.
func ReissueExternalHomeBackupTargetBinding(previous ExternalHomeBackupTargetBinding, requirement HomeBackupTargetRequirement) (ExternalHomeBackupTargetBinding, error) {
	hash, err := ComputeHomeBackupTargetRequirementHash(requirement)
	if err != nil || requirement["requirementsHash"] != hash {
		return nil, errors.New("Home backup target requirement hash does not verify")
	}
	binding, err := reissueBinding(map[string]any(previous), map[string]any(requirement),
		[]string{"stackId", "siteRef", "capabilityRef", "contractOwnerRef", "capabilityContractHash"},
		[]string{"requirementsHash", "specHash"}, "home-backup-target-binding://sha256/")
	if err != nil {
		return nil, err
	}
	reissued := ExternalHomeBackupTargetBinding(binding)
	reissued["bindingHash"], err = ComputeExternalHomeBackupTargetBindingHash(reissued)
	if err != nil {
		return nil, err
	}
	if err := validateExternalHomeBackupTargetBinding(reissued, requirement, "externalHomeBackupTargetBinding"); err != nil {
		return nil, err
	}
	return reissued, nil
}

// ReissueExternalFederationLinkBinding is the federation link counterpart of
// ReissueExternalHomeBackupTargetBinding: same fabric, custody attestation,
// release, Candidate and validity window under the successor requirement, and
// the same Sites, target nodes and bridge contract.
func ReissueExternalFederationLinkBinding(previous ExternalFederationLinkBinding, requirement FederationLinkRequirement) (ExternalFederationLinkBinding, error) {
	hash, err := ComputeFederationLinkRequirementHash(requirement)
	if err != nil || requirement["requirementsHash"] != hash {
		return nil, errors.New("federation link requirement hash does not verify")
	}
	binding, err := reissueBinding(map[string]any(previous), map[string]any(requirement),
		[]string{"stackId", "capabilityRef", "contractOwnerRef", "capabilityContractHash", "homeSiteRefs", "cloudSiteRefs", "targetNodes", "bridgeContractHash"},
		[]string{"requirementsHash", "specHash"}, "federation-link-binding://sha256/")
	if err != nil {
		return nil, err
	}
	reissued := ExternalFederationLinkBinding(binding)
	reissued["bindingHash"], err = ComputeExternalFederationLinkBindingHash(reissued)
	if err != nil {
		return nil, err
	}
	if err := validateExternalFederationLinkBinding(reissued, requirement, "externalFederationLinkBinding"); err != nil {
		return nil, err
	}
	return reissued, nil
}

// reissueBinding clones previous, requires the identity fields to equal the
// requirement's, takes the spec-bound fields from the requirement, and derives
// a fresh opaque bindingRef from the resulting body.
func reissueBinding(previous, requirement map[string]any, identity, specBound []string, refPrefix string) (map[string]any, error) {
	for _, field := range identity {
		equal, err := canonicalEqual(previous[field], requirement[field])
		if previous[field] == nil || err != nil || !equal {
			return nil, errors.New("requirement names a different target than the custodied binding")
		}
	}
	binding, err := cloneObject(previous, false)
	if err != nil {
		return nil, err
	}
	for _, field := range specBound {
		binding[field] = requirement[field]
	}
	delete(binding, "bindingHash")
	delete(binding, "bindingRef")
	referenceHash, err := canonicalHash(binding, false)
	if err != nil {
		return nil, err
	}
	binding["bindingRef"] = refPrefix + strings.TrimPrefix(referenceHash, "sha256:")
	return binding, nil
}

// Binding kinds of ReissuedExternalBinding.
const (
	ReissuedHomeBackupTargetBinding = "home-backup-target"
	ReissuedFederationLinkBinding   = "federation-link"
)

// ReissuedExternalBinding is one Inventory binding carried over to a successor
// StackSpec. SiteRef is empty for a federation link, which is not site keyed.
type ReissuedExternalBinding struct {
	Kind          string
	SiteRef       string
	CapabilityRef string
	PreviousHash  string
	Binding       map[string]any
}

// ReissueInventoryExternalBindings re-issues every Home backup target and
// federation link binding in the Inventory whose requirement in the successor
// plan changed only by its spec binding. A binding without an exact successor
// requirement is left as it is, so compilation still refuses it. It returns
// the Inventory holding the successors and what changed; the input is not
// modified.
func ReissueInventoryExternalBindings(inventory InventoryFacts, homeRequirements, federationRequirements map[string]any) (InventoryFacts, []ReissuedExternalBinding, error) {
	cloned, err := cloneObject(inventory, false)
	if err != nil {
		return nil, nil, err
	}
	var reissued []ReissuedExternalBinding
	if sites, ok := cloned["externalHomeBackupTargetBindings"].(map[string]any); ok {
		for _, siteRef := range sortedStringMapKeys(sites) {
			bindings, _ := sites[siteRef].(map[string]any)
			for _, capabilityRef := range sortedStringMapKeys(bindings) {
				previous, _ := bindings[capabilityRef].(map[string]any)
				requirement, err := homeBackupTargetRequirementAt(homeRequirements, siteRef, capabilityRef)
				if previous == nil || err != nil || previous["specHash"] == requirement["specHash"] {
					continue
				}
				successor, err := ReissueExternalHomeBackupTargetBinding(ExternalHomeBackupTargetBinding(previous), HomeBackupTargetRequirement(requirement))
				if err != nil {
					return nil, nil, err
				}
				bindings[capabilityRef] = map[string]any(successor)
				reissued = append(reissued, ReissuedExternalBinding{
					Kind: ReissuedHomeBackupTargetBinding, SiteRef: siteRef, CapabilityRef: capabilityRef,
					PreviousHash: previous["bindingHash"].(string), Binding: successor,
				})
			}
		}
	}
	if bindings, ok := cloned["externalFederationLinkBindings"].(map[string]any); ok {
		for _, capabilityRef := range sortedStringMapKeys(bindings) {
			previous, _ := bindings[capabilityRef].(map[string]any)
			requirement, err := federationLinkRequirementAt(federationRequirements, capabilityRef)
			if previous == nil || err != nil || previous["specHash"] == requirement["specHash"] {
				continue
			}
			successor, err := ReissueExternalFederationLinkBinding(ExternalFederationLinkBinding(previous), FederationLinkRequirement(requirement))
			if err != nil {
				return nil, nil, err
			}
			bindings[capabilityRef] = map[string]any(successor)
			reissued = append(reissued, ReissuedExternalBinding{
				Kind: ReissuedFederationLinkBinding, CapabilityRef: capabilityRef,
				PreviousHash: previous["bindingHash"].(string), Binding: successor,
			})
		}
	}
	return InventoryFacts(cloned), reissued, nil
}

// ApplyReissuedExternalBindings writes re-issued bindings into the Inventory
// they were derived from. Each replaces exactly the binding it succeeds; a
// raced or missing predecessor is refused.
func ApplyReissuedExternalBindings(inventory InventoryFacts, reissued []ReissuedExternalBinding) (InventoryFacts, error) {
	cloned, err := cloneObject(inventory, false)
	if err != nil {
		return nil, err
	}
	for _, item := range reissued {
		var bindings map[string]any
		switch item.Kind {
		case ReissuedHomeBackupTargetBinding:
			sites, _ := cloned["externalHomeBackupTargetBindings"].(map[string]any)
			bindings, _ = sites[item.SiteRef].(map[string]any)
		case ReissuedFederationLinkBinding:
			bindings, _ = cloned["externalFederationLinkBindings"].(map[string]any)
		}
		previous, _ := bindings[item.CapabilityRef].(map[string]any)
		if previous == nil {
			return nil, errors.New("the binding to re-issue disappeared from the Inventory")
		}
		if equal, _ := canonicalEqual(previous, item.Binding); equal {
			continue
		}
		if previous["bindingHash"] != item.PreviousHash {
			return nil, errors.New("the Inventory binding changed before its re-issue was adopted")
		}
		bindings[item.CapabilityRef] = item.Binding
	}
	return InventoryFacts(cloned), nil
}
