package nativehost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
	"github.com/kombifyio/stackkits/pkg/workloadremoval"
	"gopkg.in/yaml.v3"
)

const basementCoreRemovalProject = "stackkit-basement-core"

// basementCoreRemovalDocker is the closed Docker read and volume capability of
// Basement core removal. Compose mutation goes through the Core process runner.
type basementCoreRemovalDocker interface {
	Run(context.Context, []string) ([]byte, error)
}

type osBasementCoreRemovalDocker struct{}

func (osBasementCoreRemovalDocker) Run(ctx context.Context, args []string) ([]byte, error) {
	if err := validateBasementCoreRemovalDockerArgs(args); err != nil {
		return nil, err
	}
	return runClosedDockerCLI(ctx, args)
}

func validateBasementCoreRemovalDockerArgs(args []string) error {
	projectFilter := "label=" + composeProjectLabel + "=" + basementCoreRemovalProject
	switch {
	case slices.Equal(args, []string{"ps", "-aq", "--no-trunc"}),
		slices.Equal(args, []string{"ps", "-aq", "--no-trunc", "--filter", projectFilter}),
		slices.Equal(args, []string{"network", "ls", "--filter", projectFilter, "--format", "{{.Name}}"}),
		slices.Equal(args, []string{"volume", "ls", "--filter", projectFilter, "--format", "{{.Name}}"}):
		return nil
	case len(args) == 2 && args[0] == "inspect" && validStandaloneComposeContainerID(args[1]):
		return nil
	case len(args) == 3 && args[0] == "volume" && (args[1] == "inspect" || args[1] == "rm") &&
		strings.HasPrefix(args[2], basementCoreRemovalProject+"_") && validStandaloneComposeVolumeName(args[2]):
		return nil
	}
	return errors.New("Basement core removal docker contract rejected the requested operation")
}

type basementCoreRemover struct {
	workspace   string
	compose     basementCoreProcessRunner
	docker      basementCoreRemovalDocker
	environment func(string) ([]string, error)
	authority   BasementCoreRemovalAuthority
}

type basementCoreRemovalContainer struct {
	Service string `json:"service"`
	ID      string `json:"id"`
}

type basementCoreRemovalObservation struct {
	Project         string                         `json:"project"`
	WorkingDir      string                         `json:"workingDir"`
	DataDisposition string                         `json:"dataDisposition"`
	Removed         []basementCoreRemovalContainer `json:"removed"`
	VolumesDeleted  []string                       `json:"volumesDeleted"`
	VolumesRetained []string                       `json:"volumesRetained"`
}

const (
	basementCoreRemovalComposeUnit   = basementCoreUnitRef
	basementCoreRemovalOpenTofuUnit  = "opentofu"
	basementCoreRemovalTerramateUnit = "terramate"
)

// BasementCoreRemovalUnit reports which applied unit of the closed local
// Basement core runtime profiles the sealed target is: the native Compose
// unit, or the OpenTofu or Terramate root that wraps the same Compose project.
// Contract hashes are checked against the verified plan by the caller and the
// unit's artifacts by its removal owner; only this identity, never an adapter
// name, grants the native removal capability.
func BasementCoreRemovalUnit(target runtimeexecutor.RuntimeTarget) (string, bool) {
	profile, ok := basementClosedLocalCoreExecutionProfileForModule(target.ModuleRef)
	if !ok || basementCoreRemovalTargetShape(target, profile) != nil {
		return "", false
	}
	return target.UnitRef, true
}

func basementCoreRemovalTargetShape(target runtimeexecutor.RuntimeTarget, profile closedLocalCoreExecutionProfile) error {
	if len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 {
		return errors.New("Basement core removal requires one exact Site and node placement")
	}
	if target.OwnerKind != "module" || target.OwnerRef != profile.moduleRef || target.OwnerVersion != "" ||
		target.ProviderRef != profile.providerRef || target.ModuleRef != profile.moduleRef ||
		target.OwnerContractHash != target.ModuleContractHash ||
		strings.TrimSpace(target.InstanceRef) == "" || target.WorkloadRef != profile.workloadRef ||
		target.RuntimeKind != "container" || target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "docker" ||
		len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 ||
		len(target.AccessBindingRefs) != 0 || len(target.BackupTargetCapabilities) != 0 ||
		len(target.BackupTargetBindingRefs) != 0 || target.RuntimeAdapter != nil {
		return fmt.Errorf("runtime target is not the exact applied %s runtime", profile.displayName)
	}
	switch target.UnitRef {
	case basementCoreRemovalComposeUnit:
		if target.UnitContractHash != profile.rendererContract().ContractHash ||
			target.InstanceRef != profile.unitRef+"-node-"+target.NodeRefs[0] ||
			target.ImageRef != profile.imageRef || target.ImageDigest != profile.imageDigest ||
			!slices.Equal(target.ArtifactRefs, []string{profile.artifactPrefix + target.InstanceRef}) {
			return fmt.Errorf("runtime target is not the exact applied %s runtime", profile.displayName)
		}
	case basementCoreRemovalOpenTofuUnit, basementCoreRemovalTerramateUnit:
		// The OpenTofu root's artifacts and unit contract are verified by the
		// OpenTofu removal owner against its own renderer contracts.
	default:
		return fmt.Errorf("applied %s unit %q has no native removal owner", profile.displayName, target.UnitRef)
	}
	return nil
}

