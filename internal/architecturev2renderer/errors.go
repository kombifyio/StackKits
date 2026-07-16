// Package architecturev2renderer renders only governed Architecture v2
// ResolvedPlans. It has no compatibility path into the StackSpec v1 model or
// generator.
package architecturev2renderer

import "fmt"

// ErrorCode is stable across renderer and managed-output adapters. Callers
// should classify errors with errors.As instead of parsing messages.
type ErrorCode string

const (
	ErrAuthorization       ErrorCode = "authorization_required"
	ErrInvalidPlan         ErrorCode = "invalid_render_plan"
	ErrInvalidPath         ErrorCode = "invalid_render_path"
	ErrDuplicate           ErrorCode = "duplicate_render_contract"
	ErrUnknownRenderer     ErrorCode = "unknown_renderer"
	ErrRendererFailure     ErrorCode = "renderer_failure"
	ErrUndeclaredOutput    ErrorCode = "undeclared_render_output"
	ErrMissingOutput       ErrorCode = "missing_render_output"
	ErrOutputChanged       ErrorCode = "render_output_changed"
	ErrUnsafeOutputRoot    ErrorCode = "unsafe_output_root"
	ErrOutputTransaction   ErrorCode = "output_transaction_failed"
	ErrTransactionCleanup  ErrorCode = "output_transaction_cleanup_failed"
	ErrTransactionRollback ErrorCode = "output_transaction_rollback_failed"
)

// Error identifies one fail-closed renderer or managed-output decision.
type Error struct {
	Code    ErrorCode
	Path    string
	Message string
	Err     error
	// Committed is true only when the managed output swap and post-swap
	// verification completed before a later transaction-cleanup failure. A
	// caller must not interpret such an error as a rolled-back installation.
	Committed bool
}

func (e *Error) Error() string {
	location := ""
	if e.Path != "" {
		location = " at " + e.Path
	}
	if e.Err != nil {
		return fmt.Sprintf("architecturev2renderer %s%s: %s: %v", e.Code, location, e.Message, e.Err)
	}
	return fmt.Sprintf("architecturev2renderer %s%s: %s", e.Code, location, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

func fail(code ErrorCode, path, format string, args ...any) error {
	return &Error{Code: code, Path: path, Message: fmt.Sprintf(format, args...)}
}

func wrap(code ErrorCode, path, message string, err error) error {
	return &Error{Code: code, Path: path, Message: message, Err: err}
}
