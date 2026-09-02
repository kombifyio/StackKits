package hostconformance

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
)

// StorageFilesystemClass is the bounded classification of the filesystem
// mounted at a resolved storage target. Only local-posix is sufficient for a
// persistent application data root; all other known classes are deliberately
// reported so admission can distinguish a known mismatch from an observation
// that is missing or too unfamiliar to assess.
type StorageFilesystemClass string

const (
	StorageFilesystemLocalPOSIX StorageFilesystemClass = "local-posix"
	StorageFilesystemNetwork    StorageFilesystemClass = "network"
	StorageFilesystemNonPOSIX   StorageFilesystemClass = "non-posix"
	StorageFilesystemEphemeral  StorageFilesystemClass = "ephemeral"
	StorageFilesystemUnknown    StorageFilesystemClass = "unknown"
)

// StorageFilesystemFacts describes the mount selected for one exact storage
// path. MountPoint is diagnostic evidence; callers keep the original contract
// path as the storage identity. A local-posix observation is restricted to
// filesystems with known ownership and persistence semantics.
type StorageFilesystemFacts struct {
	FilesystemType    string
	FilesystemClass   StorageFilesystemClass
	MountPoint        string
	MountSource       string
	SupportsOwnership bool
}

// ObserveStorageFilesystem reads the Linux mount table through the existing
// LocalSource boundary. It does not mount, create, or otherwise mutate a
// filesystem. The longest mountpoint containing the resolved target is used so
// a nested data root cannot accidentally inherit a parent filesystem fact.
func ObserveStorageFilesystem(ctx context.Context, source LocalSource, storagePath string) (StorageFilesystemFacts, error) {
	if source == nil {
		source = osLocalSource{}
	}
	target, err := validateStorageFilesystemPath(storagePath)
	if err != nil {
		return StorageFilesystemFacts{}, err
	}
	resolvedTarget, resolved := resolveStorageFilesystemPath(ctx, source, target)
	if !resolved {
		// Matching the unresolved spelling could select the parent mount when
		// the contract path is a symlink into another filesystem. Preserve an
		// explicit unknown observation so admission remains unverified.
		return StorageFilesystemFacts{FilesystemClass: StorageFilesystemUnknown}, nil
	}
	mountInfo, err := source.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return StorageFilesystemFacts{}, fmt.Errorf("read /proc/self/mountinfo: %w", err)
	}
	mount, ok := longestMountInfoMount(mountInfo, resolvedTarget)
	if !ok {
		return StorageFilesystemFacts{FilesystemClass: StorageFilesystemUnknown}, nil
	}
	fstype := strings.ToLower(strings.TrimSpace(mount.filesystemType))
	if fstype == "" {
		return StorageFilesystemFacts{}, errors.New("mount observation has no filesystem type")
	}
	filesystemClass, supportsOwnership := classifyStorageFilesystem(fstype, mount.source)
	return StorageFilesystemFacts{
		FilesystemType:    fstype,
		FilesystemClass:   filesystemClass,
		MountPoint:        mount.mountPoint,
		MountSource:       mount.source,
		SupportsOwnership: supportsOwnership,
	}, nil
}

type mountInfoMount struct {
	mountPoint     string
	filesystemType string
	source         string
}

func validateStorageFilesystemPath(storagePath string) (string, error) {
	if storagePath == "" || !strings.HasPrefix(storagePath, "/") || strings.ContainsAny(storagePath, "\x00\r\n") {
		return "", fmt.Errorf("storage target path %q is not an absolute path", storagePath)
	}
	cleaned := path.Clean(storagePath)
	if cleaned == "." || !strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("storage target path %q is not an absolute path", storagePath)
	}
	return cleaned, nil
}

// resolveStorageFilesystemPath uses the same LocalSource authority as all
// other host facts. If realpath is unavailable or cannot resolve a target,
// matching is refused because the unresolved spelling could identify a
// different filesystem through a symlink.
func resolveStorageFilesystemPath(ctx context.Context, source LocalSource, storagePath string) (string, bool) {
	if _, err := source.LookPath("realpath"); err != nil {
		return "", false
	}
	output, err := source.Run(ctx, "realpath", "-e", storagePath)
	if err != nil {
		return "", false
	}
	resolved, err := canonicalAbsolutePathOutput(output)
	if err != nil {
		return "", false
	}
	return resolved, true
}

func canonicalAbsolutePathOutput(output []byte) (string, error) {
	value := string(output)
	value = strings.TrimSuffix(value, "\n")
	value = strings.TrimSuffix(value, "\r")
	if value == "" || strings.ContainsAny(value, "\x00\r\n") || !strings.HasPrefix(value, "/") {
		return "", errors.New("realpath returned no canonical absolute path")
	}
	cleaned := path.Clean(value)
	if cleaned == "." || !strings.HasPrefix(cleaned, "/") {
		return "", errors.New("realpath returned no canonical absolute path")
	}
	return cleaned, nil
}

