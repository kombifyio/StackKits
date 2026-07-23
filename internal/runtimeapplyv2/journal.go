// Package runtimeapply defines the provider-neutral durable operation journal
// contract for a sealed runtimeexecutor Apply. It owns no persistence,
// executor selection, transport, provider lifecycle, credential, lease, or
// generation authority.
package runtimeapply

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const APIVersion = "kombify.runtime-apply-operation/v1alpha1"

type CompensationMode string

const (
	CompensationNone     CompensationMode = "none"
	CompensationExplicit CompensationMode = "explicit"
)

type HealthExpectation struct {
	RequirementID string `json:"requirement_id"`
	TargetRef     string `json:"target_ref"`
}

type RuntimeExpectation struct {
	RequirementID string `json:"requirement_id"`
	InstanceRef   string `json:"instance_ref"`
}

// Step is one exact independently journaled child request. Compensation is a
// declared capability only; it is never inferred from an executor or failure.
type Step struct {
	ID                    string                           `json:"id"`
	RequestDigest         string                           `json:"request_digest"`
	PlanHash              string                           `json:"plan_hash"`
	ManifestHash          string                           `json:"manifest_hash"`
	GenerationReceiptHash string                           `json:"generation_receipt_hash"`
	RequirementsHash      string                           `json:"requirements_hash"`
	EvidenceBundleHash    string                           `json:"evidence_bundle_hash"`
	ArtifactSetHash       string                           `json:"artifact_set_hash"`
	Executor              runtimeexecutor.ExecutorIdentity `json:"executor"`
	Runtime               []RuntimeExpectation             `json:"runtime"`
	Health                []HealthExpectation              `json:"health"`
	Compensation          CompensationMode                 `json:"compensation"`
}

// Operation binds a complete parent request to its exact sorted child set.
// OperationID deliberately equals ParentRequestDigest: a retry can resume only
// the same sealed authority, while new evidence or authority creates a new
// operation.
type Operation struct {
	APIVersion            string                           `json:"api_version"`
	OperationID           string                           `json:"operation_id"`
	ParentRequestDigest   string                           `json:"parent_request_digest"`
	PlanHash              string                           `json:"plan_hash"`
	ManifestHash          string                           `json:"manifest_hash"`
	GenerationReceiptHash string                           `json:"generation_receipt_hash"`
	RequirementsHash      string                           `json:"requirements_hash"`
	EvidenceBundleHash    string                           `json:"evidence_bundle_hash"`
	Executor              runtimeexecutor.ExecutorIdentity `json:"executor"`
	Steps                 []Step                           `json:"steps"`
}

type Disposition string

const (
	DispositionAcquired   Disposition = "acquired"
	DispositionResume     Disposition = "resume"
	DispositionReplay     Disposition = "replay"
	DispositionInProgress Disposition = "in-progress"
	DispositionConflict   Disposition = "conflict"
)

type OperationState string

const (
	OperationRunning           OperationState = "running"
	OperationReconcileRequired OperationState = "reconcile-required"
	OperationCompleted         OperationState = "completed"
	OperationCompensated       OperationState = "compensated"
)

type StepState string

const (
	StepPending     StepState = "pending"
	StepRunning     StepState = "running"
	StepSucceeded   StepState = "succeeded"
	StepFailed      StepState = "failed"
	StepCompensated StepState = "compensated"
)

type FailureCode string

const (
	FailureExecutorFailed     FailureCode = "executor-failed"
	FailureCancelled          FailureCode = "cancelled"
	FailureVerificationFailed FailureCode = "verification-failed"
)

type StepSnapshot struct {
	StepID                    string                           `json:"step_id"`
	RequestDigest             string                           `json:"request_digest"`
	State                     StepState                        `json:"state"`
	Result                    *runtimeexecutor.ExecutionResult `json:"result,omitempty"`
	FailureCode               FailureCode                      `json:"failure_code,omitempty"`
	CompensationReceiptDigest string                           `json:"compensation_receipt_digest,omitempty"`
}

type Snapshot struct {
	OperationID string         `json:"operation_id"`
	State       OperationState `json:"state"`
	Steps       []StepSnapshot `json:"steps"`
}

// Reservation is the closed atomic Begin result. Acquired and resume return
// an opaque fencing token. Replay returns only a completed exact snapshot.
type Reservation struct {
	Disposition Disposition `json:"disposition"`
	FenceToken  string      `json:"fence_token,omitempty"`
	Snapshot    *Snapshot   `json:"snapshot,omitempty"`
}

