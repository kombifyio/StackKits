package commands

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/system"
	"github.com/kombifyio/stackkits/pkg/models"
)

// getDiskSpace returns available/total GB and mount point for the Docker data directory.
// Checks /var/lib/docker first (may be a separate mount), falls back to /.
func getDiskSpace() (availGB float64, totalGB float64, mount string) {
	for _, path := range []string{"/var/lib/docker", "/"} {
		cmd := exec.Command("df", "-B1", "--output=avail,size,target", path)
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		if a, t, m := parseDfOutput(string(out)); a > 0 || t > 0 {
			return a, t, m
		}
	}
	return 0, 0, ""
}

// parseDfOutput parses `df -B1 --output=avail,size,target` output into GB values.
func parseDfOutput(output string) (availGB float64, totalGB float64, mount string) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return 0, 0, ""
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 3 {
		return 0, 0, ""
	}
	avail, err1 := strconv.ParseFloat(fields[0], 64)
	total, err2 := strconv.ParseFloat(fields[1], 64)
	if err1 != nil || err2 != nil {
		return 0, 0, ""
	}
	return avail / 1024 / 1024 / 1024, total / 1024 / 1024 / 1024, fields[2]
}

// isNoSpaceError checks if a Docker pull error message indicates disk full.
func isNoSpaceError(errMsg string) bool {
	return strings.Contains(errMsg, "no space left on device")
}

// detectLVM checks if the root filesystem is on LVM and returns free VG space.
func detectLVM() (isLVM bool, vgFreeGB float64, lvPath string) {
	// Check if root device is an LVM logical volume
	dfCmd := exec.Command("df", "--output=source", "/")
	dfOut, err := dfCmd.Output()
	if err != nil {
		return false, 0, ""
	}
	lines := strings.Split(strings.TrimSpace(string(dfOut)), "\n")
	if len(lines) < 2 {
		return false, 0, ""
	}
	rootDev := strings.TrimSpace(lines[1])
	// LVM devices appear as /dev/mapper/* or /dev/<vg>/<lv>
	if !strings.HasPrefix(rootDev, "/dev/mapper/") && !strings.Contains(rootDev, "-") {
		return false, 0, ""
	}
	lvPath = rootDev

	// Get VG free space via vgs
	vgsCmd := exec.Command("vgs", "--noheadings", "--nosuffix", "--units", "g", "-o", "vg_free")
	vgsOut, err := vgsCmd.Output()
	if err != nil {
		return true, 0, lvPath
	}
	freeStr := strings.TrimSpace(string(vgsOut))
	// May have multiple VGs; take the first line
	if idx := strings.Index(freeStr, "\n"); idx > 0 {
		freeStr = freeStr[:idx]
	}
	freeStr = strings.TrimSpace(freeStr)
	freeGB, err := strconv.ParseFloat(freeStr, 64)
	if err != nil {
		return true, 0, lvPath
	}
	return true, freeGB, lvPath
}

// tryAutoExtendLVM extends the root LV to use all free VG space, then resizes the filesystem.
// Returns true if the extension succeeded and freed up space.
func tryAutoExtendLVM() bool {
	isLVM, vgFreeGB, lvPath := detectLVM()
	if !isLVM || vgFreeGB < 1.0 || lvPath == "" {
		return false
	}

	printInfo("LVM detected with %.1f GB free — auto-extending %s...", vgFreeGB, lvPath)

	// Extend the LV
	extCmd := exec.Command("lvextend", "-l", "+100%FREE", lvPath)
	if out, err := extCmd.CombinedOutput(); err != nil {
		printWarning("LVM extend failed: %s", strings.TrimSpace(string(out)))
		return false
	}

	// Resize the filesystem (try resize2fs for ext4, then xfs_growfs for XFS)
	resizeCmd := exec.Command("resize2fs", lvPath)
	if out, err := resizeCmd.CombinedOutput(); err != nil {
		// Try XFS
		xfsCmd := exec.Command("xfs_growfs", "/")
		if xfsOut, xfsErr := xfsCmd.CombinedOutput(); xfsErr != nil {
			printWarning("Filesystem resize failed: %s / %s", strings.TrimSpace(string(out)), strings.TrimSpace(string(xfsOut)))
			return false
		}
	}

	// Verify new space
	newAvail, _, mount := getDiskSpace()
	printSuccess("LVM extended — now %.1f GB available on %s", newAvail, mount)
	return true
}

