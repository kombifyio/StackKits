package opentofu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// LiveDriftReason classifies a concrete runtime divergence that `tofu plan`
// alone cannot see on Stage-1 Compose wrappers (ADR-0054 §1).
type LiveDriftReason string

const (
	LiveDriftServiceMissing      LiveDriftReason = "service_missing"
	LiveDriftContainerNotRunning LiveDriftReason = "container_not_running"
	LiveDriftContainerOOMKilled  LiveDriftReason = "container_oom_killed"
	LiveDriftImageDigestMismatch LiveDriftReason = "image_digest_mismatch"
	LiveDriftHealthUnhealthy     LiveDriftReason = "health_unhealthy"
	LiveDriftRouteProbeFailed    LiveDriftReason = "route_probe_failed"
	LiveDriftRuntimeUnavailable  LiveDriftReason = "runtime_unavailable"
)

// ExpectedServiceRuntime declares the expected live state of one container
// service derived from the ResolvedPlan / Compose payload.
type ExpectedServiceRuntime struct {
	Service        string `json:"service"`
	ExpectedImage  string `json:"expectedImage,omitempty"`
	ExpectedDigest string `json:"expectedDigest,omitempty"`
	RequireHealthy bool   `json:"requireHealthy,omitempty"`
	OneShot        bool   `json:"oneShot,omitempty"`
}

// RouteProbeTarget is one loopback or route HTTP probe paired with container
// inspection during live drift checks.
type RouteProbeTarget struct {
	Name           string        `json:"name"`
	URL            string        `json:"url"`
	ExpectedStatus int           `json:"expectedStatus,omitempty"`
	Timeout        time.Duration `json:"timeout,omitempty"`
}

// LiveDriftRequest describes what ObserveLiveDrift inspects for one root.
type LiveDriftRequest struct {
	ComposeProject string                   `json:"composeProject"`
	ComposeFile    string                   `json:"composeFile,omitempty"`
	EnvFile        string                   `json:"envFile,omitempty"`
	Expected       []ExpectedServiceRuntime `json:"expected"`
	RouteProbes    []RouteProbeTarget       `json:"routeProbes,omitempty"`
	// Environment optionally supplies DOCKER_HOST / COMPOSE_PROJECT_NAME etc.
	Environment []string `json:"-"`
}

// ObservedContainerState is the secret-free runtime fact for one container.
type ObservedContainerState struct {
	StartedAt   string   `json:"startedAt,omitempty"`
	Service     string   `json:"service"`
	ContainerID string   `json:"containerId"`
	Name        string   `json:"name"`
	State       string   `json:"state"`
	ExitCode    int      `json:"exitCode"`
	OOMKilled   bool     `json:"oomKilled,omitempty"`
	Health      string   `json:"health,omitempty"`
	Image       string   `json:"image,omitempty"`
	ImageDigest string   `json:"imageDigest,omitempty"`
	VolumeNames []string `json:"volumeNames,omitempty"`
}

// LiveDriftFinding records one secret-free live drift defect.
type LiveDriftFinding struct {
	Service string          `json:"service,omitempty"`
	Target  string          `json:"target,omitempty"`
	Reason  LiveDriftReason `json:"reason"`
	Detail  string          `json:"detail"`
}

// LiveDriftReport is the secret-free result of live runtime observation.
type LiveDriftReport struct {
	SchemaVersion  string                   `json:"schemaVersion"`
	ComposeProject string                   `json:"composeProject"`
	ObservedAt     time.Time                `json:"observedAt"`
	Healthy        bool                     `json:"healthy"`
	Containers     []ObservedContainerState `json:"containers"`
	Findings       []LiveDriftFinding       `json:"findings,omitempty"`
}

// CombinedConvergenceResult pairs `tofu plan -detailed-exitcode` with live
// runtime observation. Per ADR-0054 §1, a green `tofu plan` alone NEVER
// reports converged/healthy.
type CombinedConvergenceResult struct {
	PlanExitCode int             `json:"planExitCode"`
	PlanClean    bool            `json:"planClean"`
	LiveHealthy  bool            `json:"liveHealthy"`
	Converged    bool            `json:"converged"`
	Live         LiveDriftReport `json:"live"`
	Detail       string          `json:"detail,omitempty"`
}

