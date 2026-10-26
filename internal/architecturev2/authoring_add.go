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
	return s.MaterializeUseCaseChange(raw, slug, UseCaseChange{}, true)
}

// MaterializeUseCaseChange modifies one selection in retained native intent.
// It authors no runtime operation; managed callers submit the resulting candidate
// through the existing capability-gated Advanced change-set lifecycle.
func (s *Service) MaterializeUseCaseChange(raw []byte, slug string, change UseCaseChange, add bool) (StackSpecValidation, error) {
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
	slug = strings.TrimSpace(slug)
	workloads := initialAuthoringObject(spec, "workloads")
	if existing, exists := workloads[slug]; exists {
		entry, _ := existing.(map[string]any)
		if enabled, declared := entry["enabled"].(bool); add && declared && !enabled {
			return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, fmt.Sprintf("use case %q is explicitly disabled; review its existing intent before enabling it", slug), nil)
		}
		return s.configureRetainedUseCase(spec, slug, entry, change)
	}
	if !add {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, fmt.Sprintf("use case %q is not selected; add it explicitly first", slug), nil)
	}
	definition := s.authority.definitions[current.KitProfile]
	overrides := AuthoringOverrides{
		APIVersion: stackspecmigration.APIVersionV2Alpha2, CatalogDefaults: true,
		UseCases:       []string{slug},
		ModuleProfiles: map[string]ModuleProfileOverride{},
	}
	if change.Alternative != "" {
		overrides.UseCaseAlternatives = map[string]string{slug: change.Alternative}
	}
	overrides.Platform = change.RuntimeAdapterRef
	// Resolve the selected module before applying its independently chosen profiles.
	moduleRef := workloadAlternativeModule(s.authority.catalog, slug, change.Alternative)
	if moduleRef == "" && change.Alternative == "" {
		for _, contract := range s.authority.catalog.Workloads {
			metadata, _ := contract["metadata"].(map[string]any)
			if metadata["id"] == slug {
				alternative, _ := contract["defaultAlternative"].(string)
				moduleRef = workloadAlternativeModule(s.authority.catalog, slug, alternative)
			}
		}
	}
	if moduleRef != "" {
		overrides.ModuleProfiles[moduleRef] = change.Profiles
	}
	selections, err := resolveNativeSelectedWorkloads(current.KitProfile, definition, s.authority.catalog, overrides, false)
	if err != nil {
		return StackSpecValidation{}, err
	}
	combined := make(map[string]useCaseWorkloadSelection, len(workloads)+len(selections))
	platformSelections := make(map[string]useCaseWorkloadSelection, len(workloads)+len(selections))
	targetSite, targetNode, err := useCaseChangePlacement(spec, change.NodeRef)
	if err != nil {
		return StackSpecValidation{}, err
	}
	for id, raw := range workloads {
		entry, _ := raw.(map[string]any)
		if enabled, declared := entry["enabled"].(bool); declared && !enabled {
			continue
		}
		alternative, _ := entry["alternative"].(string)
		combined[id] = useCaseWorkloadSelection{Alternative: alternative}
		if workloadSelectionIncludesNode(spec, entry, targetSite, targetNode) {
			platformSelections[id] = combined[id]
		}
	}
	for id, selection := range selections {
		combined[id] = selection
		platformSelections[id] = selection
	}
	if err := selectNewWorkloadPlatform(s.authority.catalog, platformSelections, selections); err != nil {
		return StackSpecValidation{}, err
	}
	nodes, _ := spec["nodes"].([]any)
	if len(nodes) == 1 {
		if err := refuseSharedExclusiveNodeUseCases(s.authority.catalog, combined); err != nil {
			return StackSpecValidation{}, err
		}
	}
	modules := initialAuthoringObject(spec, "modules")
	for _, id := range sortedSelectionKeys(selections) {
		selection := selections[id]
		if existing, exists := modules[selection.ModuleRef]; exists {
			entry, _ := existing.(map[string]any)
			if enabled, declared := entry["enabled"].(bool); declared && !enabled {
				return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, fmt.Sprintf("module %q is explicitly disabled", selection.ModuleRef), nil)
			}
			for field, requested := range moduleProfileIntent(change.Profiles) {
				if entry[field] != requested {
					return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, fmt.Sprintf("module %q already has a different %s; an addition cannot change an existing module consumer's profile", selection.ModuleRef, field), nil)
				}
			}
		} else {
			profile := overrides.ModuleProfiles[selection.ModuleRef]
			modules[selection.ModuleRef] = moduleProfileIntent(profile)
		}
		site, node := targetSite, targetNode
		authoritySite, err := initialWorkloadSiteRef(spec, id)
		if err != nil {
			return StackSpecValidation{}, err
		}
		if site != authoritySite {
			return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, fmt.Sprintf("selected node site %q differs from workload %q data authority %q; declare an explicit data placement before adding it", site, id, authoritySite), nil)
		}
		entry := map[string]any{
			"alternative": selection.Alternative,
			"placement":   map[string]any{"siteRefs": []any{site}, "nodeRefs": []any{node}, "requiresRoles": []any{}},
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
		if err := mergeUseCaseInputs(entry, change); err != nil {
			return StackSpecValidation{}, err
		}
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
