package runtimeexecutorlocal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	cloudHostSecurityProviderRef      = "stackkits-cloud-host-security"
	cloudHostSecurityModuleRef        = "stackkits-cloud-host-security-runtime"
	cloudHostSecurityUnitRef          = "executor-contract"
	cloudHostSecurityOutputRef        = "cloud/host-security/executor-contract.json"
	cloudHostSecurityArtifactPrefix   = "cloud-host-security-executor-contract-instance-"
	cloudHostSecurityHealthSourceRef  = "cloud-host-security-contract"
	cloudHostSecurityMaxArtifactBytes = 128 << 10
)

type CloudFirewallPolicy struct {
	PolicyDigest        string   `json:"policyDigest"`
	StackID             string   `json:"stackId"`
	SiteRef             string   `json:"siteRef"`
	NodeRef             string   `json:"nodeRef"`
	ExecutionChannelRef string   `json:"executionChannelRef"`
	Roles               []string `json:"roles"`
	NetworkMode         string   `json:"networkMode"`
	TransportSubnet     string   `json:"transportSubnet"`
	IPv6                bool     `json:"ipv6"`
	InboundPolicy       string   `json:"inboundPolicy"`
	ExceptionAuthority  string   `json:"exceptionAuthority"`
}

type CloudHardeningPolicy struct {
	PolicyDigest        string `json:"policyDigest"`
	StackID             string `json:"stackId"`
	SiteRef             string `json:"siteRef"`
	NodeRef             string `json:"nodeRef"`
	ExecutionChannelRef string `json:"executionChannelRef"`
	Profile             string `json:"profile"`
	TLSMinVersion       string `json:"tlsMinVersion"`
}

type CloudHostSecurityApplyObservation struct {
	PolicyDigest string `json:"policyDigest"`
	Status       string `json:"status"`
}

type CloudHostSecurityVerifyExpectation struct {
	StackID             string `json:"stackId"`
	SiteRef             string `json:"siteRef"`
	NodeRef             string `json:"nodeRef"`
	ExecutionChannelRef string `json:"executionChannelRef"`
	PolicyDigest        string `json:"policyDigest"`
}

type CloudHostSecurityVerifyObservation struct {
	PolicyDigest    string `json:"policyDigest"`
	Status          string `json:"status"`
	FirewallStatus  string `json:"firewallStatus"`
	HardeningStatus string `json:"hardeningStatus"`
}

// CloudHostSecurityOperations is implemented by the authenticated execution
// channel owner. It intentionally exposes neither generic shell execution nor
// provider, endpoint, credential, discovery, or server-lifecycle operations.
type CloudHostSecurityOperations interface {
	ApplyFirewall(context.Context, CloudFirewallPolicy) (CloudHostSecurityApplyObservation, error)
	ApplyHardening(context.Context, CloudHardeningPolicy) (CloudHostSecurityApplyObservation, error)
	VerifyHostSecurity(context.Context, CloudHostSecurityVerifyExpectation) (CloudHostSecurityVerifyObservation, error)
}

// CloudHostSecurityAuthority is service-owned catalog authority selected at
// adapter registration. Request data can never define these hashes.
type CloudHostSecurityAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

// CloudHostSecurityExecutor applies the closed Cloud policy to one previously
// authorized node. It remains unregistered until a real authenticated host
// operations implementation is available.
type CloudHostSecurityExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalTargetBinding
	authority  CloudHostSecurityAuthority
	operations CloudHostSecurityOperations
}

func NewCloudHostSecurityExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority CloudHostSecurityAuthority, operations CloudHostSecurityOperations) *CloudHostSecurityExecutor {
	return &CloudHostSecurityExecutor{identity: identity, binding: binding, authority: authority, operations: operations}
}

