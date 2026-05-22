package cue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireBaseKit skips the test if the base-kit directory exists but its CUE
// imports cannot be resolved (e.g. missing cue.mod at the repo root). This
// avoids false failures in CI or shallow checkouts.
func requireBaseKit(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Skipf("StackKit directory not found: %s", dir)
	}
	bridge := NewTerraformBridge(dir)
	if err := bridge.ValidateBeforeGeneration(); err != nil && strings.Contains(err.Error(), "cannot find package") {
		t.Skipf("CUE module resolution not available: %v", err)
	}
}

func TestTerraformBridge_GenerateTFVars(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tfbridge-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	tests := []struct {
		name        string
		stackkitDir string
		wantErr     bool
	}{
		{
			name:        "base-kit valid",
			stackkitDir: filepath.Join("..", "..", "base-kit"),
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireBaseKit(t, tt.stackkitDir)

			bridge := NewTerraformBridge(tt.stackkitDir)
			outputDir := filepath.Join(tmpDir, tt.name)

			err := bridge.GenerateTFVars(outputDir)
			if (err != nil) != tt.wantErr {
				t.Errorf("GenerateTFVars() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				outputPath := filepath.Join(outputDir, "terraform.tfvars.json")
				if _, err := os.Stat(outputPath); os.IsNotExist(err) {
					t.Errorf("Expected output file not created: %s", outputPath)
				}
			}
		})
	}
}

