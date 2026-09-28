package architecturev2

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/usecasecatalog"
)

// CapabilityOff turns an optional capability module off.
const CapabilityOff = "off"

// resolveUseCaseCapabilities turns closed capability-module selections into
// the existing workload selections (UseCases plus UseCaseAlternatives). It is
// a translation, not a second resolver: the result is materialized exactly
// like hand-written --use-case/--use-case-alternative intent. Without a
// capability selection the overrides are returned unchanged, so existing
// authoring keeps its exact StackSpec.
func resolveUseCaseCapabilities(contracts map[string][]usecasecatalog.Capability, catalog resolvedplan.Catalog, overrides AuthoringOverrides) (AuthoringOverrides, error) {
	if len(overrides.UseCaseCapabilities) == 0 {
		return overrides, nil
	}
	overrides.UseCases = slices.Clone(overrides.UseCases)
	overrides.UseCaseAlternatives = maps.Clone(overrides.UseCaseAlternatives)
	if overrides.UseCaseAlternatives == nil {
		overrides.UseCaseAlternatives = map[string]string{}
	}
	useCaseIDs := slices.Sorted(maps.Keys(overrides.UseCaseCapabilities))
	for _, useCaseID := range useCaseIDs {
		if !slices.ContainsFunc(overrides.UseCases, func(id string) bool { return strings.TrimSpace(id) == useCaseID }) {
			return AuthoringOverrides{}, invalidCapability("capability selection for unselected use case %q requires --use-case %s", useCaseID, useCaseID)
		}
		capabilities := contracts[useCaseID]
		if len(capabilities) == 0 {
			return AuthoringOverrides{}, invalidCapability("use case %q declares no capability modules", useCaseID)
		}
		realized, err := resolveCapabilityComposition(useCaseID, capabilities, catalog, overrides)
		if err != nil {
			return AuthoringOverrides{}, err
		}
		for _, workloadID := range slices.Sorted(maps.Keys(realized)) {
			alternative := realized[workloadID]
			if existing := strings.TrimSpace(overrides.UseCaseAlternatives[workloadID]); existing != "" && existing != alternative {
				return AuthoringOverrides{}, invalidCapability("capability selection installs %s=%s, which conflicts with --use-case-alternative %s=%s", workloadID, alternative, workloadID, existing)
			}
			overrides.UseCaseAlternatives[workloadID] = alternative
			if !slices.Contains(overrides.UseCases, workloadID) {
				overrides.UseCases = append(overrides.UseCases, workloadID)
			}
		}
	}
	return overrides, nil
}

