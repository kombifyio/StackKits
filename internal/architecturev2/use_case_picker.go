package architecturev2

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/kombifyio/stackkits/internal/capacityfit"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
	"github.com/kombifyio/stackkits/internal/usecasecatalog"
)

// PickerWorkload is a numeric preview of the catalog's default alternative at
// its standard compute profile. It is neither authored intent nor admission.
type PickerWorkload struct {
	ID          string
	Title       string
	Alternative string
	Module      capacityfit.Module
	Default     bool
}

type UseCasePickerCatalog struct {
	Required []PickerWorkload
	Optional []PickerWorkload
}

// EmbeddedUseCasePickerCatalog filters the hash-verified use-case projection
// through the same kit workload policy and module contracts used by authoring.
// Internal workloads and add-ons remain outside this main-use-case picker.
func EmbeddedUseCasePickerCatalog(kit, nodeRef string) (UseCasePickerCatalog, error) {
	result := UseCasePickerCatalog{}
	if nodeRef == "" {
		return result, fmt.Errorf("picker capacity requires a node reference")
	}
	if stackspecmigration.KitProfile(kit) == stackspecmigration.KitProfileModern {
		return result, fmt.Errorf("Modern Homelab requires a placed resolved plan; the native picker previews one server only")
	}
	authority, err := loadEmbeddedAuthority()
	if err != nil {
		return result, err
	}
	definition, found := authority.definitions[stackspecmigration.KitProfile(kit)]
	if !found {
		return result, fmt.Errorf("embedded product authority has no kit definition for %q", kit)
	}
	manifest, err := readEmbeddedManifest()
	if err != nil {
		return result, err
	}
	path, err := cleanBundleRelativePath(manifest.Documents["computeTierFits"])
	if err != nil {
		return result, err
	}
	data, err := embeddedBundleFS.ReadFile(embeddedProductBundleRoot + "/" + path)
	if err != nil {
		return result, err
	}
	var document struct {
		UseCases []usecasecatalog.UseCase `json:"useCases"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return result, err
	}
	policy, _ := definition["workloads"].(map[string]any)
	groups := map[string][]string{}
	for _, group := range []string{"required", "defaults", "optional", "forbidden"} {
		values, _ := policy[group].([]any)
		for _, value := range values {
			if id, ok := value.(string); ok {
				groups[group] = append(groups[group], id)
			}
		}
	}
	for _, id := range groups["required"] {
		entry, err := pickerWorkload(authority.catalog, id, id, nodeRef)
		if err != nil {
			return result, err
		}
		result.Required = append(result.Required, entry)
	}
	for _, useCase := range document.UseCases {
		id := useCase.ID
		if slices.Contains(groups["required"], id) || slices.Contains(groups["forbidden"], id) ||
			(!slices.Contains(groups["optional"], id) && !slices.Contains(groups["defaults"], id)) {
			continue
		}
		entry, err := pickerWorkload(authority.catalog, id, useCase.Title, nodeRef)
		if err != nil {
			return result, err
		}
		entry.Default = slices.Contains(groups["defaults"], id)
		result.Optional = append(result.Optional, entry)
	}
	if len(result.Required) == 0 || len(result.Optional) == 0 {
		return result, fmt.Errorf("kit %q has no required Core or selectable main use cases", kit)
	}
	return result, nil
}

func pickerWorkload(catalog resolvedplan.Catalog, id, title, nodeRef string) (PickerWorkload, error) {
	entry := PickerWorkload{ID: id, Title: title}
	for _, workload := range catalog.Workloads {
		metadata, _ := workload["metadata"].(map[string]any)
		if metadata["id"] != id {
			continue
		}
		entry.Alternative, _ = workload["defaultAlternative"].(string)
		moduleID := workloadAlternativeModule(catalog, id, entry.Alternative)
		module, found := nativeCatalogModule(catalog, moduleID)
		if !found {
			return entry, fmt.Errorf("picker workload %q has no catalog-bound default module", id)
		}
		entry.Module = capacityfit.Module{ID: moduleID, NodeRefs: []string{nodeRef}, CapacityDeclaration: "not_declared"}
		profiles, _ := module["computeProfiles"].(map[string]any)
		profile, found := profiles["standard"].(map[string]any)
		if !found {
			return entry, nil // An absent standard profile is unknown, never a substitute profile.
		}
		projected, err := resolvedplan.ProjectCapacityModules([]any{map[string]any{
			"id": moduleID, "nodeRefs": []any{nodeRef}, "computeProfileBinding": profile,
		}})
		if err != nil {
			return entry, err
		}
		entry.Module = projected[0]
		return entry, nil
	}
	return entry, fmt.Errorf("picker workload %q is absent from the product catalog", id)
}
