package runtimeexecutoropentofu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
	"github.com/kombifyio/stackkits/internal/tofu"
)

// Authority is the factory-owned trust for one prepared target.
type Authority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHashes map[string]string
}

// Runtime is the construction-owned host capability. Binary and ProvidersDir
// default to the packaged tofu and provider mirror; they are resolved at
// Execute so a missing package fails only an OpenTofu rollout, never a
// Compose one.
type Runtime struct {
	WorkspaceRoot string
	Native        runtimeexecutorlocal.NativeComposeRuntime
	Binary        string
	ProvidersDir  string
	// Timeout bounds each tofu command; zero keeps the wrapper default.
	Timeout time.Duration
}

// Executor realizes exactly one module's OpenTofu root.
type Executor struct {
	identity  runtimeexecutor.ExecutorIdentity
	binding   runtimeexecutorlocal.LocalTargetBinding
	authority Authority
	module    ModuleBinding
	runtime   Runtime
}

// NewExecutor binds one prepared target scope to the host runtime.
func NewExecutor(identity runtimeexecutor.ExecutorIdentity, binding runtimeexecutorlocal.LocalTargetBinding, authority Authority, module ModuleBinding, runtime Runtime) *Executor {
	hashes := make(map[string]string, len(authority.HealthContractHashes))
	for key, value := range authority.HealthContractHashes {
		hashes[key] = value
	}
	authority.HealthContractHashes = hashes
	return &Executor{identity: identity, binding: binding, authority: authority, module: module, runtime: runtime}
}

func (e *Executor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

// Execute writes the root, runs init, plan, and apply offline, and verifies
// the module. It never refreshes only, never replaces, and never destroys.
func (e *Executor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("OpenTofu executor requires a context")
	}
	if e == nil || e.runtime.Native == nil || strings.TrimSpace(e.runtime.WorkspaceRoot) == "" ||
		strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" ||
		strings.TrimSpace(e.binding.ExecutionChannelRef) == "" || e.module.Validate() != nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("OpenTofu executor requires one explicit local authority, module binding, and runtime")
	}
	target, artifact, stack, err := e.validateRequest(request)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	definition, err := rootComposePayload(artifact.Content)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	binary, providers, err := e.runtime.packagedTools()
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	health := append([]runtimeexecutor.HealthTarget(nil), request.HealthTargets...)
	workspace, err := filepath.Abs(e.runtime.WorkspaceRoot)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("resolve workspace: %w", err)
	}
	native := runtimeexecutorlocal.NativeComposeRequest{
		Target: target, Health: health, HealthContractHashes: e.authority.HealthContractHashes,
		ArtifactID: artifact.ID, ArtifactDigest: artifact.Digest, Definition: definition,
		// The root's local_file writes ../compose.yaml: the native runtime
		// Compose file, so both executors manage one project.
		ComposePath: filepath.Join(workspace, ".stackkit", "runtime", e.module.RuntimeDir, ComposeFile),
	}
	// Native steps that precede `up` (stackkit-server staging, origin
	// provisioner check) run before OpenTofu touches the runtime.
	preparation, err := e.runtime.Native.PrepareCompose(ctx, native)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("prepare the native side steps of %s: %w", target.ModuleRef, err)
	}
	relative, err := RootRelativePath(e.module.RuntimeDir)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	root, err := ensureRootDir(workspace, relative)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	files := []rootFile{{ConfigFile, artifact.Content, 0o640}}
	if stack != nil {
		// Under the terramate target the stack file sits beside main.tf, the
		// runtime root the stack graph names for this stack.
		files = append(files, rootFile{StackFile, stack.Content, 0o640})
	}
	if err := writeRoot(root, RootMarker{
		SchemaVersion: RootMarkerSchemaVersion, ModuleRef: target.ModuleRef, InstanceRef: target.InstanceRef,
		RuntimeDir: e.module.RuntimeDir, ComposeProject: e.module.ComposeProject,
	}, providers, files...); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	environment, err := e.runtime.Native.ComposeEnvironment(e.module.ModuleRef)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("resolve Compose interpolation environment for %s: %w", e.module.ModuleRef, err)
	}
	// local-exec inherits COMPOSE_PROJECT_NAME; the wrapped Compose project
	// is the native one whatever the rendered command line spells.
	environment = append(environment, "COMPOSE_PROJECT_NAME="+e.module.ComposeProject)
	record := observationRecord{
		SchemaVersion: observationSchemaVersion, ModuleRef: target.ModuleRef, InstanceRef: target.InstanceRef,
		RequirementID: target.RequirementID, ArtifactID: artifact.ID, ArtifactDigest: artifact.Digest,
		ComposeProject: e.module.ComposeProject, Root: relative,
	}
	if record.tofuRun, err = e.runtime.runRoot(ctx, root, binary, environment); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}

	// Native steps that follow `up` (stackkit-server recreation, step-ca
	// reload, PocketID owner realization and the TinyAuth reconcile `up`) run
	// against the Compose file OpenTofu wrote, as the native Apply does.
	if err := e.runtime.Native.CompleteCompose(ctx, native, preparation); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("complete the native side steps of %s: %w", target.ModuleRef, err)
	}
	observed, err := e.runtime.Native.ObserveCompose(ctx, native)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify the OpenTofu-applied %s: %w", target.ModuleRef, err)
	}
	if err := requireObservedHealth(observed, health); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	record.Verify = &observed
	digest, err := writeObservation(root, record)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	observationBase := target.ModuleRef + "/" + target.InstanceRef
	outcome := runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{
			RequirementID: target.RequirementID, InstanceRef: target.InstanceRef,
			Status:            runtimeexecutor.RuntimeStatusApplied,
			ObservationRef:    "runtime-observation://opentofu/" + observationBase,
			ObservationDigest: digest,
		}},
		Health: make([]runtimeexecutor.HealthOutcome, len(health)),
	}
	for index, item := range health {
		outcome.Health[index] = runtimeexecutor.HealthOutcome{
			RequirementID: item.RequirementID, TargetRef: item.TargetRef,
			Status:            runtimeexecutor.HealthStatusHealthy,
			ObservationRef:    "health-observation://opentofu/" + observationBase + "/" + item.RequirementID,
			ObservationDigest: digest,
		}
	}
	sort.Slice(outcome.Health, func(i, j int) bool {
		return outcome.Health[i].RequirementID < outcome.Health[j].RequirementID
	})
	return outcome, nil
}

