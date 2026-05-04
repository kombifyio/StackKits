// Package config tests
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoader(t *testing.T) {
	t.Run("creates loader with base path", func(t *testing.T) {
		loader := NewLoader("/test/path")
		assert.NotNil(t, loader)
	})
}

func TestLoadStackSpec(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "stackkit-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	t.Run("loads valid spec file", func(t *testing.T) {
		specContent := `name: test-deployment
stackkit: base-kit
mode: simple
domain: homelab.local
network:
  mode: local
  subnet: 172.20.0.0/16
compute:
  tier: standard
ssh:
  user: root
  port: 22
`
		specPath := filepath.Join(tmpDir, "stack-spec.yaml")
		err := os.WriteFile(specPath, []byte(specContent), 0600)
		require.NoError(t, err)

		loader := NewLoader(tmpDir)
		spec, err := loader.LoadStackSpec("stack-spec.yaml")

		require.NoError(t, err)
		assert.Equal(t, "test-deployment", spec.Name)
		assert.Equal(t, "base-kit", spec.StackKit)
		assert.Equal(t, "local", spec.Network.Mode)
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		loader := NewLoader(tmpDir)
		_, err := loader.LoadStackSpec("nonexistent.yaml")

		assert.Error(t, err)
	})

	t.Run("applies defaults", func(t *testing.T) {
		specContent := `name: minimal
stackkit: base-kit
`
		specPath := filepath.Join(tmpDir, "minimal-spec.yaml")
		err := os.WriteFile(specPath, []byte(specContent), 0600)
		require.NoError(t, err)

		loader := NewLoader(tmpDir)
		spec, err := loader.LoadStackSpec("minimal-spec.yaml")

		require.NoError(t, err)
		assert.Equal(t, "simple", spec.Mode)
		assert.Equal(t, "local", spec.Network.Mode)
		assert.Equal(t, "", spec.Network.Subnet)
		assert.Equal(t, "standard", spec.Compute.Tier)
		assert.Equal(t, 22, spec.SSH.Port)
		assert.Equal(t, "root", spec.SSH.User)
	})

	t.Run("returns error for invalid YAML", func(t *testing.T) {
		specContent := `invalid: yaml: content: [}`
		specPath := filepath.Join(tmpDir, "invalid.yaml")
		err := os.WriteFile(specPath, []byte(specContent), 0600)
		require.NoError(t, err)

		loader := NewLoader(tmpDir)
		_, err = loader.LoadStackSpec("invalid.yaml")

		assert.Error(t, err)
	})
}

func TestLoadStackSpecKombinationAlias(t *testing.T) {
	t.Run("loads kombination.yaml when default spec is missing", func(t *testing.T) {
		tmpDir := t.TempDir()
		specContent := `name: techstack-intent
stackkit: base-kit
`
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, GetSpecAliasPath()), []byte(specContent), 0600))

		loader := NewLoader(tmpDir)
		_, displayPath, aliasUsed, err := loader.ResolveStackSpecPathForRead(GetDefaultSpecPath())
		require.NoError(t, err)
		assert.Equal(t, GetSpecAliasPath(), displayPath)
		assert.True(t, aliasUsed)

		spec, err := loader.LoadStackSpec(GetDefaultSpecPath())

		require.NoError(t, err)
		assert.Equal(t, "techstack-intent", spec.Name)
		assert.Equal(t, "base-kit", spec.StackKit)
	})

	t.Run("prefers canonical stack-spec.yaml when both files exist", func(t *testing.T) {
		tmpDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, GetDefaultSpecPath()), []byte("name: canonical\nstackkit: base-kit\n"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, GetSpecAliasPath()), []byte("name: alias\nstackkit: base-kit\n"), 0600))

		loader := NewLoader(tmpDir)
		spec, err := loader.LoadStackSpec(GetDefaultSpecPath())

		require.NoError(t, err)
		assert.Equal(t, "canonical", spec.Name)
	})

	t.Run("loads explicit kombination.yaml path", func(t *testing.T) {
		tmpDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, GetSpecAliasPath()), []byte("name: explicit-alias\nstackkit: base-kit\n"), 0600))

		loader := NewLoader(tmpDir)
		spec, err := loader.LoadStackSpec(GetSpecAliasPath())

		require.NoError(t, err)
		assert.Equal(t, "explicit-alias", spec.Name)
	})
}

