package resolvedplan

import (
	"encoding/json"
	"fmt"
	"io"
)

// ModuleComputeProfileInput projects the selected catalog body, not a second
// resource policy. Fit, capacity and renderers consume the same hashed body.
type ModuleComputeProfileInput struct {
	ModuleRef   string         `json:"moduleRef"`
	ProfileRef  string         `json:"profileRef"`
	ProfileHash string         `json:"profileHash"`
	Profile     map[string]any `json:"profile"`
}

// ParseModuleComputeProfileInput verifies module ownership and the canonical
// profile hash using the compiler's existing hash authority.
func ParseModuleComputeProfileInput(data []byte, moduleID string) (ModuleComputeProfileInput, error) {
	var input ModuleComputeProfileInput
	decoder := json.NewDecoder(bytesReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, fmt.Errorf("decode selected compute profile: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return input, fmt.Errorf("selected compute profile has trailing data")
	}
	if moduleID == "" || input.ModuleRef != moduleID || input.ProfileRef == "" || input.Profile == nil || !canonicalSHA256Pattern.MatchString(input.ProfileHash) {
		return input, fmt.Errorf("selected compute profile requires exact module, profile identity and body hash")
	}
	hash, err := moduleProfileHash(moduleID, input.Profile)
	if err != nil || hash != input.ProfileHash {
		return input, fmt.Errorf("selected compute profile body differs from its bound hash")
	}
	return input, nil
}

func selectedModuleComputeProfileInput(modules []any, moduleID string) (any, bool, error) {
	for _, raw := range modules {
		module, err := asObject(raw, "resolvedPlan.modules")
		if err != nil {
			return nil, false, err
		}
		if module["id"] != moduleID {
			continue
		}
		value := map[string]any{
			"moduleRef": moduleID, "profileRef": module["computeProfile"],
			"profileHash": module["computeProfileHash"], "profile": module["computeProfileBinding"],
		}
		data, err := json.Marshal(value)
		if err != nil {
			return nil, false, err
		}
		if _, err := ParseModuleComputeProfileInput(data, moduleID); err != nil {
			return nil, false, err
		}
		return value, true, nil
	}
	return nil, false, nil
}
