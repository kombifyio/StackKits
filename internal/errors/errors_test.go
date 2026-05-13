package errors

import (
	stderrors "errors"
	"fmt"
	"strings"
	"testing"
)

func TestStackKitErrorFormatsContextSuggestionsAndCause(t *testing.T) {
	cause := stderrors.New("docker unavailable")
	err := NewInfrastructureError(
		"docker_not_available",
		"Docker daemon is not accessible",
		WithField("binary", "docker"),
		WithSuggestion("Start Docker Desktop"),
		WithCause(cause),
	)

	if !stderrors.Is(err, cause) {
		t.Fatal("StackKitError should unwrap the cause")
	}

	msg := err.Error()
	for _, want := range []string{
		"[infrastructure:docker_not_available] Docker daemon is not accessible",
		"binary: docker",
		"Start Docker Desktop",
		"Cause: docker unavailable",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("Error() missing %q in:\n%s", want, msg)
		}
	}
}

func TestStackKitErrorRecoverableOnlyWithAutoFix(t *testing.T) {
	err := NewValidationError("bad_spec", "invalid spec")
	err.Recoverable = true
	if err.IsRecoverable() {
		t.Fatal("error without AutoFix must not be recoverable")
	}

	fixed := false
	err = NewValidationError("bad_spec", "invalid spec", WithAutoFix(func() error {
		fixed = true
		return nil
	}))

	if !err.IsRecoverable() {
		t.Fatal("error with AutoFix should be recoverable")
	}
	if err := err.TryAutoFix(); err != nil {
		t.Fatalf("TryAutoFix: %v", err)
	}
	if !fixed {
		t.Fatal("AutoFix function was not called")
	}
}

func TestTryAutoFixWithoutFixReturnsError(t *testing.T) {
	err := NewDeploymentError("apply_failed", "apply failed")
	if fixErr := err.TryAutoFix(); fixErr == nil || !strings.Contains(fixErr.Error(), "no auto-fix available") {
		t.Fatalf("TryAutoFix() = %v, want no auto-fix error", fixErr)
	}
}

func TestErrorHandlerWrapsUnknownErrors(t *testing.T) {
	var seen *StackKitError
	handler := &ErrorHandler{
		OnError: func(err *StackKitError) {
			seen = err
		},
	}

	err := handler.Handle(fmt.Errorf("plain failure"))
	if err == nil {
		t.Fatal("Handle should return an error")
	}
	skErr, ok := err.(*StackKitError)
	if !ok {
		t.Fatalf("returned error type = %T, want *StackKitError", err)
	}
	if skErr.Category != CategoryUnknown || skErr.Code != "unknown_error" {
		t.Fatalf("wrapped error = %#v", skErr)
	}
	if seen != skErr {
		t.Fatal("OnError should receive the wrapped error")
	}
}

func TestErrorHandlerAutoRecoverSuccess(t *testing.T) {
	handler := &ErrorHandler{AutoRecover: true}
	err := NewResourceError("port_conflict", "port in use", WithAutoFix(func() error {
		return nil
	}))

	if got := handler.Handle(err); got != nil {
		t.Fatalf("Handle recoverable error = %v, want nil", got)
	}
}

func TestErrorHandlerCallsFatalHook(t *testing.T) {
	fatalCalled := false
	handler := &ErrorHandler{
		OnFatal: func(err *StackKitError) {
			fatalCalled = true
		},
	}

	err := handler.Handle(NewAuthError("denied", "access denied", WithSeverity(SeverityFatal)))
	if err == nil {
		t.Fatal("fatal error should be returned")
	}
	if !fatalCalled {
		t.Fatal("OnFatal was not called")
	}
}

func TestConvenienceConstructorsSetCategories(t *testing.T) {
	tests := []struct {
		name string
		err  *StackKitError
		want ErrorCategory
	}{
		{"validation", NewValidationError("x", "x"), CategoryValidation},
		{"infrastructure", NewInfrastructureError("x", "x"), CategoryInfrastructure},
		{"deployment", NewDeploymentError("x", "x"), CategoryDeployment},
		{"resource", NewResourceError("x", "x"), CategoryResource},
		{"auth", NewAuthError("x", "x"), CategoryAuth},
		{"dependency", NewDependencyError("x", "x"), CategoryDependency},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Category != tt.want {
				t.Fatalf("Category = %q, want %q", tt.err.Category, tt.want)
			}
			if tt.err.Severity != SeverityError {
				t.Fatalf("Severity = %q, want default %q", tt.err.Severity, SeverityError)
			}
		})
	}
}

func TestCommonErrorsIncludeActionableSuggestions(t *testing.T) {
	tests := []struct {
		name string
		err  *StackKitError
		want string
	}{
		{"port", PortConflictError(80, "nginx"), "Use a different port"},
		{"docker", DockerNotAvailableError(), "Docker Desktop"},
		{"vm", VMNotHealthyError(), "docker compose logs vm"},
		{"verification", DeploymentVerificationError(1, 0), "Docker daemon connectivity"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.err.Error(), tt.want) {
				t.Fatalf("%s error missing suggestion %q:\n%s", tt.name, tt.want, tt.err.Error())
			}
		})
	}
}
