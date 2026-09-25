package nativehost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// NativeComposeRootSteps runs the native Core Apply side steps around a Core
// Compose payload that a coordinated rollback re-applies through its restored
// OpenTofu root (`tofu apply -replace`), where no runtime execution request
// exists. They are the steps the runtime executor runs around its own `tofu
// apply` (PrepareCompose and CompleteCompose): the origin provisioner check
// and stackkit-server staging before, and the stackkit-server recreation,
// step-ca reload, PocketID owner realization and the TinyAuth reconciling
// `up` after. Health probes are not part of the completion; the joined
// rollback verify proves them.
type NativeComposeRootSteps struct {
	runtime     *osNativeComposeRuntime
	moduleRef   string
	composePath string
	definition  []byte
	preparation NativeComposePreparation
}

// PrepareNativeComposeRoot runs the pre-`up` side steps for the Core module
// whose runtime compose.yaml the restored root carries. serverExecutable is
// the stackkit executable of the release the rollback restores; the
// stackkit-server shipped beside it is staged, as the Apply of that release
// staged its own.
func PrepareNativeComposeRoot(ctx context.Context, workspaceRoot, moduleRef, serverExecutable string) (*NativeComposeRootSteps, error) {
	if ctx == nil {
		return nil, errors.New("native Compose root steps require a context")
	}
	runtimeDir, _, ok := NativeComposeProject(moduleRef)
	if !ok {
		return nil, fmt.Errorf("native Compose runtime does not own module %q", moduleRef)
	}
	if strings.TrimSpace(serverExecutable) == "" {
		return nil, errors.New("native Compose root steps require the restored release executable")
	}
	server, err := stackKitServerBinaryBeside(serverExecutable)
	if err != nil {
		return nil, err
	}
	operations, err := NewOSNativeComposeRuntime(workspaceRoot)
	if err != nil {
		return nil, err
	}
	runtime, ok := operations.(*osNativeComposeRuntime)
	if !ok {
		return nil, errors.New("native Compose root steps require the OS runtime")
	}
	runtime.basement.serverBinary = func() (string, error) { return server, nil }
	runtime.cloudServerBinary = runtime.basement.serverBinary
	return prepareNativeComposeRoot(ctx, runtime, moduleRef, runtimeDir)
}

func prepareNativeComposeRoot(ctx context.Context, runtime *osNativeComposeRuntime, moduleRef, runtimeDir string) (*NativeComposeRootSteps, error) {
	var err error
	steps := &NativeComposeRootSteps{
		runtime: runtime, moduleRef: moduleRef,
		composePath: filepath.Join(runtime.workspaceRoot, ".stackkit", "runtime", runtimeDir, "compose.yaml"),
	}
	if steps.definition, err = runtime.readComposeDefinition(steps.composePath); err != nil {
		return nil, err
	}
	if err := steps.requireModuleArtifact(); err != nil {
		return nil, err
	}
	switch {
	case isBasementCoreModule(moduleRef):
		if err := runtime.basement.ready(ctx); err != nil {
			return nil, err
		}
		preparation, err := runtime.basement.prepareApply()
		if err != nil {
			return nil, err
		}
		steps.preparation = NativeComposePreparation{moduleRef: moduleRef, originReload: preparation.originReload, recreateServer: preparation.recreateServer}
	default:
		cloud, err := runtime.cloud(moduleRef)
		if err != nil {
			return nil, err
		}
		if err := cloud.ready(ctx); err != nil {
			return nil, err
		}
		recreateServer, err := cloud.prepareApply(steps.definition)
		if err != nil {
			return nil, err
		}
		steps.preparation = NativeComposePreparation{moduleRef: moduleRef, recreateServer: recreateServer}
	}
	return steps, nil
}

