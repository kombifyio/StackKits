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

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

// RuntimeRootRestore is one governed generation's requested file repair.
// WorkloadBundle selects workload rendering; otherwise CoreConfig is used.
type RuntimeRootRestore struct {
	RuntimeRoot    string
	CoreConfig     []byte
	WorkloadBundle []byte
}

type RestoredRootFile struct {
	RuntimeRoot string
	nativehost.RuntimeFileRestore
}

// RestoreRuntimeRootFiles admits the complete current/destination set before
// the first reconcile write, retaining all project leases through the batch.
func RestoreRuntimeRootFiles(ctx context.Context, workspace string, roots []RuntimeRootRestore) (_ []RestoredRootFile, returnErr error) {
	paths := make([]string, 0, len(roots))
	for _, root := range roots {
		paths = append(paths, root.RuntimeRoot)
	}
	admission, err := AcquireGenericRestoreAdmission(workspace, paths)
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, admission.Close()) }()
	return admission.RestoreRuntimeRootFiles(ctx, roots)
}

// RestoreRuntimeRootFiles reuses a caller's admission when selection itself
// reads destination custody. It cannot add destinations to the held lease.
func (a *GenericRestoreAdmission) RestoreRuntimeRootFiles(ctx context.Context, roots []RuntimeRootRestore) ([]RestoredRootFile, error) {
	paths := make([]string, 0, len(roots))
	for _, root := range roots {
		paths = append(paths, root.RuntimeRoot)
	}
	if err := a.requireRoots(paths); err != nil {
		return nil, err
	}
	var restored []RestoredRootFile
	for _, root := range roots {
		var files []nativehost.RuntimeFileRestore
		var err error
		if root.WorkloadBundle != nil {
			files, err = a.RestoreWorkloadRoot(ctx, root.RuntimeRoot, root.WorkloadBundle)
		} else {
			files, err = a.RestoreCoreRoot(root.RuntimeRoot, root.CoreConfig)
		}
		if err != nil {
			return restored, fmt.Errorf("restore runtime root %s: %w", root.RuntimeRoot, err)
		}
		for _, file := range files {
			restored = append(restored, RestoredRootFile{RuntimeRoot: root.RuntimeRoot, RuntimeFileRestore: file})
		}
	}
	return restored, nil
}

// Governed runtime file restore of an Advanced drift reconcile
// (docs/ARCHITECTURE.md "Advanced drift per stack (Stage 1)"). A drifted
// runtime file is what the reconcile repairs, yet the reconcile's mandatory
// rollback checkpoint refuses it: the checkpoint's Kopia snapshot quiesces
// every selected workload through a custody check that requires the runtime
// Compose files to equal the authorized render, and the executor-state
// capture binds every root to its governed bytes. The reconcile therefore
// rewrites the governed files of every stack it forces with the bytes Apply
// writes before it takes the checkpoint. Only files are written: no Compose
// or OpenTofu process runs and no data changes; the forced convergence
// afterwards restarts what drifted.

// RestoreWorkloadRoot restores the governed runtime files of one workload
// stack from its workload bundle: the Compose project files the native
// preparation writes (compose.yaml, .env, configuration files) and the
// root's main.tf that RenderWorkloadRoot renders around that Compose file.
// runtimeRoot is the stack's root; the bundle must be the workload of that
// root, which the executor has already materialized.
func RestoreWorkloadRoot(ctx context.Context, workspaceRoot, runtimeRoot string, bundle []byte) (_ []nativehost.RuntimeFileRestore, returnErr error) {
	admission, err := AcquireGenericRestoreAdmission(workspaceRoot, []string{runtimeRoot})
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, admission.Close()) }()
	return admission.RestoreWorkloadRoot(ctx, runtimeRoot, bundle)
}

// RestoreWorkloadRoot performs a nested repair without relocking the project.
func (a *GenericRestoreAdmission) RestoreWorkloadRoot(ctx context.Context, runtimeRoot string, bundle []byte) ([]nativehost.RuntimeFileRestore, error) {
	if err := a.requireRoots([]string{runtimeRoot}); err != nil {
		return nil, err
	}
	return restoreWorkloadRoot(ctx, a.transaction.Name(), runtimeRoot, bundle)
}

