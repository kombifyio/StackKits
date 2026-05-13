package terramate

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewExecutor(t *testing.T) {
	t.Run("default configuration", func(t *testing.T) {
		e := NewExecutor()
		assert.Equal(t, ".", e.workDir)
		assert.Equal(t, "terramate", e.binary)
		assert.Equal(t, 30*time.Minute, e.timeout)
		assert.True(t, e.changeDetect)
		assert.Equal(t, 1, e.parallelism)
		assert.Equal(t, "tofu", e.tofuBinary)
	})

	t.Run("with custom options", func(t *testing.T) {
		e := NewExecutor(
			WithWorkDir("/custom/path"),
			WithBinary("/usr/local/bin/terramate"),
			WithTimeout(10*time.Minute),
			WithChangeDetection(false),
			WithParallelism(4),
			WithTofuBinary("terraform"),
		)
		assert.Equal(t, "/custom/path", e.workDir)
		assert.Equal(t, "/usr/local/bin/terramate", e.binary)
		assert.Equal(t, 10*time.Minute, e.timeout)
		assert.False(t, e.changeDetect)
		assert.Equal(t, 4, e.parallelism)
		assert.Equal(t, "terraform", e.tofuBinary)
	})
}

func TestIsInstalled(t *testing.T) {
	t.Run("non-existent binary", func(t *testing.T) {
		e := NewExecutor(WithBinary("nonexistent-binary-12345"))
		assert.False(t, e.IsInstalled())
	})
}

func TestParsePlanChanges(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected []Change
	}{
		{
			name:     "empty output",
			output:   "",
			expected: nil,
		},
		{
			name: "single create",
			output: `
Terraform will perform the following actions:

  # docker_container.traefik will be created
  + resource "docker_container" "traefik" {
      + name = "traefik"
    }

Plan: 1 to add, 0 to change, 0 to destroy.
`,
			expected: []Change{
				{ResourceType: "docker_container", ResourceName: "traefik", Action: "create"},
			},
		},
		{
			name: "multiple changes",
			output: `
  # docker_container.app will be updated in-place
  ~ resource "docker_container" "app" {
      ~ env = []
    }

  # docker_network.backend will be created
  + resource "docker_network" "backend" {
      + name = "backend"
    }

  # docker_volume.data will be destroyed
  - resource "docker_volume" "data" {
      - name = "data"
    }
`,
			expected: []Change{
				{ResourceType: "docker_container", ResourceName: "app", Action: "update"},
				{ResourceType: "docker_network", ResourceName: "backend", Action: "create"},
				{ResourceType: "docker_volume", ResourceName: "data", Action: "delete"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes := parsePlanChanges(tt.output)
			assert.Equal(t, tt.expected, changes)
		})
	}
}

func TestCreateStackConfig(t *testing.T) {
	config := CreateStackConfig("stacks/web", "Web Stack", "Web application stack")

	assert.Contains(t, config, `name        = "Web Stack"`)
	assert.Contains(t, config, `description = "Web application stack"`)
	assert.Contains(t, config, `id          = "stacks/web"`)
	assert.Contains(t, config, "globals {")
}

func TestCreateRootConfig(t *testing.T) {
	config := CreateRootConfig("my-homelab")

	assert.Contains(t, config, `project_name = "my-homelab"`)
	assert.Contains(t, config, "terramate {")
	assert.Contains(t, config, "git {")
	assert.Contains(t, config, "run {")
}

func TestDriftResult(t *testing.T) {
	result := &DriftResult{
		HasDrift:  true,
		CheckedAt: time.Now(),
		Duration:  5 * time.Second,
		Stacks: []StackDrift{
			{
				Path:     "stacks/web",
				Name:     "web",
				HasDrift: true,
				Changes: []Change{
					{ResourceType: "docker_container", ResourceName: "nginx", Action: "update"},
				},
			},
			{
				Path:     "stacks/db",
				Name:     "db",
				HasDrift: false,
			},
		},
	}

	assert.True(t, result.HasDrift)
	assert.Len(t, result.Stacks, 2)
	assert.True(t, result.Stacks[0].HasDrift)
	assert.False(t, result.Stacks[1].HasDrift)
}

func TestResult(t *testing.T) {
	result := &Result{
		Success:  true,
		ExitCode: 0,
		Stdout:   "success output",
		Stderr:   "",
		Duration: 1 * time.Second,
	}

	assert.True(t, result.Success)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "success output", result.Stdout)
}

func TestStack(t *testing.T) {
	stack := Stack{
		ID:          "stacks/web",
		Name:        "Web",
		Description: "Web application",
		Path:        "stacks/web",
		Tags:        []string{"web", "frontend"},
		Metadata:    map[string]string{"team": "platform"},
	}

	assert.Equal(t, "stacks/web", stack.ID)
	assert.Contains(t, stack.Tags, "web")
	assert.Equal(t, "platform", stack.Metadata["team"])
}

func TestExecutorWithMockWorkDir(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "terramate-test-*")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	e := NewExecutor(
		WithWorkDir(tmpDir),
		WithTimeout(5*time.Second),
	)

	assert.Equal(t, tmpDir, e.workDir)
	assert.Equal(t, 5*time.Second, e.timeout)
}

func TestVersionWithNonExistentBinary(t *testing.T) {
	e := NewExecutor(
		WithBinary("nonexistent-terramate-binary"),
		WithTimeout(5*time.Second),
	)

	ctx := context.Background()
	_, err := e.Version(ctx)
	assert.Error(t, err)
}

func TestListWithNonExistentBinary(t *testing.T) {
	e := NewExecutor(
		WithBinary("nonexistent-terramate-binary"),
		WithTimeout(5*time.Second),
	)

	ctx := context.Background()
	_, err := e.List(ctx)
	assert.Error(t, err)
}

func TestChange(t *testing.T) {
	change := Change{
		ResourceType: "docker_container",
		ResourceName: "nginx",
		Action:       "update",
		Details:      "env changed",
	}

	assert.Equal(t, "docker_container", change.ResourceType)
	assert.Equal(t, "nginx", change.ResourceName)
	assert.Equal(t, "update", change.Action)
	assert.Equal(t, "env changed", change.Details)
}
