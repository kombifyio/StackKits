package backupexec

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// ApplicationRuntimeBinding is an adapter's exact node-custody translation of
// one held Plan graph. It has no policy, credential or lifecycle custody.
type ApplicationRuntimeBinding struct {
	Runtime         localbackuppolicy.ApplicationRuntime
	PhysicalProject string
	VolumeNames     map[string]string
	Control         func(context.Context, string, string) error
}

func NewDockerV2QuiescerWithApplicationBindings(source localbackuppolicy.Source, verifier ApplicationContainerCustodyVerifier, bindings []ApplicationRuntimeBinding) (ContainerQuiescer, error) {
	if len(source.ApplicationRuntimes) != 0 && verifier == nil {
		return nil, errors.New("application source has no adapter custody verifier")
	}
	_, physical, err := bindApplicationSources(source, bindings)
	if err != nil {
		return nil, err
	}
	byProject := map[string]ApplicationRuntimeBinding{}
	for _, binding := range bindings {
		byProject[binding.PhysicalProject] = binding
	}
	wrapped := func(ctx context.Context, runtime localbackuppolicy.ApplicationRuntime) (map[string]string, error) {
		if binding, bound := byProject[runtime.ComposeProject]; bound {
			logical := runtime
			logical.ComposeProject = binding.Runtime.ComposeProject
			if !reflect.DeepEqual(logical, binding.Runtime) {
				return nil, errors.New("physical runtime differs from its held graph")
			}
			return verifier(ctx, logical)
		}
		return verifier(ctx, runtime)
	}
	quiescer := NewDockerV2QuiescerWithApplicationCustody(source, wrapped).(*dockerV2Quiescer)
	if quiescer.initErr != nil {
		return nil, quiescer.initErr
	}
	quiescer.managed = map[string]struct{}{}
	for _, name := range physical.ManagedVolumeNames {
		quiescer.managed[name] = struct{}{}
	}
	quiescer.applicationVolumes = map[string]struct{}{}
	for _, volume := range physical.ApplicationVolumes {
		quiescer.applicationVolumes[volume.VolumeName] = struct{}{}
	}
	for _, binding := range bindings {
		graph := quiescer.applicationGraphs[binding.Runtime.ComposeProject]
		delete(quiescer.applicationGraphs, binding.Runtime.ComposeProject)
		graph.runtime.ComposeProject = binding.PhysicalProject
		for _, volumes := range graph.selectedVolume {
			for logical, target := range volumes {
				if actual := binding.VolumeNames[logical]; actual != "" {
					delete(volumes, logical)
					volumes[actual] = target
				}
			}
		}
		quiescer.applicationGraphs[binding.PhysicalProject] = graph
	}
	quiescer.applicationControl = func(ctx context.Context, runtime localbackuppolicy.ApplicationRuntime, component, action string) (bool, error) {
		binding, bound := byProject[runtime.ComposeProject]
		if !bound {
			return false, nil
		}
		return true, binding.Control(ctx, component, action)
	}
	if len(bindings) != 0 {
		for project, graph := range quiescer.applicationGraphs {
			if _, managed := byProject[project]; managed {
				continue
			}
			for component, order := range graph.stopOrder {
				graph.stopOrder[component] = 1024 + order
			}
		}
	}
	return quiescer, nil
}

// NewDockerV2SnapshotSettlerWithApplicationBindings uses the same validated
// logical-to-physical source translation as snapshot execution. Settlement
// must verify the actual Kopia mounts before restarting stopped writers.
func NewDockerV2SnapshotSettlerWithApplicationBindings(source localbackuppolicy.Source, bindings []ApplicationRuntimeBinding) (func(context.Context) error, error) {
	return snapshotSettlerWithApplicationBindings(func(timeout time.Duration) snapshotSettlementClient {
		return docker.NewLocalClient(docker.WithTimeout(timeout))
	}, source, bindings)
}

