package nativehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localorigin"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/workloadremoval"
	"gopkg.in/yaml.v3"
)

// paasRemovalJournal binds the exact app, containers and volumes before the
// first effect. A resumed removal acts only on these identities; matching
// names on replacement resources never grant deletion authority.
type paasRemovalJournal struct {
	Version              string `json:"version"`
	AppliedRequestDigest string `json:"appliedRequestDigest"`
	ArtifactDigest       string `json:"artifactDigest"`
	Project              string `json:"project"`
	// RuntimeProject is the Compose project the manager runs the app as: the
	// Coolify service UUID, Runtipi's <project>__user. The JSON name is the
	// one existing Coolify journals carry.
	RuntimeProject   string                         `json:"serviceUuid"`
	ServerID         string                         `json:"serverId"`
	DataDisposition  string                         `json:"dataDisposition"`
	EntryContainerID string                         `json:"entryContainerId,omitempty"`
	Containers       map[string]string              `json:"containers"`
	Volumes          map[string]dockerVolumeInspect `json:"volumes"`
	Deployment       platformdeploy.HostDeployment  `json:"deployment"`
	Complete         bool                           `json:"complete"`
}

type paasRemovalJournalRecord struct {
	Journal   paasRemovalJournal                      `json:"journal"`
	Signature localevidence.OwnerPolicyStateSignature `json:"signature"`
}

// paasRemovalManager is one authenticated session with the node's manager.
type paasRemovalManager interface {
	ServerID() string
	AppAbsent(context.Context, platformdeploy.DeploymentRef) (bool, error)
	// RemoveApp deletes the manager's app record and containers but never the
	// node-owned volumes; StackKits applies the data disposition itself.
	RemoveApp(context.Context, platformdeploy.DeploymentRef, string) error
}

// paasRemovalOwner is the manager-specific half of one workload removal.
type paasRemovalOwner struct {
	label, adapterRef, moduleRef string
	journalFile, journalVersion  string
	prepared                     *osStandaloneComposeWorkloadOperations
	// custody returns the signed node deployment of the workload, if present.
	custody func(SelectedPaaSWorkloadDeployment) (platformdeploy.HostDeployment, bool, error)
	// refuse rejects a deployment this owner must not remove; it may be nil.
	refuse         func(platformdeploy.HostDeployment) error
	runtimeProject func(platformdeploy.HostDeployment) string
	// guard re-checks, before every manager removal call, that the call can
	// delete nothing outside the journal; it may be nil.
	guard   func(context.Context, SelectedPaaSWorkloadDeployment, paasRemovalManager, paasRemovalJournal) error
	connect func(context.Context, SelectedPaaSWorkloadDeployment) (paasRemovalManager, error)
}

