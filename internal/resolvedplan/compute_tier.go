package resolvedplan

import (
	"fmt"
	"strings"
)

type kitComputeTierGraph struct {
	platformManagement  string
	moduleSubstitutions map[string]string
	enableCapabilities  []string
}

func loadKitComputeTierGraph(definition map[string]any, tier string) (*kitComputeTierGraph, error) {
	tier = trimComputeTier(tier)
	if tier == "" {
		tier = "standard"
	}
	graphs, exists, err := optionalObjectField(definition, "definition", "computeTierGraphs")
	if err != nil {
		return nil, err
	}
	if !exists {
		if tier != "standard" {
			return nil, fail(ErrInvalidInput, "spec.install.computeTier", "computeTier %q has no declared kit graph", tier)
		}
		return &kitComputeTierGraph{platformManagement: "", moduleSubstitutions: map[string]string{}}, nil
	}
	graph, exists, err := optionalObjectField(graphs, "definition.computeTierGraphs", tier)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fail(ErrInvalidInput, "spec.install.computeTier", "computeTier %q has no declared kit graph", tier)
	}
	management, err := stringField(graph, "definition.computeTierGraphs."+tier, "platformManagement")
	if err != nil {
		return nil, err
	}
	substitutions := map[string]string{}
	rawSubstitutions, hasSubstitutions, err := optionalObjectField(graph, "definition.computeTierGraphs."+tier, "moduleSubstitutions")
	if err != nil {
		return nil, err
	}
	if hasSubstitutions {
		for source, rawTarget := range rawSubstitutions {
			target, ok := rawTarget.(string)
			if !ok || source == "" || target == "" {
				return nil, fail(ErrInvalidInput, "definition.computeTierGraphs."+tier+".moduleSubstitutions", "module substitution %q is not a contract ID", source)
			}
			substitutions[source] = target
		}
	}
	enable, err := stringListField(graph, "definition.computeTierGraphs."+tier, "enableCapabilities", false)
	if err != nil {
		return nil, err
	}
	return &kitComputeTierGraph{
		platformManagement:  management,
		moduleSubstitutions: substitutions,
		enableCapabilities:  enable,
	}, nil
}

func trimComputeTier(tier string) string {
	return strings.TrimSpace(tier)
}

func applyComputeTierModuleSubstitutions(selection *providerModuleSelection, resolved *resolution, catalog *indexedCatalog, substitutions map[string]string) error {
	if len(substitutions) == 0 {
		return nil
	}
	for source, target := range substitutions {
		if _, selected := selection.selected[source]; !selected {
			continue
		}
		if _, exists := catalog.modules[target]; !exists {
			return fail(ErrUnknownModule, "spec.install.computeTier", "module substitution target %q is not a governed catalog module", target)
		}
		providerID, governed := selection.governed[target]
		if !governed {
			declaredProvider, hasProvider, err := optionalStringField(catalog.modules[target], "catalog.modules."+target, "providerRef")
			if err != nil {
				return err
			}
			if !hasProvider {
				return fail(ErrUnrealizedModule, "spec.install.computeTier", "module substitution target %q is not governed", target)
			}
			providerID = declaredProvider
			if err := registerSubstitutedModule(selection, catalog, providerID, target); err != nil {
				return err
			}
		}
		delete(selection.selected, source)
		selection.selected[target] = providerID
		if requiredProvider, required := selection.required[source]; required {
			delete(selection.required, source)
			selection.required[target] = requiredProvider
		}
		for _, workload := range resolved.workloads {
			if workload.moduleID != source {
				continue
			}
			delete(resolved.workloadByModule, source)
			workload.moduleID = target
			workload.providerID = providerID
			resolved.workloadByModule[target] = workload.id
		}
	}
	return nil
}

func registerSubstitutedModule(selection *providerModuleSelection, catalog *indexedCatalog, providerID, moduleID string) error {
	if _, exists := catalog.providers[providerID]; !exists {
		return fail(ErrUnknownProvider, "spec.install.computeTier", "module substitution target %q names unknown provider %q", moduleID, providerID)
	}
	if existing, exists := selection.governed[moduleID]; exists && existing != providerID {
		return fail(ErrContractConflict, "spec.install.computeTier", "module %q is governed more than once by %q and %q", moduleID, existing, providerID)
	}
	selection.governed[moduleID] = providerID
	selection.optional[moduleID] = providerID
	return nil
}

