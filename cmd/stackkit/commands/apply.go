package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/iac"
	"github.com/kombifyio/stackkits/internal/kombifyme"
	"github.com/kombifyio/stackkits/internal/netenv"
	"github.com/kombifyio/stackkits/internal/tofu"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
)

var applyAutoApprove bool

var applyCmd = &cobra.Command{
	Use:   "apply [plan-file]",
	Short: "Apply infrastructure changes",
	Long: `Apply the planned changes to the infrastructure.

This command runs 'tofu apply' to create, update, or destroy
infrastructure resources as needed.

Examples:
  stackkit apply                   Apply changes (with confirmation)
  stackkit apply --auto-approve    Apply without confirmation
  stackkit apply plan.tfplan       Apply a saved plan`,
	Args: cobra.MaximumNArgs(1),
	RunE: runApply,
}

func init() {
	applyCmd.Flags().BoolVar(&applyAutoApprove, "auto-approve", false, "Skip interactive approval")
}

func runApply(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	wd := getWorkDir()

	// Load spec — create from kit defaults if missing
	loader := config.NewLoader(wd)
	spec, err := loader.LoadStackSpec(specFile)
	if err != nil {
		specPath := filepath.Join(wd, specFile)
		if _, statErr := os.Stat(specPath); os.IsNotExist(statErr) {
			spec, err = createDefaultSpec(loader, wd)
			if err != nil {
				return fmt.Errorf("no spec file and auto-init failed: %w", err)
			}
		} else {
			return fmt.Errorf("failed to load spec: %w", err)
		}
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
		if initErr := executor.Init(ctx); initErr != nil {
			deployLog.Error("tofu.init",
				slog.String("status", "failed"),
				slog.String("error", initErr.Error()),
			)
			return fmt.Errorf("init error: %w", initErr)
		}
		deployLog.Event("tofu.init",
			slog.String("status", "success"),
		)
		printSuccess("Initialized successfully")
	}

	// Run apply with troubleshooting retry wrapper
	printInfo("Applying changes...")
	startTime := time.Now()
	deployLog.Event("apply.attempt_start")

	result, err := troubleshootAndApply(ctx, executor, applyAutoApprove, planFile, deployDir)
	if err != nil {
		return fmt.Errorf("apply error: %w", err)
	}

	duration := time.Since(startTime)

	// Display output
	if result.Stdout != "" {
		fmt.Println()
		fmt.Println(result.Stdout)
	}

	if !result.Success {
		userMsg := formatApplyError(result.Stderr)
		deployLog.Error("apply.failed",
			slog.String("error", userMsg),
			slog.Duration("duration", duration),
		)
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

	// Update deployment state
	state := &models.DeploymentState{
		StackKit:    spec.StackKit,
		Mode:        spec.Mode,
		Status:      models.StatusRunning,
		LastApplied: time.Now(),
	}

	deployLog.Event("apply.success",
		slog.Duration("duration", duration),
	)

	stateFile := filepath.Join(wd, ".stackkit", "state.yaml")
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
	verifyServiceURLs(ctx, spec)

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
	if err := os.WriteFile(specPath, data, 0600); err != nil {
		return nil, fmt.Errorf("failed to write %s: %w", specFile, err)
	}
	printSuccess("Created %s from %s defaults", specFile, kitName)

	return loader.LoadStackSpec(specFile)
}

// ensurePrerequisites checks that Docker and OpenTofu are available,
// offering to install them if missing. Skips Docker for native runtime.
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

	// Check OpenTofu
	tofuExec := tofu.NewExecutor()
	if !tofuExec.IsInstalled() {
		if applyAutoApprove {
			printInfo("OpenTofu is not installed, installing...")
		} else {
			printWarning("OpenTofu is not installed")
			fmt.Print("Install OpenTofu now? [Y/n] ")
			var answer string
			_, _ = fmt.Scanln(&answer)
			if len(answer) > 0 && (answer[0] == 'n' || answer[0] == 'N') {
				return fmt.Errorf("OpenTofu is required. Install it manually or run 'stackkit prepare'")
			}
			printInfo("Installing OpenTofu...")
		}
		if err := installTofuLocal(ctx); err != nil {
			return fmt.Errorf("failed to install OpenTofu: %w", err)
		}
		tofuExec = tofu.NewExecutor()
		if !tofuExec.IsInstalled() {
			return fmt.Errorf("OpenTofu installation completed but binary not found in PATH")
		}
		printSuccess("OpenTofu installed")
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
				return fmt.Errorf("Docker is required. Install it manually or run 'stackkit prepare'")
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

		// Apply failed — try to match a known pattern and fix
		if attempt >= maxRetries {
			break
		}

		fixed := false
		for _, pattern := range patterns {
			if !pattern.Match(result.Stderr) {
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
		EndpointURL: fmt.Sprintf("https://api.%s.kombify.me", spec.SubdomainPrefix),
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
func verifyServiceURLs(ctx context.Context, spec *models.StackSpec) {
	if spec == nil {
		return
	}

	domain := spec.Domain
	if domain == "" {
		domain = models.DomainHomeLab
	}

	// Build the primary test URL (dashboard)
	proto := "http"
	testHost := "base." + domain
	if spec.SubdomainPrefix != "" {
		testHost = spec.SubdomainPrefix + "-dash." + domain
	}
	testURL := proto + "://" + testHost

	// Try to resolve the hostname
	_, err := net.LookupHost(testHost)
	dnsOK := err == nil

	// Try to reach the service
	httpOK := false
	if dnsOK {
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		req, reqErr := http.NewRequestWithContext(checkCtx, http.MethodGet, testURL, nil)
		if reqErr == nil {
			client := &http.Client{Timeout: 5 * time.Second}
			resp, httpErr := client.Do(req)
			if httpErr == nil {
				resp.Body.Close()
				httpOK = resp.StatusCode < 500
			}
		}
	}

	if dnsOK && httpOK {
		printSuccess("Service URLs verified: %s is reachable", testURL)
		return
	}

	// URLs are not reachable — provide actionable guidance
	fmt.Println()
	printWarning("Service URL check: %s is not reachable", testURL)

	caps := loadDockerCapabilities()
	if caps != nil && netenv.NodeContextIsCloud(caps.ResolvedContext) {
		// On a cloud/VPS context with local domains — this is the root cause
		if models.IsLocalDomain(domain) {
			printError("Local domain '%s' is not accessible on a public server", domain)
			fmt.Println()
			printInfo("Your server is a VPS/cloud instance but is configured with a local domain.")
			printInfo("Local domains (*.local, *.lab, *.lan, *.home, *.homebase) only work on home networks with dnsmasq.")
			fmt.Println()
			printInfo("To fix this, update your stack-spec.yaml domain to one of:")
			fmt.Println("  1. domain: kombify.me    (free public subdomains via kombify.me)")
			fmt.Println("  2. domain: yourdomain.com  (your own domain with DNS configured)")
			fmt.Println()
			printInfo("Then re-deploy:")
			fmt.Println("  stackkit generate --force")
			fmt.Println("  stackkit apply --auto-approve")
		}
	} else if !dnsOK {
		printInfo("DNS resolution failed for '%s'", testHost)
		if caps != nil && caps.PrivateIP != "" {
			printInfo("Add to /etc/hosts on your workstation:")
			fmt.Printf("  %s  %s\n", caps.PrivateIP, testHost)
		}
	}
}
