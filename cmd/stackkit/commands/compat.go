package commands

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
)

var compatProviders bool

var compatCmd = &cobra.Command{
	Use:   "compat",
	Short: "Check VPS compatibility with StackKits",
	Long: `Run a quick, non-destructive check of your VPS compatibility with Docker and StackKits.

This checks:
  • Virtualization type (KVM, OpenVZ, LXC, bare metal)
  • unshare(2) syscall availability
  • OverlayFS support
  • Bridge networking support
  • iptables NAT support
  • Cgroup version

Examples:
  stackkit compat              Check current system
  stackkit compat --providers  Show provider compatibility matrix`,
	RunE: runCompat,
}

func init() {
	compatCmd.Flags().BoolVar(&compatProviders, "providers", false, "Show provider compatibility matrix")
}

func runCompat(cmd *cobra.Command, args []string) error {
	if compatProviders {
		printProviderMatrix()
		return nil
	}

	fmt.Println()
	fmt.Println(bold("VPS Compatibility Check"))
	fmt.Println()

	// Detect virtualization
	virtType := detectVirtualization()
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
		fmt.Printf("  Tier: %s — all StackKit features will work\n", green("Full"))
	case models.TierDegraded:
		fmt.Printf("  Tier: %s — Docker works with automatic workarounds\n", yellow("Degraded"))
		printDegradedDetails(overlayOK, bridgeOK, iptablesOK, storageDriver)
	case models.TierIncompatible:
		fmt.Printf("  Tier: %s — Docker cannot run on this VPS\n", red("Incompatible"))
		fmt.Println()
		fmt.Println("  This VPS cannot run StackKits. See 'stackkit compat --providers' for")
		fmt.Println("  recommended VPS providers that support Docker.")
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
	fmt.Println("  Workarounds that will be applied:")
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

type providerInfo struct {
	name  string
	virt  string
	tier  models.CompatibilityTier
	price string
	notes string
}

func printProviderMatrix() {
	providers := []providerInfo{
		{"Hetzner Cloud", "KVM", models.TierFull, "~$4/mo", "hetzner.cloud"},
		{"DigitalOcean", "KVM", models.TierFull, "~$4/mo", "digitalocean.com"},
		{"Linode (Akamai)", "KVM", models.TierFull, "~$5/mo", "linode.com"},
		{"Vultr", "KVM", models.TierFull, "~$5/mo", "vultr.com"},
		{"Contabo (KVM)", "KVM", models.TierFull, "~$5/mo", "contabo.com"},
		{"OVH Cloud", "KVM", models.TierFull, "~$4/mo", "ovhcloud.com"},
		{"Scaleway", "KVM", models.TierFull, "~$4/mo", "scaleway.com"},
		{"Oracle Free (ARM)", "KVM", models.TierFull, "Free", "cloud.oracle.com"},
		{"Proxmox LXC (nested)", "LXC", models.TierDegraded, "varies", "nesting=true required"},
		{"Contabo (OpenVZ)", "OpenVZ", models.TierIncompatible, "~$3/mo", "unshare blocked"},
		{"Hostinger VPS", "OpenVZ/LXC", models.TierIncompatible, "~$3/mo", "unshare blocked"},
		{"Budget $2-3 VPS", "OpenVZ", models.TierIncompatible, "~$2/mo", "unshare blocked"},
		{"Proxmox LXC (restricted)", "LXC", models.TierIncompatible, "varies", "nesting=false"},
	}

	fmt.Println()
	fmt.Println(bold("VPS Provider Compatibility Matrix"))
	fmt.Println()
	fmt.Printf("  %-25s %-12s %-15s %-10s %s\n", "Provider", "Virt", "Tier", "Price", "Notes")
	fmt.Printf("  %-25s %-12s %-15s %-10s %s\n", strings.Repeat("─", 25), strings.Repeat("─", 12), strings.Repeat("─", 15), strings.Repeat("─", 10), strings.Repeat("─", 25))

	for _, p := range providers {
		tierStr := ""
		switch p.tier {
		case models.TierFull:
			tierStr = green("Full")
		case models.TierDegraded:
			tierStr = yellow("Degraded")
		case models.TierIncompatible:
			tierStr = red("Incompatible")
		}
		fmt.Printf("  %-25s %-12s %-15s %-10s %s\n", p.name, p.virt, tierStr, p.price, p.notes)
	}

	fmt.Println()
	fmt.Println("  " + green("Full") + "          Docker works perfectly, all features")
	fmt.Println("  " + yellow("Degraded") + "      Docker works with auto-workarounds (vfs, host network)")
	fmt.Println("  " + red("Incompatible") + "  Kernel blocks unshare — Docker cannot run at all")
	fmt.Println()
}
