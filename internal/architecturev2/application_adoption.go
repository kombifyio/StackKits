package architecturev2

import (
	"encoding/json"
	"fmt"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// PrepareApplicationAdoption extends validated intent only in memory. It uses
// the same CUE workload selection and access projection as initial authoring;
// it neither persists desired intent nor generates or applies runtime files.
func (s *Service) PrepareApplicationAdoption(raw []byte, workload, siteRef, nodeRef string) (StackSpecValidation, error) {
	if workload != "smart-home" {
		return StackSpecValidation{}, fmt.Errorf("unsupported_source: no application adoption preparation profile exists")
	}
	validation, err := s.ValidateStackSpec(raw)
	if err != nil {
		return StackSpecValidation{}, err
	}
	spec, err := resolvedplan.DecodeDocument[map[string]any](validation.CanonicalStackSpec)
	if err != nil {
		return StackSpecValidation{}, err
	}
	workloads := initialAuthoringObject(spec, "workloads")
	if _, selected := workloads[workload]; selected {
		return validation, nil
	}
	if spec["apiVersion"] != "stackkit/v2alpha2" {
		return StackSpecValidation{}, fmt.Errorf("unsupported_source: application adoption preparation requires native v2alpha2 intent")
	}
	definition := s.authority.definitions[validation.KitProfile]
	overrides := AuthoringOverrides{CatalogDefaults: true, APIVersion: "stackkit/v2alpha2", ModuleProfiles: map[string]ModuleProfileOverride{}, UseCaseAlternatives: map[string]string{workload: "home-assistant"}, UseCases: []string{workload}}
	for id, raw := range workloads {
		entry, _ := raw.(map[string]any)
		alternative, _ := entry["alternative"].(string)
		overrides.UseCases = append(overrides.UseCases, id)
		overrides.UseCaseAlternatives[id] = alternative
	}
	modules := initialAuthoringObject(spec, "modules")
	for id, raw := range modules {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return StackSpecValidation{}, err
		}
		var profile ModuleProfileOverride
		if err := json.Unmarshal(encoded, &profile); err != nil {
			return StackSpecValidation{}, err
		}
		overrides.ModuleProfiles[id] = profile
	}
	selections, err := resolveNativeWorkloadSelections(validation.KitProfile, definition, s.authority.catalog, overrides)
	if err != nil {
		return StackSpecValidation{}, err
	}
	selection, selected := selections[workload]
	if !selected || selection.ModuleRef == "" {
		return StackSpecValidation{}, fmt.Errorf("unsupported_source: kit has no admitted adoption workload selection")
	}
	profile := overrides.ModuleProfiles[selection.ModuleRef]
	modules[selection.ModuleRef] = map[string]any{"enabled": true, "computeProfile": profile.ComputeProfile}
	workloads[workload] = map[string]any{"alternative": selection.Alternative, "runtimeAdapterRef": "standalone-compose", "placement": map[string]any{"siteRefs": []any{siteRef}, "nodeRefs": []any{nodeRef}, "requiresRoles": []any{}}}
	data := initialAuthoringObject(spec, "data")
	bindings := initialAuthoringObject(data, "bindings")
	endpointData, _ := selection.ServiceEndpoint["data"].(map[string]any)
	ref, _ := endpointData["bindingRef"].(string)
	classes, _ := endpointData["requiredClasses"].([]any)
	if ref == "" || len(classes) == 0 {
		return StackSpecValidation{}, fmt.Errorf("unsupported_source: selected module has no declared preservation data binding")
	}
	if _, exists := bindings[ref]; exists {
		return StackSpecValidation{}, fmt.Errorf("source_identity_conflict: unselected adoption workload already has data intent")
	}
	bindings[ref] = map[string]any{"classes": classes, "primarySiteRef": siteRef}
	authoring, _ := definition["authoring"].(map[string]any)
	if err := projectInitialWorkloadAccess(spec, authoring, map[string]useCaseWorkloadSelection{workload: selection}); err != nil {
		return StackSpecValidation{}, err
	}
	candidate, err := resolvedplan.CanonicalJSON(spec)
	if err != nil {
		return StackSpecValidation{}, err
	}
	return s.ValidateStackSpec(candidate)
}
