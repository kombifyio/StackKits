package backuplifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// VerifyApplicationTransferStaging rechecks extracted content at its consuming
// boundary. ContentVerified from a prior process is never sufficient authority.
func VerifyApplicationTransferStaging(target string, manifest EmergencyManifest, private ...map[string][]byte) error {
	root, err := confinedfs.Open(target)
	if err != nil {
		return err
	}
	defer root.Close()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	expected := map[string]EmergencyEntry{}
	for _, entry := range manifest.Entries {
		if expected[entry.Path].Path != "" {
			return errors.New("duplicate transfer entry")
		}
		expected[entry.Path] = entry
		if len(private) > 0 {
			if data, ok := private[0][entry.Path]; ok {
				sum := sha256.Sum256(data)
				if entry.Directory || int64(len(data)) != entry.Bytes || hex.EncodeToString(sum[:]) != entry.SHA256 {
					return errors.New("private transfer entry content differs")
				}
				continue
			}
		}
		info, err := view.Lstat(entry.Path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || entry.Directory != info.IsDir() || (!entry.Directory && !info.Mode().IsRegular()) {
			return errors.New("transfer staging type changed")
		}
		if !entry.Directory {
			file, err := view.Open(entry.Path)
			if err != nil {
				return err
			}
			hash := sha256.New()
			n, readErr := io.Copy(hash, file)
			closeErr := file.Close()
			if err = errors.Join(readErr, closeErr); err != nil {
				return err
			}
			if n != entry.Bytes || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
				return errors.New("transfer staging content changed")
			}
		}
	}
	for _, source := range manifest.Sources {
		if source.Coverage != "included" {
			return errors.New("application transfer may not omit selected data")
		}
		err = filepath.WalkDir(filepath.Join(target, filepath.FromSlash(source.ArchivePath)), func(name string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, e := filepath.Rel(target, name)
			if e != nil {
				return e
			}
			relative = filepath.ToSlash(relative)
			if _, ok := expected[relative]; !ok {
				return errors.New("transfer staging contains unlisted data")
			}
			if entry.Type()&os.ModeSymlink != 0 || (!entry.IsDir() && !entry.Type().IsRegular()) {
				return errors.New("transfer staging contains link or special entry")
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return root.VerifyPathIdentity()
}

// RestoreApplicationTransferMetadata restores only metadata of authenticated,
// task-owned extracted application files. Emergency recovery itself keeps its
// historically conservative private staging modes.
func RestoreApplicationTransferMetadata(target string, manifest EmergencyManifest, private ...map[string][]byte) error {
	if err := VerifyApplicationTransferStaging(target, manifest, private...); err != nil {
		return err
	}
	for i := len(manifest.Entries) - 1; i >= 0; i-- {
		entry := manifest.Entries[i]
		if len(private) > 0 {
			if _, ok := private[0][entry.Path]; ok {
				continue
			}
		}
		if !strings.HasPrefix(entry.Path, "sources/") || entry.UID < 0 || entry.GID < 0 {
			return errors.New("invalid transfer file metadata")
		}
		name := filepath.Join(target, filepath.FromSlash(entry.Path))
		if err := os.Chown(name, entry.UID, entry.GID); err != nil {
			return err
		}
		if err := os.Chmod(name, os.FileMode(entry.Mode)&0o777); err != nil {
			return err
		}
	}
	return nil
}