func TestTerraformBridge_ValidateBeforeGeneration(t *testing.T) {
	tests := []struct {
		name        string
		stackkitDir string
		wantErr     bool
	}{
		{
			name:        "base-kit valid CUE",
			stackkitDir: filepath.Join("..", "..", "base-kit"),
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireBaseKit(t, tt.stackkitDir)

			bridge := NewTerraformBridge(tt.stackkitDir)
			err := bridge.ValidateBeforeGeneration()
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateBeforeGeneration() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestTerraformBridge_GenerateWithValidation(t *testing.T) {
	stackkitDir := filepath.Join("..", "..", "base-kit")
	requireBaseKit(t, stackkitDir)

	tmpDir := t.TempDir()

	t.Run("generates tfvars after validation", func(t *testing.T) {
		bridge := NewTerraformBridge(stackkitDir)
		outputDir := filepath.Join(tmpDir, "validated-output")

		err := bridge.GenerateWithValidation(outputDir)
		if err != nil {
			t.Errorf("GenerateWithValidation() error = %v", err)
			return
		}

		outputPath := filepath.Join(outputDir, "terraform.tfvars.json")
		if _, err := os.Stat(outputPath); os.IsNotExist(err) {
			t.Errorf("Expected output file not created: %s", outputPath)
		}
	})

	t.Run("fails for non-existent stackkit dir", func(t *testing.T) {
		bridge := NewTerraformBridge("/nonexistent/stackkit/dir")
		err := bridge.GenerateWithValidation(filepath.Join(tmpDir, "bad-output"))
		if err == nil {
			t.Error("Expected error for non-existent directory")
		}
	})
}

func TestTerraformBridge_GenerateTFVars_OutputCreation(t *testing.T) {
	stackkitDir := filepath.Join("..", "..", "base-kit")
	requireBaseKit(t, stackkitDir)

	t.Run("creates nested output directories", func(t *testing.T) {
		tmpDir := t.TempDir()
		nestedOutput := filepath.Join(tmpDir, "a", "b", "c")

		bridge := NewTerraformBridge(stackkitDir)
		err := bridge.GenerateTFVars(nestedOutput)
		if err != nil {
			t.Errorf("GenerateTFVars() should create nested dirs, got error: %v", err)
		}
	})

	t.Run("output contains valid JSON", func(t *testing.T) {
		tmpDir := t.TempDir()

		bridge := NewTerraformBridge(stackkitDir)
		err := bridge.GenerateTFVars(tmpDir)
		if err != nil {
			t.Fatalf("GenerateTFVars() error: %v", err)
		}

		data, err := os.ReadFile(filepath.Join(tmpDir, "terraform.tfvars.json"))
		if err != nil {
			t.Fatalf("Failed to read output: %v", err)
		}

		if len(data) < 2 {
			t.Error("Output file is too small to be valid JSON")
		}
	})
}

// --- Tests for extractTFVars ---

func TestExtractTFVars(t *testing.T) {
	ctx := cuecontext.New()
	bridge := &TerraformBridge{ctx: ctx, stackkitDir: "."}

	t.Run("defaults: working gateway, PocketID, and dashboard enabled", func(t *testing.T) {
		value := ctx.CompileString(`{ foo: "bar" }`)
		require.NoError(t, value.Err())

		tfvars, err := bridge.extractTFVars(value)
		require.NoError(t, err)
		assert.True(t, tfvars.EnableTraefik)
		assert.True(t, tfvars.EnableTinyauth)
		assert.True(t, tfvars.EnablePocketID)
		assert.False(t, tfvars.EnableDokploy)
		assert.False(t, tfvars.EnableDokployApps)
		assert.False(t, tfvars.EnableKomodo)
		assert.False(t, tfvars.EnableKomodo)
		assert.False(t, tfvars.EnableKomodo)
		assert.False(t, tfvars.EnableKomodo)
		assert.True(t, tfvars.EnableCoolify)
		assert.True(t, tfvars.EnableDashboard)
		assert.Empty(t, tfvars.Domain)
		assert.Empty(t, tfvars.NetworkSubnet)
	})

	t.Run("extracts domain from network block", func(t *testing.T) {
		value := ctx.CompileString(`{
			network: {
				domain: "example.com"
			}
		}`)
		require.NoError(t, value.Err())

		tfvars, err := bridge.extractTFVars(value)
		require.NoError(t, err)
		assert.Equal(t, "example.com", tfvars.Domain)
	})

	t.Run("extracts subnet from network block", func(t *testing.T) {
		value := ctx.CompileString(`{
			network: {
				domain: "stack.local"
				subnet: "172.20.0.0/16"
			}
		}`)
		require.NoError(t, value.Err())

		tfvars, err := bridge.extractTFVars(value)
		require.NoError(t, err)
		assert.Equal(t, "stack.local", tfvars.Domain)
		assert.Equal(t, "172.20.0.0/16", tfvars.NetworkSubnet)
	})

	t.Run("extracts from stack sub-path", func(t *testing.T) {
		value := ctx.CompileString(`{
			stack: {
				network: {
					domain: "stack.example.com"
					subnet: "10.0.0.0/24"
				}
			}
		}`)
		require.NoError(t, value.Err())

		tfvars, err := bridge.extractTFVars(value)
		require.NoError(t, err)
		assert.Equal(t, "stack.example.com", tfvars.Domain)
		assert.Equal(t, "10.0.0.0/24", tfvars.NetworkSubnet)
	})

	t.Run("extracts from testStack sub-path", func(t *testing.T) {
		value := ctx.CompileString(`{
			testStack: {
				network: {
					domain: "test.local"
				}
			}
		}`)
		require.NoError(t, value.Err())

		tfvars, err := bridge.extractTFVars(value)
		require.NoError(t, err)
		assert.Equal(t, "test.local", tfvars.Domain)
	})
}

func TestExtractFromStack(t *testing.T) {
	ctx := cuecontext.New()
	bridge := &TerraformBridge{ctx: ctx, stackkitDir: "."}

	t.Run("extracts domain and subnet from stack.network", func(t *testing.T) {
		stack := ctx.CompileString(`{
			network: {
				domain: "prod.example.com"
				subnet: "192.168.100.0/24"
			}
		}`)
		require.NoError(t, stack.Err())

		tfvars := newDefaultTFVars()
		bridge.extractFromStack(stack, tfvars)

		assert.Equal(t, "prod.example.com", tfvars.Domain)
		assert.Equal(t, "192.168.100.0/24", tfvars.NetworkSubnet)
	})

	t.Run("stack without network leaves domain empty", func(t *testing.T) {
		stack := ctx.CompileString(`{ name: "bare" }`)
		require.NoError(t, stack.Err())

		tfvars := newDefaultTFVars()
		bridge.extractFromStack(stack, tfvars)
		assert.Empty(t, tfvars.Domain)
	})

	t.Run("stack network without subnet leaves subnet empty", func(t *testing.T) {
		stack := ctx.CompileString(`{
			network: { domain: "local.dev" }
		}`)
		require.NoError(t, stack.Err())

		tfvars := newDefaultTFVars()
		bridge.extractFromStack(stack, tfvars)
		assert.Equal(t, "local.dev", tfvars.Domain)
		assert.Empty(t, tfvars.NetworkSubnet)
	})
}

func TestWriteTFVars(t *testing.T) {
	ctx := cuecontext.New()
	bridge := &TerraformBridge{ctx: ctx, stackkitDir: "."}

	t.Run("writes valid JSON file with correct fields", func(t *testing.T) {
		tmpDir := t.TempDir()
		tfvars := &TFVars{
			Domain:          "example.com",
			NetworkSubnet:   "172.20.0.0/16",
			EnableTraefik:   true,
			EnableTinyauth:  true,
			EnablePocketID:  true,
			EnableCoolify:   true,
			EnableDashboard: false,
		}

		err := bridge.writeTFVars(tfvars, tmpDir)
		require.NoError(t, err)

		data, err := os.ReadFile(filepath.Join(tmpDir, "terraform.tfvars.json"))
		require.NoError(t, err)

		var parsed TFVars
		require.NoError(t, json.Unmarshal(data, &parsed))
		assert.Equal(t, "example.com", parsed.Domain)
		assert.Equal(t, "172.20.0.0/16", parsed.NetworkSubnet)
		assert.True(t, parsed.EnableTraefik)
		assert.True(t, parsed.EnableCoolify)
		assert.False(t, parsed.EnableDashboard)
	})

	t.Run("fails for invalid output directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		blocker := filepath.Join(tmpDir, "blocker")
		require.NoError(t, os.WriteFile(blocker, []byte("x"), 0600))

		tfvars := &TFVars{EnableTraefik: true}
		err := bridge.writeTFVars(tfvars, filepath.Join(blocker, "sub"))
		assert.Error(t, err)
	})
}

func TestGenerateTFVars_ErrorPaths(t *testing.T) {
	t.Run("fails for non-existent stackkit dir", func(t *testing.T) {
		bridge := NewTerraformBridge("/nonexistent/stackkit")
		tmpDir := t.TempDir()
		err := bridge.GenerateTFVars(tmpDir)
		assert.Error(t, err)
	})

	t.Run("fails for directory with invalid CUE", func(t *testing.T) {
		tmpDir := t.TempDir()
		cueContent := `package broken
name: "test
`
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "bad.cue"), []byte(cueContent), 0600))

		bridge := NewTerraformBridge(tmpDir)
		outputDir := filepath.Join(tmpDir, "output")
		err := bridge.GenerateTFVars(outputDir)
		assert.Error(t, err)
	})

	t.Run("fails for empty directory", func(t *testing.T) {
		emptyDir := t.TempDir()
		bridge := NewTerraformBridge(emptyDir)
		err := bridge.GenerateTFVars(filepath.Join(emptyDir, "output"))
		assert.Error(t, err)
	})
}