// BasementCoreRemovalAuthority is the exact applied runtime a unit's owner
// proved from the sealed request before native removal may run.
type BasementCoreRemovalAuthority struct {
	// ArtifactDigest is the applied executable artifact's digest.
	ArtifactDigest string
	// Definition is the Compose project the applied artifact carries.
	Definition []byte
	// Teardown removes the project's containers and networks. Nil runs the
	// native `docker compose down`; an OpenTofu unit destroys its root, whose
	// destroy provisioner runs the same `down`.
	Teardown func(ctx context.Context, environment []string) error
}

// RemoveBasementCoreWorkload removes an applied native Compose Basement core.
func RemoveBasementCoreWorkload(ctx context.Context, workspace string, request workloadremoval.Request) (workloadremoval.Result, error) {
	if len(request.Applied.RuntimeTargets) != 1 {
		return workloadremoval.Result{}, errors.New("Basement core removal requires exactly one applied runtime target")
	}
	target := request.Applied.RuntimeTargets[0]
	profile, supported := basementClosedLocalCoreExecutionProfileForModule(target.ModuleRef)
	if !supported {
		return workloadremoval.Result{}, errors.New("Basement core removal does not support the applied local profile")
	}
	if err := basementCoreRemovalTargetShape(target, profile); err != nil {
		return workloadremoval.Result{}, err
	}
	if target.UnitRef != basementCoreRemovalComposeUnit {
		return workloadremoval.Result{}, fmt.Errorf("applied unit %q is not removed by the native Compose owner", target.UnitRef)
	}
	artifact, err := exactClosedLocalCoreArtifact(
		request.Applied.Artifacts, target, target.ArtifactRefs[0], profile.rendererContract().ContractHash, profile,
	)
	if err != nil {
		return workloadremoval.Result{}, err
	}
	return RemoveBasementCoreProject(ctx, workspace, request, BasementCoreRemovalAuthority{
		ArtifactDigest: artifact.Digest, Definition: artifact.Content,
	})
}

// RemoveBasementCoreProject removes the applied Basement core Compose project
// under one fresh Owner-signed request. Containers and project networks are
// removed; named data volumes are kept unless the signed disposition is
// delete. Owner custody, the workspace and every other workload stay outside
// its authority, and a workload that still uses the Core refuses removal.
func RemoveBasementCoreProject(
	ctx context.Context, workspace string, request workloadremoval.Request, authority BasementCoreRemovalAuthority,
) (workloadremoval.Result, error) {
	if strings.TrimSpace(workspace) == "" {
		return workloadremoval.Result{}, errors.New("Basement core removal requires a workspace root")
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return workloadremoval.Result{}, errors.New("resolve Basement core removal workspace")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return workloadremoval.Result{}, errors.New("Basement core removal requires an existing plain workspace directory")
	}
	return basementCoreRemover{
		workspace: filepath.Clean(absolute), compose: osBasementCoreProcessRunner{},
		docker: osBasementCoreRemovalDocker{}, environment: localevidence.ComposeInterpolationEnvironment,
		authority: authority,
	}.remove(ctx, request)
}

