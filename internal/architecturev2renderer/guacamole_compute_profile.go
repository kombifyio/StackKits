package architecturev2renderer

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// Retained v2 has no container quota. The selected module budget in v3 is
// divided across its closed resident pair, so it is never allocated twice.
func guacamoleSelectedComponentLimits(profile json.RawMessage) (*selectedPaaSRuntimeLimits, error) {
	if len(profile) == 0 {
		return nil, nil
	}
	input, err := resolvedplan.ParseModuleComputeProfileInput(profile, guacamoleWorkloadModuleID)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(input.Profile["components"])
	if err != nil {
		return nil, err
	}
	var residents []string
	if err := json.Unmarshal(raw, &residents); err != nil || !sameStringSet(residents, []string{"guacd", "guacamole"}) {
		return nil, fmt.Errorf("selected Guacamole allocation requires its exact resident pair")
	}
	limits, _, err := computeProfileLimits(profile, guacamoleWorkloadModuleID)
	if err != nil {
		return nil, err
	}
	count := int64(len(residents))
	divide := func(value string) (string, error) {
		mib, err := strconv.ParseInt(strings.TrimSuffix(value, "m"), 10, 64)
		if err != nil || mib < count || mib%count != 0 {
			return "", fmt.Errorf("selected module allocation cannot be conserved across its resident pair")
		}
		return strconv.FormatInt(mib/count, 10) + "m", nil
	}
	limits.MemoryLimit, err = divide(limits.MemoryLimit)
	if err != nil {
		return nil, err
	}
	limits.MemoryReservation, err = divide(limits.MemoryReservation)
	if err != nil {
		return nil, err
	}
	limits.CPUs /= float64(count)
	return limits, nil
}
