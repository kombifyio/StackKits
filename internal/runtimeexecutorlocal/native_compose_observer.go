package runtimeexecutorlocal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// NativeComposeRequest binds one Core module whose Compose project another
// executor materializes, such as the OpenTofu Stage 1 wrapper root, to the
// native Apply side steps and Verify semantics. Target and Health are the
// exact runtime and post-apply targets of that executor; HealthContractHashes
// is the factory-owned trust map for their sources.
type NativeComposeRequest struct {
	Target               runtimeexecutor.RuntimeTarget
	Health               []runtimeexecutor.HealthTarget
	HealthContractHashes map[string]string
	ArtifactID           string
	ArtifactDigest       string
	// Definition is the Compose payload the other executor applies. It must
	// be the module's CUE-owned Compose artifact.
	Definition []byte
	// ComposePath is the absolute compose.yaml inside the workspace runtime
	// tree that the other executor writes; after apply it must hold exactly
	// Definition.
	ComposePath string
}

// NativeComposePreparation carries the pre-Compose results that decide the
// native follow-up steps after the other executor's `up`.
type NativeComposePreparation struct {
	moduleRef      string
	originReload   bool
	recreateServer bool
}

// NativeComposeObservation is the secret-free result of a native observation.
// Probes are keyed by the health requirement IDs of the request.
type NativeComposeObservation struct {
	ProjectRef         string                           `json:"projectRef"`
	Status             string                           `json:"status"`
	OwnerRef           string                           `json:"ownerRef"`
	PocketIDSubject    string                           `json:"pocketIdSubject"`
	OwnerBindingDigest string                           `json:"ownerBindingDigest"`
	Services           []BasementCoreServiceObservation `json:"services"`
	Probes             []BasementCoreProbeObservation   `json:"probes"`
}

// NativeComposeRuntime exposes the native Compose executor's interpolation
// environment, the Apply steps around its `up` (stackkit-server staging and
// recreation, step-ca reload, PocketID owner realization and the TinyAuth
// reconcile), and its Verify semantics. It never writes compose.yaml.
type NativeComposeRuntime interface {
	ComposeEnvironment(moduleRef string) ([]string, error)
	PrepareCompose(context.Context, NativeComposeRequest) (NativeComposePreparation, error)
	CompleteCompose(context.Context, NativeComposeRequest, NativeComposePreparation) error
	ObserveCompose(context.Context, NativeComposeRequest) (NativeComposeObservation, error)
}

// NativeComposeProject returns the runtime directory under .stackkit/runtime
// and the Docker Compose project name the native executor owns for a Core
// module. Another executor for the same module must reuse both so that it
// manages the same containers.
func NativeComposeProject(moduleRef string) (runtimeDir, projectName string, ok bool) {
	switch moduleRef {
	case basementCoreModuleRef, basementCoreLiteModuleRef:
		return "basement-core", "stackkit-basement-core", true
	case cloudCoreModuleRef:
		return "cloud-core", "stackkit-cloud-core", true
	case cloudStandaloneCoreModuleRef:
		return "cloud-core-standalone", "stackkit-cloud-core-standalone", true
	default:
		return "", "", false
	}
}

type osNativeComposeRuntime struct {
	workspaceRoot string
	basement      *osBasementCoreOperations
}

// NewOSNativeComposeRuntime grants the same fixed local Docker capability as
// the native Core operations for the construction-owned workspace.
func NewOSNativeComposeRuntime(workspaceRoot string) (NativeComposeRuntime, error) {
	operations, err := NewOSBasementCoreOperations(workspaceRoot)
	if err != nil {
		return nil, err
	}
	basement, ok := operations.(*osBasementCoreOperations)
	if !ok {
		return nil, errors.New("native Compose runtime requires the OS Basement operations")
	}
	return &osNativeComposeRuntime{workspaceRoot: basement.workspaceRoot, basement: basement}, nil
}

func (r *osNativeComposeRuntime) ComposeEnvironment(moduleRef string) ([]string, error) {
	switch {
	case isBasementCoreModule(moduleRef):
		return r.basement.environment()
	case isCloudCoreModule(moduleRef):
		operations, err := r.cloud(moduleRef)
		if err != nil {
			return nil, err
		}
		return operations.environment(), nil
	default:
		return nil, fmt.Errorf("native Compose runtime does not own module %q", moduleRef)
	}
}

