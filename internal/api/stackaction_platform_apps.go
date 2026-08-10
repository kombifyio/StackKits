package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/platformdeploy"
	"github.com/kombifyio/stackkits/internal/stackaction"
	"github.com/kombifyio/stackkits/pkg/models"
)

type runtimePlatformConfigFileStackAction struct {
	Platform                    string                           `json:"platform,omitempty"`
	Endpoint                    string                           `json:"endpoint,omitempty"`
	BaseURL                     string                           `json:"baseUrl,omitempty"`
	Token                       string                           `json:"token,omitempty"`
	APIKey                      string                           `json:"apiKey,omitempty"`
	APISecret                   string                           `json:"apiSecret,omitempty"`
	EnvironmentID               string                           `json:"environmentId,omitempty"`
	ServerID                    string                           `json:"serverId,omitempty"`
	ProjectUUID                 string                           `json:"projectUuid,omitempty"`
	EnvironmentUUID             string                           `json:"environmentUuid,omitempty"`
	DestinationUUID             string                           `json:"destinationUuid,omitempty"`
	LegacyDockerComposeAPI      bool                             `json:"legacyDockerComposeApi,omitempty"`
	DisableDockerRuntimeObserve bool                             `json:"disableDockerRuntimeObserve,omitempty"`
	BootstrapEvidence           platformdeploy.BootstrapEvidence `json:"bootstrapEvidence,omitempty"`
	found                       bool                             `json:"-"`
}

type runtimePlatformDeployOptionsStackAction struct {
	Remote *preparedRuntimeTargetStackAction
}

type runtimePlatformAdapterResultStackAction struct {
	Adapter    platformdeploy.Adapter
	Configured bool
	Checks     []stackActionCheck
	Cleanup    func()
}

type runtimePlatformDeploymentEvidenceStackAction struct {
	Refs       []platformdeploy.DeploymentRef
	SystemApps []models.PlatformAppState
	Apps       []models.PlatformAppState
	Failures   []platformdeploy.ComponentFailure
}

var startRuntimePlatformSSHTunnelStackAction = startRuntimePlatformSSHTunnelDefaultStackAction

