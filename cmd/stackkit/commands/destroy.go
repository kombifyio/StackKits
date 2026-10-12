package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/workloadremoval"
	"github.com/spf13/cobra"
)

var (
	removeAutoApprove          bool
	removeForce                bool
	removePurge                bool
	removeJSON                 bool
	removeTerminalEvidenceJSON bool
	removeDeleteData           bool
	removeWorkloadRef          string
	removeV2ExecutionOptions   architectureV2ExecutionCLIOptions
	newRemoveDockerClient      = docker.NewLocalClient
)

const removeConfirmationValue = "yes"

var removeCmd = &cobra.Command{
	Use:   "remove",
	Short: "Uninstall the local StackKits deployment",
	Long: `Uninstall the StackKits runtime on this machine.

With no --workload, this is the product uninstall: it stops and deletes every
Compose project named stackkit-* (Basement core, Cloud core, and selected use
cases), including their networks, while retaining named data volumes. Add
--delete-data to delete those volumes intentionally. --purge also deletes
the selected workspace's state, deploy directory and cached images.
It looks in the current
directory, then ~/my-homelab or ~/my-cloud-homelab.

--workload removes one exact Architecture v2 workload through its applied
runtime owner. That path keeps application volumes unless --delete-data is set.`,
	Example: `  # Uninstall the whole local deployment after typing yes
  stackkit remove

  # Uninstall without the confirmation prompt
  stackkit remove --auto-approve

  # Uninstall the local deployment and intentionally delete its named volumes
  stackkit remove --delete-data --auto-approve

  # Remove only the Files workload and keep its data volumes
  stackkit remove --workload files

  # Remove the Photos workload, delete its data volumes, and print the result as JSON
  stackkit remove --workload photos --delete-data --auto-approve --json`,
	RunE: runRemove,
}

func init() {
	removeCmd.Flags().BoolVar(&removeAutoApprove, "auto-approve", false, "Skip interactive approval")
	removeCmd.Flags().BoolVar(&removeForce, "force", false, "Legacy compatibility flag; incomplete removal still fails and retains recovery state")
	removeCmd.Flags().BoolVar(&removePurge, "purge", false, "Delete named runtime volumes, selected workspace state and deploy directory, and cached StackKit images")
	removeCmd.Flags().BoolVar(&removeJSON, "json", false, "Emit the canonical Architecture v2 removal result as JSON")
	removeCmd.Flags().BoolVar(&removeTerminalEvidenceJSON, "terminal-evidence-json", false, "Emit bounded canonical Architecture v2 terminal evidence as JSON")
	removeCmd.Flags().BoolVar(&removeDeleteData, "delete-data", false, "Delete named volumes in the selected removal scope (default keeps application data)")
	removeCmd.Flags().StringVar(&removeWorkloadRef, "workload", "", "Exact Architecture v2 ResolvedPlan workload ref to remove")
	removeCmd.Flags().StringVar(&removeV2ExecutionOptions.localSiteRef, "local-site", "", "Exact applied Site placement owned by this removal process")
	removeCmd.Flags().StringVar(&removeV2ExecutionOptions.localNodeRef, "local-node", "", "Exact applied node placement owned by this removal process")
	removeCmd.Flags().StringVar(&removeV2ExecutionOptions.localChannelRef, "local-execution-channel", "", "Exact applied execution channel owned by this removal process")
	removeCmd.Flags().StringVar(&removeV2ExecutionOptions.inventoryPath, "inventory", "", "Architecture v2 observed Inventory (otherwise one conventional inventory file is selected)")
	removeCmd.Flags().StringVar(&removeV2ExecutionOptions.planPath, "resolved-plan", "", "Architecture v2 canonical ResolvedPlan")
	removeCmd.Flags().StringVar(&removeV2ExecutionOptions.manifestPath, "artifact-manifest", "", "Architecture v2 generation manifest")
	removeCmd.Flags().StringVar(&removeV2ExecutionOptions.receiptPath, "generation-receipt", "", "Architecture v2 generation receipt")
}

