package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/netenv"
	"github.com/kombifyio/stackkits/internal/rollout"
	"github.com/kombifyio/stackkits/internal/ssh"
	"github.com/kombifyio/stackkits/internal/terramate"
	"github.com/kombifyio/stackkits/internal/tofu"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
)

var (
	prepareHost           string
	prepareUser           string
	prepareKey            string
	preparePort           int
	prepareDryRun         bool
	prepareSkipDocker     bool
	prepareSkipTofu       bool
	prepareAutoFix        bool
	prepareForce          bool
	prepareNonInteractive bool
)

const prePullImagesDisabledFalseValue = "false"

var prepareCmd = &cobra.Command{
	Use:     "prepare",
	Aliases: []string{"prep"},
	Short:   "Prepare a system for StackKit deployment",
	Long: `Prepare a bare system for StackKit deployment AND validate/adjust the spec file.

This command:
  1. Checks/installs Docker
  2. Checks StackKit-packaged OpenTofu
  3. Validates the spec file against CUE schemas
  4. Checks hardware requirements
  5. Applies auto-fixes for common issues

Examples:
  stackkit prepare                      Prepare local system
  stackkit prepare --spec ./spec.yaml   Prepare and validate spec
  stackkit prepare --host 192.168.1.100 Prepare remote system
  stackkit prepare --dry-run            Show what would be done`,
	RunE: runPrepare,
}

func init() {
	prepareCmd.Flags().StringVar(&prepareHost, "host", "localhost", "Target host IP/hostname")
	prepareCmd.Flags().StringVar(&prepareUser, "user", "", "SSH username")
	prepareCmd.Flags().StringVar(&prepareKey, "key", "", "SSH private key path")
	prepareCmd.Flags().IntVar(&preparePort, "port", 22, "SSH port for remote targets")
	prepareCmd.Flags().BoolVar(&prepareDryRun, "dry-run", false, "Show what would be done")
	prepareCmd.Flags().BoolVar(&prepareSkipDocker, "skip-docker", false, "Skip Docker installation check")
	prepareCmd.Flags().BoolVar(&prepareSkipTofu, "skip-tofu", false, "Skip packaged OpenTofu check")
	prepareCmd.Flags().BoolVar(&prepareAutoFix, "auto-fix", true, "Auto-correct fixable issues")
	prepareCmd.Flags().BoolVar(&prepareForce, "force", false, "Continue even with insufficient disk space")
	prepareCmd.Flags().BoolVar(&prepareNonInteractive, "non-interactive", false, "Fail instead of prompting when prepare needs operator input")
}

func runPrepare(cmd *cobra.Command, args []string) (retErr error) {
	ctx := context.Background()
	wd := getWorkDir()
	isRemote := prepareHost != "localhost" && prepareHost != ""
	rolloutEvent("prepare", "started", "prepare started", map[string]string{
		"host": prepareHost,
	})
	defer func() {
		if retErr != nil {
			rolloutFailure("prepare", retErr)
			closeRolloutRecorder(rollout.Summary{
				Status:  "failed",
				Message: retErr.Error(),
			})
			return
		}
		rolloutEvent("prepare", "succeeded", "prepare succeeded", nil)
	}()

	deployLog.Event("prepare.start",
		slog.Bool("is_remote", isRemote),
		slog.String("host", prepareHost),
	)

	printInfo("Preparing system for StackKit deployment")

	// Load spec if provided
	loader := config.NewLoader(wd)
	rolloutEvent("spec.load", "started", "loading stack spec", map[string]string{
		"spec_file": specFile,
	})
	spec, err := loader.LoadStackSpec(specFile)
	if err != nil && !os.IsNotExist(err) {
		rolloutFailure("spec.load", err)
		printWarning("Could not load spec file: %v", err)
	}
	if spec != nil {
		rolloutEvent("spec.load", "succeeded", "stack spec loaded", map[string]string{
			"stackkit": spec.StackKit,
			"mode":     spec.Mode,
			"domain":   spec.Domain,
		})
	} else {
		rolloutEvent("spec.load", "skipped", "stack spec not present", map[string]string{
			"spec_file": specFile,
		})
	}

	// Validate spec if loaded
	if spec != nil {
		printInfo("Validating spec file...")
		validator := cue.NewValidator(wd)
		result, err := validator.ValidateSpec(spec)
		if err != nil {
			return fmt.Errorf("validation error: %w", err)
		}

		if !result.Valid {
			deployLog.Error("prepare.spec_validation",
				slog.Bool("valid", false),
				slog.Int("error_count", len(result.Errors)),
			)
			printError("Spec validation failed:")
			for _, e := range result.Errors {
				fmt.Printf("  • %s: %s\n", red(e.Path), e.Message)
			}
			return fmt.Errorf("spec validation failed with %d errors", len(result.Errors))
		}

		deployLog.Event("prepare.spec_validation",
			slog.Bool("valid", true),
			slog.Int("error_count", 0),
		)

		printSuccess("Spec file is valid")

		for _, w := range result.Warnings {
			printWarning("%s: %s", w.Path, w.Message)
		}
	}

	if isRemote {
		return prepareRemoteSystem(ctx, spec)
	}

	return prepareLocalSystem(ctx, spec, loader)
}

