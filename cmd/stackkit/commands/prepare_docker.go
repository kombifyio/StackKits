package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/ssh"
	"github.com/kombifyio/stackkits/internal/system"
	"github.com/kombifyio/stackkits/pkg/models"
)

func startDockerDaemon(ctx context.Context) (*models.DockerCapabilities, error) {
	isSystemd := false
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		isSystemd = true
	}

	// Phase 1: Clean up stale state
	os.Remove("/var/run/docker.pid") //nolint:errcheck

	// Phase 2: Load required kernel modules
	loadDockerKernelModules()

	// Phase 3: Detect iptables capability and fix if possible
	iptablesAvailable := ensureIptables()

	// Phase 4: Detect best storage driver
	storageDriver := detectStorageDriver()

	// Phase 5: Detect bridge network capability
	bridgeAvailable := detectBridgeSupport()

	// Phase 6: Write adaptive daemon.json based on system capabilities
	ensureDaemonConfig(iptablesAvailable, storageDriver, bridgeAvailable)

	// Phase 7: Ensure containerd is ready (Docker depends on it)
	ensureContainerd(isSystemd)

	// Phase 8: Enable Docker service
	if isSystemd {
		exec.Command("systemctl", "enable", "docker").Run()        //nolint:errcheck
		exec.Command("systemctl", "enable", "docker.socket").Run() //nolint:errcheck
	}

	virtType := detectVirtualization()
	unshareOK := testUnshare()
	tier := classifyCompatibilityTier(virtType, unshareOK, bridgeAvailable, storageDriver != models.StorageVFS)

	caps := &models.DockerCapabilities{
		BridgeNetworking:   bridgeAvailable,
		Iptables:           iptablesAvailable,
		StorageDriver:      storageDriver,
		VirtualizationType: virtType,
		CompatibilityTier:  tier,
		UnshareAvailable:   unshareOK,
		CgroupVersion:      detectCgroupVersion(),
	}

	// Phase 9: Start Docker
	if err := tryStartDocker(ctx, isSystemd); err == nil {
		return caps, nil
	}

	// Phase 10: First start failed — read logs, attempt targeted fixes, retry
	logOutput := getDockerLogs(isSystemd)
	needsRestart := false

	// Auto-fix: bridge creation blocked
	if strings.Contains(logOutput, "Failed to create bridge") ||
		strings.Contains(logOutput, "error creating default \"bridge\" network") {
		if bridgeAvailable {
			printWarning("Bridge network creation failed — disabling default bridge...")
			bridgeAvailable = false
			caps.BridgeNetworking = false
			needsRestart = true
		}
	}

	// Auto-fix: iptables/ip6tables failure in Docker logs
	if strings.Contains(logOutput, "iptables") && strings.Contains(logOutput, "Permission denied") {
		if iptablesAvailable {
			printWarning("Docker failed due to iptables — disabling...")
			iptablesAvailable = false
			caps.Iptables = false
			needsRestart = true
		}
	}

	// Auto-fix: storage driver failure (only match actual driver init errors)
	if strings.Contains(logOutput, "error initializing graphdriver") ||
		strings.Contains(logOutput, "driver not supported") {
		if storageDriver != models.StorageVFS {
			fallbackDriver := models.StorageFuseOverlay
			if _, err := exec.LookPath("fuse-overlayfs"); err != nil {
				fallbackDriver = models.StorageVFS
			}
			printWarning("Storage driver %q failed — switching to %q...", storageDriver, fallbackDriver)
			storageDriver = fallbackDriver
			caps.StorageDriver = storageDriver
			os.RemoveAll("/var/lib/docker/overlay2") //nolint:errcheck
			os.RemoveAll("/var/lib/docker/network")  //nolint:errcheck
			needsRestart = true
		}
	}

	// Apply fixes and retry
	if needsRestart {
		writeDaemonJSON(iptablesAvailable, storageDriver, bridgeAvailable)
		stopDocker(isSystemd)
		if err := tryStartDocker(ctx, isSystemd); err == nil {
			printSuccess("Docker started after auto-fix")
			return caps, nil
		}
		logOutput = getDockerLogs(isSystemd)
	}

	// All auto-fixes exhausted — report clearly
	printWarning("Docker failed to start — diagnostics:")
	fmt.Println(logOutput)
	if _, err := os.Stat("/var/run/docker.sock"); os.IsNotExist(err) {
		printWarning("Docker socket /var/run/docker.sock does not exist")
	}
	return nil, fmt.Errorf("docker daemon failed to start — see diagnostics above")
}

// detectCapabilities probes the system for Docker-relevant capabilities
// without restarting the daemon. Used when Docker is already running.
func detectCapabilities() *models.DockerCapabilities {
	bridge := detectBridgeSupport()
	storage := detectStorageDriver()
	virtType := detectVirtualization()
	unshareOK := testUnshare()
	tier := classifyCompatibilityTier(virtType, unshareOK, bridge, storage != models.StorageVFS)

	availGB, totalGB, mount := getDiskSpace()
	isLVM, _, _ := detectLVM()

	return &models.DockerCapabilities{
		BridgeNetworking:   bridge,
		Iptables:           testIptablesNAT(),
		StorageDriver:      storage,
		VirtualizationType: virtType,
		CompatibilityTier:  tier,
		UnshareAvailable:   unshareOK,
		CgroupVersion:      detectCgroupVersion(),
		MemoryLimits:       true,
		DiskTotalGB:        totalGB,
		DiskAvailGB:        availGB,
		DiskMount:          mount,
		LVMDetected:        isLVM,
		CPUCores:           system.DetectCPUCores(),
		MemoryGB:           system.DetectMemoryGB(),
	}
}

