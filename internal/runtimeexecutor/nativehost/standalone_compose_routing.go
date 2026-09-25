package nativehost

import "fmt"

// The plan selects the core module; only these immutable core contracts may
// supply a Docker routing network. A missing owner is never guessed.
func standaloneComposeCoreNetwork(module string) (string, error) {
	switch module {
	case "stackkits-basement-core-runtime", "stackkits-basement-core-lite-runtime":
		return "stackkit-basement-core", nil
	case "stackkits-cloud-core-runtime":
		return "stackkit-cloud-core", nil
	case "stackkits-cloud-core-standalone-runtime":
		return "stackkit-cloud-core-standalone", nil
	default:
		return "", fmt.Errorf("standalone Compose route requires a supported plan-bound core owner")
	}
}