func snapshotSettlerWithApplicationBindings(newClient func(time.Duration) snapshotSettlementClient, source localbackuppolicy.Source, bindings []ApplicationRuntimeBinding) (func(context.Context) error, error) {
	bound, _, err := bindApplicationSources(source, bindings)
	if err != nil {
		return nil, err
	}
	return dockerV2SnapshotSettler(newClient, bound), nil
}

func bindApplicationSources(source localbackuppolicy.Source, bindings []ApplicationRuntimeBinding) (localbackuppolicy.Source, localbackuppolicy.Source, error) {
	physical := source
	physical.ApplicationRuntimes = append([]localbackuppolicy.ApplicationRuntime(nil), source.ApplicationRuntimes...)
	physical.ApplicationVolumes = append([]localbackuppolicy.ApplicationVolume(nil), source.ApplicationVolumes...)
	physical.ManagedVolumeNames = append([]string(nil), source.ManagedVolumeNames...)
	names := map[string]string{}
	seen := map[string]bool{}
	physicalProjects := map[string]bool{}
	for _, binding := range bindings {
		if binding.Runtime.RuntimeAdapterRef != "coolify" || !localbackuppolicy.ValidComposeVolumeName(binding.PhysicalProject) || binding.Control == nil || seen[binding.Runtime.ComposeProject] || physicalProjects[binding.PhysicalProject] {
			return source, physical, errors.New("invalid or repeated adapter-owned application binding")
		}
		found := false
		for i, runtime := range source.ApplicationRuntimes {
			if reflect.DeepEqual(runtime, binding.Runtime) {
				physical.ApplicationRuntimes[i].ComposeProject = binding.PhysicalProject
				found = true
			}
		}
		if !found {
			return source, physical, errors.New("application binding differs from the held Plan graph")
		}
		seen[binding.Runtime.ComposeProject] = true
		physicalProjects[binding.PhysicalProject] = true
		selected := map[string]bool{}
		for i, volume := range source.ApplicationVolumes {
			if volume.ComposeProject != binding.Runtime.ComposeProject {
				continue
			}
			name := binding.VolumeNames[volume.VolumeName]
			if !localbackuppolicy.ValidComposeVolumeName(name) || (name != volume.VolumeName && name != binding.PhysicalProject+"_"+volume.LogicalName) {
				return source, physical, errors.New("application binding has no exact selected physical volume")
			}
			selected[volume.VolumeName] = true
			names[volume.VolumeName] = name
			physical.ApplicationVolumes[i].ComposeProject, physical.ApplicationVolumes[i].VolumeName = binding.PhysicalProject, name
		}
		for logical := range binding.VolumeNames {
			if !selected[logical] {
				return source, physical, errors.New("application binding widens the selected data")
			}
		}
	}
	for _, runtime := range source.ApplicationRuntimes {
		if runtime.RuntimeAdapterRef == "coolify" && !seen[runtime.ComposeProject] {
			return source, physical, errors.New("Coolify source has no adapter-owned node binding")
		}
	}
	for i, name := range physical.ManagedVolumeNames {
		if bound := names[name]; bound != "" {
			physical.ManagedVolumeNames[i] = bound
		}
	}
	bound, err := source.WithRuntimeVolumeNames(names)
	return bound, physical, err
}

func NewDockerV2EngineForPolicyWithApplicationBindings(policy localbackuppolicy.Policy, bindings []ApplicationRuntimeBinding) (V2Engine, error) {
	raw, err := localbackuppolicy.ArtifactBytes(policy)
	if err != nil {
		return V2Engine{}, err
	}
	held, err := localbackuppolicy.Decode(raw)
	if err != nil {
		return V2Engine{}, err
	}
	source, _, err := bindApplicationSources(held.SourceProjection(), bindings)
	if err != nil {
		return V2Engine{}, err
	}
	return newV2EngineForHeldPolicy(dockerV2ExecutorForSource(func(timeout time.Duration) dockerV2Client { return docker.NewLocalClient(docker.WithTimeout(timeout)) }, source), held)
}