func (e *Executor) validateRequest(request runtimeexecutor.ExecutionRequest) (runtimeexecutor.RuntimeTarget, runtimeexecutor.Artifact, *runtimeexecutor.Artifact, error) {
	if len(request.RuntimeTargets) != 1 || len(request.AccessBindings) != 0 || len(request.HealthTargets) == 0 {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.Artifact{}, nil, errors.New("OpenTofu executor requires exactly one runtime target, its health targets, and no access binding")
	}
	target := request.RuntimeTargets[0]
	wantArtifacts := 1
	if target.UnitRef == TerramateUnitRef {
		wantArtifacts = 2
	}
	if target.OwnerKind != "module" || target.OwnerRef != e.module.ModuleRef || target.OwnerVersion != "" ||
		target.ModuleRef != e.module.ModuleRef || target.ProviderRef != e.module.ProviderRef ||
		target.WorkloadRef != e.module.WorkloadRef || (target.UnitRef != UnitRef && target.UnitRef != TerramateUnitRef) ||
		target.ProviderContractHash != e.authority.ProviderContractHash ||
		target.ModuleContractHash != e.authority.ModuleContractHash ||
		target.OwnerContractHash != e.authority.ModuleContractHash || !validDigest(target.UnitContractHash) ||
		target.RuntimeKind != "container" || target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "docker" ||
		strings.TrimSpace(target.InstanceRef) == "" || target.ExecutionChannelRef != e.binding.ExecutionChannelRef ||
		!slices.Equal(target.SiteRefs, []string{e.binding.SiteRef}) ||
		!slices.Equal(target.NodeRefs, []string{e.binding.NodeRef}) ||
		len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		len(target.BackupTargetCapabilities) != 0 || len(target.BackupTargetBindingRefs) != 0 ||
		target.RuntimeAdapter != nil || len(target.ArtifactRefs) != wantArtifacts {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.Artifact{}, nil, fmt.Errorf("runtime target is not the exact locally bound OpenTofu unit of %s", e.module.ModuleRef)
	}
	artifact, stack, err := exactRootArtifacts(request.Artifacts, target)
	if err != nil {
		return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.Artifact{}, nil, err
	}
	seen := make(map[string]struct{}, len(request.HealthTargets))
	for _, item := range request.HealthTargets {
		hash, trusted := e.authority.HealthContractHashes[item.SourceRef]
		if _, duplicate := seen[item.SourceRef]; duplicate || !trusted || item.ContractHash != hash ||
			item.Phase != "post-apply" || !healthTargetsRuntime(item, target) {
			return runtimeexecutor.RuntimeTarget{}, runtimeexecutor.Artifact{}, nil, fmt.Errorf("health target %q is not a trusted post-apply gate of %s", item.RequirementID, target.RequirementID)
		}
		seen[item.SourceRef] = struct{}{}
	}
	return target, artifact, stack, nil
}

