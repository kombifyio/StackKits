package imagepreparation

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/tofu"
)

// Only the selected release cache and the neutral manifest may be in the bake
// metadata root. Foreign or old release trees are not silently image inputs.
func neutralCacheRoot(root, releaseDir string) error {
	if err := SafePath(root); err != nil {
		return err
	}
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	releaseDir, err = filepath.Abs(releaseDir)
	if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("neutral cache cannot contain symlinks")
		}
		if name == root {
			return nil
		}
		if name == filepath.Join(root, ManifestName) && entry.Type().IsRegular() {
			return nil
		}
		if entry.IsDir() && (name == releaseDir || strings.HasPrefix(releaseDir, name+string(filepath.Separator))) {
			return nil
		}
		if !entry.IsDir() && entry.Type().IsRegular() && filepath.Dir(name) == releaseDir {
			return nil
		}
		return fmt.Errorf("%w: neutral cache contains unexpected entry: %s", ErrNotNeutral, name)
	})
}

func releaseCacheFiles(dir string, asset releaseindex.Asset) error {
	allowed := map[string]bool{}
	for _, name := range []string{releaseindex.ReleaseIndexAssetName, releaseindex.ReleaseIndexAttestationAssetName, releaseindex.TrustedRootAssetName, releaseindex.ReleaseReceiptName, asset.Archive.Name, asset.SBOM.Name, asset.Attestation.Name} {
		allowed[name] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !allowed[entry.Name()] {
			return fmt.Errorf("release cache contains unexpected entry %s", entry.Name())
		}
	}
	return nil
}

// Match the existing OpenTofu resolver's search order without accepting an
// environment override. Its regular executor consumes this same mirror.
func installedProviderRoot(executable string) (string, error) {
	dir := filepath.Dir(executable)
	for _, candidate := range []string{filepath.Join(dir, tofu.ProvidersDirName), filepath.Join(dir, "bin", tofu.ProvidersDirName), filepath.Join(dir, "..", "lib", "stackkit", tofu.ProvidersDirName)} {
		candidate = filepath.Clean(candidate)
		if err := SafePath(candidate); err != nil {
			return "", err
		}
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", errors.New("packaged provider mirror is not a directory")
		}
		return candidate, nil
	}
	return "", tofu.ErrProviderMirrorMissing
}

func digestTree(root string) (map[string]string, error) {
	digests := map[string]string{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("packaged mirror cannot contain symlinks")
		}
		if entry.IsDir() {
			return nil
		}
		digest, err := FileDigest(name)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		digests[filepath.ToSlash(relative)] = digest
		return nil
	})
	return digests, err
}