// writeDockerCapabilities persists detected Docker capabilities for use by generate.
func writeDockerCapabilities(caps *models.DockerCapabilities) {
	if caps == nil {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".stackkits")
	os.MkdirAll(dir, 0755) //nolint:errcheck

	data, err := json.MarshalIndent(caps, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(filepath.Join(dir, "capabilities.json"), data, 0644) //nolint:errcheck
}

// testDockerRuntime verifies Docker can actually create and run containers.
// On heavily restricted VPS (OpenVZ/LXC), the Docker daemon starts but
// the kernel blocks the unshare() syscall, making it impossible to create
// namespaces for containers or even register image layers.
// Returns true if Docker is functional, false if the VPS is incompatible.
func testDockerRuntime(ctx context.Context, caps *models.DockerCapabilities) bool {
	printInfo("Testing Docker container runtime...")

	// Try the simplest possible container operation: pull + run hello-world
	// This tests both layer registration (unshare for storage) and container
	// creation (unshare for namespaces).
	testCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(testCtx, "docker", "run", "--rm", "hello-world") // #nosec G204
	output, err := cmd.CombinedOutput()
	outputStr := strings.TrimSpace(string(output))

	if err == nil {
		printSuccess("Docker runtime is functional")
		caps.DockerFunctional = true
		caps.UnshareAvailable = true
		caps.MemoryLimits = testDockerMemoryLimits(ctx)
		if caps.CompatibilityTier == "" {
			caps.CompatibilityTier = models.TierFull
		}
		return true
	}

	// Check for the specific unshare/namespace error that indicates
	// a fundamentally incompatible VPS (OpenVZ/LXC without nesting).
	if strings.Contains(outputStr, "unshare") ||
		strings.Contains(outputStr, "operation not permitted") ||
		strings.Contains(outputStr, "failed to register layer") {
		caps.DockerFunctional = false
		caps.UnshareAvailable = false
		caps.CompatibilityTier = models.TierIncompatible
		caps.RuntimeError = "kernel blocks container namespaces (unshare: operation not permitted)"
		if caps.VirtualizationType == "" {
			caps.VirtualizationType = detectVirtualization()
		}

		fmt.Println()
		printError("%s", "Docker cannot run containers on this VPS")
		fmt.Println()
		fmt.Printf("  Virtualization: %s\n", caps.VirtualizationType)
		fmt.Println("  Your VPS uses container-based virtualization that blocks")
		fmt.Println("  the kernel features Docker needs (namespaces, cgroups, unshare).")
		fmt.Println("  The Docker daemon starts, but no containers can be created.")
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
		fmt.Println("  Run 'stackkit compat' for a full compatibility report.")
		fmt.Println()
		return false
	}

	// Some other error (network, disk, etc.) — not a fatal runtime issue,
	// let the rest of prepare handle it.
	printWarning("Docker test container failed: %s", outputStr)
	caps.DockerFunctional = true // Assume functional, may be transient
	caps.MemoryLimits = true
	return true
}

func testDockerMemoryLimits(ctx context.Context) bool {
	testCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(testCtx, "docker", "run", "--rm", "--memory", "32m", "hello-world") // #nosec G204
	output, err := cmd.CombinedOutput()
	if err == nil {
		return true
	}

	outputStr := strings.TrimSpace(string(output))
	if outputStr == "" {
		outputStr = err.Error()
	}
	printWarning("Docker memory limits are unavailable; generated local containers will omit memory limits: %s", firstLine(outputStr))
	return false
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return s
}

// testDockerDNS verifies DNS resolution works inside Docker containers.
// On restricted VPS (OpenVZ/LXC), Docker's internal DNS resolver depends on
// netfilter/conntrack. When iptables is disabled, container DNS breaks even
// though the host OS DNS works fine.
func testDockerDNS(ctx context.Context, caps *models.DockerCapabilities) *models.DockerCapabilities {
	if caps == nil {
		caps = &models.DockerCapabilities{}
	}

	printInfo("Testing Docker DNS resolution...")

	// Test: DNS lookup inside a container with explicit DNS server
	testCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(testCtx, "docker", "run", "--rm", "--dns", "1.1.1.1",
		"alpine:3.19", "nslookup", "registry-1.docker.io")
	output, err := cmd.CombinedOutput()

	if err == nil {
		printSuccess("Docker DNS is working")
		caps.DNSWorking = true
		caps.DNSFix = models.DNSFixNone
		return caps
	}

	printWarning("Docker DNS is not working: %s", strings.TrimSpace(string(output)))

	// Fix 1: Inject DNS servers into daemon.json and restart
	printInfo("Configuring explicit DNS servers (1.1.1.1, 8.8.8.8)...")
	applyDNSToDaemonJSON()
	restartDockerForDNS(ctx)

	// Re-test
	testCtx2, cancel2 := context.WithTimeout(ctx, 30*time.Second)
	defer cancel2()
	cmd2 := exec.CommandContext(testCtx2, "docker", "run", "--rm",
		"alpine:3.19", "nslookup", "registry-1.docker.io")
	if cmd2.Run() == nil {
		printSuccess("Docker DNS fixed (configured explicit DNS servers)")
		caps.DNSWorking = true
		caps.DNSFix = "daemon-json"
		return caps
	}

	// DNS still broken — we'll rely on pre-pulling from the host
	printWarning("Docker DNS remains unavailable (restricted VPS)")
	printInfo("Images will be pre-pulled from the host network")
	caps.DNSWorking = false
	caps.DNSFix = "host-prepull"
	return caps
}

// applyDNSToDaemonJSON injects explicit DNS servers into /etc/docker/daemon.json.
func applyDNSToDaemonJSON() {
	daemonCfg, err := os.ReadFile("/etc/docker/daemon.json")
	if err != nil {
		return
	}

	var cfg map[string]interface{}
	if json.Unmarshal(daemonCfg, &cfg) != nil {
		return
	}

	if _, hasDNS := cfg["dns"]; hasDNS {
		return // DNS already configured
	}

	cfg["dns"] = []string{"1.1.1.1", "8.8.8.8"}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile("/etc/docker/daemon.json", data, 0644) //nolint:errcheck
}

// restartDockerForDNS restarts Docker daemon to apply DNS config changes.
func restartDockerForDNS(ctx context.Context) {
	isSystemd := false
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		isSystemd = true
	}
	stopDocker(isSystemd)
	tryStartDocker(ctx, isSystemd) //nolint:errcheck
}

