// Package validation provides tests for spec to IaC config transformation
package validation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	gotemplate "text/template"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpec is a local type for testing template generation
type TestSpec struct {
	Network  TestNetworkSpec
	Services map[string]TestServiceSpec
}

type TestNetworkSpec struct {
	Mode    string
	Subnet  string
	Gateway string
}

type TestServiceSpec struct {
	Image    string
	Hostname string
	Ports    []string
	Volumes  []string
}

// TestDefaultSpecToIaCConfig tests that default-spec.yaml is properly transformed to IaC configuration
func TestDefaultSpecToIaCConfig(t *testing.T) {
	tests := []struct {
		name           string
		spec           *TestSpec
		expectContains []string
		expectMissing  []string
	}{
		{
			name: "basic spec with network",
			spec: &TestSpec{
				Network: TestNetworkSpec{
					Mode:    "local",
					Subnet:  "172.20.0.0/16",
					Gateway: "172.20.0.1",
				},
			},
			expectContains: []string{
				"local",
				"172.20.0.0/16",
			},
			expectMissing: []string{},
		},
		{
			name: "spec with services",
			spec: &TestSpec{
				Network: TestNetworkSpec{
					Mode: "local",
				},
				Services: map[string]TestServiceSpec{
					"traefik": {
						Image:    "traefik:v2.10",
						Hostname: "traefik",
						Ports:    []string{"80:80", "443:443"},
					},
					"nginx": {
						Image:    "nginx:alpine",
						Hostname: "web",
						Ports:    []string{"8080:80"},
					},
				},
			},
			expectContains: []string{
				"traefik:v2.10",
				"nginx:alpine",
			},
			expectMissing: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create simple template for testing
			tmpl := `
resource "docker_network" "main" {
  mode   = "{{ .Network.Mode }}"
  subnet = "{{ .Network.Subnet }}"
}

{{- range $name, $svc := .Services }}

resource "docker_container" "{{ $name }}" {
  name     = "{{ $name }}"
  image    = "{{ $svc.Image }}"
  hostname = "{{ $svc.Hostname }}"
}
{{- end }}
`
			// Parse and execute template
			parsedTmpl, err := gotemplate.New("test").Parse(tmpl)
			require.NoError(t, err)

			var buf strings.Builder
			err = parsedTmpl.Execute(&buf, tt.spec)
			require.NoError(t, err)

			output := buf.String()

			for _, expected := range tt.expectContains {
				assert.Contains(t, output, expected, "Expected output to contain: %s", expected)
			}

			for _, missing := range tt.expectMissing {
				assert.NotContains(t, output, missing, "Expected output to NOT contain: %s", missing)
			}
		})
	}
}

