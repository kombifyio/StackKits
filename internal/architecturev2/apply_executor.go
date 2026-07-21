package architecturev2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	verifiedApplyResultAPIVersion = "stackkit.apply-result/v1"
	verifiedApplyResultKind       = "VerifiedApplyResult"
)

var applyObservationRefPatterns = map[string]*regexp.Regexp{
	"runtime": regexp.MustCompile(`^(?:apply|runtime)-observation://[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._/-]*$`),
	"health":  regexp.MustCompile(`^(?:apply|health)-observation://[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._/-]*$`),
}

type applyRuntimeCapability struct {
	OwnerKind            string
	OwnerRef             string
	OwnerContractHash    string
	ProviderRef          string
	ProviderContractHash string
	ModuleRef            string
	ModuleContractHash   string
	UnitRef              string
	UnitContractHash     string
	RuntimeKind          string
	RuntimeDelivery      string
	RuntimeEngine        string
}

type applyArtifactCapability struct {
	OwnerKind            string
	OwnerContractHash    string
	ProviderRef          string
	ProviderContractHash string
	ModuleRef            string
	ModuleContractHash   string
	UnitRef              string
	UnitContractHash     string
	Kind                 string
	Format               string
	Mode                 string
}

// applyAccessCapability is an explicit adapter declaration that it consumes
// one exact Home access capability for one exact governed runtime authority.
// Registrations without this declaration fail closed for non-empty access
// bindings; shared common validates shape while the Home adapter remains the
// trusted custody/attestation authority for external fabric existence.
type applyAccessCapability struct {
	OwnerKind              string
	OwnerRef               string
	OwnerContractHash      string
	ProviderRef            string
	ProviderContractHash   string
	ModuleRef              string
	ModuleContractHash     string
	UnitRef                string
	UnitContractHash       string
	CapabilityRef          string
	CapabilityContractHash string
}

type applyExecutorRegistration struct {
	Adapter            runtimeexecutor.Executor
	Capabilities       []applyRuntimeCapability
	AccessCapabilities []applyAccessCapability
	ArtifactContracts  []applyArtifactCapability
	TrustedProducers   map[string]generationartifact.ApplyEvidenceProducerTrust
}

type applyRuntimeExecutor interface {
	Identity() generationartifact.ApplyExecutorIdentity
	Execute(context.Context, applyRuntimeExecutionRequest) (applyRuntimeExecutionResult, error)
}

type applyExecutorEntry struct {
	identity           generationartifact.ApplyExecutorIdentity
	adapter            applyRuntimeExecutor
	capabilities       map[applyRuntimeCapability]struct{}
	accessCapabilities map[applyAccessCapability]struct{}
	artifactContracts  map[applyArtifactCapability]struct{}
	trustedProducers   map[string]generationartifact.ApplyEvidenceProducerTrust
}

// applyExecutorRegistry is package-private until a product registry can bind
// concrete runtime adapters. It is the sole future owner of executor identity
// and evidence trust; neither is request data.
type applyExecutorRegistry struct {
	entry applyExecutorEntry
}