func TestValidateStackKit_ErrorPaths(t *testing.T) {
	t.Run("returns result with errors for non-existent directory", func(t *testing.T) {
		validator := NewValidator(".")
		result, err := validator.ValidateStackKit("/nonexistent/dir")
		if err != nil {
			return
		}
		assert.False(t, result.Valid)
		assert.NotEmpty(t, result.Errors)
	})

	t.Run("reports load error for invalid CUE package", func(t *testing.T) {
		tmpDir := t.TempDir()
		cueContent := `package broken
import "nonexistent/module"
x: module.Y
`
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "bad.cue"), []byte(cueContent), 0600))

		validator := NewValidator(tmpDir)
		result, err := validator.ValidateStackKit(tmpDir)
		if err == nil {
			assert.False(t, result.Valid)
			assert.NotEmpty(t, result.Errors)
		}
	})

	t.Run("reports validation error for non-concrete CUE", func(t *testing.T) {
		tmpDir := t.TempDir()
		cueContent := `package test
name: string
version: string
`
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "abstract.cue"), []byte(cueContent), 0600))

		validator := NewValidator(tmpDir)
		result, err := validator.ValidateStackKit(tmpDir)
		require.NoError(t, err)
		assert.False(t, result.Valid)
		assert.NotEmpty(t, result.Errors)
		hasValidationError := false
		for _, e := range result.Errors {
			if e.Code == "VALIDATION_ERROR" {
				hasValidationError = true
				break
			}
		}
		assert.True(t, hasValidationError)
	})
}

func TestValidateBeforeGeneration_Failure(t *testing.T) {
	t.Run("returns error when validation fails", func(t *testing.T) {
		tmpDir := t.TempDir()
		cueContent := `package test
name: string
`
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "abstract.cue"), []byte(cueContent), 0600))

		bridge := NewTerraformBridge(tmpDir)
		err := bridge.ValidateBeforeGeneration()
		assert.Error(t, err)
	})
}

// --- Tests for GenerateTFVarsFromSpec ---

