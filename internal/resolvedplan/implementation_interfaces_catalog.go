package resolvedplan

import "fmt"

type catalogDirectInterfaceRequirement struct {
	moduleRef       string
	unitRef         string
	providerRef     string
	daemonRef       string
	policy          string
	placement       map[string]any
	evidenceRefs    []string
	providerBacking bool
}

// validateExplicitImplementationInterfaceContracts runs before CUE
// normalization. Conditional CUE constraints can materialize known constant
// fields such as version or coLocation; authority authors must still declare
// every interface field explicitly so omission cannot silently become policy.
func validateExplicitImplementationInterfaceContracts(catalog Catalog) error {
	for moduleIndex, module := range catalog.Modules {
		modulePath := fmt.Sprintf("catalog.modules[%d]", moduleIndex)
		units, err := objectListOptional(map[string]any(module), "renderUnits")
		if err != nil {
			return err
		}
		for unitIndex, unit := range units {
			unitPath := fmt.Sprintf("%s.renderUnits[%d]", modulePath, unitIndex)
			for _, field := range []string{"providesInterfaces", "requiresInterfaces"} {
				contracts, err := objectListOptional(unit, field)
				if err != nil {
					return err
				}
				for contractIndex, contract := range contracts {
					contractPath := fmt.Sprintf("%s.%s[%d]", unitPath, field, contractIndex)
					for _, required := range []string{"id", "kind", "protocol", "version", "endpoint", "scopes", "coLocation", "daemonRef", "policyProfile"} {
						if _, exists := contract[required]; !exists {
							return fail(ErrContractConflict, contractPath+"."+required, "implementation interface field must be declared explicitly before CUE normalization")
						}
					}
					kind, _ := contract["kind"].(string)
					if kind == dockerSocketDirectInterfaceKind {
						endpoint, err := objectField(contract, contractPath, "endpoint")
						if err != nil {
							return err
						}
						if _, err := parseDirectSocketEndpointPath(endpoint, contractPath+".endpoint", ErrContractConflict); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	return nil
}

func validateCatalogImplementationInterfaces(catalog Catalog) error {
	providerIDs, err := catalogProviderIDs(catalog.Providers)
	if err != nil {
		return err
	}
	providedIDs := map[string]string{}
	providedPolicies := map[string]string{}
	directRequirements := map[string]catalogDirectInterfaceRequirement{}
	for index, rawModule := range catalog.Modules {
		module := map[string]any(rawModule)
		moduleID, err := metadataID(module, fmt.Sprintf("catalog.modules[%d]", index))
		if err != nil {
			return err
		}
		if err := validateCatalogModuleInterfaces(moduleID, module, providedIDs, providedPolicies, directRequirements); err != nil {
			return err
		}
	}
	return validateCatalogPrivilegedApprovals(catalog.PrivilegedInterfaceApprovals, providerIDs, directRequirements)
}

func catalogProviderIDs(providers []CapabilityProvider) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(providers))
	for index, rawProvider := range providers {
		id, err := metadataID(map[string]any(rawProvider), fmt.Sprintf("catalog.providers[%d]", index))
		if err != nil {
			return nil, err
		}
		result[id] = struct{}{}
	}
	return result, nil
}

func validateCatalogModuleInterfaces(moduleID string, module map[string]any, providedIDs, providedPolicies map[string]string, directRequirements map[string]catalogDirectInterfaceRequirement) error {
	modulePath := "catalog.modules." + moduleID
	providerRef, _, err := optionalStringField(module, modulePath, "providerRef")
	if err != nil {
		return err
	}
	evidenceRefs, err := stringListField(module, modulePath, "evidence", false)
	if err != nil {
		return err
	}
	units, err := objectListField(module, modulePath, "renderUnits")
	if err != nil {
		return err
	}
	moduleRequiresProxy, moduleRequiresDirect := false, false
	for index, unit := range units {
		unitPath := fmt.Sprintf("%s.renderUnits[%d]", modulePath, index)
		unitID, err := stringField(unit, unitPath, "id")
		if err != nil {
			return err
		}
		placement, err := objectField(unit, unitPath, "placement")
		if err != nil {
			return err
		}
		provided, err := objectListOptional(unit, "providesInterfaces")
		if err != nil {
			return err
		}
		for contractIndex, contract := range provided {
			if err := validateCatalogProvidedInterface(moduleID, unitID, contractIndex, contract, placement, providedIDs, providedPolicies); err != nil {
				return err
			}
		}
		required, err := objectListOptional(unit, "requiresInterfaces")
		if err != nil {
			return err
		}
		for contractIndex, contract := range required {
			kind, err := stringField(contract, fmt.Sprintf("%s.requiresInterfaces[%d]", unitPath, contractIndex), "kind")
			if err != nil {
				return err
			}
			if _, forbidden := contract["providerBindings"]; forbidden {
				return fail(ErrContractConflict, fmt.Sprintf("%s.requiresInterfaces[%d].providerBindings", unitPath, contractIndex), "provider bindings are compiler-owned and forbidden in catalog authority")
			}
			switch kind {
			case dockerHTTPReadonlyInterfaceKind:
				moduleRequiresProxy = true
			case dockerSocketDirectInterfaceKind:
				moduleRequiresDirect = true
				if err := registerCatalogDirectRequirement(moduleID, unitID, providerRef, contractIndex, contract, placement, evidenceRefs, len(provided) > 0, directRequirements); err != nil {
					return err
				}
			default:
				return fail(ErrContractConflict, fmt.Sprintf("%s.requiresInterfaces[%d].kind", unitPath, contractIndex), "unsupported implementation interface kind %q", kind)
			}
		}
	}
	if moduleRequiresDirect && moduleRequiresProxy {
		return fail(ErrContractConflict, modulePath+".renderUnits", "direct Docker socket access and proxied Docker HTTP access are mutually exclusive within a module")
	}
	return nil
}

func validateCatalogProvidedInterface(moduleID, unitID string, index int, contract, placement map[string]any, providedIDs, providedPolicies map[string]string) error {
	path := fmt.Sprintf("catalog.modules.%s.renderUnits.%s.providesInterfaces[%d]", moduleID, unitID, index)
	id, err := stringField(contract, path, "id")
	if err != nil {
		return err
	}
	if owner, exists := providedIDs[id]; exists {
		return fail(ErrContractConflict, path+".id", "provided implementation interface %q is already owned by %s", id, owner)
	}
	providedIDs[id] = moduleID + "/" + unitID
	kind, err := stringField(contract, path, "kind")
	if err != nil {
		return err
	}
	if kind != dockerHTTPReadonlyInterfaceKind {
		return fail(ErrContractConflict, path+".kind", "render units may only provide %q", dockerHTTPReadonlyInterfaceKind)
	}
	if err := requirePlacement(placement, path, "node-local", "one-per-daemon"); err != nil {
		return err
	}
	daemonRef, err := stringField(contract, path, "daemonRef")
	if err != nil {
		return err
	}
	placementDaemon, err := stringField(placement, path+".placement", "daemonRef")
	if err != nil {
		return err
	}
	if daemonRef != placementDaemon {
		return fail(ErrContractConflict, path+".daemonRef", "provider daemon %q does not match render-unit daemon %q", daemonRef, placementDaemon)
	}
	policy, err := stringField(contract, path, "policyProfile")
	if err != nil {
		return err
	}
	policyKey := daemonRef + "/" + policy
	if owner, exists := providedPolicies[policyKey]; exists {
		return fail(ErrContractConflict, path+".policyProfile", "Docker daemon/policy profile %q is already provided by %s", policyKey, owner)
	}
	providedPolicies[policyKey] = moduleID + "/" + unitID
	scopes, err := stringListField(contract, path, "scopes", true)
	if err != nil {
		return err
	}
	if len(scopes) != len(sortStringsUnique(scopes)) {
		return fail(ErrContractConflict, path+".scopes", "provided Docker HTTP scopes must be unique")
	}
	for _, baselineScope := range dockerHTTPReadonlyBaselineScopes {
		if !contains(scopes, baselineScope) {
			return fail(ErrContractConflict, path+".scopes", "provider is missing required baseline scope %q", baselineScope)
		}
	}
	return nil
}

func registerCatalogDirectRequirement(moduleID, unitID, providerRef string, index int, contract, placement map[string]any, evidenceRefs []string, providerBacking bool, directRequirements map[string]catalogDirectInterfaceRequirement) error {
	path := fmt.Sprintf("catalog.modules.%s.renderUnits.%s.requiresInterfaces[%d]", moduleID, unitID, index)
	if providerRef == "" {
		return fail(ErrContractConflict, path, "direct Docker socket access requires an explicitly governed module providerRef")
	}
	if err := requirePlacement(placement, path, "node-local", "one-per-daemon"); err != nil {
		return err
	}
	daemonRef, err := stringField(contract, path, "daemonRef")
	if err != nil {
		return err
	}
	placementDaemon, err := stringField(placement, path+".placement", "daemonRef")
	if err != nil {
		return err
	}
	if daemonRef != placementDaemon {
		return fail(ErrContractConflict, path+".daemonRef", "direct Docker daemon %q does not match render-unit daemon %q", daemonRef, placementDaemon)
	}
	endpoint, err := objectField(contract, path, "endpoint")
	if err != nil {
		return err
	}
	if _, err := parseDirectSocketEndpointPath(endpoint, path+".endpoint", ErrContractConflict); err != nil {
		return err
	}
	policy, err := stringField(contract, path, "policyProfile")
	if err != nil {
		return err
	}
	key := implementationApprovalSubject(moduleID, unitID, daemonRef, policy)
	if _, duplicate := directRequirements[key]; duplicate {
		return fail(ErrContractConflict, path, "direct Docker interface subject %q is duplicated", key)
	}
	directRequirements[key] = catalogDirectInterfaceRequirement{
		moduleRef: moduleID, unitRef: unitID, providerRef: providerRef,
		daemonRef: daemonRef, policy: policy, placement: placement,
		evidenceRefs: evidenceRefs, providerBacking: providerBacking,
	}
	return nil
}

func validateCatalogPrivilegedApprovals(approvals []PrivilegedInterfaceApproval, providerIDs map[string]struct{}, directRequirements map[string]catalogDirectInterfaceRequirement) error {
	approvalIDs := map[string]struct{}{}
	approvedSubjects := map[string]struct{}{}
	for index, rawApproval := range approvals {
		approval := map[string]any(rawApproval)
		path := fmt.Sprintf("catalog.privilegedInterfaceApprovals[%d]", index)
		id, err := stringField(approval, path, "id")
		if err != nil {
			return err
		}
		if _, duplicate := approvalIDs[id]; duplicate {
			return fail(ErrContractConflict, path+".id", "privileged interface approval %q is duplicated", id)
		}
		approvalIDs[id] = struct{}{}
		subject, requirement, err := matchCatalogPrivilegedApproval(approval, path, providerIDs, directRequirements)
		if err != nil {
			return err
		}
		if _, duplicate := approvedSubjects[subject]; duplicate {
			return fail(ErrContractConflict, path, "direct Docker interface subject %q has more than one approval", subject)
		}
		approvedSubjects[subject] = struct{}{}
		evidenceRef, err := stringField(approval, path, "evidenceRef")
		if err != nil {
			return err
		}
		if !contains(requirement.evidenceRefs, evidenceRef) {
			return fail(ErrContractConflict, path+".evidenceRef", "approval evidence %q is not governed by module %q", evidenceRef, requirement.moduleRef)
		}
	}
	for subject := range directRequirements {
		if _, approved := approvedSubjects[subject]; !approved {
			return fail(ErrContractConflict, "catalog.privilegedInterfaceApprovals", "direct Docker interface subject %q has no central approval", subject)
		}
	}
	return nil
}

func matchCatalogPrivilegedApproval(approval map[string]any, path string, providerIDs map[string]struct{}, directRequirements map[string]catalogDirectInterfaceRequirement) (string, catalogDirectInterfaceRequirement, error) {
	kind, err := stringField(approval, path, "kind")
	if err != nil {
		return "", catalogDirectInterfaceRequirement{}, err
	}
	if kind != dockerSocketDirectInterfaceKind {
		return "", catalogDirectInterfaceRequirement{}, fail(ErrContractConflict, path+".kind", "privileged interface approvals may only authorize %q", dockerSocketDirectInterfaceKind)
	}
	moduleRef, err := stringField(approval, path, "moduleRef")
	if err != nil {
		return "", catalogDirectInterfaceRequirement{}, err
	}
	unitRef, err := stringField(approval, path, "unitRef")
	if err != nil {
		return "", catalogDirectInterfaceRequirement{}, err
	}
	daemonRef, err := stringField(approval, path, "daemonRef")
	if err != nil {
		return "", catalogDirectInterfaceRequirement{}, err
	}
	policy, err := stringField(approval, path, "policyProfile")
	if err != nil {
		return "", catalogDirectInterfaceRequirement{}, err
	}
	subject := implementationApprovalSubject(moduleRef, unitRef, daemonRef, policy)
	requirement, exists := directRequirements[subject]
	if !exists {
		return "", catalogDirectInterfaceRequirement{}, fail(ErrContractConflict, path, "approval has no exact direct Docker interface requirement")
	}
	providerRef, err := stringField(approval, path, "providerRef")
	if err != nil {
		return "", catalogDirectInterfaceRequirement{}, err
	}
	if _, exists := providerIDs[providerRef]; !exists {
		return "", catalogDirectInterfaceRequirement{}, fail(ErrContractConflict, path+".providerRef", "approval references unknown capability provider %q", providerRef)
	}
	if providerRef != requirement.providerRef {
		return "", catalogDirectInterfaceRequirement{}, fail(ErrContractConflict, path+".providerRef", "approval provider %q does not govern module provider %q", providerRef, requirement.providerRef)
	}
	reasonCode, err := stringField(approval, path, "reasonCode")
	if err != nil {
		return "", catalogDirectInterfaceRequirement{}, err
	}
	if reasonCode == "provider-backing" && !requirement.providerBacking {
		return "", catalogDirectInterfaceRequirement{}, fail(ErrContractConflict, path+".reasonCode", "provider-backing approval requires the same render unit to provide the governed proxy interface")
	}
	if reasonCode == "lifecycle-owner" && requirement.providerBacking {
		return "", catalogDirectInterfaceRequirement{}, fail(ErrContractConflict, path+".reasonCode", "a proxy-backing render unit must use the provider-backing reason code")
	}
	return subject, requirement, nil
}

func requirePlacement(placement map[string]any, path, expectedScope, expectedCardinality string) error {
	scope, err := stringField(placement, path+".placement", "scope")
	if err != nil {
		return err
	}
	cardinality, err := stringField(placement, path+".placement", "cardinality")
	if err != nil {
		return err
	}
	if scope != expectedScope || cardinality != expectedCardinality {
		return fail(ErrContractConflict, path+".placement", "requires placement %q/%q, got %q/%q", expectedScope, expectedCardinality, scope, cardinality)
	}
	return nil
}

func implementationApprovalSubject(moduleRef, unitRef, daemonRef, policyProfile string) string {
	return moduleRef + "/" + unitRef + "/" + daemonRef + "/" + policyProfile
}