func newApplyExecutorRegistry(registration applyExecutorRegistration) (*applyExecutorRegistry, error) {
	if registration.Adapter == nil {
		return nil, applyExecutorError(generationartifact.ErrExecutorMissing, "apply.executor", "one exact runtime executor adapter is required", nil)
	}
	adapter, err := newSharedRuntimeExecutorBridge(registration.Adapter)
	if err != nil {
		return nil, err
	}
	identity, err := callApplyRuntimeExecutorIdentity(adapter)
	if err != nil {
		return nil, applyExecutorError(generationartifact.ErrInvalidContract, "apply.executor.identity", "registered executor identity could not be read", err)
	}
	if err := generationartifact.ValidateApplyExecutorIdentity(identity); err != nil {
		return nil, applyExecutorError(generationartifact.ErrInvalidContract, "apply.executor.identity", "registered executor identity is invalid", err)
	}
	capabilities := make(map[applyRuntimeCapability]struct{}, len(registration.Capabilities))
	for index, capability := range registration.Capabilities {
		if strings.TrimSpace(capability.OwnerKind) == "" || strings.TrimSpace(capability.OwnerRef) == "" || strings.TrimSpace(capability.OwnerContractHash) == "" ||
			strings.TrimSpace(capability.ProviderRef) == "" || strings.TrimSpace(capability.ProviderContractHash) == "" || strings.TrimSpace(capability.RuntimeKind) == "" || strings.TrimSpace(capability.RuntimeDelivery) == "" ||
			capability.OwnerKind != strings.TrimSpace(capability.OwnerKind) || capability.OwnerRef != strings.TrimSpace(capability.OwnerRef) ||
			capability.OwnerContractHash != strings.TrimSpace(capability.OwnerContractHash) || capability.ProviderRef != strings.TrimSpace(capability.ProviderRef) || capability.ProviderContractHash != strings.TrimSpace(capability.ProviderContractHash) ||
			capability.ModuleRef != strings.TrimSpace(capability.ModuleRef) || capability.ModuleContractHash != strings.TrimSpace(capability.ModuleContractHash) ||
			capability.UnitRef != strings.TrimSpace(capability.UnitRef) || capability.UnitContractHash != strings.TrimSpace(capability.UnitContractHash) ||
			capability.RuntimeKind != strings.TrimSpace(capability.RuntimeKind) || capability.RuntimeDelivery != strings.TrimSpace(capability.RuntimeDelivery) ||
			capability.RuntimeEngine != strings.TrimSpace(capability.RuntimeEngine) {
			return nil, applyExecutorError(generationartifact.ErrInvalidContract, fmt.Sprintf("apply.executor.capabilities[%d]", index), "governed runtime owner, contracts, provider, kind, delivery, and optional engine must be canonical", nil)
		}
		if _, duplicate := capabilities[capability]; duplicate {
			return nil, applyExecutorError(generationartifact.ErrInvalidContract, fmt.Sprintf("apply.executor.capabilities[%d]", index), "duplicate runtime/delivery capability", nil)
		}
		capabilities[capability] = struct{}{}
	}
	artifactContracts := make(map[applyArtifactCapability]struct{}, len(registration.ArtifactContracts))
	for index, contract := range registration.ArtifactContracts {
		if strings.TrimSpace(contract.OwnerKind) == "" || strings.TrimSpace(contract.Kind) == "" || strings.TrimSpace(contract.Format) == "" || strings.TrimSpace(contract.Mode) == "" ||
			contract.OwnerKind != strings.TrimSpace(contract.OwnerKind) || contract.OwnerContractHash != strings.TrimSpace(contract.OwnerContractHash) ||
			contract.ProviderRef != strings.TrimSpace(contract.ProviderRef) || contract.ProviderContractHash != strings.TrimSpace(contract.ProviderContractHash) ||
			contract.ModuleRef != strings.TrimSpace(contract.ModuleRef) || contract.ModuleContractHash != strings.TrimSpace(contract.ModuleContractHash) ||
			contract.UnitRef != strings.TrimSpace(contract.UnitRef) || contract.UnitContractHash != strings.TrimSpace(contract.UnitContractHash) ||
			contract.Kind != strings.TrimSpace(contract.Kind) || contract.Format != strings.TrimSpace(contract.Format) || contract.Mode != strings.TrimSpace(contract.Mode) {
			return nil, applyExecutorError(generationartifact.ErrInvalidContract, fmt.Sprintf("apply.executor.artifactContracts[%d]", index), "artifact owner, contracts, kind, format, and mode must be canonical", nil)
		}
		if contract.OwnerKind == "plan" {
			if contract.OwnerContractHash != "" || contract.ProviderRef != "" || contract.ProviderContractHash != "" || contract.ModuleRef != "" || contract.ModuleContractHash != "" || contract.UnitRef != "" || contract.UnitContractHash != "" {
				return nil, applyExecutorError(generationartifact.ErrInvalidContract, fmt.Sprintf("apply.executor.artifactContracts[%d]", index), "plan-owned metadata must not carry runtime owner authority", nil)
			}
		} else if contract.OwnerKind == "render-instance" {
			if contract.OwnerContractHash == "" || contract.ProviderRef == "" || contract.ProviderContractHash == "" || contract.ModuleRef == "" || contract.ModuleContractHash == "" || contract.UnitRef == "" || contract.UnitContractHash == "" {
				return nil, applyExecutorError(generationartifact.ErrInvalidContract, fmt.Sprintf("apply.executor.artifactContracts[%d]", index), "render-instance artifact capability requires exact provider/module/unit contracts", nil)
			}
		} else {
			return nil, applyExecutorError(generationartifact.ErrInvalidContract, fmt.Sprintf("apply.executor.artifactContracts[%d]", index), "unsupported artifact owner kind %q", nil, contract.OwnerKind)
		}
		if _, duplicate := artifactContracts[contract]; duplicate {
			return nil, applyExecutorError(generationartifact.ErrInvalidContract, fmt.Sprintf("apply.executor.artifactContracts[%d]", index), "duplicate artifact contract", nil)
		}
		artifactContracts[contract] = struct{}{}
	}
	accessCapabilities := make(map[applyAccessCapability]struct{}, len(registration.AccessCapabilities))
	for index, capability := range registration.AccessCapabilities {
		if capability.OwnerKind != strings.TrimSpace(capability.OwnerKind) || capability.OwnerRef != strings.TrimSpace(capability.OwnerRef) ||
			capability.ProviderRef != strings.TrimSpace(capability.ProviderRef) || capability.ModuleRef != strings.TrimSpace(capability.ModuleRef) ||
			capability.UnitRef != strings.TrimSpace(capability.UnitRef) || capability.CapabilityRef != strings.TrimSpace(capability.CapabilityRef) ||
			capability.OwnerKind == "" || capability.OwnerRef == "" || capability.ProviderRef == "" || capability.CapabilityRef == "" ||
			!validApplySHA256(capability.OwnerContractHash) || !validApplySHA256(capability.ProviderContractHash) ||
			!validApplySHA256(capability.ModuleContractHash) || !validApplySHA256(capability.UnitContractHash) || !validApplySHA256(capability.CapabilityContractHash) ||
			(capability.CapabilityRef != "private-remote-access" && capability.CapabilityRef != "public-publish-egress") {
			return nil, applyExecutorError(generationartifact.ErrInvalidContract, fmt.Sprintf("apply.executor.accessCapabilities[%d]", index), "exact runtime authority and closed Home access capability contracts must be canonical", nil)
		}
		matchesRuntimeAuthority := false
		for runtimeCapability := range capabilities {
			if runtimeCapability.OwnerKind == capability.OwnerKind && runtimeCapability.OwnerRef == capability.OwnerRef && runtimeCapability.OwnerContractHash == capability.OwnerContractHash &&
				runtimeCapability.ProviderRef == capability.ProviderRef && runtimeCapability.ProviderContractHash == capability.ProviderContractHash &&
				runtimeCapability.ModuleRef == capability.ModuleRef && runtimeCapability.ModuleContractHash == capability.ModuleContractHash &&
				runtimeCapability.UnitRef == capability.UnitRef && runtimeCapability.UnitContractHash == capability.UnitContractHash {
				matchesRuntimeAuthority = true
				break
			}
		}
		if !matchesRuntimeAuthority {
			return nil, applyExecutorError(generationartifact.ErrInvalidContract, fmt.Sprintf("apply.executor.accessCapabilities[%d]", index), "access capability has no exact registered runtime authority", nil)
		}
		if _, duplicate := accessCapabilities[capability]; duplicate {
			return nil, applyExecutorError(generationartifact.ErrInvalidContract, fmt.Sprintf("apply.executor.accessCapabilities[%d]", index), "duplicate Home access capability", nil)
		}
		accessCapabilities[capability] = struct{}{}
	}
	return &applyExecutorRegistry{entry: applyExecutorEntry{
		identity: identity, adapter: adapter, capabilities: capabilities, accessCapabilities: accessCapabilities, artifactContracts: artifactContracts,
		trustedProducers: cloneApplyProducerTrust(registration.TrustedProducers),
	}}, nil
}

