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
	cloudOffsiteBackupProviderRef       = "stackkits-cloud-offsite-backup"
	cloudOffsiteBackupModuleRef         = "stackkits-cloud-offsite-backup-runtime"
	cloudOffsiteBackupUnitRef           = "executor-contract"
	cloudOffsiteBackupOutputRef         = "cloud/backup/executor-contract.json"
	cloudOffsiteBackupArtifactPrefix    = "cloud-offsite-backup-executor-contract-instance-"
	cloudOffsiteBackupHealthSourceRef   = "cloud-offsite-backup-health"
	cloudOffsiteBackupMaxArtifactBytes  = 512 << 10
	cloudOffsiteBackupMaxObservationAge = 5 * time.Minute
)

type CloudOffsiteBackupApplyPolicy struct {
	PolicyDigest           string `json:"policyDigest"`
	RequestDigest          string `json:"requestDigest"`
	ArtifactDigest         string `json:"artifactDigest"`
	StateDigest            string `json:"stateDigest"`
	EvaluatedAt            string `json:"evaluatedAt"`
	StackID                string `json:"stackId"`
	SiteRef                string `json:"siteRef"`
	NodeRef                string `json:"nodeRef"`
	ExecutionChannelRef    string `json:"executionChannelRef"`
	CapabilityRef          string `json:"capabilityRef"`
	ContractOwnerRef       string `json:"contractOwnerRef"`
	CapabilityContractHash string `json:"capabilityContractHash"`
	RequirementsHash       string `json:"requirementsHash"`
	BindingRef             string `json:"bindingRef"`
	BindingHash            string `json:"bindingHash"`
	BackupTargetRef        string `json:"backupTargetRef"`
	CustodyAttestationRef  string `json:"custodyAttestationRef"`
	StackKitsVersion       string `json:"stackkitsVersion"`
	CandidateDigest        string `json:"candidateDigest"`
	SpecHash               string `json:"specHash"`
	ValidUntil             string `json:"validUntil"`
}

type CloudOffsiteBackupExpectation struct {
	PolicyDigest          string `json:"policyDigest"`
	RequestDigest         string `json:"requestDigest"`
	ArtifactDigest        string `json:"artifactDigest"`
	StateDigest           string `json:"stateDigest"`
	EvaluatedAt           string `json:"evaluatedAt"`
	StackID               string `json:"stackId"`
	SiteRef               string `json:"siteRef"`
	NodeRef               string `json:"nodeRef"`
	ExecutionChannelRef   string `json:"executionChannelRef"`
	BindingRef            string `json:"bindingRef"`
	BindingHash           string `json:"bindingHash"`
	BackupTargetRef       string `json:"backupTargetRef"`
	CustodyAttestationRef string `json:"custodyAttestationRef"`
	ValidUntil            string `json:"validUntil"`
}

type CloudOffsiteBackupObservation struct {
	Operation               string `json:"operation"`
	PolicyDigest            string `json:"policyDigest"`
	RequestDigest           string `json:"requestDigest"`
	ArtifactDigest          string `json:"artifactDigest"`
	StateDigest             string `json:"stateDigest"`
	EvaluatedAt             string `json:"evaluatedAt"`
	ObservedAt              string `json:"observedAt"`
	StackID                 string `json:"stackId"`
	SiteRef                 string `json:"siteRef"`
	NodeRef                 string `json:"nodeRef"`
	ExecutionChannelRef     string `json:"executionChannelRef"`
	BindingRef              string `json:"bindingRef"`
	BindingHash             string `json:"bindingHash"`
	BackupTargetRef         string `json:"backupTargetRef"`
	CustodyAttestationRef   string `json:"custodyAttestationRef"`
	Status                  string `json:"status"`
	ObsoleteBindings        int    `json:"obsoleteBindings"`
	BackupObservationRef    string `json:"backupObservationRef,omitempty"`
	BackupObservationDigest string `json:"backupObservationDigest,omitempty"`
	RestoreReadbackRef      string `json:"restoreReadbackRef,omitempty"`
	RestoreReadbackDigest   string `json:"restoreReadbackDigest,omitempty"`
}