func longestMountInfoMount(data []byte, target string) (mountInfoMount, bool) {
	var selected mountInfoMount
	selectedLength := -1
	ambiguous := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		separator := -1
		for index := 6; index < len(fields); index++ {
			if fields[index] == "-" {
				separator = index
				break
			}
		}
		if separator < 6 || separator+2 >= len(fields) {
			continue
		}
		mountPoint, err := decodeMountInfoField(fields[4])
		if err != nil {
			continue
		}
		mountPoint = path.Clean(mountPoint)
		if mountPoint == "." || !mountPathContains(mountPoint, target) {
			continue
		}
		mountSource, err := decodeMountInfoField(fields[separator+2])
		if err != nil {
			continue
		}
		filesystemType := strings.TrimSpace(fields[separator+1])
		if filesystemType == "" {
			continue
		}
		// Mountinfo does not provide a safe visibility ordering for stacked
		// mounts at one path. Equal-length matches are therefore ambiguous and
		// cannot authorize a filesystem requirement.
		if len(mountPoint) < selectedLength {
			continue
		}
		if len(mountPoint) == selectedLength {
			ambiguous = true
			continue
		}
		selected = mountInfoMount{
			mountPoint:     mountPoint,
			filesystemType: filesystemType,
			source:         mountSource,
		}
		selectedLength = len(mountPoint)
		ambiguous = false
	}
	return selected, selectedLength >= 0 && !ambiguous
}

func mountPathContains(mountPoint, target string) bool {
	if mountPoint == "/" {
		return strings.HasPrefix(target, "/")
	}
	return target == mountPoint || strings.HasPrefix(target, mountPoint+"/")
}

func decodeMountInfoField(value string) (string, error) {
	var decoded strings.Builder
	decoded.Grow(len(value))
	for index := 0; index < len(value); index++ {
		if value[index] != '\\' {
			decoded.WriteByte(value[index])
			continue
		}
		if index+3 >= len(value) {
			return "", errors.New("mountinfo escape is truncated")
		}
		var byteValue byte
		for offset := 1; offset <= 3; offset++ {
			digit := value[index+offset]
			if digit < '0' || digit > '7' {
				return "", errors.New("mountinfo escape is not octal")
			}
			byteValue = byteValue*8 + digit - '0'
		}
		if byteValue == 0 {
			return "", errors.New("mountinfo field contains NUL")
		}
		decoded.WriteByte(byteValue)
		index += 3
	}
	return decoded.String(), nil
}

func classifyStorageFilesystem(filesystemType, mountSource string) (StorageFilesystemClass, bool) {
	filesystemType = strings.ToLower(strings.TrimSpace(filesystemType))
	if _, ok := localPOSIXStorageFilesystems[filesystemType]; ok {
		return StorageFilesystemLocalPOSIX, true
	}
	if _, ok := networkStorageFilesystems[filesystemType]; ok || networkMountSource(mountSource) {
		return StorageFilesystemNetwork, false
	}
	if _, ok := nonPOSIXStorageFilesystems[filesystemType]; ok {
		return StorageFilesystemNonPOSIX, false
	}
	if _, ok := ephemeralStorageFilesystems[filesystemType]; ok {
		return StorageFilesystemEphemeral, false
	}
	return StorageFilesystemUnknown, false
}

func knownStorageFilesystemClass(value string) bool {
	switch StorageFilesystemClass(value) {
	case StorageFilesystemLocalPOSIX, StorageFilesystemNetwork, StorageFilesystemNonPOSIX, StorageFilesystemEphemeral, StorageFilesystemUnknown:
		return true
	default:
		return false
	}
}

var localPOSIXStorageFilesystems = map[string]struct{}{
	"ext2": {}, "ext3": {}, "ext4": {}, "xfs": {}, "btrfs": {}, "zfs": {},
}

var networkStorageFilesystems = map[string]struct{}{
	"9p": {}, "afs": {}, "ceph": {}, "cifs": {}, "fuse.sshfs": {}, "glusterfs": {},
	"nfs": {}, "nfs4": {}, "smb3": {}, "sshfs": {},
}

var nonPOSIXStorageFilesystems = map[string]struct{}{
	"exfat": {}, "fat": {}, "hfs": {}, "hfsplus": {}, "iso9660": {}, "msdos": {},
	"ntfs": {}, "ntfs3": {}, "udf": {}, "vfat": {},
}

var ephemeralStorageFilesystems = map[string]struct{}{
	"aufs": {}, "devtmpfs": {}, "overlay": {}, "ramfs": {}, "squashfs": {}, "tmpfs": {},
}

func networkMountSource(source string) bool {
	source = strings.TrimSpace(strings.ToLower(source))
	return strings.HasPrefix(source, "//") || strings.HasPrefix(source, "nfs:") || strings.HasPrefix(source, "cifs:")
}