func (r *applyExecutorRegistry) authorizer(service *Service) (*applyAuthorizer, error) {
	if r == nil || r.entry.adapter == nil {
		return nil, applyExecutorError(generationartifact.ErrExecutorMissing, "apply.executor", "runtime executor registry is not initialized", nil)
	}
	return service.newApplyAuthorizer(applyAuthorizationPolicy{
		Executor: r.entry.identity, TrustedProducers: r.entry.trustedProducers,
	})
}

type applyRuntimeExecutionRequest struct {
	Binding               generationartifact.PlanBinding
	ManifestHash          string
	GenerationReceiptHash string
	RequirementsHash      string
	EvidenceBundleHash    string
	ArtifactSetHash       string
	Executor              generationartifact.ApplyExecutorIdentity
	Requirements          generationartifact.ApplyRequirements
	Artifacts             []applyArtifactSnapshot
	ExecutionAt           time.Time
}

type applyRuntimeObservation struct {
	RequirementID     string `json:"requirementId"`
	InstanceRef       string `json:"instanceRef"`
	Status            string `json:"status"`
	ObservationRef    string `json:"observationRef"`
	ObservationDigest string `json:"observationDigest"`
}

type applyHealthObservation struct {
	RequirementID     string `json:"requirementId"`
	TargetRef         string `json:"targetRef"`
	Status            string `json:"status"`
	ObservationRef    string `json:"observationRef"`
	ObservationDigest string `json:"observationDigest"`
}

