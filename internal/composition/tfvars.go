package composition

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"

	cueval "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/pkg/models"
	"golang.org/x/crypto/bcrypt"
)

// GenerateTFVarsJSON generates terraform.tfvars.json matching the template
// variables and overlays composition-owned identity credentials.
func GenerateTFVarsJSON(spec *models.StackSpec, cr *CompositionResult) ([]byte, error) {
	bridge := cueval.NewTerraformBridge(".")
	data, err := bridge.GenerateTFVarsBytesFromSpec(spec)
	if err != nil {
		return nil, err
	}

	if cr == nil {
		return data, nil
	}

	var tfvars map[string]any
	if err := json.Unmarshal(data, &tfvars); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tfvars for composition overlay: %w", err)
	}

	if cr.Identity != nil {
		if err := overlayIdentityConfig(tfvars, cr.Identity); err != nil {
			return nil, err
		}
	}
	if err := overlayRuntimeTemplateVars(tfvars, spec, cr); err != nil {
		return nil, err
	}
	overlayEnableFlags(tfvars, cr.EnabledModules)

	data, err = json.MarshalIndent(tfvars, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to re-marshal tfvars: %w", err)
	}
	return append(data, '\n'), nil
}

func overlayRuntimeTemplateVars(tfvars map[string]any, spec *models.StackSpec, cr *CompositionResult) error {
	timezone := "UTC"
	if spec != nil && spec.System != nil && spec.System.Timezone != "" {
		timezone = spec.System.Timezone
	}
	tfvars["timezone"] = timezone

	if cr.Identity != nil {
		tfvars["tinyauth_secure_cookie"] = fmt.Sprintf("%t", cr.Identity.SecureCookie)
	}
	if _, ok := tfvars["tinyauth_session_expiry"]; !ok {
		tfvars["tinyauth_session_expiry"] = "86400"
	}
	if err := overlayMonitoringAgentRuntimeTemplateVars(tfvars, cr); err != nil {
		return err
	}
	if err := overlayMonitoringCoreRuntimeTemplateVars(tfvars, cr); err != nil {
		return err
	}

	if moduleIsEnabled(cr.EnabledModules, "vaultwarden") {
		if existing, ok := tfvars["vaultwarden_admin_token"].(string); !ok || existing == "" {
			token, err := GenerateRandomPassword(48)
			if err != nil {
				return fmt.Errorf("generate vaultwarden admin token: %w", err)
			}
			tfvars["vaultwarden_admin_token"] = token
		}
	}

	return nil
}

func overlayMonitoringCoreRuntimeTemplateVars(tfvars map[string]any, cr *CompositionResult) error {
	if cr == nil || !moduleIsEnabled(cr.EnabledModules, "monitoring-core") {
		return nil
	}

	retentionPeriod := "30d"
	memoryAllowedPercent := 40
	maxConcurrentRequests := 4
	gatewayMemoryLimitMiB := 256
	gatewayBatchTimeout := "15s"
	gatewayMaxConcurrentStreams := 50
	gatewayRemoteWriteEndpoint := "http://victoriametrics:8428/api/v1/write"

	if cr.ModuleSettings != nil {
		if settings, ok := cr.ModuleSettings["monitoring-core"]; ok {
			if backend, ok := lookupMap(settings, "backend"); ok {
				retentionPeriod = lookupString(backend, "retentionPeriod", retentionPeriod)
				memoryAllowedPercent = lookupInt(backend, "memoryAllowedPercent", memoryAllowedPercent)
				maxConcurrentRequests = lookupInt(backend, "maxConcurrentRequests", maxConcurrentRequests)
				if port := lookupInt(backend, "port", 8428); port > 0 {
					gatewayRemoteWriteEndpoint = fmt.Sprintf("http://victoriametrics:%d/api/v1/write", port)
				}
			}
			if gateway, ok := lookupMap(settings, "gateway"); ok {
				gatewayMemoryLimitMiB = lookupInt(gateway, "memoryLimitMiB", gatewayMemoryLimitMiB)
				gatewayBatchTimeout = lookupString(gateway, "batchTimeout", gatewayBatchTimeout)
				gatewayMaxConcurrentStreams = lookupInt(gateway, "maxConcurrentStreams", gatewayMaxConcurrentStreams)
				gatewayRemoteWriteEndpoint = lookupString(gateway, "remoteWriteEndpoint", gatewayRemoteWriteEndpoint)
			}
		}
	}

	gatewayLimitMiB, gatewaySpikeLimitMiB := monitoringAgentMemoryLimiter(gatewayMemoryLimitMiB)

	tfvars["monitoring_core_vm_retention_period"] = retentionPeriod
	tfvars["monitoring_core_vm_memory_allowed_percent"] = fmt.Sprintf("%d", memoryAllowedPercent)
	tfvars["monitoring_core_vm_max_concurrent_requests"] = fmt.Sprintf("%d", maxConcurrentRequests)
	tfvars["monitoring_core_gateway_gomemlimit"] = fmt.Sprintf("%dMiB", gatewayMemoryLimitMiB)
	tfvars["monitoring_core_gateway_batch_timeout"] = gatewayBatchTimeout
	tfvars["monitoring_core_gateway_max_concurrent_streams"] = fmt.Sprintf("%d", gatewayMaxConcurrentStreams)
	tfvars["monitoring_core_gateway_memory_limit_mib"] = fmt.Sprintf("%d", gatewayLimitMiB)
	tfvars["monitoring_core_gateway_memory_spike_limit_mib"] = fmt.Sprintf("%d", gatewaySpikeLimitMiB)
	tfvars["monitoring_core_gateway_remote_write_endpoint"] = gatewayRemoteWriteEndpoint

	return nil
}