// removePaaSWorkload implements the canonical Owner-signed removal of one
// manager-run application. The manager deletes the app and its stored
// configuration but never its data; StackKits created the named volumes and
// alone applies the requested data disposition. The Marketplace and other
// apps stay untouched.
func removePaaSWorkload(ctx context.Context, workspace string, request workloadremoval.Request, owner paasRemovalOwner) (result workloadremoval.Result, err error) {
	progressed := false
	defer func() {
		if err != nil {
			var progress *RemovalProgressError
			if !errors.As(err, &progress) {
				err = &RemovalProgressError{Err: err, Progressed: progressed}
			}
		}
	}()
	label := owner.label
	if ctx == nil {
		return result, fmt.Errorf("%s workload removal requires a context", label)
	}
	validUntil, err := time.Parse(time.RFC3339Nano, request.ValidUntil)
	if err != nil {
		return result, fmt.Errorf("workload-removal validity is unreadable: %w", err)
	}
	ctx, cancel := context.WithDeadline(ctx, validUntil)
	defer cancel()
	fresh := func() error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("workload-removal authorization expired before mutation: %w", err)
		}
		return request.ValidateAt(time.Now().UTC())
	}
	if err := fresh(); err != nil {
		return result, err
	}
	payload, err := request.AuthorizationBytes()
	if err != nil {
		return result, err
	}
	if err := localevidence.VerifyOwnerLifecycleMutation(workspace, payload, localevidence.OwnerLifecycleMutationSignature{
		OwnerRef: request.Authorization.OwnerRef, KeyID: request.Authorization.KeyID, Value: request.Authorization.Value,
	}); err != nil {
		return result, fmt.Errorf("verify workload-removal Owner authorization: %w", err)
	}
	target := request.Applied.RuntimeTargets[0]
	deployment, err := selectedPaaSDeploymentFromApplied(target, request.Applied.Artifacts, owner.adapterRef, owner.moduleRef)
	if err != nil {
		return result, err
	}
	if err := requireAppliedAdapterRemoval(deployment, owner.adapterRef, owner.moduleRef); err != nil {
		return result, err
	}
	remover := standaloneComposeRemover{ops: owner.prepared}
	project, err := owner.prepared.prepareWithIdentityMutation(ctx, deployment, false)
	if err != nil {
		return result, err
	}
	if err := owner.prepared.verifyPersisted(project); err != nil {
		return result, err
	}
	owned, hostPaths := standaloneComposeVolumeDeclarations(project.bundle)
	if err := standaloneComposeRejectUnsafeDelete(request.DataDisposition, hostPaths); err != nil {
		return result, err
	}
	state, present, err := owner.custody(deployment)
	if err != nil {
		return result, err
	}
	if present && owner.refuse != nil {
		if err := owner.refuse(state); err != nil {
			return result, err
		}
	}
	journal, journaled, err := readPaaSRemovalJournal(workspace, project.directory, owner)
	if err != nil {
		return result, err
	}
	// A completed journal from an earlier lifetime of this workload is stale
	// once a new signed deployment exists for it. The same record means this
	// removal stopped before it retired the record, so it resumes.
	if journaled && journal.Complete && present && !sameHostDeployment(state, journal.Deployment) {
		journaled = false
	}
	if journaled {
		if journal.AppliedRequestDigest != request.AppliedRequestDigest || journal.ArtifactDigest != deployment.ArtifactDigest || journal.Project != project.name {
			return result, fmt.Errorf("%s interrupted removal requires its original applied workload", label)
		}
		if !journal.Complete && journal.DataDisposition != request.DataDisposition {
			return result, fmt.Errorf("%s interrupted removal requires its original data disposition", label)
		}
		if journal.Complete && journal.DataDisposition == workloadremoval.DataDispositionDelete && request.DataDisposition != workloadremoval.DataDispositionDelete {
			return result, fmt.Errorf("%s workload data was explicitly deleted; retaining it requires a new installation", label)
		}
		if present && !sameHostDeployment(state, journal.Deployment) {
			return result, fmt.Errorf("%s deployment custody changed after removal began", label)
		}
	} else {
		if !present || state.ArtifactDigest != deployment.ArtifactDigest {
			return result, fmt.Errorf("%s workload removal requires the exact signed deployment custody", label)
		}
		journal, err = admitPaaSRemoval(ctx, owner, remover, deployment, project, owned, state, request)
		if err != nil {
			return result, err
		}
	}
	journal.DataDisposition = request.DataDisposition
	journal.Complete = false
	if err := writePaaSRemovalJournal(workspace, project.directory, owner, journal); err != nil {
		return result, err
	}
	if err := fresh(); err != nil {
		return result, err
	}
	if present {
		manager, err := owner.connect(ctx, deployment)
		if err != nil {
			return result, err
		}
		if manager.ServerID() != journal.ServerID {
			return result, fmt.Errorf("%s removal server differs from node custody", label)
		}
		absent, err := manager.AppAbsent(ctx, journal.Deployment.Deployment)
		if err != nil {
			return result, err
		}
		if !absent {
			if owner.guard != nil {
				if err := owner.guard(ctx, deployment, manager, journal); err != nil {
					return result, err
				}
			}
			progressed = true
			if err := manager.RemoveApp(ctx, journal.Deployment.Deployment, request.DataDisposition); err != nil {
				return result, err
			}
		}
		if err := waitPaaSAppAbsent(ctx, label, manager, journal.Deployment.Deployment); err != nil {
			return result, err
		}
	}
	project.runtimeProject = journal.RuntimeProject
	if err := waitPaaSContainersAbsent(ctx, label, remover, project, journal); err != nil {
		return result, err
	}
	if project.bundle.Route.ServiceRef != "" && journal.EntryContainerID != "" {
		progressed = true
		if err := localorigin.WithdrawBackend(workspace, localorigin.Backend{
			ModuleRef: project.bundle.ModuleRef, UnitRef: project.unitRef, InstanceRef: project.bundle.InstanceRef,
			NodeRef: project.bundle.NodeRef, ServiceRef: project.bundle.Route.ServiceRef, ContainerID: journal.EntryContainerID,
		}); err != nil {
			return result, err
		}
	}
	if request.DataDisposition == workloadremoval.DataDispositionDelete {
		if err := fresh(); err != nil {
			return result, err
		}
		progressed = true
		if err := deletePaaSRemovalVolumes(ctx, label, remover, journal); err != nil {
			return result, err
		}
	}
	final := map[string]dockerVolumeInspect{}
	for _, volume := range owned {
		name := journal.Volumes[volume.Key].Name
		if name == "" {
			continue
		}
		found, err := remover.volumeNamePresent(ctx, name)
		if err != nil {
			return result, err
		}
		if found {
			inspected, err := remover.inspectVolume(ctx, name)
			if err != nil {
				return result, err
			}
			final[volume.Key] = inspected
		}
	}
	statuses, err := remover.readPaaSStatuses(ctx, label, project)
	if err != nil {
		return result, err
	}
	observation, err := observeStandaloneComposeRemoval(project, request.DataDisposition, statuses, owned, hostPaths, final)
	if err != nil {
		return result, err
	}
	journal.Complete = true
	if err := writePaaSRemovalJournal(workspace, project.directory, owner, journal); err != nil {
		return result, err
	}
	if present {
		progressed = true
		if err := platformdeploy.RemoveOwnerHostDeployment(workspace, project.name, journal.Deployment); err != nil {
			return result, err
		}
	}
	canonical, err := resolvedplan.CanonicalJSON(observation)
	if err != nil {
		return result, fmt.Errorf("canonicalize %s removal observation: %w", label, err)
	}
	sum := sha256.Sum256(canonical)
	if err := fresh(); err != nil {
		return result, err
	}
	return workloadremoval.NewResult(request, time.Now().UTC(), workloadremoval.Outcome{
		RequirementID:     target.RequirementID,
		WorkloadRef:       request.WorkloadRef,
		InstanceRef:       target.InstanceRef,
		RuntimeOwnerRef:   owner.adapterRef,
		ArtifactDigest:    deployment.ArtifactDigest,
		DataDisposition:   request.DataDisposition,
		Status:            workloadremoval.StatusRemoved,
		ObservedState:     workloadremoval.ObservedStateAbsent,
		ObservationRef:    "removal-observation://" + owner.adapterRef + "/" + request.WorkloadRef + "/" + target.InstanceRef,
		ObservationDigest: "sha256:" + hex.EncodeToString(sum[:]),
	})
}

