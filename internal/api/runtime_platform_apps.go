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
	"github.com/kombifyio/stackkits/pkg/models"
)

type runtimePlatformConfigFile struct {
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

type runtimePlatformDeployOptions struct {
	Remote *preparedRuntimeTarget
}

type runtimePlatformAdapterResult struct {
	Adapter    platformdeploy.Adapter
	Configured bool
	Checks     []runtimeActionCheck
	Cleanup    func()
}

type runtimePlatformDeploymentEvidence struct {
	Refs       []platformdeploy.DeploymentRef
	SystemApps []models.PlatformAppState
	Apps       []models.PlatformAppState
}

var startRuntimePlatformSSHTunnel = startRuntimePlatformSSHTunnelDefault

func runRuntimePlatformAppDeployments(ctx context.Context, deployDir string, opts ...runtimePlatformDeployOptions) (runtimePlatformDeploymentEvidence, []runtimeActionCheck, error) {
	options := runtimePlatformDeployOptions{}
	if len(opts) > 0 {
		options = opts[0]
	}
	manifestPaths := []string{
		filepath.Join(deployDir, "platform-apps", "manifest.json"),
		filepath.Join(deployDir, ".platform-apps-manifest.json"),
	}

	var evidence runtimePlatformDeploymentEvidence
	var checks []runtimeActionCheck
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

		for _, deployBundle := range runtimePlatformDeploymentBundles(bundle) {
			l3Count := runtimeStackKitOwnedAppCount(deployBundle.Apps)
			deployCount := len(deployBundle.SystemApps) + l3Count
			if deployCount == 0 {
				continue
			}

			adapterResult, err := runtimePlatformAdapterForBundle(ctx, deployBundle, deployDir, options)
			if err != nil {
				return evidence, checks, err
			}
			checks = append(checks, adapterResult.Checks...)
			if !adapterResult.Configured {
				return evidence, checks, fmt.Errorf("platform API config for %s is required for %d StackKit-owned app(s)", deployBundle.Platform, deployCount)
			}
			if adapterResult.Cleanup != nil {
				defer adapterResult.Cleanup()
			}

			deployed, err := platformdeploy.ApplyBundle(ctx, adapterResult.Adapter, deployBundle)
			if err != nil {
				return evidence, checks, err
			}
			systemApps, apps := runtimePlatformAppStates(deployBundle, deployed)
			evidence.Refs = append(evidence.Refs, deployed...)
			evidence.SystemApps = append(evidence.SystemApps, systemApps...)
			evidence.Apps = append(evidence.Apps, apps...)
			checks = append(checks, runtimeActionCheck{
				Name:   "platform_apps",
				Status: "ok",
				Detail: fmt.Sprintf("%s deployed %d app(s)", deployBundle.Platform, len(deployed)),
			})
		}
	}

	if !manifestSeen {
		checks = append(checks, runtimeActionCheck{Name: "platform_apps", Status: "skipped", Detail: "manifest not present"})
	} else if len(evidence.Refs) == 0 {
		checks = append(checks, runtimeActionCheck{Name: "platform_apps", Status: "skipped", Detail: "no StackKit-owned platform apps"})
	}

	return evidence, checks, nil
}

func runtimePlatformAppStates(bundle platformdeploy.BundleManifest, refs []platformdeploy.DeploymentRef) ([]models.PlatformAppState, []models.PlatformAppState) {
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
			systemApps = append(systemApps, runtimePlatformAppState(ref, app.AppManifest, app.Role))
			continue
		}
		if app, ok := appByName[ref.AppName]; ok {
			apps = append(apps, runtimePlatformAppState(ref, app, ""))
		}
	}
	return systemApps, apps
}

func runtimePlatformAppState(ref platformdeploy.DeploymentRef, app platformdeploy.AppManifest, role string) models.PlatformAppState {
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
		SetupDrops:     runtimeSetupDropsToState(app.SetupDrops),
		LastDeployed:   ref.LastDeployed,
	}
}

