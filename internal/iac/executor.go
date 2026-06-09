// Package iac provides a unified interface for IaC execution.
// It supports both Day 1 operations (OpenTofu only) and Day 1+2 operations (Terramate).
package iac

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/terramate"
	"github.com/kombifyio/stackkits/internal/tofu"
	"github.com/kombifyio/stackkits/pkg/models"
)

// ExecutionMode represents the IaC execution mode
type ExecutionMode string

const (
	// ModeOpenTofu uses OpenTofu directly (Day 1 only)
	ModeOpenTofu ExecutionMode = "opentofu"
	// ModeTerramate uses Terramate for orchestration (Day 1 + Day 2)
	ModeTerramate ExecutionMode = "terramate"
)

// Executor provides a unified interface for IaC operations
type Executor interface {
	// Core operations
	Init(ctx context.Context) error
	Plan(ctx context.Context, outFile string, destroy bool) (*PlanResult, error)
	Apply(ctx context.Context, autoApprove bool, planFile string) (*ExecResult, error)
	Destroy(ctx context.Context, autoApprove bool) (*ExecResult, error)
	Validate(ctx context.Context) (*ExecResult, error)
	Output(ctx context.Context) (string, error)

	// Day 2 operations (may not be supported in all modes)
	DetectDrift(ctx context.Context) (*DriftResult, error)
	Refresh(ctx context.Context) error

	// Metadata
	Mode() ExecutionMode
	IsInstalled() bool
	Version(ctx context.Context) (string, error)
}

// ExecResult represents the result of an IaC command execution.
type ExecResult struct {
	Success  bool
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// PlanResult represents the result of a plan operation
type PlanResult struct {
	HasChanges bool
	Add        int
	Change     int
	Destroy    int
	Output     string
	Duration   time.Duration
}

// DriftResult represents the result of drift detection
type DriftResult struct {
	HasDrift  bool
	Resources []DriftedResource
	CheckedAt time.Time
	Duration  time.Duration
}

// DriftedResource represents a resource with drift
type DriftedResource struct {
	Type    string
	Name    string
	Address string
	Action  string
	Details string
}

// Config holds configuration for the executor
type Config struct {
	WorkDir     string
	Mode        ExecutionMode
	Timeout     time.Duration
	Parallelism int
	AutoApprove bool
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	return &Config{
		WorkDir:     ".",
		Mode:        ModeOpenTofu,
		Timeout:     14 * time.Minute,
		Parallelism: 1,
		AutoApprove: false,
	}
}

// NewExecutor creates a new IaC executor based on the mode
func NewExecutor(cfg *Config) (Executor, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	switch cfg.Mode {
	case ModeOpenTofu:
		return newOpenTofuExecutor(cfg), nil
	case ModeTerramate:
		return newTerramateExecutor(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported execution mode: %s", cfg.Mode)
	}
}

// NewExecutorFromSpec creates an executor based on the stack spec
func NewExecutorFromSpec(spec *models.StackSpec, workDir string) (Executor, error) {
	cfg := DefaultConfig()
	cfg.WorkDir = workDir

	// Determine engine from the normalized install mode. runtime:native is
	// handled before IaC execution and remains separate from this selector.
	if spec.UsesAdvancedIAC() {
		cfg.Mode = ModeTerramate
	} else {
		cfg.Mode = ModeOpenTofu
	}

	return NewExecutor(cfg)
}

// OpenTofuExecutor implements Executor using OpenTofu directly
type OpenTofuExecutor struct {
	executor *tofu.Executor
	workDir  string
}

func newOpenTofuExecutor(cfg *Config) *OpenTofuExecutor {
	return &OpenTofuExecutor{
		executor: tofu.NewExecutor(
			tofu.WithWorkDir(cfg.WorkDir),
			tofu.WithTimeout(cfg.Timeout),
			tofu.WithAutoApprove(cfg.AutoApprove),
		),
		workDir: cfg.WorkDir,
	}
}

func (e *OpenTofuExecutor) Mode() ExecutionMode {
	return ModeOpenTofu
}

func (e *OpenTofuExecutor) IsInstalled() bool {
	return e.executor.IsInstalled()
}

func (e *OpenTofuExecutor) Version(ctx context.Context) (string, error) {
	return e.executor.Version(ctx)
}

func (e *OpenTofuExecutor) Init(ctx context.Context) error {
	const maxAttempts = 3
	var lastStderr string

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result, err := e.executor.Init(ctx)
		if err != nil {
			return err
		}
		if result.Success {
			return nil
		}

		lastStderr = result.Stderr
		combinedOutput := result.Stderr + "\n" + result.Stdout
		if attempt == maxAttempts || !isTransientTofuInitFailure(combinedOutput) {
			return fmt.Errorf("init failed: %s", result.Stderr)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt*2) * time.Second):
		}
	}

	return fmt.Errorf("init failed: %s", lastStderr)
}