// baseKitImages returns the Docker images used by the basement-kit for a given compute tier.
// Coolify is installed through its official installer, but its runtime images
// are still pre-pulled for the default standard tier so registry/auth/rate-limit
// problems fail early in stackkit prepare instead of halfway through apply.
// Empty tier returns ALL images (used for destroy/cleanup), unless an explicit
// installer service profile scopes the prepare pass before a spec exists.
func baseKitImages(tier string) []string {
	adminOnly := strings.EqualFold(strings.TrimSpace(os.Getenv("STACKKIT_SERVICE_PROFILE")), "admin-only")
	platform := selectedPrePullPlatform()

	// Common images across all tiers
	images := []string{
		"ghcr.io/traefik/traefik:v3",
		"ghcr.io/steveiliop56/tinyauth:v5.0.7",
		"ghcr.io/pocket-id/pocket-id:v2.7.0",
		"public.ecr.aws/docker/library/nginx:alpine",
		"ghcr.io/gethomepage/homepage:latest",
		"ghcr.io/tecnativa/docker-socket-proxy:latest",
		"ghcr.io/traefik/whoami:latest",
		"registry.k8s.io/coredns/coredns:v1.11.3",
	}

	if tier == models.ComputeTierLow {
		// Low compute: keep diagnostics and the default identity stack, but skip
		// heavier app images.
		images = append(images,
			"ghcr.io/louislam/uptime-kuma:2.0.2",
			"public.ecr.aws/docker/library/python:3.11-alpine",
		)
		if !adminOnly {
			images = append(images,
				"cloudreve/cloudreve:latest",
			)
		}
	} else if tier == "" && !adminOnly {
		// All images (for destroy/cleanup/recovery)
		images = append(images,
			"public.ecr.aws/docker/library/postgres:16-alpine",
			"public.ecr.aws/docker/library/postgres:15-alpine",
			"public.ecr.aws/docker/library/redis:7-alpine",
			"public.ecr.aws/docker/library/busybox:latest",
			"ghcr.io/coollabsio/coolify-helper:1.0.13",
			"ghcr.io/coollabsio/coolify-realtime:1.0.16",
			"ghcr.io/coollabsio/sentinel:0.0.21",
			"ghcr.io/coollabsio/coolify:4.1.2",
			"public.ecr.aws/docker/library/mongo:7",
			"ghcr.io/moghtech/komodo-core:2",
			"ghcr.io/moghtech/komodo-periphery:2",
			"smallstep/step-ca:0.30.2",
			"dokploy/dokploy:latest",
			"public.ecr.aws/docker/library/node:22-alpine",
			"ghcr.io/louislam/uptime-kuma:2.0.2",
			"public.ecr.aws/docker/library/python:3.11-alpine",
			"ghcr.io/dani-garcia/vaultwarden:latest",
			"cloudreve/cloudreve:latest",
			"nextcloud:30-apache",
			"mariadb:11",
			"redis:7-alpine",
			"ghcr.io/immich-app/immich-server:release",
			"ghcr.io/immich-app/immich-machine-learning:release",
			"ghcr.io/immich-app/postgres:16-vectorchord0.3.0-pgvectors0.3.0",
			"ghcr.io/valkey-io/valkey:9",
			"louislam/dockge:1",
		)
	} else {
		// Standard/high: selected platform baseline, monitoring, and default apps.
		if !adminOnly {
			images = append(images,
				"ghcr.io/immich-app/immich-server:release",
				"ghcr.io/immich-app/immich-machine-learning:release",
				"ghcr.io/immich-app/postgres:16-vectorchord0.3.0-pgvectors0.3.0",
				"ghcr.io/valkey-io/valkey:9",
			)
		}
		images = append(images, platformPrePullImages(platform)...)
		images = append(images,
			"ghcr.io/louislam/uptime-kuma:2.0.2",
			"public.ecr.aws/docker/library/python:3.11-alpine",
		)
		if !adminOnly {
			images = append(images,
				"ghcr.io/dani-garcia/vaultwarden:latest",
				"cloudreve/cloudreve:latest",
			)
		}
	}

	if image := strings.TrimSpace(os.Getenv("STACKKIT_SERVER_IMAGE")); image != "" && shouldPrePullImage(image) {
		images = append(images, image)
	}

	images = prioritizePrePullImages(images)
	return images
}