func runtimeSetupDropsToState(drops []platformdeploy.SetupDropManifest) []models.SetupDropSpec {
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

func runtimePlatformDeploymentBundles(bundle platformdeploy.BundleManifest) []platformdeploy.BundleManifest {
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
		platform := runtimeFirstNonEmpty(app.Platform, app.ManagedBy, bundle.Platform)
		group := ensure(platform)
		group.SystemApps = append(group.SystemApps, app)
	}
	for _, app := range bundle.Apps {
		if !platformdeploy.IsStackKitOwnedApp(app) {
			continue
		}
		platform := runtimeFirstNonEmpty(app.Platform, app.ManagedBy, bundle.Platform)
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

func runtimeStackKitOwnedAppCount(apps []platformdeploy.AppManifest) int {
	count := 0
	for _, app := range apps {
		if platformdeploy.IsStackKitOwnedApp(app) {
			count++
		}
	}
	return count
}

func runtimePlatformAdapterForBundle(ctx context.Context, bundle platformdeploy.BundleManifest, deployDir string, options runtimePlatformDeployOptions) (runtimePlatformAdapterResult, error) {
	switch strings.ToLower(strings.TrimSpace(bundle.Platform)) {
	case "", "none":
		if bundle.Fallback.Enabled && bundle.Fallback.Mode == "standalone-compose" {
			return runtimePlatformAdapterResult{Adapter: platformdeploy.NewLocalComposeAdapter(deployDir), Configured: true}, nil
		}
		return runtimePlatformAdapterResult{}, fmt.Errorf("local compose adapter requires platformFallback.mode=standalone-compose")
	case "coolify":
		cfg, configured, checks, cleanup, err := runtimePlatformHTTPConfigForBundle(ctx, bundle, deployDir, options)
		if err != nil {
			return runtimePlatformAdapterResult{}, err
		}
		if !configured {
			return runtimePlatformAdapterResult{Checks: checks}, nil
		}
		return runtimePlatformAdapterResult{Adapter: platformdeploy.NewCoolifyAdapter(cfg), Configured: true, Checks: checks, Cleanup: cleanup}, nil
	case "dokploy":
		cfg, configured, checks, cleanup, err := runtimePlatformHTTPConfigForBundle(ctx, bundle, deployDir, options)
		if err != nil {
			return runtimePlatformAdapterResult{}, err
		}
		if !configured {
			return runtimePlatformAdapterResult{Checks: checks}, nil
		}
		return runtimePlatformAdapterResult{Adapter: platformdeploy.NewDokployAdapter(cfg), Configured: true, Checks: checks, Cleanup: cleanup}, nil
	case "komodo":
		cfg, configured, checks, cleanup, err := runtimePlatformHTTPConfigForBundle(ctx, bundle, deployDir, options)
		if err != nil {
			return runtimePlatformAdapterResult{}, err
		}
		if !configured {
			return runtimePlatformAdapterResult{Checks: checks}, nil
		}
		return runtimePlatformAdapterResult{Adapter: platformdeploy.NewKomodoAdapter(cfg), Configured: true, Checks: checks, Cleanup: cleanup}, nil
	default:
		return runtimePlatformAdapterResult{}, fmt.Errorf("unsupported platform app adapter %q", bundle.Platform)
	}
}

func runtimePlatformHTTPConfigForBundle(ctx context.Context, bundle platformdeploy.BundleManifest, deployDir string, options runtimePlatformDeployOptions) (platformdeploy.HTTPConfig, bool, []runtimeActionCheck, func(), error) {
	persisted := runtimeLoadPlatformConfigFile(deployDir)
	cfg := platformdeploy.HTTPConfig{
		BaseURL:                     runtimeFirstNonEmpty(runtimeFirstPlatformEnv(bundle.Platform, "endpoint"), persisted.endpoint()),
		Token:                       runtimeFirstNonEmpty(runtimeFirstPlatformEnv(bundle.Platform, "token"), persisted.Token),
		APIKey:                      runtimeFirstNonEmpty(runtimeFirstPlatformEnv(bundle.Platform, "api_key"), persisted.APIKey, persisted.Token),
		Secret:                      runtimeFirstNonEmpty(runtimeFirstPlatformEnv(bundle.Platform, "api_secret"), persisted.APISecret),
		EnvironmentID:               runtimeFirstNonEmpty(runtimeFirstPlatformEnv(bundle.Platform, "environment_id"), persisted.EnvironmentID),
		ServerID:                    runtimeFirstNonEmpty(runtimeFirstPlatformEnv(bundle.Platform, "server_id"), persisted.ServerID),
		ProjectUUID:                 runtimeFirstNonEmpty(runtimeFirstPlatformEnv(bundle.Platform, "project_uuid"), persisted.ProjectUUID),
		EnvironmentUUID:             runtimeFirstNonEmpty(runtimeFirstPlatformEnv(bundle.Platform, "environment_uuid"), persisted.EnvironmentUUID),
		DestinationUUID:             runtimeFirstNonEmpty(runtimeFirstPlatformEnv(bundle.Platform, "destination_uuid"), persisted.DestinationUUID),
		LegacyDockerComposeAPI:      persisted.LegacyDockerComposeAPI,
		DisableDockerRuntimeObserve: persisted.DisableDockerRuntimeObserve,
		WaitForReadiness:            strings.EqualFold(strings.TrimSpace(bundle.Platform), "coolify"),
	}
	if options.Remote != nil && len(options.Remote.env) > 0 {
		cfg.DockerEnv = append([]string(nil), options.Remote.env...)
	}
	checks := []runtimeActionCheck{}
	if persisted.found && platformdeploy.RequiresBootstrapEvidence(bundle.Platform) {
		if err := platformdeploy.ValidateBootstrapEvidence(bundle.Platform, persisted.BootstrapEvidence); err != nil {
			return cfg, false, checks, nil, err
		}
		checks = append(checks, runtimeActionCheck{
			Name:   "platform_bootstrap_evidence",
			Status: "ok",
			Detail: fmt.Sprintf("%s bootstrap evidence covers required beta capabilities", strings.ToLower(strings.TrimSpace(bundle.Platform))),
		})
	}
	cleanup := func() {}
	tunnelURL, tunnelCleanup, tunnelErr := runtimePlatformTunnelEndpoint(ctx, bundle.Platform, cfg.BaseURL, options.Remote)
	if tunnelErr != nil {
		return cfg, false, checks, nil, tunnelErr
	}
	if tunnelURL != "" {
		cfg.BaseURL = tunnelURL
		cleanup = tunnelCleanup
		checks = append(checks, runtimeActionCheck{
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

func runtimeLoadPlatformConfigFile(deployDir string) runtimePlatformConfigFile {
	path := filepath.Join(deployDir, ".stackkit", "platform.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return runtimePlatformConfigFile{}
	}
	var cfg runtimePlatformConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return runtimePlatformConfigFile{}
	}
	cfg.found = true
	return cfg
}

func (cfg runtimePlatformConfigFile) endpoint() string {
	return runtimeFirstNonEmpty(cfg.Endpoint, cfg.BaseURL)
}

func runtimeFirstPlatformEnv(platform, suffix string) string {
	keyPlatform := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(strings.TrimSpace(platform)))
	keySuffix := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(strings.TrimSpace(suffix)))
	if keyPlatform == "" || keySuffix == "" {
		return ""
	}
	return os.Getenv("STACKKIT_" + keyPlatform + "_" + keySuffix)
}

func runtimeFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func runtimePlatformTunnelEndpoint(ctx context.Context, platform, endpoint string, remote *preparedRuntimeTarget) (string, func(), error) {
	if remote == nil || remote.target == nil || strings.TrimSpace(remote.keyPath) == "" {
		return "", nil, nil
	}
	remoteHost, remotePort, ok := runtimePlatformLoopbackEndpoint(endpoint)
	if !ok {
		return "", nil, nil
	}
	localURL, cleanup, err := startRuntimePlatformSSHTunnel(ctx, remote, remoteHost, remotePort)
	if err != nil {
		return "", nil, fmt.Errorf("%s platform API endpoint %q is node-local but SSH tunnel setup failed: %w", strings.ToLower(strings.TrimSpace(platform)), endpoint, err)
	}
	return localURL, cleanup, nil
}

func runtimePlatformLoopbackEndpoint(endpoint string) (string, string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
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

func startRuntimePlatformSSHTunnelDefault(ctx context.Context, remote *preparedRuntimeTarget, remoteHost, remotePort string) (string, func(), error) {
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
	args := append(runtimeTargetSSHBaseArgs(remote.target, remote.keyPath),
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
	if err := waitForRuntimePlatformTunnel(ctx, localPort, &output); err != nil {
		cleanup()
		return "", nil, err
	}
	return "http://127.0.0.1:" + strconv.Itoa(localPort), cleanup, nil
}

func waitForRuntimePlatformTunnel(ctx context.Context, localPort int, output *strings.Builder) error {
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
