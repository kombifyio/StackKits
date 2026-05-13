package verify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/pkg/models"
)

type fakeDockerClient struct {
	installed  bool
	running    bool
	containers []docker.ContainerInfo
	health     map[string]models.HealthStatus
	err        error
}

func (f fakeDockerClient) IsInstalled() bool {
	return f.installed
}

func (f fakeDockerClient) IsRunning(context.Context) bool {
	return f.running
}

func (f fakeDockerClient) GetStackKitContainers(context.Context) ([]docker.ContainerInfo, error) {
	return f.containers, f.err
}

func (f fakeDockerClient) GetContainerHealth(_ context.Context, id string) (models.HealthStatus, error) {
	if health, ok := f.health[id]; ok {
		return health, nil
	}
	return models.HealthStatusNone, nil
}

func TestRunLocalFailsWithoutDeploymentState(t *testing.T) {
	report := RunLocal(context.Background(), Input{
		Spec: &models.StackSpec{StackKit: "base-kit", Mode: "simple"},
		Docker: fakeDockerClient{
			installed: true,
			running:   true,
		},
	})

	if report.Status != StatusFail {
		t.Fatalf("status = %q, want fail", report.Status)
	}
	assertCheck(t, report, "deployment-state", StatusFail)
}

func TestRunLocalChecksStackKitContainersAndHealth(t *testing.T) {
	report := RunLocal(context.Background(), Input{
		Spec:  &models.StackSpec{StackKit: "base-kit", Mode: "simple"},
		State: &models.DeploymentState{Status: models.StatusRunning, LastApplied: time.Now()},
		Docker: fakeDockerClient{
			installed: true,
			running:   true,
			containers: []docker.ContainerInfo{
				{ID: "abc123456789", Name: "traefik", State: docker.ContainerState{Running: true}},
				{ID: "def123456789", Name: "pocketid", State: docker.ContainerState{Running: true}},
			},
			health: map[string]models.HealthStatus{
				"abc123456789": models.HealthStatusHealthy,
				"def123456789": models.HealthStatusNone,
			},
		},
	})

	if report.Status != StatusWarn {
		t.Fatalf("status = %q, want warn because pocketid has no Docker healthcheck", report.Status)
	}
	assertCheck(t, report, "docker-installed", StatusPass)
	assertCheck(t, report, "docker-running", StatusPass)
	assertCheck(t, report, "container:traefik", StatusPass)
	assertCheck(t, report, "health:pocketid", StatusWarn)
}

func TestRunLocalHTTPChecksTreatReachableProtectedEndpointsAsPass(t *testing.T) {
	protected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "login required", http.StatusUnauthorized)
	}))
	defer protected.Close()

	report := RunLocal(context.Background(), Input{
		Spec:  &models.StackSpec{StackKit: "base-kit", Mode: "simple"},
		State: &models.DeploymentState{Status: models.StatusRunning, LastApplied: time.Now()},
		Docker: fakeDockerClient{
			installed: true,
			running:   true,
			containers: []docker.ContainerInfo{
				{ID: "abc123456789", Name: "traefik", State: docker.ContainerState{Running: true}},
			},
			health: map[string]models.HealthStatus{"abc123456789": models.HealthStatusHealthy},
		},
		Access: &AccessSummary{
			Services: []AccessService{{Key: "auth", URL: protected.URL}},
		},
		Options: Options{HTTP: true},
		HTTP:    protected.Client(),
	})

	if report.Status != StatusPass {
		t.Fatalf("status = %q, checks=%#v", report.Status, report.Checks)
	}
	assertCheck(t, report, "http:auth", StatusPass)
}

func TestRunLocalHTTPChecksFailOnServerErrors(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "broken", http.StatusBadGateway)
	}))
	defer broken.Close()

	report := RunLocal(context.Background(), Input{
		Spec:  &models.StackSpec{StackKit: "base-kit", Mode: "simple"},
		State: &models.DeploymentState{Status: models.StatusRunning, LastApplied: time.Now()},
		Docker: fakeDockerClient{
			installed: true,
			running:   true,
			containers: []docker.ContainerInfo{
				{ID: "abc123456789", Name: "traefik", State: docker.ContainerState{Running: true}},
			},
			health: map[string]models.HealthStatus{"abc123456789": models.HealthStatusHealthy},
		},
		Access: &AccessSummary{
			Services: []AccessService{{Key: "dashboard", URL: broken.URL}},
		},
		Options: Options{HTTP: true},
		HTTP:    broken.Client(),
	})

	if report.Status != StatusFail {
		t.Fatalf("status = %q, want fail", report.Status)
	}
	check := assertCheck(t, report, "http:dashboard", StatusFail)
	if !strings.Contains(check.Message, "502") {
		t.Fatalf("message = %q, want status code", check.Message)
	}
}

func TestRunLocalStrictEscalatesWarnings(t *testing.T) {
	report := RunLocal(context.Background(), Input{
		Spec:  &models.StackSpec{StackKit: "base-kit", Mode: "simple"},
		State: &models.DeploymentState{Status: models.StatusDegraded, LastApplied: time.Now()},
		Docker: fakeDockerClient{
			installed: true,
			running:   true,
			containers: []docker.ContainerInfo{
				{ID: "abc123456789", Name: "traefik", State: docker.ContainerState{Running: true}},
			},
			health: map[string]models.HealthStatus{"abc123456789": models.HealthStatusHealthy},
		},
		Options: Options{Strict: true},
	})

	if report.Status != StatusFail {
		t.Fatalf("strict report status = %q, want fail", report.Status)
	}
}

func TestReportJSONOmitsRemoteMetadataForLocalReports(t *testing.T) {
	data, err := json.Marshal(Report{
		Status:      StatusPass,
		GeneratedAt: time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC),
		Checks:      []Check{{Name: "spec", Status: StatusPass, Message: "loaded"}},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(data)
	if strings.Contains(got, "remote") || strings.Contains(got, "targetHost") {
		t.Fatalf("local report should omit remote metadata: %s", got)
	}
}

func TestReportJSONIncludesRemoteMetadataForRemoteReports(t *testing.T) {
	data, err := json.Marshal(Report{
		Status:      StatusPass,
		Remote:      true,
		TargetHost:  "203.0.113.10",
		GeneratedAt: time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC),
		Checks:      []Check{{Name: "spec", Status: StatusPass, Message: "loaded"}},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, `"remote":true`) || !strings.Contains(got, `"targetHost":"203.0.113.10"`) {
		t.Fatalf("remote report should include metadata: %s", got)
	}
}

func assertCheck(t *testing.T, report Report, name string, status Status) Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			if check.Status != status {
				t.Fatalf("%s status = %q, want %q (check=%#v)", name, check.Status, status, check)
			}
			return check
		}
	}
	t.Fatalf("check %q not found in %#v", name, report.Checks)
	return Check{}
}