func runRuntimePlatformAppDeploymentsStackAction(ctx context.Context, deployDir string, opts ...runtimePlatformDeployOptionsStackAction) (runtimePlatformDeploymentEvidenceStackAction, []stackActionCheck, error) {
	options := runtimePlatformDeployOptionsStackAction{}
	if len(opts) > 0 {
		options = opts[0]
	}
	manifestPaths := []string{
		filepath.Join(deployDir, "platform-apps", "manifest.json"),
		filepath.Join(deployDir, ".platform-apps-manifest.json"),
	}

	var evidence runtimePlatformDeploymentEvidenceStackAction
	var checks []stackActionCheck
	manifestSeen := false

	for _, manifestPath := range manifestPaths {
		if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return evidence, checks, fmt.Errorf("inspect platform app manifest: %w", err)
		}
		manifestSeen = true

		bundle, err := platformdeploy.LoadBundleManifest(manifestPath)
		if err != nil {
			return evidence, checks, err
		}

		for _, deployBundle := range runtimePlatformDeploymentBundlesStackAction(bundle) {
			l3Count := runtimeStackKitOwnedAppCountStackAction(deployBundle.Apps)
			deployCount := len(deployBundle.SystemApps) + l3Count
			if deployCount == 0 {
				continue
			}

			adapterResult, adapterErr := runtimePlatformAdapterForBundleStackAction(ctx, deployBundle, deployDir, options)
			checks = append(checks, adapterResult.Checks...)
			var primary platformdeploy.BundleResult
			if adapterErr != nil && !strings.Contains(adapterErr.Error(), "unsupported platform app adapter") {
				return evidence, checks, adapterErr
			}
			if adapterErr != nil || !adapterResult.Configured {
				if adapterErr == nil {
					adapterErr = fmt.Errorf("preferred adapter %s is not configured", deployBundle.Platform)
				}
				primary.Failures = runtimeDeploymentFailuresForBundleStackAction(deployBundle, "adapter-configuration", adapterErr)
			} else {
				if adapterResult.Cleanup != nil {
					defer adapterResult.Cleanup()
				}
				var err error
				primary, err = platformdeploy.ApplyBundleResilient(ctx, adapterResult.Adapter, deployBundle)
				if err != nil {
					return evidence, checks, err
				}
			}

			deployed := append([]platformdeploy.DeploymentRef(nil), primary.Refs...)
			remaining := append([]platformdeploy.ComponentFailure(nil), primary.Failures...)
			fallbackBundle := platformdeploy.StandaloneFallbackBundle(deployBundle, primary.Failures)
			if runtimeDeployablePlatformAppCountStackAction(fallbackBundle) > 0 && !runtimeExplicitStandaloneFallbackStackAction(deployBundle) {
				fallbackOptions := []platformdeploy.LocalComposeOption{}
				if options.Remote != nil {
					fallbackOptions = append(fallbackOptions, platformdeploy.WithLocalComposeEnv(options.Remote.env))
				}
				fallbackResult, err := platformdeploy.ApplyBundleResilient(ctx, platformdeploy.NewLocalComposeAdapter(deployDir, fallbackOptions...), fallbackBundle)
				if err != nil {
					return evidence, checks, err
				}
				deployed = append(deployed, fallbackResult.Refs...)
				remaining = runtimeRemainingPlatformFailuresStackAction(primary.Failures, fallbackResult)
				if len(fallbackResult.Refs) > 0 {
					checks = append(checks, stackActionCheck{Name: "platform_apps_fallback", Status: "warning", Detail: fmt.Sprintf("standalone Compose recovered %d component(s) after %s was unavailable", len(fallbackResult.Refs), deployBundle.Platform)})
				}
			}

			systemApps, apps := runtimePlatformAppStatesStackAction(deployBundle, primary.Refs)
			if strings.EqualFold(strings.TrimSpace(deployBundle.Platform), "none") {
				for i := range systemApps {
					systemApps[i].Management = platformdeploy.AppManagementFallback
				}
				for i := range apps {
					apps[i].Management = platformdeploy.AppManagementFallback
				}
			}
			fallbackSystemApps, fallbackApps := runtimePlatformAppStatesStackAction(fallbackBundle, deployed[len(primary.Refs):])
			for i := range fallbackSystemApps {
				fallbackSystemApps[i].Management = platformdeploy.AppManagementFallback
			}
			for i := range fallbackApps {
				fallbackApps[i].Management = platformdeploy.AppManagementFallback
			}
			systemApps = append(systemApps, fallbackSystemApps...)
			apps = append(apps, fallbackApps...)
			failedSystemApps, failedApps := runtimePlatformFailureStatesStackAction(deployBundle, remaining)
			systemApps = append(systemApps, failedSystemApps...)
			apps = append(apps, failedApps...)
			evidence.Refs = append(evidence.Refs, deployed...)
			evidence.SystemApps = append(evidence.SystemApps, systemApps...)
			evidence.Apps = append(evidence.Apps, apps...)
			evidence.Failures = append(evidence.Failures, remaining...)
			status := "ok"
			detail := fmt.Sprintf("%s deployed %d app(s)", deployBundle.Platform, len(deployed))
			if len(remaining) > 0 {
				status = "warning"
				detail = fmt.Sprintf("%s completed with %d deployed and %d degraded app(s)", deployBundle.Platform, len(deployed), len(remaining))
			}
			checks = append(checks, stackActionCheck{Name: "platform_apps", Status: stackaction.CheckStatus(status), Detail: detail})
		}
	}

	if !manifestSeen {
		checks = append(checks, stackActionCheck{Name: "platform_apps", Status: "skipped", Detail: "manifest not present"})
	} else if len(evidence.Refs) == 0 {
		checks = append(checks, stackActionCheck{Name: "platform_apps", Status: "skipped", Detail: "no StackKit-owned platform apps"})
	}

	return evidence, checks, nil
}

func runtimeDeployablePlatformAppCountStackAction(bundle platformdeploy.BundleManifest) int {
	return len(bundle.SystemApps) + runtimeStackKitOwnedAppCountStackAction(bundle.Apps)
}

func runtimeExplicitStandaloneFallbackStackAction(bundle platformdeploy.BundleManifest) bool {
	return bundle.Fallback.Enabled && bundle.Fallback.Mode == "standalone-compose" && strings.EqualFold(strings.TrimSpace(bundle.Platform), "none")
}

