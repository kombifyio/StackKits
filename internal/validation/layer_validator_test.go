package validation

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLayerValidator_ValidateStackKit_Complete tests full validation with all layers present
func TestLayerValidator_ValidateStackKit_Complete(t *testing.T) {
	// Create temporary test StackKit
	tmpDir := t.TempDir()
	stackkitDir := filepath.Join(tmpDir, "test-stackkit")
	require.NoError(t, os.MkdirAll(stackkitDir, 0750))

	// Write complete CUE file with all 3 layers
	cueContent := `
package test

// Layer 1: Foundation
system: {
	timezone: "UTC"
	locale:   "en_US.UTF-8"
}

packages: {
	base: ["curl", "wget", "git"]
	extra: ["htop"]
}

security: {
	ssh: {
		port: 22
		permitRootLogin: "no"
		passwordAuth: false
		pubkeyAuth: true
	}
	firewall: {
		enabled: true
		backend: "ufw"
		defaultInbound: "deny"
	}
}

// Layer 2: Platform
platform: "docker"

container: {
	engine: "docker"
	rootless: false
}

network: {
	defaults: {
		subnet: "172.20.0.0/16"
	}
}

// Layer 3: Applications
services: {
	dokploy: {
		name: "dokploy"
		type: "paas"
		enabled: true
		image: "dokploy/dokploy:latest"
	}
}
`
	cuePath := filepath.Join(stackkitDir, "test.cue")
	require.NoError(t, os.WriteFile(cuePath, []byte(cueContent), 0600))

	validator := NewLayerValidator(tmpDir)
	result, err := validator.ValidateStackKit(stackkitDir)

	require.NoError(t, err)
	assert.True(t, result.Valid, "Expected valid StackKit")
	assert.True(t, result.Layer1.Valid, "Layer 1 should be valid")
	assert.True(t, result.Layer2.Valid, "Layer 2 should be valid")
	assert.True(t, result.Layer3.Valid, "Layer 3 should be valid")
}

// TestLayerValidator_ValidateStackKit_MissingLayer1 tests missing foundation fields
func TestLayerValidator_ValidateStackKit_MissingLayer1(t *testing.T) {
	tmpDir := t.TempDir()
	stackkitDir := filepath.Join(tmpDir, "test-stackkit")
	require.NoError(t, os.MkdirAll(stackkitDir, 0750))

	// Missing system configuration
	cueContent := `
package test

// Missing Layer 1 system config

// Layer 2: Platform
platform: "docker"

container: {
	engine: "docker"
}

network: {
	defaults: {
		subnet: "172.20.0.0/16"
	}
}

// Layer 3: Applications
services: {
	dokploy: {
		type: "paas"
		enabled: true
	}
}
`
	cuePath := filepath.Join(stackkitDir, "test.cue")
	require.NoError(t, os.WriteFile(cuePath, []byte(cueContent), 0600))

	validator := NewLayerValidator(tmpDir)
	result, err := validator.ValidateStackKit(stackkitDir)

	require.NoError(t, err)
	assert.False(t, result.Valid, "Expected invalid StackKit")
	assert.False(t, result.Layer1.Valid, "Layer 1 should be invalid")
	assert.NotEmpty(t, result.Layer1.Errors, "Layer 1 should have errors")

	// Check specific error
	found := false
	for _, e := range result.Layer1.Errors {
		if e.Code == "L1_MISSING_SYSTEM" {
			found = true
			assert.Contains(t, e.Message, "system")
			break
		}
	}
	assert.True(t, found, "Should have L1_MISSING_SYSTEM error")
}

