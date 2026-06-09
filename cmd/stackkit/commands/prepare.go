package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/netenv"
	"github.com/kombifyio/stackkits/internal/rollout"
	"github.com/kombifyio/stackkits/internal/ssh"
	"github.com/kombifyio/stackkits/internal/tofu"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
)

var (
	prepareHost       string
	prepareUser       string
	prepareKey        string
	prepareDryRun     bool
	prepareSkipDocker bool
	prepareSkipTofu   bool
	prepareAutoFix    bool
	prepareForce      bool
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
	prepareCmd.Flags().BoolVar(&prepareDryRun, "dry-run", false, "Show what would be done")
	prepareCmd.Flags().BoolVar(&prepareSkipDocker, "skip-docker", false, "Skip Docker installation check")
	prepareCmd.Flags().BoolVar(&prepareSkipTofu, "skip-tofu", false, "Skip packaged OpenTofu check")
	prepareCmd.Flags().BoolVar(&prepareAutoFix, "auto-fix", true, "Auto-correct fixable issues")
	prepareCmd.Flags().BoolVar(&prepareForce, "force", false, "Continue even with insufficient disk space")
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
	}

	// Network environment detection + NodeContext resolution
	if !prepareDryRun {
		printInfo("Detecting network environment...")
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
	}

	// Early disk space pre-flight: check before installing anything.
	// If critically low, tries LVM auto-extend or offers interactive resolution.
	if !prepareDryRun {
		if err := checkDiskPreFlight(reqs, spec, loader); err != nil {
			return fmt.Errorf("system preparation failed: %w", err)
		}
	}

	// Check Docker
	if !prepareSkipDocker {
		printInfo("Checking Docker installation...")
		dockerClient := docker.NewClient()

		installed := dockerClient.IsInstalled()
		if !installed {
			if prepareDryRun {
				printWarning("Docker not installed - would install")
			} else {
				printInfo("Installing Docker...")
				if err := installDockerLocal(ctx); err != nil {
					return fmt.Errorf("failed to install Docker: %w", err)
				}
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
	}

	// Docker runtime + DNS test + image pre-pull (after Docker, before OpenTofu)
	if !prepareSkipDocker && !prepareDryRun {
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
				return err
			}
			return prepareNativeMode(ctx, spec, loader)
		}
		deployLog.Event("prepare.docker_runtime",
			slog.Bool("success", true),
		)

		caps = testDockerDNS(ctx, caps)
		if shouldPrePullImages() {
			prePullImages(ctx, caps, preparePrePullComputeTier(spec))
			if len(caps.PrePullFailed) > 0 && dockerPrePullRequired() {
				return fmt.Errorf("docker image pre-pull failed for %d required images", len(caps.PrePullFailed))
			}
		} else {
			printInfo("Skipping optional Docker image pre-pull (STACKKIT_PREPULL_IMAGES=false)")
		}
		writeDockerCapabilities(caps)
	}

	// Check packaged OpenTofu
	if err := ensurePackagedOpenTofu(ctx); err != nil {
		return err
	}

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
	checkLocalResources(reqs)
	deployLog.Event("prepare.resources_checked")

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
	if err := ensurePackagedOpenTofu(ctx); err != nil {
		return err
	}

	// Check system resources
	printInfo("Checking system resources...")
	checkLocalResources(nil)

	if prepareDryRun {
		printInfo("Dry run complete - no changes made")
	} else {
		printSuccess("System is ready for native StackKit deployment (no Docker)")
	}

	return nil
}

func prepareRemoteSystem(ctx context.Context, spec *models.StackSpec) error {
	printInfo("Preparing remote host: %s", prepareHost)

	// Set SSH options
	opts := []ssh.ClientOption{
		ssh.WithHost(prepareHost),
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
		return fmt.Errorf("failed to connect to %s: %w", prepareHost, err)
	}
	defer func() { _ = sshClient.Close() }()

	printSuccess("Connected to %s", prepareHost)

	// Get system info
	printInfo("Gathering system information...")
	sysInfo, err := sshClient.GetSystemInfo(ctx)
	if err != nil {
		printWarning("Could not get full system info: %v", err)
	} else {
		printSuccess("OS: %s %s (%s)", sysInfo.OS, sysInfo.OSVersion, sysInfo.Arch)
		printSuccess("CPU: %d cores, RAM: %d MB, Disk: %d GB free",
			sysInfo.CPUCores, sysInfo.MemoryMB, sysInfo.DiskGB)
	}

	// Check Docker
	if err := checkRemoteDocker(ctx, sshClient, sysInfo); err != nil {
		return err
	}

	// Install StackKit-packaged OpenTofu on the target.
	if err := checkRemoteTofu(ctx, sshClient, sysInfo); err != nil {
		return err
	}

	// Check ports
	if spec != nil {
		printInfo("Checking required ports...")
		requiredPorts := []int{80, 443}
		for _, port := range requiredPorts {
			if sshClient.CheckPort(ctx, port) {
				printSuccess("Port %d is available", port)
			} else {
				printWarning("Port %d is in use", port)
			}
		}
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
		return nil
	}
	if sysInfo.DockerVersion != "" {
		printSuccess("Docker %s installed", sysInfo.DockerVersion)
		return nil
	}
	if prepareDryRun {
		printWarning("Docker not installed - would install")
		return nil
	}
	printInfo("Installing Docker...")
	if err := installDockerRemote(ctx, sshClient, sysInfo.OS); err != nil {
		return fmt.Errorf("failed to install Docker: %w", err)
	}
	printSuccess("Docker installed successfully")
	return nil
}

func checkRemoteTofu(ctx context.Context, sshClient *ssh.Client, sysInfo *models.SystemInfo) error {
	if prepareSkipTofu {
		return nil
	}

	if prepareDryRun {
		printWarning("StackKit-packaged OpenTofu would be installed on the remote target")
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

func checkLocalResources(reqs *models.Requirements) {
	// CPU check
	numCPU := runtime.NumCPU()
	printSuccess("CPU: %d cores", numCPU)

	// Memory check — use runtime.MemStats for Go-accessible info,
	// then read system total from OS-specific /proc/meminfo on Linux.
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	sysMB := m.Sys / 1024 / 1024
	if sysMB > 0 {
		printSuccess("Go runtime memory: %d MB allocated", sysMB)
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