func prioritizePrePullImages(images []string) []string {
	priority := []string{
		"ghcr.io/immich-app/immich-server:release",
		"ghcr.io/immich-app/immich-machine-learning:release",
		"ghcr.io/coollabsio/coolify:4.1.2",
		"ghcr.io/gethomepage/homepage:latest",
		"cloudreve/cloudreve:latest",
		"ghcr.io/dani-garcia/vaultwarden:latest",
		"ghcr.io/louislam/uptime-kuma:2.0.2",
		"ghcr.io/immich-app/postgres:16-vectorchord0.3.0-pgvectors0.3.0",
		"ghcr.io/coollabsio/coolify-helper:1.0.13",
		"ghcr.io/coollabsio/coolify-realtime:1.0.16",
		"ghcr.io/coollabsio/sentinel:0.0.21",
	}
	available := make(map[string]bool, len(images))
	for _, image := range images {
		available[image] = true
	}
	prioritized := make([]string, 0, len(images))
	used := make(map[string]bool, len(images))
	for _, image := range priority {
		if available[image] {
			prioritized = append(prioritized, image)
			used[image] = true
		}
	}
	for _, image := range images {
		if !used[image] {
			prioritized = append(prioritized, image)
		}
	}
	return prioritized
}

func selectedPrePullPlatform() string {
	platform := strings.TrimSpace(os.Getenv("STACKKIT_PLATFORM"))
	if platform == "" {
		platform = strings.TrimSpace(os.Getenv("STACKKIT_PAAS"))
	}
	platform = strings.ToLower(platform)
	switch platform {
	case "komodo", "dokploy", "coolify", "none":
		return platform
	default:
		return "coolify"
	}
}

func platformPrePullImages(platform string) []string {
	switch platform {
	case "komodo":
		return []string{
			"public.ecr.aws/docker/library/mongo:7",
			"ghcr.io/moghtech/komodo-core:2",
			"ghcr.io/moghtech/komodo-periphery:2",
			"smallstep/step-ca:0.30.2",
		}
	case "dokploy":
		return []string{
			"public.ecr.aws/docker/library/postgres:16-alpine",
			"public.ecr.aws/docker/library/redis:7-alpine",
			"dokploy/dokploy:latest",
			"public.ecr.aws/docker/library/node:22-alpine",
		}
	case "none":
		return nil
	default:
		return []string{
			"public.ecr.aws/docker/library/postgres:15-alpine",
			"public.ecr.aws/docker/library/redis:7-alpine",
			"public.ecr.aws/docker/library/busybox:latest",
			"ghcr.io/coollabsio/coolify-helper:1.0.13",
			"ghcr.io/coollabsio/coolify-realtime:1.0.16",
			"ghcr.io/coollabsio/sentinel:0.0.21",
			"ghcr.io/coollabsio/coolify:4.1.2",
		}
	}
}

func shouldPrePullImage(image string) bool {
	image = strings.TrimSpace(image)
	if image == "" {
		return false
	}
	return strings.Contains(image, "/")
}

// prePullImages pulls all basement-kit Docker images from the host network.
// This is critical on restricted VPS where container DNS is broken — the host
// network has working DNS, so `docker pull` from the host succeeds.
const (
	defaultPrePullImageTimeout = 2 * time.Minute
	defaultPrePullPhaseBudget  = 10 * time.Minute
	defaultPrePullConcurrency  = 3
	defaultPrePullRetries      = 1
)

type prePullResult struct {
	index    int
	image    string
	pulled   bool
	failed   bool
	diskFull bool
	message  string
}