func prepareLocalSystem(ctx context.Context, spec *models.StackSpec, loader *config.Loader) error {
	// Load StackKit definition if available — used for disk/resource requirements
	var reqs *models.Requirements
	if spec != nil && spec.StackKit != "" {
		if kitDir, err := loader.FindStackKitDir(spec.StackKit); err == nil {
			if kit, err := loader.LoadStackKit(filepath.Join(kitDir, "stackkit.yaml")); err == nil {
				reqs = &kit.Requirements
			}
		}
	}

	// Phase 0: Early VPS compatibility detection (before Docker install)
	if !prepareSkipDocker && !prepareDryRun {
		printInfo("Checking VPS compatibility...")
		rolloutEvent("vps_compat", "started", "checking VPS compatibility", nil)
		virtType := detectVirtualization()
		unshareOK := testUnshare()
		cgroupVer := detectCgroupVersion()

		tier := classifyCompatibilityTier(virtType, unshareOK, detectBridgeSupport(), detectStorageDriver() != models.StorageVFS)

		deployLog.Event("prepare.vps_compat",
			slog.String("virt_type", virtType),
			slog.Bool("unshare_ok", unshareOK),
			slog.String("tier", string(tier)),
		)

		if tier == models.TierIncompatible {
			// Write capabilities for inspection
			caps := &models.DockerCapabilities{
				VirtualizationType: virtType,
				CompatibilityTier:  models.TierIncompatible,
				UnshareAvailable:   false,
				CgroupVersion:      cgroupVer,
				DockerFunctional:   false,
				RuntimeError:       "kernel blocks container namespaces (unshare: operation not permitted)",
			}
			writeDockerCapabilities(caps)

			// Offer native mode instead of failing
			if err := promptForNativeMode(spec, loader, virtType); err != nil {
				rolloutFailure("vps_compat", err)
				return err
			}
			// User accepted native mode — skip Docker entirely
			return prepareNativeMode(ctx, spec, loader)
		}

		if tier == models.TierDegraded {
			printWarning("VPS has limited Docker support — workarounds will be applied automatically")
			printInfo("  Virtualization: %s, unshare: %v", virtType, unshareOK)
		} else {
			printSuccess("VPS compatibility: %s (%s)", tier, virtType)
		}
		rolloutEvent("vps_compat", "succeeded", "VPS compatibility checked", map[string]string{
			"tier":      string(tier),
			"virt_type": virtType,
			"cgroup":    cgroupVer,
		})
	}

	// Network environment detection + NodeContext resolution
	if !prepareDryRun {
		printInfo("Detecting network environment...")
		rolloutEvent("network_env", "started", "detecting network environment", nil)
		netResult := netenv.Detect(ctx)

		deployLog.Event("prepare.network_env",
			slog.String("environment", string(netResult.Environment)),
			slog.String("public_ip", netResult.PublicIP),
			slog.String("private_ip", netResult.PrivateIP),
			slog.Bool("is_nat", netResult.IsNAT),
			slog.Bool("has_public_interface", netResult.HasPublicInterface),
		)

		// Store in capabilities for use by generate
		caps := loadDockerCapabilities()
		if caps == nil {
			caps = &models.DockerCapabilities{}
		}
		caps.NetworkEnv = netResult.Environment
		caps.PublicIP = netResult.PublicIP
		caps.PrivateIP = netResult.PrivateIP
		caps.IsNAT = netResult.IsNAT
		caps.HasPublicInterface = netResult.HasPublicInterface

		// Resolve NodeContext from network + hardware detection
		// Hardware info may not be available yet (detected later in prepare),
		// so we resolve with what we have now; generate will re-resolve with full info.
		resolved := netenv.ResolveFromResult(netResult, caps.CPUCores, caps.MemoryGB)

		// CLI --context flag overrides auto-detection
		if contextFlag != "" {
			resolved = models.NodeContext(contextFlag)
		}
		caps.ResolvedContext = resolved
		writeDockerCapabilities(caps)

		printSuccess("Network: %s", netenv.FormatEnvironment(netResult.Environment))
		printSuccess("Context: %s", netenv.FormatNodeContext(resolved))
		if netResult.PublicIP != "" {
			printInfo("  Public IP: %s", netResult.PublicIP)
		}
		if netResult.PrivateIP != "" {
			printInfo("  Private IP: %s", netResult.PrivateIP)
		}
		rolloutEvent("network_env", "succeeded", "network environment detected", map[string]string{
			"environment": string(netResult.Environment),
			"public_ip":   netResult.PublicIP,
			"private_ip":  netResult.PrivateIP,
			"context":     string(resolved),
		})
	}

	// Early disk space pre-flight: check before installing anything.
	// If critically low, tries LVM auto-extend or offers interactive resolution.
	if !prepareDryRun {
		rolloutEvent("resources.disk", "started", "checking disk resources", nil)
		if err := checkDiskPreFlight(reqs, spec, loader); err != nil {
			rolloutFailure("resources.disk", err)
			return fmt.Errorf("system preparation failed: %w", err)
		}
		rolloutEvent("resources.disk", "succeeded", "disk resources checked", nil)
	}

	// Check Docker
	if !prepareSkipDocker {
		printInfo("Checking Docker installation...")
		rolloutEvent("docker.check", "started", "checking Docker installation", nil)
		dockerClient := docker.NewClient()

		installed := dockerClient.IsInstalled()
		if !installed {
			if prepareDryRun {
				printWarning("Docker not installed - would install")
				rolloutEvent("docker.install", "skipped", "Docker not installed - dry run would install it", nil)
			} else {
				printInfo("Installing Docker...")
				rolloutEvent("apt_wait", "started", "waiting for package manager locks before Docker install", map[string]string{
					"method": "apt_or_get_docker",
				})
				if err := waitForLocalPackageManager(ctx); err != nil {
					rolloutFailure("apt_wait", err)
					return fmt.Errorf("failed to install Docker: %w", err)
				}
				rolloutEvent("apt_wait", "succeeded", "package manager locks released", nil)
				rolloutEvent("docker.install", "started", "installing Docker", map[string]string{
					"method": "get_docker",
				})
				if err := installDockerLocal(ctx); err != nil {
					rolloutFailure("docker.install", fmt.Errorf("docker install failed: %w", err))
					return fmt.Errorf("failed to install Docker: %w", err)
				}
				rolloutEvent("docker.install", "succeeded", "Docker installed", nil)
				printSuccess("Docker installed successfully")
			}
		} else {
			version, err := dockerClient.Version(ctx)
			if err != nil {
				printWarning("Could not get Docker version: %v", err)
			} else {
				printSuccess("Docker %s installed", version)
			}

			running := dockerClient.IsRunning(ctx)
			if !running {
				if prepareDryRun {
					printWarning("Docker daemon is not running - would attempt to start")
				} else {
					printInfo("Docker daemon is not running, starting...")
					caps, err := startDockerDaemon(ctx)
					if err != nil {
						return fmt.Errorf("docker is installed but won't start: %w", err)
					}
					printSuccess("Docker daemon started")
					writeDockerCapabilities(caps)
				}
			} else {
				printSuccess("Docker daemon is running")
				// Detect capabilities even when Docker is already running.
				// This ensures capabilities.json exists for generate to read,
				// e.g. when the installer is re-run on a restricted VPS.
				caps := detectCapabilities()
				writeDockerCapabilities(caps)
			}

			dockerVersion := ""
			if v, err := dockerClient.Version(ctx); err == nil {
				dockerVersion = v
			}
			deployLog.Event("prepare.docker",
				slog.Bool("installed", true),
				slog.Bool("running", running),
				slog.String("version", dockerVersion),
			)
		}

		if !installed {
			deployLog.Event("prepare.docker",
				slog.Bool("installed", false),
				slog.Bool("running", false),
				slog.String("version", ""),
			)
		}
		rolloutEvent("docker.check", "succeeded", "Docker installation checked", map[string]string{
			"installed": fmt.Sprintf("%t", installed),
		})
	}

	// Docker runtime + DNS test + image pre-pull (after Docker, before OpenTofu)
	if !prepareSkipDocker && !prepareDryRun {
		rolloutEvent("docker.runtime", "started", "testing Docker runtime", nil)
		caps := loadDockerCapabilities()
		if caps == nil {
			caps = detectCapabilities()
		}

		// Critical: test that Docker can actually run containers.
		// On some VPS (OpenVZ/LXC), the daemon starts but the kernel blocks
		// unshare/namespace creation, making all container operations fail.
		if !testDockerRuntime(ctx, caps) {
			deployLog.Error("prepare.docker_runtime",
				slog.Bool("success", false),
			)
			writeDockerCapabilities(caps)
			// Docker installed but can't run containers — offer native mode
			if err := promptForNativeMode(spec, loader, caps.VirtualizationType); err != nil {
				rolloutFailure("docker.runtime", err)
				return err
			}
			return prepareNativeMode(ctx, spec, loader)
		}
		rolloutEvent("docker.runtime", "succeeded", "Docker runtime is functional", nil)
		deployLog.Event("prepare.docker_runtime",
			slog.Bool("success", true),
		)

		rolloutEvent("docker.dns", "started", "testing Docker DNS resolution", nil)
		caps = testDockerDNS(ctx, caps)
		if caps.DNSWorking {
			rolloutEvent("docker.dns", "succeeded", "Docker DNS resolution works", map[string]string{
				"fix": string(caps.DNSFix),
			})
		} else {
			rolloutEvent("docker.dns", "failed", "Docker DNS resolution unavailable; host pre-pull fallback selected", map[string]string{
				"fix": string(caps.DNSFix),
			})
		}
		if shouldPrePullImages() {
			rolloutEvent("docker.prepull", "started", "pre-pulling Docker images", map[string]string{
				"compute_tier": preparePrePullComputeTier(spec),
			})
			prePullImages(ctx, caps, preparePrePullComputeTier(spec))
			if len(caps.PrePullFailed) > 0 && dockerPrePullRequired() {
				rolloutFailure("docker.prepull", fmt.Errorf("docker image pre-pull failed for %d required images", len(caps.PrePullFailed)))
				return fmt.Errorf("docker image pre-pull failed for %d required images", len(caps.PrePullFailed))
			}
			rolloutEvent("docker.prepull", "succeeded", "Docker image pre-pull completed", map[string]string{
				"pulled": fmt.Sprintf("%d", len(caps.PrePulledImages)),
				"failed": fmt.Sprintf("%d", len(caps.PrePullFailed)),
			})
		} else {
			printInfo("Skipping optional Docker image pre-pull (STACKKIT_PREPULL_IMAGES=false)")
			rolloutEvent("docker.prepull", "skipped", "Docker image pre-pull disabled", nil)
		}
		writeDockerCapabilities(caps)
	}

	// Check packaged OpenTofu
	rolloutEvent("opentofu.check", "started", "checking StackKit-packaged OpenTofu", nil)
	if err := ensurePackagedOpenTofu(ctx); err != nil {
		rolloutFailure("opentofu.check", err)
		return err
	}
	rolloutEvent("opentofu.check", "succeeded", "StackKit-packaged OpenTofu is available", nil)

	if spec != nil && spec.UsesAdvancedIAC() {
		rolloutEvent("terramate.check", "started", "checking Terramate for advanced lifecycle", nil)
		if err := ensureTerramate(ctx); err != nil {
			rolloutFailure("terramate.check", err)
			return err
		}
		rolloutEvent("terramate.check", "succeeded", "Terramate is available for advanced lifecycle", nil)
	}
	if err := ensureTechStackGuardLocal(ctx, spec); err != nil {
		rolloutFailure("techstack.guard", err)
		return err
	}
	emitPrepareTelemetryHandshake()

	// Clean up installation artifacts to reclaim disk space
	if !prepareDryRun {
		cleanupInstallArtifacts(ctx)
	}

	// Auto-detect compute tier from hardware profile
	if spec != nil && !prepareDryRun {
		caps := loadDockerCapabilities()
		if caps != nil && caps.CPUCores > 0 && caps.MemoryGB > 0 {
			detected := autoDetectComputeTier(caps.CPUCores, caps.MemoryGB)
			if spec.Compute.Tier == "" || spec.Compute.Tier == models.ComputeTierStandard {
				spec.Compute.Tier = detected
				printInfo("Compute tier auto-detected: %s (%d CPU, %.1f GB RAM)", bold(detected), caps.CPUCores, caps.MemoryGB)
				saveSpec(spec, loader)
			}
			deployLog.Event("prepare.compute_tier",
				slog.String("detected_tier", detected),
				slog.Int("cpu", caps.CPUCores),
				slog.Float64("memory_gb", caps.MemoryGB),
			)

			// Re-resolve NodeContext now that hardware info is available
			// (initial resolution in network detection may not have had CPU/memory)
			if caps.NetworkEnv != "" {
				netResult := &netenv.Result{Environment: caps.NetworkEnv}
				resolved := netenv.ResolveFromResult(netResult, caps.CPUCores, caps.MemoryGB)
				if contextFlag != "" {
					resolved = models.NodeContext(contextFlag)
				}
				if resolved != caps.ResolvedContext {
					printInfo("Context refined: %s -> %s (with hardware info)", caps.ResolvedContext, resolved)
					caps.ResolvedContext = resolved
					writeDockerCapabilities(caps)
				}
			}
		}
	}

	// Check system resources
	printInfo("Checking system resources...")
	rolloutEvent("resources.check", "started", "checking system resources", nil)
	resourceAttrs := checkLocalResources(reqs)
	deployLog.Event("prepare.resources_checked")
	rolloutEvent("resources.check", "succeeded", "system resources checked", resourceAttrs)

	if prepareDryRun {
		printInfo("Dry run complete - no changes made")
	} else {
		printSuccess("System is ready for StackKit deployment")
	}

	return nil
}

