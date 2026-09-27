package tofu

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/sumdb/dirhash"
)

const (
	ProvidersDirEnv               = "STACKKIT_TOFU_PROVIDERS_DIR"
	ProvidersDirName              = "providers"
	ProviderRegistryHost          = "registry.opentofu.org"
	PinnedLocalProviderVersion    = "2.5.3"
	PinnedKomodoProviderVersion   = "0.12.0"
	ProviderManifestFile          = "stackkit-provider-manifest.json"
	ProviderLockFile              = "stackkit-provider-lock.hcl"
	providerManifestSchemaVersion = 1
	maxProviderMetadataBytes      = 128 << 10
	emptyProviderLock             = "# StackKit provider-free root: no external providers.\n"
)

//go:embed provider_manifest.json
var canonicalProviderManifest []byte

var OfflineInheritedEnv = []string{
	"TF_PLUGIN_CACHE_DIR", "TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE",
	"TF_CLI_CONFIG_FILE", "TF_CLI_ARGS", "TF_CLI_ARGS_init", "TF_CLI_ARGS_plan", "TF_CLI_ARGS_apply",
	"TF_DATA_DIR", "TF_ENCRYPTION", "TF_WORKSPACE", "TOFU_CLI_CONFIG_FILE",
}

var (
	ErrProviderMirrorMissing  = errors.New("packaged OpenTofu provider mirror is missing")
	ErrProviderClosureInvalid = errors.New("packaged OpenTofu provider closure is invalid")
)

type providerManifest struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Providers     []providerManifestEntry `json:"providers"`
}

type providerManifestEntry struct {
	Source      string                     `json:"source"`
	Version     string                     `json:"version"`
	Constraints string                     `json:"constraints"`
	Platforms   []providerManifestPlatform `json:"platforms"`
}

type providerManifestPlatform struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Archive  string `json:"archive"`
	Filename string `json:"filename"`
	H1       string `json:"h1"`
	ZH       string `json:"zh"`
}

// ProviderClosure is a validated packaged provider mirror and dependency lock.
// Its fields stay private so callers cannot construct authority without first
// validating the bundle metadata and current-platform package.
type ProviderClosure struct {
	directory string
	manifest  providerManifest
}

func (c *ProviderClosure) Directory() string {
	if c == nil {
		return ""
	}
	return c.directory
}

// LockForConfiguration selects only the providers declared by this root.
// Provider-free terraform_data roots receive an empty lock.
func (c *ProviderClosure) LockForConfiguration(config []byte) ([]byte, error) {
	if c == nil {
		return nil, ErrProviderClosureInvalid
	}
	return providerLockForConfiguration(config, c.manifest)
}

func PackagedProvidersDir() (string, bool) {
	if override := os.Getenv(ProvidersDirEnv); override != "" {
		return override, directoryExists(override)
	}
	exePath, err := os.Executable()
	if err != nil || exePath == "" {
		return "", false
	}
	exeDir := filepath.Dir(exePath)
	for _, candidate := range []string{
		filepath.Join(exeDir, ProvidersDirName),
		filepath.Join(exeDir, "bin", ProvidersDirName),
		filepath.Join(exeDir, "..", "lib", "stackkit", ProvidersDirName),
	} {
		if directoryExists(candidate) {
			return filepath.Clean(candidate), true
		}
	}
	return "", false
}

// LoadProviderClosure validates the manifest, lock, and every package for the
// running platform before a tofu process can execute. Release archive trust
// authenticates the manifest; this closes its hashes over installed bytes.
func LoadProviderClosure(dir string) (*ProviderClosure, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, missingProviderMirrorError()
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve mirror: %v", ErrProviderClosureInvalid, err)
	}
	manifestBytes, err := readProviderMetadata(filepath.Join(absolute, ProviderManifestFile))
	if err != nil {
		return nil, err
	}
	manifest, err := decodeProviderManifest(manifestBytes)
	if err != nil {
		return nil, err
	}
	lock := renderProviderLock(manifest)
	packagedLock, err := readProviderMetadata(filepath.Join(absolute, ProviderLockFile))
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(packagedLock, lock) {
		return nil, fmt.Errorf("%w: packaged lock differs from its provider manifest", ErrProviderClosureInvalid)
	}
	if err := validateProviderPackages(absolute, manifest, runtime.GOOS, runtime.GOARCH); err != nil {
		return nil, err
	}
	return &ProviderClosure{directory: absolute, manifest: manifest}, nil
}

func RequireLocalProviderMirror(dir string) error {
	_, err := LoadProviderClosure(dir)
	return err
}