func runRemove(cmd *cobra.Command, args []string) error {
	if commandRequestsMachineOutput(cmd) && !removeAutoApprove {
		return machineAwareCommandError(cmd, errors.New("removal approval required for machine output"),
			"Review the removal scope and data retention options, then re-run with --auto-approve to explicitly approve removal.")
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	wd := adoptConventionalInstallerWorkspace(getWorkDir())

	if !removeAutoApprove {
		fmt.Println()
		if strings.TrimSpace(removeWorkloadRef) != "" {
			if removeDeleteData {
				printError("WARNING: This will remove workload %q and delete only its owned data volumes!", strings.TrimSpace(removeWorkloadRef))
			} else {
				printError("WARNING: This will remove workload %q and keep its application data volumes.", strings.TrimSpace(removeWorkloadRef))
			}
		} else if removePurge {
			printError("WARNING: This will remove the local StackKits runtime, its named volumes, and the selected workspace state and deploy directory.")
		} else if removeDeleteData {
			printError("WARNING: This will remove the local StackKits runtime and delete its named data volumes.")
		} else {
			printError("WARNING: This will stop and delete the local StackKits runtime and keep its named data volumes.")
		}
		fmt.Printf("Type %q to confirm: ", removeConfirmationValue)

		var confirm string
		_, _ = fmt.Scanln(&confirm)
		if confirm != removeConfirmationValue {
			printInfo("Remove canceled")
			return nil
		}
	}

	removeV2ExecutionOptions.context = ctx
	removeV2ExecutionOptions.workloadRef = strings.TrimSpace(removeWorkloadRef)
	if removeDeleteData {
		removeV2ExecutionOptions.dataDisposition = workloadremoval.DataDispositionDelete
	} else {
		removeV2ExecutionOptions.dataDisposition = workloadremoval.DataDispositionRetain
	}
	removeV2ExecutionOptions.removalJSON = removeJSON
	removeV2ExecutionOptions.removalEvidenceJSON = removeTerminalEvidenceJSON
	if removeJSON && removeTerminalEvidenceJSON {
		return errors.New("--json and --terminal-evidence-json are mutually exclusive")
	}
	if strings.TrimSpace(removeWorkloadRef) == "" {
		if removeJSON || removeTerminalEvidenceJSON {
			return errors.New("--json and --terminal-evidence-json require --workload")
		}
		return removeWholeLocalDeployment(ctx, wd, removeDeleteData || removePurge, removePurge)
	}
	if removeJSON {
		removeV2ExecutionOptions.removalSink = func(result workloadremoval.Result) error {
			canonical, err := result.Canonical()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(canonical))
			return err
		}
	} else {
		removeV2ExecutionOptions.removalSink = nil
	}
	if removeTerminalEvidenceJSON {
		removeV2ExecutionOptions.removalEvidenceSink = func(evidence workloadremoval.Evidence) error {
			canonical, err := evidence.Canonical()
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(canonical)
			return err
		}
	} else {
		removeV2ExecutionOptions.removalEvidenceSink = nil
	}
	_, sourceVersion, classified, classifyErr := classifyArchitectureV2ExecutionSpec(wd, specFile)
	if classifyErr != nil {
		return classifyErr
	}
	if removeDeleteData && !(classified && sourceVersion.IsV2()) {
		return errors.New("--delete-data requires canonical Architecture v2 native workload removal")
	}
	if classified && sourceVersion.IsV2() && (removePurge || removeForce) {
		return errors.New("canonical Architecture v2 workload removal does not accept legacy --purge or --force authority")
	}
	if handled, err := newArchitectureV2ExecutionGate().preflight(
		wd, specFile, architectureV2Remove, removeV2ExecutionOptions,
	); handled {
		return err
	}
	return errors.New("--workload, --json, and --terminal-evidence-json require canonical Architecture v2 removal")
}