func TestLoadObservabilityExampleSpecs(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	loader := NewLoader(repoRoot)
	tests := []struct {
		name       string
		path       string
		wantDomain string
		wantTier   string
		wantAddons []string
	}{
		{
			name:       "otlp baseline",
			path:       filepath.Join("base-kit", "examples", "otlp-baseline-spec.yaml"),
			wantDomain: "home.localhost",
			wantTier:   string(models.ComputeTierStandard),
		},
		{
			name:       "otlp victoriametrics",
			path:       filepath.Join("base-kit", "examples", "otlp-victoriametrics-spec.yaml"),
			wantDomain: "observability.example.com",
			wantTier:   string(models.ComputeTierHigh),
			wantAddons: []string{"monitoring-core"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := loader.LoadStackSpec(tt.path)
			require.NoError(t, err)
			assert.Equal(t, "base-kit", spec.StackKit)
			assert.Equal(t, tt.wantDomain, spec.Domain)
			assert.Equal(t, tt.wantTier, spec.Compute.Tier)
			assert.Len(t, spec.Nodes, 1)
			assert.ElementsMatch(t, tt.wantAddons, spec.Addons)
		})
	}
}

func TestSaveStackSpec(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "stackkit-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	t.Run("saves spec file", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "saved-deployment",
			StackKit: "base-kit",
			Mode:     "simple",
			Network: models.NetworkSpec{
				Mode:   "local",
				Subnet: "172.20.0.0/16",
			},
		}

		loader := NewLoader(tmpDir)
		specPath := filepath.Join(tmpDir, "saved-spec.yaml")
		err := loader.SaveStackSpec(spec, specPath)

		require.NoError(t, err)

		// Verify file exists
		_, err = os.Stat(specPath)
		assert.NoError(t, err)

		// Load and verify
		loaded, err := loader.LoadStackSpec("saved-spec.yaml")
		require.NoError(t, err)
		assert.Equal(t, "saved-deployment", loaded.Name)
	})

	t.Run("creates directory if needed", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "nested",
			StackKit: "base-kit",
		}

		loader := NewLoader(tmpDir)
		specPath := filepath.Join(tmpDir, "nested", "dir", "spec.yaml")
		err := loader.SaveStackSpec(spec, specPath)

		require.NoError(t, err)

		_, err = os.Stat(specPath)
		assert.NoError(t, err)
	})
}

func TestLoadStackKit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "stackkit-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	t.Run("loads valid stackkit.yaml", func(t *testing.T) {
		stackkitContent := `metadata:
  apiVersion: stackkit/v1
  kind: StackKit
  name: test-stackkit
  version: 1.0.0
  displayName: Test StackKit
  description: A test stackkit
  license: MIT

supportedOS:
  - ubuntu-24
  - ubuntu-22
  - debian-12

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
    name: Simple
    description: OpenTofu only
    engine: opentofu
    default: true

`
		stackkitPath := filepath.Join(tmpDir, "stackkit.yaml")
		err := os.WriteFile(stackkitPath, []byte(stackkitContent), 0600)
		require.NoError(t, err)

		loader := NewLoader(tmpDir)
		sk, err := loader.LoadStackKit("stackkit.yaml")

		require.NoError(t, err)
		assert.Equal(t, "test-stackkit", sk.Metadata.Name)
		assert.Equal(t, "1.0.0", sk.Metadata.Version)
		assert.Contains(t, sk.SupportedOS, "ubuntu-24")
	})

	t.Run("validates required fields", func(t *testing.T) {
		// Missing name
		stackkitContent := `metadata:
  version: 1.0.0
supportedOS:
  - ubuntu-24
`
		stackkitPath := filepath.Join(tmpDir, "invalid-stackkit.yaml")
		err := os.WriteFile(stackkitPath, []byte(stackkitContent), 0600)
		require.NoError(t, err)

		loader := NewLoader(tmpDir)
		_, err = loader.LoadStackKit("invalid-stackkit.yaml")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "name")
	})
}

