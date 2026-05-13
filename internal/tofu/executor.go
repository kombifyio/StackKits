// Package tofu provides OpenTofu execution capabilities.
package tofu

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Executor handles OpenTofu command execution
type Executor struct {
	workDir     string
	binary      string
	timeout     time.Duration
	autoApprove bool
}

// ExecutorOption configures the Executor
type ExecutorOption func(*Executor)

// WithWorkDir sets the working directory
func WithWorkDir(dir string) ExecutorOption {
	return func(e *Executor) {
		e.workDir = dir
	}
}

// WithBinary sets the tofu binary path
func WithBinary(binary string) ExecutorOption {
	return func(e *Executor) {
		e.binary = binary
	}
}

// WithTimeout sets the execution timeout
func WithTimeout(timeout time.Duration) ExecutorOption {
	return func(e *Executor) {
		e.timeout = timeout
	}
}

// WithAutoApprove enables auto-approve for apply/destroy
func WithAutoApprove(autoApprove bool) ExecutorOption {
	return func(e *Executor) {
		e.autoApprove = autoApprove
	}
}

// SetAutoApprove sets the auto-approve flag dynamically
func (e *Executor) SetAutoApprove(autoApprove bool) {
	e.autoApprove = autoApprove
}

// NewExecutor creates a new OpenTofu executor
func NewExecutor(opts ...ExecutorOption) *Executor {
	e := &Executor{
		workDir: ".",
		binary:  DefaultBinary(),
		timeout: 30 * time.Minute,
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

// BinaryName returns the OpenTofu executable name for the current platform.
func BinaryName() string {
	if runtime.GOOS == "windows" {
		return "tofu.exe"
	}
	return "tofu"
}

// PackagedBinaryPath returns the StackKit-packaged OpenTofu binary path.
// It intentionally does not fall back to PATH: product and release tests must
// prove that OpenTofu ships with StackKit, not that the host happens to have it.
func PackagedBinaryPath() (string, bool) {
	if override := os.Getenv("STACKKIT_TOFU_BINARY"); override != "" {
		return override, fileExists(override)
	}

	exePath, err := os.Executable()
	if err != nil || exePath == "" {
		return "", false
	}

	exeDir := filepath.Dir(exePath)
	for _, candidate := range []string{
		filepath.Join(exeDir, BinaryName()),
		filepath.Join(exeDir, "bin", BinaryName()),
	} {
		if fileExists(candidate) {
			return candidate, true
		}
	}

	return "", false
}

// DefaultBinary resolves the OpenTofu binary used by regular execution.
// It intentionally avoids PATH fallback so a host-installed OpenTofu cannot
// make StackKit appear release-ready when the package is missing its own copy.
func DefaultBinary() string {
	if path, ok := PackagedBinaryPath(); ok {
		return path
	}
	return filepath.Join(".stackkit-missing-packaged-opentofu", BinaryName())
}

func fileExists(path string) bool {
	_, err := exec.LookPath(path)
	return err == nil
}

// Result represents the result of a tofu command
type Result struct {
	Success  bool
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
}

// Init runs tofu init
func (e *Executor) Init(ctx context.Context) (*Result, error) {
	return e.run(ctx, "init", "-input=false")
}

// Plan runs tofu plan
func (e *Executor) Plan(ctx context.Context, outFile string, destroy bool) (*Result, error) {
	args := []string{"plan", "-input=false", "-detailed-exitcode"}
	if destroy {
		args = append(args, "-destroy")
	}
	if outFile != "" {
		args = append(args, fmt.Sprintf("-out=%s", outFile))
	}
	return e.run(ctx, args...)
}

// Apply runs tofu apply
func (e *Executor) Apply(ctx context.Context, planFile string) (*Result, error) {
	args := []string{"apply", "-input=false"}
	if e.autoApprove {
		args = append(args, "-auto-approve")
	}
	if planFile != "" {
		args = append(args, planFile)
	}
	return e.run(ctx, args...)
}

// Destroy runs tofu destroy
func (e *Executor) Destroy(ctx context.Context) (*Result, error) {
	args := []string{"destroy", "-input=false"}
	if e.autoApprove {
		args = append(args, "-auto-approve")
	}
	return e.run(ctx, args...)
}

// Output runs tofu output and returns the outputs
func (e *Executor) Output(ctx context.Context) (*Result, error) {
	return e.run(ctx, "output", "-json")
}

// Show runs tofu show on a plan file
func (e *Executor) Show(ctx context.Context, planFile string) (*Result, error) {
	return e.run(ctx, "show", "-json", planFile)
}

// State returns the current state
func (e *Executor) State(ctx context.Context) (*Result, error) {
	return e.run(ctx, "state", "list")
}

// Validate runs tofu validate
func (e *Executor) Validate(ctx context.Context) (*Result, error) {
	return e.run(ctx, "validate", "-json")
}

// Version returns the tofu version
func (e *Executor) Version(ctx context.Context) (string, error) {
	result, err := e.run(ctx, "version")
	if err != nil {
		return "", err
	}
	if !result.Success {
		return "", fmt.Errorf("failed to get version: %s", result.Stderr)
	}

	// Parse version from output (e.g., "OpenTofu v1.6.0")
	lines := strings.Split(result.Stdout, "\n")
	if len(lines) > 0 {
		parts := strings.Fields(lines[0])
		if len(parts) >= 2 {
			return strings.TrimPrefix(parts[1], "v"), nil
		}
	}
	return "", fmt.Errorf("unable to parse version from: %s", result.Stdout)
}

// IsInstalled checks if tofu is installed
func (e *Executor) IsInstalled() bool {
	_, err := exec.LookPath(e.binary)
	return err == nil
}

// run executes a tofu command
func (e *Executor) run(ctx context.Context, args ...string) (*Result, error) {
	start := time.Now()

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, e.binary, args...) // #nosec G204 -- binary path is set at construction, not from user input
	cmd.Dir = e.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Set environment - preserve existing env vars including DOCKER_HOST
	env := os.Environ()
	env = append(env, "TF_IN_AUTOMATION=1")
	env = append(env, "TF_INPUT=0")
	cmd.Env = env

	err := cmd.Run()
	duration := time.Since(start)

	result := &Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}

	// Check for context timeout (TD-010: improved timeout handling)
	if ctx.Err() == context.DeadlineExceeded {
		result.Success = false
		result.ExitCode = -1
		return result, &TimeoutError{
			Command:  strings.Join(append([]string{e.binary}, args...), " "),
			Duration: e.timeout,
		}
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			// Exit code 2 for plan means changes detected (not an error)
			if len(args) > 0 && args[0] == "plan" && result.ExitCode == 2 {
				result.Success = true
				return result, nil
			}
		}
		result.Success = false
		return result, nil
	}

	result.Success = true
	result.ExitCode = 0
	return result, nil
}

