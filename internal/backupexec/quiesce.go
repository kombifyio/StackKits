package backupexec

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// QuiesceMount is the immutable Docker mount identity retained in the
// snapshot operation journal before any container is stopped.
type QuiesceMount struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
	Propagation string `json:"propagation"`
}

// QuiesceContainer is the Docker identity needed to stop and restore one
// container without falling back to a mutable name lookup.
type QuiesceContainer struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Status         string         `json:"status"`
	Running        bool           `json:"running"`
	Paused         bool           `json:"paused"`
	Restarting     bool           `json:"restarting"`
	ExitCode       int            `json:"exitCode"`
	OOMKilled      bool           `json:"oomKilled"`
	Error          string         `json:"error,omitempty"`
	StopSignal     string         `json:"stopSignal,omitempty"`
	WorkloadRef    string         `json:"workloadRef,omitempty"`
	SiteRef        string         `json:"siteRef,omitempty"`
	NodeRef        string         `json:"nodeRef,omitempty"`
	ComposeProject string         `json:"composeProject,omitempty"`
	ComposeService string         `json:"composeService,omitempty"`
	ComponentRef   string         `json:"componentRef,omitempty"`
	Lifecycle      string         `json:"lifecycle,omitempty"`
	Image          string         `json:"image,omitempty"`
	StopOrder      int            `json:"stopOrder,omitempty"`
	Mounts         []QuiesceMount `json:"mounts"`
}

// ContainerQuiescer is the narrow Docker control boundary used by the local
// backup lifecycle. Implementations must use the policy's fixed local daemon;
// no caller supplied socket or container command crosses this interface.
type ContainerQuiescer interface {
	ManagedContainers(context.Context) ([]QuiesceContainer, error)
	InspectContainer(context.Context, string) (QuiesceContainer, error)
	StopContainer(context.Context, string) error
	StartContainer(context.Context, string) error
}

type dockerQuiesceClient interface {
	ListContainers(context.Context, bool) ([]docker.ContainerInfo, error)
	InspectContainer(context.Context, string) (*docker.ContainerInfo, error)
	StopContainer(context.Context, string) error
	StopContainerWithTimeout(context.Context, string, time.Duration) error
	StartContainer(context.Context, string) error
}

type dockerQuiesceClientFactory func(time.Duration) dockerQuiesceClient

// ApplicationContainerCustodyVerifier is the adapter-owned identity boundary
// for a selected Standalone-Compose graph. It must return one exact Docker
// daemon identity per policy component after checking the persisted Compose
// artifact. Health/readiness is deliberately outside this callback so stopped
// containers remain addressable during snapshot recovery.
type ApplicationContainerCustodyVerifier func(context.Context, localbackuppolicy.ApplicationRuntime) (map[string]string, error)

type dockerV2Quiescer struct {
	newClient          dockerQuiesceClientFactory
	managed            map[string]struct{}
	applicationVolumes map[string]struct{}
	applicationGraphs  map[string]dockerApplicationGraph
	applicationCustody ApplicationContainerCustodyVerifier
	initErr            error
}

type dockerApplicationGraph struct {
	runtime        localbackuppolicy.ApplicationRuntime
	components     map[string]localbackuppolicy.ApplicationRuntimeComponent
	stopOrder      map[string]int
	selectedVolume map[string]map[string]string
}

const quiesceStopGrace = 20 * time.Second

// NewDockerV2Quiescer returns the rootful, local Docker controller governed by
// the same source policy as the native Kopia executor.
func NewDockerV2Quiescer(source localbackuppolicy.Source) ContainerQuiescer {
	return dockerV2QuiescerForSource(func(timeout time.Duration) dockerQuiesceClient {
		return docker.NewLocalClient(docker.WithTimeout(timeout))
	}, source)
}

// NewDockerV2QuiescerWithApplicationCustody is the production constructor for
// a source containing Standalone-Compose applications. The caller must pass
// the already admitted adapter authority; a nil verifier deliberately rejects
// application quiescence rather than treating mutable Docker labels as a
// substitute for persisted Compose custody.
func NewDockerV2QuiescerWithApplicationCustody(source localbackuppolicy.Source, verifier ApplicationContainerCustodyVerifier) ContainerQuiescer {
	return dockerV2QuiescerForSourceWithApplicationCustody(func(timeout time.Duration) dockerQuiesceClient {
		return docker.NewLocalClient(docker.WithTimeout(timeout))
	}, source, verifier)
}