// PrepareProviderClosure is the release packaging boundary. It admits only
// the committed manifest, verifies every upstream archive and unpacked package,
// and writes the deterministic lock shipped in the same provider directory.
func PrepareProviderClosure(dir, targetOS, targetArch string, archives map[string]string) error {
	manifestBytes, err := readProviderMetadata(filepath.Join(dir, ProviderManifestFile))
	if err != nil {
		return err
	}
	manifest, err := decodeProviderManifest(manifestBytes)
	if err != nil {
		return err
	}
	if len(archives) != len(manifest.Providers) {
		return fmt.Errorf("%w: provider archive set differs from the canonical manifest", ErrProviderClosureInvalid)
	}
	for _, provider := range manifest.Providers {
		platform, ok := providerPlatform(manifest, provider.Source, targetOS, targetArch)
		if !ok {
			return fmt.Errorf("%w: no canonical %s package for %s_%s", ErrProviderClosureInvalid, provider.Source, targetOS, targetArch)
		}
		archive := archives[provider.Source]
		if archive == "" || filepath.Base(archive) != platform.Archive {
			return fmt.Errorf("%w: %s archive is absent or misnamed", ErrProviderClosureInvalid, provider.Source)
		}
		archiveHash, err := sha256File(archive)
		if err != nil {
			return fmt.Errorf("%w: read provider archive: %v", ErrProviderClosureInvalid, err)
		}
		if "zh:"+archiveHash != platform.ZH {
			return fmt.Errorf("%w: %s archive hash differs from the canonical manifest", ErrProviderClosureInvalid, platform.Archive)
		}
		h1, err := dirhash.HashZip(archive, dirhash.Hash1)
		if err != nil || h1 != platform.H1 {
			return fmt.Errorf("%w: %s archive package hash differs from the canonical manifest", ErrProviderClosureInvalid, platform.Archive)
		}
	}
	if err := validateProviderPackages(dir, manifest, targetOS, targetArch); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ProviderLockFile), renderProviderLock(manifest), 0o644)
}

// CanonicalLockForConfiguration supplies the exact root lock for a signed
// legacy checkpoint that predates lock capture.
func CanonicalLockForConfiguration(config []byte) ([]byte, error) {
	manifest, err := decodeProviderManifest(canonicalProviderManifest)
	if err != nil {
		return nil, err
	}
	return providerLockForConfiguration(config, manifest)
}

func CanonicalProviderManifest() []byte { return append([]byte(nil), canonicalProviderManifest...) }

// OfflineCLIConfig declares only the filesystem mirror, leaving no direct
// installer method with which OpenTofu could contact a registry.
func OfflineCLIConfig(providersDir string) []byte {
	mirror := strconv.Quote(filepath.ToSlash(providersDir))
	return []byte("provider_installation {\n" +
		"  filesystem_mirror {\n" +
		"    path = " + mirror + "\n" +
		"  }\n" +
		"}\n")
}

func decodeProviderManifest(data []byte) (providerManifest, error) {
	if !bytes.Equal(data, canonicalProviderManifest) {
		return providerManifest{}, fmt.Errorf("%w: packaged manifest differs from the release source", ErrProviderClosureInvalid)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest providerManifest
	if err := decoder.Decode(&manifest); err != nil {
		return providerManifest{}, fmt.Errorf("%w: decode provider manifest: %v", ErrProviderClosureInvalid, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return providerManifest{}, fmt.Errorf("%w: provider manifest has trailing JSON", ErrProviderClosureInvalid)
	}
	if manifest.SchemaVersion != providerManifestSchemaVersion || len(manifest.Providers) == 0 {
		return providerManifest{}, fmt.Errorf("%w: provider manifest schema or provider set is incomplete", ErrProviderClosureInvalid)
	}
	wantedPlatforms := []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64", "windows_amd64"}
	seenProviders := make(map[string]struct{}, len(manifest.Providers))
	previousSource := ""
	for _, provider := range manifest.Providers {
		parts := strings.Split(provider.Source, "/")
		if len(parts) != 3 || parts[0] != ProviderRegistryHost || provider.Source <= previousSource ||
			strings.TrimSpace(provider.Version) == "" || strings.TrimSpace(provider.Constraints) == "" {
			return providerManifest{}, fmt.Errorf("%w: provider identity or ordering is invalid", ErrProviderClosureInvalid)
		}
		if provider.Constraints != provider.Version {
			return providerManifest{}, fmt.Errorf("%w: %s constraints differ from its exact pin", ErrProviderClosureInvalid, provider.Source)
		}
		if _, duplicate := seenProviders[provider.Source]; duplicate {
			return providerManifest{}, fmt.Errorf("%w: duplicate provider %s", ErrProviderClosureInvalid, provider.Source)
		}
		seenProviders[provider.Source] = struct{}{}
		previousSource = provider.Source
		if len(provider.Platforms) != len(wantedPlatforms) {
			return providerManifest{}, fmt.Errorf("%w: %s does not close every bundled platform", ErrProviderClosureInvalid, provider.Source)
		}
		for index, platform := range provider.Platforms {
			key := platform.OS + "_" + platform.Arch
			if key != wantedPlatforms[index] || filepath.Base(platform.Archive) != platform.Archive ||
				filepath.Base(platform.Filename) != platform.Filename || platform.Filename == "." || platform.Archive == "." ||
				!validH1(platform.H1) || !validZH(platform.ZH) {
				return providerManifest{}, fmt.Errorf("%w: %s platform metadata is incomplete or unordered", ErrProviderClosureInvalid, provider.Source)
			}
		}
	}
	for _, source := range []string{ProviderRegistryHost + "/hashicorp/local", ProviderRegistryHost + "/sebastianfs82/komodo"} {
		if _, ok := seenProviders[source]; !ok {
			return providerManifest{}, fmt.Errorf("%w: %s is absent", ErrProviderClosureInvalid, source)
		}
	}
	for _, provider := range manifest.Providers {
		if provider.Source == ProviderRegistryHost+"/hashicorp/local" && provider.Version != PinnedLocalProviderVersion {
			return providerManifest{}, fmt.Errorf("%w: hashicorp/local pin differs from %s", ErrProviderClosureInvalid, PinnedLocalProviderVersion)
		}
		if provider.Source == ProviderRegistryHost+"/sebastianfs82/komodo" && provider.Version != PinnedKomodoProviderVersion {
			return providerManifest{}, fmt.Errorf("%w: sebastianfs82/komodo pin differs from %s", ErrProviderClosureInvalid, PinnedKomodoProviderVersion)
		}
	}
	return manifest, nil
}

func validH1(value string) bool {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "h1:"))
	return strings.HasPrefix(value, "h1:") && err == nil && len(decoded) == sha256.Size
}

