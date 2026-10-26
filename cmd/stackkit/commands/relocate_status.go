package commands

import (
	"encoding/json"
	"errors"
	"github.com/kombifyio/stackkits/internal/workloadrelocation"
	relocatecoolify "github.com/kombifyio/stackkits/internal/workloadrelocation/coolify"
	"github.com/spf13/cobra"
	"os"
)

func newRelocateStatusCommand() *cobra.Command {
	options := &relocateOptions{}
	command := &cobra.Command{Use: "status", Short: "Read one signed move receipt and verify its current local runtime without restoring again", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := requireCoolifyPlatform(options.platform); err != nil {
			return err
		}
		if options.planPath == "" || options.transferID == "" {
			return errors.New("status requires the exact plan and transfer identity")
		}
		plan, err := readRelocationPlan(cmd.InOrStdin(), options.planPath)
		if err != nil {
			return err
		}
		runner, err := relocatecoolify.Locate(cmd.Context(), options.engine)
		if err != nil {
			return err
		}
		var receipt workloadrelocation.Receipt
		found := false
		for _, phase := range []string{"resumed", "aborted", "sent", "received", "prepared"} {
			candidate, err := workloadrelocation.LoadReceipt(getWorkDir(), options.transferID, phase, plan)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if found {
				continue
			}
			found = true
			receipt = candidate
			if phase == "resumed" || phase == "aborted" {
				break
			}
		}
		if !found {
			// Recover only a retained successful engine result, then observe the
			// actual destination again before signing it under local Owner custody.
			raw, err := os.ReadFile(relocationEngineReceiptPath(options.transferID))
			if err != nil {
				return err
			}
			var head struct {
				TransferID string `json:"transfer_id"`
			}
			if json.Unmarshal(raw, &head) != nil || head.TransferID != options.transferID {
				return errors.New("retained engine result belongs to another transfer")
			}
			if _, err := runner.Observe(cmd.Context(), raw, "received", getWorkDir()); err != nil {
				return err
			}
			receipt, err = workloadrelocation.StoreReceipt(getWorkDir(), options.transferID, "received", plan, raw)
			if err != nil {
				return err
			}
		}
		observed, err := runner.Observe(cmd.Context(), receipt.Data, receipt.Phase, getWorkDir())
		if err != nil {
			return err
		}
		status, err := workloadrelocation.SignStatus(getWorkDir(), receipt, observed)
		if err != nil {
			return err
		}
		return writeCommandResult(cmd, cmd.CommandPath(), status)
	}}
	addRelocateCommonFlags(command, options)
	command.Flags().StringVar(&options.planPath, "plan", "", "Exact checked source plan, or - for stdin")
	command.Flags().StringVar(&options.transferID, "transfer-id", "", "Semantic move identity")
	addRelocateJSONFlag(command, options)
	return command
}
