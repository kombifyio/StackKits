package nativehost

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"gopkg.in/yaml.v3"
)

const dokployDeploymentTimeout = 15 * time.Minute

type osDokployWorkloadOperations struct {
	workspace string
	prepared  *osStandaloneComposeWorkloadOperations
}

// NewOSDokployWorkloadOperations deploys selected workloads through the node's
// own Dokploy, which the selected provider's bootstrap installed. It accepts
// no external endpoint or credential.
func NewOSDokployWorkloadOperations(workspace string) (SelectedPaaSWorkloadOperations, error) {
	shared, err := NewOSStandaloneComposeWorkloadOperations(workspace)
	if err != nil {
		return nil, err
	}
	prepared := shared.(*osStandaloneComposeWorkloadOperations)
	prepared.adapterRef, prepared.adapterModuleRef = "dokploy", "stackkits-dokploy-runtime"
	return &osDokployWorkloadOperations{workspace: prepared.workspaceRoot, prepared: prepared}, nil
}

func (o *osDokployWorkloadOperations) manager(d SelectedPaaSWorkloadDeployment) (platformdeploy.HostDeployment, bool, error) {
	project := "stackkit-" + d.WorkloadRef + "-" + d.NodeRef
	present, err := platformdeploy.HasOwnerHostDeployment(o.workspace, project)
	if err != nil || !present {
		return platformdeploy.HostDeployment{}, present, err
	}
	state, err := platformdeploy.LoadOwnerHostDeployment(o.workspace, project)
	if err != nil {
		return state, true, err
	}
	if err := platformdeploy.RequireHostDeploymentManager(state, "dokploy"); err != nil {
		return state, true, &WorkloadManagerConflictError{Project: project, CurrentManager: platformdeploy.HostDeploymentManager(state), RequestedManager: "dokploy"}
	}
	if state.WorkloadRef != d.WorkloadRef || state.InstanceRef != d.InstanceRef || state.SiteRef != d.SiteRef || state.NodeRef != d.NodeRef {
		return state, true, errors.New("Dokploy workload custody differs from the exact node placement")
	}
	return state, true, nil
}

// configuration resolves the node's Dokploy API from bootstrap custody. The
// key reaches only the loopback endpoint and is never written elsewhere.
func (o *osDokployWorkloadOperations) configuration(ctx context.Context, d SelectedPaaSWorkloadDeployment) (platformdeploy.HTTPConfig, error) {
	owner, err := localevidence.LoadOwnerCustody(o.workspace)
	if err != nil || owner.Binding.SiteRef != d.SiteRef || owner.Binding.NodeRef != d.NodeRef {
		return platformdeploy.HTTPConfig{}, errors.New("Dokploy requires the established exact node owner")
	}
	root, err := confinedfs.Open(o.workspace)
	if err != nil {
		return platformdeploy.HTTPConfig{}, err
	}
	defer root.Close()
	view, err := root.View(dokployDirectory)
	if err != nil {
		return platformdeploy.HTTPConfig{}, errors.New("Dokploy workloads require the node's bootstrapped Dokploy")
	}
	state, err := loadDokployCustody(view)
	if err != nil {
		return platformdeploy.HTTPConfig{}, errors.New("Dokploy workloads require the node's bootstrapped Dokploy")
	}
	if state.OwnerRef != owner.OwnerRef || state.SiteRef != d.SiteRef || state.NodeRef != d.NodeRef || state.APIKey == "" || state.EnvironmentID == "" {
		return platformdeploy.HTTPConfig{}, errors.New("Dokploy bootstrap custody differs from the exact node or is incomplete")
	}
	bootstrap := &osDokployBootstrapOperations{workspace: o.workspace}
	endpoint, err := bootstrap.endpoint(ctx, filepath.Join(o.workspace, filepath.FromSlash(dokployDirectory)))
	if err != nil {
		return platformdeploy.HTTPConfig{}, err
	}
	return platformdeploy.HTTPConfig{BaseURL: endpoint, Token: state.APIKey, EnvironmentID: state.EnvironmentID}, nil
}

