package hostsecurity

import (
	"time"
)

const (
	// EvidenceSchemaVersion identifies the host security evidence document.
	EvidenceSchemaVersion = "stackkit.host-security-evidence/v1"
	// RepairSchemaVersion identifies the repair plan and record document.
	RepairSchemaVersion = "stackkit.host-security-repair/v1"
	// ExceptionsSchemaVersion identifies the owner-controlled exceptions file.
	ExceptionsSchemaVersion = "stackkit.host-security-exceptions/v1"
	// BaselineVersion is the version of the controls and expected values this
	// package verifies. Consumers compare it to know which baseline an
	// observation was judged against.
	BaselineVersion = "stackkit.host-security-baseline/1.0.0"
)

// State is the outcome of judging one control or the whole host.
type State string

const (
	// StateCompliant means the control was observed and matches the baseline.
	StateCompliant State = "compliant"
	// StateDrifted means the control was observed and differs from the baseline.
	StateDrifted State = "drifted"
	// StateUnknown means the control could not be observed or the observation
	// is no longer fresh. It is never compliant.
	StateUnknown State = "unknown"
	// StateException means the control is drifted but the owner approved the
	// deviation with an unexpired, attributed exception.
	StateException State = "exception"
)

// Mode is the lifecycle mode the evidence was produced under.
type Mode string

const (
	// ModeStandard is the account-free standalone StackKits CLI.
	ModeStandard Mode = "standard"
	// ModeAdvanced is a deployment managed by Techstack through the pinned CLI.
	ModeAdvanced Mode = "advanced"
)

// SiteKind selects which site-specific expectations apply.
type SiteKind string

const (
	SiteHome    SiteKind = "home"
	SiteCloud   SiteKind = "cloud"
	SiteUnknown SiteKind = "unknown"
)

// Control identifiers. They are stable: Techstack keys findings on them.
const (
	ControlFirewall          = "firewall.default_inbound"
	ControlSSHPassword       = "ssh.password_authentication"
	ControlSSHRootLogin      = "ssh.root_login"
	ControlSSHPort           = "ssh.port"
	ControlBruteForce        = "bruteforce.fail2ban"
	ControlUnattended        = "updates.unattended_upgrades"
	ControlPendingSecurity   = "updates.pending_security"
	ControlRebootRequired    = "updates.reboot_required"
	ControlSysctl            = "kernel.sysctl"
	ControlListeners         = "exposure.listeners"
	ControlPublishedPorts    = "exposure.published_ports"
	ControlCertificateExpiry = "tls.certificate_expiry"
)

// enforcedControls are the controls the baseline itself enforces. They gate an
// apply. The remaining controls are continuous evidence (patch lag, pending
// reboot, exposure, certificates) that no apply can make true by itself.
var enforcedControls = []string{
	ControlFirewall, ControlSSHPassword, ControlSSHRootLogin, ControlSSHPort,
	ControlBruteForce, ControlUnattended, ControlSysctl,
}

// EnforcedControls lists the control IDs the baseline enforces.
func EnforcedControls() []string { return append([]string(nil), enforcedControls...) }

// ReasonNoAuthorizedKey marks ssh.password_authentication drift that cannot be
// repaired yet because disabling password logins would lock the owner out: no
// account (or not the account in use) holds an authorized SSH key.
const ReasonNoAuthorizedKey = "no_authorized_key"

// ReasonExposedPublishedPort marks a container-published port bound beyond
// loopback that is not a declared service. The host's input firewall does not
// filter such a port: Docker forwards it after DNAT, so it never reaches the
// input hook.
const ReasonExposedPublishedPort = "exposed_published_port"

// RemediationCapability says who can restore a control.
type RemediationCapability string

const (
	// RemediationAutomatic means `stackkit host security repair --apply` can
	// restore the control without cutting the management path.
	RemediationAutomatic RemediationCapability = "automatic"
	// RemediationManual means another StackKits command or the owner must act.
	RemediationManual RemediationCapability = "manual"
	// RemediationNone means nothing to restore.
	RemediationNone RemediationCapability = "none"
)

// Remediation describes how a drifted control is restored.
type Remediation struct {
	Capability RemediationCapability `json:"capability"`
	Action     string                `json:"action,omitempty"`
}

