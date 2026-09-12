package generationartifact

import "github.com/kombifyio/stackkits/internal/resolvedplan"

// ProjectExternalBackupTargetBinding uses the normal Apply projection for a
// newly owner-issued binding before it has been persisted into Inventory.
// It does not change or authorize execution of the held Plan.
func (p VerifiedPlan) ProjectExternalBackupTargetBinding(binding resolvedplan.ExternalBackupTargetBinding) ([]ApplyBackupTargetBindingRequirement, error) {
	plan, err := resolvedplan.DecodeCanonicalPlan(p.canonical)
	if err != nil {
		return nil, err
	}
	site, err := requiredString(binding, "siteRef", "externalBackupTargetBinding.siteRef")
	if err != nil {
		return nil, err
	}
	capability, err := requiredString(binding, "capabilityRef", "externalBackupTargetBinding.capabilityRef")
	if err != nil {
		return nil, err
	}
	plan["externalBackupTargetBindings"] = map[string]any{site: map[string]any{capability: map[string]any(binding)}}
	providers, err := parseApplyProviderContracts(plan)
	if err != nil {
		return nil, err
	}
	modules, err := parseApplyModules(plan, providers)
	if err != nil {
		return nil, err
	}
	var result ApplyRequirements
	if err := appendApplyBackupTargetBindingRequirements(plan, modules, &result); err != nil {
		return nil, err
	}
	return result.BackupTargetBindings, nil
}