// checkDiskPreFlight runs an early disk space check and attempts LVM auto-extend
// if space is critically low. Uses StackKit requirements if available, otherwise
// falls back to sensible defaults. Returns an error only if space is fatally
// insufficient and cannot be fixed.
func checkDiskPreFlight(reqs *models.Requirements, spec *models.StackSpec, loader *config.Loader) error {
	availGB, totalGB, mount := getDiskSpace()
	if totalGB == 0 {
		// Can't detect disk space (non-Linux) — skip silently
		return nil
	}

	// Use StackKit-declared requirements or fall back to defaults
	minDiskGB := 10.0
	recDiskGB := 20.0
	kitLabel := "StackKit"
	if reqs != nil {
		if reqs.Minimum.Disk > 0 {
			minDiskGB = float64(reqs.Minimum.Disk)
		}
		if reqs.Recommended.Disk > 0 {
			recDiskGB = float64(reqs.Recommended.Disk)
		}
	}

	printInfo("Disk space: %.1f GB available / %.1f GB total on %s", availGB, totalGB, mount)
	if reqs != nil {
		printInfo("  %s requires: minimum %d GB, recommended %d GB", kitLabel, int(minDiskGB), int(recDiskGB))
	}

	if availGB >= minDiskGB {
		if availGB < recDiskGB {
			printWarning("Disk space (%.1f GB) is below recommended %d GB — consider freeing space", availGB, int(recDiskGB))
		}
		return nil
	}

	// Below minimum — try LVM auto-extend
	printWarning("Insufficient disk space: %.1f GB available (minimum: %d GB)", availGB, int(minDiskGB))

	if tryAutoExtendLVM() {
		// Re-check after extend
		newAvail, _, _ := getDiskSpace()
		if newAvail >= minDiskGB {
			return nil
		}
	}

	// Still insufficient — try interactive resolution
	if spec != nil && loader != nil {
		return resolveInsufficientStorage(availGB, minDiskGB, mount, spec, loader)
	}

	// No spec/loader (e.g. remote prepare) — fall back to force check
	if prepareForce {
		printWarning("Insufficient disk space (%.1f GB / %d GB minimum) — continuing anyway (--force)", availGB, int(minDiskGB))
		return nil
	}

	return fmt.Errorf("insufficient disk space: %.1f GB available on %s, %s requires at least %d GB (use --force to override)", availGB, mount, kitLabel, int(minDiskGB))
}

