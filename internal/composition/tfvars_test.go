package composition

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kombifyio/stackkits/pkg/models"
)

func TestGenerateTFVarsJSONOverlaysIdentitySecrets(t *testing.T) {
	spec := &models.StackSpec{
		Name:       "prod-ready",
		StackKit:   "base-kit",
		Domain:     "example.com",
		AdminEmail: "admin@example.com",
		System:     &models.SystemSpec{Timezone: "Europe/Berlin"},
	}
	cr := &CompositionResult{
		EnabledModules: []string{"traefik", "tinyauth", "vaultwarden"},
		Identity: &IdentityConfig{
			AdminEmail:            "admin@example.com",
			AdminPassword:         "generated-admin-password",
			TinyAuthEnabled:       true,
			TinyAuthSessionSecret: "generated-session-secret",
			SecureCookie:          true,
		},
	}

	data, err := GenerateTFVarsJSON(spec, cr)
	if err != nil {
		t.Fatalf("GenerateTFVarsJSON() error = %v", err)
	}

	var tfvars map[string]any
	if err := json.Unmarshal(data, &tfvars); err != nil {
		t.Fatalf("generated tfvars are not JSON: %v", err)
	}

	if tfvars["admin_email"] != "admin@example.com" {
		t.Fatalf("admin_email = %v, want admin@example.com", tfvars["admin_email"])
	}
	if tfvars["admin_password_plaintext"] != "generated-admin-password" {
		t.Fatalf("admin_password_plaintext was not overlaid")
	}
	users, _ := tfvars["tinyauth_users"].(string)
	if !strings.HasPrefix(users, "admin@example.com:$2") {
		t.Fatalf("tinyauth_users = %q, want bcrypt user entry", users)
	}
	if strings.Contains(users, "generated-admin-password") {
		t.Fatalf("tinyauth_users contains the plaintext admin password")
	}
	if tfvars["tinyauth_session_secret"] != "generated-session-secret" {
		t.Fatalf("tinyauth_session_secret was not overlaid")
	}
	if tfvars["tinyauth_secure_cookie"] != "true" {
		t.Fatalf("tinyauth_secure_cookie = %v, want true", tfvars["tinyauth_secure_cookie"])
	}
	if tfvars["timezone"] != "Europe/Berlin" {
		t.Fatalf("timezone = %v, want Europe/Berlin", tfvars["timezone"])
	}
	if tfvars["enable_tinyauth"] != true {
		t.Fatalf("enable_tinyauth = %v, want true", tfvars["enable_tinyauth"])
	}
	if token, _ := tfvars["vaultwarden_admin_token"].(string); token == "" {
		t.Fatalf("vaultwarden_admin_token was not generated")
	}
}

func TestGenerateTFVarsJSONDefaultsRuntimeTemplateVars(t *testing.T) {
	spec := &models.StackSpec{
		Name:     "prod-ready",
		StackKit: "base-kit",
		Domain:   "home.lab",
	}
	cr := &CompositionResult{EnabledModules: []string{"tinyauth"}}

	data, err := GenerateTFVarsJSON(spec, cr)
	if err != nil {
		t.Fatalf("GenerateTFVarsJSON() error = %v", err)
	}

	var tfvars map[string]any
	if err := json.Unmarshal(data, &tfvars); err != nil {
		t.Fatalf("generated tfvars are not JSON: %v", err)
	}

	if tfvars["timezone"] != "UTC" {
		t.Fatalf("timezone = %v, want UTC", tfvars["timezone"])
	}
	if tfvars["tinyauth_session_expiry"] != "86400" {
		t.Fatalf("tinyauth_session_expiry = %v, want 86400", tfvars["tinyauth_session_expiry"])
	}
	if tfvars["enable_tinyauth"] != true {
		t.Fatalf("enable_tinyauth = %v, want true", tfvars["enable_tinyauth"])
	}
}