func removeWholeLocalDeployment(ctx context.Context, wd string, deleteVolumes, purge bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	printWarning("Removing the local StackKits Docker stack")
	if deleteVolumes {
		printWarning("Data policy: delete named volumes owned by the removed StackKits runtime")
	} else {
		printInfo("Data policy: retain named application volumes and the original workspace")
	}
	client := newRemoveDockerClient()
	if !client.IsInstalled() {
		return errors.New("docker is not installed; cannot uninstall the running stack")
	}
	if !client.IsRunning(ctx) {
		return errors.New("docker daemon is not running; cannot uninstall the running stack")
	}
	discovered, err := client.ListComposeProjects(ctx)
	if err != nil {
		return actionableerror.WithDiagnostic("remove_incomplete", fmt.Errorf("list local Compose projects: %w", err))
	}
	projects := docker.StackKitsComposeProjectsToRemove(discovered)
	var failed []string
	for _, project := range projects {
		printInfo("Removing Compose project %s", project)
		if rmErr := client.RemoveComposeProject(ctx, project, deleteVolumes); rmErr != nil {
			failed = append(failed, project+": "+rmErr.Error())
			printWarning("  %s", rmErr)
			continue
		}
		printSuccess("  Removed %s", project)
	}
	if err := dockerFallbackCleanup(ctx, deleteVolumes, purge); err != nil {
		failed = append(failed, err.Error())
	}
	if len(failed) > 0 {
		return actionableerror.WithDiagnostic("remove_incomplete", fmt.Errorf("remove incomplete: %s", strings.Join(failed, "; ")))
	}
	cleanupFiles(wd, purge)
	removeCoreMCPCredentials(wd)
	printSuccess("Remove complete")
	if !deleteVolumes {
		printInfo("Keep the original workspace and release when reinstalling to reuse retained data volumes")
	} else {
		printInfo("Named runtime volumes deleted; external backups and host bind mounts are outside this deletion scope")
	}
	return nil
}

// dockerFallbackCleanup removes StackKit resources directly via Docker CLI.
// Completes local cleanup after Compose removal or unavailable Apply evidence;
// named data volumes are touched only under the explicit deletion policy.
func dockerFallbackCleanup(ctx context.Context, deleteVolumes, purge bool) error {
	dockerClient := newRemoveDockerClient()
	if !dockerClient.IsInstalled() {
		printWarning("Docker is not installed — skipping container cleanup")
		return nil
	}
	if !dockerClient.IsRunning(ctx) {
		printWarning("Docker daemon is not running — skipping container cleanup")
		return nil
	}

	var errors []string

	// 1. Remove containers (must be first — releases network/volume references)
	containers, err := dockerClient.GetStackKitContainers(ctx)
	if err != nil {
		printWarning("Could not list containers: %v", err)
	} else if len(containers) > 0 {
		printInfo("Removing %d container(s)...", len(containers))
		for _, c := range containers {
			if rmErr := dockerClient.RemoveContainer(ctx, c.ID); rmErr != nil {
				errors = append(errors, fmt.Sprintf("container %s: %v", c.Name, rmErr))
				printWarning("  Failed to remove container %s: %v", c.Name, rmErr)
			} else {
				printSuccess("  Removed container %s", c.Name)
			}
		}
	}

	// 2. Remove networks
	networks, err := dockerClient.ListNetworksByLabel(ctx, "stackkit.layer")
	if err != nil {
		printWarning("Could not list networks: %v", err)
	} else if len(networks) > 0 {
		printInfo("Removing %d network(s)...", len(networks))
		for _, n := range networks {
			if rmErr := dockerClient.RemoveNetwork(ctx, n); rmErr != nil {
				errors = append(errors, fmt.Sprintf("network %s: %v", n, rmErr))
				printWarning("  Failed to remove network %s: %v", n, rmErr)
			} else {
				printSuccess("  Removed network %s", n)
			}
		}
	}

	// 3. Data deletion is a separate explicit policy, including orphaned volumes.
	var volumes []string
	if deleteVolumes {
		volumes, err = dockerClient.ListVolumesByLabel(ctx, "stackkit.layer")
		if err != nil {
			return fmt.Errorf("list owned data volumes before deletion: %w", err)
		}
	}
	if len(volumes) > 0 {
		printInfo("Removing %d volume(s)...", len(volumes))
		for _, v := range volumes {
			if rmErr := dockerClient.RemoveVolume(ctx, v); rmErr != nil {
				errors = append(errors, fmt.Sprintf("volume %s: %v", v, rmErr))
				printWarning("  Failed to remove volume %s: %v", v, rmErr)
			} else {
				printSuccess("  Removed volume %s", v)
			}
		}
	}

	// 4. Purge images only after the owned runtime resources were removed.
	if purge && len(errors) == 0 {
		removeStackKitImages(ctx)
	}

	deployLog.Event("remove.docker_cleanup",
		slog.Int("containers", len(containers)),
		slog.Int("networks", len(networks)),
		slog.Int("volumes", len(volumes)),
		slog.Bool("delete_data", deleteVolumes),
		slog.Int("failed_resources", len(errors)),
	)

	if len(errors) > 0 {
		return fmt.Errorf("%d resource(s) failed to remove", len(errors))
	}
	return nil
}

