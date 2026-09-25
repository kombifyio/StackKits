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
	if !nativehost.GenerationTargetExecutesOpenTofu(deployment.GenerationTarget) {
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
	prepared, err := o.native.PrepareWorkloadCompose(ctx, deployment)
	if err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	relative, err := WorkloadRootRelativePath(prepared.ProjectName)
	if err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	if prepared.ProjectName != "stackkit-"+deployment.WorkloadRef+"-"+deployment.NodeRef ||
		prepared.EnvFile != EnvFile ||
		filepath.Join(workspace, filepath.FromSlash(relative)) != filepath.Join(prepared.Directory, RootDirName) {
		return nativehost.SelectedPaaSApplyReceipt{}, errors.New("native workload project is not the stack graph runtime root")
	}
	config, err := RenderWorkloadRoot(prepared)
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
	}, providers, rootFile{ConfigFile, config, 0o640}); err != nil {
		return nativehost.SelectedPaaSApplyReceipt{}, err
	}
	record := workloadObservation{
		SchemaVersion: workloadObservationSchemaVersion, ModuleRef: deployment.ModuleRef,
		InstanceRef: deployment.InstanceRef, ArtifactID: deployment.ArtifactRef, ArtifactDigest: deployment.ArtifactDigest,
		Root: relative, ComposeProject: prepared.ProjectName, ComposeSHA256: digestBytes(prepared.Compose),
	}
	// The native runner passes only a fixed locale to Docker Compose; the
	// wrapper's local-exec adds the native project name.
	environment := []string{"LANG=C", "LC_ALL=C", "COMPOSE_PROJECT_NAME=" + prepared.ProjectName}
	if record.tofuRun, err = o.runtime.runRoot(ctx, root, binary, environment); err != nil {
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

type workloadObservation struct {
	SchemaVersion  string `json:"schemaVersion"`
	ModuleRef      string `json:"moduleRef"`
	InstanceRef    string `json:"instanceRef"`
	ArtifactID     string `json:"artifactId"`
	ArtifactDigest string `json:"artifactDigest"`
	Root           string `json:"root"`
	ComposeProject string `json:"composeProject"`
	ComposeSHA256  string `json:"composeSha256"`
	tofuRun
}

var _ nativehost.SelectedPaaSWorkloadOperations = (*WorkloadOperations)(nil)
var _ nativehost.SelectedPaaSWorkloadObservationValidator = (*WorkloadOperations)(nil)