func prePullImages(ctx context.Context, caps *models.DockerCapabilities, computeTier string) {
	images := baseKitImages(computeTier)
	perImageTimeout := dockerPrePullImageTimeout()
	phaseBudget := dockerPrePullPhaseBudget()
	concurrency := dockerPrePullConcurrency(len(images))
	retries := dockerPrePullRetries()
	printInfo("Pre-pulling %d Docker images (budget %s, per image %s, concurrency %d, retries %d)...", len(images), phaseBudget, perImageTimeout, concurrency, retries)

	phaseCtx, phaseCancel := context.WithTimeout(ctx, phaseBudget)
	defer phaseCancel()

	resultsByIndex := make([]prePullResult, len(images))
	indexes := make([]int, 0, len(images))
	for index := range images {
		indexes = append(indexes, index)
	}
	diskFull := false
	_ = runPrePullPass(phaseCtx, images, indexes, concurrency, perImageTimeout, retries, func(result prePullResult) {
		resultsByIndex[result.index] = result
		if result.diskFull {
			diskFull = true
			phaseCancel()
		}
		if result.pulled {
			fmt.Printf("  [%d/%d] %s ✓\n", result.index+1, len(images), result.image)
			return
		}
		fmt.Printf("  [%d/%d] %s ✗\n", result.index+1, len(images), result.image)
		if result.message != "" {
			printWarning("    Failed: %s", result.message)
		}
	})

	pulled := []string{}
	failed := []string{}
	for _, result := range resultsByIndex {
		if result.pulled {
			pulled = append(pulled, result.image)
		} else if result.failed {
			failed = append(failed, result.image)
		}
	}

	caps.PrePulledImages = pulled
	caps.PrePullFailed = failed

	if len(failed) == 0 {
		printSuccess("All %d images pre-pulled", len(pulled))
	} else if diskFull {
		fmt.Println()
		printError("Disk full — stopped pulling images (%d/%d pulled)", len(pulled), len(images))
		availGB, _, mount := getDiskSpace()
		printInfo("  Available: %.0f MB on %s", availGB*1024, mount)
		if isLVM, vgFreeGB, lvPath := detectLVM(); isLVM && vgFreeGB > 1.0 {
			printInfo("  LVM detected: %.1f GB free in volume group — extend and retry:", vgFreeGB)
			printInfo("    sudo lvextend -l +100%%FREE %s && sudo resize2fs %s", lvPath, lvPath)
			printInfo("    stackkit prepare")
		} else {
			printInfo("  Free up disk space or add a larger disk, then run: stackkit prepare")
		}
		fmt.Println()
	} else {
		printWarning("%d images pulled, %d failed", len(pulled), len(failed))
	}
}

func runPrePullPass(ctx context.Context, images []string, indexes []int, concurrency int, perImageTimeout time.Duration, retries int, onResult func(prePullResult)) []prePullResult {
	if len(indexes) == 0 {
		return nil
	}
	if concurrency > len(indexes) {
		concurrency = len(indexes)
	}
	jobs := make(chan int, len(indexes))
	results := make(chan prePullResult, len(indexes))
	for worker := 0; worker < concurrency; worker++ {
		go func() {
			for index := range jobs {
				results <- pullDockerImage(ctx, index, images[index], perImageTimeout, retries)
			}
		}()
	}
	for _, index := range indexes {
		jobs <- index
	}
	close(jobs)

	passResults := make([]prePullResult, 0, len(indexes))
	for range indexes {
		result := <-results
		if onResult != nil {
			onResult(result)
		}
		passResults = append(passResults, result)
	}
	return passResults
}

func retryablePrePullFailure(ctx context.Context, result prePullResult) bool {
	if !result.failed || result.diskFull || ctx.Err() != nil {
		return false
	}
	message := strings.ToLower(result.message)
	if strings.Contains(message, "pre-pull budget exhausted") ||
		strings.Contains(message, "manifest unknown") ||
		strings.Contains(message, "pull access denied") ||
		strings.Contains(message, "repository does not exist") {
		return false
	}
	return true
}

var (
	dockerImagePull = pullDockerImageOnce
	dockerImageTag  = tagDockerImage
)

func pullDockerImage(ctx context.Context, index int, image string, perImageTimeout time.Duration, retries int) prePullResult {
	result := pullDockerImageWithRetries(ctx, index, image, perImageTimeout, retries)
	if result.pulled || !rateLimitedPrePullFailure(result) || ctx.Err() != nil {
		return result
	}
	for _, mirror := range prePullMirrorImages(image) {
		mirrorResult := pullDockerImageWithRetries(ctx, index, mirror, perImageTimeout, retries)
		if !mirrorResult.pulled {
			if mirrorResult.message != "" {
				result.message = fmt.Sprintf("%s; mirror %s failed: %s", result.message, mirror, mirrorResult.message)
			}
			continue
		}
		if err := dockerImageTag(ctx, mirror, image); err != nil {
			return prePullResult{
				index:   index,
				image:   image,
				failed:  true,
				message: fmt.Sprintf("docker tag mirror %s as %s failed: %v", mirror, image, err),
			}
		}
		return prePullResult{index: index, image: image, pulled: true}
	}
	return result
}

func pullDockerImageWithRetries(ctx context.Context, index int, image string, perImageTimeout time.Duration, retries int) prePullResult {
	var result prePullResult
	for attempt := 0; attempt <= retries; attempt++ {
		result = dockerImagePull(ctx, index, image, perImageTimeout)
		if result.pulled || !retryablePrePullFailure(ctx, result) || attempt == retries {
			return result
		}
	}
	return result
}

func rateLimitedPrePullFailure(result prePullResult) bool {
	if !result.failed {
		return false
	}
	message := strings.ToLower(result.message)
	return strings.Contains(message, "toomanyrequests") ||
		strings.Contains(message, "rate exceeded") ||
		strings.Contains(message, "pull rate limit") ||
		strings.Contains(message, "rate limit reached")
}

func prePullMirrorImages(image string) []string {
	const dockerLibraryECRPrefix = "public.ecr.aws/docker/library/"
	image = strings.TrimSpace(image)
	if strings.HasPrefix(image, dockerLibraryECRPrefix) {
		mirror := strings.TrimPrefix(image, dockerLibraryECRPrefix)
		if mirror != "" {
			return []string{mirror}
		}
		return nil
	}
	if strings.Contains(image, "/") {
		return nil
	}
	name := officialLibraryImageName(image)
	if _, ok := officialLibraryMirrorNames[name]; ok {
		return []string{dockerLibraryECRPrefix + image}
	}
	return nil
}

