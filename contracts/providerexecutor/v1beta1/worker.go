package providerexecutor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// WorkerExecution is a bounded dispatch to a customer-owned executor. The
// original admitted command remains the authority; this envelope cannot grant
// a lease or a credential. Payload is the exact desired-spec document retained
// by the admitting product, never an argv list or credential transport.
type WorkerExecution struct {
	Command      Command                `json:"command"`
	InvocationID string                 `json:"invocation_id"`
	WorkerID     string                 `json:"worker_id"`
	HeadSequence uint64                 `json:"head_sequence"`
	HeadDigest   string                 `json:"head_digest"`
	Phase        WorkerPhase            `json:"phase"`
	Stage        string                 `json:"stage,omitempty"`
	Prerequisite *WorkerExecutionResult `json:"prerequisite,omitempty"`
	Payload      json.RawMessage        `json:"payload,omitempty"`
	TaskRef      string                 `json:"task_ref,omitempty"`
	ExpiresAt    time.Time              `json:"expires_at"`
}

type WorkerPhase string

const (
	WorkerPhaseSubmit  WorkerPhase = "submit"
	WorkerPhaseObserve WorkerPhase = "observe"
)

// Validate checks dispatch integrity and bounded lifetime. Credential custody,
// live lease revision and invocation admission are revalidated by the owning
// product immediately before dispatch; a valid envelope alone is not authority.
func (w WorkerExecution) Validate(now time.Time) error {
	if err := w.Command.Validate(); err != nil {
		return err
	}
	if w.InvocationID == "" || len(w.InvocationID) > 256 || w.WorkerID == "" || len(w.WorkerID) > 256 {
		return fmt.Errorf("%w: worker invocation identity required", ErrInvalidCommand)
	}
	if w.HeadSequence == 0 || w.HeadSequence > MaxJSONSafeInteger || !validDigest(w.HeadDigest) {
		return fmt.Errorf("%w: exact execution head required", ErrInvalidCommand)
	}
	if !w.ExpiresAt.After(now) || w.ExpiresAt.After(now.Add(5*time.Minute)) {
		return fmt.Errorf("%w: worker dispatch expired or unbounded", ErrInvalidCommand)
	}
	if w.Phase != WorkerPhaseSubmit && w.Phase != WorkerPhaseObserve {
		return fmt.Errorf("%w: unsupported worker phase", ErrInvalidCommand)
	}
	if len(w.Stage) > 64 {
		return fmt.Errorf("%w: worker stage too long", ErrInvalidCommand)
	}
	if w.Prerequisite != nil && (w.Prerequisite.CommandDigest != w.Command.CommandDigest || w.Prerequisite.InvocationID == "" || len(w.Prerequisite.Observation) > 128<<10 || w.Prerequisite.ErrorCode != "") {
		return fmt.Errorf("%w: prerequisite does not belong to this command", ErrInvalidCommand)
	}
	if len(w.TaskRef) > 2048 || (w.Phase == WorkerPhaseSubmit && w.TaskRef != "") {
		return fmt.Errorf("%w: task reference only belongs to observation", ErrInvalidCommand)
	}
	if len(w.Payload) > 128<<10 {
		return fmt.Errorf("%w: worker payload too large", ErrInvalidCommand)
	}
	if w.Command.DesiredSpecHash != "" || len(w.Payload) != 0 {
		if !json.Valid(w.Payload) {
			return fmt.Errorf("%w: worker payload is not JSON", ErrInvalidCommand)
		}
		hash := sha256.Sum256(w.Payload)
		if w.Command.DesiredSpecHash != "sha256:"+hex.EncodeToString(hash[:]) {
			return fmt.Errorf("%w: worker payload differs from admitted desired spec", ErrInvalidCommand)
		}
	}
	return nil
}

// WorkerExecutionResult reports only observed execution facts. Accepted is not
// completion: TaskRef must be observed separately before convergence is claimed.
type WorkerExecutionResult struct {
	InvocationID  string          `json:"invocation_id"`
	CommandDigest string          `json:"command_digest"`
	TaskRef       string          `json:"task_ref,omitempty"`
	Accepted      bool            `json:"accepted"`
	Complete      bool            `json:"complete"`
	Succeeded     bool            `json:"succeeded"`
	Observation   json.RawMessage `json:"observation,omitempty"`
	ErrorCode     string          `json:"error_code,omitempty"`
}
