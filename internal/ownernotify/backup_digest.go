package ownernotify

import (
	"fmt"
	"strings"
	"time"
)

const (
	// EventBackupFailed reports a backup that cannot currently protect data.
	EventBackupFailed = "backup-failed"
	// EventRestoreVerificationOverdue reports a missing or too old staged
	// restore proof while snapshots are otherwise healthy.
	EventRestoreVerificationOverdue = "backup-restore-verification-overdue"
	// EventBackupDigest is the opt-in "all is well" backup summary.
	EventBackupDigest = "backup-digest"
)

// BackupDigestInput carries the facts the backup digest reports. It holds
// states and ages only: no repository location, path or credential.
type BackupDigestInput struct {
	// Configuration and Schedule are the binding states `stackkit status`
	// reports (not-configured, bound, enabled, stale, unavailable, ...).
	Configuration string
	Schedule      string
	// RepositoryReady is false when the repository status could not be read
	// or reports not ready; RepositoryProblem names why without a location.
	RepositoryReady   bool
	RepositoryProblem string
	// Snapshot and StagedRestore are evidence states (recorded, unverified,
	// clock-skew) with their age when recorded.
	SnapshotState         string
	SnapshotAgeSeconds    *int64
	StagedRestoreState    string
	StagedRestoreAge      *int64
	MaxRestoreEvidenceAge time.Duration
	// ObjectivesBreached and ObjectivesUnverified count recovery objectives
	// whose data-loss check is breached or unverified.
	ObjectivesBreached   int
	ObjectivesUnverified int
	// Attention carries the exact remedy commands status names.
	Attention []string
}

// ComposeBackupDigest selects the event and writes the owner message for the
// backup digest recipe. A failure outranks an overdue restore proof, which
// outranks the plain digest, so the owner's event allowlist decides whether a
// healthy week produces a message at all.
func ComposeBackupDigest(input BackupDigestInput) Message {
	var problems []string
	if input.Configuration != "bound" {
		problems = append(problems, "backup configuration is "+orUnknown(input.Configuration))
	}
	if input.Schedule != "enabled" {
		problems = append(problems, "backup schedule is "+orUnknown(input.Schedule))
	}
	if !input.RepositoryReady {
		reason := "backup repository is not ready"
		if strings.TrimSpace(input.RepositoryProblem) != "" {
			reason += ": " + input.RepositoryProblem
		}
		problems = append(problems, reason)
	}
	if input.SnapshotState != "recorded" {
		problems = append(problems, "no current snapshot is recorded ("+orUnknown(input.SnapshotState)+")")
	}
	if input.ObjectivesBreached > 0 {
		problems = append(problems, fmt.Sprintf("%d recovery objective(s) breached", input.ObjectivesBreached))
	}

	restoreOverdue := input.StagedRestoreState != "recorded" || input.StagedRestoreAge == nil ||
		(input.MaxRestoreEvidenceAge > 0 && time.Duration(*input.StagedRestoreAge)*time.Second > input.MaxRestoreEvidenceAge)

	facts := []string{
		"Configuration: " + orUnknown(input.Configuration),
		"Schedule: " + orUnknown(input.Schedule),
		"Repository: " + map[bool]string{true: "ready", false: "not ready"}[input.RepositoryReady],
		"Last snapshot: " + describeEvidence(input.SnapshotState, input.SnapshotAgeSeconds),
		"Last restore check: " + describeEvidence(input.StagedRestoreState, input.StagedRestoreAge) + limitSuffix(input.MaxRestoreEvidenceAge),
		fmt.Sprintf("Recovery objectives: %d breached, %d unverified", input.ObjectivesBreached, input.ObjectivesUnverified),
	}

	message := Message{}
	switch {
	case len(problems) > 0:
		message.Event, message.Title, message.Priority = EventBackupFailed, "StackKit backup needs attention", 4
		message.Tags = []string{"warning"}
		facts = append([]string{"Problem: " + strings.Join(problems, "; ")}, facts...)
	case restoreOverdue:
		message.Event, message.Title, message.Priority = EventRestoreVerificationOverdue, "StackKit restore check is overdue", 3
		message.Tags = []string{"hourglass"}
	default:
		message.Event, message.Title, message.Priority = EventBackupDigest, "StackKit backup digest", 2
		message.Tags = []string{"floppy_disk"}
	}
	for _, step := range input.Attention {
		if strings.TrimSpace(step) != "" {
			facts = append(facts, "Next step: "+step)
		}
	}
	message.Body = strings.Join(facts, "\n")
	return message
}

func describeEvidence(state string, ageSeconds *int64) string {
	if state != "recorded" || ageSeconds == nil {
		return orUnknown(state)
	}
	return humanAge(time.Duration(*ageSeconds)*time.Second) + " ago"
}

func limitSuffix(limit time.Duration) string {
	if limit <= 0 {
		return ""
	}
	return " (limit " + humanAge(limit) + ")"
}

func humanAge(age time.Duration) string {
	switch {
	case age < time.Hour:
		return fmt.Sprintf("%dm", int(age.Minutes()))
	case age < 48*time.Hour:
		return fmt.Sprintf("%dh", int(age.Hours()))
	default:
		return fmt.Sprintf("%dd", int(age.Hours()/24))
	}
}

func orUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}
