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
)

var (
	ErrInvalidEnvelope = errors.New("denial: envelope is not client-error-envelope/v1")
	ErrGuidanceObject  = errors.New("denial: user_guidance must be an object")
)

// UserGuidance is the human-facing part of a denial. It is always a JSON
// object with title, body, and at least one next step.
type UserGuidance struct {
	Title        string        `json:"title"`
	Body         string        `json:"body"`
	NextSteps    []string      `json:"next_steps"`
	Localization *Localization `json:"localization,omitempty"`
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
	Cause            Cause          `json:"cause,omitempty"`
	RetryAfterMS     *int64         `json:"retry_after_ms,omitempty"`
	ResetAt          string         `json:"reset_at,omitempty"`
	Actions          []Action       `json:"actions,omitempty"`
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
	if !bounded(e.UserGuidance.Title, 160) || !bounded(e.UserGuidance.Body, 2000) {
		return ErrGuidanceObject
	}
	if len(e.UserGuidance.NextSteps) == 0 || len(e.UserGuidance.NextSteps) > 8 {
		return ErrGuidanceObject
	}
	for _, step := range e.UserGuidance.NextSteps {
		if !bounded(step, 500) {
			return ErrGuidanceObject
		}
	}
	return e.validateExtensions()
}

// MarshalJSON always writes user_guidance as an object.
func (e Envelope) MarshalJSON() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	type wire Envelope
	if e.modelFailure() {
		// The pointer distinguishes an authoritative zero from unknown. Model
		// failures always carry null for unknown, and [] for no supported action.
		return json.Marshal(struct {
			wire
			RetryAfterMS *int64   `json:"retry_after_ms"`
			Actions      []Action `json:"actions"`
		}{wire(e), e.RetryAfterMS, e.Actions})
	}
	return json.Marshal(wire(e))
}

// parseEnvelope validates only the canonical object; transport aliases are
// checked and removed by Parse before this boundary.
func parseEnvelope(data []byte) (Envelope, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return Envelope{}, ErrInvalidEnvelope
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return Envelope{}, ErrInvalidEnvelope
	}
	if !onlyFields(probe, "error_code", "reason_code", "request_id", "capability", "provider_id", "required_features", "missing_features", "cause", "retry_after_ms", "reset_at", "actions", "retryable", "user_guidance", "remediation", "support_context") {
		return Envelope{}, ErrInvalidEnvelope
	}
	rawGuidance, ok := probe["user_guidance"]
	if !ok || len(bytes.TrimSpace(rawGuidance)) == 0 {
		return Envelope{}, ErrGuidanceObject
	}
	if bytes.TrimSpace(rawGuidance)[0] != '{' {
		return Envelope{}, ErrGuidanceObject
	}
	for _, key := range []string{"error_code", "reason_code", "retryable", "user_guidance"} {
		if _, present := probe[key]; !present {
			return Envelope{}, ErrInvalidEnvelope
		}
	}
	for key, value := range probe {
		if key != "retry_after_ms" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return Envelope{}, ErrInvalidEnvelope
		}
	}
	for _, key := range []string{"request_id", "provider_id", "capability", "reset_at", "remediation", "cause"} {
		if raw, present := probe[key]; present {
			var text string
			if json.Unmarshal(raw, &text) != nil || text == "" {
				return Envelope{}, ErrInvalidEnvelope
			}
		}
	}
	var guidanceProbe map[string]json.RawMessage
	if json.Unmarshal(rawGuidance, &guidanceProbe) != nil || !onlyFields(guidanceProbe, "title", "body", "next_steps", "localization") {
		return Envelope{}, ErrGuidanceObject
	}
	if raw, present := guidanceProbe["localization"]; present {
		var localizationProbe map[string]json.RawMessage
		if json.Unmarshal(raw, &localizationProbe) != nil || localizationProbe == nil || !onlyFields(localizationProbe, "title_message_id", "body_message_id", "next_step_message_ids", "message_params") {
			return Envelope{}, ErrGuidanceObject
		}
		if params, present := localizationProbe["message_params"]; present && bytes.Equal(bytes.TrimSpace(params), []byte("null")) {
			return Envelope{}, ErrGuidanceObject
		}
	}
	var actionProbe []map[string]json.RawMessage
	if raw, present := probe["actions"]; present {
		if json.Unmarshal(raw, &actionProbe) != nil {
			return Envelope{}, ErrInvalidEnvelope
		}
		for _, action := range actionProbe {
			if !onlyFields(action, "kind", "model_id", "provider_id", "endpoint_id") {
				return Envelope{}, ErrInvalidEnvelope
			}
			for _, field := range action {
				var text string
				if json.Unmarshal(field, &text) != nil || text == "" {
					return Envelope{}, ErrInvalidEnvelope
				}
			}
		}
	}
	var envelope Envelope
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return Envelope{}, ErrInvalidEnvelope
	}
	if envelope.modelFailure() {
		if _, present := probe["retry_after_ms"]; !present {
			return Envelope{}, ErrInvalidEnvelope
		}
	}
	if err := envelope.Validate(); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

func codeOK(value string) bool {
	return codePattern.MatchString(value)
}