type applyRuntimeExecutionResult struct {
	Runtime               []applyRuntimeObservation
	Health                []applyHealthObservation
	SharedArtifactSetHash string
	SharedRequestDigest   string
	SharedResultDigest    string
}

type verifiedApplyResultEnvelope struct {
	APIVersion            string                                   `json:"apiVersion"`
	Kind                  string                                   `json:"kind"`
	Binding               generationartifact.PlanBinding           `json:"binding"`
	ManifestHash          string                                   `json:"manifestHash"`
	GenerationReceiptHash string                                   `json:"generationReceiptHash"`
	RequirementsHash      string                                   `json:"requirementsHash"`
	EvidenceBundleHash    string                                   `json:"evidenceBundleHash"`
	ArtifactSetHash       string                                   `json:"artifactSetHash"`
	SharedArtifactSetHash string                                   `json:"sharedArtifactSetHash,omitempty"`
	SharedRequestDigest   string                                   `json:"sharedRequestDigest,omitempty"`
	SharedResultDigest    string                                   `json:"sharedResultDigest,omitempty"`
	Executor              generationartifact.ApplyExecutorIdentity `json:"executor"`
	Runtime               []applyRuntimeObservation                `json:"runtime"`
	Health                []applyHealthObservation                 `json:"health"`
}

// VerifiedApplyResult is a defensive, hash-bound post-Apply projection. It is
// not a provider lifecycle receipt and contains no provider-native handles.
type VerifiedApplyResult struct {
	envelope   verifiedApplyResultEnvelope
	resultHash string
}

func (r VerifiedApplyResult) ResultHash() string { return r.resultHash }

// Canonical returns the immutable, hash-bound result envelope. The result hash
// is intentionally the content address and is not embedded recursively.
func (r VerifiedApplyResult) Canonical() ([]byte, error) {
	if r.resultHash == "" {
		return nil, applyExecutorError(generationartifact.ErrInvalidContract, "apply.result", "verified Apply result is empty", nil)
	}
	canonical, err := resolvedplan.CanonicalJSON(r.envelope)
	if err != nil {
		return nil, applyExecutorError(generationartifact.ErrInvalidContract, "apply.result", "canonicalize verified Apply result", err)
	}
	digest := sha256.Sum256(canonical)
	if "sha256:"+hex.EncodeToString(digest[:]) != r.resultHash {
		return nil, applyExecutorError(generationartifact.ErrBindingMismatch, "apply.result", "verified Apply result hash no longer matches its envelope", nil)
	}
	return canonical, nil
}

