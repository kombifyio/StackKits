package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/applyoutcome"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/logging"
	"github.com/kombifyio/stackkits/internal/managedentitlement"
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

// Preserve the existing product classifications without exposing error fields.
// Code types can hold arbitrary strings, so only registered values are public.
func typedProductFailureReason(err error) (reason string, typed bool) {
	var artifact *generationartifact.Error
	if errors.As(err, &artifact) && artifact != nil {
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
	if typedReason, typed := typedProductFailureReason(err); typed {
		status = "failed"
		reason = machineCommandFailureReason(cmd, status)
		if typedReason != "" {
			reason = typedReason
		}
		message = "StackKits rejected the command at a typed product boundary."
	}
	if runtime := applyoutcome.Classify(err.Error()); runtime.Class != applyoutcome.ClassUnknown {
		status = "failed"
		reason = string(runtime.Class)
		retryable = runtime.Retryable
		guidance = append(append([]string(nil), runtime.Remediation...), guidance...)
	}
	detail := actionableerror.New(
		"stackkit_command_failed", reason, message, guidance, retryable,
	)
	if writeErr := writeCommandResultStatus(cmd, cmd.CommandPath(), status, detail); writeErr != nil {
		return errors.Join(err, fmt.Errorf("write machine-readable command failure: %w", writeErr))
	}
	return err
}

func machineAwareCommandError(cmd *cobra.Command, err error, guidance ...string) error {
	if err == nil || !commandRequestsMachineOutput(cmd) {
		return err
	}
	return writeMachineCommandFailure(cmd, err, guidance...)
}
