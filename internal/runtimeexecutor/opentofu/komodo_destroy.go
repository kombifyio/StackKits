package opentofu

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/tofu"
)

// DestroyKomodoWorkloadRoot is the closed internal root operation. It is not a
// public remove/transition admission; callers still own that lifecycle policy.
// All internal runRootDestroy calls for a Komodo marker dispatch here too.
func (r Runtime) DestroyKomodoWorkloadRoot(ctx context.Context, project string) error {
	relative, err := WorkloadRootRelativePath(project)
	if err != nil {
		return err
	}
	workspace, err := filepath.Abs(r.WorkspaceRoot)
	if err != nil {
		return err
	}
	root := filepath.Join(workspace, filepath.FromSlash(relative))
	binary, _, err := r.packagedTools()
	if err != nil {
		return err
	}
	_, err = r.runKomodoRootDestroy(ctx, relative, root, binary, nil)
	return err
}

func (r Runtime) runKomodoRootDestroy(ctx context.Context, relative, root, binary string, environment []string) (tofuRun, error) {
	var empty tofuRun
	if ctx == nil {
		return empty, errors.New("Komodo destroy requires its operation context")
	}
	if err := requireKomodoProcessPlatform(); err != nil {
		return empty, err
	}
	workspace, err := filepath.Abs(r.WorkspaceRoot)
	if err != nil {
		return empty, err
	}
	project := filepath.Base(filepath.Dir(relative))
	expected, err := WorkloadRootRelativePath(project)
	if err != nil || relative != expected || root != filepath.Join(workspace, filepath.FromSlash(expected)) {
		return empty, errors.New("Komodo destroy requires its exact workload root")
	}
	// Derive the lock key from the requested root, before authoritative marker or
	// custody reads. A construction-held borrow can be reused, never re-locked.
	if r.komodoLease == nil {
		fs, err := confinedfs.Open(workspace)
		if err != nil {
			return empty, err
		}
		defer fs.Close()
		tx, err := fs.BeginTransaction()
		if err != nil {
			return empty, err
		}
		defer tx.Close()
		lock, err := tx.TryAcquireOutputLock(".stackkit/runtime/applications/" + project)
		if err != nil {
			return empty, err
		}
		defer lock.Release()
		lease, err := lock.Borrow(tx, ".stackkit/runtime/applications/"+project)
		if err != nil {
			return empty, err
		}
		defer lease.Close()
		r.komodoLease = lease
	}
	if r.komodoLease.OutputRoot() != ".stackkit/runtime/applications/"+project {
		return empty, errors.New("Komodo destroy project lease differs")
	}
	marker, err := ReadRootMarker(root)
	if err != nil || !isKomodoProviderRoot(marker) || marker.ComposeProject != project || marker.RuntimeDir != project || marker.Kind != RootKindWorkload {
		return empty, errors.New("Komodo destroy requires the identified provider root")
	}
	held, err := platformdeploy.LoadOwnerHostDeployment(workspace, project)
	if err != nil || held.Signature == nil || !komodoProviderRootMatches(marker, held) || held.InstanceRef != marker.InstanceRef || platformdeploy.RequireHostDeploymentManager(held, "komodo") != nil {
		return empty, errors.New("Komodo destroy requires authenticated held ownership")
	}
	cfg, err := platformdeploy.LoadOwnerNativeKomodoConfig(ctx, workspace)
	if err != nil {
		return empty, err
	}
	if held.ServerID != cfg.ServerID || held.Manager != "komodo" || held.Deployment.AppName != project || project != "stackkit-"+held.WorkloadRef+"-"+held.NodeRef {
		return empty, errors.New("Komodo destroy server differs from held custody")
	}
	adapter := platformdeploy.NewKomodoAdapter(cfg)
	if err := adapter.RequireProviderHostStack(ctx, held.Deployment, filepath.Dir(root)); err != nil {
		return empty, err
	}
	leases, err := NewKomodoActionLeases(r.komodoLease, workspace, project)
	if err != nil {
		return empty, err
	}
	defer leases.Close()
	credentials, err := komodoProviderEnvironment(cfg)
	if err != nil {
		return empty, err
	}
	// Replace caller credential/capability fields with this exact node's custody.
	for _, name := range []string{"KOMODO_ENDPOINT", "KOMODO_API_KEY", "KOMODO_API_SECRET", "KOMODO_USERNAME", "KOMODO_PASSWORD", "STACKKIT_KOMODO_BRIDGE_BINARY"} {
		environment = withoutEnvironment(environment, name)
	}
	environment = append(append(append([]string(nil), environment...), credentials...), leases.Environment()...)
	r.WorkspaceRoot = workspace
	var run tofuRun
	err = r.withStateCustody(relative, func(key []byte) error {
		var err error
		run, err = r.runRootWithKeyMode(ctx, root, binary, environment, key, true)
		return err
	})
	return run, err
}

func (r Runtime) komodoProcessOwner(root string) (tofu.ProcessCustody, error) {
	marker, err := ReadRootMarker(root)
	if err != nil {
		return nil, err
	}
	if marker.Execution != komodoProviderExecution {
		return nil, nil
	}
	if err := requireKomodoProcessPlatform(); err != nil {
		return nil, err
	}
	workspace, err := filepath.Abs(r.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	expected, err := WorkloadRootRelativePath(marker.ComposeProject)
	if err != nil || root != filepath.Join(workspace, filepath.FromSlash(expected)) || r.komodoLease == nil || r.komodoLease.OutputRoot() != ".stackkit/runtime/applications/"+marker.ComposeProject {
		return nil, errors.New("Komodo engine requires its exact held project lease")
	}
	return komodoProcessCustody{workspace, marker.ComposeProject, r.komodoLease}, nil
}
