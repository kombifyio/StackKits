// Package cue tests
package cue

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidator(t *testing.T) {
	t.Run("creates validator", func(t *testing.T) {
		validator := NewValidator("/test/path")
		assert.NotNil(t, validator)
	})

	t.Run("returns schema directory", func(t *testing.T) {
		basePath := filepath.Join("test", "path")
		validator := NewValidator(basePath)
		schemaDir := validator.GetSchemaDir()
		expected := filepath.Join(basePath, "base")
		assert.Equal(t, expected, schemaDir)
	})
}

func TestValidateSpec(t *testing.T) {
	validator := NewValidator(".")

	t.Run("validates complete spec", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "test-deployment",
			StackKit: "base-kit",
			Mode:     "simple",
			Network: models.NetworkSpec{
				Mode:   "local",
				Subnet: "172.20.0.0/16",
			},
			Compute: models.ComputeSpec{
				Tier: "standard",
			},
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.True(t, result.Valid)
		assert.Empty(t, result.Errors)
	})

	t.Run("fails for missing name", func(t *testing.T) {
		spec := &models.StackSpec{
			StackKit: "base-kit",
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.False(t, result.Valid)
		assert.NotEmpty(t, result.Errors)

		hasNameError := false
		for _, e := range result.Errors {
			if e.Path == "name" {
				hasNameError = true
				break
			}
		}
		assert.True(t, hasNameError)
	})

	t.Run("fails for missing stackkit", func(t *testing.T) {
		spec := &models.StackSpec{
			Name: "test",
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.False(t, result.Valid)
	})

	t.Run("fails for invalid network mode", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "test",
			StackKit: "base-kit",
			Network: models.NetworkSpec{
				Mode: "invalid-mode",
			},
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.False(t, result.Valid)

		hasNetworkError := false
		for _, e := range result.Errors {
			if e.Path == "network.mode" {
				hasNetworkError = true
				break
			}
		}
		assert.True(t, hasNetworkError)
	})

	t.Run("fails for invalid compute tier", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "test",
			StackKit: "base-kit",
			Compute: models.ComputeSpec{
				Tier: "ultra-mega",
			},
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.False(t, result.Valid)
	})

	t.Run("validates valid network modes", func(t *testing.T) {
		validModes := []string{"local", "public", "hybrid"}

		for _, mode := range validModes {
			spec := &models.StackSpec{
				Name:     "test",
				StackKit: "base-kit",
				Network: models.NetworkSpec{
					Mode: mode,
				},
			}

			result, err := validator.ValidateSpec(spec)

			require.NoError(t, err)
			assert.True(t, result.Valid, "mode %s should be valid", mode)
		}
	})

	t.Run("validates valid compute tiers", func(t *testing.T) {
		validTiers := []string{"low", "standard", "high"}

		for _, tier := range validTiers {
			spec := &models.StackSpec{
				Name:     "test",
				StackKit: "base-kit",
				Compute: models.ComputeSpec{
					Tier: tier,
				},
			}

			result, err := validator.ValidateSpec(spec)

			require.NoError(t, err)
			assert.True(t, result.Valid, "tier %s should be valid", tier)
		}
	})

	t.Run("rejects platform bypass for normal stackkits", func(t *testing.T) {
		for _, paas := range []string{models.PAASNone, models.PAASDockge} {
			spec := &models.StackSpec{
				Name:     "test",
				StackKit: "base-kit",
				PAAS:     paas,
				Domain:   "apps.example.com",
				Network:  models.NetworkSpec{Mode: "public"},
				Email:    "owner@example.com",
			}

			result, err := validator.ValidateSpec(spec)

			require.NoError(t, err)
			assert.False(t, result.Valid, "paas %s should be rejected", paas)
			assert.Contains(t, validationErrorPaths(result.Errors), "paas")
		}
	})

	t.Run("rejects local base kit without paas adapter", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:       "test",
			StackKit:   "base-kit",
			Context:    string(models.ContextLocal),
			Domain:     models.DomainHomeLab,
			AdminEmail: "ci@kombify.io",
			Network:    models.NetworkSpec{Mode: "local"},
			PAAS:       models.PAASNone,
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.False(t, result.Valid, "local Base Kit paas=none should be rejected")
		assert.Contains(t, validationErrorPaths(result.Errors), "paas")
		require.NotEmpty(t, result.Errors)
		assert.Contains(t, result.Errors[0].Message, "dokploy")
	})

	t.Run("rejects local base kit with apps and no paas adapter", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:       "test",
			StackKit:   "base-kit",
			Context:    string(models.ContextLocal),
			Domain:     models.DomainHomeLab,
			AdminEmail: "ci@kombify.io",
			Network:    models.NetworkSpec{Mode: "local"},
			PAAS:       models.PAASNone,
			Apps: map[string]models.AppSpec{
				"web": {
					Image: "ghcr.io/kombifyio/stackkits-smoke-sveltekit:0.1.0",
				},
			},
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.False(t, result.Valid)
		assert.Contains(t, validationErrorPaths(result.Errors), "paas")
	})

	t.Run("accepts standard platform adapters", func(t *testing.T) {
		for _, paas := range []string{models.PAASDokploy, models.PAASCoolify} {
			spec := &models.StackSpec{
				Name:     "test",
				StackKit: "base-kit",
				PAAS:     paas,
			}

			result, err := validator.ValidateSpec(spec)

			require.NoError(t, err)
			assert.True(t, result.Valid, "paas %s should be accepted: %#v", paas, result.Errors)
		}
	})

	t.Run("validates nodes", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "test",
			StackKit: "ha-kit",
			Nodes: []models.NodeSpec{
				{Name: "", IP: "192.168.1.10"}, // Missing name
			},
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.False(t, result.Valid)
	})

	t.Run("validates node IP required", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "test",
			StackKit: "ha-kit",
			Nodes: []models.NodeSpec{
				{Name: "node-1", IP: ""}, // Missing IP
			},
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.False(t, result.Valid)
	})

	t.Run("accepts basekit multi-node main worker storage topology", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "test",
			StackKit: "base-kit",
			Nodes: []models.NodeSpec{
				{Name: "main-1", Role: models.NodeRoleMain, IP: "192.168.1.10"},
				{Name: "worker-1", Role: models.NodeRoleWorker, IP: "192.168.1.11"},
				{Name: "storage-1", Role: models.NodeRoleStorage, IP: "192.168.1.12"},
			},
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.True(t, result.Valid, "expected valid multi-node topology: %#v", result.Errors)
	})

	t.Run("rejects multi-node topology without exactly one main role", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "test",
			StackKit: "base-kit",
			Nodes: []models.NodeSpec{
				{Name: "main-1", Role: models.NodeRoleMain, IP: "192.168.1.10"},
				{Name: "main-2", Role: models.NodeRoleControlPlane, IP: "192.168.1.11"},
			},
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.False(t, result.Valid)
		assert.Contains(t, validationErrorPaths(result.Errors), "nodes")
	})

	t.Run("rejects duplicate node names", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "test",
			StackKit: "base-kit",
			Nodes: []models.NodeSpec{
				{Name: "node-1", Role: models.NodeRoleMain, IP: "192.168.1.10"},
				{Name: "node-1", Role: models.NodeRoleWorker, IP: "192.168.1.11"},
			},
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.False(t, result.Valid)
		assert.Contains(t, validationErrorPaths(result.Errors), "nodes[1].name")
	})

	t.Run("warns for missing domain in public mode", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "test",
			StackKit: "base-kit",
			Network: models.NetworkSpec{
				Mode: "public",
			},
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.True(t, result.Valid) // Valid but with warnings
		assert.NotEmpty(t, result.Warnings)
	})

	t.Run("fails for missing email in public mode", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "test",
			StackKit: "base-kit",
			Domain:   "example.com",
			Network: models.NetworkSpec{
				Mode: "public",
			},
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.False(t, result.Valid)
		assert.NotEmpty(t, result.Errors)

		hasEmailError := false
		for _, e := range result.Errors {
			if e.Path == "email" {
				hasEmailError = true
				break
			}
		}
		assert.True(t, hasEmailError)
	})

	t.Run("accepts SaaS auto owner without owner username or owner email", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "test",
			StackKit: "base-kit",
			Context:  string(models.ContextCloud),
			Domain:   "kombify.me",
			Network:  models.NetworkSpec{Mode: "public"},
			Owner: models.OwnerConfig{
				BootstrapMode:       models.OwnerBootstrapModeAuto,
				Source:              models.OwnerSourceCloud,
				RecoveryMaterialRef: "techstack://recovery/stacks/test",
			},
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.True(t, result.Valid, "expected SaaS auto owner to validate without owner.email/owner.username: %#v", result.Errors)
	})

	t.Run("rejects custom owner without explicit owner identity", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:       "test",
			StackKit:   "base-kit",
			Context:    string(models.ContextLocal),
			Domain:     models.DomainHomeLab,
			AdminEmail: "admin@home.localhost",
			Network:    models.NetworkSpec{Mode: "local"},
			Owner: models.OwnerConfig{
				BootstrapMode:          models.OwnerBootstrapModeCustom,
				Source:                 models.OwnerSourceLocal,
				RecoveryPassphraseHash: "$argon2id$v=19$m=65536,t=3,p=4$dGVzdHNhbHQ$dGVzdGhhc2g",
			},
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.False(t, result.Valid)
		assert.Contains(t, validationErrorPaths(result.Errors), "owner.email")
		assert.Contains(t, validationErrorPaths(result.Errors), "owner.username")
	})

	t.Run("accepts explicit owner none lane for OSS BYOS specs", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "test",
			StackKit: "base-kit",
			Context:  string(models.ContextLocal),
			Domain:   models.DomainHomeLab,
			Network:  models.NetworkSpec{Mode: "local"},
			Owner: models.OwnerConfig{
				BootstrapMode: models.OwnerBootstrapModeNone,
			},
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.True(t, result.Valid, "expected owner bootstrap none to validate: %#v", result.Errors)
	})

	t.Run("validates sveltekit app contract", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "test",
			StackKit: "base-kit",
			Apps: map[string]models.AppSpec{
				"web": {
					Kind:  "sveltekit",
					Image: "ghcr.io/kombify/example-sveltekit:latest",
					Port:  3000,
					Route: models.AppRouteSpec{
						Host: "app.home.lab",
						Auth: "login-gateway",
					},
					Health:  models.AppHealthSpec{Path: "/health"},
					Env:     map[string]string{"PUBLIC_APP_NAME": "My App"},
					Secrets: map[string]string{"SESSION_SECRET": "env:SESSION_SECRET"},
				},
			},
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.True(t, result.Valid)
		assert.Empty(t, result.Errors)
	})

	t.Run("fails invalid sveltekit app contract", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "test",
			StackKit: "base-kit",
			Apps: map[string]models.AppSpec{
				"Bad_App": {
					Kind:    "nextjs",
					Port:    70000,
					Health:  models.AppHealthSpec{Path: "health"},
					Route:   models.AppRouteSpec{Auth: "none"},
					Env:     map[string]string{"BAD-NAME": "x"},
					Secrets: map[string]string{"SESSION_SECRET": "literal-secret"},
				},
			},
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.False(t, result.Valid)
		assert.NotEmpty(t, result.Errors)
	})

	t.Run("fails invalid sveltekit route host", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "test",
			StackKit: "base-kit",
			Apps: map[string]models.AppSpec{
				"web": {
					Image: "ghcr.io/kombifyio/stackkits-smoke-sveltekit:0.1.0",
					Route: models.AppRouteSpec{Host: "https://app.example.com/path"},
				},
			},
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.False(t, result.Valid)
		assert.Contains(t, validationErrorPaths(result.Errors), "apps.web.route.host")
	})

	t.Run("fails invalid app setup policy", func(t *testing.T) {
		spec := &models.StackSpec{
			Name:     "test",
			StackKit: "base-kit",
			Apps: map[string]models.AppSpec{
				"web": {
					Image: "ghcr.io/kombifyio/stackkits-smoke-sveltekit:0.1.0",
					Setup: models.AppSetupSpec{Policy: "always"},
				},
			},
		}

		result, err := validator.ValidateSpec(spec)

		require.NoError(t, err)
		assert.False(t, result.Valid)
		assert.Contains(t, validationErrorPaths(result.Errors), "apps.web.setup.policy")
	})
}