func (r *applyExecutorRegistry) execute(ctx context.Context, authorization VerifiedApplyAuthorization) (VerifiedApplyResult, error) {
	return r.executeWithClock(ctx, authorization, nil)
}

func (r *applyExecutorRegistry) executeAt(ctx context.Context, authorization VerifiedApplyAuthorization, at time.Time) (VerifiedApplyResult, error) {
	return r.executeWithClock(ctx, authorization, func() time.Time { return at })
}

func (r *applyExecutorRegistry) executeWithClock(ctx context.Context, authorization VerifiedApplyAuthorization, clock func() time.Time) (VerifiedApplyResult, error) {
	if ctx == nil {
		return VerifiedApplyResult{}, applyExecutorError(generationartifact.ErrInvalidContract, "apply.executor.context", "non-nil execution context is required", nil)
	}
	var (
		grant applyExecutionGrant
		err   error
	)
	if clock == nil {
		grant, err = authorization.consume()
	} else {
		grant, err = authorization.consumeWithClock(clock)
	}
	if err != nil {
		return VerifiedApplyResult{}, err
	}
	defer grant.release()
	if r == nil || r.entry.adapter == nil || r.entry.identity != grant.executor {
		return VerifiedApplyResult{}, applyExecutorError(generationartifact.ErrExecutorMissing, "apply.executor.identity", "authorization has no exact registered executor", nil)
	}
	currentIdentity, err := callApplyRuntimeExecutorIdentity(r.entry.adapter)
	if err != nil {
		return VerifiedApplyResult{}, applyExecutorError(generationartifact.ErrExecutorFailed, "apply.executor.identity", "registered executor identity could not be revalidated", err)
	}
	if currentIdentity != r.entry.identity {
		return VerifiedApplyResult{}, applyExecutorError(generationartifact.ErrBindingMismatch, "apply.executor.identity", "registered executor identity changed after registry construction", nil)
	}
	requirements := grant.plan.ApplyRequirements()
	if err := validateApplyExecutionSupport(r.entry, requirements, grant.artifacts); err != nil {
		return VerifiedApplyResult{}, err
	}
	artifactSetHash, err := hashApplyArtifactSet(grant.artifacts)
	if err != nil {
		return VerifiedApplyResult{}, err
	}
	request := applyRuntimeExecutionRequest{
		Binding: grant.plan.Binding(), ManifestHash: grant.manifestHash, GenerationReceiptHash: grant.receiptHash, RequirementsHash: grant.requirementsHash,
		EvidenceBundleHash: grant.bundleHash, ArtifactSetHash: artifactSetHash, Executor: grant.executor,
		Requirements: requirements, Artifacts: cloneApplyArtifactSnapshots(grant.artifacts), ExecutionAt: grant.evaluatedAt,
	}
	if err := ctx.Err(); err != nil {
		return VerifiedApplyResult{}, applyExecutorError(generationartifact.ErrExecutorFailed, "apply.executor.context", "execution context is already cancelled", err)
	}
	adapterRequest := request
	adapterRequest.Requirements = grant.plan.ApplyRequirements()
	adapterRequest.Artifacts = cloneApplyArtifactSnapshots(grant.artifacts)
	result, err := callApplyRuntimeExecutor(ctx, r.entry.adapter, adapterRequest)
	if err != nil {
		return VerifiedApplyResult{}, applyExecutorError(generationartifact.ErrExecutorFailed, "apply.executor", "runtime executor failed", err)
	}
	if err := ctx.Err(); err != nil {
		return VerifiedApplyResult{}, applyExecutorError(generationartifact.ErrExecutorFailed, "apply.executor.context", "execution context was cancelled during the adapter call", err)
	}
	return verifyApplyRuntimeExecutionResult(request, result)
}

