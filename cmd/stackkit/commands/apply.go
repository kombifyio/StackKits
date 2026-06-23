package commands

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/iac"
	"github.com/kombifyio/stackkits/internal/kombifyme"
	"github.com/kombifyio/stackkits/internal/netenv"
	"github.com/kombifyio/stackkits/internal/rollout"
	"github.com/kombifyio/stackkits/internal/tofu"
	stackverify "github.com/kombifyio/stackkits/internal/verify"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
)

var (
	applyAutoApprove       bool
	applyTenantDeployment  string
	applyReportingEndpoint string
	applyReportingToken    string
	applyVerify            bool
	applyVerifyHTTP        bool
	applyVerifyStrict      bool
	applySkipPlatformApps  bool
)

var applyCmd = &cobra.Command{
	Use:   "apply [plan-file]",
	Short: "Apply infrastructure changes",
	Long: `Apply the planned changes to the infrastructure.

This command runs StackKit-packaged OpenTofu to create, update, or
destroy infrastructure resources as needed.

Examples:
  stackkit apply                   Apply changes (with confirmation)
  stackkit apply --auto-approve    Apply without confirmation
  stackkit apply plan.tfplan       Apply a saved plan`,
	Args: cobra.MaximumNArgs(1),
	RunE: runApply,
}

func init() {
	applyCmd.Flags().BoolVar(&applyAutoApprove, "auto-approve", false, "Skip interactive approval")
	applyCmd.Flags().StringVar(&applyTenantDeployment, "tenant-deployment", "", "sk_tenant_deployment UUID: report success/failure back to Admin after apply")
	applyCmd.Flags().StringVar(&applyReportingEndpoint, "admin-endpoint", "", "Admin API base URL for tenant-deployment spec fetch and reporting (env: STACKKIT_ADMIN_ENDPOINT, STACKKIT_ADMIN_URL)")
	applyCmd.Flags().StringVar(&applyReportingToken, "admin-token", "", "Admin/bootstrap API token (env: STACKKIT_BOOTSTRAP_TOKEN, STACKKIT_ADMIN_TOKEN)")
	applyCmd.Flags().BoolVar(&applyVerify, "verify", false, "Run stackkit verify after a successful apply")
	applyCmd.Flags().BoolVar(&applyVerifyHTTP, "verify-http", false, "Include HTTP route checks in post-apply verification")
	applyCmd.Flags().BoolVar(&applyVerifyStrict, "verify-strict", false, "Treat post-apply verification warnings as failures")
	applyCmd.Flags().BoolVar(&applySkipPlatformApps, "skip-platform-apps", false, "Skip platform app lifecycle handoff and automatic app setup actions")
}