func validationErrorPaths(errors []models.ValidationError) []string {
	paths := make([]string, 0, len(errors))
	for _, err := range errors {
		paths = append(paths, err.Path)
	}
	return paths
}

func TestValidateCUEFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cue-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	validator := NewValidator(tmpDir)

	t.Run("validates valid CUE file", func(t *testing.T) {
		cueContent := `package test

name: "test"
version: "1.0.0"
`
		cuePath := filepath.Join(tmpDir, "valid.cue")
		err := os.WriteFile(cuePath, []byte(cueContent), 0600)
		require.NoError(t, err)

		result, err := validator.ValidateCUEFile(cuePath)

		require.NoError(t, err)
		assert.True(t, result.Valid)
	})

	t.Run("fails for invalid CUE syntax", func(t *testing.T) {
		cueContent := `package test

name: "test
version: "1.0.0"
`
		cuePath := filepath.Join(tmpDir, "invalid.cue")
		err := os.WriteFile(cuePath, []byte(cueContent), 0600)
		require.NoError(t, err)

		result, err := validator.ValidateCUEFile(cuePath)

		require.NoError(t, err)
		assert.False(t, result.Valid)
		assert.NotEmpty(t, result.Errors)
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		_, err := validator.ValidateCUEFile("/nonexistent/file.cue")

		assert.Error(t, err)
	})
}

