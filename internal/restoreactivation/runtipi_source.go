package restoreactivation

import (
	"encoding/json"
	"errors"
)

// RuntipiAppGroupSourceSelected admits a source shape to the native group
// factory. It makes no support claim: actual effects require retained private
// source/image/helper qualification and signed current node custody.
func RuntipiAppGroupSourceSelected(canonical []byte, workload string) (bool, error) {
	var plan map[string]any
	if err := json.Unmarshal(canonical, &plan); err != nil {
		return false, err
	}
	return runtipiAppGroupSourceSelected(plan, workload)
}

func runtipiAppGroupSourceSelected(plan map[string]any, workload string) (bool, error) {
	graphs, err := deriveStandaloneComposeRuntimeContracts(plan)
	if err != nil {
		return false, err
	}
	matches := 0
	for _, graph := range graphs {
		if graph.WorkloadRef != workload || graph.RuntimeAdapterRef != "runtipi" {
			continue
		}
		matches++
		for _, component := range graph.Components {
			if component.Lifecycle != "daemon" {
				return false, errors.New("Runtipi app-group source contains an unqualified one-shot component")
			}
		}
	}
	if matches > 1 {
		return false, errors.New("Runtipi source has ambiguous node-local group placement")
	}
	return matches == 1, nil
}
