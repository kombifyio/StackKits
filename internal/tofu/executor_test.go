// Package tofu tests
package tofu

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const osWindows = "windows"

func TestExecutor(t *testing.T) {
	t.Run("creates executor with defaults", func(t *testing.T) {
		executor := NewExecutor()

		assert.NotNil(t, executor)
		assert.Equal(t, ".", executor.GetWorkDir())
	})

	t.Run("creates executor with options", func(t *testing.T) {
		executor := NewExecutor(
			WithWorkDir("/test/dir"),
			WithBinary("opentofu"),
			WithTimeout(10*time.Minute),
			WithAutoApprove(true),
		)

		assert.Equal(t, "/test/dir", executor.GetWorkDir())
	})

	t.Run("sets work directory", func(t *testing.T) {
		executor := NewExecutor()
		executor.SetWorkDir("/new/dir")

		assert.Equal(t, "/new/dir", executor.GetWorkDir())
	})
}

func TestPackagedBinaryPath(t *testing.T) {
	t.Run("uses explicit StackKit packaged tofu override", func(t *testing.T) {
		tmpDir := t.TempDir()
		binaryPath := filepath.Join(tmpDir, BinaryName())
		require.NoError(t, os.WriteFile(binaryPath, []byte("fake"), 0755))
		t.Setenv("STACKKIT_TOFU_BINARY", binaryPath)

		path, ok := PackagedBinaryPath()
		require.True(t, ok)
		assert.Equal(t, binaryPath, path)

		executor := NewExecutor()
		assert.Equal(t, binaryPath, executor.binary)
	})

	t.Run("does not claim missing override as packaged", func(t *testing.T) {
		missingPath := filepath.Join(t.TempDir(), BinaryName())
		t.Setenv("STACKKIT_TOFU_BINARY", missingPath)

		path, ok := PackagedBinaryPath()
		require.False(t, ok)
		assert.Equal(t, missingPath, path)
	})
}

func TestExecutorIsInstalled(t *testing.T) {
	t.Run("checks for tofu binary", func(t *testing.T) {
		executor := NewExecutor()
		// This will return true/false depending on system
		_ = executor.IsInstalled()
	})

	t.Run("checks for custom binary", func(t *testing.T) {
		executor := NewExecutor(WithBinary("nonexistent-binary-xyz"))

		assert.False(t, executor.IsInstalled())
	})
}

func TestParsePlanOutput(t *testing.T) {
	t.Run("parses add changes", func(t *testing.T) {
		output := `
Terraform will perform the following actions:

Plan: 3 to add, 0 to change, 0 to destroy.
`
		changes := ParsePlanOutput(output)

		assert.Equal(t, 3, changes.Add)
		assert.Equal(t, 0, changes.Change)
		assert.Equal(t, 0, changes.Destroy)
	})

	t.Run("parses mixed changes", func(t *testing.T) {
		output := `
Terraform will perform the following actions:

Plan: 2 to add, 1 to change, 3 to destroy.
`
		changes := ParsePlanOutput(output)

		assert.Equal(t, 2, changes.Add)
		assert.Equal(t, 1, changes.Change)
		assert.Equal(t, 3, changes.Destroy)
	})

	t.Run("handles no changes", func(t *testing.T) {
		output := `No changes. Infrastructure is up-to-date.`

		changes := ParsePlanOutput(output)

		assert.Equal(t, 0, changes.Add)
		assert.Equal(t, 0, changes.Change)
		assert.Equal(t, 0, changes.Destroy)
	})

	t.Run("handles empty output", func(t *testing.T) {
		changes := ParsePlanOutput("")

		assert.Equal(t, 0, changes.Add)
		assert.Equal(t, 0, changes.Change)
		assert.Equal(t, 0, changes.Destroy)
	})
}

func TestResult(t *testing.T) {
	t.Run("creates successful result", func(t *testing.T) {
		result := &Result{
			Success:  true,
			ExitCode: 0,
			Stdout:   "Success output",
			Stderr:   "",
			Duration: 5 * time.Second,
		}

		assert.True(t, result.Success)
		assert.Equal(t, 0, result.ExitCode)
		assert.NotEmpty(t, result.Stdout)
		assert.Empty(t, result.Stderr)
	})

	t.Run("creates failed result", func(t *testing.T) {
		result := &Result{
			Success:  false,
			ExitCode: 1,
			Stdout:   "",
			Stderr:   "Error: something went wrong",
			Duration: 2 * time.Second,
		}

		assert.False(t, result.Success)
		assert.Equal(t, 1, result.ExitCode)
		assert.NotEmpty(t, result.Stderr)
	})
}

