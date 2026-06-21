package platformdeploy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// CoolifyAdapter deploys generated compose bundles through Coolify's API.
// Coolify authenticates with bearer tokens.
type CoolifyAdapter struct {
	client apiClient
	cfg    HTTPConfig
}

type coolifyObserveLoopState struct {
	lastStatuses             map[int]coolifyObservedStatus
	lastErrors               map[int]error
	lastStartRequests        map[int]time.Time
	lastRuntimeStartRequests map[int]time.Time
	runtimeRunningSince      map[int]time.Time
}

var (
	coolifyStatusPollInterval     = 2 * time.Second
	coolifyStatusTimeout          = 10 * time.Minute
	coolifyServiceReadyPoll       = 500 * time.Millisecond
	coolifyServiceReadyTimeout    = 30 * time.Second
	coolifyStartReconcileInterval = 15 * time.Second
	coolifyDockerRuntimeTimeout   = 5 * time.Second
	coolifyDockerRuntimeStableFor = 30 * time.Second
	coolifyDockerRuntimeStatus    = inspectCoolifyDockerRuntimeStatus
	coolifyServiceRuntimeStart    = startCoolifyServiceRuntime
)

func NewCoolifyAdapter(cfg HTTPConfig) *CoolifyAdapter {
	return &CoolifyAdapter{
		client: apiClient{cfg: cfg, authMode: authBearer},
		cfg:    cfg,
	}
}

func (a *CoolifyAdapter) BootstrapProviderName() string {
	return "coolify"
}

func (a *CoolifyAdapter) BootstrapCapabilities() []BootstrapCapability {
	return []BootstrapCapability{
		BootstrapCapabilityProxyRouting,
		BootstrapCapabilityAPIAccess,
		BootstrapCapabilityTeamManagement,
		BootstrapCapabilityBackups,
		BootstrapCapabilitySecrets,
		BootstrapCapabilityHealthchecks,
		BootstrapCapabilityServiceHandoff,
	}
}

func (a *CoolifyAdapter) ApplyCompose(ctx context.Context, manifest AppManifest) (DeploymentRef, error) {
	ref, err := a.UpsertCompose(ctx, manifest)
	if err != nil {
		return DeploymentRef{}, err
	}
	ref.ServiceNames = composeLongRunningServiceNames(manifest.ComposeYAML)
	if !a.cfg.LegacyDockerComposeAPI {
		if err := a.waitForServiceResources(ctx, ref, manifest); err != nil {
			return DeploymentRef{}, err
		}
	}
	deploymentID, err := a.Deploy(ctx, ref)
	if err != nil {
		return DeploymentRef{}, err
	}
	ref.DeploymentID = deploymentID
	ref.LastDeployed = time.Now().UTC()
	return ref, nil
}

func (a *CoolifyAdapter) ObserveDeployment(ctx context.Context, ref DeploymentRef) (DeploymentRef, error) {
	refs, err := a.ObserveDeployments(ctx, []DeploymentRef{ref})
	if err != nil {
		return ref, err
	}
	if len(refs) == 0 {
		return ref, fmt.Errorf("coolify observe %q returned no deployment refs", ref.AppName)
	}
	return refs[0], nil
}

