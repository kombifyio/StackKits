package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/platformdeploy"
	"github.com/kombifyio/stackkits/internal/runtimeaction"
	"github.com/kombifyio/stackkits/pkg/models"
)

const runtimeObservationHTTPTimeout = 10 * time.Second

var (
	errRuntimeHealthProbeDisallowedTarget = errors.New("health probe target is not publicly routable")
	errRuntimeHealthProbeRedirect         = errors.New("health probe redirects are not allowed")
)

// runtimeObservationDockerClient is deliberately the narrow Docker surface
// already used by StackKit verification. A runtime action must not create a
// second status subsystem merely to render dashboard health.
type runtimeObservationDockerClient interface {
	IsInstalled() bool
	IsRunning(context.Context) bool
	GetStackKitContainers(context.Context) ([]docker.ContainerInfo, error)
	GetContainersByLabel(context.Context, string) ([]docker.ContainerInfo, error)
	GetContainerHealth(context.Context, string) (models.HealthStatus, error)
}

var newRuntimeObservationDockerClient = func(env []string) runtimeObservationDockerClient {
	return docker.NewClient(docker.WithEnv(env...))
}

var newRuntimeObservationHTTPClient = func() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Health probes are a measured public-service signal, never a mechanism to
	// reach operator-local services through a proxy inherited from the node.
	transport.Proxy = nil
	transport.DialContext = runtimeHealthProbeDialContext
	return &http.Client{Timeout: runtimeObservationHTTPTimeout, Transport: transport}
}

var runtimeHealthProbeLookupIP = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// collectRuntimeLiveObservation converts current Docker, platform-adapter, and
// optional health-path evidence into the versioned runtime-action contract.
// It is best-effort: an unreachable host is returned as measured evidence and
// does not turn a completed rollout/verify operation into a synthetic success
// or erase its original result.
func collectRuntimeLiveObservation(
	ctx context.Context,
	resp runtimeActionResponse,
	remote *preparedRuntimeTarget,
	refs []platformdeploy.DeploymentRef,
) *runtimeaction.LiveObservation {
	observedAt := time.Now().UTC()
	observation := &runtimeaction.LiveObservation{
		Version:    runtimeaction.ObservationVersionV1,
		ObservedAt: observedAt,
		Host: runtimeaction.HostObservation{
			Host: runtimeObservationHost(remote),
		},
		Platform: runtimeObservationPlatform(resp.TofuDir, refs),
	}

	env := []string(nil)
	if remote != nil {
		env = append(env, remote.env...)
	}
	client := newRuntimeObservationDockerClient(env)
	if !client.IsInstalled() {
		return runtimeObservationHostFailure(observation, "docker_not_installed")
	}
	if !client.IsRunning(ctx) {
		return runtimeObservationHostFailure(observation, "docker_unreachable")
	}
	observation.Host.Reachable = true
	observation.Host.DockerReachable = true

	healthTargets := runtimeHealthProbeTargets(resp.TofuDir)
	seenContainers := map[string]bool{}
	seenServices := map[string]bool{}

	for _, ref := range refs {
		service := runtimePlatformServiceObservation(ctx, client, ref)
		if target, ok := healthTargets[service.Name]; ok {
			service.HealthPath = target.HealthPath
			service.Probe = probeRuntimeHealthPath(ctx, target.URL)
			service.Status, service.FailureClass = runtimeServiceStatusWithProbe(service.Status, service.FailureClass, service.Probe)
			seenServices[service.Name] = true
		}
		for _, container := range service.Containers {
			if container.ID != "" {
				seenContainers[container.ID] = true
			}
		}
		observation.Services = append(observation.Services, service)
	}

	containers, err := client.GetStackKitContainers(ctx)
	if err != nil {
		observation.FailureClass = "container_inventory_failed"
	} else {
		for _, container := range containers {
			if seenContainers[container.ID] {
				continue
			}
			service := runtimeContainerServiceObservation(ctx, client, container)
			if target, ok := healthTargets[service.Name]; ok {
				service.HealthPath = target.HealthPath
				service.Probe = probeRuntimeHealthPath(ctx, target.URL)
				service.Status, service.FailureClass = runtimeServiceStatusWithProbe(service.Status, service.FailureClass, service.Probe)
				seenServices[service.Name] = true
			}
			observation.Services = append(observation.Services, service)
		}
	}

	// Verify actions may not have fresh platform refs, but a generated
	// healthPath is still current runtime evidence. Do not derive health from
	// stackkit_outputs when a probe is unavailable.
	for name, target := range healthTargets {
		if seenServices[name] {
			continue
		}
		probe := probeRuntimeHealthPath(ctx, target.URL)
		status, failureClass := runtimeServiceStatusWithProbe(runtimeaction.ServiceHealthUnknown, "", probe)
		observation.Services = append(observation.Services, runtimeaction.ServiceObservation{
			Name:         name,
			Status:       status,
			HealthPath:   target.HealthPath,
			Probe:        probe,
			FailureClass: failureClass,
		})
	}

	sort.SliceStable(observation.Services, func(i, j int) bool {
		return observation.Services[i].Name < observation.Services[j].Name
	})
	return observation
}

