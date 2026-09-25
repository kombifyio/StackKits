// Package hostmaintenance plans and carries out operating-system package
// updates and reboots on one node.
//
// It is the node-side half of host maintenance: Techstack dispatches the typed
// operation, the pinned StackKits CLI executes it here. Every host interaction
// goes through the Host interface so the decisions (what is pending, what is
// held, whether the node may be touched at all) are testable without root and
// on any operating system.
package hostmaintenance

import (
	"errors"
	"time"
)

// SchemaVersion identifies the result document of every host maintenance
// operation.
const SchemaVersion = "stackkit.host-maintenance/v1"

// Operations reported in Result.Operation.
const (
	OperationUpdatesPlan  = "updates.plan"
	OperationUpdatesApply = "updates.apply"
	OperationReboot       = "reboot"
)

// Refusal codes. A refusal is written before any side effect: nothing on the
// host changed.
const (
	CodeUnsupportedPackageManager   = "unsupported_package_manager"
	CodeUnsupportedOS               = "unsupported_os"
	CodePlanStale                   = "plan_stale"
	CodePackageManagerBusy          = "package_manager_busy"
	CodeControlPlaneHost            = "control_plane_host"
	CodeUnattendedRebootUnsupported = "unattended_reboot_unsupported"
	CodeDpkgBroken                  = "dpkg_broken"
)

// Failure codes. A failure happened while executing; for apply it may have
// changed some packages, which Result.Changes records.
const (
	FailureHostProbe          = "host_probe_failed"
	FailureRefresh            = "package_index_refresh_failed"
	FailureSimulation         = "simulation_failed"
	FailureInstallSetRejected = "install_set_rejected"
	FailureInstall            = "install_failed"
	FailureSchedule           = "reboot_schedule_failed"
	// FailureWaitExpired accompanies OutcomeRunning: the CLI stopped waiting,
	// the update unit did not stop.
	FailureWaitExpired = "apply_wait_expired"
)

// Apply outcomes.
const (
	OutcomeApplied = "applied"
	OutcomeNoop    = "noop"
	OutcomeFailed  = "failed"
	// OutcomeRunning means the CLI stopped waiting while the update unit keeps
	// running. dpkg is never interrupted by a CLI or agent timeout.
	OutcomeRunning = "running"
)

// Bounded durations. The plan bound covers the whole command; the apply bound
// is the CLI's wait for the update unit, not a limit on dpkg.
const (
	PlanTimeout      = 3 * time.Minute
	ApplyWaitTimeout = 20 * time.Minute
	MinRebootDelay   = time.Second
	MaxRebootDelay   = 5 * time.Minute
	// RebootGuardMaxWait bounds how long the reboot guard waits at fire time
	// for a package operation to finish. When it expires the reboot is
	// abandoned, never forced; boot_id stays the same.
	RebootGuardMaxWait = 15 * time.Minute
)

// Result is the stackkit.host-maintenance/v1 document.
type Result struct {
	SchemaVersion  string    `json:"schema_version"`
	Operation      string    `json:"operation"`
	ObservedAt     time.Time `json:"observed_at"`
	OS             *OSInfo   `json:"os,omitempty"`
	PackageManager string    `json:"package_manager,omitempty"`

	// Plan fields; apply repeats them from its re-simulation.
	PlanDigest    string    `json:"plan_digest,omitempty"`
	PendingCount  *int      `json:"pending_count,omitempty"`
	SecurityCount *int      `json:"security_count,omitempty"`
	Packages      []Package `json:"packages,omitempty"`
	Held          []Package `json:"held,omitempty"`
	KeptBack      []string  `json:"kept_back,omitempty"`
	HoldScope     string    `json:"hold_scope,omitempty"`
	RebootLikely  *bool     `json:"reboot_likely,omitempty"`
	DpkgProblems  []string  `json:"dpkg_problems,omitempty"`

	// Apply fields.
	Outcome                string    `json:"outcome,omitempty"`
	Unit                   string    `json:"unit,omitempty"`
	Changes                []Change  `json:"changes,omitempty"`
	RebootRequired         *bool     `json:"reboot_required,omitempty"`
	RebootRequiredPackages []string  `json:"reboot_required_packages,omitempty"`
	AutoMarksNotRestored   []string  `json:"auto_marks_not_restored,omitempty"`
	StartedAt              time.Time `json:"started_at,omitzero"`
	FinishedAt             time.Time `json:"finished_at,omitzero"`

	// Reboot fields.
	Scheduled    *bool     `json:"scheduled,omitempty"`
	BootIDBefore string    `json:"boot_id_before,omitempty"`
	ScheduledAt  time.Time `json:"scheduled_at,omitzero"`
	DelaySeconds int       `json:"delay_seconds,omitempty"`

	Refusal *Problem `json:"refusal,omitempty"`
	Failure *Problem `json:"failure,omitempty"`
}

// OSInfo is what /etc/os-release reported.
type OSInfo struct {
	ID         string `json:"id"`
	VersionID  string `json:"version_id,omitempty"`
	PrettyName string `json:"pretty_name,omitempty"`
}

// Package is one pending upgrade.
type Package struct {
	Name     string `json:"name"`
	From     string `json:"from"`
	To       string `json:"to"`
	Security bool   `json:"security"`
}

// Change is one package's installed version before and after apply. An empty
// version means the package was not installed.
type Change struct {
	Name   string `json:"name"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// Problem is a refusal or failure with a stable code. Message is diagnostic
// and must not be used as a policy input.
type Problem struct {
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Guidance []string `json:"guidance,omitempty"`
}

// RefusalError is returned when an operation refused before any side effect.
type RefusalError struct{ Problem Problem }

func (e *RefusalError) Error() string { return e.Problem.Code + ": " + e.Problem.Message }

// FailureError is returned when an operation failed while executing.
type FailureError struct{ Problem Problem }

func (e *FailureError) Error() string { return e.Problem.Code + ": " + e.Problem.Message }

// ErrStillRunning is returned when apply stopped waiting for a unit that is
// still running.
var ErrStillRunning = errors.New("host update is still running in its systemd unit")

func refuse(result *Result, code, message string, guidance ...string) error {
	problem := Problem{Code: code, Message: message, Guidance: guidance}
	result.Refusal = &problem
	return &RefusalError{Problem: problem}
}

func fail(result *Result, code, message string, guidance ...string) error {
	problem := Problem{Code: code, Message: message, Guidance: guidance}
	result.Failure = &problem
	if result.Operation == OperationUpdatesApply && result.Outcome == "" {
		result.Outcome = OutcomeFailed
	}
	return &FailureError{Problem: problem}
}

func intPtr(value int) *int    { return &value }
func boolPtr(value bool) *bool { return &value }