// admitPaaSRemoval observes the signed app read-only: only declared
// components on their pinned images, and only the volumes Apply created.
func admitPaaSRemoval(ctx context.Context, owner paasRemovalOwner, remover standaloneComposeRemover, deployment SelectedPaaSWorkloadDeployment, project standaloneComposeProject, owned []standaloneComposeOwnedVolume, state platformdeploy.HostDeployment, request workloadremoval.Request) (paasRemovalJournal, error) {
	label := owner.label
	manager, err := owner.connect(ctx, deployment)
	if err != nil {
		return paasRemovalJournal{}, err
	}
	runtimeProject := owner.runtimeProject(state)
	if manager.ServerID() != state.ServerID || !validStandaloneComposeVolumeName(runtimeProject) {
		return paasRemovalJournal{}, fmt.Errorf("%s removal app differs from node custody", label)
	}
	project.runtimeProject = runtimeProject
	statuses, err := remover.readPaaSStatuses(ctx, label, project)
	if err != nil {
		return paasRemovalJournal{}, err
	}
	containers := map[string]string{}
	declared := map[string]string{}
	for _, component := range project.bundle.Components {
		declared[component.ID] = component.ImageRef + "@" + component.ImageDigest
	}
	for service, status := range statuses {
		image, known := declared[service]
		if !known {
			return paasRemovalJournal{}, fmt.Errorf("%s app contains undeclared component %q; removal refuses orphan cleanup", label, service)
		}
		if status.Image != image {
			return paasRemovalJournal{}, fmt.Errorf("%s component %q differs from its pinned image", label, service)
		}
		id := strings.TrimPrefix(strings.TrimSpace(status.ID), "sha256:")
		if !validStandaloneComposeContainerID(id) {
			return paasRemovalJournal{}, fmt.Errorf("%s component %q has no exact container identity", label, service)
		}
		containers[service] = id
	}
	names, err := paasProjectVolumeNames(label, project)
	if err != nil {
		return paasRemovalJournal{}, err
	}
	volumes := map[string]dockerVolumeInspect{}
	for _, volume := range owned {
		name := names[volume.Key]
		if name == "" {
			return paasRemovalJournal{}, fmt.Errorf("%s volume %q has no node-owned name", label, volume.Key)
		}
		found, err := remover.volumeNamePresent(ctx, name)
		if err != nil {
			return paasRemovalJournal{}, err
		}
		if !found {
			return paasRemovalJournal{}, fmt.Errorf("%s volume %q is absent before removal", label, name)
		}
		inspected, err := remover.inspectVolume(ctx, name)
		if err != nil {
			return paasRemovalJournal{}, err
		}
		if inspected.Driver != "local" {
			return paasRemovalJournal{}, fmt.Errorf("%s volume %q is not a local node volume", label, name)
		}
		volumes[volume.Key] = inspected
	}
	return paasRemovalJournal{
		Version: owner.journalVersion, AppliedRequestDigest: request.AppliedRequestDigest,
		ArtifactDigest: deployment.ArtifactDigest, Project: project.name, RuntimeProject: runtimeProject,
		ServerID: state.ServerID, DataDisposition: request.DataDisposition,
		EntryContainerID: containers[project.bundle.EntryComponent], Containers: containers, Volumes: volumes,
		Deployment: state,
	}, nil
}

