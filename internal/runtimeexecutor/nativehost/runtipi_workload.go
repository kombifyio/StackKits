package nativehost

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"gopkg.in/yaml.v3"
)

const runtipiInstallationSecretRef = "secret://platform/runtipi/installation"

// A bootstrap must retain this record through encrypted local Owner custody.
// An API URL or an unrelated Runtipi account alone grants no node authority.
type runtipiInstallationCustody struct {
	OwnerRef       string `json:"ownerRef"`
	SiteRef        string `json:"siteRef"`
	NodeRef        string `json:"nodeRef"`
	InstallationID string `json:"installationId"`
	BaseURL        string `json:"baseUrl"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	RecipeDigest   string `json:"recipeDigest"`
	Domain         string `json:"domain"`
	Bootstrapped   bool   `json:"bootstrapped"`
}

type osRuntipiWorkloadOperations struct {
	workspace string
	prepared  *osStandaloneComposeWorkloadOperations
}

// NewOSRuntipiWorkloadOperations reuses the native workload renderer, image/data
// observer and node deployment custody. It does not bootstrap/adopt an instance.
// Like the current Coolify owner, SelectedPaaSWorkloadOperations owns Apply and
// Observe only. Stop/uninstall exist on the upstream adapter and E2E driver;
// they are not additional public owner operations or qualified lifecycle claims.
func NewOSRuntipiWorkloadOperations(workspace string) (SelectedPaaSWorkloadOperations, error) {
	shared, err := NewOSStandaloneComposeWorkloadOperations(workspace)
	if err != nil {
		return nil, err
	}
	prepared := shared.(*osStandaloneComposeWorkloadOperations)
	prepared.adapterRef, prepared.adapterModuleRef = "runtipi", "stackkits-runtipi-runtime"
	return &osRuntipiWorkloadOperations{workspace: prepared.workspaceRoot, prepared: prepared}, nil
}

func (o *osRuntipiWorkloadOperations) configuration(ctx context.Context, deployment SelectedPaaSWorkloadDeployment) (*platformdeploy.RuntipiAdapter, string, error) {
	owner, err := localevidence.LoadOwnerCustody(o.workspace)
	if err != nil || owner.Binding.SiteRef != deployment.SiteRef || owner.Binding.NodeRef != deployment.NodeRef {
		return nil, "", errors.New("Runtipi requires the established exact node Owner")
	}
	raw, err := localevidence.ResolveEncryptedLocalIssuedSecret(o.workspace, runtipiInstallationSecretRef)
	if err != nil {
		return nil, "", errors.New("Runtipi requires an installed node-bound bootstrap in encrypted Owner custody")
	}
	defer clear(raw)
	var custody runtipiInstallationCustody
	if json.Unmarshal(raw, &custody) != nil || custody.OwnerRef != owner.OwnerRef || custody.SiteRef != deployment.SiteRef || custody.NodeRef != deployment.NodeRef || custody.InstallationID == "" || custody.Username == "" || custody.Password == "" || !custody.Bootstrapped || !strings.HasPrefix(custody.RecipeDigest, "sha256:") {
		return nil, "", errors.New("Runtipi installation custody differs from the exact Owner node")
	}
	endpoint, err := url.Parse(custody.BaseURL)
	if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" || endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, "", errors.New("Runtipi node API must remain on its retained loopback endpoint")
	}
	adapter := platformdeploy.NewRuntipiAdapter(platformdeploy.HTTPConfig{BaseURL: custody.BaseURL})
	if err := adapter.Login(ctx, custody.Username, custody.Password); err != nil {
		return nil, "", err
	}
	return adapter, custody.InstallationID, nil
}

func (o *osRuntipiWorkloadOperations) ApplyWorkload(ctx context.Context, deployment SelectedPaaSWorkloadDeployment) (SelectedPaaSApplyReceipt, error) {
	project := "stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef
	if len(project) > 50 {
		return SelectedPaaSApplyReceipt{}, errors.New("Runtipi requires a node workload identity within its upstream 50-character app-name limit")
	}
	present, err := platformdeploy.HasOwnerHostDeployment(o.workspace, project)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	var prior platformdeploy.HostDeployment
	if present {
		prior, err = platformdeploy.LoadOwnerHostDeployment(o.workspace, project)
		if err != nil {
			return SelectedPaaSApplyReceipt{}, err
		}
		if err := platformdeploy.RequireHostDeploymentManager(prior, "runtipi"); err != nil {
			return SelectedPaaSApplyReceipt{}, err
		}
		if prior.WorkloadRef != deployment.WorkloadRef || prior.InstanceRef != deployment.InstanceRef || prior.SiteRef != deployment.SiteRef || prior.NodeRef != deployment.NodeRef {
			return SelectedPaaSApplyReceipt{}, errors.New("Runtipi workload custody differs from the exact node placement")
		}
	} else {
		raw, err := o.prepared.runner.Run(ctx, []string{"ps", "--all", "--filter", "label=com.docker.compose.project=" + project, "--format", "{{.ID}}"}, o.workspace)
		if err != nil {
			return SelectedPaaSApplyReceipt{}, err
		}
		if strings.TrimSpace(string(raw)) != "" {
			return SelectedPaaSApplyReceipt{}, &WorkloadManagerConflictError{Project: project, CurrentManager: "unclaimed", RequestedManager: "runtipi"}
		}
	}
	adapter, installationID, err := o.configuration(ctx, deployment)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	if present && prior.ServerID != installationID {
		return SelectedPaaSApplyReceipt{}, errors.New("Runtipi instance differs from the retained workload manager")
	}
	prepared, err := o.prepared.PrepareWorkloadCompose(ctx, deployment)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	compose, err := platformNodeCompose(prepared)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	var graph struct {
		Volumes map[string]struct {
			Name string `yaml:"name"`
		} `yaml:"volumes"`
	}
	if err := yaml.Unmarshal(compose, &graph); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	for _, volume := range graph.Volumes {
		if !strings.HasPrefix(volume.Name, project+"_") {
			return SelectedPaaSApplyReceipt{}, errors.New("Runtipi volume differs from the exact node workload")
		}
		if _, err := o.prepared.runner.Run(ctx, []string{"volume", "create", volume.Name}, prepared.Directory); err != nil {
			return SelectedPaaSApplyReceipt{}, err
		}
	}
	manifest := platformdeploy.AppManifest{Name: project, ServiceKey: prepared.project.entry.ID, Port: prepared.project.entry.HealthPort, Platform: "runtipi", ManagedBy: "runtipi", ComposeYAML: string(compose)}
	ref, action := prior.Deployment, "start"
	if prior.Deployment.ObservedStatus == "pending-update" {
		action = "update"
	}
	if !present {
		ref, err = adapter.ApplyCompose(ctx, manifest)
		action = "install"
	} else if prior.ArtifactDigest != deployment.ArtifactDigest {
		err = adapter.UpdateCompose(ctx, ref, manifest)
		action = "update"
	}
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	ref.ObservedStatus = "pending-" + action
	state := platformdeploy.HostDeployment{WorkloadRef: deployment.WorkloadRef, InstanceRef: deployment.InstanceRef, ArtifactDigest: deployment.ArtifactDigest, SiteRef: deployment.SiteRef, NodeRef: deployment.NodeRef, ServerID: installationID, Manager: "runtipi", Deployment: ref, Handover: prior.Handover}
	// Persist platform identity before queueing installation. A failed operation
	// can be reconciled without creating a second upstream app or taking custody.
	if err := platformdeploy.SaveOwnerHostDeployment(o.workspace, project, state); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	app, err := adapter.App(ctx, ref)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	if app.App == nil || app.App.Status == "missing" {
		action = "install"
	}
	if app.App == nil || app.App.Status != "running" || action == "update" {
		if action == "install" {
			err = adapter.Install(ctx, ref, "")
		} else {
			err = adapter.Control(ctx, ref, action)
		}
		if err != nil {
			return SelectedPaaSApplyReceipt{}, err
		}
	}
	prepared.project.runtimeProject = ref.AppName + "__user"
	if err := o.prepared.CompleteWorkloadCompose(ctx, prepared); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	observed, err := adapter.ObserveDeployment(ctx, ref)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	state.Deployment = observed
	if err := platformdeploy.SaveOwnerHostDeployment(o.workspace, project, state); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	return SelectedPaaSApplyReceipt{InstanceRef: deployment.InstanceRef, ArtifactDigest: deployment.ArtifactDigest, Status: "applied"}, nil
}

func (o *osRuntipiWorkloadOperations) ObserveWorkload(ctx context.Context, deployment SelectedPaaSWorkloadDeployment) (SelectedPaaSWorkloadObservation, error) {
	project := "stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef
	state, err := platformdeploy.LoadOwnerHostDeployment(o.workspace, project)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if err := platformdeploy.RequireHostDeploymentManager(state, "runtipi"); err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if state.ArtifactDigest != deployment.ArtifactDigest || state.InstanceRef != deployment.InstanceRef || state.SiteRef != deployment.SiteRef || state.NodeRef != deployment.NodeRef {
		return SelectedPaaSWorkloadObservation{}, errors.New("Runtipi observation differs from retained workload custody")
	}
	adapter, installationID, err := o.configuration(ctx, deployment)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if state.ServerID != installationID {
		return SelectedPaaSWorkloadObservation{}, errors.New("Runtipi observation differs from the installed node instance")
	}
	if _, err := adapter.ObserveDeployment(ctx, state.Deployment); err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	prepared, err := o.prepared.prepareWithIdentityMutation(ctx, deployment, false)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if err := o.prepared.verifyPersisted(prepared); err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	prepared.runtimeProject = state.Deployment.AppName + "__user"
	return o.prepared.observePreparedWorkload(ctx, deployment, prepared)
}

func (o *osRuntipiWorkloadOperations) ValidateWorkloadObservation(d SelectedPaaSWorkloadDeployment, v SelectedPaaSWorkloadObservation) error {
	return o.prepared.ValidateWorkloadObservation(d, v)
}
