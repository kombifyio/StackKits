package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/logging"
	"github.com/spf13/cobra"
)

var machineOutputCommandActive bool

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
	return machineOutputCommandActive || applyJSON || statusJSON || verifyJSON || logsJSON || logsJSONL || removeTerminalEvidenceJSON
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

func writeMachineCommandFailure(cmd *cobra.Command, err error, guidance ...string) error {
	if cmd == nil || err == nil {
		return err
	}
	status := machineCommandFailureStatus(err)
	if len(guidance) == 0 {
		guidance = []string{
			"Correct the reported local authority or input condition, then retry the same command.",
			"Inspect `stackkit logs latest --json` for bounded local evidence when a run was created.",
		}
	}
	detail := actionableerror.New(
		"stackkit_command_failed", machineCommandFailureReason(cmd, status), logging.RedactText(err.Error()), guidance, false,
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
