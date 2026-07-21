package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/iac"
	"github.com/spf13/cobra"
)

var (
	planOut                string
	planDestroy            bool
	planJSON               bool
	planV2ExecutionOptions architectureV2ExecutionCLIOptions
)

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Preview infrastructure changes",
	Long: `Inspect the governed Architecture v2 plan or preview v0.6 infrastructure changes.

On native v0.7, this command verifies and inspects the exact ResolvedPlan,
generation manifest, receipt, and generated artifact hashes without invoking
an executor. Exact v0.6 retains its packaged OpenTofu preview.

Examples:
  stackkit plan                    Preview changes
  stackkit plan -o plan.tfplan     Save plan to file
  stackkit plan --destroy          Preview destroy`,
	RunE: runPlan,
}

func init() {
	planCmd.Flags().StringVarP(&planOut, "out", "o", "", "Save plan to file")
	planCmd.Flags().BoolVar(&planDestroy, "destroy", false, "Create destroy plan")
	planCmd.Flags().BoolVar(&planJSON, "json", false, "Emit the native Architecture v2 plan inspection as JSON")
	planCmd.Flags().StringVar(&planV2ExecutionOptions.inventoryPath, "inventory", "", "Architecture v2 observed Inventory (otherwise one conventional inventory file is selected)")
	planCmd.Flags().StringVar(&planV2ExecutionOptions.planPath, "resolved-plan", "", "Architecture v2 canonical ResolvedPlan (default: <outputRoot>/.stackkit/resolved-plan.json)")
	planCmd.Flags().StringVar(&planV2ExecutionOptions.manifestPath, "artifact-manifest", "", "Architecture v2 generation manifest (default: <outputRoot>/.stackkit/generation-manifest.json)")
	planCmd.Flags().StringVar(&planV2ExecutionOptions.receiptPath, "generation-receipt", "", "Architecture v2 generation receipt (default: <outputRoot>/.stackkit/generation-receipt.json)")
}

func runPlan(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	wd := getWorkDir()
	options := planV2ExecutionOptions
	options.planOut = planOut
	options.planDestroy = planDestroy
	options.inspectionSink = func(inspection generationartifact.PlanInspection) error {
		return writeArchitectureV2PlanInspection(cmd.OutOrStdout(), inspection, planJSON)
	}
	if handled, err := newArchitectureV2ExecutionGate().preflight(wd, specFile, architectureV2Plan, options); handled {
		return err
	}
	if planJSON {
		return &architectureV2PlanOptionError{Flag: "--json", Message: "JSON plan inspection is available only for native Architecture v2; exact v0.6 keeps the OpenTofu preview"}
	}

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

type architectureV2PlanOptionError struct {
	Flag    string
	Message string
}

func (e *architectureV2PlanOptionError) Error() string {
	return fmt.Sprintf("native Architecture v2 plan option %s is unsupported: %s", e.Flag, e.Message)
}

func writeArchitectureV2PlanInspection(writer io.Writer, inspection generationartifact.PlanInspection, jsonOutput bool) error {
	if jsonOutput {
		canonical, err := inspection.MarshalCanonical()
		if err != nil {
			return err
		}
		_, err = writer.Write(append(canonical, '\n'))
		return err
	}
	binding := inspection.Binding
	if _, err := fmt.Fprintf(writer, "Architecture v2 plan inspection\n\nPlan:       %s\nSpec:       %s\nInventory:  %s\nDefinition: %s\nRenderer:   %s@%s\n", binding.PlanHash, binding.SpecHash, binding.InventoryHash, binding.DefinitionHash, binding.Renderer.ID, binding.Renderer.Version); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Generation: %s\nApply:      %s\nDiff:       %s\nExecutor:   not invoked\n", inspection.Readiness.Generation.Status, inspection.Readiness.Apply.Status, inspection.InfrastructureDiff); err != nil {
		return err
	}
	for _, blocker := range inspection.Readiness.Apply.Blockers {
		if _, err := fmt.Fprintf(writer, "  apply blocker: %s", blocker.Code); err != nil {
			return err
		}
		if len(blocker.Refs) > 0 {
			if _, err := fmt.Fprintf(writer, " [%s]", strings.Join(blocker.Refs, ", ")); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(writer); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(writer, "\nManifest: %s\nArtifacts (%d):\n", inspection.Manifest.Hash, len(inspection.Manifest.Artifacts)); err != nil {
		return err
	}
	for _, artifact := range inspection.Manifest.Artifacts {
		if _, err := fmt.Fprintf(writer, "  %s  %s  %s\n", artifact.SHA256, artifact.ID, artifact.Path); err != nil {
			return err
		}
	}
	return nil
}
