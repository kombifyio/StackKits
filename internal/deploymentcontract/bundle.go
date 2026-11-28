package deploymentcontract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func LoadBundleManifest(path string) (BundleManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BundleManifest{}, fmt.Errorf("read platform app manifest: %w", err)
	}

	var bundle BundleManifest
	if err := json.Unmarshal(data, &bundle); err != nil {
		return BundleManifest{}, fmt.Errorf("decode platform app manifest: %w", err)
	}

	for i := range bundle.Apps {
		app := &bundle.Apps[i]
		app.ApplyDefaults(bundle.Platform)
		if app.ComposeYAML != "" || app.ComposePath == "" {
			continue
		}
		compose, err := readComposeForManifest(path, app.ComposePath)
		if err != nil {
			return BundleManifest{}, err
		}
		app.ComposeYAML = compose
	}
	for i := range bundle.SystemApps {
		app := &bundle.SystemApps[i].AppManifest
		app.ApplyDefaults(bundle.Platform)
		if app.ComposeYAML != "" || app.ComposePath == "" {
			continue
		}
		compose, err := readComposeForManifest(path, app.ComposePath)
		if err != nil {
			return BundleManifest{}, err
		}
		app.ComposeYAML = compose
	}

	return bundle, nil
}

// IsStackKitOwnedApp reports whether this L3 app belongs to the StackKit-owned
// product surface. If no PaaS adapter is configured, callers may record it as
// unmanaged state rather than deploying it.
func IsStackKitOwnedApp(app AppManifest) bool {
	return app.Ownership == AppOwnershipStackKit
}

func (app *AppManifest) ApplyDefaults(platform string) {
	if app.ManagedBy == "" {
		app.ManagedBy = platform
	}
	if app.Platform == "" {
		app.Platform = platform
	}
	if app.SetupPolicy == "" {
		app.SetupPolicy = SetupPolicyManual
	}
}

func readComposeForManifest(manifestPath, composePath string) (string, error) {
	candidates := []string{composePath}
	if !filepath.IsAbs(composePath) {
		manifestDir := filepath.Dir(manifestPath)
		candidates = []string{
			filepath.Join(manifestDir, composePath),
			filepath.Join(filepath.Dir(manifestDir), composePath),
		}
	}

	var lastErr error
	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err == nil {
			return string(data), nil
		}
		lastErr = err
	}
	return "", fmt.Errorf("read compose file %q referenced by %s: %w", composePath, manifestPath, lastErr)
}
