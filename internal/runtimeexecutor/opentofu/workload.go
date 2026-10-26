package opentofu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
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
func (o *WorkloadOperations) ApplyWorkload(ctx context.Context, deployment nativehost.SelectedPaaSWorkloadDeployment) (_ nativehost.SelectedPaaSApplyReceipt, returnErr error) {
	if o == nil || o.native == nil {
		return nativehost.SelectedPaaSApplyReceipt{}, errors.New("OpenTofu workload operations are not initialized")
	}
	// This check also precedes the direct Compose generation target, whose
	// native Apply would otherwise rewrite files before seeing any Tofu root.
	project := "stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef
	guardRelative, err := WorkloadRootRelativePath(project)
	if err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	// Direct Apply owns this project only. In a workspace operation, borrow
	// its complete fixed batch and recheck every member before any mutation.
	ctx, release, err := admitGenericOperation(ctx, o.runtime.WorkspaceRoot, []string{guardRelative}, false)
	if err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, fmt.Errorf("workload ownership requires an explicit governed manager transition or authenticated root preparation: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, release()) }()
	admission := ctx.Value(genericAdmissionContextKey{}).(*GenericRestoreAdmission)
	fs, tx := admission.fs, admission.transaction
	if err := refusePlatformProviderTransition(o.runtime.WorkspaceRoot, guardRelative); err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	nativeDocker, err := nativeDockerOptIn(deployment.ModuleRef)
	if err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	if !nativehost.GenerationTargetExecutesOpenTofu(deployment.GenerationTarget) {
		if nativeDocker {
			return nativehost.SelectedPaaSApplyReceipt{}, fmt.Errorf("%s selects %s, which needs the opentofu or terramate generation target", NativeModulesEnv, deployment.ModuleRef)
		}
		if err := handoffPreparedWorkload(fs, tx, guardRelative, deployment); err != nil {
			return nativehost.SelectedPaaSApplyReceipt{}, err
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
	if err := handoffPreparedWorkload(fs, tx, relative, deployment); err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
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
		if err := writeNativeSecretFiles(prepared, nativeRoot.SecretFiles); err != nil {
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

// handoffPreparedWorkload moves authenticated preparation into the current
// ordinary executor's ownership before any project mutation or old-engine
// Destroy. The incoming deployment is checked against its actual bundle; the
// marker describes the existing configuration until writeRoot installs the new
// execution. A failed Apply therefore retries as an owned executor, without
// relaxing prepared-file digests or manufacturing a historical checkpoint marker.
func handoffPreparedWorkload(fs *confinedfs.Root, tx *confinedfs.Transaction, root string, d nativehost.SelectedPaaSWorkloadDeployment) error {
	exists, _, err := tx.Exists(root)
	if err != nil {
		return err
	}
	if !exists && !nativehost.GenerationTargetExecutesOpenTofu(d.GenerationTarget) {
		return nil
	}
	marked, _, err := tx.Exists(path.Join(root, MarkerFile))
	if err != nil {
		return err
	}
	if marked {
		prepared, _, err := tx.Exists(path.Join(root, RootPreparationFile))
		if err != nil {
			return err
		}
		// A previous publication can report an installed marker and a failed
		// flush. Retry must prove durability before using that handoff, too.
		if prepared {
			return tx.CheckSiblingStaging(root, true)
		}
		return nil
	}
	if exists {
		if _, err := readRootPreparation(tx, root); err != nil {
			return err
		}
	}
	bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(d.Bundle)
	if err != nil {
		return fmt.Errorf("authenticate executor ownership handoff: %w", err)
	}
	if bundle.WorkloadRef != d.WorkloadRef || bundle.ModuleRef != d.ModuleRef || bundle.Release != d.Release ||
		bundle.SiteRef != d.SiteRef || bundle.NodeRef != d.NodeRef || bundle.InstanceRef != d.InstanceRef ||
		d.ArtifactRef == "" || d.UnitRef == "" || digestBytes(d.Bundle) != d.ArtifactDigest {
		return errors.New("executor ownership handoff differs from the exact authorized workload deployment")
	}
	if err := tx.CheckSiblingStaging(root, true); err != nil {
		return err
	}
	execution := ""
	config, _, err := tx.ReadStableBounded(path.Join(root, ConfigFile), maxOpenTofuStateBytes)
	if err == nil {
		native, err := architecturev2renderer.NativeDockerRootConfig(config)
		clear(config)
		if err != nil {
			return err
		}
		if native {
			execution = RootExecutionNativeDocker
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	project := "stackkit-" + d.WorkloadRef + "-" + d.NodeRef
	marker := RootMarker{SchemaVersion: RootMarkerSchemaVersion, ModuleRef: d.ModuleRef, InstanceRef: d.InstanceRef,
		RuntimeDir: project, ComposeProject: project, Kind: RootKindWorkload, Execution: execution}
	if !exists {
		return publishOrdinaryRoot(fs, tx, root, marker, d.ArtifactDigest)
	}
	encoded, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	view, err := fs.View(".")
	if err != nil {
		return err
	}
	return writePreparedRootFile(tx, view, path.Dir(root), path.Join(root, MarkerFile), append(encoded, '\n'), true)
}

// ObserveWorkload is the native readback under every target.
func (o *WorkloadOperations) ObserveWorkload(ctx context.Context, deployment nativehost.SelectedPaaSWorkloadDeployment) (_ nativehost.SelectedPaaSWorkloadObservation, returnErr error) {
	if o == nil || o.native == nil {
		return nativehost.SelectedPaaSWorkloadObservation{}, errors.New("OpenTofu workload operations are not initialized")
	}
	root, err := WorkloadRootRelativePath("stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef)
	if err != nil {
		return nativehost.SelectedPaaSWorkloadObservation{}, err
	}
	ctx, release, err := admitGenericOperation(ctx, o.runtime.WorkspaceRoot, []string{root}, false)
	if err != nil {
		return nativehost.SelectedPaaSWorkloadObservation{}, err
	}
	defer func() { returnErr = errors.Join(returnErr, release()) }()
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
	return architecturev2renderer.RenderNativeDockerOpenTofu(nativeWorkloadSpec(moduleRef, prepared, false))
}

func nativeWorkloadSpec(moduleRef string, prepared nativehost.NativeWorkloadCompose, pilotAliases bool) architecturev2renderer.NativeDockerSpec {
	return architecturev2renderer.NativeDockerSpec{
		ModuleRef: moduleRef, ProjectName: prepared.ProjectName, Compose: prepared.Compose, Wait: prepared.Wait,
		SecretExecBinary: SecretExecBinary(), PilotNetworkAliases: pilotAliases,
	}
}

// SecretExecBinaryEnv overrides the host path of the governed entrypoint
// shim binary (internal/secretexec).
const SecretExecBinaryEnv = "STACKKIT_SECRET_EXEC_BINARY"

// SecretExecBinary is the host path a native root mounts as the governed
// entrypoint shim: SecretExecBinaryEnv, else the running stackkit binary,
// else a stackkit binary beside the running executable (stackkit-server).
// It is empty when none exists; a root that needs the shim then fails
// closed in the renderer.
func SecretExecBinary() string {
	if path := os.Getenv(SecretExecBinaryEnv); path != "" {
		return path
	}
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	if executable, err = filepath.EvalSymlinks(executable); err != nil {
		return ""
	}
	if filepath.Base(executable) != "stackkit" {
		executable = filepath.Join(filepath.Dir(executable), "stackkit")
	}
	if info, err := os.Stat(executable); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return executable
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
// Either way the data volumes stay. ApplyWorkload holds the project lock
// across this check and any Destroy, so provider adoption cannot race it.
func (o *WorkloadOperations) leaveOtherExecution(ctx context.Context, workspace, relative, binary, execution string, environment []string) error {
	root := filepath.Join(workspace, filepath.FromSlash(relative))
	if err := refusePlatformProviderTransition(workspace, relative); err != nil {
		return err
	}
	marker, err := ReadRootMarker(root)
	if errors.Is(err, os.ErrNotExist) {
		// A signed scaffold owns no running engine. A recovered root owns its
		// authenticated generic configuration, not an invented runtime marker.
		config, readErr := os.ReadFile(filepath.Join(root, ConfigFile))
		if errors.Is(readErr, os.ErrNotExist) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
		native, parseErr := architecturev2renderer.NativeDockerRootConfig(config)
		if parseErr != nil {
			return parseErr
		}
		if native {
			marker.Execution = RootExecutionNativeDocker
		}
	} else if err != nil {
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

func refusePlatformProviderTransition(workspace, relative string) error {
	if err := RequireGenericRestoreAdmission(workspace, []string{relative}); err != nil {
		return fmt.Errorf("workload ownership requires an explicit governed manager transition or authenticated root preparation: %w", err)
	}
	return nil
}

// writeNativeSecretFiles writes each secret file of a native root from the
// prepared private .env into the owner-only secrets directory of the
// project, with the mode its reader needs (NativeDockerSecretFile.Mode).
func writeNativeSecretFiles(prepared nativehost.NativeWorkloadCompose, files []architecturev2renderer.NativeDockerSecretFile) error {
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
		content, err := architecturev2renderer.RenderNativeDockerSecretFile(file, dotenv)
		if err != nil {
			return err
		}
		err = writeFileAtomic(directory, filepath.Base(file.RelPath), content, file.Mode())
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
