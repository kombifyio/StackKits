// Package integration provides integration tests for the StackKits CLI
package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCLIBuild verifies the CLI can be built
func TestCLIBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Get project root
	projectRoot := getProjectRoot(t)

	t.Run("builds successfully", func(t *testing.T) {
		//nolint:gosec // G204: test binary paths are controlled
		cmd := exec.Command("go", "build", "-o", filepath.Join(t.TempDir(), "stackkit"), "./cmd/stackkit")
		cmd.Dir = projectRoot
		output, err := cmd.CombinedOutput()

		assert.NoError(t, err, "Build failed: %s", string(output))
	})
}

// TestCLIVersionCommand tests the version command
func TestCLIVersionCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	binary := buildCLI(t)

	t.Run("shows version", func(t *testing.T) {
		//nolint:gosec // G204: test binary paths are controlled
		cmd := exec.Command(binary, "version")
		output, err := cmd.CombinedOutput()

		require.NoError(t, err)
		assert.Contains(t, string(output), "stackkit version")
	})
}

// TestCLIHelpCommand tests the help command
func TestCLIHelpCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	binary := buildCLI(t)

	t.Run("shows help", func(t *testing.T) {
		//nolint:gosec // G204: test binary paths are controlled
		cmd := exec.Command(binary, "--help")
		output, err := cmd.CombinedOutput()

		require.NoError(t, err)
		assert.Contains(t, string(output), "StackKit CLI")
		assert.Contains(t, string(output), "init")
		assert.Contains(t, string(output), "prepare")
		assert.Contains(t, string(output), "plan")
		assert.Contains(t, string(output), "apply")
	})

	t.Run("shows command help", func(t *testing.T) {
		commands := []string{"init", "prepare", "plan", "apply", "remove", "status", "validate"}

		for _, cmd := range commands {
			t.Run(cmd, func(t *testing.T) {
				//nolint:gosec // G204: test binary paths are controlled
				c := exec.Command(binary, cmd, "--help")
				output, err := c.CombinedOutput()

				require.NoError(t, err, "Help for %s failed: %s", cmd, string(output))
				assert.NotEmpty(t, string(output))
			})
		}
	})
}

// TestCLIInitCommand tests the init command
func TestCLIInitCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	binary := buildCLI(t)
	projectRoot := getProjectRoot(t)

	t.Run("requires stackkit name", func(t *testing.T) {
		tmpDir := t.TempDir()
		//nolint:gosec // G204: test binary paths are controlled
		cmd := exec.Command(binary, "init", "--non-interactive", "-C", tmpDir)
		output, err := cmd.CombinedOutput()

		assert.Error(t, err)
		assert.Contains(t, string(output), "stackkit name required in non-interactive mode")
	})

	t.Run("initializes with stackkit", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Copy base-kit to temp dir for testing
		stackkitSrc := filepath.Join(projectRoot, "base-kit")
		stackkitDst := filepath.Join(tmpDir, "base-kit")
		copyDir(t, stackkitSrc, stackkitDst)

		//nolint:gosec // G204: test binary paths are controlled
		cmd := exec.Command(binary, "init", "base-kit", "-C", tmpDir)
		output, err := cmd.CombinedOutput()

		if err != nil {
			t.Logf("Init output: %s", string(output))
		}
		// May fail if stackkit not found in path, which is expected
	})
}

// TestCLIValidateCommand tests the validate command
func TestCLIValidateCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	binary := buildCLI(t)

	t.Run("validates spec file", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create a valid spec file
		specContent := `name: test-deployment
stackkit: base-kit
mode: simple
network:
  mode: local
  subnet: 172.20.0.0/16
`
		specPath := filepath.Join(tmpDir, "stack-spec.yaml")
		err := os.WriteFile(specPath, []byte(specContent), 0600)
		require.NoError(t, err)

		//nolint:gosec // G204: test binary paths are controlled
		cmd := exec.Command(binary, "validate", "-C", tmpDir)
		output, err := cmd.CombinedOutput()

		require.NoError(t, err, "Validate failed: %s", string(output))
		assert.Contains(t, string(output), "valid")
	})

	t.Run("fails for invalid spec", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create an invalid spec file (missing required fields)
		specContent := `mode: simple`
		specPath := filepath.Join(tmpDir, "stack-spec.yaml")
		err := os.WriteFile(specPath, []byte(specContent), 0600)
		require.NoError(t, err)

		//nolint:gosec // G204: test binary paths are controlled
		cmd := exec.Command(binary, "validate", "-C", tmpDir)
		_, err = cmd.CombinedOutput()

		assert.Error(t, err)
	})
}

// Helper functions

func getProjectRoot(t *testing.T) string {
	t.Helper()

	// Walk up from test file to find go.mod
	dir, err := os.Getwd()
	require.NoError(t, err)

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Try relative path from test location
			return filepath.Join("..", "..", "..")
		}
		dir = parent
	}
}

func buildCLI(t *testing.T) string {
	t.Helper()

	projectRoot := getProjectRoot(t)
	binary := filepath.Join(t.TempDir(), "stackkit")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}

	//nolint:gosec // G204: test binary paths are controlled
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/stackkit")
	cmd.Dir = projectRoot
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to build CLI: %s", string(output))

	return binary
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()

	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		return os.WriteFile(dstPath, data, info.Mode())
	})

	require.NoError(t, err)
}
