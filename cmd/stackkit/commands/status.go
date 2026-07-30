package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var (
	statusJSON             bool
	statusResolvedPlanPath string
)

var statusCmd = &cobra.Command{
	Use:         "status",
	Short:       "Show deployment status",
	Annotations: map[string]string{legacyV06BeforeObservabilityAnnotation: "status"},
	Long: `Display the current status of the StackKit deployment.

Shows:
  • Deployment state (running, degraded, error)
  • Service statuses and health
  • Resource usage
  • URLs and endpoints

Examples:
  stackkit status            Show status
  stackkit status --json     Output as JSON`,
	RunE: runStatus,
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Output as JSON")
	statusCmd.Flags().StringVar(&statusResolvedPlanPath, "resolved-plan", "", "Canonical Architecture v2 ResolvedPlan (default: deploy/.stackkit/resolved-plan.json)")
}

func runStatus(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	wd := getWorkDir()
	if architectureV2RejectsV1Execution(version) {
		return runArchitectureV2Status(cmd, wd)
	}

	loader := config.NewLoader(wd)
	spec, err := loadLegacyOperationalStackSpec(wd, specFile, architectureV2Status)
	if err != nil {
		return fmt.Errorf("failed to load spec: %w", err)
	}

	// Load deployment state
	stateFile := filepath.Join(wd, ".stackkit", "state.yaml")
	state, err := loader.LoadDeploymentState(stateFile)
	if err != nil || state == nil {
		printWarning("No deployment state found. Run 'stackkit apply' first.")
		return nil
	}

	// Print header
	fmt.Println()
	fmt.Printf("  %s: %s\n", bold("StackKit"), spec.StackKit)
	fmt.Printf("  %s: %s\n", bold("Mode"), spec.Mode)
	fmt.Printf("  %s: %s\n", bold("Last Applied"), state.LastApplied.Format("2006-01-02 15:04:05"))
	fmt.Println()

	// Get Docker containers
	dockerClient := docker.NewClient()
	if !dockerClient.IsInstalled() || !dockerClient.IsRunning(ctx) {
		printWarning("Docker is not running")
		return nil
	}

	containers, err := dockerClient.GetStackKitContainers(ctx)
	if err != nil {
		printWarning("Could not get container status: %v", err)
		return nil
	}

	if len(containers) == 0 {
		printInfo("No containers found")
		return nil
	}

	// Build service states
	var services []models.ServiceState
	access, accessErr := buildAccessSummary(wd, spec)
	if accessErr != nil {
		printWarning("Could not build access summary: %v", accessErr)
	} else {
		attachObservedSetupActions(access, state)
		if err := writeAccessSummary(wd, access); err != nil {
			printWarning("Could not write access summary: %v", err)
		}
	}
	urls := urlAliases(access)
	for _, c := range containers {
		health, _ := dockerClient.GetContainerHealth(ctx, c.ID)
		name := strings.ToLower(c.Name)
		services = append(services, models.ServiceState{
			Name:      c.Name,
			Container: c.ID[:12],
			Status:    docker.GetServiceStatus(&c),
			URL:       urls[name],
			Health:    health,
		})
	}

	overallStatus := determineOverallStatus(services)

	// JSON output mode
	if statusJSON {
		output := buildStatusJSONOutput(spec, state, services, overallStatus, access)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(output)
	}

	// Display table
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Service", "Status", "Health", "Container", "URL"})
	table.SetBorder(false)
	table.SetHeaderColor(
		tablewriter.Colors{tablewriter.Bold},
		tablewriter.Colors{tablewriter.Bold},
		tablewriter.Colors{tablewriter.Bold},
		tablewriter.Colors{tablewriter.Bold},
		tablewriter.Colors{tablewriter.Bold},
	)

	for _, s := range services {
		statusStr := formatStatus(s.Status)
		healthStr := formatHealth(s.Health)
		table.Append([]string{s.Name, statusStr, healthStr, s.Container, s.URL})
	}

	table.Render()

	// Overall status
	fmt.Println()
	switch overallStatus {
	case models.StatusRunning:
		printSuccess("Deployment is healthy")
	case models.StatusDegraded:
		printWarning("Deployment is degraded")
	case models.StatusError:
		printError("Deployment has errors")
	}

	if access != nil && access.HubURL != "" {
		fmt.Println()
		printSuccess("Hub: %s", access.HubURL)
	}

	return nil
}

