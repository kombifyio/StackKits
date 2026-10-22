package upgradelifecycle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/opentofu"
	"github.com/kombifyio/stackkits/internal/tofu"
)

// A release child resolves its OpenTofu provider mirror from
// STACKKIT_TOFU_PROVIDERS_DIR and accepts only a mirror whose manifest equals
// the one compiled into it. The installation mirror (../lib/stackkit/providers
// beside the running CLI) carries one manifest, so a source and a target
// release whose bundled provider sets differ cannot both run on it. Each
// staged release child therefore names, in a private file beside its
// executable, the mirror that carries its own manifest: the installation
// mirror when the manifests are equal, otherwise a durable copy of the
// release's verified mirror under .stackkit/provider-mirrors, keyed by the
// manifest digest. Before a child runs, every governed OpenTofu root of the
// workspace is rebound to that mirror; the roots record the mirror path in
// stackkit.tofurc, and a release refuses a root bound to another path.
const (
	releaseProviderMirrorFile = "stackkit-provider-mirror"
	providerMirrorsDir        = "provider-mirrors"
	maxProviderMirrorPathSize = 4 << 10
	maxProviderMirrorFiles    = 2_000
	maxProviderMirrorBytes    = int64(1 << 30)
)

// stageReleaseProviderMirror records, beside a release executable staged in
// dir, the mirror that release runs on. A release that ships no provider
// mirror predates the OpenTofu runtime and gets none.
func stageReleaseProviderMirror(extractRoot, dir string, receipt releaseindex.Receipt) error {
	extracted := filepath.Join(extractRoot, tofu.ProvidersDirName)
	info, err := os.Lstat(extracted)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() {
		return errors.New("executor state: verified release provider mirror is not a plain directory")
	}
	manifest, err := tofu.ReadProviderManifest(extracted)
	if err != nil {
		return fmt.Errorf("executor state: read verified release provider manifest: %w", err)
	}
	mirror, err := selectReleaseProviderMirror(extracted, manifest, receipt)
	if err != nil {
		return err
	}
	return writeReleaseProviderMirrorFile(dir, mirror)
}

func selectReleaseProviderMirror(extracted string, manifest []byte, receipt releaseindex.Receipt) (string, error) {
	if installed, ok := installationProviderMirror(); ok {
		if current, err := tofu.ReadProviderManifest(installed); err == nil && bytes.Equal(current, manifest) {
			return installed, nil
		}
	}
	workspace, err := releaseWorkspaceRoot(receipt)
	if err != nil {
		return "", fmt.Errorf(
			"release %s bundles a different OpenTofu provider set than this installation and its own mirror cannot be kept: %w",
			receipt.Version, err,
		)
	}
	mirror, err := ensureDurableProviderMirror(workspace, extracted, manifest)
	if err != nil {
		return "", fmt.Errorf(
			"release %s bundles a different OpenTofu provider set than this installation and its own verified mirror cannot be kept: %w",
			receipt.Version, err,
		)
	}
	return mirror, nil
}

func installationProviderMirror() (string, bool) {
	installed, ok := tofu.PackagedProvidersDir()
	if !ok {
		return "", false
	}
	absolute, err := filepath.Abs(installed)
	if err != nil {
		return "", false
	}
	return absolute, true
}

// releaseWorkspaceRoot derives the workspace of a release installed under
// <workspace>/.stackkit/releases/<kit>/<version>/<os>-<arch>.
func releaseWorkspaceRoot(receipt releaseindex.Receipt) (string, error) {
	installDir := filepath.Clean(receipt.InstallDir)
	versionDir := filepath.Dir(installDir)
	kitDir := filepath.Dir(versionDir)
	releasesDir := filepath.Dir(kitDir)
	stackkitDir := filepath.Dir(releasesDir)
	if !filepath.IsAbs(installDir) ||
		filepath.Base(installDir) != receipt.Platform.OS+"-"+receipt.Platform.Arch ||
		filepath.Base(versionDir) != receipt.Version ||
		filepath.Base(kitDir) != receipt.Kit ||
		filepath.Base(releasesDir) != "releases" ||
		filepath.Base(stackkitDir) != ".stackkit" {
		return "", errors.New("the release is not installed in workspace release custody")
	}
	return filepath.Dir(stackkitDir), nil
}

