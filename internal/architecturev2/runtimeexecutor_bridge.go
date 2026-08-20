package architecturev2

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// kombify-go-common currently names its workload-adapter wire class
// "selected-paas". StackKits keeps the broader CUE-owned
// "application-adapter" semantic and translates only at this shared-package
// boundary so standalone delivery is never modeled as a PaaS.
const sharedApplicationAdapterRuntimeDelivery = "selected-paas"

// sharedRuntimeExecutorBridge is the only translation boundary between the
// StackKits-owned authorization grant and the provider-neutral shared executor
// contract. StackKits retains registry, policy, trust, locking, and one-shot
// authorization authority; the shared package receives only immutable DTOs.
type sharedRuntimeExecutorBridge struct {
	executor runtimeexecutor.Executor
}

type sharedRuntimeExecutionChannelBinder interface {
	bindRuntimeExecutionChannels(runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionRequest, error)
}

type applyRuntimeSharedRequestProvider interface {
	sharedRuntimeRequest(applyRuntimeExecutionRequest) (runtimeexecutor.ExecutionRequest, error)
}

func newSharedRuntimeExecutorBridge(executor runtimeexecutor.Executor) (applyRuntimeExecutor, error) {
	if executor == nil {
		return nil, applyExecutorError(generationartifact.ErrExecutorMissing, "apply.executor", "shared runtime executor is required", nil)
	}
	return &sharedRuntimeExecutorBridge{executor: executor}, nil
}

func (b *sharedRuntimeExecutorBridge) Identity() generationartifact.ApplyExecutorIdentity {
	identity := b.executor.Identity()
	return generationartifact.ApplyExecutorIdentity{ID: identity.ID, Version: identity.Version, Digest: identity.Digest}
}

func (b *sharedRuntimeExecutorBridge) PrepareProductApplyRecovery(ctx context.Context, request applyRuntimeExecutionRequest, outputRoot string, validUntil time.Time) error {
	custodian, ok := b.executor.(productApplyRecoveryCustodian)
	if !ok {
		return nil
	}
	shared, err := b.sharedExecutionRequest(request)
	if err != nil {
		return err
	}
	canonical, err := newProductApplyRecoveryCapsule(request, shared, outputRoot, validUntil)
	if err != nil {
		return err
	}
	return custodian.storeProductApplyRecovery(ctx, shared.RequestDigest, canonical)
}

func (b *sharedRuntimeExecutorBridge) Execute(ctx context.Context, request applyRuntimeExecutionRequest) (applyRuntimeExecutionResult, error) {
	sharedRequest, err := b.sharedExecutionRequest(request)
	if err != nil {
		return applyRuntimeExecutionResult{}, err
	}
	var result runtimeexecutor.ExecutionResult
	if len(sharedRequest.AccessBindings) == 0 && len(sharedRequest.BackupTargetBindings) == 0 {
		result, err = runtimeexecutor.Invoke(ctx, b.executor, sharedRequest)
	} else {
		result, err = runtimeexecutor.InvokeAt(ctx, b.executor, sharedRequest, request.ExecutionAt)
	}
	if err != nil {
		return applyRuntimeExecutionResult{}, fmt.Errorf(
			"shared runtime execution failed (%s): %w",
			runtimeExecutionErrorChain(err),
			err,
		)
	}
	if result.RequestDigest != sharedRequest.RequestDigest {
		return applyRuntimeExecutionResult{}, fmt.Errorf("shared runtime executor result does not bind the exact sealed request")
	}
	return stackKitsExecutionResult(result), nil
}

// runtimeExecutionErrorChain preserves bounded, already-redacted nested
// executor stages. The shared runtime contract intentionally hides adapter
// payloads, but nested dispatch used to hide even the actionable failing
// boundary and made live failures impossible to diagnose.
func runtimeExecutionErrorChain(err error) string {
	const maxDepth = 12
	parts := make([]string, 0, maxDepth)
	for depth := 0; err != nil && depth < maxDepth; depth++ {
		message := strings.TrimSpace(err.Error())
		if message != "" && (len(parts) == 0 || parts[len(parts)-1] != message) {
			parts = append(parts, message)
		}
		err = errors.Unwrap(err)
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, " -> ")
}