func runtimeObservationHost(remote *preparedRuntimeTarget) string {
	if remote != nil && remote.target != nil && strings.TrimSpace(remote.target.Host) != "" {
		return strings.TrimSpace(remote.target.Host)
	}
	return "local"
}

// prepareRuntimeObservationTarget opens the same Docker-over-SSH transport as
// a rollout without running bootstrap or writing tfvars. Verify must remain a
// read-only observation path.
func prepareRuntimeObservationTarget(target *runtimeActionTarget) (*preparedRuntimeTarget, func(), error) {
	target = normalizeRuntimeActionTarget(target)
	if target == nil {
		return nil, nil, nil
	}
	dockerHost, err := runtimeTargetDockerHost(target)
	if err != nil {
		return nil, nil, err
	}
	keyPath, homeDir, cleanup, err := materializeRuntimeTargetSSHKey(target)
	if err != nil {
		return nil, cleanup, err
	}
	env := []string{"DOCKER_HOST=" + dockerHost}
	if homeDir != "" {
		env = append(env, "HOME="+homeDir)
	}
	if keyPath != "" {
		env = append(env, "DOCKER_SSH_COMMAND="+runtimeTargetSSHCommand(target, keyPath))
	}
	return &preparedRuntimeTarget{
		dockerHost: dockerHost,
		env:        env,
		target:     target,
		keyPath:    keyPath,
	}, cleanup, nil
}

func runtimeObservationTargetFailure(target *runtimeActionTarget, failureClass string) *runtimeaction.LiveObservation {
	target = normalizeRuntimeActionTarget(target)
	host := "local"
	if target != nil {
		host = target.Host
	}
	return &runtimeaction.LiveObservation{
		Version:    runtimeaction.ObservationVersionV1,
		ObservedAt: time.Now().UTC(),
		Host: runtimeaction.HostObservation{
			Host:         host,
			FailureClass: failureClass,
		},
		FailureClass: failureClass,
	}
}

func runtimeObservationHostFailure(observation *runtimeaction.LiveObservation, failureClass string) *runtimeaction.LiveObservation {
	observation.Host.FailureClass = failureClass
	observation.FailureClass = failureClass
	return observation
}

func runtimeObservationPlatform(deployDir string, refs []platformdeploy.DeploymentRef) *runtimeaction.PlatformObservation {
	cfg := runtimeLoadPlatformConfigFile(deployDir)
	platform := runtimeFirstNonEmpty(cfg.Platform)
	if platform == "" && len(refs) > 0 {
		platform = strings.TrimSpace(refs[0].Platform)
	}
	observation := &runtimeaction.PlatformObservation{
		Name:            platform,
		Endpoint:        cfg.endpoint(),
		ServerID:        strings.TrimSpace(cfg.ServerID),
		ProjectUUID:     strings.TrimSpace(cfg.ProjectUUID),
		EnvironmentUUID: strings.TrimSpace(cfg.EnvironmentUUID),
		DestinationUUID: strings.TrimSpace(cfg.DestinationUUID),
	}
	if observation.Name == "" && observation.Endpoint == "" && observation.ServerID == "" && observation.ProjectUUID == "" && observation.EnvironmentUUID == "" && observation.DestinationUUID == "" {
		return nil
	}
	return observation
}

