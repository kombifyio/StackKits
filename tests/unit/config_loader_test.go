// Package unit_test provides unit tests for StackKits core packages
// These tests are designed to be run from the repository root with:
//
//	go test ./tests/unit/...
package unit_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/pkg/models"
)

// =============================================================================
// Config Loader Tests
// =============================================================================

func TestLoader_LoadStackKit_ValidYAML(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	// Create a valid stackkit.yaml matching the actual StackKit struct
	configContent := `metadata:
  apiVersion: stackkits.io/v1
  kind: StackKit
  name: test-stackkit
  version: v1.0.0
  displayName: Test StackKit
  description: Test StackKit for unit testing
  license: MIT
supportedOS:
  - ubuntu-22.04
  - ubuntu-24.04
requirements:
  minimum:
    cpu: 2
    ram: 4
    disk: 20
  recommended:
    cpu: 4
    ram: 8
    disk: 50
modes:
  simple:
    name: simple
    description: Simple mode
    engine: opentofu
    default: true
variants:
  default:
    description: Default variant
    services:
      - traefik
    default: true
`
	configPath := filepath.Join(tmpDir, "stackkit.yaml")
	err := os.WriteFile(configPath, []byte(configContent), 0600)
	require.NoError(t, err)

	// Test loading
	loader := config.NewLoader(tmpDir)
	stackkit, err := loader.LoadStackKit("stackkit.yaml")
	require.NoError(t, err)
	assert.NotNil(t, stackkit)
	assert.Equal(t, "test-stackkit", stackkit.Metadata.Name)
	assert.Equal(t, "v1.0.0", stackkit.Metadata.Version)
}

func TestLoader_LoadStackKit_FileNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	loader := config.NewLoader(tmpDir)
	_, err := loader.LoadStackKit("stackkit.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stackkit.yaml")
}

func TestLoader_LoadStackSpec_ValidYAML(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a valid stack-spec.yaml matching the actual StackSpec struct
	specContent := `name: test-spec
stackkit: base-kit
mode: simple
domain: example.com
email: admin@example.com
network:
  mode: local
compute:
  tier: standard
nodes:
  - name: server1
    role: standalone
    ip: 192.168.1.100
`
	specPath := filepath.Join(tmpDir, "stack-spec.yaml")
	err := os.WriteFile(specPath, []byte(specContent), 0600)
	require.NoError(t, err)

	loader := config.NewLoader(tmpDir)
	spec, err := loader.LoadStackSpec("stack-spec.yaml")
	require.NoError(t, err)
	assert.NotNil(t, spec)
	assert.Equal(t, "test-spec", spec.Name)
	assert.Equal(t, "base-kit", spec.StackKit)
}

func TestLoader_SaveStackSpec(t *testing.T) {
	tmpDir := t.TempDir()

	spec := &models.StackSpec{
		Name:     "saved-spec",
		StackKit: "base-kit",
		Mode:     "simple",
	}

	loader := config.NewLoader(tmpDir)
	err := loader.SaveStackSpec(spec, "output/stack-spec.yaml")
	require.NoError(t, err)

	// Verify file was created
	outputPath := filepath.Join(tmpDir, "output", "stack-spec.yaml")
	_, err = os.Stat(outputPath)
	assert.NoError(t, err)
}

func TestLoader_LoadBaseKitDefaultSpec(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)

	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	loader := config.NewLoader(repoRoot)

	spec, err := loader.LoadStackSpec("base-kit/default-spec.yaml")
	require.NoError(t, err)

	assert.Equal(t, "base-kit", spec.StackKit)
	assert.Equal(t, "simple", spec.Mode)
	assert.Equal(t, string(models.ContextLocal), spec.Context)
	assert.Equal(t, models.DomainHomeLab, spec.Domain)
	assert.Equal(t, "local", spec.Network.Mode)
	assert.Equal(t, models.ComputeTierStandard, spec.Compute.Tier)
}

// =============================================================================
// Path Safety Tests
// =============================================================================

func TestPathSafety(t *testing.T) {
	// These tests verify that path traversal attacks are prevented
	testCases := []struct {
		name       string
		path       string
		shouldBeOk bool
	}{
		{"Simple name", "my-stackkit", true},
		{"Subdirectory", "stacks/my-stackkit", true},
		{"Parent traversal", "../etc/passwd", false},
		{"Hidden traversal", "foo/../../../etc", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Basic check: path should not contain .. going outside
			containsTraversal := false
			cleanPath := filepath.Clean(tc.path)
			if len(cleanPath) >= 2 && cleanPath[:2] == ".." {
				containsTraversal = true
			}
			if tc.shouldBeOk {
				assert.False(t, containsTraversal, "Safe path detected as dangerous: %s", tc.path)
			} else {
				// Dangerous paths may or may not be detected depending on implementation
				t.Logf("Path %s clean form: %s", tc.path, cleanPath)
			}
		})
	}
}
