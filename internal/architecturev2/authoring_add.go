package architecturev2

import (
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
)

// MaterializeUseCaseAddition authors only the requested workload against an
// existing native installation. Existing intent is never regenerated from the
// kit's initial spec, and existing selections are treated as already applied.
func (s *Service) MaterializeUseCaseAddition(raw []byte, slug string) (StackSpecValidation, error) {
	current, err := s.ValidateStackSpec(raw)
	if err != nil {
		return StackSpecValidation{}, err
	}
	spec, err := resolvedplan.DecodeDocument[map[string]any](current.CanonicalStackSpec)
	if err != nil {
		return StackSpecValidation{}, err
	}
	if spec["apiVersion"] != stackspecmigration.APIVersionV2Alpha2 {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, "use-cases add requires native stackkit/v2alpha2 intent; migrate the existing spec explicitly first", nil)
	}
	// Adding local intent must not introduce a standalone fleet executor.
	sites, _ := spec["sites"].([]any)
	nodes, _ := spec["nodes"].([]any)
	install, _ := spec["install"].(map[string]any)
	if install["mode"] == "advanced" || current.KitProfile == stackspecmigration.KitProfileModern || len(sites) != 1 || len(nodes) != 1 {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, "multi-server and multi-site changes require Techstack Advanced dispatch", nil)
	}
	slug = strings.TrimSpace(slug)
	workloads := initialAuthoringObject(spec, "workloads")
	if existing, exists := workloads[slug]; exists {
		entry, _ := existing.(map[string]any)
		if enabled, declared := entry["enabled"].(bool); declared && !enabled {
			return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, fmt.Sprintf("use case %q is explicitly disabled; review its existing intent before enabling it", slug), nil)
		}
		return current, nil
	}
	definition := s.authority.definitions[current.KitProfile]
	overrides := AuthoringOverrides{
		APIVersion: stackspecmigration.APIVersionV2Alpha2, CatalogDefaults: true,
		UseCases: []string{slug}, Platform: "standalone-compose",
		ModuleProfiles: map[string]ModuleProfileOverride{},
	}
	selections, err := resolveNativeSelectedWorkloads(current.KitProfile, definition, s.authority.catalog, overrides, false)
	if err != nil {
		return StackSpecValidation{}, err
	}
	combined := make(map[string]useCaseWorkloadSelection, len(workloads)+len(selections))
	for id := range workloads {
		combined[id] = useCaseWorkloadSelection{}
	}
	for id, selection := range selections {
		combined[id] = selection
	}
	if err := refuseSharedExclusiveNodeUseCases(s.authority.catalog, combined); err != nil {
		return StackSpecValidation{}, err
	}
	modules := initialAuthoringObject(spec, "modules")
	for _, id := range sortedSelectionKeys(selections) {
		selection := selections[id]
		if existing, exists := modules[selection.ModuleRef]; exists {
			entry, _ := existing.(map[string]any)
			if enabled, declared := entry["enabled"].(bool); declared && !enabled {
				return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, fmt.Sprintf("module %q is explicitly disabled", selection.ModuleRef), nil)
			}
		} else {
			profile := overrides.ModuleProfiles[selection.ModuleRef]
			modules[selection.ModuleRef] = map[string]any{"computeProfile": profile.ComputeProfile}
		}
		site, err := initialWorkloadSiteRef(spec, id)
		if err != nil {
			return StackSpecValidation{}, err
		}
		entry := map[string]any{
			"alternative": selection.Alternative,
			"placement":   map[string]any{"siteRefs": []any{site}, "nodeRefs": []any{}, "requiresRoles": []any{}},
		}
		if selection.RuntimeAdapterRef != "" {
			entry["runtimeAdapterRef"] = selection.RuntimeAdapterRef
		}
		if len(selection.RequiredSecretRefs) > 0 {
			refs := map[string]any{}
			for _, ref := range selection.RequiredSecretRefs {
				refs[ref] = fmt.Sprintf("secret://workloads/%s/%s", id, ref)
			}
			entry["secretRefs"] = refs
		}
		workloads[id] = entry
	}
	authoring, _ := definition["authoring"].(map[string]any)
	if err := projectInitialWorkloadAccess(spec, authoring, selections); err != nil {
		return StackSpecValidation{}, err
	}
	candidate, err := resolvedplan.CanonicalJSON(spec)
	if err != nil {
		return StackSpecValidation{}, err
	}
	// Authoring validates desired intent; observed runtime admission belongs
	// to resolve/generate/apply with the installation's actual Inventory.
	return s.ValidateStackSpec(candidate)
}