func runApply(cmd *cobra.Command, args []string) (retErr error) {
	ctx := context.Background()
	wd := getWorkDir()
	rolloutEvent("apply", "started", "apply started", nil)
	defer func() {
		if retErr != nil {
			rolloutFailure("apply", retErr)
			closeRolloutRecorder(rollout.Summary{
				Status:  "failed",
				Message: retErr.Error(),
			})
			return
		}
		rolloutEvent("apply", "succeeded", "apply succeeded", nil)
		closeRolloutRecorder(rollout.Summary{Status: "success"})
	}()

	// When linked to a tenant deployment, report 'failed' on any error exit.
	if applyTenantDeployment != "" {
		recordTenantDeploymentEvent(applyTenantDeployment, "apply", "started", "stackkit apply started", "")
		defer func() {
			if retErr != nil {
				failureClass := rollout.ClassifyFailure(retErr.Error())
				recordTenantDeploymentEvent(applyTenantDeployment, "apply", "failed", retErr.Error(), failureClass)
				recordTenantDeploymentEvent(applyTenantDeployment, "lifecycle_patch", "started", "reporting failed lifecycle state", failureClass)
				if reportErr := reportTenantDeploymentState(applyTenantDeployment, "failed", retErr.Error(),
					"stackkit apply failed"); reportErr != nil {
					recordTenantDeploymentEvent(applyTenantDeployment, "lifecycle_patch", "failed", reportErr.Error(), rollout.ClassifyFailure(reportErr.Error()))
					printWarning("Could not report failed state: %v", reportErr)
				} else {
					recordTenantDeploymentEvent(applyTenantDeployment, "lifecycle_patch", "succeeded", "failed lifecycle state reported", failureClass)
				}
			}
		}()
	}

	// Load spec — three-tier resolution:
	//   1. Local stack-spec.yaml (operator/dev workflow).
	//   2. If --tenant-deployment is set AND no local spec exists,
	//      fetch the composed spec from Admin over the bootstrap
	//      token. This is the "VM boots from a clean image" path.
	//   3. Fall back to createDefaultSpec (kits/modules tree found
	//      next to the binary).
	loader := config.NewLoader(wd)
	rolloutEvent("spec.load", "started", "loading stack spec", map[string]string{
		"spec_file": specFile,
	})
	spec, err := loader.LoadStackSpec(specFile)
	if err != nil {
		specPath := filepath.Join(wd, specFile)
		if _, statErr := os.Stat(specPath); os.IsNotExist(statErr) {
			if applyTenantDeployment != "" {
				rolloutEvent("spec.load", "failed", "local stack spec missing; fetching tenant spec", map[string]string{
					"tenant_deployment_id": applyTenantDeployment,
				})
				printInfo("No local %s — fetching composed spec from Admin for deployment %s",
					specFile, applyTenantDeployment)
				fetched, fetchErr := fetchTenantSpec(ctx, applyTenantDeployment, wd)
				if fetchErr != nil {
					rolloutFailure("tenant_spec_fetch", fetchErr)
					return fmt.Errorf("tenant-deployment spec fetch: %w", fetchErr)
				}
				// Re-load from disk so downstream code goes through
				// the usual validation + defaulting path in the loader.
				spec, err = loader.LoadStackSpec(specFile)
				if err != nil {
					// Defensive: the fetched spec didn't round-trip.
					// Use it directly and log the loader error.
					printWarning("Admin-fetched spec failed loader validation: %v (using raw spec)", err)
					spec = fetched
				}
			} else {
				rolloutEvent("spec.load", "failed", "local stack spec missing; creating default spec", nil)
				spec, err = createDefaultSpec(loader, wd)
				if err != nil {
					return fmt.Errorf("no spec file and auto-init failed: %w", err)
				}
			}
		} else {
			rolloutFailure("spec.load", err)
			return fmt.Errorf("failed to load spec: %w", err)
		}
	}
	if spec != nil {
		rolloutEvent("spec.load", "succeeded", "stack spec loaded", map[string]string{
			"stackkit": spec.StackKit,
			"mode":     spec.Mode,
			"domain":   spec.Domain,
		})
	}
	if err := requireManagedIdentityBootstrapHandoff(wd, spec); err != nil {
		return err
	}

	deployLog.Event("apply.start",
		slog.String("stackkit", spec.StackKit),
		slog.String("mode", spec.Mode),
		slog.Bool("auto_approve", applyAutoApprove),
	)

	printInfo("Applying deployment: %s (mode: %s)", spec.StackKit, spec.Mode)

	// Ensure prerequisites are installed (skip Docker for native runtime)
	if err := ensurePrerequisites(ctx, spec); err != nil {
		return err
	}
	if err := applyPublicBetaSecurityBaseline(ctx, wd, spec); err != nil {
		return err
	}

	// Determine deploy directory — auto-generate if missing or empty
	deployDir := filepath.Join(wd, config.GetDeployDir())
	needsGenerate := false
	if _, statErr := os.Stat(deployDir); os.IsNotExist(statErr) {
		needsGenerate = true
	} else if hasTF, _ := tofu.HasTerraformFiles(deployDir); !hasTF {
		needsGenerate = true
	}
	reason := ""
	if needsGenerate {
		if _, statErr := os.Stat(deployDir); os.IsNotExist(statErr) {
			reason = "deploy_dir_missing"
		} else {
			reason = "no_terraform_files"
		}
	}
	deployLog.Event("apply.auto_generate",
		slog.Bool("needs_generate", needsGenerate),
		slog.String("reason", reason),
	)

	if needsGenerate {
		printInfo("Deploy directory not found or empty, running generate...")
		genOutputDir = config.GetDeployDir()
		genForce = true
		if genErr := runGenerate(cmd, nil); genErr != nil {
			return fmt.Errorf("auto-generate failed: %w", genErr)
		}
		if reloaded, reloadErr := loader.LoadStackSpec(specFile); reloadErr == nil {
			spec = reloaded
		} else {
			printWarning("Could not reload generated stack spec: %v", reloadErr)
		}
	}

	// Get plan file if provided
	planFile := ""
	if len(args) > 0 {
		planFile = args[0]
	}

	// Create IaC executor from spec (supports OpenTofu and Terramate modes)
	executor, err := iac.NewExecutorFromSpec(spec, deployDir)
	if err != nil {
		return fmt.Errorf("failed to create executor: %w", err)
	}
	deployLog.Event("apply.executor",
		slog.String("mode", string(executor.Mode())),
	)

	// OpenTofu was already checked by ensurePrerequisites

	// Initialize if needed
	tfStatePath := filepath.Join(deployDir, ".terraform")
	if _, statErr := os.Stat(tfStatePath); os.IsNotExist(statErr) {
		printInfo("Initializing %s...", executor.Mode())
		rolloutEvent("tofu.init", "started", "initializing OpenTofu", map[string]string{
			"mode": string(executor.Mode()),
		})
		recordTenantDeploymentEvent(applyTenantDeployment, "tofu.init", "started", "initializing OpenTofu", "")
		if initErr := executor.Init(ctx); initErr != nil {
			deployLog.Error("tofu.init",
				slog.String("status", "failed"),
				slog.String("error", initErr.Error()),
			)
			rolloutFailure("tofu.init", initErr)
			recordTenantDeploymentEvent(applyTenantDeployment, "tofu.init", "failed", initErr.Error(), rollout.ClassifyFailure(initErr.Error()))
			return fmt.Errorf("init error: %w", initErr)
		}
		deployLog.Event("tofu.init",
			slog.String("status", "success"),
		)
		rolloutEvent("tofu.init", "succeeded", "OpenTofu initialized", nil)
		recordTenantDeploymentEvent(applyTenantDeployment, "tofu.init", "succeeded", "OpenTofu initialized", "")
		printSuccess("Initialized successfully")
	}

	// Run apply with troubleshooting retry wrapper
	printInfo("Applying changes...")
	startTime := time.Now()
	deployLog.Event("apply.attempt_start")
	rolloutEvent("tofu.apply", "started", "OpenTofu apply started", nil)
	recordTenantDeploymentEvent(applyTenantDeployment, "tofu.apply", "started", "OpenTofu apply started", "")

	result, err := troubleshootAndApply(ctx, executor, applyAutoApprove, planFile, deployDir)
	if err != nil {
		rolloutFailure("tofu.apply", err)
		recordTenantDeploymentEvent(applyTenantDeployment, "tofu.apply", "failed", err.Error(), rollout.ClassifyFailure(err.Error()))
		return fmt.Errorf("apply error: %w", err)
	}

	duration := time.Since(startTime)

	// Display output
	if result.Stdout != "" {
		fmt.Println()
		fmt.Println(result.Stdout)
	}

	if !result.Success {
		userMsg := formatApplyError(result.Stderr + "\n" + result.Stdout)
		deployLog.Error("apply.failed",
			slog.String("error", userMsg),
			slog.Duration("duration", duration),
		)
		rolloutEvent("tofu.apply", "failed", userMsg, map[string]string{
			"failure_class": rollout.ClassifyFailure(userMsg),
		})
		recordTenantDeploymentEvent(applyTenantDeployment, "tofu.apply", "failed", userMsg, rollout.ClassifyFailure(userMsg))
		printError("%s", userMsg)
		fmt.Println()
		printInfo("Troubleshooting tips:")
		fmt.Println("  1. Run 'stackkit prepare' to re-detect system capabilities")
		fmt.Println("  2. Run 'stackkit apply' to retry the deployment")
		fmt.Println()
		printWarning("To clean up a failed deployment:")
		fmt.Println("  stackkit remove               (remove deployed resources)")
		fmt.Println("  stackkit remove --purge       (full reset, remove everything)")
		return fmt.Errorf("deployment failed")
	}
	rolloutEvent("tofu.apply", "succeeded", "OpenTofu apply succeeded", map[string]string{
		"duration": duration.Round(time.Second).String(),
	})
	recordTenantDeploymentEvent(applyTenantDeployment, "tofu.apply", "succeeded", "OpenTofu apply succeeded", "")

	// Update deployment state
	stateFile := filepath.Join(wd, ".stackkit", "state.yaml")
	state := &models.DeploymentState{
		StackKit:    spec.StackKit,
		Mode:        spec.Mode,
		Status:      models.StatusRunning,
		LastApplied: time.Now(),
	}
	if stateErr := preserveExistingSetupRuns(loader, stateFile, state); stateErr != nil {
		printWarning("Could not load existing deployment state for setup-run preservation: %v", stateErr)
	}

	access, accessErr := buildAccessSummary(wd, spec)
	if accessErr != nil {
		printWarning("Could not build access summary: %v", accessErr)
		deployLog.Warn("access.summary",
			slog.String("status", "failed"),
			slog.String("error", accessErr.Error()),
		)
	} else {
		attachObservedSetupActions(access, state)
		state.Services = serviceStatesFromAccessSummary(access)
		if writeErr := writeAccessSummary(wd, access); writeErr != nil {
			printWarning("Could not write access summary: %v", writeErr)
		} else {
			deployLog.Event("access.summary",
				slog.String("status", "written"),
				slog.String("hub_url", access.HubURL),
			)
		}
	}

	if shouldSkipPlatformApps() {
		printInfo("Skipping platform app lifecycle and automatic app setup actions")
		rolloutEvent("platform_apps", "skipped", "platform app handoff skipped", nil)
		rolloutEvent("setup_actions", "skipped", "automatic setup actions skipped", nil)
		recordTenantDeploymentEvent(applyTenantDeployment, "platform_apps", "skipped", "platform app handoff skipped", "")
		recordTenantDeploymentEvent(applyTenantDeployment, "setup_actions", "skipped", "automatic setup actions skipped", "")
	} else {
		rolloutEvent("platform_apps", "started", "platform app handoff processing started", nil)
		recordTenantDeploymentEvent(applyTenantDeployment, "platform_apps", "started", "platform app handoff processing started", "")
		if err := runPlatformAppDeployments(ctx, deployDir, state); err != nil {
			rolloutFailure("platform_apps", err)
			recordTenantDeploymentEvent(applyTenantDeployment, "platform_apps", "failed", err.Error(), rollout.ClassifyFailure(err.Error()))
			return fmt.Errorf("platform app handoff processing: %w", err)
		}
		rolloutEvent("platform_apps", "succeeded", "platform app handoff processing succeeded", nil)
		recordTenantDeploymentEvent(applyTenantDeployment, "platform_apps", "succeeded", "platform app handoff processing succeeded", "")

		rolloutEvent("setup_actions", "started", "automatic setup action processing started", nil)
		recordTenantDeploymentEvent(applyTenantDeployment, "setup_actions", "started", "automatic setup action processing started", "")
		updatedState, setupActionErr := runAutomaticNodeSetupActions(ctx, access, state, loader, stateFile)
		if setupActionErr != nil {
			rolloutFailure("setup_actions", setupActionErr)
			recordTenantDeploymentEvent(applyTenantDeployment, "setup_actions", "failed", setupActionErr.Error(), rollout.ClassifyFailure(setupActionErr.Error()))
			return fmt.Errorf("automatic setup actions: %w", setupActionErr)
		}
		if updatedState != nil {
			state = updatedState
		}
		rolloutEvent("setup_actions", "succeeded", "automatic setup action processing succeeded", nil)
		recordTenantDeploymentEvent(applyTenantDeployment, "setup_actions", "succeeded", "automatic setup action processing succeeded", "")
	}

	if access != nil {
		attachObservedSetupActions(access, state)
		if writeErr := writeAccessSummary(wd, access); writeErr != nil {
			printWarning("Could not update access summary setup actions: %v", writeErr)
		}
	}

	deployLog.Event("apply.success",
		slog.Duration("duration", duration),
	)

	if mkdirErr := os.MkdirAll(filepath.Dir(stateFile), 0750); mkdirErr != nil {
		deployLog.Warn("state.saved",
			slog.String("status", "failed"),
			slog.String("error", mkdirErr.Error()),
		)
		printWarning("Failed to create state directory: %v", mkdirErr)
	} else if saveErr := loader.SaveDeploymentState(state, stateFile); saveErr != nil {
		deployLog.Warn("state.saved",
			slog.String("status", "failed"),
			slog.String("error", saveErr.Error()),
		)
		printWarning("Failed to save deployment state: %v", saveErr)
	} else {
		deployLog.Event("state.saved",
			slog.String("status", "success"),
		)
	}

	// Register with kombify for Direct Connect (only for kombify.me domains)
	registerWithKombify(spec, state)

	// Clean up dangling images and build cache left from deployment
	dockerClient := docker.NewClient()
	if dockerClient.IsInstalled() && dockerClient.IsRunning(ctx) {
		if reclaimed, pruneErr := dockerClient.Prune(ctx); pruneErr == nil {
			deployLog.Event("docker.prune",
				slog.Int64("reclaimed_bytes", int64(reclaimed)),
			)
			if reclaimed > 1024*1024 {
				printSuccess("Reclaimed %d MB of disk space", reclaimed/(1024*1024))
			}
		} else {
			deployLog.Warn("docker.prune",
				slog.String("error", pruneErr.Error()),
			)
		}
	}

	fmt.Println()
	printSuccess("Apply complete! (took %s)", duration.Round(time.Second))

	// Get and display outputs
	output, err := executor.Output(ctx)
	if err == nil && output != "" {
		printInfo("Deployment outputs:")
		fmt.Println(output)
	}

	// Post-deploy: verify service URLs are reachable
	verifyServiceURLs(ctx, spec, access)

	// Post-deploy: bootstrap PocketID owner + break-glass artifacts on the
	// firstnode (Phase 1, gated on --cluster-mode=first --owner-source=local).
	// No-op for existing deploys that don't opt into the new identity flow.
	if err := runOwnerBootstrap(cmd, spec); err != nil {
		return err
	}

	if applyVerify || applyVerifyHTTP || applyVerifyStrict {
		fmt.Println()
		printInfo("Running post-deployment verification...")
		rolloutEvent("verify", "started", "post-deployment verification started", nil)
		recordTenantDeploymentEvent(applyTenantDeployment, "verify", "started", "post-deployment verification started", "")
		report := buildLocalVerifyReport(ctx, wd, spec, state, stackverify.Options{
			HTTP:   applyVerifyHTTP,
			Strict: applyVerifyStrict,
		})
		if err := emitVerifyReport(cmd.OutOrStdout(), report, false); err != nil {
			rolloutFailure("verify", err)
			recordTenantDeploymentEvent(applyTenantDeployment, "verify", "failed", err.Error(), rollout.ClassifyFailure(err.Error()))
			return fmt.Errorf("post-deployment verify: %w", err)
		}
		rolloutEvent("verify", "succeeded", "post-deployment verification succeeded", nil)
		recordTenantDeploymentEvent(applyTenantDeployment, "verify", "succeeded", "post-deployment verification succeeded", "")
	}

	// Post-deploy: report tenant deployment state (if linked to a managed StackKit)
	if applyTenantDeployment != "" {
		recordTenantDeploymentEvent(applyTenantDeployment, "lifecycle_patch", "started", "reporting healthy lifecycle state", "")
		if err := reportTenantDeploymentState(applyTenantDeployment, "healthy", "",
			fmt.Sprintf("apply succeeded for stackkit=%s in %s", spec.StackKit, duration.Round(time.Second))); err != nil {
			recordTenantDeploymentEvent(applyTenantDeployment, "lifecycle_patch", "failed", err.Error(), rollout.ClassifyFailure(err.Error()))
			printWarning("Could not report deployment state to Admin: %v", err)
		} else {
			recordTenantDeploymentEvent(applyTenantDeployment, "lifecycle_patch", "succeeded", "healthy lifecycle state reported", "")
		}
	}

	return nil
}

