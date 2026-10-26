package architecturev2

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
)

// UseCaseChange leaves omitted fields and all unrelated desired intent intact.
// Settings are public JSON values; secret inputs contain references only.
type UseCaseChange struct {
	Alternative, RuntimeAdapterRef, NodeRef string
	Profiles                                ModuleProfileOverride
	Settings                                map[string]any
	SecretRefs                              map[string]string
}

func moduleProfileIntent(profile ModuleProfileOverride) map[string]any {
	intent := map[string]any{}
	for key, value := range map[string]string{"computeProfile": profile.ComputeProfile, "storageProfile": profile.StorageProfile, "acceleratorProfile": profile.AcceleratorProfile} {
		if value != "" {
			intent[key] = value
		}
	}
	return intent
}

func useCaseChangePlacement(spec map[string]any, selected string) (string, string, error) {
	nodes, _ := spec["nodes"].([]any)
	var candidates []map[string]any
	for _, raw := range nodes {
		node, _ := raw.(map[string]any)
		if enabled, declared := node["enabled"].(bool); declared && !enabled {
			continue
		}
		if selected == "" || node["id"] == selected {
			candidates = append(candidates, node)
		}
	}
	if len(candidates) != 1 {
		return "", "", resolveError(ErrInvalidStackSpec, "select exactly one existing enabled node with --node; no node is provisioned by intent authoring", nil)
	}
	site, _ := candidates[0]["siteRef"].(string)
	node, _ := candidates[0]["id"].(string)
	return site, node, nil
}

func workloadSelectionIncludesNode(spec, entry map[string]any, site, node string) bool {
	placement, _ := entry["placement"].(map[string]any)
	if refs, _ := placement["siteRefs"].([]any); len(refs) > 0 && !slices.Contains(refs, any(site)) {
		return false
	}
	if refs, _ := placement["nodeRefs"].([]any); len(refs) > 0 && !slices.Contains(refs, any(node)) {
		return false
	}
	roles, _ := placement["requiresRoles"].([]any)
	nodes, _ := spec["nodes"].([]any)
	// Unpinned placement is resolved by the compiler's scheduling policy. Raw
	// authoring cannot assume that it belongs to every node at that site.
	if refs, _ := placement["nodeRefs"].([]any); len(refs) == 0 {
		eligible := 0
		for _, raw := range nodes {
			candidate, _ := raw.(map[string]any)
			if enabled, declared := candidate["enabled"].(bool); declared && !enabled {
				continue
			}
			actual, _ := candidate["roles"].([]any)
			if candidate["siteRef"] == site && !slices.ContainsFunc(roles, func(role any) bool { return !slices.Contains(actual, role) }) {
				eligible++
			}
		}
		if eligible != 1 {
			return false
		}
	}
	for _, raw := range nodes {
		candidate, _ := raw.(map[string]any)
		if candidate["id"] != node {
			continue
		}
		actual, _ := candidate["roles"].([]any)
		return !slices.ContainsFunc(roles, func(role any) bool { return !slices.Contains(actual, role) })
	}
	return false
}

func mergeUseCaseInputs(entry map[string]any, change UseCaseChange) error {
	if len(change.Settings) > 0 {
		settings := initialAuthoringObject(entry, "settings")
		for key, value := range change.Settings {
			settings[key] = value
		}
	}
	if len(change.SecretRefs) > 0 {
		refs := initialAuthoringObject(entry, "secretRefs")
		for key, ref := range change.SecretRefs {
			if !strings.HasPrefix(ref, "secret://") || strings.TrimSpace(ref) != ref {
				return resolveError(ErrInvalidStackSpec, fmt.Sprintf("input %q requires a secret:// reference, never secret material", key), nil)
			}
			refs[key] = ref
		}
	}
	return nil
}