type CloudOffsiteBackupEvidence struct {
	SchemaVersion  string                        `json:"schemaVersion"`
	RequestDigest  string                        `json:"requestDigest"`
	ArtifactDigest string                        `json:"artifactDigest"`
	PolicyDigest   string                        `json:"policyDigest"`
	StateDigest    string                        `json:"stateDigest"`
	EvaluatedAt    string                        `json:"evaluatedAt"`
	Apply          CloudOffsiteBackupObservation `json:"apply"`
	Reconcile      CloudOffsiteBackupObservation `json:"reconcile"`
	Verify         CloudOffsiteBackupObservation `json:"verify"`
}

type CloudOffsiteBackupEvidenceReceipt struct {
	EvidenceDigest string `json:"evidenceDigest"`
	CommittedAt    string `json:"committedAt"`
}

// CloudOffsiteBackupOperations is implemented by an authenticated Cloud host
// channel. It owns target access and backup tooling; StackKits supplies only
// the exact opaque target/custody policy and verifies returned evidence.
type CloudOffsiteBackupOperations interface {
	BindOffsiteBackupTarget(context.Context, CloudOffsiteBackupApplyPolicy) (CloudOffsiteBackupObservation, error)
	RemoveObsoleteOffsiteBackupBindings(context.Context, CloudOffsiteBackupExpectation) (CloudOffsiteBackupObservation, error)
	VerifyOffsiteBackupTarget(context.Context, CloudOffsiteBackupExpectation) (CloudOffsiteBackupObservation, error)
	CommitCloudOffsiteBackupEvidence(context.Context, CloudOffsiteBackupEvidence) (CloudOffsiteBackupEvidenceReceipt, error)
}

type CloudOffsiteBackupAuthority struct {
	ProviderContractHash string
	ModuleContractHash   string
	HealthContractHash   string
}

type CloudOffsiteBackupExecutor struct {
	identity   runtimeexecutor.ExecutorIdentity
	binding    LocalTargetBinding
	authority  CloudOffsiteBackupAuthority
	operations CloudOffsiteBackupOperations
	clock      func() time.Time
}

func NewCloudOffsiteBackupExecutor(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority CloudOffsiteBackupAuthority, operations CloudOffsiteBackupOperations) *CloudOffsiteBackupExecutor {
	return NewCloudOffsiteBackupExecutorWithClock(identity, binding, authority, operations, time.Now)
}

func NewCloudOffsiteBackupExecutorWithClock(identity runtimeexecutor.ExecutorIdentity, binding LocalTargetBinding, authority CloudOffsiteBackupAuthority, operations CloudOffsiteBackupOperations, now func() time.Time) *CloudOffsiteBackupExecutor {
	return &CloudOffsiteBackupExecutor{identity: identity, binding: binding, authority: authority, operations: operations, clock: now}
}

func (e *CloudOffsiteBackupExecutor) Identity() runtimeexecutor.ExecutorIdentity { return e.identity }