func officialLibraryImageName(image string) string {
	name := strings.SplitN(image, "@", 2)[0]
	if index := strings.Index(name, ":"); index >= 0 {
		name = name[:index]
	}
	return name
}

var officialLibraryMirrorNames = map[string]struct{}{
	"busybox":   {},
	"mariadb":   {},
	"mongo":     {},
	"nextcloud": {},
	"nginx":     {},
	"node":      {},
	"postgres":  {},
	"python":    {},
	"redis":     {},
}

func pullDockerImageOnce(ctx context.Context, index int, image string, perImageTimeout time.Duration) prePullResult {
	result := prePullResult{index: index, image: image}
	if ctx.Err() != nil {
		result.failed = true
		result.message = "pre-pull budget exhausted"
		return result
	}

	if availGB, _, _ := getDiskSpace(); availGB > 0 && availGB < 1.0 {
		result.failed = true
		result.diskFull = true
		result.message = fmt.Sprintf("skipped — %.0f MB disk remaining", availGB*1024)
		return result
	}

	pullTimeout := perImageTimeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			result.failed = true
			result.message = "pre-pull budget exhausted"
			return result
		}
		if remaining < pullTimeout {
			pullTimeout = remaining
		}
	}

	pullCtx, cancel := context.WithTimeout(ctx, pullTimeout)
	defer cancel()

	cmd := exec.CommandContext(pullCtx, "docker", "pull", image) // #nosec G204
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		result.failed = true
		errMsg := strings.TrimSpace(stderr.String())
		switch {
		case ctx.Err() == context.DeadlineExceeded:
			errMsg = "pre-pull budget exhausted"
		case pullCtx.Err() == context.DeadlineExceeded:
			errMsg = fmt.Sprintf("docker pull timed out after %s", pullTimeout)
		case pullCtx.Err() == context.Canceled:
			errMsg = "pre-pull canceled"
		}
		result.message = errMsg
		result.diskFull = isNoSpaceError(errMsg)
		return result
	}

	result.pulled = true
	return result
}

func tagDockerImage(ctx context.Context, source, target string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	tagCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(tagCtx, "docker", "tag", source, target) // #nosec G204
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		switch {
		case ctx.Err() == context.DeadlineExceeded:
			errMsg = "pre-pull budget exhausted"
		case tagCtx.Err() == context.DeadlineExceeded:
			errMsg = "docker tag timed out after 30s"
		case tagCtx.Err() == context.Canceled:
			errMsg = "docker tag canceled"
		}
		if errMsg != "" {
			return fmt.Errorf("%w: %s", err, errMsg)
		}
		return err
	}
	return nil
}

func dockerPrePullImageTimeout() time.Duration {
	return durationFromEnv(defaultPrePullImageTimeout, "STACKKIT_PREPULL_IMAGE_TIMEOUT", "STACKKIT_PREPULL_IMAGE_TIMEOUT_SECONDS")
}

func dockerPrePullPhaseBudget() time.Duration {
	return durationFromEnv(defaultPrePullPhaseBudget, "STACKKIT_PREPULL_BUDGET", "STACKKIT_PREPULL_BUDGET_SECONDS")
}

func dockerPrePullConcurrency(imageCount int) int {
	if imageCount <= 0 {
		return 1
	}
	value := strings.TrimSpace(os.Getenv("STACKKIT_PREPULL_CONCURRENCY"))
	if value == "" {
		if imageCount < defaultPrePullConcurrency {
			return imageCount
		}
		return defaultPrePullConcurrency
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		printWarning("Ignoring invalid STACKKIT_PREPULL_CONCURRENCY=%q", value)
		return defaultPrePullConcurrency
	}
	if parsed > 8 {
		parsed = 8
	}
	if parsed > imageCount {
		return imageCount
	}
	return parsed
}

func dockerPrePullRetries() int {
	value := strings.TrimSpace(os.Getenv("STACKKIT_PREPULL_RETRIES"))
	if value == "" {
		return defaultPrePullRetries
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		printWarning("Ignoring invalid STACKKIT_PREPULL_RETRIES=%q", value)
		return defaultPrePullRetries
	}
	if parsed > 3 {
		return 3
	}
	return parsed
}

func dockerPrePullRequired() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("STACKKIT_PREPULL_REQUIRED")))
	switch value {
	case "1", "true", "yes", "required", "strict":
		return true
	default:
		return false
	}
}

func durationFromEnv(fallback time.Duration, names ...string) time.Duration {
	for _, name := range names {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			continue
		}
		if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
		if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
			return duration
		}
		printWarning("Ignoring invalid duration %s=%q", name, value)
	}
	return fallback
}

// loadDockerKernelModules loads kernel modules required by Docker networking and storage.
func loadDockerKernelModules() {
	modules := []string{
		"ip_tables",      // base iptables
		"iptable_nat",    // NAT table (port mapping)
		"iptable_filter", // filter table
		"nf_nat",         // netfilter NAT core
		"br_netfilter",   // bridge netfilter (container traffic)
		"overlay",        // overlay2 storage driver
		"bridge",         // bridge networking
	}
	for _, mod := range modules {
		if err := exec.Command("modprobe", mod).Run(); err != nil {
			printWarning("modprobe %s failed: %v (non-fatal, may be built-in)", mod, err)
		}
	}

	// Enable IPv4 forwarding (required for container networking)
	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0644); err != nil { //nolint:gosec
		printWarning("failed to enable IPv4 forwarding: %v", err)
	}
}

