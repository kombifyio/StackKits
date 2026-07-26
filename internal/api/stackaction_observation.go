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
	stackaction "github.com/kombifyio/stackkits/internal/stackaction"
	"github.com/kombifyio/stackkits/pkg/models"
)

const runtimeObservationHTTPTimeoutStackAction = 10 * time.Second

var (
	errStackActionRuntimeHealthProbeDisallowedTarget = errors.New("health probe target is not publicly routable")
	errStackActionRuntimeHealthProbeRedirect         = errors.New("health probe redirects are not allowed")
)

// runtimeObservationDockerClient is deliberately the narrow Docker surface
// already used by StackKit verification. A StackAction must not create a
// second status subsystem merely to render dashboard health.
type runtimeObservationDockerClientStackAction interface {
	IsInstalled() bool
	IsRunning(context.Context) bool
	GetStackKitContainers(context.Context) ([]docker.ContainerInfo, error)
	GetContainersByLabel(context.Context, string) ([]docker.ContainerInfo, error)
	GetContainerHealth(context.Context, string) (models.HealthStatus, error)
}

var newRuntimeObservationDockerClientStackAction = func(env []string) runtimeObservationDockerClientStackAction {
	return docker.NewClient(docker.WithEnv(env...))
}

var newRuntimeObservationHTTPClientStackAction = func() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Health probes are a measured public-service signal, never a mechanism to
	// reach operator-local services through a proxy inherited from the node.
	transport.Proxy = nil
	transport.DialContext = runtimeHealthProbeDialContextStackAction
	return &http.Client{Timeout: runtimeObservationHTTPTimeoutStackAction, Transport: transport}
}