func (r *osNativeComposeRuntime) PrepareCompose(ctx context.Context, request NativeComposeRequest) (NativeComposePreparation, error) {
	if err := requireNativeComposeRequest(ctx, request); err != nil {
		return NativeComposePreparation{}, err
	}
	moduleRef := request.Target.ModuleRef
	switch {
	case isBasementCoreModule(moduleRef):
		if _, err := r.basementProject(request); err != nil {
			return NativeComposePreparation{}, err
		}
		if err := r.basement.ready(ctx); err != nil {
			return NativeComposePreparation{}, err
		}
		preparation, err := r.basement.prepareApply()
		if err != nil {
			return NativeComposePreparation{}, err
		}
		return NativeComposePreparation{moduleRef: moduleRef, originReload: preparation.originReload, recreateServer: preparation.recreateServer}, nil
	case isCloudCoreModule(moduleRef):
		operations, project, err := r.cloudProject(request)
		if err != nil {
			return NativeComposePreparation{}, err
		}
		if err := operations.ready(ctx); err != nil {
			return NativeComposePreparation{}, err
		}
		recreateServer, err := operations.prepareApply(project.Definition)
		if err != nil {
			return NativeComposePreparation{}, err
		}
		return NativeComposePreparation{moduleRef: moduleRef, recreateServer: recreateServer}, nil
	default:
		return NativeComposePreparation{}, fmt.Errorf("native Compose runtime does not own module %q", moduleRef)
	}
}

func (r *osNativeComposeRuntime) CompleteCompose(ctx context.Context, request NativeComposeRequest, preparation NativeComposePreparation) error {
	if err := requireNativeComposeRequest(ctx, request); err != nil {
		return err
	}
	if preparation.moduleRef != request.Target.ModuleRef {
		return errors.New("native Compose completion differs from its preparation")
	}
	if err := r.requireAppliedDefinition(request); err != nil {
		return err
	}
	switch {
	case isBasementCoreModule(request.Target.ModuleRef):
		environment, err := r.basement.environment()
		if err != nil {
			return err
		}
		_, err = r.basement.completeApply(ctx, request.ComposePath, environment, basementApplyPreparation{
			originReload: preparation.originReload, recreateServer: preparation.recreateServer,
		})
		return err
	case isCloudCoreModule(request.Target.ModuleRef):
		operations, project, err := r.cloudProject(request)
		if err != nil {
			return err
		}
		_, err = operations.completeApply(ctx, request.ComposePath, project, preparation.recreateServer)
		return err
	default:
		return fmt.Errorf("native Compose runtime does not own module %q", request.Target.ModuleRef)
	}
}