func runtimeDeploymentFailuresForBundleStackAction(bundle platformdeploy.BundleManifest, stage string, err error) []platformdeploy.ComponentFailure {
	var failures []platformdeploy.ComponentFailure
	for _, app := range bundle.SystemApps {
		failures = append(failures, platformdeploy.ComponentFailure{AppName: app.Name, Platform: bundle.Platform, Stage: stage, Message: err.Error(), Retryable: true})
	}
	for _, app := range bundle.Apps {
		if platformdeploy.IsStackKitOwnedApp(app) {
			failures = append(failures, platformdeploy.ComponentFailure{AppName: app.Name, Platform: bundle.Platform, Stage: stage, Message: err.Error(), Retryable: true})
		}
	}
	return failures
}

func runtimeRemainingPlatformFailuresStackAction(primary []platformdeploy.ComponentFailure, fallback platformdeploy.BundleResult) []platformdeploy.ComponentFailure {
	recovered := map[string]bool{}
	for _, ref := range fallback.Refs {
		recovered[ref.AppName] = true
	}
	failed := map[string]bool{}
	for _, failure := range fallback.Failures {
		failed[failure.AppName] = true
	}
	var remaining []platformdeploy.ComponentFailure
	for _, failure := range primary {
		if failure.IdentityCommitted || (!recovered[failure.AppName] && !failed[failure.AppName]) {
			remaining = append(remaining, failure)
		}
	}
	return append(remaining, fallback.Failures...)
}

func runtimePlatformFailureStatesStackAction(bundle platformdeploy.BundleManifest, failures []platformdeploy.ComponentFailure) ([]models.PlatformAppState, []models.PlatformAppState) {
	systemByName := map[string]platformdeploy.SystemAppManifest{}
	for _, app := range bundle.SystemApps {
		systemByName[app.Name] = app
	}
	appByName := map[string]platformdeploy.AppManifest{}
	for _, app := range bundle.Apps {
		appByName[app.Name] = app
	}
	var systemApps, apps []models.PlatformAppState
	for _, failure := range failures {
		if app, ok := systemByName[failure.AppName]; ok {
			state := runtimePlatformAppStateStackAction(platformdeploy.DeploymentRef{Platform: failure.Platform, AppName: failure.AppName, ObservedStatus: "error"}, app.AppManifest, app.Role)
			state.FailureStage, state.FailureMessage, state.Retryable = failure.Stage, failure.Message, failure.Retryable
			systemApps = append(systemApps, state)
			continue
		}
		if app, ok := appByName[failure.AppName]; ok {
			state := runtimePlatformAppStateStackAction(platformdeploy.DeploymentRef{Platform: failure.Platform, AppName: failure.AppName, ObservedStatus: "error"}, app, "")
			state.FailureStage, state.FailureMessage, state.Retryable = failure.Stage, failure.Message, failure.Retryable
			apps = append(apps, state)
		}
	}
	return systemApps, apps
}

func runtimePlatformAppStatesStackAction(bundle platformdeploy.BundleManifest, refs []platformdeploy.DeploymentRef) ([]models.PlatformAppState, []models.PlatformAppState) {
	systemByName := make(map[string]platformdeploy.SystemAppManifest, len(bundle.SystemApps))
	for _, app := range bundle.SystemApps {
		systemByName[app.Name] = app
	}
	appByName := make(map[string]platformdeploy.AppManifest, len(bundle.Apps))
	for _, app := range bundle.Apps {
		if !platformdeploy.IsStackKitOwnedApp(app) {
			continue
		}
		appByName[app.Name] = app
	}

	var systemApps []models.PlatformAppState
	var apps []models.PlatformAppState
	for _, ref := range refs {
		if app, ok := systemByName[ref.AppName]; ok {
			systemApps = append(systemApps, runtimePlatformAppStateStackAction(ref, app.AppManifest, app.Role))
			continue
		}
		if app, ok := appByName[ref.AppName]; ok {
			apps = append(apps, runtimePlatformAppStateStackAction(ref, app, ""))
		}
	}
	return systemApps, apps
}

func runtimePlatformAppStateStackAction(ref platformdeploy.DeploymentRef, app platformdeploy.AppManifest, role string) models.PlatformAppState {
	return models.PlatformAppState{
		Name:           ref.AppName,
		Role:           role,
		Platform:       ref.Platform,
		Management:     platformdeploy.AppManagementManaged,
		ExternalID:     ref.ExternalID,
		DeploymentID:   ref.DeploymentID,
		ObservedStatus: ref.ObservedStatus,
		ObservedAt:     ref.ObservedAt,
		ComposePath:    app.ComposePath,
		SetupPolicy:    app.SetupPolicy,
		SetupDrops:     runtimeSetupDropsToStateStackAction(app.SetupDrops),
		LastDeployed:   ref.LastDeployed,
	}
}