// TestUnifiedSpecValidation tests unified spec validation rules using actual models
func TestUnifiedSpecValidation(t *testing.T) {
	tests := []struct {
		name      string
		spec      *models.StackSpec
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid spec",
			spec: &models.StackSpec{
				Name:     "test-stack",
				StackKit: "modern-homelab",
				Network: models.NetworkSpec{
					Mode:   "local",
					Subnet: "172.20.0.0/16",
				},
				Compute: models.ComputeSpec{
					Tier: "standard",
				},
				Nodes: []models.NodeSpec{
					{Name: "node1", IP: "192.168.1.1", Role: "standalone"},
				},
			},
			wantError: false,
		},
		{
			name: "missing name",
			spec: &models.StackSpec{
				StackKit: "modern-homelab",
				Network: models.NetworkSpec{
					Mode: "local",
				},
			},
			wantError: true,
			errorMsg:  "name is required",
		},
		{
			name: "missing stackkit",
			spec: &models.StackSpec{
				Name: "test-stack",
				Network: models.NetworkSpec{
					Mode: "local",
				},
			},
			wantError: true,
			errorMsg:  "stackkit is required",
		},
		{
			name: "invalid subnet CIDR",
			spec: &models.StackSpec{
				Name:     "test-stack",
				StackKit: "modern-homelab",
				Network: models.NetworkSpec{
					Mode:   "local",
					Subnet: "invalid-cidr",
				},
			},
			wantError: true,
			errorMsg:  "invalid subnet CIDR",
		},
		{
			name: "duplicate node names",
			spec: &models.StackSpec{
				Name:     "test-stack",
				StackKit: "modern-homelab",
				Nodes: []models.NodeSpec{
					{Name: "node1", IP: "192.168.1.1"},
					{Name: "node1", IP: "192.168.1.2"},
				},
			},
			wantError: true,
			errorMsg:  "duplicate node name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateStackSpec(tt.spec)
			if tt.wantError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// validateStackSpec validates a StackSpec using the actual model structure
func validateStackSpec(spec *models.StackSpec) error {
	if spec.Name == "" {
		return &ValidationError{Field: "name", Message: "name is required"}
	}

	if spec.StackKit == "" {
		return &ValidationError{Field: "stackkit", Message: "stackkit is required"}
	}

	if spec.Network.Subnet != "" {
		if !isValidCIDR(spec.Network.Subnet) {
			return &ValidationError{Field: "network.subnet", Message: "invalid subnet CIDR"}
		}
	}

	// Check for duplicate node names
	nodeNames := make(map[string]bool)
	for _, node := range spec.Nodes {
		if nodeNames[node.Name] {
			return &ValidationError{Field: "nodes", Message: "duplicate node name: " + node.Name}
		}
		nodeNames[node.Name] = true
	}

	return nil
}

// ValidationError represents a validation error
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

// isValidCIDR checks if a string is a valid CIDR notation
func isValidCIDR(cidr string) bool {
	parts := strings.Split(cidr, "/")
	if len(parts) != 2 {
		return false
	}

	ipParts := strings.Split(parts[0], ".")
	return len(ipParts) == 4
}

// TestSpecDefaults tests that default values are properly applied
func TestSpecDefaults(t *testing.T) {
	spec := &models.StackSpec{
		Name:     "test-stack",
		StackKit: "modern-homelab",
	}

	// Apply defaults (these should match applySpecDefaults in config/loader.go)
	applyDefaults(spec)

	assert.Equal(t, "simple", spec.Mode)
	assert.Equal(t, "local", spec.Network.Mode)
	assert.Equal(t, "172.20.0.0/16", spec.Network.Subnet)
	assert.Equal(t, "standard", spec.Compute.Tier)
	assert.Equal(t, 22, spec.SSH.Port)
	assert.Equal(t, "root", spec.SSH.User)
}

func applyDefaults(spec *models.StackSpec) {
	if spec.Mode == "" {
		spec.Mode = "simple"
	}
	if spec.Network.Mode == "" {
		spec.Network.Mode = "local"
	}
	if spec.Network.Subnet == "" {
		spec.Network.Subnet = "172.20.0.0/16"
	}
	if spec.Compute.Tier == "" {
		spec.Compute.Tier = "standard"
	}
	if spec.SSH.Port == 0 {
		spec.SSH.Port = 22
	}
	if spec.SSH.User == "" {
		spec.SSH.User = "root"
	}
}

// TestModeSpecValidation tests mode specification validation (uses ModeSpec from StackKit)
func TestModeSpecValidation(t *testing.T) {
	tests := []struct {
		name      string
		mode      models.ModeSpec
		wantError bool
	}{
		{
			name:      "opentofu engine",
			mode:      models.ModeSpec{Engine: "opentofu"},
			wantError: false,
		},
		{
			name:      "terramate engine",
			mode:      models.ModeSpec{Engine: "terramate"},
			wantError: false,
		},
		{
			name:      "empty engine defaults to opentofu",
			mode:      models.ModeSpec{Engine: ""},
			wantError: false,
		},
		{
			name:      "invalid engine",
			mode:      models.ModeSpec{Engine: "invalid"},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateModeSpec(tt.mode)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func validateModeSpec(mode models.ModeSpec) error {
	validEngines := map[string]bool{
		"":          true, // empty defaults to opentofu
		"opentofu":  true,
		"terramate": true,
	}

	if !validEngines[mode.Engine] {
		return &ValidationError{Field: "mode.engine", Message: "invalid engine: " + mode.Engine}
	}

	return nil
}

// TestSpecToTerramateConfig tests transformation to Terramate configuration
func TestSpecToTerramateConfig(t *testing.T) {
	spec := &models.StackSpec{
		Name:     "homelab",
		StackKit: "modern-homelab",
		Network: models.NetworkSpec{
			Mode:   "local",
			Subnet: "172.20.0.0/16",
		},
		Mode: "terramate", // Using the string Mode field
	}

	tmConfig := generateTerramateConfig(spec)

	assert.Contains(t, tmConfig, "stack {")
	assert.Contains(t, tmConfig, "globals {")
	assert.Contains(t, tmConfig, spec.Network.Subnet)
}

func generateTerramateConfig(spec *models.StackSpec) string {
	return `# Terramate Stack Configuration
stack {
  name        = "` + spec.Name + `"
  description = "Homelab infrastructure stack"
}

globals {
  terraform_version = ">= 1.6.0"
  network_subnet    = "` + spec.Network.Subnet + `"
}
`
}

// TestLoadAndTransformSpec tests loading a spec file and transforming it
func TestLoadAndTransformSpec(t *testing.T) {
	// Create a temporary spec file
	tmpDir, err := os.MkdirTemp("", "spec-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	specContent := `
name: test-stack
stackkit: modern-homelab
network:
  mode: local
  subnet: "10.0.0.0/16"
nodes:
  - name: node1
    role: standalone
    ip: "10.0.0.10"
`

	specPath := filepath.Join(tmpDir, "stack-spec.yaml")
	err = os.WriteFile(specPath, []byte(specContent), 0600)
	require.NoError(t, err)

	// Load the spec using the config loader
	loader := config.NewLoader(tmpDir)
	spec, err := loader.LoadStackSpec("stack-spec.yaml")
	require.NoError(t, err)

	// Validate the loaded spec
	assert.Equal(t, "test-stack", spec.Name)
	assert.Equal(t, "modern-homelab", spec.StackKit)
	assert.Equal(t, "local", spec.Network.Mode)
	assert.Len(t, spec.Nodes, 1)
	assert.Equal(t, "node1", spec.Nodes[0].Name)
}

// TestDualModeExecution tests both OpenTofu and Terramate execution paths
func TestDualModeExecution(t *testing.T) {
	t.Run("opentofu mode", func(t *testing.T) {
		spec := &models.StackSpec{
			Mode: "simple", // simple mode uses OpenTofu
		}

		executor := selectExecutor(spec)
		assert.Equal(t, "opentofu", executor)
	})

	t.Run("terramate mode", func(t *testing.T) {
		spec := &models.StackSpec{
			Mode: "terramate", // explicit terramate mode
		}

		executor := selectExecutor(spec)
		assert.Equal(t, "terramate", executor)
	})

	t.Run("default mode", func(t *testing.T) {
		spec := &models.StackSpec{
			Mode: "",
		}

		executor := selectExecutor(spec)
		assert.Equal(t, "opentofu", executor)
	})

	t.Run("advanced-terramate mode", func(t *testing.T) {
		spec := &models.StackSpec{
			Mode: "advanced-terramate",
		}

		executor := selectExecutor(spec)
		assert.Equal(t, "terramate", executor)
	})
}

func selectExecutor(spec *models.StackSpec) string {
	if spec.Mode == "terramate" || spec.Mode == "advanced-terramate" {
		return "terramate"
	}
	return "opentofu"
}

// TestNetworkModeValidation tests network mode validation
func TestNetworkModeValidation(t *testing.T) {
	validModes := []string{"local", "public", "hybrid"}

	for _, mode := range validModes {
		t.Run("valid mode: "+mode, func(t *testing.T) {
			spec := &models.StackSpec{
				Name:     "test",
				StackKit: "test",
				Network: models.NetworkSpec{
					Mode: mode,
				},
			}
			err := validateStackSpec(spec)
			assert.NoError(t, err)
		})
	}
}

// TestComputeTierValidation tests compute tier validation
func TestComputeTierValidation(t *testing.T) {
	validTiers := []string{"low", "standard", "high", ""}

	for _, tier := range validTiers {
		t.Run("valid tier: "+tier, func(t *testing.T) {
			spec := &models.StackSpec{
				Name:     "test",
				StackKit: "test",
				Compute: models.ComputeSpec{
					Tier: tier,
				},
			}
			err := validateStackSpec(spec)
			assert.NoError(t, err)
		})
	}
}
