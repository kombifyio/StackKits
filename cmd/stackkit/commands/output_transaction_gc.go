package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/spf13/cobra"
)

var outputTransactionCmd = newOutputTransactionCommand()

func newOutputTransactionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "output-transaction",
		Short:       "Inspect explicit Architecture v2 output-transaction lifecycle actions",
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	}
	cmd.AddCommand(newRetiredOutputTransactionGCCmd())
	return cmd
}

func newRetiredOutputTransactionGCCmd() *cobra.Command {
	var transactionID, action string
	var apply bool
	cmd := &cobra.Command{
		Use:   "gc-retired",
		Short: "Inspect or explicitly garbage-collect one retired output transaction tombstone",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRetiredOutputTransactionGC(cmd.OutOrStdout(), getWorkDir(), transactionID, action, apply)
		},
	}
	cmd.Flags().StringVar(&transactionID, "transaction-id", "", "Exact lowercase transaction ID")
	cmd.Flags().StringVar(&action, "action", "", "Exact action reported by a prior inspection")
	cmd.Flags().BoolVar(&apply, "apply", false, "Execute the echoed action; inspection is the default")
	_ = cmd.MarkFlagRequired("transaction-id")
	return cmd
}

func runRetiredOutputTransactionGC(stdout io.Writer, workingDirectory, transactionID, action string, apply bool) (returnErr error) {
	if stdout == nil {
		return errors.New("retired output transaction GC stdout is required")
	}
	root, err := confinedfs.Open(workingDirectory)
	if err != nil {
		return fmt.Errorf("open held workspace for retired output transaction GC: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	workspace, err := root.BeginTransaction()
	if err != nil {
		return fmt.Errorf("begin held workspace transaction for retired output transaction GC: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, workspace.Close()) }()
	inspection, err := architecturev2.InspectRetiredOutputGC(workspace, strings.TrimSpace(transactionID))
	if err != nil {
		return fmt.Errorf("inspect retired output transaction tombstone: %w", err)
	}
	if apply {
		expected := inspection
		expected.Action = architecturev2.RetiredOutputGCAction(strings.TrimSpace(action))
		if strings.TrimSpace(action) == "" {
			return errors.New("--apply requires the exact --action returned by inspection")
		}
		if err := architecturev2.ApplyRetiredOutputGC(workspace, expected); err != nil {
			return fmt.Errorf("apply retired output transaction GC: %w", err)
		}
	}
	encoded, err := json.Marshal(struct {
		TransactionID string `json:"transactionId"`
		Action        string `json:"action"`
		Applied       bool   `json:"applied"`
	}{TransactionID: inspection.TransactionID, Action: string(inspection.Action), Applied: apply})
	if err != nil {
		return fmt.Errorf("encode retired output transaction GC inspection: %w", err)
	}
	_, err = fmt.Fprintln(stdout, string(encoded))
	return err
}
