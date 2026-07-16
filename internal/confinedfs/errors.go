// Package confinedfs provides held-root, root-relative filesystem operations.
// It deliberately accepts only portable relative paths and plain directories
// or regular files at governed boundaries.
package confinedfs

import (
	"errors"
	"fmt"
)

// ErrorCode classifies a fail-closed confined filesystem decision.
type ErrorCode string

const (
	ErrInvalidPath         ErrorCode = "invalid_path"
	ErrUnsafeEntry         ErrorCode = "unsafe_entry"
	ErrRootChanged         ErrorCode = "root_identity_changed"
	ErrIdentityUnsupported ErrorCode = "identity_unsupported"
	ErrClosed              ErrorCode = "root_closed"
	ErrIO                  ErrorCode = "io"
)

// Error is returned for every package-level validation or I/O failure.
// Installed is set only when an atomic write completed its rename before a
// later durability or root-identity check failed.
type Error struct {
	Code      ErrorCode
	Op        string
	Path      string
	Message   string
	Err       error
	Installed bool
}

func (e *Error) Error() string {
	location := ""
	if e.Path != "" {
		location = " at " + e.Path
	}
	if e.Err != nil {
		return fmt.Sprintf("confinedfs %s %s%s: %s: %v", e.Code, e.Op, location, e.Message, e.Err)
	}
	return fmt.Sprintf("confinedfs %s %s%s: %s", e.Code, e.Op, location, e.Message)
}

// Unwrap exposes the underlying operating-system error.
func (e *Error) Unwrap() error { return e.Err }

func fail(code ErrorCode, op, valuePath, format string, args ...any) error {
	return &Error{Code: code, Op: op, Path: valuePath, Message: fmt.Sprintf(format, args...)}
}

func wrap(code ErrorCode, op, valuePath, message string, err error) error {
	if err == nil {
		return nil
	}
	var confined *Error
	if errors.As(err, &confined) {
		return err
	}
	return &Error{Code: code, Op: op, Path: valuePath, Message: message, Err: err}
}

func markInstalled(err error) error {
	if err == nil {
		return nil
	}
	var confined *Error
	if errors.As(err, &confined) {
		copy := *confined
		copy.Installed = true
		return &copy
	}
	return &Error{Code: ErrIO, Op: "atomic-write", Message: "post-install operation failed", Err: err, Installed: true}
}
