package nativehost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
)

const DokployVaultModule = "stackkits-vaultwarden-runtime"

var dokployContainerID = regexp.MustCompile(`^[0-9a-f]{64}$`)
var dokployPhysicalProjectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,55}-[a-z]{6}$`)

// DokployWorkloadPreparation shares the real node renderer, secret-file
// custody, completion and independent Docker/HTTP observer. It never calls
// Compose up/down: only the published Dokploy provider mutates this workload.
type DokployWorkloadPreparation struct {
	workspace string
	prepared  *osStandaloneComposeWorkloadOperations
}

func NewOSDokployWorkloadPreparation(workspace string) (*DokployWorkloadPreparation, error) {
	shared, err := NewOSStandaloneComposeWorkloadOperations(workspace)
	if err != nil {
		return nil, err
	}
	prepared := shared.(*osStandaloneComposeWorkloadOperations)
	prepared.adapterRef, prepared.adapterModuleRef = "dokploy", "stackkits-dokploy-runtime"
	return &DokployWorkloadPreparation{workspace: prepared.workspaceRoot, prepared: prepared}, nil
}

func DokployRecipeDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func DokployProviderRecipeDigest(compose, description string) string {
	raw, _ := json.Marshal(struct{ Compose, Description string }{compose, description})
	return DokployRecipeDigest(raw)
}

// Description binds an activation to the private .env by digest only. Its
// contents never enter provider configuration/state or the platform API.
func (o *DokployWorkloadPreparation) Description(d SelectedPaaSWorkloadDeployment, p NativeWorkloadCompose) (string, error) {
	root, err := confinedfs.Open(p.Directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	view, err := root.View(".")
	if err != nil {
		return "", err
	}
	file, err := view.Open(filepath.Base(p.EnvFile))
	if err != nil {
		return "", err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return "", errors.New("Dokploy node environment exceeds its private recipe boundary")
	}
	defer clear(raw)
	return "StackKits " + d.ArtifactDigest + " env=" + DokployRecipeDigest(raw), nil
}

// RequirePrivateEnvironment proves that the actual local Dokploy Compose
// process can read the same file. A host path alone is insufficient when
// Dokploy runs in its standard Swarm container. No mount is added here.
func (o *DokployWorkloadPreparation) RequirePrivateEnvironment(ctx context.Context, p NativeWorkloadCompose, description string) error {
	deny := errors.New("Dokploy requires one local dokploy Swarm task with the exact node-private environment file readable at its original path; explicitly provision that read-only path before adoption or apply")
	_, digest, ok := strings.Cut(description, " env=sha256:")
	if !ok || !dokployContainerID.MatchString(digest) {
		return deny
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := runClosedDockerCLI(ctx, []string{"ps", "--filter", "label=com.docker.swarm.service.name=dokploy", "--format", "{{.ID}}", "--no-trunc"})
	if err != nil {
		return deny
	}
	ids := strings.Fields(string(raw))
	if len(ids) != 1 || !dokployContainerID.MatchString(ids[0]) {
		return deny
	}
	environmentPath := filepath.Join(p.Directory, p.EnvFile)
	raw, err = runClosedDockerCLI(ctx, []string{"inspect", "--format", "{{json .Mounts}}", ids[0]})
	if err != nil {
		return deny
	}
	var mounts []dockerContainerMount
	if json.Unmarshal(raw, &mounts) != nil {
		return deny
	}
	var covering dockerContainerMount
	for _, mount := range mounts {
		if !filepath.IsAbs(mount.Destination) {
			continue
		}
		relative, err := filepath.Rel(mount.Destination, environmentPath)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && len(mount.Destination) > len(covering.Destination) {
			covering = mount
		}
	}
	if covering.Type != "bind" || covering.RW || covering.Source != covering.Destination {
		return deny
	}
	raw, err = runClosedDockerCLI(ctx, []string{"exec", ids[0], "sha256sum", environmentPath})
	if err != nil {
		return deny
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 1 || fields[0] != digest {
		return deny
	}
	return nil
}

type DokployAdoptionRecipe struct {
	ProjectName string `json:"projectName"`
	Compose     string `json:"compose"`
}

// PrepareAdoptionRecipe prepares local files without platform mutation. The
// owner can configure the exact existing service and retained named volumes
// before explicit adoption; no display name selects an upstream service.
func (o *DokployWorkloadPreparation) PrepareAdoptionRecipe(ctx context.Context, d SelectedPaaSWorkloadDeployment) (DokployAdoptionRecipe, error) {
	var empty DokployAdoptionRecipe
	if err := o.ValidateDeployment(d); err != nil {
		return empty, err
	}
	project := "stackkit-" + d.WorkloadRef + "-" + d.NodeRef
	present, err := platformdeploy.HasOwnerHostDeployment(o.workspace, project)
	if err != nil {
		return empty, err
	}
	if present {
		if _, err := o.HeldDeployment(d); err != nil {
			return empty, err
		}
	}
	if _, err := LoadOwnerDokployProviderConfig(ctx, o.workspace, d.SiteRef, d.NodeRef); err != nil {
		return empty, err
	}
	p, err := o.PrepareWorkloadCompose(ctx, d)
	if err != nil {
		return empty, err
	}
	payload, err := o.ProviderCompose(p)
	if err != nil {
		return empty, err
	}
	description, err := o.Description(d, p)
	if err != nil {
		return empty, err
	}
	if err := o.RequirePrivateEnvironment(ctx, p, description); err != nil {
		return empty, err
	}
	return DokployAdoptionRecipe{ProjectName: project, Compose: string(payload)}, nil
}

// ValidateDeployment binds the compiled Vault artifact to the established
// owner node before any file preparation, IdP change or provider process.
func (o *DokployWorkloadPreparation) ValidateDeployment(d SelectedPaaSWorkloadDeployment) error {
	owner, err := localevidence.LoadOwnerCustody(o.workspace)
	if err != nil {
		return err
	}
	if d.ModuleRef != DokployVaultModule || d.WorkloadRef != "vault" || d.SiteRef != owner.Binding.SiteRef ||
		d.NodeRef != owner.Binding.NodeRef || d.ExecutionChannelRef != owner.Binding.ChannelRef ||
		d.InstanceRef == "" || d.ArtifactRef == "" || d.ArtifactDigest != DokployRecipeDigest(d.Bundle) ||
		d.RuntimeAdapter.ID != "dokploy" || d.RuntimeAdapter.ProviderRef != "stackkits-dokploy" || d.RuntimeAdapter.ModuleRef != "stackkits-dokploy-runtime" {
		return errors.New("Dokploy opt-in requires the exact owner-node Vault artifact and runtime adapter")
	}
	bundle, err := architecturev2renderer.ParseVaultwardenWorkloadBundle(d.Bundle)
	if err != nil {
		return err
	}
	if bundle.WorkloadRef != d.WorkloadRef || bundle.ModuleRef != d.ModuleRef || bundle.Release != d.Release ||
		bundle.SiteRef != d.SiteRef || bundle.NodeRef != d.NodeRef || bundle.InstanceRef != d.InstanceRef ||
		normalizeApplicationDeliveryRoute(bundle.Route) != normalizeApplicationDeliveryRoute(d.Route) {
		return errors.New("Dokploy Vault artifact differs from the requested node, release or route")
	}
	return nil
}

func (o *DokployWorkloadPreparation) PrepareWorkloadCompose(ctx context.Context, d SelectedPaaSWorkloadDeployment) (NativeWorkloadCompose, error) {
	if err := o.ValidateDeployment(d); err != nil {
		return NativeWorkloadCompose{}, err
	}
	return o.prepared.PrepareWorkloadCompose(ctx, d)
}

// ProviderCompose makes data/network names independent of Dokploy's random
// Docker project, and keeps secret material in existing node-private files.
func (o *DokployWorkloadPreparation) ProviderCompose(p NativeWorkloadCompose) ([]byte, error) {
	return platformNodeCompose(p)
}

func (o *DokployWorkloadPreparation) CompleteWorkloadCompose(ctx context.Context, p NativeWorkloadCompose, physicalProject string) error {
	p.project.runtimeProject = physicalProject
	return o.prepared.CompleteWorkloadCompose(ctx, p)
}

// HeldDeployment uses the canonical signed platform-deployment.json authority.
// A missing or invalid record never permits adopting by display name.
func (o *DokployWorkloadPreparation) HeldDeployment(d SelectedPaaSWorkloadDeployment) (platformdeploy.HostDeployment, error) {
	var empty platformdeploy.HostDeployment
	if err := o.ValidateDeployment(d); err != nil {
		return empty, err
	}
	state, err := platformdeploy.LoadOwnerHostDeployment(o.workspace, "stackkit-"+d.WorkloadRef+"-"+d.NodeRef)
	if err != nil {
		return empty, err
	}
	if platformdeploy.RequireHostDeploymentManager(state, "dokploy") != nil || state.Signature == nil ||
		state.Manager != "dokploy" || state.WorkloadRef != d.WorkloadRef || state.InstanceRef != d.InstanceRef ||
		state.SiteRef != d.SiteRef || state.NodeRef != d.NodeRef || state.ServerID != "" {
		return empty, errors.New("Dokploy requires signed exact-node workload custody; use explicit adoption or reconcile the existing manager")
	}
	return state, nil
}

// Adopt explicitly binds an already-running, byte-identical generated service.
// No project, service, container or volume is created or replaced here.
func (o *DokployWorkloadPreparation) Adopt(ctx context.Context, d SelectedPaaSWorkloadDeployment, composeID string) (platformdeploy.HostDeployment, error) {
	var empty platformdeploy.HostDeployment
	if err := o.ValidateDeployment(d); err != nil {
		return empty, err
	}
	project := "stackkit-" + d.WorkloadRef + "-" + d.NodeRef
	present, err := platformdeploy.HasOwnerHostDeployment(o.workspace, project)
	if err != nil {
		return empty, err
	}
	if present {
		state, err := o.HeldDeployment(d)
		if err != nil {
			return empty, err
		}
		if state.Deployment.ExternalID != composeID || state.ArtifactDigest != d.ArtifactDigest {
			return empty, errors.New("Dokploy adoption cannot replace an existing held ID or artifact")
		}
	}
	cfg, err := LoadOwnerDokployProviderConfig(ctx, o.workspace, d.SiteRef, d.NodeRef)
	if err != nil {
		return empty, err
	}
	if cfg.ServerID != "" {
		return empty, errors.New("Dokploy opt-in admits only its own node, without an invented server ID")
	}
	ref := platformdeploy.DeploymentRef{Platform: "dokploy", AppName: project, ExternalID: composeID}
	observed, err := platformdeploy.NewDokployAdapter(cfg).RequireProviderCompose(ctx, ref)
	if err != nil {
		return empty, err
	}
	p, err := o.PrepareWorkloadCompose(ctx, d)
	if err != nil {
		return empty, err
	}
	payload, err := o.ProviderCompose(p)
	if err != nil {
		return empty, err
	}
	if !bytes.Equal(payload, []byte(observed.ComposeFile)) {
		return empty, errors.New("Dokploy adoption requires the exact generated node-file Compose recipe with explicit retained volume names")
	}
	description, err := o.Description(d, p)
	if err != nil {
		return empty, err
	}
	if err := o.RequirePrivateEnvironment(ctx, p, description); err != nil {
		return empty, err
	}
	history, err := platformdeploy.NewDokployAdapter(cfg).LatestProviderDeployment(ctx, ref)
	if err != nil {
		return empty, err
	}
	p.project.runtimeProject = observed.AppName
	if err := o.verifyRuntimeBindings(ctx, p.project); err != nil {
		return empty, err
	}
	observation, err := o.prepared.observePreparedWorkload(ctx, d, p.project)
	if err != nil {
		return empty, err
	}
	if err := o.ValidateWorkloadObservation(d, observation); err != nil {
		return empty, err
	}
	ref.DeploymentID = history.DeploymentID
	state := platformdeploy.HostDeployment{WorkloadRef: d.WorkloadRef, InstanceRef: d.InstanceRef, ArtifactDigest: d.ArtifactDigest,
		SiteRef: d.SiteRef, NodeRef: d.NodeRef, Manager: "dokploy", Deployment: ref,
		EnvironmentID: cfg.EnvironmentID, RuntimeProject: observed.AppName, RecipeDigest: DokployProviderRecipeDigest(observed.ComposeFile, observed.Description)}
	if present {
		held, err := o.HeldDeployment(d)
		if err != nil {
			return empty, err
		}
		if held.EnvironmentID != state.EnvironmentID || held.RuntimeProject != state.RuntimeProject || held.RecipeDigest != state.RecipeDigest || held.Deployment.DeploymentID != state.Deployment.DeploymentID {
			return empty, errors.New("Dokploy adoption cannot replace signed placement, recipe or deployment history")
		}
		return held, nil
	}
	if err := platformdeploy.SaveOwnerHostDeployment(o.workspace, project, state); err != nil {
		return empty, err
	}
	state, err = o.HeldDeployment(d)
	if err != nil {
		return empty, err
	}
	return state, nil
}

func (o *DokployWorkloadPreparation) ObserveWorkload(ctx context.Context, d SelectedPaaSWorkloadDeployment) (SelectedPaaSWorkloadObservation, error) {
	state, err := o.HeldDeployment(d)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if state.ArtifactDigest != d.ArtifactDigest {
		return SelectedPaaSWorkloadObservation{}, errors.New("Dokploy custody does not bind the current artifact")
	}
	cfg, err := LoadOwnerDokployProviderConfig(ctx, o.workspace, d.SiteRef, d.NodeRef)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	observed, err := platformdeploy.NewDokployAdapter(cfg).RequireProviderCompose(ctx, state.Deployment)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if observed.AppName != state.RuntimeProject || observed.EnvironmentID != state.EnvironmentID || DokployProviderRecipeDigest(observed.ComposeFile, observed.Description) != state.RecipeDigest {
		return SelectedPaaSWorkloadObservation{}, errors.New("Dokploy API readback differs from signed physical identity or recipe")
	}
	history, err := platformdeploy.NewDokployAdapter(cfg).LatestProviderDeployment(ctx, state.Deployment)
	if err != nil || history.DeploymentID != state.Deployment.DeploymentID {
		return SelectedPaaSWorkloadObservation{}, errors.New("Dokploy deployment history differs from signed successful activation")
	}
	project, err := o.prepared.prepareWithIdentityMutation(ctx, d, false)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if err := o.prepared.verifyPersisted(project); err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	payload, err := o.ProviderCompose(preparedWorkloadCompose(project))
	description, descriptionErr := o.Description(d, preparedWorkloadCompose(project))
	if err != nil || descriptionErr != nil || DokployProviderRecipeDigest(string(payload), description) != state.RecipeDigest {
		return SelectedPaaSWorkloadObservation{}, errors.New("Dokploy runtime recipe differs from current generated node files")
	}
	project.runtimeProject = state.RuntimeProject
	if err := o.verifyRuntimeBindings(ctx, project); err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	result, err := o.prepared.observePreparedWorkload(ctx, d, project)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if err := o.ValidateWorkloadObservation(d, result); err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	return result, nil
}

func (o *DokployWorkloadPreparation) ValidateWorkloadObservation(d SelectedPaaSWorkloadDeployment, observation SelectedPaaSWorkloadObservation) error {
	return o.prepared.ValidateWorkloadObservation(d, observation)
}
