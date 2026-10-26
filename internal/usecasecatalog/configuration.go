package usecasecatalog

import (
	"encoding/json"
	"sort"
	"strings"
)

// This projects only CUE-owned native input descriptors, never Wizard settings.
func configurationInputsFromAlternative(alternative map[string]any) *ConfigurationInputs {
	var input struct {
		Settings struct {
			Descriptors []ConfigurationSetting `json:"descriptors"`
		} `json:"settings"`
		Secrets struct {
			Allowed []string `json:"allowedRefs"`
		} `json:"secretInputs"`
	}
	raw, _ := json.Marshal(alternative["inputs"])
	if json.Unmarshal(raw, &input) != nil {
		return nil
	}
	settings := append([]ConfigurationSetting{}, input.Settings.Descriptors...)
	refs := append([]string{}, input.Secrets.Allowed...)
	sort.Slice(settings, func(i, j int) bool { return settings[i].Key < settings[j].Key })
	sort.Strings(refs)
	return &ConfigurationInputs{Settings: settings, SecretRefs: refs}
}

func configurationProfileIDs(module map[string]any, field string) []string {
	profiles, _ := module[field].(map[string]any)
	var result []string
	for key := range profiles {
		if strings.TrimSpace(key) != "" {
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}
