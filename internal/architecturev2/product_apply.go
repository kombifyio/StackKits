package architecturev2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// ProductApplyInput contains request-specific bytes and already-held
// filesystem capabilities. Executor identity, capabilities, and producer
// trust are deliberately absent: the product service owns them.
type ProductApplyInput struct {
	Current        CurrentResolution
	Workspace      *confinedfs.Transaction
	OutputLock     *confinedfs.OutputLock
	Versions       generationartifact.ComponentVersions
	EvidenceBundle []byte
}

// ExecuteProductApply is the sole product entry into the native-v2 executor
// registry. It never accepts an adapter, executor identity, or trust root from
// the command/request caller.
func (s *Service) ExecuteProductApply(ctx context.Context, input ProductApplyInput) (result VerifiedApplyResult, returnErr error) {
	if ctx == nil {
		return VerifiedApplyResult{}, resolveError(ErrApplyAuthorization, "Apply execution context is required", nil)
	}
	if s == nil || !input.Current.valid || input.Current.owner != s.generation {
		return VerifiedApplyResult{}, resolveError(ErrApplyAuthorization, "Apply requires a current resolution issued by this product service", nil)
	}
	registry, err := s.productApplyExecutorRegistry(input.Current.plan, input.Versions.Runtime)
	if err != nil {
		return VerifiedApplyResult{}, err
	}
	authorizer, err := registry.authorizer(s)
	if err != nil {
		return VerifiedApplyResult{}, err
	}
	authorization, err := authorizer.authorize(applyAuthorizationInput{
		Context: ctx, Current: input.Current, Workspace: input.Workspace, OutputLock: input.OutputLock,
		Versions: input.Versions, EvidenceBundle: append([]byte(nil), input.EvidenceBundle...),
	})
	if err != nil {
		return VerifiedApplyResult{}, err
	}
	defer func() { returnErr = errors.Join(returnErr, authorization.Close()) }()
	return registry.execute(ctx, authorization)
}

func (s *Service) productApplyExecutorRegistry(plan generationartifact.VerifiedPlan, runtimeVersion string) (*applyExecutorRegistry, error) {
	if s != nil && s.productRuntimeOwners != nil {
		return newProductRuntimeOwnerApplyExecutorRegistry(plan, runtimeVersion, s.productApplyTrust, s.productRuntimeOwners)
	}
	return newProductApplyExecutorRegistry(plan, runtimeVersion, s.productApplyTrust)
}