var runtimeHealthProbeLookupIPStackAction = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// collectRuntimeLiveObservation converts current Docker, platform-adapter, and
// optional health-path evidence into the versioned StackAction contract.
// It is best-effort: an unreachable host is returned as measured evidence and
// does not turn a completed rollout/verify operation into a synthetic success
// or erase its original result.
func collectRuntimeLiveObservationStackAction(
	ctx context.Context,
	resp stackActionResponse,
	remote *preparedRuntimeTargetStackAction, refs []platformdeploy.DeploymentRef,
) *stackaction.LiveObservation {
	observedAt := time.Now().UTC()
	observation := &stackaction.LiveObservation{
		Version:    stackaction.ObservationVersionV1,
		ObservedAt: observedAt,
		Host: stackaction.HostObservation{
			Host: runtimeObservationHostStackAction(remote),
		},
		Platform: runtimeObservationPlatformStackAction(resp.TofuDir, refs),
	}

	env := []string(nil)
	if remote != nil {
		env = append(env, remote.env...)
	}
	client := newRuntimeObservationDockerClientStackAction(env)
	if !client.IsInstalled() {
		return runtimeObservationHostFailureStackAction(observation, "docker_not_installed")
	}
	if !client.IsRunning(ctx) {
		return runtimeObservationHostFailureStackAction(observation, "docker_unreachable")
	}
	observation.Host.Reachable = true
	observation.Host.DockerReachable = true

	healthTargets := runtimeHealthProbeTargetsStackAction(resp.TofuDir)
	seenContainers := map[string]bool{}
	seenServices := map[string]bool{}

	for _, ref := range refs {
		service := runtimePlatformServiceObservationStackAction(ctx, client, ref)
		if target, ok := healthTargets[service.Name]; ok {
			service.HealthPath = target.HealthPath
			service.Probe = probeRuntimeHealthPathStackAction(ctx, target.URL)
			service.Status, service.FailureClass = runtimeServiceStatusWithProbeStackAction(service.Status, service.FailureClass, service.Probe)
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
			service := runtimeContainerServiceObservationStackAction(ctx, client, container)
			if target, ok := healthTargets[service.Name]; ok {
				service.HealthPath = target.HealthPath
				service.Probe = probeRuntimeHealthPathStackAction(ctx, target.URL)
				service.Status, service.FailureClass = runtimeServiceStatusWithProbeStackAction(service.Status, service.FailureClass, service.Probe)
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
		probe := probeRuntimeHealthPathStackAction(ctx, target.URL)
		status, failureClass := runtimeServiceStatusWithProbeStackAction(stackaction.ServiceHealthUnknown, "", probe)
		observation.Services = append(observation.Services, stackaction.ServiceObservation{
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

func runtimeObservationHostStackAction(remote *preparedRuntimeTargetStackAction) string {
	if remote != nil && remote.target != nil && strings.TrimSpace(remote.target.Host) != "" {
		return strings.TrimSpace(remote.target.Host)
	}
	return "local"
}

// prepareRuntimeObservationTarget opens the same Docker-over-SSH transport as
// a rollout without running bootstrap or writing tfvars. Verify must remain a
// read-only observation path.
func prepareRuntimeObservationTargetStackAction(ctx context.Context, target *stackActionTarget, resolver StackActionReferenceResolver) (*preparedRuntimeTargetStackAction, func(), error) {
	target = normalizeStackActionTarget(target)
	if target == nil {
		return nil, nil, nil
	}
	dockerHost, err := runtimeTargetDockerHostStackAction(target)
	if err != nil {
		return nil, nil, err
	}
	keyPath, homeDir, cleanup, err := materializeRuntimeTargetSSHKeyStackAction(ctx, target, resolver)
	if err != nil {
		return nil, cleanup, err
	}
	env := []string{"DOCKER_HOST=" + dockerHost}
	if homeDir != "" {
		env = append(env, "HOME="+homeDir)
	}
	if keyPath != "" {
		env = append(env, "DOCKER_SSH_COMMAND="+runtimeTargetSSHCommandStackAction(target, keyPath))
	}
	return &preparedRuntimeTargetStackAction{
		dockerHost: dockerHost,
		env:        env,
		target:     target,
		keyPath:    keyPath,
	}, cleanup, nil
}

func runtimeObservationTargetFailureStackAction(target *stackActionTarget, failureClass string) *stackaction.LiveObservation {
	target = normalizeStackActionTarget(target)
	host := "local"
	if target != nil {
		host = target.Host
	}
	return &stackaction.LiveObservation{
		Version:    stackaction.ObservationVersionV1,
		ObservedAt: time.Now().UTC(),
		Host: stackaction.HostObservation{
			Host:         host,
			FailureClass: failureClass,
		},
		FailureClass: failureClass,
	}
}

func runtimeObservationHostFailureStackAction(observation *stackaction.LiveObservation, failureClass string) *stackaction.LiveObservation {
	observation.Host.FailureClass = failureClass
	observation.FailureClass = failureClass
	return observation
}

func runtimeObservationPlatformStackAction(deployDir string, refs []platformdeploy.DeploymentRef) *stackaction.PlatformObservation {
	cfg := runtimeLoadPlatformConfigFileStackAction(deployDir)
	platform := runtimeFirstNonEmptyStackAction(cfg.Platform)
	if platform == "" && len(refs) > 0 {
		platform = strings.TrimSpace(refs[0].Platform)
	}
	observation := &stackaction.PlatformObservation{
		Name:            platform,
		Endpoint:        cfg.endpointStackAction(),
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

func runtimePlatformServiceObservationStackAction(ctx context.Context, client runtimeObservationDockerClientStackAction, ref platformdeploy.DeploymentRef) stackaction.ServiceObservation {
	service := stackaction.ServiceObservation{
		Name:           runtimeFirstNonEmptyStackAction(ref.AppName, ref.ExternalID),
		Status:         runtimeObservedServiceStatusStackAction(ref.ObservedStatus),
		PlatformAppID:  strings.TrimSpace(ref.ExternalID),
		PlatformStatus: strings.TrimSpace(ref.ObservedStatus),
	}
	if service.Name == "" {
		service.Name = "platform-app"
	}
	if service.Status == stackaction.ServiceHealthUnhealthy {
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
		observation, status, failureClass := runtimeContainerObservationStackAction(ctx, client, container)
		service.Containers = append(service.Containers, observation)
		service.Status, service.FailureClass = runtimeCombineServiceStatusStackAction(service.Status, service.FailureClass, status, failureClass)
	}
	return service
}

func runtimeContainerServiceObservationStackAction(ctx context.Context, client runtimeObservationDockerClientStackAction, container docker.ContainerInfo) stackaction.ServiceObservation {
	containerObservation, status, failureClass := runtimeContainerObservationStackAction(ctx, client, container)
	name := strings.TrimSpace(container.Name)
	if name == "" {
		name = strings.TrimSpace(container.ID)
	}
	if name == "" {
		name = "container"
	}
	return stackaction.ServiceObservation{
		Name:         name,
		Status:       status,
		Containers:   []stackaction.ContainerObservation{containerObservation},
		FailureClass: failureClass,
	}
}

func runtimeContainerObservationStackAction(ctx context.Context, client runtimeObservationDockerClientStackAction, container docker.ContainerInfo) (stackaction.ContainerObservation, stackaction.ServiceHealthStatus, string) {
	observation := stackaction.ContainerObservation{
		ID:      strings.TrimSpace(container.ID),
		Name:    strings.TrimSpace(container.Name),
		State:   strings.TrimSpace(container.State.Status),
		Running: container.State.Running,
	}
	if !container.State.Running {
		return observation, stackaction.ServiceHealthUnhealthy, "container_not_running"
	}

	target := observation.ID
	if target == "" {
		target = observation.Name
	}
	health, err := client.GetContainerHealth(ctx, target)
	if err != nil {
		return observation, stackaction.ServiceHealthUnknown, "docker_inspect_failed"
	}
	observation.Health = string(health)
	switch health {
	case models.HealthStatusHealthy:
		return observation, stackaction.ServiceHealthHealthy, ""
	case models.HealthStatusStarting:
		return observation, stackaction.ServiceHealthStarting, ""
	case models.HealthStatusUnhealthy:
		return observation, stackaction.ServiceHealthUnhealthy, "docker_health_unhealthy"
	default:
		return observation, stackaction.ServiceHealthUnknown, ""
	}
}

func runtimeObservedServiceStatusStackAction(raw string) stackaction.ServiceHealthStatus {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case normalized == "":
		return stackaction.ServiceHealthUnknown
	case strings.Contains(normalized, "unhealthy"), strings.Contains(normalized, "failed"), strings.Contains(normalized, "error"), strings.Contains(normalized, "exited"), strings.Contains(normalized, "stopped"), strings.Contains(normalized, "missing"):
		return stackaction.ServiceHealthUnhealthy
	case strings.Contains(normalized, "starting"), strings.Contains(normalized, "created"), strings.Contains(normalized, "restarting"), strings.Contains(normalized, "pending"), strings.Contains(normalized, "provision"):
		return stackaction.ServiceHealthStarting
	case strings.Contains(normalized, "healthy"), strings.Contains(normalized, "running"), strings.Contains(normalized, "ready"), strings.Contains(normalized, "active"), normalized == "ok":
		return stackaction.ServiceHealthHealthy
	default:
		return stackaction.ServiceHealthUnknown
	}
}

func runtimeCombineServiceStatusStackAction(current stackaction.ServiceHealthStatus, currentFailure string, next stackaction.ServiceHealthStatus, nextFailure string) (stackaction.ServiceHealthStatus, string) {
	if current == stackaction.ServiceHealthUnhealthy || next == stackaction.ServiceHealthUnhealthy {
		if next == stackaction.ServiceHealthUnhealthy && nextFailure != "" {
			return next, nextFailure
		}
		return stackaction.ServiceHealthUnhealthy, currentFailure
	}
	if current == stackaction.ServiceHealthStarting || next == stackaction.ServiceHealthStarting {
		return stackaction.ServiceHealthStarting, ""
	}
	if current == stackaction.ServiceHealthHealthy || next == stackaction.ServiceHealthHealthy {
		return stackaction.ServiceHealthHealthy, ""
	}
	if nextFailure != "" {
		return stackaction.ServiceHealthUnknown, nextFailure
	}
	return stackaction.ServiceHealthUnknown, currentFailure
}

func runtimeServiceStatusWithProbeStackAction(current stackaction.ServiceHealthStatus, currentFailure string, probe *stackaction.HTTPProbeObservation) (stackaction.ServiceHealthStatus, string) {
	if probe == nil {
		return current, currentFailure
	}
	if !probe.Reached {
		return stackaction.ServiceHealthUnhealthy, runtimeFirstNonEmptyStackAction(probe.FailureClass, "health_probe_failed")
	}
	if current == stackaction.ServiceHealthUnhealthy || current == stackaction.ServiceHealthStarting {
		return current, currentFailure
	}
	return stackaction.ServiceHealthHealthy, ""
}

type runtimeHealthProbeTargetStackAction struct {
	Name       string
	URL        string
	HealthPath string
}

func runtimeHealthProbeTargetsStackAction(deployDir string) map[string]runtimeHealthProbeTargetStackAction {
	targets := map[string]runtimeHealthProbeTargetStackAction{}
	for _, manifestPath := range runtimePlatformManifestPathsStackAction(deployDir) {
		bundle, err := platformdeploy.LoadBundleManifest(manifestPath)
		if err != nil {
			continue
		}
		for _, app := range runtimeProbeAppsStackAction(bundle) {
			name := strings.TrimSpace(app.Name)
			healthPath := strings.TrimSpace(app.HealthPath)
			if name == "" || healthPath == "" {
				continue
			}
			if probeURL, ok := runtimeHealthProbeURLStackAction(app, healthPath); ok {
				targets[name] = runtimeHealthProbeTargetStackAction{Name: name, URL: probeURL, HealthPath: healthPath}
			}
		}
	}
	return targets
}

func runtimePlatformManifestPathsStackAction(deployDir string) []string {
	deployDir = strings.TrimSpace(deployDir)
	if deployDir == "" {
		return nil
	}
	return []string{
		filepath.Join(deployDir, "platform-apps", "manifest.json"),
		filepath.Join(deployDir, ".platform-apps-manifest.json"),
	}
}

func runtimeProbeAppsStackAction(bundle platformdeploy.BundleManifest) []platformdeploy.AppManifest {
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

func runtimeHealthProbeURLStackAction(app platformdeploy.AppManifest, healthPath string) (string, bool) {
	base := runtimeFirstNonEmptyStackAction(app.RouteURL, app.URL)
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

func probeRuntimeHealthPathStackAction(ctx context.Context, target string) *stackaction.HTTPProbeObservation {
	probe := &stackaction.HTTPProbeObservation{URL: target}
	if err := validateRuntimeHealthProbeTargetStackAction(ctx, target); err != nil {
		probe.FailureClass = runtimeHealthProbeFailureClassStackAction(err)
		return probe
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		probe.FailureClass = "health_probe_invalid_url"
		return probe
	}
	client := newRuntimeObservationHTTPClientStackAction()
	if client == nil {
		probe.FailureClass = "health_probe_failed"
		return probe
	}
	// Enforce the redirect rule at the call site as well as in the default
	// client, so injected transports cannot turn a measured health probe into
	// a cross-network request.
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errStackActionRuntimeHealthProbeRedirect
	}
	response, err := clientCopy.Do(req)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		probe.FailureClass = runtimeHealthProbeFailureClassStackAction(err)
		return probe
	}
	defer response.Body.Close()
	probe.StatusCode = response.StatusCode
	probe.Reached = runtimeHealthProbeReachedStackAction(response.StatusCode)
	if !probe.Reached {
		probe.FailureClass = "health_probe_failed"
	}
	return probe
}

func validateRuntimeHealthProbeTargetStackAction(ctx context.Context, target string) error {
	parsed, err := url.Parse(target)
	if err != nil || parsed == nil || parsed.Host == "" {
		return fmt.Errorf("health probe URL is invalid")
	}
	if scheme := strings.ToLower(parsed.Scheme); scheme != "http" && scheme != "https" {
		return errStackActionRuntimeHealthProbeDisallowedTarget
	}
	if parsed.User != nil || parsed.Hostname() == "" || runtimeHealthProbeBlockedHostnameStackAction(parsed.Hostname()) {
		return errStackActionRuntimeHealthProbeDisallowedTarget
	}
	_, err = runtimeHealthProbePublicAddressStackAction(ctx, parsed.Hostname())
	return err
}

func runtimeHealthProbeDialContextStackAction(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("health probe destination is invalid: %w", err)
	}
	publicAddress, err := runtimeHealthProbePublicAddressStackAction(ctx, host)
	if err != nil {
		return nil, err
	}
	return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(publicAddress.String(), port))
}

func runtimeHealthProbePublicAddressStackAction(ctx context.Context, host string) (netip.Addr, error) {
	host = strings.TrimSpace(host)
	if runtimeHealthProbeBlockedHostnameStackAction(host) {
		return netip.Addr{}, errStackActionRuntimeHealthProbeDisallowedTarget
	}
	if address, err := netip.ParseAddr(host); err == nil {
		address = address.Unmap()
		if !runtimeHealthProbeIsPublicAddressStackAction(address) {
			return netip.Addr{}, errStackActionRuntimeHealthProbeDisallowedTarget
		}
		return address, nil
	}
	addresses, err := runtimeHealthProbeLookupIPStackAction(ctx, host)
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
			return netip.Addr{}, errStackActionRuntimeHealthProbeDisallowedTarget
		}
		address = address.Unmap()
		if !runtimeHealthProbeIsPublicAddressStackAction(address) {
			return netip.Addr{}, errStackActionRuntimeHealthProbeDisallowedTarget
		}
		if !selected.IsValid() {
			selected = address
		}
	}
	if !selected.IsValid() {
		return netip.Addr{}, errStackActionRuntimeHealthProbeDisallowedTarget
	}
	return selected, nil
}

func runtimeHealthProbeBlockedHostnameStackAction(host string) bool {
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

func runtimeHealthProbeIsPublicAddressStackAction(address netip.Addr) bool {
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

func runtimeHealthProbeFailureClassStackAction(err error) string {
	if errors.Is(err, errStackActionRuntimeHealthProbeDisallowedTarget) {
		return "health_probe_disallowed_target"
	}
	if errors.Is(err, errStackActionRuntimeHealthProbeRedirect) {
		return "health_probe_redirect_disallowed"
	}
	return "health_probe_failed"
}

func runtimeHealthProbeReachedStackAction(statusCode int) bool {
	return (statusCode >= http.StatusOK && statusCode < http.StatusBadRequest) || statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden
}
