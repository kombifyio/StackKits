package platformdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type localComposeRunner func(ctx context.Context, dir string, env []string, args ...string) ([]byte, error)

// LocalComposeOption configures the local Docker Compose adapter.
type LocalComposeOption func(*LocalComposeAdapter)

// WithLocalComposeRunner injects the command runner used by tests.
func WithLocalComposeRunner(runner localComposeRunner) LocalComposeOption {
	return func(adapter *LocalComposeAdapter) {
		if runner != nil {
			adapter.run = runner
		}
	}
}

// WithLocalComposeEnv binds Compose to the same target transport used by the
// resolved runtime (for example a remote DOCKER_HOST from an enrolled agent).
func WithLocalComposeEnv(env []string) LocalComposeOption {
	return func(adapter *LocalComposeAdapter) {
		adapter.env = append([]string(nil), env...)
	}
}

// LocalComposeAdapter deploys generated compose bundles directly on the node.
type LocalComposeAdapter struct {
	workDir string
	env     []string
	run     localComposeRunner
}

// NewLocalComposeAdapter returns an adapter for local, no-PaaS compose rollout.
func NewLocalComposeAdapter(workDir string, opts ...LocalComposeOption) *LocalComposeAdapter {
	adapter := &LocalComposeAdapter{
		workDir: workDir,
		run:     runLocalComposeCommand,
	}
	for _, opt := range opts {
		opt(adapter)
	}
	return adapter
}

// ApplyCompose runs docker compose up for one generated compose bundle.
func (a *LocalComposeAdapter) ApplyCompose(ctx context.Context, manifest AppManifest) (DeploymentRef, error) {
	composePath := strings.TrimSpace(manifest.ComposePath)
	if composePath == "" {
		return DeploymentRef{}, fmt.Errorf("local compose app %q missing composePath", manifest.Name)
	}
	appName := strings.TrimSpace(manifest.Name)
	if appName == "" {
		return DeploymentRef{}, fmt.Errorf("local compose app missing name")
	}

	project := "stackkit-" + appName
	output, err := a.run(ctx, a.workDir, a.env, "compose", "-p", project, "-f", composePath, "up", "-d")
	if err != nil {
		return DeploymentRef{}, fmt.Errorf("docker compose up %q: %w: %s", appName, err, strings.TrimSpace(string(output)))
	}

	platform := manifest.ManagedBy
	if platform == "" {
		platform = manifest.Platform
	}
	return DeploymentRef{
		Platform:     platform,
		AppName:      appName,
		ExternalID:   "local-compose:" + appName,
		DeploymentID: project,
		LastDeployed: time.Now().UTC(),
		ComposePath:  composePath,
	}, nil
}

// ObserveDeployment verifies the generated Compose project instead of treating
// a successful `up -d` process exit as runtime evidence.
func (a *LocalComposeAdapter) ObserveDeployment(ctx context.Context, ref DeploymentRef) (DeploymentRef, error) {
	if strings.TrimSpace(ref.DeploymentID) == "" || strings.TrimSpace(ref.ComposePath) == "" {
		return ref, fmt.Errorf("local compose observe %q requires project and compose path", ref.AppName)
	}
	output, err := a.run(ctx, a.workDir, a.env, "compose", "-p", ref.DeploymentID, "-f", ref.ComposePath, "ps", "--format", "json")
	if err != nil {
		return ref, fmt.Errorf("docker compose observe %q: %w: %s", ref.AppName, err, strings.TrimSpace(string(output)))
	}
	var rows []struct {
		Service string `json:"Service"`
		State   string `json:"State"`
		Health  string `json:"Health"`
	}
	trimmed := strings.TrimSpace(string(output))
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(output, &rows); err != nil {
			return ref, fmt.Errorf("decode docker compose observation %q: %w", ref.AppName, err)
		}
	} else {
		for _, line := range strings.Split(trimmed, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var row struct {
				Service string `json:"Service"`
				State   string `json:"State"`
				Health  string `json:"Health"`
			}
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				return ref, fmt.Errorf("decode docker compose observation %q: %w", ref.AppName, err)
			}
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return ref, fmt.Errorf("local compose app %q has no observed containers", ref.AppName)
	}
	services := make([]string, 0, len(rows))
	for _, row := range rows {
		state, health := strings.ToLower(strings.TrimSpace(row.State)), strings.ToLower(strings.TrimSpace(row.Health))
		if state != "running" || health == "unhealthy" {
			return ref, fmt.Errorf("local compose app %q service %q observed state=%q health=%q", ref.AppName, row.Service, state, health)
		}
		services = append(services, strings.TrimSpace(row.Service))
	}
	ref.ServiceNames = services
	ref.ObservedStatus = "running"
	ref.ObservedAt = time.Now().UTC()
	return ref, nil
}

func runLocalComposeCommand(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd.CombinedOutput()
}