// promptForNativeMode asks the user whether to switch to native (bare-metal) mode
// when Docker is not available on this VPS.
func promptForNativeMode(spec *models.StackSpec, loader *config.Loader, virtType string) error {
	if prepareNonInteractive || !isTerminal() {
		return fmt.Errorf("docker is incompatible with virtualization %s and prepare is non-interactive; choose a KVM/full-virtualization VPS or configure native runtime explicitly", virtType)
	}
	fmt.Println()
	printError("%s", "Docker as our containerization environment will not work on your type of VM.")
	fmt.Println()
	fmt.Printf("  Virtualization: %s\n", virtType)
	fmt.Println("  Your VPS uses container-based virtualization (OpenVZ/LXC) that blocks")
	fmt.Println("  the kernel features Docker needs (namespaces, cgroups, unshare).")
	fmt.Println()
	fmt.Println("  " + bold("Option:") + " Install services as native binaries (systemd) instead of containers.")
	fmt.Println("  This installs Traefik, TinyAuth, PocketID, and other services directly")
	fmt.Println("  on the host as systemd services. No Docker required.")
	fmt.Println()

	fmt.Print("  Install in native/bare-metal mode? [Y/n] ")
	var answer string
	_, _ = fmt.Scanln(&answer)
	if len(answer) > 0 && (answer[0] == 'n' || answer[0] == 'N') {
		fmt.Println()
		fmt.Println("  " + bold("What you need:") + " A VPS with KVM or full virtualization.")
		fmt.Println("  These providers offer compatible VPS from ~$4/month:")
		fmt.Println()
		fmt.Println("    • Hetzner Cloud    — https://hetzner.cloud")
		fmt.Println("    • DigitalOcean     — https://digitalocean.com")
		fmt.Println("    • Linode (Akamai)  — https://linode.com")
		fmt.Println("    • Vultr            — https://vultr.com")
		fmt.Println("    • Contabo (KVM)    — https://contabo.com")
		fmt.Println()
		return fmt.Errorf("VPS is incompatible with Docker — native mode declined")
	}

	// Persist runtime choice
	spec.Runtime = models.RuntimeNative
	wd := getWorkDir()
	specPath := specFile
	if !filepath.IsAbs(specPath) {
		specPath = filepath.Join(wd, specPath)
	}
	if err := loader.SaveStackSpec(spec, specPath); err != nil {
		printWarning("Could not save runtime to spec: %v", err)
	}

	printSuccess("Switching to native mode")
	return nil
}

// prepareNativeMode prepares the system for native binary deployment (no Docker).
func prepareNativeMode(ctx context.Context, spec *models.StackSpec, loader *config.Loader) error {
	// Check packaged OpenTofu (still needed for native mode)
	rolloutEvent("opentofu.check", "started", "checking StackKit-packaged OpenTofu for native mode", nil)
	if err := ensurePackagedOpenTofu(ctx); err != nil {
		rolloutFailure("opentofu.check", err)
		return err
	}
	rolloutEvent("opentofu.check", "succeeded", "StackKit-packaged OpenTofu is available", nil)
	emitPrepareTelemetryHandshake()

	// Check system resources
	printInfo("Checking system resources...")
	rolloutEvent("resources.check", "started", "checking system resources", nil)
	resourceAttrs := checkLocalResources(nil)
	rolloutEvent("resources.check", "succeeded", "system resources checked", resourceAttrs)

	if prepareDryRun {
		printInfo("Dry run complete - no changes made")
	} else {
		printSuccess("System is ready for native StackKit deployment (no Docker)")
	}

	return nil
}