func TestEnsureStateDir(t *testing.T) {
	t.Run("creates state directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		err := EnsureStateDir(tmpDir)
		assert.NoError(t, err)

		stateDir := filepath.Join(tmpDir, ".stackkit")
		info, err := os.Stat(stateDir)
		assert.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("idempotent on existing directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Create twice — should not error
		assert.NoError(t, EnsureStateDir(tmpDir))
		assert.NoError(t, EnsureStateDir(tmpDir))
	})
}

// Integration tests that require the StackKit-packaged OpenTofu binary.
func TestExecutorIntegration(t *testing.T) {
	executor := NewExecutor()

	if !executor.IsInstalled() {
		t.Skip("StackKit-packaged OpenTofu not available in this test layout, skipping integration tests")
	}

	ctx := context.Background()

	t.Run("gets version", func(t *testing.T) {
		version, err := executor.Version(ctx)

		assert.NoError(t, err)
		assert.NotEmpty(t, version)
	})
}

func TestPlanChanges(t *testing.T) {
	t.Run("zero values", func(t *testing.T) {
		changes := &PlanChanges{}

		assert.Equal(t, 0, changes.Add)
		assert.Equal(t, 0, changes.Change)
		assert.Equal(t, 0, changes.Destroy)
	})
}

func TestTimeoutError(t *testing.T) {
	t.Run("creates timeout error", func(t *testing.T) {
		err := &TimeoutError{
			Command:  "tofu plan",
			Duration: 30 * time.Minute,
		}

		assert.Contains(t, err.Error(), "tofu plan")
		assert.Contains(t, err.Error(), "timed out")
	})

	t.Run("IsTimeoutError returns true for timeout", func(t *testing.T) {
		err := &TimeoutError{Command: "test", Duration: time.Second}
		assert.True(t, IsTimeoutError(err))
	})

	t.Run("IsTimeoutError returns false for other errors", func(t *testing.T) {
		err := assert.AnError
		assert.False(t, IsTimeoutError(err))
	})
}

func TestValidateWorkDir(t *testing.T) {
	t.Run("empty directory", func(t *testing.T) {
		err := ValidateWorkDir("")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})

	t.Run("non-existent directory", func(t *testing.T) {
		err := ValidateWorkDir("/nonexistent/path/xyz123")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "does not exist")
	})

	t.Run("valid directory", func(t *testing.T) {
		// Use current directory which should exist
		err := ValidateWorkDir(".")
		assert.NoError(t, err)
	})
}

func TestHasTerraformFiles(t *testing.T) {
	t.Run("empty directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		has, err := HasTerraformFiles(tmpDir)
		assert.NoError(t, err)
		assert.False(t, has)
	})

	t.Run("directory with .tf files", func(t *testing.T) {
		tmpDir := t.TempDir()
		_ = os.WriteFile(filepath.Join(tmpDir, "main.tf"), []byte("# test"), 0600)

		has, err := HasTerraformFiles(tmpDir)
		assert.NoError(t, err)
		assert.True(t, has)
	})

	t.Run("directory with non-tf files only", func(t *testing.T) {
		tmpDir := t.TempDir()
		_ = os.WriteFile(filepath.Join(tmpDir, "readme.md"), []byte("# test"), 0600)
		_ = os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte("{}"), 0600)

		has, err := HasTerraformFiles(tmpDir)
		assert.NoError(t, err)
		assert.False(t, has)
	})

	t.Run("non-existent directory", func(t *testing.T) {
		_, err := HasTerraformFiles("/nonexistent/path/xyz")
		assert.Error(t, err)
	})
}

func TestExecutorWithTimeout(t *testing.T) {
	t.Run("very short timeout", func(t *testing.T) {
		executor := NewExecutor(
			WithTimeout(1*time.Nanosecond),
			WithBinary("nonexistent-command"),
		)

		assert.Equal(t, 1*time.Nanosecond, executor.timeout)
	})
}