func isTransientTofuInitFailure(output string) bool {
	lower := strings.ToLower(output)
	for _, marker := range []string{
		"502 bad gateway",
		"503 service unavailable",
		"504 gateway timeout",
		"connection reset",
		"could not query provider registry",
		"failed to retrieve cryptographic signature",
		"request failed",
		"timeout awaiting response headers",
		"tls handshake timeout",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func (e *OpenTofuExecutor) Plan(ctx context.Context, outFile string, destroy bool) (*PlanResult, error) {
	result, err := e.executor.Plan(ctx, outFile, destroy)
	if err != nil {
		return nil, err
	}

	changes := tofu.ParsePlanOutput(result.Stdout)
	return &PlanResult{
		HasChanges: result.ExitCode == 2 || changes.Add > 0 || changes.Change > 0 || changes.Destroy > 0,
		Add:        changes.Add,
		Change:     changes.Change,
		Destroy:    changes.Destroy,
		Output:     result.Stdout,
		Duration:   result.Duration,
	}, nil
}

// tofuResultToExec converts a tofu.Result to an iac.ExecResult.
func tofuResultToExec(result *tofu.Result) *ExecResult {
	return &ExecResult{
		Success:  result.Success,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
		Duration: result.Duration,
	}
}

func (e *OpenTofuExecutor) Apply(ctx context.Context, autoApprove bool, planFile string) (*ExecResult, error) {
	e.executor.SetAutoApprove(autoApprove)
	result, err := e.executor.Apply(ctx, planFile)
	if err != nil {
		return nil, err
	}
	return tofuResultToExec(result), nil
}

func (e *OpenTofuExecutor) Destroy(ctx context.Context, autoApprove bool) (*ExecResult, error) {
	e.executor.SetAutoApprove(autoApprove)
	result, err := e.executor.Destroy(ctx)
	if err != nil {
		return nil, err
	}
	return tofuResultToExec(result), nil
}

func (e *OpenTofuExecutor) Validate(ctx context.Context) (*ExecResult, error) {
	result, err := e.executor.Validate(ctx)
	if err != nil {
		return nil, err
	}
	return tofuResultToExec(result), nil
}

func (e *OpenTofuExecutor) Output(ctx context.Context) (string, error) {
	result, err := e.executor.Output(ctx)
	if err != nil {
		return "", err
	}
	if !result.Success {
		return "", fmt.Errorf("output failed: %s", result.Stderr)
	}
	return result.Stdout, nil
}

func (e *OpenTofuExecutor) Refresh(ctx context.Context) error {
	result, err := e.executor.Refresh(ctx)
	if err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("refresh failed: %s", result.Stderr)
	}
	return nil
}

func (e *OpenTofuExecutor) DetectDrift(ctx context.Context) (*DriftResult, error) {
	// OpenTofu doesn't have native drift detection, so we run a plan
	// and check for changes
	startTime := time.Now()

	// Run refresh first
	if err := e.Refresh(ctx); err != nil {
		return nil, fmt.Errorf("refresh failed: %w", err)
	}

	// Then plan to see differences (not a destroy plan for drift detection)
	planResult, err := e.Plan(ctx, "", false)
	if err != nil {
		return nil, err
	}

	result := &DriftResult{
		HasDrift:  planResult.HasChanges,
		CheckedAt: startTime,
		Duration:  time.Since(startTime),
		Resources: []DriftedResource{},
	}

	// Parse plan output for resource changes
	if planResult.HasChanges {
		// Simplified parsing - in production you'd want more sophisticated parsing
		if planResult.Add > 0 {
			result.Resources = append(result.Resources, DriftedResource{
				Type:   "unknown",
				Action: "create",
			})
		}
		if planResult.Change > 0 {
			result.Resources = append(result.Resources, DriftedResource{
				Type:   "unknown",
				Action: "update",
			})
		}
		if planResult.Destroy > 0 {
			result.Resources = append(result.Resources, DriftedResource{
				Type:   "unknown",
				Action: "delete",
			})
		}
	}

	return result, nil
}

// TerramateExecutor implements Executor using Terramate
type TerramateExecutor struct {
	executor *terramate.Executor
	workDir  string
}

func newTerramateExecutor(cfg *Config) *TerramateExecutor {
	return &TerramateExecutor{
		executor: terramate.NewExecutor(
			terramate.WithWorkDir(cfg.WorkDir),
			terramate.WithTimeout(cfg.Timeout),
			terramate.WithParallelism(cfg.Parallelism),
		),
		workDir: cfg.WorkDir,
	}
}

func (e *TerramateExecutor) Mode() ExecutionMode {
	return ModeTerramate
}

func (e *TerramateExecutor) IsInstalled() bool {
	return e.executor.IsInstalled()
}

func (e *TerramateExecutor) Version(ctx context.Context) (string, error) {
	return e.executor.Version(ctx)
}

func (e *TerramateExecutor) Init(ctx context.Context) error {
	result, err := e.executor.RunInit(ctx)
	if err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("init failed: %s", result.Stderr)
	}
	return nil
}

