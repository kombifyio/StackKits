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
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	cloudHostSecurityProviderRef       = "stackkits-cloud-host-security"
	cloudHostSecurityModuleRef         = "stackkits-cloud-host-security-runtime"
	cloudHostSecurityUnitRef           = "executor-contract"
	cloudHostSecurityOutputRef         = "cloud/host-security/executor-contract.json"
	cloudHostSecurityArtifactPrefix    = "cloud-host-security-executor-contract-instance-"
	cloudHostSecurityHealthSourceRef   = "cloud-host-security-health"
	cloudHostSecurityMaxArtifactBytes  = 128 << 10
	cloudHostSecurityMaxObservationAge = 5 * time.Minute
)

type CloudFirewallPolicy struct {
	PolicyDigest         string   `json:"policyDigest"`
	RequestDigest        string   `json:"requestDigest"`
	EvaluatedAt          string   `json:"evaluatedAt"`
	StackID              string   `json:"stackId"`
	SiteRef              string   `json:"siteRef"`
	NodeRef              string   `json:"nodeRef"`
	ExecutionChannelRef  string   `json:"executionChannelRef"`
	Roles                []string `json:"roles"`
	NetworkMode          string   `json:"networkMode"`
	TransportSubnet      string   `json:"transportSubnet"`
	IPv6                 bool     `json:"ipv6"`
	BaseRuleset          string   `json:"baseRuleset"`
	PublicEdgeChain      string   `json:"publicEdgeChain"`
	DefaultIngress       string   `json:"defaultIngress"`
	DeclaredServicesOnly bool     `json:"declaredServicesOnly"`
	BaseIngressRuleRefs  []string `json:"baseIngressRuleRefs"`
	StateDigest          string   `json:"stateDigest"`
}

type CloudHardeningPolicy struct {
	PolicyDigest             string `json:"policyDigest"`
	RequestDigest            string `json:"requestDigest"`
	EvaluatedAt              string `json:"evaluatedAt"`
	StackID                  string `json:"stackId"`
	SiteRef                  string `json:"siteRef"`
	NodeRef                  string `json:"nodeRef"`
	ExecutionChannelRef      string `json:"executionChannelRef"`
	Profile                  string `json:"profile"`
	TLSMinVersion            string `json:"tlsMinVersion"`
	SSHKeyOnly               bool   `json:"sshKeyOnly"`
	SSHRootLogin             string `json:"sshRootLogin"`
	BruteForceProtection     string `json:"bruteForceProtection"`
	AutomaticSecurityUpdates string `json:"automaticSecurityUpdates"`
	StateDigest              string `json:"stateDigest"`
}

type CloudHostSecurityApplyObservation struct {
	Operation           string `json:"operation"`
	PolicyDigest        string `json:"policyDigest"`
	RequestDigest       string `json:"requestDigest"`
	StackID             string `json:"stackId"`
	SiteRef             string `json:"siteRef"`
	NodeRef             string `json:"nodeRef"`
	ExecutionChannelRef string `json:"executionChannelRef"`
	EvaluatedAt         string `json:"evaluatedAt"`
	ObservedAt          string `json:"observedAt"`
	StateDigest         string `json:"stateDigest"`
	Status              string `json:"status"`
}

type CloudHostSecurityVerifyExpectation struct {
	StackID                  string   `json:"stackId"`
	SiteRef                  string   `json:"siteRef"`
	NodeRef                  string   `json:"nodeRef"`
	ExecutionChannelRef      string   `json:"executionChannelRef"`
	PolicyDigest             string   `json:"policyDigest"`
	RequestDigest            string   `json:"requestDigest"`
	ArtifactDigest           string   `json:"artifactDigest"`
	EvaluatedAt              string   `json:"evaluatedAt"`
	NetworkMode              string   `json:"networkMode"`
	TransportSubnet          string   `json:"transportSubnet"`
	IPv6                     bool     `json:"ipv6"`
	BaseRuleset              string   `json:"baseRuleset"`
	PublicEdgeChain          string   `json:"publicEdgeChain"`
	DefaultIngress           string   `json:"defaultIngress"`
	DeclaredServicesOnly     bool     `json:"declaredServicesOnly"`
	BaseIngressRuleRefs      []string `json:"baseIngressRuleRefs"`
	FirewallStateDigest      string   `json:"firewallStateDigest"`
	HardeningProfile         string   `json:"hardeningProfile"`
	TLSMinVersion            string   `json:"tlsMinVersion"`
	SSHKeyOnly               bool     `json:"sshKeyOnly"`
	SSHRootLogin             string   `json:"sshRootLogin"`
	BruteForceProtection     string   `json:"bruteForceProtection"`
	AutomaticSecurityUpdates string   `json:"automaticSecurityUpdates"`
	HardeningStateDigest     string   `json:"hardeningStateDigest"`
}

