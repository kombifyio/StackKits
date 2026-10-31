package runtimelease

import (
	"context"
	"errors"
	"fmt"
)

// AuthorityErrorKind classifies failures reported by a caller-owned Runtime
// Lease Authority transport without embedding that transport in this package.
type AuthorityErrorKind string

// Runtime Lease Authority error classes.
const (
	AuthorityErrorTransport AuthorityErrorKind = "transport"
	AuthorityErrorStatus    AuthorityErrorKind = "status"
	AuthorityErrorProtocol  AuthorityErrorKind = "protocol"
)

// AuthorityError reports a failed authority call. It never contains a
// response body. StatusCode is set only for AuthorityErrorStatus.
type AuthorityError struct {
	Operation  string
	Kind       AuthorityErrorKind
	StatusCode int
	Err        error
}

// Error returns a secret-free description of the authority failure.
func (e *AuthorityError) Error() string {
	if e == nil {
		return "runtimelease: authority error"
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("runtimelease: authority %s failed with status %d", e.Operation, e.StatusCode)
	}
	if e.Err != nil {
		return fmt.Sprintf("runtimelease: authority %s %s failure: %v", e.Operation, e.Kind, e.Err)
	}
	return fmt.Sprintf("runtimelease: authority %s %s failure", e.Operation, e.Kind)
}

// Unwrap exposes the underlying transport or protocol error.
func (e *AuthorityError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// FallbackEligible reports whether err is an authority outage for which a
// caller may separately verify an authority-signed cached snapshot. Explicit
// invalid results, 4xx responses, malformed responses, and caller cancellation
// are never eligible. The accepted status codes are 500, 502, 503, and 504.
func FallbackEligible(err error) bool {
	var authorityErr *AuthorityError
	if !errors.As(err, &authorityErr) || authorityErr == nil {
		return false
	}
	switch authorityErr.Kind {
	case AuthorityErrorTransport:
		return !errors.Is(authorityErr.Err, context.Canceled)
	case AuthorityErrorStatus:
		switch authorityErr.StatusCode {
		case 500, 502, 503, 504:
			return true
		default:
			return false
		}
	default:
		return false
	}
}