func runtimeSetupDropsToStateStackAction(drops []platformdeploy.SetupDropManifest) []models.SetupDropSpec {
	if len(drops) == 0 {
		return nil
	}
	stateDrops := make([]models.SetupDropSpec, 0, len(drops))
	for _, drop := range drops {
		stateDrops = append(stateDrops, models.SetupDropSpec{
			Name:          drop.Name,
			Version:       drop.Version,
			Runner:        drop.Runner,
			Description:   drop.Description,
			RollbackNotes: append([]string(nil), drop.RollbackNotes...),
			Command:       append([]string(nil), drop.Command...),
			Env:           drop.Env,
			Secrets:       drop.Secrets,
		})
	}
	return stateDrops
}

func runtimePlatformDeploymentBundlesStackAction(bundle platformdeploy.BundleManifest) []platformdeploy.BundleManifest {
	groups := map[string]*platformdeploy.BundleManifest{}
	order := []string{}

	ensure := func(platform string) *platformdeploy.BundleManifest {
		platform = strings.TrimSpace(platform)
		if platform == "" {
			platform = strings.TrimSpace(bundle.Platform)
		}
		if platform == "" {
			platform = "none"
		}
		if _, ok := groups[platform]; !ok {
			groups[platform] = &platformdeploy.BundleManifest{
				Version:  bundle.Version,
				Platform: platform,
				Fallback: bundle.Fallback,
			}
			order = append(order, platform)
		}
		return groups[platform]
	}

	for _, app := range bundle.SystemApps {
		platform := runtimeFirstNonEmptyStackAction(app.Platform, app.ManagedBy, bundle.Platform)
		group := ensure(platform)
		group.SystemApps = append(group.SystemApps, app)
	}
	for _, app := range bundle.Apps {
		if !platformdeploy.IsStackKitOwnedApp(app) {
			continue
		}
		platform := runtimeFirstNonEmptyStackAction(app.Platform, app.ManagedBy, bundle.Platform)
		group := ensure(platform)
		group.Apps = append(group.Apps, app)
	}

	out := make([]platformdeploy.BundleManifest, 0, len(order))
	for _, platform := range order {
		group := groups[platform]
		if len(group.SystemApps) == 0 && len(group.Apps) == 0 {
			continue
		}
		out = append(out, *group)
	}
	return out
}

func runtimeStackKitOwnedAppCountStackAction(apps []platformdeploy.AppManifest) int {
	count := 0
	for _, app := range apps {
		if platformdeploy.IsStackKitOwnedApp(app) {
			count++
		}
	}
	return count
}

func runtimePlatformAdapterForBundleStackAction(ctx context.Context, bundle platformdeploy.BundleManifest, deployDir string, options runtimePlatformDeployOptionsStackAction) (runtimePlatformAdapterResultStackAction, error) {
	switch strings.ToLower(strings.TrimSpace(bundle.Platform)) {
	case "", "none":
		localOptions := []platformdeploy.LocalComposeOption{}
		if options.Remote != nil {
			localOptions = append(localOptions, platformdeploy.WithLocalComposeEnv(options.Remote.env))
		}
		return runtimePlatformAdapterResultStackAction{Adapter: platformdeploy.NewLocalComposeAdapter(deployDir, localOptions...), Configured: true}, nil
	case "coolify":
		cfg, configured, checks, cleanup, err := runtimePlatformHTTPConfigForBundleStackAction(ctx, bundle, deployDir, options)
		if err != nil {
			return runtimePlatformAdapterResultStackAction{}, err
		}
		if !configured {
			return runtimePlatformAdapterResultStackAction{Checks: checks}, nil
		}
		return runtimePlatformAdapterResultStackAction{Adapter: platformdeploy.NewCoolifyAdapter(cfg), Configured: true, Checks: checks, Cleanup: cleanup}, nil
	case "dokploy":
		cfg, configured, checks, cleanup, err := runtimePlatformHTTPConfigForBundleStackAction(ctx, bundle, deployDir, options)
		if err != nil {
			return runtimePlatformAdapterResultStackAction{}, err
		}
		if !configured {
			return runtimePlatformAdapterResultStackAction{Checks: checks}, nil
		}
		return runtimePlatformAdapterResultStackAction{Adapter: platformdeploy.NewDokployAdapter(cfg), Configured: true, Checks: checks, Cleanup: cleanup}, nil
	case "komodo":
		cfg, configured, checks, cleanup, err := runtimePlatformHTTPConfigForBundleStackAction(ctx, bundle, deployDir, options)
		if err != nil {
			return runtimePlatformAdapterResultStackAction{}, err
		}
		if !configured {
			return runtimePlatformAdapterResultStackAction{Checks: checks}, nil
		}
		return runtimePlatformAdapterResultStackAction{Adapter: platformdeploy.NewKomodoAdapter(cfg), Configured: true, Checks: checks, Cleanup: cleanup}, nil
	default:
		return runtimePlatformAdapterResultStackAction{}, fmt.Errorf("unsupported platform app adapter %q", bundle.Platform)
	}
}

