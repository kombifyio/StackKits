package opentofu

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

const (
	workloadObservationSchemaVersion = "stackkit.opentofu-workload-apply-observation/v1"
	workloadResourcePrefix           = "workload"

	// NativeModulesEnv is the explicit per-module opt-in of the ADR-0045
	// Stage 2 pilot: a comma-separated list of workload module refs whose
	// roots render native Docker-provider resources instead of the Stage 1
	// Compose wrapper. Unset, every root stays the wrapper. An entry without
	// a native renderer, or an opted-in module under the compose target,
	// fails closed; nothing falls back silently.
	NativeModulesEnv = "STACKKIT_OPENTOFU_NATIVE_MODULES"
)

// WorkloadOperations realizes the standalone workload bundles (the ten
// selected-PaaS applications) through their OpenTofu wrapper root when the
// request's generation target is opentofu or terramate, and through the
// native Compose owner otherwise (S-F fallback). Under an OpenTofu target the
// native preparation materializes the Compose project and its private .env
// exactly as today; the executor renders main.tf around that Compose file
// with RenderComposePayloadOpenTofu and runs init, plan, and apply in
// .stackkit/runtime/applications/<project>/opentofu instead of the native
// `docker compose up`; the native completion and observation run unchanged.
type WorkloadOperations struct {
	native  nativehost.NativeWorkloadComposeOperations
	runtime Runtime
}

// NewWorkloadOperations wraps the native standalone Compose owner. The
// runtime's Native field is not used: workload verification is the native
// owner's ObserveWorkload.
func NewWorkloadOperations(native nativehost.NativeWorkloadComposeOperations, runtime Runtime) (*WorkloadOperations, error) {
	if native == nil || strings.TrimSpace(runtime.WorkspaceRoot) == "" {
		return nil, errors.New("OpenTofu workload operations require the native standalone Compose owner and a workspace")
	}
	return &WorkloadOperations{native: native, runtime: runtime}, nil
}

