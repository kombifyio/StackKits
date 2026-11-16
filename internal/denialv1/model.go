package denial

import (
	"encoding/json"
	"math"
	"regexp"
	"time"
	"unicode/utf8"
)

type Cause string

const (
	CauseAuthentication       Cause = "authentication"
	CauseAuthorization        Cause = "authorization"
	CauseModelPolicy          Cause = "model_policy"
	CauseFunding              Cause = "funding"
	CauseCapacity             Cause = "capacity"
	CauseConfiguration        Cause = "configuration"
	CauseInfrastructure       Cause = "infrastructure"
	CauseProvider             Cause = "provider"
	CapabilityModelExecutions       = "ai.model-executions"
	CapabilityAgentRuns             = "ai.agent-runs"
)

type ActionKind string

const (
	ActionRetry             ActionKind = "retry"
	ActionSignIn            ActionKind = "sign_in"
	ActionSelectModel       ActionKind = "select_model"
	ActionSelectFreeModel   ActionKind = "select_free_model"
	ActionConfigureProvider ActionKind = "configure_provider"
	ActionUseProviderKey    ActionKind = "use_provider_key"
	ActionUseModelEndpoint  ActionKind = "use_model_endpoint"
	ActionManagePlan        ActionKind = "manage_plan"
	ActionBuyAIBoost        ActionKind = "buy_ai_boost"
	ActionContactSupport    ActionKind = "contact_support"
)

// Action is semantic: clients map it to their own trusted UI, never a served
// executable or arbitrary navigation URL. Validate checks the kind's parameters.
type Action struct {
	Kind       ActionKind `json:"kind"`
	ModelID    string     `json:"model_id,omitempty"`
	ProviderID string     `json:"provider_id,omitempty"`
	EndpointID string     `json:"endpoint_id,omitempty"`
}

type Localization struct {
	TitleMessageID     string         `json:"title_message_id"`
	BodyMessageID      string         `json:"body_message_id"`
	NextStepMessageIDs []string       `json:"next_step_message_ids"`
	MessageParams      map[string]any `json:"message_params,omitempty"`
}

var (
	timestampPattern  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$`)
	codePattern       = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
	messagePattern    = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)+$`)
	paramPattern      = regexp.MustCompile(`^[a-z][a-zA-Z0-9_]*$`)
	capabilityPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)
	featurePattern    = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)
)

func bounded(value string, max int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) > 0 && utf8.RuneCountInString(value) <= max
}

func (a Action) Validate() error {
	model, provider, endpoint := false, false, false
	switch a.Kind {
	case ActionSelectModel:
		model = true
		if a.ModelID == "" {
			return ErrInvalidEnvelope
		}
	case ActionSelectFreeModel:
		model = true // Empty opens a picker restricted to free models.
	case ActionConfigureProvider:
		provider = true
	case ActionUseProviderKey:
		provider = true
		if a.ProviderID == "" {
			return ErrInvalidEnvelope
		}
	case ActionUseModelEndpoint:
		endpoint = true
		if a.EndpointID == "" {
			return ErrInvalidEnvelope
		}
	case ActionRetry, ActionSignIn, ActionManagePlan, ActionBuyAIBoost, ActionContactSupport:
	default:
		return ErrInvalidEnvelope
	}
	if (!model && a.ModelID != "") || (!provider && a.ProviderID != "") || (!endpoint && a.EndpointID != "") {
		return ErrInvalidEnvelope
	}
	for _, value := range []string{a.ModelID, a.ProviderID, a.EndpointID} {
		if value != "" && !bounded(value, 256) {
			return ErrInvalidEnvelope
		}
	}
	return nil
}

