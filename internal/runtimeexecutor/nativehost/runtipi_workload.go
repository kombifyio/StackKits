package nativehost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"gopkg.in/yaml.v3"
)

const runtipiInstallationSecretRef = "secret://platform/runtipi/installation"
const runtipiArtifactLabel = "io.stackkit.runtipi-artifact-digest"
const runtipiRecipeLabel = "io.stackkit.runtipi-recipe-digest"

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
	if ctx == nil {
		return SelectedPaaSApplyReceipt{}, errors.New("Runtipi workload apply requires its operation context")
	}
	ctx, cancel := context.WithTimeout(ctx, standaloneComposeApplyBudget)
	defer cancel()
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
	// Join the current operation without changing signed reconciliation intent.
	// Cancellation/failure leaves the prior pending update available to retry.
	var app platformdeploy.RuntipiAppObservation
	if present {
		app, err = adapter.WaitForAppStatus(ctx, prior.Deployment, "running", "stopped", "missing")
		if err != nil {
			return SelectedPaaSApplyReceipt{}, err
		}
	}
	prepared, err := o.prepared.PrepareWorkloadCompose(ctx, deployment)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	compose, recipeDigest, err := runtipiNodeCompose(prepared, deployment.ArtifactDigest)
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
	ref := prior.Deployment
	needsRecipe := present && (prior.ArtifactDigest != deployment.ArtifactDigest || app.Status() == "missing")
	if present {
		matches, err := adapter.ComposeMatches(ctx, ref, manifest)
		if err != nil {
			return SelectedPaaSApplyReceipt{}, err
		}
		needsRecipe = needsRecipe || !matches
	}
	if present && app.Status() != "missing" {
		project := prepared.project
		project.runtimeProject = ref.AppName + "__user"
		if err := o.verifyRuntimeRevision(ctx, project, deployment.ArtifactDigest, recipeDigest); err != nil {
			var mismatch *runtipiRevisionMismatchError
			if !errors.As(err, &mismatch) {
				return SelectedPaaSApplyReceipt{}, err
			}
			// Covers both changed artifacts and unchanged legacy installs whose
			// containers lack the current recipe/revision labels.
			needsRecipe = true
		}
	}
	if !present {
		ref, err = adapter.ApplyCompose(ctx, manifest)
		if err != nil {
			return SelectedPaaSApplyReceipt{}, err
		}
	}
	intent := "pending-start"
	if !present || app.Status() == "missing" {
		intent = "pending-install"
	}
	if needsRecipe || prior.Deployment.ObservedStatus == "pending-update" {
		intent = "pending-update"
	}
	// Keep the exact manager, node and data custody. The existing signed record
	// is desired intent until the independent runtime and application checks
	// pass; joining, backup, editing or a queue acknowledgment never clears it.
	state := prior
	state.WorkloadRef, state.InstanceRef = deployment.WorkloadRef, deployment.InstanceRef
	state.ArtifactDigest, state.SiteRef, state.NodeRef = deployment.ArtifactDigest, deployment.SiteRef, deployment.NodeRef
	state.ServerID, state.Manager, state.Deployment = installationID, "runtipi", ref
	state.Deployment.ObservedStatus = intent
	if err := platformdeploy.SaveOwnerHostDeployment(o.workspace, project, state); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	if needsRecipe {
		_, err = adapter.ActivateCompose(ctx, ref, manifest)
	} else if app.Status() == "missing" {
		err = adapter.Install(ctx, ref, "")
	} else if app.Status() == "stopped" {
		err = adapter.Control(ctx, ref, "start")
	}
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	if _, err := adapter.WaitForAppStatus(ctx, ref, "running"); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	prepared.project.runtimeProject = ref.AppName + "__user"
	if err := o.prepared.CompleteWorkloadCompose(ctx, prepared); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	if err := o.verifyRuntimeRevision(ctx, prepared.project, deployment.ArtifactDigest, recipeDigest); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	if matches, err := adapter.ComposeMatches(ctx, ref, manifest); err != nil || !matches {
		if err == nil {
			err = &runtipiRevisionMismatchError{}
		}
		return SelectedPaaSApplyReceipt{}, err
	}
	observation, err := o.prepared.observePreparedWorkload(ctx, deployment, prepared.project)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	if err := o.prepared.ValidateWorkloadObservation(deployment, observation); err != nil {
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
	compose, recipeDigest, err := runtipiNodeCompose(preparedWorkloadCompose(prepared), deployment.ArtifactDigest)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if err := o.verifyRuntimeRevision(ctx, prepared, deployment.ArtifactDigest, recipeDigest); err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	manifest := platformdeploy.AppManifest{Name: project, ServiceKey: prepared.entry.ID, Port: prepared.entry.HealthPort, ComposeYAML: string(compose)}
	if matches, err := adapter.ComposeMatches(ctx, state.Deployment, manifest); err != nil || !matches {
		if err == nil {
			err = &runtipiRevisionMismatchError{}
		}
		return SelectedPaaSWorkloadObservation{}, err
	}
	return o.prepared.observePreparedWorkload(ctx, deployment, prepared)
}

