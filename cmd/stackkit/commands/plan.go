package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/iac"
	"github.com/spf13/cobra"
)

var (
	planOut     string
	planDestroy bool
)

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Preview infrastructure changes",
	Long: `Generate and show an execution plan for the infrastructure.

This command runs StackKit-packaged OpenTofu to preview what changes
would be made to your infrastructure without actually applying them.

Examples:
  stackkit plan                    Preview changes
  stackkit plan -o plan.tfplan     Save plan to file
  stackkit plan --destroy          Preview destroy`,
	RunE: runPlan,
}

func init() {
	planCmd.Flags().StringVarP(&planOut, "out", "o", "", "Save plan to file")
	planCmd.Flags().BoolVar(&planDestroy, "destroy", false, "Create destroy plan")
}

func runPlan(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	wd := getWorkDir()

	// Load spec
	loader := config.NewLoader(wd)
	spec, err := loader.LoadStackSpec(specFile)
	if err != nil {
		return fmt.Errorf("failed to load spec: %w", err)
	}

	printInfo("Planning deployment: %s (mode: %s)", spec.StackKit, spec.Mode)

	// Determine deploy directory
	deployDir := filepath.Join(wd, config.GetDeployDir())
	if _, statErr := os.Stat(deployDir); os.IsNotExist(statErr) {
		return fmt.Errorf("deploy directory not found: %s\nRun 'stackkit init' first", deployDir)
	}

	// Create IaC executor from spec (supports OpenTofu and Terramate modes)
	executor, err := iac.NewExecutorFromSpec(spec, deployDir)
	if err != nil {
		return fmt.Errorf("failed to create executor: %w", err)
	}

	// Check if tool is installed
	if !executor.IsInstalled() {
		return fmt.Errorf("%s is not installed. Run 'stackkit prepare' first", executor.Mode())
	}

	// Initialize if needed
	tfStatePath := filepath.Join(deployDir, ".terraform")
	if _, statErr := os.Stat(tfStatePath); os.IsNotExist(statErr) {
		printInfo("Initializing %s...", executor.Mode())
		if initErr := executor.Init(ctx); initErr != nil {
			return fmt.Errorf("init error: %w", initErr)
		}
		printSuccess("Initialized successfully")
	}

	// Run plan
	printInfo("Running plan...")

	planFile := planOut
	if planFile == "" {
		planFile = filepath.Join(deployDir, "plan.tfplan")
	}

	planResult, err := executor.Plan(ctx, planFile, planDestroy)
	if err != nil {
		return fmt.Errorf("plan error: %w", err)
	}

	// Display plan output
	if planResult.Output != "" {
		fmt.Println()
		fmt.Println(planResult.Output)
	}

	fmt.Println()
	if planResult.HasChanges {
		printInfo("Plan summary: %d to add, %d to change, %d to destroy",
			planResult.Add, planResult.Change, planResult.Destroy)

		if planOut != "" {
			printSuccess("Plan saved to: %s", planFile)
			printInfo("Run 'stackkit apply %s' to apply this plan", planFile)
		} else {
			printInfo("Run 'stackkit apply' to apply these changes")
		}
	} else {
		printSuccess("No changes. Infrastructure is up-to-date.")
	}

	return nil
}