// CombinePlanAndLiveDrift enforces the mandatory live-drift pairing invariant:
// converged is true ONLY when `tofu plan` exited 0 AND live observation is
// completely healthy.
func CombinePlanAndLiveDrift(planExitCode int, live LiveDriftReport) CombinedConvergenceResult {
	planClean := planExitCode == 0
	converged := planClean && live.Healthy && len(live.Findings) == 0
	detail := "tofu plan clean and live runtime healthy"
	switch {
	case planExitCode == 2 && !live.Healthy:
		detail = fmt.Sprintf("tofu plan drifted (exit 2) and live runtime has %d finding(s)", len(live.Findings))
	case planExitCode == 2:
		detail = "tofu plan drifted (exit 2)"
	case planExitCode != 0:
		detail = fmt.Sprintf("tofu plan failed (exit %d)", planExitCode)
	case !live.Healthy || len(live.Findings) > 0:
		detail = fmt.Sprintf("tofu plan clean (exit 0) but live runtime drifted (%d finding(s))", len(live.Findings))
	}
	return CombinedConvergenceResult{
		PlanExitCode: planExitCode,
		PlanClean:    planClean,
		LiveHealthy:  live.Healthy && len(live.Findings) == 0,
		Converged:    converged,
		Live:         live,
		Detail:       detail,
	}
}

// ContainerInspector fetches secret-free container states for a Compose project.
type ContainerInspector interface {
	InspectProjectContainers(ctx context.Context, req LiveDriftRequest) ([]ObservedContainerState, error)
}

// LiveDriftObserver evaluates live container state, pinned image digests,
// healthchecks, and optional loopback/route HTTP probes.
type LiveDriftObserver struct {
	Inspector  ContainerInspector
	HTTPClient *http.Client
	Now        func() time.Time
}

// NewDefaultLiveDriftObserver creates an observer backed by `docker` CLI
// (compatible with Docker Engine and rootless Podman via DOCKER_HOST).
func NewDefaultLiveDriftObserver() *LiveDriftObserver {
	return &LiveDriftObserver{
		Inspector:  DockerCLIContainerInspector{Binary: "docker"},
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		Now:        time.Now,
	}
}