func runtimePlatformHTTPConfigForBundleStackAction(ctx context.Context, bundle platformdeploy.BundleManifest, deployDir string, options runtimePlatformDeployOptionsStackAction) (platformdeploy.HTTPConfig, bool, []stackActionCheck, func(), error) {
	persisted := runtimeLoadPlatformConfigFileStackAction(deployDir)
	cfg := platformdeploy.HTTPConfig{
		BaseURL:                     runtimeFirstNonEmptyStackAction(runtimeFirstPlatformEnvStackAction(bundle.Platform, "endpoint"), persisted.endpointStackAction()),
		Token:                       runtimeFirstNonEmptyStackAction(runtimeFirstPlatformEnvStackAction(bundle.Platform, "token"), persisted.Token),
		APIKey:                      runtimeFirstNonEmptyStackAction(runtimeFirstPlatformEnvStackAction(bundle.Platform, "api_key"), persisted.APIKey, persisted.Token),
		Secret:                      runtimeFirstNonEmptyStackAction(runtimeFirstPlatformEnvStackAction(bundle.Platform, "api_secret"), persisted.APISecret),
		EnvironmentID:               runtimeFirstNonEmptyStackAction(runtimeFirstPlatformEnvStackAction(bundle.Platform, "environment_id"), persisted.EnvironmentID),
		ServerID:                    runtimeFirstNonEmptyStackAction(runtimeFirstPlatformEnvStackAction(bundle.Platform, "server_id"), persisted.ServerID),
		ProjectUUID:                 runtimeFirstNonEmptyStackAction(runtimeFirstPlatformEnvStackAction(bundle.Platform, "project_uuid"), persisted.ProjectUUID),
		EnvironmentUUID:             runtimeFirstNonEmptyStackAction(runtimeFirstPlatformEnvStackAction(bundle.Platform, "environment_uuid"), persisted.EnvironmentUUID),
		DestinationUUID:             runtimeFirstNonEmptyStackAction(runtimeFirstPlatformEnvStackAction(bundle.Platform, "destination_uuid"), persisted.DestinationUUID),
		LegacyDockerComposeAPI:      persisted.LegacyDockerComposeAPI,
		DisableDockerRuntimeObserve: persisted.DisableDockerRuntimeObserve,
		WaitForReadiness:            strings.EqualFold(strings.TrimSpace(bundle.Platform), "coolify"),
	}
	if options.Remote != nil && len(options.Remote.env) > 0 {
		cfg.DockerEnv = append([]string(nil), options.Remote.env...)
	}
	checks := []stackActionCheck{}
	if persisted.found && platformdeploy.RequiresBootstrapEvidence(bundle.Platform) {
		if err := platformdeploy.ValidateBootstrapEvidence(bundle.Platform, persisted.BootstrapEvidence); err != nil {
			return cfg, false, checks, nil, err
		}
		checks = append(checks, stackActionCheck{
			Name:   "platform_bootstrap_evidence",
			Status: "ok",
			Detail: fmt.Sprintf("%s bootstrap evidence covers required beta capabilities", strings.ToLower(strings.TrimSpace(bundle.Platform))),
		})
	}
	cleanup := func() {}
	tunnelURL, tunnelCleanup, tunnelErr := runtimePlatformTunnelEndpointStackAction(ctx, bundle.Platform, cfg.BaseURL, options.Remote)
	if tunnelErr != nil {
		return cfg, false, checks, nil, tunnelErr
	}
	if tunnelURL != "" {
		cfg.BaseURL = tunnelURL
		cleanup = tunnelCleanup
		checks = append(checks, stackActionCheck{
			Name:   "platform_api_tunnel",
			Status: "ok",
			Detail: fmt.Sprintf("%s API endpoint forwarded to remote runtime target", strings.ToLower(strings.TrimSpace(bundle.Platform))),
		})
	}
	if bundle.Platform == "komodo" {
		return cfg, cfg.BaseURL != "" && cfg.APIKey != "" && cfg.Secret != "", checks, cleanup, nil
	}
	return cfg, cfg.BaseURL != "" && cfg.Token != "", checks, cleanup, nil
}