// paasProjectVolumeNames applies the node naming platformNodeCompose sends to
// the manager: an explicit name, else <project>_<key>.
func paasProjectVolumeNames(label string, project standaloneComposeProject) (map[string]string, error) {
	var document struct {
		Volumes map[string]struct {
			Name string `yaml:"name"`
		} `yaml:"volumes"`
	}
	if err := yaml.Unmarshal(project.compose, &document); err != nil {
		return nil, fmt.Errorf("%s node recipe is unreadable", label)
	}
	names := map[string]string{}
	for key, volume := range document.Volumes {
		name := volume.Name
		if name == "" {
			name = project.name + "_" + key
		}
		if !strings.HasPrefix(name, project.name+"_") || !validStandaloneComposeVolumeName(name) {
			return nil, fmt.Errorf("%s volume differs from the exact node workload", label)
		}
		names[key] = name
	}
	return names, nil
}

func (r standaloneComposeRemover) readPaaSStatuses(ctx context.Context, label string, project standaloneComposeProject) (map[string]standaloneComposePS, error) {
	raw, err := r.ops.runner.Run(ctx, standaloneComposeArgs(project, "ps"), project.directory)
	if err != nil {
		return nil, fmt.Errorf("%s app status observation failed: %w", label, err)
	}
	return parseStandaloneComposeStatusesAllowingEmpty(raw)
}