// ensureIptables tests iptables NAT support, switching backends if needed.
// Returns true if iptables NAT works, false if Docker must run without it.
func ensureIptables() bool {
	// Test 1: Does iptables NAT work with the current backend?
	if testIptablesNAT() {
		return true
	}

	// Test 2: Try switching to iptables-legacy
	printWarning("iptables NAT failed — trying iptables-legacy...")
	if switchToIptablesLegacy() {
		if testIptablesNAT() {
			printSuccess("iptables-legacy works")
			return true
		}
		printWarning("iptables-legacy also failed")
	}

	// Test 3: iptables is completely broken on this system
	printWarning("iptables NAT unavailable — Docker will run without iptables management")
	return false
}

// testIptablesNAT checks if iptables can access the NAT table.
func testIptablesNAT() bool {
	cmd := exec.Command("iptables", "--wait", "-t", "nat", "-L", "-n")
	return cmd.Run() == nil
}

// switchToIptablesLegacy switches iptables from nf_tables to legacy backend.
func switchToIptablesLegacy() bool {
	cmds := []struct{ name, link string }{
		{"iptables", "/usr/sbin/iptables-legacy"},
		{"ip6tables", "/usr/sbin/ip6tables-legacy"},
	}
	success := false
	for _, c := range cmds {
		if _, err := os.Stat(c.link); err == nil {
			cmd := exec.Command("update-alternatives", "--set", c.name, c.link)
			if err := cmd.Run(); err == nil {
				success = true
			}
		}
	}
	return success
}

// detectStorageDriver tests which Docker storage driver the system supports.
func detectStorageDriver() string {
	// Test overlay2: try mounting an overlay filesystem
	testDir := "/tmp/stackkit-overlay-test"
	os.MkdirAll(testDir+"/lower", 0755)  //nolint:errcheck
	os.MkdirAll(testDir+"/upper", 0755)  //nolint:errcheck
	os.MkdirAll(testDir+"/work", 0755)   //nolint:errcheck
	os.MkdirAll(testDir+"/merged", 0755) //nolint:errcheck
	defer os.RemoveAll(testDir)          //nolint:errcheck

	mountCmd := exec.Command("mount", "-t", "overlay", "overlay",
		"-o", fmt.Sprintf("lowerdir=%s/lower,upperdir=%s/upper,workdir=%s/work", testDir, testDir, testDir),
		testDir+"/merged")
	if mountCmd.Run() == nil {
		exec.Command("umount", testDir+"/merged").Run() //nolint:errcheck
		return models.StorageOverlay2
	}

	// overlay2 not supported — try fuse-overlayfs
	if _, err := exec.LookPath("fuse-overlayfs"); err == nil {
		printInfo("overlay2 not available — using fuse-overlayfs")
		return models.StorageFuseOverlay
	}

	// Last resort: vfs (no copy-on-write, uses more disk, but works everywhere)
	printWarning("overlay2 not available — using vfs storage driver (slower, uses more disk)")
	return models.StorageVFS
}

// detectBridgeSupport tests if the kernel allows creating bridge network interfaces.
func detectBridgeSupport() bool {
	testBridge := "sk-br-test"
	createCmd := exec.Command("ip", "link", "add", "name", testBridge, "type", "bridge")
	if err := createCmd.Run(); err != nil {
		printWarning("Bridge networking not available — Docker will use host networking")
		return false
	}
	exec.Command("ip", "link", "delete", testBridge).Run() //nolint:errcheck
	return true
}

// ensureDaemonConfig writes /etc/docker/daemon.json adapted to system capabilities.
func ensureDaemonConfig(iptablesAvailable bool, storageDriver string, bridgeAvailable bool) {
	// Only preserve existing config if it wasn't written by stackkit
	if _, err := os.Stat("/etc/docker/daemon.json"); err == nil {
		existing, readErr := os.ReadFile("/etc/docker/daemon.json")
		if readErr == nil && len(existing) > 5 && !strings.Contains(string(existing), "max-concurrent-downloads") {
			// User has a custom config (not ours) — respect it
			return
		}
	}
	writeDaemonJSON(iptablesAvailable, storageDriver, bridgeAvailable)
}

// writeDaemonJSON writes an adaptive daemon.json based on system capabilities.
func writeDaemonJSON(iptablesAvailable bool, storageDriver string, bridgeAvailable bool) {
	os.MkdirAll("/etc/docker", 0755) //nolint:errcheck

	iptablesStr := "true"
	ip6tablesStr := "true"
	if !iptablesAvailable {
		iptablesStr = "false"
		ip6tablesStr = "false"
	}

	// Detect DNS: systemd-resolved uses 127.0.0.53 which doesn't work in containers
	dnsLine := ""
	if resolv, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		if strings.Contains(string(resolv), "127.0.0.53") {
			dnsLine = `  "dns": ["1.1.1.1", "8.8.8.8"],` + "\n"
		}
	}

	// Bridge config: disable default bridge if kernel blocks it
	bridgeLine := ""
	if !bridgeAvailable {
		bridgeLine = `  "bridge": "none",` + "\n"
	}

	config := fmt.Sprintf(`{
  "storage-driver": "%s",
  "iptables": %s,
  "ip6tables": %s,
%s%s  "live-restore": true,
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "10m",
    "max-file": "3"
  },
  "max-concurrent-downloads": 3
}`, storageDriver, iptablesStr, ip6tablesStr, bridgeLine, dnsLine)

	os.WriteFile("/etc/docker/daemon.json", []byte(config), 0644) //nolint:errcheck

	details := fmt.Sprintf("storage=%s, iptables=%s", storageDriver, iptablesStr)
	if !bridgeAvailable {
		details += ", bridge=none"
	}
	printInfo("Configured /etc/docker/daemon.json (%s)", details)
}