func (e *CloudOffsiteBackupExecutor) Execute(ctx context.Context, request runtimeexecutor.ExecutionRequest) (runtimeexecutor.ExecutionOutcome, error) {
	if ctx == nil {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud offsite-backup executor requires a context")
	}
	if e == nil || e.operations == nil || e.clock == nil || strings.TrimSpace(e.binding.SiteRef) == "" ||
		strings.TrimSpace(e.binding.NodeRef) == "" || strings.TrimSpace(e.binding.ExecutionChannelRef) == "" ||
		!validCoreHostBootstrapDigest(e.authority.ProviderContractHash) ||
		!validCoreHostBootstrapDigest(e.authority.ModuleContractHash) ||
		!validCoreHostBootstrapDigest(e.authority.HealthContractHash) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud offsite-backup executor requires one explicit authenticated Cloud target binding")
	}
	if err := request.Validate(); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("validate sealed Cloud offsite-backup request: %w", err)
	}
	target, health, policy, expectation, err := validateCloudOffsiteBackupRequest(request, e.binding, e.authority)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evaluatedAt := e.clock().UTC()
	if evaluatedAt.IsZero() {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud offsite-backup executor clock returned zero time")
	}
	validUntil, err := time.Parse(time.RFC3339Nano, policy.ValidUntil)
	if err != nil || !evaluatedAt.Before(validUntil) {
		return runtimeexecutor.ExecutionOutcome{}, errors.New("Cloud offsite-backup binding expired before operations")
	}
	policy.RequestDigest, policy.EvaluatedAt = request.RequestDigest, evaluatedAt.Format(time.RFC3339Nano)
	expectation.RequestDigest, expectation.EvaluatedAt = request.RequestDigest, policy.EvaluatedAt

	apply, err := e.operations.BindOffsiteBackupTarget(ctx, policy)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("bind exact Cloud offsite-backup target: %w", err)
	}
	applyAt, err := validateCloudOffsiteBackupObservation(apply, expectation, "bind-offsite-backup-target", "bound", false, evaluatedAt, evaluatedAt, e.clock().UTC())
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	reconcile, err := e.operations.RemoveObsoleteOffsiteBackupBindings(ctx, expectation)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("remove obsolete Cloud offsite-backup bindings: %w", err)
	}
	reconcileAt, err := validateCloudOffsiteBackupObservation(reconcile, expectation, "remove-obsolete-offsite-backup-binding", "reconciled", false, evaluatedAt, applyAt, e.clock().UTC())
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	verify, err := e.operations.VerifyOffsiteBackupTarget(ctx, expectation)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("verify exact Cloud offsite-backup target: %w", err)
	}
	verifyAt, err := validateCloudOffsiteBackupObservation(verify, expectation, "verify-offsite-backup-target", "ready", true, evaluatedAt, reconcileAt, e.clock().UTC())
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	evidenceRecord := CloudOffsiteBackupEvidence{
		SchemaVersion: "stackkit.cloud-offsite-backup-evidence/v1", RequestDigest: request.RequestDigest,
		ArtifactDigest: expectation.ArtifactDigest, PolicyDigest: expectation.PolicyDigest, StateDigest: expectation.StateDigest,
		EvaluatedAt: expectation.EvaluatedAt, Apply: apply, Reconcile: reconcile, Verify: verify,
	}
	evidence, err := json.Marshal(evidenceRecord)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("marshal Cloud offsite-backup evidence: %w", err)
	}
	sum := sha256.Sum256(evidence)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	receipt, err := e.operations.CommitCloudOffsiteBackupEvidence(ctx, evidenceRecord)
	if err != nil {
		return runtimeexecutor.ExecutionOutcome{}, fmt.Errorf("commit exact Cloud offsite-backup evidence: %w", err)
	}
	if err := validateCloudOffsiteBackupReceipt(receipt, digest, evaluatedAt, verifyAt, e.clock().UTC()); err != nil {
		return runtimeexecutor.ExecutionOutcome{}, err
	}
	ref := strings.TrimPrefix(digest, "sha256:")
	return runtimeexecutor.ExecutionOutcome{
		Runtime: []runtimeexecutor.RuntimeOutcome{{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef, Status: runtimeexecutor.RuntimeStatusApplied, ObservationRef: "runtime-observation://cloud-offsite-backup/" + ref, ObservationDigest: digest}},
		Health:  []runtimeexecutor.HealthOutcome{{RequirementID: health.RequirementID, TargetRef: health.TargetRef, Status: runtimeexecutor.HealthStatusHealthy, ObservationRef: "health-observation://cloud-offsite-backup/" + ref, ObservationDigest: digest}},
	}, nil
}

