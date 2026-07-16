// Package architecturev2 is the single integration boundary from StackSpec
// documents to the governed Architecture v2 ResolvedPlan compiler.
package architecturev2

import (
	"fmt"

	"github.com/kombifyio/stackkits/internal/stackspecmigration"
)

// ErrorCode is a stable adapter-facing classification. CLI and API callers
// can map it without parsing compiler or CUE diagnostics.
type ErrorCode string

const (
	ErrInvalidStackSpec        ErrorCode = "invalid_stackspec"
	ErrInvalidInventory        ErrorCode = "invalid_inventory"
	ErrMigrationRequired       ErrorCode = "migration_required"
	ErrMigrationBlocked        ErrorCode = "migration_blocked"
	ErrAuthorityLoad           ErrorCode = "authority_load"
	ErrResolveFailed           ErrorCode = "resolve_failed"
	ErrGenerationAuthorization ErrorCode = "generation_authorization_failed"
	ErrRequestTooLarge         ErrorCode = "request_too_large"
	ErrUnsupportedMedia        ErrorCode = "unsupported_media_type"
	ErrResolveBusy             ErrorCode = "resolve_busy"
)

// ResolveError preserves the underlying diagnostic and, for v1 input, the
// mandatory structured migration report. Report is never populated for a v2
// compiler failure.
type ResolveError struct {
	Code    ErrorCode                  `json:"code"`
	Message string                     `json:"message"`
	Report  *stackspecmigration.Report `json:"migrationReport,omitempty"`
	Cause   error                      `json:"-"`
}

func (e *ResolveError) Error() string {
	if e == nil {
		return "Architecture v2 resolution failed"
	}
	if e.Message != "" {
		return fmt.Sprintf("architecturev2 %s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("architecturev2 %s", e.Code)
}

// Unwrap keeps errors.As support for stackspecmigration and resolvedplan
// callers while the stable Code remains the public adapter contract.
func (e *ResolveError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func resolveError(code ErrorCode, message string, cause error) error {
	return &ResolveError{Code: code, Message: message, Cause: cause}
}