type CloudHostSecurityVerifyObservation struct {
	PolicyDigest               string   `json:"policyDigest"`
	RequestDigest              string   `json:"requestDigest"`
	StackID                    string   `json:"stackId"`
	SiteRef                    string   `json:"siteRef"`
	NodeRef                    string   `json:"nodeRef"`
	ExecutionChannelRef        string   `json:"executionChannelRef"`
	EvaluatedAt                string   `json:"evaluatedAt"`
	ObservedAt                 string   `json:"observedAt"`
	Status                     string   `json:"status"`
	FirewallStatus             string   `json:"firewallStatus"`
	FirewallStateDigest        string   `json:"firewallStateDigest"`
	NetworkMode                string   `json:"networkMode"`
	TransportSubnet            string   `json:"transportSubnet"`
	IPv6                       bool     `json:"ipv6"`
	BaseRuleset                string   `json:"baseRuleset"`
	PublicEdgeChain            string   `json:"publicEdgeChain"`
	DefaultIngress             string   `json:"defaultIngress"`
	DeclaredServicesOnly       bool     `json:"declaredServicesOnly"`
	BaseIngressRuleRefs        []string `json:"baseIngressRuleRefs"`
	UnauthorizedExceptionCount int      `json:"unauthorizedExceptionCount"`
	HardeningStatus            string   `json:"hardeningStatus"`
	HardeningStateDigest       string   `json:"hardeningStateDigest"`
	HardeningProfile           string   `json:"hardeningProfile"`
	TLSMinVersion              string   `json:"tlsMinVersion"`
	SSHKeyOnly                 bool     `json:"sshKeyOnly"`
	SSHRootLogin               string   `json:"sshRootLogin"`
	BruteForceProtection       string   `json:"bruteForceProtection"`
	AutomaticSecurityUpdates   string   `json:"automaticSecurityUpdates"`
}

type CloudHostSecurityEvidence struct {
	SchemaVersion     string                             `json:"schemaVersion"`
	RequestDigest     string                             `json:"requestDigest"`
	ArtifactDigest    string                             `json:"artifactDigest"`
	PolicyDigest      string                             `json:"policyDigest"`
	EvaluatedAt       string                             `json:"evaluatedAt"`
	FirewallApply     CloudHostSecurityApplyObservation  `json:"firewallApply"`
	FirewallReconcile CloudHostSecurityApplyObservation  `json:"firewallReconcile"`
	HardeningApply    CloudHostSecurityApplyObservation  `json:"hardeningApply"`
	Verify            CloudHostSecurityVerifyObservation `json:"verify"`
}

type CloudHostSecurityEvidenceReceipt struct {
	EvidenceDigest string `json:"evidenceDigest"`
	CommittedAt    string `json:"committedAt"`
}

// CloudHostSecurityOperations is implemented by the authenticated execution
// channel owner. It intentionally exposes neither generic shell execution nor
// provider, endpoint, credential, discovery, or server-lifecycle operations.
type CloudHostSecurityOperations interface {
	ApplyFirewall(context.Context, CloudFirewallPolicy) (CloudHostSecurityApplyObservation, error)
	ReconcileFirewall(context.Context, CloudFirewallPolicy) (CloudHostSecurityApplyObservation, error)
	ApplyHardening(context.Context, CloudHardeningPolicy) (CloudHostSecurityApplyObservation, error)
	VerifyHostSecurity(context.Context, CloudHostSecurityVerifyExpectation) (CloudHostSecurityVerifyObservation, error)
	CommitEvidence(context.Context, CloudHostSecurityEvidence) (CloudHostSecurityEvidenceReceipt, error)
}

// CloudHostSecurityAuthority is service-owned catalog authority selected at
// adapter registration. Request data can never define these hashes.
type CloudHostSecurityAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

// CloudHostSecurityExecutor applies the closed Cloud policy to one previously
// authorized node. Product registration requires a real authenticated host
// operations implementation and never discovers one from request data.
type CloudHostSecurityExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalTargetBinding
	authority  CloudHostSecurityAuthority
	operations CloudHostSecurityOperations
	clock      func() time.Time
}

func NewCloudHostSecurityExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority CloudHostSecurityAuthority, operations CloudHostSecurityOperations) *CloudHostSecurityExecutor {
	return NewCloudHostSecurityExecutorWithClock(identity, binding, authority, operations, time.Now)
}

func NewCloudHostSecurityExecutorWithClock(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority CloudHostSecurityAuthority, operations CloudHostSecurityOperations, now func() time.Time) *CloudHostSecurityExecutor {
	return &CloudHostSecurityExecutor{identity: identity, binding: binding, authority: authority, operations: operations, clock: now}
}

func (e *CloudHostSecurityExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *CloudHostSecurityExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud host-security executor requires a context")
	}
	if e == nil || e.operations == nil || e.clock == nil || strings.TrimSpace(e.binding.SiteRef) == "" || strings.TrimSpace(e.binding.NodeRef) == "" || strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) || !validCoreHostBootstrapDigest(e.authority.ModuleContractHash) || !validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud host-security executor requires one explicit authenticated Cloud target binding")
	}
	if err := request.Validate(); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate sealed Cloud host-security request: %w", err)
	}
	target, health, firewall, hardening, expectation, err := validateCloudHostSecurityRequest(request, e.binding, e.authority)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evaluatedAt := e.clock().UTC()
	if evaluatedAt.IsZero() {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud host-security executor clock returned zero time")
	}
	evaluatedAtText := evaluatedAt.Format(time.RFC3339Nano)
	firewall.RequestDigest, firewall.EvaluatedAt = request.RequestDigest, evaluatedAtText
	hardening.RequestDigest, hardening.EvaluatedAt = request.RequestDigest, evaluatedAtText
	expectation.RequestDigest, expectation.EvaluatedAt = request.RequestDigest, evaluatedAtText
	firewallObservation, err := e.operations.ApplyFirewall(ctx, defensiveCloudFirewallPolicy(firewall))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("apply exact Cloud host firewall: %w", err)
	}
	firewallObservedAt, err := validateCloudHostSecurityApplyObservation(firewallObservation, expectation, "apply-cloud-host-firewall", "applied", firewall.StateDigest, evaluatedAt, evaluatedAt, e.clock().UTC())
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	reconcileObservation, err := e.operations.ReconcileFirewall(ctx, defensiveCloudFirewallPolicy(firewall))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("reconcile exact Cloud host firewall: %w", err)
	}
	reconcileObservedAt, err := validateCloudHostSecurityApplyObservation(reconcileObservation, expectation, "reconcile-cloud-host-firewall", "reconciled", firewall.StateDigest, evaluatedAt, firewallObservedAt, e.clock().UTC())
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	hardeningObservation, err := e.operations.ApplyHardening(ctx, hardening)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("apply exact Cloud host hardening: %w", err)
	}
	hardeningObservedAt, err := validateCloudHostSecurityApplyObservation(hardeningObservation, expectation, "apply-cloud-host-hardening", "applied", hardening.StateDigest, evaluatedAt, reconcileObservedAt, e.clock().UTC())
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	verifyObservation, err := e.operations.VerifyHostSecurity(ctx, expectation)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify exact Cloud host security: %w", err)
	}
	verifyObservedAt, err := validateCloudHostSecurityVerifyObservation(verifyObservation, expectation, evaluatedAt, hardeningObservedAt, e.clock().UTC())
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evidenceRecord := CloudHostSecurityEvidence{
		SchemaVersion: "stackkit.cloud-host-security-evidence/v2", RequestDigest: request.RequestDigest,
		ArtifactDigest: expectation.ArtifactDigest, PolicyDigest: expectation.PolicyDigest, EvaluatedAt: evaluatedAtText,
		FirewallApply: firewallObservation, FirewallReconcile: reconcileObservation, HardeningApply: hardeningObservation, Verify: verifyObservation,
	}
	evidence, err := json.Marshal(evidenceRecord)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal Cloud host-security evidence: %w", err)
	}
	digest := sha256.Sum256(evidence)
	digestString := "sha256:" + hex.EncodeToString(digest[:])
	receipt, err := e.operations.CommitEvidence(ctx, cloneCloudHostSecurityEvidence(evidenceRecord))
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("commit exact Cloud host-security evidence: %w", err)
	}
	if _, err := validateCloudHostSecurityEvidenceReceipt(receipt, digestString, evaluatedAt, verifyObservedAt, e.clock().UTC()); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied, ObservationRef: "runtime-observation://cloud-host-security/" + strings.TrimPrefix(digestString, "sha256:"), ObservationDigest: digestString}},
		Health:  []runtimeexecutor.HealthOutcome{{RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy, ObservationRef: "health-observation://cloud-host-security/" + strings.TrimPrefix(digestString, "sha256:"), ObservationDigest: digestString}},
	}, nil
}