// TestLayerValidator_ValidateStackKit_MissingLayer2 tests missing platform declaration
func TestLayerValidator_ValidateStackKit_MissingLayer2(t *testing.T) {
	tmpDir := t.TempDir()
	stackkitDir := filepath.Join(tmpDir, "test-stackkit")
	require.NoError(t, os.MkdirAll(stackkitDir, 0750))

	// Missing platform declaration
	cueContent := `
package test

// Layer 1: Foundation
system: {
	timezone: "UTC"
}

packages: {
	base: ["curl"]
}

security: {
	ssh: {
		port: 22
		permitRootLogin: "no"
	}
	firewall: {
		enabled: true
		backend: "ufw"
	}
}

// Missing Layer 2 platform declaration

container: {
	engine: "docker"
}

network: {
	defaults: {
		subnet: "172.20.0.0/16"
	}
}

// Layer 3: Applications
services: {
	dokploy: {
		type: "paas"
		enabled: true
	}
}
`
	cuePath := filepath.Join(stackkitDir, "test.cue")
	require.NoError(t, os.WriteFile(cuePath, []byte(cueContent), 0600))

	validator := NewLayerValidator(tmpDir)
	result, err := validator.ValidateStackKit(stackkitDir)

	require.NoError(t, err)
	assert.False(t, result.Valid, "Expected invalid StackKit")
	assert.False(t, result.Layer2.Valid, "Layer 2 should be invalid")

	found := false
	for _, e := range result.Layer2.Errors {
		if e.Code == "L2_MISSING_PLATFORM" {
			found = true
			assert.Contains(t, e.Message, "platform not declared")
			break
		}
	}
	assert.True(t, found, "Should have L2_MISSING_PLATFORM error")
}

// TestLayerValidator_ValidateStackKit_Layer3WithoutPAAS tests that Layer 3 passes without PAAS
// (PAAS belongs in Layer 2, not Layer 3)
func TestLayerValidator_ValidateStackKit_Layer3WithoutPAAS(t *testing.T) {
	tmpDir := t.TempDir()
	stackkitDir := filepath.Join(tmpDir, "test-stackkit")
	require.NoError(t, os.MkdirAll(stackkitDir, 0750))

	// Services without PAAS — this is correct for Layer 3
	cueContent := `
package test

// Layer 1: Foundation
system: {
	timezone: "UTC"
}

packages: {
	base: ["curl"]
}

security: {
	ssh: {
		port: 22
		permitRootLogin: "no"
	}
	firewall: {
		enabled: true
		backend: "ufw"
	}
}

// Layer 2: Platform
platform: "docker"

container: {
	engine: "docker"
}

network: {
	defaults: {
		subnet: "172.20.0.0/16"
	}
}

// Layer 3: Applications (no PAAS — correct, PAAS is Layer 2)
services: {
	whoami: {
		type: "utility"
		enabled: true
	}
}
`
	cuePath := filepath.Join(stackkitDir, "test.cue")
	require.NoError(t, os.WriteFile(cuePath, []byte(cueContent), 0600))

	validator := NewLayerValidator(tmpDir)
	result, err := validator.ValidateStackKit(stackkitDir)

	require.NoError(t, err)
	assert.True(t, result.Layer3.Valid, "Layer 3 should be valid without PAAS services")

	// Should NOT have L3_MISSING_PAAS error
	for _, e := range result.Layer3.Errors {
		assert.NotEqual(t, "L3_MISSING_PAAS", e.Code, "Should not require PAAS in Layer 3")
	}
}

// TestLayerValidator_ValidateStackKit_InvalidPlatform tests invalid platform value
func TestLayerValidator_ValidateStackKit_InvalidPlatform(t *testing.T) {
	tmpDir := t.TempDir()
	stackkitDir := filepath.Join(tmpDir, "test-stackkit")
	require.NoError(t, os.MkdirAll(stackkitDir, 0750))

	cueContent := `
package test

// Layer 1: Foundation
system: { timezone: "UTC" }
packages: { base: ["curl"] }
security: {
	ssh: { port: 22, permitRootLogin: "no" }
	firewall: { enabled: true, backend: "ufw" }
}

// Layer 2: Invalid platform
platform: "invalid-platform"

container: {
	engine: "docker"
}

network: {
	defaults: { subnet: "172.20.0.0/16" }
}

// Layer 3: Applications
services: {
	dokploy: { type: "paas", enabled: true }
}
`
	cuePath := filepath.Join(stackkitDir, "test.cue")
	require.NoError(t, os.WriteFile(cuePath, []byte(cueContent), 0600))

	validator := NewLayerValidator(tmpDir)
	result, err := validator.ValidateStackKit(stackkitDir)

	require.NoError(t, err)
	assert.False(t, result.Valid, "Expected invalid StackKit")

	found := false
	for _, e := range result.Layer2.Errors {
		if e.Code == "L2_INVALID_PLATFORM_VALUE" {
			found = true
			break
		}
	}
	assert.True(t, found, "Should have L2_INVALID_PLATFORM_VALUE error")
}

