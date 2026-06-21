// Package commands implements the CLI commands for stackkit.
package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/kombifyio/stackkits/internal/logging"
	"github.com/kombifyio/stackkits/internal/rollout"
	"github.com/kombifyio/stackkits/internal/telemetry"
	"github.com/spf13/cobra"
)

var (
	version   = "dev"
	gitCommit = "unknown"
	buildDate = "unknown"
)

// SetVersionInfo sets version information from build
func SetVersionInfo(v, gc, bd string) {
	version = v
	gitCommit = gc
	buildDate = bd
}

// Color helpers
var (
	green  = color.New(color.FgGreen).SprintFunc()
	yellow = color.New(color.FgYellow).SprintFunc()
	red    = color.New(color.FgRed).SprintFunc()
	cyan   = color.New(color.FgCyan).SprintFunc()
	bold   = color.New(color.Bold).SprintFunc()
)

// Global flags
var (
	verbose     bool
	quiet       bool
	workDir     string
	specFile    string
	contextFlag string
	noLog       bool
)

// deployLog is the structured deploy logger for the current CLI run.
var deployLog *logging.DeployLogger

// rolloutRecorder writes product-facing evidence for the current CLI run.
var (
	rolloutRecorder       *rollout.Recorder
	rolloutRecorderClosed bool
	rolloutFailurePhase   string
	rolloutOTelRuntime    telemetry.OTelRuntime
	rolloutOTelShutdown   func(context.Context) error
	rolloutPhaseSpans     map[string]telemetry.SpanHandle
)

// rootCmd represents the base command
var rootCmd = &cobra.Command{
	Use:   "stackkit",
	Short: "StackKit CLI - Infrastructure deployment from declarative blueprints",
	Long: `StackKit CLI enables infrastructure deployment directly from the terminal.

It handles:
  • StackKit discovery and selection
  • Configuration validation (CUE)
  • OpenTofu execution
  • Drift detection and updates
  • System prerequisites (Docker, OpenTofu)

Examples:
  stackkit init base-kit           Initialize a new deployment
  stackkit prepare --spec spec.yaml    Prepare system and validate spec
  stackkit plan                        Preview infrastructure changes
  stackkit apply                       Apply infrastructure changes
  stackkit verify                      Run post-deployment verification checks
  stackkit status                      Check deployment status
  stackkit remove                      Tear down deployment`,
	SilenceUsage: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Show banner for root help and key workflow commands
		name := cmd.Name()
		if name == "stackkit" || name == "init" || name == "apply" {
			printBanner()
		}

		// Initialize structured deploy logger (skip for help/completion/version)
		switch name {
		case "stackkit", "help", "completion", "version", "logs", "list", "agent", "install-plan", "self-check", "prompt", "mcp-config":
			// no logging for these commands
		default:
			if !noLog {
				initDeployLogger()
			}
		}
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		closeRolloutRecorder(rollout.Summary{Status: "success"})
		if deployLog != nil {
			deployLog.Close()
		}
	},
}

