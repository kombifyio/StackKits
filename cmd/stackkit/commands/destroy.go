package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/iac"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
)

var (
	removeAutoApprove bool
	removeForce       bool
	removePurge       bool
)

const removeConfirmationValue = "yes"

var removeCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove the deployed infrastructure",
	Long: `Remove all resources created by the deployment.

This command runs 'tofu destroy' to tear down all infrastructure
resources managed by the StackKit deployment. If OpenTofu is unavailable
or the state is corrupt, it falls back to Docker-level cleanup using labels.

Use --purge for a full factory reset (removes images, state, deploy dir).

WARNING: This will permanently delete all resources and data.

Examples:
  stackkit remove                 Remove with confirmation
  stackkit remove --auto-approve  Remove without confirmation
  stackkit remove --force         Force remove even with errors
  stackkit remove --purge         Full factory reset`,
	RunE: runRemove,
}

func init() {
	removeCmd.Flags().BoolVar(&removeAutoApprove, "auto-approve", false, "Skip interactive approval")
	removeCmd.Flags().BoolVar(&removeForce, "force", false, "Force remove even with errors")
	removeCmd.Flags().BoolVar(&removePurge, "purge", false, "Remove all StackKit data including images, state, and deploy directory")
}

func runRemove(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	wd := getWorkDir()

	// Load spec
	loader := config.NewLoader(wd)
	spec, err := loader.LoadStackSpec(specFile)
	if err != nil {
		printWarning("Could not load spec file, continuing with remove")
		spec = &models.StackSpec{StackKit: "unknown"}
	}

	printWarning("Removing deployment: %s", spec.StackKit)
	if removePurge {
		printWarning("Purge mode: will remove all StackKit data (images, state, deploy directory)")
	}

	// Confirmation if not auto-approved
	if !removeAutoApprove {
		fmt.Println()
		if removePurge {
			printError("WARNING: This will permanently remove all resources AND all StackKit data!")
		} else {
			printError("WARNING: This will permanently remove all resources!")
		}
		fmt.Printf("Type %q to confirm: ", removeConfirmationValue)

		var confirm string
		_, _ = fmt.Scanln(&confirm)
		if confirm != removeConfirmationValue {
			printInfo("Remove canceled")
			return nil
		}
	}

	startTime := time.Now()

	deployLog.Event("remove.start",
		slog.String("stackkit", spec.StackKit),
		slog.Bool("purge", removePurge),
		slog.Bool("force", removeForce),
		slog.Bool("auto_approve", removeAutoApprove),
	)

	// Phase 1: Try OpenTofu destroy
	tofuSucceeded := tryTofuDestroy(ctx, spec, wd)

	deployLog.Event("remove.tofu_destroy",
		slog.Bool("success", tofuSucceeded),
	)

	// Phase 2: Docker fallback (or purge image cleanup)
	if !tofuSucceeded {
		printInfo("Cleaning up Docker resources by label...")
		if err := dockerFallbackCleanup(ctx, removePurge); err != nil {
			printWarning("Docker cleanup encountered errors: %v", err)
			if !removeForce {
				return fmt.Errorf("cleanup failed: %w", err)
			}
		}
	} else if removePurge {
		// Tofu succeeded but purge requested — remove images
		printInfo("Removing Docker images...")
		removeStackKitImages(ctx)
	}

	// Phase 2b: Prune dangling images and build cache
	dockerClient := docker.NewClient()
	if dockerClient.IsInstalled() && dockerClient.IsRunning(ctx) {
		if reclaimed, err := dockerClient.Prune(ctx); err == nil && reclaimed > 1024*1024 {
			printSuccess("Reclaimed %d MB from dangling images/build cache", reclaimed/(1024*1024))
		}
	}

	// Phase 3: File cleanup
	cleanupFiles(wd, removePurge)

	deployLog.Event("remove.files_cleanup",
		slog.Bool("purge", removePurge),
	)

	// Phase 4: Update state (skip if purging — state dir is removed)
	if !removePurge {
		state := &models.DeploymentState{
			StackKit:    spec.StackKit,
			Mode:        spec.Mode,
			Status:      models.StatusRemoved,
			LastApplied: time.Now(),
		}

		stateFile := filepath.Join(wd, ".stackkit", "state.yaml")
		if mkErr := os.MkdirAll(filepath.Dir(stateFile), 0750); mkErr != nil {
			printWarning("Failed to create state directory: %v", mkErr)
		} else if saveErr := loader.SaveDeploymentState(state, stateFile); saveErr != nil {
			printWarning("Failed to save deployment state: %v", saveErr)
		}
	}

	duration := time.Since(startTime)

	deployLog.Event("remove.complete",
		slog.Duration("duration", duration),
	)

	fmt.Println()
	printSuccess("Remove complete! (took %s)", duration.Round(time.Second))
	if removePurge {
		printInfo("Factory reset complete. Run 'stackkit prepare' to start fresh.")
	}

	return nil
}

