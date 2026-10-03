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
func RestoreWorkloadRoot(ctx context.Context, workspaceRoot, runtimeRoot string, bundle []byte) ([]nativehost.RuntimeFileRestore, error) {
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

// AlignRestoredWorkloadRoot completes the files a coordinated rollback
// restored into one workload root (state, main.tf, lock, compose.yaml, .env):
// the executor marker takes the execution of the restored configuration, so
// the next Apply leaves it through the right owner, and a native
// Docker-provider root gets its owner-only secret env files rendered from the
// restored .env, which its containers mount and its configuration digests.
// The restored main.tf must be exactly the native rendering of the restored
// Compose payload. A Core or contract root is left alone.
func AlignRestoredWorkloadRoot(workspaceRoot, runtimeRoot string) error {
	workspace, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return err
	}
	root := filepath.Join(workspace, filepath.FromSlash(runtimeRoot))
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
		var rendered architecturev2renderer.NativeDockerRoot
		matched := false
		// Wait is the one rendering input outside the payload; a checkpoint
		// the S2.0 pilot release rendered lacks the service aliases.
		for _, variant := range []struct{ wait, pilot bool }{{true, false}, {false, false}, {true, true}, {false, true}} {
			prepared.Wait = variant.wait
			if rendered, err = architecturev2renderer.RenderNativeDockerOpenTofu(nativeWorkloadSpec(marker.ModuleRef, prepared, variant.pilot)); err != nil {
				return err
			}
			if bytes.Equal(rendered.Config, config) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("the restored root of %s is not the native rendering of its restored Compose payload", runtimeRoot)
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
func RestoreCoreRoot(workspaceRoot, runtimeRoot string, config []byte) ([]nativehost.RuntimeFileRestore, error) {
	workspace, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, err
	}
	runtimeDir := path.Base(path.Dir(runtimeRoot))
	if expected, err := RootRelativePath(runtimeDir); err != nil || expected != runtimeRoot {
		return nil, fmt.Errorf("%s is not a Core OpenTofu root", runtimeRoot)
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