func providerMirrorsRoot(workspace string) string {
	return filepath.Join(workspace, ".stackkit", providerMirrorsDir)
}

// ensureDurableProviderMirror keeps the verified mirror of one manifest at
// .stackkit/provider-mirrors/sha256-<manifest digest>. An existing copy is
// reused only when it still verifies; a copy that does not is refused, never
// repaired in place, because current roots may be bound to it.
func ensureDurableProviderMirror(workspace, extracted string, manifest []byte) (string, error) {
	sum := sha256.Sum256(manifest)
	root := providerMirrorsRoot(workspace)
	target := filepath.Join(root, "sha256-"+hex.EncodeToString(sum[:]))
	if _, err := os.Lstat(target); err == nil {
		if err := requirePlainProviderMirrorDir(target); err != nil {
			return "", err
		}
		if err := tofu.VerifyReleaseProviderMirror(target, manifest); err != nil {
			return "", fmt.Errorf("kept provider mirror %s does not verify; remove it and retry: %w", target, err)
		}
		return target, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create provider mirror directory: %w", err)
	}
	if err := requirePlainProviderMirrorDir(root); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(root, ".stage-")
	if err != nil {
		return "", fmt.Errorf("stage provider mirror: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := copyProviderMirrorTree(extracted, stage); err != nil {
		return "", fmt.Errorf("stage provider mirror: %w", err)
	}
	if err := tofu.VerifyReleaseProviderMirror(stage, manifest); err != nil {
		return "", fmt.Errorf("staged provider mirror does not verify: %w", err)
	}
	if err := os.Rename(stage, target); err != nil {
		// A concurrent stager may have kept the same manifest first.
		if verifyErr := tofu.VerifyReleaseProviderMirror(target, manifest); verifyErr == nil {
			return target, nil
		}
		return "", fmt.Errorf("install provider mirror: %w", err)
	}
	return target, nil
}

func requirePlainProviderMirrorDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("provider mirror path %s is not a plain directory", path)
	}
	return nil
}

// copyProviderMirrorTree copies the regular files and directories of source
// into the empty directory target; links and special files are refused.
func copyProviderMirrorTree(source, target string) error {
	files := 0
	var total int64
	return filepath.WalkDir(source, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		destination := filepath.Join(target, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			return os.Mkdir(destination, 0o700)
		case info.Mode().IsRegular():
			files++
			total += info.Size()
			if files > maxProviderMirrorFiles || total > maxProviderMirrorBytes {
				return errors.New("provider mirror exceeds its bound")
			}
			return copyProviderMirrorFile(current, destination, info.Mode().Perm()&0o700)
		default:
			return fmt.Errorf("provider mirror entry %s is not a regular file", filepath.ToSlash(relative))
		}
	})
}

func copyProviderMirrorFile(source, target string, mode fs.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode|0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	return errors.Join(copyErr, syncErr, output.Close())
}

func writeReleaseProviderMirrorFile(dir, mirror string) error {
	if !filepath.IsAbs(mirror) || strings.ContainsAny(mirror, "\r\n") {
		return errors.New("executor state: release provider mirror path is not absolute")
	}
	target := filepath.Join(dir, releaseProviderMirrorFile)
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("executor state: record release provider mirror: %w", err)
	}
	_, writeErr := file.WriteString(mirror)
	return errors.Join(writeErr, file.Close())
}

// releaseProviderMirrorFor returns the mirror recorded beside binary, if any.
func releaseProviderMirrorFor(binary string) (string, bool, error) {
	if strings.TrimSpace(binary) == "" {
		return "", false, nil
	}
	name := filepath.Join(filepath.Dir(binary), releaseProviderMirrorFile)
	info, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxProviderMirrorPathSize {
		return "", false, errors.New("release provider mirror record is not a bounded regular file")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return "", false, fmt.Errorf("read release provider mirror record: %w", err)
	}
	mirror := string(data)
	if !filepath.IsAbs(mirror) || strings.ContainsAny(mirror, "\r\n") {
		return "", false, errors.New("release provider mirror record is not an absolute path")
	}
	return mirror, true, nil
}