// exactRootArtifacts selects the executable OpenTofu root the target owns
// and, for the terramate unit, its stack file. Only immutable resolved-plan
// metadata may accompany them.
func exactRootArtifacts(artifacts []runtimeexecutor.Artifact, target runtimeexecutor.RuntimeTarget) (runtimeexecutor.Artifact, *runtimeexecutor.Artifact, error) {
	owned := make(map[string]runtimeexecutor.Artifact, len(target.ArtifactRefs))
	for _, candidate := range artifacts {
		if slices.Contains(target.ArtifactRefs, candidate.ID) {
			if _, duplicate := owned[candidate.ID]; duplicate {
				return runtimeexecutor.Artifact{}, nil, errors.New("OpenTofu request repeats a target artifact")
			}
			owned[candidate.ID] = candidate
			continue
		}
		if candidate.OwnerKind != "plan" || candidate.ExecutionClass != runtimeexecutor.ArtifactExecutionClassPlan ||
			candidate.Kind != "metadata" || candidate.Format != "json" {
			return runtimeexecutor.Artifact{}, nil, errors.New("OpenTofu request contains an unrelated executable artifact")
		}
	}
	if len(owned) != len(target.ArtifactRefs) {
		return runtimeexecutor.Artifact{}, nil, fmt.Errorf("artifact is not the exact CUE-owned OpenTofu root of %s", target.ModuleRef)
	}
	kind := ArtifactKind
	if target.UnitRef == TerramateUnitRef {
		kind = TerramateUnitRef
	}
	var root, stack *runtimeexecutor.Artifact
	for _, ref := range target.ArtifactRefs {
		artifact := owned[ref]
		if artifact.Kind != kind || artifact.Format != ArtifactFormat || artifact.Mode != ArtifactMode ||
			artifact.OwnerKind != "render-instance" || artifact.OwnerRef != target.InstanceRef ||
			artifact.OwnerContractHash != target.UnitContractHash || artifact.ProviderRef != target.ProviderRef ||
			artifact.ProviderContractHash != target.ProviderContractHash || artifact.ModuleRef != target.ModuleRef ||
			artifact.ModuleContractHash != target.ModuleContractHash || artifact.UnitRef != target.UnitRef ||
			artifact.UnitContractHash != target.UnitContractHash || artifact.InstanceRef != target.InstanceRef ||
			!slices.Equal(artifact.SiteRefs, target.SiteRefs) || !slices.Equal(artifact.NodeRefs, target.NodeRefs) ||
			len(artifact.Content) == 0 || len(artifact.Content) > maxArtifactBytes {
			return runtimeexecutor.Artifact{}, nil, fmt.Errorf("artifact is not the exact CUE-owned OpenTofu root of %s", target.ModuleRef)
		}
		if artifact.Digest != digestBytes(artifact.Content) {
			return runtimeexecutor.Artifact{}, nil, errors.New("OpenTofu artifact digest does not match immutable content")
		}
		switch path.Base(artifact.OutputRef) {
		case ConfigFile:
			if root != nil {
				return runtimeexecutor.Artifact{}, nil, errors.New("OpenTofu target owns more than one root configuration")
			}
			root = &artifact
		case StackFile:
			if stack != nil || target.UnitRef != TerramateUnitRef {
				return runtimeexecutor.Artifact{}, nil, errors.New("OpenTofu target owns an unexpected Terramate stack file")
			}
			stack = &artifact
		default:
			return runtimeexecutor.Artifact{}, nil, fmt.Errorf("artifact is not the exact CUE-owned OpenTofu root of %s", target.ModuleRef)
		}
	}
	if root == nil || (target.UnitRef == TerramateUnitRef) != (stack != nil) {
		return runtimeexecutor.Artifact{}, nil, fmt.Errorf("artifact is not the exact CUE-owned OpenTofu root of %s", target.ModuleRef)
	}
	return *root, stack, nil
}