func waitPaaSAppAbsent(ctx context.Context, label string, manager paasRemovalManager, ref platformdeploy.DeploymentRef) error {
	for {
		absent, err := manager.AppAbsent(ctx, ref)
		if err != nil {
			return err
		}
		if absent {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s did not delete the app in time: %w", label, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}

// waitPaaSContainersAbsent waits for the manager's asynchronous cleanup and
// then requires that no container of the app's project remains at all.
func waitPaaSContainersAbsent(ctx context.Context, label string, remover standaloneComposeRemover, project standaloneComposeProject, journal paasRemovalJournal) error {
	for {
		raw, err := remover.dockerCLI().Run(ctx, []string{"ps", "-aq", "--no-trunc", "--filter", "label=com.docker.compose.project=" + journal.RuntimeProject})
		if err != nil {
			return err
		}
		if len(strings.Fields(string(raw))) == 0 {
			for _, id := range journal.Containers {
				absent, err := remover.containerIDAbsent(ctx, id)
				if err != nil {
					return err
				}
				if !absent {
					return fmt.Errorf("admitted %s container is still present after app deletion", label)
				}
			}
			statuses, err := remover.readPaaSStatuses(ctx, label, project)
			if err != nil {
				return err
			}
			if len(statuses) != 0 {
				return fmt.Errorf("%s app project still has containers after deletion", label)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s did not remove the app containers in time: %w", label, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}

func deletePaaSRemovalVolumes(ctx context.Context, label string, remover standaloneComposeRemover, journal paasRemovalJournal) error {
	keys := make([]string, 0, len(journal.Volumes))
	for key := range journal.Volumes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		expected := journal.Volumes[key]
		found, err := remover.volumeNamePresent(ctx, expected.Name)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		current, err := remover.inspectVolume(ctx, expected.Name)
		if err != nil {
			return err
		}
		if current.Driver != expected.Driver || current.CreatedAt != expected.CreatedAt || current.Mountpoint != expected.Mountpoint ||
			current.Scope != expected.Scope || !maps.Equal(normalizedVolumeOptions(current.Options), normalizedVolumeOptions(expected.Options)) {
			return fmt.Errorf("%s volume %q differs from admitted custody", label, expected.Name)
		}
		if _, err := remover.dockerCLI().Run(ctx, []string{"volume", "rm", expected.Name}); err != nil {
			if found, presentErr := remover.volumeNamePresent(ctx, expected.Name); presentErr != nil || found {
				return fmt.Errorf("delete %s volume %q: %w", label, expected.Name, errors.Join(err, presentErr))
			}
		}
	}
	return nil
}

func requireAppliedAdapterRemoval(deployment SelectedPaaSWorkloadDeployment, adapterRef, moduleRef string) error {
	for _, artifact := range deployment.AdapterArtifacts {
		if !slicesContainsString(deployment.RuntimeAdapter.ArtifactRefs, artifact.ID) {
			continue
		}
		var adapter struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
			Adapter    struct {
				ID         string   `json:"id"`
				ModuleRef  string   `json:"moduleRef"`
				Operations []string `json:"operations"`
			} `json:"adapter"`
		}
		if err := json.Unmarshal(artifact.Content, &adapter); err != nil {
			return errors.New("applied runtime adapter contract is unreadable")
		}
		if adapter.APIVersion != "stackkit.runtime-adapter/v1" || adapter.Kind != "WorkloadRuntimeAdapter" ||
			adapter.Adapter.ID != adapterRef || adapter.Adapter.ModuleRef != moduleRef {
			return fmt.Errorf("applied runtime adapter is not the %s contract", adapterRef)
		}
		if slicesContainsString(adapter.Adapter.Operations, "remove") {
			return nil
		}
	}
	return fmt.Errorf("applied %s adapter contract does not admit remove; run `stackkit generate` and `stackkit apply` with this release first", adapterRef)
}

func sameHostDeployment(left, right platformdeploy.HostDeployment) bool {
	a, errA := json.Marshal(left)
	b, errB := json.Marshal(right)
	return errA == nil && errB == nil && string(a) == string(b)
}

func readPaaSRemovalJournal(workspace, directory string, owner paasRemovalOwner) (paasRemovalJournal, bool, error) {
	root, err := confinedfs.Open(directory)
	if errors.Is(err, os.ErrNotExist) {
		return paasRemovalJournal{}, false, nil
	}
	if err != nil {
		return paasRemovalJournal{}, false, err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return paasRemovalJournal{}, false, err
	}
	file, err := view.Open(owner.journalFile)
	if errors.Is(err, os.ErrNotExist) {
		return paasRemovalJournal{}, false, nil
	}
	if err != nil {
		return paasRemovalJournal{}, false, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return paasRemovalJournal{}, false, fmt.Errorf("%s removal journal cannot be read", owner.label)
	}
	var record paasRemovalJournalRecord
	if err := json.Unmarshal(raw, &record); err != nil || record.Journal.Version != owner.journalVersion {
		return paasRemovalJournal{}, false, fmt.Errorf("%s removal journal is invalid", owner.label)
	}
	canonical, err := resolvedplan.CanonicalJSON(record.Journal)
	if err != nil {
		return paasRemovalJournal{}, false, err
	}
	if err := localevidence.VerifyOwnerPolicyState(workspace, canonical, record.Signature); err != nil {
		return paasRemovalJournal{}, false, fmt.Errorf("%s removal journal signature: %w", owner.label, err)
	}
	return record.Journal, true, nil
}

func writePaaSRemovalJournal(workspace, directory string, owner paasRemovalOwner, journal paasRemovalJournal) error {
	canonical, err := resolvedplan.CanonicalJSON(journal)
	if err != nil {
		return err
	}
	signature, err := localevidence.SignOwnerPolicyState(workspace, canonical)
	if err != nil {
		return err
	}
	data, err := json.Marshal(paasRemovalJournalRecord{Journal: journal, Signature: signature})
	if err != nil {
		return err
	}
	root, err := confinedfs.Open(directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(owner.journalFile, append(data, '\n'))
	if err == nil && (!result.Installed || !result.FileSynced) {
		return fmt.Errorf("%s removal journal was not durably installed", owner.label)
	}
	return err
}