func TestGenerateTFVarsJSONIncludesMonitoringAgentDefaultsWhenEnabled(t *testing.T) {
	spec := &models.StackSpec{
		Name:     "prod-ready",
		StackKit: "base-kit",
		Domain:   "home.lab",
	}
	cr := &CompositionResult{EnabledModules: []string{"monitoring-agent"}}

	data, err := GenerateTFVarsJSON(spec, cr)
	if err != nil {
		t.Fatalf("GenerateTFVarsJSON() error = %v", err)
	}

	var tfvars map[string]any
	if err := json.Unmarshal(data, &tfvars); err != nil {
		t.Fatalf("generated tfvars are not JSON: %v", err)
	}

	if tfvars["monitoring_agent_profile"] != "standard" {
		t.Fatalf("monitoring_agent_profile = %v, want standard", tfvars["monitoring_agent_profile"])
	}
	if tfvars["monitoring_agent_otlp_endpoint"] != "techstack:4317" {
		t.Fatalf("monitoring_agent_otlp_endpoint = %v, want techstack:4317", tfvars["monitoring_agent_otlp_endpoint"])
	}
	if tfvars["monitoring_agent_collection_interval"] != "30s" {
		t.Fatalf("monitoring_agent_collection_interval = %v, want 30s", tfvars["monitoring_agent_collection_interval"])
	}
	if tfvars["monitoring_agent_batch_timeout"] != "30s" {
		t.Fatalf("monitoring_agent_batch_timeout = %v, want 30s", tfvars["monitoring_agent_batch_timeout"])
	}
	if tfvars["monitoring_agent_gomemlimit"] != "256MiB" {
		t.Fatalf("monitoring_agent_gomemlimit = %v, want 256MiB", tfvars["monitoring_agent_gomemlimit"])
	}
}

func TestGenerateTFVarsJSONOverlaysMonitoringAgentRuntimeTemplateVars(t *testing.T) {
	spec := &models.StackSpec{
		Name:     "prod-ready",
		StackKit: "base-kit",
		Domain:   "home.lab",
	}
	cr := &CompositionResult{
		EnabledModules: []string{"monitoring-agent"},
		ModuleSettings: map[string]map[string]any{
			"monitoring-agent": {
				"collector": map[string]any{
					"profile":            "full",
					"endpoint":           "monitoring-gateway:4317",
					"memoryLimitMiB":     256,
					"collectionInterval": "10s",
					"batchTimeout":       "15s",
					"dockerStats": map[string]any{
						"endpoint": "unix:///custom-docker.sock",
					},
				},
			},
		},
	}

	data, err := GenerateTFVarsJSON(spec, cr)
	if err != nil {
		t.Fatalf("GenerateTFVarsJSON() error = %v", err)
	}

	var tfvars map[string]any
	if err := json.Unmarshal(data, &tfvars); err != nil {
		t.Fatalf("generated tfvars are not JSON: %v", err)
	}

	if tfvars["monitoring_agent_profile"] != "full" {
		t.Fatalf("monitoring_agent_profile = %v, want full", tfvars["monitoring_agent_profile"])
	}
	if tfvars["monitoring_agent_otlp_endpoint"] != "monitoring-gateway:4317" {
		t.Fatalf("monitoring_agent_otlp_endpoint = %v, want monitoring-gateway:4317", tfvars["monitoring_agent_otlp_endpoint"])
	}
	if tfvars["monitoring_agent_collection_interval"] != "10s" {
		t.Fatalf("monitoring_agent_collection_interval = %v, want 10s", tfvars["monitoring_agent_collection_interval"])
	}
	if tfvars["monitoring_agent_batch_timeout"] != "15s" {
		t.Fatalf("monitoring_agent_batch_timeout = %v, want 15s", tfvars["monitoring_agent_batch_timeout"])
	}
	if tfvars["monitoring_agent_docker_endpoint"] != "unix:///custom-docker.sock" {
		t.Fatalf("monitoring_agent_docker_endpoint = %v, want unix:///custom-docker.sock", tfvars["monitoring_agent_docker_endpoint"])
	}
	if tfvars["monitoring_agent_gomemlimit"] != "256MiB" {
		t.Fatalf("monitoring_agent_gomemlimit = %v, want 256MiB", tfvars["monitoring_agent_gomemlimit"])
	}
	if tfvars["monitoring_agent_memory_limit_mib"] != "200" {
		t.Fatalf("monitoring_agent_memory_limit_mib = %v, want 200", tfvars["monitoring_agent_memory_limit_mib"])
	}
	if tfvars["monitoring_agent_memory_spike_limit_mib"] != "50" {
		t.Fatalf("monitoring_agent_memory_spike_limit_mib = %v, want 50", tfvars["monitoring_agent_memory_spike_limit_mib"])
	}
}