func (o *osDokployWorkloadOperations) ApplyWorkload(ctx context.Context, d SelectedPaaSWorkloadDeployment) (SelectedPaaSApplyReceipt, error) {
	prior, present, err := o.manager(d)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	project := "stackkit-" + d.WorkloadRef + "-" + d.NodeRef
	if !present {
		raw, err := o.prepared.runner.Run(ctx, []string{"ps", "--all", "--filter", "label=com.docker.compose.project=" + project, "--format", "{{.ID}}"}, o.workspace)
		if err != nil {
			return SelectedPaaSApplyReceipt{}, err
		}
		if strings.TrimSpace(string(raw)) != "" {
			return SelectedPaaSApplyReceipt{}, &WorkloadManagerConflictError{Project: project, CurrentManager: "unclaimed", RequestedManager: "dokploy"}
		}
	}
	cfg, err := o.configuration(ctx, d)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	if present && prior.EnvironmentID != cfg.EnvironmentID {
		return SelectedPaaSApplyReceipt{}, errors.New("Dokploy workload environment differs from node custody")
	}
	prepared, err := o.prepared.PrepareWorkloadCompose(ctx, d)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	compose, err := platformDerivedNodeCompose(prepared, "Dokploy")
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	if err := o.createExternalVolumes(ctx, compose, prepared, project); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	adapter := platformdeploy.NewDokployAdapter(cfg)
	manifest := platformdeploy.AppManifest{Name: project, Platform: "dokploy", ManagedBy: "dokploy", ComposePath: "docker-compose.yml", ComposeYAML: string(compose)}
	if !present {
		// A create whose identity was lost (no response, crash before custody)
		// leaves a service under this name; a second create would run beside it
		// on the same external volumes.
		leftover, err := adapter.EnvironmentComposeIDs(ctx, project)
		if err != nil {
			return SelectedPaaSApplyReceipt{}, err
		}
		if len(leftover) != 0 {
			return SelectedPaaSApplyReceipt{}, errors.New("Dokploy environment has a service for this workload without node custody; inspect it in Dokploy before retrying")
		}
	}
	previousIDs, previousDeployments := map[string]bool{}, map[string]bool{}
	var ref platformdeploy.DeploymentRef
	if present {
		ref = prior.Deployment
		state, err := adapter.ComposeState(ctx, ref)
		if err != nil {
			return SelectedPaaSApplyReceipt{}, err
		}
		if state.AppName != prior.RuntimeProject {
			return SelectedPaaSApplyReceipt{}, errors.New("Dokploy physical Compose project differs from node custody")
		}
		for _, deployment := range state.Deployments {
			previousDeployments[deployment.ID] = true
		}
		running := prepared.project
		running.runtimeProject = prior.RuntimeProject
		raw, err := o.prepared.runner.Run(ctx, standaloneComposeArgs(running, "ps"), running.directory)
		if err != nil {
			return SelectedPaaSApplyReceipt{}, err
		}
		if len(strings.TrimSpace(string(raw))) != 0 {
			statuses, err := parseStandaloneComposeStatuses(raw)
			if err != nil {
				return SelectedPaaSApplyReceipt{}, err
			}
			for _, status := range statuses {
				previousIDs[status.ID] = true
			}
		}
		if ref, err = adapter.UpdateCompose(ctx, ref, manifest); err != nil {
			return SelectedPaaSApplyReceipt{}, err
		}
	} else if ref, err = adapter.UpsertCompose(ctx, manifest); err != nil {
		// A created service keeps its identity so a retry updates it instead
		// of creating a second suffixed Compose project.
		if ref.ExternalID != "" {
			if _, saveErr := o.saveCreated(ctx, adapter, d, cfg, ref, DokployRecipeDigest(compose)); saveErr != nil {
				return SelectedPaaSApplyReceipt{}, fmt.Errorf("%w; recording the created service also failed: %v", err, saveErr)
			}
		}
		return SelectedPaaSApplyReceipt{}, err
	}
	// An update keeps the signed recipe of the running deployment until the
	// new one succeeds, so a failed redeploy still reads as drift.
	signed := DokployRecipeDigest(compose)
	if present {
		signed = prior.RecipeDigest
	}
	state, err := o.saveCreated(ctx, adapter, d, cfg, ref, signed)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	if present {
		err = adapter.Redeploy(ctx, ref)
	} else {
		err = adapter.Deploy(ctx, ref)
	}
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	if state.Deployment.DeploymentID, err = adapter.WaitNewDeployment(ctx, ref, previousDeployments, dokployDeploymentTimeout); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	state.Deployment.LastDeployed = time.Now().UTC()
	state.RecipeDigest = DokployRecipeDigest(compose)
	if err := platformdeploy.SaveOwnerHostDeployment(o.workspace, project, state); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	prepared.project.runtimeProject = state.RuntimeProject
	if len(previousIDs) != 0 {
		if err := o.prepared.waitForComponentReadiness(ctx, prepared.project, func(statuses map[string]standaloneComposePS) (bool, error) {
			for _, component := range prepared.project.bundle.Components {
				status, exists := statuses[component.ID]
				if !exists || previousIDs[status.ID] {
					return false, nil
				}
			}
			return true, nil
		}); err != nil {
			return SelectedPaaSApplyReceipt{}, err
		}
	}
	if err := o.prepared.CompleteWorkloadCompose(ctx, prepared); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	return SelectedPaaSApplyReceipt{InstanceRef: d.InstanceRef, ArtifactDigest: d.ArtifactDigest, Status: "applied"}, nil
}