// Execute runs the root command
func Execute() error {
	defer func() {
		closeRolloutRecorder(rollout.Summary{Status: "success"})
		if deployLog != nil {
			deployLog.Close()
			deployLog = nil
		}
		rolloutRecorder = nil
		rolloutRecorderClosed = false
		rolloutFailurePhase = ""
		rolloutOTelRuntime = telemetry.OTelRuntime{}
		rolloutOTelShutdown = nil
		rolloutPhaseSpans = nil
	}()
	return rootCmd.Execute()
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")
	rootCmd.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-essential output")
	rootCmd.PersistentFlags().StringVarP(&workDir, "chdir", "C", ".", "Change to directory before running")
	rootCmd.PersistentFlags().StringVarP(&specFile, "spec", "s", "stack-spec.yaml", "Path to stack specification file (kombination.yaml is accepted when the default is missing)")
	rootCmd.PersistentFlags().StringVar(&contextFlag, "context", "", "Node context override (local, cloud, pi). Auto-detected if omitted.")
	rootCmd.PersistentFlags().BoolVar(&noLog, "no-log", false, "Disable structured deploy logging")

	// Add subcommands
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(prepareCmd)
	rootCmd.AddCommand(generateCmd)
	rootCmd.AddCommand(planCmd)
	rootCmd.AddCommand(applyCmd)
	rootCmd.AddCommand(verifyCmd)
	rootCmd.AddCommand(removeCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(completionCmd)
	rootCmd.AddCommand(appCmd)
	rootCmd.AddCommand(compatCmd)
	rootCmd.AddCommand(clusterCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(agentCmd)
}

// Helper functions for output

// printSuccess prints a success message
func printSuccess(format string, args ...interface{}) {
	if !quiet {
		fmt.Printf("%s %s\n", green("✓"), fmt.Sprintf(format, args...))
	}
}

// printWarning prints a warning message
func printWarning(format string, args ...interface{}) {
	if !quiet {
		fmt.Printf("%s %s\n", yellow("⚠"), fmt.Sprintf(format, args...))
	}
}

// printError prints an error message
func printError(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(os.Stderr, "%s %s\n", red("✗"), fmt.Sprintf(format, args...))
}

// printInfo prints an info message
func printInfo(format string, args ...interface{}) {
	if !quiet {
		fmt.Printf("%s %s\n", cyan("ℹ"), fmt.Sprintf(format, args...))
	}
}

// printVerbose prints verbose output
func printVerbose(format string, args ...interface{}) {
	if verbose {
		fmt.Printf("  %s\n", fmt.Sprintf(format, args...))
	}
}

// initDeployLogger creates the structured deploy logger.
// Closes any previously open logger (for test safety).
func initDeployLogger() {
	if deployLog != nil {
		deployLog.Close()
		deployLog = nil
	}
	closeRolloutRecorder(rollout.Summary{Status: "success"})
	rolloutRecorder = nil
	rolloutRecorderClosed = false
	rolloutFailurePhase = ""

	wd := getWorkDir()
	logDir := filepath.Join(wd, ".stackkit", "logs")
	deployLog = logging.New(logDir)
	initRolloutRecorder(wd)
}

func initRolloutRecorder(wd string) {
	runID := ""
	if deployLog != nil {
		runID = deployLog.RunID()
	}
	rec, err := rollout.NewRecorder(filepath.Join(wd, ".stackkit"), rollout.Metadata{
		RunID:              runID,
		TenantDeploymentID: firstEnv("STACKKIT_TENANT_DEPLOYMENT_ID"),
		TenantID:           firstEnv("STACKKIT_TENANT_ID"),
		Environment:        firstEnv("STACKKIT_ENVIRONMENT", "KOMBIFY_ENVIRONMENT", "GO_ENV"),
		Provider:           firstEnv("STACKKIT_PROVIDER", "STACKKIT_E2E_CLOUD_NODE_ENGINE"),
	})
	if err != nil {
		printVerbose("rollout evidence disabled: %v", err)
		return
	}
	rolloutRecorder = rec
	rolloutFailurePhase = ""
	initRolloutTelemetry(runID)
}

func initRolloutTelemetry(runID string) {
	rolloutOTelRuntime = telemetry.OTelRuntime{}
	rolloutOTelShutdown = nil
	rolloutPhaseSpans = nil
	runtime, shutdown, err := telemetry.SetupOTel(context.Background(), telemetry.OTelConfig{
		ServiceName:        "stackkit-cli",
		ServiceVersion:     version,
		RunID:              runID,
		TenantDeploymentID: firstEnv("STACKKIT_TENANT_DEPLOYMENT_ID"),
		StackKit:           firstEnv("STACKKIT_STACKKIT", "STACKKIT_KIT"),
		Environment:        firstEnv("STACKKIT_ENVIRONMENT", "KOMBIFY_ENVIRONMENT", "GO_ENV"),
		Provider:           firstEnv("STACKKIT_PROVIDER", "STACKKIT_E2E_CLOUD_NODE_ENGINE"),
		NodeID:             firstEnv("STACKKIT_NODE_ID", "STACKKIT_TARGET_NODE_ID"),
	})
	if err != nil {
		printVerbose("otel telemetry disabled: %v", err)
		return
	}
	rolloutOTelRuntime = runtime
	rolloutOTelShutdown = shutdown
	if runtime.Enabled {
		rolloutPhaseSpans = map[string]telemetry.SpanHandle{}
	}
}

func rolloutEvent(phase, status, message string, attrs map[string]string) {
	if rolloutRecorder == nil {
		return
	}
	recordRolloutSpanEvent(phase, status, message, attrs, nil)
	rolloutRecorder.Event(rollout.Event{
		Phase:      phase,
		Status:     status,
		Message:    message,
		Attributes: attrs,
	})
}

func rolloutFailure(phase string, err error) {
	if err == nil {
		return
	}
	if rolloutRecorder == nil {
		return
	}
	if rolloutFailurePhase == "" || rolloutFailurePhase == "apply" {
		rolloutFailurePhase = phase
	}
	recordRolloutSpanEvent(phase, "failed", err.Error(), nil, err)
	rolloutRecorder.Event(rollout.Event{
		Phase:        phase,
		Status:       "failed",
		Message:      err.Error(),
		FailureClass: rollout.ClassifyFailure(err.Error()),
	})
}

func closeRolloutRecorder(summary rollout.Summary) {
	if rolloutRecorder == nil || rolloutRecorderClosed {
		return
	}
	if len(summary.Artifacts) == 0 && deployLog != nil && deployLog.LogPath() != "" {
		summary.Artifacts = []string{deployLog.LogPath()}
	}
	if summary.Status == "" {
		summary.Status = "success"
	}
	if summary.Message != "" && summary.FailureClass == "" {
		summary.FailureClass = rollout.ClassifyFailure(summary.Message)
	}
	if summary.Status == "failed" {
		if sentryArtifact := captureSentryFailureEvidence(summary); sentryArtifact != "" {
			summary.Artifacts = append(summary.Artifacts, sentryArtifact)
		}
	}
	closeRolloutTelemetry(summary)
	_ = rolloutRecorder.Close(summary)
	rolloutRecorderClosed = true
}

func recordRolloutSpanEvent(phase, status, message string, attrs map[string]string, err error) {
	if !rolloutOTelRuntime.Enabled {
		return
	}
	phase = strings.TrimSpace(phase)
	if phase == "" {
		phase = "rollout"
	}
	status = strings.TrimSpace(status)
	if rolloutPhaseSpans == nil {
		rolloutPhaseSpans = map[string]telemetry.SpanHandle{}
	}
	span, open := rolloutPhaseSpans[phase]
	spanAttrs := rolloutSpanAttributes(phase, status, message, attrs)
	terminal := rolloutSpanStatusTerminal(status)
	if !open {
		_, span = telemetry.StartSpan(context.Background(), "stackkit.rollout."+phase, spanAttrs)
		if !terminal {
			rolloutPhaseSpans[phase] = span
		}
	} else {
		span.AddEvent("rollout."+status, spanAttrs)
		span.SetAttributes(spanAttrs)
	}
	if err != nil {
		span.RecordError(err)
	}
	span.SetRolloutStatus(status, message)
	if terminal {
		span.End()
		delete(rolloutPhaseSpans, phase)
	}
}

func closeRolloutTelemetry(summary rollout.Summary) {
	if rolloutOTelRuntime.Enabled {
		for phase, span := range rolloutPhaseSpans {
			spanAttrs := rolloutSpanAttributes(phase, summary.Status, summary.Message, map[string]string{
				"failure_class": summary.FailureClass,
			})
			span.SetAttributes(spanAttrs)
			span.SetRolloutStatus(summary.Status, summary.Message)
			span.End()
			delete(rolloutPhaseSpans, phase)
		}
	}
	if rolloutOTelShutdown != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := rolloutOTelShutdown(ctx); err != nil {
			printVerbose("otel telemetry shutdown failed: %v", err)
		}
		cancel()
	}
	rolloutOTelRuntime = telemetry.OTelRuntime{}
	rolloutOTelShutdown = nil
	rolloutPhaseSpans = nil
}

func rolloutSpanAttributes(phase, status, message string, attrs map[string]string) map[string]string {
	spanAttrs := map[string]string{
		"stackkit.rollout.phase":  phase,
		"stackkit.rollout.status": status,
	}
	if strings.TrimSpace(message) != "" {
		spanAttrs["stackkit.rollout.message"] = telemetry.RedactTelemetryValue(message)
	}
	for key, value := range attrs {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		spanAttrs["stackkit.rollout."+key] = value
	}
	return spanAttrs
}

func rolloutSpanStatusTerminal(status string) bool {
	switch strings.TrimSpace(status) {
	case "succeeded", "success", "skipped", "failed":
		return true
	default:
		return false
	}
}

func captureSentryFailureEvidence(summary rollout.Summary) string {
	if rolloutRecorder == nil || rolloutRecorder.Root() == "" {
		return ""
	}
	phase := rolloutFailurePhase
	if phase == "" {
		phase = "rollout"
	}
	failureClass := summary.FailureClass
	if failureClass == "" && summary.Message != "" {
		failureClass = rollout.ClassifyFailure(summary.Message)
	}
	path, runtime, err := telemetry.CaptureSentryFailure(rolloutRecorder.Root(), telemetry.SentryConfig{
		RunID:              rolloutRecorder.RunID(),
		TenantDeploymentID: firstNonEmpty(applyTenantDeployment, firstEnv("STACKKIT_TENANT_DEPLOYMENT_ID")),
		StackKit:           firstEnv("STACKKIT_STACKKIT", "STACKKIT_KIT"),
		Environment:        firstEnv("STACKKIT_ENVIRONMENT", "KOMBIFY_ENVIRONMENT", "GO_ENV"),
		Provider:           firstEnv("STACKKIT_PROVIDER", "STACKKIT_E2E_CLOUD_NODE_ENGINE"),
		Phase:              phase,
		FailureClass:       failureClass,
		RolloutMode:        rolloutModeForTelemetry(),
	}, summary.Message, nil)
	if err != nil {
		printVerbose("sentry failure evidence unavailable: %v", err)
		return ""
	}
	if runtime.ForbiddenAuthToken {
		printVerbose("sentry failure evidence disabled: Sentry auth/API token is not allowed on target nodes")
	}
	return path
}

func rolloutModeForTelemetry() string {
	if strings.TrimSpace(applyTenantDeployment) != "" || strings.TrimSpace(firstEnv("STACKKIT_TENANT_DEPLOYMENT_ID")) != "" {
		return "techstack"
	}
	return "cli"
}

// getLogDir returns the log directory path for the current working directory.
func getLogDir() string {
	return filepath.Join(getWorkDir(), ".stackkit", "logs")
}

// getWorkDir returns the effective working directory
func getWorkDir() string {
	if workDir != "." {
		return workDir
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