// Observe inspects the project's live containers and route probes.
func (o *LiveDriftObserver) Observe(ctx context.Context, req LiveDriftRequest) LiveDriftReport {
	nowFn := o.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	report := LiveDriftReport{
		SchemaVersion:  "stackkit.live-drift/v1",
		ComposeProject: req.ComposeProject,
		ObservedAt:     nowFn().UTC(),
		Healthy:        false,
	}
	if strings.TrimSpace(req.ComposeProject) == "" {
		report.Findings = append(report.Findings, LiveDriftFinding{
			Reason: LiveDriftRuntimeUnavailable,
			Detail: "composeProject is required for live drift observation",
		})
		return report
	}
	inspector := o.Inspector
	if inspector == nil {
		inspector = DockerCLIContainerInspector{Binary: "docker"}
	}
	containers, err := inspector.InspectProjectContainers(ctx, req)
	if err != nil {
		report.Findings = append(report.Findings, LiveDriftFinding{
			Reason: LiveDriftRuntimeUnavailable,
			Detail: fmt.Sprintf("inspect live containers for %s: %v", req.ComposeProject, err),
		})
		return report
	}
	sort.Slice(containers, func(i, j int) bool {
		if containers[i].Service != containers[j].Service {
			return containers[i].Service < containers[j].Service
		}
		return containers[i].ContainerID < containers[j].ContainerID
	})
	report.Containers = containers

	byService := make(map[string]ObservedContainerState, len(containers))
	for _, c := range containers {
		if c.Service != "" {
			byService[c.Service] = c
		}
	}

	// Evaluate declared expected services.
	for _, exp := range req.Expected {
		c, ok := byService[exp.Service]
		if !ok {
			report.Findings = append(report.Findings, LiveDriftFinding{
				Service: exp.Service,
				Reason:  LiveDriftServiceMissing,
				Detail:  fmt.Sprintf("service %q has no container in project %s", exp.Service, req.ComposeProject),
			})
			continue
		}
		if c.OOMKilled {
			report.Findings = append(report.Findings, LiveDriftFinding{
				Service: exp.Service,
				Reason:  LiveDriftContainerOOMKilled,
				Detail:  fmt.Sprintf("service %q container %s was OOM-killed (exit %d)", exp.Service, shortID(c.ContainerID), c.ExitCode),
			})
		}
		stateLower := strings.ToLower(strings.TrimSpace(c.State))
		if exp.OneShot {
			if stateLower != "exited" || c.ExitCode != 0 {
				report.Findings = append(report.Findings, LiveDriftFinding{
					Service: exp.Service,
					Reason:  LiveDriftContainerNotRunning,
					Detail:  fmt.Sprintf("one-shot service %q state=%q exit=%d (expected exited/0)", exp.Service, c.State, c.ExitCode),
				})
			}
		} else if stateLower != "running" {
			report.Findings = append(report.Findings, LiveDriftFinding{
				Service: exp.Service,
				Reason:  LiveDriftContainerNotRunning,
				Detail:  fmt.Sprintf("service %q container %s is %q (exit %d), expected running", exp.Service, shortID(c.ContainerID), c.State, c.ExitCode),
			})
		}
		healthLower := strings.ToLower(strings.TrimSpace(c.Health))
		if healthLower == "unhealthy" || (exp.RequireHealthy && healthLower != "" && healthLower != "healthy") {
			report.Findings = append(report.Findings, LiveDriftFinding{
				Service: exp.Service,
				Reason:  LiveDriftHealthUnhealthy,
				Detail:  fmt.Sprintf("service %q healthcheck status is %q", exp.Service, c.Health),
			})
		}
		if wantDigest := extractSHA256Digest(exp.ExpectedDigest); wantDigest != "" {
			gotDigest := extractSHA256Digest(c.ImageDigest)
			if gotDigest == "" {
				gotDigest = extractSHA256Digest(c.Image)
			}
			if gotDigest != wantDigest {
				report.Findings = append(report.Findings, LiveDriftFinding{
					Service: exp.Service,
					Reason:  LiveDriftImageDigestMismatch,
					Detail:  fmt.Sprintf("service %q running digest %q differs from pinned %q", exp.Service, gotDigest, wantDigest),
				})
			}
		}
	}

	// If no explicit expected list was provided, still fail closed on any
	// non-running or unhealthy container present in the project.
	if len(req.Expected) == 0 {
		if len(containers) == 0 {
			report.Findings = append(report.Findings, LiveDriftFinding{
				Reason: LiveDriftServiceMissing,
				Detail: fmt.Sprintf("project %s has zero containers", req.ComposeProject),
			})
		}
		for _, c := range containers {
			if c.OOMKilled {
				report.Findings = append(report.Findings, LiveDriftFinding{
					Service: c.Service,
					Reason:  LiveDriftContainerOOMKilled,
					Detail:  fmt.Sprintf("container %s was OOM-killed", shortID(c.ContainerID)),
				})
			}
			if strings.ToLower(strings.TrimSpace(c.State)) != "running" {
				report.Findings = append(report.Findings, LiveDriftFinding{
					Service: c.Service,
					Reason:  LiveDriftContainerNotRunning,
					Detail:  fmt.Sprintf("container %s (%s) is %q (exit %d)", c.Service, shortID(c.ContainerID), c.State, c.ExitCode),
				})
			}
			if strings.EqualFold(strings.TrimSpace(c.Health), "unhealthy") {
				report.Findings = append(report.Findings, LiveDriftFinding{
					Service: c.Service,
					Reason:  LiveDriftHealthUnhealthy,
					Detail:  fmt.Sprintf("container %s (%s) is unhealthy", c.Service, shortID(c.ContainerID)),
				})
			}
		}
	}

	// Optional loopback / route HTTP probes.
	client := o.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	for _, probe := range req.RouteProbes {
		if err := runRouteProbe(ctx, client, probe); err != nil {
			report.Findings = append(report.Findings, LiveDriftFinding{
				Target: probe.Name,
				Reason: LiveDriftRouteProbeFailed,
				Detail: err.Error(),
			})
		}
	}

	report.Healthy = len(report.Findings) == 0
	return report
}

func runRouteProbe(ctx context.Context, client *http.Client, probe RouteProbeTarget) error {
	timeout := probe.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, probe.URL, nil)
	if err != nil {
		return fmt.Errorf("route probe %q invalid URL: %w", probe.Name, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("route probe %q failed: %w", probe.Name, err)
	}
	_ = resp.Body.Close()
	if probe.ExpectedStatus > 0 {
		if resp.StatusCode != probe.ExpectedStatus {
			return fmt.Errorf("route probe %q returned HTTP %d (expected %d)", probe.Name, resp.StatusCode, probe.ExpectedStatus)
		}
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("route probe %q returned HTTP %d", probe.Name, resp.StatusCode)
	}
	return nil
}