func preserveExistingSetupRuns(loader *config.Loader, stateFile string, state *models.DeploymentState) error {
	if loader == nil || state == nil {
		return nil
	}
	existingState, err := loader.LoadDeploymentState(stateFile)
	if err != nil {
		return err
	}
	if existingState == nil || len(existingState.SetupRuns) == 0 {
		return nil
	}
	state.SetupRuns = append([]models.SetupRunState(nil), existingState.SetupRuns...)
	return nil
}

func shouldSkipPlatformApps() bool {
	if applySkipPlatformApps {
		return true
	}
	for _, key := range []string{"STACKKIT_SKIP_PLATFORM_APPS", "STACKKIT_NO_APP_ORCHESTRATION"} {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
		case "1", "true", "yes", "y", "on", "enabled":
			return true
		}
	}
	return false
}

// reportTenantDeploymentState PATCHes sk_tenant_deployment with a lifecycle
// transition. Used by `apply --tenant-deployment=<uuid>` to bridge the
// filesystem-apply flow back into the managed-tenant lifecycle.
func reportTenantDeploymentState(deploymentID, state, lastError, message string) error {
	endpoint := strings.TrimRight(applyReportingEndpoint, "/")
	if endpoint == "" {
		// Prefer the harmonized STACKKIT_ADMIN_ENDPOINT (also used by
		// internal/registry.AutoClient); fall back to the legacy
		// STACKKIT_ADMIN_URL for compatibility with older admin-jobs
		// deployments that have not yet been updated.
		endpoint = strings.TrimRight(os.Getenv("STACKKIT_ADMIN_ENDPOINT"), "/")
	}
	if endpoint == "" {
		endpoint = strings.TrimRight(os.Getenv("STACKKIT_ADMIN_URL"), "/")
	}
	if endpoint == "" {
		return fmt.Errorf("no --admin-endpoint, STACKKIT_ADMIN_ENDPOINT, or STACKKIT_ADMIN_URL configured")
	}
	token := resolveTenantDeploymentToken()
	if token == "" {
		return fmt.Errorf("no STACKKIT_BOOTSTRAP_TOKEN, --admin-token, or STACKKIT_ADMIN_TOKEN configured")
	}

	payload := map[string]interface{}{
		"lifecycleState": state,
		"message":        message,
		"actor":          "stackkit-cli",
	}
	if lastError != "" {
		payload["lastError"] = lastError
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/sk/tenants/deployments/%s", endpoint, deploymentID)
	req, err := http.NewRequest(http.MethodPatch, url, strings.NewReader(string(body))) // #nosec G107 G704 -- endpoint is an operator-supplied CLI flag.
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req) // #nosec G107 G704 -- request URL is operator-supplied CLI endpoint.
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("admin returned %d", resp.StatusCode)
	}
	printSuccess("Reported deployment %s -> %s", deploymentID, state)
	return nil
}