func runtimeLoadPlatformConfigFileStackAction(deployDir string) runtimePlatformConfigFileStackAction {
	path := filepath.Join(deployDir, ".stackkit", "platform.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return runtimePlatformConfigFileStackAction{}
	}
	var cfg runtimePlatformConfigFileStackAction
	if err := json.Unmarshal(data, &cfg); err != nil {
		return runtimePlatformConfigFileStackAction{}
	}
	cfg.found = true
	return cfg
}

func (cfg runtimePlatformConfigFileStackAction) endpointStackAction() string {
	return runtimeFirstNonEmptyStackAction(cfg.Endpoint, cfg.BaseURL)
}

func runtimeFirstPlatformEnvStackAction(platform, suffix string) string {
	keyPlatform := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(strings.TrimSpace(platform)))
	keySuffix := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(strings.TrimSpace(suffix)))
	if keyPlatform == "" || keySuffix == "" {
		return ""
	}
	return os.Getenv("STACKKIT_" + keyPlatform + "_" + keySuffix)
}

func runtimeFirstNonEmptyStackAction(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func runtimePlatformTunnelEndpointStackAction(ctx context.Context, platform, endpointStackAction string, remote *preparedRuntimeTargetStackAction) (string, func(), error) {
	if remote == nil || remote.target == nil || strings.TrimSpace(remote.keyPath) == "" {
		return "", nil, nil
	}
	remoteHost, remotePort, ok := runtimePlatformLoopbackEndpointStackAction(endpointStackAction)
	if !ok {
		return "", nil, nil
	}
	localURL, cleanup, err := startRuntimePlatformSSHTunnelStackAction(ctx, remote, remoteHost, remotePort)
	if err != nil {
		return "", nil, fmt.Errorf("%s platform API endpoint %q is node-local but SSH tunnel setup failed: %w", strings.ToLower(strings.TrimSpace(platform)), endpointStackAction, err)
	}
	return localURL, cleanup, nil
}

func runtimePlatformLoopbackEndpointStackAction(endpointStackAction string) (string, string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(endpointStackAction))
	if err != nil || parsed.Scheme == "" {
		return "", "", false
	}
	host := strings.ToLower(strings.Trim(parsed.Hostname(), "[]"))
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return "", "", false
	}
	port := parsed.Port()
	if port == "" {
		switch parsed.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return "", "", false
		}
	}
	return "127.0.0.1", port, true
}

func startRuntimePlatformSSHTunnelDefaultStackAction(ctx context.Context, remote *preparedRuntimeTargetStackAction, remoteHost, remotePort string) (string, func(), error) {
	if remote == nil || remote.target == nil || strings.TrimSpace(remote.keyPath) == "" {
		return "", nil, fmt.Errorf("runtime target SSH material is required")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("reserve local tunnel port: %w", err)
	}
	localPort := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	tunnelCtx, cancel := context.WithCancel(ctx)
	args := append(runtimeTargetSSHBaseArgsStackAction(remote.target, remote.keyPath),
		"-N",
		"-L", "127.0.0.1:"+strconv.Itoa(localPort)+":"+remoteHost+":"+remotePort,
		remote.target.User+"@"+remote.target.Host,
	)
	cmd := exec.CommandContext(tunnelCtx, "ssh", args...) // #nosec G204 -- SSH args are assembled without shell interpolation.
	var output strings.Builder
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		cancel()
		return "", nil, fmt.Errorf("start ssh tunnel: %w", err)
	}
	cleanup := func() {
		cancel()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	if err := waitForRuntimePlatformTunnelStackAction(ctx, localPort, &output); err != nil {
		cleanup()
		return "", nil, err
	}
	return "http://127.0.0.1:" + strconv.Itoa(localPort), cleanup, nil
}

func waitForRuntimePlatformTunnelStackAction(ctx context.Context, localPort int, output *strings.Builder) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(localPort), 300*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			detail := strings.TrimSpace(output.String())
			if detail != "" {
				return fmt.Errorf("wait for ssh tunnel: %s", detail)
			}
			return fmt.Errorf("wait for ssh tunnel on 127.0.0.1:%d: %w", localPort, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