// TestLayerValidator_ValidateStackKit_PAASByName tests PAAS detection by service name
func TestLayerValidator_ValidateStackKit_PAASByName(t *testing.T) {
	tmpDir := t.TempDir()
	stackkitDir := filepath.Join(tmpDir, "test-stackkit")
	require.NoError(t, os.MkdirAll(stackkitDir, 0750))

	// PAAS detected by name (not type)
	cueContent := `
package test

// Layer 1: Foundation
system: { timezone: "UTC" }
packages: { base: ["curl"] }
security: {
	ssh: { port: 22, permitRootLogin: "no" }
	firewall: { enabled: true, backend: "ufw" }
}

// Layer 2: Platform
platform: "docker"

container: {
	engine: "docker"
}

network: {
	defaults: { subnet: "172.20.0.0/16" }
}

// Layer 3: Applications (PAAS detected by name)
services: {
	dokploy: {
		name: "dokploy"
		enabled: true
		image: "dokploy/dokploy:latest"
	}
}
`
	cuePath := filepath.Join(stackkitDir, "test.cue")
	require.NoError(t, os.WriteFile(cuePath, []byte(cueContent), 0600))

	validator := NewLayerValidator(tmpDir)
	result, err := validator.ValidateStackKit(stackkitDir)

	require.NoError(t, err)
	assert.True(t, result.Valid, "Expected valid StackKit - PAAS detected by name")
	assert.True(t, result.Layer3.Valid, "Layer 3 should be valid - dokploy name detected")
}

// testMissingComponent is a helper that validates a StackKit with specific CUE content
// is invalid and produces the expected error code in the given layer's errors.
func testMissingComponent(t *testing.T, cueContent string, layerErrors func(*StackKitValidationResult) []LayerError, expectedErrorCode string) {
	t.Helper()

	tmpDir := t.TempDir()
	stackkitDir := filepath.Join(tmpDir, "test-stackkit")
	require.NoError(t, os.MkdirAll(stackkitDir, 0750))

	cuePath := filepath.Join(stackkitDir, "test.cue")
	require.NoError(t, os.WriteFile(cuePath, []byte(cueContent), 0600))

	validator := NewLayerValidator(tmpDir)
	result, err := validator.ValidateStackKit(stackkitDir)

	require.NoError(t, err)
	assert.False(t, result.Valid)

	found := false
	for _, e := range layerErrors(result) {
		if e.Code == expectedErrorCode {
			found = true
			break
		}
	}
	assert.True(t, found, "Should have %s error", expectedErrorCode)
}

func layer1Errors(r *StackKitValidationResult) []LayerError { return r.Layer1.Errors }
func layer2Errors(r *StackKitValidationResult) []LayerError { return r.Layer2.Errors }
func layer3Errors(r *StackKitValidationResult) []LayerError { return r.Layer3.Errors }

func TestLayerValidator_ValidateStackKit_MissingPackages(t *testing.T) {
	testMissingComponent(t, `
package test

// Layer 1: Foundation (missing packages)
system: { timezone: "UTC" }
security: {
	ssh: { port: 22 }
	firewall: { enabled: true }
}

// Layer 2: Platform
platform: "docker"
container: { engine: "docker" }
network: { defaults: { subnet: "172.20.0.0/16" } }

// Layer 3: Applications
services: { dokploy: { type: "paas", enabled: true } }
`, layer1Errors, "L1_MISSING_PACKAGES")
}

func TestLayerValidator_ValidateStackKit_MissingSSH(t *testing.T) {
	testMissingComponent(t, `
package test

// Layer 1: Foundation (missing SSH)
system: { timezone: "UTC" }
packages: { base: ["curl"] }
security: {
	firewall: { enabled: true }
}

// Layer 2: Platform
platform: "docker"
container: { engine: "docker" }
network: { defaults: { subnet: "172.20.0.0/16" } }

// Layer 3: Applications
services: { dokploy: { type: "paas", enabled: true } }
`, layer1Errors, "L1_MISSING_SSH")
}

