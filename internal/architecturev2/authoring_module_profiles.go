package architecturev2

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
)

// ModuleProfileOverride is typed authoring intent, never an inventory-derived
// choice. Storage and accelerator are independent, optional catalog dimensions.
type ModuleProfileOverride struct {
	ComputeProfile     string `json:"computeProfile"`
	StorageProfile     string `json:"storageProfile,omitempty"`
	AcceleratorProfile string `json:"acceleratorProfile,omitempty"`
}

func nativeModuleProfileAuthoring(overrides AuthoringOverrides) (bool, error) {
	switch strings.TrimSpace(overrides.APIVersion) {
	case "", stackspecmigration.APIVersionV2Alpha1:
		if len(overrides.ModuleProfiles) != 0 || len(overrides.UseCaseAlternatives) != 0 {
			return false, resolveError(ErrInvalidStackSpec, "module profiles and explicit alternatives require apiVersion stackkit/v2alpha2", nil)
		}
		return false, nil
	case stackspecmigration.APIVersionV2Alpha2:
		if strings.TrimSpace(overrides.ComputeTier) != "" {
			return false, resolveError(ErrInvalidStackSpec, "native v2alpha2 forbids install.computeTier; select a compute profile for each module", nil)
		}
		return true, nil
	default:
		return false, resolveError(ErrInvalidStackSpec, fmt.Sprintf("unsupported authoring apiVersion %q", overrides.APIVersion), nil)
	}
}

