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
	if tfvars["compute_tier"] != models.ComputeTierStandard {
		t.Fatalf("compute_tier = %v, want %s", tfvars["compute_tier"], models.ComputeTierStandard)
	}
	if tfvars["tinyauth_session_expiry"] != "86400" {
		t.Fatalf("tinyauth_session_expiry = %v, want 86400", tfvars["tinyauth_session_expiry"])
	}
	if tfvars["enable_tinyauth"] != true {
		t.Fatalf("enable_tinyauth = %v, want true", tfvars["enable_tinyauth"])
	}
}

func TestGenerateTFVarsJSONOverlaysStackKitServerImage(t *testing.T) {
	t.Setenv("STACKKIT_SERVER_IMAGE", "stackkit-server:local")
	spec := &models.StackSpec{
		Name:     "prod-ready",
		StackKit: "base-kit",
		Domain:   "home.localhost",
	}
	cr := &CompositionResult{EnabledModules: []string{"dashboard"}}

	data, err := GenerateTFVarsJSON(spec, cr)
	if err != nil {
		t.Fatalf("GenerateTFVarsJSON() error = %v", err)
	}

	var tfvars map[string]any
	if err := json.Unmarshal(data, &tfvars); err != nil {
		t.Fatalf("generated tfvars are not JSON: %v", err)
	}
	if tfvars["stackkit_server_image"] != "stackkit-server:local" {
		t.Fatalf("stackkit_server_image = %v, want stackkit-server:local", tfvars["stackkit_server_image"])
	}
}

func TestGenerateTFVarsJSONSpecStackKitServerImageOverridesEnv(t *testing.T) {
	t.Setenv("STACKKIT_SERVER_IMAGE", "stackkit-server:env")
	spec := &models.StackSpec{
		Name:     "prod-ready",
		StackKit: "base-kit",
		Domain:   "home.localhost",
		Environment: map[string]string{
			"STACKKIT_SERVER_IMAGE": "stackkit-server:spec",
		},
	}
	cr := &CompositionResult{EnabledModules: []string{"dashboard"}}

	data, err := GenerateTFVarsJSON(spec, cr)
	if err != nil {
		t.Fatalf("GenerateTFVarsJSON() error = %v", err)
	}

	var tfvars map[string]any
	if err := json.Unmarshal(data, &tfvars); err != nil {
		t.Fatalf("generated tfvars are not JSON: %v", err)
	}
	if tfvars["stackkit_server_image"] != "stackkit-server:spec" {
		t.Fatalf("stackkit_server_image = %v, want stackkit-server:spec", tfvars["stackkit_server_image"])
	}
}

func TestGenerateTFVarsJSONDoesNotEnableStandaloneTraefikForPaaSManagedProxy(t *testing.T) {
	spec := &models.StackSpec{
		Name:     "basekit-default",
		StackKit: "base-kit",
		Context:  string(models.ContextLocal),
		Domain:   models.DomainHomeLab,
		PAAS:     models.PAASDokploy,
	}
	cr := &CompositionResult{EnabledModules: []string{"traefik", "dokploy", "tinyauth", "pocketid"}}

	data, err := GenerateTFVarsJSON(spec, cr)
	if err != nil {
		t.Fatalf("GenerateTFVarsJSON() error = %v", err)
	}

	var tfvars map[string]any
	if err := json.Unmarshal(data, &tfvars); err != nil {
		t.Fatalf("generated tfvars are not JSON: %v", err)
	}

	if tfvars["reverse_proxy_backend"] != models.ReverseProxyDokploy {
		t.Fatalf("reverse_proxy_backend = %v, want %s", tfvars["reverse_proxy_backend"], models.ReverseProxyDokploy)
	}
	if tfvars["enable_dokploy"] != true {
		t.Fatalf("enable_dokploy = %v, want true", tfvars["enable_dokploy"])
	}
	if tfvars["enable_traefik"] != false {
		t.Fatalf("enable_traefik = %v, want false because Dokploy owns the reverse proxy", tfvars["enable_traefik"])
	}
}
