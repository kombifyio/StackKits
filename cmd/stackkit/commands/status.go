package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/kombifyme"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var (
	statusJSON bool
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show deployment status",
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
}

func runStatus(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	wd := getWorkDir()

	// Load spec
	loader := config.NewLoader(wd)
	spec, err := loader.LoadStackSpec(specFile)
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

	// kombify.me subdomain status (when domain is kombify.me)
	if strings.EqualFold(spec.Domain, models.DomainKombifyMe) {
		showSubdomainStatus(spec)
	}

	if access != nil && access.HubURL != "" {
		fmt.Println()
		printSuccess("Hub: %s", access.HubURL)
	}

	return nil
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

// showSubdomainStatus queries the kombify.me API and displays subdomain status.
func showSubdomainStatus(spec *models.StackSpec) {
	apiKey, err := kombifyme.LoadAPIKey()
	if err != nil {
		printWarning("kombify.me: no API key configured")
		return
	}

	client := kombifyme.NewClient(apiKey)

	if spec.SubdomainPrefix == "" {
		printWarning("kombify.me: no subdomain prefix set")
		return
	}

	// Use the base subdomain ID if stored, otherwise try listing services by the prefix
	services, err := client.ListServicesByPrefix(spec.SubdomainPrefix)
	if err != nil {
		printWarning("kombify.me: could not fetch subdomain status: %v", err)
		return
	}

	if len(services) == 0 {
		printInfo("kombify.me: no subdomains registered")
		return
	}

	fmt.Println()
	fmt.Printf("  %s\n", bold("kombify.me Subdomains"))
	fmt.Println()

	subTable := tablewriter.NewWriter(os.Stdout)
	subTable.SetHeader([]string{"Subdomain", "FQDN", "Status", "Exposed"})
	subTable.SetBorder(false)
	subTable.SetHeaderColor(
		tablewriter.Colors{tablewriter.Bold},
		tablewriter.Colors{tablewriter.Bold},
		tablewriter.Colors{tablewriter.Bold},
		tablewriter.Colors{tablewriter.Bold},
	)

	for _, s := range services {
		statusStr := s.Status
		if s.Status == "active" {
			statusStr = green("active")
		} else if s.Status == "dormant" {
			statusStr = yellow("dormant")
		}
		exposedStr := "-"
		if s.Exposed {
			exposedStr = green("yes")
		}
		subTable.Append([]string{s.Name, s.FQDN, statusStr, exposedStr})
	}

	subTable.Render()

	// Check if any subdomains are dormant (pending verification)
	hasDormant := false
	for _, s := range services {
		if s.Status == "dormant" {
			hasDormant = true
			break
		}
	}
	if hasDormant {
		fmt.Println()
		printWarning("Some subdomains are dormant — verify your email to activate them")
	}
}
