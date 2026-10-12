package nativehost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

// osKomodoWorkloadOperations uses the existing workload renderer and private
// node custody. Only the selected Komodo API mutates the generated project;
// the shared Compose observer reads exact images, data and router bindings.
type osKomodoWorkloadOperations struct {
	workspace string
	prepared  *osStandaloneComposeWorkloadOperations
}

// WorkloadManagerConflictError requires an explicit custody transition before
// another manager can mutate the same project and its persistent volumes.
type WorkloadManagerConflictError struct {
	Project          string
	CurrentManager   string
	RequestedManager string
}

func (e *WorkloadManagerConflictError) Error() string {
	return fmt.Sprintf("workload project %q has manager %q; request an explicit transition to %q before Apply", e.Project, e.CurrentManager, e.RequestedManager)
}

func NewOSKomodoWorkloadOperations(workspace string) (SelectedPaaSWorkloadOperations, error) {
	shared, err := NewOSStandaloneComposeWorkloadOperations(workspace)
	if err != nil {
		return nil, err
	}
	prepared := shared.(*osStandaloneComposeWorkloadOperations)
	prepared.adapterRef, prepared.adapterModuleRef = "komodo", "stackkits-komodo-core-runtime"
	return &osKomodoWorkloadOperations{workspace: prepared.workspaceRoot, prepared: prepared}, nil
}

func (o *osKomodoWorkloadOperations) ApplyWorkload(ctx context.Context, deployment SelectedPaaSWorkloadDeployment) (SelectedPaaSApplyReceipt, error) {
	cfg, err := platformdeploy.LoadOwnerKomodoConfig(o.workspace)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	if cfg.ServerID == "" {
		return SelectedPaaSApplyReceipt{}, errors.New("local Komodo requires its registered node server identity")
	}
	project := "stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef
	present, err := platformdeploy.HasOwnerHostDeployment(o.workspace, project)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	if present {
		state, err := platformdeploy.LoadOwnerHostDeployment(o.workspace, project)
		if err != nil || state.WorkloadRef != deployment.WorkloadRef || state.NodeRef != deployment.NodeRef ||
			state.SiteRef != deployment.SiteRef || state.ServerID != cfg.ServerID {
			return SelectedPaaSApplyReceipt{}, errors.New("existing Komodo project belongs to another node binding")
		}
	} else {
		args := []string{"ps", "--all", "--filter", "label=com.docker.compose.project=" + project, "--format", "{{.ID}}"}
		raw, err := o.prepared.runner.Run(ctx, args, o.workspace)
		if err != nil {
			return SelectedPaaSApplyReceipt{}, fmt.Errorf("observe existing workload manager: %w", err)
		}
		if strings.TrimSpace(string(raw)) != "" {
			return SelectedPaaSApplyReceipt{}, &WorkloadManagerConflictError{Project: project, CurrentManager: "unclaimed", RequestedManager: "komodo"}
		}
	}
	prepared, err := o.prepared.PrepareWorkloadCompose(ctx, deployment)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	ignored := []string{}
	for _, component := range prepared.project.bundle.Components {
		if component.Lifecycle == "one-shot" || component.HealthFailure == "degraded" {
			ignored = append(ignored, component.ID)
		}
	}
	adapter := platformdeploy.NewKomodoAdapter(cfg)
	ref, err := adapter.UpsertStack(ctx, platformdeploy.AppManifest{
		Name: prepared.ProjectName, Platform: "komodo", ManagedBy: "komodo",
		HostDirectory: prepared.Directory, ComposePath: "compose.yaml", ComposeYAML: string(prepared.Compose),
		IgnoredServices: ignored,
	})
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	state := platformdeploy.HostDeployment{
		WorkloadRef: deployment.WorkloadRef, InstanceRef: deployment.InstanceRef, ArtifactDigest: deployment.ArtifactDigest,
		SiteRef: deployment.SiteRef, NodeRef: deployment.NodeRef, ServerID: cfg.ServerID, Deployment: ref,
	}
	if err := platformdeploy.SaveOwnerHostDeployment(o.workspace, prepared.ProjectName, state); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	ref.DeploymentID, err = adapter.Deploy(ctx, ref)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	ref.LastDeployed = time.Now().UTC()
	ref, err = adapter.ObserveDeployment(ctx, ref)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	state.Deployment = ref
	if err := platformdeploy.SaveOwnerHostDeployment(o.workspace, prepared.ProjectName, state); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	if err := o.prepared.CompleteWorkloadCompose(ctx, prepared); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	return SelectedPaaSApplyReceipt{InstanceRef: deployment.InstanceRef, ArtifactDigest: deployment.ArtifactDigest, Status: "applied"}, nil
}

func (o *osKomodoWorkloadOperations) ObserveWorkload(ctx context.Context, deployment SelectedPaaSWorkloadDeployment) (SelectedPaaSWorkloadObservation, error) {
	project := "stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef
	state, err := platformdeploy.LoadOwnerHostDeployment(o.workspace, project)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	cfg, err := platformdeploy.LoadOwnerKomodoConfig(o.workspace)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if state.WorkloadRef != deployment.WorkloadRef || state.InstanceRef != deployment.InstanceRef ||
		state.ArtifactDigest != deployment.ArtifactDigest || state.SiteRef != deployment.SiteRef ||
		state.NodeRef != deployment.NodeRef || state.ServerID != cfg.ServerID {
		return SelectedPaaSWorkloadObservation{}, errors.New("Komodo deployment custody differs from the exact workload target")
	}
	if _, err := platformdeploy.NewKomodoAdapter(cfg).ObserveDeployment(ctx, state.Deployment); err != nil {
		return SelectedPaaSWorkloadObservation{}, fmt.Errorf("observe selected Komodo deployment: %w", err)
	}
	return o.prepared.ObserveWorkload(ctx, deployment)
}

func (o *osKomodoWorkloadOperations) ValidateWorkloadObservation(deployment SelectedPaaSWorkloadDeployment, observation SelectedPaaSWorkloadObservation) error {
	return o.prepared.ValidateWorkloadObservation(deployment, observation)
}

func validWorkloadManagerQuery(args []string) bool {
	if len(args) != 6 || args[0] != "ps" || args[1] != "--all" || args[2] != "--filter" ||
		!strings.HasPrefix(args[3], "label=com.docker.compose.project=stackkit-") || args[4] != "--format" || args[5] != "{{.ID}}" {
		return false
	}
	project := strings.TrimPrefix(args[3], "label=com.docker.compose.project=")
	for _, char := range project {
		if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
			return false
		}
	}
	return len(project) <= 128
}
