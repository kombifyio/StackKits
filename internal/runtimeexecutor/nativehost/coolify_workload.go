package nativehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"gopkg.in/yaml.v3"
)

type osCoolifyWorkloadOperations struct {
	workspace string
	prepared  *osStandaloneComposeWorkloadOperations
}

func validWorkloadVolumeCreateQuery(args []string, directory string) bool {
	project := filepath.Base(directory)
	if len(args) != 3 || args[0] != "volume" || args[1] != "create" ||
		!strings.HasPrefix(project, "stackkit-") || filepath.Base(filepath.Dir(directory)) != "applications" ||
		!strings.HasPrefix(args[2], project+"_") || len(args[2]) <= len(project)+1 {
		return false
	}
	for _, c := range args[2] {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// NewOSCoolifyWorkloadOperations uses the installed Marketplace's node custody.
// It neither bootstraps another platform nor accepts external credentials.
func NewOSCoolifyWorkloadOperations(workspace string) (SelectedPaaSWorkloadOperations, error) {
	shared, err := NewOSStandaloneComposeWorkloadOperations(workspace)
	if err != nil {
		return nil, err
	}
	prepared := shared.(*osStandaloneComposeWorkloadOperations)
	prepared.adapterRef, prepared.adapterModuleRef = "coolify", "stackkits-coolify-runtime"
	return &osCoolifyWorkloadOperations{workspace: prepared.workspaceRoot, prepared: prepared}, nil
}

func (o *osCoolifyWorkloadOperations) manager(deployment SelectedPaaSWorkloadDeployment) (platformdeploy.HostDeployment, bool, error) {
	project := "stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef
	present, err := platformdeploy.HasOwnerHostDeployment(o.workspace, project)
	if err != nil || !present {
		return platformdeploy.HostDeployment{}, present, err
	}
	state, err := platformdeploy.LoadOwnerHostDeployment(o.workspace, project)
	if err != nil {
		return state, true, err
	}
	if err := platformdeploy.RequireHostDeploymentManager(state, "coolify"); err != nil {
		return state, true, &WorkloadManagerConflictError{Project: project, CurrentManager: platformdeploy.HostDeploymentManager(state), RequestedManager: "coolify"}
	}
	if state.WorkloadRef != deployment.WorkloadRef || state.InstanceRef != deployment.InstanceRef || state.SiteRef != deployment.SiteRef || state.NodeRef != deployment.NodeRef {
		return state, true, errors.New("Coolify workload custody differs from the exact node placement")
	}
	return state, true, nil
}

func (o *osCoolifyWorkloadOperations) ApplyWorkload(ctx context.Context, deployment SelectedPaaSWorkloadDeployment) (SelectedPaaSApplyReceipt, error) {
	prior, present, err := o.manager(deployment)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	project := "stackkit-" + deployment.WorkloadRef + "-" + deployment.NodeRef
	if !present {
		raw, err := o.prepared.runner.Run(ctx, []string{"ps", "--all", "--filter", "label=com.docker.compose.project=" + project, "--format", "{{.ID}}"}, o.workspace)
		if err != nil {
			return SelectedPaaSApplyReceipt{}, err
		}
		if strings.TrimSpace(string(raw)) != "" {
			return SelectedPaaSApplyReceipt{}, &WorkloadManagerConflictError{Project: project, CurrentManager: "unclaimed", RequestedManager: "coolify"}
		}
	}
	cfg, custody, err := o.configuration(ctx, deployment)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	if present && prior.ServerID != cfg.ServerID {
		return SelectedPaaSApplyReceipt{}, errors.New("Coolify workload server differs from node custody")
	}
	prepared, err := o.prepared.PrepareWorkloadCompose(ctx, deployment)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	compose, err := platformNodeCompose(prepared)
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	var nodeCompose struct {
		Volumes map[string]struct {
			Name string `yaml:"name"`
		} `yaml:"volumes"`
	}
	if err := yaml.Unmarshal(compose, &nodeCompose); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	for _, volume := range nodeCompose.Volumes {
		if !strings.HasPrefix(volume.Name, project+"_") {
			return SelectedPaaSApplyReceipt{}, errors.New("Coolify volume differs from the exact node workload")
		}
		if _, err := o.prepared.runner.Run(ctx, []string{"volume", "create", volume.Name}, prepared.Directory); err != nil {
			return SelectedPaaSApplyReceipt{}, err
		}
	}
	if err := o.project(ctx, &cfg, &custody, deployment.WorkloadRef); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	adapter := platformdeploy.NewCoolifyAdapter(cfg)
	previousIDs := map[string]bool{}
	if present {
		project := prepared.project
		project.runtimeProject = prior.Deployment.ExternalID
		raw, err := o.prepared.runner.Run(ctx, standaloneComposeArgs(project, "ps"), project.directory)
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
	}
	if !present {
		occupied, err := adapter.ProjectHasServices(ctx, cfg.ProjectUUID)
		if err != nil {
			return SelectedPaaSApplyReceipt{}, err
		}
		if occupied {
			return SelectedPaaSApplyReceipt{}, errors.New("Coolify workload project has a service without exact node deployment custody; inspect before retrying")
		}
	}
	manifest := platformdeploy.AppManifest{Name: project, Platform: "coolify", ManagedBy: "coolify", ComposePath: "compose.yaml", ComposeYAML: string(compose)}
	var ref platformdeploy.DeploymentRef
	if present {
		ref, err = adapter.UpdateCompose(ctx, prior.Deployment, manifest)
	} else {
		ref, err = adapter.UpsertCompose(ctx, manifest)
	}
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	state := platformdeploy.HostDeployment{WorkloadRef: deployment.WorkloadRef, InstanceRef: deployment.InstanceRef, ArtifactDigest: deployment.ArtifactDigest, SiteRef: deployment.SiteRef, NodeRef: deployment.NodeRef, ServerID: cfg.ServerID, Deployment: ref, Manager: "coolify"}
	if err := platformdeploy.SaveOwnerHostDeployment(o.workspace, project, state); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	if present {
		ref.DeploymentID, err = adapter.Redeploy(ctx, ref)
	} else {
		ref.DeploymentID, err = adapter.Deploy(ctx, ref)
	}
	if err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	ref.LastDeployed = time.Now().UTC()
	state.Deployment = ref
	if err := platformdeploy.SaveOwnerHostDeployment(o.workspace, project, state); err != nil {
		return SelectedPaaSApplyReceipt{}, err
	}
	prepared.project.runtimeProject = ref.ExternalID
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
	return SelectedPaaSApplyReceipt{InstanceRef: deployment.InstanceRef, ArtifactDigest: deployment.ArtifactDigest, Status: "applied"}, nil
}

func (o *osCoolifyWorkloadOperations) ObserveWorkload(ctx context.Context, deployment SelectedPaaSWorkloadDeployment) (SelectedPaaSWorkloadObservation, error) {
	state, present, err := o.manager(deployment)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if !present || state.ArtifactDigest != deployment.ArtifactDigest {
		return SelectedPaaSWorkloadObservation{}, errors.New("Coolify requires current workload deployment custody")
	}
	cfg, _, err := o.configuration(ctx, deployment)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if state.ServerID != cfg.ServerID {
		return SelectedPaaSWorkloadObservation{}, errors.New("Coolify observation server differs from node custody")
	}
	if err := platformdeploy.NewCoolifyAdapter(cfg).Status(ctx, state.Deployment); err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	project, err := o.prepared.prepareWithIdentityMutation(ctx, deployment, false)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if err := o.prepared.verifyPersisted(project); err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	project.runtimeProject = state.Deployment.ExternalID
	return o.prepared.observePreparedWorkload(ctx, deployment, project)
}

func (o *osCoolifyWorkloadOperations) ValidateWorkloadObservation(d SelectedPaaSWorkloadDeployment, v SelectedPaaSWorkloadObservation) error {
	return o.prepared.ValidateWorkloadObservation(d, v)
}

func (o *osCoolifyWorkloadOperations) configuration(ctx context.Context, d SelectedPaaSWorkloadDeployment) (platformdeploy.HTTPConfig, marketplaceCustody, error) {
	owner, err := localevidence.LoadOwnerCustody(o.workspace)
	if err != nil || owner.Binding.SiteRef != d.SiteRef || owner.Binding.NodeRef != d.NodeRef {
		return platformdeploy.HTTPConfig{}, marketplaceCustody{}, errors.New("Coolify requires the established exact node owner")
	}
	address, err := localevidence.LocalIdentityRuntimeAddress(o.workspace)
	if err != nil {
		return platformdeploy.HTTPConfig{}, marketplaceCustody{}, err
	}
	kit := "basement-kit"
	if _, err := localevidence.LoadBasementRuntimeCustody(o.workspace); err != nil {
		if !errors.Is(err, localevidence.ErrBasementRuntimeCustodyMissing) {
			return platformdeploy.HTTPConfig{}, marketplaceCustody{}, err
		}
		kit = "cloud-kit"
	}
	root, err := confinedfs.Open(o.workspace)
	if err != nil {
		return platformdeploy.HTTPConfig{}, marketplaceCustody{}, err
	}
	defer root.Close()
	view, err := root.View(marketplaceDirectory)
	if err != nil {
		return platformdeploy.HTTPConfig{}, marketplaceCustody{}, err
	}
	file, err := view.Open("compose.yaml")
	if err != nil {
		return platformdeploy.HTTPConfig{}, marketplaceCustody{}, errors.New("Coolify requires the installed Marketplace recipe")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 131073))
	if err != nil || len(raw) > 131072 {
		return platformdeploy.HTTPConfig{}, marketplaceCustody{}, errors.New("Marketplace installation recipe is invalid")
	}
	var recipe struct {
		Services map[string]struct {
			Image       string `yaml:"image"`
			MemoryLimit string `yaml:"mem_limit"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal(raw, &recipe) != nil {
		return platformdeploy.HTTPConfig{}, marketplaceCustody{}, errors.New("Marketplace installation recipe is invalid")
	}
	document := architecturev2renderer.MarketplaceCoolifyDocument{APIVersion: "stackkit.marketplace-coolify/v1", Kind: "MarketplaceCoolify", KitRef: kit, SiteRef: d.SiteRef, NodeRef: d.NodeRef, DomainBase: address.Domain, SubdomainPrefix: address.SubdomainPrefix}
	for _, id := range []string{"coolify", "postgres", "redis"} {
		entry := recipe.Services[id]
		image, digest, ok := strings.Cut(entry.Image, "@")
		if !ok {
			return platformdeploy.HTTPConfig{}, marketplaceCustody{}, errors.New("Marketplace image is not pinned")
		}
		document.Components = append(document.Components, architecturev2renderer.KomodoBootstrapImage{ID: id, ImageRef: image, ImageDigest: digest, MemoryLimit: entry.MemoryLimit})
	}
	ops, custody, err := existingMarketplace(ctx, o.workspace, document)
	if err != nil {
		return platformdeploy.HTTPConfig{}, custody, err
	}
	if _, err := ObserveMarketplaceContainerCustody(ctx, o.workspace, document); err != nil {
		return platformdeploy.HTTPConfig{}, custody, err
	}
	endpoint, err := ops.endpoint(ctx, filepath.Join(o.workspace, marketplaceDirectory))
	return platformdeploy.HTTPConfig{BaseURL: endpoint, Token: custody.APIToken, ServerID: custody.ServerID, ProjectUUID: custody.CuratedProjects[d.WorkloadRef], EnvironmentID: "production"}, custody, err
}

func (o *osCoolifyWorkloadOperations) project(ctx context.Context, cfg *platformdeploy.HTTPConfig, c *marketplaceCustody, workload string) error {
	adapter := platformdeploy.NewCoolifyAdapter(*cfg)
	projects, err := adapter.Projects(ctx)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(c.OwnerRef + "/" + c.SiteRef + "/" + c.NodeRef + "/" + workload))
	name := "stackkit-curated-" + hex.EncodeToString(sum[:8])
	if c.CuratedProjects[workload] != "" {
		for _, p := range projects {
			if p.UUID == c.CuratedProjects[workload] && p.Name == name {
				return nil
			}
		}
		return errors.New("Coolify curated project differs from node custody")
	}
	for _, p := range projects {
		if p.Name == name {
			return errors.New("Coolify curated project exists without node custody; inspect before retrying")
		}
	}
	id, err := adapter.CreateProject(ctx, name)
	if err != nil {
		return err
	}
	if c.CuratedProjects == nil {
		c.CuratedProjects = map[string]string{}
	}
	c.CuratedProjects[workload] = id
	cfg.ProjectUUID = id
	root, err := confinedfs.Open(o.workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	view, err := root.View(marketplaceDirectory)
	if err != nil {
		return err
	}
	return saveMarketplaceCustody(view, *c)
}

// Explicit data/network names preserve the node graph under a platform-owned
// Compose project. API content carries private file references, not credentials.
func platformNodeCompose(p NativeWorkloadCompose) ([]byte, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(p.Compose, &doc); err != nil {
		return nil, err
	}
	for _, section := range []string{"volumes", "networks"} {
		entries, _ := doc[section].(map[string]any)
		for name, value := range entries {
			entry, _ := value.(map[string]any)
			if entry == nil {
				entry = map[string]any{}
			}
			if _, exists := entry["name"]; !exists {
				entry["name"] = p.ProjectName + "_" + name
			}
			if section == "volumes" {
				entry["external"] = true
			}
			entries[name] = entry
		}
	}
	services, _ := doc["services"].(map[string]any)
	for _, value := range services {
		service := value.(map[string]any)
		service["env_file"] = []string{filepath.Join(p.Directory, p.EnvFile)}
		env, _ := service["environment"].(map[string]any)
		for key, value := range env {
			if text, ok := value.(string); ok && strings.Contains(text, "${") {
				delete(env, key)
			}
		}
		mounts, _ := service["volumes"].([]any)
		for i, value := range mounts {
			if text, ok := value.(string); ok && strings.HasPrefix(text, "./") {
				source, target, ok := strings.Cut(text, ":")
				if !ok {
					return nil, errors.New("platform private mount is invalid")
				}
				mounts[i] = filepath.Join(p.Directory, source) + ":" + target
			}
		}
	}
	return yaml.Marshal(doc)
}
