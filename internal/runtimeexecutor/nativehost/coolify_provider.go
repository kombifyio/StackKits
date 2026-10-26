package nativehost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"maps"
	"sort"
	"time"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"gopkg.in/yaml.v3"
)

const coolifyProviderRevisionLabel = "io.stackkit.coolify-recipe-digest"
const coolifyProviderPending = "pending-provider-activation"
const coolifyProviderUncertain = "uncertain-provider-activation"

// ErrCoolifyPendingRevision refuses changes to files an acknowledged remote
// activation can still consume. Callers may observe or retry that same revision.
var ErrCoolifyPendingRevision = errors.New("Coolify has a pending activation; settle its held revision before changing the desired recipe")

// CoolifyProviderWorkload shares preparation with the existing platform owner.
// Only the provider may configure and activate its imported service.
type CoolifyProviderWorkload struct {
	Prepared                    NativeWorkloadCompose
	Config                      platformdeploy.HTTPConfig
	Prior                       platformdeploy.HostDeployment
	Compose                     []byte
	EnvironmentDigest, Revision string
	operations                  *osCoolifyWorkloadOperations
	activationSourceIDs         map[string]string
}

// PrepareCoolifyProviderWorkload admits only an existing signed service. Fresh
// creation stays with the retained adapter until its provider custody journal
// and real-host parity are implemented.
func PrepareCoolifyProviderWorkload(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment) (*CoolifyProviderWorkload, error) {
	ops, err := NewOSCoolifyWorkloadOperations(workspace)
	if err != nil {
		return nil, err
	}
	o := ops.(*osCoolifyWorkloadOperations)
	prior, present, err := o.manager(deployment)
	if err != nil {
		return nil, err
	}
	if !present || prior.Manager != "coolify" || prior.Signature == nil {
		return nil, errors.New("Coolify provider requires explicit import of an existing signed node service")
	}
	if prior.Deployment.ObservedStatus == coolifyProviderUncertain {
		return nil, errors.New("Coolify activation outcome is uncertain; reconcile its held service and provider state before another Apply")
	}
	if prior.Deployment.ObservedStatus == coolifyProviderPending && len(prior.ActivationSourceIDs) == 0 {
		return nil, errors.New("Coolify pending activation has no signed runtime witness; reconcile it explicitly before Apply")
	}
	if prior.PlanBinding != nil && prior.ArtifactDigest != deployment.ArtifactDigest {
		return nil, errors.New("Coolify manager transition binding must authorize the current artifact before provider mutation")
	}
	if prior.Deployment.ObservedStatus == coolifyProviderPending && prior.ArtifactDigest != deployment.ArtifactDigest {
		return nil, ErrCoolifyPendingRevision
	}
	// Render from retained identity without changing runtime files or IdP
	// custody. Pending admission must precede everything an async job reads.
	project, err := o.prepared.prepareWithIdentityMutation(ctx, deployment, false)
	if err != nil {
		return nil, err
	}
	prepared := preparedWorkloadCompose(project)
	compose, err := platformNodeCompose(prepared)
	if err != nil {
		return nil, err
	}
	configured, envDigest, revision, err := coolifyProviderRecipe(prepared, deployment.ArtifactDigest)
	if err != nil {
		return nil, err
	}
	recipeDigest, err := platformdeploy.CoolifyProviderRecipeDigest(string(configured))
	if err != nil {
		return nil, err
	}
	if prior.Deployment.ObservedStatus == coolifyProviderPending && (prior.ArtifactDigest != deployment.ArtifactDigest || prior.RecipeDigest != recipeDigest) {
		return nil, ErrCoolifyPendingRevision
	}
	cfg, _, err := o.configuration(ctx, deployment)
	if err != nil {
		return nil, err
	}
	if cfg.ServerID != prior.ServerID || cfg.ProjectUUID == "" {
		return nil, errors.New("Coolify provider requires its retained node server and curated project")
	}
	expected := []string{string(configured)}
	if prior.RecipeDigest == "" {
		if prior.ArtifactDigest != deployment.ArtifactDigest {
			return nil, errors.New("first Coolify import must retain the exact current workload artifact")
		}
		expected = append(expected, string(compose))
	}
	// The current desired recipe also permits recovery after provider Update
	// succeeded but asynchronous activation or the surrounding Apply failed.
	if err := platformdeploy.NewCoolifyAdapter(cfg).RequireProviderService(ctx, prior.Deployment, prior.RecipeDigest, expected...); err != nil {
		return nil, err
	}
	if err := o.prepared.persist(project); err != nil {
		return nil, err
	}
	return &CoolifyProviderWorkload{Prepared: prepared, Config: cfg, Prior: prior, Compose: configured, EnvironmentDigest: envDigest, Revision: revision, operations: o}, nil
}