// AppliedException names the owner decision that turned a drifted control into
// an approved exception.
type AppliedException struct {
	Owner     string    `json:"owner"`
	Reason    string    `json:"reason"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Control is the judged state of one baseline control.
type Control struct {
	ID          string            `json:"id"`
	State       State             `json:"state"`
	Expected    string            `json:"expected"`
	Observed    string            `json:"observed"`
	ObservedAt  time.Time         `json:"observed_at"`
	Reason      string            `json:"reason,omitempty"`
	ReasonCode  string            `json:"reason_code,omitempty"`
	Remediation Remediation       `json:"remediation"`
	Exception   *AppliedException `json:"exception,omitempty"`
}

// ExceptionStatus says what became of one declared exception.
type ExceptionStatus string

const (
	ExceptionActive  ExceptionStatus = "active"
	ExceptionUnused  ExceptionStatus = "unused"
	ExceptionExpired ExceptionStatus = "expired"
	ExceptionInvalid ExceptionStatus = "invalid"
)

// ExceptionRecord reports one declared exception and whether it applied, so a
// deviation the owner approved stays visible.
type ExceptionRecord struct {
	Control   string          `json:"control"`
	Owner     string          `json:"owner,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	ExpiresAt time.Time       `json:"expires_at,omitzero"`
	Status    ExceptionStatus `json:"status"`
	Detail    string          `json:"detail,omitempty"`
}

// Evidence is the stackkit.host-security-evidence/v1 document.
type Evidence struct {
	SchemaVersion   string            `json:"schema_version"`
	Kit             string            `json:"kit,omitempty"`
	Mode            Mode              `json:"mode"`
	SiteKind        SiteKind          `json:"site_kind"`
	BaselineVersion string            `json:"baseline_version"`
	NodeRef         string            `json:"node_ref,omitempty"`
	PlanHash        string            `json:"plan_hash,omitempty"`
	ObservedAt      time.Time         `json:"observed_at"`
	ExpiresAt       time.Time         `json:"expires_at"`
	FreshnessSecs   int               `json:"freshness_budget_seconds"`
	Overall         State             `json:"overall"`
	Controls        []Control         `json:"controls"`
	Exceptions      []ExceptionRecord `json:"exceptions,omitempty"`
	Notices         []string          `json:"notices,omitempty"`
}

// Judge returns the overall state of the evidence at the given time. It never
// trusts the stored Overall field: it is recomputed from the controls, and
// evidence past its expiry is unknown regardless of what it once said.
func (e Evidence) Judge(now time.Time) State {
	if e.SchemaVersion != EvidenceSchemaVersion || e.ExpiresAt.IsZero() || !now.Before(e.ExpiresAt) {
		return StateUnknown
	}
	return overall(e.Controls)
}

// AtTime returns the evidence as it must be read at the given time: once past
// its freshness budget every control becomes unknown, because a measurement
// that old proves nothing about the host now.
func (e Evidence) AtTime(now time.Time) Evidence {
	if e.fresh(now) {
		e.Overall = overall(e.Controls)
		return e
	}
	stale := make([]Control, len(e.Controls))
	for i, control := range e.Controls {
		control.State = StateUnknown
		control.Exception = nil
		control.Reason = "evidence_stale: observation expired at " + e.ExpiresAt.UTC().Format(time.RFC3339)
		stale[i] = control
	}
	e.Controls = stale
	e.Overall = StateUnknown
	return e
}

func (e Evidence) fresh(now time.Time) bool {
	return e.SchemaVersion == EvidenceSchemaVersion && !e.ExpiresAt.IsZero() && now.Before(e.ExpiresAt)
}

// overall folds control states. Drift outranks unknown, unknown outranks an
// approved exception, and only a host with nothing but compliant controls is
// compliant. An empty control list proves nothing and is unknown.
func overall(controls []Control) State {
	if len(controls) == 0 {
		return StateUnknown
	}
	result := StateCompliant
	for _, control := range controls {
		switch control.State {
		case StateDrifted:
			return StateDrifted
		case StateCompliant:
		case StateException:
			if result == StateCompliant {
				result = StateException
			}
		default:
			result = StateUnknown
		}
	}
	return result
}