func defensiveCloudFirewallPolicy(policy CloudFirewallPolicy) CloudFirewallPolicy {
	policy.Roles = append([]string(nil), policy.Roles...)
	policy.BaseIngressRuleRefs = append([]string(nil), policy.BaseIngressRuleRefs...)
	return policy
}

func cloneCloudHostSecurityEvidence(evidence CloudHostSecurityEvidence) CloudHostSecurityEvidence {
	evidence.Verify.BaseIngressRuleRefs = append([]string(nil), evidence.Verify.BaseIngressRuleRefs...)
	return evidence
}

func validateCloudHostSecurityRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority CloudHostSecurityAuthority) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, CloudFirewallPolicy, CloudHardeningPolicy, CloudHostSecurityVerifyExpectation, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if !validCoreHostBootstrapDigest(request.RequestDigest) || len(request.RuntimeTargets) != 1 || len(request.HealthTargets) != 1 || len(request.AccessBindings) != 0 || len(request.Artifacts) != 1 {
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
	wantRequirementID := cloudHostSecurityModuleRef + "/" + cloudHostSecurityUnitRef + "/" + wantInstance
	if target.RequirementID != wantRequirementID || target.InstanceRef != wantInstance || target.ArtifactRefs[0] != wantArtifactID {
		return emptyTarget, emptyHealth, CloudFirewallPolicy{}, CloudHardeningPolicy{}, CloudHostSecurityVerifyExpectation{}, errors.New("runtime target does not bind the exact node-local Cloud host-security artifact")
	}
	health := request.HealthTargets[0]
	wantHealthRequirementID := "module-" + cloudHostSecurityModuleRef + "-" + cloudHostSecurityHealthSourceRef + "-node-" + binding.NodeRef
	if health.RequirementID != wantHealthRequirementID ||
		health.SourceRef != cloudHostSecurityHealthSourceRef || health.ContractHash != authority.HealthContractHash || health.Phase != "post-apply" || health.Kind != "contract" || health.TargetKind != "module" ||
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
		ArtifactDigest       string `json:"artifactDigest"`
		RequestDigest        string `json:"requestDigest"`
		ProviderContractHash string `json:"providerContractHash"`
		ModuleContractHash   string `json:"moduleContractHash"`
		HealthContractHash   string `json:"healthContractHash"`
		SiteRef              string `json:"siteRef"`
		NodeRef              string `json:"nodeRef"`
		ExecutionChannelRef  string `json:"executionChannelRef"`
	}{artifact.Digest, request.RequestDigest, authority.ProviderContractHash, authority.ModuleContractHash, authority.HealthContractHash, binding.SiteRef, binding.NodeRef, binding.ExecutionChannelRef})
	if err != nil {
		return emptyTarget, emptyHealth, CloudFirewallPolicy{}, CloudHardeningPolicy{}, CloudHostSecurityVerifyExpectation{}, fmt.Errorf("bind Cloud host-security policy: %w", err)
	}
	policyDigestBytes := sha256.Sum256(policyDigestInput)
	policyDigest := "sha256:" + hex.EncodeToString(policyDigestBytes[:])
	firewall := CloudFirewallPolicy{
		PolicyDigest: policyDigest, StackID: policy.StackID, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, ExecutionChannelRef: binding.ExecutionChannelRef,
		Roles: append([]string(nil), policy.Roles...), NetworkMode: policy.NetworkMode, TransportSubnet: policy.TransportSubnet, IPv6: policy.IPv6,
		BaseRuleset: cloudHostFirewallRulesetRef(binding.SiteRef, binding.NodeRef), PublicEdgeChain: cloudPublicEdgeChainRef(binding.SiteRef, binding.NodeRef),
		DefaultIngress: policy.Firewall.DefaultIngress, DeclaredServicesOnly: policy.Firewall.DeclaredServiceIngressOnly,
		BaseIngressRuleRefs: []string{},
	}
	firewall.StateDigest, err = digestCloudHostSecurityState(struct {
		StackID              string   `json:"stackId"`
		SiteRef              string   `json:"siteRef"`
		NodeRef              string   `json:"nodeRef"`
		NetworkMode          string   `json:"networkMode"`
		TransportSubnet      string   `json:"transportSubnet"`
		IPv6                 bool     `json:"ipv6"`
		BaseRuleset          string   `json:"baseRuleset"`
		PublicEdgeChain      string   `json:"publicEdgeChain"`
		DefaultIngress       string   `json:"defaultIngress"`
		DeclaredServicesOnly bool     `json:"declaredServicesOnly"`
		BaseIngressRuleRefs  []string `json:"baseIngressRuleRefs"`
	}{firewall.StackID, firewall.SiteRef, firewall.NodeRef, firewall.NetworkMode, firewall.TransportSubnet, firewall.IPv6, firewall.BaseRuleset, firewall.PublicEdgeChain, firewall.DefaultIngress, firewall.DeclaredServicesOnly, firewall.BaseIngressRuleRefs})
	if err != nil {
		return emptyTarget, emptyHealth, CloudFirewallPolicy{}, CloudHardeningPolicy{}, CloudHostSecurityVerifyExpectation{}, err
	}
	hardening := CloudHardeningPolicy{
		PolicyDigest: policyDigest, StackID: policy.StackID, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, ExecutionChannelRef: binding.ExecutionChannelRef,
		Profile: policy.Hardening.Profile, TLSMinVersion: policy.TLSMinVersion,
		SSHKeyOnly: policy.Hardening.SSHKeyOnly, SSHRootLogin: policy.Hardening.SSHRootLogin,
		BruteForceProtection: policy.Hardening.BruteForceProtection, AutomaticSecurityUpdates: policy.Hardening.AutomaticSecurityUpdates,
	}
	hardening.StateDigest, err = digestCloudHostSecurityState(struct {
		StackID                  string `json:"stackId"`
		SiteRef                  string `json:"siteRef"`
		NodeRef                  string `json:"nodeRef"`
		Profile                  string `json:"profile"`
		TLSMinVersion            string `json:"tlsMinVersion"`
		SSHKeyOnly               bool   `json:"sshKeyOnly"`
		SSHRootLogin             string `json:"sshRootLogin"`
		BruteForceProtection     string `json:"bruteForceProtection"`
		AutomaticSecurityUpdates string `json:"automaticSecurityUpdates"`
	}{hardening.StackID, hardening.SiteRef, hardening.NodeRef, hardening.Profile, hardening.TLSMinVersion, hardening.SSHKeyOnly, hardening.SSHRootLogin, hardening.BruteForceProtection, hardening.AutomaticSecurityUpdates})
	if err != nil {
		return emptyTarget, emptyHealth, CloudFirewallPolicy{}, CloudHardeningPolicy{}, CloudHostSecurityVerifyExpectation{}, err
	}
	expectation := CloudHostSecurityVerifyExpectation{
		StackID: policy.StackID, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, ExecutionChannelRef: binding.ExecutionChannelRef, PolicyDigest: policyDigest,
		ArtifactDigest: artifact.Digest,
		NetworkMode:    policy.NetworkMode, TransportSubnet: policy.TransportSubnet, IPv6: policy.IPv6,
		BaseRuleset: firewall.BaseRuleset, PublicEdgeChain: firewall.PublicEdgeChain, DefaultIngress: firewall.DefaultIngress,
		DeclaredServicesOnly: firewall.DeclaredServicesOnly, BaseIngressRuleRefs: append([]string(nil), firewall.BaseIngressRuleRefs...), FirewallStateDigest: firewall.StateDigest,
		HardeningProfile: hardening.Profile, TLSMinVersion: hardening.TLSMinVersion, HardeningStateDigest: hardening.StateDigest,
		SSHKeyOnly: hardening.SSHKeyOnly, SSHRootLogin: hardening.SSHRootLogin,
		BruteForceProtection: hardening.BruteForceProtection, AutomaticSecurityUpdates: hardening.AutomaticSecurityUpdates,
	}
	return target, health, firewall, hardening, expectation, nil
}