func runtimePlatformServiceObservation(ctx context.Context, client runtimeObservationDockerClient, ref platformdeploy.DeploymentRef) runtimeaction.ServiceObservation {
	service := runtimeaction.ServiceObservation{
		Name:           runtimeFirstNonEmpty(ref.AppName, ref.ExternalID),
		Status:         runtimeObservedServiceStatus(ref.ObservedStatus),
		PlatformAppID:  strings.TrimSpace(ref.ExternalID),
		PlatformStatus: strings.TrimSpace(ref.ObservedStatus),
	}
	if service.Name == "" {
		service.Name = "platform-app"
	}
	if service.Status == runtimeaction.ServiceHealthUnhealthy {
		service.FailureClass = "platform_unhealthy"
	}
	if strings.TrimSpace(ref.ExternalID) == "" {
		return service
	}

	containers, err := client.GetContainersByLabel(ctx, "com.docker.compose.project="+ref.ExternalID)
	if err != nil {
		if service.FailureClass == "" {
			service.FailureClass = "container_inventory_failed"
		}
		return service
	}
	for _, container := range containers {
		observation, status, failureClass := runtimeContainerObservation(ctx, client, container)
		service.Containers = append(service.Containers, observation)
		service.Status, service.FailureClass = runtimeCombineServiceStatus(service.Status, service.FailureClass, status, failureClass)
	}
	return service
}

func runtimeContainerServiceObservation(ctx context.Context, client runtimeObservationDockerClient, container docker.ContainerInfo) runtimeaction.ServiceObservation {
	containerObservation, status, failureClass := runtimeContainerObservation(ctx, client, container)
	name := strings.TrimSpace(container.Name)
	if name == "" {
		name = strings.TrimSpace(container.ID)
	}
	if name == "" {
		name = "container"
	}
	return runtimeaction.ServiceObservation{
		Name:         name,
		Status:       status,
		Containers:   []runtimeaction.ContainerObservation{containerObservation},
		FailureClass: failureClass,
	}
}

func runtimeContainerObservation(ctx context.Context, client runtimeObservationDockerClient, container docker.ContainerInfo) (runtimeaction.ContainerObservation, runtimeaction.ServiceHealthStatus, string) {
	observation := runtimeaction.ContainerObservation{
		ID:      strings.TrimSpace(container.ID),
		Name:    strings.TrimSpace(container.Name),
		State:   strings.TrimSpace(container.State.Status),
		Running: container.State.Running,
	}
	if !container.State.Running {
		return observation, runtimeaction.ServiceHealthUnhealthy, "container_not_running"
	}

	target := observation.ID
	if target == "" {
		target = observation.Name
	}
	health, err := client.GetContainerHealth(ctx, target)
	if err != nil {
		return observation, runtimeaction.ServiceHealthUnknown, "docker_inspect_failed"
	}
	observation.Health = string(health)
	switch health {
	case models.HealthStatusHealthy:
		return observation, runtimeaction.ServiceHealthHealthy, ""
	case models.HealthStatusStarting:
		return observation, runtimeaction.ServiceHealthStarting, ""
	case models.HealthStatusUnhealthy:
		return observation, runtimeaction.ServiceHealthUnhealthy, "docker_health_unhealthy"
	default:
		return observation, runtimeaction.ServiceHealthUnknown, ""
	}
}

func runtimeObservedServiceStatus(raw string) runtimeaction.ServiceHealthStatus {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case normalized == "":
		return runtimeaction.ServiceHealthUnknown
	case strings.Contains(normalized, "unhealthy"), strings.Contains(normalized, "failed"), strings.Contains(normalized, "error"), strings.Contains(normalized, "exited"), strings.Contains(normalized, "stopped"), strings.Contains(normalized, "missing"):
		return runtimeaction.ServiceHealthUnhealthy
	case strings.Contains(normalized, "starting"), strings.Contains(normalized, "created"), strings.Contains(normalized, "restarting"), strings.Contains(normalized, "pending"), strings.Contains(normalized, "provision"):
		return runtimeaction.ServiceHealthStarting
	case strings.Contains(normalized, "healthy"), strings.Contains(normalized, "running"), strings.Contains(normalized, "ready"), strings.Contains(normalized, "active"), normalized == "ok":
		return runtimeaction.ServiceHealthHealthy
	default:
		return runtimeaction.ServiceHealthUnknown
	}
}

