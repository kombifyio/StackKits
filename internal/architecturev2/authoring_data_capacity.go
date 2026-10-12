package architecturev2

import (
	"fmt"
	"sort"
)

// DataCapacityIntent names only user inputs. CUE derives projectedGiB and
// requiredGiB, so every consumer receives the same reservation formula.
// Pointers preserve omission; missing inputs are rejected by the CUE contract.
type DataCapacityIntent struct {
	InitialGiB        *float64 `json:"initialGiB"`
	GrowthGiBPerMonth *float64 `json:"growthGiBPerMonth"`
	HorizonMonths     *int     `json:"horizonMonths"`
	ReservePercent    *float64 `json:"reservePercent"`
}

func applyInitialDataCapacityIntent(spec map[string]any, selections map[string]useCaseWorkloadSelection, demands map[string]DataCapacityIntent, native bool) error {
	if len(demands) == 0 {
		return nil
	}
	if !native {
		return fmt.Errorf("data capacity authoring requires stackkit/v2alpha2")
	}
	selected := map[string]bool{}
	for _, selection := range selections {
		data, _ := selection.ServiceEndpoint["data"].(map[string]any)
		binding, _ := data["bindingRef"].(string)
		if binding != "" {
			selected[binding] = true
		}
	}
	data, _ := spec["data"].(map[string]any)
	bindings, _ := data["bindings"].(map[string]any)
	keys := make([]string, 0, len(demands))
	for binding := range demands {
		keys = append(keys, binding)
	}
	sort.Strings(keys)
	for _, ref := range keys {
		binding, exists := bindings[ref].(map[string]any)
		if !selected[ref] || !exists {
			return fmt.Errorf("capacity budget %q requires its selected workload and declared data binding", ref)
		}
		intent := demands[ref]
		binding["capacityDemand"] = map[string]any{"initialGiB": intent.InitialGiB, "growthGiBPerMonth": intent.GrowthGiBPerMonth, "horizonMonths": intent.HorizonMonths, "reservePercent": intent.ReservePercent}
	}
	return nil
}
