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

const (
	coolifyRemovalJournalFile    = "coolify-removal.json"
	coolifyRemovalJournalVersion = "stackkit.coolify-workload-removal/v1"
	coolifyAdapterRef            = "coolify"
	coolifyAdapterModuleRef      = "stackkits-coolify-runtime"
)

// coolifyRemovalJournal binds the exact service, containers and volumes before
// the first effect. A resumed removal acts only on these identities; matching
// names on replacement resources never grant deletion authority.
type coolifyRemovalJournal struct {
	Version              string                         `json:"version"`
	AppliedRequestDigest string                         `json:"appliedRequestDigest"`
	ArtifactDigest       string                         `json:"artifactDigest"`
	Project              string                         `json:"project"`
	ServiceUUID          string                         `json:"serviceUuid"`
	ServerID             string                         `json:"serverId"`
	DataDisposition      string                         `json:"dataDisposition"`
	EntryContainerID     string                         `json:"entryContainerId,omitempty"`
	Containers           map[string]string              `json:"containers"`
	Volumes              map[string]dockerVolumeInspect `json:"volumes"`
	Deployment           platformdeploy.HostDeployment  `json:"deployment"`
	Complete             bool                           `json:"complete"`
}

type coolifyRemovalJournalRecord struct {
	Journal   coolifyRemovalJournal                   `json:"journal"`
	Signature localevidence.OwnerPolicyStateSignature `json:"signature"`
}