// A failed upstream update can roll back to running with the same image but an
// older configuration. Bind its generated services to the authorized bundle;
// status and image readback alone cannot distinguish that rollback.
func runtipiNodeCompose(prepared NativeWorkloadCompose, artifactDigest string) ([]byte, string, error) {
	raw, err := platformNodeCompose(prepared)
	if err != nil {
		return nil, "", err
	}
	var graph map[string]any
	if err := yaml.Unmarshal(raw, &graph); err != nil {
		return nil, "", err
	}
	services := graph["services"].(map[string]any)
	for _, value := range services {
		service := value.(map[string]any)
		labels, _ := service["labels"].(map[string]any)
		if labels == nil {
			labels = map[string]any{}
		}
		labels[runtipiArtifactLabel] = artifactDigest
		delete(labels, runtipiRecipeLabel)
		service["labels"] = labels
	}
	raw, err = yaml.Marshal(graph)
	if err != nil {
		return nil, "", err
	}
	// Hash the entire node recipe, including the artifact label, before adding
	// the non-recursive recipe marker. Renderer/format changes at the same
	// workload digest therefore converge instead of failing forever.
	hash := sha256.Sum256(append([]byte("stackkit.runtipi-node-recipe/v1\x00"), raw...))
	recipeDigest := "sha256:" + hex.EncodeToString(hash[:])
	for _, value := range services {
		value.(map[string]any)["labels"].(map[string]any)[runtipiRecipeLabel] = recipeDigest
	}
	raw, err = yaml.Marshal(graph)
	return raw, recipeDigest, err
}

type runtipiRevisionMismatchError struct{}

func (*runtipiRevisionMismatchError) Error() string {
	return "Runtipi runtime recipe differs from the authorized workload; retain pending reconciliation"
}

func (o *osRuntipiWorkloadOperations) verifyRuntimeRevision(ctx context.Context, project standaloneComposeProject, artifactDigest, recipeDigest string) error {
	raw, err := o.prepared.runner.Run(ctx, standaloneComposeArgs(project, "ps"), project.directory)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return &runtipiRevisionMismatchError{}
	}
	statuses, err := parseStandaloneComposeStatuses(raw)
	if err != nil {
		return err
	}
	for _, component := range project.bundle.Components {
		status, present := statuses[component.ID]
		labels := standaloneComposeCSVMap(status.Labels)
		if !present || labels[runtipiArtifactLabel] != artifactDigest || labels[runtipiRecipeLabel] != recipeDigest {
			return &runtipiRevisionMismatchError{}
		}
	}
	return nil
}

func (o *osRuntipiWorkloadOperations) ValidateWorkloadObservation(d SelectedPaaSWorkloadDeployment, v SelectedPaaSWorkloadObservation) error {
	return o.prepared.ValidateWorkloadObservation(d, v)
}