// createDefaultSpec finds a StackKit and copies its default-spec.yaml
// into the working directory as stack-spec.yaml.
func createDefaultSpec(loader *config.Loader, wd string) (*models.StackSpec, error) {
	kits, err := discoverStackKits(loader, wd)
	if err != nil || len(kits) == 0 {
		return nil, fmt.Errorf("no StackKits found — run 'stackkit init <kit>' first")
	}

	// Prefer base-kit, fall back to single kit, otherwise ask
	var kitName string
	for _, k := range kits {
		if k.Metadata.Name == "base-kit" {
			kitName = k.Metadata.Name
			break
		}
	}
	if kitName == "" && len(kits) == 1 {
		kitName = kits[0].Metadata.Name
	}
	if kitName == "" {
		p := newPrompter()
		var choices []choice
		for _, sk := range kits {
			choices = append(choices, choice{
				Key:     sk.Metadata.Name,
				Display: sk.Metadata.DisplayName,
			})
		}
		choices[0].IsDefault = true
		kitName, err = p.selectOne("Multiple StackKits found. Select one:", choices)
		if err != nil {
			return nil, err
		}
	}

	printInfo("No spec file found, using defaults from %s", bold(kitName))

	// Find kit directory
	kitDir, err := loader.FindStackKitDir(kitName)
	if err != nil {
		parentLoader := config.NewLoader(filepath.Dir(wd))
		kitDir, err = parentLoader.FindStackKitDir(kitName)
		if err != nil {
			return nil, fmt.Errorf("could not find %s: %w", kitName, err)
		}
	}

	// Copy default-spec.yaml to stack-spec.yaml
	defaultSpecPath := filepath.Join(kitDir, "default-spec.yaml")
	data, err := os.ReadFile(defaultSpecPath)
	if err != nil {
		return nil, fmt.Errorf("no default-spec.yaml in %s: %w", kitName, err)
	}

	specPath := filepath.Join(wd, specFile)
	if err := os.WriteFile(specPath, data, 0600); err != nil { // #nosec G304 G703 -- specFile is a fixed CLI artifact name, wd is the operator's working directory.
		return nil, fmt.Errorf("failed to write %s: %w", specFile, err)
	}
	printSuccess("Created %s from %s defaults", specFile, kitName)

	return loader.LoadStackSpec(specFile)
}