// Complete runs the post-`up` side steps after the forced apply, against the
// runtime compose.yaml the restored root wrote. It refuses a payload that
// differs from the one prepared.
func (s *NativeComposeRootSteps) Complete(ctx context.Context) error {
	if ctx == nil || s == nil || s.runtime == nil || s.preparation.moduleRef != s.moduleRef {
		return errors.New("native Compose root completion requires its preparation")
	}
	applied, err := s.runtime.readComposeDefinition(s.composePath)
	if err != nil {
		return err
	}
	if !bytes.Equal(applied, s.definition) {
		return errors.New("the forced apply wrote a Core Compose payload that differs from the restored checkpoint")
	}
	if isBasementCoreModule(s.moduleRef) {
		environment, err := s.runtime.basement.environment()
		if err != nil {
			return err
		}
		_, err = s.runtime.basement.completeApply(ctx, s.composePath, environment, basementApplyPreparation{
			originReload: s.preparation.originReload, recreateServer: s.preparation.recreateServer,
		})
		return err
	}
	cloud, err := s.runtime.cloud(s.moduleRef)
	if err != nil {
		return err
	}
	profile, _ := cloudCoreProfileForModule(s.moduleRef)
	services := profile.services()
	project := CloudCoreProject{
		ModuleRef: profile.moduleRef(), Definition: append([]byte(nil), s.definition...),
		Services: make([]BasementCoreServiceExpectation, len(services)),
	}
	for index, service := range services {
		project.Services[index] = BasementCoreServiceExpectation(service)
	}
	_, err = cloud.completeApply(ctx, s.composePath, project, s.preparation.recreateServer)
	return err
}

func (s *NativeComposeRootSteps) requireModuleArtifact() error {
	if isBasementCoreModule(s.moduleRef) {
		profile, _ := basementClosedLocalCoreExecutionProfileForModule(s.moduleRef)
		if !profile.validateComposeArtifact(s.definition) {
			return fmt.Errorf("restored Compose payload is not the CUE-owned %s artifact", profile.displayName)
		}
		return nil
	}
	profile, ok := cloudCoreProfileForModule(s.moduleRef)
	if !ok || !profile.validArtifact(s.definition) {
		return errors.New("restored Compose payload is not the CUE-owned Cloud core artifact")
	}
	return nil
}

// CompleteRestoredWorkloadCompose runs the native completion of one
// standalone workload whose Compose project a coordinated rollback restored
// and re-applied through its OpenTofu root: the game node's Wings
// recreation, the blocking-component and application HTTP readiness waits
// and the routed origin backend record, the steps CompleteWorkloadCompose
// runs after an executor apply. bundle is the checkpoint's workload bundle;
// the restored compose.yaml, .env and configuration files must be exactly
// what the native preparation renders from it, the same admission `stackkit
// setup` applies before it talks to the application.
func CompleteRestoredWorkloadCompose(ctx context.Context, workspaceRoot string, bundle []byte) error {
	operations, err := NewOSStandaloneComposeWorkloadOperations(workspaceRoot)
	if err != nil {
		return err
	}
	o, ok := operations.(*osStandaloneComposeWorkloadOperations)
	if !ok {
		return errors.New("standalone Compose operations have an unexpected implementation")
	}
	return o.completeRestored(ctx, bundle)
}

func (o *osStandaloneComposeWorkloadOperations) completeRestored(ctx context.Context, raw []byte) error {
	if ctx == nil {
		return errors.New("standalone Compose operations require a context")
	}
	bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(raw)
	if err != nil {
		return fmt.Errorf("validate the restored workload bundle: %w", err)
	}
	// Every selected-PaaS instance is <unitRef>-node-<nodeRef>, with a
	// daemon suffix for the game node (see the selected-PaaS validators).
	unitRef, _, found := strings.Cut(bundle.InstanceRef, "-node-"+bundle.NodeRef)
	if !found || unitRef == "" {
		return errors.New("restored workload instance does not name its render unit")
	}
	project, err := o.prepare(ctx, SelectedPaaSWorkloadDeployment{
		WorkloadRef: bundle.WorkloadRef, ModuleRef: bundle.ModuleRef, UnitRef: unitRef, Release: bundle.Release,
		SiteRef: bundle.SiteRef, NodeRef: bundle.NodeRef, InstanceRef: bundle.InstanceRef,
		Bundle: raw, Route: bundle.Route,
		RuntimeAdapter: runtimeexecutor.RuntimeAdapterBinding{ID: standaloneComposeAdapterRef, ModuleRef: standaloneComposeModuleRef},
	})
	if err != nil {
		return err
	}
	if err := o.verifyPersisted(project); err != nil {
		return fmt.Errorf("restored workload project differs from its checkpoint bundle: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, standaloneComposeApplyBudget)
	defer cancel()
	return o.completeWorkloadCompose(ctx, project)
}