func (b *sharedRuntimeExecutorBridge) sharedExecutionRequest(request applyRuntimeExecutionRequest) (runtimeexecutor.ExecutionRequest, error) {
	shared, err := unsealedSharedExecutionRequest(request)
	if err != nil {
		return runtimeexecutor.ExecutionRequest{}, err
	}
	if binder, ok := b.executor.(sharedRuntimeExecutionChannelBinder); ok {
		shared, err = binder.bindRuntimeExecutionChannels(shared)
		if err != nil {
			return runtimeexecutor.ExecutionRequest{}, err
		}
	}
	return runtimeexecutor.SealRequest(shared)
}

func (b *sharedRuntimeExecutorBridge) runtimeRequestDigest(request applyRuntimeExecutionRequest) (string, error) {
	shared, err := b.sharedRuntimeRequest(request)
	if err != nil {
		return "", err
	}
	return shared.RequestDigest, nil
}

func (b *sharedRuntimeExecutorBridge) sharedRuntimeRequest(request applyRuntimeExecutionRequest) (runtimeexecutor.ExecutionRequest, error) {
	return b.sharedExecutionRequest(request)
}

// sharedExecutionRequest is lossless for executable targets and immutable
// artifact bytes. Workload, secret, provider-owner, and evidence policy graphs
// remain behind the already-verified StackKits authorization boundary. From
// the host graph, only an exact opaque executionChannelRef may cross for a
// single-node target; addresses, credentials, provider data, and discovery
// inputs remain unreachable.
func sharedExecutionRequest(request applyRuntimeExecutionRequest) (runtimeexecutor.ExecutionRequest, error) {
	shared, err := unsealedSharedExecutionRequest(request)
	if err != nil {
		return runtimeexecutor.ExecutionRequest{}, err
	}
	return runtimeexecutor.SealRequest(shared)
}

