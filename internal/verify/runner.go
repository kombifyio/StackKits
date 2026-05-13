// Package verify implements post-deployment StackKit verification.
package verify

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/pkg/models"
)

// Status is the verification result for a report or individual check.
type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Check is one post-deployment verification assertion.
type Check struct {
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Target  string `json:"target,omitempty"`
	Message string `json:"message"`
}

// Report is the machine-readable output of a verify run.
type Report struct {
	Status      Status    `json:"status"`
	StackKit    string    `json:"stackkit,omitempty"`
	Mode        string    `json:"mode,omitempty"`
	Remote      bool      `json:"remote,omitempty"`
	TargetHost  string    `json:"targetHost,omitempty"`
	GeneratedAt time.Time `json:"generatedAt"`
	Checks      []Check   `json:"checks"`
}

// Options controls optional verification surfaces.
type Options struct {
	Strict bool
	HTTP   bool
}

// AccessSummary is the subset of command access metadata needed by verify.
type AccessSummary struct {
	Services []AccessService `json:"services"`
}

// AccessService is a routable service endpoint.
type AccessService struct {
	Key string `json:"key"`
	URL string `json:"url"`
}

// DockerClient is the Docker surface needed by the verifier.
type DockerClient interface {
	IsInstalled() bool
	IsRunning(context.Context) bool
	GetStackKitContainers(context.Context) ([]docker.ContainerInfo, error)
	GetContainerHealth(context.Context, string) (models.HealthStatus, error)
}

// Input holds all dependencies and state for a local verification run.
type Input struct {
	Spec    *models.StackSpec
	State   *models.DeploymentState
	Docker  DockerClient
	Access  *AccessSummary
	Options Options
	HTTP    *http.Client
}

// RunLocal verifies a StackKit deployment from the host where the deployment runs.
func RunLocal(ctx context.Context, input Input) Report {
	report := Report{
		Status:      StatusPass,
		GeneratedAt: time.Now().UTC(),
	}
	add := func(name string, status Status, target, message string) {
		report.Checks = append(report.Checks, Check{
			Name:    name,
			Status:  status,
			Target:  target,
			Message: message,
		})
		switch status {
		case StatusFail:
			report.Status = StatusFail
		case StatusWarn:
			if report.Status == StatusPass {
				report.Status = StatusWarn
			}
		}
	}

	if input.Spec == nil {
		add("spec", StatusFail, "", "stack spec is missing")
	} else {
		report.StackKit = input.Spec.StackKit
		report.Mode = input.Spec.Mode
		add("spec", StatusPass, "", "stack spec loaded")
	}

	if input.State == nil {
		add("deployment-state", StatusFail, ".stackkit/state.yaml", "deployment state is missing; run stackkit apply first")
	} else {
		checkDeploymentState(input.State, add)
	}

	if input.Docker == nil {
		add("docker-client", StatusFail, "docker", "Docker verifier dependency is missing")
		finishStrict(&report, input.Options)
		return report
	}
	if !input.Docker.IsInstalled() {
		add("docker-installed", StatusFail, "docker", "Docker binary is not installed or not on PATH")
		finishStrict(&report, input.Options)
		return report
	}
	add("docker-installed", StatusPass, "docker", "Docker binary is available")

	if !input.Docker.IsRunning(ctx) {
		add("docker-running", StatusFail, "docker", "Docker daemon is not running")
		finishStrict(&report, input.Options)
		return report
	}
	add("docker-running", StatusPass, "docker", "Docker daemon is running")

	containers, err := input.Docker.GetStackKitContainers(ctx)
	if err != nil {
		add("stackkit-containers", StatusFail, "docker", fmt.Sprintf("could not list StackKit containers: %v", err))
		finishStrict(&report, input.Options)
		return report
	}
	if len(containers) == 0 {
		add("stackkit-containers", StatusFail, "docker", "no containers with label stackkit.layer found")
		finishStrict(&report, input.Options)
		return report
	}
	add("stackkit-containers", StatusPass, "docker", fmt.Sprintf("%d StackKit container(s) found", len(containers)))

	for _, container := range containers {
		verifyContainer(ctx, input.Docker, container, add)
	}

	if input.Options.HTTP {
		verifyHTTPRoutes(ctx, input.Access, input.HTTP, add)
	}

	finishStrict(&report, input.Options)
	return report
}

