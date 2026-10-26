package opentofu

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/tofu"
)

const (
	CoolifyProviderModulesEnv = "STACKKIT_COOLIFY_PROVIDER_MODULES"
	CoolifyProviderImportEnv  = "STACKKIT_COOLIFY_PROVIDER_IMPORT_ID"
	coolifyProviderExecution  = "coolify-provider"
)

// CoolifyWorkloadOperations retains the API owner except for explicitly
// imported Vault and Files workloads. Both configuration and activation use the pinned
// provider; the node's existing native observer remains authoritative.
type CoolifyWorkloadOperations struct {
	runtime   Runtime
	fallback  nativehost.SelectedPaaSWorkloadOperations
	validator nativehost.SelectedPaaSWorkloadObservationValidator
}

func NewCoolifyWorkloadOperations(runtime Runtime) (*CoolifyWorkloadOperations, error) {
	fallback, err := nativehost.NewOSCoolifyWorkloadOperations(runtime.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	validator, ok := fallback.(nativehost.SelectedPaaSWorkloadObservationValidator)
	if !ok {
		return nil, errors.New("Coolify provider requires the native semantic observation validator")
	}
	return &CoolifyWorkloadOperations{runtime: runtime, fallback: fallback, validator: validator}, nil
}

func coolifyProviderOptIn(module string) (bool, error) {
	selected := false
	for _, entry := range strings.Split(os.Getenv(CoolifyProviderModulesEnv), ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if entry != "stackkits-vaultwarden-runtime" && entry != "stackkits-cloudreve-runtime" {
			return false, fmt.Errorf("%s admits only stackkits-vaultwarden-runtime or stackkits-cloudreve-runtime", CoolifyProviderModulesEnv)
		}
		selected = selected || entry == module
	}
	return selected, nil
}

func (o *CoolifyWorkloadOperations) ApplyWorkload(ctx context.Context, deployment nativehost.SelectedPaaSWorkloadDeployment) (nativehost.SelectedPaaSApplyReceipt, error) {
	var empty nativehost.SelectedPaaSApplyReceipt
	selected, err := coolifyProviderOptIn(deployment.ModuleRef)
	if err != nil {
		return empty, err
	}
	workspace, err := filepath.Abs(o.runtime.WorkspaceRoot)
	if err != nil {
		return empty, err
	}
	project := "stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef
	relative, err := WorkloadRootRelativePath(project)
	if err != nil {
		return empty, err
	}
	root := filepath.Join(workspace, filepath.FromSlash(relative))
	if ctx == nil || selected && !nativehost.GenerationTargetExecutesOpenTofu(deployment.GenerationTarget) {
		return empty, errors.New("Coolify workload requires a context; provider opt-in requires the opentofu or terramate target")
	}
	// Serialize both provider and retained API ownership under one node-project
	// lock, before reading the marker or custody. OpenTofu command locks alone
	// cannot serialize preparation, asynchronous activation and observation.
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
	workloadLock, err := tx.TryAcquireOutputLock(filepath.ToSlash(filepath.Dir(relative)))
	if err != nil {
		return empty, err
	}
	defer workloadLock.Release()
	providerRoot := false
	if _, err := os.Lstat(filepath.Join(root, MarkerFile)); err == nil {
		marker, err := ReadRootMarker(root)
		if err != nil {
			return empty, err
		}
		providerRoot = marker.Execution == coolifyProviderExecution
		if selected && !providerRoot {
			return empty, errors.New("existing OpenTofu workload requires an explicit manager transition before Coolify provider Apply")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return empty, err
	}
	if !selected {
		if providerRoot {
			return empty, errors.New("Coolify provider owns this workload; retain its opt-in or explicitly transition it before API Apply")
		}
		return o.fallback.ApplyWorkload(ctx, deployment)
	}
	if deployment.ModuleRef == "stackkits-cloudreve-runtime" {
		if err := admitCoolifyFilesDeployment(deployment); err != nil {
			return empty, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	prior, err := platformdeploy.LoadOwnerHostDeployment(workspace, project)
	if err != nil {
		return empty, errors.New("Coolify provider requires an explicitly imported, signed existing service; fresh creation remains with the retained adapter")
	}
	if err := platformdeploy.RequireHostDeploymentManager(prior, "coolify"); err != nil {
		return empty, err
	}
	_, stateErr := os.Lstat(filepath.Join(root, StateFile))
	hasState := stateErr == nil
	if stateErr != nil && !errors.Is(stateErr, os.ErrNotExist) {
		return empty, stateErr
	}
	importID := os.Getenv(CoolifyProviderImportEnv)
	if importID != "" && (hasState || importID != prior.Deployment.ExternalID) {
		return empty, errors.New("Coolify explicit import requires its exact signed held ID and no OpenTofu state")
	}
	if !hasState && importID == "" {
		return empty, fmt.Errorf("first Coolify provider Apply requires %s matching signed node custody", CoolifyProviderImportEnv)
	}
	if hasState && !providerRoot {
		return empty, errors.New("Coolify state has no matching provider root marker")
	}
	binary, providers, err := o.runtime.packagedTools()
	if err != nil {
		return empty, err
	}
	if err := o.runtime.requireStateCustody(ctx, relative, binary, providers.Directory()); err != nil {
		return empty, err
	}
	prepared, err := nativehost.PrepareCoolifyProviderWorkload(ctx, workspace, deployment)
	if err != nil {
		return empty, err
	}
	if prepared.Prior.Deployment.ExternalID != prior.Deployment.ExternalID || prepared.Prior.ServerID != prior.ServerID || prepared.Prior.ArtifactDigest != prior.ArtifactDigest {
		return empty, errors.New("Coolify signed workload custody changed during provider preparation")
	}
	if prepared.Prepared.ProjectName != project || prepared.Prepared.Directory != filepath.Dir(root) || prepared.Prepared.EnvFile != EnvFile {
		return empty, errors.New("Coolify provider preparation differs from the exact workload root")
	}
	config, err := architecturev2renderer.RenderCoolifyWorkloadOpenTofu(architecturev2renderer.CoolifyWorkloadSpec{
		ProjectName: project, ProjectID: prepared.Config.ProjectUUID, ServerID: prepared.Config.ServerID,
		ServiceID: prior.Deployment.ExternalID, Compose: string(prepared.Compose), ArtifactDigest: deployment.ArtifactDigest, EnvironmentDigest: prepared.EnvironmentDigest,
	})
	if err != nil {
		return empty, err
	}
	lock, err := providers.LockForConfiguration(config)
	if err != nil {
		return empty, err
	}
	root, err = ensureRootDir(workspace, relative)
	if err != nil {
		return empty, err
	}
	if err := writeRoot(root, RootMarker{SchemaVersion: RootMarkerSchemaVersion, ModuleRef: deployment.ModuleRef,
		InstanceRef: deployment.InstanceRef, RuntimeDir: project, ComposeProject: project, Kind: RootKindWorkload, Execution: coolifyProviderExecution,
	}, providers.Directory(), lock, rootFile{ConfigFile, config, 0o640}); err != nil {
		return empty, err
	}
	current, err := prepared.RuntimeCurrent(ctx)
	if err != nil {
		return empty, err
	}
	environment := []string{"COOLIFY_ENDPOINT=" + prepared.Config.BaseURL, "COOLIFY_TOKEN=" + prepared.Config.Token,
		"COOLIFY_INSECURE=false", "COOLIFY_CA_CERT=", "COOLIFY_CF_ACCESS_CLIENT_ID=", "COOLIFY_CF_ACCESS_CLIENT_SECRET="}
	taintActivation := !current && !prepared.ActivationPending()
	if err := o.runtime.prepareCoolifyProviderState(ctx, relative, root, binary, environment, importID, taintActivation); err != nil {
		return empty, err
	}
	runtime := o.runtime
	priorAdmission := runtime.PlanAdmission
	activated := false
	runtime.PlanAdmission = func(ctx context.Context, runner *tofu.Executor, plan string) error {
		if priorAdmission != nil {
			if err := priorAdmission(ctx, runner, plan); err != nil {
				return err
			}
		}
		plannedActivation, err := admitCoolifyProviderPlan(ctx, runner, plan, prior.Deployment.ExternalID, prepared.ActivationPending())
		if err != nil {
			return err
		}
		if plannedActivation {
			sourceIDs, err := prepared.ActivationSource(ctx)
			if err != nil {
				return err
			}
			if err := prepared.BeginActivation(deployment, sourceIDs); err != nil {
				return err
			}
		}
		activated = plannedActivation
		return nil
	}
	run, err := runtime.runRoot(ctx, relative, root, binary, environment)
	if err != nil {
		return empty, err
	}
	if err := prepared.Complete(ctx, deployment, activated); err != nil {
		return empty, err
	}
	if _, err := writeObservation(root, workloadObservation{SchemaVersion: workloadObservationSchemaVersion,
		ModuleRef: deployment.ModuleRef, InstanceRef: deployment.InstanceRef, ArtifactID: deployment.ArtifactRef, ArtifactDigest: deployment.ArtifactDigest,
		Root: relative, ComposeProject: project, ComposeSHA256: digestBytes(prepared.Prepared.Compose), Execution: coolifyProviderExecution, tofuRun: run,
	}); err != nil {
		return empty, err
	}
	return nativehost.SelectedPaaSApplyReceipt{InstanceRef: deployment.InstanceRef, ArtifactDigest: deployment.ArtifactDigest, Status: "applied"}, nil
}

// The action resource has a no-op Read. A stopped or stale runtime therefore
// explicitly taints only that action, leaving the service and data in place.
func (r Runtime) prepareCoolifyProviderState(ctx context.Context, relative, root, binary string, environment []string, importID string, activate bool) error {
	return r.withStateCustody(relative, func(key []byte) error {
		runner := tofu.NewExecutor(tofu.WithWorkDir(root), tofu.WithBinary(binary),
			tofu.WithoutInheritedEnv(tofu.OfflineInheritedEnv...), tofu.WithoutEnvPrefix("TF_LOG", "TF_CLI_ARGS_"),
			tofu.WithEnv(append(append([]string(nil), environment...), "TF_CLI_CONFIG_FILE="+filepath.Join(root, CLIConfigFile), "TF_CLI_ARGS=-no-color", "TF_ENCRYPTION="+openTofuEncryptionConfig(key, false))...))
		result, err := runner.InitReadonly(ctx)
		stepErr := requireTofuStep("Coolify provider init", result, err)
		discardTofuOutput(result)
		if stepErr != nil {
			return stepErr
		}
		if importID != "" {
			result, err = runner.Import(ctx, "coolify_service.workload", importID)
			stepErr = requireTofuStep("Coolify explicit import", result, err)
			discardTofuOutput(result)
			if stepErr != nil {
				return stepErr
			}
		}
		if activate {
			result, err = runner.State(ctx)
			stepErr = requireTofuStep("Coolify activation state", result, err)
			present := result != nil && strings.Contains("\n"+strings.TrimSpace(result.Stdout)+"\n", "\ncoolify_resource_action.activate\n")
			discardTofuOutput(result)
			if stepErr != nil {
				return stepErr
			}
			if present {
				result, err = runner.Taint(ctx, "coolify_resource_action.activate")
				stepErr = requireTofuStep("Coolify activation reconciliation", result, err)
				discardTofuOutput(result)
				if stepErr != nil {
					return stepErr
				}
			}
		}
		_, err = protectState(root)
		return err
	})
}

func (o *CoolifyWorkloadOperations) ObserveWorkload(ctx context.Context, deployment nativehost.SelectedPaaSWorkloadDeployment) (nativehost.SelectedPaaSWorkloadObservation, error) {
	relative, err := WorkloadRootRelativePath("stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef)
	if err != nil {
		return nativehost.SelectedPaaSWorkloadObservation{}, err
	}
	root := filepath.Join(o.runtime.WorkspaceRoot, filepath.FromSlash(relative))
	if _, err := os.Lstat(filepath.Join(root, MarkerFile)); err == nil {
		marker, err := ReadRootMarker(root)
		if err != nil {
			return nativehost.SelectedPaaSWorkloadObservation{}, err
		}
		if marker.Execution == coolifyProviderExecution {
			if deployment.ModuleRef == "stackkits-cloudreve-runtime" {
				if err := admitCoolifyFilesDeployment(deployment); err != nil {
					return nativehost.SelectedPaaSWorkloadObservation{}, err
				}
			}
			return nativehost.ObserveCoolifyProviderWorkload(ctx, o.runtime.WorkspaceRoot, deployment)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nativehost.SelectedPaaSWorkloadObservation{}, err
	}
	return o.fallback.ObserveWorkload(ctx, deployment)
}

func (o *CoolifyWorkloadOperations) ValidateWorkloadObservation(deployment nativehost.SelectedPaaSWorkloadDeployment, observation nativehost.SelectedPaaSWorkloadObservation) error {
	return o.validator.ValidateWorkloadObservation(deployment, observation)
}
