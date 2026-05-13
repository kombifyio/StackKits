package platformdeploy

import (
	"context"
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
		defaultAppPlatform(app, bundle.Platform)
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
		defaultAppPlatform(app, bundle.Platform)
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

func ApplyBundle(ctx context.Context, adapter Adapter, bundle BundleManifest) ([]DeploymentRef, error) {
	refs := make([]DeploymentRef, 0, len(bundle.SystemApps)+len(bundle.Apps))
	for _, systemApp := range bundle.SystemApps {
		app := systemApp.AppManifest
		defaultAppPlatform(&app, bundle.Platform)
		ref, err := adapter.ApplyCompose(ctx, app)
		if err != nil {
			return refs, fmt.Errorf("deploy platform system app %q: %w", app.Name, err)
		}
		refs = append(refs, ref)
	}
	for _, app := range bundle.Apps {
		defaultAppPlatform(&app, bundle.Platform)
		ref, err := adapter.ApplyCompose(ctx, app)
		if err != nil {
			return refs, fmt.Errorf("deploy platform app %q: %w", app.Name, err)
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func defaultAppPlatform(app *AppManifest, platform string) {
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
