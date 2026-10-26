package opentofu

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/tofu"
)

const (
	KomodoProviderModulesEnv = "STACKKIT_KOMODO_PROVIDER_MODULES"
	KomodoProviderImportEnv  = "STACKKIT_KOMODO_PROVIDER_IMPORT_ID"
	komodoProviderExecution  = "komodo-provider"
	komodoActionFile         = "komodo-action.json"
)

var komodoStackIDPattern = regexp.MustCompile(`^[0-9a-f]{24}$`)

// KomodoWorkloadOperations switches explicitly selected Vault and imported
// Files workloads to provider configuration and the ordered API lifecycle
// bridge. Other workloads retain the existing adapter.
type KomodoWorkloadOperations struct {
	runtime  Runtime
	prepared nativehost.NativeWorkloadComposeOperations
	fallback nativehost.SelectedPaaSWorkloadOperations
}

func NewKomodoWorkloadOperations(runtime Runtime) (*KomodoWorkloadOperations, error) {
	prepared, err := nativehost.NewOSKomodoWorkloadPreparation(runtime.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	fallback, err := nativehost.NewOSKomodoWorkloadOperations(runtime.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	return &KomodoWorkloadOperations{runtime: runtime, prepared: prepared, fallback: fallback}, nil
}

func komodoProviderOptIn(module string) (bool, error) {
	selected := false
	for _, entry := range strings.Split(os.Getenv(KomodoProviderModulesEnv), ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, admitted := komodoProviderIdentity(entry); !admitted {
			return false, fmt.Errorf("%s admits only %s or %s", KomodoProviderModulesEnv, komodoVaultModule, komodoFilesModule)
		}
		selected = selected || entry == module
	}
	return selected, nil
}

func (o *KomodoWorkloadOperations) ApplyWorkload(ctx context.Context, deployment nativehost.SelectedPaaSWorkloadDeployment) (nativehost.SelectedPaaSApplyReceipt, error) {
	var empty nativehost.SelectedPaaSApplyReceipt
	selected, err := komodoProviderOptIn(deployment.ModuleRef)
	if err != nil {
		return empty, err
	}
	if selected {
		if err := requireKomodoProviderDeployment(deployment); err != nil {
			return empty, err
		}
		if err := requireKomodoProcessPlatform(); err != nil {
			return empty, err
		}
	}
	project := "stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef
	relative, err := WorkloadRootRelativePath(project)
	if err != nil {
		return empty, err
	}
	workspace, err := filepath.Abs(o.runtime.WorkspaceRoot)
	if err != nil {
		return empty, err
	}
	root := filepath.Join(workspace, filepath.FromSlash(relative))
	// Every provider, Compose transition and recovery uses this same project
	// key. Read custody only after acquiring it, and hold it through completion.
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
	projectLock, err := tx.TryAcquireOutputLock(filepath.ToSlash(filepath.Dir(relative)))
	if err != nil {
		return empty, err
	}
	defer projectLock.Release()
	markerPresent := false
	if _, err := os.Lstat(filepath.Join(root, MarkerFile)); err == nil {
		marker, err := ReadRootMarker(root)
		if err != nil {
			return empty, err
		}
		markerPresent = marker.Execution == komodoProviderExecution
		if selected && markerPresent && (!isKomodoProviderRoot(marker) || marker.ModuleRef != deployment.ModuleRef || marker.InstanceRef != deployment.InstanceRef || marker.ComposeProject != project) {
			return empty, errors.New("Komodo provider root differs from the authorized workload identity")
		}
		if selected && !markerPresent {
			return empty, errors.New("existing OpenTofu workload requires an explicit manager transition before Komodo provider Apply")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return empty, err
	}
	if !selected {
		if markerPresent {
			return empty, errors.New("Komodo provider owns this workload; retain its explicit opt-in or transition it before using the HTTP adapter")
		}
		return o.fallback.ApplyWorkload(ctx, deployment)
	}
	if ctx == nil || !nativehost.GenerationTargetExecutesOpenTofu(deployment.GenerationTarget) {
		return empty, errors.New("Komodo provider opt-in requires a context and the opentofu or terramate generation target")
	}
	if _, err := localevidence.LoadOwnerCustody(workspace); err != nil {
		return empty, err
	}
	binary, providers, err := o.runtime.packagedTools()
	if err != nil {
		return empty, err
	}
	if err := o.runtime.requireStateCustody(ctx, relative, binary, providers.Directory()); err != nil {
		return empty, err
	}
	cfg, err := platformdeploy.LoadOwnerNativeKomodoConfig(ctx, workspace)
	if err != nil {
		return empty, err
	}
	if err := requireKomodoProcessPlatform(); err != nil {
		return empty, fmt.Errorf("Komodo provider action lease unavailable: %w", err)
	}
	lease, err := projectLock.Borrow(tx, filepath.ToSlash(filepath.Dir(relative)))
	if err != nil {
		return empty, err
	}
	defer lease.Close()
	executionRuntime := o.runtime
	executionRuntime.komodoLease = lease
	executionRuntime.WorkspaceRoot = workspace
	actionLeases, err := NewKomodoActionLeases(lease, workspace, project)
	if err != nil {
		return empty, err
	}
	defer actionLeases.Close()
	environment, err := komodoProviderEnvironment(cfg)
	if err != nil {
		return empty, err
	}
	environment = append(environment, actionLeases.Environment()...)
	adapter := platformdeploy.NewKomodoAdapter(cfg)
	importID := os.Getenv(KomodoProviderImportEnv)
	if os.Getenv(KomodoProviderRecoverIDEnv) != "" || os.Getenv(KomodoProviderRecoverFromEnv) != "" {
		if err := o.recoverKomodoOwnership(ctx, deployment, root, cfg); err != nil {
			return empty, err
		}
		if _, err := os.Lstat(filepath.Join(root, StateFile)); errors.Is(err, os.ErrNotExist) {
			importID = os.Getenv(KomodoProviderRecoverIDEnv)
		}
	}
	prior, heldID, err := o.requireKomodoOwnership(ctx, deployment, project, root, cfg, markerPresent, importID)
	if err != nil {
		return empty, err
	}
	// Preflight precedes provider refresh/import, which can copy remote secrets
	// into state and can adopt a missing ID by name in the upstream provider.
	if heldID != "" {
		if err := adapter.RequireProviderHostStack(ctx, prior.Deployment, filepath.Dir(root)); err != nil {
			return empty, err
		}
	} else if err := adapter.RequireAbsentProviderStack(ctx, project); err != nil {
		return empty, err
	}
	prior, err = o.resumeKomodoObservedCustody(ctx, workspace, root, prior)
	if err != nil {
		return empty, err
	}
	source, err := komodoUpdateSource(workspace, root, prior, deployment)
	if err != nil {
		return empty, err
	}
	prepared, recipeFiles, err := nativehost.StageKomodoProviderRecipe(ctx, o.prepared, deployment)
	if err != nil {
		return empty, err
	}
	defer clearKomodoFiles(recipeFiles)
	if prepared.ProjectName != project || prepared.Directory != filepath.Dir(root) || prepared.EnvFile != EnvFile {
		return empty, errors.New("Komodo provider preparation differs from its exact node project")
	}
	config, err := architecturev2renderer.RenderKomodoWorkloadOpenTofu(architecturev2renderer.KomodoWorkloadSpec{
		ProjectName: project, Directory: prepared.Directory, ServerID: cfg.ServerID, HeldID: heldID,
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
	intent, err := newKomodoActionIntent(workspace, deployment, prepared, recipeFiles[EnvFile], cfg.ServerID, heldID, prior)
	if err != nil {
		return empty, err
	}
	intentBytes, err := json.Marshal(intent)
	if err != nil {
		return empty, err
	}
	// Retain the applied source and authenticate the pending target before
	// replacing any recipe, environment, config file or helper intent.
	if err := journalKomodoUpdate(workspace, root, source, prior, intent, deployment.Bundle, recipeFiles); err != nil {
		return empty, err
	}
	if err := nativehost.PersistKomodoProviderRecipe(o.prepared, prepared); err != nil {
		return empty, err
	}
	if err := writeRoot(root, RootMarker{
		SchemaVersion: RootMarkerSchemaVersion, ModuleRef: deployment.ModuleRef, InstanceRef: deployment.InstanceRef,
		RuntimeDir: project, ComposeProject: project, Kind: RootKindWorkload, Execution: komodoProviderExecution,
	}, providers.Directory(), lock, rootFile{ConfigFile, config, 0o640}, rootFile{komodoActionFile, intentBytes, 0o600}); err != nil {
		return empty, err
	}
	if importID != "" {
		if err := executionRuntime.importKomodoStack(ctx, relative, root, binary, environment, importID); err != nil {
			return empty, err
		}
	}
	runtime := executionRuntime
	previousAdmission := runtime.PlanAdmission
	admit := KomodoPlanAdmission(heldID, project, cfg.ServerID, prepared.Directory)
	runtime.PlanAdmission = func(ctx context.Context, runner *tofu.Executor, plan string) error {
		if previousAdmission != nil {
			if err := previousAdmission(ctx, runner, plan); err != nil {
				return err
			}
		}
		if err := admit(ctx, runner, plan); err != nil {
			return err
		}
		if heldID == "" {
			return adapter.RequireAbsentProviderStack(ctx, project)
		}
		return adapter.RequireProviderHostStack(ctx, platformdeploy.DeploymentRef{Platform: "komodo", AppName: project, ExternalID: heldID}, prepared.Directory)
	}
	run, err := runtime.runRoot(ctx, relative, root, binary, environment)
	if err != nil {
		return empty, err
	}
	state, err := platformdeploy.LoadOwnerHostDeployment(workspace, project)
	if err != nil {
		return empty, errors.New("Komodo provider apply has no independently held deployment ID")
	}
	if err := adapter.RequireProviderHostStack(ctx, state.Deployment, prepared.Directory); err != nil {
		return empty, err
	}
	if _, err := adapter.ObserveDeployment(ctx, state.Deployment); err != nil {
		return empty, errors.New("Komodo provider workload is not observed running")
	}
	// A no-op plan does not launch the helper. Retain its current signed intent
	// too, so recording the held ID cannot look like an unfinished update later.
	if err := commitKomodoApplied(workspace, root, intent); err != nil {
		return empty, err
	}
	if err := o.prepared.CompleteWorkloadCompose(ctx, prepared); err != nil {
		return empty, err
	}
	if _, err := o.ObserveWorkload(ctx, deployment); err != nil {
		return empty, err
	}
	if _, err := writeObservation(root, workloadObservation{
		SchemaVersion: workloadObservationSchemaVersion, ModuleRef: deployment.ModuleRef, InstanceRef: deployment.InstanceRef,
		ArtifactID: deployment.ArtifactRef, ArtifactDigest: deployment.ArtifactDigest, Root: relative,
		ComposeProject: project, ComposeSHA256: digestBytes(prepared.Compose), Execution: komodoProviderExecution, tofuRun: run,
	}); err != nil {
		return empty, err
	}
	return nativehost.SelectedPaaSApplyReceipt{InstanceRef: deployment.InstanceRef, ArtifactDigest: deployment.ArtifactDigest, Status: "applied"}, nil
}

func (o *KomodoWorkloadOperations) requireKomodoOwnership(ctx context.Context, deployment nativehost.SelectedPaaSWorkloadDeployment, project, root string, cfg platformdeploy.HTTPConfig, providerRoot bool, importID string) (platformdeploy.HostDeployment, string, error) {
	var empty platformdeploy.HostDeployment
	present, err := platformdeploy.HasOwnerHostDeployment(o.runtime.WorkspaceRoot, project)
	if err != nil {
		return empty, "", err
	}
	_, stateErr := os.Lstat(filepath.Join(root, StateFile))
	hasState := stateErr == nil
	if stateErr != nil && !errors.Is(stateErr, os.ErrNotExist) {
		return empty, "", stateErr
	}
	if importID != "" && (!komodoStackIDPattern.MatchString(importID) || hasState) {
		return empty, "", errors.New("explicit Komodo import requires a valid exact ID and no existing OpenTofu state")
	}
	if present {
		prior, err := platformdeploy.LoadOwnerHostDeployment(o.runtime.WorkspaceRoot, project)
		if err != nil {
			return empty, "", err
		}
		if err := platformdeploy.RequireHostDeploymentManager(prior, "komodo"); err != nil {
			return empty, "", err
		}
		if prior.Manager != "komodo" || prior.Signature == nil {
			return empty, "", errors.New("legacy Komodo custody requires an explicit signed manager transition before provider import")
		}
		if prior.WorkloadRef != deployment.WorkloadRef || prior.InstanceRef != deployment.InstanceRef || prior.NodeRef != deployment.NodeRef || prior.SiteRef != deployment.SiteRef || prior.ServerID != cfg.ServerID ||
			!komodoStackIDPattern.MatchString(prior.Deployment.ExternalID) {
			return empty, "", errors.New("Komodo held deployment belongs to another target; explicit transition is required")
		}
		if (!providerRoot || !hasState) && importID != prior.Deployment.ExternalID {
			return empty, "", fmt.Errorf("existing workload requires explicit %s matching its held ID", KomodoProviderImportEnv)
		}
		return prior, prior.Deployment.ExternalID, nil
	}
	if deployment.ModuleRef == komodoFilesModule {
		return empty, "", errors.New("Komodo Files provider requires existing signed deployment custody and explicit exact-ID import; fresh allocation is not admitted")
	}
	if hasState || importID != "" {
		return empty, "", errors.New("Komodo provider state/import requires independently signed node deployment custody; transition the existing workload first")
	}
	// Observe only; never create or remove Docker workloads. A project with no
	// platform claim still cannot be silently taken from another manager.
	if err := nativehost.RequireAbsentKomodoWorkloadManager(ctx, o.prepared, project); err != nil {
		return empty, "", err
	}
	return empty, "", nil
}

func komodoProviderEnvironment(cfg platformdeploy.HTTPConfig) ([]string, error) {
	binary := SecretExecBinary()
	if binary == "" {
		return nil, errors.New("Komodo lifecycle bridge requires the installed stackkit binary")
	}
	return []string{"KOMODO_ENDPOINT=" + cfg.BaseURL, "KOMODO_API_KEY=" + cfg.APIKey, "KOMODO_API_SECRET=" + cfg.Secret,
		"KOMODO_USERNAME=", "KOMODO_PASSWORD=", "STACKKIT_KOMODO_BRIDGE_BINARY=" + binary}, nil
}

func (r Runtime) importKomodoStack(ctx context.Context, relative, root, binary string, environment []string, id string) error {
	owner, err := r.komodoProcessOwner(root)
	if err != nil {
		return err
	}
	return r.withStateCustody(relative, func(key []byte) error {
		runner := tofu.NewExecutor(tofu.WithProcessCustody(owner), tofu.WithWorkDir(root), tofu.WithBinary(binary),
			tofu.WithoutInheritedEnv(tofu.OfflineInheritedEnv...), tofu.WithoutEnvPrefix("TF_LOG", "TF_CLI_ARGS_"),
			tofu.WithEnv(append(environment, "TF_CLI_CONFIG_FILE="+filepath.Join(root, CLIConfigFile), "TF_CLI_ARGS=-no-color", "TF_ENCRYPTION="+openTofuEncryptionConfig(key, false))...))
		result, err := runner.InitReadonly(ctx)
		stepErr := requireTofuStep("Komodo import init", result, err)
		discardTofuOutput(result)
		if stepErr != nil {
			return stepErr
		}
		result, err = runner.Import(ctx, "komodo_stack.workload", id)
		stepErr = requireTofuStep("Komodo explicit import", result, err)
		discardTofuOutput(result)
		if stepErr != nil {
			return stepErr
		}
		_, err = protectState(root)
		return err
	})
}

func (o *KomodoWorkloadOperations) ObserveWorkload(ctx context.Context, deployment nativehost.SelectedPaaSWorkloadDeployment) (nativehost.SelectedPaaSWorkloadObservation, error) {
	project := "stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef
	relative, err := WorkloadRootRelativePath(project)
	if err != nil {
		return nativehost.SelectedPaaSWorkloadObservation{}, err
	}
	root := filepath.Join(o.runtime.WorkspaceRoot, filepath.FromSlash(relative))
	provider := false
	if _, err := os.Lstat(filepath.Join(root, MarkerFile)); err == nil {
		marker, err := ReadRootMarker(root)
		if err != nil {
			return nativehost.SelectedPaaSWorkloadObservation{}, err
		}
		provider = marker.Execution == komodoProviderExecution
		if provider {
			if err := requireKomodoProviderDeployment(deployment); err != nil {
				return nativehost.SelectedPaaSWorkloadObservation{}, err
			}
			held, err := platformdeploy.LoadOwnerHostDeployment(o.runtime.WorkspaceRoot, project)
			if err != nil || !komodoProviderRootMatches(marker, held) || marker.ModuleRef != deployment.ModuleRef || marker.InstanceRef != deployment.InstanceRef {
				return nativehost.SelectedPaaSWorkloadObservation{}, errors.New("Komodo provider observation differs from its authenticated workload root")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nativehost.SelectedPaaSWorkloadObservation{}, err
	}
	observation, err := o.fallback.ObserveWorkload(ctx, deployment)
	if err != nil && provider {
		return nativehost.SelectedPaaSWorkloadObservation{}, errors.New("Komodo provider workload did not pass independent API/container/route observation")
	}
	return observation, err
}

func (o *KomodoWorkloadOperations) ValidateWorkloadObservation(deployment nativehost.SelectedPaaSWorkloadDeployment, observation nativehost.SelectedPaaSWorkloadObservation) error {
	return o.prepared.ValidateWorkloadObservation(deployment, observation)
}

// KomodoActionLeases composes the shared single-recipient primitive for each
// bounded lifecycle action. All three borrow the SAME parent project flock;
// the service itself is shared with other executors and remains unmodified.
// The capability/socket values enter only process environment, never recipes,
// signed intent, configuration digests, state, argv or diagnostics.
type KomodoActionLeases struct {
	servers     []*actionLeaseServer
	environment []string
}

func NewKomodoActionLeases(lease *confinedfs.OutputLockLease, workspace, project string) (*KomodoActionLeases, error) {
	if err := requireKomodoProcessPlatform(); err != nil {
		return nil, fmt.Errorf("Komodo provider action lease unavailable: %w", err)
	}
	if lease == nil || !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace || !runtimeDirPattern.MatchString(project) {
		return nil, errors.New("Komodo action leases require the exact held workspace/project lock")
	}
	exact, err := confinedfs.OutputLockRootsEqual(lease.OutputRoot(), ".stackkit/runtime/applications/"+project)
	if err != nil || !exact {
		return nil, errors.New("Komodo action lease belongs to another project")
	}
	set := &KomodoActionLeases{}
	for _, action := range []string{"DEPLOY", "DESTROY", "VERIFY_DESTROY"} {
		random := make([]byte, 32)
		if _, err := rand.Read(random); err != nil {
			set.Close()
			return nil, err
		}
		capability := hex.EncodeToString(random)
		clear(random)
		server, err := newActionLeaseServer(lease, workspace, project, capability)
		if err != nil {
			set.Close()
			return nil, err
		}
		set.servers = append(set.servers, server)
		prefix := "STACKKIT_KOMODO_" + action
		set.environment = append(set.environment, prefix+"_CAPABILITY="+capability, prefix+"_LEASE_SOCKET="+server.Endpoint())
	}
	return set, nil
}
func (s *KomodoActionLeases) Environment() []string { return append([]string(nil), s.environment...) }
func (s *KomodoActionLeases) Close() {
	for n := len(s.servers) - 1; n >= 0; n-- {
		s.servers[n].Close()
	}
	s.servers = nil
	s.environment = nil
}
