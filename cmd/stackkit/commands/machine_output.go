package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/applyoutcome"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackupschedule"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/logging"
	"github.com/kombifyio/stackkits/internal/managedentitlement"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/spf13/cobra"
)

var machineOutputCommandActive bool

func machineAwareNoArgs(cmd *cobra.Command, args []string) error {
	return machineAwareCommandError(cmd, cobra.NoArgs(cmd, args))
}

func commandRequestsMachineOutput(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	if cmd.Name() == "capacity" {
		return true
	}
	for current := cmd; current != nil; current = current.Parent() {
		for _, name := range []string{"json", "jsonl", "terminal-evidence-json"} {
			flag := current.Flags().Lookup(name)
			if flag == nil {
				flag = current.PersistentFlags().Lookup(name)
			}
			if flag != nil && strings.EqualFold(strings.TrimSpace(flag.Value.String()), "true") {
				return true
			}
		}
	}
	return false
}

func humanOutputSuppressed() bool {
	return machineOutputCommandActive || applyJSON || statusJSON || verifyJSON || logsJSON || logsJSONL || removeTerminalEvidenceJSON || prepareJSON
}

func machineCommandFailureStatus(err error) string {
	if err == nil {
		return "failed"
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{" denied", "denied:", "not authorized", "approval required", "requires explicit owner approval"} {
		if strings.Contains(message, marker) {
			return "denied"
		}
	}
	return "failed"
}

func machineCommandFailureReason(cmd *cobra.Command, status string) string {
	name := "command"
	if cmd != nil && strings.TrimSpace(cmd.Name()) != "" {
		name = strings.ReplaceAll(strings.TrimSpace(cmd.Name()), "-", "_")
	}
	return name + "_" + status
}

// restoreActivationFailureReason names the outcome and closed step of a failed
// restore activation, e.g. restore_activation_recovered_in_runtime_start_started.
// The stage is a closed pre-journal step or the last committed journal phase,
// never cause text. A typed product reason keeps precedence.
func restoreActivationFailureReason(err error) (string, bool) {
	var stage *restoreactivation.StageError
	if !errors.As(err, &stage) || !restoreActivationStagePattern.MatchString(stage.Stage) {
		return "", false
	}
	step := strings.ReplaceAll(stage.Stage, "-", "_")
	var notStarted *restoreactivation.ActivationNotStartedError
	var recovered *restoreactivation.ActivationRecoveredError
	switch {
	case errors.As(err, &notStarted):
		return "restore_activation_not_started_at_" + step, true
	case errors.As(err, &recovered):
		return "restore_activation_recovered_in_" + step, true
	default:
		return "restore_activation_failed_in_" + step, true
	}
}

var restoreActivationStagePattern = regexp.MustCompile(`^[a-z][a-z-]{1,39}$`)

// backupRunFailureReason names the closed step at which a snapshot run failed,
// e.g. backup_run_failed_in_quiesce_stop. Without it every run failure was the
// generic run_failed, and evidence that keeps only closed tokens could not say
// whether writers stopped, Kopia failed or the resume failed. Restore
// activation and typed product reasons keep precedence.
func backupRunFailureReason(err error) (string, bool) {
	var stage *backuplifecycle.RunStageError
	if !errors.As(err, &stage) || !restoreActivationStagePattern.MatchString(stage.Stage) {
		return "", false
	}
	if _, restoring := restoreActivationFailureReason(err); restoring {
		return "", false
	}
	return "backup_run_failed_in_" + strings.ReplaceAll(stage.Stage, "-", "_"), true
}

// Preserve the existing product classifications without exposing error fields.
// Code types can hold arbitrary strings, so only registered values are public.
func typedProductFailureReason(err error) (reason string, typed bool) {
	var artifact *generationartifact.Error
	if errors.As(err, &artifact) && artifact != nil {
		if artifact.Code == generationartifact.ErrReadinessBlocked {
			for _, blocker := range artifact.Blockers {
				if blocker.Code == "runtime-capacity-unsatisfied" {
					return blocker.Code, true
				}
			}
		}
		switch artifact.Code {
		case generationartifact.ErrInvalidPlan, generationartifact.ErrInvalidContract,
			generationartifact.ErrNonCanonical, generationartifact.ErrHashMismatch,
			generationartifact.ErrBindingMismatch, generationartifact.ErrInvalidPath,
			generationartifact.ErrPathEscape, generationartifact.ErrDuplicateArtifact,
			generationartifact.ErrArtifactMissing, generationartifact.ErrArtifactChanged,
			generationartifact.ErrIncompatible, generationartifact.ErrReadinessBlocked,
			generationartifact.ErrRendererMissing, generationartifact.ErrExecutorMissing,
			generationartifact.ErrExecutorFailed, generationartifact.ErrVerifierMissing,
			generationartifact.ErrEvidenceSetMismatch, generationartifact.ErrDuplicateEvidence,
			generationartifact.ErrEvidenceFreshness, generationartifact.ErrEvidenceUntrusted,
			generationartifact.ErrIO:
			return string(artifact.Code), true
		}
		return "", true
	}
	var resolution *architecturev2.ResolveError
	if errors.As(err, &resolution) && resolution != nil {
		switch resolution.Code {
		case architecturev2.ErrInvalidStackSpec, architecturev2.ErrInvalidInventory,
			architecturev2.ErrMigrationRequired, architecturev2.ErrMigrationBlocked,
			architecturev2.ErrAuthorityLoad, architecturev2.ErrResolveFailed,
			architecturev2.ErrGenerationAuthorization, architecturev2.ErrApplyAuthorization,
			architecturev2.ErrRequestTooLarge, architecturev2.ErrUnsupportedMedia,
			architecturev2.ErrResolveBusy, architecturev2.ErrOperationalUnavailable:
			return string(resolution.Code), true
		}
		return "", true
	}
	return "", false
}

func writeMachineCommandFailure(cmd *cobra.Command, err error, guidance ...string) error {
	if cmd == nil || err == nil {
		return err
	}
	var entitlementDenial *managedentitlement.Denial
	if errors.As(err, &entitlementDenial) {
		if writeErr := writeCommandResultStatus(cmd, cmd.CommandPath(), "denied", entitlementDenial.Envelope()); writeErr != nil {
			return errors.Join(err, fmt.Errorf("write machine-readable command failure: %w", writeErr))
		}
		return err
	}
	status, detail := commandFailureDetail(cmd, err, guidance...)
	if writeErr := writeCommandResultStatus(cmd, cmd.CommandPath(), status, detail); writeErr != nil {
		return errors.Join(err, fmt.Errorf("write machine-readable command failure: %w", writeErr))
	}
	return err
}

// One projection serves both JSON output and the human terminal diagnosis.
func commandFailureDetail(cmd *cobra.Command, err error, guidance ...string) (string, actionableerror.Contract) {
	var memberDenial *localevidence.MemberSigningDenial
	if errors.As(err, &memberDenial) {
		detail := actionableerror.New(
			"stackkit_command_failed", localevidence.ReasonMemberCustodyVerifyOnly,
			logging.RedactText(err.Error()), memberDenial.Guidance(), false,
		)
		return "denied", detail
	}
	// A backup configuration or schedule approval made for an earlier Apply
	// (every re-apply, a CLI upgrade included, re-signs Plan and Apply) is a
	// local condition with one exact remedy; report that remedy instead of
	// the generic retry guidance so a scheduled run or dashboard never
	// stops silently.
	var staleConfiguration *backuplifecycle.ConfigurationBindingError
	if errors.As(err, &staleConfiguration) {
		detail := actionableerror.New(
			"stackkit_command_failed", "backup_configuration_stale",
			logging.RedactText(err.Error()), staleConfiguration.Guidance(), false,
		)
		return "failed", detail
	}
	var staleSchedule *localbackupschedule.StaleAuthorizationError
	if errors.As(err, &staleSchedule) {
		detail := actionableerror.New(
			"stackkit_command_failed", "backup_schedule_stale",
			logging.RedactText(err.Error()), staleSchedule.Guidance(), false,
		)
		return "failed", detail
	}
	var entitlementDenial *managedentitlement.Denial
	if errors.As(err, &entitlementDenial) {
		return "denied", actionableerror.New(entitlementDenial.ErrorCode, entitlementDenial.ReasonCode,
			logging.RedactText(err.Error()), entitlementDenial.UserGuidance.NextSteps, entitlementDenial.Retryable)
	}
	var actionableDenial interface {
		ActionableError() actionableerror.Contract
	}
	if errors.As(err, &actionableDenial) {
		detail := actionableDenial.ActionableError()
		if detail.Validate() == nil {
			status := "denied"
			var condition interface{ ActionableStatus() string }
			if errors.As(err, &condition) && condition.ActionableStatus() == "failed" {
				status = "failed"
			}
			return status, detail
		}
	}
	status := machineCommandFailureStatus(err)
	if len(guidance) == 0 {
		guidance = []string{
			"Correct the reported local authority or input condition, then retry the same command.",
			"Inspect `stackkit logs latest --json` for bounded local evidence when a run was created.",
		}
	}
	// A recognized host or container-runtime condition carries its own reason
	// code, retry semantics, and remediation. Reporting the generic command
	// reason with retryable=false made a rate-limited registry and a blocked
	// kernel indistinguishable to an agent or dashboard.
	//
	// Such a condition is also never an owner-authority denial, even when its
	// text contains the word "denied" (a registry rejecting a pull, or a
	// refused Docker socket). Classifying first keeps the envelope status
	// honest for exactly those cases.
	reason := machineCommandFailureReason(cmd, status)
	retryable := false
	message := logging.RedactText(err.Error())
	typedReason, typed := typedProductFailureReason(err)
	if typed {
		status = "failed"
		reason = machineCommandFailureReason(cmd, status)
		if typedReason != "" {
			reason = typedReason
		}
		message = "StackKits rejected the command at a typed product boundary."
	}
	if stageReason, ok := restoreActivationFailureReason(err); ok && !typed {
		reason = stageReason
	}
	if stageReason, ok := backupRunFailureReason(err); ok && !typed {
		reason = stageReason
	}
	var pathError *os.PathError
	if errors.As(err, &pathError) && os.IsNotExist(pathError) {
		switch filepath.Base(pathError.Path) {
		case "stack-spec.yaml", "kombination.yaml":
			reason = "workspace_missing"
		}
	}
	if diagnostic := diagnoseExecutorFailure(err); diagnostic.Code == "authorization_time_mismatch" || diagnostic.Code == "origin_tls_rejected" {
		reason = diagnostic.Code
	}
	retryAfter := ""
	if runtime := applyoutcome.Classify(err.Error()); runtime.Class != applyoutcome.ClassUnknown {
		status = "failed"
		reason = string(runtime.Class)
		retryable = runtime.Retryable
		if retryable && !runtime.RetryAfter.IsZero() {
			retryAfter = runtime.RetryAfter.UTC().Format(time.RFC3339)
		}
	}
	if known, ok := actionableerror.Diagnostic(reason); ok {
		status = "failed"
		message, guidance, retryable = known.Message, known.UserGuidance, known.Retryable
	}
	detail := actionableerror.New(
		"stackkit_command_failed", reason, message, guidance, retryable,
	)
	detail.RetryAfter = retryAfter
	return status, detail
}

func machineAwareCommandError(cmd *cobra.Command, err error, guidance ...string) error {
	if err == nil || !commandRequestsMachineOutput(cmd) {
		return err
	}
	return writeMachineCommandFailure(cmd, err, guidance...)
}
