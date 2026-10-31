package datasubject

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Kind is the lifecycle operation of a request.
type Kind string

// Request kinds.
const (
	KindExport Kind = "export"
	KindErase  Kind = "erase"
)

// RequestState is the participant-reported progress of a request.
type RequestState string

// Request states.
const (
	StateAccepted RequestState = "accepted"
	StateRunning  RequestState = "running"
	StateDone     RequestState = "done"
	StateBlocked  RequestState = "blocked"
	StateFailed   RequestState = "failed"
)

// ReceiptState is the terminal outcome a participant reports.
type ReceiptState string

// Receipt states. Only Erased and NothingHeld let an erasure complete;
// Exported is the terminal state of an export.
const (
	ReceiptErased      ReceiptState = "erased"
	ReceiptNothingHeld ReceiptState = "nothing_held"
	ReceiptExported    ReceiptState = "exported"
	ReceiptBlocked     ReceiptState = "blocked"
	ReceiptFailed      ReceiptState = "failed"
)

// Request is delivered to every participant; it is idempotent on RequestID.
type Request struct {
	RequestID   string    `json:"request_id"`
	Kind        Kind      `json:"kind"`
	SubjectRef  string    `json:"subject_ref"`
	RequestedAt time.Time `json:"requested_at"`
	Deadline    time.Time `json:"deadline"`
}

// Validate checks the request locally.
func (r Request) Validate() error {
	if _, err := uuid.Parse(r.RequestID); err != nil {
		return fmt.Errorf("datasubject: request_id must be a UUID: %w", err)
	}
	if r.Kind != KindExport && r.Kind != KindErase {
		return fmt.Errorf("datasubject: unknown kind %q", r.Kind)
	}
	if err := validateSubjectRef(r.SubjectRef); err != nil {
		return err
	}
	if r.RequestedAt.IsZero() || r.Deadline.IsZero() || !r.Deadline.After(r.RequestedAt) {
		return errors.New("datasubject: deadline must be after requested_at")
	}
	return nil
}

func validateSubjectRef(ref string) error {
	if strings.TrimSpace(ref) == "" {
		return errors.New("datasubject: subject_ref is required")
	}
	if strings.ContainsAny(ref, "@ \t\r\n") {
		return errors.New("datasubject: subject_ref must be an opaque reference, not an e-mail address")
	}
	return nil
}

// Dataset reports what a participant did with one dataset.
type Dataset struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Rows   int64  `json:"rows"`
	Store  string `json:"store"`
}

// Retained reports data that stays for a declared reason.
type Retained struct {
	ID            string    `json:"id"`
	Reason        string    `json:"reason"`
	RetainedUntil time.Time `json:"retained_until"`
}

// Processor reports the deletion mechanism of a downstream processor.
type Processor struct {
	Name      string `json:"name"`
	Action    string `json:"action"`
	Reference string `json:"reference"`
}

// Receipt is the signed terminal answer of one participant.
type Receipt struct {
	RequestID   string       `json:"request_id"`
	Service     string       `json:"service"`
	Kind        Kind         `json:"kind"`
	State       ReceiptState `json:"state"`
	CompletedAt time.Time    `json:"completed_at"`
	Datasets    []Dataset    `json:"datasets"`
	Retained    []Retained   `json:"retained"`
	Processors  []Processor  `json:"processors"`
	ReasonCode  *string      `json:"reason_code"`
	Signature   []byte       `json:"signature"`
}

// Validate checks the receipt shape, not the signature.
func (r Receipt) Validate() error {
	if _, err := uuid.Parse(r.RequestID); err != nil {
		return fmt.Errorf("datasubject: request_id must be a UUID: %w", err)
	}
	if strings.TrimSpace(r.Service) == "" {
		return errors.New("datasubject: service is required")
	}
	switch r.Kind {
	case KindErase:
		if r.State == ReceiptExported {
			return errors.New("datasubject: an erase receipt cannot be exported")
		}
	case KindExport:
		if r.State == ReceiptErased {
			return errors.New("datasubject: an export receipt cannot be erased")
		}
	default:
		return fmt.Errorf("datasubject: unknown kind %q", r.Kind)
	}
	switch r.State {
	case ReceiptErased, ReceiptNothingHeld, ReceiptExported:
	case ReceiptBlocked, ReceiptFailed:
		if r.ReasonCode == nil || strings.TrimSpace(*r.ReasonCode) == "" {
			return errors.New("datasubject: blocked and failed receipts require a reason_code")
		}
	default:
		return fmt.Errorf("datasubject: unknown state %q", r.State)
	}
	if r.CompletedAt.IsZero() {
		return errors.New("datasubject: completed_at is required")
	}
	for _, kept := range r.Retained {
		if kept.Reason == "" || kept.RetainedUntil.IsZero() {
			return fmt.Errorf("datasubject: retained %q needs a reason and retained_until", kept.ID)
		}
	}
	return nil
}

// SigningBytes is the canonical JSON of the receipt without its signature.
func (r Receipt) SigningBytes() ([]byte, error) {
	r.Signature = nil
	return json.Marshal(r)
}

// Seal validates the receipt and signs it with the participant key.
func (r Receipt) Seal(key ed25519.PrivateKey) (Receipt, error) {
	if err := r.Validate(); err != nil {
		return Receipt{}, err
	}
	payload, err := r.SigningBytes()
	if err != nil {
		return Receipt{}, err
	}
	r.Signature = ed25519.Sign(key, payload)
	return r, nil
}

// Verify checks the receipt shape and its signature against a participant key.
func (r Receipt) Verify(key ed25519.PublicKey) error {
	if err := r.Validate(); err != nil {
		return err
	}
	payload, err := r.SigningBytes()
	if err != nil {
		return err
	}
	if !ed25519.Verify(key, payload, r.Signature) {
		return errors.New("datasubject: receipt signature does not verify")
	}
	return nil
}

// CanCompleteErasure applies the coordinator rule: an erasure completes only
// when every registered participant returned erased or nothing_held for the
// request. The error names the first participant that does not qualify.
func CanCompleteErasure(requestID string, participants []string, receipts []Receipt) error {
	byService := make(map[string]Receipt, len(receipts))
	for _, receipt := range receipts {
		if receipt.RequestID == requestID && receipt.Kind == KindErase {
			byService[receipt.Service] = receipt
		}
	}
	for _, service := range participants {
		receipt, ok := byService[service]
		if !ok {
			return fmt.Errorf("datasubject: participant %q has not returned a receipt", service)
		}
		if receipt.State != ReceiptErased && receipt.State != ReceiptNothingHeld {
			return fmt.Errorf("datasubject: participant %q reported %q", service, receipt.State)
		}
	}
	return nil
}
