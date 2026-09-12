package runtimeexecutorlocal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localorigin"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
	"github.com/kombifyio/stackkits/pkg/workloadremoval"
)

const (
	composeProjectLabel     = "com.docker.compose.project"
	composeServiceLabel     = "com.docker.compose.service"
	composeWorkingDirLabel  = "com.docker.compose.project.working_dir"
	composeConfigFilesLabel = "com.docker.compose.project.config_files"
	composeVolumeLabel      = "com.docker.compose.volume"
	standaloneComposeOwner  = "standalone-compose"
	volumeCustodyAPIVersion = "stackkit.workload-removal-volume-custody/v1"
	volumeCustodyRelDir     = ".stackkit/evidence/removal/volume-custody"
)

var (
	errVolumeCustodyAbsent  = errors.New("standalone Compose volume custody is absent")
	errVolumeCustodyInvalid = errors.New("standalone Compose volume custody is invalid")
)

// RemovalProgressError records whether runtime mutation started. Callers must
// retain recovery state when Progressed is true instead of treating the
// workload as unchanged.
type RemovalProgressError struct {
	Err        error
	Progressed bool
}

func (e *RemovalProgressError) Error() string {
	if e == nil || e.Err == nil {
		return "standalone Compose removal failed"
	}
	return e.Err.Error()
}

func (e *RemovalProgressError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type standaloneComposeDockerCLI interface {
	Run(context.Context, []string) ([]byte, error)
}

type standaloneComposeRemover struct {
	ops    *osStandaloneComposeWorkloadOperations
	docker standaloneComposeDockerCLI
}

type standaloneComposeRemovalObservation struct {
	Project         string                                      `json:"project"`
	WorkingDir      string                                      `json:"workingDir"`
	DataDisposition string                                      `json:"dataDisposition"`
	Containers      []standaloneComposeRemovalContainerEvidence `json:"containers"`
	Volumes         []standaloneComposeRemovalVolumeEvidence    `json:"volumes"`
}

type standaloneComposeRemovalContainerEvidence struct {
	Service     string `json:"service"`
	ContainerID string `json:"containerId,omitempty"`
	State       string `json:"state"`
}

type standaloneComposeRemovalVolumeEvidence struct {
	Key    string `json:"key"`
	Name   string `json:"name,omitempty"`
	State  string `json:"state"`
	Driver string `json:"driver,omitempty"`
}

type standaloneComposeOwnedVolume struct {
	Key         string
	ComponentID string
	VolumeID    string
	Target      string
	HostPath    string
}

type dockerVolumeInspect struct {
	CreatedAt  string            `json:"CreatedAt"`
	Driver     string            `json:"Driver"`
	Labels     map[string]string `json:"Labels"`
	Mountpoint string            `json:"Mountpoint"`
	Name       string            `json:"Name"`
	Options    map[string]string `json:"Options"`
	Scope      string            `json:"Scope"`
}

type dockerContainerMount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	Driver      string `json:"Driver"`
	RW          bool   `json:"RW"`
}

