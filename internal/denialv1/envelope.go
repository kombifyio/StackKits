// Package denial is the single client-error-envelope/v1 type for cost-bearing
// and entitlement denials. The schema lives in the workspace
// (public StackKits standards/client-error-envelope.v1.schema.json). Callers must
// not declare a parallel envelope; user_guidance is always an object.
package denial

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidEnvelope = errors.New("denial: envelope is not client-error-envelope/v1")
	ErrGuidanceObject  = errors.New("denial: user_guidance must be an object")
)

// UserGuidance is the human-facing part of a denial. It is always a JSON
// object with title, body, and at least one next step.
type UserGuidance struct {
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	NextSteps []string `json:"next_steps"`
}

// Envelope is the machine-readable denial. Required fields match the workspace
// schema: error_code, reason_code, retryable, user_guidance.
type Envelope struct {
	ErrorCode        string         `json:"error_code"`
	ReasonCode       string         `json:"reason_code"`
	RequestID        string         `json:"request_id,omitempty"`
	Capability       string         `json:"capability,omitempty"`
	ProviderID       string         `json:"provider_id,omitempty"`
	RequiredFeatures []string       `json:"required_features,omitempty"`
	MissingFeatures  []string       `json:"missing_features,omitempty"`
	Retryable        bool           `json:"retryable"`
	UserGuidance     UserGuidance   `json:"user_guidance"`
	Remediation      string         `json:"remediation,omitempty"`
	SupportContext   map[string]any `json:"support_context,omitempty"`
}

func (e *Envelope) Error() string {
	if e == nil {
		return "denial: empty envelope"
	}
	return fmt.Sprintf("denied (%s/%s)", e.ErrorCode, e.ReasonCode)
}

// Validate reports whether the envelope satisfies the required schema fields.
func (e *Envelope) Validate() error {
	if e == nil {
		return ErrInvalidEnvelope
	}
	if !codeOK(e.ErrorCode) || !codeOK(e.ReasonCode) {
		return ErrInvalidEnvelope
	}
	if strings.TrimSpace(e.UserGuidance.Title) == "" || strings.TrimSpace(e.UserGuidance.Body) == "" {
		return ErrGuidanceObject
	}
	if len(e.UserGuidance.NextSteps) == 0 {
		return ErrGuidanceObject
	}
	for _, step := range e.UserGuidance.NextSteps {
		if strings.TrimSpace(step) == "" {
			return ErrGuidanceObject
		}
	}
	return nil
}

// MarshalJSON always writes user_guidance as an object.
func (e Envelope) MarshalJSON() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	type wire Envelope
	return json.Marshal(wire(e))
}

// Parse decodes a client-error-envelope/v1 document and fails closed when
// user_guidance is a string or any required field is missing.
func Parse(data []byte) (Envelope, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return Envelope{}, ErrInvalidEnvelope
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return Envelope{}, ErrInvalidEnvelope
	}
	rawGuidance, ok := probe["user_guidance"]
	if !ok || len(bytes.TrimSpace(rawGuidance)) == 0 {
		return Envelope{}, ErrGuidanceObject
	}
	if bytes.TrimSpace(rawGuidance)[0] != '{' {
		return Envelope{}, ErrGuidanceObject
	}
	var envelope Envelope
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return Envelope{}, ErrInvalidEnvelope
	}
	if err := envelope.Validate(); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

func codeOK(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			continue
		case i > 0 && r >= '0' && r <= '9':
			continue
		case i > 0 && (r == '.' || r == '_' || r == '-'):
			continue
		default:
			return false
		}
	}
	return true
}
