package denial

import (
	"bytes"
	"encoding/json"
	"slices"
)

// Parse accepts the canonical envelope and the three running representations
// declared in workspace client-error-transport.v1.schema.json. Aliases are
// validated as their named transport, never admitted as canonical extensions.
func Parse(data []byte) (Envelope, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return Envelope{}, ErrInvalidEnvelope
	}
	if _, stream := fields["denial"]; stream {
		return parseStream(data)
	}
	if raw, http := fields["error"]; http {
		aliases := map[string]json.RawMessage{}
		keys := []string{"error", "resetAt", "alternatives", "topUp"}
		if raw = bytes.TrimSpace(raw); len(raw) > 0 && raw[0] == '"' {
			keys = []string{"error", "reason", "service", "source", "bucket"}
		}
		for _, key := range keys {
			if value, present := fields[key]; present {
				if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					return Envelope{}, ErrInvalidEnvelope
				}
				aliases[key] = value
				delete(fields, key)
			}
		}
		wire, err := json.Marshal(aliases)
		if err != nil {
			return Envelope{}, ErrInvalidEnvelope
		}
		canonical, err := json.Marshal(fields)
		if err != nil {
			return Envelope{}, ErrInvalidEnvelope
		}
		envelope, err := parseEnvelope(canonical)
		if err != nil {
			return Envelope{}, err
		}
		if keys[1] == "reason" {
			err = validateGatewayHTTP(wire, envelope)
		} else {
			err = validateAPIHTTP(wire, envelope)
		}
		if err != nil {
			return Envelope{}, err
		}
		return envelope, nil
	}
	return parseEnvelope(data)
}