// rebindOpenTofuRoots points the stackkit.tofurc of every governed OpenTofu
// root of workspace at mirror. Only a configuration that is exactly the
// offline mirror configuration of some path is rewritten: the rebind changes
// which verified mirror a root installs from, nothing else. Anything else is
// left for the release to refuse.
func rebindOpenTofuRoots(workspace, mirror string) error {
	if strings.TrimSpace(workspace) == "" {
		return nil
	}
	runtimeRoot := filepath.Join(workspace, ".stackkit", "runtime")
	if err := requirePlainProviderMirrorDir(runtimeRoot); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("rebind OpenTofu roots: %w", err)
	}
	roots, err := governedOpenTofuRoots(runtimeRoot)
	if err != nil {
		return fmt.Errorf("rebind OpenTofu roots: %w", err)
	}
	want := tofu.OfflineCLIConfig(mirror)
	for _, root := range roots {
		name := filepath.Join(root, opentofu.CLIConfigFile)
		info, err := os.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxProviderMirrorPathSize*2 {
			continue
		}
		current, err := os.ReadFile(name)
		if err != nil {
			return fmt.Errorf("rebind OpenTofu root: %w", err)
		}
		if bytes.Equal(current, want) {
			continue
		}
		if _, ok := tofu.OfflineCLIConfigMirror(current); !ok {
			continue
		}
		if err := writeProviderMirrorFileAtomic(root, opentofu.CLIConfigFile, want); err != nil {
			return fmt.Errorf("rebind OpenTofu root: %w", err)
		}
	}
	return nil
}

// governedOpenTofuRoots lists .stackkit/runtime/<dir>/opentofu and
// .stackkit/runtime/{applications,modules}/<dir>/opentofu plain directories.
func governedOpenTofuRoots(runtimeRoot string) ([]string, error) {
	var roots []string
	add := func(parent string) error {
		entries, err := os.ReadDir(parent)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			root := filepath.Join(parent, entry.Name(), opentofu.RootDirName)
			if requirePlainProviderMirrorDir(root) == nil {
				roots = append(roots, root)
			}
		}
		return nil
	}
	if err := add(runtimeRoot); err != nil {
		return nil, err
	}
	for _, subtree := range []string{opentofu.ApplicationsDir, opentofu.ModulesDir} {
		parent := filepath.Join(runtimeRoot, subtree)
		if requirePlainProviderMirrorDir(parent) != nil {
			continue
		}
		if err := add(parent); err != nil {
			return nil, err
		}
	}
	sort.Strings(roots)
	return roots, nil
}

func writeProviderMirrorFileAtomic(directory, name string, data []byte) error {
	temporary, err := os.CreateTemp(directory, "."+name+".tmp-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	_, writeErr := temporary.Write(data)
	chmodErr := temporary.Chmod(0o600)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if err := errors.Join(writeErr, chmodErr, syncErr, closeErr); err != nil {
		return err
	}
	return os.Rename(temporaryName, filepath.Join(directory, name))
}

// recoveredReleaseProviderMirror selects the mirror for a recovered release
// executable. The snapshot retains only the executable, which embeds its
// provider manifest verbatim; the installation mirror wins when it carries
// that manifest, otherwise the kept mirror of an earlier staging of the same
// release does.
func recoveredReleaseProviderMirror(workspace string, executable []byte) (string, bool) {
	var candidates []string
	if installed, ok := installationProviderMirror(); ok {
		candidates = append(candidates, installed)
	}
	if entries, err := os.ReadDir(providerMirrorsRoot(workspace)); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && strings.HasPrefix(entry.Name(), "sha256-") {
				candidates = append(candidates, filepath.Join(providerMirrorsRoot(workspace), entry.Name()))
			}
		}
	}
	for _, candidate := range candidates {
		manifest, err := tofu.ReadProviderManifest(candidate)
		if err != nil || len(manifest) == 0 || !bytes.Contains(executable, manifest) {
			continue
		}
		if tofu.VerifyReleaseProviderMirror(candidate, manifest) == nil {
			return candidate, true
		}
	}
	return "", false
}
