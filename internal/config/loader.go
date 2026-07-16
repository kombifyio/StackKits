// Package config handles configuration file parsing and management.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/productkits"
	"github.com/kombifyio/stackkits/pkg/models"
	"gopkg.in/yaml.v3"
)

const (
	defaultStackSpecPath = "stack-spec.yaml"
	stackSpecAliasPath   = "kombination.yaml"
)

// Loader handles loading configuration files
type Loader struct {
	basePath string
}

// NewLoader creates a new configuration loader
func NewLoader(basePath string) *Loader {
	return &Loader{basePath: basePath}
}

// LoadStackKit loads a stackkit.yaml file
func (l *Loader) LoadStackKit(path string) (*models.StackKit, error) {
	fullPath := l.resolvePath(path)

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read stackkit.yaml: %w", err)
	}

	var stackkit models.StackKit
	if err := yaml.Unmarshal(data, &stackkit); err != nil {
		return nil, fmt.Errorf("failed to parse stackkit.yaml: %w", err)
	}

	if err := validateStackKit(&stackkit); err != nil {
		return nil, err
	}

	return &stackkit, nil
}

// LoadStackSpec loads a StackKit deployment spec.
// stack-spec.yaml remains the canonical CLI file. If the caller asks for the
// canonical default and it is missing, kombination.yaml is accepted as a
// user-intent alias for TechStack/CLI interoperability.
func (l *Loader) LoadStackSpec(path string) (*models.StackSpec, error) {
	fullPath, displayPath, _, err := l.ResolveStackSpecPathForRead(path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", displayPath, err)
	}

	var spec models.StackSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", displayPath, err)
	}

	// Apply defaults
	applySpecDefaults(&spec)
	if err := productkits.Validate(spec.StackKit); err != nil {
		return nil, fmt.Errorf("invalid stackkit product in %s: %w", displayPath, err)
	}

	return &spec, nil
}

// ResolveStackSpecPathForRead resolves the spec path and reports whether the
// TechStack/user-intent alias was selected.
func (l *Loader) ResolveStackSpecPathForRead(path string) (string, string, bool, error) {
	fullPath := l.resolvePath(path)
	if path != defaultStackSpecPath {
		return fullPath, path, false, nil
	}

	if _, err := os.Stat(fullPath); err == nil {
		return fullPath, defaultStackSpecPath, false, nil
	} else if !os.IsNotExist(err) {
		return "", defaultStackSpecPath, false, fmt.Errorf("failed to stat %s: %w", defaultStackSpecPath, err)
	}

	aliasPath := l.resolvePath(stackSpecAliasPath)
	if _, err := os.Stat(aliasPath); err == nil {
		return aliasPath, stackSpecAliasPath, true, nil
	} else if !os.IsNotExist(err) {
		return "", stackSpecAliasPath, false, fmt.Errorf("failed to stat %s: %w", stackSpecAliasPath, err)
	}

	return fullPath, defaultStackSpecPath, false, nil
}

// SaveStackSpec saves a stack-spec.yaml file
func (l *Loader) SaveStackSpec(spec *models.StackSpec, path string) error {
	fullPath := l.resolvePath(path)

	data, err := yaml.Marshal(spec)
	if err != nil {
		return fmt.Errorf("failed to marshal stack-spec: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	if err := os.WriteFile(fullPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write stack-spec.yaml: %w", err)
	}

	return nil
}

// LoadDeploymentState loads the deployment state file
func (l *Loader) LoadDeploymentState(path string) (*models.DeploymentState, error) {
	fullPath := l.resolvePath(path)

	data, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No state file exists yet
		}
		return nil, fmt.Errorf("failed to read deployment state: %w", err)
	}

	var state models.DeploymentState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse deployment state: %w", err)
	}

	return &state, nil
}

// SaveDeploymentState saves the deployment state file
func (l *Loader) SaveDeploymentState(state *models.DeploymentState, path string) error {
	fullPath := l.resolvePath(path)

	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal deployment state: %w", err)
	}

	if err := os.WriteFile(fullPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write deployment state: %w", err)
	}

	return nil
}

// FindStackKitDir finds the stackkit directory for a given name
func (l *Loader) FindStackKitDir(name string) (string, error) {
	// Validate name to prevent path traversal (TD-007)
	if err := validateStackKitName(name); err != nil {
		return "", err
	}

	// Check if it's a path
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		absPath, err := filepath.Abs(name)
		if err != nil {
			return "", fmt.Errorf("invalid path: %w", err)
		}
		if err := validateStackKitDirectoryProduct(absPath, ""); err != nil {
			return "", err
		}
		return absPath, nil
	}

	if err := productkits.Validate(name); err != nil {
		return "", err
	}

	// Get user home directory (cross-platform, fixes TD-025)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "" // Fall back to empty if unavailable
	}

	// Check common locations
	locations := []string{
		filepath.Join(l.basePath, name),
		filepath.Join(l.basePath, "..", name),
	}

	// Only add home directory location if available
	if homeDir != "" {
		locations = append(locations, filepath.Join(homeDir, ".stackkits", name))
	}

	for _, loc := range locations {
		stackkitPath := filepath.Join(loc, "stackkit.yaml")
		if _, err := os.Stat(stackkitPath); err == nil {
			if err := validateStackKitDirectoryProduct(loc, name); err != nil {
				return "", err
			}
			return loc, nil
		}
	}

	return "", fmt.Errorf("stackkit '%s' not found", name)
}