func (e *TerramateExecutor) Plan(ctx context.Context, outFile string, destroy bool) (*PlanResult, error) {
	// Terramate orchestrates tofu plan across stacks; outFile and destroy are handled by Terramate
	result, err := e.executor.RunPlan(ctx)
	if err != nil {
		return nil, err
	}

	// Parse plan output
	changes := tofu.ParsePlanOutput(result.Stdout)
	return &PlanResult{
		HasChanges: result.ExitCode == 2 || changes.Add > 0 || changes.Change > 0 || changes.Destroy > 0,
		Add:        changes.Add,
		Change:     changes.Change,
		Destroy:    changes.Destroy,
		Output:     result.Stdout,
		Duration:   result.Duration,
	}, nil
}

// tmResultToExec converts a terramate.Result to an iac.ExecResult.
func tmResultToExec(result *terramate.Result) *ExecResult {
	return &ExecResult{
		Success:  result.Success,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
		Duration: result.Duration,
	}
}

func (e *TerramateExecutor) Apply(ctx context.Context, autoApprove bool, planFile string) (*ExecResult, error) {
	// Terramate handles plan-file semantics internally; planFile is ignored
	result, err := e.executor.RunApply(ctx, autoApprove)
	if err != nil {
		return nil, err
	}
	return tmResultToExec(result), nil
}

func (e *TerramateExecutor) Destroy(ctx context.Context, autoApprove bool) (*ExecResult, error) {
	result, err := e.executor.RunDestroy(ctx, autoApprove)
	if err != nil {
		return nil, err
	}
	return tmResultToExec(result), nil
}

func (e *TerramateExecutor) Validate(ctx context.Context) (*ExecResult, error) {
	// Terramate doesn't have a dedicated validate; run plan as validation
	result, err := e.executor.RunPlan(ctx)
	if err != nil {
		return nil, err
	}
	return tmResultToExec(result), nil
}

func (e *TerramateExecutor) Output(ctx context.Context) (string, error) {
	result, err := e.executor.Output(ctx)
	if err != nil {
		return "", err
	}
	if !result.Success {
		return "", fmt.Errorf("output failed: %s", result.Stderr)
	}
	return result.Stdout, nil
}

func (e *TerramateExecutor) Refresh(ctx context.Context) error {
	result, err := e.executor.Refresh(ctx)
	if err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("refresh failed: %s", result.Stderr)
	}
	return nil
}

func (e *TerramateExecutor) DetectDrift(ctx context.Context) (*DriftResult, error) {
	tmResult, err := e.executor.DetectDrift(ctx)
	if err != nil {
		return nil, err
	}

	result := &DriftResult{
		HasDrift:  tmResult.HasDrift,
		CheckedAt: tmResult.CheckedAt,
		Duration:  tmResult.Duration,
		Resources: []DriftedResource{},
	}

	// Convert Terramate changes to our format
	for _, stack := range tmResult.Stacks {
		for _, change := range stack.Changes {
			result.Resources = append(result.Resources, DriftedResource{
				Type:    change.ResourceType,
				Name:    change.ResourceName,
				Address: fmt.Sprintf("%s.%s", change.ResourceType, change.ResourceName),
				Action:  change.Action,
				Details: change.Details,
			})
		}
	}

	return result, nil
}