func checkDeploymentState(state *models.DeploymentState, add func(string, Status, string, string)) {
	switch state.Status {
	case models.StatusRunning:
		add("deployment-state", StatusPass, ".stackkit/state.yaml", "deployment state is running")
	case models.StatusDegraded:
		add("deployment-state", StatusWarn, ".stackkit/state.yaml", "deployment state is degraded")
	case models.StatusError, models.StatusRemoved:
		add("deployment-state", StatusFail, ".stackkit/state.yaml", fmt.Sprintf("deployment state is %s", state.Status))
	case models.StatusPending, models.StatusPlanning, models.StatusApplying:
		add("deployment-state", StatusWarn, ".stackkit/state.yaml", fmt.Sprintf("deployment state is still %s", state.Status))
	default:
		add("deployment-state", StatusWarn, ".stackkit/state.yaml", fmt.Sprintf("deployment state is %q", state.Status))
	}
	if state.LastApplied.IsZero() {
		add("last-applied", StatusWarn, ".stackkit/state.yaml", "deployment state has no lastApplied timestamp")
	} else {
		add("last-applied", StatusPass, ".stackkit/state.yaml", "deployment timestamp is present")
	}
}

func verifyContainer(
	ctx context.Context,
	client DockerClient,
	container docker.ContainerInfo,
	add func(string, Status, string, string),
) {
	name := strings.TrimSpace(container.Name)
	if name == "" {
		name = container.ID
	}
	target := name
	if container.ID != "" {
		target = container.ID
	}

	switch {
	case container.State.Running:
		add("container:"+name, StatusPass, target, "container is running")
	case container.State.Restarting:
		add("container:"+name, StatusWarn, target, "container is restarting")
	default:
		add("container:"+name, StatusFail, target, "container is not running")
	}

	healthTarget := container.ID
	if healthTarget == "" {
		healthTarget = name
	}
	health, err := client.GetContainerHealth(ctx, healthTarget)
	if err != nil {
		add("health:"+name, StatusWarn, target, fmt.Sprintf("could not read Docker health: %v", err))
		return
	}
	switch health {
	case models.HealthStatusHealthy:
		add("health:"+name, StatusPass, target, "Docker health is healthy")
	case models.HealthStatusUnhealthy:
		add("health:"+name, StatusFail, target, "Docker health is unhealthy")
	case models.HealthStatusStarting:
		add("health:"+name, StatusWarn, target, "Docker health is starting")
	case models.HealthStatusNone:
		add("health:"+name, StatusWarn, target, "container has no Docker healthcheck")
	default:
		add("health:"+name, StatusWarn, target, "Docker health is unknown")
	}
}

func verifyHTTPRoutes(
	ctx context.Context,
	access *AccessSummary,
	client *http.Client,
	add func(string, Status, string, string),
) {
	if access == nil || len(access.Services) == 0 {
		add("http-routes", StatusWarn, "", "no access summary available; run stackkit generate/apply first")
		return
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	for _, service := range access.Services {
		key := strings.TrimSpace(service.Key)
		if key == "" {
			key = "service"
		}
		url := strings.TrimSpace(service.URL)
		if url == "" {
			add("http:"+key, StatusWarn, "", "service has no URL")
			continue
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			add("http:"+key, StatusFail, url, fmt.Sprintf("invalid URL: %v", err))
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			add("http:"+key, StatusFail, url, fmt.Sprintf("request failed: %v", err))
			continue
		}
		_ = resp.Body.Close()

		if isReachableHTTPStatus(resp.StatusCode) {
			add("http:"+key, StatusPass, url, fmt.Sprintf("route is reachable with HTTP %d", resp.StatusCode))
			continue
		}
		add("http:"+key, StatusFail, url, fmt.Sprintf("route returned HTTP %d", resp.StatusCode))
	}
}

func isReachableHTTPStatus(code int) bool {
	if code >= 200 && code < 400 {
		return true
	}
	return code == http.StatusUnauthorized || code == http.StatusForbidden
}

func finishStrict(report *Report, options Options) {
	if options.Strict && report.Status == StatusWarn {
		report.Status = StatusFail
	}
}