func TestGenerateTFVarsJSONOverlaysMonitoringCoreRuntimeTemplateVars(t *testing.T) {
	spec := &models.StackSpec{
		Name:     "prod-ready",
		StackKit: "base-kit",
		Domain:   "home.lab",
	}
	cr := &CompositionResult{
		EnabledModules: []string{"monitoring-core"},
		ModuleSettings: map[string]map[string]any{
			"monitoring-core": {
				"backend": map[string]any{
					"retentionPeriod":      "90d",
					"memoryAllowedPercent": 55,
					"maxConcurrentRequests": 8,
					"port":                 18428,
				},
				"gateway": map[string]any{
					"memoryLimitMiB":      256,
					"batchTimeout":        "20s",
					"maxConcurrentStreams": 75,
					"remoteWriteEndpoint": "http://victoriametrics:18428/api/v1/write",
				},
			},
		},
	}

	data, err := GenerateTFVarsJSON(spec, cr)
	if err != nil {
		t.Fatalf("GenerateTFVarsJSON() error = %v", err)
	}

	var tfvars map[string]any
	if err := json.Unmarshal(data, &tfvars); err != nil {
		t.Fatalf("generated tfvars are not JSON: %v", err)
	}

	if tfvars["monitoring_core_vm_retention_period"] != "90d" {
		t.Fatalf("monitoring_core_vm_retention_period = %v, want 90d", tfvars["monitoring_core_vm_retention_period"])
	}
	if tfvars["monitoring_core_vm_memory_allowed_percent"] != "55" {
		t.Fatalf("monitoring_core_vm_memory_allowed_percent = %v, want 55", tfvars["monitoring_core_vm_memory_allowed_percent"])
	}
	if tfvars["monitoring_core_vm_max_concurrent_requests"] != "8" {
		t.Fatalf("monitoring_core_vm_max_concurrent_requests = %v, want 8", tfvars["monitoring_core_vm_max_concurrent_requests"])
	}
	if tfvars["monitoring_core_gateway_gomemlimit"] != "256MiB" {
		t.Fatalf("monitoring_core_gateway_gomemlimit = %v, want 256MiB", tfvars["monitoring_core_gateway_gomemlimit"])
	}
	if tfvars["monitoring_core_gateway_batch_timeout"] != "20s" {
		t.Fatalf("monitoring_core_gateway_batch_timeout = %v, want 20s", tfvars["monitoring_core_gateway_batch_timeout"])
	}
	if tfvars["monitoring_core_gateway_max_concurrent_streams"] != "75" {
		t.Fatalf("monitoring_core_gateway_max_concurrent_streams = %v, want 75", tfvars["monitoring_core_gateway_max_concurrent_streams"])
	}
	if tfvars["monitoring_core_gateway_memory_limit_mib"] != "200" {
		t.Fatalf("monitoring_core_gateway_memory_limit_mib = %v, want 200", tfvars["monitoring_core_gateway_memory_limit_mib"])
	}
	if tfvars["monitoring_core_gateway_memory_spike_limit_mib"] != "50" {
		t.Fatalf("monitoring_core_gateway_memory_spike_limit_mib = %v, want 50", tfvars["monitoring_core_gateway_memory_spike_limit_mib"])
	}
	if tfvars["monitoring_core_gateway_remote_write_endpoint"] != "http://victoriametrics:18428/api/v1/write" {
		t.Fatalf("monitoring_core_gateway_remote_write_endpoint = %v, want http://victoriametrics:18428/api/v1/write", tfvars["monitoring_core_gateway_remote_write_endpoint"])
	}
}
