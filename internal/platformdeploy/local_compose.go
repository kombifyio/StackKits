package platformdeploy

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type localComposeRunner func(ctx context.Context, dir string, args ...string) ([]byte, error)

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

// LocalComposeAdapter deploys generated compose bundles directly on the node.
type LocalComposeAdapter struct {
	workDir string
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
	output, err := a.run(ctx, a.workDir, "compose", "-p", project, "-f", composePath, "up", "-d")
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
	}, nil
}

func runLocalComposeCommand(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}