type dockerContainerInspect struct {
	ID     string `json:"Id"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	Mounts []dockerContainerMount `json:"Mounts"`
}

type standaloneComposePresentContainer struct {
	Service     string
	ID          string
	Image       string
	WorkingDir  string
	ConfigFiles string
	Mounts      []dockerContainerMount
}

type standaloneComposeVolumeCustody struct {
	APIVersion           string                                `json:"apiVersion"`
	AppliedRequestDigest string                                `json:"appliedRequestDigest"`
	WorkloadRef          string                                `json:"workloadRef"`
	InstanceRef          string                                `json:"instanceRef"`
	Project              string                                `json:"project"`
	WorkingDir           string                                `json:"workingDir"`
	EntryContainerID     string                                `json:"entryContainerId"`
	Volumes              []standaloneComposeVolumeCustodyEntry `json:"volumes"`
}

type standaloneComposeVolumeCustodyEntry struct {
	Key        string            `json:"key"`
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Target     string            `json:"target"`
	CreatedAt  string            `json:"createdAt"`
	Mountpoint string            `json:"mountpoint"`
	Scope      string            `json:"scope"`
	Options    map[string]string `json:"options"`
}

type standaloneComposeVolumeCustodyRecord struct {
	Custody   standaloneComposeVolumeCustody                `json:"custody"`
	Signature localevidence.OwnerLifecycleMutationSignature `json:"signature"`
}

// RemoveStandaloneComposeWorkload removes one applied standalone Compose
// workload using the sealed owner request. It never rewrites generated
// Compose files and never deletes bind-mounted media or undeclared volumes.
func RemoveStandaloneComposeWorkload(
	ctx context.Context,
	workspace string,
	request workloadremoval.Request,
) (workloadremoval.Result, error) {
	operations, err := NewOSStandaloneComposeWorkloadOperations(workspace)
	if err != nil {
		return workloadremoval.Result{}, err
	}
	ops, ok := operations.(*osStandaloneComposeWorkloadOperations)
	if !ok {
		return workloadremoval.Result{}, errors.New("standalone Compose operations have an unexpected implementation")
	}
	return (standaloneComposeRemover{ops: ops}).remove(ctx, request)
}

func (r standaloneComposeRemover) dockerCLI() standaloneComposeDockerCLI {
	if r.docker != nil {
		return r.docker
	}
	return osStandaloneComposeDockerCLI{}
}

func (r standaloneComposeRemover) remove(
	ctx context.Context,
	request workloadremoval.Request,
) (result workloadremoval.Result, err error) {
	progressed := false
	defer func() {
		if err == nil {
			return
		}
		var progress *RemovalProgressError
		if errors.As(err, &progress) {
			return
		}
		err = &RemovalProgressError{Err: err, Progressed: progressed}
	}()
	if ctx == nil {
		return workloadremoval.Result{}, errors.New("standalone Compose removal requires a context")
	}
	if err := ctx.Err(); err != nil {
		return workloadremoval.Result{}, err
	}
	if r.ops == nil {
		return workloadremoval.Result{}, errors.New("standalone Compose operations are not initialized")
	}
	validUntil, err := time.Parse(time.RFC3339Nano, request.ValidUntil)
	if err != nil {
		return workloadremoval.Result{}, fmt.Errorf("workload-removal validity is unreadable: %w", err)
	}
	ctx, cancel := context.WithDeadline(ctx, validUntil)
	defer cancel()
	if err := r.requireFresh(ctx, request); err != nil {
		return workloadremoval.Result{}, err
	}
	payload, err := request.AuthorizationBytes()
	if err != nil {
		return workloadremoval.Result{}, err
	}
	if err := localevidence.VerifyOwnerLifecycleMutation(r.ops.workspaceRoot, payload, localevidence.OwnerLifecycleMutationSignature{
		OwnerRef: request.Authorization.OwnerRef, KeyID: request.Authorization.KeyID, Value: request.Authorization.Value,
	}); err != nil {
		return workloadremoval.Result{}, fmt.Errorf("verify workload-removal Owner authorization: %w", err)
	}
	deployment, err := standaloneComposeDeploymentFromRemovalRequest(request)
	if err != nil {
		return workloadremoval.Result{}, err
	}
	project, err := r.ops.prepare(ctx, deployment)
	if err != nil {
		return workloadremoval.Result{}, err
	}
	if err := r.ops.verifyPersisted(project); err != nil {
		return workloadremoval.Result{}, err
	}
	owned, hostPaths := standaloneComposeVolumeDeclarations(project.bundle)
	if err := standaloneComposeRejectUnsafeDelete(request.DataDisposition, hostPaths); err != nil {
		return workloadremoval.Result{}, err
	}
	statuses, err := r.readComposeStatuses(ctx, project)
	if err != nil {
		return workloadremoval.Result{}, err
	}
	if err := r.ops.verifyPersisted(project); err != nil {
		return workloadremoval.Result{}, err
	}
	present, err := r.inspectPresentContainers(ctx, project, statuses)
	if err != nil {
		return workloadremoval.Result{}, err
	}
	volumes, err := r.admitOwnedVolumes(ctx, request, project, owned, present)
	if err != nil {
		return workloadremoval.Result{}, err
	}
	if err := r.requireFresh(ctx, request); err != nil {
		return workloadremoval.Result{}, err
	}
	if len(present) != 0 {
		progressed = true
		if err := r.stopAndRemoveContainers(ctx, present); err != nil {
			return workloadremoval.Result{}, err
		}
	}
	if err := r.verifyAdmittedContainersAbsent(ctx, project, present); err != nil {
		return workloadremoval.Result{}, err
	}
	if err := r.withdrawOrigin(request, project); err != nil {
		return workloadremoval.Result{}, err
	}
	if request.DataDisposition == workloadremoval.DataDispositionDelete {
		if err := r.requireFresh(ctx, request); err != nil {
			return workloadremoval.Result{}, err
		}
		progressed = true
		if err := r.deleteOwnedVolumes(ctx, project, volumes); err != nil {
			return workloadremoval.Result{}, err
		}
	}
	finalVolumes, err := r.observeAdmittedVolumes(ctx, project, volumes, request.DataDisposition)
	if err != nil {
		return workloadremoval.Result{}, err
	}
	if err := r.ops.verifyPersisted(project); err != nil {
		return workloadremoval.Result{}, err
	}
	after, err := r.readComposeStatuses(ctx, project)
	if err != nil {
		return workloadremoval.Result{}, err
	}
	observation, err := observeStandaloneComposeRemoval(project, request.DataDisposition, after, owned, hostPaths, finalVolumes)
	if err != nil {
		return workloadremoval.Result{}, err
	}
	canonical, err := resolvedplan.CanonicalJSON(observation)
	if err != nil {
		return workloadremoval.Result{}, fmt.Errorf("canonicalize standalone Compose removal observation: %w", err)
	}
	sum := sha256.Sum256(canonical)
	target := request.Applied.RuntimeTargets[0]
	if err := r.requireFresh(ctx, request); err != nil {
		return workloadremoval.Result{}, err
	}
	return workloadremoval.NewResult(request, time.Now().UTC(), workloadremoval.Outcome{
		RequirementID:     target.RequirementID,
		WorkloadRef:       request.WorkloadRef,
		InstanceRef:       target.InstanceRef,
		RuntimeOwnerRef:   standaloneComposeOwner,
		ArtifactDigest:    appliedStandaloneArtifactDigest(request),
		DataDisposition:   request.DataDisposition,
		Status:            workloadremoval.StatusRemoved,
		ObservedState:     workloadremoval.ObservedStateAbsent,
		ObservationRef:    "removal-observation://standalone-compose/" + request.WorkloadRef + "/" + target.InstanceRef,
		ObservationDigest: "sha256:" + hex.EncodeToString(sum[:]),
	})
}

func (r standaloneComposeRemover) requireFresh(ctx context.Context, request workloadremoval.Request) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("workload-removal authorization expired before mutation: %w", err)
	}
	if err := request.ValidateAt(time.Now().UTC()); err != nil {
		return fmt.Errorf("validate standalone Compose workload removal: %w", err)
	}
	return nil
}

func appliedStandaloneArtifactDigest(request workloadremoval.Request) string {
	target := request.Applied.RuntimeTargets[0]
	if len(target.ArtifactRefs) != 1 {
		return ""
	}
	for _, artifact := range request.Applied.Artifacts {
		if artifact.ID == target.ArtifactRefs[0] {
			return artifact.Digest
		}
	}
	return ""
}

func standaloneComposeDeploymentFromRemovalRequest(request workloadremoval.Request) (SelectedPaaSWorkloadDeployment, error) {
	target := request.Applied.RuntimeTargets[0]
	if target.RuntimeAdapter == nil || target.RuntimeAdapter.ID != standaloneComposeAdapterRef ||
		target.RuntimeAdapter.ModuleRef != standaloneComposeModuleRef {
		return SelectedPaaSWorkloadDeployment{}, errors.New("applied workload is not bound to the standalone Compose adapter")
	}
	if len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 {
		return SelectedPaaSWorkloadDeployment{}, errors.New("standalone Compose removal requires one exact Site and node")
	}
	if len(target.ArtifactRefs) != 1 {
		return SelectedPaaSWorkloadDeployment{}, errors.New("standalone Compose removal requires exactly one applied workload artifact")
	}
	var artifactContent []byte
	var artifactID, artifactDigest string
	var adapterArtifacts []runtimeexecutor.Artifact
	for _, artifact := range request.Applied.Artifacts {
		if artifact.ID == target.ArtifactRefs[0] {
			artifactContent = append([]byte(nil), artifact.Content...)
			artifactID, artifactDigest = artifact.ID, artifact.Digest
			continue
		}
		adapterArtifacts = append(adapterArtifacts, artifact)
		if slicesContainsString(target.RuntimeAdapter.ArtifactRefs, artifact.ID) {
			if err := requireStandaloneComposeRemovalAdmission(artifact.Content); err != nil {
				return SelectedPaaSWorkloadDeployment{}, err
			}
		}
	}
	if len(artifactContent) == 0 || artifactDigest == "" {
		return SelectedPaaSWorkloadDeployment{}, errors.New("applied standalone Compose workload artifact is absent")
	}
	sum := sha256.Sum256(artifactContent)
	if artifactDigest != "sha256:"+hex.EncodeToString(sum[:]) {
		return SelectedPaaSWorkloadDeployment{}, errors.New("applied standalone Compose workload artifact digest does not match its content")
	}
	bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(artifactContent)
	if err != nil {
		return SelectedPaaSWorkloadDeployment{}, fmt.Errorf("validate applied standalone workload bundle: %w", err)
	}
	if bundle.WorkloadRef != target.WorkloadRef || bundle.ModuleRef != target.ModuleRef ||
		bundle.InstanceRef != target.InstanceRef || bundle.SiteRef != target.SiteRefs[0] ||
		bundle.NodeRef != target.NodeRefs[0] {
		return SelectedPaaSWorkloadDeployment{}, errors.New("applied standalone workload bundle differs from the sealed target")
	}
	return SelectedPaaSWorkloadDeployment{
		WorkloadRef: bundle.WorkloadRef, ModuleRef: bundle.ModuleRef, UnitRef: target.UnitRef,
		Release: bundle.Release, SiteRef: bundle.SiteRef, NodeRef: bundle.NodeRef,
		InstanceRef: bundle.InstanceRef, ExecutionChannelRef: target.ExecutionChannelRef,
		ArtifactRef: artifactID, ArtifactDigest: artifactDigest, Bundle: artifactContent,
		Route: bundle.Route, RuntimeAdapter: *target.RuntimeAdapter, AdapterArtifacts: adapterArtifacts,
	}, nil
}

func requireStandaloneComposeRemovalAdmission(raw []byte) error {
	var adapter struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Adapter    struct {
			ID         string   `json:"id"`
			ModuleRef  string   `json:"moduleRef"`
			Operations []string `json:"operations"`
		} `json:"adapter"`
	}
	if err := json.Unmarshal(raw, &adapter); err != nil {
		return errors.New("applied standalone Compose adapter contract is unreadable")
	}
	if adapter.APIVersion != "stackkit.runtime-adapter/v1" || adapter.Kind != "WorkloadRuntimeAdapter" ||
		adapter.Adapter.ID != standaloneComposeAdapterRef || adapter.Adapter.ModuleRef != standaloneComposeModuleRef {
		return errors.New("applied runtime adapter is not the standalone Compose contract")
	}
	for _, operation := range adapter.Adapter.Operations {
		if operation == "remove" {
			return nil
		}
	}
	return errors.New("applied standalone Compose adapter contract does not admit remove")
}

func slicesContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func standaloneComposeVolumeDeclarations(
	bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor,
) (named []standaloneComposeOwnedVolume, hostPaths []standaloneComposeOwnedVolume) {
	for _, component := range bundle.Components {
		for _, volume := range component.Volumes {
			declared := standaloneComposeOwnedVolume{
				Key: component.ID + "-" + volume.ID, ComponentID: component.ID, VolumeID: volume.ID,
				Target: volume.Target, HostPath: volume.HostPath,
			}
			if volume.HostPath != "" {
				hostPaths = append(hostPaths, declared)
				continue
			}
			if strings.TrimSpace(volume.ID) == "" || strings.TrimSpace(volume.Target) == "" {
				continue
			}
			named = append(named, declared)
		}
	}
	return named, hostPaths
}

func standaloneComposeRemovalArgs(project standaloneComposeProject, extra ...string) []string {
	return append([]string{
		"compose", "--project-name", project.name, "--env-file", filepath.Join(project.directory, ".env"),
		"-f", filepath.Join(project.directory, "compose.yaml"),
	}, extra...)
}

func (r standaloneComposeRemover) readComposeStatuses(
	ctx context.Context,
	project standaloneComposeProject,
) (map[string]standaloneComposePS, error) {
	raw, err := r.ops.runner.Run(ctx, standaloneComposeRemovalArgs(project, "ps", "--all", "--no-trunc", "--format", "json"), project.directory)
	if err != nil {
		return nil, fmt.Errorf("standalone Docker Compose removal status observation failed: %w", err)
	}
	return parseStandaloneComposeStatusesAllowingEmpty(raw)
}

func parseStandaloneComposeStatusesAllowingEmpty(raw []byte) (map[string]standaloneComposePS, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]standaloneComposePS{}, nil
	}
	return parseStandaloneComposeStatuses(raw)
}

func (r standaloneComposeRemover) inspectPresentContainers(
	ctx context.Context,
	project standaloneComposeProject,
	statuses map[string]standaloneComposePS,
) ([]standaloneComposePresentContainer, error) {
	declared := map[string]architecturev2renderer.ApplicationDeliveryComponentDescriptor{}
	for _, component := range project.bundle.Components {
		declared[component.ID] = component
	}
	var unexpected []string
	for service := range statuses {
		if _, known := declared[service]; !known {
			unexpected = append(unexpected, service)
		}
	}
	if len(unexpected) != 0 {
		sort.Strings(unexpected)
		return nil, fmt.Errorf("standalone Compose project contains undeclared components %q; removal refuses orphan cleanup", unexpected)
	}
	var present []standaloneComposePresentContainer
	for _, component := range project.bundle.Components {
		status, exists := statuses[component.ID]
		if !exists {
			continue
		}
		if status.Image != component.ImageRef+"@"+component.ImageDigest {
			return nil, fmt.Errorf("standalone Compose component %q differs from its pinned image", component.ID)
		}
		id := strings.TrimPrefix(strings.TrimSpace(status.ID), "sha256:")
		if !validStandaloneComposeContainerID(id) {
			return nil, fmt.Errorf("standalone Compose component %q has no exact container identity", component.ID)
		}
		inspected, err := r.inspectContainer(ctx, id)
		if err != nil {
			return nil, err
		}
		if err := r.verifyWorkspaceContainer(project, component.ID, inspected); err != nil {
			return nil, err
		}
		present = append(present, standaloneComposePresentContainer{
			Service: component.ID, ID: id, Image: status.Image,
			WorkingDir:  inspected.Config.Labels[composeWorkingDirLabel],
			ConfigFiles: inspected.Config.Labels[composeConfigFilesLabel],
			Mounts:      inspected.Mounts,
		})
	}
	return present, nil
}

func (r standaloneComposeRemover) inspectContainer(ctx context.Context, id string) (dockerContainerInspect, error) {
	raw, err := r.dockerCLI().Run(ctx, []string{"inspect", id})
	if err != nil {
		return dockerContainerInspect{}, fmt.Errorf("inspect standalone Compose container identity: %w", err)
	}
	var values []dockerContainerInspect
	if err := json.Unmarshal(raw, &values); err != nil || len(values) != 1 {
		return dockerContainerInspect{}, errors.New("standalone Compose container inspection is invalid")
	}
	actual := values[0]
	actual.ID = strings.TrimPrefix(actual.ID, "sha256:")
	if actual.ID != id {
		return dockerContainerInspect{}, errors.New("standalone Compose container identity changed during inspection")
	}
	return actual, nil
}

func (r standaloneComposeRemover) verifyWorkspaceContainer(
	project standaloneComposeProject,
	service string,
	inspected dockerContainerInspect,
) error {
	labels := inspected.Config.Labels
	if labels[composeProjectLabel] != project.name || labels[composeServiceLabel] != service {
		return errors.New("standalone Compose container is not the exact applied project service")
	}
	workingDir := labels[composeWorkingDirLabel]
	if workingDir == "" || filepath.Clean(workingDir) != filepath.Clean(project.directory) {
		return errors.New("standalone Compose container is not bound to this owner workspace")
	}
	composeFile := filepath.Clean(filepath.Join(project.directory, "compose.yaml"))
	if !composeConfigFilesExact(labels[composeConfigFilesLabel], composeFile) {
		return errors.New("standalone Compose container is not bound to the exact persisted Compose definition")
	}
	return nil
}

func composeConfigFilesExact(raw, composeFile string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 1 {
		return false
	}
	return filepath.Clean(strings.TrimSpace(parts[0])) == filepath.Clean(composeFile)
}

func standaloneComposeRejectUnsafeDelete(disposition string, hostPaths []standaloneComposeOwnedVolume) error {
	if disposition != workloadremoval.DataDispositionDelete || len(hostPaths) == 0 {
		return nil
	}
	names := make([]string, 0, len(hostPaths))
	for _, volume := range hostPaths {
		names = append(names, volume.Key+"="+volume.HostPath)
	}
	sort.Strings(names)
	return fmt.Errorf("cannot delete bind-mounted host-path data %s; named-volume deletion does not remove host media. Remove the workload with data retained and manage host media separately", strings.Join(names, ", "))
}

func (r standaloneComposeRemover) admitOwnedVolumes(
	ctx context.Context,
	request workloadremoval.Request,
	project standaloneComposeProject,
	owned []standaloneComposeOwnedVolume,
	present []standaloneComposePresentContainer,
) (map[string]dockerVolumeInspect, error) {
	existing, err := r.loadVolumeCustody(request, project)
	if err != nil && !errors.Is(err, errVolumeCustodyAbsent) {
		return nil, err
	}
	if err == nil {
		for _, container := range present {
			if container.Service == project.bundle.EntryComponent && container.ID != existing.EntryContainerID {
				return nil, errors.New("entry container differs from the original removal custody")
			}
		}
		if err := volumeCustodyMatchesDeclared(existing, owned); err != nil {
			return nil, err
		}
		live, err := r.inspectCustodyIdentities(ctx, project, existing, request.DataDisposition == workloadremoval.DataDispositionDelete)
		if err != nil {
			return nil, err
		}
		if len(present) != 0 {
			mounted, mountErr := r.volumesFromContainerMounts(ctx, project, owned, present)
			if mountErr != nil {
				return nil, mountErr
			}
			if err := mountedMatchesCustody(mounted, existing); err != nil {
				return nil, err
			}
		}
		return live, nil
	}
	if len(present) == 0 {
		return nil, errVolumeCustodyAbsent
	}
	mounted, err := r.volumesFromContainerMounts(ctx, project, owned, present)
	if err != nil {
		return nil, err
	}
	if err := mountedMatchesDeclared(mounted, owned); err != nil {
		return nil, err
	}
	if err := r.persistVolumeCustody(request, project, owned, mounted, present); err != nil {
		return nil, err
	}
	return mounted, nil
}

func (r standaloneComposeRemover) volumesFromContainerMounts(
	ctx context.Context,
	project standaloneComposeProject,
	owned []standaloneComposeOwnedVolume,
	present []standaloneComposePresentContainer,
) (map[string]dockerVolumeInspect, error) {
	declared := map[string]standaloneComposeOwnedVolume{}
	for _, volume := range owned {
		declared[volume.ComponentID+"\x00"+volume.Target] = volume
	}
	found := map[string]dockerVolumeInspect{}
	for _, container := range present {
		for _, mount := range container.Mounts {
			if mount.Type != "volume" {
				continue
			}
			volume, ok := declared[container.Service+"\x00"+mount.Destination]
			if !ok {
				continue
			}
			if mount.Name == "" || mount.Driver != "" && mount.Driver != "local" {
				return nil, fmt.Errorf("standalone Compose volume %q is not a local named volume", volume.Key)
			}
			inspected, err := r.inspectVolume(ctx, mount.Name)
			if err != nil {
				return nil, fmt.Errorf("inspect mounted standalone Compose volume %q: %w", volume.Key, err)
			}
			if err := verifyAdmittedLocalVolume(project, volume.Key, inspected); err != nil {
				return nil, err
			}
			if existing, duplicate := found[volume.Key]; duplicate && existing.Name != inspected.Name {
				return nil, fmt.Errorf("standalone Compose volume %q is attached to more than one named volume", volume.Key)
			}
			found[volume.Key] = inspected
		}
	}
	return found, nil
}

func verifyAdmittedLocalVolume(project standaloneComposeProject, key string, inspected dockerVolumeInspect) error {
	if inspected.Name == "" || !validStandaloneComposeVolumeName(inspected.Name) {
		return fmt.Errorf("standalone Compose volume %q has an invalid daemon name", key)
	}
	if inspected.Driver != "local" || inspected.Scope != "local" {
		return fmt.Errorf("standalone Compose volume %q is not a local named volume", key)
	}
	if len(inspected.Options) != 0 {
		return fmt.Errorf("standalone Compose volume %q has unsupported driver options", key)
	}
	if strings.TrimSpace(inspected.Mountpoint) == "" {
		return fmt.Errorf("standalone Compose volume %q is missing its mountpoint observation", key)
	}
	if err := requireParseableVolumeCreatedAt(inspected.CreatedAt); err != nil {
		return fmt.Errorf("standalone Compose volume %q: %w", key, err)
	}
	if inspected.Labels[composeProjectLabel] != project.name || inspected.Labels[composeVolumeLabel] != key {
		return errors.New("standalone Compose volume is not the declared project volume")
	}
	return nil
}

func requireParseableVolumeCreatedAt(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("creation identity is absent")
	}
	if _, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, value); err == nil {
		return nil
	}
	return errors.New("creation identity is unparseable")
}

func volumeCustodyMatchesDeclared(custody standaloneComposeVolumeCustody, owned []standaloneComposeOwnedVolume) error {
	if len(custody.Volumes) != len(owned) {
		return errors.New("standalone Compose volume custody does not cover the exact declared volume set")
	}
	declared := map[string]standaloneComposeOwnedVolume{}
	for _, volume := range owned {
		declared[volume.Key] = volume
	}
	seen := map[string]struct{}{}
	for _, entry := range custody.Volumes {
		volume, ok := declared[entry.Key]
		if !ok || volume.Target != entry.Target {
			return errors.New("standalone Compose volume custody has an undeclared or retargeted volume")
		}
		if _, dup := seen[entry.Key]; dup {
			return errors.New("standalone Compose volume custody contains duplicate volume keys")
		}
		seen[entry.Key] = struct{}{}
	}
	return nil
}

func mountedMatchesDeclared(mounted map[string]dockerVolumeInspect, owned []standaloneComposeOwnedVolume) error {
	if len(mounted) != len(owned) {
		return errors.New("standalone Compose declared data volumes are not all mounted on the inspected containers")
	}
	for _, volume := range owned {
		if _, ok := mounted[volume.Key]; !ok {
			return errors.New("standalone Compose declared data volumes are not all mounted on the inspected containers")
		}
	}
	return nil
}

func mountedMatchesCustody(mounted map[string]dockerVolumeInspect, custody standaloneComposeVolumeCustody) error {
	byKey := map[string]standaloneComposeVolumeCustodyEntry{}
	for _, entry := range custody.Volumes {
		byKey[entry.Key] = entry
	}
	for key, inspected := range mounted {
		entry, ok := byKey[key]
		if !ok || !volumeObservationMatchesCustody(inspected, entry) {
			return errors.New("surviving container mounts differ from admitted volume custody")
		}
	}
	return nil
}

func volumeObservationMatchesCustody(inspected dockerVolumeInspect, entry standaloneComposeVolumeCustodyEntry) bool {
	return inspected.Name == entry.Name && inspected.Driver == entry.Driver &&
		inspected.CreatedAt == entry.CreatedAt && inspected.Mountpoint == entry.Mountpoint &&
		inspected.Scope == entry.Scope && maps.Equal(normalizedVolumeOptions(inspected.Options), normalizedVolumeOptions(entry.Options)) &&
		inspected.Labels[composeProjectLabel] != "" && inspected.Labels[composeVolumeLabel] == entry.Key
}

func normalizedVolumeOptions(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	return values
}

func custodyEntryFromInspect(key, target string, inspected dockerVolumeInspect) standaloneComposeVolumeCustodyEntry {
	return standaloneComposeVolumeCustodyEntry{
		Key: key, Name: inspected.Name, Driver: inspected.Driver, Target: target,
		CreatedAt: inspected.CreatedAt, Mountpoint: inspected.Mountpoint, Scope: inspected.Scope,
		Options: normalizedVolumeOptions(inspected.Options),
	}
}

func (r standaloneComposeRemover) persistVolumeCustody(
	request workloadremoval.Request,
	project standaloneComposeProject,
	owned []standaloneComposeOwnedVolume,
	volumes map[string]dockerVolumeInspect,
	present []standaloneComposePresentContainer,
) error {
	target := request.Applied.RuntimeTargets[0]
	custody := standaloneComposeVolumeCustody{
		APIVersion: volumeCustodyAPIVersion, AppliedRequestDigest: request.AppliedRequestDigest,
		WorkloadRef: request.WorkloadRef, InstanceRef: target.InstanceRef,
		Project: project.name, WorkingDir: project.directory,
	}
	for _, container := range present {
		if container.Service == project.bundle.EntryComponent {
			custody.EntryContainerID = container.ID
		}
	}
	if !validStandaloneComposeContainerID(custody.EntryContainerID) {
		return errors.New("initial removal custody requires the observed entry container")
	}
	for _, volume := range owned {
		custody.Volumes = append(custody.Volumes, custodyEntryFromInspect(volume.Key, volume.Target, volumes[volume.Key]))
	}
	sort.Slice(custody.Volumes, func(i, j int) bool { return custody.Volumes[i].Key < custody.Volumes[j].Key })
	canonical, err := resolvedplan.CanonicalJSON(custody)
	if err != nil {
		return fmt.Errorf("canonicalize volume custody: %w", err)
	}
	signature, err := localevidence.SignOwnerLifecycleMutation(r.ops.workspaceRoot, canonical)
	if err != nil {
		return fmt.Errorf("sign volume custody: %w", err)
	}
	record, err := json.Marshal(standaloneComposeVolumeCustodyRecord{Custody: custody, Signature: signature})
	if err != nil {
		return err
	}
	root, err := confinedfs.Open(r.ops.workspaceRoot)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Close() }()
	rel := volumeCustodyPath(request.AppliedRequestDigest, request.WorkloadRef)
	if err := tx.MkdirAll(filepath.ToSlash(filepath.Dir(rel)), 0700); err != nil {
		return err
	}
	view, err := root.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600NoReplace(rel, record)
	if err != nil {
		return err
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("standalone Compose volume custody write was not durable")
	}
	_, err = tx.SyncDirectory(filepath.ToSlash(filepath.Dir(rel)))
	return err
}

func (r standaloneComposeRemover) loadVolumeCustody(
	request workloadremoval.Request,
	project standaloneComposeProject,
) (standaloneComposeVolumeCustody, error) {
	root, err := confinedfs.Open(r.ops.workspaceRoot)
	if err != nil {
		return standaloneComposeVolumeCustody{}, err
	}
	defer func() { _ = root.Close() }()
	tx, err := root.BeginTransaction()
	if err != nil {
		return standaloneComposeVolumeCustody{}, err
	}
	defer func() { _ = tx.Close() }()
	rel := volumeCustodyPath(request.AppliedRequestDigest, request.WorkloadRef)
	raw, info, err := tx.ReadStableBounded(rel, 256<<10)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return standaloneComposeVolumeCustody{}, errVolumeCustodyAbsent
		}
		return standaloneComposeVolumeCustody{}, fmt.Errorf("%w: %v", errVolumeCustodyInvalid, err)
	}
	if info == nil || !info.Mode().IsRegular() {
		return standaloneComposeVolumeCustody{}, fmt.Errorf("%w: record is not a signed regular file", errVolumeCustodyInvalid)
	}
	var record standaloneComposeVolumeCustodyRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return standaloneComposeVolumeCustody{}, fmt.Errorf("%w: record is unreadable", errVolumeCustodyInvalid)
	}
	canonical, err := resolvedplan.CanonicalJSON(record.Custody)
	if err != nil {
		return standaloneComposeVolumeCustody{}, fmt.Errorf("%w: %v", errVolumeCustodyInvalid, err)
	}
	if err := localevidence.VerifyOwnerLifecycleMutation(r.ops.workspaceRoot, canonical, record.Signature); err != nil {
		return standaloneComposeVolumeCustody{}, fmt.Errorf("%w: %v", errVolumeCustodyInvalid, err)
	}
	target := request.Applied.RuntimeTargets[0]
	if record.Custody.APIVersion != volumeCustodyAPIVersion ||
		!validStandaloneComposeContainerID(record.Custody.EntryContainerID) ||
		record.Custody.AppliedRequestDigest != request.AppliedRequestDigest ||
		record.Custody.WorkloadRef != request.WorkloadRef ||
		record.Custody.InstanceRef != target.InstanceRef ||
		record.Custody.Project != project.name ||
		filepath.Clean(record.Custody.WorkingDir) != filepath.Clean(project.directory) {
		return standaloneComposeVolumeCustody{}, fmt.Errorf("%w: record does not bind this applied workload", errVolumeCustodyInvalid)
	}
	return record.Custody, nil
}

func volumeCustodyPath(appliedDigest, workloadRef string) string {
	id := strings.TrimPrefix(appliedDigest, "sha256:")
	return filepath.ToSlash(filepath.Join(volumeCustodyRelDir, id, workloadRef+".json"))
}

func (r standaloneComposeRemover) inspectCustodyIdentities(
	ctx context.Context,
	project standaloneComposeProject,
	custody standaloneComposeVolumeCustody,
	allowAbsent bool,
) (map[string]dockerVolumeInspect, error) {
	found := map[string]dockerVolumeInspect{}
	for _, entry := range custody.Volumes {
		inspected, err := r.inspectVolume(ctx, entry.Name)
		if err != nil {
			present, presentErr := r.volumeNamePresent(ctx, entry.Name)
			if presentErr != nil {
				return nil, fmt.Errorf("observe admitted standalone Compose volume %q: %w", entry.Key, presentErr)
			}
			if !present && allowAbsent {
				// Keep the original identity in the admitted set so a volume
				// reappearing later is checked before deletion and final proof.
				found[entry.Key] = dockerVolumeInspect{
					Name: entry.Name, Driver: entry.Driver, CreatedAt: entry.CreatedAt,
					Mountpoint: entry.Mountpoint, Scope: entry.Scope, Options: entry.Options,
					Labels: map[string]string{composeProjectLabel: project.name, composeVolumeLabel: entry.Key},
				}
				continue
			}
			if !present {
				return nil, fmt.Errorf("admitted standalone Compose volume %q is absent", entry.Key)
			}
			return nil, fmt.Errorf("inspect admitted standalone Compose volume %q: %w", entry.Key, err)
		}
		if err := verifyAdmittedLocalVolume(project, entry.Key, inspected); err != nil {
			return nil, err
		}
		if !volumeObservationMatchesCustody(inspected, entry) {
			return nil, errors.New("standalone Compose volume identity differs from admitted custody")
		}
		found[entry.Key] = inspected
	}
	return found, nil
}

func (r standaloneComposeRemover) observeAdmittedVolumes(
	ctx context.Context,
	project standaloneComposeProject,
	admitted map[string]dockerVolumeInspect,
	disposition string,
) (map[string]dockerVolumeInspect, error) {
	allowAbsent := disposition == workloadremoval.DataDispositionDelete
	found := map[string]dockerVolumeInspect{}
	keys := make([]string, 0, len(admitted))
	for key := range admitted {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		expected := admitted[key]
		inspected, err := r.inspectVolume(ctx, expected.Name)
		if err != nil {
			present, presentErr := r.volumeNamePresent(ctx, expected.Name)
			if presentErr != nil {
				return nil, fmt.Errorf("observe standalone Compose volume %q: %w", key, presentErr)
			}
			if present {
				return nil, fmt.Errorf("inspect standalone Compose volume %q: %w", key, err)
			}
			if !allowAbsent {
				return nil, fmt.Errorf("admitted standalone Compose volume %q is absent", key)
			}
			continue
		}
		if err := verifyAdmittedLocalVolume(project, key, inspected); err != nil {
			return nil, err
		}
		if inspected.Name != expected.Name || inspected.Driver != expected.Driver ||
			inspected.CreatedAt != expected.CreatedAt || inspected.Mountpoint != expected.Mountpoint ||
			inspected.Scope != expected.Scope || !maps.Equal(normalizedVolumeOptions(inspected.Options), normalizedVolumeOptions(expected.Options)) {
			return nil, errors.New("standalone Compose volume identity changed after admission")
		}
		found[key] = inspected
	}
	return found, nil
}

func (r standaloneComposeRemover) inspectVolume(ctx context.Context, name string) (dockerVolumeInspect, error) {
	raw, err := r.dockerCLI().Run(ctx, []string{"volume", "inspect", name})
	if err != nil {
		return dockerVolumeInspect{}, err
	}
	var values []dockerVolumeInspect
	if err := json.Unmarshal(raw, &values); err != nil || len(values) != 1 {
		return dockerVolumeInspect{}, errors.New("standalone Compose volume inspection is invalid")
	}
	if values[0].Name != name {
		return dockerVolumeInspect{}, errors.New("standalone Compose volume identity changed during inspection")
	}
	if values[0].Labels == nil {
		values[0].Labels = map[string]string{}
	}
	if values[0].Options == nil {
		values[0].Options = map[string]string{}
	}
	return values[0], nil
}

func (r standaloneComposeRemover) volumeNamePresent(ctx context.Context, name string) (bool, error) {
	filter, err := exactVolumeNameFilter(name)
	if err != nil {
		return false, err
	}
	raw, err := r.dockerCLI().Run(ctx, []string{"volume", "ls", "--filter", filter, "--format", "{{.Name}}"})
	if err != nil {
		return false, err
	}
	found := false
	for _, actual := range strings.Fields(string(raw)) {
		if !validStandaloneComposeVolumeName(actual) {
			return false, errors.New("volume listing returned an invalid name")
		}
		if actual == name {
			found = true
			continue
		}
		return false, errors.New("volume listing escaped the exact name filter")
	}
	return found, nil
}

func exactVolumeNameFilter(name string) (string, error) {
	if !validStandaloneComposeVolumeName(name) {
		return "", errors.New("standalone Compose volume name is outside the closed contract")
	}
	return "name=^" + regexp.QuoteMeta(name) + "$", nil
}

func (r standaloneComposeRemover) stopAndRemoveContainers(
	ctx context.Context,
	present []standaloneComposePresentContainer,
) error {
	for _, container := range present {
		if _, err := r.dockerCLI().Run(ctx, []string{"stop", container.ID}); err != nil {
			absent, absentErr := r.containerIDAbsent(ctx, container.ID)
			if absentErr != nil {
				return fmt.Errorf("stop standalone Compose container: %w", errors.Join(err, absentErr))
			}
			if !absent {
				return fmt.Errorf("stop standalone Compose container: %w", err)
			}
			continue
		}
		if _, err := r.dockerCLI().Run(ctx, []string{"rm", container.ID}); err != nil {
			absent, absentErr := r.containerIDAbsent(ctx, container.ID)
			if absentErr != nil {
				return fmt.Errorf("remove standalone Compose container: %w", errors.Join(err, absentErr))
			}
			if !absent {
				return fmt.Errorf("remove standalone Compose container: %w", err)
			}
		}
	}
	return nil
}

func (r standaloneComposeRemover) verifyAdmittedContainersAbsent(
	ctx context.Context,
	project standaloneComposeProject,
	present []standaloneComposePresentContainer,
) error {
	for _, container := range present {
		absent, err := r.containerIDAbsent(ctx, container.ID)
		if err != nil {
			return err
		}
		if !absent {
			return errors.New("admitted standalone Compose container is still present after removal")
		}
	}
	statuses, err := r.readComposeStatuses(ctx, project)
	if err != nil {
		return err
	}
	if len(statuses) != 0 {
		return errors.New("standalone Compose project has replacement containers after admitted identity removal")
	}
	return nil
}

func (r standaloneComposeRemover) containerIDAbsent(ctx context.Context, id string) (bool, error) {
	raw, err := r.dockerCLI().Run(ctx, []string{"ps", "-aq", "--no-trunc", "--filter", "id=" + id})
	if err != nil {
		return false, fmt.Errorf("observe admitted container absence: %w", err)
	}
	for _, actual := range strings.Fields(string(raw)) {
		if actual != id {
			return false, errors.New("container absence readback escaped the exact identity filter")
		}
		return false, nil
	}
	return true, nil
}

func (r standaloneComposeRemover) deleteOwnedVolumes(
	ctx context.Context,
	project standaloneComposeProject,
	custody map[string]dockerVolumeInspect,
) error {
	keys := make([]string, 0, len(custody))
	for key := range custody {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		expected := custody[key]
		fresh, err := r.inspectVolume(ctx, expected.Name)
		if err != nil {
			present, presentErr := r.volumeNamePresent(ctx, expected.Name)
			if presentErr != nil {
				return fmt.Errorf("refresh owned standalone Compose volume %q before deletion: %w", key, presentErr)
			}
			if !present {
				continue
			}
			return fmt.Errorf("refresh owned standalone Compose volume %q before deletion: %w", key, err)
		}
		if err := verifyAdmittedLocalVolume(project, key, fresh); err != nil {
			return err
		}
		if fresh.Name != expected.Name || fresh.Driver != expected.Driver ||
			fresh.CreatedAt != expected.CreatedAt || fresh.Mountpoint != expected.Mountpoint ||
			fresh.Scope != expected.Scope || !maps.Equal(normalizedVolumeOptions(fresh.Options), normalizedVolumeOptions(expected.Options)) {
			return errors.New("standalone Compose volume identity differs from admitted custody")
		}
		if _, err := r.dockerCLI().Run(ctx, []string{"volume", "rm", fresh.Name}); err != nil {
			present, presentErr := r.volumeNamePresent(ctx, fresh.Name)
			if presentErr != nil {
				return fmt.Errorf("delete owned standalone Compose volume %q: %w", key, presentErr)
			}
			if !present {
				continue
			}
			return fmt.Errorf("delete owned standalone Compose volume %q: %w", key, err)
		}
	}
	return nil
}

func (r standaloneComposeRemover) withdrawOrigin(request workloadremoval.Request, project standaloneComposeProject) error {
	if project.bundle.Route.ServiceRef == "" {
		return nil
	}
	custody, err := r.loadVolumeCustody(request, project)
	if err != nil {
		return err
	}
	return localorigin.WithdrawBackend(r.ops.workspaceRoot, localorigin.Backend{
		ModuleRef: project.bundle.ModuleRef, UnitRef: project.unitRef, InstanceRef: project.bundle.InstanceRef,
		NodeRef: project.bundle.NodeRef, ServiceRef: project.bundle.Route.ServiceRef, ContainerID: custody.EntryContainerID,
	})
}

func observeStandaloneComposeRemoval(
	project standaloneComposeProject,
	disposition string,
	statuses map[string]standaloneComposePS,
	owned []standaloneComposeOwnedVolume,
	hostPaths []standaloneComposeOwnedVolume,
	volumes map[string]dockerVolumeInspect,
) (standaloneComposeRemovalObservation, error) {
	observation := standaloneComposeRemovalObservation{
		Project: project.name, WorkingDir: project.directory, DataDisposition: disposition,
	}
	for _, component := range project.bundle.Components {
		entry := standaloneComposeRemovalContainerEvidence{Service: component.ID, State: "absent"}
		if status, exists := statuses[component.ID]; exists {
			entry.ContainerID = strings.TrimPrefix(strings.TrimSpace(status.ID), "sha256:")
			entry.State = strings.TrimSpace(status.State)
			if entry.State == "" {
				entry.State = "present"
			}
		}
		observation.Containers = append(observation.Containers, entry)
	}
	sort.Slice(observation.Containers, func(i, j int) bool {
		return observation.Containers[i].Service < observation.Containers[j].Service
	})
	for _, volume := range owned {
		entry := standaloneComposeRemovalVolumeEvidence{Key: volume.Key, State: "absent"}
		if inspected, exists := volumes[volume.Key]; exists {
			entry.Name = inspected.Name
			entry.Driver = inspected.Driver
			entry.State = "present"
		}
		observation.Volumes = append(observation.Volumes, entry)
	}
	for _, volume := range hostPaths {
		observation.Volumes = append(observation.Volumes, standaloneComposeRemovalVolumeEvidence{
			Key: volume.Key, State: "host-path-retained",
		})
	}
	sort.Slice(observation.Volumes, func(i, j int) bool {
		return observation.Volumes[i].Key < observation.Volumes[j].Key
	})
	if len(statuses) != 0 {
		return standaloneComposeRemovalObservation{}, errors.New("standalone Compose removal observation still contains workload containers")
	}
	switch disposition {
	case workloadremoval.DataDispositionRetain:
		for _, volume := range owned {
			if _, exists := volumes[volume.Key]; !exists {
				return standaloneComposeRemovalObservation{}, errors.New("standalone Compose retain-data removal could not observe the declared data volumes")
			}
		}
	case workloadremoval.DataDispositionDelete:
		for _, volume := range owned {
			if _, exists := volumes[volume.Key]; exists {
				return standaloneComposeRemovalObservation{}, errors.New("standalone Compose delete-data removal did not prove owned volumes are absent")
			}
		}
	default:
		return standaloneComposeRemovalObservation{}, errors.New("standalone Compose removal observation has no data disposition")
	}
	return observation, nil
}

type osStandaloneComposeDockerCLI struct{}

func (osStandaloneComposeDockerCLI) Run(ctx context.Context, args []string) ([]byte, error) {
	if err := validateStandaloneComposeDockerArgs(args); err != nil {
		return nil, err
	}
	executable, err := exec.LookPath("docker")
	if err != nil {
		return nil, &standaloneComposeProcessError{output: "docker-not-found", cause: err}
	}
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = []string{"LANG=C", "LC_ALL=C"}
	if runtime.GOOS == "windows" {
		if directory := os.Getenv("ProgramFiles"); directory != "" {
			command.Env = append(command.Env, "ProgramFiles="+directory)
		}
	}
	output := &standaloneComposeBoundedBuffer{remaining: standaloneComposeOutputMax}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		return nil, &standaloneComposeProcessError{
			output: boundedBasementCoreProcessDiagnostic(output.Bytes()), cause: err,
		}
	}
	if output.exceeded {
		return nil, &standaloneComposeProcessError{
			output: "output-exceeded", cause: errors.New("bounded process output exceeded"),
		}
	}
	return append([]byte(nil), output.Bytes()...), nil
}

func validateStandaloneComposeDockerArgs(args []string) error {
	if len(args) == 0 {
		return errors.New("standalone Compose docker contract is empty")
	}
	switch args[0] {
	case "inspect", "stop", "rm":
		if len(args) != 2 || !validStandaloneComposeContainerID(args[1]) {
			return errors.New("standalone Compose container identity is outside the closed contract")
		}
		return nil
	case "ps":
		if len(args) != 5 || args[1] != "-aq" || args[2] != "--no-trunc" || args[3] != "--filter" ||
			!strings.HasPrefix(args[4], "id=") || !validStandaloneComposeContainerID(strings.TrimPrefix(args[4], "id=")) {
			return errors.New("standalone Compose container listing is outside the closed contract")
		}
		return nil
	case "volume":
		if len(args) >= 2 && args[1] == "ls" {
			if len(args) != 6 || args[2] != "--filter" || args[4] != "--format" || args[5] != "{{.Name}}" {
				return errors.New("standalone Compose volume listing is outside the closed contract")
			}
			if _, err := parseExactVolumeNameFilter(args[3]); err != nil {
				return err
			}
			return nil
		}
		if len(args) != 3 || (args[1] != "inspect" && args[1] != "rm") || !validStandaloneComposeVolumeName(args[2]) {
			return errors.New("standalone Compose volume identity is outside the closed contract")
		}
		return nil
	}
	return errors.New("standalone Compose docker contract rejected the requested operation")
}

func validStandaloneComposeContainerID(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validStandaloneComposeVolumeName(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, " \t\n\r/") {
		return false
	}
	if value[0] == '-' || strings.HasPrefix(value, "--") {
		return false
	}
	if !isDockerNameAlphanumeric(value[0]) {
		return false
	}
	for i := 1; i < len(value); i++ {
		c := value[i]
		if isDockerNameAlphanumeric(c) || c == '_' || c == '.' || c == '-' {
			continue
		}
		return false
	}
	return true
}

func isDockerNameAlphanumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func parseExactVolumeNameFilter(filter string) (string, error) {
	if !strings.HasPrefix(filter, "name=^") || !strings.HasSuffix(filter, "$") {
		return "", errors.New("standalone Compose volume filter is outside the closed contract")
	}
	quoted := strings.TrimSuffix(strings.TrimPrefix(filter, "name=^"), "$")
	name := strings.ReplaceAll(quoted, `\.`, ".")
	if regexp.QuoteMeta(name) != quoted || !validStandaloneComposeVolumeName(name) {
		return "", errors.New("standalone Compose volume filter is outside the closed contract")
	}
	return name, nil
}