// RemoveCoolifyWorkload implements the canonical Owner-signed removal of one
// Coolify-managed application. Coolify deletes the service and its stored
// configuration but never its data; StackKits created the named volumes and
// alone applies the requested data disposition. The Marketplace, other
// services and the curated Coolify project stay untouched.
func RemoveCoolifyWorkload(ctx context.Context, workspace string, request workloadremoval.Request) (result workloadremoval.Result, err error) {
	progressed := false
	defer func() {
		if err != nil {
			var progress *RemovalProgressError
			if !errors.As(err, &progress) {
				err = &RemovalProgressError{Err: err, Progressed: progressed}
			}
		}
	}()
	if ctx == nil {
		return result, errors.New("Coolify workload removal requires a context")
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
	deployment, err := selectedPaaSDeploymentFromApplied(target, request.Applied.Artifacts, coolifyAdapterRef, coolifyAdapterModuleRef)
	if err != nil {
		return result, err
	}
	if err := requireAppliedAdapterRemoval(deployment, coolifyAdapterRef, coolifyAdapterModuleRef); err != nil {
		return result, err
	}
	operations, err := NewOSCoolifyWorkloadOperations(workspace)
	if err != nil {
		return result, err
	}
	o := operations.(*osCoolifyWorkloadOperations)
	remover := standaloneComposeRemover{ops: o.prepared}
	project, err := o.prepared.prepareWithIdentityMutation(ctx, deployment, false)
	if err != nil {
		return result, err
	}
	if err := o.prepared.verifyPersisted(project); err != nil {
		return result, err
	}
	owned, hostPaths := standaloneComposeVolumeDeclarations(project.bundle)
	if err := standaloneComposeRejectUnsafeDelete(request.DataDisposition, hostPaths); err != nil {
		return result, err
	}
	state, present, err := o.manager(deployment)
	if err != nil {
		return result, err
	}
	// The opt-in Coolify provider owns its service through an OpenTofu root and
	// encrypted state; deleting the service here would leave that root behind.
	if present && state.RecipeDigest != "" {
		return result, errors.New("Coolify workload removal does not admit a provider-owned service; its OpenTofu provider root owns it")
	}
	journal, journaled, err := readCoolifyRemovalJournal(workspace, project.directory)
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
			return result, errors.New("Coolify interrupted removal requires its original applied workload")
		}
		if !journal.Complete && journal.DataDisposition != request.DataDisposition {
			return result, errors.New("Coolify interrupted removal requires its original data disposition")
		}
		if journal.Complete && journal.DataDisposition == workloadremoval.DataDispositionDelete && request.DataDisposition != workloadremoval.DataDispositionDelete {
			return result, errors.New("Coolify workload data was explicitly deleted; retaining it requires a new installation")
		}
		if present && !sameHostDeployment(state, journal.Deployment) {
			return result, errors.New("Coolify deployment custody changed after removal began")
		}
	} else {
		if !present || state.ArtifactDigest != deployment.ArtifactDigest {
			return result, errors.New("Coolify workload removal requires the exact signed deployment custody")
		}
		journal, err = admitCoolifyRemoval(ctx, o, remover, deployment, project, owned, state, request)
		if err != nil {
			return result, err
		}
	}
	journal.DataDisposition = request.DataDisposition
	journal.Complete = false
	if err := writeCoolifyRemovalJournal(workspace, project.directory, journal); err != nil {
		return result, err
	}
	if err := fresh(); err != nil {
		return result, err
	}
	if present {
		cfg, _, err := o.configuration(ctx, deployment)
		if err != nil {
			return result, err
		}
		if cfg.ServerID != journal.ServerID {
			return result, errors.New("Coolify removal server differs from node custody")
		}
		adapter := platformdeploy.NewCoolifyAdapter(cfg)
		absent, err := adapter.ServiceAbsent(ctx, journal.Deployment.Deployment)
		if err != nil {
			return result, err
		}
		if !absent {
			progressed = true
			if err := adapter.DeleteServiceKeepingData(ctx, journal.Deployment.Deployment); err != nil {
				return result, err
			}
		}
		if err := waitCoolifyServiceAbsent(ctx, adapter, journal.Deployment.Deployment); err != nil {
			return result, err
		}
	}
	project.runtimeProject = journal.ServiceUUID
	if err := waitCoolifyContainersAbsent(ctx, remover, project, journal); err != nil {
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
		if err := deleteCoolifyRemovalVolumes(ctx, remover, journal); err != nil {
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
	statuses, err := remover.readCoolifyStatuses(ctx, project)
	if err != nil {
		return result, err
	}
	observation, err := observeStandaloneComposeRemoval(project, request.DataDisposition, statuses, owned, hostPaths, final)
	if err != nil {
		return result, err
	}
	journal.Complete = true
	if err := writeCoolifyRemovalJournal(workspace, project.directory, journal); err != nil {
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
		return result, fmt.Errorf("canonicalize Coolify removal observation: %w", err)
	}
	sum := sha256.Sum256(canonical)
	if err := fresh(); err != nil {
		return result, err
	}
	return workloadremoval.NewResult(request, time.Now().UTC(), workloadremoval.Outcome{
		RequirementID:     target.RequirementID,
		WorkloadRef:       request.WorkloadRef,
		InstanceRef:       target.InstanceRef,
		RuntimeOwnerRef:   coolifyAdapterRef,
		ArtifactDigest:    deployment.ArtifactDigest,
		DataDisposition:   request.DataDisposition,
		Status:            workloadremoval.StatusRemoved,
		ObservedState:     workloadremoval.ObservedStateAbsent,
		ObservationRef:    "removal-observation://coolify/" + request.WorkloadRef + "/" + target.InstanceRef,
		ObservationDigest: "sha256:" + hex.EncodeToString(sum[:]),
	})
}

// admitCoolifyRemoval observes the signed service read-only: only declared
// components on their pinned images, and only the volumes Apply created.
func admitCoolifyRemoval(ctx context.Context, o *osCoolifyWorkloadOperations, remover standaloneComposeRemover, deployment SelectedPaaSWorkloadDeployment, project standaloneComposeProject, owned []standaloneComposeOwnedVolume, state platformdeploy.HostDeployment, request workloadremoval.Request) (coolifyRemovalJournal, error) {
	cfg, _, err := o.configuration(ctx, deployment)
	if err != nil {
		return coolifyRemovalJournal{}, err
	}
	if cfg.ServerID != state.ServerID || !validStandaloneComposeVolumeName(state.Deployment.ExternalID) {
		return coolifyRemovalJournal{}, errors.New("Coolify removal service differs from node custody")
	}
	project.runtimeProject = state.Deployment.ExternalID
	statuses, err := remover.readCoolifyStatuses(ctx, project)
	if err != nil {
		return coolifyRemovalJournal{}, err
	}
	containers := map[string]string{}
	declared := map[string]string{}
	for _, component := range project.bundle.Components {
		declared[component.ID] = component.ImageRef + "@" + component.ImageDigest
	}
	for service, status := range statuses {
		image, known := declared[service]
		if !known {
			return coolifyRemovalJournal{}, fmt.Errorf("Coolify service contains undeclared component %q; removal refuses orphan cleanup", service)
		}
		if status.Image != image {
			return coolifyRemovalJournal{}, fmt.Errorf("Coolify component %q differs from its pinned image", service)
		}
		id := strings.TrimPrefix(strings.TrimSpace(status.ID), "sha256:")
		if !validStandaloneComposeContainerID(id) {
			return coolifyRemovalJournal{}, fmt.Errorf("Coolify component %q has no exact container identity", service)
		}
		containers[service] = id
	}
	names, err := coolifyProjectVolumeNames(project)
	if err != nil {
		return coolifyRemovalJournal{}, err
	}
	volumes := map[string]dockerVolumeInspect{}
	for _, volume := range owned {
		name := names[volume.Key]
		if name == "" {
			return coolifyRemovalJournal{}, fmt.Errorf("Coolify volume %q has no node-owned name", volume.Key)
		}
		found, err := remover.volumeNamePresent(ctx, name)
		if err != nil {
			return coolifyRemovalJournal{}, err
		}
		if !found {
			return coolifyRemovalJournal{}, fmt.Errorf("Coolify volume %q is absent before removal", name)
		}
		inspected, err := remover.inspectVolume(ctx, name)
		if err != nil {
			return coolifyRemovalJournal{}, err
		}
		if inspected.Driver != "local" {
			return coolifyRemovalJournal{}, fmt.Errorf("Coolify volume %q is not a local node volume", name)
		}
		volumes[volume.Key] = inspected
	}
	return coolifyRemovalJournal{
		Version: coolifyRemovalJournalVersion, AppliedRequestDigest: request.AppliedRequestDigest,
		ArtifactDigest: deployment.ArtifactDigest, Project: project.name, ServiceUUID: state.Deployment.ExternalID,
		ServerID: state.ServerID, DataDisposition: request.DataDisposition,
		EntryContainerID: containers[project.bundle.EntryComponent], Containers: containers, Volumes: volumes,
		Deployment: state,
	}, nil
}

// coolifyProjectVolumeNames applies the node naming platformNodeCompose sends
// to Coolify: an explicit name, else <project>_<key>.
func coolifyProjectVolumeNames(project standaloneComposeProject) (map[string]string, error) {
	var document struct {
		Volumes map[string]struct {
			Name string `yaml:"name"`
		} `yaml:"volumes"`
	}
	if err := yaml.Unmarshal(project.compose, &document); err != nil {
		return nil, errors.New("Coolify node recipe is unreadable")
	}
	names := map[string]string{}
	for key, volume := range document.Volumes {
		name := volume.Name
		if name == "" {
			name = project.name + "_" + key
		}
		if !strings.HasPrefix(name, project.name+"_") || !validStandaloneComposeVolumeName(name) {
			return nil, errors.New("Coolify volume differs from the exact node workload")
		}
		names[key] = name
	}
	return names, nil
}

func (r standaloneComposeRemover) readCoolifyStatuses(ctx context.Context, project standaloneComposeProject) (map[string]standaloneComposePS, error) {
	raw, err := r.ops.runner.Run(ctx, standaloneComposeArgs(project, "ps"), project.directory)
	if err != nil {
		return nil, fmt.Errorf("Coolify service status observation failed: %w", err)
	}
	return parseStandaloneComposeStatusesAllowingEmpty(raw)
}

func waitCoolifyServiceAbsent(ctx context.Context, adapter *platformdeploy.CoolifyAdapter, ref platformdeploy.DeploymentRef) error {
	for {
		absent, err := adapter.ServiceAbsent(ctx, ref)
		if err != nil {
			return err
		}
		if absent {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Coolify did not delete the service in time: %w", ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}

// waitCoolifyContainersAbsent waits for Coolify's asynchronous cleanup and
// then requires that no container of the service project remains at all.
func waitCoolifyContainersAbsent(ctx context.Context, remover standaloneComposeRemover, project standaloneComposeProject, journal coolifyRemovalJournal) error {
	for {
		raw, err := remover.dockerCLI().Run(ctx, []string{"ps", "-aq", "--no-trunc", "--filter", "label=com.docker.compose.project=" + journal.ServiceUUID})
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
					return errors.New("admitted Coolify container is still present after service deletion")
				}
			}
			statuses, err := remover.readCoolifyStatuses(ctx, project)
			if err != nil {
				return err
			}
			if len(statuses) != 0 {
				return errors.New("Coolify service project still has containers after deletion")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Coolify did not remove the service containers in time: %w", ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}

func deleteCoolifyRemovalVolumes(ctx context.Context, remover standaloneComposeRemover, journal coolifyRemovalJournal) error {
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
			return fmt.Errorf("Coolify volume %q differs from admitted custody", expected.Name)
		}
		if _, err := remover.dockerCLI().Run(ctx, []string{"volume", "rm", expected.Name}); err != nil {
			if found, presentErr := remover.volumeNamePresent(ctx, expected.Name); presentErr != nil || found {
				return fmt.Errorf("delete Coolify volume %q: %w", expected.Name, errors.Join(err, presentErr))
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

func readCoolifyRemovalJournal(workspace, directory string) (coolifyRemovalJournal, bool, error) {
	root, err := confinedfs.Open(directory)
	if errors.Is(err, os.ErrNotExist) {
		return coolifyRemovalJournal{}, false, nil
	}
	if err != nil {
		return coolifyRemovalJournal{}, false, err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return coolifyRemovalJournal{}, false, err
	}
	file, err := view.Open(coolifyRemovalJournalFile)
	if errors.Is(err, os.ErrNotExist) {
		return coolifyRemovalJournal{}, false, nil
	}
	if err != nil {
		return coolifyRemovalJournal{}, false, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return coolifyRemovalJournal{}, false, errors.New("Coolify removal journal cannot be read")
	}
	var record coolifyRemovalJournalRecord
	if err := json.Unmarshal(raw, &record); err != nil || record.Journal.Version != coolifyRemovalJournalVersion {
		return coolifyRemovalJournal{}, false, errors.New("Coolify removal journal is invalid")
	}
	canonical, err := resolvedplan.CanonicalJSON(record.Journal)
	if err != nil {
		return coolifyRemovalJournal{}, false, err
	}
	if err := localevidence.VerifyOwnerPolicyState(workspace, canonical, record.Signature); err != nil {
		return coolifyRemovalJournal{}, false, fmt.Errorf("Coolify removal journal signature: %w", err)
	}
	return record.Journal, true, nil
}

func writeCoolifyRemovalJournal(workspace, directory string, journal coolifyRemovalJournal) error {
	canonical, err := resolvedplan.CanonicalJSON(journal)
	if err != nil {
		return err
	}
	signature, err := localevidence.SignOwnerPolicyState(workspace, canonical)
	if err != nil {
		return err
	}
	data, err := json.Marshal(coolifyRemovalJournalRecord{Journal: journal, Signature: signature})
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
	result, err := view.WriteAtomic0600(coolifyRemovalJournalFile, append(data, '\n'))
	if err == nil && (!result.Installed || !result.FileSynced) {
		return errors.New("Coolify removal journal was not durably installed")
	}
	return err
}