func unsealedSharedExecutionRequest(request applyRuntimeExecutionRequest) (runtimeexecutor.ExecutionRequest, error) {
	shared := runtimeexecutor.ExecutionRequest{
		APIVersion: runtimeexecutor.APIVersion,
		Executor: runtimeexecutor.ExecutorIdentity{
			ID: request.Executor.ID, Version: request.Executor.Version, Digest: request.Executor.Digest,
		},
		PlanHash: request.Binding.PlanHash, ManifestHash: request.ManifestHash,
		GenerationReceiptHash: request.GenerationReceiptHash, RequirementsHash: request.RequirementsHash,
		EvidenceBundleHash:   request.EvidenceBundleHash,
		AccessBindings:       make([]runtimeexecutor.AccessBinding, len(request.Requirements.AccessBindings)),
		BackupTargetBindings: make([]runtimeexecutor.BackupTargetBinding, len(request.Requirements.BackupTargetBindings)),
		Artifacts:            make([]runtimeexecutor.Artifact, 0, len(request.Artifacts)),
	}
	if len(request.Requirements.AccessBindings) != 0 || len(request.Requirements.BackupTargetBindings) != 0 {
		if request.ExecutionAt.IsZero() || request.ExecutionAt.Location() != time.UTC {
			return runtimeexecutor.ExecutionRequest{}, fmt.Errorf("external binding execution requires one exact UTC authorization instant")
		}
		shared.AuthorizationTime = request.ExecutionAt.Format(time.RFC3339Nano)
	}
	runtimeTargets, err := sharedRuntimeTargets(request.Requirements)
	if err != nil {
		return runtimeexecutor.ExecutionRequest{}, err
	}
	shared.RuntimeTargets = runtimeTargets
	shared.HealthTargets = sharedHealthTargets(request.Requirements.HealthRequirements)
	for index, binding := range request.Requirements.AccessBindings {
		shared.AccessBindings[index] = runtimeexecutor.AccessBinding{
			ID: binding.ID, Kind: "home-access", RuntimeRequirementID: binding.RuntimeRequirementID,
			StackID: binding.StackID, SiteRef: binding.SiteRef, CapabilityRef: binding.CapabilityRef,
			ContractOwnerRef: binding.ContractOwnerRef, CapabilityContractHash: binding.CapabilityContractHash,
			TargetNodeRefs: append([]string(nil), binding.TargetNodeRefs...), RequirementsHash: binding.RequirementsHash,
			BindingRef: binding.BindingRef, BindingHash: binding.BindingHash, AccessFabricRef: binding.AccessFabricRef,
			StackKitsVersion: binding.StackKitsVersion, CandidateDigest: binding.CandidateDigest, SpecHash: binding.SpecHash,
			IssuedAt: binding.IssuedAt, ValidUntil: binding.ValidUntil,
		}
	}
	for index, binding := range request.Requirements.BackupTargetBindings {
		shared.BackupTargetBindings[index] = runtimeexecutor.BackupTargetBinding{
			ID: binding.ID, Kind: "backup-target", RuntimeRequirementID: binding.RuntimeRequirementID,
			StackID: binding.StackID, SiteRef: binding.SiteRef, CapabilityRef: binding.CapabilityRef,
			ContractOwnerRef: binding.ContractOwnerRef, CapabilityContractHash: binding.CapabilityContractHash,
			TargetNodeRefs: append([]string(nil), binding.TargetNodeRefs...), RequirementsHash: binding.RequirementsHash,
			BindingRef: binding.BindingRef, BindingHash: binding.BindingHash, BackupTargetRef: binding.BackupTargetRef,
			CustodyAttestationRef: binding.CustodyAttestationRef, StackKitsVersion: binding.StackKitsVersion,
			CandidateDigest: binding.CandidateDigest, SpecHash: binding.SpecHash,
			IssuedAt: binding.IssuedAt, ValidUntil: binding.ValidUntil,
		}
	}
	adapterArtifactRefs := sharedRuntimeAdapterArtifactRefs(request.Requirements.RuntimeInstances)
	for _, artifact := range request.Artifacts {
		if artifact.ExecutionClass == generationartifact.ApplyExecutionClassArtifactOnly {
			continue
		}
		if artifact.ExecutionClass == generationartifact.ApplyExecutionClassContractHandoff {
			if _, selected := adapterArtifactRefs[artifact.ID]; !selected {
				continue
			}
		}
		shared.Artifacts = append(shared.Artifacts, runtimeexecutor.Artifact{
			ID: artifact.ID, Kind: artifact.Kind, Format: artifact.Format, Mode: artifact.Mode,
			ExecutionClass: artifact.ExecutionClass,
			OwnerKind:      artifact.OwnerKind, OwnerRef: artifact.OwnerRef, OwnerContractHash: artifact.OwnerContractHash,
			ProviderRef: artifact.ProviderRef, ProviderContractHash: artifact.ProviderContractHash,
			ModuleRef: artifact.ModuleRef, ModuleContractHash: artifact.ModuleContractHash,
			UnitRef: artifact.UnitRef, UnitContractHash: artifact.UnitContractHash,
			InstanceRef: artifact.InstanceRef, OutputRef: artifact.OutputRef,
			SiteRefs: append([]string(nil), artifact.SiteRefs...), NodeRefs: append([]string(nil), artifact.NodeRefs...),
			Digest: artifact.SHA256, Content: append([]byte(nil), artifact.Content...),
		})
	}
	return shared, nil
}