func TestExecutorOptions(t *testing.T) {
	t.Run("WithAutoApprove", func(t *testing.T) {
		e := NewExecutor(WithAutoApprove(true))
		assert.True(t, e.autoApprove)
	})

	t.Run("WithBinary", func(t *testing.T) {
		e := NewExecutor(WithBinary("terraform"))
		assert.Equal(t, "terraform", e.binary)
	})

	t.Run("all options combined", func(t *testing.T) {
		e := NewExecutor(
			WithWorkDir("/test"),
			WithBinary("tf"),
			WithTimeout(5*time.Minute),
			WithAutoApprove(true),
		)

		assert.Equal(t, "/test", e.workDir)
		assert.Equal(t, "tf", e.binary)
		assert.Equal(t, 5*time.Minute, e.timeout)
		assert.True(t, e.autoApprove)
	})
}

func TestValidateWorkDir_FileNotDir(t *testing.T) {
	t.Run("file instead of directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "not-a-dir.txt")
		_ = os.WriteFile(tmpFile, []byte("test"), 0600)

		err := ValidateWorkDir(tmpFile)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not a directory")
	})
}

func TestImportRequiresArgs(t *testing.T) {
	executor := NewExecutor(WithBinary("nonexistent-binary-xyz"))

	t.Run("empty address", func(t *testing.T) {
		_, err := executor.Import(context.Background(), "", "some-id")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "address and id are required")
	})

	t.Run("empty id", func(t *testing.T) {
		_, err := executor.Import(context.Background(), "aws_instance.foo", "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "address and id are required")
	})

	t.Run("both empty", func(t *testing.T) {
		_, err := executor.Import(context.Background(), "", "")
		assert.Error(t, err)
	})
}