func dockerV2QuiescerForSource(newClient dockerQuiesceClientFactory, source localbackuppolicy.Source) ContainerQuiescer {
	return dockerV2QuiescerForSourceWithApplicationCustody(newClient, source, nil)
}

func dockerV2QuiescerForSourceWithApplicationCustody(newClient dockerQuiesceClientFactory, source localbackuppolicy.Source, verifier ApplicationContainerCustodyVerifier) ContainerQuiescer {
	managed := make(map[string]struct{}, len(source.ManagedVolumeNames))
	for _, name := range source.ManagedVolumeNames {
		managed[name] = struct{}{}
	}
	applicationVolumes := make(map[string]struct{}, len(source.ApplicationVolumes))
	for _, application := range source.ApplicationVolumes {
		applicationVolumes[application.VolumeName] = struct{}{}
	}
	if err := localbackuppolicy.ValidateSourceProjection(source); err != nil {
		return &dockerV2Quiescer{newClient: newClient, managed: managed, applicationVolumes: applicationVolumes, initErr: err}
	}
	if len(source.ApplicationRuntimes) > 0 && verifier == nil {
		return &dockerV2Quiescer{
			newClient: newClient, managed: managed, applicationVolumes: applicationVolumes,
			initErr: fmt.Errorf("selected Standalone-Compose application graph has no adapter-owned container custody verifier"),
		}
	}
	graphs := make(map[string]dockerApplicationGraph, len(source.ApplicationRuntimes))
	for _, runtime := range source.ApplicationRuntimes {
		order, err := localbackuppolicy.ApplicationRuntimeStopOrder(runtime)
		if err != nil {
			return &dockerV2Quiescer{newClient: newClient, managed: managed, applicationVolumes: applicationVolumes, initErr: err}
		}
		components := make(map[string]localbackuppolicy.ApplicationRuntimeComponent, len(runtime.Components))
		stopOrder := make(map[string]int, len(order))
		for _, component := range runtime.Components {
			components[component.ComponentRef] = component
		}
		for index, componentRef := range order {
			stopOrder[componentRef] = index
		}
		selectedVolume := make(map[string]map[string]string)
		for _, application := range source.ApplicationVolumes {
			if application.ComposeProject != runtime.ComposeProject {
				continue
			}
			volumes := selectedVolume[application.ComponentRef]
			if volumes == nil {
				volumes = make(map[string]string)
				selectedVolume[application.ComponentRef] = volumes
			}
			volumes[application.VolumeName] = application.Target
		}
		graphs[runtime.ComposeProject] = dockerApplicationGraph{
			runtime: runtime, components: components, stopOrder: stopOrder,
			selectedVolume: selectedVolume,
		}
	}
	return &dockerV2Quiescer{
		newClient: newClient, managed: managed, applicationVolumes: applicationVolumes,
		applicationGraphs: graphs, applicationCustody: verifier,
	}
}