func validateCloudOffsiteBackupRequest(request runtimeexecutor.ExecutionRequest, binding LocalTargetBinding, authority CloudOffsiteBackupAuthority) (runtimeexecutor.RuntimeTarget, runtimeexecutor.HealthTarget, CloudOffsiteBackupApplyPolicy, CloudOffsiteBackupExpectation, error) {
	emptyTarget, emptyHealth := runtimeexecutor.RuntimeTarget{}, runtimeexecutor.HealthTarget{}
	if !validCoreHostBootstrapDigest(request.RequestDigest) || len(request.RuntimeTargets) != 1 ||
		len(request.HealthTargets) != 1 || len(request.AccessBindings) != 0 ||
		len(request.BackupTargetBindings) != 1 || len(request.Artifacts) != 1 {
		return emptyTarget, emptyHealth, CloudOffsiteBackupApplyPolicy{}, CloudOffsiteBackupExpectation{}, errors.New("Cloud offsite-backup executor requires exactly one runtime, health target, backup-target binding, and artifact")
	}
	target := request.RuntimeTargets[0]
	contract := architecturev2renderer.CloudOffsiteBackupExecutorBundleRendererContract()
	if target.OwnerKind != "module" || target.OwnerRef != cloudOffsiteBackupModuleRef ||
		target.ProviderRef != cloudOffsiteBackupProviderRef || target.ProviderContractHash != authority.ProviderContractHash ||
		target.ModuleRef != cloudOffsiteBackupModuleRef || target.ModuleContractHash != authority.ModuleContractHash ||
		target.OwnerContractHash != authority.ModuleContractHash || target.UnitRef != cloudOffsiteBackupUnitRef ||
		target.UnitContractHash != contract.ContractHash || target.RuntimeKind != "host" || target.RuntimeDelivery != "stackkit" ||
		target.RuntimeEngine != "" || target.WorkloadRef != "" || target.ImageRef != "" ||
		len(target.DaemonBindings) != 0 || len(target.AccessCapabilities) != 0 || len(target.AccessBindingRefs) != 0 ||
		!slices.Equal(target.SiteRefs, []string{binding.SiteRef}) || !slices.Equal(target.NodeRefs, []string{binding.NodeRef}) ||
		target.ExecutionChannelRef != binding.ExecutionChannelRef || len(target.BackupTargetBindingRefs) != 1 ||
		len(target.BackupTargetCapabilities) != 1 || target.BackupTargetCapabilities[0].Ref != "offsite-object-backup" ||
		len(target.ArtifactRefs) != 1 {
		return emptyTarget, emptyHealth, CloudOffsiteBackupApplyPolicy{}, CloudOffsiteBackupExpectation{}, errors.New("runtime target is not the exact bound Cloud offsite-backup contract")
	}
	wantInstance := cloudOffsiteBackupUnitRef + "-node-" + binding.NodeRef
	wantArtifactID := cloudOffsiteBackupArtifactPrefix + wantInstance
	wantRequirementID := cloudOffsiteBackupModuleRef + "/" + cloudOffsiteBackupUnitRef + "/" + wantInstance
	if target.RequirementID != wantRequirementID || target.InstanceRef != wantInstance || target.ArtifactRefs[0] != wantArtifactID {
		return emptyTarget, emptyHealth, CloudOffsiteBackupApplyPolicy{}, CloudOffsiteBackupExpectation{}, errors.New("runtime target does not bind the exact node-local Cloud offsite-backup artifact")
	}
	external := request.BackupTargetBindings[0]
	if external.ID != target.BackupTargetBindingRefs[0] || external.RuntimeRequirementID != target.RequirementID ||
		external.Kind != "backup-target" || external.SiteRef != binding.SiteRef ||
		!slices.Equal(external.TargetNodeRefs, []string{binding.NodeRef}) ||
		external.CapabilityRef != target.BackupTargetCapabilities[0].Ref ||
		external.CapabilityContractHash != target.BackupTargetCapabilities[0].ContractHash ||
		external.ContractOwnerRef != target.ProviderRef {
		return emptyTarget, emptyHealth, CloudOffsiteBackupApplyPolicy{}, CloudOffsiteBackupExpectation{}, errors.New("backup-target binding does not match the exact runtime authority")
	}
	health := request.HealthTargets[0]
	wantHealthID := "module-" + cloudOffsiteBackupModuleRef + "-" + cloudOffsiteBackupHealthSourceRef + "-node-" + binding.NodeRef
	if health.RequirementID != wantHealthID || health.SourceRef != cloudOffsiteBackupHealthSourceRef ||
		health.ContractHash != authority.HealthContractHash || health.Phase != "post-apply" ||
		health.Kind != "contract" || health.TargetKind != "module" || health.TargetRef != cloudOffsiteBackupModuleRef ||
		!slices.Equal(health.SiteRefs, target.SiteRefs) || !slices.Equal(health.NodeRefs, target.NodeRefs) {
		return emptyTarget, emptyHealth, CloudOffsiteBackupApplyPolicy{}, CloudOffsiteBackupExpectation{}, errors.New("health target is not the exact Cloud offsite-backup postcondition")
	}
	artifact := request.Artifacts[0]
	if artifact.ID != wantArtifactID || artifact.Kind != "native-config" || artifact.Format != "json" ||
		artifact.Mode != "0640" || artifact.OwnerKind != "render-instance" || artifact.OwnerRef != wantInstance ||
		artifact.OwnerContractHash != target.UnitContractHash || artifact.ProviderRef != target.ProviderRef ||
		artifact.ProviderContractHash != target.ProviderContractHash || artifact.ModuleRef != target.ModuleRef ||
		artifact.ModuleContractHash != target.ModuleContractHash || artifact.UnitRef != target.UnitRef ||
		artifact.UnitContractHash != target.UnitContractHash || artifact.InstanceRef != wantInstance ||
		artifact.OutputRef != cloudOffsiteBackupOutputRef || !slices.Equal(artifact.SiteRefs, target.SiteRefs) ||
		!slices.Equal(artifact.NodeRefs, target.NodeRefs) || len(artifact.Content) == 0 ||
		len(artifact.Content) > cloudOffsiteBackupMaxArtifactBytes {
		return emptyTarget, emptyHealth, CloudOffsiteBackupApplyPolicy{}, CloudOffsiteBackupExpectation{}, errors.New("artifact is not the exact CUE-owned Cloud offsite-backup instance")
	}
	sum := sha256.Sum256(artifact.Content)
	if artifact.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return emptyTarget, emptyHealth, CloudOffsiteBackupApplyPolicy{}, CloudOffsiteBackupExpectation{}, errors.New("Cloud offsite-backup artifact digest does not match its immutable content")
	}
	governed, err := architecturev2renderer.ValidateCloudOffsiteBackupExecutorArtifact(artifact.Content, binding.SiteRef, binding.NodeRef)
	if err != nil {
		return emptyTarget, emptyHealth, CloudOffsiteBackupApplyPolicy{}, CloudOffsiteBackupExpectation{}, fmt.Errorf("validate governed Cloud offsite-backup policy: %w", err)
	}
	if governed.StackID != external.StackID || governed.CapabilityRef != external.CapabilityRef ||
		governed.ContractOwnerRef != external.ContractOwnerRef ||
		governed.CapabilityContractHash != external.CapabilityContractHash ||
		governed.RequirementsHash != external.RequirementsHash || governed.BindingRef != external.BindingRef ||
		governed.BindingHash != external.BindingHash || governed.BackupTargetRef != external.BackupTargetRef ||
		governed.CustodyAttestationRef != external.CustodyAttestationRef ||
		governed.StackKitsVersion != external.StackKitsVersion || governed.CandidateDigest != external.CandidateDigest ||
		governed.SpecHash != external.SpecHash || governed.IssuedAt != external.IssuedAt ||
		governed.ValidUntil != external.ValidUntil {
		return emptyTarget, emptyHealth, CloudOffsiteBackupApplyPolicy{}, CloudOffsiteBackupExpectation{}, errors.New("artifact and shared backup-target projection differ")
	}
	policyDigest, err := digestCloudOffsiteBackup(struct {
		ArtifactDigest, RequestDigest, ProviderContractHash, ModuleContractHash, HealthContractHash string
		ProjectionHash, ExecutionChannelRef                                                         string
	}{artifact.Digest, request.RequestDigest, authority.ProviderContractHash, authority.ModuleContractHash, authority.HealthContractHash, external.ProjectionHash, binding.ExecutionChannelRef})
	if err != nil {
		return emptyTarget, emptyHealth, CloudOffsiteBackupApplyPolicy{}, CloudOffsiteBackupExpectation{}, err
	}
	stateDigest, err := digestCloudOffsiteBackup(struct {
		StackID, SiteRef, NodeRef, BindingRef, BindingHash, BackupTargetRef, CustodyAttestationRef string
	}{governed.StackID, binding.SiteRef, binding.NodeRef, governed.BindingRef, governed.BindingHash, governed.BackupTargetRef, governed.CustodyAttestationRef})
	if err != nil {
		return emptyTarget, emptyHealth, CloudOffsiteBackupApplyPolicy{}, CloudOffsiteBackupExpectation{}, err
	}
	policy := CloudOffsiteBackupApplyPolicy{
		PolicyDigest: policyDigest, ArtifactDigest: artifact.Digest, StateDigest: stateDigest,
		StackID: governed.StackID, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef,
		ExecutionChannelRef: binding.ExecutionChannelRef, CapabilityRef: governed.CapabilityRef,
		ContractOwnerRef: governed.ContractOwnerRef, CapabilityContractHash: governed.CapabilityContractHash,
		RequirementsHash: governed.RequirementsHash, BindingRef: governed.BindingRef, BindingHash: governed.BindingHash,
		BackupTargetRef: governed.BackupTargetRef, CustodyAttestationRef: governed.CustodyAttestationRef,
		StackKitsVersion: governed.StackKitsVersion, CandidateDigest: governed.CandidateDigest,
		SpecHash: governed.SpecHash, ValidUntil: governed.ValidUntil,
	}
	expectation := CloudOffsiteBackupExpectation{
		PolicyDigest: policyDigest, ArtifactDigest: artifact.Digest, StateDigest: stateDigest,
		StackID: governed.StackID, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef,
		ExecutionChannelRef: binding.ExecutionChannelRef, BindingRef: governed.BindingRef,
		BindingHash: governed.BindingHash, BackupTargetRef: governed.BackupTargetRef,
		CustodyAttestationRef: governed.CustodyAttestationRef, ValidUntil: governed.ValidUntil,
	}
	return target, health, policy, expectation, nil
}