// resolveCapabilityComposition returns workload ID -> alternative for one use
// case after enforcing the closed selection rules in a fixed order: known IDs,
// required modules, dependencies, realization, accelerator, conflicts, then
// add-on wiring. Two modules may resolve one workload only when each tool is
// part of the chosen alternative (CapabilityOption.AlternativeRefs), and an
// add-on that joins another workload's network is admitted only when the
// alternative chosen for that workload declares how it uses the add-on.
//
//nolint:gocyclo // Each branch is one closed-selection rule with its own guidance.
func resolveCapabilityComposition(useCaseID string, capabilities []usecasecatalog.Capability, catalog resolvedplan.Catalog, overrides AuthoringOverrides) (map[string]string, error) {
	selections := overrides.UseCaseCapabilities[useCaseID]
	byID := make(map[string]usecasecatalog.Capability, len(capabilities))
	known := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		byID[capability.ID] = capability
		known = append(known, capability.ID)
	}
	for _, capabilityID := range slices.Sorted(maps.Keys(selections)) {
		capability, ok := byID[capabilityID]
		if !ok {
			return nil, invalidCapability("use case %q has no capability %q; known capabilities: %s", useCaseID, capabilityID, strings.Join(known, ", "))
		}
		choice := selections[capabilityID]
		if choice == CapabilityOff {
			if capability.Required {
				return nil, invalidCapability("capability %s.%s is required and cannot be off", useCaseID, capabilityID)
			}
			continue
		}
		if _, ok := capabilityOption(capability, choice); !ok {
			return nil, invalidCapability("capability %s.%s has no option %q; choose %s or %s", useCaseID, capabilityID, choice, strings.Join(optionIDs(capability), ", "), CapabilityOff)
		}
	}
	enabled := map[string]usecasecatalog.CapabilityOption{}
	for _, capability := range capabilities {
		choice, explicit := selections[capability.ID]
		switch {
		case explicit && choice == CapabilityOff:
		case explicit:
			enabled[capability.ID], _ = capabilityOption(capability, choice)
		case capability.EnabledByDefault:
			enabled[capability.ID] = capability.Default
		}
	}
	for _, capability := range capabilities {
		if _, on := enabled[capability.ID]; !on {
			continue
		}
		for _, required := range capability.Requires {
			if _, on := enabled[required]; !on {
				return nil, invalidCapability("capability %s.%s requires %s.%s; select it with --use-case-capability %s.%s=%s", useCaseID, capability.ID, useCaseID, required, useCaseID, required, byID[required].Default.ID)
			}
		}
	}
	realized := map[string]string{}
	realizedBy := map[string]string{}
	realizedOption := map[string]usecasecatalog.CapabilityOption{}
	for _, capability := range capabilities {
		option, on := enabled[capability.ID]
		if !on {
			continue
		}
		if option.Realization != "install" {
			guidance := "omit it"
			if installable := installableOptions(useCaseID, []usecasecatalog.Capability{capability}); len(installable) > 0 {
				guidance = "choose " + strings.Join(installable, " or ")
			}
			pending := ""
			if strings.TrimSpace(option.Pending) != "" {
				pending = " (" + option.Pending + ")"
			}
			return nil, invalidCapability("capability %s.%s=%s is planned and not installable in this release%s; %s. Installable now: %s", useCaseID, capability.ID, option.ID, pending, guidance, strings.Join(installableOptions(useCaseID, capabilities), ", "))
		}
		if capability.RequiresAccelerator {
			moduleID := workloadAlternativeModule(catalog, option.WorkloadRef, option.AlternativeRef)
			if strings.TrimSpace(overrides.ModuleProfiles[moduleID].AcceleratorProfile) == "" {
				return nil, invalidCapability("capability %s.%s=%s needs a GPU; select one with --module-accelerator-profile %s=<profile>", useCaseID, capability.ID, option.ID, moduleID)
			}
		}
		if existing, taken := realized[option.WorkloadRef]; taken && existing != option.AlternativeRef {
			switch {
			case option.InstallsWith(existing):
				// This tool is part of the alternative another module chose.
				continue
			case realizedOption[option.WorkloadRef].InstallsWith(option.AlternativeRef):
				// The earlier tool is part of this alternative too: this module decides.
			default:
				return nil, invalidCapability("capability %s.%s=%s needs %s=%s, which conflicts with %s.%s needing %s=%s", useCaseID, capability.ID, option.ID, option.WorkloadRef, option.AlternativeRef, useCaseID, realizedBy[option.WorkloadRef], option.WorkloadRef, existing)
			}
		}
		realized[option.WorkloadRef] = option.AlternativeRef
		realizedBy[option.WorkloadRef] = capability.ID
		realizedOption[option.WorkloadRef] = option
	}
	// A module that is off must not be installed as part of another module's
	// workload alternative: that would be a silent no-op of the "off".
	for _, capability := range capabilities {
		if _, on := enabled[capability.ID]; on {
			continue
		}
		for _, option := range capability.Options() {
			if option.Realization == "install" && option.InstallsWith(realized[option.WorkloadRef]) {
				return nil, invalidCapability("capability %s.%s cannot be off: %s is installed together with %s.%s by %s=%s", useCaseID, capability.ID, option.Name, useCaseID, realizedBy[option.WorkloadRef], option.WorkloadRef, realized[option.WorkloadRef])
			}
		}
	}
	// An add-on joins another workload's network to be used by it. The
	// alternative chosen for that workload must declare the wiring; otherwise
	// the add-on would run unused, which is refused instead of silently done.
	for _, capability := range capabilities {
		option, on := enabled[capability.ID]
		if !on {
			continue
		}
		addOnModule := workloadAlternativeModule(catalog, option.WorkloadRef, option.AlternativeRef)
		for _, primaryRef := range modulePeerWorkloads(catalog, addOnModule) {
			primaryAlternative, realizedPrimary := realized[primaryRef]
			if !realizedPrimary || moduleWiresCompanion(catalog, workloadAlternativeModule(catalog, primaryRef, primaryAlternative), option.WorkloadRef, option.AlternativeRef) {
				continue
			}
			primaryCapability := realizedBy[primaryRef]
			guidance := fmt.Sprintf("omit %s.%s", useCaseID, capability.ID)
			usable := []string{}
			for _, candidate := range byID[primaryCapability].Options() {
				if candidate.Realization == "install" && moduleWiresCompanion(catalog, workloadAlternativeModule(catalog, candidate.WorkloadRef, candidate.AlternativeRef), option.WorkloadRef, option.AlternativeRef) {
					usable = append(usable, fmt.Sprintf("%s.%s=%s", useCaseID, primaryCapability, candidate.ID))
				}
			}
			if len(usable) > 0 {
				guidance += " or choose " + strings.Join(usable, " or ")
			}
			return nil, invalidCapability("capability %s.%s=%s is not usable with %s.%s=%s: %s does not use %s; %s", useCaseID, capability.ID, option.ID, useCaseID, primaryCapability, realizedOption[primaryRef].ID, realizedOption[primaryRef].Name, option.Name, guidance)
		}
	}
	return realized, nil
}

