// Package terramate provides Terramate execution capabilities for Day 2 operations.
// Terramate enables drift detection, change sets, and orchestrated deployments.
package terramate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Executor handles Terramate command execution
type Executor struct {
	workDir      string
	binary       string
	timeout      time.Duration
	changeDetect bool
	parallelism  int
	tofuBinary   string
}

// ExecutorOption configures the Executor
type ExecutorOption func(*Executor)

// WithWorkDir sets the working directory
func WithWorkDir(dir string) ExecutorOption {
	return func(e *Executor) {
		e.workDir = dir
	}
}

// WithBinary sets the terramate binary path
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

// WithChangeDetection enables Git-based change detection
func WithChangeDetection(enabled bool) ExecutorOption {
	return func(e *Executor) {
		e.changeDetect = enabled
	}
}

// WithParallelism sets the parallelism level
func WithParallelism(p int) ExecutorOption {
	return func(e *Executor) {
		e.parallelism = p
	}
}

// WithTofuBinary sets the OpenTofu binary for terramate run
func WithTofuBinary(binary string) ExecutorOption {
	return func(e *Executor) {
		e.tofuBinary = binary
	}
}

// NewExecutor creates a new Terramate executor
func NewExecutor(opts ...ExecutorOption) *Executor {
	e := &Executor{
		workDir:      ".",
		binary:       "terramate",
		timeout:      30 * time.Minute,
		changeDetect: true,
		parallelism:  1,
		tofuBinary:   "tofu",
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

// Result represents the result of a terramate command
type Result struct {
	Success  bool          `json:"success"`
	ExitCode int           `json:"exitCode"`
	Stdout   string        `json:"stdout"`
	Stderr   string        `json:"stderr"`
	Duration time.Duration `json:"duration"`
}

// DriftResult represents the result of drift detection
type DriftResult struct {
	HasDrift  bool          `json:"hasDrift"`
	Stacks    []StackDrift  `json:"stacks"`
	CheckedAt time.Time     `json:"checkedAt"`
	Duration  time.Duration `json:"duration"`
}

// StackDrift represents drift in a single stack
type StackDrift struct {
	Path     string   `json:"path"`
	Name     string   `json:"name"`
	HasDrift bool     `json:"hasDrift"`
	Changes  []Change `json:"changes,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// Change represents a detected change
type Change struct {
	ResourceType string `json:"resourceType"`
	ResourceName string `json:"resourceName"`
	Action       string `json:"action"` // "create", "update", "delete", "no-op"
	Details      string `json:"details,omitempty"`
}

// Stack represents a Terramate stack
type Stack struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Path        string            `json:"path"`
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// IsInstalled checks if Terramate is installed
func (e *Executor) IsInstalled() bool {
	_, err := exec.LookPath(e.binary)
	return err == nil
}

// Version returns the Terramate version
func (e *Executor) Version(ctx context.Context) (string, error) {
	result, err := e.run(ctx, "version")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

// Init initializes Terramate in the workspace
func (e *Executor) Init(ctx context.Context) (*Result, error) {
	return e.run(ctx, "create", "--all-terraform")
}

// List lists all stacks
func (e *Executor) List(ctx context.Context) ([]Stack, error) {
	result, err := e.run(ctx, "list", "--json")
	if err != nil {
		return nil, err
	}

	// Parse JSON output
	var stacks []Stack
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		var stack Stack
		if err := json.Unmarshal([]byte(line), &stack); err != nil {
			// Try simple path format
			stack = Stack{Path: line, Name: filepath.Base(line)}
		}
		stacks = append(stacks, stack)
	}

	return stacks, nil
}

// ListChanged lists stacks with changes (for change detection)
func (e *Executor) ListChanged(ctx context.Context) ([]Stack, error) {
	result, err := e.run(ctx, "list", "--changed")
	if err != nil {
		return nil, err
	}

	var stacks []Stack
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		stacks = append(stacks, Stack{Path: line, Name: filepath.Base(line)})
	}

	return stacks, nil
}

// RunInit runs tofu init on all stacks
func (e *Executor) RunInit(ctx context.Context) (*Result, error) {
	args := []string{"run"}
	if e.changeDetect {
		args = append(args, "--changed")
	}
	args = append(args, "--", e.tofuBinary, "init", "-input=false")
	return e.run(ctx, args...)
}

// RunPlan runs tofu plan on all stacks
func (e *Executor) RunPlan(ctx context.Context) (*Result, error) {
	args := []string{"run"}
	if e.changeDetect {
		args = append(args, "--changed")
	}
	args = append(args, "--", e.tofuBinary, "plan", "-input=false", "-detailed-exitcode")
	return e.run(ctx, args...)
}

// RunApply runs tofu apply on all stacks
func (e *Executor) RunApply(ctx context.Context, autoApprove bool) (*Result, error) {
	args := []string{"run"}
	if e.changeDetect {
		args = append(args, "--changed")
	}
	applyArgs := []string{e.tofuBinary, "apply", "-input=false"}
	if autoApprove {
		applyArgs = append(applyArgs, "-auto-approve")
	}
	args = append(args, "--")
	args = append(args, applyArgs...)
	return e.run(ctx, args...)
}

// RunDestroy runs tofu destroy on all stacks
func (e *Executor) RunDestroy(ctx context.Context, autoApprove bool) (*Result, error) {
	args := []string{"run", "--reverse"}
	destroyArgs := []string{e.tofuBinary, "destroy", "-input=false"}
	if autoApprove {
		destroyArgs = append(destroyArgs, "-auto-approve")
	}
	args = append(args, "--")
	args = append(args, destroyArgs...)
	return e.run(ctx, args...)
}

// DetectDrift runs drift detection across all stacks
func (e *Executor) DetectDrift(ctx context.Context) (*DriftResult, error) {
	startTime := time.Now()

	// Get all stacks
	stacks, err := e.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list stacks: %w", err)
	}

	result := &DriftResult{
		CheckedAt: startTime,
		Stacks:    make([]StackDrift, 0, len(stacks)),
	}

	// Check drift for each stack
	for _, stack := range stacks {
		stackDrift, err := e.checkStackDrift(ctx, stack)
		if err != nil {
			stackDrift = StackDrift{
				Path:     stack.Path,
				Name:     stack.Name,
				HasDrift: false,
				Error:    err.Error(),
			}
		}
		result.Stacks = append(result.Stacks, stackDrift)
		if stackDrift.HasDrift {
			result.HasDrift = true
		}
	}

	result.Duration = time.Since(startTime)
	return result, nil
}

// checkStackDrift checks drift for a single stack
func (e *Executor) checkStackDrift(ctx context.Context, stack Stack) (StackDrift, error) {
	stackDir := filepath.Join(e.workDir, stack.Path)

	// Run tofu plan in the stack directory
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, e.tofuBinary, "plan", "-input=false", "-detailed-exitcode", "-no-color") // #nosec G204 -- binary path is set at construction, not from user input
	cmd.Dir = stackDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	drift := StackDrift{
		Path: stack.Path,
		Name: stack.Name,
	}

	// Exit code 2 means changes detected (drift)
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() == 2 {
			drift.HasDrift = true
			drift.Changes = parsePlanChanges(stdout.String())
		}
	} else if err != nil {
		return drift, fmt.Errorf("plan failed: %w", err)
	}

	return drift, nil
}

// parsePlanChanges parses the plan output for changes
func parsePlanChanges(output string) []Change {
	var changes []Change

	// Match lines like: # docker_container.traefik will be updated in-place
	re := regexp.MustCompile(`#\s+(\S+)\.(\S+)\s+will be\s+(\S+)`)
	matches := re.FindAllStringSubmatch(output, -1)

	for _, match := range matches {
		if len(match) >= 4 {
			action := match[3]
			switch action {
			case "created":
				action = "create"
			case "updated":
				action = "update"
			case "destroyed":
				action = "delete"
			}
			changes = append(changes, Change{
				ResourceType: match[1],
				ResourceName: match[2],
				Action:       action,
			})
		}
	}

	return changes
}

// Refresh runs tofu refresh on all stacks
func (e *Executor) Refresh(ctx context.Context) (*Result, error) {
	args := []string{"run", "--", e.tofuBinary, "refresh", "-input=false"}
	return e.run(ctx, args...)
}

// Output gets outputs from all stacks
func (e *Executor) Output(ctx context.Context) (*Result, error) {
	args := []string{"run", "--", e.tofuBinary, "output", "-json"}
	return e.run(ctx, args...)
}

// Generate generates code from Terramate configurations
func (e *Executor) Generate(ctx context.Context) (*Result, error) {
	return e.run(ctx, "generate")
}

// run executes a terramate command
func (e *Executor) run(ctx context.Context, args ...string) (*Result, error) {
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, e.binary, args...) // #nosec G204 -- binary path is set at construction, not from user input
	cmd.Dir = e.workDir

	// Set environment
	cmd.Env = append(os.Environ(),
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
		fmt.Sprintf("TERRAMATE_EXPERIMENTAL_PARALLEL=%d", e.parallelism),
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startTime := time.Now()
	err := cmd.Run()
	duration := time.Since(startTime)

	result := &Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = 1
		}
		result.Success = false
		return result, err
	}

	result.Success = true
	result.ExitCode = 0
	return result, nil
}

// CreateStackConfig creates a terramate.tm.hcl configuration file
func CreateStackConfig(path, name, description string) string {
	return fmt.Sprintf(`# Terramate Stack Configuration
# Generated by StackKit CLI

stack {
  name        = %q
  description = %q
  id          = %q
}

# Orchestration settings
globals {
  terraform_version = ">= 1.6.0"
  backend           = "local"
}
`, name, description, path)
}

// CreateRootConfig creates a root terramate.tm.hcl configuration
func CreateRootConfig(projectName string) string {
	return fmt.Sprintf(`# Terramate Root Configuration
# Generated by StackKit CLI

terramate {
  config {
    # Enable experimental features
    experiments = ["scripts"]
    
    # Git settings for change detection
    git {
      check_untracked   = true
      check_uncommitted = true
      check_remote      = true
    }
    
    # Run settings
    run {
      env {
        TF_IN_AUTOMATION = "1"
        TF_INPUT         = "0"
      }
    }
  }
}

globals {
  project_name = %q
}
`, projectName)
}
