package commands

import (
	"context"
	"errors"
	"os"
	"reflect"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/localbackupschedule"
)

// architectureV2BackupStatus is the additive `backup` projection of
// `stackkit status`. It answers whether the local backup configuration and
// the Owner-approved schedule still bind the current Apply, without touching
// Kopia. Every re-apply (a CLI upgrade included) re-signs Plan and Apply; a
// configuration or schedule approval made for the earlier Apply refuses every
// later backup command and pauses the timer, so status names the exact
// remedy instead of letting scheduled snapshots stop silently.
type architectureV2BackupStatus struct {
	APIVersion string `json:"apiVersion"`
	// Configuration: not-configured, bound, stale or unavailable.
	Configuration string `json:"configuration"`
	// Schedule: not-configured, prepared, enabled, disabled, stale or
	// unavailable.
	Schedule string `json:"schedule"`
	// Attention carries the exact commands that resolve a stale state.
	Attention []string `json:"attention,omitempty"`
	// Reason explains an unavailable axis; never an owner secret.
	Reason string `json:"reason,omitempty"`
}

const (
	architectureV2BackupStatusAPIVersion = "stackkit.backup-binding-status/v1"
	backupBindingNotConfigured           = "not-configured"
	backupBindingBound                   = "bound"
	backupBindingStale                   = "stale"
	backupBindingUnavailable             = "unavailable"
)

// readArchitectureV2BackupStatus never makes status unavailable: a workspace
// without backup authority (no Apply yet, or a kit without a local Kopia
// policy) reports nil unless a configuration or schedule record exists that
// the owner should know about.
func readArchitectureV2BackupStatus(ctx context.Context, workspace string) *architectureV2BackupStatus {
	if ctx == nil {
		ctx = context.Background()
	}
	authority, authorityErr := inspectNativeV2BackupAuthority(ctx, workspace, specFile)
	schedule, scheduleErr := localbackupschedule.LoadAuthorization(workspace)
	return buildArchitectureV2BackupStatus(ctx, workspace, authority, authorityErr, schedule, scheduleErr)
}

func buildArchitectureV2BackupStatus(
	ctx context.Context,
	workspace string,
	authority nativeV2BackupAuthority,
	authorityErr error,
	schedule localbackupschedule.Authorization,
	scheduleErr error,
) *architectureV2BackupStatus {
	result := &architectureV2BackupStatus{
		APIVersion:    architectureV2BackupStatusAPIVersion,
		Configuration: backupBindingUnavailable,
		Schedule:      backupBindingUnavailable,
	}
	hasSchedule := scheduleErr == nil
	if authorityErr != nil {
		if !hasSchedule && errors.Is(scheduleErr, os.ErrNotExist) {
			return nil
		}
		result.Reason = "backup authority unavailable: " + authorityErr.Error()
	} else {
		input := backuplifecycle.StatusInput{
			OwnerRef: authority.OwnerRef, AuthorityRef: authority.AuthorityRef,
			Lineage: authority.Lineage, PolicyArtifact: append([]byte(nil), authority.PolicyArtifact...),
		}
		var stale *backuplifecycle.ConfigurationBindingError
		switch _, err := backuplifecycle.InspectConfigurationBinding(authority.WorkspaceRoot, input); {
		case err == nil:
			result.Configuration = backupBindingBound
		case errors.Is(err, os.ErrNotExist):
			result.Configuration = backupBindingNotConfigured
		case errors.As(err, &stale):
			result.Configuration = backupBindingStale
			result.Attention = append(result.Attention, stale.Guidance()[0])
		default:
			result.Reason = "backup configuration unavailable: " + err.Error()
		}
	}
	switch {
	case hasSchedule:
		result.Schedule = schedule.State
		if schedule.State != "enabled" || authorityErr != nil {
			break
		}
		if !scheduleBindingFollowsAuthority(ctx, authority, schedule.Binding) {
			result.Schedule = backupBindingStale
			result.Attention = append(result.Attention, (&localbackupschedule.StaleAuthorizationError{}).Guidance()...)
		}
	case errors.Is(scheduleErr, os.ErrNotExist):
		result.Schedule = backupBindingNotConfigured
	default:
		if result.Reason == "" {
			result.Reason = "backup schedule authorization unavailable: " + scheduleErr.Error()
		}
	}
	return result
}

// scheduleBindingFollowsAuthority compares the approved schedule binding with
// the current authority. The Apply lineage and policy digest are always
// comparable; the packaged-CLI identity is compared only when this process
// is that packaged CLI, which is the case on a host and never in a dev build.
func scheduleBindingFollowsAuthority(ctx context.Context, current nativeV2BackupAuthority, approved localbackupschedule.AuthorizationBinding) bool {
	if !reflect.DeepEqual(approved.Lineage, current.Lineage) || approved.PolicyDigest != current.PolicyDigest {
		return false
	}
	if _, _, binding, err := nativeBackupScheduleInputs(ctx, current); err == nil {
		return reflect.DeepEqual(binding, approved)
	}
	return true
}