func (a *CoolifyAdapter) ObserveDeployments(ctx context.Context, refs []DeploymentRef) ([]DeploymentRef, error) {
	if len(refs) == 0 {
		return refs, nil
	}
	observed := append([]DeploymentRef(nil), refs...)
	state := coolifyObserveLoopState{
		lastStatuses:             make(map[int]coolifyObservedStatus, len(refs)),
		lastErrors:               make(map[int]error, len(refs)),
		lastStartRequests:        make(map[int]time.Time, len(refs)),
		lastRuntimeStartRequests: make(map[int]time.Time, len(refs)),
		runtimeRunningSince:      make(map[int]time.Time, len(refs)),
	}
	for _, ref := range refs {
		if ref.ExternalID == "" {
			return observed, fmt.Errorf("coolify observe %q requires external id", ref.AppName)
		}
	}

	timeout := coolifyStatusTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	pollInterval := coolifyStatusPollInterval
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	startReconcileInterval := coolifyStartReconcileInterval
	if startReconcileInterval <= 0 {
		startReconcileInterval = 15 * time.Second
	}
	deadline := time.Now().Add(timeout)

	for {
		allRunning := true
		for i, ref := range observed {
			if !a.observeCoolifyDeploymentTick(ctx, observed, i, &state, startReconcileInterval, ref) {
				allRunning = false
				continue
			}
		}
		if allRunning {
			return observed, nil
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return observed, ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	return observed, coolifyStartEvidenceError(observed, state.lastStatuses, state.lastErrors, timeout)
}

func (a *CoolifyAdapter) observeCoolifyDeploymentTick(ctx context.Context, observed []DeploymentRef, index int, state *coolifyObserveLoopState, startReconcileInterval time.Duration, ref DeploymentRef) bool {
	status, err := a.serviceStatus(ctx, ref)
	if err != nil {
		state.lastErrors[index] = err
		if a.observeCoolifyDockerRuntimeFallbackTick(ctx, observed, index, state, startReconcileInterval, ref, err) {
			return true
		}
		delete(state.runtimeRunningSince, index)
		return false
	}
	state.lastStatuses[index] = status
	observed[index].ObservedStatus = status.Status
	observed[index].ObservedAt = status.ObservedAt
	a.trackCoolifyRuntimeStability(index, status, state)
	if status.running() {
		return a.coolifyObservedStatusReady(ctx, &observed[index], status, state, index, startReconcileInterval)
	}
	a.reconcileCoolifyDeploymentStart(ctx, &observed[index], status, state, index, startReconcileInterval)
	return false
}

func (a *CoolifyAdapter) observeCoolifyDockerRuntimeFallbackTick(ctx context.Context, observed []DeploymentRef, index int, state *coolifyObserveLoopState, startReconcileInterval time.Duration, ref DeploymentRef, apiErr error) bool {
	status, runtimeErr, ok := a.coolifyDockerRuntimeObservedStatus(ctx, ref)
	if runtimeErr != nil {
		state.lastErrors[index] = fmt.Errorf("%w; docker runtime observe: %v", apiErr, runtimeErr)
	}
	if !ok {
		return false
	}
	state.lastStatuses[index] = status
	observed[index].ObservedStatus = status.Status
	observed[index].ObservedAt = status.ObservedAt
	a.trackCoolifyRuntimeStability(index, status, state)
	if status.running() {
		return a.coolifyObservedStatusReady(ctx, &observed[index], status, state, index, startReconcileInterval)
	}
	a.reconcileCoolifyDockerRuntimeStart(ctx, &observed[index], status, state, index, startReconcileInterval)
	return false
}

func (a *CoolifyAdapter) coolifyObservedStatusReady(ctx context.Context, ref *DeploymentRef, status coolifyObservedStatus, state *coolifyObserveLoopState, index int, startReconcileInterval time.Duration) bool {
	if !status.RuntimeKnown {
		return true
	}
	if !status.RuntimeRunning {
		a.reconcileCoolifyDeploymentStart(ctx, ref, status, state, index, startReconcileInterval)
		return false
	}
	return dockerRuntimeStatusStable(state.runtimeRunningSince[index])
}

func (a *CoolifyAdapter) trackCoolifyRuntimeStability(index int, status coolifyObservedStatus, state *coolifyObserveLoopState) {
	if status.RuntimeRunning {
		if _, ok := state.runtimeRunningSince[index]; !ok {
			state.runtimeRunningSince[index] = time.Now()
		}
		return
	}
	delete(state.runtimeRunningSince, index)
}

func (a *CoolifyAdapter) reconcileCoolifyDeploymentStart(ctx context.Context, ref *DeploymentRef, status coolifyObservedStatus, state *coolifyObserveLoopState, index int, startReconcileInterval time.Duration) {
	if status.shouldStartDockerRuntime() && shouldReconcileCoolifyStart(state.lastRuntimeStartRequests[index], startReconcileInterval) {
		a.reconcileCoolifyDockerRuntimeStart(ctx, ref, status, state, index, startReconcileInterval)
		return
	}
	if status.shouldReconcileStart() && shouldReconcileCoolifyStart(state.lastStartRequests[index], startReconcileInterval) {
		deploymentID, startErr := a.Deploy(ctx, *ref)
		state.lastStartRequests[index] = time.Now()
		if startErr != nil {
			state.lastErrors[index] = fmt.Errorf("reconcile coolify start %q: %w", ref.AppName, startErr)
		} else if deploymentID != "" {
			ref.DeploymentID = deploymentID
		}
	}
}

func (a *CoolifyAdapter) reconcileCoolifyDockerRuntimeStart(ctx context.Context, ref *DeploymentRef, status coolifyObservedStatus, state *coolifyObserveLoopState, index int, startReconcileInterval time.Duration) {
	if !status.RuntimeKnown || status.RuntimeRunning {
		return
	}
	if !shouldReconcileCoolifyStart(state.lastRuntimeStartRequests[index], startReconcileInterval) {
		return
	}
	state.lastRuntimeStartRequests[index] = time.Now()
	if startErr := coolifyServiceRuntimeStart(ctx, *ref, a.cfg.DockerEnv); startErr != nil {
		state.lastErrors[index] = fmt.Errorf("start coolify docker runtime %q: %w", ref.AppName, startErr)
	}
}

func dockerRuntimeStatusStable(since time.Time) bool {
	stableFor := coolifyDockerRuntimeStableFor
	if stableFor <= 0 {
		return true
	}
	return !since.IsZero() && time.Since(since) >= stableFor
}

func shouldReconcileCoolifyStart(last time.Time, interval time.Duration) bool {
	return last.IsZero() || time.Since(last) >= interval
}

func coolifyStartEvidenceError(refs []DeploymentRef, statuses map[int]coolifyObservedStatus, errs map[int]error, timeout time.Duration) error {
	for i, ref := range refs {
		if status, ok := statuses[i]; ok && status.running() {
			continue
		}
		statusText := statuses[i].Status
		if statusText == "" {
			statusText = "unknown"
		}
		if err := errs[i]; err != nil {
			return fmt.Errorf("coolify service %q (externalId=%s) did not reach running status after %s; last status=%q; last error: %w", ref.AppName, ref.ExternalID, timeout, statusText, err)
		}
		return fmt.Errorf("coolify service %q (externalId=%s) did not reach running status after %s; last status=%q", ref.AppName, ref.ExternalID, timeout, statusText)
	}
	return fmt.Errorf("coolify services did not reach running status after %s", timeout)
}

func (a *CoolifyAdapter) UpsertCompose(ctx context.Context, manifest AppManifest) (DeploymentRef, error) {
	if a.cfg.LegacyDockerComposeAPI {
		return a.upsertLegacyDockerCompose(ctx, manifest)
	}

	payload := coolifyServicePayload(manifest, a.cfg)

	var created map[string]any
	status, body, err := a.client.postJSON(ctx, "/api/v1/services", payload, &created)
	if err != nil {
		if status != http.StatusConflict {
			return DeploymentRef{}, fmt.Errorf("coolify service create %q: %w", manifest.Name, err)
		}
		return a.updateConflictingService(ctx, manifest, payload, body, "service")
	}
	uuid := firstString(created, "uuid", "id")
	if uuid == "" {
		uuid = manifest.Name
	}
	return DeploymentRef{Platform: manifest.ManagedBy, AppName: manifest.Name, ExternalID: uuid}, nil
}

func (a *CoolifyAdapter) upsertLegacyDockerCompose(ctx context.Context, manifest AppManifest) (DeploymentRef, error) {
	payload := map[string]any{
		"name":               manifest.Name,
		"description":        "Managed by StackKit",
		"docker_compose_raw": normalizeComposeYAML(manifest.ComposeYAML),
		"instant_deploy":     false,
	}
	if a.cfg.ProjectUUID != "" {
		payload["project_uuid"] = a.cfg.ProjectUUID
	}
	if a.cfg.ServerID != "" {
		payload["server_uuid"] = a.cfg.ServerID
	}
	if a.cfg.EnvironmentUUID != "" {
		payload["environment_uuid"] = a.cfg.EnvironmentUUID
	}
	if a.cfg.EnvironmentID != "" {
		payload["environment_name"] = a.cfg.EnvironmentID
	}
	if a.cfg.DestinationUUID != "" {
		payload["destination_uuid"] = a.cfg.DestinationUUID
	}

	var created map[string]any
	status, body, err := a.client.postJSON(ctx, "/api/v1/applications/dockercompose", payload, &created)
	if err != nil {
		if status != http.StatusConflict {
			return DeploymentRef{}, fmt.Errorf("coolify compose app create %q: %w", manifest.Name, err)
		}
		return a.updateConflictingService(ctx, manifest, payload, body, "compose app")
	}
	uuid := firstString(created, "uuid", "id")
	if uuid == "" {
		uuid = manifest.Name
	}
	return DeploymentRef{Platform: manifest.ManagedBy, AppName: manifest.Name, ExternalID: uuid}, nil
}

func (a *CoolifyAdapter) updateConflictingService(ctx context.Context, manifest AppManifest, payload map[string]any, body []byte, label string) (DeploymentRef, error) {
	uuid := idFromBody(body)
	if uuid == "" {
		return DeploymentRef{}, fmt.Errorf("coolify %s create conflict %q did not include uuid", label, manifest.Name)
	}
	var updated map[string]any
	if _, _, updateErr := a.client.doJSON(ctx, http.MethodPatch, "/api/v1/services/"+url.PathEscape(uuid), payload, &updated); updateErr != nil {
		return DeploymentRef{}, fmt.Errorf("coolify %s update %q: %w", label, manifest.Name, updateErr)
	}
	if id := firstString(updated, "uuid", "id"); id != "" {
		uuid = id
	}
	return DeploymentRef{Platform: manifest.ManagedBy, AppName: manifest.Name, ExternalID: uuid}, nil
}

func (a *CoolifyAdapter) Deploy(ctx context.Context, ref DeploymentRef) (string, error) {
	if ref.ExternalID == "" {
		return "", fmt.Errorf("coolify deploy requires external id")
	}
	var deployed map[string]any
	if a.cfg.LegacyDockerComposeAPI {
		path := "/api/v1/deploy?uuid=" + url.QueryEscape(ref.ExternalID)
		if _, _, err := a.client.getJSON(ctx, path, &deployed); err != nil {
			return "", fmt.Errorf("coolify deploy %q: %w", ref.AppName, err)
		}
		return firstDeploymentID(deployed), nil
	}
	path := "/api/v1/services/" + url.PathEscape(ref.ExternalID) + "/start"
	if _, _, err := a.client.doJSON(ctx, http.MethodPost, path, nil, &deployed); err != nil {
		return "", fmt.Errorf("coolify deploy %q: %w", ref.AppName, err)
	}
	return firstDeploymentID(deployed), nil
}

func (a *CoolifyAdapter) waitForServiceResources(ctx context.Context, ref DeploymentRef, manifest AppManifest) error {
	expected := composeServiceNames(manifest.ComposeYAML)
	if len(expected) == 0 {
		expected = []string{coolifyServiceResourceName(manifest.Name, normalizeComposeYAML(manifest.ComposeYAML))}
	}
	timeout := coolifyServiceReadyTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	poll := coolifyServiceReadyPoll
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	var lastMissing []string
	for {
		status, err := a.serviceResourceStatus(ctx, ref)
		if err == nil {
			lastMissing = missingServiceResources(expected, status.Resources)
			if len(lastMissing) == 0 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("coolify service %q resources not ready before start after %s: %w", ref.AppName, timeout, err)
			}
			return fmt.Errorf("coolify service %q resources not ready before start after %s; missing: %s", ref.AppName, timeout, strings.Join(lastMissing, ", "))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

func (a *CoolifyAdapter) Status(ctx context.Context, ref DeploymentRef) error {
	if ref.ExternalID == "" {
		return fmt.Errorf("coolify status requires external id")
	}
	_, err := a.serviceStatus(ctx, ref)
	return err
}

type coolifyObservedStatus struct {
	Status            string
	ServerStatus      bool
	ServerStatusKnown bool
	RuntimeKnown      bool
	RuntimeRunning    bool
	RuntimeActive     bool
	RuntimeStatus     string
	ObservedAt        time.Time
}

func (status coolifyObservedStatus) running() bool {
	normalized := strings.ToLower(strings.TrimSpace(status.Status))
	if normalized == "docker:running" {
		return true
	}
	if !strings.HasPrefix(normalized, "running") {
		return false
	}
	return !status.ServerStatusKnown || status.ServerStatus
}

func (status coolifyObservedStatus) shouldReconcileStart() bool {
	if !status.RuntimeKnown {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(status.RuntimeStatus), "docker:missing containers")
}

func (status coolifyObservedStatus) shouldStartDockerRuntime() bool {
	return status.RuntimeKnown && !status.RuntimeRunning && !status.RuntimeActive && !status.shouldReconcileStart()
}

func (a *CoolifyAdapter) coolifyDockerRuntimeObservedStatus(ctx context.Context, ref DeploymentRef) (coolifyObservedStatus, error, bool) {
	if a.cfg.LegacyDockerComposeAPI || a.cfg.DisableDockerRuntimeObserve {
		return coolifyObservedStatus{}, nil, false
	}
	runtimeStatus, err := coolifyDockerRuntimeStatus(ctx, ref, a.cfg.DockerEnv)
	if err != nil {
		return coolifyObservedStatus{}, err, false
	}
	if !runtimeStatus.Known {
		return coolifyObservedStatus{}, nil, false
	}
	status := strings.TrimSpace(runtimeStatus.Status)
	if status == "" {
		status = "docker:unknown"
	}
	return coolifyObservedStatus{
		Status:         status,
		RuntimeKnown:   true,
		RuntimeRunning: runtimeStatus.Running,
		RuntimeActive:  runtimeStatus.Active,
		RuntimeStatus:  status,
		ObservedAt:     time.Now().UTC(),
	}, nil, true
}

func (a *CoolifyAdapter) serviceStatus(ctx context.Context, ref DeploymentRef) (coolifyObservedStatus, error) {
	path := "/api/v1/services/" + url.PathEscape(ref.ExternalID)
	if a.cfg.LegacyDockerComposeAPI {
		path = "/api/v1/applications/" + url.PathEscape(ref.ExternalID)
	}
	var payload map[string]any
	if _, _, err := a.client.getJSON(ctx, path, &payload); err != nil {
		return coolifyObservedStatus{}, fmt.Errorf("coolify status %q: %w", ref.AppName, err)
	}
	status := firstString(payload, "status", "application_status", "human_status")
	serverStatus, serverStatusKnown := payload["server_status"].(bool)
	if status == "" && serverStatusKnown {
		status = fmt.Sprintf("server:%t", serverStatus)
	}
	observed := coolifyObservedStatus{
		Status:            status,
		ServerStatus:      serverStatus,
		ServerStatusKnown: serverStatusKnown,
		ObservedAt:        time.Now().UTC(),
	}
	if !a.cfg.LegacyDockerComposeAPI && !a.cfg.DisableDockerRuntimeObserve {
		if runtimeStatus, runtimeErr := coolifyDockerRuntimeStatus(ctx, ref, a.cfg.DockerEnv); runtimeErr == nil && runtimeStatus.Known {
			observed.RuntimeKnown = true
			observed.RuntimeRunning = runtimeStatus.Running
			observed.RuntimeActive = runtimeStatus.Active
			observed.RuntimeStatus = runtimeStatus.Status
			if runtimeStatus.Status != "" && (!observed.running() || !runtimeStatus.Running) {
				observed.Status = runtimeStatus.Status
			}
			if runtimeStatus.Running {
				observed.ServerStatus = false
				observed.ServerStatusKnown = false
			}
		}
	}
	return observed, nil
}

type coolifyDockerRuntimeObservation struct {
	Known   bool
	Running bool
	Active  bool
	Status  string
}

type dockerInspectContainer struct {
	Name   string `json:"Name"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Status  string `json:"Status"`
		Running bool   `json:"Running"`
		Health  *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
}

func inspectCoolifyDockerRuntimeStatus(ctx context.Context, ref DeploymentRef, dockerEnv []string) (coolifyDockerRuntimeObservation, error) {
	serviceNames := ref.ServiceNames
	if len(serviceNames) == 0 && strings.TrimSpace(ref.AppName) != "" {
		serviceNames = []string{strings.TrimSpace(ref.AppName)}
	}
	if strings.TrimSpace(ref.ExternalID) == "" || len(serviceNames) == 0 {
		return coolifyDockerRuntimeObservation{}, nil
	}

	timeout := coolifyDockerRuntimeTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	dockerCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	idsCmd := exec.CommandContext(dockerCtx, "docker", "ps", "-aq", "--filter", "label=com.docker.compose.project="+ref.ExternalID) // #nosec G204
	idsCmd.Env = dockerCommandEnv(dockerEnv)
	idsOutput, err := idsCmd.Output()
	if err != nil {
		return coolifyDockerRuntimeObservation{}, err
	}
	ids := strings.Fields(string(idsOutput))
	if len(ids) == 0 {
		return coolifyDockerRuntimeObservation{Known: true, Status: "docker:missing containers"}, nil
	}

	args := append([]string{"inspect"}, ids...)
	inspectCmd := exec.CommandContext(dockerCtx, "docker", args...) // #nosec G204
	inspectCmd.Env = dockerCommandEnv(dockerEnv)
	inspectOutput, err := inspectCmd.Output()
	if err != nil {
		return coolifyDockerRuntimeObservation{}, err
	}
	var containers []dockerInspectContainer
	if err := json.NewDecoder(bytes.NewReader(inspectOutput)).Decode(&containers); err != nil {
		return coolifyDockerRuntimeObservation{}, fmt.Errorf("decode docker inspect status: %w", err)
	}
	return coolifyDockerRuntimeForServices(serviceNames, containers), nil
}

func startCoolifyServiceRuntime(ctx context.Context, ref DeploymentRef, dockerEnv []string) error {
	composePath, err := coolifyServiceComposePath(ref.ExternalID)
	if err != nil {
		return err
	}
	timeout := coolifyStatusPollInterval
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	if timeout < 2*time.Minute {
		timeout = 2 * time.Minute
	}
	startCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(startCtx, "docker", coolifyServiceRuntimeStartArgs(composePath, ref)...) // #nosec G204
	cmd.Env = dockerCommandEnv(dockerEnv)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose up %s: %w: %s", ref.ExternalID, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func coolifyServiceRuntimeStartArgs(composePath string, ref DeploymentRef) []string {
	args := []string{"compose", "-f", composePath, "up", "-d", "--no-recreate"}
	for _, serviceName := range ref.ServiceNames {
		serviceName = strings.TrimSpace(serviceName)
		if serviceName != "" {
			args = append(args, serviceName)
		}
	}
	return args
}

func dockerCommandEnv(extra []string) []string {
	if len(extra) == 0 {
		return nil
	}
	return append(os.Environ(), extra...)
}

func coolifyServiceComposePath(externalID string) (string, error) {
	externalID = strings.TrimSpace(externalID)
	if externalID == "" || strings.ContainsAny(externalID, `/\`) || externalID == "." || externalID == ".." {
		return "", fmt.Errorf("invalid coolify service external id %q", externalID)
	}
	return filepath.Join("/data/coolify/services", externalID, "docker-compose.yml"), nil
}

func coolifyDockerRuntimeForServices(serviceNames []string, containers []dockerInspectContainer) coolifyDockerRuntimeObservation {
	waiting := make([]string, 0)
	active := false
	for _, serviceName := range serviceNames {
		serviceName = strings.TrimSpace(serviceName)
		if serviceName == "" {
			continue
		}
		matched := false
		serviceRunning := false
		serviceActive := false
		lastState := "missing"
		for _, container := range containers {
			if container.Config.Labels["com.docker.compose.service"] != serviceName {
				continue
			}
			matched = true
			state := dockerContainerStateText(container)
			lastState = state
			if dockerContainerActive(container) {
				serviceActive = true
			}
			if dockerContainerReady(container) {
				serviceRunning = true
				break
			}
		}
		if serviceRunning {
			active = true
			continue
		}
		if serviceActive {
			active = true
		}
		if !matched {
			waiting = append(waiting, serviceName+"=missing")
			continue
		}
		waiting = append(waiting, serviceName+"="+lastState)
	}
	if len(waiting) == 0 {
		return coolifyDockerRuntimeObservation{Known: true, Running: true, Active: true, Status: "docker:running"}
	}
	return coolifyDockerRuntimeObservation{Known: true, Active: active, Status: "docker:waiting " + strings.Join(waiting, ", ")}
}

func dockerContainerReady(container dockerInspectContainer) bool {
	if !strings.EqualFold(container.State.Status, "running") || !container.State.Running {
		return false
	}
	if container.State.Health == nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(container.State.Health.Status), "healthy")
}

func dockerContainerActive(container dockerInspectContainer) bool {
	return strings.EqualFold(container.State.Status, "running") && container.State.Running
}

func dockerContainerStateText(container dockerInspectContainer) string {
	state := strings.TrimSpace(container.State.Status)
	if state == "" {
		state = "unknown"
	}
	if container.State.Health != nil && strings.TrimSpace(container.State.Health.Status) != "" {
		state += "(" + strings.TrimSpace(container.State.Health.Status) + ")"
	}
	return state
}

type coolifyServiceResourceStatus struct {
	coolifyObservedStatus
	Resources map[string]struct{}
}

func (a *CoolifyAdapter) serviceResourceStatus(ctx context.Context, ref DeploymentRef) (coolifyServiceResourceStatus, error) {
	path := "/api/v1/services/" + url.PathEscape(ref.ExternalID)
	var payload map[string]any
	if _, _, err := a.client.getJSON(ctx, path, &payload); err != nil {
		return coolifyServiceResourceStatus{}, fmt.Errorf("coolify status %q: %w", ref.AppName, err)
	}
	status := firstString(payload, "status", "application_status", "human_status")
	serverStatus, serverStatusKnown := payload["server_status"].(bool)
	if status == "" && serverStatusKnown {
		status = fmt.Sprintf("server:%t", serverStatus)
	}
	return coolifyServiceResourceStatus{
		coolifyObservedStatus: coolifyObservedStatus{
			Status:            status,
			ServerStatus:      serverStatus,
			ServerStatusKnown: serverStatusKnown,
			ObservedAt:        time.Now().UTC(),
		},
		Resources: coolifyServiceResourceNames(payload),
	}, nil
}

func (a *CoolifyAdapter) Delete(ctx context.Context, ref DeploymentRef) error {
	if ref.ExternalID == "" {
		return fmt.Errorf("coolify delete requires external id")
	}
	path := "/api/v1/services/" + url.PathEscape(ref.ExternalID)
	if a.cfg.LegacyDockerComposeAPI {
		path = "/api/v1/applications/" + url.PathEscape(ref.ExternalID)
	}
	if _, _, err := a.client.doJSON(ctx, "DELETE", path, nil, nil); err != nil {
		return fmt.Errorf("coolify delete %q: %w", ref.AppName, err)
	}
	return nil
}

func coolifyServicePayload(manifest AppManifest, cfg HTTPConfig) map[string]any {
	compose := normalizeComposeYAML(manifest.ComposeYAML)
	resourceName := coolifyServiceResourceName(manifest.Name, compose)
	payload := map[string]any{
		"name":                              resourceName,
		"description":                       "Managed by StackKit",
		"docker_compose_raw":                base64.StdEncoding.EncodeToString([]byte(compose)),
		"instant_deploy":                    false,
		"is_container_label_escape_enabled": true,
	}
	if cfg.ProjectUUID != "" {
		payload["project_uuid"] = cfg.ProjectUUID
	}
	if cfg.ServerID != "" {
		payload["server_uuid"] = cfg.ServerID
	}
	if cfg.EnvironmentUUID != "" {
		payload["environment_uuid"] = cfg.EnvironmentUUID
	}
	if cfg.EnvironmentID != "" {
		payload["environment_name"] = cfg.EnvironmentID
	}
	if cfg.DestinationUUID != "" {
		payload["destination_uuid"] = cfg.DestinationUUID
	}
	if route := coolifyServiceRoute(manifest); route != "" {
		payload["urls"] = []map[string]string{{
			"name": resourceName,
			"url":  route,
		}}
	}
	return payload
}

func coolifyServiceResourceName(appName, compose string) string {
	firstService, appNameExists := firstComposeServiceName(compose, appName)
	if appNameExists || firstService == "" {
		return appName
	}
	return firstService
}

func firstComposeServiceName(compose, appName string) (string, bool) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(compose), &root); err != nil || len(root.Content) == 0 {
		return "", false
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return "", false
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value != "services" {
			continue
		}
		services := doc.Content[i+1]
		if services.Kind != yaml.MappingNode || len(services.Content) == 0 {
			return "", false
		}
		first := ""
		for j := 0; j+1 < len(services.Content); j += 2 {
			name := services.Content[j].Value
			if first == "" {
				first = name
			}
			if name == appName {
				return first, true
			}
		}
		return first, false
	}
	return "", false
}

func composeServiceNames(compose string) []string {
	return composeServiceNamesMatching(compose, func(string, *yaml.Node) bool {
		return true
	})
}

func composeLongRunningServiceNames(compose string) []string {
	return composeServiceNamesMatching(compose, func(_ string, service *yaml.Node) bool {
		return !composeServiceIsOneShot(service)
	})
}

func composeServiceNamesMatching(compose string, include func(string, *yaml.Node) bool) []string {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(normalizeComposeYAML(compose)), &root); err != nil || len(root.Content) == 0 {
		return nil
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value != "services" {
			continue
		}
		services := doc.Content[i+1]
		if services.Kind != yaml.MappingNode {
			return nil
		}
		names := make([]string, 0, len(services.Content)/2)
		for j := 0; j+1 < len(services.Content); j += 2 {
			name := strings.TrimSpace(services.Content[j].Value)
			if name != "" && include(name, services.Content[j+1]) {
				names = append(names, name)
			}
		}
		return names
	}
	return nil
}

func composeServiceIsOneShot(service *yaml.Node) bool {
	if service == nil || service.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(service.Content); i += 2 {
		if service.Content[i].Value != "restart" {
			continue
		}
		value := strings.Trim(strings.ToLower(strings.TrimSpace(service.Content[i+1].Value)), `"'`)
		return value == "no" || value == "false"
	}
	return false
}

func coolifyServiceResourceNames(payload map[string]any) map[string]struct{} {
	resources := make(map[string]struct{})
	for _, key := range []string{"applications", "databases"} {
		items, _ := payload[key].([]any)
		for _, item := range items {
			resource, _ := item.(map[string]any)
			if name := firstString(resource, "name"); name != "" {
				resources[name] = struct{}{}
			}
		}
	}
	return resources
}

func missingServiceResources(expected []string, actual map[string]struct{}) []string {
	missing := make([]string, 0)
	for _, name := range expected {
		if _, ok := actual[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

func coolifyServiceRoute(manifest AppManifest) string {
	route := manifest.URL
	if route == "" {
		route = manifest.Host
	}
	if route == "" {
		return ""
	}
	if strings.HasPrefix(route, "http://") || strings.HasPrefix(route, "https://") {
		return route
	}
	return "https://" + route
}

func normalizeComposeYAML(compose string) string {
	compose = strings.ReplaceAll(compose, "\r\n", "\n")
	compose = strings.Trim(compose, "\n")
	lines := strings.Split(compose, "\n")
	minIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := 0
		for indent < len(line) && line[indent] == ' ' {
			indent++
		}
		if minIndent == -1 || indent < minIndent {
			minIndent = indent
		}
	}
	if minIndent > 0 {
		for i, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if len(line) >= minIndent {
				lines[i] = line[minIndent:]
			}
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n")) + "\n"
}

func firstDeploymentID(payload map[string]any) string {
	items, _ := payload["deployments"].([]any)
	if len(items) == 0 {
		return ""
	}
	first, _ := items[0].(map[string]any)
	return firstString(first, "deployment_uuid", "uuid", "id")
}