func runArchitectureV2Status(cmd *cobra.Command, wd string) error {
	planPath := strings.TrimSpace(statusResolvedPlanPath)
	if planPath == "" {
		planPath = filepath.Join("deploy", ".stackkit", "resolved-plan.json")
	}
	planPath = resolvePathFromWorkDir(wd, planPath)
	raw, err := os.ReadFile(planPath)
	if err != nil {
		return fmt.Errorf("status: read canonical ResolvedPlan %s: %w", planPath, err)
	}
	authority, err := newArchitectureV2ExecutionGate().newAuthority()
	if err != nil {
		return fmt.Errorf("status: open Architecture v2 authority: %w", err)
	}
	if closer, ok := authority.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	verified, err := authority.VerifyCanonicalPlan(raw)
	if err != nil {
		return fmt.Errorf("status: verify canonical ResolvedPlan: %w", err)
	}
	plan, err := resolvedplan.DecodeCanonicalPlan(verified.Canonical())
	if err != nil {
		return fmt.Errorf("status: decode verified ResolvedPlan: %w", err)
	}
	lifecycles, err := loadApplicationLifecycleEvidence(wd, plan)
	if err != nil {
		return fmt.Errorf("status: load Application Kit lifecycle evidence: %w", err)
	}
	output := map[string]any{
		"apiVersion":            "stackkit.status/v2",
		"stackId":               plan["stackId"],
		"kit":                   plan["kit"],
		"planHash":              plan["planHash"],
		"executionReadiness":    plan["executionReadiness"],
		"workloads":             plan["workloads"],
		"applicationLifecycles": lifecycles,
	}
	if statusJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Stack: %v\nPlan: %v\n", plan["stackId"], plan["planHash"])
	if len(lifecycles) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Application Kits: none selected")
		return nil
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Application Kits:")
	for _, state := range lifecycles {
		status := "not-started"
		if len(state.Operations) > 0 {
			status = state.Operations[len(state.Operations)-1].Status
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s: %s\n", state.WorkloadRef, status)
	}
	return nil
}

func loadApplicationLifecycleEvidence(wd string, plan resolvedplan.ResolvedPlan) ([]applicationlifecycle.State, error) {
	raw, ok := plan["applicationLifecycles"].([]any)
	if !ok {
		return nil, errors.New("verified ResolvedPlan applicationLifecycles is missing")
	}
	states := make([]applicationlifecycle.State, 0, len(raw))
	store := applicationlifecycle.Store{Workspace: wd}
	for index, item := range raw {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("verified ResolvedPlan applicationLifecycles[%d] is invalid", index)
		}
		workloadRef, ok := object["workloadRef"].(string)
		if !ok {
			return nil, fmt.Errorf("verified ResolvedPlan applicationLifecycles[%d].workloadRef is invalid", index)
		}
		contract, err := applicationlifecycle.ContractFromResolvedPlan(plan, workloadRef)
		if err != nil {
			return nil, err
		}
		state, err := store.Load(contract)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

func buildStatusJSONOutput(spec *models.StackSpec, state *models.DeploymentState, services []models.ServiceState, overallStatus models.DeploymentStatus, access *accessSummary) map[string]interface{} {
	output := map[string]interface{}{
		"stackkit":    spec.StackKit,
		"mode":        spec.Mode,
		"lastApplied": state.LastApplied.Format("2006-01-02T15:04:05Z"),
		"status":      string(overallStatus),
		"services":    services,
	}
	if len(state.PlatformSystemApps) > 0 {
		output["platformSystemApps"] = state.PlatformSystemApps
	}
	if len(state.PlatformApps) > 0 {
		output["platformApps"] = state.PlatformApps
	}
	if access != nil {
		attachObservedSetupActions(access, state)
		output["hubUrl"] = access.HubURL
		output["access"] = access
	}
	return output
}

func formatStatus(status models.ServiceStatus) string {
	switch status {
	case models.ServiceStatusRunning:
		return green("running")
	case models.ServiceStatusStopped:
		return red("stopped")
	case models.ServiceStatusStarting:
		return yellow("starting")
	case models.ServiceStatusError:
		return red("error")
	default:
		return "unknown"
	}
}

func formatHealth(health models.HealthStatus) string {
	switch health {
	case models.HealthStatusHealthy:
		return green("healthy")
	case models.HealthStatusUnhealthy:
		return red("unhealthy")
	case models.HealthStatusStarting:
		return yellow("starting")
	case models.HealthStatusNone:
		return "-"
	default:
		return "-"
	}
}

func determineOverallStatus(services []models.ServiceState) models.DeploymentStatus {
	hasError := false
	hasDegraded := false

	for _, s := range services {
		if s.Status == models.ServiceStatusError {
			hasError = true
		}
		if s.Status == models.ServiceStatusStopped {
			hasDegraded = true
		}
		if s.Health == models.HealthStatusUnhealthy {
			hasDegraded = true
		}
	}

	if hasError {
		return models.StatusError
	}
	if hasDegraded {
		return models.StatusDegraded
	}
	return models.StatusRunning
}