func prepareRemoteSystem(ctx context.Context, spec *models.StackSpec) error {
	printInfo("Preparing remote host: %s", prepareHost)
	rolloutEvent("target.connect", "started", "connecting to remote prepare target", map[string]string{
		"host": prepareHost,
	})

	// Set SSH options
	opts := []ssh.ClientOption{
		ssh.WithHost(prepareHost),
		ssh.WithPort(preparePort),
	}
	if prepareUser != "" {
		opts = append(opts, ssh.WithUser(prepareUser))
	}
	if prepareKey != "" {
		opts = append(opts, ssh.WithKeyPath(prepareKey))
	}

	// Connect
	sshClient := ssh.NewClient(opts...)
	if err := sshClient.Connect(); err != nil {
		rolloutFailure("target.connect", err)
		return fmt.Errorf("failed to connect to %s: %w", prepareHost, err)
	}
	defer func() { _ = sshClient.Close() }()

	printSuccess("Connected to %s", prepareHost)
	rolloutEvent("target.connect", "succeeded", "connected to remote prepare target", map[string]string{
		"host": prepareHost,
	})

	// Get system info
	printInfo("Gathering system information...")
	rolloutEvent("target.inspect", "started", "gathering remote system information", nil)
	sysInfo, err := sshClient.GetSystemInfo(ctx)
	if err != nil {
		printWarning("Could not get full system info: %v", err)
		rolloutEvent("target.inspect", "failed", "remote system information incomplete", map[string]string{
			"error": err.Error(),
		})
	} else {
		printSuccess("OS: %s %s (%s)", sysInfo.OS, sysInfo.OSVersion, sysInfo.Arch)
		printSuccess("CPU: %d cores, RAM: %d MB, Disk: %d GB free",
			sysInfo.CPUCores, sysInfo.MemoryMB, sysInfo.DiskGB)
		rolloutEvent("target.inspect", "succeeded", "remote system information collected", map[string]string{
			"os":      sysInfo.OS,
			"version": sysInfo.OSVersion,
			"arch":    sysInfo.Arch,
		})
	}

	// Check Docker
	rolloutEvent("docker.check", "started", "checking remote Docker installation", nil)
	if err := checkRemoteDocker(ctx, sshClient, sysInfo); err != nil {
		rolloutFailure("docker.check", err)
		return err
	}
	rolloutEvent("docker.check", "succeeded", "remote Docker installation checked", nil)

	// Install StackKit-packaged OpenTofu on the target.
	rolloutEvent("opentofu.check", "started", "checking remote StackKit-packaged OpenTofu", nil)
	if err := checkRemoteTofu(ctx, sshClient, sysInfo); err != nil {
		rolloutFailure("opentofu.check", err)
		return err
	}
	rolloutEvent("opentofu.check", "succeeded", "remote StackKit-packaged OpenTofu installed", nil)

	if spec != nil && spec.UsesAdvancedIAC() {
		rolloutEvent("terramate.check", "started", "checking Terramate for remote advanced lifecycle", nil)
		if err := checkRemoteTerramate(ctx, sshClient, sysInfo); err != nil {
			rolloutFailure("terramate.check", err)
			return err
		}
		rolloutEvent("terramate.check", "succeeded", "remote Terramate is available", nil)
	}
	if err := ensureTechStackGuardRemote(ctx, spec, sshClient); err != nil {
		rolloutFailure("techstack.guard", err)
		return err
	}

	emitPrepareTelemetryHandshake()

	// Check ports
	if spec != nil {
		printInfo("Checking required ports...")
		rolloutEvent("ports.check", "started", "checking required remote ports", nil)
		requiredPorts := []int{80, 443}
		for _, port := range requiredPorts {
			if sshClient.CheckPort(ctx, port) {
				printSuccess("Port %d is available", port)
			} else {
				printWarning("Port %d is in use", port)
			}
		}
		rolloutEvent("ports.check", "succeeded", "required remote ports checked", nil)
	}

	if prepareDryRun {
		printInfo("Dry run complete - no changes made")
	} else {
		printSuccess("Remote system is ready for StackKit deployment")
	}

	return nil
}

func checkRemoteDocker(ctx context.Context, sshClient *ssh.Client, sysInfo *models.SystemInfo) error {
	if prepareSkipDocker {
		rolloutEvent("docker.check", "skipped", "remote Docker check skipped by flag", nil)
		return nil
	}
	if sysInfo == nil {
		return fmt.Errorf("target.inspect_failed: remote system information unavailable before Docker check")
	}
	if sysInfo.DockerVersion != "" {
		printSuccess("Docker %s installed", sysInfo.DockerVersion)
		rolloutEvent("docker.check", "succeeded", "remote Docker already installed", map[string]string{
			"version": sysInfo.DockerVersion,
		})
		return nil
	}
	if prepareDryRun {
		printWarning("Docker not installed - would install")
		rolloutEvent("docker.install", "skipped", "remote Docker not installed - dry run would install it", nil)
		return nil
	}
	printInfo("Installing Docker...")
	rolloutEvent("apt_wait", "started", "waiting for remote package manager locks before Docker install", map[string]string{
		"method": "apt_or_package_manager",
	})
	if err := waitForRemotePackageManager(ctx, sshClient, sysInfo.OS); err != nil {
		rolloutFailure("apt_wait", err)
		return fmt.Errorf("failed to install Docker: %w", err)
	}
	rolloutEvent("apt_wait", "succeeded", "remote package manager locks released", nil)
	rolloutEvent("docker.install", "started", "installing remote Docker", map[string]string{
		"os": sysInfo.OS,
	})
	if err := installDockerRemote(ctx, sshClient, sysInfo.OS); err != nil {
		rolloutFailure("docker.install", fmt.Errorf("remote docker install failed: %w", err))
		return fmt.Errorf("failed to install Docker: %w", err)
	}
	rolloutEvent("docker.install", "succeeded", "remote Docker installed", nil)
	printSuccess("Docker installed successfully")
	return nil
}

func checkRemoteTofu(ctx context.Context, sshClient *ssh.Client, sysInfo *models.SystemInfo) error {
	if prepareSkipTofu {
		return nil
	}
	if sysInfo == nil {
		return fmt.Errorf("target.inspect_failed: remote system information unavailable before OpenTofu check")
	}

	if prepareDryRun {
		printWarning("StackKit-packaged OpenTofu would be installed on the remote target")
		return nil
	}
	if strings.TrimSpace(sysInfo.TofuVersion) != "" {
		printSuccess("Remote OpenTofu %s already installed", sysInfo.TofuVersion)
		return nil
	}

	packagedTofu, ok := tofu.PackagedBinaryPath()
	if !ok {
		return fmt.Errorf("StackKit-packaged OpenTofu binary not found; reinstall StackKit from the official release package")
	}
	if err := ensurePackagedTofuMatchesRemote(sysInfo); err != nil {
		return err
	}

	printInfo("Installing StackKit-packaged OpenTofu on remote target...")
	remoteTmp := "/tmp/stackkit-tofu"
	if err := sshClient.CopyFile(ctx, packagedTofu, remoteTmp); err != nil {
		return fmt.Errorf("failed to upload packaged OpenTofu: %w", err)
	}

	stdout, stderr, err := sshClient.RunWithSudo(ctx, "install -m 755 /tmp/stackkit-tofu /usr/local/bin/tofu && rm -f /tmp/stackkit-tofu && /usr/local/bin/tofu version")
	if err != nil {
		return fmt.Errorf("failed to install packaged OpenTofu: %w: %s", err, stderr)
	}
	version := strings.TrimSpace(strings.Split(stdout, "\n")[0])
	if version == "" && sysInfo != nil && sysInfo.TofuVersion != "" {
		version = "OpenTofu v" + sysInfo.TofuVersion
	}
	if version == "" {
		printSuccess("StackKit-packaged OpenTofu installed successfully")
	} else {
		printSuccess("StackKit-packaged %s installed successfully", version)
	}
	return nil
}