func restoreWorkloadRoot(ctx context.Context, workspaceRoot, runtimeRoot string, bundle []byte) ([]nativehost.RuntimeFileRestore, error) {
	workspace, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, err
	}
	// The restore keeps the root's execution: a native Docker-provider root
	// is re-rendered natively, never silently replaced by the wrapper.
	marker, err := ReadRootMarker(filepath.Join(workspace, filepath.FromSlash(runtimeRoot)))
	if err != nil {
		return nil, err
	}
	if IsPlatformProviderExecution(marker.Execution) {
		return nil, fmt.Errorf("%s restore requires explicit signed provider recovery; Compose restore cannot take ownership", marker.Execution)
	}
	descriptor, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(bundle)
	if err != nil {
		return nil, err
	}
	relative, err := WorkloadRootRelativePath("stackkit-" + descriptor.WorkloadRef + "-" + descriptor.NodeRef)
	if err != nil || relative != runtimeRoot {
		return nil, fmt.Errorf("the workload bundle of %s is not the workload of %s", descriptor.InstanceRef, runtimeRoot)
	}
	prepared, restores, err := nativehost.RestoreWorkloadRuntimeFiles(ctx, workspace, bundle)
	if err != nil {
		return nil, err
	}
	if prepared.ProjectName != path.Base(path.Dir(relative)) {
		return nil, errors.New("the restored workload project is not the stack graph runtime root")
	}
	var config []byte
	if marker.Execution == RootExecutionNativeDocker {
		native, err := RenderNativeWorkloadRoot(marker.ModuleRef, prepared)
		if err != nil {
			return nil, err
		}
		// The secret env files derive from the restored .env; only their
		// digest is in the root.
		if err := writeNativeSecretFiles(prepared, native.SecretFiles); err != nil {
			return nil, err
		}
		config = native.Config
	} else if config, err = RenderWorkloadRoot(prepared); err != nil {
		return nil, err
	}
	restored, err := restoreRootFile(workspace, path.Join(relative, ConfigFile), config, 0o640)
	if err != nil {
		return nil, err
	}
	return append(restores, restored...), nil
}

// ValidateOrdinaryWorkloadCheckpoint requires a closed ordinary rendering of
// the captured Compose payload before a markerless root can acquire an executor.
// A platform provider configuration cannot acquire a generic wrapper identity.
func ValidateOrdinaryWorkloadCheckpoint(moduleRef, project string, config, compose []byte) (string, error) {
	execution, _, err := matchOrdinaryWorkloadCheckpoint(moduleRef, project, config, compose)
	return execution, err
}

func matchOrdinaryWorkloadCheckpoint(moduleRef, project string, config, compose []byte) (string, architecturev2renderer.NativeDockerRoot, error) {
	var rendered architecturev2renderer.NativeDockerRoot
	native, err := architecturev2renderer.NativeDockerRootConfig(config)
	if err != nil {
		return "", rendered, err
	}
	prepared := nativehost.NativeWorkloadCompose{ProjectName: project, Compose: compose, EnvFile: EnvFile}
	for _, variant := range []struct{ wait, pilot bool }{{true, false}, {false, false}, {true, true}, {false, true}} {
		prepared.Wait = variant.wait
		if native {
			rendered, err = architecturev2renderer.RenderNativeDockerOpenTofu(nativeWorkloadSpec(moduleRef, prepared, variant.pilot))
			if err != nil {
				return "", rendered, err
			}
			if bytes.Equal(rendered.Config, config) {
				return RootExecutionNativeDocker, rendered, nil
			}
		} else if !variant.pilot {
			expected, err := RenderWorkloadRoot(prepared)
			if err != nil {
				return "", rendered, err
			}
			if bytes.Equal(expected, config) {
				return "", rendered, nil
			}
		}
	}
	return "", rendered, errors.New("checkpoint workload configuration is not the ordinary rendering of its captured Compose payload")
}

// AlignRestoredWorkloadRoot completes the files a coordinated rollback
// restored into one workload root (state, main.tf, lock, compose.yaml, .env):
// the executor marker takes the execution of the restored configuration, so
// the next Apply leaves it through the right owner, and a native
// Docker-provider root gets its owner-only secret env files rendered from the
// restored .env, which its containers mount and its configuration digests.
// The restored main.tf must be exactly the native rendering of the restored
// Compose payload. A Core or contract root is left alone.
func AlignRestoredWorkloadRoot(workspaceRoot, runtimeRoot string) (returnErr error) {
	admission, err := AcquireGenericRestoreAdmission(workspaceRoot, []string{runtimeRoot})
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, admission.Close()) }()
	return admission.AlignRestoredWorkloadRoot(runtimeRoot)
}

// AlignRestoredWorkloadRoot keeps a coordinated rollback's batch lease through
// marker alignment and native secret preparation.
func (a *GenericRestoreAdmission) AlignRestoredWorkloadRoot(runtimeRoot string) error {
	if err := a.requireRoots([]string{runtimeRoot}); err != nil {
		return err
	}
	return alignRestoredWorkloadRoot(a.transaction.Name(), runtimeRoot)
}

