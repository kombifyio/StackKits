package system

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/pkg/models"
)

// lsblkOutput matches the JSON output of lsblk --json.
type lsblkOutput struct {
	BlockDevices []lsblkDevice `json:"blockdevices"`
}

type lsblkDevice struct {
	Name       string  `json:"name"`
	Path       string  `json:"path"`
	Size       float64 `json:"size"` // bytes when using -b
	Type       string  `json:"type"`
	Mountpoint *string `json:"mountpoint"`
	FSType     *string `json:"fstype"`
	Model      *string `json:"model"`
	RM         bool    `json:"rm"`
}

// DetectBlockDevices discovers block devices on the host using lsblk.
func DetectBlockDevices(ctx context.Context) ([]models.BlockDevice, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "lsblk", "--json", "-b",
		"-o", "NAME,PATH,SIZE,TYPE,MOUNTPOINT,FSTYPE,MODEL,RM")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("lsblk: %w", err)
	}

	return ParseLsblkOutput(out)
}

// ParseLsblkOutput parses the JSON output of lsblk into BlockDevice models.
func ParseLsblkOutput(data []byte) ([]models.BlockDevice, error) {
	var parsed lsblkOutput
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse lsblk: %w", err)
	}

	devices := make([]models.BlockDevice, 0, len(parsed.BlockDevices))
	for _, d := range parsed.BlockDevices {
		dev := models.BlockDevice{
			Name:      d.Name,
			Path:      d.Path,
			SizeGB:    d.Size / 1024 / 1024 / 1024,
			Type:      d.Type,
			Removable: d.RM,
		}
		if d.Mountpoint != nil {
			dev.Mountpoint = *d.Mountpoint
		}
		if d.FSType != nil {
			dev.FSType = *d.FSType
		}
		if d.Model != nil {
			dev.Model = strings.TrimSpace(*d.Model)
		}
		devices = append(devices, dev)
	}
	return devices, nil
}

// FilterAvailableDevices returns devices that are unmounted, have no filesystem,
// are not the root disk, and are at least 1 GB.
func FilterAvailableDevices(devices []models.BlockDevice, rootDevice string) []models.BlockDevice {
	var available []models.BlockDevice
	for _, d := range devices {
		if d.Type != "disk" {
			continue
		}
		if d.Mountpoint != "" {
			continue
		}
		if d.FSType != "" {
			continue
		}
		if d.SizeGB < 1.0 {
			continue
		}
		// Skip the root disk (e.g. "sda" matches "/dev/sda")
		if rootDevice != "" && strings.Contains(rootDevice, d.Name) {
			continue
		}
		available = append(available, d)
	}
	return available
}

// FormatAndMount formats a device with ext4, mounts it, and adds an fstab entry.
func FormatAndMount(ctx context.Context, devicePath, mountPoint string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Format
	cmd := exec.CommandContext(ctx, "mkfs.ext4", "-F", devicePath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mkfs.ext4 %s: %s: %w", devicePath, strings.TrimSpace(string(out)), err)
	}

	// Create mount point
	cmd = exec.CommandContext(ctx, "mkdir", "-p", mountPoint)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mkdir %s: %w", mountPoint, err)
	}

	// Mount
	cmd = exec.CommandContext(ctx, "mount", devicePath, mountPoint)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mount %s: %s: %w", devicePath, strings.TrimSpace(string(out)), err)
	}

	// Get UUID for fstab
	cmd = exec.CommandContext(ctx, "blkid", "-s", "UUID", "-o", "value", devicePath)
	uuidOut, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("blkid %s: %w", devicePath, err)
	}
	uuid := strings.TrimSpace(string(uuidOut))
	if uuid == "" {
		return fmt.Errorf("could not determine UUID for %s", devicePath)
	}

	// Append to fstab
	fstabLine := fmt.Sprintf("UUID=%s %s ext4 defaults,nofail 0 2\n", uuid, mountPoint)
	cmd = exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf("echo %q >> /etc/fstab", fstabLine))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("fstab update: %w", err)
	}

	return nil
}

// AddToLVM adds a device as a physical volume to the given volume group,
// then extends the root logical volume and resizes the filesystem.
func AddToLVM(ctx context.Context, devicePath, vgName string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// pvcreate
	cmd := exec.CommandContext(ctx, "pvcreate", devicePath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pvcreate %s: %s: %w", devicePath, strings.TrimSpace(string(out)), err)
	}

	// vgextend
	cmd = exec.CommandContext(ctx, "vgextend", vgName, devicePath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("vgextend %s %s: %s: %w", vgName, devicePath, strings.TrimSpace(string(out)), err)
	}

	// Find root LV
	cmd = exec.CommandContext(ctx, "df", "--output=source", "/")
	dfOut, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("detect root LV: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(dfOut)), "\n")
	if len(lines) < 2 {
		return fmt.Errorf("could not determine root device")
	}
	rootLV := strings.TrimSpace(lines[1])

	// lvextend
	cmd = exec.CommandContext(ctx, "lvextend", "-l", "+100%FREE", rootLV)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("lvextend %s: %s: %w", rootLV, strings.TrimSpace(string(out)), err)
	}

	// resize2fs (try ext4 first, then xfs_growfs)
	cmd = exec.CommandContext(ctx, "resize2fs", rootLV)
	if out, err := cmd.CombinedOutput(); err != nil {
		cmd = exec.CommandContext(ctx, "xfs_growfs", "/")
		if xfsOut, xfsErr := cmd.CombinedOutput(); xfsErr != nil {
			return fmt.Errorf("resize failed: ext4=%s, xfs=%s",
				strings.TrimSpace(string(out)), strings.TrimSpace(string(xfsOut)))
		}
	}

	return nil
}
