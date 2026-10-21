package architecturev2

import (
	"fmt"
	"sort"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// selectNewWorkloadPlatform binds only newly authored workloads. A recorded
// adapter (including Compose) remains the user's choice. Catalog declarations
// identify which selected installation actually supplies an execution adapter;
// an application name or an allowed adapter alone does not imply installation.
func selectNewWorkloadPlatform(catalog resolvedplan.Catalog, installed, additions map[string]useCaseWorkloadSelection) error {
	platforms := map[string]bool{}
	for _, candidate := range catalog.Workloads {
		contract := map[string]any(candidate)
		metadata, _ := contract["metadata"].(map[string]any)
		id, _ := metadata["id"].(string)
		selection, selected := installed[id]
		if !selected {
			continue
		}
		alternatives, _ := contract["alternatives"].([]any)
		for _, raw := range alternatives {
			alternative, _ := raw.(map[string]any)
			if alternative["id"] == selection.Alternative {
				if adapter, _ := alternative["providesRuntimeAdapterRef"].(string); adapter != "" {
					platforms[adapter] = true
				}
			}
		}
	}
	for _, candidate := range catalog.Workloads {
		contract := map[string]any(candidate)
		metadata, _ := contract["metadata"].(map[string]any)
		id, _ := metadata["id"].(string)
		selection, added := additions[id]
		if !added || selection.RuntimeAdapterRef != "" {
			continue
		}
		var compatible []string
		for _, adapter := range workloadAlternativeAllowedAdapterRefs(contract, selection.Alternative) {
			if platforms[adapter] {
				compatible = append(compatible, adapter)
			}
		}
		if len(compatible) > 1 {
			sort.Strings(compatible)
			return resolveError(ErrInvalidStackSpec, fmt.Sprintf("use case %q has multiple selected platforms %v; select its runtime explicitly with --platform", id, compatible), nil)
		}
		if len(compatible) == 1 {
			selection.RuntimeAdapterRef = compatible[0]
		} else {
			alternatives, _ := contract["alternatives"].([]any)
			for _, raw := range alternatives {
				alternative, _ := raw.(map[string]any)
				if alternative["id"] == selection.Alternative {
					runtime, _ := alternative["runtime"].(map[string]any)
					selection.RuntimeAdapterRef, _ = runtime["defaultAdapterRef"].(string)
				}
			}
		}
		additions[id] = selection
	}
	return nil
}