// ManagedContainers returns every governed Core writer and every container in
// a selected application graph. Graph containers are included even without a
// persistent volume so dependent application services cannot keep writing while
// a database volume is snapshotted. Stopped containers are retained so a
// paused/restarting target cannot be silently ignored during validation.
func (q *dockerV2Quiescer) ManagedContainers(ctx context.Context) ([]QuiesceContainer, error) {
	client, err := q.client(ctx)
	if err != nil {
		return nil, err
	}
	admittedIDs, err := q.applicationContainerIDs(ctx)
	if err != nil {
		return nil, err
	}
	listed, err := client.ListContainers(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("list Docker containers for snapshot quiescence: %w", err)
	}
	observed := make([]QuiesceContainer, 0)
	seen := make(map[string]struct{}, len(listed))
	seenGraphComponents := make(map[string]map[string]string, len(q.applicationGraphs))
	for _, listedContainer := range listed {
		if strings.TrimSpace(listedContainer.ID) == "" {
			return nil, fmt.Errorf("Docker container listing contains an empty identity")
		}
		container, err := q.inspectWithClient(ctx, client, listedContainer.ID)
		if err != nil {
			return nil, err
		}
		container, include, err := q.classifyContainer(container)
		if err != nil {
			return nil, err
		}
		if container.ComponentRef != "" {
			if expectedID := admittedIDs[container.ComposeProject][container.ComponentRef]; expectedID != container.ID {
				return nil, fmt.Errorf("Docker container %q is not the adapter-admitted identity for Compose component %q in project %q", container.ID, container.ComponentRef, container.ComposeProject)
			}
		}
		if !include {
			continue
		}
		if _, duplicate := seen[container.ID]; duplicate {
			return nil, fmt.Errorf("Docker container %q appears more than once during snapshot quiescence", container.ID)
		}
		seen[container.ID] = struct{}{}
		if container.ComposeProject != "" {
			services := seenGraphComponents[container.ComposeProject]
			if services == nil {
				services = make(map[string]string)
				seenGraphComponents[container.ComposeProject] = services
			}
			if previousID, duplicate := services[container.ComponentRef]; duplicate {
				return nil, fmt.Errorf("selected Compose graph component %q in project %q has multiple Docker containers (%q and %q)", container.ComponentRef, container.ComposeProject, previousID, container.ID)
			}
			services[container.ComponentRef] = container.ID
		}
		observed = append(observed, container)
	}
	for project, graph := range q.applicationGraphs {
		seenServices := seenGraphComponents[project]
		for componentRef := range graph.components {
			if _, exists := seenServices[componentRef]; !exists {
				return nil, fmt.Errorf("selected Compose graph component %q in project %q has no exact Docker container", componentRef, project)
			}
		}
	}
	slices.SortFunc(observed, func(left, right QuiesceContainer) int {
		leftGraph := left.ComponentRef != ""
		rightGraph := right.ComponentRef != ""
		if leftGraph != rightGraph {
			if leftGraph {
				return -1
			}
			return 1
		}
		if leftGraph && left.ComposeProject != right.ComposeProject {
			return strings.Compare(left.ComposeProject, right.ComposeProject)
		}
		if leftGraph && left.StopOrder != right.StopOrder {
			if left.StopOrder < right.StopOrder {
				return -1
			}
			return 1
		}
		if leftGraph && left.ComponentRef != right.ComponentRef {
			return strings.Compare(left.ComponentRef, right.ComponentRef)
		}
		return strings.Compare(left.ID, right.ID)
	})
	return observed, nil
}

func (q *dockerV2Quiescer) applicationContainerIDs(ctx context.Context) (map[string]map[string]string, error) {
	if len(q.applicationGraphs) == 0 {
		return nil, nil
	}
	if q.applicationCustody == nil {
		return nil, fmt.Errorf("selected Standalone-Compose application graph has no adapter-owned container custody verifier")
	}
	admitted := make(map[string]map[string]string, len(q.applicationGraphs))
	for project, graph := range q.applicationGraphs {
		ids, err := q.applicationCustody(ctx, graph.runtime)
		if err != nil {
			return nil, fmt.Errorf("verify adapter-owned Docker custody for Compose project %q: %w", project, err)
		}
		if len(ids) != len(graph.components) {
			return nil, fmt.Errorf("adapter-owned Docker custody for Compose project %q does not contain exactly one identity per selected component", project)
		}
		seenIDs := make(map[string]string, len(ids))
		for componentRef := range graph.components {
			id := strings.TrimSpace(ids[componentRef])
			if id == "" {
				return nil, fmt.Errorf("adapter-owned Docker custody for Compose component %q in project %q has no exact container identity", componentRef, project)
			}
			if previous, duplicate := seenIDs[id]; duplicate {
				return nil, fmt.Errorf("adapter-owned Docker custody maps Compose components %q and %q in project %q to the same container", previous, componentRef, project)
			}
			seenIDs[id] = componentRef
		}
		for componentRef := range ids {
			if _, exists := graph.components[componentRef]; !exists {
				return nil, fmt.Errorf("adapter-owned Docker custody contains unselected Compose component %q in project %q", componentRef, project)
			}
		}
		admitted[project] = ids
	}
	return admitted, nil
}