func TestGenerateTFVarsFromSpec(t *testing.T) {
	t.Run("generates tfvars with domain and subnet", func(t *testing.T) {
		setBridgeCapabilitiesHome(t, models.ContextCloud)

		tmpDir := t.TempDir()
		bridge := NewTerraformBridge(tmpDir)

		spec := &models.StackSpec{
			Name:     "my-homelab",
			StackKit: "base-kit",
			Domain:   "homelab.example.com",
			Network:  models.NetworkSpec{Subnet: "172.20.0.0/16"},
		}

		outputDir := filepath.Join(tmpDir, "output")
		err := bridge.GenerateTFVarsFromSpec(spec, outputDir)
		require.NoError(t, err)

		data, err := os.ReadFile(filepath.Join(outputDir, "terraform.tfvars.json"))
		require.NoError(t, err)

		var tfvars TFVars
		require.NoError(t, json.Unmarshal(data, &tfvars))

		assert.Equal(t, "homelab.example.com", tfvars.Domain)
		assert.Equal(t, "172.20.0.0/16", tfvars.NetworkSubnet)
		assert.False(t, tfvars.EnableTraefik)
		assert.True(t, tfvars.EnableTinyauth)
		assert.True(t, tfvars.EnablePocketID)
		assert.False(t, tfvars.EnableDokploy)
		assert.False(t, tfvars.EnableDokployApps)
		assert.True(t, tfvars.EnableCoolify)
		assert.False(t, tfvars.EnablePlatformFallback)
		assert.Equal(t, models.PlatformFallbackDisabled, tfvars.PlatformFallbackMode)
		assert.True(t, tfvars.EnableDashboard)
	})

	t.Run("generates minimal tfvars for empty spec", func(t *testing.T) {
		setBridgeCapabilitiesHome(t, models.ContextLocal)

		tmpDir := t.TempDir()
		bridge := NewTerraformBridge(tmpDir)

		spec := &models.StackSpec{
			Name:     "local-homelab",
			StackKit: "base-kit",
		}

		outputDir := filepath.Join(tmpDir, "output")
		err := bridge.GenerateTFVarsFromSpec(spec, outputDir)
		require.NoError(t, err)

		data, err := os.ReadFile(filepath.Join(outputDir, "terraform.tfvars.json"))
		require.NoError(t, err)

		var tfvars TFVars
		require.NoError(t, json.Unmarshal(data, &tfvars))

		assert.Equal(t, models.DomainHomeLab, tfvars.Domain)
		assert.Equal(t, "", tfvars.NetworkSubnet)
		assert.False(t, tfvars.EnableTraefik)
		assert.True(t, tfvars.EnablePocketID)
		assert.False(t, tfvars.EnableDokploy)
		assert.False(t, tfvars.EnableKomodo)
		assert.True(t, tfvars.EnableCoolify)
		assert.True(t, tfvars.EnableDashboard)
	})

	t.Run("service enabled=false cannot disable mandatory PocketID", func(t *testing.T) {
		setBridgeCapabilitiesHome(t, models.ContextLocal)

		tmpDir := t.TempDir()
		bridge := NewTerraformBridge(tmpDir)

		spec := &models.StackSpec{
			Name:     "custom",
			StackKit: "base-kit",
			Services: map[string]any{
				"pocketid":  map[string]any{"enabled": false},
				"dashboard": map[string]any{"enabled": true},
			},
		}

		outputDir := filepath.Join(tmpDir, "output")
		err := bridge.GenerateTFVarsFromSpec(spec, outputDir)
		require.NoError(t, err)

		data, err := os.ReadFile(filepath.Join(outputDir, "terraform.tfvars.json"))
		require.NoError(t, err)

		var tfvars TFVars
		require.NoError(t, json.Unmarshal(data, &tfvars))

		assert.True(t, tfvars.EnablePocketID)
		assert.True(t, tfvars.EnableDashboard)
		// Others still default to true
		assert.False(t, tfvars.EnableTraefik)
		assert.True(t, tfvars.EnableTinyauth)
	})
}

