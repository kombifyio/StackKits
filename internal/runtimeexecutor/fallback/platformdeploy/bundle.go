package platformdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	if readiness, ok := adapter.(ReadinessChecker); ok {
		if err := readiness.WaitReady(ctx); err != nil {
			return nil, fmt.Errorf("wait for platform API readiness: %w", err)
		}
	}
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
		appRefs = append(appRefs, ref)
	}
	if len(appRefs) > 0 {
		var err error
		appRefs, err = observeDeployments(ctx, adapter, appRefs)
		if err != nil {
			return append(systemRefs, appRefs...), err
		}
	}
	return append(systemRefs, appRefs...), nil
}

// ApplyBundleResilient executes every independent StackKit-owned application
// and preserves partial success. Adapter/application failures become component
// results; cancellation remains a fatal orchestration boundary.
func ApplyBundleResilient(ctx context.Context, adapter Adapter, bundle BundleManifest) (BundleResult, error) {
	var result BundleResult
	apps := deployableApps(bundle)
	if readiness, ok := adapter.(ReadinessChecker); ok {
		if err := readiness.WaitReady(ctx); err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			for _, app := range apps {
				result.Failures = append(result.Failures, componentFailure(app, bundle.Platform, "adapter-readiness", err, false))
			}
			return result, nil
		}
	}

	for _, app := range apps {
		defaultAppPlatform(&app, bundle.Platform)
		ref, err := adapter.ApplyCompose(ctx, app)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			identityCommitted := strings.TrimSpace(ref.ExternalID) != ""
			if identityCommitted {
				// The adapter already created or adopted a durable platform
				// identity. Retain it for reconciliation and never fan this
				// component out to a second adapter or Compose fallback.
				result.Refs = append(result.Refs, ref)
			}
			result.Failures = append(result.Failures, componentFailure(app, bundle.Platform, "apply", err, identityCommitted))
			continue
		}

		observed, observeErr := observeDeployments(ctx, adapter, []DeploymentRef{ref})
		if len(observed) > 0 {
			ref = observed[0]
		}
		result.Refs = append(result.Refs, ref)
		if observeErr != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			result.Failures = append(result.Failures, componentFailure(app, bundle.Platform, "observe", observeErr, true))
		}
	}
	return result, nil
}

func deployableApps(bundle BundleManifest) []AppManifest {
	apps := make([]AppManifest, 0, len(bundle.SystemApps)+len(bundle.Apps))
	for _, systemApp := range bundle.SystemApps {
		apps = append(apps, systemApp.AppManifest)
	}
	for _, app := range bundle.Apps {
		if IsStackKitOwnedApp(app) {
			apps = append(apps, app)
		}
	}
	return apps
}

func componentFailure(app AppManifest, platform, stage string, err error, identityCommitted bool) ComponentFailure {
	return ComponentFailure{
		AppName: app.Name, Platform: firstNonEmptyPlatform(app.ManagedBy, app.Platform, platform),
		Stage: stage, Message: err.Error(), IdentityCommitted: identityCommitted, Retryable: true,
	}
}

func firstNonEmptyPlatform(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "none"
}

// StandaloneFallbackBundle selects only components that have no committed
// platform identity. It is safe to execute after a preferred adapter failed.
func StandaloneFallbackBundle(bundle BundleManifest, failures []ComponentFailure) BundleManifest {
	wanted := map[string]bool{}
	for _, failure := range failures {
		if !failure.IdentityCommitted {
			wanted[failure.AppName] = true
		}
	}
	fallback := BundleManifest{
		Version: bundle.Version, Platform: "none",
		Fallback:  FallbackManifest{Enabled: true, Mode: "standalone-compose"},
		Bootstrap: bundle.Bootstrap,
	}
	for _, systemApp := range bundle.SystemApps {
		if !wanted[systemApp.Name] {
			continue
		}
		systemApp.Platform, systemApp.ManagedBy = "none", "standalone-compose"
		fallback.SystemApps = append(fallback.SystemApps, systemApp)
	}
	for _, app := range bundle.Apps {
		if !wanted[app.Name] || !IsStackKitOwnedApp(app) {
			continue
		}
		app.Platform, app.ManagedBy = "none", "standalone-compose"
		fallback.Apps = append(fallback.Apps, app)
	}
	return fallback
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
