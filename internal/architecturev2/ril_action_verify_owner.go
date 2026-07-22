package architecturev2

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/rilactionv2"
)

const (
	rilGovernedStateVerifierRef = "stackkits-governed-state-verifier-v1"
	maxRILActionRecords         = 4096
)

// RILActionEvidence aliases the neutral, public-safe shared evidence wire.
type RILActionEvidence = rilaction.Evidence

type rilActionRecord struct {
	digest   string
	token    string
	evidence *RILActionEvidence
}

// memoryRILActionLedger provides bounded process-local at-most-once execution.
// Durable cross-process replay and evidence custody intentionally remain a
// separate runtime integration responsibility and are not claimed here.
type memoryRILActionLedger struct {
	mu      sync.Mutex
	records map[string]rilActionRecord
}

func newMemoryRILActionLedger() *memoryRILActionLedger {
	return &memoryRILActionLedger{records: make(map[string]rilActionRecord)}
}

// ExecuteRILActionAt validates, admits, replay-guards, and immediately invokes
// the one exact built-in read-only owner. The supplied time is the same trusted
// UTC sample used for request, approval, grant, admission, and evidence.
func (s *Service) ExecuteRILActionAt(input RILActionAdmissionInput) (RILActionEvidence, error) {
	return s.ExecuteRILAction(context.Background(), input)
}

// ExecuteRILAction is the context-aware execution entrypoint used by durable
// integrations. The ledger is invoked only after exact action admission.
func (s *Service) ExecuteRILAction(ctx context.Context, input RILActionAdmissionInput) (RILActionEvidence, error) {
	if s == nil || s.rilActionLedger == nil {
		return RILActionEvidence{}, resolveError(ErrAuthorityLoad, "RIL action execution ledger is not initialized", nil)
	}
	stableInput := input
	stableInput.Envelope = append([]byte(nil), input.Envelope...)
	validated, err := s.AdmitRILActionAt(stableInput)
	if err != nil {
		return RILActionEvidence{}, err
	}
	request, err := rilaction.DecodeRequestAt(stableInput.Envelope, stableInput.EvaluatedAt)
	if err != nil {
		return RILActionEvidence{}, resolveError(ErrRILActionAdmission, "approved action envelope changed after admission", err)
	}
	reservationRequest, err := rilaction.NewLedgerReservationRequest(request, validated.RequestDigest, stableInput.EvaluatedAt)
	if err != nil {
		return RILActionEvidence{}, resolveError(ErrRILActionAdmission, "construct RIL action ledger reservation", err)
	}
	reservation, err := s.rilActionLedger.Reserve(ctx, reservationRequest)
	if err != nil {
		return RILActionEvidence{}, resolveError(ErrRILActionExecution, "reserve RIL action execution", err)
	}
	if err := rilaction.ValidateLedgerReservation(request, reservationRequest, reservation); err != nil {
		return RILActionEvidence{}, resolveError(ErrRILActionExecution, "validate RIL action ledger reservation", err)
	}
	switch reservation.Disposition {
	case rilaction.LedgerReplay:
		evidence := *reservation.Evidence
		if evidence.Status == "failed" {
			return evidence, resolveError(ErrRILActionExecution, "replayed RIL action previously failed", nil)
		}
		return evidence, nil
	case rilaction.LedgerInProgress:
		return RILActionEvidence{}, resolveError(ErrRILActionBusy, "approved action execution is already in progress", nil)
	case rilaction.LedgerConflict:
		return RILActionEvidence{}, resolveError(ErrRILActionReplay, "idempotency identity is already bound to another approved action", nil)
	}

	evidence, executionErr := s.executeGovernedStateVerification(stableInput.Current, request, validated)
	completion, err := rilaction.NewLedgerCompletion(request, reservationRequest, reservation.ReservationToken, evidence)
	if err != nil {
		return evidence, resolveError(ErrRILActionExecution, "construct RIL action ledger completion", err)
	}
	if err := s.rilActionLedger.Complete(ctx, completion); err != nil {
		return evidence, resolveError(ErrRILActionExecution, "commit RIL action evidence", err)
	}
	return evidence, executionErr
}

func (s *Service) executeGovernedStateVerification(current CurrentResolution, request rilaction.Request, validated RILActionValidation) (RILActionEvidence, error) {
	evidence, err := newRILActionEvidence(request, validated)
	if err != nil {
		return RILActionEvidence{}, resolveError(ErrRILActionExecution, "construct governed action evidence", err)
	}
	if request.Primitive.ID != "verify-stackkit-state" || validated.PrimitiveID != "verify-stackkit-state" {
		return validatedRILActionFailure(request, evidence, "unsupported-primitive", resolveError(ErrRILActionUnavailable, "the selected RIL action owner is not implemented", nil))
	}
	if !s.generation.matchesIssuedResolution(current.key, current.epoch, current.plan.Binding()) {
		return validatedRILActionFailure(request, evidence, "stale-current-resolution", resolveError(ErrRILActionExecution, "governed state verification failed closed", nil))
	}
	verified, err := generationartifact.VerifyPlan(current.plan.Canonical(), s.validator)
	if err != nil || verified.Binding() != current.plan.Binding() || verified.Binding().PlanHash != request.ResolvedPlanHash {
		return validatedRILActionFailure(request, evidence, "plan-authority-drift", resolveError(ErrRILActionExecution, "governed state verification failed closed", err))
	}
	evidence.Status = "succeeded"
	evidence.Verification.Status = "passed"
	for index := range evidence.Verification.Checks {
		evidence.Verification.Checks[index].Status = "passed"
	}
	evidence.SummaryCodes = []string{"governed-plan-readback-passed"}
	if err := rilaction.ValidateEvidenceForRequest(request, evidence); err != nil {
		return RILActionEvidence{}, resolveError(ErrRILActionExecution, "validate governed action evidence", err)
	}
	return evidence, nil
}