// StepCommit is one atomic fenced journal transition. Stores must reject a
// stale token and a request digest that differs from the registered step.
type StepCommit struct {
	OperationID               string                           `json:"operation_id"`
	FenceToken                string                           `json:"fence_token"`
	StepID                    string                           `json:"step_id"`
	RequestDigest             string                           `json:"request_digest"`
	ExpectedState             StepState                        `json:"expected_state"`
	State                     StepState                        `json:"state"`
	Result                    *runtimeexecutor.ExecutionResult `json:"result,omitempty"`
	FailureCode               FailureCode                      `json:"failure_code,omitempty"`
	CompensationReceiptDigest string                           `json:"compensation_receipt_digest,omitempty"`
}

type Finalization struct {
	OperationID   string         `json:"operation_id"`
	FenceToken    string         `json:"fence_token"`
	ExpectedState OperationState `json:"expected_state"`
	State         OperationState `json:"state"`
}

// Journal is the persistence-neutral durable SPI. Implementations own atomic
// storage, fencing, retention, and abandoned-operation policy only.
type Journal interface {
	Begin(context.Context, Operation) (Reservation, error)
	CommitStep(context.Context, StepCommit) (Snapshot, error)
	Finalize(context.Context, Finalization) (Snapshot, error)
}

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{15,255}$`)

func NewStep(request runtimeexecutor.ExecutionRequest, compensation CompensationMode) (Step, error) {
	if err := request.Validate(); err != nil {
		return Step{}, fmt.Errorf("validate child execution request: %w", err)
	}
	if compensation != CompensationNone && compensation != CompensationExplicit {
		return Step{}, errors.New("runtime Apply journal step has unsupported compensation mode")
	}
	runtimeTargets := make([]RuntimeExpectation, len(request.RuntimeTargets))
	for index, target := range request.RuntimeTargets {
		runtimeTargets[index] = RuntimeExpectation{RequirementID: target.RequirementID, InstanceRef: target.InstanceRef}
	}
	sort.Slice(runtimeTargets, func(i, j int) bool { return runtimeTargets[i].RequirementID < runtimeTargets[j].RequirementID })
	health := make([]HealthExpectation, len(request.HealthTargets))
	for index, item := range request.HealthTargets {
		if !healthTargetsOneRuntime(item, request.RuntimeTargets) {
			return Step{}, errors.New("runtime Apply journal step contains foreign health authority")
		}
		health[index] = HealthExpectation{RequirementID: item.RequirementID, TargetRef: item.TargetRef}
	}
	sort.Slice(health, func(i, j int) bool { return health[i].RequirementID < health[j].RequirementID })
	return Step{
		ID: request.RequestDigest, RequestDigest: request.RequestDigest,
		PlanHash: request.PlanHash, ManifestHash: request.ManifestHash,
		GenerationReceiptHash: request.GenerationReceiptHash, RequirementsHash: request.RequirementsHash,
		EvidenceBundleHash: request.EvidenceBundleHash, ArtifactSetHash: request.ArtifactSetHash,
		Executor: request.Executor, Runtime: runtimeTargets,
		Health: health, Compensation: compensation,
	}, nil
}

func NewOperation(parent runtimeexecutor.ExecutionRequest, steps []Step) (Operation, error) {
	if err := parent.Validate(); err != nil {
		return Operation{}, fmt.Errorf("validate parent execution request: %w", err)
	}
	operation := Operation{
		APIVersion: APIVersion, OperationID: parent.RequestDigest, ParentRequestDigest: parent.RequestDigest,
		PlanHash: parent.PlanHash, ManifestHash: parent.ManifestHash,
		GenerationReceiptHash: parent.GenerationReceiptHash, RequirementsHash: parent.RequirementsHash,
		EvidenceBundleHash: parent.EvidenceBundleHash, Executor: parent.Executor,
		Steps: append([]Step(nil), steps...),
	}
	sort.Slice(operation.Steps, func(i, j int) bool { return operation.Steps[i].ID < operation.Steps[j].ID })
	if err := ValidateOperationForRequest(parent, operation); err != nil {
		return Operation{}, err
	}
	return operation, nil
}

func ValidateOperationForRequest(parent runtimeexecutor.ExecutionRequest, operation Operation) error {
	if err := parent.Validate(); err != nil {
		return err
	}
	if operation.APIVersion != APIVersion || operation.OperationID != parent.RequestDigest ||
		operation.ParentRequestDigest != parent.RequestDigest || operation.PlanHash != parent.PlanHash ||
		operation.ManifestHash != parent.ManifestHash || operation.GenerationReceiptHash != parent.GenerationReceiptHash ||
		operation.RequirementsHash != parent.RequirementsHash || operation.EvidenceBundleHash != parent.EvidenceBundleHash ||
		operation.Executor != parent.Executor {
		return errors.New("runtime Apply operation does not bind the exact parent request")
	}
	if len(operation.Steps) == 0 {
		return errors.New("runtime Apply operation requires at least one child step")
	}
	for index := range operation.Steps {
		if index > 0 && operation.Steps[index-1].ID >= operation.Steps[index].ID {
			return errors.New("runtime Apply operation steps must be sorted")
		}
	}
	targets := make(map[string]runtimeexecutor.RuntimeTarget, len(parent.RuntimeTargets))
	for _, target := range parent.RuntimeTargets {
		targets[target.RequirementID] = target
	}
	seenStepIDs := map[string]struct{}{}
	runtimeSeen := map[string]struct{}{}
	seenDigests := map[string]struct{}{}
	healthSeen := map[HealthExpectation]struct{}{}
	for _, step := range operation.Steps {
		if step.ID != step.RequestDigest || len(step.Runtime) == 0 {
			return errors.New("runtime Apply step ID must equal its exact child request digest")
		}
		if _, duplicate := seenStepIDs[step.ID]; duplicate {
			return errors.New("runtime Apply operation contains a duplicate step")
		}
		seenStepIDs[step.ID] = struct{}{}
		if _, duplicate := seenDigests[step.RequestDigest]; duplicate {
			return errors.New("runtime Apply operation contains a duplicate child request digest")
		}
		seenDigests[step.RequestDigest] = struct{}{}
		if !digestPattern.MatchString(step.RequestDigest) || !digestPattern.MatchString(step.ArtifactSetHash) ||
			step.PlanHash != parent.PlanHash || step.ManifestHash != parent.ManifestHash ||
			step.GenerationReceiptHash != parent.GenerationReceiptHash || step.RequirementsHash != parent.RequirementsHash ||
			step.EvidenceBundleHash != parent.EvidenceBundleHash ||
			(step.Compensation != CompensationNone && step.Compensation != CompensationExplicit) {
			return errors.New("runtime Apply step has invalid or foreign authority")
		}
		if step.Executor.ID == "" || step.Executor.Version == "" || !digestPattern.MatchString(step.Executor.Digest) {
			return errors.New("runtime Apply step has invalid executor identity")
		}
		for runtimeIndex, runtime := range step.Runtime {
			if runtimeIndex > 0 && step.Runtime[runtimeIndex-1].RequirementID >= runtime.RequirementID {
				return errors.New("runtime Apply step runtime expectations must be sorted and unique")
			}
			target, exists := targets[runtime.RequirementID]
			if !exists || runtime.InstanceRef != target.InstanceRef {
				return errors.New("runtime Apply step does not bind an exact parent runtime target")
			}
			if _, duplicate := runtimeSeen[runtime.RequirementID]; duplicate {
				return errors.New("runtime Apply operation contains duplicate runtime authority")
			}
			runtimeSeen[runtime.RequirementID] = struct{}{}
		}
		for _, item := range step.Health {
			if item.RequirementID == "" || item.TargetRef == "" {
				return errors.New("runtime Apply step has invalid health expectation")
			}
			if _, duplicate := healthSeen[item]; duplicate {
				return errors.New("runtime Apply operation contains duplicate health authority")
			}
			healthSeen[item] = struct{}{}
		}
	}
	if len(runtimeSeen) != len(parent.RuntimeTargets) {
		return errors.New("runtime Apply operation does not cover the complete runtime target set")
	}
	if len(healthSeen) != len(parent.HealthTargets) {
		return errors.New("runtime Apply operation does not cover the complete health target set")
	}
	for _, health := range parent.HealthTargets {
		if _, exists := healthSeen[HealthExpectation{RequirementID: health.RequirementID, TargetRef: health.TargetRef}]; !exists {
			return errors.New("runtime Apply operation contains foreign health authority")
		}
	}
	return nil
}

// VerifyStepResult rejects a journal replay unless it is the exact verified
// child result for the registered step and expected runtime/health set.
func VerifyStepResult(step Step, result runtimeexecutor.ExecutionResult) error {
	if err := result.Validate(); err != nil {
		return fmt.Errorf("validate runtime Apply step result: %w", err)
	}
	if result.Executor != step.Executor || result.PlanHash != step.PlanHash || result.ManifestHash != step.ManifestHash ||
		result.GenerationReceiptHash != step.GenerationReceiptHash || result.RequirementsHash != step.RequirementsHash ||
		result.EvidenceBundleHash != step.EvidenceBundleHash || result.ArtifactSetHash != step.ArtifactSetHash ||
		result.RequestDigest != step.RequestDigest {
		return errors.New("runtime Apply step result has foreign authority")
	}
	if len(result.Runtime) != len(step.Runtime) {
		return errors.New("runtime Apply step result does not prove the exact runtime target")
	}
	for index, expected := range step.Runtime {
		actual := result.Runtime[index]
		if actual.RequirementID != expected.RequirementID || actual.InstanceRef != expected.InstanceRef || actual.Status != runtimeexecutor.RuntimeStatusApplied {
			return errors.New("runtime Apply step result contains foreign runtime evidence")
		}
	}
	if len(result.Health) != len(step.Health) {
		return errors.New("runtime Apply step result does not prove the exact health set")
	}
	for index, expected := range step.Health {
		actual := result.Health[index]
		if actual.RequirementID != expected.RequirementID || actual.TargetRef != expected.TargetRef || actual.Status != runtimeexecutor.HealthStatusHealthy {
			return errors.New("runtime Apply step result contains foreign health evidence")
		}
	}
	return nil
}

func ValidateReservation(operation Operation, reservation Reservation) error {
	switch reservation.Disposition {
	case DispositionAcquired:
		if !tokenPattern.MatchString(reservation.FenceToken) || reservation.Snapshot != nil {
			return errors.New("acquired runtime Apply reservation requires only an opaque fence token")
		}
	case DispositionResume:
		if !tokenPattern.MatchString(reservation.FenceToken) || reservation.Snapshot == nil {
			return errors.New("resume runtime Apply reservation requires a fence token and snapshot")
		}
		if err := ValidateSnapshot(operation, *reservation.Snapshot); err != nil {
			return err
		}
		if reservation.Snapshot.State != OperationRunning && reservation.Snapshot.State != OperationReconcileRequired {
			return errors.New("resume runtime Apply reservation has no resumable state")
		}
	case DispositionReplay:
		if reservation.FenceToken != "" || reservation.Snapshot == nil {
			return errors.New("replay runtime Apply reservation requires only a final snapshot")
		}
		if err := ValidateSnapshot(operation, *reservation.Snapshot); err != nil {
			return err
		}
		if reservation.Snapshot.State != OperationCompleted && reservation.Snapshot.State != OperationCompensated {
			return errors.New("replay runtime Apply reservation is not final")
		}
	case DispositionInProgress, DispositionConflict:
		if reservation.FenceToken != "" || reservation.Snapshot != nil {
			return errors.New("non-owned runtime Apply reservation must not expose a token or snapshot")
		}
	default:
		return errors.New("runtime Apply reservation has unsupported disposition")
	}
	return nil
}

func ValidateSnapshot(operation Operation, snapshot Snapshot) error {
	if snapshot.OperationID != operation.OperationID || len(snapshot.Steps) != len(operation.Steps) {
		return errors.New("runtime Apply snapshot does not bind the exact operation and step set")
	}
	failed, pending, running, succeeded, compensated := 0, 0, 0, 0, 0
	for index, state := range snapshot.Steps {
		step := operation.Steps[index]
		if state.StepID != step.ID || state.RequestDigest != step.RequestDigest {
			return errors.New("runtime Apply snapshot step is reordered or substituted")
		}
		if err := validateStepState(step, state.State, state.Result, state.FailureCode, state.CompensationReceiptDigest); err != nil {
			return err
		}
		switch state.State {
		case StepPending:
			pending++
		case StepRunning:
			running++
		case StepSucceeded:
			succeeded++
		case StepFailed:
			failed++
		case StepCompensated:
			compensated++
		}
	}
	switch snapshot.State {
	case OperationRunning:
		if failed != 0 || compensated != 0 || pending+running+succeeded != len(snapshot.Steps) {
			return errors.New("running runtime Apply snapshot has impossible step states")
		}
	case OperationReconcileRequired:
		if failed == 0 || running != 0 {
			return errors.New("reconcile-required runtime Apply snapshot requires a failed step and no running step")
		}
	case OperationCompleted:
		if succeeded != len(snapshot.Steps) {
			return errors.New("completed runtime Apply snapshot requires every step to succeed")
		}
	case OperationCompensated:
		if compensated == 0 || pending != 0 || running != 0 || succeeded != 0 || compensated+failed != len(snapshot.Steps) {
			return errors.New("compensated runtime Apply snapshot has uncompensated successful work")
		}
	default:
		return errors.New("runtime Apply snapshot has unsupported operation state")
	}
	return nil
}

func ValidateStepCommit(operation Operation, commit StepCommit) error {
	if commit.OperationID != operation.OperationID || !tokenPattern.MatchString(commit.FenceToken) {
		return errors.New("runtime Apply step commit has invalid operation or fence authority")
	}
	index := sort.Search(len(operation.Steps), func(index int) bool { return operation.Steps[index].ID >= commit.StepID })
	if index == len(operation.Steps) || operation.Steps[index].ID != commit.StepID || operation.Steps[index].RequestDigest != commit.RequestDigest {
		return errors.New("runtime Apply step commit does not bind an exact operation step")
	}
	if !validStepTransition(commit.ExpectedState, commit.State) {
		return errors.New("runtime Apply step commit has an invalid state transition")
	}
	return validateStepState(operation.Steps[index], commit.State, commit.Result, commit.FailureCode, commit.CompensationReceiptDigest)
}

func ValidateFinalization(operation Operation, finalization Finalization) error {
	if finalization.OperationID != operation.OperationID || !tokenPattern.MatchString(finalization.FenceToken) {
		return errors.New("runtime Apply finalization has invalid operation or fence authority")
	}
	if !validOperationTransition(finalization.ExpectedState, finalization.State) {
		return errors.New("runtime Apply finalization has unsupported terminal state")
	}
	return nil
}

func validateStepState(step Step, state StepState, result *runtimeexecutor.ExecutionResult, failure FailureCode, compensationDigest string) error {
	switch state {
	case StepPending, StepRunning:
		if result != nil || failure != "" || compensationDigest != "" {
			return errors.New("pending/running runtime Apply step contains terminal evidence")
		}
	case StepSucceeded:
		if result == nil || failure != "" || compensationDigest != "" {
			return errors.New("succeeded runtime Apply step requires only an exact result")
		}
		return VerifyStepResult(step, *result)
	case StepFailed:
		if result != nil || !validFailureCode(failure) || compensationDigest != "" {
			return errors.New("failed runtime Apply step requires only a closed failure code")
		}
	case StepCompensated:
		if step.Compensation != CompensationExplicit || result == nil || failure != "" || !digestPattern.MatchString(compensationDigest) {
			return errors.New("compensated runtime Apply step requires explicit capability, original result, and receipt digest")
		}
		return VerifyStepResult(step, *result)
	default:
		return errors.New("runtime Apply step has unsupported state")
	}
	return nil
}

func validFailureCode(code FailureCode) bool {
	return code == FailureExecutorFailed || code == FailureCancelled || code == FailureVerificationFailed
}

func validStepTransition(from, to StepState) bool {
	return from == StepPending && to == StepRunning ||
		from == StepRunning && (to == StepSucceeded || to == StepFailed) ||
		from == StepFailed && to == StepRunning ||
		from == StepSucceeded && to == StepCompensated
}

func validOperationTransition(from, to OperationState) bool {
	return from == OperationRunning && (to == OperationCompleted || to == OperationReconcileRequired) ||
		from == OperationReconcileRequired && (to == OperationCompleted || to == OperationCompensated) ||
		(from == OperationCompleted || from == OperationCompensated) && from == to
}

func healthTargetsOneRuntime(health runtimeexecutor.HealthTarget, targets []runtimeexecutor.RuntimeTarget) bool {
	matches := 0
	for _, target := range targets {
		if healthTargetsRuntime(health, target) {
			matches++
		}
	}
	return matches == 1
}

func healthTargetsRuntime(health runtimeexecutor.HealthTarget, target runtimeexecutor.RuntimeTarget) bool {
	if len(health.SiteRefs) != len(target.SiteRefs) || len(health.NodeRefs) != len(target.NodeRefs) {
		return false
	}
	for index := range health.SiteRefs {
		if health.SiteRefs[index] != target.SiteRefs[index] {
			return false
		}
	}
	for index := range health.NodeRefs {
		if health.NodeRefs[index] != target.NodeRefs[index] {
			return false
		}
	}
	return health.TargetKind == "module" && health.TargetRef == target.ModuleRef ||
		health.TargetKind == "provider" && health.TargetRef == target.ProviderRef ||
		health.TargetKind == "runtime" && health.TargetRef == target.InstanceRef
}