// PlanChanges represents changes detected by plan
type PlanChanges struct {
	Add     int
	Change  int
	Destroy int
}

// ParsePlanOutput parses plan output to extract changes
func ParsePlanOutput(output string) *PlanChanges {
	changes := &PlanChanges{}

	// Look for "Plan: X to add, Y to change, Z to destroy"
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Plan:") {
			// Parse the numbers
			parts := strings.Fields(line)
			for i, part := range parts {
				if part == "to" && i > 0 && i+1 < len(parts) {
					num := 0
					_, _ = fmt.Sscanf(parts[i-1], "%d", &num)
					switch parts[i+1] {
					case "add,", "add.":
						changes.Add = num
					case "change,", "change.":
						changes.Change = num
					case "destroy.", "destroy":
						changes.Destroy = num
					}
				}
			}
		}
	}

	return changes
}

// GetWorkDir returns the working directory
func (e *Executor) GetWorkDir() string {
	return e.workDir
}

// SetWorkDir sets the working directory
func (e *Executor) SetWorkDir(dir string) {
	e.workDir = dir
}

// Refresh runs tofu refresh to sync state with real infrastructure
func (e *Executor) Refresh(ctx context.Context) (*Result, error) {
	return e.run(ctx, "refresh", "-input=false")
}

// Format runs tofu fmt to format configuration files
func (e *Executor) Format(ctx context.Context) (*Result, error) {
	return e.run(ctx, "fmt", "-recursive")
}

// Import imports an existing resource into state
func (e *Executor) Import(ctx context.Context, address, id string) (*Result, error) {
	if address == "" || id == "" {
		return nil, fmt.Errorf("address and id are required for import")
	}
	return e.run(ctx, "import", address, id)
}

// Taint marks a resource for recreation
func (e *Executor) Taint(ctx context.Context, address string) (*Result, error) {
	if address == "" {
		return nil, fmt.Errorf("resource address is required")
	}
	return e.run(ctx, "taint", address)
}

// Untaint removes the taint from a resource
func (e *Executor) Untaint(ctx context.Context, address string) (*Result, error) {
	if address == "" {
		return nil, fmt.Errorf("resource address is required")
	}
	return e.run(ctx, "untaint", address)
}

// Graph generates a visual graph of resources
func (e *Executor) Graph(ctx context.Context) (*Result, error) {
	return e.run(ctx, "graph")
}

// Providers shows required providers
func (e *Executor) Providers(ctx context.Context) (*Result, error) {
	return e.run(ctx, "providers")
}

// TimeoutError represents a command timeout
type TimeoutError struct {
	Command  string
	Duration time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("command '%s' timed out after %v", e.Command, e.Duration)
}

// IsTimeoutError checks if an error is a timeout error
func IsTimeoutError(err error) bool {
	_, ok := err.(*TimeoutError)
	return ok
}

// EnsureStateDir ensures the state directory exists
func EnsureStateDir(baseDir string) error {
	stateDir := filepath.Join(baseDir, ".stackkit")
	return os.MkdirAll(stateDir, 0750)
}

// ValidateWorkDir validates the working directory
func ValidateWorkDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("working directory cannot be empty")
	}

	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return fmt.Errorf("working directory does not exist: %s", dir)
	}
	if err != nil {
		return fmt.Errorf("failed to stat working directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", dir)
	}

	return nil
}

// HasTerraformFiles checks if directory contains .tf files
func HasTerraformFiles(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".tf") {
			return true, nil
		}
	}

	return false, nil
}