// ensurePrerequisites checks that Docker is available and that the StackKit
// release package includes its OpenTofu binary. Skips Docker for native runtime.
func ensurePrerequisites(ctx context.Context, spec *models.StackSpec) error {
	isNative := spec != nil && spec.Runtime == models.RuntimeNative

	// Check Docker (skip for native runtime)
	if isNative {
		printInfo("Native runtime — skipping Docker checks")
	} else {
		if err := ensureDocker(ctx); err != nil {
			return err
		}
	}

	// Check packaged OpenTofu. Do not prompt the user to install OpenTofu
	// manually; it is a StackKit package component.
	packagedTofu, ok := tofu.PackagedBinaryPath()
	if !ok {
		return fmt.Errorf("StackKit-packaged OpenTofu binary not found; reinstall StackKit from the official release package")
	}
	tofuExec := tofu.NewExecutor(tofu.WithBinary(packagedTofu))
	if !tofuExec.IsInstalled() {
		return fmt.Errorf("StackKit-packaged OpenTofu binary is not executable: %s", packagedTofu)
	}
	printSuccess("StackKit-packaged OpenTofu available")

	if spec != nil && spec.UsesAdvancedIAC() {
		if _, err := exec.LookPath("terramate"); err != nil {
			return fmt.Errorf("StackKit-packaged Terramate binary not found on PATH; reinstall StackKit from the official release package")
		}
		printSuccess("StackKit-packaged Terramate available")
	}

	return nil
}

