package architecturev2renderer

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// validateRuntimeListenerComposeParity proves that the rendered Compose host
// bindings are exactly the catalog-owned listener declarations. It deliberately
// knows nothing about a specific Kit, provider, or service inventory.
func validateRuntimeListenerComposeParity(listenerData, composeData []byte, path string) error {
	var listeners []rawModuleRuntimeListener
	if err := decodeStrict(listenerData, &listeners); err != nil {
		return wrap(ErrInvalidPlan, path, "decode runtime listeners", err)
	}
	var compose struct {
		Services map[string]struct {
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(composeData, &compose); err != nil {
		return wrap(ErrRendererFailure, path, "decode rendered Compose port bindings", err)
	}
	expected := make(map[string]string, len(listeners))
	for index, listener := range listeners {
		key := listener.ComponentRef + "\x00" + composeRuntimeListenerBinding(listener)
		if previous, duplicate := expected[key]; duplicate {
			return fail(ErrInvalidPlan, fmt.Sprintf("%s[%d]", path, index), "duplicates physical binding declared by %q", previous)
		}
		expected[key] = listener.ID
	}
	actualCount := 0
	for serviceRef, service := range compose.Services {
		for portIndex, binding := range service.Ports {
			actualCount++
			key := serviceRef + "\x00" + binding
			if _, declared := expected[key]; !declared {
				return fail(ErrOutputChanged, fmt.Sprintf("%s.compose.services.%s.ports[%d]", path, serviceRef, portIndex), "rendered host binding %q is not declared", binding)
			}
			delete(expected, key)
		}
	}
	if len(expected) != 0 || actualCount != len(listeners) {
		return fail(ErrOutputChanged, path, "rendered Compose bindings do not exactly match the runtime listener inventory")
	}
	return nil
}

func composeRuntimeListenerBinding(listener rawModuleRuntimeListener) string {
	address := listener.BindAddress
	if strings.Contains(address, ":") {
		address = "[" + address + "]"
	}
	binding := fmt.Sprintf("%s:%d:%d", address, listener.Port, listener.TargetPort)
	if listener.Transport == "udp" {
		binding += "/udp"
	}
	return binding
}