// ensureContainerd starts containerd and waits for its socket to be ready.
func ensureContainerd(isSystemd bool) {
	if isSystemd {
		exec.Command("systemctl", "enable", "containerd").Run() //nolint:errcheck
		exec.Command("systemctl", "start", "containerd").Run()  //nolint:errcheck
	} else {
		exec.Command("service", "containerd", "start").Run() //nolint:errcheck
	}

	// Wait for containerd socket (up to 10s)
	for i := 0; i < 10; i++ {
		if _, err := os.Stat("/run/containerd/containerd.sock"); err == nil {
			return
		}
		time.Sleep(1 * time.Second)
	}
}

// tryStartDocker starts the Docker service and waits up to 30s for it to respond.
func tryStartDocker(ctx context.Context, isSystemd bool) error {
	os.Remove("/var/run/docker.pid") //nolint:errcheck

	if isSystemd {
		cmd := exec.Command("systemctl", "start", "docker")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
	} else {
		cmd := exec.Command("service", "docker", "start")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
	}

	dockerClient := docker.NewClient()
	for i := 0; i < 30; i++ {
		if dockerClient.IsRunning(ctx) {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("timeout waiting for Docker daemon (30s)")
}

// stopDocker stops the Docker daemon for a clean restart.
func stopDocker(isSystemd bool) {
	if isSystemd {
		exec.Command("systemctl", "stop", "docker").Run()        //nolint:errcheck
		exec.Command("systemctl", "stop", "docker.socket").Run() //nolint:errcheck
	} else {
		exec.Command("service", "docker", "stop").Run() //nolint:errcheck
	}
	os.Remove("/var/run/docker.pid") //nolint:errcheck
	time.Sleep(2 * time.Second)
}

// getDockerLogs returns recent Docker daemon log output.
func getDockerLogs(isSystemd bool) string {
	if isSystemd {
		cmd := exec.Command("journalctl", "-u", "docker", "--no-pager", "-n", "30")
		output, _ := cmd.CombinedOutput()
		return string(output)
	}
	for _, logFile := range []string{"/var/log/docker.log", "/var/log/syslog"} {
		if _, err := os.Stat(logFile); err == nil {
			cmd := exec.Command("tail", "-30", logFile)
			output, _ := cmd.CombinedOutput()
			return string(output)
		}
	}
	return "no Docker logs found"
}

func installDockerLocal(ctx context.Context) error {
	cmd := exec.Command("sh", "-c", packageManagerLockWaitScript()+"\ncurl -fsSL https://get.docker.com | sh")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	// Enable and start Docker daemon — this must succeed for deployment to work
	caps, err := startDockerDaemon(ctx)
	if err != nil {
		return fmt.Errorf("docker installed but failed to start: %w", err)
	}
	writeDockerCapabilities(caps)
	return nil
}

func installDockerRemote(ctx context.Context, client *ssh.Client, osType string) error {
	var installCmd string

	switch osType {
	case "ubuntu", "debian":
		installCmd = packageManagerLockWaitScript() + `
curl -fsSL https://get.docker.com | sh
systemctl enable docker
systemctl start docker
`
	case "rocky", "centos", "rhel", "fedora":
		installCmd = `
dnf config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo
dnf install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
systemctl enable docker
systemctl start docker
`
	default:
		return fmt.Errorf("unsupported OS for automatic Docker installation: %s", osType)
	}

	_, stderr, err := client.RunWithSudo(ctx, installCmd)
	if err != nil {
		return fmt.Errorf("install failed: %w: %s", err, stderr)
	}

	return nil
}

func packageManagerLockWaitScript() string {
	return `if command -v apt-get >/dev/null 2>&1; then
  for i in $(seq 1 72); do
    if command -v fuser >/dev/null 2>&1 && fuser /var/lib/dpkg/lock-frontend /var/lib/dpkg/lock /var/cache/apt/archives/lock >/dev/null 2>&1; then
      echo "Waiting for apt/dpkg lock to be released..."
      sleep 5
      continue
    fi
    if pgrep -x apt-get >/dev/null 2>&1 || pgrep -x apt >/dev/null 2>&1 || pgrep -x dpkg >/dev/null 2>&1 || pgrep -x unattended-upgr >/dev/null 2>&1; then
      echo "Waiting for apt/dpkg process to finish..."
      sleep 5
      continue
    fi
    break
  done
  if command -v fuser >/dev/null 2>&1 && fuser /var/lib/dpkg/lock-frontend /var/lib/dpkg/lock /var/cache/apt/archives/lock >/dev/null 2>&1; then
    echo "Timed out waiting for apt/dpkg lock" >&2
    exit 1
  fi
fi`
}