func healthTargetsRuntime(health runtimeexecutor.HealthTarget, target runtimeexecutor.RuntimeTarget) bool {
	if !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return false
	}
	if health.RuntimeRequirementID != "" {
		return health.RuntimeRequirementID == target.RequirementID
	}
	return health.TargetKind == "module" && health.TargetRef == target.ModuleRef ||
		health.TargetKind == "provider" && health.TargetRef == target.ProviderRef ||
		health.TargetKind == "runtime" && health.TargetRef == target.InstanceRef
}

func requireTofuStep(step string, result *tofu.Result, err error) error {
	if err != nil {
		return fmt.Errorf("tofu %s did not complete: %w", step, err)
	}
	if result == nil || !result.Success {
		exitCode := -1
		diagnostic := ""
		if result != nil {
			exitCode = result.ExitCode
			diagnostic = boundedDiagnostic(result.Stderr)
		}
		return fmt.Errorf("tofu %s failed with exit code %d: %s", step, exitCode, diagnostic)
	}
	return nil
}

func requireObservedHealth(observed runtimeexecutorlocal.NativeComposeObservation, health []runtimeexecutor.HealthTarget) error {
	if observed.Status != "ready" {
		return errors.New("OpenTofu-applied module is not ready")
	}
	healthy := make(map[string]struct{}, len(observed.Probes))
	for _, probe := range observed.Probes {
		if probe.Status == "healthy" {
			healthy[probe.RequirementID] = struct{}{}
		}
	}
	for _, item := range health {
		if _, ok := healthy[item.RequirementID]; !ok {
			return fmt.Errorf("OpenTofu-applied module did not prove health target %q", item.RequirementID)
		}
	}
	return nil
}

type stepRecord struct {
	ExitCode int `json:"exitCode"`
}

type planRecord struct {
	ExitCode int `json:"exitCode"`
	Add      int `json:"add"`
	Change   int `json:"change"`
	Destroy  int `json:"destroy"`
}

type observationRecord struct {
	SchemaVersion  string `json:"schemaVersion"`
	ModuleRef      string `json:"moduleRef"`
	InstanceRef    string `json:"instanceRef"`
	RequirementID  string `json:"requirementId"`
	ArtifactID     string `json:"artifactId"`
	ArtifactDigest string `json:"artifactDigest"`
	Root           string `json:"root"`
	ComposeProject string `json:"composeProject,omitempty"`
	tofuRun
	Verify *runtimeexecutorlocal.NativeComposeObservation `json:"verify,omitempty"`
}

func writeObservation(root string, record any) (string, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("encode OpenTofu apply observation: %w", err)
	}
	if err := writeFileAtomic(root, ObservationFile, append(encoded, '\n'), 0o600); err != nil {
		return "", err
	}
	return digestBytes(encoded), nil
}

func boundedDiagnostic(stderr string) string {
	trimmed := strings.TrimSpace(stderr)
	if len(trimmed) > 512 {
		trimmed = "..." + trimmed[len(trimmed)-512:]
	}
	return trimmed
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

var _ runtimeexecutor.Executor = (*Executor)(nil)