// ensureDocker checks Docker is installed and running, installing if needed.
func ensureDocker(ctx context.Context) error {
	dockerClient := docker.NewClient()
	if !dockerClient.IsInstalled() {
		if applyAutoApprove {
			printInfo("Docker is not installed, installing...")
		} else {
			printWarning("Docker is not installed")
			fmt.Print("Install Docker now? [Y/n] ")
			var answer string
			_, _ = fmt.Scanln(&answer)
			if len(answer) > 0 && (answer[0] == 'n' || answer[0] == 'N') {
				return fmt.Errorf("docker is required; allow StackKit to prepare it or run 'stackkit prepare'")
			}
			printInfo("Installing Docker...")
		}
		if err := installDockerLocal(ctx); err != nil {
			return fmt.Errorf("failed to install Docker: %w", err)
		}
		printSuccess("Docker installed")
	}

	if !dockerClient.IsRunning(ctx) {
		printInfo("Docker daemon is not running, starting...")
		caps, err := startDockerDaemon(ctx)
		if err != nil {
			return fmt.Errorf("failed to start Docker daemon: %w", err)
		}
		writeDockerCapabilities(caps)
		printSuccess("Docker daemon started")
	}

	return nil
}

// =============================================================================
// TROUBLESHOOTING ENGINE
// =============================================================================

// applyFailurePattern represents a known failure pattern and its automated fix.
type applyFailurePattern struct {
	Name        string
	Match       func(stderr string) bool
	Fix         func(ctx context.Context, deployDir string) error
	UserMessage string
}

// knownFailurePatterns returns the ordered list of failure patterns to check.
func knownFailurePatterns() []applyFailurePattern {
	return []applyFailurePattern{
		{
			Name: "docker-image-pull",
			Match: func(stderr string) bool {
				return (strings.Contains(stderr, "unable to find") ||
					strings.Contains(stderr, "unable to pull") ||
					strings.Contains(stderr, "Error pulling image") ||
					strings.Contains(stderr, "i/o timeout")) &&
					strings.Contains(stderr, "docker_image")
			},
			Fix: func(ctx context.Context, deployDir string) error {
				printInfo("Pre-pulling images from host network...")
				caps := loadDockerCapabilities()
				if caps == nil {
					caps = &models.DockerCapabilities{}
				}
				prePullImages(ctx, caps, "")
				writeDockerCapabilities(caps)
				if len(caps.PrePullFailed) > 0 {
					return fmt.Errorf("%d images failed to pull", len(caps.PrePullFailed))
				}
				return nil
			},
			UserMessage: "Docker image pulls failed (DNS or network issue). Pulling images from host network...",
		},
		{
			Name: "docker-network",
			Match: func(stderr string) bool {
				return strings.Contains(stderr, "docker_network") &&
					(strings.Contains(stderr, "operation not permitted") ||
						strings.Contains(stderr, "Unable to create network"))
			},
			Fix: func(ctx context.Context, deployDir string) error {
				printInfo("Switching to host networking mode...")
				if err := patchTfvarsNetworkMode(deployDir, "host"); err != nil {
					return err
				}
				// Re-init to pick up the tfvars change
				tofuExec := tofu.NewExecutor()
				tofuExec.SetWorkDir(deployDir)
				_, err := tofuExec.Init(ctx)
				return err
			},
			UserMessage: "Bridge networking blocked (restricted VPS). Switching to host networking mode...",
		},
		{
			Name: "docker-daemon",
			Match: func(stderr string) bool {
				return strings.Contains(stderr, "Cannot connect to the Docker daemon") ||
					strings.Contains(stderr, "docker.sock") ||
					strings.Contains(stderr, "connection refused")
			},
			Fix: func(ctx context.Context, _ string) error {
				printInfo("Restarting Docker daemon...")
				caps, err := startDockerDaemon(ctx)
				if err != nil {
					return err
				}
				writeDockerCapabilities(caps)
				return nil
			},
			UserMessage: "Docker daemon lost connection. Restarting Docker...",
		},
		{
			Name: "state-lock",
			Match: func(stderr string) bool {
				return strings.Contains(stderr, "Error acquiring the state lock") ||
					strings.Contains(stderr, "state is locked")
			},
			Fix: func(_ context.Context, _ string) error {
				printInfo("Waiting for state lock to release...")
				time.Sleep(5 * time.Second)
				return nil
			},
			UserMessage: "Infrastructure state is locked. Waiting...",
		},
	}
}

// troubleshootAndApply wraps executor.Apply with detect-fix-retry logic.
// Follows the same pattern as startDockerDaemon() in prepare.go:
// Attempt → Detect failure pattern → Apply fix → Retry → Escalate.
func troubleshootAndApply(
	ctx context.Context,
	executor iac.Executor,
	autoApprove bool,
	planFile string,
	deployDir string,
) (*iac.ExecResult, error) {
	const maxRetries = 2
	patterns := knownFailurePatterns()

	var lastResult *iac.ExecResult
	var appliedFixes []string

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			fmt.Println()
			printInfo("Retry attempt %d/%d...", attempt, maxRetries)
		}

		result, err := executor.Apply(ctx, autoApprove, planFile)
		if err != nil {
			return nil, fmt.Errorf("apply error: %w", err)
		}

		lastResult = result
		deployLog.Event("tofu.apply",
			slog.Int("attempt", attempt),
			slog.Bool("success", result.Success),
		)

		if result.Success {
			if attempt > 0 {
				printSuccess("Apply succeeded after troubleshooting (%d fix(es) applied)", len(appliedFixes))
			}
			return result, nil
		}

		combinedOutput := result.Stderr + "\n" + result.Stdout

		// Apply failed — try to match a known pattern and fix
		if attempt >= maxRetries {
			break
		}

		fixed := false
		for _, pattern := range patterns {
			if !pattern.Match(combinedOutput) {
				continue
			}

			deployLog.Event("troubleshoot.pattern_matched",
				slog.String("pattern", pattern.Name),
			)

			// Don't apply the same fix twice
			alreadyApplied := false
			for _, name := range appliedFixes {
				if name == pattern.Name {
					alreadyApplied = true
					break
				}
			}
			if alreadyApplied {
				deployLog.Warn("troubleshoot.skip_duplicate",
					slog.String("pattern", pattern.Name),
				)
				continue
			}

			fmt.Println()
			printWarning("%s", pattern.UserMessage)

			if fixErr := pattern.Fix(ctx, deployDir); fixErr != nil {
				deployLog.Error("troubleshoot.fix_applied",
					slog.String("pattern", pattern.Name),
					slog.Bool("success", false),
					slog.String("error", fixErr.Error()),
				)
				printWarning("Auto-fix failed: %v", fixErr)
				continue
			}

			deployLog.Event("troubleshoot.fix_applied",
				slog.String("pattern", pattern.Name),
				slog.Bool("success", true),
			)
			appliedFixes = append(appliedFixes, pattern.Name)
			fixed = true
			break
		}

		if !fixed {
			deployLog.Warn("troubleshoot.no_match")
			break // no pattern matched — don't retry blindly
		}
	}

	return lastResult, nil
}

