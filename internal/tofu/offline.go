package tofu

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	// ProvidersDirEnv overrides the packaged OpenTofu provider mirror.
	ProvidersDirEnv = "STACKKIT_TOFU_PROVIDERS_DIR"
	// ProvidersDirName is the mirror directory shipped beside the packaged
	// tofu binary in every release archive.
	ProvidersDirName = "providers"
	// ProviderRegistryHost is the registry namespace the mirror serves.
	ProviderRegistryHost = "registry.opentofu.org"
	// PinnedLocalProviderVersion is the hashicorp/local release that
	// scripts/release/fetch-opentofu-providers.sh packages. The rendered
	// Stage 1 roots constrain the provider to a range that admits it.
	PinnedLocalProviderVersion = "2.5.3"
)

// OfflineInheritedEnv names host variables that could redirect provider
// installation, add CLI arguments, or move the working data directory. The
// runtime executor removes them before every OpenTofu process it starts.
var OfflineInheritedEnv = []string{
	"TF_PLUGIN_CACHE_DIR",
	"TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE",
	"TF_CLI_CONFIG_FILE",
	"TF_CLI_ARGS",
	"TF_CLI_ARGS_init",
	"TF_CLI_ARGS_plan",
	"TF_CLI_ARGS_apply",
	"TF_DATA_DIR",
	"TF_WORKSPACE",
	"TOFU_CLI_CONFIG_FILE",
}

// ErrProviderMirrorMissing reports that the packaged offline provider mirror
// is absent or incomplete for this platform.
var ErrProviderMirrorMissing = errors.New("packaged OpenTofu provider mirror is missing")

// PackagedProvidersDir returns the StackKit-packaged provider mirror. Like
// PackagedBinaryPath it never consults PATH or a host plugin cache: the
// override is STACKKIT_TOFU_PROVIDERS_DIR, otherwise providers/ beside the
// executable, beside bin/, or in the Debian package's lib directory.
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

// RequireLocalProviderMirror verifies that dir holds the pinned
// hashicorp/local provider for the running platform in the unpacked
// filesystem-mirror layout. It fails closed with an actionable error.
func RequireLocalProviderMirror(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("%w: reinstall the StackKit release archive, which ships %s/ beside tofu, or set %s", ErrProviderMirrorMissing, ProvidersDirName, ProvidersDirEnv)
	}
	platform := runtime.GOOS + "_" + runtime.GOARCH
	providerDir := filepath.Join(dir, ProviderRegistryHost, "hashicorp", "local", PinnedLocalProviderVersion, platform)
	entries, err := os.ReadDir(providerDir)
	if err != nil {
		return fmt.Errorf("%w: %s has no hashicorp/local %s for %s; reinstall the StackKit release archive or set %s", ErrProviderMirrorMissing, dir, PinnedLocalProviderVersion, platform, ProvidersDirEnv)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "terraform-provider-local") {
			return nil
		}
	}
	return fmt.Errorf("%w: %s holds no terraform-provider-local executable", ErrProviderMirrorMissing, providerDir)
}

// OfflineCLIConfig renders a CLI configuration that installs every
// registry.opentofu.org provider from the filesystem mirror and forbids direct
// registry installation, so tofu init never reaches the network.
func OfflineCLIConfig(providersDir string) []byte {
	mirror := strconv.Quote(filepath.ToSlash(providersDir))
	include := strconv.Quote(ProviderRegistryHost + "/*/*")
	return []byte("provider_installation {\n" +
		"  filesystem_mirror {\n" +
		"    path    = " + mirror + "\n" +
		"    include = [" + include + "]\n" +
		"  }\n" +
		"  direct {\n" +
		"    exclude = [" + include + "]\n" +
		"  }\n" +
		"}\n")
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