func runtimeCombineServiceStatus(current runtimeaction.ServiceHealthStatus, currentFailure string, next runtimeaction.ServiceHealthStatus, nextFailure string) (runtimeaction.ServiceHealthStatus, string) {
	if current == runtimeaction.ServiceHealthUnhealthy || next == runtimeaction.ServiceHealthUnhealthy {
		if next == runtimeaction.ServiceHealthUnhealthy && nextFailure != "" {
			return next, nextFailure
		}
		return runtimeaction.ServiceHealthUnhealthy, currentFailure
	}
	if current == runtimeaction.ServiceHealthStarting || next == runtimeaction.ServiceHealthStarting {
		return runtimeaction.ServiceHealthStarting, ""
	}
	if current == runtimeaction.ServiceHealthHealthy || next == runtimeaction.ServiceHealthHealthy {
		return runtimeaction.ServiceHealthHealthy, ""
	}
	if nextFailure != "" {
		return runtimeaction.ServiceHealthUnknown, nextFailure
	}
	return runtimeaction.ServiceHealthUnknown, currentFailure
}

func runtimeServiceStatusWithProbe(current runtimeaction.ServiceHealthStatus, currentFailure string, probe *runtimeaction.HTTPProbeObservation) (runtimeaction.ServiceHealthStatus, string) {
	if probe == nil {
		return current, currentFailure
	}
	if !probe.Reached {
		return runtimeaction.ServiceHealthUnhealthy, runtimeFirstNonEmpty(probe.FailureClass, "health_probe_failed")
	}
	if current == runtimeaction.ServiceHealthUnhealthy || current == runtimeaction.ServiceHealthStarting {
		return current, currentFailure
	}
	return runtimeaction.ServiceHealthHealthy, ""
}

type runtimeHealthProbeTarget struct {
	Name       string
	URL        string
	HealthPath string
}

func runtimeHealthProbeTargets(deployDir string) map[string]runtimeHealthProbeTarget {
	targets := map[string]runtimeHealthProbeTarget{}
	for _, manifestPath := range runtimePlatformManifestPaths(deployDir) {
		bundle, err := platformdeploy.LoadBundleManifest(manifestPath)
		if err != nil {
			continue
		}
		for _, app := range runtimeProbeApps(bundle) {
			name := strings.TrimSpace(app.Name)
			healthPath := strings.TrimSpace(app.HealthPath)
			if name == "" || healthPath == "" {
				continue
			}
			if probeURL, ok := runtimeHealthProbeURL(app, healthPath); ok {
				targets[name] = runtimeHealthProbeTarget{Name: name, URL: probeURL, HealthPath: healthPath}
			}
		}
	}
	return targets
}

func runtimePlatformManifestPaths(deployDir string) []string {
	deployDir = strings.TrimSpace(deployDir)
	if deployDir == "" {
		return nil
	}
	return []string{
		filepath.Join(deployDir, "platform-apps", "manifest.json"),
		filepath.Join(deployDir, ".platform-apps-manifest.json"),
	}
}

func runtimeProbeApps(bundle platformdeploy.BundleManifest) []platformdeploy.AppManifest {
	apps := make([]platformdeploy.AppManifest, 0, len(bundle.SystemApps)+len(bundle.Apps))
	for _, app := range bundle.SystemApps {
		apps = append(apps, app.AppManifest)
	}
	for _, app := range bundle.Apps {
		if platformdeploy.IsStackKitOwnedApp(app) {
			apps = append(apps, app)
		}
	}
	return apps
}

func runtimeHealthProbeURL(app platformdeploy.AppManifest, healthPath string) (string, bool) {
	base := runtimeFirstNonEmpty(app.RouteURL, app.URL)
	if base == "" && strings.TrimSpace(app.Host) != "" {
		base = "http://" + strings.TrimSpace(app.Host)
		if app.Port > 0 {
			base += fmt.Sprintf(":%d", app.Port)
		}
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	if !strings.HasPrefix(healthPath, "/") {
		return "", false
	}
	parsed.Path = path.Clean(healthPath)
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), true
}