func (q *dockerV2Quiescer) verifyApplicationContainerIdentity(ctx context.Context, container QuiesceContainer) error {
	if container.ComponentRef == "" {
		return nil
	}
	admitted, err := q.applicationContainerIDs(ctx)
	if err != nil {
		return err
	}
	if admitted[container.ComposeProject][container.ComponentRef] != container.ID {
		return fmt.Errorf("Docker container %q is not the adapter-admitted identity for Compose component %q in project %q", container.ID, container.ComponentRef, container.ComposeProject)
	}
	return nil
}

func (q *dockerV2Quiescer) InspectContainer(ctx context.Context, id string) (QuiesceContainer, error) {
	client, err := q.client(ctx)
	if err != nil {
		return QuiesceContainer{}, err
	}
	container, err := q.inspectWithClient(ctx, client, id)
	if err != nil {
		return QuiesceContainer{}, err
	}
	classified, _, err := q.classifyContainer(container)
	if err != nil {
		return QuiesceContainer{}, err
	}
	if err := q.verifyApplicationContainerIdentity(ctx, classified); err != nil {
		return QuiesceContainer{}, err
	}
	return classified, nil
}

func (q *dockerV2Quiescer) StopContainer(ctx context.Context, id string) error {
	client, err := q.client(ctx)
	if err != nil {
		return err
	}
	container, err := q.inspectWithClient(ctx, client, id)
	if err != nil {
		return err
	}
	container, _, err = q.classifyContainer(container)
	if err != nil {
		return err
	}
	if err := q.verifyApplicationContainerIdentity(ctx, container); err != nil {
		return err
	}
	if container.Paused || container.Restarting {
		return fmt.Errorf("Docker container %q is paused or restarting", container.ID)
	}
	if !container.Running {
		return nil
	}
	if _, ok := dockerStopSignal(container.StopSignal); !ok {
		return fmt.Errorf("Docker container %q has an unsupported stop signal", container.ID)
	}
	if err := client.StopContainerWithTimeout(ctx, container.ID, quiesceStopGrace); err != nil {
		return fmt.Errorf("stop Docker container %q: %w", container.ID, err)
	}
	stopped, err := q.inspectWithClient(ctx, client, container.ID)
	if err != nil {
		return err
	}
	stopped, _, err = q.classifyContainer(stopped)
	if err != nil {
		return err
	}
	if err := q.verifyApplicationContainerIdentity(ctx, stopped); err != nil {
		return err
	}
	if err := validateCleanDockerStop(stopped, container.StopSignal); err != nil {
		return fmt.Errorf("Docker container %q did not reach a clean stopped state: %w", container.ID, err)
	}
	return nil
}

func (q *dockerV2Quiescer) StartContainer(ctx context.Context, id string) error {
	client, err := q.client(ctx)
	if err != nil {
		return err
	}
	container, err := q.inspectWithClient(ctx, client, id)
	if err != nil {
		return err
	}
	container, _, err = q.classifyContainer(container)
	if err != nil {
		return err
	}
	if err := q.verifyApplicationContainerIdentity(ctx, container); err != nil {
		return err
	}
	if container.Paused || container.Restarting {
		return fmt.Errorf("Docker container %q is paused or restarting", container.ID)
	}
	if container.Lifecycle == "one-shot" {
		return fmt.Errorf("Docker one-shot container %q cannot be restarted by snapshot recovery", container.ID)
	}
	if container.Running {
		return nil
	}
	if err := client.StartContainer(ctx, container.ID); err != nil {
		return fmt.Errorf("start Docker container %q: %w", container.ID, err)
	}
	started, err := q.inspectWithClient(ctx, client, container.ID)
	if err != nil {
		return err
	}
	started, _, err = q.classifyContainer(started)
	if err != nil {
		return err
	}
	if err := q.verifyApplicationContainerIdentity(ctx, started); err != nil {
		return err
	}
	if !started.Running || started.Paused || started.Restarting {
		return fmt.Errorf("Docker container %q did not reach a stable running state", container.ID)
	}
	return nil
}

// IsSuccessfulOneShotCompletion reports the only stopped state that is safe
// to treat as already completed during recovery. It deliberately requires
// the graph-bound lifecycle marker so a Core container's exit is never
// mistaken for application initialization completion.
func IsSuccessfulOneShotCompletion(container QuiesceContainer) bool {
	return container.Lifecycle == "one-shot" && !container.Running && !container.Paused && !container.Restarting &&
		strings.EqualFold(strings.TrimSpace(container.Status), "exited") && container.ExitCode == 0 &&
		!container.OOMKilled && strings.TrimSpace(container.Error) == ""
}