func ensurePackagedTofuMatchesRemote(sysInfo *models.SystemInfo) error {
	if sysInfo == nil {
		return fmt.Errorf("remote system information unavailable; cannot choose a packaged OpenTofu binary safely")
	}

	targetArch := normalizeRemoteArch(sysInfo.Arch)
	if targetArch == "" {
		return fmt.Errorf("unsupported remote architecture for packaged OpenTofu: %s", sysInfo.Arch)
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != targetArch {
		return fmt.Errorf("packaged OpenTofu binary is %s/%s, but remote target requires linux/%s; run the StackKit installer on the target or use the matching Linux release package", runtime.GOOS, runtime.GOARCH, targetArch)
	}
	return nil
}

func normalizeRemoteArch(arch string) string {
	switch strings.TrimSpace(strings.ToLower(arch)) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return ""
	}
}

func checkLocalResources(reqs *models.Requirements) map[string]string {
	attrs := map[string]string{}
	// CPU check
	numCPU := runtime.NumCPU()
	printSuccess("CPU: %d cores", numCPU)
	attrs["cpu_cores"] = fmt.Sprintf("%d", numCPU)

	// Memory check — use runtime.MemStats for Go-accessible info,
	// then read system total from OS-specific /proc/meminfo on Linux.
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	sysMB := m.Sys / 1024 / 1024
	if sysMB > 0 {
		printSuccess("Go runtime memory: %d MB allocated", sysMB)
		attrs["go_runtime_memory_mb"] = fmt.Sprintf("%d", sysMB)
	}

	// Try reading system total memory (Linux)
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		var totalKB uint64
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "MemTotal:") {
				_, _ = fmt.Sscanf(line, "MemTotal: %d kB", &totalKB)
				break
			}
		}
		if totalKB > 0 {
			totalGB := float64(totalKB) / 1024 / 1024
			printSuccess("System memory: %.1f GB", totalGB)
			attrs["memory_gb"] = fmt.Sprintf("%.1f", totalGB)
			if totalGB < 2.0 {
				printWarning("Low memory — some services may not start")
			}
		} else {
			printInfo("System memory: could not parse /proc/meminfo")
		}
	} else {
		// Windows/macOS — no /proc/meminfo available
		printInfo("System memory: auto-detection not available on this OS (check manually)")
	}

	// Disk space check
	minDiskGB := 10.0
	recDiskGB := 20.0
	if reqs != nil {
		if reqs.Minimum.Disk > 0 {
			minDiskGB = float64(reqs.Minimum.Disk)
		}
		if reqs.Recommended.Disk > 0 {
			recDiskGB = float64(reqs.Recommended.Disk)
		}
	}

	availGB, totalGB, mount := getDiskSpace()
	if totalGB > 0 {
		printSuccess("Disk: %.1f GB available / %.1f GB total on %s", availGB, totalGB, mount)
		attrs["disk_available_gb"] = fmt.Sprintf("%.1f", availGB)
		attrs["disk_total_gb"] = fmt.Sprintf("%.1f", totalGB)
		attrs["disk_required_gb"] = fmt.Sprintf("%.1f", minDiskGB)
		attrs["disk_recommended_gb"] = fmt.Sprintf("%.1f", recDiskGB)
		attrs["mount"] = mount
		if availGB < minDiskGB {
			printError("Insufficient disk space — StackKit requires at least %d GB", int(minDiskGB))
			printInfo("  Available: %.1f GB on %s", availGB, mount)
			if isLVM, vgFreeGB, lvPath := detectLVM(); isLVM && vgFreeGB > 1.0 {
				printInfo("  LVM detected: %.1f GB free in volume group", vgFreeGB)
				printInfo("  Run: sudo lvextend -l +100%%FREE %s && sudo resize2fs %s", lvPath, lvPath)
			}
		} else if availGB < recDiskGB {
			printWarning("Disk space (%.1f GB) is below recommended %d GB", availGB, int(recDiskGB))
			if isLVM, vgFreeGB, lvPath := detectLVM(); isLVM && vgFreeGB > 1.0 {
				printInfo("  LVM detected: %.1f GB free in volume group — consider extending", vgFreeGB)
				printInfo("  Run: sudo lvextend -l +100%%FREE %s && sudo resize2fs %s", lvPath, lvPath)
			}
		}
	}
	return attrs
}

// saveSpec persists the spec to the active spec path.
func saveSpec(spec *models.StackSpec, loader *config.Loader) {
	wd := getWorkDir()
	specPath := specFile
	if !filepath.IsAbs(specPath) {
		specPath = filepath.Join(wd, specPath)
	}
	if err := loader.SaveStackSpec(spec, specPath); err != nil {
		printWarning("Could not save spec: %v", err)
	}
}

// isTerminal returns true if stdin is a terminal (not piped/redirected).
func isTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// autoDetectComputeTier returns the compute tier based on hardware profile.
func autoDetectComputeTier(cpuCores int, memoryGB float64) string {
	var result string
	if cpuCores >= 8 && memoryGB >= 16 {
		result = models.ComputeTierHigh
	} else if cpuCores >= 4 && memoryGB >= 8 {
		result = models.ComputeTierStandard
	} else {
		result = models.ComputeTierLow
	}
	deployLog.Event("prepare.tier_detection",
		slog.Int("cpu_cores", cpuCores),
		slog.Float64("memory_gb", memoryGB),
		slog.String("result", result),
	)
	return result
}

// classifyCompatibilityTier classifies the system's Docker compatibility based on
// detected capabilities. This provides an early signal before Docker is installed.
func classifyCompatibilityTier(virtType string, unshareOK, bridgeOK, overlayOK bool) models.CompatibilityTier {
	// If unshare is blocked, nothing works — incompatible
	if !unshareOK {
		result := models.TierIncompatible
		deployLog.Event("prepare.compat_classification",
			slog.String("virt_type", virtType),
			slog.Bool("unshare_ok", unshareOK),
			slog.Bool("bridge_ok", bridgeOK),
			slog.Bool("overlay_ok", overlayOK),
			slog.String("result", string(result)),
		)
		return result
	}

	// Known incompatible virtualization types
	switch virtType {
	case models.VirtOpenVZ:
		// OpenVZ almost always blocks unshare, but if we got here unshare passed
		// Still likely degraded due to other restrictions
		if !bridgeOK || !overlayOK {
			deployLog.Event("prepare.compat_classification",
				slog.String("virt_type", virtType),
				slog.Bool("unshare_ok", unshareOK),
				slog.Bool("bridge_ok", bridgeOK),
				slog.Bool("overlay_ok", overlayOK),
				slog.String("result", string(models.TierDegraded)),
			)
			return models.TierDegraded
		}
	case models.VirtLXC:
		// LXC with nesting can work, but usually lacks overlay/bridge
		if !bridgeOK || !overlayOK {
			deployLog.Event("prepare.compat_classification",
				slog.String("virt_type", virtType),
				slog.Bool("unshare_ok", unshareOK),
				slog.Bool("bridge_ok", bridgeOK),
				slog.Bool("overlay_ok", overlayOK),
				slog.String("result", string(models.TierDegraded)),
			)
			return models.TierDegraded
		}
	}

	// If everything works, it's full compatibility
	var result models.CompatibilityTier
	if bridgeOK && overlayOK {
		result = models.TierFull
	} else {
		// Some features missing but unshare works — degraded
		result = models.TierDegraded
	}

	deployLog.Event("prepare.compat_classification",
		slog.String("virt_type", virtType),
		slog.Bool("unshare_ok", unshareOK),
		slog.Bool("bridge_ok", bridgeOK),
		slog.Bool("overlay_ok", overlayOK),
		slog.String("result", string(result)),
	)
	return result
}