func coolifyProviderRecipe(prepared NativeWorkloadCompose, artifactDigest string) ([]byte, string, string, error) {
	compose, err := platformNodeCompose(prepared)
	if err != nil {
		return nil, "", "", err
	}
	envHash := sha256.Sum256(prepared.project.environment)
	envDigest := "sha256:" + hex.EncodeToString(envHash[:])
	hash := sha256.New()
	_, _ = hash.Write([]byte("stackkit.coolify-provider-recipe/v1\x00" + artifactDigest + "\x00" + envDigest + "\x00"))
	_, _ = hash.Write(compose)
	files := make([]string, 0, len(prepared.project.configFiles))
	for name := range prepared.project.configFiles {
		files = append(files, name)
	}
	sort.Strings(files)
	for _, name := range files {
		digest := sha256.Sum256(prepared.project.configFiles[name])
		_, _ = hash.Write([]byte("\x00" + name + "\x00" + hex.EncodeToString(digest[:])))
	}
	revision := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	var graph map[string]any
	if err := yaml.Unmarshal(compose, &graph); err != nil {
		return nil, "", "", err
	}
	services, ok := graph["services"].(map[string]any)
	if !ok || len(services) == 0 {
		return nil, "", "", errors.New("Coolify provider requires a workload service graph")
	}
	for _, entry := range services {
		service, ok := entry.(map[string]any)
		if !ok {
			return nil, "", "", errors.New("Coolify workload service is invalid")
		}
		labels, _ := service["labels"].(map[string]any)
		if labels == nil {
			labels = map[string]any{}
		}
		labels[coolifyProviderRevisionLabel] = revision
		service["labels"] = labels
	}
	configured, err := yaml.Marshal(graph)
	return configured, envDigest, revision, err
}

func (p *CoolifyProviderWorkload) runtimeStatuses(ctx context.Context) (map[string]standaloneComposePS, error) {
	project := p.Prepared.project
	project.runtimeProject = p.Prior.Deployment.ExternalID
	raw, err := p.operations.prepared.runner.Run(ctx, standaloneComposeArgs(project, "ps"), project.directory)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]standaloneComposePS{}, nil
	}
	return parseStandaloneComposeStatuses(raw)
}

// ActivationSource observes the actual runtime before a saved plan is allowed
// to queue restart. Empty IDs witness components that do not exist yet.
func (p *CoolifyProviderWorkload) ActivationSource(ctx context.Context) (map[string]string, error) {
	statuses, err := p.runtimeStatuses(ctx)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]string, len(p.Prepared.project.bundle.Components))
	for _, component := range p.Prepared.project.bundle.Components {
		if status, present := statuses[component.ID]; present && status.ID == "" {
			return nil, errors.New("Coolify activation source has an unidentified container")
		}
		ids[component.ID] = statuses[component.ID].ID
	}
	return ids, nil
}

// RuntimeCurrent requires the desired labels and, for an unsettled activation,
// a runtime that replaced every previously observed container.
func (p *CoolifyProviderWorkload) RuntimeCurrent(ctx context.Context) (bool, error) {
	statuses, err := p.runtimeStatuses(ctx)
	if err != nil {
		return false, err
	}
	for _, component := range p.Prepared.project.bundle.Components {
		status, present := statuses[component.ID]
		if !present || status.ID == "" || standaloneComposeCSVMap(status.Labels)[coolifyProviderRevisionLabel] != p.Revision {
			return false, nil
		}
		if component.Lifecycle == "daemon" && status.State != "running" {
			return false, nil
		}
		if p.Prior.Deployment.ObservedStatus == coolifyProviderPending || p.Prior.Deployment.ObservedStatus == coolifyProviderUncertain {
			previous, witnessed := p.Prior.ActivationSourceIDs[component.ID]
			if !witnessed || previous != "" && status.ID == previous {
				return false, nil
			}
		}
	}
	return true, nil
}

// ActivationPending joins an acknowledged asynchronous request. An observer
// timeout never proves its remote job stopped and cannot authorize a restart.
func (p *CoolifyProviderWorkload) ActivationPending() bool {
	return p.Prior.Deployment.ObservedStatus == coolifyProviderPending
}

