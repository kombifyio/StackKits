package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/platformdeploy"
	"github.com/kombifyio/stackkits/pkg/models"
)

// External PaaS credential handling for the managed publisher path.
//
// These helpers live in their own file because their only consumers are behind
// the `publisher` build tag (tenant_spec_fetch.go, apply_managed_publisher.go).
// They previously sat beside the retired OpenTofu platform-app deployment and
// were removed with it, which broke `go build -tags publisher`: the default
// build does not compile their callers, so an unused-symbol analysis run
// without the tag reports them as dead.

type platformConfigFile struct {
	Platform                    string                           `json:"platform,omitempty"`
	Endpoint                    string                           `json:"endpoint,omitempty"`
	BaseURL                     string                           `json:"baseUrl,omitempty"`
	Token                       string                           `json:"token,omitempty"`
	APIKey                      string                           `json:"apiKey,omitempty"`
	APISecret                   string                           `json:"apiSecret,omitempty"`
	EnvironmentID               string                           `json:"environmentId,omitempty"`
	ServerID                    string                           `json:"serverId,omitempty"`
	ProjectUUID                 string                           `json:"projectUuid,omitempty"`
	EnvironmentUUID             string                           `json:"environmentUuid,omitempty"`
	DestinationUUID             string                           `json:"destinationUuid,omitempty"`
	LegacyDockerComposeAPI      bool                             `json:"legacyDockerComposeApi,omitempty"`
	DisableDockerRuntimeObserve bool                             `json:"disableDockerRuntimeObserve,omitempty"`
	BootstrapEvidence           platformdeploy.BootstrapEvidence `json:"bootstrapEvidence,omitempty"`
	found                       bool                             `json:"-"`
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func platformConfigFromValueMap(defaultPlatform string, values map[string]string) (platformConfigFile, bool) {
	platform, platformSeen := platformFromValueMap(defaultPlatform, values)
	cfg := platformConfigFile{
		Platform:                    platform,
		Endpoint:                    firstPlatformValue(platform, values, "endpoint"),
		Token:                       firstPlatformValue(platform, values, "token"),
		APIKey:                      firstPlatformValue(platform, values, "api_key"),
		APISecret:                   firstPlatformValue(platform, values, "api_secret"),
		EnvironmentID:               firstPlatformValue(platform, values, "environment_id"),
		ServerID:                    firstPlatformValue(platform, values, "server_id"),
		ProjectUUID:                 firstPlatformValue(platform, values, "project_uuid"),
		EnvironmentUUID:             firstPlatformValue(platform, values, "environment_uuid"),
		DestinationUUID:             firstPlatformValue(platform, values, "destination_uuid"),
		LegacyDockerComposeAPI:      platformBoolValue(firstPlatformValue(platform, values, "legacy_docker_compose_api")),
		DisableDockerRuntimeObserve: platformBoolValue(firstPlatformValue(platform, values, "disable_docker_runtime_observe")),
	}
	return cfg, platformSeen || cfg.endpoint() != "" || cfg.Token != "" ||
		cfg.APIKey != "" || cfg.APISecret != "" ||
		cfg.EnvironmentID != "" || cfg.ServerID != "" || cfg.ProjectUUID != "" ||
		cfg.EnvironmentUUID != "" || cfg.DestinationUUID != "" || cfg.LegacyDockerComposeAPI ||
		cfg.DisableDockerRuntimeObserve
}

func validatePlatformConfigPlatform(platform string) error {
	switch platform {
	case models.PAASCoolify, models.PAASDokploy, models.PAASKomodo:
		return nil
	default:
		return fmt.Errorf("unsupported tenant spec platform config %q; expected coolify, komodo, or dokploy", platform)
	}
}

func redactPlatformConfigEnvironment(values map[string]string) {
	for _, key := range allPlatformConfigEnvKeys() {
		delete(values, key)
	}
}

func platformFromValueMap(defaultPlatform string, values map[string]string) (string, bool) {
	for _, key := range []string{"STACKKIT_PLATFORM", "STACKKIT_PAAS"} {
		if value := strings.TrimSpace(values[key]); value != "" {
			return strings.ToLower(value), true
		}
	}
	if values["COOLIFY_API_URL"] != "" || values["COOLIFY_API_TOKEN"] != "" {
		return models.PAASCoolify, true
	}
	if values["DOKPLOY_API_URL"] != "" || values["DOKPLOY_API_KEY"] != "" {
		return models.PAASDokploy, true
	}
	if values["KOMODO_API_URL"] != "" || values["KOMODO_API_KEY"] != "" || values["KOMODO_API_SECRET"] != "" {
		return models.PAASKomodo, true
	}
	defaultPlatform = strings.ToLower(strings.TrimSpace(defaultPlatform))
	if defaultPlatform == models.PAASCoolify || defaultPlatform == models.PAASDokploy || defaultPlatform == models.PAASKomodo {
		return defaultPlatform, false
	}
	if genericPlatformConfigPresent(values) {
		return models.PAASCoolify, false
	}
	if defaultPlatform != "" {
		return defaultPlatform, false
	}
	return models.PAASCoolify, false
}

func genericPlatformConfigPresent(values map[string]string) bool {
	for _, key := range []string{
		"STACKKIT_PLATFORM_ENDPOINT",
		"STACKKIT_PLATFORM_TOKEN",
		"STACKKIT_PLATFORM_API_KEY",
		"STACKKIT_PLATFORM_API_SECRET",
		"STACKKIT_PLATFORM_ENVIRONMENT_ID",
		"STACKKIT_PLATFORM_ENVIRONMENT_NAME",
		"STACKKIT_PLATFORM_SERVER_ID",
		"STACKKIT_PLATFORM_SERVER_UUID",
		"STACKKIT_PLATFORM_PROJECT_UUID",
		"STACKKIT_PLATFORM_ENVIRONMENT_UUID",
		"STACKKIT_PLATFORM_DESTINATION_UUID",
		"STACKKIT_PLATFORM_LEGACY_DOCKERCOMPOSE_API",
	} {
		if strings.TrimSpace(values[key]) != "" {
			return true
		}
	}
	return false
}

func firstPlatformValue(platform string, values map[string]string, field string) string {
	for _, key := range platformConfigEnvKeys(platform, field) {
		if value := strings.TrimSpace(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func allPlatformConfigEnvKeys() []string {
	seen := map[string]bool{}
	var keys []string
	for _, platform := range []string{models.PAASCoolify, models.PAASDokploy, models.PAASKomodo} {
		for _, field := range []string{"endpoint", "token", "api_key", "api_secret", "environment_id", "server_id", "project_uuid", "environment_uuid", "destination_uuid", "legacy_docker_compose_api"} {
			for _, key := range platformConfigEnvKeys(platform, field) {
				if !seen[key] {
					seen[key] = true
					keys = append(keys, key)
				}
			}
		}
	}
	for _, key := range []string{"STACKKIT_PLATFORM", "STACKKIT_PAAS"} {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	return keys
}

var platformSpecificConfigEnvKeys = map[string]map[string][]string{
	models.PAASCoolify: {
		"endpoint":                  {"COOLIFY_API_URL", "STACKKIT_PLATFORM_ENDPOINT"},
		"token":                     {"COOLIFY_API_TOKEN", "STACKKIT_PLATFORM_TOKEN"},
		"environment_id":            {"COOLIFY_ENVIRONMENT_NAME", "STACKKIT_PLATFORM_ENVIRONMENT_NAME"},
		"server_id":                 {"COOLIFY_SERVER_UUID", "STACKKIT_PLATFORM_SERVER_UUID", "STACKKIT_PLATFORM_SERVER_ID"},
		"legacy_docker_compose_api": {"COOLIFY_LEGACY_DOCKERCOMPOSE_API", "STACKKIT_PLATFORM_LEGACY_DOCKERCOMPOSE_API"},
	},
	models.PAASDokploy: {
		"endpoint":       {"DOKPLOY_API_URL", "STACKKIT_PLATFORM_ENDPOINT"},
		"token":          {"DOKPLOY_API_KEY", "STACKKIT_PLATFORM_TOKEN"},
		"environment_id": {"DOKPLOY_ENVIRONMENT_ID", "STACKKIT_PLATFORM_ENVIRONMENT_ID"},
		"server_id":      {"DOKPLOY_SERVER_ID", "STACKKIT_PLATFORM_SERVER_ID"},
	},
	models.PAASKomodo: {
		"endpoint":   {"KOMODO_API_URL", "STACKKIT_PLATFORM_ENDPOINT"},
		"api_key":    {"KOMODO_API_KEY", "STACKKIT_PLATFORM_API_KEY"},
		"api_secret": {"KOMODO_API_SECRET", "STACKKIT_PLATFORM_API_SECRET"},
		"server_id":  {"KOMODO_SERVER_ID", "STACKKIT_PLATFORM_SERVER_ID"},
	},
}

var sharedPlatformConfigEnvKeys = map[string][]string{
	"project_uuid":     {"COOLIFY_PROJECT_UUID", "STACKKIT_PLATFORM_PROJECT_UUID"},
	"environment_uuid": {"COOLIFY_ENVIRONMENT_UUID", "STACKKIT_PLATFORM_ENVIRONMENT_UUID"},
	"destination_uuid": {"COOLIFY_DESTINATION_UUID", "STACKKIT_PLATFORM_DESTINATION_UUID"},
}

func platformConfigEnvKeys(platform, field string) []string {
	if fields := platformSpecificConfigEnvKeys[platform]; fields != nil {
		if keys := fields[field]; len(keys) > 0 {
			return keys
		}
	}
	return sharedPlatformConfigEnvKeys[field]
}

const platformBoolEnabledTrueValue = "true"

func (cfg platformConfigFile) endpoint() string {
	return firstNonEmpty(cfg.Endpoint, cfg.BaseURL)
}

func platformBoolValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", platformBoolEnabledTrueValue, "yes", "y", "on", "enabled":
		return true
	default:
		return false
	}
}

func firstPlatformEnv(platform, field string) string {
	return firstEnv(platformConfigEnvKeys(platform, field)...)
}

func firstPlatformBoolEnv(platform, field string) (bool, bool) {
	for _, key := range platformConfigEnvKeys(platform, field) {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return platformBoolValue(value), true
		}
	}
	return false, false
}