func digestCloudHostSecurityState(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal Cloud host-security state: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateCloudHostSecurityApplyObservation(observation CloudHostSecurityApplyObservation, expectation CloudHostSecurityVerifyExpectation, operation, status, stateDigest string, evaluatedAt, notBefore, checkedAt time.Time) (time.Time, error) {
	if observation.Operation != operation || observation.PolicyDigest != expectation.PolicyDigest || observation.RequestDigest != expectation.RequestDigest ||
		observation.StackID != expectation.StackID || observation.SiteRef != expectation.SiteRef || observation.NodeRef != expectation.NodeRef ||
		observation.ExecutionChannelRef != expectation.ExecutionChannelRef || observation.EvaluatedAt != expectation.EvaluatedAt ||
		observation.StateDigest != stateDigest || observation.Status != status {
		return time.Time{}, fmt.Errorf("%s observation does not prove the exact applied Cloud host policy", operation)
	}
	observedAt, err := validateCloudHostSecurityObservationTime(observation.ObservedAt, evaluatedAt, notBefore, checkedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s observation is not fresh: %w", operation, err)
	}
	return observedAt, nil
}

func validateCloudHostSecurityVerifyObservation(observation CloudHostSecurityVerifyObservation, expectation CloudHostSecurityVerifyExpectation, evaluatedAt, notBefore, checkedAt time.Time) (time.Time, error) {
	if observation.PolicyDigest != expectation.PolicyDigest || observation.RequestDigest != expectation.RequestDigest ||
		observation.StackID != expectation.StackID || observation.SiteRef != expectation.SiteRef || observation.NodeRef != expectation.NodeRef ||
		observation.ExecutionChannelRef != expectation.ExecutionChannelRef || observation.EvaluatedAt != expectation.EvaluatedAt ||
		observation.Status != "ready" || observation.FirewallStatus != "enforced" || observation.FirewallStateDigest != expectation.FirewallStateDigest ||
		observation.NetworkMode != expectation.NetworkMode || observation.TransportSubnet != expectation.TransportSubnet || observation.IPv6 != expectation.IPv6 ||
		observation.BaseRuleset != expectation.BaseRuleset || observation.PublicEdgeChain != expectation.PublicEdgeChain ||
		observation.DefaultIngress != expectation.DefaultIngress || observation.DeclaredServicesOnly != expectation.DeclaredServicesOnly ||
		!slices.Equal(observation.BaseIngressRuleRefs, expectation.BaseIngressRuleRefs) || observation.UnauthorizedExceptionCount != 0 ||
		observation.HardeningStatus != "enforced" || observation.HardeningStateDigest != expectation.HardeningStateDigest ||
		observation.HardeningProfile != expectation.HardeningProfile || observation.TLSMinVersion != expectation.TLSMinVersion ||
		observation.SSHKeyOnly != expectation.SSHKeyOnly ||
		observation.SSHRootLogin != expectation.SSHRootLogin ||
		observation.BruteForceProtection != expectation.BruteForceProtection ||
		observation.AutomaticSecurityUpdates != expectation.AutomaticSecurityUpdates {
		return time.Time{}, errors.New("verification does not prove the exact enforced Cloud host policy")
	}
	observedAt, err := validateCloudHostSecurityObservationTime(observation.ObservedAt, evaluatedAt, notBefore, checkedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("Cloud host-security verification is not fresh: %w", err)
	}
	return observedAt, nil
}

func validateCloudHostSecurityEvidenceReceipt(receipt CloudHostSecurityEvidenceReceipt, digest string, evaluatedAt, notBefore, checkedAt time.Time) (time.Time, error) {
	if receipt.EvidenceDigest != digest {
		return time.Time{}, errors.New("Cloud host-security evidence custody did not commit the exact evidence digest")
	}
	committedAt, err := validateCloudHostSecurityObservationTime(receipt.CommittedAt, evaluatedAt, notBefore, checkedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("Cloud host-security evidence custody receipt is not fresh: %w", err)
	}
	return committedAt, nil
}

func validateCloudHostSecurityObservationTime(raw string, evaluatedAt, notBefore, checkedAt time.Time) (time.Time, error) {
	if evaluatedAt.IsZero() || notBefore.IsZero() || checkedAt.IsZero() || notBefore.Before(evaluatedAt) || checkedAt.Before(notBefore) {
		return time.Time{}, errors.New("executor clock moved backwards or returned zero time")
	}
	observedAt, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || observedAt.Location() != time.UTC || observedAt.Format(time.RFC3339Nano) != raw ||
		observedAt.Before(notBefore) || observedAt.After(checkedAt) || checkedAt.Sub(observedAt) > cloudHostSecurityMaxObservationAge {
		return time.Time{}, errors.New("observation timestamp is not canonical UTC within the invocation freshness window")
	}
	return observedAt, nil
}

var _ runtimeexecutor.Executor = (*CloudHostSecurityExecutor)(nil)