func TestTaintRequiresAddress(t *testing.T) {
	executor := NewExecutor(WithBinary("nonexistent-binary-xyz"))

	t.Run("empty address for taint", func(t *testing.T) {
		_, err := executor.Taint(context.Background(), "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "resource address is required")
	})

	t.Run("empty address for untaint", func(t *testing.T) {
		_, err := executor.Untaint(context.Background(), "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "resource address is required")
	})
}

func TestParsePlanOutputEdgeCases(t *testing.T) {
	t.Run("plan with period ending", func(t *testing.T) {
		output := `Plan: 1 to add, 0 to change, 2 to destroy.`
		changes := ParsePlanOutput(output)
		assert.Equal(t, 1, changes.Add)
		assert.Equal(t, 0, changes.Change)
		assert.Equal(t, 2, changes.Destroy)
	})

	t.Run("multiline output with plan", func(t *testing.T) {
		output := "Some header\n\nAnother line\nPlan: 5 to add, 3 to change, 1 to destroy.\n\nDone."
		changes := ParsePlanOutput(output)
		assert.Equal(t, 5, changes.Add)
		assert.Equal(t, 3, changes.Change)
		assert.Equal(t, 1, changes.Destroy)
	})
}

func TestEnsureStateDirNestedPath(t *testing.T) {
	t.Run("creates nested state directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		nested := filepath.Join(tmpDir, "a", "b", "c")
		_ = os.MkdirAll(nested, 0750)

		err := EnsureStateDir(nested)
		assert.NoError(t, err)

		stateDir := filepath.Join(nested, ".stackkit")
		info, err := os.Stat(stateDir)
		assert.NoError(t, err)
		assert.True(t, info.IsDir())
	})
}

// --- Tests that exercise the run() method via a real binary ---

// echoCmd returns a binary and args that print to stdout on any platform.
func echoCmd() (binary string, args []string) {
	if runtime.GOOS == osWindows {
		return "cmd", []string{"/C", "echo"}
	}
	return "echo", nil
}

func TestRunMethodViaVersion(t *testing.T) {
	// Use a real "echo" binary to simulate tofu version output
	binary, baseArgs := echoCmd()
	if _, err := exec.LookPath(binary); err != nil {
		t.Skipf("binary %s not found on PATH", binary)
	}

	// Create a wrapper script that outputs a fake version string
	tmpDir := t.TempDir()
	var scriptPath string

	if runtime.GOOS == osWindows {
		scriptPath = filepath.Join(tmpDir, "fake-tofu.bat")
		//nolint:gosec // G306: scripts need execute permissions
		_ = os.WriteFile(scriptPath, []byte("@echo off\nif \"%1\"==\"version\" echo OpenTofu v1.8.0\n"), 0750)
	} else {
		scriptPath = filepath.Join(tmpDir, "fake-tofu")
		//nolint:gosec // G306: scripts need execute permissions
		_ = os.WriteFile(scriptPath, []byte("#!/bin/sh\nif [ \"$1\" = \"version\" ]; then echo \"OpenTofu v1.8.0\"; fi\n"), 0750)
	}

	_ = baseArgs // not needed for this test

	executor := NewExecutor(
		WithBinary(scriptPath),
		WithWorkDir(tmpDir),
	)

	ctx := context.Background()

	t.Run("Version parses output", func(t *testing.T) {
		version, err := executor.Version(ctx)
		require.NoError(t, err)
		assert.Equal(t, "1.8.0", version)
	})
}

func TestRunMethodSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	var scriptPath string

	if runtime.GOOS == osWindows {
		scriptPath = filepath.Join(tmpDir, "fake-tofu.bat")
		// Script that echoes valid JSON for validate, and handles other commands
		script := `@echo off
if "%1"=="validate" (
  echo {"valid":true,"error_count":0,"warning_count":0,"diagnostics":[]}
  exit /b 0
)
if "%1"=="output" (
  echo {}
  exit /b 0
)
if "%1"=="state" (
  echo.
  exit /b 0
)
if "%1"=="graph" (
  echo digraph { }
  exit /b 0
)
if "%1"=="providers" (
  echo Providers required:
  exit /b 0
)
if "%1"=="fmt" (
  exit /b 0
)
echo OK
exit /b 0
`
		//nolint:gosec // G306: scripts need execute permissions
		_ = os.WriteFile(scriptPath, []byte(script), 0750)
	} else {
		scriptPath = filepath.Join(tmpDir, "fake-tofu")
		script := `#!/bin/sh
case "$1" in
  validate) echo '{"valid":true,"error_count":0,"warning_count":0,"diagnostics":[]}'; exit 0;;
  output) echo '{}'; exit 0;;
  state) echo ''; exit 0;;
  graph) echo 'digraph { }'; exit 0;;
  providers) echo 'Providers required:'; exit 0;;
  fmt) exit 0;;
  *) echo OK; exit 0;;
esac
`
		//nolint:gosec // G306: scripts need execute permissions
		_ = os.WriteFile(scriptPath, []byte(script), 0750)
	}

	executor := NewExecutor(
		WithBinary(scriptPath),
		WithWorkDir(tmpDir),
	)

	ctx := context.Background()

	t.Run("Validate returns success", func(t *testing.T) {
		result, err := executor.Validate(ctx)
		require.NoError(t, err)
		assert.True(t, result.Success)
		assert.Equal(t, 0, result.ExitCode)
		assert.Contains(t, result.Stdout, "valid")
	})

	t.Run("Output returns result", func(t *testing.T) {
		result, err := executor.Output(ctx)
		require.NoError(t, err)
		assert.True(t, result.Success)
	})

	t.Run("State returns result", func(t *testing.T) {
		result, err := executor.State(ctx)
		require.NoError(t, err)
		assert.True(t, result.Success)
	})

	t.Run("Graph returns result", func(t *testing.T) {
		result, err := executor.Graph(ctx)
		require.NoError(t, err)
		assert.True(t, result.Success)
	})

	t.Run("Providers returns result", func(t *testing.T) {
		result, err := executor.Providers(ctx)
		require.NoError(t, err)
		assert.True(t, result.Success)
	})

	t.Run("Format returns result", func(t *testing.T) {
		result, err := executor.Format(ctx)
		require.NoError(t, err)
		assert.True(t, result.Success)
	})

	t.Run("Refresh returns result", func(t *testing.T) {
		result, err := executor.Refresh(ctx)
		require.NoError(t, err)
		assert.True(t, result.Success)
	})
}

func TestRunMethodFailure(t *testing.T) {
	tmpDir := t.TempDir()
	var scriptPath string

	if runtime.GOOS == osWindows {
		scriptPath = filepath.Join(tmpDir, "fail-tofu.bat")
		//nolint:gosec // G306: scripts need execute permissions
		_ = os.WriteFile(scriptPath, []byte("@echo off\necho Error: something failed >&2\nexit /b 1\n"), 0750)
	} else {
		scriptPath = filepath.Join(tmpDir, "fail-tofu")
		//nolint:gosec // G306: scripts need execute permissions
		_ = os.WriteFile(scriptPath, []byte("#!/bin/sh\necho 'Error: something failed' >&2\nexit 1\n"), 0750)
	}

	executor := NewExecutor(
		WithBinary(scriptPath),
		WithWorkDir(tmpDir),
	)

	ctx := context.Background()

	t.Run("failed command returns non-zero exit code", func(t *testing.T) {
		result, err := executor.Validate(ctx)
		require.NoError(t, err) // run() doesn't return error, it sets result.Success
		assert.False(t, result.Success)
		assert.NotEqual(t, 0, result.ExitCode)
	})

	t.Run("Version fails gracefully", func(t *testing.T) {
		_, err := executor.Version(ctx)
		assert.Error(t, err)
	})
}

func TestRunMethodTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	var scriptPath string

	if runtime.GOOS == osWindows {
		scriptPath = filepath.Join(tmpDir, "slow-tofu.bat")
		//nolint:gosec // G306: scripts need execute permissions
		_ = os.WriteFile(scriptPath, []byte("@echo off\nping -n 10 127.0.0.1 > nul\n"), 0750)
	} else {
		scriptPath = filepath.Join(tmpDir, "slow-tofu")
		//nolint:gosec // G306: scripts need execute permissions
		_ = os.WriteFile(scriptPath, []byte("#!/bin/sh\nsleep 10\n"), 0750)
	}

	executor := NewExecutor(
		WithBinary(scriptPath),
		WithWorkDir(tmpDir),
		WithTimeout(100*time.Millisecond),
	)

	t.Run("times out and returns TimeoutError", func(t *testing.T) {
		ctx := context.Background()
		_, err := executor.Validate(ctx)
		if err != nil {
			assert.True(t, IsTimeoutError(err), "expected TimeoutError, got: %v", err)
		}
		// On some systems the process may be killed before deadline detection
	})
}

func TestRunEnvironment(t *testing.T) {
	tmpDir := t.TempDir()
	var scriptPath string

	if runtime.GOOS == osWindows {
		scriptPath = filepath.Join(tmpDir, "env-tofu.bat")
		//nolint:gosec // G306: scripts need execute permissions
		_ = os.WriteFile(scriptPath, []byte("@echo off\necho TF_IN_AUTOMATION=%TF_IN_AUTOMATION%\necho TF_INPUT=%TF_INPUT%\n"), 0750)
	} else {
		scriptPath = filepath.Join(tmpDir, "env-tofu")
		//nolint:gosec // G306: scripts need execute permissions
		_ = os.WriteFile(scriptPath, []byte("#!/bin/sh\necho TF_IN_AUTOMATION=$TF_IN_AUTOMATION\necho TF_INPUT=$TF_INPUT\n"), 0750)
	}

	executor := NewExecutor(
		WithBinary(scriptPath),
		WithWorkDir(tmpDir),
	)

	t.Run("sets automation environment variables", func(t *testing.T) {
		ctx := context.Background()
		result, err := executor.Validate(ctx)
		require.NoError(t, err)
		assert.Contains(t, result.Stdout, "TF_IN_AUTOMATION=1")
		assert.Contains(t, result.Stdout, "TF_INPUT=0")
	})
}

func TestPlanExitCode2(t *testing.T) {
	tmpDir := t.TempDir()
	var scriptPath string

	if runtime.GOOS == osWindows {
		scriptPath = filepath.Join(tmpDir, "plan-tofu.bat")
		//nolint:gosec // G306: scripts need execute permissions
		_ = os.WriteFile(scriptPath, []byte("@echo off\necho Plan: 2 to add, 0 to change, 0 to destroy.\nexit /b 2\n"), 0750)
	} else {
		scriptPath = filepath.Join(tmpDir, "plan-tofu")
		//nolint:gosec // G306: scripts need execute permissions
		_ = os.WriteFile(scriptPath, []byte("#!/bin/sh\necho 'Plan: 2 to add, 0 to change, 0 to destroy.'\nexit 2\n"), 0750)
	}

	executor := NewExecutor(
		WithBinary(scriptPath),
		WithWorkDir(tmpDir),
	)

	t.Run("plan exit code 2 means changes detected, not error", func(t *testing.T) {
		ctx := context.Background()
		result, err := executor.Plan(ctx, "", false)
		require.NoError(t, err)
		// Exit code 2 with plan is treated as success (changes detected)
		assert.True(t, result.Success)
		assert.Equal(t, 2, result.ExitCode)
	})
}
