package architecturev2renderer

import "encoding/json"

// Old applied v2 custody retains its exact original allocation. New artifacts
// use the common compiler profile binding; allocation never changes DNS policy.
func adguardHomeSelectedLimits(profile json.RawMessage) (*selectedPaaSRuntimeLimits, error) {
	if len(profile) == 0 {
		return &selectedPaaSRuntimeLimits{MemoryLimit: "256m", MemoryReservation: "128m"}, nil
	}
	limits, _, err := computeProfileLimits(profile, adguardHomeWorkloadModuleID)
	return limits, err
}
