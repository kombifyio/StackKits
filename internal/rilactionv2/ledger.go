package rilaction

import (
	"context"
	"fmt"
	"time"
)

// LedgerDisposition is the complete result vocabulary of one atomic reserve.
type LedgerDisposition string

const (
	LedgerAcquired   LedgerDisposition = "acquired"
	LedgerReplay     LedgerDisposition = "replay"
	LedgerInProgress LedgerDisposition = "in-progress"
	LedgerConflict   LedgerDisposition = "conflict"
)

// LedgerReservationRequest is the minimal non-secret identity a durable store
// atomically binds before an owner is invoked. ValidUntil bounds abandoned
// reservations; takeover policy belongs to the concrete durable store.
type LedgerReservationRequest struct {
	TenantID       string `json:"tenant_id"`
	IdempotencyKey string `json:"idempotency_key"`
	ExecutionID    string `json:"execution_id"`
	RequestDigest  string `json:"request_digest"`
	RequestedAt    string `json:"requested_at"`
	ValidUntil     string `json:"valid_until"`
}

// LedgerReservation is returned by one atomic reserve. ReservationToken is an
// opaque fencing token present only for the caller that acquired execution.
type LedgerReservation struct {
	Disposition      LedgerDisposition `json:"disposition"`
	ReservationToken string            `json:"reservation_token,omitempty"`
	Evidence         *Evidence         `json:"evidence,omitempty"`
}

// LedgerCompletion commits one final evidence record under the exact token
// returned by Reserve. Stores must reject stale or substituted tokens.
type LedgerCompletion struct {
	TenantID         string   `json:"tenant_id"`
	IdempotencyKey   string   `json:"idempotency_key"`
	ExecutionID      string   `json:"execution_id"`
	RequestDigest    string   `json:"request_digest"`
	ReservationToken string   `json:"reservation_token"`
	Evidence         Evidence `json:"evidence"`
}

// ExecutionLedger is the persistence-neutral atomic replay/evidence SPI.
// Implementations own transactions, durability, retention, and abandoned
// reservation handling; they never select an executor or authorize an action.
type ExecutionLedger interface {
	Reserve(context.Context, LedgerReservationRequest) (LedgerReservation, error)
	Complete(context.Context, LedgerCompletion) error
}

// NewLedgerReservationRequest derives the exact store request from one fresh
// approved handoff and the same trusted UTC instant used for admission.
func NewLedgerReservationRequest(request Request, requestDigest string, at time.Time) (LedgerReservationRequest, error) {
	if err := ValidateRequestAt(request, at); err != nil {
		return LedgerReservationRequest{}, err
	}
	wantDigest, err := ComputeRequestDigest(request)
	if err != nil {
		return LedgerReservationRequest{}, err
	}
	if requestDigest != wantDigest {
		return LedgerReservationRequest{}, invalid("ledger.request_digest", "does not match the approved request")
	}
	return LedgerReservationRequest{
		TenantID: request.TenantID, IdempotencyKey: request.IdempotencyKey,
		ExecutionID: request.ExecutionID, RequestDigest: requestDigest,
		RequestedAt: at.Format(time.RFC3339Nano), ValidUntil: request.ValidUntil,
	}, nil
}

// ValidateLedgerReservation closes one store result before StackKits uses it.
func ValidateLedgerReservation(request Request, reservationRequest LedgerReservationRequest, reservation LedgerReservation) error {
	requestDigest, err := ComputeRequestDigest(request)
	if err != nil {
		return err
	}
	if reservationRequest.TenantID != request.TenantID || reservationRequest.IdempotencyKey != request.IdempotencyKey ||
		reservationRequest.ExecutionID != request.ExecutionID || reservationRequest.RequestDigest != requestDigest ||
		reservationRequest.ValidUntil != request.ValidUntil {
		return invalid("ledger.reservation_request", "does not match the approved request")
	}
	requestedAt, err := parseTimestamp("ledger.requested_at", reservationRequest.RequestedAt)
	if err != nil {
		return err
	}
	issuedAt, _ := parseCanonicalUTC(request.IssuedAt)
	validUntil, _ := parseCanonicalUTC(request.ValidUntil)
	if requestedAt.Before(issuedAt) || !requestedAt.Before(validUntil) {
		return invalid("ledger.requested_at", "falls outside the approved request window")
	}
	switch reservation.Disposition {
	case LedgerAcquired:
		if err := validateReservationToken(reservation.ReservationToken); err != nil {
			return err
		}
		if reservation.Evidence != nil {
			return invalid("ledger.evidence", "must be absent when execution is acquired")
		}
	case LedgerReplay:
		if reservation.ReservationToken != "" || reservation.Evidence == nil {
			return invalid("ledger.replay", "requires evidence and no reservation token")
		}
		if err := ValidateEvidenceForRequest(request, *reservation.Evidence); err != nil {
			return fmt.Errorf("validate replay evidence: %w", err)
		}
	case LedgerInProgress, LedgerConflict:
		if reservation.ReservationToken != "" || reservation.Evidence != nil {
			return invalid("ledger.reservation", "non-acquired result cannot expose token or evidence")
		}
	default:
		return invalid("ledger.disposition", "is unsupported")
	}
	return nil
}

// NewLedgerCompletion binds final evidence to the acquired reservation token.
func NewLedgerCompletion(request Request, reservationRequest LedgerReservationRequest, reservationToken string, evidence Evidence) (LedgerCompletion, error) {
	if err := ValidateEvidenceForRequest(request, evidence); err != nil {
		return LedgerCompletion{}, err
	}
	if err := ValidateLedgerReservation(request, reservationRequest, LedgerReservation{
		Disposition: LedgerAcquired, ReservationToken: reservationToken,
	}); err != nil {
		return LedgerCompletion{}, err
	}
	requestDigest, err := ComputeRequestDigest(request)
	if err != nil {
		return LedgerCompletion{}, err
	}
	return LedgerCompletion{
		TenantID: request.TenantID, IdempotencyKey: request.IdempotencyKey,
		ExecutionID: request.ExecutionID, RequestDigest: requestDigest,
		ReservationToken: reservationToken, Evidence: evidence,
	}, nil
}

func validateReservationToken(value string) error {
	if len(value) < 16 {
		return invalid("ledger.reservation_token", "must contain at least 16 characters")
	}
	return validateStableID("ledger.reservation_token", value)
}