func validateApplyExecutionSupport(entry applyExecutorEntry, requirements generationartifact.ApplyRequirements, artifacts []applyArtifactSnapshot) error {
	runtimeByID := make(map[string]generationartifact.ApplyRuntimeRequirement, len(requirements.RuntimeInstances))
	for _, runtime := range requirements.RuntimeInstances {
		runtimeByID[runtime.ID] = runtime
		capability := applyRuntimeCapability{
			OwnerKind: runtime.OwnerKind, OwnerRef: runtime.OwnerRef, OwnerContractHash: runtime.OwnerContractHash, ProviderRef: runtime.ProviderRef, ProviderContractHash: runtime.ProviderContractHash,
			ModuleRef: runtime.ModuleRef, ModuleContractHash: runtime.ModuleContractHash, UnitRef: runtime.UnitRef, UnitContractHash: runtime.UnitContractHash,
			RuntimeKind: runtime.RuntimeKind, RuntimeDelivery: runtime.RuntimeDelivery, RuntimeEngine: runtime.RuntimeEngine,
		}
		if _, supported := entry.capabilities[capability]; !supported {
			return applyExecutorError(generationartifact.ErrExecutorMissing, "apply.executor.capabilities", "no registered adapter supports runtime %q with delivery %q and engine %q", nil, runtime.RuntimeKind, runtime.RuntimeDelivery, runtime.RuntimeEngine)
		}
	}
	for _, binding := range requirements.AccessBindings {
		runtime, exists := runtimeByID[binding.RuntimeRequirementID]
		if !exists || !containsApplyString(runtime.AccessBindingRefs, binding.ID) || binding.ContractOwnerRef != runtime.ProviderRef {
			return applyExecutorError(generationartifact.ErrBindingMismatch, "apply.executor.accessCapabilities", "Home access binding %q has no exact governed runtime authority", nil, binding.ID)
		}
		capability := applyAccessCapability{
			OwnerKind: runtime.OwnerKind, OwnerRef: runtime.OwnerRef, OwnerContractHash: runtime.OwnerContractHash,
			ProviderRef: runtime.ProviderRef, ProviderContractHash: runtime.ProviderContractHash,
			ModuleRef: runtime.ModuleRef, ModuleContractHash: runtime.ModuleContractHash,
			UnitRef: runtime.UnitRef, UnitContractHash: runtime.UnitContractHash,
			CapabilityRef: binding.CapabilityRef, CapabilityContractHash: binding.CapabilityContractHash,
		}
		if _, supported := entry.accessCapabilities[capability]; !supported {
			return applyExecutorError(generationartifact.ErrExecutorMissing, "apply.executor.accessCapabilities", "selected adapter does not consume exact Home access capability %q", nil, binding.CapabilityRef)
		}
	}
	for _, artifact := range artifacts {
		contract := applyArtifactCapability{
			OwnerKind: artifact.OwnerKind, OwnerContractHash: artifact.OwnerContractHash,
			ProviderRef: artifact.ProviderRef, ProviderContractHash: artifact.ProviderContractHash,
			ModuleRef: artifact.ModuleRef, ModuleContractHash: artifact.ModuleContractHash,
			UnitRef: artifact.UnitRef, UnitContractHash: artifact.UnitContractHash,
			Kind: artifact.Kind, Format: artifact.Format, Mode: artifact.Mode,
		}
		if artifact.OwnerKind == "plan" {
			// The exact plan hash remains in the immutable artifact snapshot and
			// artifact-set hash. Static capability registration authorizes only
			// the closed plan-metadata shape, never one dynamic plan identity.
			contract.OwnerContractHash = ""
		}
		if _, supported := entry.artifactContracts[contract]; !supported {
			return applyExecutorError(generationartifact.ErrExecutorMissing, "apply.executor.artifactContracts", "no registered adapter supports artifact kind %q with format %q", nil, artifact.Kind, artifact.Format)
		}
	}
	return nil
}

