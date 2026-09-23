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
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localowner"
)

type osCloudCoreOperations struct {
	workspaceRoot string
	runtimeName   string
	runner        basementCoreProcessRunner
	prober        basementCoreProber
	ownerIdentity basementOwnerIdentity
}

func NewOSCloudCoreOperations(workspaceRoot string) (CloudCoreOperations, error) {
	return newOSCloudCoreOperations(workspaceRoot, "cloud-core")
}

// NewOSCloudStandaloneCoreOperations uses the same lifecycle with separate
// project and artifact custody; it never adopts an existing PaaS core.
func NewOSCloudStandaloneCoreOperations(workspaceRoot string) (CloudCoreOperations, error) {
	return newOSCloudCoreOperations(workspaceRoot, "cloud-core-standalone")
}

func newOSCloudCoreOperations(workspaceRoot, runtimeName string) (CloudCoreOperations, error) {
	absolute, err := filepath.Abs(workspaceRoot)
	if err != nil || strings.TrimSpace(workspaceRoot) == "" {
		return nil, errors.New("Cloud core operations require an absolute workspace root")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Cloud core operations require an existing plain workspace directory")
	}
	ownerIdentity, err := localowner.NewService(absolute)
	if err != nil {
		return nil, err
	}
	return &osCloudCoreOperations{
		workspaceRoot: filepath.Clean(absolute), runtimeName: runtimeName,
		runner: osCloudCoreProcessRunner{}, prober: osBasementCoreProber{},
		ownerIdentity: localBasementOwnerIdentity{service: ownerIdentity},
	}, nil
}

func (o *osCloudCoreOperations) ApplyProject(ctx context.Context, project CloudCoreProject) (CloudCoreApplyObservation, error) {
	if err := o.ready(ctx); err != nil {
		return CloudCoreApplyObservation{}, err
	}
	if err := o.validateModule(project); err != nil {
		return CloudCoreApplyObservation{}, err
	}
	if _, err := localevidence.LoadCloudRuntimeCustody(o.workspaceRoot); err != nil {
		return CloudCoreApplyObservation{}, fmt.Errorf("verify Cloud runtime custody before Apply: %w", err)
	}
	composePath, err := o.persistCompose(project)
	if err != nil {
		return CloudCoreApplyObservation{}, err
	}
	if _, err := o.runner.Run(ctx, o.composeArgs(composePath, "up"), filepath.Dir(composePath), o.environment()); err != nil {
		return CloudCoreApplyObservation{}, fmt.Errorf("Cloud Docker Compose Apply did not complete: %w", err)
	}
	if err := o.waitUntilReady(ctx, composePath, project); err != nil {
		return CloudCoreApplyObservation{}, err
	}
	binding, err := o.ownerIdentity.Realize(ctx)
	if err != nil {
		return CloudCoreApplyObservation{}, fmt.Errorf("Cloud PocketID owner realization did not complete: %w", err)
	}
	// Owner realization registers TinyAuth's PocketID client and installs its
	// secret as a private optional env override. Reconcile Compose once more so
	// TinyAuth runs with that bound credential before Apply can succeed.
	if _, err := o.runner.Run(ctx, o.composeArgs(composePath, "up"), filepath.Dir(composePath), o.environment()); err != nil {
		return CloudCoreApplyObservation{}, fmt.Errorf("Cloud TinyAuth PocketID binding did not complete: %w", err)
	}
	if err := o.waitUntilReady(ctx, composePath, project); err != nil {
		return CloudCoreApplyObservation{}, err
	}
	return CloudCoreApplyObservation{
		ProjectRef: project.ProjectRef, ArtifactDigest: project.ArtifactDigest, Status: "applied",
		OwnerRef: binding.OwnerRef, PocketIDSubject: binding.PocketIDSubject,
		OwnerBindingDigest: localevidence.OwnerRuntimeBindingDigest(binding),
	}, nil
}

func (o *osCloudCoreOperations) waitUntilReady(ctx context.Context, composePath string, project CloudCoreProject) error {
	readinessCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	lastPending := []string{"runtime-status"}
	for {
		pending, err := o.pendingReadiness(readinessCtx, composePath, project)
		if err != nil {
			if parentErr := ctx.Err(); parentErr != nil {
				return parentErr
			}
			if readinessCtx.Err() != nil {
				return fmt.Errorf("Cloud core readiness did not complete: %s", strings.Join(lastPending, ","))
			}
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		lastPending = pending
		select {
		case <-readinessCtx.Done():
			if err := ctx.Err(); err != nil {
				return err
			}
			return fmt.Errorf("Cloud core readiness did not complete: %s", strings.Join(lastPending, ","))
		case <-time.After(5 * time.Second):
		}
	}
}

func (o *osCloudCoreOperations) pendingReadiness(ctx context.Context, composePath string, project CloudCoreProject) ([]string, error) {
	raw, err := o.runner.Run(ctx, o.composeArgs(composePath, "ps"), filepath.Dir(composePath), o.environment())
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return []string{"runtime-status"}, nil
	}
	_, pending, err := inspectBasementCoreComposeStatus(raw, project.Services)
	if err != nil {
		return nil, fmt.Errorf("Cloud core readiness differs from the authorized project: %w", err)
	}
	if len(pending) != 0 {
		sort.Strings(pending)
		return pending, nil
	}
	for _, expectation := range project.Health {
		attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := o.prober.Probe(attemptCtx, expectation)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			pending = append(pending, "probe:"+expectation.RequirementID)
		}
	}
	sort.Strings(pending)
	return pending, nil
}