func newRILActionEvidence(request rilaction.Request, validated RILActionValidation) (RILActionEvidence, error) {
	evidenceID, err := rilaction.ComputeEvidenceID(validated.RequestDigest, rilGovernedStateVerifierRef)
	if err != nil {
		return RILActionEvidence{}, err
	}
	targetRef, err := rilaction.TargetReference(request)
	if err != nil {
		return RILActionEvidence{}, err
	}
	return RILActionEvidence{
		APIVersion: rilaction.EvidenceAPIVersionV1, EvidenceID: evidenceID,
		EvidenceSinkRef: request.EvidenceSinkRef,
		ActionCardID:    request.ActionCardID, ExecutionID: request.ExecutionID, TraceID: request.TraceID,
		TenantID: request.TenantID, StackID: request.StackID,
		PrimitiveID: request.Primitive.ID, PrimitiveContractHash: request.Primitive.ContractHash,
		ResolvedPlanHash: request.ResolvedPlanHash, RequestDigest: validated.RequestDigest,
		ExecutorRef: rilGovernedStateVerifierRef, TargetRef: targetRef, Status: "failed",
		Verification: rilaction.VerificationEvidence{
			Kind: "governed-plan-readback", Status: "failed", RuntimeStateObserved: false,
			Checks: []rilaction.VerificationCheck{
				{ID: "canonical-plan", Status: "failed"},
				{ID: "cue-contract", Status: "failed"},
				{ID: "current-resolution", Status: "failed"},
			},
		},
		Recovery:     rilaction.RecoveryEvidence{Kind: "none", Status: "not-required"},
		SummaryCodes: []string{"governed-plan-readback-failed"},
		EvaluatedAt:  validated.EvaluatedAt.UTC().Format(time.RFC3339Nano),
	}, nil
}

func validatedRILActionFailure(request rilaction.Request, evidence RILActionEvidence, summaryCode string, executionErr error) (RILActionEvidence, error) {
	evidence.SummaryCodes = []string{summaryCode}
	if err := rilaction.ValidateEvidenceForRequest(request, evidence); err != nil {
		return RILActionEvidence{}, resolveError(ErrRILActionExecution, "validate failed governed action evidence", err)
	}
	return evidence, executionErr
}

func (c *memoryRILActionLedger) Reserve(ctx context.Context, request rilaction.LedgerReservationRequest) (rilaction.LedgerReservation, error) {
	if err := ctx.Err(); err != nil {
		return rilaction.LedgerReservation{}, err
	}
	key := request.TenantID + "\x00" + request.IdempotencyKey
	c.mu.Lock()
	defer c.mu.Unlock()
	if record, exists := c.records[key]; exists {
		if record.digest != request.RequestDigest {
			return rilaction.LedgerReservation{Disposition: rilaction.LedgerConflict}, nil
		}
		if record.evidence == nil {
			return rilaction.LedgerReservation{Disposition: rilaction.LedgerInProgress}, nil
		}
		evidence := cloneRILActionEvidence(*record.evidence)
		return rilaction.LedgerReservation{Disposition: rilaction.LedgerReplay, Evidence: &evidence}, nil
	}
	if len(c.records) >= maxRILActionRecords {
		return rilaction.LedgerReservation{}, fmt.Errorf("process-local RIL action replay ledger is full")
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return rilaction.LedgerReservation{}, fmt.Errorf("create reservation token: %w", err)
	}
	token := "reservation-" + hex.EncodeToString(tokenBytes)
	c.records[key] = rilActionRecord{digest: request.RequestDigest, token: token}
	return rilaction.LedgerReservation{Disposition: rilaction.LedgerAcquired, ReservationToken: token}, nil
}

func (c *memoryRILActionLedger) Complete(ctx context.Context, completion rilaction.LedgerCompletion) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key := completion.TenantID + "\x00" + completion.IdempotencyKey
	c.mu.Lock()
	defer c.mu.Unlock()
	record, exists := c.records[key]
	if !exists || record.digest != completion.RequestDigest || record.token != completion.ReservationToken || record.evidence != nil {
		return fmt.Errorf("RIL action completion does not match the active reservation")
	}
	evidence := cloneRILActionEvidence(completion.Evidence)
	record.evidence = &evidence
	c.records[key] = record
	return nil
}

func cloneRILActionEvidence(evidence RILActionEvidence) RILActionEvidence {
	clone := evidence
	clone.SummaryCodes = append([]string(nil), evidence.SummaryCodes...)
	clone.Verification.Checks = append([]rilaction.VerificationCheck(nil), evidence.Verification.Checks...)
	return clone
}

var _ rilaction.ExecutionLedger = (*memoryRILActionLedger)(nil)