func containsApplyString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func verifyApplyRuntimeExecutionResult(request applyRuntimeExecutionRequest, result applyRuntimeExecutionResult) (VerifiedApplyResult, error) {
	if err := verifySharedRuntimeExecutionBinding(request, result); err != nil {
		return VerifiedApplyResult{}, err
	}
	if err := verifyApplyRuntimeObservations(request.Requirements.RuntimeInstances, result.Runtime); err != nil {
		return VerifiedApplyResult{}, err
	}
	if err := verifyApplyHealthObservations(request.Requirements.HealthRequirements, result.Health); err != nil {
		return VerifiedApplyResult{}, err
	}
	envelope := verifiedApplyResultEnvelope{
		APIVersion: verifiedApplyResultAPIVersion, Kind: verifiedApplyResultKind, Binding: request.Binding,
		ManifestHash: request.ManifestHash, GenerationReceiptHash: request.GenerationReceiptHash, RequirementsHash: request.RequirementsHash,
		EvidenceBundleHash: request.EvidenceBundleHash, ArtifactSetHash: request.ArtifactSetHash,
		SharedArtifactSetHash: result.SharedArtifactSetHash, SharedRequestDigest: result.SharedRequestDigest,
		SharedResultDigest: result.SharedResultDigest,
		Executor:           request.Executor, Runtime: append([]applyRuntimeObservation(nil), result.Runtime...),
		Health: append([]applyHealthObservation(nil), result.Health...),
	}
	canonical, err := resolvedplan.CanonicalJSON(envelope)
	if err != nil {
		return VerifiedApplyResult{}, applyExecutorError(generationartifact.ErrInvalidContract, "apply.result", "canonicalize verified Apply result", err)
	}
	digest := sha256.Sum256(canonical)
	return VerifiedApplyResult{envelope: envelope, resultHash: "sha256:" + hex.EncodeToString(digest[:])}, nil
}

func callApplyRuntimeExecutor(ctx context.Context, adapter applyRuntimeExecutor, request applyRuntimeExecutionRequest) (result applyRuntimeExecutionResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("runtime executor panic: %v", recovered)
		}
	}()
	return adapter.Execute(ctx, request)
}

func callApplyRuntimeExecutorIdentity(adapter applyRuntimeExecutor) (identity generationartifact.ApplyExecutorIdentity, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("runtime executor identity panic: %v", recovered)
		}
	}()
	return adapter.Identity(), nil
}

func verifyApplyRuntimeObservations(requirements []generationartifact.ApplyRuntimeRequirement, observations []applyRuntimeObservation) error {
	if len(observations) != len(requirements) {
		return applyExecutorError(generationartifact.ErrEvidenceSetMismatch, "apply.result.runtime", "got %d runtime observations, require exact set of %d", nil, len(observations), len(requirements))
	}
	for index, requirement := range requirements {
		observation := observations[index]
		if observation.RequirementID != requirement.ID || observation.InstanceRef != requirement.InstanceRef {
			return applyExecutorError(generationartifact.ErrBindingMismatch, fmt.Sprintf("apply.result.runtime[%d]", index), "runtime observation does not match exact requirement and instance", nil)
		}
		if observation.Status != "applied" {
			return applyExecutorError(generationartifact.ErrExecutorFailed, fmt.Sprintf("apply.result.runtime[%d].status", index), "runtime requirement was not applied", nil)
		}
		if err := validateApplyObservationIdentity(observation.ObservationRef, observation.ObservationDigest, fmt.Sprintf("apply.result.runtime[%d]", index), "runtime"); err != nil {
			return err
		}
	}
	return nil
}

