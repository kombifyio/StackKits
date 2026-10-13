package architecturev2renderer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Retain the exact merged2090 v3 recipe for applied-custody readers.
// The corrected renderer never emits its reservation-derived flow control.
func mosquittoRetainedV3Command(profile json.RawMessage) ([]string, error) {
	_, reservedBytes, err := computeProfileLimits(profile, mosquittoWorkloadModuleID)
	if err != nil {
		return nil, err
	}
	var command []string
	if err := json.Unmarshal([]byte(mosquittoV2CommandJSON), &command); err != nil {
		return nil, err
	}
	configuration := fmt.Sprintf("max_queued_bytes %d\\nmax_inflight_bytes %d\\nper_listener_settings true", reservedBytes/4, reservedBytes/16)
	command[2] = strings.Replace(command[2], "per_listener_settings true", configuration, 1)
	return command, nil
}