func (s *Service) configureRetainedUseCase(spec map[string]any, slug string, entry map[string]any, change UseCaseChange) (StackSpecValidation, error) {
	alternative, _ := entry["alternative"].(string)
	if change.Alternative != "" && change.Alternative != alternative {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, "changing an existing application's alternative requires an explicit data migration; configure retains its current application and data", nil)
	}
	adapter, _ := entry["runtimeAdapterRef"].(string)
	if change.RuntimeAdapterRef != "" && change.RuntimeAdapterRef != adapter {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, "changing an existing application's runtime adapter requires an explicit migration; configure retains its current delivery identity", nil)
	}
	if change.NodeRef != "" {
		placement, _ := entry["placement"].(map[string]any)
		refs, _ := placement["nodeRefs"].([]any)
		if len(refs) != 1 || refs[0] != change.NodeRef {
			return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, "changing an existing application's node requires an explicit relocation; configure retains its placement", nil)
		}
	}
	moduleID := workloadAlternativeModule(s.authority.catalog, slug, alternative)
	module, exists := nativeCatalogModule(s.authority.catalog, moduleID)
	if !exists {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, "selected use case has no catalog module", nil)
	}
	if err := validatePublicConfigurationSettings(s.authority.catalog, slug, alternative, change.Settings); err != nil {
		return StackSpecValidation{}, err
	}
	modules, _ := spec["modules"].(map[string]any)
	retainedModule, _ := modules[moduleID].(map[string]any)
	moduleIntent := map[string]any{}
	for key, value := range retainedModule {
		moduleIntent[key] = value
	}
	for key, value := range moduleProfileIntent(change.Profiles) {
		moduleIntent[key] = value
	}
	// The current canonical spec has already been validated. Configure validates
	// only requested profile names; omitted profiles retain the compiler's
	// existing interpretation without inventing an explicit selection.
	for field, selected := range map[string]string{"computeProfiles": change.Profiles.ComputeProfile, "storageProfiles": change.Profiles.StorageProfile, "acceleratorProfiles": change.Profiles.AcceleratorProfile} {
		if selected != "" {
			if _, err := nativeAuthoringProfile(module, moduleID, field, selected, false); err != nil {
				return StackSpecValidation{}, err
			}
		}
	}
	if change.Profiles == (ModuleProfileOverride{}) && len(change.Settings) == 0 && len(change.SecretRefs) == 0 {
		return s.validateChangedIntent(spec)
	}
	if change.Profiles != (ModuleProfileOverride{}) {
		modules = initialAuthoringObject(spec, "modules")
		modules[moduleID] = moduleIntent
	}
	if err := mergeUseCaseInputs(entry, change); err != nil {
		return StackSpecValidation{}, err
	}
	return s.validateChangedIntent(spec)
}

// CapabilityChange supplements the existing Foundation through its canonical
// capability selection. Execution still belongs to normal plan/apply or Advanced.
type CapabilityChange struct {
	Enable      bool
	ProviderRef string
	Settings    map[string]any
	SecretRefs  map[string]string
}

func (s *Service) MaterializeCapabilityChange(raw []byte, id string, change CapabilityChange) (StackSpecValidation, error) {
	current, err := s.ValidateStackSpec(raw)
	if err != nil {
		return StackSpecValidation{}, err
	}
	spec, err := resolvedplan.DecodeDocument[map[string]any](current.CanonicalStackSpec)
	if err != nil {
		return StackSpecValidation{}, err
	}
	if spec["apiVersion"] != stackspecmigration.APIVersionV2Alpha2 {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, "capability authoring requires native stackkit/v2alpha2", nil)
	}
	id = strings.TrimSpace(id)
	definition := s.authority.definitions[current.KitProfile]
	policy, _ := definition["capabilities"].(map[string]any)
	declared := false
	for _, field := range []string{"required", "defaults", "optional"} {
		declared = declared || nativeStringListContains(policy[field], id)
	}
	if !declared {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, fmt.Sprintf("capability %q is not declared by %s", id, current.KitProfile), nil)
	}
	capabilities := initialAuthoringObject(spec, "capabilities")
	if change.Enable {
		disabled, _ := capabilities["disable"].([]any)
		if disabled == nil {
			disabled = []any{}
		}
		capabilities["disable"] = slices.DeleteFunc(disabled, func(raw any) bool { return raw == id })
		if err := appendNestedStringList(spec, []string{id}, "capabilities", "enable"); err != nil {
			return StackSpecValidation{}, err
		}
	}
	config := initialAuthoringObject(capabilities, "config")
	entry := initialAuthoringObject(config, id)
	if change.ProviderRef != "" {
		entry["providerRef"] = change.ProviderRef
	}
	if err := mergeUseCaseInputs(entry, UseCaseChange{Settings: change.Settings, SecretRefs: change.SecretRefs}); err != nil {
		return StackSpecValidation{}, err
	}
	return s.validateChangedIntent(spec)
}

func (s *Service) validateChangedIntent(spec map[string]any) (StackSpecValidation, error) {
	candidate, err := resolvedplan.CanonicalJSON(spec)
	if err != nil {
		return StackSpecValidation{}, err
	}
	return s.ValidateStackSpec(candidate)
}