func alignRestoredWorkloadRoot(workspaceRoot, runtimeRoot string) error {
	workspace, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return err
	}
	root := filepath.Join(workspace, filepath.FromSlash(runtimeRoot))
	if marker, err := ReadRootMarker(root); err == nil && IsPlatformProviderExecution(marker.Execution) {
		return fmt.Errorf("%s checkpoint alignment requires explicit provider recovery; Compose alignment cannot take ownership", marker.Execution)
	}
	config, err := os.ReadFile(filepath.Join(root, ConfigFile))
	if err != nil {
		return fmt.Errorf("read the restored root: %w", err)
	}
	native, err := architecturev2renderer.NativeDockerRootConfig(config)
	if err != nil {
		return err
	}
	marker, err := ReadRootMarker(root)
	if err != nil || marker.Kind != RootKindWorkload {
		if native {
			return fmt.Errorf("the restored native root %s is not an identified workload root", runtimeRoot)
		}
		return nil
	}
	execution := ""
	if native {
		execution = RootExecutionNativeDocker
		directory := filepath.Dir(root)
		compose, err := os.ReadFile(filepath.Join(directory, ComposeFile))
		if err != nil {
			return fmt.Errorf("read the restored workload Compose payload: %w", err)
		}
		prepared := nativehost.NativeWorkloadCompose{
			ProjectName: marker.ComposeProject, Directory: directory, Compose: compose, EnvFile: EnvFile,
		}
		_, rendered, err := matchOrdinaryWorkloadCheckpoint(marker.ModuleRef, marker.ComposeProject, config, compose)
		if err != nil {
			return err
		}

		if err := writeNativeSecretFiles(prepared, rendered.SecretFiles); err != nil {
			return err
		}
	}
	if marker.Execution == execution {
		return nil
	}
	marker.Execution = execution
	encoded, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	return writeFileAtomic(root, MarkerFile, append(encoded, '\n'), 0o600)
}

// RestoreCoreRoot restores the governed runtime files of one Core stack: the
// root's main.tf, which is the governed generation artifact, and the
// ../compose.yaml its local_file writes, the payload the artifact embeds.
func RestoreCoreRoot(workspaceRoot, runtimeRoot string, config []byte) (_ []nativehost.RuntimeFileRestore, returnErr error) {
	admission, err := AcquireGenericRestoreAdmission(workspaceRoot, []string{runtimeRoot})
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, admission.Close()) }()
	return admission.RestoreCoreRoot(runtimeRoot, config)
}

// RestoreCoreRoot performs a nested repair without releasing the batch lease.
func (a *GenericRestoreAdmission) RestoreCoreRoot(runtimeRoot string, config []byte) ([]nativehost.RuntimeFileRestore, error) {
	if err := a.requireRoots([]string{runtimeRoot}); err != nil {
		return nil, err
	}
	return restoreCoreRoot(a.transaction.Name(), runtimeRoot, config)
}

func restoreCoreRoot(workspaceRoot, runtimeRoot string, config []byte) ([]nativehost.RuntimeFileRestore, error) {
	workspace, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, err
	}
	runtimeDir := path.Base(path.Dir(runtimeRoot))
	if expected, err := RootRelativePath(runtimeDir); err != nil || expected != runtimeRoot {
		return nil, fmt.Errorf("%s is not a Core OpenTofu root", runtimeRoot)
	}
	// Reconcile repairs materialized executors. A governed scaffold has not
	// installed its manager marker yet; its normal Apply owns that transition.
	marker, err := ReadRootMarker(filepath.Join(workspace, filepath.FromSlash(runtimeRoot)))
	if err != nil {
		return nil, err
	}
	if marker.Kind != "" || IsPlatformProviderExecution(marker.Execution) {
		return nil, errors.New("Core restore requires an identified generic Core root")
	}
	payload, err := rootComposePayload(config)
	if err != nil {
		return nil, err
	}
	restores, err := restoreRootFile(workspace, path.Join(path.Dir(runtimeRoot), ComposeFile), payload, 0o600)
	if err != nil {
		return nil, err
	}
	restored, err := restoreRootFile(workspace, path.Join(runtimeRoot, ConfigFile), config, 0o640)
	if err != nil {
		return nil, err
	}
	return append(restores, restored...), nil
}

// restoreRootFile rewrites one governed file below an existing plain
// directory when its bytes differ, and returns the drift record.
func restoreRootFile(workspace, relative string, data []byte, mode os.FileMode) ([]nativehost.RuntimeFileRestore, error) {
	directory := filepath.Join(workspace, filepath.FromSlash(path.Dir(relative)))
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("governed runtime directory " + path.Dir(relative) + " is not a plain directory")
	}
	drifted, err := nativehost.RuntimeFileDrift(filepath.Join(directory, path.Base(relative)), data)
	if err != nil || drifted == nil {
		return nil, err
	}
	if err := writeFileAtomic(directory, path.Base(relative), data, mode); err != nil {
		return nil, err
	}
	drifted.Path = relative
	return []nativehost.RuntimeFileRestore{*drifted}, nil
}
