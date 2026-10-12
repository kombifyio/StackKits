package commands

import (
	"encoding/json"
	"fmt"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"io"
	"strings"
)

func parseInitDataCapacity(values []string) (map[string]architecturev2.DataCapacityIntent, error) {
	demands := make(map[string]architecturev2.DataCapacityIntent, len(values))
	for _, value := range values {
		binding, raw, found := strings.Cut(value, "=")
		binding = strings.TrimSpace(binding)
		if !found || binding == "" || len(raw) > 4096 {
			return nil, fmt.Errorf("--data-capacity requires binding=JSON with explicit new-data budget inputs")
		}
		if _, exists := demands[binding]; exists {
			return nil, fmt.Errorf("duplicate --data-capacity binding %q", binding)
		}
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		var intent architecturev2.DataCapacityIntent
		if err := decoder.Decode(&intent); err != nil {
			return nil, fmt.Errorf("--data-capacity %s: %w", binding, err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return nil, fmt.Errorf("--data-capacity %s must contain exactly one budget object", binding)
		}
		demands[binding] = intent
	}
	return demands, nil
}