// BeginActivation journals uncertainty before the provider can request a
// restart. A crash or transport failure cannot authorize a duplicate request.
func (p *CoolifyProviderWorkload) BeginActivation(deployment SelectedPaaSWorkloadDeployment, sourceIDs map[string]string) error {
	if len(sourceIDs) == 0 {
		return errors.New("Coolify activation requires a complete pre-activation runtime witness")
	}
	digest, err := platformdeploy.CoolifyProviderRecipeDigest(string(p.Compose))
	if err != nil {
		return err
	}
	state := p.Prior
	state.ArtifactDigest = deployment.ArtifactDigest
	state.RecipeDigest = digest
	state.ActivationSourceIDs = maps.Clone(sourceIDs)
	state.Deployment.ObservedStatus = coolifyProviderUncertain
	state.Deployment.LastDeployed = time.Now().UTC()
	if err := platformdeploy.SaveOwnerHostDeployment(p.operations.workspace, p.Prepared.ProjectName, state); err != nil {
		return err
	}
	p.activationSourceIDs = maps.Clone(sourceIDs)
	return nil
}

// Complete waits for the provider-started revision and the established native
// application checks. Custody records desired intent before these checks so an
// interrupted asynchronous activation can be resumed with the same exact ID.
func (p *CoolifyProviderWorkload) Complete(ctx context.Context, deployment SelectedPaaSWorkloadDeployment, activated bool) error {
	digest, err := platformdeploy.CoolifyProviderRecipeDigest(string(p.Compose))
	if err != nil {
		return err
	}
	state := p.Prior
	state.ArtifactDigest = deployment.ArtifactDigest
	state.RecipeDigest = digest
	if activated {
		state.Deployment.ObservedStatus = coolifyProviderPending
		state.Deployment.LastDeployed = time.Now().UTC()
		state.ActivationSourceIDs = maps.Clone(p.activationSourceIDs)
	}
	if err := platformdeploy.SaveOwnerHostDeployment(p.operations.workspace, p.Prepared.ProjectName, state); err != nil {
		return err
	}
	p.Prepared.project.runtimeProject = state.Deployment.ExternalID
	if err := p.operations.prepared.waitForComponentReadiness(ctx, p.Prepared.project, func(statuses map[string]standaloneComposePS) (bool, error) {
		for _, component := range p.Prepared.project.bundle.Components {
			status, present := statuses[component.ID]
			if !present || status.ID == "" || standaloneComposeCSVMap(status.Labels)[coolifyProviderRevisionLabel] != p.Revision {
				return false, nil
			}
			if state.Deployment.ObservedStatus == coolifyProviderPending {
				previous, witnessed := state.ActivationSourceIDs[component.ID]
				if !witnessed || previous != "" && status.ID == previous {
					return false, nil
				}
			}
		}
		return true, nil
	}); err != nil {
		return err
	}
	if err := p.operations.prepared.CompleteWorkloadCompose(ctx, p.Prepared); err != nil {
		return err
	}
	if _, err := ObserveCoolifyProviderWorkload(ctx, p.operations.workspace, deployment); err != nil {
		return err
	}
	state.Deployment.ObservedStatus = "applied"
	state.ActivationSourceIDs = nil
	return platformdeploy.SaveOwnerHostDeployment(p.operations.workspace, p.Prepared.ProjectName, state)
}

// ObserveCoolifyProviderWorkload is read-only and reconstructs the authorized
// revision from existing node custody rather than trusting API desired state.
func ObserveCoolifyProviderWorkload(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment) (SelectedPaaSWorkloadObservation, error) {
	o, state, project, err := coolifyOwnedWorkload(ctx, workspace, deployment)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	prepared := preparedWorkloadCompose(project)
	compose, envDigest, revision, err := coolifyProviderRecipe(prepared, deployment.ArtifactDigest)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	cfg, _, err := o.configuration(ctx, deployment)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if cfg.ServerID != state.ServerID {
		return SelectedPaaSWorkloadObservation{}, errors.New("Coolify provider observation changed the held server")
	}
	digest, err := platformdeploy.CoolifyProviderRecipeDigest(string(compose))
	if err != nil || state.RecipeDigest != digest {
		return SelectedPaaSWorkloadObservation{}, errors.New("Coolify provider observation differs from signed recipe custody")
	}
	if err := platformdeploy.NewCoolifyAdapter(cfg).RequireProviderService(ctx, state.Deployment, "", string(compose)); err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	p := &CoolifyProviderWorkload{Prepared: prepared, Config: cfg, Prior: state, Compose: compose, EnvironmentDigest: envDigest, Revision: revision, operations: o}
	current, err := p.RuntimeCurrent(ctx)
	if err != nil {
		return SelectedPaaSWorkloadObservation{}, err
	}
	if !current {
		return SelectedPaaSWorkloadObservation{}, errors.New("Coolify provider runtime has not reached the authorized workload revision")
	}
	return o.ObserveWorkload(ctx, deployment)
}
