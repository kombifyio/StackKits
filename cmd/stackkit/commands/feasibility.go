package commands

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/spf13/cobra"
)

const feasibilityCommandName = "feasibility"

// feasibilityFailure is the data of a failed stackkit.command-result/v1: an
// input that cannot be read or decoded, so no report exists.
type feasibilityFailure struct {
	APIVersion string                            `json:"apiVersion"`
	Kind       string                            `json:"kind"`
	CLIVersion string                            `json:"cliVersion"`
	Errors     []architecturev2.FeasibilityIssue `json:"errors"`
}

func newFeasibilityCommand() *cobra.Command {
	var inventoryPath, moduleRoot string
	var asJSON bool
	command := &cobra.Command{
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		Use:         feasibilityCommandName,
		Short:       "Check whether a proposed placement fits an exact node inventory",
		Long: "Resolve a StackSpec, with the nodeRefs it pins, against the exact observed Inventory " +
			"and report per workload whether it is feasible, infeasible or unknown, with the requested " +
			"and resolved nodes and the reasons, plus the capacity verdict of every node. " +
			"The operation is read-only: it attests no host facts, persists no plan or state, " +
			"writes no .stackkit files and uses no network. It exits 0 whenever a report is produced, " +
			"whatever the outcome, and 1 only when an input cannot be read or decoded.",
		Example: "  stackkit feasibility --spec stack-spec.yaml --inventory inventory.yaml --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			wd := getWorkDir()
			failed := func(code, path, message string) error {
				issue := architecturev2.FeasibilityIssue{Code: code, Path: path, Message: message}
				if asJSON {
					data := feasibilityFailure{
						APIVersion: architecturev2.FeasibilityAPIVersion, Kind: "FeasibilityFailure",
						CLIVersion: version, Errors: []architecturev2.FeasibilityIssue{issue},
					}
					if err := writeCommandResultStatus(cmd, feasibilityCommandName, "failed", data); err != nil {
						return errors.Join(fmt.Errorf("feasibility %s: %s", code, message), err)
					}
				}
				return fmt.Errorf("feasibility %s: %s", code, message)
			}
			if strings.TrimSpace(inventoryPath) == "" {
				return failed("inventory_required", "inventory", "--inventory is required: feasibility judges a proposal against an exact observed Inventory")
			}
			spec, err := os.ReadFile(resolvePathFromWorkDir(wd, specFile))
			if err != nil {
				return failed("input_unreadable", "spec", fmt.Sprintf("read StackSpec: %v", err))
			}
			inventory, err := os.ReadFile(resolvePathFromWorkDir(wd, inventoryPath))
			if err != nil {
				return failed("input_unreadable", "inventory", fmt.Sprintf("read Inventory: %v", err))
			}
			service, err := newArchitectureV2CLIService(wd, moduleRoot, os.Getenv(architectureAuthorityRootEnv))
			if err != nil {
				return failed("authority_load", "", err.Error())
			}
			report, err := service.AssessFeasibility(architecturev2.FeasibilityInput{StackSpec: spec, Inventory: inventory})
			if err != nil {
				// An input that cannot be decoded names its document; an internal
				// fault (anything else) names none.
				code, path := "internal_fault", ""
				var resolveErr *architecturev2.ResolveError
				if errors.As(err, &resolveErr) {
					code = string(resolveErr.Code)
					switch resolveErr.Code {
					case architecturev2.ErrInvalidInventory:
						path = "inventory"
					case architecturev2.ErrInvalidStackSpec, architecturev2.ErrMigrationRequired, architecturev2.ErrMigrationBlocked:
						path = "spec"
					}
				}
				return failed(code, path, err.Error())
			}
			report.CLIVersion = version
			if asJSON {
				return writeCommandResult(cmd, feasibilityCommandName, report)
			}
			return writeFeasibilityText(cmd, report)
		},
	}
	command.Flags().StringVar(&inventoryPath, "inventory", "", "Observed Inventory of the proposed nodes (required)")
	command.Flags().StringVar(&moduleRoot, "module-root", "", "Architecture authority root")
	command.Flags().BoolVar(&asJSON, "json", false, "Emit stackkit.command-result/v1 JSON")
	return command
}

func writeFeasibilityText(cmd *cobra.Command, report architecturev2.FeasibilityReport) error {
	out := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(out, "Feasibility: %s\n", report.Outcome); err != nil {
		return err
	}
	for _, workload := range report.Workloads {
		if _, err := fmt.Fprintf(out, "  %s: %s on %v (requested %v)\n", workload.Workload, workload.Verdict, workload.ResolvedNodeRefs, workload.RequestedNodeRefs); err != nil {
			return err
		}
		for _, reason := range workload.Reasons {
			if _, err := fmt.Fprintf(out, "    %s: %s\n", reason.Code, reason.Message); err != nil {
				return err
			}
		}
	}
	for _, node := range report.Nodes {
		if _, err := fmt.Fprintf(out, "  node %s: %s %v\n", node.NodeRef, node.Verdict, node.ReasonCodes); err != nil {
			return err
		}
	}
	for _, issue := range report.Errors {
		if _, err := fmt.Fprintf(out, "  error %s at %s: %s\n", issue.Code, issue.Path, issue.Message); err != nil {
			return err
		}
	}
	for _, advisory := range report.Advisories {
		if _, err := fmt.Fprintf(out, "  advisory %s on node %s\n", advisory.Code, advisory.NodeRef); err != nil {
			return err
		}
	}
	return nil
}
