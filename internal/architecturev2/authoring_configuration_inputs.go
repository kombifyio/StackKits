package architecturev2

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// Declared public Configure descriptors enforce their scalar types. Other
// existing CLI JSON inputs retain their CUE-owned validation contract.
func validatePublicConfigurationSettings(catalog resolvedplan.Catalog, workloadID, alternativeID string, settings map[string]any) error {
	if len(settings) == 0 {
		return nil
	}
	var descriptors []any
	for _, workload := range catalog.Workloads {
		metadata, _ := workload["metadata"].(map[string]any)
		if metadata["id"] != workloadID {
			continue
		}
		alternatives, _ := workload["alternatives"].([]any)
		for _, raw := range alternatives {
			alternative, _ := raw.(map[string]any)
			if alternative["id"] != alternativeID {
				continue
			}
			inputs, _ := alternative["inputs"].(map[string]any)
			contract, _ := inputs["settings"].(map[string]any)
			descriptors, _ = contract["descriptors"].([]any)
		}
	}
	for key, value := range settings {
		for _, raw := range descriptors {
			descriptor, _ := raw.(map[string]any)
			if descriptor["key"] != key {
				continue
			}
			valid := false
			switch descriptor["type"] {
			case "string":
				_, valid = value.(string)
			case "boolean":
				_, valid = value.(bool)
			case "number":
				switch value.(type) {
				case json.Number, float64, float32, int, int64:
					valid = true
				}
			}
			if !valid {
				return resolveError(ErrInvalidStackSpec, fmt.Sprintf("public setting %q differs from its catalog scalar type", key), nil)
			}
			if options, ok := descriptor["options"].([]any); ok {
				found := false
				for _, option := range options {
					found = found || configurationScalarEqual(option, value)
				}
				if !found {
					return resolveError(ErrInvalidStackSpec, fmt.Sprintf("public setting %q names no catalog option", key), nil)
				}
			}
		}
	}
	return nil
}

func configurationScalarEqual(left, right any) bool {
	if reflect.DeepEqual(left, right) {
		return true
	}
	// JSON numbers from evaluated CUE use json.Number; CLI JSON uses float64.
	// Compare their decimal value rather than their Go representation.
	leftJSON, err := json.Marshal(left)
	if err != nil {
		return false
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		return false
	}
	leftNumber, leftOK := new(big.Rat).SetString(string(leftJSON))
	rightNumber, rightOK := new(big.Rat).SetString(string(rightJSON))
	return leftOK && rightOK && leftNumber.Cmp(rightNumber) == 0
}
