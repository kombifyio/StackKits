package architecturev2

import (
	"fmt"
	"strings"
)

func applyInitialSetupStyle(spec map[string]any, setupStyle string) error {
	normalized := strings.ToLower(strings.TrimSpace(setupStyle))
	if normalized == "" {
		return nil
	}
	var applicationDefault string
	switch normalized {
	case "bootstrapped":
		applicationDefault = "on-demand"
	case "clean", "clean-rollout":
		applicationDefault = "manual"
	default:
		return fmt.Errorf("invalid setup style %q: expected \"bootstrapped\" or \"clean\"", setupStyle)
	}
	install, _ := spec["install"].(map[string]any)
	if install == nil {
		install = map[string]any{}
		spec["install"] = install
	}
	platform, _ := install["platform"].(map[string]any)
	if platform == nil {
		platform = map[string]any{}
		install["platform"] = platform
	}
	setupPolicy, _ := platform["setupPolicy"].(map[string]any)
	if setupPolicy == nil {
		setupPolicy = map[string]any{}
		platform["setupPolicy"] = setupPolicy
	}
	setupPolicy["applicationDefault"] = applicationDefault
	return nil
}
