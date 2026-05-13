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
