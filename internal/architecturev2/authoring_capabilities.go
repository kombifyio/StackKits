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
// required modules, dependencies, realization, accelerator, then conflicts.
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
			return nil, invalidCapability("capability %s.%s=%s is planned and not installable in this release; %s. Installable now: %s", useCaseID, capability.ID, option.ID, guidance, strings.Join(installableOptions(useCaseID, capabilities), ", "))
		}
		if capability.RequiresAccelerator {
			moduleID := workloadAlternativeModule(catalog, option.WorkloadRef, option.AlternativeRef)
			if strings.TrimSpace(overrides.ModuleProfiles[moduleID].AcceleratorProfile) == "" {
				return nil, invalidCapability("capability %s.%s=%s needs a GPU; select one with --module-accelerator-profile %s=<profile>", useCaseID, capability.ID, option.ID, moduleID)
			}
		}
		if existing, taken := realized[option.WorkloadRef]; taken && existing != option.AlternativeRef {
			return nil, invalidCapability("capability %s.%s=%s needs %s=%s, which conflicts with %s.%s needing %s=%s", useCaseID, capability.ID, option.ID, option.WorkloadRef, option.AlternativeRef, useCaseID, realizedBy[option.WorkloadRef], option.WorkloadRef, existing)
		}
		realized[option.WorkloadRef] = option.AlternativeRef
		realizedBy[option.WorkloadRef] = capability.ID
	}
	// A module that is off must not be installed as part of another module's
	// workload alternative: that would be a silent no-op of the "off".
	for _, capability := range capabilities {
		if _, on := enabled[capability.ID]; on {
			continue
		}
		for _, option := range capability.Options() {
			if option.Realization == "install" && realized[option.WorkloadRef] == option.AlternativeRef {
				return nil, invalidCapability("capability %s.%s cannot be off: %s is installed together with %s.%s by %s=%s", useCaseID, capability.ID, option.Name, useCaseID, realizedBy[option.WorkloadRef], option.WorkloadRef, option.AlternativeRef)
			}
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

func invalidCapability(format string, args ...any) error {
	return resolveError(ErrInvalidStackSpec, fmt.Sprintf(format, args...), nil)
}