func TestValidateStackKit_UsesWorkspaceModuleRoot(t *testing.T) {
	root := t.TempDir()
	writeCueModule(t, root, "example.com/workspace@v0")

	baseDir := filepath.Join(root, "base")
	require.NoError(t, os.MkdirAll(baseDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "module.cue"), []byte(`package base

#Thing: {
	name: string
}
`), 0600))

	kitDir := filepath.Join(root, "base-kit")
	require.NoError(t, os.MkdirAll(kitDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(kitDir, "stackfile.cue"), []byte(`package base_kit

import "example.com/workspace/base"

thing: base.#Thing & {
	name: "ok"
}
`), 0600))

	validator := NewValidator(root)
	result, err := validator.ValidateStackKit(kitDir)
	require.NoError(t, err)
	require.True(t, result.Valid, "errors: %#v", result.Errors)
}

func TestValidateStackKit_DoesNotCreateNestedCueModuleWhenWorkspaceExists(t *testing.T) {
	root := t.TempDir()
	writeCueModule(t, root, "example.com/workspace@v0")

	baseDir := filepath.Join(root, "base")
	require.NoError(t, os.MkdirAll(baseDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "module.cue"), []byte(`package base

#Thing: {
	name: string
}
`), 0600))

	kitDir := filepath.Join(root, "base-kit")
	require.NoError(t, os.MkdirAll(kitDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(kitDir, "stackfile.cue"), []byte(`package base_kit

import "example.com/workspace/base"

thing: base.#Thing & {
	name: "ok"
}
`), 0600))

	validator := NewValidator(root)
	result, err := validator.ValidateStackKit(kitDir)
	require.NoError(t, err)
	require.True(t, result.Valid, "errors: %#v", result.Errors)

	_, err = os.Stat(filepath.Join(kitDir, "cue.mod", "module.cue"))
	require.True(t, os.IsNotExist(err), "validator should not create nested cue.mod in a workspace checkout")
}

func writeCueModule(t *testing.T, dir, module string) {
	t.Helper()
	modDir := filepath.Join(dir, "cue.mod")
	require.NoError(t, os.MkdirAll(modDir, 0750))
	content := `module: "` + module + `"
language: {
	version: "v0.9.0"
}
`
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "module.cue"), []byte(content), 0600))
}