func (e *CloudHostSecurityExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *CloudHostSecurityExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud host-security executor requires a context")
	}
	if e == nil || e.operations == nil || strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" || strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) || !validCoreHostBootstrapDigest(e.authority.ModuleContractHash) || !validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud host-security executor requires one explicit authenticated Cloud target binding")
	}
	target, health, firewall, hardening, expectation, err := validateCloudHostSecurityRequest(request, e.binding, e.authority)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	firewallObservation, err := e.operations.ApplyFirewall(ctx, defensiveCloudFirewallPolicy(firewall))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("apply exact Cloud host firewall: %w", err)
	}
	if firewallObservation.PolicyDigest != expectation.PolicyDigest || firewallObservation.Status != "applied" {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("firewall observation does not prove the exact applied Cloud host policy")
	}
	hardeningObservation, err := e.operations.ApplyHardening(ctx, hardening)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("apply exact Cloud host hardening: %w", err)
	}
	if hardeningObservation.PolicyDigest != expectation.PolicyDigest || hardeningObservation.Status != "applied" {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("hardening observation does not prove the exact applied Cloud host policy")
	}
	verifyObservation, err := e.operations.VerifyHostSecurity(ctx, expectation)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify exact Cloud host security: %w", err)
	}
	if verifyObservation.PolicyDigest != expectation.PolicyDigest || verifyObservation.Status != "ready" || verifyObservation.FirewallStatus != "enforced" || verifyObservation.HardeningStatus != "enforced" {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("verification does not prove the exact enforced Cloud host policy")
	}
	evidence, err := json.Marshal(struct {
		SchemaVersion string                             `json:"schemaVersion"`
		Firewall      CloudHostSecurityApplyObservation  `json:"firewall"`
		Hardening     CloudHostSecurityApplyObservation  `json:"hardening"`
		Verify        CloudHostSecurityVerifyObservation `json:"verify"`
	}{SchemaVersion: "stackkit.cloud-host-security-evidence/v1", Firewall: firewallObservation, Hardening: hardeningObservation, Verify: verifyObservation})
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal Cloud host-security evidence: %w", err)
	}
	digest := sha256.Sum256(evidence)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied, ObservationRef: "runtime-observation://cloud-host-security/" + target.InstanceRef, ObservationDigest: digestString}},
		Health:  []runtimeexecutor.HealthOutcome{{RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy, ObservationRef: "health-observation://cloud-host-security/" + target.InstanceRef, ObservationDigest: digestString}},
	}, nil
}

func defensiveCloudFirewallPolicy(policy CloudFirewallPolicy) CloudFirewallPolicy {
	policy.Roles = append([]string(nil), policy.Roles...)
	return policy
}

func validateCloudHostSecurityRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority CloudHostSecurityAuthority) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, CloudFirewallPolicy, CloudHardeningPolicy, CloudHostSecurityVerifyExpectation, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.AccessBindings) != 0 || len(request.Artifacts) != 1 {
		return emptyTarget, emptyHealth, CloudFirewallPolicy{}, CloudHardeningPolicy{}, CloudHostSecurityVerifyExpectation{}, errors.New("Cloud host-security executor requires exactly one runtime, one health target, one artifact, and no access binding")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.CloudHostSecurityExecutorBundleRendererContract()
	if target.OwnerKind != "module" || target.OwnerRef != cloudHostSecurityModuleRef || target.OwnerVersion != "" ||
		target.ProviderRef != cloudHostSecurityProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != cloudHostSecurityModuleRef || target.ModuleContractHash != authority.ModuleContractHash || target.OwnerContractHash != authority.ModuleContractHash ||
		target.UnitRef != cloudHostSecurityUnitRef || target.UnitContractHash != contract.ContractHash || target.RuntimeKind != "host" || target.RuntimeDelivery != "stackkit" || target.RuntimeEngine != "" ||
		target.WorkloadRef != "" || target.ImageRef != "" || len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) || target.ExecutionChannelRef != binding.ExecutionChannelRef || len(target.ArtifactRefs) != 1 {
		return emptyTarget, emptyHealth, CloudFirewallPolicy{}, CloudHardeningPolicy{}, CloudHostSecurityVerifyExpectation{}, errors.New("runtime target is not the exact bound Cloud host-security contract")
	}
	wantInstance := cloudHostSecurityUnitRef + "-node-" + binding.NodeRef
	wantArtifactID := cloudHostSecurityArtifactPrefix + wantInstance
	if target.InstanceRef != wantInstance || target.ArtifactRefs[0] != wantArtifactID {
		return emptyTarget, emptyHealth, CloudFirewallPolicy{}, CloudHardeningPolicy{}, CloudHostSecurityVerifyExpectation{}, errors.New("runtime target does not bind the exact node-local Cloud host-security artifact")
	}
	health := request.HealthTargets[0]
	if health.SourceRef != cloudHostSecurityHealthSourceRef || health.ContractHash != authority.HealthContractHash || health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
		health.TargetRef != cloudHostSecurityModuleRef || health.RouteRef != "" || health.BackendPoolRef != "" || !slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, CloudFirewallPolicy{}, CloudHardeningPolicy{}, CloudHostSecurityVerifyExpectation{}, errors.New("health target is not the exact Cloud host-security postcondition")
	}
	var artifact runtimeexecutor.Artifact
	found := 0
	for _, candidate := range request.Artifacts {
		if candidate.ID == wantArtifactID {
			artifact = candidate
			found++
		}
	}
	if found != 1 || artifact.Kind != "native-config" || artifact.Format != "json" || artifact.Mode != "0640" || artifact.OwnerKind != "render-instance" || artifact.OwnerRef != wantInstance ||
		artifact.OwnerContractHash != target.UnitContractHash || artifact.ProviderRef != cloudHostSecurityProviderRef || artifact.ProviderContractHash != target.ProviderContractHash ||
		artifact.ModuleRef != cloudHostSecurityModuleRef || artifact.ModuleContractHash != target.ModuleContractHash || artifact.UnitRef != cloudHostSecurityUnitRef || artifact.UnitContractHash != target.UnitContractHash ||
		artifact.InstanceRef != wantInstance || artifact.OutputRef != cloudHostSecurityOutputRef || !slices.Equal(artifact.SiteRefs, target.SiteRefs) || !slices.Equal(artifact.NodeRefs, target.NodeRefs) ||
		len(artifact.Content) == 0 || len(artifact.Content) > cloudHostSecurityMaxArtifactBytes {
		return emptyTarget, emptyHealth, CloudFirewallPolicy{}, CloudHardeningPolicy{}, CloudHostSecurityVerifyExpectation{}, errors.New("artifact is not the exact CUE-owned Cloud host-security instance")
	}
	digest := sha256.Sum256(artifact.Content)
	wantArtifactDigest := "sha256:" + hex.EncodeToString(digest[:])
	if artifact.Digest != wantArtifactDigest {
		return emptyTarget, emptyHealth, CloudFirewallPolicy{}, CloudHardeningPolicy{}, CloudHostSecurityVerifyExpectation{}, errors.New("Cloud host-security artifact digest does not match its immutable content")
	}
	policy, err := architecturev2renderer.ValidateCloudHostSecurityExecutorArtifact(artifact.Content, binding.SiteRef, binding.NodeRef)
	if err != nil {
		return emptyTarget, emptyHealth, CloudFirewallPolicy{}, CloudHardeningPolicy{}, CloudHostSecurityVerifyExpectation{}, fmt.Errorf("validate governed Cloud host-security policy: %w", err)
	}
	policyDigestInput, err := json.Marshal(struct {
		ArtifactDigest      string `json:"artifactDigest"`
		SiteRef             string `json:"siteRef"`
		NodeRef             string `json:"nodeRef"`
		ExecutionChannelRef string `json:"executionChannelRef"`
	}{artifact.Digest, binding.SiteRef, binding.NodeRef, binding.ExecutionChannelRef})
	if err != nil {
		return emptyTarget, emptyHealth, CloudFirewallPolicy{}, CloudHardeningPolicy{}, CloudHostSecurityVerifyExpectation{}, fmt.Errorf("bind Cloud host-security policy: %w", err)
	}
	policyDigestBytes := sha256.Sum256(policyDigestInput)
	policyDigest := "sha256:" + hex.EncodeToString(policyDigestBytes[:])
	firewall := CloudFirewallPolicy{
		PolicyDigest: policyDigest, StackID: policy.StackID, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, ExecutionChannelRef: binding.ExecutionChannelRef,
		Roles: append([]string(nil), policy.Roles...), NetworkMode: policy.NetworkMode, TransportSubnet: policy.TransportSubnet, IPv6: policy.IPv6,
		InboundPolicy: "default-deny-declared-services-only", ExceptionAuthority: "stackkits-cloud-public-edge-runtime",
	}
	hardening := CloudHardeningPolicy{PolicyDigest: policyDigest, StackID: policy.StackID, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, ExecutionChannelRef: binding.ExecutionChannelRef, Profile: "internet-host-baseline-v1", TLSMinVersion: policy.TLSMinVersion}
	expectation := CloudHostSecurityVerifyExpectation{StackID: policy.StackID, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, ExecutionChannelRef: binding.ExecutionChannelRef, PolicyDigest: policyDigest}
	return target, health, firewall, hardening, expectation, nil
}

var _ runtimeexecutor.Executor = (*CloudHostSecurityExecutor)(nil)