func preparePrePullComputeTier(spec *models.StackSpec) string {
	if spec == nil || strings.TrimSpace(spec.Compute.Tier) == "" {
		return models.ComputeTierStandard
	}
	return spec.Compute.Tier
}

func shouldPrePullImages() bool {
	value := os.Getenv("STACKKIT_PREPULL_IMAGES")
	if value == "" {
		value = os.Getenv("STACKKIT_IMAGE_PREPULL")
	}
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "0", prePullImagesDisabledFalseValue, "no", "off", "skip", "disabled":
		return false
	default:
		return true
	}
}

// ensurePackagedOpenTofu checks the StackKit-packaged OpenTofu binary.
// Respects prepareSkipTofu and prepareDryRun flags.
func ensurePackagedOpenTofu(ctx context.Context) error {
	if prepareSkipTofu {
		return nil
	}

	printInfo("Checking StackKit-packaged OpenTofu...")
	packagedTofu, ok := tofu.PackagedBinaryPath()
	if !ok {
		if prepareDryRun {
			printWarning("StackKit-packaged OpenTofu missing - release package must include it")
			return nil
		}
		return fmt.Errorf("StackKit-packaged OpenTofu binary not found; reinstall StackKit from the official release package")
	}

	tofuExec := tofu.NewExecutor(tofu.WithBinary(packagedTofu))
	if !tofuExec.IsInstalled() {
		return fmt.Errorf("StackKit-packaged OpenTofu binary is not executable: %s", packagedTofu)
	}

	version, err := tofuExec.Version(ctx)
	if err != nil {
		printWarning("Could not get packaged OpenTofu version: %v", err)
		return nil
	}

	printSuccess("StackKit-packaged OpenTofu %s available", version)
	return nil
}

func ensureTerramate(ctx context.Context) error {
	printInfo("Checking StackKit-packaged Terramate...")
	packagedTerramate, ok := terramate.PackagedBinaryPath()
	if !ok {
		if prepareDryRun {
			printWarning("StackKit-packaged Terramate missing - release package must include it for advanced mode")
			return nil
		}
		return fmt.Errorf("StackKit-packaged Terramate binary not found; advanced mode requires the official release package with terramate")
	}
	if prepareDryRun {
		printWarning("StackKit-packaged Terramate would be checked")
		return nil
	}
	terramateExec := terramate.NewExecutor(terramate.WithBinary(packagedTerramate))
	if !terramateExec.IsInstalled() {
		return fmt.Errorf("StackKit-packaged Terramate binary is not executable: %s", packagedTerramate)
	}
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	version, err := terramateExec.Version(checkCtx)
	if err != nil {
		return fmt.Errorf("StackKit-packaged Terramate version check failed: %w", err)
	}
	version = strings.TrimSpace(strings.Split(version, "\n")[0])
	if version == "" {
		version = "available"
	}
	printSuccess("StackKit-packaged Terramate %s available", version)
	return nil
}

func checkRemoteTerramate(ctx context.Context, sshClient *ssh.Client, sysInfo *models.SystemInfo) error {
	if prepareDryRun {
		printWarning("StackKit-packaged Terramate would be installed on the remote target")
		return nil
	}
	if sysInfo == nil {
		return fmt.Errorf("target.inspect_failed: remote system information unavailable before Terramate check")
	}
	if stdout, _, err := sshClient.Run(ctx, "command -v terramate >/dev/null 2>&1 && terramate version"); err == nil {
		version := strings.TrimSpace(strings.Split(stdout, "\n")[0])
		if version == "" {
			version = "available"
		}
		printSuccess("Remote Terramate %s already installed", version)
		return nil
	}
	packagedTerramate, ok := terramate.PackagedBinaryPath()
	if !ok {
		return fmt.Errorf("StackKit-packaged Terramate binary not found; advanced mode requires the official release package with terramate")
	}
	if err := ensurePackagedTerramateMatchesRemote(sysInfo); err != nil {
		return err
	}

	printInfo("Installing StackKit-packaged Terramate on remote target...")
	remoteTmp := "/tmp/stackkit-terramate"
	if err := sshClient.CopyFile(ctx, packagedTerramate, remoteTmp); err != nil {
		return fmt.Errorf("failed to upload packaged Terramate: %w", err)
	}

	stdout, stderr, err := sshClient.RunWithSudo(ctx, "install -m 755 /tmp/stackkit-terramate /usr/local/bin/terramate && rm -f /tmp/stackkit-terramate && /usr/local/bin/terramate version")
	if err != nil {
		return fmt.Errorf("failed to install packaged Terramate: %w: %s", err, stderr)
	}
	version := strings.TrimSpace(strings.Split(stdout, "\n")[0])
	if version == "" {
		version = "available"
	}
	printSuccess("StackKit-packaged remote Terramate %s installed successfully", version)
	return nil
}