// resolveInsufficientStorage presents interactive options when disk space is below minimum.
func resolveInsufficientStorage(availGB, minDiskGB float64, mount string,
	spec *models.StackSpec, loader *config.Loader) error {

	// Non-interactive: auto-downgrade to low tier
	if prepareForce || !isTerminal() {
		spec.Compute.Tier = models.ComputeTierLow
		printWarning("Auto-downgrading to low compute tier (%.1f GB available, %d GB minimum)", availGB, int(minDiskGB))
		if availGB >= 5.0 {
			saveSpec(spec, loader)
			return nil
		}
		printWarning("Disk space (%.1f GB) is even below low-tier minimum (5 GB) — continuing anyway", availGB)
		saveSpec(spec, loader)
		return nil
	}

	// Detect available block devices
	ctx := context.Background()
	devices, _ := system.DetectBlockDevices(ctx)

	// Determine root device to exclude
	rootDev := ""
	if dfOut, err := exec.Command("df", "--output=source", "/").Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(dfOut)), "\n")
		if len(lines) >= 2 {
			rootDev = strings.TrimSpace(lines[1])
		}
	}
	available := system.FilterAvailableDevices(devices, rootDev)

	// Build choices
	var choices []choice

	for i, dev := range available {
		label := fmt.Sprintf("Use %s (%.0f GB", dev.Path, dev.SizeGB)
		if dev.Model != "" {
			label += ", " + dev.Model
		}
		if dev.Removable {
			label += ", removable"
		}
		label += ")"
		choices = append(choices, choice{
			Key:         fmt.Sprintf("device-%d", i),
			Display:     label,
			Description: "format and mount for Docker storage",
		})
	}

	choices = append(choices, choice{
		Key:         "attach",
		Display:     "Attach external storage",
		Description: "plug in a drive, then re-scan",
	})

	if spec.Compute.Tier != models.ComputeTierLow {
		choices = append(choices, choice{
			Key:         "tier-downgrade",
			Display:     "Downgrade to low compute tier",
			Description: "lighter services (Dockge instead of Dokploy), requires only 5 GB",
			IsDefault:   true,
		})
	}

	choices = append(choices, choice{
		Key:         "force",
		Display:     "Continue anyway",
		Description: "may fail during deploy",
	})

	fmt.Println()
	printInfo("Your device has limited storage. Choose how to proceed:")

	p := newPrompter()
	selected, err := p.selectOne("Storage Resolution", choices)
	if err != nil {
		return fmt.Errorf("storage resolution: %w", err)
	}

	switch {
	case strings.HasPrefix(selected, "device-"):
		// Parse device index
		idxStr := strings.TrimPrefix(selected, "device-")
		idx, _ := strconv.Atoi(idxStr)
		if idx < 0 || idx >= len(available) {
			return fmt.Errorf("invalid device selection")
		}
		dev := available[idx]
		return handleDeviceStorage(ctx, dev, spec, loader)

	case selected == "attach":
		fmt.Println()
		printInfo("Plug in an external drive, then press Enter to re-scan...")
		_, _ = fmt.Scanln()
		newDevices, _ := system.DetectBlockDevices(ctx)
		newAvailable := system.FilterAvailableDevices(newDevices, rootDev)
		if len(newAvailable) == 0 {
			return fmt.Errorf("no new devices detected — re-run stackkit prepare after attaching storage")
		}
		// Use the first new device
		dev := newAvailable[0]
		printInfo("Detected: %s (%.0f GB)", dev.Path, dev.SizeGB)
		return handleDeviceStorage(ctx, dev, spec, loader)

	case selected == "tier-downgrade":
		spec.Compute.Tier = models.ComputeTierLow
		printSuccess("Compute tier set to \"low\" — minimum disk requirement is now 5 GB")
		if availGB < 5.0 {
			printWarning("Disk space (%.1f GB) is still below 5 GB minimum — deploy may fail", availGB)
		}
		saveSpec(spec, loader)
		return nil

	case selected == "force":
		printWarning("Continuing with insufficient disk space (%.1f GB / %d GB minimum)", availGB, int(minDiskGB))
		return nil
	}

	return fmt.Errorf("unexpected selection: %s", selected)
}

// handleDeviceStorage formats/mounts a device or adds it to LVM.
func handleDeviceStorage(ctx context.Context, dev models.BlockDevice,
	spec *models.StackSpec, loader *config.Loader) error {

	isLVM, _, _ := detectLVM()

	if isLVM {
		// Get VG name
		vgsCmd := exec.Command("vgs", "--noheadings", "-o", "vg_name")
		vgsOut, err := vgsCmd.Output()
		if err == nil {
			vgName := strings.TrimSpace(strings.Split(string(vgsOut), "\n")[0])
			if vgName != "" {
				printInfo("Adding %s to LVM volume group %s...", dev.Path, vgName)
				if err := system.AddToLVM(ctx, dev.Path, vgName); err != nil {
					return fmt.Errorf("LVM extend with %s: %w", dev.Path, err)
				}
				newAvail, _, mount := getDiskSpace()
				printSuccess("LVM extended — now %.1f GB available on %s", newAvail, mount)
				spec.Storage.ExternalDevice = dev.Path
				saveSpec(spec, loader)
				return nil
			}
		}
	}

	// Format and mount at /var/lib/docker
	mountPoint := "/var/lib/docker"
	printInfo("Formatting %s and mounting at %s...", dev.Path, mountPoint)
	if err := system.FormatAndMount(ctx, dev.Path, mountPoint); err != nil {
		return fmt.Errorf("format/mount %s: %w", dev.Path, err)
	}
	newAvail, _, _ := getDiskSpace()
	printSuccess("Mounted %s at %s — now %.1f GB available", dev.Path, mountPoint, newAvail)
	spec.Storage.ExternalDevice = dev.Path
	spec.Storage.MountPoint = mountPoint
	saveSpec(spec, loader)
	return nil
}
