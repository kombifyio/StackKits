// Package engine is the canonical render boundary of the StackKits target
// architecture (ADR-0054, ADR-0055). It owns the typed workload Compose
// model, the Stage 1 OpenTofu wrapper around a Compose payload, and the
// generic #AppModuleV3 loader and Compose renderer. It imports no legacy
// generation package (internal/cue, internal/composition, pkg/models), so
// internal/architecturev2renderer can delegate to it without widening its
// production dependency graph.
package engine

import "fmt"

// ErrorCode classifies a fail-closed engine decision.
type ErrorCode string

const (
	// ErrRendererFailure matches architecturev2renderer.ErrRendererFailure so
	// a delegating renderer can keep its stable error code.
	ErrRendererFailure ErrorCode = "renderer_failure"
	// ErrInvalidModule rejects an #AppModuleV3 declaration.
	ErrInvalidModule ErrorCode = "invalid_app_module"
	// ErrUnknownAdapter rejects a reference outside the closed registries.
	ErrUnknownAdapter ErrorCode = "unknown_adapter"
)

// Error identifies one fail-closed engine decision.
type Error struct {
	Code    ErrorCode
	Path    string
	Message string
	Err     error
}

func (e *Error) Error() string {
	location := ""
	if e.Path != "" {
		location = " at " + e.Path
	}
	if e.Err != nil {
		return fmt.Sprintf("engine %s%s: %s: %v", e.Code, location, e.Message, e.Err)
	}
	return fmt.Sprintf("engine %s%s: %s", e.Code, location, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

func fail(code ErrorCode, path, format string, args ...any) error {
	return &Error{Code: code, Path: path, Message: fmt.Sprintf(format, args...)}
}

func wrap(code ErrorCode, path, message string, err error) error {
	return &Error{Code: code, Path: path, Message: message, Err: err}
}