func (r *osNativeComposeRuntime) ObserveCompose(ctx context.Context, request NativeComposeRequest) (NativeComposeObservation, error) {
	if err := requireNativeComposeRequest(ctx, request); err != nil {
		return NativeComposeObservation{}, err
	}
	if err := r.requireAppliedDefinition(request); err != nil {
		return NativeComposeObservation{}, err
	}
	switch {
	case isBasementCoreModule(request.Target.ModuleRef):
		project, err := r.basementProject(request)
		if err != nil {
			return NativeComposeObservation{}, err
		}
		if err := r.basement.ready(ctx); err != nil {
			return NativeComposeObservation{}, err
		}
		if _, err := localevidence.LoadBasementRuntimeCustody(r.workspaceRoot); err != nil {
			return NativeComposeObservation{}, fmt.Errorf("verify local Basement runtime custody before observation: %w", err)
		}
		observed, err := r.basement.observeProject(ctx, project, request.ComposePath)
		if err != nil {
			return NativeComposeObservation{}, err
		}
		if err := validateBasementCoreVerification(project, observed); err != nil {
			return NativeComposeObservation{}, err
		}
		return NativeComposeObservation{
			ProjectRef: observed.ProjectRef, Status: observed.Status,
			OwnerRef: observed.OwnerRef, PocketIDSubject: observed.PocketIDSubject,
			OwnerBindingDigest: observed.OwnerBindingDigest,
			Services:           observed.Services, Probes: observed.Probes,
		}, nil
	case isCloudCoreModule(request.Target.ModuleRef):
		operations, project, err := r.cloudProject(request)
		if err != nil {
			return NativeComposeObservation{}, err
		}
		if err := operations.ready(ctx); err != nil {
			return NativeComposeObservation{}, err
		}
		custody, err := localevidence.LoadCloudRuntimeCustody(r.workspaceRoot)
		if err != nil {
			return NativeComposeObservation{}, fmt.Errorf("verify Cloud runtime custody before observation: %w", err)
		}
		if err := requireCloudIdentityAddress(custody, project.Definition); err != nil {
			return NativeComposeObservation{}, err
		}
		observed, err := operations.observeProject(ctx, project, request.ComposePath)
		if err != nil {
			return NativeComposeObservation{}, err
		}
		if err := validateCloudCoreVerification(project, observed); err != nil {
			return NativeComposeObservation{}, err
		}
		return NativeComposeObservation{
			ProjectRef: observed.ProjectRef, Status: observed.Status,
			OwnerRef: observed.OwnerRef, PocketIDSubject: observed.PocketIDSubject,
			OwnerBindingDigest: observed.OwnerBindingDigest,
			Services:           observed.Services, Probes: observed.Probes,
		}, nil
	default:
		return NativeComposeObservation{}, fmt.Errorf("native Compose runtime does not own module %q", request.Target.ModuleRef)
	}
}

func requireNativeComposeRequest(ctx context.Context, request NativeComposeRequest) error {
	if ctx == nil {
		return errors.New("native Compose runtime requires a context")
	}
	target := request.Target
	if len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" {
		return errors.New("native Compose runtime requires one exact Site, node, and channel")
	}
	runtimeDir, _, ok := NativeComposeProject(target.ModuleRef)
	if !ok || len(request.Definition) == 0 || filepath.Base(filepath.Dir(request.ComposePath)) != runtimeDir {
		return errors.New("native Compose request does not bind the module's runtime Compose path")
	}
	return nil
}

// basementProject builds the exact native project the Basement Verify
// expects from the other executor's request.
func (r *osNativeComposeRuntime) basementProject(request NativeComposeRequest) (BasementCoreProject, error) {
	profile, _ := basementClosedLocalCoreExecutionProfileForModule(request.Target.ModuleRef)
	if !profile.validateComposeArtifact(request.Definition) {
		return BasementCoreProject{}, fmt.Errorf("Compose payload is not the CUE-owned %s artifact", profile.displayName)
	}
	if len(request.Health) != len(profile.healthSpecs) {
		return BasementCoreProject{}, fmt.Errorf("%s requires %d health targets", profile.displayName, len(profile.healthSpecs))
	}
	_, expectations, err := exactClosedLocalCoreHealth(request.Health, request.Target,
		BasementCoreAuthority{HealthContractHashes: request.HealthContractHashes}, profile)
	if err != nil {
		return BasementCoreProject{}, err
	}
	services := profile.serviceContracts()
	project := BasementCoreProject{
		ModuleRef: profile.moduleRef, ProjectRef: request.Target.InstanceRef,
		SiteRef: request.Target.SiteRefs[0], NodeRef: request.Target.NodeRefs[0],
		ExecutionChannelRef: request.Target.ExecutionChannelRef,
		ArtifactID:          request.ArtifactID, ArtifactDigest: request.ArtifactDigest,
		Definition: append([]byte(nil), request.Definition...),
		Services:   make([]BasementCoreServiceExpectation, len(services)), Health: expectations,
	}
	for index, service := range services {
		project.Services[index] = BasementCoreServiceExpectation(service)
	}
	return project, nil
}

