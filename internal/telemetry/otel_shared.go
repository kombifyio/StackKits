// Package telemetry contains opt-in runtime telemetry setup helpers.
package telemetry

import "strings"

type OTelConfig struct {
	ServiceName        string
	ServiceVersion     string
	Environment        string
	RunID              string
	TenantDeploymentID string
	StackKit           string
	Provider           string
	NodeID             string
}

type OTelRuntime struct {
	// Enabled reports export: ResolveOTelRuntime sets it from the configured
	// endpoint, and SetupOTel clears it when the exporter could not be created.
	// SetupOTel installs local instrumentation independently of this flag.
	Enabled            bool
	Endpoint           string
	Protocol           string
	Headers            map[string]string
	ServiceName        string
	ServiceVersion     string
	Environment        string
	RunID              string
	TenantDeploymentID string
	StackKit           string
	Provider           string
	NodeID             string
	ResourceAttributes map[string]string
}

type EnvLookup func(string) (string, bool)

func lookupTrimmed(lookup EnvLookup, key string) string {
	value, ok := lookup(key)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