func applyComputeTierWorkloadSubstitutions(resolved *resolution, catalog *indexedCatalog, substitutions map[string]string) error {
	if len(substitutions) == 0 {
		return nil
	}
	for _, workload := range resolved.workloads {
		target, exists := substitutions[workload.moduleID]
		if !exists {
			continue
		}
		alternativeID, alternative, err := workloadAlternativeForModule(workload.contract, workload.id, target)
		if err != nil {
			return err
		}
		providerID, err := stringField(alternative, "catalog.workloads."+workload.id+".alternatives."+alternativeID, "providerRef")
		if err != nil {
			return err
		}
		if err := validateWorkloadImplementation(workload.id, providerID, target, workload.contract, alternative, catalog); err != nil {
			return err
		}
		adapterID, adapterProviderID, adapterModuleID, err := resolveWorkloadRuntimeAdapter(workload.id, map[string]any{}, alternative, target, catalog)
		if err != nil {
			return err
		}
		fallbackIDs, err := resolveWorkloadRuntimeFallbacks(workload.id, map[string]any{}, alternative, adapterID)
		if err != nil {
			return err
		}
		delete(resolved.workloadByModule, workload.moduleID)
		workload.alternativeID = alternativeID
		workload.alternative = alternative
		workload.moduleID = target
		workload.providerID = providerID
		workload.runtimeAdapterID = adapterID
		workload.runtimeAdapterProviderID = adapterProviderID
		workload.runtimeAdapterModuleID = adapterModuleID
		workload.runtimeAdapterFallbackIDs = fallbackIDs
		resolved.workloadByModule[target] = workload.id
	}
	return nil
}

func workloadAlternativeForModule(contract map[string]any, workloadID, moduleID string) (string, map[string]any, error) {
	alternatives, err := objectListField(contract, "catalog.workloads."+workloadID, "alternatives")
	if err != nil {
		return "", nil, err
	}
	var matchID string
	var match map[string]any
	for index, alternative := range alternatives {
		path := fmt.Sprintf("catalog.workloads.%s.alternatives[%d]", workloadID, index)
		id, err := stringField(alternative, path, "id")
		if err != nil {
			return "", nil, err
		}
		ref, err := stringField(alternative, path, "moduleRef")
		if err != nil {
			return "", nil, err
		}
		if ref != moduleID {
			continue
		}
		if match != nil {
			return "", nil, fail(ErrContractConflict, "spec.install.computeTier", "module %q is declared by multiple %s alternatives", moduleID, workloadID)
		}
		matchID, match = id, alternative
	}
	if match == nil {
		return "", nil, fail(ErrUnknownWorkloadAlternative, "spec.install.computeTier", "workload %q has no alternative for substituted module %q", workloadID, moduleID)
	}
	return matchID, match, nil
}

func computeTierFromInstall(install map[string]any) (string, error) {
	computeTier, exists, err := optionalStringField(install, "spec.install", "computeTier")
	if err != nil {
		return "", err
	}
	if !exists || trimComputeTier(computeTier) == "" {
		return "standard", nil
	}
	return trimComputeTier(computeTier), nil
}

// WorkloadComputeTierFit is the catalog binding of one workload to one
// install.computeTier graph. Undeclared fits admit the default alternative.
type WorkloadComputeTierFit struct {
	Declared      bool
	Included      bool
	AlternativeID string
	Reason        string
}

// CatalogWorkloadComputeTierFit reads #WorkloadContractV2.computeTiers.
// Missing computeTiers or a missing tier admits the caller to use defaultAlternative.
func CatalogWorkloadComputeTierFit(contract map[string]any, tier string) WorkloadComputeTierFit {
	tier = trimComputeTier(tier)
	if tier == "" {
		tier = "standard"
	}
	if contract == nil {
		return WorkloadComputeTierFit{Included: true}
	}
	raw, _ := contract["computeTiers"].(map[string]any)
	if raw == nil {
		return WorkloadComputeTierFit{Included: true}
	}
	fit, _ := raw[tier].(map[string]any)
	if fit == nil {
		return WorkloadComputeTierFit{Included: true}
	}
	included, _ := fit["included"].(bool)
	if !included {
		reason, _ := fit["reason"].(string)
		return WorkloadComputeTierFit{Declared: true, Included: false, Reason: strings.TrimSpace(reason)}
	}
	alternativeID, _ := fit["alternativeID"].(string)
	return WorkloadComputeTierFit{
		Declared:      true,
		Included:      true,
		AlternativeID: strings.TrimSpace(alternativeID),
	}
}