// cloudProject builds the exact native project the Cloud Verify expects.
func (r *osNativeComposeRuntime) cloudProject(request NativeComposeRequest) (*osCloudCoreOperations, CloudCoreProject, error) {
	profile, _ := cloudCoreProfileForModule(request.Target.ModuleRef)
	if !profile.validArtifact(request.Definition) {
		return nil, CloudCoreProject{}, errors.New("Compose payload is not the CUE-owned Cloud core artifact")
	}
	_, expectations, err := exactCloudCoreProfileHealth(request.Health, request.Target,
		CloudCoreAuthority{HealthContractHashes: request.HealthContractHashes}, profile)
	if err != nil {
		return nil, CloudCoreProject{}, err
	}
	operations, err := r.cloud(request.Target.ModuleRef)
	if err != nil {
		return nil, CloudCoreProject{}, err
	}
	services := profile.services()
	project := CloudCoreProject{
		ModuleRef: profile.moduleRef(), ProjectRef: request.Target.InstanceRef,
		SiteRef: request.Target.SiteRefs[0], NodeRef: request.Target.NodeRefs[0],
		ExecutionChannelRef: request.Target.ExecutionChannelRef,
		ArtifactID:          request.ArtifactID, ArtifactDigest: request.ArtifactDigest,
		Definition: append([]byte(nil), request.Definition...),
		Services:   make([]BasementCoreServiceExpectation, len(services)), Health: expectations,
	}
	for index, service := range services {
		project.Services[index] = BasementCoreServiceExpectation(service)
	}
	return operations, project, nil
}

// requireAppliedDefinition proves that the runtime compose.yaml the other
// executor wrote holds exactly the requested definition.
func (r *osNativeComposeRuntime) requireAppliedDefinition(request NativeComposeRequest) error {
	applied, err := r.readComposeDefinition(request.ComposePath)
	if err != nil {
		return err
	}
	if !bytes.Equal(applied, request.Definition) {
		return basementCoreObservationDrift(request.Target.InstanceRef, "runtime-compose-changed", nil)
	}
	return nil
}

func (r *osNativeComposeRuntime) cloud(moduleRef string) (*osCloudCoreOperations, error) {
	runtimeDir, _, _ := NativeComposeProject(moduleRef)
	operations, err := newOSCloudCoreOperations(r.workspaceRoot, runtimeDir)
	if err != nil {
		return nil, err
	}
	cloud, ok := operations.(*osCloudCoreOperations)
	if !ok {
		return nil, errors.New("native Compose runtime requires the OS Cloud operations")
	}
	return cloud, nil
}

// readComposeDefinition reads a materialized compose.yaml that lives in the
// workspace runtime tree. Unlike the native file it may carry the 0640 mode
// the OpenTofu local_file resource writes; the confined stable read still
// rejects links, escapes, and concurrent replacement.
func (r *osNativeComposeRuntime) readComposeDefinition(composePath string) ([]byte, error) {
	if !filepath.IsAbs(composePath) || filepath.Clean(composePath) != composePath || filepath.Base(composePath) != "compose.yaml" {
		return nil, errors.New("native Compose observation requires an exact absolute compose.yaml path")
	}
	relative, err := filepath.Rel(r.workspaceRoot, composePath)
	runtimeTree := filepath.Join(".stackkit", "runtime") + string(filepath.Separator)
	if err != nil || !strings.HasPrefix(relative, runtimeTree) {
		return nil, errors.New("native Compose observation path is outside the workspace runtime tree")
	}
	root, err := confinedfs.Open(r.workspaceRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return nil, err
	}
	defer func() { _ = transaction.Close() }()
	content, info, err := transaction.ReadStableBounded(filepath.ToSlash(relative), basementCoreProcessOutputLimit)
	if err != nil {
		return nil, fmt.Errorf("read materialized Compose definition: %w", err)
	}
	if !info.Mode().IsRegular() || len(content) == 0 {
		return nil, errors.New("materialized Compose definition is not a non-empty regular file")
	}
	return content, nil
}

func isBasementCoreModule(moduleRef string) bool {
	return moduleRef == basementCoreModuleRef || moduleRef == basementCoreLiteModuleRef
}

func isCloudCoreModule(moduleRef string) bool {
	_, ok := cloudCoreProfileForModule(moduleRef)
	return ok
}

var _ NativeComposeRuntime = (*osNativeComposeRuntime)(nil)
