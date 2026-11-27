package servicecontrol

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

// dokployServiceAdapter controls one selected workload that the dokploy
// runtime adapter deployed through the node's own Dokploy. Dokploy keeps no
// runtime state for a running Compose service, so the Docker engine is the
// controller, on the physical Compose project that the signed custody names;
// the Compose recipe and env file are the node's own, which Dokploy deployed
// byte for byte.
const dokployServiceAdapter = "dokploy"

var dokployRuntimeProjectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,125}$`)

// dokployRuntimeTarget resolves the exact signed Dokploy custody of a workload
// service. Unlike the other workload adapters it has no legacy fallback: a
// Dokploy service is controllable only while its owner custody is present.
func (c *Controller) dokployRuntimeTarget(definition serviceDefinition) (runtimeTarget, error) {
	state, err := loadDokployServiceCustody(c.workspace, definition.RuntimeRef)
	if err != nil {
		return runtimeTarget{}, err
	}
	path := applicationComposePath(c.workspace, definition.RuntimeRef)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return runtimeTarget{}, &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "declared Dokploy runtime recipe is unavailable"}
	}
	return runtimeTarget{Adapter: definition.Adapter, RuntimeRef: definition.RuntimeRef, ComposePath: filepath.Clean(path), RuntimeProject: state.RuntimeProject}, nil
}

func loadDokployServiceCustody(workspace, runtimeRef string) (platformdeploy.HostDeployment, error) {
	project := "stackkit-" + runtimeRef
	present, err := platformdeploy.HasOwnerHostDeployment(workspace, project)
	if err != nil {
		return platformdeploy.HostDeployment{}, err
	}
	if !present {
		return platformdeploy.HostDeployment{}, &Error{ReasonCode: ReasonRuntimeUnavailable, Retryable: true, Message: "declared Dokploy workload has no owner custody"}
	}
	state, err := platformdeploy.LoadOwnerHostDeployment(workspace, project)
	if err != nil {
		return platformdeploy.HostDeployment{}, err
	}
	if err := platformdeploy.RequireHostDeploymentManager(state, dokployServiceAdapter); err != nil {
		return platformdeploy.HostDeployment{}, &Error{ReasonCode: "workload_manager_conflict", Message: err.Error()}
	}
	if state.Signature == nil || !dokployRuntimeProjectPattern.MatchString(state.RuntimeProject) {
		return platformdeploy.HostDeployment{}, &Error{ReasonCode: ReasonRuntimeUnavailable, Message: "Dokploy workload custody has no signed physical Compose project"}
	}
	return state, nil
}

// runDokployService runs one bounded Compose command on the signed physical
// project. It re-reads the owner custody, so a manager transition or a project
// that changed after the target was resolved is refused.
func runDokployService(ctx context.Context, request runtimeCommandRequest) (runtimeCommandOutput, error) {
	directory, workspace, err := applicationComposeBounds(request)
	if err != nil {
		return runtimeCommandOutput{}, err
	}
	state, err := loadDokployServiceCustody(workspace, request.RuntimeRef)
	if err != nil {
		return runtimeCommandOutput{}, err
	}
	if state.RuntimeProject != request.RuntimeProject {
		return runtimeCommandOutput{}, errors.New("Dokploy physical Compose project changed during service control")
	}
	return runBoundedCompose(ctx, request, directory, state.RuntimeProject)
}