func overlayMonitoringAgentRuntimeTemplateVars(tfvars map[string]any, cr *CompositionResult) error {
	if cr == nil || !moduleIsEnabled(cr.EnabledModules, "monitoring-agent") {
		return nil
	}

	profile := "standard"
	endpoint := "techstack:4317"
	collectionInterval := defaultMonitoringAgentCollectionInterval(profile)
	batchTimeout := defaultMonitoringAgentBatchTimeout(profile)
	memoryLimitMiB := 256
	dockerEndpoint := "unix:///var/run/docker.sock"

	if cr.ModuleSettings != nil {
		if settings, ok := cr.ModuleSettings["monitoring-agent"]; ok {
			if collector, ok := lookupMap(settings, "collector"); ok {
				profile = lookupString(collector, "profile", profile)
				endpoint = lookupString(collector, "endpoint", endpoint)
				memoryLimitMiB = lookupInt(collector, "memoryLimitMiB", memoryLimitMiB)
				collectionInterval = lookupString(collector, "collectionInterval", defaultMonitoringAgentCollectionInterval(profile))
				batchTimeout = lookupString(collector, "batchTimeout", defaultMonitoringAgentBatchTimeout(profile))
				if dockerStats, ok := lookupMap(collector, "dockerStats"); ok {
					dockerEndpoint = lookupString(dockerStats, "endpoint", dockerEndpoint)
				}
			}
		}
	}

	memoryLimiterLimitMiB, memorySpikeLimitMiB := monitoringAgentMemoryLimiter(memoryLimitMiB)

	tfvars["monitoring_agent_profile"] = profile
	tfvars["monitoring_agent_otlp_endpoint"] = endpoint
	tfvars["monitoring_agent_collection_interval"] = collectionInterval
	tfvars["monitoring_agent_batch_timeout"] = batchTimeout
	tfvars["monitoring_agent_docker_endpoint"] = dockerEndpoint
	tfvars["monitoring_agent_gomemlimit"] = fmt.Sprintf("%dMiB", memoryLimitMiB)
	tfvars["monitoring_agent_memory_limit_mib"] = fmt.Sprintf("%d", memoryLimiterLimitMiB)
	tfvars["monitoring_agent_memory_spike_limit_mib"] = fmt.Sprintf("%d", memorySpikeLimitMiB)

	return nil
}

func defaultMonitoringAgentCollectionInterval(profile string) string {
	if profile == "full" {
		return "10s"
	}
	return "30s"
}

func defaultMonitoringAgentBatchTimeout(profile string) string {
	if profile == "full" {
		return "15s"
	}
	return "30s"
}