func onlyFields(fields map[string]json.RawMessage, names ...string) bool {
	for key := range fields {
		found := false
		for _, name := range names {
			if key == name {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func objectFieldsOnly(raw json.RawMessage, names ...string) bool {
	var fields map[string]json.RawMessage
	return json.Unmarshal(raw, &fields) == nil && fields != nil && onlyFields(fields, names...)
}

func decodeTransport(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return ErrInvalidEnvelope
	}
	return nil
}

type apiHTTPAliases struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	ResetAt      *string `json:"resetAt"`
	Alternatives []struct {
		Route       string          `json:"route"`
		ProviderIDs json.RawMessage `json:"providerIds"`
		EndpointIDs json.RawMessage `json:"endpointIds"`
	} `json:"alternatives"`
	TopUp *struct {
		AddonID string `json:"addonId"`
		Route   string `json:"route"`
	} `json:"topUp"`
}

func validateAPIHTTP(data []byte, envelope Envelope) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || !objectFieldsOnly(fields["error"], "code", "message") {
		return ErrInvalidEnvelope
	}
	var alternatives []map[string]json.RawMessage
	if raw, present := fields["alternatives"]; present {
		if json.Unmarshal(raw, &alternatives) != nil {
			return ErrInvalidEnvelope
		}
		for _, alternative := range alternatives {
			if !onlyFields(alternative, "route", "providerIds", "endpointIds") {
				return ErrInvalidEnvelope
			}
		}
	}
	if raw, present := fields["topUp"]; present && !objectFieldsOnly(raw, "addonId", "route") {
		return ErrInvalidEnvelope
	}
	var aliases apiHTTPAliases
	if decodeTransport(data, &aliases) != nil || !codeOK(aliases.Error.Code) || !bounded(aliases.Error.Message, 2000) || !transportResetOK(aliases.ResetAt) || len(aliases.Alternatives) > 2 || aliases.Error.Code != envelope.ErrorCode || !transportResetAgrees(aliases.ResetAt, envelope) {
		return ErrInvalidEnvelope
	}
	for _, alternative := range aliases.Alternatives {
		switch alternative.Route {
		case "byok":
			if len(alternative.EndpointIDs) != 0 || !transportIDsOK(alternative.ProviderIDs) || !transportActionsAgree(alternative.ProviderIDs, envelope.Actions, ActionUseProviderKey) {
				return ErrInvalidEnvelope
			}
		case "endpoint":
			if len(alternative.ProviderIDs) != 0 || !transportIDsOK(alternative.EndpointIDs) || !transportActionsAgree(alternative.EndpointIDs, envelope.Actions, ActionUseModelEndpoint) {
				return ErrInvalidEnvelope
			}
		default:
			return ErrInvalidEnvelope
		}
	}
	if aliases.TopUp != nil && (aliases.TopUp.AddonID != "ai_boost" || aliases.TopUp.Route != "https://kombify.io/dashboard/billing#addons") {
		return ErrInvalidEnvelope
	}
	if aliases.TopUp != nil && envelope.Actions != nil && !slices.ContainsFunc(envelope.Actions, func(action Action) bool { return action.Kind == ActionBuyAIBoost }) {
		return ErrInvalidEnvelope
	}
	return nil
}

func transportActionsAgree(raw json.RawMessage, actions []Action, kind ActionKind) bool {
	if actions == nil {
		return true
	}
	var ids []string
	if json.Unmarshal(raw, &ids) != nil {
		return false
	}
	for _, id := range ids {
		if !slices.ContainsFunc(actions, func(action Action) bool {
			return action.Kind == kind && ((kind == ActionUseProviderKey && action.ProviderID == id) || (kind == ActionUseModelEndpoint && action.EndpointID == id))
		}) {
			return false
		}
	}
	return true
}

func transportIDsOK(raw json.RawMessage) bool {
	var values []string
	if json.Unmarshal(raw, &values) != nil || len(values) == 0 || len(values) > 64 {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !bounded(value, 256) || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func transportResetOK(reset *string) bool {
	return reset == nil || *reset == "" || (Envelope{ResetAt: *reset}).validReset()
}

func transportResetAgrees(reset *string, envelope Envelope) bool {
	return reset == nil || (!envelope.modelFailure() && envelope.ResetAt == "") || *reset == envelope.ResetAt
}

func validateGatewayHTTP(data []byte, envelope Envelope) error {
	var aliases struct {
		Error   string  `json:"error"`
		Reason  string  `json:"reason"`
		Service string  `json:"service"`
		Source  *string `json:"source"`
		Bucket  *string `json:"bucket"`
	}
	if decodeTransport(data, &aliases) != nil || !bounded(aliases.Error, 256) || !codeOK(aliases.Reason) || !bounded(aliases.Service, 256) || (aliases.Source != nil && *aliases.Source != "gateway_throttle") || (aliases.Bucket != nil && !bounded(*aliases.Bucket, 128)) || aliases.Reason != envelope.ReasonCode {
		return ErrInvalidEnvelope
	}
	return nil
}

func parseStream(data []byte) (Envelope, error) {
	var frame struct {
		Type         string          `json:"type"`
		Code         string          `json:"code"`
		Message      string          `json:"message"`
		Status       int             `json:"status"`
		Denial       json.RawMessage `json:"denial"`
		ErrorCode    *string         `json:"errorCode"`
		ReasonCode   *string         `json:"reasonCode"`
		Retryable    *bool           `json:"retryable"`
		ResetAt      *string         `json:"resetAt"`
		UserGuidance *struct {
			Title     string   `json:"title"`
			Body      string   `json:"body"`
			NextSteps []string `json:"nextSteps"`
		} `json:"userGuidance"`
		Param json.RawMessage `json:"param"`
	}
	if decodeTransport(data, &frame) != nil || (frame.Type != "error" && frame.Type != "RUN_ERROR") || !codeOK(frame.Code) || !bounded(frame.Message, 2000) || frame.Status < 400 || frame.Status > 599 || !transportResetOK(frame.ResetAt) {
		return Envelope{}, ErrInvalidEnvelope
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || !onlyFields(fields, "type", "code", "message", "status", "denial", "errorCode", "reasonCode", "retryable", "resetAt", "userGuidance", "param") {
		return Envelope{}, ErrInvalidEnvelope
	}
	if raw, present := fields["userGuidance"]; present && !objectFieldsOnly(raw, "title", "body", "nextSteps") {
		return Envelope{}, ErrGuidanceObject
	}
	for key, value := range fields {
		if key != "param" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return Envelope{}, ErrInvalidEnvelope
		}
	}
	for _, code := range []*string{frame.ErrorCode, frame.ReasonCode} {
		if code != nil && !codeOK(*code) {
			return Envelope{}, ErrInvalidEnvelope
		}
	}
	if guidance := frame.UserGuidance; guidance != nil {
		if !bounded(guidance.Title, 160) || !bounded(guidance.Body, 2000) || len(guidance.NextSteps) == 0 || len(guidance.NextSteps) > 8 {
			return Envelope{}, ErrGuidanceObject
		}
		for _, step := range guidance.NextSteps {
			if !bounded(step, 500) {
				return Envelope{}, ErrGuidanceObject
			}
		}
	}
	if len(frame.Param) != 0 && !bytes.Equal(bytes.TrimSpace(frame.Param), []byte("null")) {
		return Envelope{}, ErrInvalidEnvelope
	}
	envelope, err := parseEnvelope(frame.Denial)
	if err != nil {
		return Envelope{}, err
	}
	if frame.Code != envelope.ErrorCode || (frame.ErrorCode != nil && *frame.ErrorCode != envelope.ErrorCode) || (frame.ReasonCode != nil && *frame.ReasonCode != envelope.ReasonCode) || (frame.Retryable != nil && *frame.Retryable != envelope.Retryable) || !transportResetAgrees(frame.ResetAt, envelope) {
		return Envelope{}, ErrInvalidEnvelope
	}
	if guidance := frame.UserGuidance; guidance != nil && (guidance.Title != envelope.UserGuidance.Title || guidance.Body != envelope.UserGuidance.Body || !slices.Equal(guidance.NextSteps, envelope.UserGuidance.NextSteps)) {
		return Envelope{}, ErrInvalidEnvelope
	}
	return envelope, nil
}