// formatApplyError translates raw Terraform/OpenTofu stderr into a
// user-friendly error message.
func formatApplyError(stderr string) string {
	translations := []struct {
		pattern string
		message string
	}{
		{"unauthenticated pull rate limit", "Docker Hub rate limit reached. Authenticate Docker on the target or configure a registry mirror, then retry."},
		{"toomanyrequests", "Container registry rate limit reached. Authenticate Docker on the target or configure a registry mirror, then retry."},
		{"Reference to undeclared resource", "Generated OpenTofu configuration references a missing resource. Regenerate with the current StackKit bundle and retry."},
		{"unable to find", "Could not download one or more container images. Check your internet connection."},
		{"unable to pull", "Could not download one or more container images. Check your internet connection."},
		{"Error pulling image", "Failed to download a container image (DNS or network issue)."},
		{"Unable to create network", "Could not create Docker network. Your VPS may not support bridge networking."},
		{"Cannot connect to the Docker daemon", "Docker is not running. Run 'stackkit prepare' to start it."},
		{"Error acquiring the state lock", "Another deployment operation is in progress. Wait and try again."},
		{"context deadline exceeded", "Operation timed out. Check if your server has adequate resources."},
	}

	for _, t := range translations {
		if strings.Contains(stderr, t.pattern) {
			return t.message
		}
	}

	// Extract first "Error:" line as fallback
	for _, line := range strings.Split(stderr, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Error") || strings.HasPrefix(trimmed, "│ Error") {
			cleaned := strings.TrimPrefix(trimmed, "│ ")
			return "Deployment error: " + cleaned
		}
	}

	return "Deployment failed. Run 'stackkit prepare' then retry with 'stackkit apply'."
}

// registerWithKombify registers the stackkit-server instance with kombify for Direct Connect.
// Only runs when the deployment uses a kombify.me domain.
func registerWithKombify(spec *models.StackSpec, state *models.DeploymentState) {
	if spec == nil || spec.Domain != models.DomainKombifyMe {
		return
	}

	apiKey := os.Getenv("KOMBIFY_API_KEY")
	if apiKey == "" {
		deployLog.Warn("registry.skip", slog.String("reason", "no KOMBIFY_API_KEY"))
		return
	}

	fingerprint := kombifyme.DeviceFingerprint()
	instanceID := fmt.Sprintf("%s-%s-%s", spec.SubdomainPrefix, spec.StackKit, fingerprint)

	// Build service list from deployment state
	var services []models.ServiceInfo
	for _, svc := range state.Services {
		services = append(services, models.ServiceInfo{
			Name:   svc.Name,
			URL:    svc.URL,
			Status: string(svc.Status),
		})
	}

	reg := &models.InstanceRegistration{
		InstanceID:  instanceID,
		EndpointURL: fmt.Sprintf("https://%s-api.kombify.me", spec.SubdomainPrefix),
		StackKit:    spec.StackKit,
		Services:    services,
		Status:      string(state.Status),
		APIPort:     8082,
	}

	client := kombifyme.NewClient(apiKey)
	resp, err := client.RegisterInstance(reg)
	if err != nil {
		deployLog.Warn("registry.register",
			slog.String("status", "failed"),
			slog.String("error", err.Error()),
		)
		printWarning("Failed to register with kombify: %v", err)
		return
	}

	deployLog.Event("registry.register",
		slog.String("status", "success"),
		slog.String("instance_id", resp.InstanceID),
	)
	printSuccess("Registered with kombify (instance: %s)", resp.InstanceID)
}

