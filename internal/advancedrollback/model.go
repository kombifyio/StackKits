// Package advancedrollback runs the Advanced operation `rollback.coordinated`:
// it restores the OpenTofu state and payload of every local Terramate stack
// to one verified executor-state checkpoint, in reverse run order
// (ADR-0045 section 1 and section 3, docs/ARCHITECTURE.md "Coordinated
// rollback across stacks (Stage 1)").
//
// The package owns the per-stack plan, its resumable journal and the
// `stackkit.rollback-result/v1` report. Checkpoint verification, lifecycle
// authority and the StackSpec restore stay with the command and
// internal/upgradelifecycle.
package advancedrollback

import (
	"errors"
	"strings"
	"time"
)

const (
	// ResultSchemaVersion identifies the rollback report
	// (schemas/stackkit-rollback-result-v1.schema.json).
	ResultSchemaVersion = "stackkit.rollback-result/v1"
	// JournalSchemaVersion identifies the per-checkpoint rollback journal.
	JournalSchemaVersion = "stackkit.rollback-journal/v1"
	// JournalDir holds one journal per target checkpoint.
	JournalDir = ".stackkit/advanced/rollbacks"
)

// Per-stack actions, decided once when the rollback is planned.
const (
	// ActionDestroyed: the stack is absent from the checkpoint's stack graph
	// (added after it). Its root is destroyed through OpenTofu, which runs the
	// wrapper's destroy-time `docker compose down` without volumes, and then
	// removed.
	ActionDestroyed = "destroyed"
	// ActionRestored: the root exists and differs from the checkpoint. Its
	// state, configuration and payload are restored and convergence is
	// forced with `tofu apply -replace` on the wrapper trigger.
	ActionRestored = "restored"
	// ActionRecreated: the checkpoint has the root but the workspace has no
	// applied root (the stack was removed after the checkpoint). Restored
	// and forced like a restored stack.
	ActionRecreated = "recreated"
	// ActionUnchanged: the root already equals the checkpoint, or the
	// checkpoint captured no root for a stack both graphs share.
	ActionUnchanged = "unchanged"
)

// Per-stack statuses.
const (
	StackConverged = "converged"
	StackFailed    = "failed"
	StackPending   = "pending"
	StackSkipped   = "skipped"
)

// Overall statuses.
const (
	StatusConverged  = "converged"
	StatusFailed     = "failed"
	journalRunning   = "in_progress"
	journalConverged = "converged"
)

// ErrorCode classifies a failed rollback.
type ErrorCode string

const (
	// ErrNotConverged: at least one stack could not be destroyed, restored
	// or proven converged.
	ErrNotConverged ErrorCode = "advanced_rollback_not_converged"
	// ErrInvalid: the rollback request or journal is inconsistent.
	ErrInvalid ErrorCode = "advanced_rollback_invalid"
)

// Error is the structured rollback failure.
type Error struct {
	Code   ErrorCode
	Stacks []string
	Detail string
	Err    error
}

func (e *Error) Error() string {
	message := string(e.Code) + ": " + e.Detail
	if len(e.Stacks) > 0 {
		message += " (stacks " + strings.Join(e.Stacks, ", ") + ")"
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

func (e *Error) Unwrap() error { return e.Err }

// Reason returns the structured code of a rollback error.
func Reason(err error) (ErrorCode, bool) {
	var typed *Error
	if !errors.As(err, &typed) {
		return "", false
	}
	return typed.Code, true
}

// Stack is one local stack of a host project in graph run order.
type Stack struct {
	ID          string
	Role        string
	RuntimeRoot string
}

// RootFiles are the captured bytes of one OpenTofu root: the local-backend
// state and configuration inside the root, and the runtime Compose file and
// workload .env one level up when the root owns them.
type RootFiles struct {
	State          []byte
	Config         []byte
	Compose        []byte
	HasCompose     bool
	Environment    []byte
	HasEnvironment bool
}

// TargetRoot is one root of the target checkpoint.
type TargetRoot struct {
	RuntimeRoot string
	Files       RootFiles
}

// Step is one planned per-stack action in execution order.
type Step struct {
	StackID     string `json:"stackId"`
	Role        string `json:"role"`
	RuntimeRoot string `json:"runtimeRoot"`
	Action      string `json:"action"`
}

// StepResult is the recorded outcome of one step.
type StepResult struct {
	Status       string `json:"status"`
	PlanExitCode *int   `json:"planExitCode,omitempty"`
	DurationMS   int64  `json:"durationMs"`
	Detail       string `json:"detail,omitempty"`
	// FilesRestored marks a restored or recreated root whose checkpoint
	// files are written but whose forced apply has not converged yet.
	FilesRestored bool `json:"filesRestored,omitempty"`
}

// Journal is the durable plan and progress of one rollback to one
// checkpoint. A second invocation for the same checkpoint resumes it and
// skips every step already converged.
type Journal struct {
	SchemaVersion        string                `json:"schemaVersion"`
	RollbackID           string                `json:"rollbackId"`
	TargetSnapshotID     string                `json:"targetSnapshotId"`
	ChangeSetID          string                `json:"changeSetId,omitempty"`
	LifecycleOperationID string                `json:"lifecycleOperationId"`
	Status               string                `json:"status"`
	Steps                []Step                `json:"steps"`
	Results              map[string]StepResult `json:"results"`
	CreatedAt            time.Time             `json:"createdAt"`
	UpdatedAt            time.Time             `json:"updatedAt"`
}

// InProgress reports whether the journal belongs to an unfinished rollback.
func (journal Journal) InProgress() bool { return journal.Status == journalRunning }

// StackReport is one stack of the rollback report.
type StackReport struct {
	StackID      string `json:"stackId"`
	Role         string `json:"role"`
	RuntimeRoot  string `json:"runtimeRoot"`
	Action       string `json:"action"`
	Status       string `json:"status"`
	PlanExitCode *int   `json:"planExitCode,omitempty"`
	DurationMS   int64  `json:"durationMs"`
	Detail       string `json:"detail,omitempty"`
}

// Report is the `stackkit.rollback-result/v1` document.
type Report struct {
	SchemaVersion        string        `json:"schemaVersion"`
	RollbackID           string        `json:"rollbackId"`
	TargetSnapshotID     string        `json:"targetSnapshotId"`
	ChangeSetID          string        `json:"changeSetId,omitempty"`
	LifecycleOperationID string        `json:"lifecycleOperationId,omitempty"`
	Resumed              bool          `json:"resumed"`
	Order                []string      `json:"order"`
	Stacks               []StackReport `json:"stacks"`
	// AuthorityRestored: the checkpoint's StackSpec and Inventory were
	// restored through the executor-state recovery path.
	AuthorityRestored bool `json:"authorityRestored"`
	// PriorReleaseExecuted: the checkpoint's release differs from the
	// running one, so its captured executable regenerated and verified.
	PriorReleaseExecuted bool `json:"priorReleaseExecuted"`
	// RuntimeVerified: the native verify of the restored plan passed.
	RuntimeVerified bool `json:"runtimeVerified"`
	// SealedSnapshotID is the executor-state checkpoint sealed after a
	// converged rollback. SealStatus explains an absent one.
	SealedSnapshotID string `json:"sealedSnapshotId,omitempty"`
	SealStatus       string `json:"sealStatus"`
	SealDetail       string `json:"sealDetail,omitempty"`
	Status           string `json:"status"`
}

// Seal statuses.
const (
	SealSealed       = "sealed"
	SealUnsupported  = "unsupported"
	SealFailed       = "failed"
	SealNotAttempted = "not_attempted"
)
