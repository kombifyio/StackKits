package actionableerror

import "errors"

type diagnosticError struct {
	detail Contract
	cause  error
}

func (e *diagnosticError) Error() string             { return e.detail.Message }
func (e *diagnosticError) Unwrap() error             { return e.cause }
func (e *diagnosticError) ActionableError() Contract { return e.detail }
func (e *diagnosticError) ActionableStatus() string  { return "failed" }

// WithDiagnostic attaches a known condition at its construction boundary. The
// original error remains available for evidence and errors.Is/As; public text
// contains only the authority-owned cause.
func WithDiagnostic(reason string, cause error) error {
	if cause == nil {
		return nil
	}
	detail, ok := Diagnostic(reason)
	if !ok {
		return cause
	}
	return &diagnosticError{detail: detail, cause: cause}
}

// FromError preserves typed recovery and denial contracts, including external
// RetryAfter. Callers still own redaction and the transport's outcome status.
func FromError(err error) (Contract, bool) {
	var known interface{ ActionableError() Contract }
	if errors.As(err, &known) {
		detail := known.ActionableError()
		if detail.Validate() == nil {
			return detail, true
		}
	}
	return Contract{}, false
}
