package commands

import (
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
)

func architectureV2InitAPIVersion() string {
	if value := strings.TrimSpace(initAPIVersion); value != "" {
		return value
	}
	return stackspecmigration.APIVersionV2Alpha2
}

func parseInitSelections(flag string, values []string) (map[string]string, error) {
	result := make(map[string]string, len(values))
	for _, raw := range values {
		key, value, found := strings.Cut(raw, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !found || key == "" || value == "" || strings.Contains(value, "=") {
			return nil, fmt.Errorf("--%s requires id=value", flag)
		}
		if _, duplicate := result[key]; duplicate {
			return nil, fmt.Errorf("--%s repeats selection %q", flag, key)
		}
		result[key] = value
	}
	return result, nil
}

// parseInitCapabilitySelections reads use-case.capability=option pairs. A
// repeated capability is refused like any repeated selection.
func parseInitCapabilitySelections(values []string) (map[string]map[string]string, error) {
	selections, err := parseInitSelections("use-case-capability", values)
	if err != nil {
		return nil, err
	}
	result := map[string]map[string]string{}
	for key, option := range selections {
		useCase, capability, found := strings.Cut(key, ".")
		if !found || useCase == "" || capability == "" || strings.Contains(capability, ".") {
			return nil, fmt.Errorf("--use-case-capability requires use-case.capability=option, got %q", key)
		}
		if result[useCase] == nil {
			result[useCase] = map[string]string{}
		}
		result[useCase][capability] = option
	}
	return result, nil
}

func parseInitModuleProfiles() (map[string]architecturev2.ModuleProfileOverride, error) {
	result := map[string]architecturev2.ModuleProfileOverride{}
	for _, axis := range []struct {
		flag   string
		values []string
	}{
		{"module-compute-profile", initModuleComputeProfiles},
		{"module-storage-profile", initModuleStorageProfiles},
		{"module-accelerator-profile", initModuleAcceleratorProfiles},
	} {
		selections, err := parseInitSelections(axis.flag, axis.values)
		if err != nil {
			return nil, err
		}
		for moduleID, profile := range selections {
			selection := result[moduleID]
			switch axis.flag {
			case "module-compute-profile":
				selection.ComputeProfile = profile
			case "module-storage-profile":
				selection.StorageProfile = profile
			case "module-accelerator-profile":
				selection.AcceleratorProfile = profile
			}
			result[moduleID] = selection
		}
	}
	return result, nil
}
