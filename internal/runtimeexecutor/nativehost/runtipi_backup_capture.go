package nativehost

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"gopkg.in/yaml.v3"
)

// CaptureRuntipiAppGroup reads G0 from the existing native renderer, signed node
// deployment and daemon. The caller must already hold the signed Plan/Apply and
// lifecycle lease that selected deployment; this extends the existing signed
// native volume custody with the exact G0 enrollment under that same lease.
// A fresh group requires all daemon components, all running or all stopped.
// It does not admit policy/catalog runtipi support or manufacture a new binding.
func CaptureRuntipiAppGroup(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, binding backuplifecycle.AppGroupBinding) (backuplifecycle.AppGroupControl, error) {
	g := backuplifecycle.AppGroupControl{Version: 2, Binding: binding}
	if deployment.RuntimeAdapter.ID != "runtipi" || binding.LogicalProject != "stackkit-"+deployment.WorkloadRef+"-"+deployment.NodeRef || binding.PhysicalProject != binding.LogicalProject+"__user" || binding.SiteRef != deployment.SiteRef || binding.NodeRef != deployment.NodeRef {
		return g, errors.New("Runtipi capture differs from held workload placement")
	}
	_, secret, err := loadRuntipiTerminalCustody(workspace, binding)
	clear(secret)
	if err != nil {
		return g, err
	}
	state, err := platformdeploy.LoadOwnerHostDeployment(workspace, binding.LogicalProject)
	if err != nil {
		return g, err
	}
	if err = platformdeploy.RequireHostDeploymentManager(state, "runtipi"); err != nil {
		return g, err
	}
	if state.ArtifactDigest != deployment.ArtifactDigest || state.InstanceRef != deployment.InstanceRef || state.SiteRef != binding.SiteRef || state.NodeRef != binding.NodeRef || state.ServerID != binding.InstallationDigest || state.Deployment.ExternalID != binding.AppURN || state.Deployment.AppName != binding.LogicalProject || state.Deployment.ObservedStatus != "running" {
		return g, errors.New("Runtipi capture requires retained applied node/app custody")
	}
	operations, err := NewOSRuntipiWorkloadOperations(workspace)
	if err != nil {
		return g, err
	}
	o := operations.(*osRuntipiWorkloadOperations)
	project, err := o.prepared.prepareWithIdentityMutation(ctx, deployment, false)
	if err != nil {
		return g, err
	}
	if err = o.prepared.verifyPersisted(project); err != nil {
		return g, err
	}
	project.runtimeProject = binding.PhysicalProject
	recipe, revision, err := runtipiNodeCompose(preparedWorkloadCompose(project), deployment.ArtifactDigest)
	if err != nil {
		return g, err
	}
	// This recipe is compiler-owned. The native control's effective-input digest
	// independently binds the installed generated/user Compose closure at Hold.
	var graph struct {
		Services map[string]struct {
			Image   string   `yaml:"image"`
			Volumes []string `yaml:"volumes"`
		} `yaml:"services"`
		Volumes map[string]struct {
			Name     string `yaml:"name"`
			External bool   `yaml:"external"`
		} `yaml:"volumes"`
	}
	if yaml.Unmarshal(recipe, &graph) != nil || len(graph.Services) != len(project.bundle.Components) || len(graph.Volumes) == 0 {
		return g, errors.New("Runtipi capture cannot bind complete external recipe")
	}
	names := map[string]bool{}
	g.Original.ConfigDigests = map[string]string{}
	client := docker.NewLocalClient()
	for _, v := range graph.Volumes {
		if !v.External || !strings.HasPrefix(v.Name, binding.LogicalProject+"_") || names[v.Name] {
			return g, errors.New("Runtipi capture requires exact compiler-owned external volumes")
		}
		names[v.Name] = true
		identity, e := client.InspectVolumeIdentity(ctx, v.Name)
		if e != nil {
			return g, e
		}
		g.Original.Volumes = append(g.Original.Volumes, backuplifecycle.AppGroupVolume{Name: v.Name, Driver: identity.Driver, Mountpoint: identity.Mountpoint, IdentityDigest: backuplifecycle.AppGroupDigest(identity)})
	}
	raw, err := o.prepared.runner.Run(ctx, standaloneComposeArgs(project, "ps"), project.directory)
	if err != nil {
		return g, err
	}
	statuses, err := parseStandaloneComposeStatuses(raw)
	if err != nil {
		return g, err
	}
	ids, err := standaloneComposeContainerIdentities(project.bundle.Components, statuses, true)
	if err != nil {
		return g, err
	}
	if len(statuses) != len(project.bundle.Components) {
		return g, errors.New("Runtipi project has unselected components")
	}
	for _, component := range project.bundle.Components {
		if component.Lifecycle != "daemon" {
			return g, errors.New("one-shot app-group backup remains unqualified")
		}
		c, e := client.InspectContainer(ctx, ids[component.ID])
		if e != nil {
			return g, e
		}
		if c.State.Paused || c.State.Restarting || c.Config.Image != component.ImageRef+"@"+component.ImageDigest || c.Config.Image != graph.Services[component.ID].Image || c.Config.Labels[composeProjectLabel] != binding.PhysicalProject || c.Config.Labels[composeServiceLabel] != component.ID || c.Config.Labels[runtipiArtifactLabel] != deployment.ArtifactDigest || c.Config.Labels[runtipiRecipeLabel] != revision {
			return g, errors.New("Runtipi G0 differs from compiler recipe/revision/power")
		}
		expected := map[string]string{}
		for _, spec := range graph.Services[component.ID].Volumes {
			fields := strings.Split(spec, ":")
			if len(fields) != 2 || graph.Volumes[fields[0]].Name == "" || expected[fields[1]] != "" {
				return g, errors.New("unqualified compiler mount in Runtipi G0")
			}
			expected[fields[1]] = graph.Volumes[fields[0]].Name
		}
		if len(c.Mounts) != len(expected) {
			return g, errors.New("Runtipi G0 mount set differs from compiler recipe")
		}
		captured := backuplifecycle.SnapshotQuiescedContainer{ID: c.ID, Name: strings.TrimPrefix(c.Name, "/"), WasRunning: c.State.Running, WorkloadRef: deployment.WorkloadRef, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, ComposeProject: binding.PhysicalProject, ComposeService: component.ID, ComponentRef: component.ID, Image: c.Config.Image, StopOrder: 0}
		for _, m := range c.Mounts {
			if m.Type != "volume" || !m.RW || expected[m.Destination] != m.Name || m.Source != "/var/lib/docker/volumes/"+m.Name+"/_data" {
				return g, errors.New("Runtipi G0 mount escapes exact external volume recipe")
			}
			captured.Mounts = append(captured.Mounts, backuplifecycle.SnapshotQuiesceMount{Type: m.Type, Name: m.Name, Source: m.Source, Destination: m.Destination, RW: m.RW, Propagation: m.Propagation})
		}
		slices.SortFunc(captured.Mounts, func(a, b backuplifecycle.SnapshotQuiesceMount) int {
			return strings.Compare(a.Destination, b.Destination)
		})
		g.Original.ConfigDigests[component.ID], err = AppGroupConfigDigest(c)
		if err != nil {
			return g, err
		}
		g.Original.Containers = append(g.Original.Containers, captured)
	}
	slices.SortFunc(g.Original.Containers, func(a, b backuplifecycle.SnapshotQuiescedContainer) int {
		return strings.Compare(a.ComponentRef, b.ComponentRef)
	})
	slices.SortFunc(g.Original.Volumes, func(a, b backuplifecycle.AppGroupVolume) int { return strings.Compare(a.Name, b.Name) })
	if err = ensureRuntipiInitialCustody(ctx, workspace, deployment, project, &g); err != nil {
		return g, err
	}
	if err = backuplifecycle.ValidateAppGroups([]backuplifecycle.AppGroupControl{g}); err != nil {
		return g, err
	}
	// Re-inspect after capture to reject foreign writers, changed mounts/config,
	// missing components or a concurrent generation before journal persistence.
	actual, err := ObserveRuntipiAppGroupGeneration(ctx, g, "verify")
	if err != nil {
		return g, err
	}
	if backuplifecycle.AppGroupDigest(actual) != backuplifecycle.AppGroupDigest(g.Original) {
		return g, errors.New("Runtipi G0 changed during capture")
	}
	if err = o.prepared.verifyPersisted(project); err != nil {
		return g, err
	}
	return g, nil
}