func ensurePackagedTerramateMatchesRemote(sysInfo *models.SystemInfo) error {
	if sysInfo == nil {
		if runtime.GOOS == "linux" {
			return nil
		}
		return fmt.Errorf("packaged Terramate binary is %s/%s, but remote target requires Linux; run the StackKit installer on the target or use the matching Linux release package", runtime.GOOS, runtime.GOARCH)
	}
	targetArch := normalizeRemoteArch(sysInfo.Arch)
	if targetArch == "" {
		return fmt.Errorf("unsupported remote architecture for packaged Terramate: %s", sysInfo.Arch)
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != targetArch {
		return fmt.Errorf("packaged Terramate binary is %s/%s, but remote target requires linux/%s; run the StackKit installer on the target or use the matching Linux release package", runtime.GOOS, runtime.GOARCH, targetArch)
	}
	return nil
}

type techStackHandoff struct {
	ServerURL        string
	ServerID         string
	RuntimeAgentID   string
	AgentToken       string
	TenantID         string
	OwnerID          string
	StackID          string
	LeaseID          string
	HeartbeatURL     string
	InventoryURL     string
	ChannelBootstrap string
}

func techStackHandoffFromEnv() techStackHandoff {
	return techStackHandoff{
		ServerURL:        strings.TrimRight(strings.TrimSpace(os.Getenv("TECHSTACK_SERVER_URL")), "/"),
		ServerID:         strings.TrimSpace(os.Getenv("TECHSTACK_SERVER_ID")),
		RuntimeAgentID:   strings.TrimSpace(os.Getenv("TECHSTACK_RUNTIME_AGENT_ID")),
		AgentToken:       strings.TrimSpace(os.Getenv("TECHSTACK_AGENT_TOKEN")),
		TenantID:         strings.TrimSpace(os.Getenv("TECHSTACK_TENANT_ID")),
		OwnerID:          strings.TrimSpace(os.Getenv("TECHSTACK_OWNER_ID")),
		StackID:          strings.TrimSpace(os.Getenv("TECHSTACK_STACK_ID")),
		LeaseID:          strings.TrimSpace(os.Getenv("TECHSTACK_LEASE_ID")),
		HeartbeatURL:     strings.TrimSpace(os.Getenv("TECHSTACK_HEARTBEAT_URL")),
		InventoryURL:     strings.TrimSpace(os.Getenv("TECHSTACK_INVENTORY_URL")),
		ChannelBootstrap: strings.TrimSpace(os.Getenv("TECHSTACK_CHANNEL_BOOTSTRAP")),
	}
}

func (h techStackHandoff) requested() bool {
	if truthyEnv("TECHSTACK_MANAGED") {
		return true
	}
	return h.ServerURL != "" || h.ServerID != "" || h.RuntimeAgentID != "" || h.AgentToken != "" || h.LeaseID != "" || h.HeartbeatURL != "" || h.InventoryURL != ""
}

func (h techStackHandoff) validateManaged() error {
	missing := []string{}
	for name, value := range map[string]string{
		"TECHSTACK_LEASE_ID":         h.LeaseID,
		"TECHSTACK_SERVER_URL":       h.ServerURL,
		"TECHSTACK_SERVER_ID":        h.ServerID,
		"TECHSTACK_RUNTIME_AGENT_ID": h.RuntimeAgentID,
		"TECHSTACK_AGENT_TOKEN":      h.AgentToken,
	} {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if h.HeartbeatURL == "" && h.InventoryURL == "" {
		missing = append(missing, "TECHSTACK_HEARTBEAT_URL or TECHSTACK_INVENTORY_URL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("techstack_orchestration_handoff_missing: TechStack-managed prepare requires %s", strings.Join(missing, ", "))
	}
	return nil
}

func ensureTechStackGuardLocal(ctx context.Context, spec *models.StackSpec) error {
	return ensureTechStackGuard(ctx, spec, nil)
}

func ensureTechStackGuardRemote(ctx context.Context, spec *models.StackSpec, sshClient *ssh.Client) error {
	return ensureTechStackGuard(ctx, spec, sshClient)
}

func ensureTechStackGuard(ctx context.Context, spec *models.StackSpec, sshClient *ssh.Client) error {
	handoff := techStackHandoffFromEnv()
	if !handoff.requested() {
		attrs := map[string]string{}
		if spec != nil {
			attrs["mode"] = spec.EffectiveInstallMode()
		}
		rolloutEvent("techstack.guard", "skipped", "TechStack guard handoff not requested for standalone prepare", attrs)
		return nil
	}
	if err := handoff.validateManaged(); err != nil {
		return err
	}
	attrs := map[string]string{
		"server_id":        handoff.ServerID,
		"runtime_agent_id": handoff.RuntimeAgentID,
		"tenant_id":        handoff.TenantID,
		"lease_id":         handoff.LeaseID,
		"remote":           fmt.Sprintf("%t", sshClient != nil),
	}
	if spec != nil {
		attrs["mode"] = spec.EffectiveInstallMode()
	}
	rolloutEvent("techstack.guard", "started", "installing TechStack guard handoff", attrs)
	if prepareDryRun {
		rolloutEvent("techstack.guard", "skipped", "TechStack guard install skipped by dry-run", attrs)
		return nil
	}
	if sshClient != nil {
		if err := installTechStackGuardRemote(ctx, sshClient, handoff); err != nil {
			return err
		}
	} else if err := writeTechStackGuardLocalEvidence(handoff); err != nil {
		return err
	}
	rolloutEvent("techstack.guard", "succeeded", "TechStack guard handoff installed", attrs)
	return nil
}

func installTechStackGuardRemote(ctx context.Context, sshClient *ssh.Client, handoff techStackHandoff) error {
	envContent := techStackGuardEnvFile(handoff)
	scriptContent := techStackGuardScript()
	unitContent := techStackGuardSystemdUnit()
	if err := sshClient.WriteFile(ctx, "/tmp/techstack-guard.env", []byte(envContent), 0600); err != nil {
		return fmt.Errorf("techstack guard env upload failed: %w", err)
	}
	if err := sshClient.WriteFile(ctx, "/tmp/kombify-techstack-guard", []byte(scriptContent), 0755); err != nil {
		return fmt.Errorf("techstack guard script upload failed: %w", err)
	}
	if err := sshClient.WriteFile(ctx, "/tmp/kombify-techstack-guard.service", []byte(unitContent), 0644); err != nil {
		return fmt.Errorf("techstack guard unit upload failed: %w", err)
	}
	cmd := strings.Join([]string{
		"install -d -m 0750 /etc/kombify",
		"install -m 0600 /tmp/techstack-guard.env /etc/kombify/techstack-guard.env",
		"install -m 0755 /tmp/kombify-techstack-guard /usr/local/bin/kombify-techstack-guard",
		"install -m 0644 /tmp/kombify-techstack-guard.service /etc/systemd/system/kombify-techstack-guard.service",
		"rm -f /tmp/techstack-guard.env /tmp/kombify-techstack-guard /tmp/kombify-techstack-guard.service",
		"if command -v systemctl >/dev/null 2>&1; then systemctl daemon-reload && systemctl enable --now kombify-techstack-guard.service; fi",
	}, " && ")
	if _, stderr, err := sshClient.RunWithSudo(ctx, cmd); err != nil {
		return fmt.Errorf("techstack guard service install failed: %w: %s", err, strings.TrimSpace(stderr))
	}
	return nil
}

func writeTechStackGuardLocalEvidence(handoff techStackHandoff) error {
	dir := filepath.Join(getWorkDir(), ".stackkit", "runs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(techStackGuardEvidence(handoff, "local"), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "techstack-guard-evidence.json"), append(data, '\n'), 0600)
}

func techStackGuardEvidence(handoff techStackHandoff, target string) map[string]any {
	return map[string]any{
		"target":              target,
		"server_url":          handoff.ServerURL,
		"server_id":           handoff.ServerID,
		"runtime_agent_id":    handoff.RuntimeAgentID,
		"tenant_id":           handoff.TenantID,
		"owner_id":            handoff.OwnerID,
		"stack_id":            handoff.StackID,
		"lease_id":            handoff.LeaseID,
		"heartbeat_url":       handoff.HeartbeatURL,
		"inventory_url":       handoff.InventoryURL,
		"agent_token_present": handoff.AgentToken != "",
		"installed_at":        time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func techStackGuardEnvFile(handoff techStackHandoff) string {
	lines := []string{
		"TECHSTACK_SERVER_URL=" + shellEnvQuote(handoff.ServerURL),
		"TECHSTACK_SERVER_ID=" + shellEnvQuote(handoff.ServerID),
		"TECHSTACK_RUNTIME_AGENT_ID=" + shellEnvQuote(handoff.RuntimeAgentID),
		"TECHSTACK_AGENT_TOKEN=" + shellEnvQuote(handoff.AgentToken),
		"TECHSTACK_TENANT_ID=" + shellEnvQuote(handoff.TenantID),
		"TECHSTACK_OWNER_ID=" + shellEnvQuote(handoff.OwnerID),
		"TECHSTACK_STACK_ID=" + shellEnvQuote(handoff.StackID),
		"TECHSTACK_LEASE_ID=" + shellEnvQuote(handoff.LeaseID),
		"TECHSTACK_HEARTBEAT_URL=" + shellEnvQuote(handoff.HeartbeatURL),
		"TECHSTACK_INVENTORY_URL=" + shellEnvQuote(handoff.InventoryURL),
		"TECHSTACK_CHANNEL_BOOTSTRAP=" + shellEnvQuote(handoff.ChannelBootstrap),
	}
	return strings.Join(lines, "\n") + "\n"
}

func techStackGuardSystemdUnit() string {
	return `[Unit]
Description=Kombify TechStack Guard
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/etc/kombify/techstack-guard.env
ExecStart=/usr/local/bin/kombify-techstack-guard
Restart=always
RestartSec=15

[Install]
WantedBy=multi-user.target
`
}

func techStackGuardScript() string {
	return `#!/bin/sh
set -eu
interval="${TECHSTACK_GUARD_INTERVAL_SECONDS:-30}"
json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}
post_json() {
  url="$1"
  payload="$2"
  [ -n "$url" ] || return 1
  command -v curl >/dev/null 2>&1 || return 1
  curl -fsS -m 20 -H "Authorization: Bearer ${TECHSTACK_AGENT_TOKEN}" -H "Content-Type: application/json" -d "$payload" "$url" >/dev/null
}
docker_services_json() {
  if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
    printf '[]'
    return
  fi
  ids="$(docker ps -aq --filter label=stackkit.layer 2>/dev/null || true)"
  first=1
  printf '['
  for container_id in $ids; do
    record="$(docker inspect --format '{{.Id}}|{{.Name}}|{{.State.Status}}|{{if .State.Running}}true{{else}}false{{end}}|{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container_id" 2>/dev/null || true)"
    [ -n "$record" ] || continue
    IFS='|' read -r observed_id name state running health <<EOF
$record
EOF
    [ -n "$observed_id" ] || continue
    name="${name#/}"
    service_name="$name"
    [ -n "$service_name" ] || service_name="$observed_id"
    case "$running" in true|false) ;; *) running=false ;; esac
    status=unknown
    failure_class=""
    case "$running:$health:$state" in
      false:*:*|*:unhealthy:*|*:*:exited|*:*:dead|*:*:paused)
        status=unhealthy
        failure_class=container_not_healthy
        ;;
      *:starting:*|*:*:created|*:*:restarting)
        status=starting
        ;;
      true:healthy:*)
        status=healthy
        ;;
    esac
    failure_json=""
    if [ -n "$failure_class" ]; then
      failure_json=",\"failure_class\":\"$(json_escape "$failure_class")\""
    fi
    if [ "$first" -ne 1 ]; then printf ','; fi
    first=0
    printf '{"id":"%s","service_id":"%s","key":"%s","name":"%s","status":"%s","owner_stack":"%s","target_server":"%s","container_id":"%s","health":{"source":"docker","observed_at":"%s","container_state":"%s","docker_health":"%s","running":%s%s}}' \
      "$(json_escape "$observed_id")" \
      "$(json_escape "$service_name")" \
      "$(json_escape "$service_name")" \
      "$(json_escape "$name")" \
      "$(json_escape "$status")" \
      "$(json_escape "${TECHSTACK_STACK_ID:-}")" \
      "$(json_escape "$TECHSTACK_SERVER_ID")" \
      "$(json_escape "$observed_id")" \
      "$(json_escape "$observed_at")" \
      "$(json_escape "$state")" \
      "$(json_escape "$health")" \
      "$running" \
      "$failure_json"
  done
  printf ']'
}
while :; do
  hostname="$(hostname 2>/dev/null || true)"
  os="$(. /etc/os-release 2>/dev/null && printf '%s' "${ID:-linux}" || printf linux)"
  arch="$(uname -m 2>/dev/null || true)"
  cpu="$(getconf _NPROCESSORS_ONLN 2>/dev/null || printf 0)"
  mem_kb="$(awk '/^MemTotal:/ {print $2}' /proc/meminfo 2>/dev/null || printf 0)"
  disk_kb="$(df -Pk / 2>/dev/null | awk 'NR==2 {print $2}' || printf 0)"
  uptime_seconds="$(awk '{print int($1)}' /proc/uptime 2>/dev/null || printf 0)"
  ram_mb=$((mem_kb / 1024))
  disk_gb=$((disk_kb / 1024 / 1024))
  observed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  docker_reachable=false
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then docker_reachable=true; fi
  services="$(docker_services_json)"
  payload="{\"server_id\":\"$(json_escape "$TECHSTACK_SERVER_ID")\",\"runtime_agent_id\":\"$(json_escape "$TECHSTACK_RUNTIME_AGENT_ID")\",\"tenant_id\":\"$(json_escape "${TECHSTACK_TENANT_ID:-}")\",\"owner_id\":\"$(json_escape "${TECHSTACK_OWNER_ID:-}")\",\"stack_id\":\"$(json_escape "${TECHSTACK_STACK_ID:-}")\",\"lease_id\":\"$(json_escape "$TECHSTACK_LEASE_ID")\",\"hostname\":\"$(json_escape "$hostname")\",\"observed_at\":\"$(json_escape "$observed_at")\",\"host\":{\"hostname\":\"$(json_escape "$hostname")\",\"os\":\"$(json_escape "$os")\",\"arch\":\"$(json_escape "$arch")\",\"cpu_cores\":$cpu,\"ram_mb\":$ram_mb,\"disk_gb\":$disk_gb,\"uptime_seconds\":$uptime_seconds,\"docker_reachable\":$docker_reachable},\"channels\":[{\"kind\":\"https\",\"url\":\"$(json_escape "${TECHSTACK_INVENTORY_URL:-}")\",\"status\":\"ok\",\"provenance\":\"stackkit-guard\"}],\"services\":$services}"
  heartbeat_payload="{\"server_id\":\"$(json_escape "$TECHSTACK_SERVER_ID")\",\"runtime_agent_id\":\"$(json_escape "$TECHSTACK_RUNTIME_AGENT_ID")\",\"lease_id\":\"$(json_escape "$TECHSTACK_LEASE_ID")\",\"uptime_seconds\":$uptime_seconds}"
  post_json "${TECHSTACK_INVENTORY_URL:-}" "$payload" || post_json "${TECHSTACK_HEARTBEAT_URL:-}" "$heartbeat_payload" || true
  sleep "$interval"
done
`
}

func shellEnvQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func truthyEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func emitPrepareTelemetryHandshake() {
	attrs := map[string]string{
		"otlp_endpoint_configured": fmt.Sprintf("%t", strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != ""),
		"otlp_headers_configured":  fmt.Sprintf("%t", strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")) != ""),
		"sentry_dsn_configured":    fmt.Sprintf("%t", strings.TrimSpace(os.Getenv("SENTRY_DSN")) != ""),
	}
	if strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) == "" && strings.TrimSpace(os.Getenv("SENTRY_DSN")) == "" {
		rolloutEvent("telemetry.handshake", "skipped", "telemetry endpoint not configured; rollout evidence remains local", attrs)
		return
	}
	rolloutEvent("telemetry.handshake", "succeeded", "telemetry handoff configuration detected", attrs)
}

// cleanupInstallArtifacts removes package manager caches and dangling Docker
// resources left behind by prepare steps (Docker install, image pre-pull,
// image pre-pull). This prevents wasting disk on a space-constrained device.
func cleanupInstallArtifacts(ctx context.Context) {
	printInfo("Cleaning up installation artifacts...")
	var freed int64

	// Clean APT cache (Debian/Ubuntu)
	if _, err := exec.LookPath("apt-get"); err == nil {
		cmd := exec.Command("apt-get", "clean") // #nosec G204
		if err := cmd.Run(); err == nil {
			freed += 100 * 1024 * 1024 // estimate ~100MB
		}
	}

	// Prune dangling Docker images and build cache
	dockerClient := docker.NewClient()
	if dockerClient.IsInstalled() && dockerClient.IsRunning(ctx) {
		if reclaimed, err := dockerClient.Prune(ctx); err == nil && reclaimed > 0 {
			freed += reclaimed
		}
	}

	if freed > 0 {
		printSuccess("Reclaimed ~%d MB of disk space", freed/(1024*1024))
	}
}
