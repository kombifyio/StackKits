// Package generationartifact binds renderer outputs to one verified
// Architecture v2 ResolvedPlan. It is intentionally independent from the
// legacy StackSpec v1 generation path.
package generationartifact

import "fmt"

// ErrorCode is stable for future CLI/apply adapters. Callers must classify
// errors with errors.As instead of parsing their text.
type ErrorCode string

const (
	ErrInvalidPlan       ErrorCode = "invalid_plan"
	ErrInvalidContract   ErrorCode = "invalid_contract"
	ErrNonCanonical      ErrorCode = "non_canonical"
	ErrHashMismatch      ErrorCode = "hash_mismatch"
	ErrBindingMismatch   ErrorCode = "binding_mismatch"
	ErrInvalidPath       ErrorCode = "invalid_path"
	ErrPathEscape        ErrorCode = "path_escape"
	ErrDuplicateArtifact ErrorCode = "duplicate_artifact"
	ErrArtifactMissing   ErrorCode = "artifact_missing"
	ErrArtifactChanged   ErrorCode = "artifact_changed"
	ErrIncompatible      ErrorCode = "incompatible_component"
	ErrReadinessBlocked  ErrorCode = "execution_readiness_blocked"
	ErrRendererMissing   ErrorCode = "renderer_not_implemented"
	ErrExecutorMissing   ErrorCode = "executor_not_implemented"
	ErrVerifierMissing   ErrorCode = "verifier_not_implemented"
	ErrIO                ErrorCode = "io"
)

// Error identifies a fail-closed plan or generated-artifact gate decision.
type Error struct {
	Code     ErrorCode
	Path     string
	Message  string
	Phase    ExecutionPhase
	Blockers []ReadinessBlocker
	Err      error
}

func (e *Error) Error() string {
	location := ""
	if e.Path != "" {
		location = " at " + e.Path
	}
	if e.Err != nil {
		return fmt.Sprintf("generationartifact %s%s: %s: %v", e.Code, location, e.Message, e.Err)
	}
	return fmt.Sprintf("generationartifact %s%s: %s", e.Code, location, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

func fail(code ErrorCode, path, format string, args ...any) error {
	return &Error{Code: code, Path: path, Message: fmt.Sprintf(format, args...)}
}

func wrap(code ErrorCode, path, message string, err error) error {
	return &Error{Code: code, Path: path, Message: message, Err: err}
}