func sharedRuntimeTargets(requirements generationartifact.ApplyRequirements) ([]runtimeexecutor.RuntimeTarget, error) {
	result := make([]runtimeexecutor.RuntimeTarget, len(requirements.RuntimeInstances))
	for index, target := range requirements.RuntimeInstances {
		// HealthGateRefs and EvidenceGateRefs are authorization-policy graph
		// edges, not adapter inputs. StackKits has already closed and authorized
		// them; the shared executor receives the resulting exact HealthTargets
		// and the authenticated EvidenceBundleHash instead.
		daemons := make([]runtimeexecutor.DaemonTarget, len(target.DaemonBindings))
		for daemonIndex, daemon := range target.DaemonBindings {
			daemons[daemonIndex] = runtimeexecutor.DaemonTarget{
				Ref: daemon.DaemonRef, InstanceRef: daemon.InstanceRef, Engine: daemon.Engine, SocketPath: daemon.SocketPath,
			}
		}
		executionChannelRef, err := runtimeTargetExecutionChannel(target, requirements.Hosts)
		if err != nil {
			return nil, err
		}
		accessCapabilities := runtimeTargetAccessCapabilities(target.ID, requirements.AccessBindings)
		backupTargetCapabilities := runtimeTargetBackupCapabilities(target.ID, requirements.BackupTargetBindings)
		runtimeDelivery := target.RuntimeDelivery
		if runtimeDelivery == "application-adapter" {
			runtimeDelivery = sharedApplicationAdapterRuntimeDelivery
		}
		result[index] = runtimeexecutor.RuntimeTarget{
			RequirementID: target.ID, OwnerKind: target.OwnerKind, OwnerRef: target.OwnerRef,
			OwnerVersion: target.OwnerVersion, OwnerContractHash: target.OwnerContractHash, ProviderRef: target.ProviderRef,
			ProviderContractHash: target.ProviderContractHash, ModuleRef: target.ModuleRef,
			ModuleContractHash: target.ModuleContractHash, UnitRef: target.UnitRef,
			UnitContractHash: target.UnitContractHash, RuntimeKind: target.RuntimeKind,
			RuntimeDelivery: runtimeDelivery, RuntimeEngine: target.RuntimeEngine,
			InstanceRef: target.InstanceRef, ExecutionChannelRef: executionChannelRef,
			SiteRefs: append([]string(nil), target.SiteRefs...),
			NodeRefs: append([]string(nil), target.NodeRefs...), WorkloadRef: target.WorkloadRef,
			ImageRef: target.ImageRef, ImageDigest: target.ImageDigest, DaemonBindings: daemons,
			ArtifactRefs:             append([]string(nil), target.ArtifactRefs...),
			RuntimeAdapter:           sharedRuntimeAdapterRequirement(target.RuntimeAdapter),
			AccessCapabilities:       accessCapabilities,
			AccessBindingRefs:        append([]string(nil), target.AccessBindingRefs...),
			BackupTargetCapabilities: backupTargetCapabilities,
			BackupTargetBindingRefs:  append([]string(nil), target.BackupTargetBindingRefs...),
		}
	}
	return result, nil
}

// sharedHealthTargets projects executable post-apply health requirements. The
// plan compiler excludes contract-only routes and binds each executable route
// probe to its exact runtime owner before it reaches this boundary.
func sharedHealthTargets(requirements []generationartifact.ApplyHealthRequirement) []runtimeexecutor.HealthTarget {
	result := make([]runtimeexecutor.HealthTarget, len(requirements))
	for index, target := range requirements {
		result[index] = runtimeexecutor.HealthTarget{
			RequirementID: target.ID, RuntimeRequirementID: target.RuntimeRequirementID,
			SourceRef: target.SourceRef, ContractHash: target.ContractHash, Phase: target.Phase,
			Kind: target.Kind, TargetKind: target.TargetKind, TargetRef: target.TargetRef,
			RouteRef: target.RouteRef, BackendPoolRef: target.BackendPoolRef,
			Probe:    sharedHealthProbe(target.Probe),
			SiteRefs: append([]string(nil), target.SiteRefs...), NodeRefs: append([]string(nil), target.NodeRefs...),
		}
	}
	return result
}

func sharedHealthProbe(input *generationartifact.ApplyHealthProbe) *runtimeexecutor.HealthProbe {
	if input == nil {
		return nil
	}
	return &runtimeexecutor.HealthProbe{
		Protocol: input.Protocol, Port: input.Port, TimeoutSeconds: input.TimeoutSeconds,
		Method: input.Method, FollowRedirects: input.FollowRedirects, Path: input.Path,
		ExpectedStatuses: append([]int(nil), input.ExpectedStatuses...),
	}
}