func validZH(value string) bool {
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "zh:"))
	return strings.HasPrefix(value, "zh:") && err == nil && len(decoded) == sha256.Size
}

func renderProviderLock(manifest providerManifest) []byte {
	var result strings.Builder
	result.WriteString("# This file is maintained automatically by StackKit's packaged provider closure.\n")
	result.WriteString("# Manual edits are rejected by tofu init -lockfile=readonly.\n\n")
	for _, provider := range manifest.Providers {
		fmt.Fprintf(&result, "provider %q {\n  version     = %q\n  constraints = %q\n  hashes = [\n", provider.Source, provider.Version, provider.Constraints)
		hashes := make([]string, 0, 2*len(provider.Platforms))
		for _, platform := range provider.Platforms {
			hashes = append(hashes, platform.H1, platform.ZH)
		}
		sort.Strings(hashes)
		for _, hash := range hashes {
			fmt.Fprintf(&result, "    %q,\n", hash)
		}
		result.WriteString("  ]\n}\n")
	}
	return []byte(result.String())
}

func validateProviderPackages(dir string, manifest providerManifest, targetOS, targetArch string) error {
	for _, provider := range manifest.Providers {
		platform, ok := providerPlatform(manifest, provider.Source, targetOS, targetArch)
		if !ok {
			return fmt.Errorf("%w: %s has no package for %s_%s", ErrProviderClosureInvalid, provider.Source, targetOS, targetArch)
		}
		parts := strings.Split(provider.Source, "/")
		packageDir := filepath.Join(dir, parts[0], parts[1], parts[2], provider.Version, targetOS+"_"+targetArch)
		binaryInfo, err := os.Lstat(filepath.Join(packageDir, platform.Filename))
		if err != nil || !binaryInfo.Mode().IsRegular() || binaryInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s package executable is missing for %s_%s", ErrProviderMirrorMissing, provider.Source, targetOS, targetArch)
		}
		actual, err := dirhash.HashDir(packageDir, "", dirhash.Hash1)
		if err != nil || actual != platform.H1 {
			return fmt.Errorf("%w: %s unpacked package hash differs for %s_%s", ErrProviderClosureInvalid, provider.Source, targetOS, targetArch)
		}
	}
	return nil
}

func providerPlatform(manifest providerManifest, source, targetOS, targetArch string) (providerManifestPlatform, bool) {
	for _, provider := range manifest.Providers {
		if provider.Source != source {
			continue
		}
		for _, platform := range provider.Platforms {
			if platform.OS == targetOS && platform.Arch == targetArch {
				return platform, true
			}
		}
	}
	return providerManifestPlatform{}, false
}

func readProviderMetadata(name string) ([]byte, error) {
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxProviderMetadataBytes {
		return nil, fmt.Errorf("%w: %s is absent or not a bounded regular file", ErrProviderMirrorMissing, filepath.Base(name))
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %v", ErrProviderMirrorMissing, filepath.Base(name), err)
	}
	return data, nil
}

func sha256File(name string) (string, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func missingProviderMirrorError() error {
	return fmt.Errorf("%w: reinstall the StackKit release archive, which ships %s/ beside tofu, or set %s", ErrProviderMirrorMissing, ProvidersDirName, ProvidersDirEnv)
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