func probeRuntimeHealthPath(ctx context.Context, target string) *runtimeaction.HTTPProbeObservation {
	probe := &runtimeaction.HTTPProbeObservation{URL: target}
	if err := validateRuntimeHealthProbeTarget(ctx, target); err != nil {
		probe.FailureClass = runtimeHealthProbeFailureClass(err)
		return probe
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		probe.FailureClass = "health_probe_invalid_url"
		return probe
	}
	client := newRuntimeObservationHTTPClient()
	if client == nil {
		probe.FailureClass = "health_probe_failed"
		return probe
	}
	// Enforce the redirect rule at the call site as well as in the default
	// client, so injected transports cannot turn a measured health probe into
	// a cross-network request.
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errRuntimeHealthProbeRedirect
	}
	response, err := clientCopy.Do(req)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		probe.FailureClass = runtimeHealthProbeFailureClass(err)
		return probe
	}
	defer response.Body.Close()
	probe.StatusCode = response.StatusCode
	probe.Reached = runtimeHealthProbeReached(response.StatusCode)
	if !probe.Reached {
		probe.FailureClass = "health_probe_failed"
	}
	return probe
}

func validateRuntimeHealthProbeTarget(ctx context.Context, target string) error {
	parsed, err := url.Parse(target)
	if err != nil || parsed == nil || parsed.Host == "" {
		return fmt.Errorf("health probe URL is invalid")
	}
	if scheme := strings.ToLower(parsed.Scheme); scheme != "http" && scheme != "https" {
		return errRuntimeHealthProbeDisallowedTarget
	}
	if parsed.User != nil || parsed.Hostname() == "" || runtimeHealthProbeBlockedHostname(parsed.Hostname()) {
		return errRuntimeHealthProbeDisallowedTarget
	}
	_, err = runtimeHealthProbePublicAddress(ctx, parsed.Hostname())
	return err
}

func runtimeHealthProbeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("health probe destination is invalid: %w", err)
	}
	publicAddress, err := runtimeHealthProbePublicAddress(ctx, host)
	if err != nil {
		return nil, err
	}
	return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(publicAddress.String(), port))
}

func runtimeHealthProbePublicAddress(ctx context.Context, host string) (netip.Addr, error) {
	host = strings.TrimSpace(host)
	if runtimeHealthProbeBlockedHostname(host) {
		return netip.Addr{}, errRuntimeHealthProbeDisallowedTarget
	}
	if address, err := netip.ParseAddr(host); err == nil {
		address = address.Unmap()
		if !runtimeHealthProbeIsPublicAddress(address) {
			return netip.Addr{}, errRuntimeHealthProbeDisallowedTarget
		}
		return address, nil
	}
	addresses, err := runtimeHealthProbeLookupIP(ctx, host)
	if err != nil || len(addresses) == 0 {
		if err != nil {
			return netip.Addr{}, fmt.Errorf("resolve health probe destination: %w", err)
		}
		return netip.Addr{}, fmt.Errorf("resolve health probe destination: no addresses")
	}
	var selected netip.Addr
	for _, resolved := range addresses {
		address, parseErr := netip.ParseAddr(resolved.IP.String())
		if parseErr != nil {
			return netip.Addr{}, errRuntimeHealthProbeDisallowedTarget
		}
		address = address.Unmap()
		if !runtimeHealthProbeIsPublicAddress(address) {
			return netip.Addr{}, errRuntimeHealthProbeDisallowedTarget
		}
		if !selected.IsValid() {
			selected = address
		}
	}
	if !selected.IsValid() {
		return netip.Addr{}, errRuntimeHealthProbeDisallowedTarget
	}
	return selected, nil
}

func runtimeHealthProbeBlockedHostname(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return true
	}
	switch host {
	case "localhost", "metadata", "metadata.google", "metadata.google.internal", "instance-data":
		return true
	}
	return strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal")
}

func runtimeHealthProbeIsPublicAddress(address netip.Addr) bool {
	if !address.IsValid() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsPrivate() || address.IsUnspecified() || address.IsMulticast() {
		return false
	}
	for _, blocked := range []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
	} {
		if blocked.Contains(address) {
			return false
		}
	}
	return true
}

func runtimeHealthProbeFailureClass(err error) string {
	if errors.Is(err, errRuntimeHealthProbeDisallowedTarget) {
		return "health_probe_disallowed_target"
	}
	if errors.Is(err, errRuntimeHealthProbeRedirect) {
		return "health_probe_redirect_disallowed"
	}
	return "health_probe_failed"
}

func runtimeHealthProbeReached(statusCode int) bool {
	return (statusCode >= http.StatusOK && statusCode < http.StatusBadRequest) || statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden
}