func TestLayerValidator_ValidateStackKit_MissingFirewall(t *testing.T) {
	testMissingComponent(t, `
package test

// Layer 1: Foundation (missing firewall)
system: { timezone: "UTC" }
packages: { base: ["curl"] }
security: {
	ssh: { port: 22 }
}

// Layer 2: Platform
platform: "docker"
container: { engine: "docker" }
network: { defaults: { subnet: "172.20.0.0/16" } }

// Layer 3: Applications
services: { dokploy: { type: "paas", enabled: true } }
`, layer1Errors, "L1_MISSING_FIREWALL")
}

func TestLayerValidator_ValidateStackKit_MissingContainer(t *testing.T) {
	testMissingComponent(t, `
package test

// Layer 1: Foundation
system: { timezone: "UTC" }
packages: { base: ["curl"] }
security: {
	ssh: { port: 22 }
	firewall: { enabled: true }
}

// Layer 2: Platform (missing container)
platform: "docker"
network: { defaults: { subnet: "172.20.0.0/16" } }

// Layer 3: Applications
services: { dokploy: { type: "paas", enabled: true } }
`, layer2Errors, "L2_MISSING_CONTAINER")
}

func TestLayerValidator_ValidateStackKit_MissingNetworkDefaults(t *testing.T) {
	testMissingComponent(t, `
package test

// Layer 1: Foundation
system: { timezone: "UTC" }
packages: { base: ["curl"] }
security: {
	ssh: { port: 22 }
	firewall: { enabled: true }
}

// Layer 2: Platform (missing network.defaults)
platform: "docker"
container: { engine: "docker" }

// Layer 3: Applications
services: { dokploy: { type: "paas", enabled: true } }
`, layer2Errors, "L2_MISSING_NETWORK")
}

// TestLayerValidator_ValidateStackKit_AllPlatformTypes tests all valid platform types
func TestLayerValidator_ValidateStackKit_AllPlatformTypes(t *testing.T) {
	validPlatforms := []string{"docker", "docker-swarm", "bare-metal"}

	for _, platform := range validPlatforms {
		t.Run(platform, func(t *testing.T) {
			tmpDir := t.TempDir()
			stackkitDir := filepath.Join(tmpDir, "test-stackkit")
			require.NoError(t, os.MkdirAll(stackkitDir, 0750))

			cueContent := fmt.Sprintf(`
package test

system: { timezone: "UTC" }
packages: { base: ["curl"] }
security: {
	ssh: { port: 22 }
	firewall: { enabled: true }
}

platform: "%s"
container: { engine: "docker" }
network: { defaults: { subnet: "172.20.0.0/16" } }

services: { dokploy: { type: "paas", enabled: true } }
`, platform)

			cuePath := filepath.Join(stackkitDir, "test.cue")
			require.NoError(t, os.WriteFile(cuePath, []byte(cueContent), 0600))

			validator := NewLayerValidator(tmpDir)
			result, err := validator.ValidateStackKit(stackkitDir)

			require.NoError(t, err)
			assert.True(t, result.Valid, "Platform %s should be valid", platform)
		})
	}
}

func TestLayerValidator_ValidateStackKit_MissingServices(t *testing.T) {
	testMissingComponent(t, `
package test

// Layer 1: Foundation
system: { timezone: "UTC" }
packages: { base: ["curl"] }
security: {
	ssh: { port: 22 }
	firewall: { enabled: true }
}

// Layer 2: Platform
platform: "docker"
container: { engine: "docker" }
network: { defaults: { subnet: "172.20.0.0/16" } }

// Layer 3: Applications - NO SERVICES DEFINED
`, layer3Errors, "L3_MISSING_SERVICES")
}

// TestLayerValidator_LayerString tests the Layer String() method
func TestLayerValidator_LayerString(t *testing.T) {
	tests := []struct {
		layer    Layer
		expected string
	}{
		{LayerFoundation, "Foundation"},
		{LayerPlatform, "Platform"},
		{LayerApplications, "Applications"},
		{Layer(99), "Layer99"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.layer.String())
		})
	}
}

// TestLayerError_Error tests the LayerError Error() method
func TestLayerError_Error(t *testing.T) {
	err := &LayerError{
		Layer:   LayerFoundation,
		Code:    "TEST_ERROR",
		Message: "Test error message",
	}

	assert.Contains(t, err.Error(), "ERROR:")
	assert.Contains(t, err.Error(), "Test error message")
}
