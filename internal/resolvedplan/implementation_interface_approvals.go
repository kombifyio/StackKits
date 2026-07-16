package resolvedplan

import (
	"fmt"
	"sort"
)

func (c *Compiler) resolvePrivilegedInterfaceApprovals(modules []any, gates map[string]any) ([]any, error) {
	evidenceByScenario, err := resolvedEvidenceGates(gates)
	if err != nil {
		return nil, err
	}
	result := []any{}
	for _, rawModule := range modules {
		module := rawModule.(map[string]any)
		moduleID, err := stringField(module, "modules", "id")
		if err != nil {
			return nil, err
		}
		providerRef, _, err := optionalStringField(module, "modules."+moduleID, "providerRef")
		if err != nil {
			return nil, err
		}
		units, err := objectListField(module, "modules."+moduleID, "renderUnits")
		if err != nil {
			return nil, err
		}
		for _, unit := range units {
			approvals, err := c.resolveUnitPrivilegedApprovals(moduleID, providerRef, unit, evidenceByScenario)
			if err != nil {
				return nil, err
			}
			result = append(result, approvals...)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].(map[string]any)["id"].(string) < result[j].(map[string]any)["id"].(string)
	})
	return result, nil
}

func (c *Compiler) resolveUnitPrivilegedApprovals(moduleID, providerRef string, unit map[string]any, evidenceByScenario map[string]string) ([]any, error) {
	unitID, err := stringField(unit, "modules."+moduleID+".renderUnits", "id")
	if err != nil {
		return nil, err
	}
	path := "modules." + moduleID + ".renderUnits." + unitID
	requirements, err := objectListOptional(unit, "requiresInterfaces")
	if err != nil {
		return nil, err
	}
	var result []any
	for index, requirement := range requirements {
		kind, err := stringField(requirement, fmt.Sprintf("%s.requiresInterfaces[%d]", path, index), "kind")
		if err != nil {
			return nil, err
		}
		if kind != dockerSocketDirectInterfaceKind {
			continue
		}
		approval, err := c.resolveDirectInterfaceApproval(moduleID, unitID, providerRef, unit, requirement, evidenceByScenario)
		if err != nil {
			return nil, err
		}
		result = append(result, approval)
	}
	return result, nil
}

func (c *Compiler) resolveDirectInterfaceApproval(moduleID, unitID, providerRef string, unit, requirement map[string]any, evidenceByScenario map[string]string) (map[string]any, error) {
	daemonRef, err := stringField(requirement, "requiresInterfaces", "daemonRef")
	if err != nil {
		return nil, err
	}
	policy, err := stringField(requirement, "requiresInterfaces", "policyProfile")
	if err != nil {
		return nil, err
	}
	subject := implementationApprovalSubject(moduleID, unitID, daemonRef, policy)
	var matches []map[string]any
	for _, approval := range c.catalog.privilegedInterfaceApprovals {
		approvalModule, _ := approval["moduleRef"].(string)
		approvalUnit, _ := approval["unitRef"].(string)
		approvalProvider, _ := approval["providerRef"].(string)
		approvalDaemon, _ := approval["daemonRef"].(string)
		approvalPolicy, _ := approval["policyProfile"].(string)
		if approvalModule == moduleID && approvalUnit == unitID && approvalProvider == providerRef && approvalDaemon == daemonRef && approvalPolicy == policy {
			matches = append(matches, approval)
		}
	}
	if len(matches) != 1 {
		return nil, fail(ErrContractConflict, "privilegedInterfaceApprovals", "direct Docker interface subject %q resolves to %d central approvals; exactly one is required", subject, len(matches))
	}
	resolved, err := cloneObject(matches[0], true)
	if err != nil {
		return nil, err
	}
	nodeRefs, err := stringListField(unit, "modules."+moduleID+".renderUnits."+unitID, "nodeRefs", true)
	if err != nil {
		return nil, err
	}
	siteRefs, err := stringListField(unit, "modules."+moduleID+".renderUnits."+unitID, "siteRefs", true)
	if err != nil {
		return nil, err
	}
	evidenceRef, err := stringField(resolved, "privilegedInterfaceApprovals", "evidenceRef")
	if err != nil {
		return nil, err
	}
	evidenceGateRef, exists := evidenceByScenario[evidenceRef]
	if !exists {
		return nil, fail(ErrContractConflict, "privilegedInterfaceApprovals.evidenceRef", "approval evidence scenario %q has no resolved evidence gate", evidenceRef)
	}
	resolved["nodeRefs"] = stringSliceAny(sortStringsUnique(nodeRefs))
	resolved["siteRefs"] = stringSliceAny(sortStringsUnique(siteRefs))
	resolved["evidenceGateRef"] = evidenceGateRef
	return resolved, nil
}

func resolvedEvidenceGates(gates map[string]any) (map[string]string, error) {
	evidence, err := objectListField(gates, "gates", "evidence")
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(evidence))
	for index, gate := range evidence {
		path := fmt.Sprintf("gates.evidence[%d]", index)
		scenario, err := stringField(gate, path, "scenario")
		if err != nil {
			return nil, err
		}
		gateID, err := stringField(gate, path, "id")
		if err != nil {
			return nil, err
		}
		if _, duplicate := result[scenario]; duplicate {
			return nil, fail(ErrContractConflict, path+".scenario", "evidence scenario %q resolves more than once", scenario)
		}
		result[scenario] = gateID
	}
	return result, nil
}