// DockerCLIContainerInspector queries secret-free container metadata via
// `docker ps` + `docker inspect` (intentionally never reading container Env!).
type DockerCLIContainerInspector struct {
	Binary string
}

func (d DockerCLIContainerInspector) InspectProjectContainers(ctx context.Context, req LiveDriftRequest) ([]ObservedContainerState, error) {
	bin := d.Binary
	if strings.TrimSpace(bin) == "" {
		bin = "docker"
	}
	psCmd := exec.CommandContext(ctx, bin, "ps", "-a", "--no-trunc",
		"--filter", "label=com.docker.compose.project="+req.ComposeProject,
		"--format", "{{.ID}}",
	)
	if len(req.Environment) > 0 {
		psCmd.Env = req.Environment
	}
	var psOut, psErr bytes.Buffer
	psCmd.Stdout = &psOut
	psCmd.Stderr = &psErr
	if err := psCmd.Run(); err != nil {
		return nil, fmt.Errorf("docker ps failed: %w (%s)", err, strings.TrimSpace(psErr.String()))
	}
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(psOut.String()), "\n") {
		if id := strings.TrimSpace(line); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	inspectArgs := append([]string{"inspect"}, ids...)
	inspectCmd := exec.CommandContext(ctx, bin, inspectArgs...)
	if len(req.Environment) > 0 {
		inspectCmd.Env = req.Environment
	}
	var inspectOut, inspectErr bytes.Buffer
	inspectCmd.Stdout = &inspectOut
	inspectCmd.Stderr = &inspectErr
	if err := inspectCmd.Run(); err != nil {
		return nil, fmt.Errorf("docker inspect failed: %w (%s)", err, strings.TrimSpace(inspectErr.String()))
	}
	// Decode ONLY secret-free fields (deliberately omitting Config.Env!).
	var raw []struct {
		ID    string `json:"Id"`
		Name  string `json:"Name"`
		Image string `json:"Image"`
		State struct {
			StartedAt string `json:"StartedAt"`
			Status    string `json:"Status"`
			ExitCode  int    `json:"ExitCode"`
			OOMKilled bool   `json:"OOMKilled"`
			Health    *struct {
				Status string `json:"Status"`
			} `json:"Health"`
		} `json:"State"`
		Config struct {
			Image  string            `json:"Image"`
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		Mounts []struct {
			Type        string `json:"Type"`
			Name        string `json:"Name"`
			Source      string `json:"Source"`
			Destination string `json:"Destination"`
		} `json:"Mounts"`
	}
	if err := json.Unmarshal(inspectOut.Bytes(), &raw); err != nil {
		return nil, fmt.Errorf("decode docker inspect: %w", err)
	}
	out := make([]ObservedContainerState, 0, len(raw))
	for _, c := range raw {
		health := ""
		if c.State.Health != nil {
			health = c.State.Health.Status
		}
		var vols []string
		for _, m := range c.Mounts {
			if m.Name != "" {
				vols = append(vols, m.Name)
			} else if m.Source != "" {
				vols = append(vols, m.Source)
			}
		}
		sort.Strings(vols)
		out = append(out, ObservedContainerState{
			StartedAt:   c.State.StartedAt,
			Service:     c.Config.Labels["com.docker.compose.service"],
			ContainerID: c.ID,
			Name:        strings.TrimPrefix(c.Name, "/"),
			State:       c.State.Status,
			ExitCode:    c.State.ExitCode,
			OOMKilled:   c.State.OOMKilled,
			Health:      health,
			Image:       c.Config.Image,
			ImageDigest: extractSHA256Digest(c.Config.Image),
			VolumeNames: vols,
		})
	}
	return out, nil
}

func extractSHA256Digest(raw string) string {
	raw = strings.TrimSpace(raw)
	if idx := strings.Index(raw, "sha256:"); idx >= 0 {
		rest := raw[idx:]
		if cut := strings.IndexAny(rest, " \t\n\r"); cut >= 0 {
			rest = rest[:cut]
		}
		return strings.ToLower(rest)
	}
	return ""
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// ErrLiveDriftDetected signals a combined convergence failure driven by live
// runtime state diverging from the declared plan.
var ErrLiveDriftDetected = errors.New("live container or route drift detected")
