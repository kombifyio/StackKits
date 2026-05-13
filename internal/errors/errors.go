// Package errors provides standardized error handling for StackKit operations.
// It implements a hierarchical error classification system that allows
// for proper error propagation, user-friendly messages, and automated recovery.
//
// Error Categories:
//   - ValidationError: Configuration or input validation failures
//   - InfrastructureError: Docker, network, or platform failures
//   - DeploymentError: Deployment-specific failures
//   - ResourceError: Resource availability (ports, memory, etc.)
//   - AuthError: Authentication/authorization failures
//   - DependencyError: External dependency failures
//
// Usage:
//
//	err := errors.NewValidationError("port_conflict", "Port 80 is already in use",
//	    errors.WithField("port", 80),
//	    errors.WithSuggestion("Use a different port in the range 10000-19999"),
//	    errors.WithAutoFix(func() error { return autoAdjustPort(80) }),
//	)
package errors

import (
	"fmt"
	"strings"
)

// ErrorCategory classifies the type of error for appropriate handling
type ErrorCategory string

const (
	CategoryValidation     ErrorCategory = "validation"
	CategoryInfrastructure ErrorCategory = "infrastructure"
	CategoryDeployment     ErrorCategory = "deployment"
	CategoryResource       ErrorCategory = "resource"
	CategoryAuth           ErrorCategory = "auth"
	CategoryDependency     ErrorCategory = "dependency"
	CategoryUnknown        ErrorCategory = "unknown"
)

// Severity indicates how critical the error is
type Severity string

const (
	SeverityFatal   Severity = "fatal"   // Cannot continue, manual intervention required
	SeverityError   Severity = "error"   // Operation failed, can retry or fix
	SeverityWarning Severity = "warning" // Non-critical issue, can proceed
	SeverityInfo    Severity = "info"    // Informational, for logging only
)

// StackKitError is the base error type for all StackKit operations
type StackKitError struct {
	Category    ErrorCategory
	Code        string
	Message     string
	Cause       error
	Fields      map[string]interface{}
	Suggestions []string
	AutoFix     func() error
	Severity    Severity
	Recoverable bool
}

func (e *StackKitError) Error() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[%s:%s] %s", e.Category, e.Code, e.Message))

	if len(e.Fields) > 0 {
		sb.WriteString("\n  Context:")
		for k, v := range e.Fields {
			sb.WriteString(fmt.Sprintf("\n    %s: %v", k, v))
		}
	}

	if len(e.Suggestions) > 0 {
		sb.WriteString("\n  Suggestions:")
		for _, s := range e.Suggestions {
			sb.WriteString(fmt.Sprintf("\n    • %s", s))
		}
	}

	if e.Cause != nil {
		sb.WriteString(fmt.Sprintf("\n  Cause: %v", e.Cause))
	}

	return sb.String()
}

func (e *StackKitError) Unwrap() error {
	return e.Cause
}

// IsRecoverable returns true if the error can be automatically fixed
func (e *StackKitError) IsRecoverable() bool {
	return e.Recoverable && e.AutoFix != nil
}

// TryAutoFix attempts to automatically fix the error
func (e *StackKitError) TryAutoFix() error {
	if e.AutoFix == nil {
		return fmt.Errorf("no auto-fix available for error %s", e.Code)
	}
	return e.AutoFix()
}

// ErrorOption configures a StackKitError
type ErrorOption func(*StackKitError)

// WithField adds a context field to the error
func WithField(key string, value interface{}) ErrorOption {
	return func(e *StackKitError) {
		if e.Fields == nil {
			e.Fields = make(map[string]interface{})
		}
		e.Fields[key] = value
	}
}

// WithSuggestion adds a user-facing suggestion
func WithSuggestion(suggestion string) ErrorOption {
	return func(e *StackKitError) {
		e.Suggestions = append(e.Suggestions, suggestion)
	}
}

// WithAutoFix sets an auto-fix function
func WithAutoFix(fix func() error) ErrorOption {
	return func(e *StackKitError) {
		e.AutoFix = fix
		e.Recoverable = true
	}
}

// WithCause sets the underlying cause
func WithCause(cause error) ErrorOption {
	return func(e *StackKitError) {
		e.Cause = cause
	}
}

// WithSeverity sets the error severity
func WithSeverity(severity Severity) ErrorOption {
	return func(e *StackKitError) {
		e.Severity = severity
	}
}