// patchTfvarsNetworkMode updates terraform.tfvars.json to change network_mode.
func patchTfvarsNetworkMode(deployDir, mode string) error {
	tfvarsPath := filepath.Join(deployDir, "terraform.tfvars.json")
	data, err := os.ReadFile(tfvarsPath)
	if err != nil {
		return fmt.Errorf("could not read tfvars: %w", err)
	}

	var vars map[string]interface{}
	if err := json.Unmarshal(data, &vars); err != nil {
		return fmt.Errorf("could not parse tfvars: %w", err)
	}

	vars["network_mode"] = mode

	newData, err := json.MarshalIndent(vars, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(tfvarsPath, append(newData, '\n'), 0600)
}

// verifyServiceURLs checks if the key service URLs are actually reachable
// after deployment. This catches mismatches between the configured domain
// and the actual network environment (e.g., local domains on a public VPS).
func verifyServiceURLs(ctx context.Context, spec *models.StackSpec, access *accessSummary) {
	if spec == nil {
		return
	}

	probes := serviceURLProbeTargets(spec, access)
	if len(probes) == 0 {
		return
	}
	domain := spec.Domain
	if access != nil && access.Domain != "" {
		domain = access.Domain
	}

	reachable := 0
	var firstFailure serviceURLProbe
	var firstDNSOK bool
	var firstFailureReason string
	for _, probe := range probes {
		dnsOK, httpOK, reason := checkServiceURLReachable(ctx, domain, probe)
		if dnsOK && httpOK {
			reachable++
			continue
		}
		if firstFailure.URL == "" {
			firstFailure = probe
			firstDNSOK = dnsOK
			firstFailureReason = reason
		}
	}

	if reachable == len(probes) {
		printSuccess("Service URLs verified: %d URL(s) reachable", reachable)
		return
	}

	// URLs are not reachable — provide actionable guidance
	fmt.Println()
	if len(probes) == 1 {
		printWarning("Service URL check: %s is not reachable: %s", firstFailure.URL, firstFailureReason)
	} else {
		printWarning("Service URL check: %s is not reachable: %s (%d/%d reachable)", firstFailure.URL, firstFailureReason, reachable, len(probes))
	}

	caps := loadDockerCapabilities()
	if caps != nil && netenv.NodeContextIsCloud(caps.ResolvedContext) {
		// On a cloud/VPS context with local domains — this is the root cause
		if models.IsLocalDomain(domain) {
			printError("Local domain '%s' is not accessible on a public server", domain)
			fmt.Println()
			printInfo("Your server is a VPS/cloud instance but is configured with a local domain.")
			printInfo("Browser-native *.localhost domains work only on the machine opening the link. LAN domains (*.home, *.<name>.home, *.internal, *.local, *.lab, *.lan) require a StackKit-managed or verified resolver path.")
			fmt.Println()
			printInfo("To fix this, update your stack-spec.yaml domain to one of:")
			fmt.Println("  1. domain: kombify.me    (free public subdomains via kombify.me)")
			fmt.Println("  2. domain: yourdomain.com  (your own domain with DNS configured)")
			fmt.Println()
			printInfo("Then re-deploy:")
			fmt.Println("  stackkit generate --force")
			fmt.Println("  stackkit apply --auto-approve")
		}
	} else if !firstDNSOK {
		printInfo("DNS resolution failed for '%s'", firstFailure.Host)
		if models.IsLocalhostDomain(domain) {
			printInfo("The .localhost mode is device-local. Open the URL on the machine that reaches this Docker host, or use --local-dns for LAN-wide names.")
		} else if caps != nil && caps.PrivateIP != "" {
			printInfo("For LAN DNS, point router DHCP DNS to:")
			fmt.Printf("  %s\n", caps.PrivateIP)
		}
	}
}

type serviceURLProbe struct {
	Host string
	URL  string
}

func serviceURLProbeTargets(spec *models.StackSpec, access *accessSummary) []serviceURLProbe {
	probes := []serviceURLProbe{}
	seen := map[string]bool{}
	add := func(host, rawURL string) {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" || seen[rawURL] {
			return
		}
		host = normalizedProbeHost(host, rawURL)
		if host == "" {
			return
		}
		seen[rawURL] = true
		probes = append(probes, serviceURLProbe{Host: host, URL: rawURL})
	}

	host, rawURL := primaryServiceProbeTarget(spec, access)
	add(host, rawURL)
	if access != nil {
		add("", access.HubURL)
		for _, svc := range access.Services {
			add(svc.Host, svc.URL)
		}
	}
	return probes
}

func normalizedProbeHost(host, rawURL string) string {
	host = strings.TrimSpace(host)
	if host != "" && !strings.Contains(host, ":") {
		return host
	}
	if host != "" {
		if splitHost, _, err := net.SplitHostPort(host); err == nil {
			return splitHost
		}
	}
	if parsed, err := url.Parse(rawURL); err == nil {
		return parsed.Hostname()
	}
	return host
}

func checkServiceURLReachable(ctx context.Context, domain string, probe serviceURLProbe) (bool, bool, string) {
	if _, err := net.LookupHost(probe.Host); err != nil {
		return false, false, err.Error()
	}

	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, probe.URL, nil)
	if err != nil {
		return true, false, err.Error()
	}
	client := &http.Client{Timeout: 5 * time.Second}
	if strings.HasPrefix(probe.URL, "https://") && models.IsLocalDomain(domain) {
		// #nosec G402 -- local Step-CA HTTPS uses a StackKit-managed trust root
		// that is not installed in the system store during apply.
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec
	}
	resp, err := client.Do(req)
	if err != nil {
		return true, false, err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return true, false, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return true, true, ""
}

func primaryServiceProbeTarget(spec *models.StackSpec, access *accessSummary) (string, string) {
	if access != nil && strings.TrimSpace(access.HubURL) != "" {
		if parsed, err := url.Parse(access.HubURL); err == nil && parsed.Hostname() != "" {
			return parsed.Hostname(), access.HubURL
		}
	}

	domain := models.DomainHomeLab
	prefix := ""
	if spec != nil {
		if spec.Domain != "" {
			domain = spec.Domain
		}
		prefix = spec.SubdomainPrefix
	}

	proto := "http"
	if !models.IsLocalhostDomain(domain) {
		proto = "https"
	}

	host := "base." + domain
	if prefix != "" {
		host = prefix + "-base." + domain
	}
	return host, proto + "://" + host
}