func monitoringAgentMemoryLimiter(memoryLimitMiB int) (int, int) {
	limitMiB := memoryLimitMiB - 56
	if limitMiB < 64 {
		limitMiB = 64
	}

	spikeLimitMiB := limitMiB / 4
	if spikeLimitMiB < 16 {
		spikeLimitMiB = 16
	}

	return limitMiB, spikeLimitMiB
}

func lookupMap(values map[string]any, key string) (map[string]any, bool) {
	v, ok := values[key]
	if !ok {
		return nil, false
	}
	mapped, ok := v.(map[string]any)
	return mapped, ok
}

func lookupString(values map[string]any, key string, fallback string) string {
	v, ok := values[key]
	if !ok {
		return fallback
	}
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fallback
}

func lookupInt(values map[string]any, key string, fallback int) int {
	v, ok := values[key]
	if !ok {
		return fallback
	}

	switch n := v.(type) {
	case int:
		return n
	case int8:
		return int(n)
	case int16:
		return int(n)
	case int32:
		return int(n)
	case int64:
		return int(n)
	case uint:
		return int(n)
	case uint8:
		return int(n)
	case uint16:
		return int(n)
	case uint32:
		return int(n)
	case uint64:
		return int(n)
	case float32:
		return int(n)
	case float64:
		return int(n)
	default:
		return fallback
	}
}

func overlayIdentityConfig(tfvars map[string]any, ic *IdentityConfig) error {
	if ic.AdminPassword != "" {
		tfvars["admin_password_plaintext"] = ic.AdminPassword
		hash, hashErr := BcryptHash(ic.AdminPassword)
		if hashErr != nil {
			return fmt.Errorf("failed to hash admin password: %w", hashErr)
		}
		email := ic.AdminEmail
		if email == "" {
			email = "admin"
		}
		tfvars["admin_email"] = email
		tfvars["tinyauth_users"] = fmt.Sprintf("%s:%s", email, hash)
	}

	if ic.PocketIDEnabled {
		tfvars["pocketid_app_url"] = ic.PocketIDAppURL
		// pocketid_encryption_key is provisioned by the CLI's file-based path
		// (internal/identity.EnsureEncryptionKey) and written into the tfvars
		// map directly by cmd/stackkit/commands/generate.go, so that destroy →
		// re-apply round-trips reuse the same key.
		tfvars["enable_pocketid"] = true
	} else {
		tfvars["enable_pocketid"] = false
	}

	if ic.TinyAuthOAuthEnabled {
		tfvars["tinyauth_oidc_enabled"] = true
		tfvars["tinyauth_oidc_issuer"] = ic.OIDCIssuerURL
		tfvars["tinyauth_oidc_client_id"] = ic.OIDCClientID
		tfvars["tinyauth_oidc_client_secret"] = ic.OIDCClientSecret
	}
	if ic.TinyAuthSessionSecret != "" {
		tfvars["tinyauth_session_secret"] = ic.TinyAuthSessionSecret
	}

	return nil
}

func overlayEnableFlags(tfvars map[string]any, enabledModules []string) {
	enableMapping := map[string]string{
		"traefik":     "enable_traefik",
		"tinyauth":    "enable_tinyauth",
		"pocketid":    "enable_pocketid",
		"dokploy":     "enable_dokploy",
		"dockge":      "enable_dockge",
		"coolify":     "enable_coolify",
		"dashboard":   "enable_dashboard",
		"uptime-kuma": "enable_uptime_kuma",
		"vaultwarden": "enable_vaultwarden",
		"jellyfin":    "enable_jellyfin",
		"immich":      "enable_immich",
	}
	enabledSet := make(map[string]bool, len(enabledModules))
	for _, name := range enabledModules {
		enabledSet[name] = true
	}
	for module, tfvarKey := range enableMapping {
		if enabledSet[module] {
			tfvars[tfvarKey] = true
		}
	}
}

func moduleIsEnabled(enabledModules []string, name string) bool {
	for _, module := range enabledModules {
		if module == name {
			return true
		}
	}
	return false
}

func GenerateRandomPassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", fmt.Errorf("generate random password: %w", err)
		}
		b[i] = charset[n.Int64()]
	}
	return string(b), nil
}

func BcryptHash(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("bcrypt hash: %w", err)
	}
	return string(hash), nil
}
