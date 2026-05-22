package iac

import (
	"context"
	"testing"
	"time"

	"github.com/kombifyio/stackkits/internal/terramate"
	"github.com/kombifyio/stackkits/internal/tofu"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutionMode(t *testing.T) {
	t.Run("opentofu mode", func(t *testing.T) {
		assert.Equal(t, ExecutionMode("opentofu"), ModeOpenTofu)
	})

	t.Run("terramate mode", func(t *testing.T) {
		assert.Equal(t, ExecutionMode("terramate"), ModeTerramate)
	})
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, ".", cfg.WorkDir)
	assert.Equal(t, ModeOpenTofu, cfg.Mode)
	assert.Equal(t, 60*time.Minute, cfg.Timeout)
	assert.Equal(t, 1, cfg.Parallelism)
	assert.False(t, cfg.AutoApprove)
}

func TestNewExecutor(t *testing.T) {
	t.Run("creates opentofu executor", func(t *testing.T) {
		cfg := &Config{
			WorkDir: "/test",
			Mode:    ModeOpenTofu,
			Timeout: 10 * time.Minute,
		}

		exec, err := NewExecutor(cfg)
		require.NoError(t, err)
		assert.Equal(t, ModeOpenTofu, exec.Mode())
	})

	t.Run("creates terramate executor", func(t *testing.T) {
		cfg := &Config{
			WorkDir: "/test",
			Mode:    ModeTerramate,
			Timeout: 10 * time.Minute,
		}

		exec, err := NewExecutor(cfg)
		require.NoError(t, err)
		assert.Equal(t, ModeTerramate, exec.Mode())
	})

	t.Run("invalid mode returns error", func(t *testing.T) {
		cfg := &Config{
			Mode: "invalid",
		}

		_, err := NewExecutor(cfg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported execution mode")
	})

	t.Run("nil config uses defaults", func(t *testing.T) {
		exec, err := NewExecutor(nil)
		require.NoError(t, err)
		assert.Equal(t, ModeOpenTofu, exec.Mode())
	})
}

func TestIsTransientTofuInitFailure(t *testing.T) {
	assert.True(t, isTransientTofuInitFailure("failed to retrieve cryptographic signature: 502 Bad Gateway"))
	assert.True(t, isTransientTofuInitFailure("could not query provider registry: request failed after 2 attempts"))
	assert.False(t, isTransientTofuInitFailure("Error: Unsupported argument"))
}

func TestIsTransientTofuInitFailureMarkers(t *testing.T) {
	transient := []string{
		"503 service unavailable",
		"504 gateway timeout",
		"connection reset by peer",
		"failed to retrieve cryptographic signature",
		"timeout awaiting response headers",
		"tls handshake timeout",
	}
	for _, marker := range transient {
		t.Run(marker, func(t *testing.T) {
			assert.True(t, isTransientTofuInitFailure(marker))
		})
	}
}

func TestNewExecutorFromSpec(t *testing.T) {
	t.Run("opentofu from spec", func(t *testing.T) {
		spec := &models.StackSpec{
			Mode: "simple",
		}

		exec, err := NewExecutorFromSpec(spec, "/test")
		require.NoError(t, err)
		assert.Equal(t, ModeOpenTofu, exec.Mode())
	})

	t.Run("terramate from spec", func(t *testing.T) {
		spec := &models.StackSpec{
			Mode: "terramate",
		}

		exec, err := NewExecutorFromSpec(spec, "/test")
		require.NoError(t, err)
		assert.Equal(t, ModeTerramate, exec.Mode())
	})

	t.Run("empty mode defaults to opentofu", func(t *testing.T) {
		spec := &models.StackSpec{
			Mode: "",
		}

		exec, err := NewExecutorFromSpec(spec, "/test")
		require.NoError(t, err)
		assert.Equal(t, ModeOpenTofu, exec.Mode())
	})

	t.Run("advanced-terramate mode", func(t *testing.T) {
		spec := &models.StackSpec{
			Mode: "advanced-terramate",
		}

		exec, err := NewExecutorFromSpec(spec, "/test")
		require.NoError(t, err)
		assert.Equal(t, ModeTerramate, exec.Mode())
	})
}

func TestPlanResult(t *testing.T) {
	result := &PlanResult{
		HasChanges: true,
		Add:        2,
		Change:     1,
		Destroy:    0,
		Output:     "Plan: 2 to add, 1 to change, 0 to destroy.",
		Duration:   5 * time.Second,
	}

	assert.True(t, result.HasChanges)
	assert.Equal(t, 2, result.Add)
	assert.Equal(t, 1, result.Change)
	assert.Equal(t, 0, result.Destroy)
}

func TestDriftResult(t *testing.T) {
	result := &DriftResult{
		HasDrift:  true,
		CheckedAt: time.Now(),
		Duration:  10 * time.Second,
		Resources: []DriftedResource{
			{
				Type:    "docker_container",
				Name:    "nginx",
				Address: "docker_container.nginx",
				Action:  "update",
				Details: "env changed",
			},
		},
	}

	assert.True(t, result.HasDrift)
	assert.Len(t, result.Resources, 1)
	assert.Equal(t, "docker_container", result.Resources[0].Type)
}

func TestOpenTofuExecutor(t *testing.T) {
	cfg := &Config{
		WorkDir: "/test",
		Mode:    ModeOpenTofu,
		Timeout: 5 * time.Second,
	}

	exec, err := NewExecutor(cfg)
	require.NoError(t, err)

	t.Run("mode is opentofu", func(t *testing.T) {
		assert.Equal(t, ModeOpenTofu, exec.Mode())
	})

	t.Run("checks installation", func(t *testing.T) {
		// Will return true or false depending on system
		_ = exec.IsInstalled()
	})
}

func TestTerramateExecutor(t *testing.T) {
	cfg := &Config{
		WorkDir:     "/test",
		Mode:        ModeTerramate,
		Timeout:     5 * time.Second,
		Parallelism: 2,
	}

	exec, err := NewExecutor(cfg)
	require.NoError(t, err)

	t.Run("mode is terramate", func(t *testing.T) {
		assert.Equal(t, ModeTerramate, exec.Mode())
	})

	t.Run("checks installation", func(t *testing.T) {
		// Will return true or false depending on system
		_ = exec.IsInstalled()
	})
}

func TestDriftedResource(t *testing.T) {
	resource := DriftedResource{
		Type:    "docker_network",
		Name:    "backend",
		Address: "docker_network.backend",
		Action:  "create",
		Details: "network will be created",
	}

	assert.Equal(t, "docker_network", resource.Type)
	assert.Equal(t, "backend", resource.Name)
	assert.Equal(t, "docker_network.backend", resource.Address)
	assert.Equal(t, "create", resource.Action)
}

func TestTofuResultToExecCopiesFields(t *testing.T) {
	result := &tofu.Result{
		Success:  true,
		Stdout:   "ok",
		Stderr:   "",
		ExitCode: 0,
		Duration: 2 * time.Second,
	}

	got := tofuResultToExec(result)
	assert.Equal(t, true, got.Success)
	assert.Equal(t, "ok", got.Stdout)
	assert.Equal(t, "", got.Stderr)
	assert.Equal(t, 0, got.ExitCode)
	assert.Equal(t, 2*time.Second, got.Duration)
}

func TestTerramateResultToExecCopiesFields(t *testing.T) {
	result := &terramate.Result{
		Success:  false,
		Stdout:   "out",
		Stderr:   "err",
		ExitCode: 1,
		Duration: 3 * time.Second,
	}

	got := tmResultToExec(result)
	assert.Equal(t, false, got.Success)
	assert.Equal(t, "out", got.Stdout)
	assert.Equal(t, "err", got.Stderr)
	assert.Equal(t, 1, got.ExitCode)
	assert.Equal(t, 3*time.Second, got.Duration)
}

func TestConfig(t *testing.T) {
	cfg := &Config{
		WorkDir:     "/custom/path",
		Mode:        ModeTerramate,
		Timeout:     1 * time.Hour,
		Parallelism: 4,
		AutoApprove: true,
	}

	assert.Equal(t, "/custom/path", cfg.WorkDir)
	assert.Equal(t, ModeTerramate, cfg.Mode)
	assert.Equal(t, 1*time.Hour, cfg.Timeout)
	assert.Equal(t, 4, cfg.Parallelism)
	assert.True(t, cfg.AutoApprove)
}

// Integration tests that require the StackKit-packaged OpenTofu binary.
func TestExecutorIntegration(t *testing.T) {
	t.Run("packaged opentofu version if available", func(t *testing.T) {
		exec, _ := NewExecutor(nil)
		if !exec.IsInstalled() {
			t.Skip("StackKit-packaged OpenTofu not available in this test layout")
		}

		ctx := context.Background()
		version, err := exec.Version(ctx)
		require.NoError(t, err)
		assert.NotEmpty(t, version)
	})
}
