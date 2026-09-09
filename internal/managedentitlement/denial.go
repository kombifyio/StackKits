package managedentitlement

import (
	"github.com/kombifyio/stackkits/internal/actionableerror"
)

// UserGuidance is the human-readable half of FEATURE-ENTITLEMENT-UX-STANDARD §1.4.
type UserGuidance struct {
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	NextSteps []string `json:"next_steps"`
}

// SupportContext is operator/agent handoff for a managed entitlement denial.
type SupportContext struct {
	FeatureSource string `json:"feature_source"`
	CostBearing   bool   `json:"cost_bearing"`
}

// Denial is returned for every failed Evaluate. It is the FEATURE-ENTITLEMENT
// envelope plus the CLI actionable-error contract.
type Denial struct {
	ErrorCode        string
	ReasonCode       string
	Capability       string
	RequiredFeatures []string
	MissingFeatures  []string
	Retryable        bool
	UserGuidance     UserGuidance
	Remediation      string
	SupportContext   SupportContext
}

func (d *Denial) Error() string {
	if d == nil {
		return ErrorCode
	}
	title := d.UserGuidance.Title
	body := d.UserGuidance.Body
	switch {
	case title != "" && body != "":
		return title + ": " + body
	case body != "":
		return body
	case title != "":
		return title
	default:
		return d.ReasonCode
	}
}

// Envelope is the FEATURE-ENTITLEMENT-UX-STANDARD §1.4 object, with CLI
// actionable-error aliases so `--json` consumers keep a parseable contract.
func (d *Denial) Envelope() map[string]any {
	if d == nil {
		d = deny(ReasonSourceUnavailable, CapabilityTenantDeployment, []string{FeatureManagedServerless}, []string{FeatureManagedServerless}).(*Denial)
	}
	nextSteps := append([]string(nil), d.UserGuidance.NextSteps...)
	guidanceLines := make([]string, 0, 1+len(nextSteps))
	if d.UserGuidance.Body != "" {
		guidanceLines = append(guidanceLines, d.UserGuidance.Body)
	}
	guidanceLines = append(guidanceLines, nextSteps...)
	return map[string]any{
		"schemaVersion":     actionableerror.SchemaVersionV1,
		"code":              d.ErrorCode,
		"reasonCode":        d.ReasonCode,
		"message":           d.Error(),
		"userGuidance":      guidanceLines,
		"retryable":         d.Retryable,
		"error_code":        d.ErrorCode,
		"reason_code":       d.ReasonCode,
		"capability":        d.Capability,
		"required_features": append([]string(nil), d.RequiredFeatures...),
		"missing_features":  append([]string(nil), d.MissingFeatures...),
		"user_guidance": map[string]any{
			"title":      d.UserGuidance.Title,
			"body":       d.UserGuidance.Body,
			"next_steps": nextSteps,
		},
		"remediation": d.Remediation,
		"support_context": map[string]any{
			"feature_source": d.SupportContext.FeatureSource,
			"cost_bearing":   d.SupportContext.CostBearing,
		},
	}
}

func deny(reason, capability string, required, missing []string) error {
	title := "Managed StackKits is not available for this account"
	body := "This publisher command talks to kombify-managed infrastructure. The runtime could not prove this account is entitled, so the command stopped before any Admin, backup-controller, or provider call."
	next := []string{
		"Use the standalone OSS StackKits path for account-free local operations.",
		"Ask an account admin to enable managed StackKits, then retry from a host with a wired entitlement source.",
	}
	remediation := "Wire a Flagship/Stripe entitlement adapter as the process Source, or use STACKKIT_ENV=development together with STACKKIT_ALLOW_LOCAL_MANAGED_ENTITLEMENT_E2E=1 for an explicit local-dev bypass. HMAC service tokens are not an entitlement decision."
	switch reason {
	case ReasonCheckFailed:
		title = "Managed StackKits entitlement check failed"
		body = "The entitlement source returned an error, so this cost-bearing command was denied before any side effect."
	case ReasonDenied:
		title = "This account is not entitled to managed StackKits"
		body = "The entitlement source returned a negative decision for this managed capability. The command did not contact kombify infrastructure."
	}
	return &Denial{
		ErrorCode:        ErrorCode,
		ReasonCode:       reason,
		Capability:       capability,
		RequiredFeatures: append([]string(nil), required...),
		MissingFeatures:  append([]string(nil), missing...),
		Retryable:        false,
		UserGuidance: UserGuidance{
			Title:     title,
			Body:      body,
			NextSteps: next,
		},
		Remediation: remediation,
		SupportContext: SupportContext{
			FeatureSource: supportFeatureSource,
			CostBearing:   true,
		},
	}
}