func TestDeploymentState(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "stackkit-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	t.Run("saves and loads deployment state", func(t *testing.T) {
		state := &models.DeploymentState{
			StackKit: "base-kit",
			Mode:     "simple",
			Status:   models.StatusRunning,
			Services: []models.ServiceState{
				{
					Name:   "traefik",
					Status: models.ServiceStatusRunning,
					Health: models.HealthStatusHealthy,
				},
			},
		}

		loader := NewLoader(tmpDir)
		statePath := filepath.Join(tmpDir, "state.yaml")

		err := loader.SaveDeploymentState(state, statePath)
		require.NoError(t, err)

		loaded, err := loader.LoadDeploymentState(statePath)
		require.NoError(t, err)
		assert.Equal(t, "base-kit", loaded.StackKit)
		assert.Equal(t, models.StatusRunning, loaded.Status)
		assert.Len(t, loaded.Services, 1)
	})

	t.Run("returns nil for missing state", func(t *testing.T) {
		loader := NewLoader(tmpDir)
		state, err := loader.LoadDeploymentState("nonexistent.yaml")

		assert.NoError(t, err)
		assert.Nil(t, state)
	})
}

func TestExpandPath(t *testing.T) {
	t.Run("expands home directory", func(t *testing.T) {
		home, _ := os.UserHomeDir()
		expanded := ExpandPath("~/test/path")

		assert.Equal(t, filepath.Join(home, "test", "path"), expanded)
	})

	t.Run("leaves absolute paths unchanged", func(t *testing.T) {
		path := "/absolute/path"
		expanded := ExpandPath(path)

		assert.Equal(t, path, expanded)
	})

	t.Run("expands environment variables", func(t *testing.T) {
		t.Setenv("TEST_VAR", "test-value")

		expanded := ExpandPath("$TEST_VAR/path")

		assert.Contains(t, expanded, "test-value")
	})
}

func TestFindStackKitDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "stackkit-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	t.Run("finds stackkit in base path", func(t *testing.T) {
		// Create stackkit directory
		stackkitDir := filepath.Join(tmpDir, "test-stackkit")
		err := os.MkdirAll(stackkitDir, 0750)
		require.NoError(t, err)

		// Create stackkit.yaml
		stackkitPath := filepath.Join(stackkitDir, "stackkit.yaml")
		err = os.WriteFile(stackkitPath, []byte("metadata:\n  name: test"), 0600)
		require.NoError(t, err)

		loader := NewLoader(tmpDir)
		found, err := loader.FindStackKitDir("test-stackkit")

		require.NoError(t, err)
		assert.Equal(t, stackkitDir, found)
	})

	t.Run("returns error for missing stackkit", func(t *testing.T) {
		loader := NewLoader(tmpDir)
		_, err := loader.FindStackKitDir("nonexistent")

		assert.Error(t, err)
	})
}

func TestGetDefaultSpecPath(t *testing.T) {
	t.Run("returns default path", func(t *testing.T) {
		path := GetDefaultSpecPath()
		assert.Equal(t, "stack-spec.yaml", path)
	})
}

func TestGetDeployDir(t *testing.T) {
	t.Run("returns deploy directory", func(t *testing.T) {
		dir := GetDeployDir()
		assert.Equal(t, "deploy", dir)
	})
}

func TestValidateStackKitName(t *testing.T) {
	t.Run("rejects empty name", func(t *testing.T) {
		err := validateStackKitName("")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})

	t.Run("rejects path traversal", func(t *testing.T) {
		err := validateStackKitName("../../../etc/passwd")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "..")
	})

	t.Run("rejects null bytes", func(t *testing.T) {
		err := validateStackKitName("test\x00evil")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid characters")
	})

	t.Run("accepts valid names", func(t *testing.T) {
		validNames := []string{"base-kit", "ha-kit", "modern-homelab", "my_stack"}
		for _, name := range validNames {
			err := validateStackKitName(name)
			assert.NoError(t, err, "name %q should be valid", name)
		}
	})
}

