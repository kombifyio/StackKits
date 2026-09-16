package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	stackkitdocs "github.com/kombifyio/stackkits/docs/data/os-compat"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
)

var compatCmd = &cobra.Command{
	Use:   "compat",
	Short: "Show OS support evidence and host conformance diagnostics",
	Long: `Show the published StackKits compatibility evidence for this operating system
and its hypervisor, then run non-destructive diagnostics for local container-host
prerequisites.

The host diagnostics check:
  • Virtualization type (KVM, OpenVZ, LXC, bare metal)
  • unshare(2) syscall availability
  • OverlayFS support
  • Bridge networking support
  • iptables NAT support
  • Cgroup version

Published evidence comes from automated lifecycle receipts bound to a release.
Host diagnostics do not certify, rank, recommend, or price any server provider.`,
	Example: `  # Check the published OS and hypervisor evidence and the host container prerequisites
  stackkit compat`,
	Args: cobra.NoArgs,
	RunE: runCompat,
}

func runCompat(cmd *cobra.Command, args []string) error {
	virtType := detectVirtualization()
	fmt.Println()
	fmt.Println(bold("StackKits OS Compatibility"))
	fmt.Println()
	printCurrentOSEvidence()

	fmt.Println()
	fmt.Println(bold("Host Conformance Diagnostics"))
	fmt.Println("  Local prerequisite probes only; this is not server-provider certification.")
	fmt.Println()

	printCompatLine("Virtualization", virtType, virtType == models.VirtKVM || virtType == models.VirtNone)

	// Test unshare
	unshareOK := testUnshare()
	printCompatLine("unshare(2)", boolToStatus(unshareOK), unshareOK)

	// Test overlay2
	storageDriver := detectStorageDriver()
	overlayOK := storageDriver == models.StorageOverlay2
	printCompatLine("overlay2", storageDriver, overlayOK)

	// Test bridge networking
	bridgeOK := detectBridgeSupport()
	printCompatLine("bridge networking", boolToStatus(bridgeOK), bridgeOK)

	// Test iptables NAT
	iptablesOK := testIptablesNAT()
	printCompatLine("iptables NAT", boolToStatus(iptablesOK), iptablesOK)

	// Cgroup version
	cgroupVer := detectCgroupVersion()
	printCompatLine("cgroups", cgroupVer, true)

	// Classify tier
	tier := classifyCompatibilityTier(virtType, unshareOK, bridgeOK, overlayOK)
	fmt.Println()

	switch tier {
	case models.TierFull:
		fmt.Printf("  Host tier: %s — required container-host capabilities detected\n", green("Full"))
	case models.TierDegraded:
		fmt.Printf("  Host tier: %s — container runtime needs the listed fallbacks\n", yellow("Degraded"))
		printDegradedDetails(overlayOK, bridgeOK, iptablesOK, storageDriver)
	case models.TierIncompatible:
		fmt.Printf("  Host tier: %s — required container-host capabilities are missing\n", red("Incompatible"))
		fmt.Println()
		fmt.Println("  Use a host that exposes namespaces, cgroups, storage, and networking")
		fmt.Println("  prerequisites, or configure the native runtime explicitly.")
	}
	fmt.Println()

	// Check if Docker is installed and running
	if path, err := exec.LookPath("docker"); err == nil {
		printCompatLine("Docker binary", path, true)
		dockerCmd := exec.Command("docker", "info", "--format", "{{.ServerVersion}}")
		if out, err := dockerCmd.Output(); err == nil {
			printCompatLine("Docker daemon", strings.TrimSpace(string(out)), true)
		} else {
			printCompatLine("Docker daemon", "not running", false)
		}
	}

	return nil
}

type compatOSIdentity struct {
	ID           string
	DistroFamily string
	Version      string
	Arch         string
}