// tryTofuDestroy attempts OpenTofu destroy. Returns true if successful.
func tryTofuDestroy(ctx context.Context, spec *models.StackSpec, wd string) bool {
	deployDir := filepath.Join(wd, config.GetDeployDir())

	// Check deploy directory exists
	if _, statErr := os.Stat(deployDir); os.IsNotExist(statErr) {
		printWarning("Deploy directory not found — skipping OpenTofu destroy")
		deployLog.Warn("remove.tofu_skipped", slog.String("reason", "no_deploy_dir"))
		return false
	}

	// Check if .terraform state exists (tofu was initialized)
	tfStatePath := filepath.Join(deployDir, ".terraform")
	if _, statErr := os.Stat(tfStatePath); os.IsNotExist(statErr) {
		printWarning("No OpenTofu state found — skipping OpenTofu destroy")
		deployLog.Warn("remove.tofu_skipped", slog.String("reason", "no_state"))
		return false
	}

	// Create executor
	executor, err := iac.NewExecutorFromSpec(spec, deployDir)
	if err != nil {
		printWarning("Could not create executor: %v — falling back to Docker cleanup", err)
		deployLog.Warn("remove.tofu_skipped", slog.String("reason", "no_executor"), slog.String("error", err.Error()))
		return false
	}

	if !executor.IsInstalled() {
		printWarning("StackKit-packaged OpenTofu is not available — falling back to Docker cleanup")
		deployLog.Warn("remove.tofu_skipped", slog.String("reason", "not_installed"))
		return false
	}

	// Run destroy
	printInfo("Destroying infrastructure via OpenTofu...")
	result, err := executor.Destroy(ctx, true)
	if err != nil {
		printWarning("OpenTofu destroy error: %v — falling back to Docker cleanup", err)
		deployLog.Error("remove.tofu_result", slog.Bool("success", false), slog.String("error", err.Error()))
		return false
	}

	if result.Stdout != "" {
		fmt.Println()
		fmt.Println(result.Stdout)
	}

	if !result.Success {
		printWarning("OpenTofu destroy failed — falling back to Docker cleanup")
		stderr := result.Stderr
		if len(stderr) > 500 {
			stderr = stderr[:500]
		}
		if result.Stderr != "" {
			printVerbose("  %s", result.Stderr)
		}
		deployLog.Error("remove.tofu_result", slog.Bool("success", false), slog.String("stderr", stderr))
		return false
	}

	deployLog.Event("remove.tofu_result", slog.Bool("success", true))
	printSuccess("OpenTofu destroy completed")
	return true
}

// dockerFallbackCleanup removes StackKit resources directly via Docker CLI.
// Used when OpenTofu destroy fails or is unavailable.
func dockerFallbackCleanup(ctx context.Context, purge bool) error {
	dockerClient := docker.NewClient()
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

	// 3. Remove volumes
	volumes, err := dockerClient.ListVolumesByLabel(ctx, "stackkit.layer")
	if err != nil {
		printWarning("Could not list volumes: %v", err)
	} else if len(volumes) > 0 {
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

	// 4. Remove images (only with purge)
	if purge {
		removeStackKitImages(ctx)
	}

	deployLog.Event("remove.docker_cleanup",
		slog.Int("containers", len(containers)),
		slog.Int("networks", len(networks)),
		slog.Int("volumes", len(volumes)),
	)

	if len(errors) > 0 {
		return fmt.Errorf("%d resource(s) failed to remove", len(errors))
	}
	return nil
}

// removeStackKitImages removes pre-pulled StackKit images.
func removeStackKitImages(ctx context.Context) {
	images := baseKitImages("")
	dockerClient := docker.NewClient()
	printInfo("Removing %d image(s)...", len(images))
	for _, img := range images {
		if rmErr := dockerClient.RemoveImage(ctx, img); rmErr != nil {
			printVerbose("  Could not remove image %s: %v", img, rmErr)
		} else {
			printSuccess("  Removed image %s", img)
		}
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
