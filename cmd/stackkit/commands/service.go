package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/servicecontrol"
	"github.com/spf13/cobra"
)

var serviceCmd = newServiceCommand()

func newServiceCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "service",
		Short: "Control StackKits-managed Cloud Core services",
		Annotations: map[string]string{
			noDeployObservabilityAnnotation: "true",
		},
	}
	for _, action := range []string{servicecontrol.ActionStart, servicecontrol.ActionStop, servicecontrol.ActionRestart} {
		action := action
		var ownerApproved, outputJSON bool
		subcommand := &cobra.Command{
			Use:   action + " <service-key>",
			Short: strings.ToUpper(action[:1]) + action[1:] + " one managed service",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runServiceMutation(cmd, action, args[0], ownerApproved, outputJSON)
			},
		}
		subcommand.Flags().BoolVar(&ownerApproved, "owner-approve", false, "Explicitly approve the owner-bound service mutation")
		subcommand.Flags().BoolVar(&outputJSON, "json", false, "Write the versioned service action result as JSON")
		command.AddCommand(subcommand)
	}
	var tail int
	var cursor string
	var outputJSON bool
	logs := &cobra.Command{
		Use:   "logs <service-key>",
		Short: "Read bounded, redacted service logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceLogs(cmd, args[0], tail, cursor, outputJSON)
		},
	}
	logs.Flags().IntVar(&tail, "tail", 100, "Number of redacted log entries to return (1..200)")
	logs.Flags().StringVar(&cursor, "cursor", "", "Opaque cursor from a previous result")
	logs.Flags().BoolVar(&outputJSON, "json", false, "Write the versioned service log page as JSON")
	command.AddCommand(logs)
	return command
}

func runServiceMutation(cmd *cobra.Command, action, serviceKey string, ownerApproved, outputJSON bool) error {
	controller, err := serviceController()
	if err != nil {
		return machineAwareCommandError(cmd, err)
	}
	result, err := controller.Mutate(commandContext(cmd), action, serviceKey, ownerApproved)
	if err != nil {
		return machineAwareCommandError(cmd, err)
	}
	if outputJSON {
		return writeServiceJSON(cmd, result)
	}
	printSuccess("%s %s: desired=%s observed=%s", strings.Title(action), result.ServiceKey, result.DesiredState, result.ObservedState) //nolint:staticcheck // stable CLI wording
	return nil
}

func runServiceLogs(cmd *cobra.Command, serviceKey string, tail int, cursor string, outputJSON bool) error {
	controller, err := serviceController()
	if err != nil {
		return machineAwareCommandError(cmd, err)
	}
	result, err := controller.Logs(commandContext(cmd), serviceKey, tail, cursor)
	if err != nil {
		return machineAwareCommandError(cmd, err)
	}
	if outputJSON {
		return writeServiceJSON(cmd, result)
	}
	for _, entry := range result.Entries {
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", entry.Timestamp.Format("2006-01-02T15:04:05.000Z07:00"), entry.Message)
	}
	return nil
}

func serviceController() (*servicecontrol.Controller, error) {
	workspace, err := filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}
	return servicecontrol.NewOSController(workspace)
}

func commandContext(cmd *cobra.Command) context.Context {
	if cmd == nil || cmd.Context() == nil {
		return context.Background()
	}
	return cmd.Context()
}

func writeServiceJSON(cmd *cobra.Command, value any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