func capabilityOption(capability usecasecatalog.Capability, id string) (usecasecatalog.CapabilityOption, bool) {
	for _, option := range capability.Options() {
		if option.ID == id {
			return option, true
		}
	}
	return usecasecatalog.CapabilityOption{}, false
}

func optionIDs(capability usecasecatalog.Capability) []string {
	ids := []string{}
	for _, option := range capability.Options() {
		ids = append(ids, option.ID)
	}
	return ids
}

func installableOptions(useCaseID string, capabilities []usecasecatalog.Capability) []string {
	result := []string{}
	for _, capability := range capabilities {
		for _, option := range capability.Options() {
			if option.Realization == "install" {
				result = append(result, fmt.Sprintf("%s.%s=%s", useCaseID, capability.ID, option.ID))
			}
		}
	}
	sort.Strings(result)
	return result
}

func workloadAlternativeModule(catalog resolvedplan.Catalog, workloadID, alternativeID string) string {
	for _, workload := range catalog.Workloads {
		metadata, _ := workload["metadata"].(map[string]any)
		if metadata["id"] != workloadID {
			continue
		}
		alternatives, _ := workload["alternatives"].([]any)
		for _, raw := range alternatives {
			alternative, _ := raw.(map[string]any)
			if alternative["id"] == alternativeID {
				moduleID, _ := alternative["moduleRef"].(string)
				return moduleID
			}
		}
	}
	return ""
}

// catalogModule returns the Architecture v2 module contract with the given ID.
func catalogModule(catalog resolvedplan.Catalog, moduleID string) map[string]any {
	if moduleID == "" {
		return nil
	}
	for _, module := range catalog.Modules {
		metadata, _ := module["metadata"].(map[string]any)
		if metadata["id"] == moduleID {
			return module
		}
	}
	return nil
}

func moduleRuntimeComponents(catalog resolvedplan.Catalog, moduleID string) []map[string]any {
	runtime, _ := catalogModule(catalog, moduleID)["runtime"].(map[string]any)
	raw, _ := runtime["components"].([]any)
	components := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if component, ok := item.(map[string]any); ok {
			components = append(components, component)
		}
	}
	return components
}

// modulePeerWorkloads returns the workloads whose internal network a
// component of the module joins (its declared peerNetworks).
func modulePeerWorkloads(catalog resolvedplan.Catalog, moduleID string) []string {
	var refs []string
	for _, component := range moduleRuntimeComponents(catalog, moduleID) {
		peers, _ := component["peerNetworks"].([]any)
		for _, raw := range peers {
			peer, _ := raw.(map[string]any)
			if ref, _ := peer["workloadRef"].(string); ref != "" && !slices.Contains(refs, ref) {
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

// moduleWiresCompanion reports whether a component of the module declares a
// companionEnvironment entry for the add-on alternative.
func moduleWiresCompanion(catalog resolvedplan.Catalog, moduleID, workloadRef, alternativeRef string) bool {
	for _, component := range moduleRuntimeComponents(catalog, moduleID) {
		entries, _ := component["companionEnvironment"].([]any)
		for _, raw := range entries {
			entry, _ := raw.(map[string]any)
			if entry["workloadRef"] == workloadRef && entry["alternativeRef"] == alternativeRef {
				return true
			}
		}
	}
	return false
}

func invalidCapability(format string, args ...any) error {
	return resolveError(ErrInvalidStackSpec, fmt.Sprintf(format, args...), nil)
}