func sharedRuntimeAdapterArtifactRefs(targets []generationartifact.ApplyRuntimeRequirement) map[string]struct{} {
	result := map[string]struct{}{}
	for _, target := range targets {
		if target.RuntimeAdapter == nil {
			continue
		}
		for _, ref := range target.RuntimeAdapter.ArtifactRefs {
			result[ref] = struct{}{}
		}
		for _, agent := range target.RuntimeAdapter.Agents {
			for _, ref := range agent.ArtifactRefs {
				result[ref] = struct{}{}
			}
		}
	}
	return result
}

func sharedRuntimeAdapterRequirement(input *generationartifact.ApplyRuntimeAdapterRequirement) *runtimeexecutor.RuntimeAdapterBinding {
	if input == nil {
		return nil
	}
	result := &runtimeexecutor.RuntimeAdapterBinding{
		ID: input.ID, ProviderRef: input.ProviderRef, ProviderVersion: input.ProviderVersion,
		ProviderContractHash: input.ProviderContractHash, ModuleRef: input.ModuleRef, ModuleVersion: input.ModuleVersion,
		ModuleContractHash: input.ModuleContractHash, ArtifactRefs: append([]string(nil), input.ArtifactRefs...),
		Agents: make([]runtimeexecutor.RuntimeAdapterAgentBinding, len(input.Agents)),
	}
	for index, agent := range input.Agents {
		result.Agents[index] = runtimeexecutor.RuntimeAdapterAgentBinding{
			ID: agent.ID, ModuleRef: agent.ModuleRef, ModuleVersion: agent.ModuleVersion,
			ModuleContractHash: agent.ModuleContractHash, ArtifactRefs: append([]string(nil), agent.ArtifactRefs...),
		}
	}
	return result
}

