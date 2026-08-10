package runtimeexecutorlocal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/servicecontrol"
)

type osCloudCoreOperations struct {
	workspaceRoot string
	runner        basementCoreProcessRunner
	prober        basementCoreProber
}

func NewOSCloudCoreOperations(workspaceRoot string) (CloudCoreOperations, error) {
	absolute, err := filepath.Abs(workspaceRoot)
	if err != nil || strings.TrimSpace(workspaceRoot) == "" {
		return nil, errors.New("Cloud core operations require an absolute workspace root")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Cloud core operations require an existing plain workspace directory")
	}
	return &osCloudCoreOperations{workspaceRoot: filepath.Clean(absolute), runner: osCloudCoreProcessRunner{}, prober: osBasementCoreProber{}}, nil
}

func (o *osCloudCoreOperations) ApplyProject(ctx context.Context, project CloudCoreProject) (CloudCoreApplyObservation, error) {
	if err := o.ready(ctx); err != nil {
		return CloudCoreApplyObservation{}, err
	}
	if _, err := localevidence.LoadCloudRuntimeCustody(o.workspaceRoot); err != nil {
		return CloudCoreApplyObservation{}, fmt.Errorf("verify Cloud runtime custody before Apply: %w", err)
	}
	composePath, err := o.persistCompose(project)
	if err != nil {
		return CloudCoreApplyObservation{}, err
	}
	if _, err := o.runner.Run(ctx, cloudCoreComposeArgs(composePath, "up"), filepath.Dir(composePath), o.environment()); err != nil {
		return CloudCoreApplyObservation{}, errors.New("Cloud Docker Compose Apply did not complete")
	}
	controller, err := servicecontrol.NewOSController(o.workspaceRoot)
	if err != nil {
		return CloudCoreApplyObservation{}, fmt.Errorf("initialize durable service reconciliation: %w", err)
	}
	if err := controller.ReconcileAfterApply(ctx); err != nil {
		return CloudCoreApplyObservation{}, fmt.Errorf("reconcile durable service desired state: %w", err)
	}
	return CloudCoreApplyObservation{ProjectRef: project.ProjectRef, ArtifactDigest: project.ArtifactDigest, Status: "applied"}, nil
}

func (o *osCloudCoreOperations) VerifyProject(ctx context.Context, project CloudCoreProject) (CloudCoreVerifyObservation, error) {
	if err := o.ready(ctx); err != nil {
		return CloudCoreVerifyObservation{}, err
	}
	if _, err := localevidence.LoadCloudRuntimeCustody(o.workspaceRoot); err != nil {
		return CloudCoreVerifyObservation{}, fmt.Errorf("verify Cloud runtime custody before observation: %w", err)
	}
	composePath := filepath.Join(o.workspaceRoot, ".stackkit", "runtime", "cloud-core", "compose.yaml")
	content, err := readStablePrivateBasementRuntimeFile(o.workspaceRoot, composePath)
	if err != nil {
		return CloudCoreVerifyObservation{}, fmt.Errorf("verify Cloud core Compose authority: %w", err)
	}
	if !bytes.Equal(content, project.Definition) {
		return CloudCoreVerifyObservation{}, errors.New("verified Cloud runtime differs from the authorized Compose project")
	}
	raw, err := o.runner.Run(ctx, cloudCoreComposeArgs(composePath, "ps"), filepath.Dir(composePath), o.environment())
	if err != nil {
		return CloudCoreVerifyObservation{}, errors.New("verified Cloud runtime status is unavailable")
	}
	services, err := parseBasementCoreComposeStatus(raw, project.Services)
	if err != nil {
		return CloudCoreVerifyObservation{}, errors.New("verified Cloud runtime service set differs from the authorized project")
	}
	probes := make([]BasementCoreProbeObservation, 0, len(project.Health))
	for _, expectation := range project.Health {
		if err := o.prober.Probe(ctx, expectation); err != nil {
			return CloudCoreVerifyObservation{}, errors.New("verified Cloud runtime health differs from the authorized project")
		}
		probes = append(probes, BasementCoreProbeObservation{RequirementID: expectation.RequirementID, Status: "healthy"})
	}
	return CloudCoreVerifyObservation{ProjectRef: project.ProjectRef, ArtifactDigest: project.ArtifactDigest, Status: "ready", Services: services, Probes: probes}, nil
}

func (o *osCloudCoreOperations) ready(ctx context.Context) error {
	if ctx == nil {
		return errors.New("Cloud core operations require a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if o == nil || o.workspaceRoot == "" || o.runner == nil || o.prober == nil {
		return errors.New("Cloud core operations are not initialized")
	}
	return nil
}

func (o *osCloudCoreOperations) persistCompose(project CloudCoreProject) (string, error) {
	runtimeDir := filepath.Join(o.workspaceRoot, ".stackkit", "runtime", "cloud-core")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return "", fmt.Errorf("create private Cloud runtime directory: %w", err)
	}
	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		return "", err
	}
	root, err := confinedfs.Open(runtimeDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return "", err
	}
	result, err := view.WriteAtomic0600("compose.yaml", project.Definition)
	if err != nil || !result.Installed || !result.FileSynced {
		return "", errors.New("atomically persist Cloud core Compose definition")
	}
	target := filepath.Join(runtimeDir, "compose.yaml")
	if err := restrictBasementRuntimeFile(target); err != nil {
		return "", err
	}
	return target, nil
}

func (o *osCloudCoreOperations) environment() []string {
	return []string{"LANG=C", "LC_ALL=C", "STACKKIT_CUSTODY_DIR=" + filepath.Join(o.workspaceRoot, ".stackkit", "custody")}
}

func cloudCoreComposeArgs(composePath, operation string) []string {
	prefix := []string{"compose", "--project-name", "stackkit-cloud-core", "-f", composePath}
	if operation == "up" {
		return append(prefix, "up", "-d", "--wait", "--wait-timeout", "600")
	}
	return append(prefix, "ps", "--all", "--format", "json")
}

type osCloudCoreProcessRunner struct{}

func (osCloudCoreProcessRunner) Run(ctx context.Context, args []string, directory string, environment []string) ([]byte, error) {
	if len(args) < 6 || args[0] != "compose" || args[1] != "--project-name" || args[2] != "stackkit-cloud-core" ||
		args[3] != "-f" || filepath.Clean(args[4]) != args[4] || filepath.Base(args[4]) != "compose.yaml" || filepath.Dir(args[4]) != directory ||
		(!slices.Equal(args[5:], []string{"up", "-d", "--wait", "--wait-timeout", "600"}) && !slices.Equal(args[5:], []string{"ps", "--all", "--format", "json"})) {
		return nil, errors.New("Cloud core process runner rejected an unbounded command")
	}
	command := exec.CommandContext(ctx, "docker", args...) //nolint:gosec // exact finite arguments validated above
	command.Dir = directory
	command.Env = append([]string(nil), environment...)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, &basementCoreProcessError{output: boundedBasementCoreProcessDiagnostic(output), cause: err}
	}
	return output, nil
}

var (
	_ CloudCoreOperations       = (*osCloudCoreOperations)(nil)
	_ basementCoreProcessRunner = osCloudCoreProcessRunner{}
)