func verifyApplyHealthObservations(requirements []generationartifact.ApplyHealthRequirement, observations []applyHealthObservation) error {
	if len(observations) != len(requirements) {
		return applyExecutorError(generationartifact.ErrEvidenceSetMismatch, "apply.result.health", "got %d health observations, require exact set of %d", nil, len(observations), len(requirements))
	}
	for index, requirement := range requirements {
		observation := observations[index]
		if observation.RequirementID != requirement.ID || observation.TargetRef != requirement.TargetRef {
			return applyExecutorError(generationartifact.ErrBindingMismatch, fmt.Sprintf("apply.result.health[%d]", index), "health observation does not match exact requirement and target", nil)
		}
		if observation.Status != "healthy" {
			return applyExecutorError(generationartifact.ErrExecutorFailed, fmt.Sprintf("apply.result.health[%d].status", index), "health requirement is not healthy", nil)
		}
		if err := validateApplyObservationIdentity(observation.ObservationRef, observation.ObservationDigest, fmt.Sprintf("apply.result.health[%d]", index), "health"); err != nil {
			return err
		}
	}
	return nil
}

func validateApplyObservationIdentity(ref, digest, path, observationKind string) error {
	pattern, exists := applyObservationRefPatterns[observationKind]
	if !exists || !pattern.MatchString(ref) || !validApplySHA256(digest) {
		return applyExecutorError(generationartifact.ErrInvalidContract, path, "observation requires a safe opaque ref and canonical SHA-256 digest", nil)
	}
	return nil
}

func hashApplyArtifactSet(artifacts []applyArtifactSnapshot) (string, error) {
	type artifactIdentity struct {
		ID, Path, Kind, Format, Mode, ExecutionClass, OwnerKind, OwnerRef, OwnerContractHash string
		ProviderRef, ProviderContractHash, ModuleRef, ModuleContractHash                     string
		UnitRef, UnitContractHash, InstanceRef, OutputRef, SHA256                            string
		SiteRefs, NodeRefs                                                                   []string
	}
	identities := make([]artifactIdentity, 0, len(artifacts))
	for _, artifact := range artifacts {
		digest := sha256.Sum256(artifact.Content)
		if "sha256:"+hex.EncodeToString(digest[:]) != artifact.SHA256 {
			return "", applyExecutorError(generationartifact.ErrArtifactChanged, artifact.Path, "immutable artifact snapshot digest changed", nil)
		}
		identities = append(identities, artifactIdentity{
			ID: artifact.ID, Path: artifact.Path, Kind: artifact.Kind, Format: artifact.Format, Mode: artifact.Mode,
			ExecutionClass: artifact.ExecutionClass,
			OwnerKind:      artifact.OwnerKind, OwnerRef: artifact.OwnerRef, OwnerContractHash: artifact.OwnerContractHash,
			ProviderRef: artifact.ProviderRef, ProviderContractHash: artifact.ProviderContractHash,
			ModuleRef: artifact.ModuleRef, ModuleContractHash: artifact.ModuleContractHash, UnitRef: artifact.UnitRef, UnitContractHash: artifact.UnitContractHash,
			InstanceRef: artifact.InstanceRef, OutputRef: artifact.OutputRef, SiteRefs: append([]string(nil), artifact.SiteRefs...), NodeRefs: append([]string(nil), artifact.NodeRefs...), SHA256: artifact.SHA256,
		})
	}
	canonical, err := resolvedplan.CanonicalJSON(identities)
	if err != nil {
		return "", applyExecutorError(generationartifact.ErrInvalidContract, "apply.executor.artifacts", "canonicalize immutable artifact set", err)
	}
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func cloneApplyArtifactSnapshots(input []applyArtifactSnapshot) []applyArtifactSnapshot {
	result := append([]applyArtifactSnapshot(nil), input...)
	for index := range result {
		result[index].Content = append([]byte(nil), input[index].Content...)
		result[index].SiteRefs = append([]string(nil), input[index].SiteRefs...)
		result[index].NodeRefs = append([]string(nil), input[index].NodeRefs...)
	}
	return result
}

func validApplySHA256(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && value == strings.ToLower(value)
}

func applyExecutorError(code generationartifact.ErrorCode, path, message string, err error, args ...any) error {
	if len(args) != 0 {
		message = fmt.Sprintf(message, args...)
	}
	return &generationartifact.Error{Code: code, Path: path, Message: message, Err: err}
}