// saveCreated signs the exact service, its suffixed physical project and the
// given recipe digest before any deployment runs.
func (o *osDokployWorkloadOperations) saveCreated(ctx context.Context, adapter *platformdeploy.DokployAdapter, d SelectedPaaSWorkloadDeployment, cfg platformdeploy.HTTPConfig, ref platformdeploy.DeploymentRef, recipeDigest string) (platformdeploy.HostDeployment, error) {
	observed, err := adapter.ComposeState(ctx, ref)
	if err != nil {
		return platformdeploy.HostDeployment{}, err
	}
	state := platformdeploy.HostDeployment{WorkloadRef: d.WorkloadRef, InstanceRef: d.InstanceRef, ArtifactDigest: d.ArtifactDigest, SiteRef: d.SiteRef, NodeRef: d.NodeRef,
		EnvironmentID: cfg.EnvironmentID, RuntimeProject: observed.AppName, RecipeDigest: recipeDigest, Deployment: ref, Manager: "dokploy"}
	return state, platformdeploy.SaveOwnerHostDeployment(o.workspace, "stackkit-"+d.WorkloadRef+"-"+d.NodeRef, state)
}

// createExternalVolumes keeps workload data in node-named volumes that outlive
// the platform service, exactly as the Coolify path does.
func (o *osDokployWorkloadOperations) createExternalVolumes(ctx context.Context, compose []byte, prepared NativeWorkloadCompose, project string) error {
	var nodeCompose struct {
		Volumes map[string]struct {
			Name string `yaml:"name"`
		} `yaml:"volumes"`
	}
	if err := yaml.Unmarshal(compose, &nodeCompose); err != nil {
		return err
	}
	for _, volume := range nodeCompose.Volumes {
		if !strings.HasPrefix(volume.Name, project+"_") {
			return errors.New("Dokploy volume differs from the exact node workload")
		}
		if _, err := o.prepared.runner.Run(ctx, []string{"volume", "create", volume.Name}, prepared.Directory); err != nil {
			return err
		}
	}
	return nil
}

func (o *osDokployWorkloadOperations) ObserveWorkload(ctx context.Context, d SelectedPaaSWorkloadDeployment) (SelectedPaaSWorkloadObservation, error) {
	state, present, err := o.manager(d)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if !present || state.ArtifactDigest != d.ArtifactDigest {
		return SelectedPaaSWorkloadObservation{}, errors.New("Dokploy requires current workload deployment custody")
	}
	cfg, err := o.configuration(ctx, d)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if state.EnvironmentID != cfg.EnvironmentID {
		return SelectedPaaSWorkloadObservation{}, errors.New("Dokploy observation environment differs from node custody")
	}
	adapter := platformdeploy.NewDokployAdapter(cfg)
	if err := adapter.Status(ctx, state.Deployment); err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	observed, err := adapter.ComposeState(ctx, state.Deployment)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	// The stored recipe is compared deny-by-default with the signed digest, so
	// an edit made in the Dokploy UI is drift until Apply reconciles it.
	if observed.AppName != state.RuntimeProject || DokployRecipeDigest([]byte(observed.ComposeFile)) != state.RecipeDigest {
		return SelectedPaaSWorkloadObservation{}, &DokployDriftError{projectRef: "stackkit-" + d.WorkloadRef + "-" + d.NodeRef}
	}
	project, err := o.prepared.prepareWithIdentityMutation(ctx, d, false)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if err := o.prepared.verifyPersisted(project); err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	project.runtimeProject = state.RuntimeProject
	return o.prepared.observePreparedWorkload(ctx, d, project)
}

func (o *osDokployWorkloadOperations) ValidateWorkloadObservation(d SelectedPaaSWorkloadDeployment, v SelectedPaaSWorkloadObservation) error {
	return o.prepared.ValidateWorkloadObservation(d, v)
}

// DokployDriftError reports that the recipe Dokploy stores for a workload no
// longer matches the signed node recipe, for example after an edit in the
// Dokploy UI. Re-applying the workload restores the signed recipe.
type DokployDriftError struct{ projectRef string }

func (err *DokployDriftError) Error() string {
	return "Dokploy service recipe differs from the signed workload recipe"
}
func (err *DokployDriftError) DriftSubject() string    { return "runtime-configuration" }
func (err *DokployDriftError) DriftCode() string       { return "dokploy-service-recipe-changed" }
func (err *DokployDriftError) DriftProjectRef() string { return err.projectRef }
