package opentofu

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/tofu"
)

const (
	DokployProviderModulesEnv = "STACKKIT_DOKPLOY_PROVIDER_MODULES"
	DokployProviderImportEnv  = "STACKKIT_DOKPLOY_PROVIDER_IMPORT_ID"
	DokployProviderExecution  = "dokploy-provider"
)

type DokployWorkloadOperations struct {
	runtime Runtime
	native  *nativehost.DokployWorkloadPreparation
}

func NewDokployWorkloadOperations(runtime Runtime) (*DokployWorkloadOperations, error) {
	prepared, err := nativehost.NewOSDokployWorkloadPreparation(runtime.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	return &DokployWorkloadOperations{runtime: runtime, native: prepared}, nil
}

func requireDokployOptIn() error {
	if strings.TrimSpace(os.Getenv(DokployProviderModulesEnv)) != nativehost.DokployVaultModule {
		return fmt.Errorf("Dokploy provider requires explicit %s=%s; no fallback or public selection is admitted", DokployProviderModulesEnv, nativehost.DokployVaultModule)
	}
	return nil
}

func (o *DokployWorkloadOperations) PrepareAdoptionRecipe(ctx context.Context, d nativehost.SelectedPaaSWorkloadDeployment) (nativehost.DokployAdoptionRecipe, error) {
	if err := requireDokployOptIn(); err != nil {
		return nativehost.DokployAdoptionRecipe{}, err
	}
	if err := o.native.ValidateDeployment(d); err != nil {
		return nativehost.DokployAdoptionRecipe{}, err
	}
	root, tx, lock, err := o.lock("stackkit-" + d.WorkloadRef + "-" + d.NodeRef)
	if err != nil {
		return nativehost.DokployAdoptionRecipe{}, err
	}
	defer root.Close()
	defer tx.Close()
	defer lock.Release()
	return o.native.PrepareAdoptionRecipe(ctx, d)
}

func (o *DokployWorkloadOperations) lock(project string) (*confinedfs.Root, *confinedfs.Transaction, *confinedfs.OutputLock, error) {
	root, err := confinedfs.Open(o.runtime.WorkspaceRoot)
	if err != nil {
		return nil, nil, nil, err
	}
	tx, err := root.BeginTransaction()
	if err != nil {
		root.Close()
		return nil, nil, nil, err
	}
	lock, err := tx.TryAcquireOutputLock(".stackkit/runtime/applications/" + project)
	if err != nil {
		tx.Close()
		root.Close()
		return nil, nil, nil, err
	}
	return root, tx, lock, nil
}

// AdoptWorkload records an explicit exact service, never an inferred display
// name. Existing foreign roots/state cannot be reinterpreted as Dokploy.
func (o *DokployWorkloadOperations) AdoptWorkload(ctx context.Context, d nativehost.SelectedPaaSWorkloadDeployment, composeID string) (platformdeploy.HostDeployment, error) {
	var empty platformdeploy.HostDeployment
	if err := requireDokployOptIn(); err != nil {
		return empty, err
	}
	if err := o.native.ValidateDeployment(d); err != nil {
		return empty, err
	}
	project := "stackkit-" + d.WorkloadRef + "-" + d.NodeRef
	relative, err := WorkloadRootRelativePath(project)
	if err != nil {
		return empty, err
	}
	root, tx, lock, err := o.lock(project)
	if err != nil {
		return empty, err
	}
	defer root.Close()
	defer tx.Close()
	defer lock.Release()
	directory := filepath.Join(o.runtime.WorkspaceRoot, filepath.FromSlash(relative))
	for _, name := range []string{StateFile, MarkerFile} {
		if _, err := os.Lstat(filepath.Join(directory, name)); err == nil {
			return empty, errors.New("Dokploy adoption requires no existing OpenTofu state or root; retain its current manager and use an explicit transition")
		} else if !errors.Is(err, os.ErrNotExist) {
			return empty, err
		}
	}
	return o.native.Adopt(ctx, d, composeID)
}

func (o *DokployWorkloadOperations) ApplyWorkload(ctx context.Context, d nativehost.SelectedPaaSWorkloadDeployment) (nativehost.SelectedPaaSApplyReceipt, error) {
	var empty nativehost.SelectedPaaSApplyReceipt
	if err := requireDokployOptIn(); err != nil {
		return empty, err
	}
	if ctx == nil || !nativehost.GenerationTargetExecutesOpenTofu(d.GenerationTarget) {
		return empty, errors.New("Dokploy provider requires an admitted OpenTofu target and context")
	}
	if err := o.native.ValidateDeployment(d); err != nil {
		return empty, err
	}
	project := "stackkit-" + d.WorkloadRef + "-" + d.NodeRef
	relative, err := WorkloadRootRelativePath(project)
	if err != nil {
		return empty, err
	}
	rootHandle, tx, lockHandle, err := o.lock(project)
	if err != nil {
		return empty, err
	}
	defer rootHandle.Close()
	defer tx.Close()
	defer lockHandle.Release()
	workspace, err := filepath.Abs(o.runtime.WorkspaceRoot)
	if err != nil {
		return empty, err
	}
	root := filepath.Join(workspace, filepath.FromSlash(relative))
	held, err := o.native.HeldDeployment(d)
	if err != nil {
		return empty, err
	}
	hasState := false
	if _, err := os.Lstat(filepath.Join(root, StateFile)); err == nil {
		hasState = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return empty, err
	}
	if _, err := os.Lstat(filepath.Join(root, MarkerFile)); err == nil {
		marker, err := ReadRootMarker(root)
		if err != nil || marker.Execution != DokployProviderExecution || marker.ModuleRef != d.ModuleRef || marker.InstanceRef != d.InstanceRef || marker.ComposeProject != project {
			return empty, errors.New("Dokploy provider refuses a foreign or invalid OpenTofu root")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return empty, err
	} else if hasState {
		return empty, errors.New("Dokploy state without its exact provider marker cannot be adopted")
	}
	importID := os.Getenv(DokployProviderImportEnv)
	if (!hasState && importID != held.Deployment.ExternalID) || (hasState && importID != "") {
		return empty, fmt.Errorf("first Dokploy provider apply requires %s matching signed custody and no state; clear it for subsequent applies", DokployProviderImportEnv)
	}
	cfg, err := nativehost.LoadOwnerDokployProviderConfig(ctx, workspace, d.SiteRef, d.NodeRef)
	if err != nil {
		return empty, err
	}
	if cfg.EnvironmentID != held.EnvironmentID || cfg.ServerID != held.ServerID {
		return empty, errors.New("Dokploy bootstrap placement differs from signed workload custody")
	}
	adapter := platformdeploy.NewDokployAdapter(cfg)
	before, err := adapter.RequireProviderCompose(ctx, held.Deployment)
	if err != nil {
		return empty, err
	}
	if before.AppName != held.RuntimeProject {
		return empty, errors.New("Dokploy physical project differs from signed custody")
	}
	binary, providers, err := o.runtime.packagedTools()
	if err != nil {
		return empty, err
	}
	if err := o.runtime.requireStateCustody(ctx, relative, binary, providers.Directory()); err != nil {
		return empty, err
	}
	prepared, err := o.native.PrepareWorkloadCompose(ctx, d)
	if err != nil {
		return empty, err
	}
	if prepared.ProjectName != project || prepared.Directory != filepath.Dir(root) || prepared.EnvFile != EnvFile {
		return empty, errors.New("Dokploy preparation differs from its exact node workload root")
	}
	payload, err := o.native.ProviderCompose(prepared)
	if err != nil {
		return empty, err
	}
	description, err := o.native.Description(d, prepared)
	if err != nil {
		return empty, err
	}
	if err := o.native.RequirePrivateEnvironment(ctx, prepared, description); err != nil {
		return empty, err
	}
	desiredDigest := nativehost.DokployProviderRecipeDigest(string(payload), description)
	if actual := nativehost.DokployProviderRecipeDigest(before.ComposeFile, before.Description); actual != held.RecipeDigest && actual != desiredDigest {
		return empty, errors.New("Dokploy remote recipe changed outside signed custody; reconcile it before apply")
	}
	historyBefore, err := adapter.LatestProviderDeployment(ctx, held.Deployment)
	if err != nil {
		return empty, err
	}
	if nativehost.DokployProviderRecipeDigest(before.ComposeFile, before.Description) == held.RecipeDigest && historyBefore.DeploymentID != held.Deployment.DeploymentID {
		return empty, errors.New("Dokploy activation changed outside signed custody; reconcile the exact deployment before apply")
	}
	config, err := architecturev2renderer.RenderDokployWorkloadOpenTofu(architecturev2renderer.DokployWorkloadSpec{ProjectName: project, EnvironmentID: cfg.EnvironmentID,
		ComposeID: held.Deployment.ExternalID, AppName: held.RuntimeProject, ComposePath: filepath.Join(root, ComposeFile),
		ComposeSHA256: strings.TrimPrefix(nativehost.DokployRecipeDigest(payload), "sha256:"), Description: description})
	if err != nil {
		return empty, err
	}
	providerLock, err := providers.LockForConfiguration(config)
	if err != nil {
		return empty, err
	}
	root, err = ensureRootDir(workspace, relative)
	if err != nil {
		return empty, err
	}
	if err := writeRoot(root, RootMarker{SchemaVersion: RootMarkerSchemaVersion, ModuleRef: d.ModuleRef, InstanceRef: d.InstanceRef,
		RuntimeDir: project, ComposeProject: project, Kind: RootKindWorkload, Execution: DokployProviderExecution}, providers.Directory(), providerLock,
		rootFile{ConfigFile, config, 0o640}, rootFile{ComposeFile, payload, 0o600}); err != nil {
		return empty, err
	}
	environment := []string{"DOKPLOY_ENDPOINT=" + cfg.BaseURL, "DOKPLOY_API_KEY=" + cfg.Token}
	if !hasState {
		if err := o.runtime.importDokployService(ctx, relative, root, binary, environment, importID); err != nil {
			return empty, err
		}
	}
	runtime := o.runtime
	priorAdmission := runtime.PlanAdmission
	admit := DokployPlanAdmission(held, payload, description)
	runtime.PlanAdmission = func(ctx context.Context, runner *tofu.Executor, plan string) error {
		if priorAdmission != nil {
			if err := priorAdmission(ctx, runner, plan); err != nil {
				return errors.New("Dokploy caller plan restriction refused the saved plan")
			}
		}
		return admit(ctx, runner, plan)
	}
	run, err := runtime.runRoot(ctx, relative, root, binary, environment)
	if err != nil {
		return empty, err
	}
	after, err := adapter.RequireProviderCompose(ctx, held.Deployment)
	if err != nil || after.AppName != held.RuntimeProject || nativehost.DokployProviderRecipeDigest(after.ComposeFile, after.Description) != desiredDigest {
		return empty, errors.New("Dokploy provider result lacks exact held-ID and recipe readback")
	}
	historyAfter, err := adapter.LatestProviderDeployment(ctx, held.Deployment)
	if err != nil {
		return empty, err
	}
	if (desiredDigest != held.RecipeDigest && historyAfter.DeploymentID == held.Deployment.DeploymentID) ||
		(desiredDigest == held.RecipeDigest && historyAfter.DeploymentID != held.Deployment.DeploymentID) {
		return empty, errors.New("Dokploy provider result lacks independently correlated successful activation; retain custody and reconcile the held deployment")
	}
	held.Deployment.DeploymentID = historyAfter.DeploymentID
	held.ArtifactDigest, held.RecipeDigest = d.ArtifactDigest, desiredDigest
	if err := platformdeploy.SaveOwnerHostDeployment(workspace, project, held); err != nil {
		return empty, err
	}
	if err := o.native.CompleteWorkloadCompose(ctx, prepared, held.RuntimeProject); err != nil {
		return empty, err
	}
	if _, err := o.ObserveWorkload(ctx, d); err != nil {
		return empty, err
	}
	if _, err := writeObservation(root, workloadObservation{SchemaVersion: workloadObservationSchemaVersion, ModuleRef: d.ModuleRef,
		InstanceRef: d.InstanceRef, ArtifactID: d.ArtifactRef, ArtifactDigest: d.ArtifactDigest, Root: relative,
		ComposeProject: held.RuntimeProject, ComposeSHA256: digestBytes(prepared.Compose), Execution: DokployProviderExecution, tofuRun: run}); err != nil {
		return empty, err
	}
	return nativehost.SelectedPaaSApplyReceipt{InstanceRef: d.InstanceRef, ArtifactDigest: d.ArtifactDigest, Status: "applied"}, nil
}

func (r Runtime) importDokployService(ctx context.Context, relative, root, binary string, environment []string, id string) error {
	return r.withStateCustody(relative, func(key []byte) error {
		runner := tofu.NewExecutor(tofu.WithWorkDir(root), tofu.WithBinary(binary), tofu.WithoutInheritedEnv(tofu.OfflineInheritedEnv...),
			tofu.WithoutEnvPrefix("TF_LOG", "TF_CLI_ARGS_"), tofu.WithEnv(append(environment, "TF_CLI_CONFIG_FILE="+filepath.Join(root, CLIConfigFile), "TF_CLI_ARGS=-no-color", "TF_ENCRYPTION="+openTofuEncryptionConfig(key, false))...))
		result, err := runner.InitReadonly(ctx)
		stepErr := requireTofuStep("Dokploy import init", result, err)
		discardTofuOutput(result)
		if stepErr != nil {
			return stepErr
		}
		result, err = runner.Import(ctx, "dokploy_compose.workload", id)
		stepErr = requireTofuStep("Dokploy explicit import", result, err)
		discardTofuOutput(result)
		if stepErr != nil {
			return stepErr
		}
		_, err = protectState(root)
		return err
	})
}

func (o *DokployWorkloadOperations) ObserveWorkload(ctx context.Context, d nativehost.SelectedPaaSWorkloadDeployment) (nativehost.SelectedPaaSWorkloadObservation, error) {
	if err := requireDokployOptIn(); err != nil {
		return nativehost.SelectedPaaSWorkloadObservation{}, err
	}
	return o.native.ObserveWorkload(ctx, d)
}

func (o *DokployWorkloadOperations) ValidateWorkloadObservation(d nativehost.SelectedPaaSWorkloadDeployment, observation nativehost.SelectedPaaSWorkloadObservation) error {
	return o.native.ValidateWorkloadObservation(d, observation)
}

var _ nativehost.SelectedPaaSWorkloadOperations = (*DokployWorkloadOperations)(nil)