func validateStackKitDirectoryProduct(dir, expectedName string) error {
	definitionPath := filepath.Join(dir, "stackkit.yaml")
	data, err := os.ReadFile(definitionPath)
	if err != nil {
		return fmt.Errorf("failed to read local stackkit definition %s: %w", definitionPath, err)
	}
	var identity struct {
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(data, &identity); err != nil {
		return fmt.Errorf("failed to parse local stackkit definition %s: %w", definitionPath, err)
	}
	if err := productkits.Validate(identity.Metadata.Name); err != nil {
		return fmt.Errorf("invalid stackkit product in %s: %w", definitionPath, err)
	}
	if expectedName != "" && identity.Metadata.Name != expectedName {
		return fmt.Errorf(
			"stackkit product identity mismatch in %s: requested %q, definition names %q",
			definitionPath,
			expectedName,
			identity.Metadata.Name,
		)
	}
	return nil
}

// validateStackKitName validates a stackkit name to prevent path traversal attacks
func validateStackKitName(name string) error {
	if name == "" {
		return fmt.Errorf("stackkit name cannot be empty")
	}

	// Prevent path traversal
	if strings.Contains(name, "..") {
		return fmt.Errorf("stackkit name cannot contain '..'")
	}

	// Check for null bytes
	if strings.ContainsRune(name, 0) {
		return fmt.Errorf("stackkit name contains invalid characters")
	}

	return nil
}

// resolvePath resolves a path relative to the base path.
// Absolute paths are rejected to prevent directory traversal outside basePath.
func (l *Loader) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		// Verify absolute path is under basePath to prevent traversal
		absBase, err := filepath.Abs(l.basePath)
		if err == nil {
			cleaned := filepath.Clean(path)
			if strings.HasPrefix(cleaned, absBase+string(filepath.Separator)) || cleaned == absBase {
				return cleaned
			}
		}
		// Reject paths outside basePath — treat as relative
		return filepath.Join(l.basePath, filepath.Base(path))
	}
	return filepath.Join(l.basePath, path)
}

// validateStackKit validates a stackkit configuration
func validateStackKit(sk *models.StackKit) error {
	if sk.Metadata.Name == "" {
		return fmt.Errorf("stackkit metadata.name is required")
	}
	if sk.Metadata.Version == "" {
		return fmt.Errorf("stackkit metadata.version is required")
	}
	if len(sk.SupportedOS) == 0 {
		return fmt.Errorf("stackkit must support at least one OS")
	}
	return nil
}

// applySpecDefaults applies default values to a stack spec
func applySpecDefaults(spec *models.StackSpec) {
	if models.IsLegacyStackKitName(spec.StackKit) {
		slog.Warn("legacy stackkit name normalized", "from", spec.StackKit, "to", models.NormalizeStackKitName(spec.StackKit))
	}
	spec.StackKit = models.NormalizeStackKitName(spec.StackKit)
	if spec.Mode == "" {
		spec.Mode = models.InstallModeBootstrapped
	} else {
		if models.IsLegacyInstallMode(spec.Mode) {
			slog.Warn("legacy stackkit install mode normalized", "from", spec.Mode, "to", models.NormalizeInstallMode(spec.Mode))
		}
		spec.Mode = models.NormalizeInstallMode(spec.Mode)
	}
	if spec.Network.Mode == "" {
		spec.Network.Mode = "local"
	}
	if spec.Compute.Tier == "" {
		spec.Compute.Tier = "standard"
	}
	if spec.SSH.Port == 0 {
		spec.SSH.Port = 22
	}
	if spec.SSH.User == "" {
		spec.SSH.User = "root"
	}
	if spec.DemoData.Enabled == nil {
		enabled := false
		spec.DemoData.Enabled = &enabled
	}
	for name, app := range spec.Apps {
		if app.Kind == "" {
			app.Kind = "sveltekit"
		}
		if app.Port == 0 {
			app.Port = 3000
		}
		if app.Health.Path == "" {
			app.Health.Path = "/health"
		}
		if app.Route.Auth == "" {
			app.Route.Auth = "login-gateway"
		}
		if app.Route.Host == "" {
			app.Route.Host = models.DefaultAppHost(spec.Domain, spec.SubdomainPrefix, name)
		}
		spec.Apps[name] = app
	}
}

// ExpandPath expands ~ and environment variables in a path
func ExpandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[2:])
	}
	return os.ExpandEnv(path)
}

// GetDefaultSpecPath returns the default spec file path
func GetDefaultSpecPath() string {
	return defaultStackSpecPath
}

// GetSpecAliasPath returns the accepted TechStack/user-intent alias path.
func GetSpecAliasPath() string {
	return stackSpecAliasPath
}

// GetDeployDir returns the deployment output directory
func GetDeployDir() string {
	return "deploy"
}

// DiscoverStackKits scans directories for stackkit.yaml files and returns loaded StackKits.
// It scans the given directories in order and deduplicates by name.
func (l *Loader) DiscoverStackKits(dirs ...string) ([]*models.StackKit, error) {
	var kits []*models.StackKit
	seen := make(map[string]bool)

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			yamlPath := filepath.Join(dir, entry.Name(), "stackkit.yaml")
			if _, err := os.Stat(yamlPath); err != nil {
				continue
			}
			sk, err := l.LoadStackKit(yamlPath)
			if err != nil || !productkits.IsActive(sk.Metadata.Name) || seen[sk.Metadata.Name] {
				continue
			}
			seen[sk.Metadata.Name] = true
			kits = append(kits, sk)
		}
	}

	return kits, nil
}