func TestValidateStackKit(t *testing.T) {
	t.Run("rejects missing name", func(t *testing.T) {
		sk := &models.StackKit{
			Metadata:    models.StackKitMetadata{Version: "1.0.0"},
			SupportedOS: []string{"ubuntu"},
		}
		err := validateStackKit(sk)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "name is required")
	})

	t.Run("rejects missing version", func(t *testing.T) {
		sk := &models.StackKit{
			Metadata:    models.StackKitMetadata{Name: "test"},
			SupportedOS: []string{"ubuntu"},
		}
		err := validateStackKit(sk)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "version is required")
	})

	t.Run("rejects missing supported OS", func(t *testing.T) {
		sk := &models.StackKit{
			Metadata: models.StackKitMetadata{Name: "test", Version: "1.0.0"},
		}
		err := validateStackKit(sk)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "at least one OS")
	})

	t.Run("accepts valid stackkit", func(t *testing.T) {
		sk := &models.StackKit{
			Metadata:    models.StackKitMetadata{Name: "test", Version: "1.0.0"},
			SupportedOS: []string{"ubuntu", "debian"},
		}
		err := validateStackKit(sk)
		assert.NoError(t, err)
	})
}

func TestFindStackKitDirPathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewLoader(tmpDir)

	t.Run("rejects path traversal attack", func(t *testing.T) {
		_, err := loader.FindStackKitDir("../../etc")
		assert.Error(t, err)
	})

	t.Run("rejects empty name", func(t *testing.T) {
		_, err := loader.FindStackKitDir("")
		assert.Error(t, err)
	})
}

func TestSaveStackSpecEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	loader := NewLoader(tmpDir)

	t.Run("saves and reloads spec", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "round-trip-test",
			StackKit: "base-kit",
			Network: models.NetworkSpec{
				Mode:   "local",
				Subnet: "172.20.0.0/16",
			},
		}

		specPath := filepath.Join(tmpDir, "test-spec.yaml")
		err := loader.SaveStackSpec(spec, specPath)
		require.NoError(t, err)

		loaded, err := loader.LoadStackSpec(specPath)
		require.NoError(t, err)
		assert.Equal(t, "round-trip-test", loaded.Name)
		assert.Equal(t, "base-kit", loaded.StackKit)
	})

	t.Run("fails for invalid output path", func(t *testing.T) {
		// Create a file, then try to use it as a directory component
		blocker := filepath.Join(tmpDir, "blocker")
		require.NoError(t, os.WriteFile(blocker, []byte("x"), 0600))

		spec := &models.StackSpec{Name: "test", StackKit: "base-kit"}
		err := loader.SaveStackSpec(spec, filepath.Join(blocker, "sub", "spec.yaml"))
		assert.Error(t, err)
	})
}

func TestApplySpecDefaults(t *testing.T) {
	t.Run("applies all defaults to empty spec", func(t *testing.T) {
		spec := &models.StackSpec{}
		applySpecDefaults(spec)

		assert.Equal(t, "simple", spec.Mode)
		assert.Equal(t, "local", spec.Network.Mode)
		assert.Equal(t, "", spec.Network.Subnet)
		assert.Equal(t, "standard", spec.Compute.Tier)
		assert.Equal(t, 22, spec.SSH.Port)
		assert.Equal(t, "root", spec.SSH.User)
	})

	t.Run("applies sveltekit app defaults", func(t *testing.T) {
		spec := &models.StackSpec{
			Domain: "home.lab",
			Apps: map[string]models.AppSpec{
				"web": {
					Image: "ghcr.io/kombify/example-sveltekit:latest",
				},
			},
		}
		applySpecDefaults(spec)

		app := spec.Apps["web"]
		assert.Equal(t, "sveltekit", app.Kind)
		assert.Equal(t, 3000, app.Port)
		assert.Equal(t, "web.home.lab", app.Route.Host)
		assert.Equal(t, "login-gateway", app.Route.Auth)
		assert.Equal(t, "/health", app.Health.Path)
	})

	t.Run("preserves existing values", func(t *testing.T) {
		spec := &models.StackSpec{
			Network: models.NetworkSpec{
				Mode:   "public",
				Subnet: "10.0.0.0/8",
			},
			Compute: models.ComputeSpec{
				Tier: "high",
			},
			SSH: models.SSHSpec{
				Port: 2222,
				User: "ubuntu",
			},
		}
		applySpecDefaults(spec)

		assert.Equal(t, "public", spec.Network.Mode)
		assert.Equal(t, "10.0.0.0/8", spec.Network.Subnet)
		assert.Equal(t, "high", spec.Compute.Tier)
		assert.Equal(t, 2222, spec.SSH.Port)
		assert.Equal(t, "ubuntu", spec.SSH.User)
	})
}