func (o *osCloudCoreOperations) VerifyProject(ctx context.Context, project CloudCoreProject) (CloudCoreVerifyObservation, error) {
	if err := o.ready(ctx); err != nil {
		return CloudCoreVerifyObservation{}, err
	}
	if err := o.validateModule(project); err != nil {
		return CloudCoreVerifyObservation{}, err
	}
	if _, err := localevidence.LoadCloudRuntimeCustody(o.workspaceRoot); err != nil {
		return CloudCoreVerifyObservation{}, fmt.Errorf("verify Cloud runtime custody before observation: %w", err)
	}
	composePath := filepath.Join(o.workspaceRoot, ".stackkit", "runtime", o.name(), "compose.yaml")
	content, err := readStablePrivateBasementRuntimeFile(o.workspaceRoot, composePath)
	if err != nil {
		return CloudCoreVerifyObservation{}, fmt.Errorf("verify Cloud core Compose authority: %w", err)
	}
	if !bytes.Equal(content, project.Definition) {
		return CloudCoreVerifyObservation{}, errors.New("verified Cloud runtime differs from the authorized Compose project")
	}
	raw, err := o.runner.Run(ctx, o.composeArgs(composePath, "ps"), filepath.Dir(composePath), o.environment())
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
	binding, err := o.ownerIdentity.Verify(ctx)
	if err != nil {
		return CloudCoreVerifyObservation{}, fmt.Errorf("Cloud PocketID owner verification did not complete: %w", err)
	}
	return CloudCoreVerifyObservation{
		ProjectRef: project.ProjectRef, ArtifactDigest: project.ArtifactDigest, Status: "ready", Services: services, Probes: probes,
		OwnerRef: binding.OwnerRef, PocketIDSubject: binding.PocketIDSubject,
		OwnerBindingDigest: localevidence.OwnerRuntimeBindingDigest(binding),
	}, nil
}

func (o *osCloudCoreOperations) ready(ctx context.Context) error {
	if ctx == nil {
		return errors.New("Cloud core operations require a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if o == nil || o.workspaceRoot == "" || o.runner == nil || o.prober == nil || o.ownerIdentity == nil {
		return errors.New("Cloud core operations are not initialized")
	}
	if o.name() != "cloud-core" && o.name() != "cloud-core-standalone" {
		return errors.New("Cloud core operations have an unknown custody profile")
	}
	return nil
}

func (o *osCloudCoreOperations) persistCompose(project CloudCoreProject) (string, error) {
	other := "cloud-core"
	if o.name() == other {
		other = "cloud-core-standalone"
	}
	if _, err := os.Lstat(filepath.Join(o.workspaceRoot, ".stackkit", "runtime", other, "compose.yaml")); err == nil {
		return "", errors.New("Cloud core transition requires explicit reconciliation of the existing core custody")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect existing Cloud core custody: %w", err)
	}
	runtimeDir := filepath.Join(o.workspaceRoot, ".stackkit", "runtime", o.name())
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
	return (&osCloudCoreOperations{}).composeArgs(composePath, operation)
}

func (o *osCloudCoreOperations) name() string {
	if o.runtimeName == "" {
		return "cloud-core"
	}
	return o.runtimeName
}

func (o *osCloudCoreOperations) validateModule(project CloudCoreProject) error {
	expected := cloudCoreModuleRef
	if o.name() == "cloud-core-standalone" {
		expected = cloudStandaloneCoreModuleRef
	}
	if project.ModuleRef != expected {
		return errors.New("Cloud operations profile differs from the authorized module")
	}
	return nil
}

func (o *osCloudCoreOperations) composeArgs(composePath, operation string) []string {
	prefix := []string{"compose", "--project-name", "stackkit-" + o.name(), "-f", composePath}
	if operation == "up" {
		return append(prefix, "up", "-d")
	}
	return append(prefix, "ps", "--all", "--format", "json")
}

type osCloudCoreProcessRunner struct{}

func (osCloudCoreProcessRunner) Run(ctx context.Context, args []string, directory string, environment []string) ([]byte, error) {
	if len(args) < 6 || args[0] != "compose" || args[1] != "--project-name" || (args[2] != "stackkit-cloud-core" && args[2] != "stackkit-cloud-core-standalone") ||
		args[3] != "-f" || filepath.Clean(args[4]) != args[4] || filepath.Base(args[4]) != "compose.yaml" || filepath.Dir(args[4]) != directory ||
		(!slices.Equal(args[5:], []string{"up", "-d"}) && !slices.Equal(args[5:], []string{"ps", "--all", "--format", "json"})) {
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
