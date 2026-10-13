package architecturev2renderer

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const moduleComputeProfileInputRef = "compute-profile"

func validateModuleComputeProfileInput(data []byte) error {
	var identity struct {
		ModuleRef string `json:"moduleRef"`
	}
	if err := json.Unmarshal(data, &identity); err != nil {
		return err
	}
	_, err := resolvedplan.ParseModuleComputeProfileInput(data, identity.ModuleRef)
	return err
}

// consumeModuleComputeProfile checks the additional compiler binding before
// delegating the remaining route/settings input surface to its existing owner.
func consumeModuleComputeProfile(unit RenderUnit, path string) (RenderUnit, json.RawMessage, error) {
	var bindings []map[string]json.RawMessage
	var values map[string]json.RawMessage
	if err := decodeStrict(unit.InputBindingsJSON(), &bindings); err != nil {
		return unit, nil, err
	}
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return unit, nil, err
	}
	remaining := make([]map[string]json.RawMessage, 0, len(bindings))
	found := false
	for _, binding := range bindings {
		var target string
		if err := json.Unmarshal(binding["targetRef"], &target); err != nil {
			return unit, nil, err
		}
		if target != moduleComputeProfileInputRef {
			remaining = append(remaining, binding)
			continue
		}
		var identity struct {
			TargetRef   string `json:"targetRef"`
			SourceRef   string `json:"sourceRef"`
			ValueType   string `json:"valueType"`
			Cardinality string `json:"cardinality"`
			Required    bool   `json:"required"`
		}
		data, err := json.Marshal(binding)
		if err != nil {
			return unit, nil, err
		}
		if err := decodeStrict(data, &identity); err != nil || found ||
			identity.SourceRef != "modules.computeProfileBinding" || identity.ValueType != "module-compute-profile-v1" ||
			identity.Cardinality != "single" || !identity.Required {
			return unit, nil, fail(ErrInvalidPlan, path, "selected compute profile must be the exact required compiler binding")
		}
		found = true
	}
	profile := values[moduleComputeProfileInputRef]
	if !found || !slices.Contains(unit.PublicInputRefs(), moduleComputeProfileInputRef) {
		return unit, nil, fail(ErrInvalidPlan, path, "required selected compute profile binding is absent")
	}
	if _, err := resolvedplan.ParseModuleComputeProfileInput(profile, unit.ModuleID()); err != nil {
		return unit, nil, wrap(ErrInvalidPlan, path, "validate selected compute profile", err)
	}
	delete(values, moduleComputeProfileInputRef)
	unit.publicInputRefs = slices.DeleteFunc(slices.Clone(unit.publicInputRefs), func(ref string) bool { return ref == moduleComputeProfileInputRef })
	unit.inputBindingsJSON, _ = json.Marshal(remaining)
	unit.valuesJSON, _ = json.Marshal(values)
	return unit, profile, nil
}

type moduleResourceBudget struct {
	CPUCores  float64 `json:"cpuCores"`
	RAMGB     float64 `json:"ramGB"`
	StorageGB float64 `json:"storageGB,omitempty"`
}

// computeProfileLimits consumes only the selected body's allocation blocks.
// A missing axis is unknown, never an inferred zero or a tier lookup in Go.
func computeProfileLimits(data []byte, moduleID string) (*selectedPaaSRuntimeLimits, int64, error) {
	input, err := resolvedplan.ParseModuleComputeProfileInput(data, moduleID)
	if err != nil {
		return nil, 0, err
	}
	budgets := make([]moduleResourceBudget, 0, 2)
	for _, block := range []string{"reservation", "headroom"} {
		body, err := json.Marshal(input.Profile[block])
		if err != nil {
			return nil, 0, err
		}
		var budget moduleResourceBudget
		if err := decodeStrict(body, &budget); err != nil || budget.CPUCores <= 0 || budget.RAMGB <= 0 {
			return nil, 0, fmt.Errorf("selected profile requires positive CPU and RAM %s", block)
		}
		budgets = append(budgets, budget)
	}
	reservationMiB := budgets[0].RAMGB * 1024
	limitMiB := (budgets[0].RAMGB + budgets[1].RAMGB) * 1024
	cpus := budgets[0].CPUCores + budgets[1].CPUCores
	if math.IsNaN(limitMiB) || math.IsInf(limitMiB, 0) || limitMiB >= float64(math.MaxInt64)/(1024*1024) ||
		math.Trunc(limitMiB) != limitMiB || math.Trunc(reservationMiB) != reservationMiB || reservationMiB < 1 ||
		math.IsNaN(cpus) || math.IsInf(cpus, 0) {
		return nil, 0, fmt.Errorf("selected resource budget cannot be represented as positive whole MiB and CPU limits")
	}
	return &selectedPaaSRuntimeLimits{
		MemoryLimit:       strconv.FormatInt(int64(limitMiB), 10) + "m",
		MemoryReservation: strconv.FormatInt(int64(reservationMiB), 10) + "m",
		CPUs:              cpus,
	}, int64(reservationMiB) * 1024 * 1024, nil
}
