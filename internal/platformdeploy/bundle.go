package platformdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
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

// ApplyBundle applies StackKit-owned systemApps and StackKit-owned/default L3
// apps through the supplied PaaS adapter. Customer-owned apps in Apps are
// handoff metadata and are intentionally not deployed by StackKit.
func ApplyBundle(ctx context.Context, adapter Adapter, bundle BundleManifest) ([]DeploymentRef, error) {
	systemRefs := make([]DeploymentRef, 0, len(bundle.SystemApps))
	appRefs := make([]DeploymentRef, 0, len(bundle.Apps))
	for _, systemApp := range bundle.SystemApps {
		app := systemApp.AppManifest
		defaultAppPlatform(&app, bundle.Platform)
		ref, err := adapter.ApplyCompose(ctx, app)
		if err != nil {
			return append(systemRefs, appRefs...), fmt.Errorf("deploy platform system app %q: %w", app.Name, err)
		}
		systemRefs = append(systemRefs, ref)
	}
	if len(systemRefs) > 0 {
		var err error
		systemRefs, err = observeDeployments(ctx, adapter, systemRefs)
		if err != nil {
			return append(systemRefs, appRefs...), err
		}
	}
	for _, app := range bundle.Apps {
		if !IsStackKitOwnedApp(app) {
			continue
		}
		defaultAppPlatform(&app, bundle.Platform)
		ref, err := adapter.ApplyCompose(ctx, app)
		if err != nil {
			return append(systemRefs, appRefs...), fmt.Errorf("deploy StackKit L3 app %q: %w", app.Name, err)
		}
		ref.ObservedStatus = firstNonEmpty(ref.ObservedStatus, "deploy:accepted")
		if ref.ObservedAt.IsZero() {
			ref.ObservedAt = time.Now().UTC()
		}
		appRefs = append(appRefs, ref)
	}
	return append(systemRefs, appRefs...), nil
}

func observeDeployments(ctx context.Context, adapter Adapter, refs []DeploymentRef) ([]DeploymentRef, error) {
	if observer, ok := adapter.(DeploymentBatchObserver); ok {
		observed, err := observer.ObserveDeployments(ctx, refs)
		if err != nil {
			return observed, fmt.Errorf("observe platform app starts: %w", err)
		}
		return observed, nil
	}
	observer, ok := adapter.(DeploymentObserver)
	if !ok {
		return refs, nil
	}
	observed := make([]DeploymentRef, 0, len(refs))
	for _, ref := range refs {
		next, err := observer.ObserveDeployment(ctx, ref)
		if err != nil {
			return append(observed, ref), fmt.Errorf("observe platform app %q: %w", ref.AppName, err)
		}
		observed = append(observed, next)
	}
	return observed, nil
}

// IsStackKitOwnedApp reports whether this L3 app belongs to the StackKit-owned
// product surface. If no PaaS adapter is configured, callers may record it as
// unmanaged state rather than deploying it.
func IsStackKitOwnedApp(app AppManifest) bool {
	return app.Ownership == AppOwnershipStackKit
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