func TestSpecToTFVars(t *testing.T) {
	bridge := NewTerraformBridge(".")

	t.Run("returns sensible defaults for empty spec", func(t *testing.T) {
		setBridgeCapabilitiesHome(t, models.ContextLocal)

		spec := &models.StackSpec{}
		tfvars, err := bridge.specToTFVars(spec)
		require.NoError(t, err)

		assert.Equal(t, models.DomainHomeLab, tfvars.Domain)
		assert.Equal(t, "", tfvars.NetworkSubnet)
		assert.False(t, tfvars.EnableTraefik)
		assert.True(t, tfvars.EnableTinyauth)
		assert.True(t, tfvars.EnablePocketID)
		assert.False(t, tfvars.EnableDokploy)
		assert.False(t, tfvars.EnableDokployApps)
		assert.True(t, tfvars.EnableCoolify)
		assert.True(t, tfvars.EnableDashboard)
	})

	t.Run("domain is passed through", func(t *testing.T) {
		setBridgeCapabilitiesHome(t, models.ContextCloud)

		spec := &models.StackSpec{Domain: "test.example.com"}
		tfvars, err := bridge.specToTFVars(spec)
		require.NoError(t, err)

		assert.Equal(t, "test.example.com", tfvars.Domain)
	})

	t.Run("network subnet is passed through", func(t *testing.T) {
		setBridgeCapabilitiesHome(t, models.ContextCloud)

		spec := &models.StackSpec{
			Network: models.NetworkSpec{Subnet: "10.10.0.0/16"},
		}
		tfvars, err := bridge.specToTFVars(spec)
		require.NoError(t, err)

		assert.Equal(t, "10.10.0.0/16", tfvars.NetworkSubnet)
	})

	t.Run("service overrides apply over defaults", func(t *testing.T) {
		setBridgeCapabilitiesHome(t, models.ContextLocal)

		spec := &models.StackSpec{
			Services: map[string]any{
				"traefik":   map[string]any{"enabled": false},
				"coolify":   map[string]any{"enabled": false},
				"dashboard": map[string]any{"enabled": true},
			},
		}
		tfvars, err := bridge.specToTFVars(spec)
		require.NoError(t, err)

		assert.False(t, tfvars.EnableTraefik)
		assert.False(t, tfvars.EnableCoolify)
		assert.True(t, tfvars.EnableDashboard)
		// Unaffected defaults remain
		assert.True(t, tfvars.EnableTinyauth)
		assert.True(t, tfvars.EnablePocketID)
	})

	t.Run("explicit fallback disables platform install even with service overrides", func(t *testing.T) {
		setBridgeCapabilitiesHome(t, models.ContextLocal)

		spec := &models.StackSpec{
			PAAS: models.PAASCoolify,
			PlatformFallback: models.PlatformFallbackSpec{
				Enabled: true,
				Mode:    models.PlatformFallbackStandaloneCompose,
			},
			Services: map[string]any{
				"coolify": map[string]any{"enabled": true},
				"dokploy": map[string]any{"enabled": true},
			},
		}
		tfvars, err := bridge.specToTFVars(spec)
		require.NoError(t, err)

		assert.True(t, tfvars.EnablePlatformFallback)
		assert.Equal(t, models.PlatformFallbackStandaloneCompose, tfvars.PlatformFallbackMode)
		assert.Equal(t, models.ReverseProxyStandalone, tfvars.ReverseProxyBackend)
		assert.True(t, tfvars.EnableTraefik)
		assert.False(t, tfvars.EnableCoolify)
		assert.False(t, tfvars.EnableKomodo)
		assert.False(t, tfvars.EnableDokploy)
		assert.False(t, tfvars.EnableDokployApps)
		assert.False(t, tfvars.EnableDockge)
	})

	t.Run("explicit komodo uses stackkit traefik", func(t *testing.T) {
		setBridgeCapabilitiesHome(t, models.ContextLocal)

		spec := &models.StackSpec{
			PAAS: models.PAASKomodo,
		}
		tfvars, err := bridge.specToTFVars(spec)
		require.NoError(t, err)

		assert.Equal(t, models.PAASKomodo, tfvars.Paas)
		assert.Equal(t, models.ReverseProxyStackKit, tfvars.ReverseProxyBackend)
		assert.True(t, tfvars.EnableTraefik)
		assert.True(t, tfvars.EnableKomodo)
		assert.False(t, tfvars.EnableCoolify)
		assert.False(t, tfvars.EnableDokploy)
		assert.False(t, tfvars.EnableDokployApps)
	})
}

func setBridgeCapabilitiesHome(t *testing.T, ctx models.NodeContext) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	capsDir := filepath.Join(home, ".stackkits")
	require.NoError(t, os.MkdirAll(capsDir, 0750))

	caps := models.DockerCapabilities{
		ResolvedContext:  ctx,
		BridgeNetworking: true,
		StorageDriver:    models.StorageOverlay2,
		CPUCores:         4,
		MemoryGB:         8,
		PrivateIP:        "192.168.1.50",
	}

	data, err := json.Marshal(caps)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(capsDir, "capabilities.json"), data, 0600))
}