// MaterializeUseCaseRemoval retires one optional selection after removal or
// authors the candidate for an Advanced change set. Data placement and secret
// references outside that selection remain available for explicit recovery.
// Resolve must admit the candidate before any runtime removal is dispatched.
func (s *Service) MaterializeUseCaseRemoval(raw []byte, slug string) (StackSpecValidation, error) {
	current, err := s.ValidateStackSpec(raw)
	if err != nil {
		return StackSpecValidation{}, err
	}
	spec, err := resolvedplan.DecodeDocument[map[string]any](current.CanonicalStackSpec)
	if err != nil {
		return StackSpecValidation{}, err
	}
	if spec["apiVersion"] != stackspecmigration.APIVersionV2Alpha2 {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, "incremental removal requires native stackkit/v2alpha2", nil)
	}
	slug = strings.TrimSpace(slug)
	definition := s.authority.definitions[current.KitProfile]
	policy, _ := definition["workloads"].(map[string]any)
	if !nativeStringListContains(policy["optional"], slug) {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, "incremental removal requires an optional workload; required Foundation workloads remain selected", nil)
	}
	workloads := initialAuthoringObject(spec, "workloads")
	entry, exists := workloads[slug].(map[string]any)
	if !exists {
		return current, nil
	}
	alternative, _ := entry["alternative"].(string)
	moduleID := workloadAlternativeModule(s.authority.catalog, slug, alternative)
	if moduleID == "" {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, "removed workload has no exact catalog module", nil)
	}
	for id, raw := range workloads {
		if id == slug {
			continue
		}
		consumer, _ := raw.(map[string]any)
		alternative, _ := consumer["alternative"].(string)
		module := workloadAlternativeModule(s.authority.catalog, id, alternative)
		if slices.Contains(modulePeerWorkloads(s.authority.catalog, module), slug) {
			return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, fmt.Sprintf("workload %q still consumes %q's runtime network; remove the complete use case or retire that consumer first", id, slug), nil)
		}
	}
	for _, contract := range s.authority.catalog.Workloads {
		metadata, _ := contract["metadata"].(map[string]any)
		if metadata["id"] != slug {
			continue
		}
		alternatives, _ := contract["alternatives"].([]any)
		for _, raw := range alternatives {
			selected, _ := raw.(map[string]any)
			adapter, _ := selected["providesRuntimeAdapterRef"].(string)
			if selected["id"] != alternative || adapter == "" {
				continue
			}
			for id, raw := range workloads {
				consumer, _ := raw.(map[string]any)
				if id != slug && effectiveSelectedWorkloadAdapter(s.authority.catalog, id, consumer) == adapter {
					return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, fmt.Sprintf("workload %q still uses runtime adapter %q supplied by %q; migrate that consumer before removing its platform", id, adapter, slug), nil)
				}
			}
		}
	}
	delete(workloads, slug)
	modules, _ := spec["modules"].(map[string]any)
	delete(modules, moduleID)
	// Routes are selected by their exact module owner. Access policies are user
	// intent and remain available for reuse/recovery, even when no route uses one.
	routes, _ := spec["routes"].(map[string]any)
	for id, raw := range routes {
		route, _ := raw.(map[string]any)
		if route["moduleRef"] == moduleID {
			delete(routes, id)
		}
	}
	return s.validateChangedIntent(spec)
}

func effectiveSelectedWorkloadAdapter(catalog resolvedplan.Catalog, id string, entry map[string]any) string {
	if adapter, _ := entry["runtimeAdapterRef"].(string); adapter != "" {
		return adapter
	}
	for _, contract := range catalog.Workloads {
		metadata, _ := contract["metadata"].(map[string]any)
		if metadata["id"] != id {
			continue
		}
		alternatives, _ := contract["alternatives"].([]any)
		for _, raw := range alternatives {
			alternative, _ := raw.(map[string]any)
			if alternative["id"] != entry["alternative"] {
				continue
			}
			runtime, _ := alternative["runtime"].(map[string]any)
			adapter, _ := runtime["defaultAdapterRef"].(string)
			return adapter
		}
	}
	return ""
}

// MaterializeWorkloadRemovals retires a caller-selected, exact workload set in
// memory. The caller resolves the complete remaining graph and persists once,
// after its signed runtime removal or transfer outcome is verified.
func (s *Service) MaterializeWorkloadRemovals(raw []byte, workloads []string) (StackSpecValidation, error) {
	if len(workloads) == 0 {
		return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, "workload removal requires a non-empty exact selection", nil)
	}
	var candidate StackSpecValidation
	seen := map[string]bool{}
	for _, workload := range workloads {
		if seen[workload] {
			return StackSpecValidation{}, resolveError(ErrInvalidStackSpec, "workload removal selection contains duplicates", nil)
		}
		seen[workload] = true
		var err error
		candidate, err = s.MaterializeUseCaseRemoval(raw, workload)
		if err != nil {
			return candidate, err
		}
		raw = candidate.CanonicalStackSpec
	}
	return candidate, nil
}