func newProductRuntimeOwnerApplyExecutorRegistry(
	plan generationartifact.VerifiedPlan,
	runtimeVersion string,
	anchors []productApplyTrustAnchor,
	owners *ProductRuntimeOwnerRegistry,
) (*applyExecutorRegistry, error) {
	if owners == nil || owners.Identity() == (runtimeexecutor.ExecutorIdentity{}) {
		return nil, applyExecutorError(generationartifact.ErrExecutorMissing, "apply.executor", "product runtime-owner registry is not initialized", nil)
	}
	if strings.TrimSpace(runtimeVersion) == "" || owners.Identity().Version != runtimeVersion {
		return nil, applyExecutorError(generationartifact.ErrBindingMismatch, "apply.executor.identity", "product runtime version does not match the service-owned registry identity", nil)
	}
	requirements := plan.ApplyRequirements()
	if len(requirements.RuntimeInstances) == 0 || len(requirements.HealthRequirements) == 0 {
		return nil, applyExecutorError(generationartifact.ErrExecutorMissing, "apply.executor.capabilities", "product runtime-owner execution requires runtime and health targets", nil)
	}
	capabilities := make([]applyRuntimeCapability, 0, len(requirements.RuntimeInstances))
	capabilitySet := make(map[applyRuntimeCapability]struct{}, len(requirements.RuntimeInstances))
	runtimeByID := make(map[string]generationartifact.ApplyRuntimeRequirement, len(requirements.RuntimeInstances))
	for _, target := range requirements.RuntimeInstances {
		runtimeByID[target.ID] = target
		capability := applyRuntimeCapability{
			OwnerKind: target.OwnerKind, OwnerRef: target.OwnerRef, OwnerContractHash: target.OwnerContractHash,
			ProviderRef: target.ProviderRef, ProviderContractHash: target.ProviderContractHash,
			ModuleRef: target.ModuleRef, ModuleContractHash: target.ModuleContractHash,
			UnitRef: target.UnitRef, UnitContractHash: target.UnitContractHash,
			RuntimeKind: target.RuntimeKind, RuntimeDelivery: target.RuntimeDelivery, RuntimeEngine: target.RuntimeEngine,
		}
		if _, exists := capabilitySet[capability]; !exists {
			capabilitySet[capability] = struct{}{}
			capabilities = append(capabilities, capability)
		}
	}
	accessCapabilities := make([]applyAccessCapability, 0, len(requirements.AccessBindings))
	accessCapabilitySet := make(map[applyAccessCapability]struct{}, len(requirements.AccessBindings))
	for _, binding := range requirements.AccessBindings {
		target, exists := runtimeByID[binding.RuntimeRequirementID]
		if !exists || binding.ContractOwnerRef != target.ProviderRef {
			return nil, applyExecutorError(generationartifact.ErrBindingMismatch, "apply.executor.accessCapabilities", "Home access binding has no exact runtime owner", nil)
		}
		capability := applyAccessCapability{
			OwnerKind: target.OwnerKind, OwnerRef: target.OwnerRef, OwnerContractHash: target.OwnerContractHash,
			ProviderRef: target.ProviderRef, ProviderContractHash: target.ProviderContractHash,
			ModuleRef: target.ModuleRef, ModuleContractHash: target.ModuleContractHash,
			UnitRef: target.UnitRef, UnitContractHash: target.UnitContractHash,
			CapabilityRef: binding.CapabilityRef, CapabilityContractHash: binding.CapabilityContractHash,
		}
		if _, exists := accessCapabilitySet[capability]; !exists {
			accessCapabilitySet[capability] = struct{}{}
			accessCapabilities = append(accessCapabilities, capability)
		}
	}
	backupTargetCapabilities := make([]applyAccessCapability, 0, len(requirements.BackupTargetBindings))
	backupTargetCapabilitySet := make(map[applyAccessCapability]struct{}, len(requirements.BackupTargetBindings))
	for _, binding := range requirements.BackupTargetBindings {
		target, exists := runtimeByID[binding.RuntimeRequirementID]
		if !exists || binding.ContractOwnerRef != target.ProviderRef {
			return nil, applyExecutorError(generationartifact.ErrBindingMismatch, "apply.executor.backupTargetCapabilities", "Cloud backup-target binding has no exact runtime owner", nil)
		}
		capability := applyAccessCapability{
			OwnerKind: target.OwnerKind, OwnerRef: target.OwnerRef, OwnerContractHash: target.OwnerContractHash,
			ProviderRef: target.ProviderRef, ProviderContractHash: target.ProviderContractHash,
			ModuleRef: target.ModuleRef, ModuleContractHash: target.ModuleContractHash,
			UnitRef: target.UnitRef, UnitContractHash: target.UnitContractHash,
			CapabilityRef: binding.CapabilityRef, CapabilityContractHash: binding.CapabilityContractHash,
		}
		if _, exists := backupTargetCapabilitySet[capability]; !exists {
			backupTargetCapabilitySet[capability] = struct{}{}
			backupTargetCapabilities = append(backupTargetCapabilities, capability)
		}
	}
	artifacts := make([]applyArtifactCapability, 0, len(requirements.Artifacts))
	artifactSet := make(map[applyArtifactCapability]struct{}, len(requirements.Artifacts))
	for _, artifact := range requirements.Artifacts {
		contract := applyArtifactCapability{
			OwnerKind: artifact.OwnerKind, OwnerContractHash: artifact.OwnerContractHash,
			ProviderRef: artifact.ProviderRef, ProviderContractHash: artifact.ProviderContractHash,
			ModuleRef: artifact.ModuleRef, ModuleContractHash: artifact.ModuleContractHash,
			UnitRef: artifact.UnitRef, UnitContractHash: artifact.UnitContractHash,
			Kind: artifact.Kind, Format: artifact.Format, Mode: artifact.Mode,
		}
		if artifact.OwnerKind == "plan" {
			contract.OwnerContractHash = ""
		}
		if _, exists := artifactSet[contract]; !exists {
			artifactSet[contract] = struct{}{}
			artifacts = append(artifacts, contract)
		}
	}
	trustedProducers, err := materializeProductApplyTrust(plan, anchors)
	if err != nil {
		return nil, err
	}
	return newApplyExecutorRegistry(applyExecutorRegistration{
		Adapter: owners, Capabilities: capabilities, AccessCapabilities: accessCapabilities,
		BackupTargetCapabilities: backupTargetCapabilities,
		ArtifactContracts:        artifacts, TrustedProducers: trustedProducers,
	})
}

