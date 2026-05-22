package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kombifyio/stackkits/internal/testscenarios"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goldenDir is the directory where golden files are stored.
const goldenDir = "testdata/golden"

// updateGolden controls whether golden files should be (re-)written.
// Run with: go test -run TestGolden -update-golden ./cmd/stackkit/commands/
var updateGolden = false

func init() {
	// Check for -update-golden via an env var so tests can regenerate snapshots.
	// Usage: UPDATE_GOLDEN=1 go test -run TestGolden ./cmd/stackkit/commands/
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		updateGolden = true
	}
}

// goldenSpecs returns the canonical set of reference specs whose full TFVars
// output is snapshotted. Each spec represents a distinct deployment archetype.
func goldenSpecs(t *testing.T) map[string]*models.StackSpec {
	t.Helper()

	specs := map[string]*models.StackSpec{
		"local-standard": {
			Name:    "My Homelab",
			Compute: models.ComputeSpec{Tier: models.ComputeTierStandard},
			Nodes: []models.NodeSpec{
				{Name: "node1", Role: "standalone", IP: "192.168.1.100"},
			},
		},
		"local-low": {
			Name:    "Pi Lab",
			Compute: models.ComputeSpec{Tier: models.ComputeTierLow},
			Nodes: []models.NodeSpec{
				{Name: "rpi", Role: "standalone", IP: "192.168.1.50"},
			},
		},
		"cloud-kombify-me": {
			Name:            "Cloud Homelab",
			Domain:          models.DomainKombifyMe,
			SubdomainPrefix: "sh-demo-abc123",
			Compute:         models.ComputeSpec{Tier: models.ComputeTierStandard},
		},
		"cloud-custom-domain": {
			Name:       "Production Lab",
			Domain:     "lab.example.com",
			AdminEmail: "ops@example.com",
			TLS: models.TLSSpec{
				Provider: "cloudflare",
			},
			Compute: models.ComputeSpec{Tier: models.ComputeTierHigh},
		},
		"cloud-coolify-explicit": {
			Name:   "Coolify Lab",
			Domain: "apps.myserver.io",
			PAAS:   models.PAASCoolify,
			TLS: models.TLSSpec{
				Provider: "cloudflare",
			},
			Compute: models.ComputeSpec{Tier: models.ComputeTierStandard},
		},
		"local-services-customized": {
			Name:    "Custom Services",
			Compute: models.ComputeSpec{Tier: models.ComputeTierStandard},
			Services: map[string]any{
				"jellyfin":  map[string]any{"enabled": false},
				"immich":    map[string]any{"enabled": false},
				"dashboard": map[string]any{"enabled": true},
			},
		},
	}

	scenarios, err := testscenarios.LoadAll()
	require.NoError(t, err)
	for _, scenario := range scenarios {
		if scenario.Expected.Failure.MessageContains != "" {
			continue
		}
		spec := scenario.StackSpec
		specs["scenario-"+scenario.ID] = &spec
	}

	return specs
}

// TestGolden_TFVarsSnapshots generates TFVars for each reference spec and compares
// against stored golden files. Run with UPDATE_GOLDEN=1 to regenerate.
func TestGolden_TFVarsSnapshots(t *testing.T) {
	specs := goldenSpecs(t)

	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			// Determine context from spec characteristics
			ctx := models.ContextLocal
			if spec.Domain != "" && !models.IsLocalDomain(spec.Domain) {
				ctx = models.ContextCloud
			}
			setCapabilitiesHome(t, ctx)

			data, err := generateTfvarsJSON(spec, nil)
			require.NoError(t, err, "generateTfvarsJSON failed for %s", name)

			// Pretty-print for stable diffs. docker_host is environment-specific
			// and verified by focused generator tests, so it is excluded here.
			var pretty map[string]any
			require.NoError(t, json.Unmarshal(data, &pretty))
			delete(pretty, "docker_host")
			formatted, err := json.MarshalIndent(pretty, "", "  ")
			require.NoError(t, err)
			formatted = append(formatted, '\n')

			goldenPath := filepath.Join(goldenDir, name+".json")

			if updateGolden {
				require.NoError(t, os.WriteFile(goldenPath, formatted, 0600),
					"failed to write golden file %s", goldenPath)
				t.Logf("Updated golden file: %s", goldenPath)
				return
			}

			expected, err := os.ReadFile(goldenPath)
			if os.IsNotExist(err) {
				t.Fatalf("Golden file %s does not exist. Run with UPDATE_GOLDEN=1 to generate:\n"+
					"  UPDATE_GOLDEN=1 go test -run TestGolden/%s ./cmd/stackkit/commands/",
					goldenPath, name)
			}
			require.NoError(t, err, "failed to read golden file %s", goldenPath)

			assert.JSONEq(t, string(expected), string(formatted),
				"TFVars output differs from golden file %s.\n"+
					"If the change is intentional, regenerate with:\n"+
					"  UPDATE_GOLDEN=1 go test -run TestGolden ./cmd/stackkit/commands/",
				goldenPath)
		})
	}
}

// TestGolden_StructuralConsistency verifies that all golden files exist and
// contain valid JSON with the expected top-level keys.
func TestGolden_StructuralConsistency(t *testing.T) {
	requiredKeys := []string{
		"domain", "network_name", "network_subnet",
		"enable_https", "paas", "reverse_proxy_backend",
		"enable_platform_fallback", "platform_fallback_mode",
		"enable_traefik", "enable_tinyauth", "enable_pocketid",
		"enable_dokploy", "enable_dockge", "enable_coolify", "enable_komodo",
		"enable_dashboard", "enable_homepage", "enable_uptime_kuma", "enable_vaultwarden",
		"enable_jellyfin", "enable_immich",
		"admin_email", "brand_color", "dashboard_title",
	}

	specs := goldenSpecs(t)
	for name := range specs {
		t.Run(name, func(t *testing.T) {
			goldenPath := filepath.Join(goldenDir, name+".json")

			data, err := os.ReadFile(goldenPath)
			if os.IsNotExist(err) {
				t.Skipf("golden file %s not generated yet", goldenPath)
				return
			}
			require.NoError(t, err)

			var vars map[string]any
			require.NoError(t, json.Unmarshal(data, &vars), "golden file is not valid JSON")

			for _, key := range requiredKeys {
				assert.Contains(t, vars, key, "golden file missing required key: %s", key)
			}
		})
	}
}