func (l *Localization) validate(steps int) error {
	if l == nil {
		return nil
	}
	if !bounded(l.TitleMessageID, 160) || !messagePattern.MatchString(l.TitleMessageID) || !bounded(l.BodyMessageID, 160) || !messagePattern.MatchString(l.BodyMessageID) || len(l.NextStepMessageIDs) != steps || len(l.MessageParams) > 32 {
		return ErrGuidanceObject
	}
	for _, id := range l.NextStepMessageIDs {
		if !bounded(id, 160) || !messagePattern.MatchString(id) {
			return ErrGuidanceObject
		}
	}
	for key, value := range l.MessageParams {
		if !paramPattern.MatchString(key) {
			return ErrGuidanceObject
		}
		switch v := value.(type) {
		case string:
			if !utf8.ValidString(v) || utf8.RuneCountInString(v) > 512 {
				return ErrGuidanceObject
			}
		case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return ErrGuidanceObject
			}
		case float32:
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return ErrGuidanceObject
			}
		case json.Number:
			number, err := v.Float64()
			if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
				return ErrGuidanceObject
			}
		default:
			return ErrGuidanceObject
		}
	}
	return nil
}

func (e Envelope) modelFailure() bool {
	return e.Capability == CapabilityModelExecutions || e.Capability == CapabilityAgentRuns
}

func (e Envelope) validateExtensions() error {
	if e.Cause != "" {
		switch e.Cause {
		case CauseAuthentication, CauseAuthorization, CauseModelPolicy, CauseFunding, CauseCapacity, CauseConfiguration, CauseInfrastructure, CauseProvider:
		default:
			return ErrInvalidEnvelope
		}
	}
	if e.RequestID != "" && !bounded(e.RequestID, 256) {
		return ErrInvalidEnvelope
	}
	if e.ProviderID != "" && !bounded(e.ProviderID, 256) {
		return ErrInvalidEnvelope
	}
	if e.Capability != "" && !capabilityPattern.MatchString(e.Capability) {
		return ErrInvalidEnvelope
	}
	if e.RetryAfterMS != nil && (*e.RetryAfterMS < 0 || *e.RetryAfterMS > 9007199254740991) {
		return ErrInvalidEnvelope
	}
	if !e.validReset() {
		return ErrInvalidEnvelope
	}
	if e.Remediation != "" && !bounded(e.Remediation, 2000) {
		return ErrInvalidEnvelope
	}
	for _, features := range [][]string{e.RequiredFeatures, e.MissingFeatures} {
		seen := map[string]bool{}
		for _, feature := range features {
			if !featurePattern.MatchString(feature) || seen[feature] {
				return ErrInvalidEnvelope
			}
			seen[feature] = true
		}
	}
	if len(e.Actions) > 16 {
		return ErrInvalidEnvelope
	}
	for _, action := range e.Actions {
		if err := action.Validate(); err != nil {
			return err
		}
	}
	if err := e.UserGuidance.Localization.validate(len(e.UserGuidance.NextSteps)); err != nil {
		return err
	}
	if !e.modelFailure() {
		return nil
	}
	if e.Cause == "" || e.RequestID == "" || e.Actions == nil || e.UserGuidance.Localization == nil || e.SupportContext == nil {
		return ErrInvalidEnvelope
	}
	for key, value := range e.SupportContext {
		max := 256
		switch key {
		case "origin", "profile_binding", "product", "workload", "policy_binding", "catalog_binding", "model_id", "agent_id", "provider_id":
		case "route_template":
			max = 512
		case "stage":
			switch value {
			case "facts", "selection", "reservation", "provider", "settlement", "transport", "authentication", "authorization":
			default:
				return ErrInvalidEnvelope
			}
		default:
			return ErrInvalidEnvelope
		}
		text, ok := value.(string)
		if !ok || !bounded(text, max) {
			return ErrInvalidEnvelope
		}
	}
	if _, ok := e.SupportContext["stage"]; !ok {
		return ErrInvalidEnvelope
	}
	return nil
}

func (e Envelope) validReset() bool {
	if e.ResetAt == "" {
		return true
	}
	_, err := time.Parse(time.RFC3339Nano, e.ResetAt)
	return err == nil && timestampPattern.MatchString(e.ResetAt)
}