func printCurrentOSEvidence() {
	identity := detectCompatOS()
	fmt.Printf("  Detected OS:          %s\n", identity.ID)

	raw, err := stackkitdocs.FS.ReadFile("latest.json")
	if err != nil {
		fmt.Printf("  Published evidence:   %s (matrix unavailable)\n", yellow("unverified"))
		return
	}
	var matrix osMatrixDoc
	if err := json.Unmarshal(raw, &matrix); err != nil {
		fmt.Printf("  Published evidence:   %s (matrix unreadable)\n", yellow("unverified"))
		return
	}

	if row, ok := findCompatOSEvidence(matrix, identity); ok {
		fmt.Printf("  Published evidence:   %s (%s)%s\n", compatEvidenceStatus(row.Grade), matrix.StackKitsVersion, compatEvidenceNote(row.osMatrixEvidence))
	} else {
		fmt.Printf("  Published evidence:   %s — no exact OS family/distribution/version row\n", yellow("unverified"))
	}

	if hypervisorHost := detectCompatHypervisorHost(); hypervisorHost != "" {
		fmt.Printf("  Hypervisor host:      %s\n", hypervisorHost)
		fmt.Println("  StackKits run in a guest VM on this hypervisor, not on the host itself.")
		if row, ok := findCompatHypervisorRow(matrix, hypervisorHost); ok {
			fmt.Printf("  Hypervisor rollout:   %s (%s)%s\n", compatEvidenceStatus(row.Grade), matrix.StackKitsVersion, compatEvidenceNote(row.osMatrixEvidence))
			if row.Rollout != "" {
				fmt.Printf("  How:                  %s\n", row.Rollout)
			}
		}
	}
}

func compatEvidenceNote(evidence osMatrixEvidence) string {
	if evidence.Grade == "supported" || len(evidence.ReasonCodes) == 0 {
		return ""
	}
	note := " — " + osMatrixReason(evidence.ReasonCodes[0])
	if evidence.LastVerifiedRelease != "" {
		note += "; last verified " + evidence.LastVerifiedRelease
	}
	return note
}

// detectCompatHypervisorHost reports the hypervisor this host itself runs, so
// the operator learns to install StackKits into a guest VM instead.
func detectCompatHypervisorHost() string {
	if _, err := os.Stat("/etc/pve"); err == nil {
		return "Proxmox VE"
	}
	if _, err := exec.LookPath("pveversion"); err == nil {
		return "Proxmox VE"
	}
	return ""
}

func findCompatHypervisorRow(matrix osMatrixDoc, name string) (osMatrixVirtRow, bool) {
	for _, row := range matrix.Virtualization {
		if strings.EqualFold(row.Name, name) {
			return row, true
		}
	}
	return osMatrixVirtRow{}, false
}

func findCompatOSEvidence(matrix osMatrixDoc, identity compatOSIdentity) (osMatrixDocRow, bool) {
	for _, row := range matrix.Results {
		if strings.EqualFold(row.OS.Family, "linux") &&
			strings.EqualFold(row.OS.Distribution, identity.DistroFamily) &&
			row.OS.Version == identity.Version {
			return row, true
		}
	}
	return osMatrixDocRow{}, false
}

func compatEvidenceStatus(status string) string {
	switch status {
	case "supported":
		return green(status)
	case "preview":
		return yellow(status)
	case "unsupported":
		return red(status)
	default:
		return yellow("unverified")
	}
}

func detectCompatOS() compatOSIdentity {
	raw, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return compatOSIdentity{
			ID:           runtime.GOOS + "-" + runtime.GOARCH,
			DistroFamily: runtime.GOOS,
			Arch:         runtime.GOARCH,
		}
	}
	return parseCompatOSRelease(raw, runtime.GOOS, runtime.GOARCH)
}

func parseCompatOSRelease(raw []byte, goos, arch string) compatOSIdentity {
	values := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, "\"'")
		values[strings.TrimSpace(key)] = value
	}

	family := strings.ToLower(strings.TrimSpace(values["ID"]))
	if family == "" {
		family = strings.ToLower(strings.TrimSpace(goos))
	}
	version := strings.TrimSpace(values["VERSION_ID"])
	id := family
	if version != "" {
		id += "-" + version
	}
	if arch != "" {
		id += "-" + arch
	}
	return compatOSIdentity{ID: id, DistroFamily: family, Version: version, Arch: arch}
}

func printCompatLine(label, value string, ok bool) {
	status := green("✓")
	if !ok {
		status = red("✗")
	}
	fmt.Printf("  %-20s %s %s\n", label+":", value, status)
}

func boolToStatus(b bool) string {
	if b {
		return "available"
	}
	return "unavailable"
}

func printDegradedDetails(overlayOK, bridgeOK, iptablesOK bool, storageDriver string) {
	fmt.Println("  Required host fallbacks:")
	if !overlayOK {
		fmt.Printf("    • Storage driver: %s (instead of overlay2)\n", storageDriver)
	}
	if !bridgeOK {
		fmt.Println("    • Host networking (instead of bridge)")
	}
	if !iptablesOK {
		fmt.Println("    • Docker iptables management disabled")
	}
}