// removeStackKitImages removes pre-pulled StackKit images.
func removeStackKitImages(ctx context.Context) {
	images := baseKitImages("")
	dockerClient := newRemoveDockerClient()
	printInfo("Removing %d image(s)...", len(images))
	for _, img := range images {
		if rmErr := dockerClient.RemoveImage(ctx, img); rmErr != nil {
			printVerbose("  Could not remove image %s: %v", img, rmErr)
		} else {
			printSuccess("  Removed image %s", img)
		}
	}
}

// removeCoreMCPCredentials deletes the Core's MCP credentials once the runtime
// that served them is gone, so an uninstalled stack leaves no valid agent
// credential behind. Callers invoke it only after the runtime was removed.
func removeCoreMCPCredentials(wd string) {
	if err := localevidence.RemoveStackKitServerCredentials(wd); err != nil {
		printWarning("Failed to remove the StackKits MCP credentials: %v", err)
	}
}

// cleanupFiles removes StackKit file artifacts from the working directory.
func cleanupFiles(wd string, purge bool) {
	// Always remove .terraform provider cache (can be large, ~200MB)
	tfDir := filepath.Join(wd, config.GetDeployDir(), ".terraform")
	if _, err := os.Stat(tfDir); err == nil {
		if err := os.RemoveAll(tfDir); err != nil {
			printWarning("Failed to remove .terraform: %v", err)
		} else {
			printSuccess("Removed .terraform provider cache")
		}
	}

	if !purge {
		return
	}

	// Remove deploy/ directory entirely
	deployDir := filepath.Join(wd, config.GetDeployDir())
	if _, err := os.Stat(deployDir); err == nil {
		if err := os.RemoveAll(deployDir); err != nil {
			printWarning("Failed to remove deploy directory: %v", err)
		} else {
			printSuccess("Removed deploy/ directory")
		}
	}

	// Remove .stackkit/ state directory
	stateDir := filepath.Join(wd, ".stackkit")
	if _, err := os.Stat(stateDir); err == nil {
		if err := os.RemoveAll(stateDir); err != nil {
			printWarning("Failed to remove .stackkit directory: %v", err)
		} else {
			printSuccess("Removed .stackkit/ state directory")
		}
	}

	// Remove ~/.stackkits/capabilities.json
	home, err := os.UserHomeDir()
	if err == nil {
		capsFile := filepath.Join(home, ".stackkits", "capabilities.json")
		if _, statErr := os.Stat(capsFile); statErr == nil {
			if err := os.Remove(capsFile); err != nil {
				printWarning("Failed to remove capabilities.json: %v", err)
			} else {
				printSuccess("Removed capabilities.json")
			}
		}
	}
}