func (q *dockerV2Quiescer) client(ctx context.Context) (dockerQuiesceClient, error) {
	if ctx == nil {
		return nil, fmt.Errorf("snapshot quiescence context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if q == nil || q.newClient == nil {
		return nil, fmt.Errorf("Docker snapshot quiescer is not initialized")
	}
	if q.initErr != nil {
		return nil, fmt.Errorf("Docker snapshot quiescer graph is invalid: %w", q.initErr)
	}
	return q.newClient(dockerTimeout(ctx, QuickOperationTimeout)), nil
}

func (q *dockerV2Quiescer) inspectWithClient(ctx context.Context, client dockerQuiesceClient, id string) (QuiesceContainer, error) {
	if strings.TrimSpace(id) == "" {
		return QuiesceContainer{}, fmt.Errorf("Docker container identity is required")
	}
	info, err := client.InspectContainer(ctx, id)
	if err != nil {
		return QuiesceContainer{}, fmt.Errorf("inspect exact Docker container %q for snapshot quiescence: %w", id, err)
	}
	if info == nil || strings.TrimSpace(info.ID) == "" {
		return QuiesceContainer{}, fmt.Errorf("Docker inspection for %q has no exact identity", id)
	}
	if info.ID != id && !strings.HasPrefix(info.ID, id) {
		return QuiesceContainer{}, fmt.Errorf("Docker inspection identity %q differs from requested %q", info.ID, id)
	}
	name := strings.TrimPrefix(strings.TrimSpace(info.Name), "/")
	if name == "" {
		return QuiesceContainer{}, fmt.Errorf("Docker container %q has no exact name", info.ID)
	}
	mounts := make([]QuiesceMount, 0, len(info.Mounts))
	for _, mount := range info.Mounts {
		mounts = append(mounts, QuiesceMount{
			Type: mount.Type, Name: mount.Name, Source: mount.Source,
			Destination: mount.Destination, RW: mount.RW, Propagation: mount.Propagation,
		})
	}
	slices.SortFunc(mounts, func(left, right QuiesceMount) int {
		if left.Destination != right.Destination {
			return strings.Compare(left.Destination, right.Destination)
		}
		if left.Type != right.Type {
			return strings.Compare(left.Type, right.Type)
		}
		if left.Name != right.Name {
			return strings.Compare(left.Name, right.Name)
		}
		return strings.Compare(left.Source, right.Source)
	})
	labels, err := composeIdentityLabels(info)
	if err != nil {
		return QuiesceContainer{}, err
	}
	image := strings.TrimSpace(info.Config.Image)
	if image == "" {
		image = strings.TrimSpace(info.Image)
	}
	return QuiesceContainer{
		ID: info.ID, Name: name, Status: info.State.Status,
		Running: info.State.Running, Paused: info.State.Paused,
		Restarting: info.State.Restarting, ExitCode: info.State.ExitCode,
		OOMKilled: info.State.OOMKilled, Error: info.State.Error,
		StopSignal: info.Config.StopSignal, ComposeProject: labels["com.docker.compose.project"],
		ComposeService: labels["com.docker.compose.service"], Image: image, Mounts: mounts,
	}, nil
}

func composeIdentityLabels(info *docker.ContainerInfo) (map[string]string, error) {
	labels := make(map[string]string, 2)
	for _, key := range []string{"com.docker.compose.project", "com.docker.compose.service"} {
		listed, listedOK := info.Labels[key]
		configured, configuredOK := info.Config.Labels[key]
		if listedOK && configuredOK && listed != configured {
			return nil, fmt.Errorf("Docker container %q has conflicting Compose %s labels", info.ID, key)
		}
		if configuredOK {
			labels[key] = configured
		} else if listedOK {
			labels[key] = listed
		}
	}
	return labels, nil
}

func (q *dockerV2Quiescer) classifyContainer(container QuiesceContainer) (QuiesceContainer, bool, error) {
	if graph, exists := q.applicationGraphs[container.ComposeProject]; exists {
		if container.ComposeService == "" {
			return QuiesceContainer{}, false, fmt.Errorf("Docker container %q in selected Compose project %q has no service identity", container.ID, container.ComposeProject)
		}
		component, exists := graph.components[container.ComposeService]
		if !exists {
			return QuiesceContainer{}, false, fmt.Errorf("Docker container %q has an unselected service %q in governed Compose project %q", container.ID, container.ComposeService, container.ComposeProject)
		}
		expectedImage := component.ImageRef + "@" + component.ImageDigest
		if container.Image != expectedImage {
			return QuiesceContainer{}, false, fmt.Errorf("Docker service %q in project %q has image %q instead of the selected pinned image", container.ComposeService, container.ComposeProject, container.Image)
		}
		for volumeName, target := range graph.selectedVolume[container.ComposeService] {
			mounted := false
			for _, mount := range container.Mounts {
				if mount.Type == "volume" && mount.Name == volumeName && mount.Destination == target && mount.RW {
					mounted = true
					break
				}
			}
			if !mounted {
				return QuiesceContainer{}, false, fmt.Errorf("Docker service %q in project %q does not hold selected writable volume %q at %q", container.ComposeService, container.ComposeProject, volumeName, target)
			}
		}
		container.WorkloadRef = graph.runtime.WorkloadRef
		container.SiteRef = graph.runtime.SiteRef
		container.NodeRef = graph.runtime.NodeRef
		container.ComponentRef = container.ComposeService
		container.Lifecycle = component.Lifecycle
		container.StopOrder = graph.stopOrder[container.ComponentRef]
		return container, true, nil
	}
	if q.hasApplicationVolumeMount(container.Mounts) {
		return QuiesceContainer{}, false, fmt.Errorf("Docker container %q mounts a selected application volume without exact Compose custody", container.ID)
	}
	// Compose labels and image strings are observations only. Keep them out of
	// the legacy journal so they cannot be mistaken for graph-bound identity.
	container.ComposeProject = ""
	container.ComposeService = ""
	container.ComponentRef = ""
	container.Lifecycle = ""
	container.Image = ""
	return container, q.hasWritableManagedMount(container.Mounts), nil
}

func dockerStopSignal(signal string) (int, bool) {
	signal = strings.ToUpper(strings.TrimSpace(signal))
	if signal == "" {
		signal = "SIGTERM"
	}
	if strings.HasPrefix(signal, "SIG") {
		signal = strings.TrimPrefix(signal, "SIG")
	}
	if number, err := strconv.Atoi(signal); err == nil {
		return number, number == 2 || number == 15
	}
	switch signal {
	case "INT":
		return 2, true
	case "TERM":
		return 15, true
	default:
		return 0, false
	}
}

func validateCleanDockerStop(container QuiesceContainer, stopSignal string) error {
	if container.Running || container.Paused || container.Restarting || !strings.EqualFold(strings.TrimSpace(container.Status), "exited") {
		return fmt.Errorf("container did not reach an exited state")
	}
	if container.OOMKilled {
		return fmt.Errorf("container was OOM-killed")
	}
	if strings.TrimSpace(container.Error) != "" {
		return fmt.Errorf("container reported an exit error")
	}
	signal, ok := dockerStopSignal(stopSignal)
	if !ok || (container.ExitCode != 0 && container.ExitCode != 128+signal) {
		return fmt.Errorf("container exit code %d is not a clean stop", container.ExitCode)
	}
	return nil
}

// ValidateCleanDockerStop verifies the observed post-stop state before a
// journal retry may treat a previously running writer as crash-consistent.
// It is a Docker-level check and does not prove application or database
// shutdown semantics.
// Containers that were already stopped are excluded from the journal and do
// not need this operation result.
func ValidateCleanDockerStop(container QuiesceContainer) error {
	return validateCleanDockerStop(container, container.StopSignal)
}

func (q *dockerV2Quiescer) hasWritableManagedMount(mounts []QuiesceMount) bool {
	for _, mount := range mounts {
		if mount.Type == "volume" && mount.RW {
			if _, ok := q.managed[mount.Name]; ok {
				return true
			}
		}
	}
	return false
}

func (q *dockerV2Quiescer) hasApplicationVolumeMount(mounts []QuiesceMount) bool {
	for _, mount := range mounts {
		if mount.Type == "volume" {
			if _, ok := q.applicationVolumes[mount.Name]; ok {
				return true
			}
		}
	}
	return false
}