// ApplyWorkload applies one workload bundle.
func (o *WorkloadOperations) ApplyWorkload(ctx context.Context, deployment nativehost.SelectedPaaSWorkloadDeployment) (nativehost.SelectedPaaSApplyReceipt, error) {
	if o == nil || o.native == nil {
		return nativehost.SelectedPaaSApplyReceipt{}, errors.New("OpenTofu workload operations are not initialized")
	}
	nativeDocker, err := nativeDockerOptIn(deployment.ModuleRef)
	if err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	if !nativehost.GenerationTargetExecutesOpenTofu(deployment.GenerationTarget) {
		if nativeDocker {
			return nativehost.SelectedPaaSApplyReceipt{}, fmt.Errorf("%s selects %s, which needs the opentofu or terramate generation target", NativeModulesEnv, deployment.ModuleRef)
		}
		return o.native.ApplyWorkload(ctx, deployment)
	}
	if ctx == nil {
		return nativehost.SelectedPaaSApplyReceipt{}, errors.New("OpenTofu workload operations require a context")
	}
	binary, providers, err := o.runtime.packagedTools()
	if err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	workspace, err := filepath.Abs(o.runtime.WorkspaceRoot)
	if err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, fmt.Errorf("resolve workspace: %w", err)
	}
	expectedProject := "stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef
	relative, err := WorkloadRootRelativePath(expectedProject)
	if err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	if err := o.runtime.requireStateCustody(ctx, relative, binary, providers.Directory()); err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	execution := ""
	if nativeDocker {
		execution = RootExecutionNativeDocker
	}
	// The native preparation passes only a fixed locale to Docker Compose;
	// the wrapper's local-exec adds the native project name.
	environment := []string{"LANG=C", "LC_ALL=C", "COMPOSE_PROJECT_NAME=" + expectedProject}
	if err := o.leaveOtherExecution(ctx, workspace, relative, binary, execution, environment); err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	prepared, err := o.native.PrepareWorkloadCompose(ctx, deployment)
	if err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	if prepared.ProjectName != expectedProject ||
		prepared.EnvFile != EnvFile ||
		filepath.Join(workspace, filepath.FromSlash(relative)) != filepath.Join(prepared.Directory, RootDirName) {
		return nativehost.SelectedPaaSApplyReceipt{}, errors.New("native workload project is not the stack graph runtime root")
	}
	var config []byte
	var parityGaps []string
	if nativeDocker {
		nativeRoot, err := RenderNativeWorkloadRoot(deployment.ModuleRef, prepared)
		if err != nil {
			return nativehost.SelectedPaaSApplyReceipt{}, err
		}
		if err := writeNativeSecretEnvFiles(prepared, nativeRoot.SecretEnvFiles); err != nil {
			return nativehost.SelectedPaaSApplyReceipt{}, err
		}
		config, parityGaps = nativeRoot.Config, nativeRoot.ParityGaps
	} else if config, err = RenderWorkloadRoot(prepared); err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	lock, err := providers.LockForConfiguration(config)
	if err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	root, err := ensureRootDir(workspace, relative)
	if err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	if err := writeRoot(root, RootMarker{
		SchemaVersion: RootMarkerSchemaVersion, ModuleRef: deployment.ModuleRef, InstanceRef: deployment.InstanceRef,
		RuntimeDir: prepared.ProjectName, ComposeProject: prepared.ProjectName, Kind: RootKindWorkload,
		Execution: execution,
	}, providers.Directory(), lock, rootFile{ConfigFile, config, 0o640}); err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	record := workloadObservation{
		SchemaVersion: workloadObservationSchemaVersion, ModuleRef: deployment.ModuleRef,
		InstanceRef: deployment.InstanceRef, ArtifactID: deployment.ArtifactRef, ArtifactDigest: deployment.ArtifactDigest,
		Root: relative, ComposeProject: prepared.ProjectName, ComposeSHA256: digestBytes(prepared.Compose),
		Execution: execution, ParityGaps: parityGaps,
	}
	if record.tofuRun, err = o.runtime.runRoot(ctx, relative, root, binary, environment); err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	applied, err := os.ReadFile(filepath.Join(prepared.Directory, ComposeFile))
	if err != nil || !bytes.Equal(applied, prepared.Compose) {
		return nativehost.SelectedPaaSApplyReceipt{}, errors.New("OpenTofu-applied workload Compose file differs from the native project")
	}
	if err := o.native.CompleteWorkloadCompose(ctx, prepared); err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	if _, err := writeObservation(root, record); err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	return nativehost.SelectedPaaSApplyReceipt{
		InstanceRef: deployment.InstanceRef, ArtifactDigest: deployment.ArtifactDigest, Status: "applied",
	}, nil
}

// ObserveWorkload is the native readback under every target.
func (o *WorkloadOperations) ObserveWorkload(ctx context.Context, deployment nativehost.SelectedPaaSWorkloadDeployment) (nativehost.SelectedPaaSWorkloadObservation, error) {
	if o == nil || o.native == nil {
		return nativehost.SelectedPaaSWorkloadObservation{}, errors.New("OpenTofu workload operations are not initialized")
	}
	return o.native.ObserveWorkload(ctx, deployment)
}

// ValidateWorkloadObservation keeps the native semantic readback contract.
func (o *WorkloadOperations) ValidateWorkloadObservation(deployment nativehost.SelectedPaaSWorkloadDeployment, observation nativehost.SelectedPaaSWorkloadObservation) error {
	if o == nil || o.native == nil {
		return errors.New("OpenTofu workload operations are not initialized")
	}
	return o.native.ValidateWorkloadObservation(deployment, observation)
}

// RenderWorkloadRoot renders the wrapper root of one prepared workload
// project: the byte-identical Compose payload, the native project name, and
// a reference to the private .env (never its content).
func RenderWorkloadRoot(prepared nativehost.NativeWorkloadCompose) ([]byte, error) {
	return architecturev2renderer.RenderComposePayloadOpenTofu(architecturev2renderer.ComposePayloadSpec{
		ResourcePrefix: workloadResourcePrefix, ProjectName: prepared.ProjectName,
		Compose: prepared.Compose, EnvFile: prepared.EnvFile, NoWait: !prepared.Wait,
	})
}