func resolveNativeWorkloadSelections(profile stackspecmigration.KitProfile, definition resolvedplan.KitDefinition, catalog resolvedplan.Catalog, overrides AuthoringOverrides) (map[string]useCaseWorkloadSelection, error) {
	policy, _ := definition["workloads"].(map[string]any)
	allowed, selected := map[string]bool{}, map[string]bool{}
	for _, field := range []string{"required", "defaults", "optional"} {
		values, _ := policy[field].([]any)
		for _, raw := range values {
			id, _ := raw.(string)
			allowed[id] = true
			if field != "optional" {
				selected[id] = true
			}
		}
	}
	if authoring, ok := definition["authoring"].(map[string]any); ok {
		if initial, ok := authoring["initialSpec"].(map[string]any); ok {
			if workloads, ok := initial["workloads"].(map[string]any); ok {
				for id := range workloads {
					selected[id] = true
				}
			}
		}
	}
	for _, raw := range overrides.UseCases {
		id := strings.TrimSpace(raw)
		if id == "" || !allowed[id] {
			return nil, resolveError(ErrInvalidStackSpec, fmt.Sprintf("use case %q is not declared by %s", id, profile), nil)
		}
		selected[id] = true
	}
	for id := range overrides.UseCaseAlternatives {
		if !selected[id] {
			return nil, resolveError(ErrInvalidStackSpec, fmt.Sprintf("alternative for unselected use case %q requires --use-case", id), nil)
		}
	}
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make(map[string]useCaseWorkloadSelection, len(ids))
	selectedModules := map[string]bool{}
	for _, id := range ids {
		alternativeID := strings.TrimSpace(overrides.UseCaseAlternatives[id])
		if alternativeID == "" {
			return nil, resolveError(ErrInvalidStackSpec, fmt.Sprintf("native v2alpha2 requires --use-case-alternative %s=<alternative>", id), nil)
		}
		var contract, alternative map[string]any
		for _, candidate := range catalog.Workloads {
			metadata, _ := candidate["metadata"].(map[string]any)
			if metadata["id"] == id {
				contract = map[string]any(candidate)
				break
			}
		}
		alternatives, _ := contract["alternatives"].([]any)
		for _, raw := range alternatives {
			candidate, _ := raw.(map[string]any)
			if candidate["id"] == alternativeID {
				alternative = candidate
				break
			}
		}
		if alternative == nil {
			return nil, resolveError(ErrInvalidStackSpec, fmt.Sprintf("use case %q does not declare alternative %q", id, alternativeID), nil)
		}
		moduleID, _ := alternative["moduleRef"].(string)
		moduleOverride := overrides.ModuleProfiles[moduleID]
		var module map[string]any
		for _, candidate := range catalog.Modules {
			metadata, _ := candidate["metadata"].(map[string]any)
			if metadata["id"] == moduleID {
				module = map[string]any(candidate)
				break
			}
		}
		if module == nil {
			return nil, resolveError(ErrAuthorityLoad, fmt.Sprintf("use case %q references undeclared module %q", id, moduleID), nil)
		}
		computeProfile, err := resolveNativeModuleProfileOverride(moduleID, module, moduleOverride)
		if err != nil {
			return nil, err
		}
		platformManagement, _ := computeProfile["platformManagement"].(string)
		adapter := ""
		allowedAdapters := workloadAlternativeAllowedAdapterRefs(contract, alternativeID)
		if strings.TrimSpace(overrides.Platform) != "" && len(allowedAdapters) > 0 {
			adapter = strings.TrimSpace(overrides.Platform)
			if !containsStringValue(allowedAdapters, adapter) {
				return nil, resolveError(ErrInvalidStackSpec, fmt.Sprintf("platform %q is not allowed for use case %q alternative %q", adapter, id, alternativeID), nil)
			}
		}
		selectedModules[moduleID] = true
		result[id] = useCaseWorkloadSelection{
			Alternative: alternativeID, RuntimeAdapterRef: adapter,
			RequiredSecretRefs: workloadAlternativeRequiredSecretRefs(contract, alternativeID),
			PlatformManagement: platformManagement,
		}
	}
	for moduleID, moduleOverride := range overrides.ModuleProfiles {
		if selectedModules[moduleID] {
			continue
		}
		module, exists := nativeCatalogModule(catalog, moduleID)
		if !exists {
			return nil, resolveError(ErrInvalidStackSpec, fmt.Sprintf("module profile %q is not declared by the catalog", moduleID), nil)
		}
		role, _ := module["role"].(string)
		if role == "workload" {
			return nil, resolveError(ErrInvalidStackSpec, fmt.Sprintf("module profile %q does not belong to a selected workload alternative", moduleID), nil)
		}
		if !nativeCatalogModuleGoverned(catalog, moduleID, module) {
			return nil, resolveError(ErrInvalidStackSpec, fmt.Sprintf("module profile %q is not governed by a provider realization", moduleID), nil)
		}
		if _, err := resolveNativeModuleProfileOverride(moduleID, module, moduleOverride); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func resolveNativeModuleProfileOverride(moduleID string, module map[string]any, override ModuleProfileOverride) (map[string]any, error) {
	_, hasComputeProfiles := module["computeProfiles"].(map[string]any)
	computeProfile, err := nativeAuthoringProfile(module, moduleID, "computeProfiles", override.ComputeProfile, hasComputeProfiles)
	if err != nil {
		return nil, err
	}
	for _, axis := range []struct{ field, value string }{{"storageProfiles", override.StorageProfile}, {"acceleratorProfiles", override.AcceleratorProfile}} {
		if _, err := nativeAuthoringProfile(module, moduleID, axis.field, axis.value, false); err != nil {
			return nil, err
		}
	}
	return computeProfile, nil
}

func nativeCatalogModule(catalog resolvedplan.Catalog, moduleID string) (map[string]any, bool) {
	for _, candidate := range catalog.Modules {
		module := map[string]any(candidate)
		metadata, _ := module["metadata"].(map[string]any)
		if metadata != nil && metadata["id"] == moduleID {
			return module, true
		}
	}
	return nil, false
}

// nativeCatalogModuleGoverned mirrors the compiler's provider-realization
// edge: an explicit non-workload module may only refine a module listed by its
// declared provider's required or optional module references.
func nativeCatalogModuleGoverned(catalog resolvedplan.Catalog, moduleID string, module map[string]any) bool {
	providerRef, _ := module["providerRef"].(string)
	if strings.TrimSpace(providerRef) == "" {
		return false
	}
	for _, candidate := range catalog.Providers {
		provider := map[string]any(candidate)
		metadata, _ := provider["metadata"].(map[string]any)
		if metadata == nil || metadata["id"] != providerRef {
			continue
		}
		realization, _ := provider["realization"].(map[string]any)
		if realization == nil || realization["kind"] != "modules" {
			return false
		}
		moduleRefs, _ := realization["moduleRefs"].(map[string]any)
		if moduleRefs == nil {
			return false
		}
		for _, field := range []string{"required", "optional"} {
			if nativeStringListContains(moduleRefs[field], moduleID) {
				return true
			}
		}
		return false
	}
	return false
}

func nativeStringListContains(raw any, want string) bool {
	switch values := raw.(type) {
	case []any:
		for _, value := range values {
			if value == want {
				return true
			}
		}
	case []string:
		for _, value := range values {
			if value == want {
				return true
			}
		}
	}
	return false
}

func nativeAuthoringProfile(module map[string]any, moduleID, field, selected string, required bool) (map[string]any, error) {
	profiles, declared := module[field].(map[string]any)
	selected = strings.TrimSpace(selected)
	if selected == "" && !required && !declared {
		return nil, nil
	}
	if selected == "" {
		return nil, resolveError(ErrInvalidStackSpec, fmt.Sprintf("module %q requires an explicit %s selection", moduleID, field), nil)
	}
	value, exists := profiles[selected].(map[string]any)
	if !declared || !exists {
		return nil, resolveError(ErrInvalidStackSpec, fmt.Sprintf("module %q does not declare %s profile %q", moduleID, field, selected), nil)
	}
	return value, nil
}

func applyNativeModuleProfiles(spec map[string]any, overrides AuthoringOverrides, workloads map[string]useCaseWorkloadSelection) error {
	spec["apiVersion"] = stackspecmigration.APIVersionV2Alpha2
	install, _ := spec["install"].(map[string]any)
	if install == nil {
		return resolveError(ErrAuthorityLoad, "initial StackSpec has no install object", nil)
	}
	delete(install, "computeTier")
	management := ""
	for _, workload := range workloads {
		if workload.PlatformManagement == "" {
			continue
		}
		if management != "" && management != workload.PlatformManagement {
			return resolveError(ErrInvalidStackSpec, "selected module profiles require conflicting platform management", nil)
		}
		management = workload.PlatformManagement
	}
	if management != "" {
		if err := setNestedString(spec, management, "install", "platform", "management"); err != nil {
			return resolveError(ErrAuthorityLoad, "apply module-owned platform management: "+err.Error(), err)
		}
	}
	modules, _ := spec["modules"].(map[string]any)
	if modules == nil {
		modules = map[string]any{}
		spec["modules"] = modules
	}
	for moduleID, selection := range overrides.ModuleProfiles {
		intent := map[string]any{"computeProfile": strings.TrimSpace(selection.ComputeProfile)}
		if value := strings.TrimSpace(selection.StorageProfile); value != "" {
			intent["storageProfile"] = value
		}
		if value := strings.TrimSpace(selection.AcceleratorProfile); value != "" {
			intent["acceleratorProfile"] = value
		}
		modules[moduleID] = intent
	}
	return nil
}
