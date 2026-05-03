package commands

import (
	"encoding/json"
	"testing"

	"github.com/kombifyio/stackkits/internal/composition"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServiceOverrides_IndividualToggle tests that each service can be
// individually enabled or disabled via spec.Services, overriding the
// tier-based defaults set by the bridge.
func TestServiceOverrides_IndividualToggle(t *testing.T) {
	services := []struct {
		name              string
		tfvar             string
		defaultOnStandard bool // expected default for standard tier + local context
		canDisable        bool
	}{
		{"traefik", "enable_traefik", false, true}, // false because Dokploy provides reverse proxy
		{"tinyauth", "enable_tinyauth", true, true},
		{"pocketid", "enable_pocketid", true, false},
		{"dokploy", "enable_dokploy", true, true},
		{"dockge", "enable_dockge", false, true},
		{"coolify", "enable_coolify", false, true},
		{"dashboard", "enable_dashboard", true, true},
		{"uptime_kuma", "enable_uptime_kuma", true, true},
		{"vaultwarden", "enable_vaultwarden", true, true},
		{"jellyfin", "enable_jellyfin", true, true},
		{"immich", "enable_immich", true, true},
	}

	for _, svc := range services {
		t.Run("disable_"+svc.name, func(t *testing.T) {
			setCapabilitiesHome(t, models.ContextLocal)

			spec := &models.StackSpec{
				Name:    "svc-disable-" + svc.name,
				Compute: models.ComputeSpec{Tier: models.ComputeTierStandard},
				Services: map[string]any{
					svc.name: map[string]any{"enabled": false},
				},
			}

			vars := decodeTFVars(t, spec)
			assert.Equal(t, !svc.canDisable, boolVar(t, vars, svc.tfvar),
				"expected %s to respect mandatory identity-provider policy", svc.tfvar)
		})

		t.Run("enable_"+svc.name, func(t *testing.T) {
			setCapabilitiesHome(t, models.ContextLocal)

			spec := &models.StackSpec{
				Name:    "svc-enable-" + svc.name,
				Compute: models.ComputeSpec{Tier: models.ComputeTierStandard},
				Services: map[string]any{
					svc.name: map[string]any{"enabled": true},
				},
			}

			vars := decodeTFVars(t, spec)
			assert.True(t, boolVar(t, vars, svc.tfvar),
				"expected %s=true when explicitly enabled", svc.tfvar)
		})
	}
}

// TestServiceOverrides_HyphenAlias verifies that "uptime-kuma" (CUE module name)
// works alongside "uptime_kuma" (TFVars key name).
func TestServiceOverrides_HyphenAlias(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	spec := &models.StackSpec{
		Name:    "svc-alias-test",
		Compute: models.ComputeSpec{Tier: models.ComputeTierStandard},
		Services: map[string]any{
			"uptime-kuma": map[string]any{"enabled": false},
		},
	}

	vars := decodeTFVars(t, spec)
	assert.False(t, boolVar(t, vars, "enable_uptime_kuma"),
		"uptime-kuma alias should map to enable_uptime_kuma")
}

// TestServiceOverrides_LowTierOverride verifies that a low-tier default
// (e.g. jellyfin=false) can be overridden by explicit service enable.
func TestServiceOverrides_LowTierOverride(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	spec := &models.StackSpec{
		Name:    "low-with-jellyfin",
		Compute: models.ComputeSpec{Tier: models.ComputeTierLow},
		Services: map[string]any{
			"jellyfin": map[string]any{"enabled": true},
			"immich":   map[string]any{"enabled": true},
		},
	}

	vars := decodeTFVars(t, spec)

	// Low tier defaults: jellyfin=false, immich=false
	// But explicit override should win
	assert.True(t, boolVar(t, vars, "enable_jellyfin"), "explicit enable should override low-tier default")
	assert.True(t, boolVar(t, vars, "enable_immich"), "explicit enable should override low-tier default")
}

// TestServiceOverrides_MultipleServices tests a realistic multi-service
// configuration with mixed enable/disable states.
func TestServiceOverrides_MultipleServices(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	spec := &models.StackSpec{
		Name:    "multi-svc",
		Compute: models.ComputeSpec{Tier: models.ComputeTierStandard},
		Services: map[string]any{
			"jellyfin":    map[string]any{"enabled": false},
			"immich":      map[string]any{"enabled": false},
			"vaultwarden": map[string]any{"enabled": true},
			"dashboard":   map[string]any{"enabled": false},
		},
	}

	vars := decodeTFVars(t, spec)

	assert.False(t, boolVar(t, vars, "enable_jellyfin"), "jellyfin disabled")
	assert.False(t, boolVar(t, vars, "enable_immich"), "immich disabled")
	assert.True(t, boolVar(t, vars, "enable_vaultwarden"), "vaultwarden enabled")
	assert.False(t, boolVar(t, vars, "enable_dashboard"), "dashboard disabled")
	// Services not mentioned should keep their defaults
	assert.True(t, boolVar(t, vars, "enable_uptime_kuma"), "uptime_kuma default intact")
	assert.True(t, boolVar(t, vars, "enable_tinyauth"), "tinyauth default intact")
}

// TestServiceOverrides_CompositionEnableFlags verifies that the composition
// engine's enable flags are correctly overlaid onto the TFVars.
func TestServiceOverrides_CompositionEnableFlags(t *testing.T) {
	setCapabilitiesHome(t, models.ContextLocal)

	spec := &models.StackSpec{
		Name:    "composition-enables",
		Compute: models.ComputeSpec{Tier: models.ComputeTierStandard},
	}

	// Simulate a composition result that enables specific modules
	cr := &composition.CompositionResult{
		EnabledModules: []string{"traefik", "tinyauth", "dashboard", "uptime-kuma"},
		Identity: &composition.IdentityConfig{
			AdminEmail:      "admin@test.local",
			AdminPassword:   "test-password-123",
			TinyAuthEnabled: true,
		},
	}

	data, err := generateTfvarsJSON(spec, cr)
	require.NoError(t, err)

	var vars map[string]any
	require.NoError(t, json.Unmarshal(data, &vars))

	// Composition-enabled modules should be true
	assert.True(t, boolVar(t, vars, "enable_traefik"), "composition enabled traefik")
	assert.True(t, boolVar(t, vars, "enable_tinyauth"), "composition enabled tinyauth")
	assert.True(t, boolVar(t, vars, "enable_dashboard"), "composition enabled dashboard")
	assert.True(t, boolVar(t, vars, "enable_uptime_kuma"), "composition enabled uptime-kuma")

	// Modules NOT in composition result should keep bridge defaults (not forced to false)
	// Bridge default for dokploy is true (standard tier + local → dokploy)
	assert.True(t, boolVar(t, vars, "enable_dokploy"), "non-composition module keeps bridge default")
}

// TestServiceOverrides_CompositionIdentityOverlay verifies that composition
// engine identity credentials are correctly overlaid onto TFVars.
func TestServiceOverrides_CompositionIdentityOverlay(t *testing.T) {
	setCapabilitiesHome(t, models.ContextCloud)

	spec := &models.StackSpec{
		Name:       "identity-overlay",
		Domain:     "example.com",
		AdminEmail: "ops@example.com",
	}

	cr := &composition.CompositionResult{
		EnabledModules: []string{"traefik", "tinyauth", "pocketid"},
		Identity: &composition.IdentityConfig{
			AdminEmail:            "ops@example.com",
			AdminPassword:         "generated-pw-abc123",
			PocketIDEnabled:       true,
			PocketIDAppURL:        "https://id.example.com",
			TinyAuthEnabled:       true,
			TinyAuthOAuthEnabled:  true,
			OIDCIssuerURL:         "https://id.example.com",
			OIDCClientID:          "client-id-123",
			OIDCClientSecret:      "client-secret-456",
			TinyAuthSessionSecret: "session-secret-789",
		},
	}

	data, err := generateTfvarsJSON(spec, cr)
	require.NoError(t, err)

	var vars map[string]any
	require.NoError(t, json.Unmarshal(data, &vars))

	// Admin credentials
	assert.Equal(t, "generated-pw-abc123", vars["admin_password_plaintext"])
	assert.Equal(t, "ops@example.com", vars["admin_email"])
	assert.Contains(t, vars["tinyauth_users"], "ops@example.com:")

	// PocketID. pocketid_encryption_key is injected by generate.go (file-based
	// via EnsureEncryptionKey), not by generateTfvarsJSON, so it is
	// intentionally absent from this overlay.
	assert.True(t, boolVar(t, vars, "enable_pocketid"))
	assert.Equal(t, "https://id.example.com", vars["pocketid_app_url"])

	// TinyAuth OIDC
	assert.True(t, boolVar(t, vars, "tinyauth_oidc_enabled"))
	assert.Equal(t, "https://id.example.com", vars["tinyauth_oidc_issuer"])
	assert.Equal(t, "client-id-123", vars["tinyauth_oidc_client_id"])
	assert.Equal(t, "client-secret-456", vars["tinyauth_oidc_client_secret"])
	assert.Equal(t, "session-secret-789", vars["tinyauth_session_secret"])
}