func (r basementCoreRemover) remove(ctx context.Context, request workloadremoval.Request) (result workloadremoval.Result, err error) {
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
		return result, errors.New("Basement core removal requires its operation context")
	}
	if err := request.ValidateAt(time.Now().UTC()); err != nil {
		return result, err
	}
	validUntil, err := time.Parse(time.RFC3339Nano, request.ValidUntil)
	if err != nil {
		return result, fmt.Errorf("workload-removal validity is unreadable: %w", err)
	}
	ctx, cancel := context.WithDeadline(ctx, validUntil)
	defer cancel()
	fresh := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return request.ValidateAt(time.Now().UTC())
	}
	payload, err := request.AuthorizationBytes()
	if err != nil {
		return result, err
	}
	if err := localevidence.VerifyOwnerLifecycleMutation(r.workspace, payload, localevidence.OwnerLifecycleMutationSignature{
		OwnerRef: request.Authorization.OwnerRef, KeyID: request.Authorization.KeyID, Value: request.Authorization.Value,
	}); err != nil {
		return result, fmt.Errorf("verify workload-removal Owner authorization: %w", err)
	}
	target := request.Applied.RuntimeTargets[0]
	profile, supported := basementClosedLocalCoreExecutionProfileForModule(target.ModuleRef)
	if !supported {
		return result, errors.New("Basement core removal does not support the applied local profile")
	}
	if err := basementCoreRemovalTargetShape(target, profile); err != nil {
		return result, err
	}
	owner, err := localevidence.LoadOwnerCustody(r.workspace)
	if err != nil {
		return result, fmt.Errorf("load Basement core removal owner placement: %w", err)
	}
	if target.SiteRefs[0] != owner.Binding.SiteRef || target.NodeRefs[0] != owner.Binding.NodeRef ||
		target.ExecutionChannelRef != owner.Binding.ChannelRef {
		return result, errors.New("Basement core removal target differs from this workspace's local owner placement")
	}
	if !validCoreHostBootstrapDigest(r.authority.ArtifactDigest) || len(r.authority.Definition) == 0 {
		return result, errors.New("Basement core removal requires the exact applied artifact")
	}
	definition := r.authority.Definition
	bound, err := bindKopiaApplicationHostPaths(ctx, r.workspace, target.SiteRefs[0], target.NodeRefs[0], definition)
	if err != nil {
		return result, err
	}
	composePath := filepath.Join(r.workspace, ".stackkit", "runtime", "basement-core", "compose.yaml")
	persisted, persistedErr := readStablePrivateBasementRuntimeFile(r.workspace, composePath)
	composeExists := persistedErr == nil
	if persistedErr != nil && !errors.Is(persistedErr, os.ErrNotExist) {
		return result, fmt.Errorf("verify Basement core Compose authority: %w", persistedErr)
	}
	if composeExists && !bytes.Equal(persisted, definition) && !bytes.Equal(persisted, bound) {
		return result, errors.New("Basement core removal refuses a runtime Compose definition that differs from the applied artifact")
	}
	volumeKeys, err := basementCoreDeclaredVolumeKeys(definition)
	if err != nil {
		return result, err
	}
	services := map[string]struct{}{}
	for _, service := range profile.serviceContracts() {
		services[service.Ref] = struct{}{}
	}
	runtimeDir := filepath.Dir(composePath)
	present, err := r.projectContainers(ctx, runtimeDir, composePath, services)
	if err != nil {
		return result, err
	}
	if !composeExists && len(present) != 0 {
		return result, errors.New("Basement core containers are present without their applied Compose definition")
	}
	if err := r.requireNoDependentWorkload(ctx, present); err != nil {
		return result, err
	}
	networks, err := r.projectNetworks(ctx)
	if err != nil {
		return result, err
	}
	if len(present) != 0 || len(networks) != 0 || r.authority.Teardown != nil {
		environment, err := r.environment(r.workspace)
		if err != nil {
			return result, err
		}
		if err := fresh(); err != nil {
			return result, err
		}
		progressed = true
		if r.authority.Teardown != nil {
			err = r.authority.Teardown(ctx, environment)
		} else if composeExists {
			_, err = r.compose.Run(ctx, basementCoreComposeArgs(composePath, "down"), runtimeDir, environment)
		} else {
			err = errors.New("the applied Compose definition is absent")
		}
		if err != nil {
			return result, fmt.Errorf("remove the Basement core Compose project: %w", err)
		}
	}
	if err := r.requireProjectAbsent(ctx); err != nil {
		return result, err
	}
	deleted := []string{}
	if request.DataDisposition == workloadremoval.DataDispositionDelete {
		if deleted, err = r.deleteDeclaredVolumes(ctx, volumeKeys, fresh, &progressed); err != nil {
			return result, err
		}
	}
	retained, err := r.projectVolumes(ctx)
	if err != nil {
		return result, err
	}
	for _, name := range retained {
		if request.DataDisposition == workloadremoval.DataDispositionDelete && slices.Contains(declaredVolumeNames(volumeKeys), name) {
			return result, errors.New("Basement core deletion did not observe its declared data volumes absent")
		}
	}
	// The Core's MCP credentials die with the runtime that served them, so an
	// uninstalled Core leaves no valid agent credential behind.
	if err := fresh(); err != nil {
		return result, err
	}
	progressed = true
	if err := localevidence.RemoveStackKitServerCredentials(r.workspace); err != nil {
		return result, fmt.Errorf("remove the Basement core MCP credentials: %w", err)
	}
	sort.Slice(present, func(i, j int) bool { return present[i].Service < present[j].Service })
	removed := make([]basementCoreRemovalContainer, len(present))
	copy(removed, present)
	observation, err := resolvedplan.CanonicalJSON(basementCoreRemovalObservation{
		Project: basementCoreRemovalProject, WorkingDir: runtimeDir, DataDisposition: request.DataDisposition,
		Removed: removed, VolumesDeleted: deleted, VolumesRetained: retained,
	})
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(observation)
	if err := fresh(); err != nil {
		return result, err
	}
	return workloadremoval.NewResult(request, time.Now().UTC(), workloadremoval.Outcome{
		RequirementID: target.RequirementID, WorkloadRef: request.WorkloadRef, InstanceRef: target.InstanceRef,
		RuntimeOwnerRef: target.OwnerRef, ArtifactDigest: r.authority.ArtifactDigest, DataDisposition: request.DataDisposition,
		Status: workloadremoval.StatusRemoved, ObservedState: workloadremoval.ObservedStateAbsent,
		ObservationRef:    "removal-observation://basement-core/" + target.InstanceRef,
		ObservationDigest: "sha256:" + hex.EncodeToString(sum[:]),
	})
}

var basementCoreVolumeKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// basementCoreDeclaredVolumeKeys returns the named volumes the authorized
// definition declares. The renderer emits them without options, so any other
// shape is refused instead of interpreted.
func basementCoreDeclaredVolumeKeys(definition []byte) ([]string, error) {
	var document struct {
		Volumes map[string]map[string]any `yaml:"volumes"`
	}
	if err := yaml.Unmarshal(definition, &document); err != nil {
		return nil, fmt.Errorf("read the Basement core Compose volumes: %w", err)
	}
	keys := make([]string, 0, len(document.Volumes))
	for key, options := range document.Volumes {
		if !basementCoreVolumeKeyPattern.MatchString(key) || len(options) != 0 {
			return nil, errors.New("Basement core Compose declares a volume outside the closed contract")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func declaredVolumeNames(keys []string) []string {
	names := make([]string, len(keys))
	for index, key := range keys {
		names[index] = basementCoreRemovalProject + "_" + key
	}
	return names
}

func (r basementCoreRemover) projectContainers(
	ctx context.Context, runtimeDir, composePath string, services map[string]struct{},
) ([]basementCoreRemovalContainer, error) {
	raw, err := r.docker.Run(ctx, []string{"ps", "-aq", "--no-trunc", "--filter", "label=" + composeProjectLabel + "=" + basementCoreRemovalProject})
	if err != nil {
		return nil, fmt.Errorf("observe the Basement core containers: %w", err)
	}
	result := []basementCoreRemovalContainer{}
	for _, id := range strings.Fields(string(raw)) {
		inspected, err := r.inspectContainer(ctx, id)
		if err != nil {
			return nil, err
		}
		labels := inspected.Config.Labels
		service := labels[composeServiceLabel]
		if _, declared := services[service]; !declared || labels[composeProjectLabel] != basementCoreRemovalProject {
			return nil, fmt.Errorf("Basement core project contains the undeclared component %q; removal refuses orphan cleanup", service)
		}
		if workingDir := labels[composeWorkingDirLabel]; workingDir == "" || filepath.Clean(workingDir) != filepath.Clean(runtimeDir) ||
			!composeConfigFilesExact(labels[composeConfigFilesLabel], composePath) {
			return nil, errors.New("Basement core container is not bound to this workspace's applied Compose definition")
		}
		result = append(result, basementCoreRemovalContainer{Service: service, ID: id})
	}
	return result, nil
}

func (r basementCoreRemover) inspectContainer(ctx context.Context, id string) (dockerContainerInspect, error) {
	raw, err := r.docker.Run(ctx, []string{"inspect", id})
	if err != nil {
		return dockerContainerInspect{}, fmt.Errorf("inspect container identity: %w", err)
	}
	var values []dockerContainerInspect
	if err := json.Unmarshal(raw, &values); err != nil || len(values) != 1 || strings.TrimPrefix(values[0].ID, "sha256:") != id {
		return dockerContainerInspect{}, errors.New("container inspection is invalid")
	}
	return values[0], nil
}

// requireNoDependentWorkload refuses removal while another StackKits project
// still runs beside the Core, because those workloads attach to its networks and
// route through it. They are removed first.
func (r basementCoreRemover) requireNoDependentWorkload(ctx context.Context, own []basementCoreRemovalContainer) error {
	raw, err := r.docker.Run(ctx, []string{"ps", "-aq", "--no-trunc"})
	if err != nil {
		return fmt.Errorf("observe StackKits workloads beside the Basement core: %w", err)
	}
	ownIDs := map[string]struct{}{}
	for _, container := range own {
		ownIDs[container.ID] = struct{}{}
	}
	var dependents []string
	for _, id := range strings.Fields(string(raw)) {
		if _, owned := ownIDs[id]; owned {
			continue
		}
		inspected, err := r.inspectContainer(ctx, id)
		if err != nil {
			return err
		}
		project := inspected.Config.Labels[composeProjectLabel]
		if strings.HasPrefix(project, "stackkit-") && project != basementCoreRemovalProject {
			dependents = append(dependents, project)
		}
	}
	if len(dependents) != 0 {
		sort.Strings(dependents)
		return fmt.Errorf("Basement core removal requires its dependent workloads removed first: %s", strings.Join(slices.Compact(dependents), ", "))
	}
	return nil
}

func (r basementCoreRemover) projectNetworks(ctx context.Context) ([]string, error) {
	raw, err := r.docker.Run(ctx, []string{"network", "ls", "--filter", "label=" + composeProjectLabel + "=" + basementCoreRemovalProject, "--format", "{{.Name}}"})
	if err != nil {
		return nil, fmt.Errorf("observe the Basement core networks: %w", err)
	}
	return strings.Fields(string(raw)), nil
}

func (r basementCoreRemover) projectVolumes(ctx context.Context) ([]string, error) {
	raw, err := r.docker.Run(ctx, []string{"volume", "ls", "--filter", "label=" + composeProjectLabel + "=" + basementCoreRemovalProject, "--format", "{{.Name}}"})
	if err != nil {
		return nil, fmt.Errorf("observe the Basement core data volumes: %w", err)
	}
	names := strings.Fields(string(raw))
	sort.Strings(names)
	return names, nil
}

func (r basementCoreRemover) requireProjectAbsent(ctx context.Context) error {
	raw, err := r.docker.Run(ctx, []string{"ps", "-aq", "--no-trunc", "--filter", "label=" + composeProjectLabel + "=" + basementCoreRemovalProject})
	if err != nil {
		return fmt.Errorf("observe Basement core absence: %w", err)
	}
	if len(strings.Fields(string(raw))) != 0 {
		return errors.New("Basement core containers are still present after removal")
	}
	networks, err := r.projectNetworks(ctx)
	if err != nil {
		return err
	}
	if len(networks) != 0 {
		return errors.New("Basement core networks are still present after removal")
	}
	return nil
}

func (r basementCoreRemover) deleteDeclaredVolumes(
	ctx context.Context, keys []string, fresh func() error, progressed *bool,
) ([]string, error) {
	existing, err := r.projectVolumes(ctx)
	if err != nil {
		return nil, err
	}
	deleted := []string{}
	for _, key := range keys {
		name := basementCoreRemovalProject + "_" + key
		if !slices.Contains(existing, name) {
			continue
		}
		raw, err := r.docker.Run(ctx, []string{"volume", "inspect", name})
		if err != nil {
			return nil, fmt.Errorf("inspect Basement core volume %q: %w", key, err)
		}
		var inspected []dockerVolumeInspect
		if json.Unmarshal(raw, &inspected) != nil || len(inspected) != 1 {
			return nil, fmt.Errorf("Basement core volume %q observation is ambiguous", key)
		}
		volume := inspected[0]
		if volume.Name != name || volume.Driver != "local" || volume.Scope != "local" || len(volume.Options) != 0 ||
			volume.Labels[composeProjectLabel] != basementCoreRemovalProject || volume.Labels[composeVolumeLabel] != key {
			return nil, fmt.Errorf("Basement core volume %q differs from the applied project", key)
		}
		if err := fresh(); err != nil {
			return nil, err
		}
		*progressed = true
		if _, err := r.docker.Run(ctx, []string{"volume", "rm", name}); err != nil {
			return nil, fmt.Errorf("delete Basement core volume %q: %w", key, err)
		}
		deleted = append(deleted, name)
	}
	return deleted, nil
}
