package generationartifact

import (
	"fmt"
	"slices"
	"sort"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

func appendApplyProviderOwnerRequirements(plan resolvedplan.ResolvedPlan, targetNodes map[string]struct{}, result *ApplyRequirements) error {
	siteByNode := map[string]string{}
	rawNodes, ok := plan["nodes"].([]any)
	if !ok {
		return fail(ErrInvalidPlan, "resolvedPlan.nodes", "must be an array")
	}
	for index, raw := range rawNodes {
		path := fmt.Sprintf("resolvedPlan.nodes[%d]", index)
		node, ok := raw.(map[string]any)
		if !ok {
			return fail(ErrInvalidPlan, path, "must be an object")
		}
		nodeRef, err := requiredString(node, "id", path+".id")
		if err != nil {
			return err
		}
		siteRef, err := requiredString(node, "siteRef", path+".siteRef")
		if err != nil {
			return err
		}
		enabled, ok := node["enabled"].(bool)
		if !ok {
			return fail(ErrInvalidPlan, path+".enabled", "must be boolean")
		}
		if !enabled {
			continue
		}
		siteByNode[nodeRef] = siteRef
	}
	rawProviders, ok := plan["providers"].([]any)
	if !ok {
		return fail(ErrInvalidPlan, "resolvedPlan.providers", "must be an array")
	}
	for index, raw := range rawProviders {
		path := fmt.Sprintf("resolvedPlan.providers[%d]", index)
		provider, ok := raw.(map[string]any)
		if !ok {
			return fail(ErrInvalidPlan, path, "must be an object")
		}
		providerID, err := requiredString(provider, "id", path+".id")
		if err != nil {
			return err
		}
		providerContractHash, err := requiredString(provider, "contractHash", path+".contractHash")
		if err != nil {
			return err
		}
		providerSiteRefs, err := applyStringList(provider, "siteRefs", path+".siteRefs")
		if err != nil {
			return err
		}
		owner, exists := provider["owner"].(map[string]any)
		if !exists {
			continue
		}
		ownerKind, err := requiredString(owner, "kind", path+".owner.kind")
		if err != nil {
			return err
		}
		ownerRef, err := requiredString(owner, "ref", path+".owner.ref")
		if err != nil {
			return err
		}
		ownerVersion, err := requiredString(owner, "version", path+".owner.version")
		if err != nil {
			return err
		}
		ownerContractHash, err := requiredString(owner, "contractHash", path+".owner.contractHash")
		if err != nil {
			return err
		}
		healthGateRefs, err := applyStringList(owner, "healthGateRefs", path+".owner.healthGateRefs")
		if err != nil {
			return err
		}
		evidenceGateRefs, err := applyStringList(owner, "evidenceGateRefs", path+".owner.evidenceGateRefs")
		if err != nil {
			return err
		}
		evidenceRefs, err := applyRealizationEvidenceRefs(owner, path+".owner")
		if err != nil {
			return err
		}
		for _, evidenceRef := range evidenceRefs {
			result.EvidenceRequirements = append(result.EvidenceRequirements, ApplyEvidenceRequirement{ID: "provider-owner/" + providerID + "/" + evidenceRef, OwnerKind: "provider-owner", OwnerRef: ownerRef, Ref: evidenceRef})
		}
		ownerRequirement := ApplyProviderOwnerRequirement{
			ID: providerID, Ref: ownerRef, Version: ownerVersion, Kind: ownerKind, ContractHash: ownerContractHash,
			SiteRefs: append([]string(nil), providerSiteRefs...), EvidenceRefs: append([]string(nil), evidenceRefs...),
			HealthGateRefs: append([]string(nil), healthGateRefs...), EvidenceGateRefs: append([]string(nil), evidenceGateRefs...),
		}
		ownerNodeRefs, err := applyProviderOwnerNodeRefs(providerID, healthGateRefs, providerSiteRefs, siteByNode, result.HealthRequirements)
		if err != nil {
			return err
		}
		healthGateRefs, err = materializeApplyProviderOwnerHealthRequirements(
			providerID,
			ownerNodeRefs,
			siteByNode,
			healthGateRefs,
			&result.HealthRequirements,
		)
		if err != nil {
			return err
		}
		ownerRequirement.HealthGateRefs = append([]string(nil), healthGateRefs...)
		for _, nodeRef := range ownerNodeRefs {
			siteRef := siteByNode[nodeRef]
			ownerRequirement.NodeRefs = append(ownerRequirement.NodeRefs, nodeRef)
			targetNodes[nodeRef] = struct{}{}
			result.RuntimeInstances = append(result.RuntimeInstances, ApplyRuntimeRequirement{
				ID: "provider-owner/" + providerID + "/" + nodeRef, OwnerKind: "provider-owner", OwnerRef: ownerRef,
				OwnerVersion: ownerVersion, OwnerContractHash: ownerContractHash, ProviderRef: providerID, ProviderContractHash: providerContractHash, InstanceRef: nodeRef,
				RuntimeKind: ownerKind, RuntimeDelivery: "provider-owner", SiteRefs: []string{siteRef}, NodeRefs: []string{nodeRef},
				HealthGateRefs: append([]string(nil), healthGateRefs...), EvidenceGateRefs: append([]string(nil), evidenceGateRefs...),
				ArtifactRefs: []string{},
			})
		}
		result.ProviderOwners = append(result.ProviderOwners, ownerRequirement)
		inputs, err := requiredObject(owner, "inputs", path+".owner.inputs")
		if err != nil {
			return err
		}
		secretRefs, err := applyStringMap(inputs, "secretRefs", path+".owner.inputs.secretRefs")
		if err != nil {
			return err
		}
		realization, err := requiredObject(provider, "realization", path+".realization")
		if err != nil {
			return err
		}
		inputBindings, err := requiredObject(realization, "inputBindings", path+".realization.inputBindings")
		if err != nil {
			return err
		}
		for inputRef, secretRef := range secretRefs {
			bindingPath := path + ".realization.inputBindings." + inputRef
			rawBinding, exists := inputBindings[inputRef]
			if !exists {
				return fail(ErrInvalidPlan, bindingPath, "provider-owner secret input has no explicit capability-secret binding")
			}
			binding, ok := rawBinding.(map[string]any)
			if !ok {
				return fail(ErrInvalidPlan, bindingPath, "must be an object")
			}
			source, err := requiredString(binding, "source", bindingPath+".source")
			if err != nil {
				return err
			}
			if source != "capability-secret" {
				return fail(ErrInvalidPlan, bindingPath+".source", "provider-owner secret input must use capability-secret, got %q", source)
			}
			sourceRef, err := requiredString(binding, "capabilityRef", bindingPath+".capabilityRef")
			if err != nil {
				return err
			}
			sourceInputRef, err := requiredString(binding, "key", bindingPath+".key")
			if err != nil {
				return err
			}
			result.Secrets = append(result.Secrets, ApplySecretRequirement{ID: "provider-owner/" + providerID + "/" + inputRef, SourceKind: "capability", SourceRef: sourceRef, SourceInputRef: sourceInputRef, OwnerKind: "provider-owner", OwnerRef: ownerRef, InputRef: inputRef, SecretRef: secretRef, SiteRefs: append([]string(nil), providerSiteRefs...), NodeRefs: append([]string(nil), ownerRequirement.NodeRefs...)})
		}
	}
	return nil
}

func applyProviderOwnerNodeRefs(providerID string, healthGateRefs, providerSiteRefs []string, siteByNode map[string]string, healthRequirements []ApplyHealthRequirement) ([]string, error) {
	allowedSites := make(map[string]struct{}, len(providerSiteRefs))
	for _, siteRef := range providerSiteRefs {
		allowedSites[siteRef] = struct{}{}
	}
	healthByID := make(map[string]ApplyHealthRequirement, len(healthRequirements))
	for _, health := range healthRequirements {
		healthByID[health.ID] = health
	}
	nodeSet := map[string]struct{}{}
	for _, gateRef := range healthGateRefs {
		health, exists := healthByID[gateRef]
		if !exists || health.TargetKind != "provider" || health.TargetRef != providerID {
			return nil, fail(ErrInvalidPlan, "resolvedPlan.providers."+providerID+".owner.healthGateRefs", "gate %q is not an exact health requirement for this provider", gateRef)
		}
		if len(health.NodeRefs) == 0 {
			return nil, fail(ErrInvalidPlan, "resolvedPlan.gates.health."+gateRef+".nodeRefs", "provider-owner health gate must retain its exact target nodes")
		}
		for _, nodeRef := range health.NodeRefs {
			siteRef, exists := siteByNode[nodeRef]
			if !exists {
				return nil, fail(ErrInvalidPlan, "resolvedPlan.gates.health."+gateRef+".nodeRefs", "targets absent or disabled node %q", nodeRef)
			}
			if _, allowed := allowedSites[siteRef]; !allowed {
				return nil, fail(ErrInvalidPlan, "resolvedPlan.gates.health."+gateRef+".nodeRefs", "targets node %q outside provider sites", nodeRef)
			}
			nodeSet[nodeRef] = struct{}{}
		}
	}
	if len(nodeSet) == 0 {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.providers."+providerID+".owner.healthGateRefs", "provider owner has no exact target nodes")
	}
	return sortedApplyMapKeys(nodeSet), nil
}

func materializeApplyProviderOwnerHealthRequirements(
	providerID string,
	nodeRefs []string,
	siteByNode map[string]string,
	gateRefs []string,
	healthRequirements *[]ApplyHealthRequirement,
) ([]string, error) {
	healthByID := make(map[string]ApplyHealthRequirement, len(*healthRequirements))
	for _, health := range *healthRequirements {
		healthByID[health.ID] = health
	}
	replaced := make(map[string]struct{})
	materialized := make([]ApplyHealthRequirement, 0)
	resultRefs := make([]string, 0, len(gateRefs)*len(nodeRefs))
	for _, gateRef := range gateRefs {
		health, exists := healthByID[gateRef]
		if !exists || health.TargetKind != "provider" || health.TargetRef != providerID {
			return nil, fail(ErrInvalidPlan, "resolvedPlan.providers."+providerID+".owner.healthGateRefs", "gate %q is not an exact health requirement for this provider", gateRef)
		}
		if len(health.SiteRefs) == 1 && len(health.NodeRefs) == 1 {
			resultRefs = append(resultRefs, gateRef)
			continue
		}
		replaced[gateRef] = struct{}{}
		for _, nodeRef := range nodeRefs {
			siteRef := siteByNode[nodeRef]
			if !slices.Contains(health.SiteRefs, siteRef) || !slices.Contains(health.NodeRefs, nodeRef) {
				continue
			}
			runtimeID := "provider-owner/" + providerID + "/" + nodeRef
			owned := health
			owned.ID = health.ID + "/runtime/" + runtimeID
			owned.RuntimeRequirementID = runtimeID
			owned.SiteRefs = []string{siteRef}
			owned.NodeRefs = []string{nodeRef}
			materialized = append(materialized, owned)
			resultRefs = append(resultRefs, owned.ID)
		}
	}
	if len(replaced) != 0 {
		retained := make([]ApplyHealthRequirement, 0, len(*healthRequirements)-len(replaced)+len(materialized))
		for _, health := range *healthRequirements {
			if _, remove := replaced[health.ID]; !remove {
				retained = append(retained, health)
			}
		}
		*healthRequirements = append(retained, materialized...)
	}
	sort.Strings(resultRefs)
	return resultRefs, nil
}

func validateApplyProviderOwnerClosure(requirements ApplyRequirements) error {
	owners := make(map[string]ApplyProviderOwnerRequirement, len(requirements.ProviderOwners))
	for _, owner := range requirements.ProviderOwners {
		if _, duplicate := owners[owner.ID]; duplicate {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.providerOwners", "duplicate provider-owner requirement %q", owner.ID)
		}
		owners[owner.ID] = owner
	}
	healthByID := make(map[string][]ApplyHealthRequirement, len(requirements.HealthRequirements))
	for _, health := range requirements.HealthRequirements {
		healthByID[health.ID] = append(healthByID[health.ID], health)
	}
	evidenceByGateRef := make(map[string][]ApplyEvidenceRequirement, len(requirements.EvidenceRequirements))
	for _, evidence := range requirements.EvidenceRequirements {
		if evidence.GateRef != "" {
			evidenceByGateRef[evidence.GateRef] = append(evidenceByGateRef[evidence.GateRef], evidence)
		}
	}
	runtimeNodesByOwner := make(map[string]map[string]struct{}, len(owners))
	for _, runtime := range requirements.RuntimeInstances {
		if runtime.OwnerKind != "provider-owner" {
			if runtime.OwnerVersion != "" || len(runtime.HealthGateRefs) != 0 || len(runtime.EvidenceGateRefs) != 0 {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID, "non-provider runtime must not carry provider-owner identity or gates")
			}
			continue
		}
		owner, exists := owners[runtime.ProviderRef]
		if !exists {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".providerRef", "provider-owner runtime has no exact owner requirement")
		}
		if runtime.OwnerRef != owner.Ref || runtime.OwnerVersion != owner.Version || runtime.OwnerContractHash != owner.ContractHash ||
			runtime.RuntimeKind != owner.Kind || runtime.RuntimeDelivery != "provider-owner" ||
			!slices.Equal(runtime.HealthGateRefs, owner.HealthGateRefs) || !slices.Equal(runtime.EvidenceGateRefs, owner.EvidenceGateRefs) {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID, "provider-owner runtime does not retain the exact owner identity and gates")
		}
		if len(runtime.SiteRefs) != 1 || !slices.Contains(owner.SiteRefs, runtime.SiteRefs[0]) ||
			len(runtime.NodeRefs) != 1 || runtime.InstanceRef != runtime.NodeRefs[0] || !slices.Contains(owner.NodeRefs, runtime.NodeRefs[0]) {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances."+runtime.ID+".nodeRefs", "provider-owner runtime does not retain one exact owner target site and node")
		}
		if runtimeNodesByOwner[owner.ID] == nil {
			runtimeNodesByOwner[owner.ID] = map[string]struct{}{}
		}
		if _, duplicate := runtimeNodesByOwner[owner.ID][runtime.NodeRefs[0]]; duplicate {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.runtimeInstances", "provider-owner %q has duplicate runtime target node %q", owner.ID, runtime.NodeRefs[0])
		}
		runtimeNodesByOwner[owner.ID][runtime.NodeRefs[0]] = struct{}{}
	}
	for _, owner := range requirements.ProviderOwners {
		seenHealth := make(map[string]struct{}, len(owner.HealthGateRefs))
		for _, gateRef := range owner.HealthGateRefs {
			if _, duplicate := seenHealth[gateRef]; duplicate {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.providerOwners."+owner.ID+".healthGateRefs", "duplicate health gate %q", gateRef)
			}
			seenHealth[gateRef] = struct{}{}
			matches := healthByID[gateRef]
			if len(matches) != 1 || matches[0].TargetKind != "provider" || matches[0].TargetRef != owner.ID {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.providerOwners."+owner.ID+".healthGateRefs", "gate %q is not one exact materialized health gate for this provider owner", gateRef)
			}
		}
		seenEvidence := make(map[string]struct{}, len(owner.EvidenceGateRefs))
		for _, gateRef := range owner.EvidenceGateRefs {
			if _, duplicate := seenEvidence[gateRef]; duplicate {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.providerOwners."+owner.ID+".evidenceGateRefs", "duplicate evidence gate %q", gateRef)
			}
			seenEvidence[gateRef] = struct{}{}
			if len(evidenceByGateRef[gateRef]) != 1 {
				return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.providerOwners."+owner.ID+".evidenceGateRefs", "gate %q is not one exact materialized evidence gate", gateRef)
			}
		}
		if len(runtimeNodesByOwner[owner.ID]) != len(owner.NodeRefs) {
			return fail(ErrInvalidPlan, "resolvedPlan.applyRequirements.providerOwners."+owner.ID+".nodeRefs", "provider owner does not have exactly one runtime target per owner node")
		}
	}
	return nil
}
