// Package verify implements post-deployment StackKit verification.
package verify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
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
	Key         string `json:"key"`
	URL         string `json:"url"`
	ServiceRef  string `json:"serviceRef,omitempty"`
	RouteRef    string `json:"routeRef,omitempty"`
	WorkloadRef string `json:"workloadRef,omitempty"`
}

// HTTPRouteProbe is the reusable transport result consumed by both the
// legacy verify report and the native runtime-observation projection. The
// caller supplies only access-manifest targets; this package never discovers
// or invents routes.
type HTTPRouteProbe struct {
	Vantage      string    `json:"vantage"`
	Key          string    `json:"key"`
	ServiceRef   string    `json:"serviceRef,omitempty"`
	RouteRef     string    `json:"routeRef,omitempty"`
	WorkloadRef  string    `json:"workloadRef,omitempty"`
	URL          string    `json:"url"`
	Method       string    `json:"method"`
	Reached      bool      `json:"reached"`
	StatusCode   int       `json:"statusCode,omitempty"`
	ObservedAt   time.Time `json:"observedAt"`
	FailureClass string    `json:"failureClass,omitempty"`
	Error        string    `json:"error,omitempty"`
}

const (
	HTTPProbeVantage          = "verifier-host"
	maxHTTPProbeDuration      = 10 * time.Second
	maxHTTPProbeBatchDuration = 30 * time.Second
	maxHTTPProbeResponseBytes = 64 << 10
	maxHTTPProbeRedirects     = 5
	maxHTTPProbeConcurrency   = 4
)

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
	probes, err := ProbeHTTPRoutes(ctx, access, client)
	if err != nil {
		add("http-routes", StatusFail, "", err.Error())
		return
	}
	for _, probe := range probes {
		key := probe.Key
		if key == "" {
			key = "service"
		}
		if probe.Error != "" {
			add("http:"+key, StatusFail, probe.URL, probe.Error)
			continue
		}
		if isReachableHTTPStatus(probe.StatusCode) {
			add("http:"+key, StatusPass, probe.URL, fmt.Sprintf("route is reachable with HTTP %d", probe.StatusCode))
			continue
		}
		add("http:"+key, StatusFail, probe.URL, fmt.Sprintf("route returned HTTP %d", probe.StatusCode))
	}
}

// ProbeHTTPRoutes performs bounded GET requests using the verifier host as
// the sole vantage. Redirects must remain on the same authority and HTTPS
// cannot be downgraded. A per-request result preserves failed probes so a
// caller can expose an honest unverified/blocked projection for each route.
func ProbeHTTPRoutes(ctx context.Context, access *AccessSummary, client *http.Client) ([]HTTPRouteProbe, error) {
	if access == nil || len(access.Services) == 0 {
		return nil, errors.New("HTTP probe requested but no verified access manifest is available; run stackkit generate/apply first")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	client, err := prepareHTTPProbeClient(client)
	if err != nil {
		return nil, err
	}
	batchContext, cancel := context.WithTimeout(ctx, maxHTTPProbeBatchDuration)
	defer cancel()
	result := make([]HTTPRouteProbe, len(access.Services))
	semaphore := make(chan struct{}, maxHTTPProbeConcurrency)
	var wait sync.WaitGroup
	for index, service := range access.Services {
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-batchContext.Done():
				result[index] = probeHTTPRoute(batchContext, client, service)
				return
			}
			result[index] = probeHTTPRoute(batchContext, client, service)
		}()
	}
	wait.Wait()
	return result, nil
}

func prepareHTTPProbeClient(input *http.Client) (*http.Client, error) {
	client := &http.Client{}
	if input != nil {
		clone := *input
		client = &clone
	}
	if client.Timeout <= 0 || client.Timeout > maxHTTPProbeDuration {
		client.Timeout = maxHTTPProbeDuration
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	base, ok := transport.(*http.Transport)
	if !ok {
		return nil, errors.New("HTTP probe requires the standard TLS-validating transport")
	}
	base = base.Clone()
	if base.DialTLS != nil || base.DialTLSContext != nil {
		return nil, errors.New("HTTP probe refuses a custom TLS dialer")
	}
	if base.TLSClientConfig != nil && base.TLSClientConfig.InsecureSkipVerify {
		return nil, errors.New("HTTP probe refuses an insecure TLS transport")
	}
	// A proxy changes the network vantage and is not part of the verified
	// Access manifest. Direct transport keeps the claim scoped to this host.
	base.Proxy = nil
	client.Transport = base
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) == 0 {
			return nil
		}
		previous := via[len(via)-1].URL
		if previous == nil || req.URL == nil || !sameHTTPAuthority(previous, req.URL) {
			return errors.New("HTTP probe redirect changed route authority")
		}
		if previous.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("HTTP probe redirect downgraded HTTPS")
		}
		if len(via) >= maxHTTPProbeRedirects {
			return http.ErrUseLastResponse
		}
		return nil
	}
	return client, nil
}

func probeHTTPRoute(ctx context.Context, client *http.Client, service AccessService) HTTPRouteProbe {
	probe := HTTPRouteProbe{
		Vantage:     HTTPProbeVantage,
		Key:         strings.TrimSpace(service.Key),
		ServiceRef:  strings.TrimSpace(service.ServiceRef),
		RouteRef:    strings.TrimSpace(service.RouteRef),
		WorkloadRef: strings.TrimSpace(service.WorkloadRef),
		URL:         strings.TrimSpace(service.URL),
		Method:      http.MethodGet,
		ObservedAt:  time.Now().UTC(),
	}
	if probe.Key == "" {
		probe.Key = "service"
	}
	if probe.ServiceRef == "" {
		probe.ServiceRef = probe.Key
	}
	parsed, err := url.Parse(probe.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || strings.ContainsAny(probe.URL, "\r\n\x00") {
		probe.Error = "HTTP probe target is not a valid secret-free HTTP(S) URL"
		probe.FailureClass = "invalid-target"
		return probe
	}
	requestContext, cancel := context.WithTimeout(ctx, maxHTTPProbeDuration)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, probe.URL, nil)
	if err != nil {
		probe.Error = "HTTP probe request could not be created"
		probe.FailureClass = "request-creation-failed"
		return probe
	}
	response, err := client.Do(request)
	if err != nil {
		probe.Error = "HTTP probe request failed: " + err.Error()
		probe.FailureClass = "request-failed"
		return probe
	}
	defer response.Body.Close()
	probe.StatusCode = response.StatusCode
	read, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, maxHTTPProbeResponseBytes+1))
	if readErr != nil {
		probe.Error = "HTTP probe response could not be read: " + readErr.Error()
		probe.FailureClass = "response-read-failed"
		return probe
	}
	if read > maxHTTPProbeResponseBytes {
		probe.Error = "HTTP probe response exceeded the bounded response limit"
		probe.FailureClass = "response-too-large"
		return probe
	}
	probe.Reached = isReachableHTTPStatus(response.StatusCode)
	return probe
}

func sameHTTPAuthority(left, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	leftPort, rightPort := left.Port(), right.Port()
	if leftPort == "" {
		leftPort = defaultHTTPPort(left.Scheme)
	}
	if rightPort == "" {
		rightPort = defaultHTTPPort(right.Scheme)
	}
	return strings.EqualFold(left.Hostname(), right.Hostname()) && leftPort == rightPort
}

func defaultHTTPPort(scheme string) string {
	if strings.EqualFold(scheme, "https") {
		return "443"
	}
	return "80"
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
