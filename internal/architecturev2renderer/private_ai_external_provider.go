package architecturev2renderer

import (
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"strings"
)

// The optional external inference provider of the Private AI use case: an
// OpenAI-compatible API (OpenAI, Mistral, Groq, OpenRouter, a self-hosted
// gateway) that Open WebUI offers next to the node's Ollama. The setting holds
// only the provider's https base URL. The API key is a kombify Wallet item the
// Wallet sealed to this node (`stackkit wallet-item import`); `stackkit setup
// ai-provider` binds PrivateAIExternalProviderKeyRef to that item, and Apply
// resolves the newest delivered version into Open WebUI's private secret
// environment. The bundle carries the opaque reference only, and Apply fails
// closed when the bound item is not in the node's custody.
const (
	PrivateAIExternalProviderSetting = "external-provider"
	PrivateAIExternalProviderKeySlot = "external-provider-api-key"
	PrivateAIExternalProviderKeyRef  = "secret://workloads/ai/external-provider-api-key"

	privateAIExternalProviderURLVariable = "OPENAI_API_BASE_URL"
	privateAIExternalProviderKeyVariable = "OPENAI_API_KEY"
	maxPrivateAIExternalProviderURLBytes = 512
)

// NormalizePrivateAIExternalProviderURL accepts an https base URL without
// credentials, query or fragment and returns it without a trailing slash. Every
// character must survive Compose interpolation and Go JSON unchanged.
func NormalizePrivateAIExternalProviderURL(raw string) (string, error) {
	invalid := errors.New("the external provider needs an https base URL without credentials, query or fragment, such as https://api.openai.com/v1")
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if value == "" || len(value) > maxPrivateAIExternalProviderURLBytes {
		return "", invalid
	}
	for _, c := range value {
		if c <= 0x20 || c >= 0x7f || strings.ContainsRune(`"\<>&$'`+"`", c) {
			return "", invalid
		}
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", invalid
	}
	return value, nil
}

// materializePrivateAIExternalProvider wires Open WebUI to the provider while
// the setting holds a valid base URL. Open WebUI then needs egress to reach
// it; without the setting it keeps its private, offline configuration.
func materializePrivateAIExternalProvider(components []selectedPaaSRuntimeComponent, settings map[string]json.RawMessage, path string) (bool, error) {
	raw, set := settings[PrivateAIExternalProviderSetting]
	if !set {
		return false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fail(ErrInvalidPlan, path, "must be the provider's https base URL")
	}
	baseURL, err := NormalizePrivateAIExternalProviderURL(value)
	if err != nil {
		return false, fail(ErrInvalidPlan, path, "%s", err.Error())
	}
	for index := range components {
		component := &components[index]
		if component.ID != "open-webui" {
			continue
		}
		component.Environment = maps.Clone(component.Environment)
		component.SecretEnvironment = maps.Clone(component.SecretEnvironment)
		if component.Environment == nil || component.SecretEnvironment == nil {
			return false, fail(ErrInvalidPlan, path, "Open WebUI declares no private configuration")
		}
		if _, taken := component.Environment[privateAIExternalProviderURLVariable]; taken {
			return false, fail(ErrInvalidPlan, path, "Open WebUI already declares a provider URL")
		}
		if _, taken := component.SecretEnvironment[privateAIExternalProviderKeyVariable]; taken {
			return false, fail(ErrInvalidPlan, path, "Open WebUI already declares a provider key")
		}
		component.Environment["ENABLE_OPENAI_API"] = "true"
		component.Environment[privateAIExternalProviderURLVariable] = baseURL
		component.SecretEnvironment[privateAIExternalProviderKeyVariable] = PrivateAIExternalProviderKeySlot
		component.Egress = true
		return true, nil
	}
	return false, fail(ErrInvalidPlan, path, "the external provider needs the Open WebUI chat module")
}

func privateAIExternalProviderEnabled(components []selectedPaaSRuntimeComponent) bool {
	for _, component := range components {
		if component.ID == "open-webui" {
			return component.SecretEnvironment[privateAIExternalProviderKeyVariable] == PrivateAIExternalProviderKeySlot
		}
	}
	return false
}

// splitPrivateAIExternalProvider removes an exact external provider wiring
// from Open WebUI's environment, so the remaining configuration is checked
// against the private baseline. Egress is admitted only with that wiring.
func splitPrivateAIExternalProvider(c selectedPaaSRuntimeComponent, environment, secretEnvironment map[string]string) (map[string]string, map[string]string, error) {
	slot, keyed := secretEnvironment[privateAIExternalProviderKeyVariable]
	baseURL, addressed := environment[privateAIExternalProviderURLVariable]
	if !keyed && !addressed {
		if c.Egress {
			return nil, nil, errors.New("Open WebUI requests egress only for an external provider")
		}
		return environment, secretEnvironment, nil
	}
	if !keyed || !addressed || slot != PrivateAIExternalProviderKeySlot || !c.Egress || environment["ENABLE_OPENAI_API"] != "true" {
		return nil, nil, errors.New("Open WebUI external provider wiring is partial or differs")
	}
	if normalized, err := NormalizePrivateAIExternalProviderURL(baseURL); err != nil || normalized != baseURL {
		return nil, nil, errors.New("Open WebUI external provider URL is invalid")
	}
	environment, secretEnvironment = maps.Clone(environment), maps.Clone(secretEnvironment)
	delete(environment, privateAIExternalProviderURLVariable)
	environment["ENABLE_OPENAI_API"] = "false"
	delete(secretEnvironment, privateAIExternalProviderKeyVariable)
	return environment, secretEnvironment, nil
}
