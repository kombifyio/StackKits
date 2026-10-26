package opentofu

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/tofu"
)

const RuntipiAPIExecution = "runtipi-api"
const RuntipiAPIModulesEnv = "STACKKIT_RUNTIPI_API_MODULES"
const runtipiFilesModule = "stackkits-cloudreve-runtime"

// RuntipiWorkloadOperations is an opt-in, existing-workload API bridge. The
// existing native owner performs mutation inside the OpenTofu action, not
// before it. Bootstrap, adoption, removal and provider lifecycle are excluded.
type RuntipiWorkloadOperations struct {
	runtime Runtime
	native  nativehost.SelectedPaaSWorkloadOperations
}

func NewRuntipiWorkloadOperations(runtime Runtime) (*RuntipiWorkloadOperations, error) {
	operations, err := nativehost.NewOSRuntipiWorkloadOperations(runtime.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	return &RuntipiWorkloadOperations{runtime: runtime, native: operations}, nil
}

func validateRuntipiAPIRequest(workspace string, d nativehost.SelectedPaaSWorkloadDeployment) error {
	if os.Getenv(RuntipiAPIModulesEnv) != runtipiFilesModule || d.GenerationTarget != "opentofu" {
		return errors.New("Runtipi API bridge requires its explicit Files opt-in and Standard Mode opentofu target")
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return err
	}
	if d.WorkloadRef != "files" || d.ModuleRef != runtipiFilesModule || d.UnitRef != "cloudreve" || d.SiteRef != owner.Binding.SiteRef ||
		d.NodeRef != owner.Binding.NodeRef || d.ExecutionChannelRef != owner.Binding.ChannelRef ||
		d.InstanceRef == "" || d.ArtifactRef == "" || d.ArtifactDigest != digestBytes(d.Bundle) ||
		d.RuntimeAdapter.ID != "runtipi" || d.RuntimeAdapter.ProviderRef != "stackkits-runtipi" || d.RuntimeAdapter.ModuleRef != "stackkits-runtipi-runtime" {
		return errors.New("Runtipi API bridge requires the exact owner-node Files artifact and runtime adapter")
	}
	bundle, err := architecturev2renderer.ParseCloudreveWorkloadBundle(d.Bundle)
	if err != nil {
		return err
	}
	if bundle.WorkloadRef != d.WorkloadRef || bundle.ModuleRef != d.ModuleRef || bundle.Release != d.Release ||
		bundle.SiteRef != d.SiteRef || bundle.NodeRef != d.NodeRef || bundle.InstanceRef != d.InstanceRef ||
		bundle.Route != d.Route {
		return errors.New("Runtipi Files artifact differs from the requested node, release or route")
	}
	return nil
}

func requireRuntipiHeldDeployment(workspace, project string, d nativehost.SelectedPaaSWorkloadDeployment, prior *platformdeploy.HostDeployment) (platformdeploy.HostDeployment, error) {
	held, err := platformdeploy.LoadOwnerHostDeployment(workspace, project)
	if err != nil {
		return held, errors.New("Runtipi API bridge requires an already signed native workload; bootstrap/adoption must complete first")
	}
	if held.Signature == nil || platformdeploy.RequireHostDeploymentManager(held, "runtipi") != nil || held.ServerID == "" ||
		held.InstanceRef != d.InstanceRef || held.WorkloadRef != d.WorkloadRef || held.SiteRef != d.SiteRef || held.NodeRef != d.NodeRef ||
		held.Deployment.Platform != "runtipi" || held.Deployment.AppName != project || held.Deployment.ExternalID != project+":_user" {
		return held, errors.New("Runtipi API bridge differs from exact retained workload ownership")
	}
	if prior != nil && (held.ServerID != prior.ServerID || held.Deployment.ExternalID != prior.Deployment.ExternalID ||
		held.ArtifactDigest != prior.ArtifactDigest && held.ArtifactDigest != d.ArtifactDigest) {
		return held, errors.New("Runtipi API bridge ownership changed during its pending operation")
	}
	return held, nil
}

func (o *RuntipiWorkloadOperations) withProject(ctx context.Context, d nativehost.SelectedPaaSWorkloadDeployment, run func(string, string, string, *confinedfs.OutputLockLease) error) error {
	if ctx == nil || o == nil || o.native == nil {
		return errors.New("Runtipi API bridge is not initialized")
	}
	if err := requireActionLeasePlatform(); err != nil {
		return err
	}
	workspace, err := filepath.Abs(o.runtime.WorkspaceRoot)
	if err != nil {
		return err
	}
	project := "stackkit-" + d.WorkloadRef + "-" + d.NodeRef
	if len(project) > 50 {
		return errors.New("Runtipi app identity exceeds the pinned upstream limit")
	}
	relative, err := WorkloadRootRelativePath(project)
	if err != nil {
		return err
	}
	fs, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer fs.Close()
	tx, err := fs.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	lock, err := tx.TryAcquireOutputLock(filepath.ToSlash(filepath.Dir(relative)))
	if err != nil {
		return err
	}
	defer lock.Release()
	lease, err := lock.Borrow(tx, filepath.ToSlash(filepath.Dir(relative)))
	if err != nil {
		return err
	}
	defer lease.Close()
	// A surviving action helper may still own this project. Custody and its
	// signing key are read only after that operation's exclusive lease ends.
	if err := validateRuntipiAPIRequest(workspace, d); err != nil {
		return err
	}
	return run(workspace, project, relative, lease)
}

func (o *RuntipiWorkloadOperations) ApplyWorkload(ctx context.Context, d nativehost.SelectedPaaSWorkloadDeployment) (nativehost.SelectedPaaSApplyReceipt, error) {
	var receipt nativehost.SelectedPaaSApplyReceipt
	err := o.withProject(ctx, d, func(workspace, project, relative string, lease *confinedfs.OutputLockLease) error {
		root := filepath.Join(workspace, filepath.FromSlash(relative))
		requestDigest := runtipiRequestDigest(d)
		intent := runtipiActionIntent{Version: runtipiActionVersion, Request: d, RequestDigest: requestDigest, Phase: "prepared"}
		_, rootErr := os.Lstat(root)
		if rootErr == nil {
			if err := requirePlainRelativeDirectory(workspace, relative); err != nil {
				return err
			}
			marker, err := ReadRootMarker(root)
			if err != nil || marker.Execution != RuntipiAPIExecution || marker.ModuleRef != d.ModuleRef || marker.InstanceRef != d.InstanceRef || marker.ComposeProject != project {
				return errors.New("Runtipi API bridge refuses another or unidentified executor root; explicit transition is required")
			}
			previous, err := readRuntipiIntent(workspace, root)
			if err != nil {
				return err
			}
			if previous.Phase != "applied" && previous.RequestDigest != requestDigest {
				return errors.New("Runtipi has a pending signed revision; finish that exact request before changing runtime files")
			}
			if previous.Phase != "applied" {
				intent.Held = previous.Held
			}
		} else if !errors.Is(rootErr, os.ErrNotExist) {
			return rootErr
		}
		held, err := requireRuntipiHeldDeployment(workspace, project, d, nil)
		if err != nil {
			return err
		}
		if intent.Held.Signature == nil {
			intent.Held = held
		}
		if _, err := requireRuntipiHeldDeployment(workspace, project, d, &intent.Held); err != nil {
			return err
		}
		if errors.Is(rootErr, os.ErrNotExist) {
			if held.ArtifactDigest != d.ArtifactDigest || held.Deployment.ObservedStatus != "running" {
				return errors.New("first Runtipi API enrollment requires the already applied exact native revision")
			}
			if _, err := o.observeHealthy(ctx, d); err != nil {
				return err
			}
		}
		binary, providers, err := o.runtime.packagedTools()
		if err != nil {
			return err
		}
		if err := o.runtime.requireStateCustody(ctx, relative, binary, providers.Directory()); err != nil {
			return err
		}
		config, err := architecturev2renderer.RenderRuntipiWorkloadOpenTofu(project, requestDigest)
		if err != nil {
			return err
		}
		providerLock, err := providers.LockForConfiguration(config)
		if err != nil {
			return err
		}
		capability, capabilityDigest, err := newRuntipiCapability()
		if err != nil {
			return err
		}
		server, err := newActionLeaseServer(lease, workspace, project, capability)
		if err != nil {
			return err
		}
		defer server.Close()
		intent.CapabilityDigest = capabilityDigest
		marker := RootMarker{SchemaVersion: RootMarkerSchemaVersion, ModuleRef: d.ModuleRef, InstanceRef: d.InstanceRef,
			RuntimeDir: project, ComposeProject: project, Kind: RootKindWorkload, Execution: RuntipiAPIExecution}
		if errors.Is(rootErr, os.ErrNotExist) {
			// Publish the initial marker/config/request together. Staging is on
			// the project filesystem and outside the runtime root being enrolled.
			parent, err := ensureRootDir(workspace, filepath.ToSlash(filepath.Dir(relative)))
			if err != nil {
				return err
			}
			stage, err := os.MkdirTemp(parent, ".runtipi-root-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(stage)
			if err := writeRoot(stage, marker, providers.Directory(), providerLock, rootFile{ConfigFile, config, 0o640}); err != nil {
				return err
			}
			if err := writeRuntipiIntent(workspace, stage, intent); err != nil {
				return err
			}
			if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
				return errors.New("Runtipi root appeared during explicit enrollment")
			}
			if err := os.Rename(stage, root); err != nil {
				return err
			}
		} else {
			// Authenticate the new pending request before changing any config;
			// failed preparation can retry it without accepting another revision.
			if err := writeRuntipiIntent(workspace, root, intent); err != nil {
				return err
			}
			if err := writeRoot(root, marker, providers.Directory(), providerLock, rootFile{ConfigFile, config, 0o640}); err != nil {
				return err
			}
		}
		bridge, err := os.Executable()
		if err != nil {
			return err
		}
		runtime := o.runtime
		previousAdmission := runtime.PlanAdmission
		runtime.PlanAdmission = func(ctx context.Context, runner *tofu.Executor, plan string) error {
			if previousAdmission != nil {
				if err := previousAdmission(ctx, runner, plan); err != nil {
					return err
				}
			}
			activate, err := admitRuntipiAPIPlan(ctx, runner, plan, project, requestDigest)
			if err != nil {
				return err
			}
			if activate {
				intent.Phase = "executing"
				return writeRuntipiIntent(workspace, root, intent)
			}
			return nil
		}
		environment := []string{"LANG=C", "LC_ALL=C", "STACKKIT_RUNTIPI_BRIDGE_BINARY=" + bridge,
			runtipiActionCapabilityEnv + "=" + capability, actionLeaseSocketEnv + "=" + server.Endpoint(), RuntipiAPIModulesEnv + "=" + runtipiFilesModule}
		if _, err := runtime.runRoot(ctx, relative, root, binary, environment); err != nil {
			return err
		}
		if _, err := o.observeHealthy(ctx, d); err != nil {
			return fmt.Errorf("Runtipi API operation lacks independent application observation: %w", err)
		}
		intent.Phase, intent.CapabilityDigest = "applied", ""
		if err := writeRuntipiIntent(workspace, root, intent); err != nil {
			return err
		}
		receipt = nativehost.SelectedPaaSApplyReceipt{InstanceRef: d.InstanceRef, ArtifactDigest: d.ArtifactDigest, Status: "applied"}
		return nil
	})
	return receipt, err
}

func (o *RuntipiWorkloadOperations) ObserveWorkload(ctx context.Context, d nativehost.SelectedPaaSWorkloadDeployment) (nativehost.SelectedPaaSWorkloadObservation, error) {
	var observation nativehost.SelectedPaaSWorkloadObservation
	err := o.withProject(ctx, d, func(workspace, project, relative string, lease *confinedfs.OutputLockLease) error {
		root := filepath.Join(workspace, filepath.FromSlash(relative))
		if err := requirePlainRelativeDirectory(workspace, relative); err != nil {
			return err
		}
		marker, err := ReadRootMarker(root)
		if err != nil || marker.Execution != RuntipiAPIExecution || marker.ComposeProject != project || marker.InstanceRef != d.InstanceRef || marker.ModuleRef != d.ModuleRef {
			return errors.New("Runtipi observation has no exact API-owned root")
		}
		intent, err := readRuntipiIntent(workspace, root)
		if err != nil {
			return err
		}
		if intent.Phase != "applied" || intent.RequestDigest != runtipiRequestDigest(d) {
			return errors.New("Runtipi observation requires its completed exact signed request")
		}
		if _, err := requireRuntipiHeldDeployment(workspace, project, d, &intent.Held); err != nil {
			return err
		}
		observation, err = o.observeHealthy(ctx, d)
		return err
	})
	return observation, err
}

var _ nativehost.SelectedPaaSWorkloadOperations = (*RuntipiWorkloadOperations)(nil)

// observeHealthy treats HTTP status and component health as data that must
// satisfy the application contract, even when the native read itself succeeds.
func (o *RuntipiWorkloadOperations) observeHealthy(ctx context.Context, d nativehost.SelectedPaaSWorkloadDeployment) (nativehost.SelectedPaaSWorkloadObservation, error) {
	observation, err := o.native.ObserveWorkload(ctx, d)
	if err == nil {
		err = nativehost.ValidateSelectedPaaSWorkloadObservation(d, observation)
	}
	return observation, err
}