// RenderNativeWorkloadRoot renders the ADR-0045 Stage 2 pilot root of one
// prepared workload project: native Docker-provider resources for exactly
// the containers of its Compose payload, secrets mounted from owner-only
// files beside it.
func RenderNativeWorkloadRoot(moduleRef string, prepared nativehost.NativeWorkloadCompose) (architecturev2renderer.NativeDockerRoot, error) {
	return architecturev2renderer.RenderNativeDockerOpenTofu(architecturev2renderer.NativeDockerSpec{
		ModuleRef: moduleRef, ProjectName: prepared.ProjectName, Compose: prepared.Compose, Wait: prepared.Wait,
	})
}

// nativeDockerOptIn reports whether the operator opted moduleRef into the
// native pilot. Every entry must name a module with a native renderer.
func nativeDockerOptIn(moduleRef string) (bool, error) {
	selected := false
	for _, entry := range strings.Split(os.Getenv(NativeModulesEnv), ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if !architecturev2renderer.NativeDockerPilotModule(entry) {
			return false, fmt.Errorf("%s names %q, which has no native Docker rendering", NativeModulesEnv, entry)
		}
		selected = selected || entry == moduleRef
	}
	return selected, nil
}

// leaveOtherExecution destroys an existing root of the other execution
// (Stage 1 wrapper or native) before the new one is written, so the two
// never own the same containers at once. Destroying the wrapper runs
// `docker compose down`; destroying a native root forgets its volumes.
// Either way the data volumes stay.
func (o *WorkloadOperations) leaveOtherExecution(ctx context.Context, workspace, relative, binary, execution string, environment []string) error {
	root := filepath.Join(workspace, filepath.FromSlash(relative))
	if _, err := os.Lstat(filepath.Join(root, MarkerFile)); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	marker, err := ReadRootMarker(root)
	if err != nil {
		return err
	}
	if marker.Execution == execution {
		return nil
	}
	if _, err := os.Lstat(filepath.Join(root, StateFile)); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if _, err := o.runtime.runRootDestroy(ctx, relative, root, binary, environment); err != nil {
		return fmt.Errorf("leave the previous %q execution of %s: %w", marker.Execution, relative, err)
	}
	return nil
}

// writeNativeSecretEnvFiles writes each container's secret env file from the
// prepared private .env into the owner-only secrets directory of the project.
func writeNativeSecretEnvFiles(prepared nativehost.NativeWorkloadCompose, files []architecturev2renderer.NativeDockerSecretEnvFile) error {
	if len(files) == 0 {
		return nil
	}
	dotenv, err := os.ReadFile(filepath.Join(prepared.Directory, prepared.EnvFile))
	if err != nil {
		return fmt.Errorf("read the private workload .env: %w", err)
	}
	defer clear(dotenv)
	directory := filepath.Join(prepared.Directory, architecturev2renderer.NativeDockerSecretDir)
	if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create the native secret directory: %w", err)
	}
	if info, err := os.Lstat(directory); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("native secret directory is not a plain directory")
	}
	if err := os.Chmod(directory, 0o700); err != nil { //nolint:gosec // owner-only directory; it needs the search bit
		return fmt.Errorf("restrict the native secret directory: %w", err)
	}
	for _, file := range files {
		content, err := architecturev2renderer.RenderNativeDockerSecretEnvFile(file, dotenv)
		if err != nil {
			return err
		}
		err = writeFileAtomic(directory, filepath.Base(file.RelPath), content, 0o600)
		clear(content)
		if err != nil {
			return err
		}
	}
	return nil
}

type workloadObservation struct {
	SchemaVersion  string `json:"schemaVersion"`
	ModuleRef      string `json:"moduleRef"`
	InstanceRef    string `json:"instanceRef"`
	ArtifactID     string `json:"artifactId"`
	ArtifactDigest string `json:"artifactDigest"`
	Root           string `json:"root"`
	ComposeProject string `json:"composeProject"`
	ComposeSHA256  string `json:"composeSha256"`
	// Execution and ParityGaps record a native pilot root and the governed
	// Compose settings its provider could not express.
	Execution  string   `json:"execution,omitempty"`
	ParityGaps []string `json:"parityGaps,omitempty"`
	tofuRun
}

var _ nativehost.SelectedPaaSWorkloadOperations = (*WorkloadOperations)(nil)
var _ nativehost.SelectedPaaSWorkloadObservationValidator = (*WorkloadOperations)(nil)