func runtimeTargetAccessCapabilities(runtimeRequirementID string, bindings []generationartifact.ApplyAccessBindingRequirement) []runtimeexecutor.AccessCapability {
	byRef := map[string]string{}
	for _, binding := range bindings {
		if binding.RuntimeRequirementID == runtimeRequirementID {
			byRef[binding.CapabilityRef] = binding.CapabilityContractHash
		}
	}
	refs := make([]string, 0, len(byRef))
	for ref := range byRef {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	result := make([]runtimeexecutor.AccessCapability, 0, len(refs))
	for _, ref := range refs {
		result = append(result, runtimeexecutor.AccessCapability{Ref: ref, ContractHash: byRef[ref]})
	}
	return result
}

func runtimeTargetBackupCapabilities(runtimeRequirementID string, bindings []generationartifact.ApplyBackupTargetBindingRequirement) []runtimeexecutor.AccessCapability {
	byRef := map[string]string{}
	for _, binding := range bindings {
		if binding.RuntimeRequirementID == runtimeRequirementID {
			byRef[binding.CapabilityRef] = binding.CapabilityContractHash
		}
	}
	refs := make([]string, 0, len(byRef))
	for ref := range byRef {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	result := make([]runtimeexecutor.AccessCapability, 0, len(refs))
	for _, ref := range refs {
		result = append(result, runtimeexecutor.AccessCapability{Ref: ref, ContractHash: byRef[ref]})
	}
	return result
}

func runtimeTargetExecutionChannel(target generationartifact.ApplyRuntimeRequirement, hosts []generationartifact.ApplyHostRequirement) (string, error) {
	if len(target.NodeRefs) != 1 {
		return "", nil
	}
	var matched *generationartifact.ApplyHostRequirement
	for index := range hosts {
		if hosts[index].NodeRef != target.NodeRefs[0] {
			continue
		}
		if matched != nil {
			return "", fmt.Errorf("runtime target %q has multiple host requirements for node %q", target.ID, target.NodeRefs[0])
		}
		matched = &hosts[index]
	}
	if matched == nil {
		return "", nil
	}
	if len(target.SiteRefs) == 1 && matched.SiteRef != target.SiteRefs[0] {
		return "", fmt.Errorf("runtime target %q host requirement substitutes site %q", target.ID, matched.SiteRef)
	}
	return matched.ExecutionChannelRef, nil
}

func stackKitsExecutionResult(result runtimeexecutor.ExecutionResult) applyRuntimeExecutionResult {
	converted := applyRuntimeExecutionResult{
		Runtime: make([]applyRuntimeObservation, len(result.Runtime)), Health: make([]applyHealthObservation, len(result.Health)),
		SharedArtifactSetHash: result.ArtifactSetHash, SharedRequestDigest: result.RequestDigest, SharedResultDigest: result.ResultDigest,
	}
	for index, observation := range result.Runtime {
		converted.Runtime[index] = applyRuntimeObservation{
			RequirementID: observation.RequirementID, InstanceRef: observation.InstanceRef,
			Status: string(observation.Status), ObservationRef: observation.ObservationRef,
			ObservationDigest: observation.ObservationDigest,
		}
	}
	for index, observation := range result.Health {
		converted.Health[index] = applyHealthObservation{
			RequirementID: observation.RequirementID, TargetRef: observation.TargetRef,
			Status: string(observation.Status), ObservationRef: observation.ObservationRef,
			ObservationDigest: observation.ObservationDigest,
		}
	}
	return converted
}

func verifySharedRuntimeExecutionBinding(
	request applyRuntimeExecutionRequest,
	result applyRuntimeExecutionResult,
	providers ...applyRuntimeSharedRequestProvider,
) error {
	if !validApplySHA256(result.SharedArtifactSetHash) || !validApplySHA256(result.SharedRequestDigest) || !validApplySHA256(result.SharedResultDigest) {
		return applyExecutorError(generationartifact.ErrBindingMismatch, "apply.result.shared", "shared execution proof requires three canonical digests", nil)
	}
	var (
		sealed runtimeexecutor.ExecutionRequest
		err    error
	)
	if len(providers) > 0 && providers[0] != nil {
		sealed, err = providers[0].sharedRuntimeRequest(request)
	} else {
		sealed, err = sharedExecutionRequest(request)
	}
	if err != nil {
		return applyExecutorError(generationartifact.ErrBindingMismatch, "apply.result.shared", "reconstruct sealed shared execution request", err)
	}
	if result.SharedArtifactSetHash != sealed.ArtifactSetHash || result.SharedRequestDigest != sealed.RequestDigest {
		return applyExecutorError(generationartifact.ErrBindingMismatch, "apply.result.shared", "shared execution proof does not bind the exact path-free artifact set and request", nil)
	}
	sharedResult := runtimeexecutor.ExecutionResult{
		APIVersion: runtimeexecutor.APIVersion, Executor: sealed.Executor, PlanHash: sealed.PlanHash,
		ManifestHash: sealed.ManifestHash, GenerationReceiptHash: sealed.GenerationReceiptHash,
		RequirementsHash: sealed.RequirementsHash, EvidenceBundleHash: sealed.EvidenceBundleHash,
		ArtifactSetHash: sealed.ArtifactSetHash, RequestDigest: sealed.RequestDigest,
		Runtime: make([]runtimeexecutor.RuntimeOutcome, len(result.Runtime)),
		Health:  make([]runtimeexecutor.HealthOutcome, len(result.Health)), ResultDigest: result.SharedResultDigest,
	}
	for index, observation := range result.Runtime {
		sharedResult.Runtime[index] = runtimeexecutor.RuntimeOutcome{
			RequirementID: observation.RequirementID, InstanceRef: observation.InstanceRef,
			Status: runtimeexecutor.RuntimeStatus(observation.Status), ObservationRef: observation.ObservationRef,
			ObservationDigest: observation.ObservationDigest,
		}
	}
	for index, observation := range result.Health {
		sharedResult.Health[index] = runtimeexecutor.HealthOutcome{
			RequirementID: observation.RequirementID, TargetRef: observation.TargetRef,
			Status: runtimeexecutor.HealthStatus(observation.Status), ObservationRef: observation.ObservationRef,
			ObservationDigest: observation.ObservationDigest,
		}
	}
	if err := sharedResult.Validate(); err != nil {
		return applyExecutorError(generationartifact.ErrBindingMismatch, "apply.result.shared", "shared result digest does not bind the exact outcomes", err)
	}
	return nil
}