// New creates a new StackKitError with the given category and code
func New(category ErrorCategory, code string, message string, opts ...ErrorOption) *StackKitError {
	err := &StackKitError{
		Category:    category,
		Code:        code,
		Message:     message,
		Fields:      make(map[string]interface{}),
		Suggestions: []string{},
		Severity:    SeverityError,
		Recoverable: false,
	}
	for _, opt := range opts {
		opt(err)
	}
	return err
}

// Convenience constructors for common error types

func NewValidationError(code string, message string, opts ...ErrorOption) *StackKitError {
	return New(CategoryValidation, code, message, opts...)
}

func NewInfrastructureError(code string, message string, opts ...ErrorOption) *StackKitError {
	return New(CategoryInfrastructure, code, message, opts...)
}

func NewDeploymentError(code string, message string, opts ...ErrorOption) *StackKitError {
	return New(CategoryDeployment, code, message, opts...)
}

func NewResourceError(code string, message string, opts ...ErrorOption) *StackKitError {
	return New(CategoryResource, code, message, opts...)
}

func NewAuthError(code string, message string, opts ...ErrorOption) *StackKitError {
	return New(CategoryAuth, code, message, opts...)
}

func NewDependencyError(code string, message string, opts ...ErrorOption) *StackKitError {
	return New(CategoryDependency, code, message, opts...)
}

// Common error constructors

func PortConflictError(port int, service string) *StackKitError {
	return NewResourceError("port_conflict",
		fmt.Sprintf("Port %d is already in use by %s", port, service),
		WithField("port", port),
		WithField("service", service),
		WithSuggestion(fmt.Sprintf("Use a different port, e.g., %d", port+10000)),
		WithSuggestion("Run with --fix-ports to auto-adjust"),
		WithSuggestion(fmt.Sprintf("Stop the service using port %d", port)),
	)
}

func DockerNotAvailableError() *StackKitError {
	return NewInfrastructureError("docker_not_available",
		"Docker daemon is not accessible",
		WithSuggestion("Ensure Docker Desktop is running"),
		WithSuggestion("Check Docker permissions for your user"),
		WithSuggestion("Try: docker info"),
	)
}

func VMNotHealthyError() *StackKitError {
	return NewInfrastructureError("vm_not_healthy",
		"The Ubuntu VM is not in a healthy state",
		WithSuggestion("Check VM logs: docker compose logs vm"),
		WithSuggestion("Restart the VM: docker compose restart vm"),
		WithSuggestion("Recreate the VM: docker compose down vm && docker compose up -d vm"),
	)
}

func DeploymentVerificationError(hostCount, vmCount int) *StackKitError {
	return NewDeploymentError("verification_failed",
		fmt.Sprintf("Services may not be correctly deployed (host: %d, vm: %d)", hostCount, vmCount),
		WithField("host_containers", hostCount),
		WithField("vm_containers", vmCount),
		WithSuggestion("Check Docker daemon connectivity: docker compose exec vm docker ps"),
		WithSuggestion("Verify DOCKER_HOST is set to tcp://vm:2375"),
	)
}

// ErrorHandler provides centralized error handling with recovery strategies
type ErrorHandler struct {
	AutoRecover bool
	OnError     func(*StackKitError)
	OnFatal     func(*StackKitError)
}

// Handle processes an error according to its severity and recovery options
func (h *ErrorHandler) Handle(err error) error {
	if err == nil {
		return nil
	}

	skErr, ok := err.(*StackKitError)
	if !ok {
		// Wrap non-StackKit errors
		skErr = New(CategoryUnknown, "unknown_error", err.Error())
	}

	// Log the error
	if h.OnError != nil {
		h.OnError(skErr)
	}

	// Try auto-recovery if enabled and error is recoverable
	if h.AutoRecover && skErr.IsRecoverable() {
		fmt.Printf("Attempting automatic recovery for: %s\n", skErr.Code)
		if fixErr := skErr.TryAutoFix(); fixErr == nil {
			fmt.Printf("Successfully recovered from: %s\n", skErr.Code)
			return nil // Recovery successful
		}
	}

	// Handle fatal errors
	if skErr.Severity == SeverityFatal && h.OnFatal != nil {
		h.OnFatal(skErr)
	}

	return skErr
}
