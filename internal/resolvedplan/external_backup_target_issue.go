package resolvedplan

import (
	"errors"
	"time"
)

// IssueExternalBackupTargetBinding projects a local owner's opaque custody
// commitments onto the existing provider-neutral external target contract.
func IssueExternalBackupTargetBinding(requirement BackupTargetRequirement, targetRef, custodyRef, version, candidateDigest string, at time.Time) (ExternalBackupTargetBinding, error) {
	hash, err := ComputeBackupTargetRequirementHash(requirement)
	if err != nil || requirement["requirementsHash"] != hash {
		return nil, errors.New("backup target requirement hash does not verify")
	}
	binding := ExternalBackupTargetBinding{
		"apiVersion": externalBackupTargetAPIVersion, "kind": "ExternalBackupTargetBinding",
		"backupTargetRef": targetRef, "custodyAttestationRef": custodyRef,
		"stackkitsVersion": version, "candidateDigest": candidateDigest,
		"issuedAt": at.UTC().Format(time.RFC3339Nano), "validUntil": at.Add(maxExternalBackupBindingValidity).UTC().Format(time.RFC3339Nano),
	}
	for _, field := range []string{"stackId", "siteRef", "capabilityRef", "contractOwnerRef", "capabilityContractHash", "requirementsHash", "specHash"} {
		binding[field] = requirement[field]
	}
	referenceHash, err := canonicalHash(map[string]any(binding), false)
	if err != nil {
		return nil, err
	}
	binding["bindingRef"] = "backup-target-binding://sha256/" + referenceHash[len("sha256:"):]
	bindingHash, err := ComputeExternalBackupTargetBindingHash(binding)
	if err != nil {
		return nil, err
	}
	binding["bindingHash"] = bindingHash
	if err := validateExternalBackupTargetBinding(binding, requirement, "externalBackupTargetBinding"); err != nil {
		return nil, err
	}
	return binding, nil
}

// AttachExternalBackupTargetBinding preserves all other Inventory authority.
// Renewal requires the exact previous hash; a caller cannot replace a raced target.
func AttachExternalBackupTargetBinding(inventory InventoryFacts, requirement BackupTargetRequirement, binding ExternalBackupTargetBinding, previousHash string) (InventoryFacts, error) {
	if err := validateExternalBackupTargetBinding(binding, requirement, "externalBackupTargetBinding"); err != nil {
		return nil, err
	}
	cloned, err := cloneObject(inventory, false)
	if err != nil {
		return nil, err
	}
	sites, ok := cloned["externalBackupTargetBindings"].(map[string]any)
	if !ok {
		if _, exists := cloned["externalBackupTargetBindings"]; exists {
			return nil, errors.New("invalid Inventory backup target bindings")
		}
		sites = map[string]any{}
		cloned["externalBackupTargetBindings"] = sites
	}
	site := binding["siteRef"].(string)
	capability := binding["capabilityRef"].(string)
	bindings, ok := sites[site].(map[string]any)
	if !ok {
		if _, exists := sites[site]; exists {
			return nil, errors.New("invalid Inventory site target bindings")
		}
		bindings = map[string]any{}
		sites[site] = bindings
	}
	if previous, exists := bindings[capability]; exists {
		old, ok := previous.(map[string]any)
		if !ok {
			return nil, errors.New("invalid existing backup target binding")
		}
		if equal, _ := canonicalEqual(old, binding); equal {
			return InventoryFacts(cloned), nil
		}
		if previousHash == "" || old["bindingHash"] != previousHash {
			return nil, errors.New("backup target rebinding requires the exact previous binding")
		}
	} else if previousHash != "" {
		return nil, errors.New("backup target disappeared before rebinding")
	}
	bindings[capability] = map[string]any(binding)
	return InventoryFacts(cloned), nil
}