func validateCloudOffsiteBackupObservation(observation CloudOffsiteBackupObservation, expectation CloudOffsiteBackupExpectation, operation, status string, requireBackupProof bool, evaluatedAt, notBefore, now time.Time) (time.Time, error) {
	if observation.Operation != operation || observation.PolicyDigest != expectation.PolicyDigest ||
		observation.RequestDigest != expectation.RequestDigest || observation.ArtifactDigest != expectation.ArtifactDigest ||
		observation.StateDigest != expectation.StateDigest || observation.EvaluatedAt != expectation.EvaluatedAt ||
		observation.StackID != expectation.StackID || observation.SiteRef != expectation.SiteRef ||
		observation.NodeRef != expectation.NodeRef || observation.ExecutionChannelRef != expectation.ExecutionChannelRef ||
		observation.BindingRef != expectation.BindingRef || observation.BindingHash != expectation.BindingHash ||
		observation.BackupTargetRef != expectation.BackupTargetRef ||
		observation.CustodyAttestationRef != expectation.CustodyAttestationRef ||
		observation.Status != status || observation.ObsoleteBindings != 0 {
		return time.Time{}, fmt.Errorf("%s observation does not prove the exact Cloud offsite-backup state", operation)
	}
	if requireBackupProof {
		if !opaqueEvidenceRef(observation.BackupObservationRef, "backup-observation") {
			return time.Time{}, errors.New("Cloud offsite-backup verification lacks an opaque backup observation ref")
		}
		if !validCoreHostBootstrapDigest(observation.BackupObservationDigest) {
			return time.Time{}, errors.New("Cloud offsite-backup verification lacks a backup observation digest")
		}
		if !opaqueEvidenceRef(observation.RestoreReadbackRef, "restore-readback") {
			return time.Time{}, errors.New("Cloud offsite-backup verification lacks an opaque restore/readback ref")
		}
		if !validCoreHostBootstrapDigest(observation.RestoreReadbackDigest) {
			return time.Time{}, errors.New("Cloud offsite-backup verification lacks a restore/readback digest")
		}
	} else if observation.BackupObservationRef != "" || observation.BackupObservationDigest != "" ||
		observation.RestoreReadbackRef != "" || observation.RestoreReadbackDigest != "" {
		return time.Time{}, fmt.Errorf("%s observation widens into backup/restore evidence authority", operation)
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observation.ObservedAt)
	validUntil, validErr := time.Parse(time.RFC3339Nano, expectation.ValidUntil)
	if err != nil || validErr != nil || observedAt.Before(evaluatedAt) || observedAt.Before(notBefore) ||
		observedAt.After(now) || !observedAt.Before(validUntil) || now.Sub(observedAt) > cloudOffsiteBackupMaxObservationAge {
		return time.Time{}, fmt.Errorf("%s observation is stale, future-dated, expired, or non-monotonic", operation)
	}
	return observedAt, nil
}

func validateCloudOffsiteBackupReceipt(receipt CloudOffsiteBackupEvidenceReceipt, digest string, evaluatedAt, notBefore, now time.Time) error {
	committedAt, err := time.Parse(time.RFC3339Nano, receipt.CommittedAt)
	if receipt.EvidenceDigest != digest || err != nil || committedAt.Before(evaluatedAt) ||
		committedAt.Before(notBefore) || committedAt.After(now) || now.Sub(committedAt) > cloudOffsiteBackupMaxObservationAge {
		return errors.New("Cloud offsite-backup evidence receipt is unbound, stale, future-dated, or non-monotonic")
	}
	return nil
}

func digestCloudOffsiteBackup(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func opaqueEvidenceRef(value, kind string) bool {
	prefix := kind + "://sha256/"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, prefix))
	return err == nil && value == strings.ToLower(value)
}

var _ runtimeexecutor.Executor = (*CloudOffsiteBackupExecutor)(nil)