func newProductApplyExecutorRegistry(plan generationartifact.VerifiedPlan, runtimeVersion string, anchors []productApplyTrustAnchor) (*applyExecutorRegistry, error) {
	requirements := plan.ApplyRequirements()
	if len(requirements.RuntimeInstances) != 1 || len(requirements.HealthRequirements) != 1 {
		return nil, applyExecutorError(generationartifact.ErrExecutorMissing, "apply.executor.capabilities", "the current beta product registry supports exactly one security-baseline runtime and health target; got %d/%d", nil, len(requirements.RuntimeInstances), len(requirements.HealthRequirements))
	}
	var unitContractHash string
	capabilities := make([]applyRuntimeCapability, 0, len(requirements.RuntimeInstances))
	for _, target := range requirements.RuntimeInstances {
		if target.OwnerKind != "module" || target.OwnerRef != "security-baseline" || target.ModuleRef != "security-baseline" ||
			target.UnitRef != "host-policy" || target.RuntimeKind != "host" || target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "" {
			return nil, applyExecutorError(generationartifact.ErrExecutorMissing, "apply.executor.capabilities", "no product native-v2 adapter is registered for runtime target %q", nil, target.ID)
		}
		if unitContractHash == "" {
			unitContractHash = target.UnitContractHash
		} else if unitContractHash != target.UnitContractHash {
			return nil, applyExecutorError(generationartifact.ErrBindingMismatch, "apply.executor.capabilities", "security-baseline targets disagree on unit contract", nil)
		}
		capabilities = append(capabilities, applyRuntimeCapability{
			OwnerKind: target.OwnerKind, OwnerRef: target.OwnerRef, OwnerContractHash: target.OwnerContractHash,
			ProviderRef: target.ProviderRef, ProviderContractHash: target.ProviderContractHash,
			ModuleRef: target.ModuleRef, ModuleContractHash: target.ModuleContractHash,
			UnitRef: target.UnitRef, UnitContractHash: target.UnitContractHash,
			RuntimeKind: target.RuntimeKind, RuntimeDelivery: target.RuntimeDelivery, RuntimeEngine: target.RuntimeEngine,
		})
	}
	artifacts := make([]applyArtifactCapability, 0, len(requirements.Artifacts))
	for _, artifact := range requirements.Artifacts {
		if artifact.OwnerKind != "plan" && (artifact.OwnerKind != "render-instance" || artifact.ModuleRef != "security-baseline" || artifact.UnitRef != "host-policy") {
			return nil, applyExecutorError(generationartifact.ErrExecutorMissing, "apply.executor.artifactContracts", "no product native-v2 adapter is registered for artifact %q", nil, artifact.ID)
		}
		contract := applyArtifactCapability{
			OwnerKind: artifact.OwnerKind, OwnerContractHash: artifact.OwnerContractHash,
			ProviderRef: artifact.ProviderRef, ProviderContractHash: artifact.ProviderContractHash,
			ModuleRef: artifact.ModuleRef, ModuleContractHash: artifact.ModuleContractHash,
			UnitRef: artifact.UnitRef, UnitContractHash: artifact.UnitContractHash,
			Kind: artifact.Kind, Format: artifact.Format, Mode: artifact.Mode,
		}
		if artifact.OwnerKind == "plan" {
			contract.OwnerContractHash = ""
		}
		artifacts = append(artifacts, contract)
	}
	identity, err := productSecurityBaselineIdentity(runtimeVersion, unitContractHash)
	if err != nil {
		return nil, err
	}
	trustedProducers, err := materializeProductApplyTrust(plan, anchors)
	if err != nil {
		return nil, err
	}
	return newApplyExecutorRegistry(applyExecutorRegistration{
		Adapter:      runtimeexecutorlocal.NewSecurityBaselineExecutor(identity, nil),
		Capabilities: capabilities, ArtifactContracts: artifacts,
		TrustedProducers: trustedProducers,
	})
}

func productSecurityBaselineIdentity(runtimeVersion, unitContractHash string) (runtimeexecutor.ExecutorIdentity, error) {
	runtimeVersion = strings.TrimSpace(runtimeVersion)
	if runtimeVersion == "" || !strings.HasPrefix(unitContractHash, "sha256:") {
		return runtimeexecutor.ExecutorIdentity{}, applyExecutorError(generationartifact.ErrInvalidContract, "apply.executor.identity", "runtime version and exact security-baseline unit contract are required", nil)
	}
	canonical, err := resolvedplan.CanonicalJSON(map[string]any{
		"adapter": "stackkits-security-baseline-local/v1", "version": runtimeVersion, "unitContractHash": unitContractHash,
	})
	if err != nil {
		return runtimeexecutor.ExecutorIdentity{}, err
	}
	digest := sha256.Sum256(canonical)
	return runtimeexecutor.ExecutorIdentity{
		ID: "stackkits-security-baseline-local", Version: runtimeVersion, Digest: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}
