package commands

import (
	"fmt"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/servicecontrol"
	"github.com/spf13/cobra"
)

func newServiceManagerCommand() *cobra.Command {
	var to, resume string
	var approved, inspect, outputJSON, nativeCore, cancelTransfer bool
	command := &cobra.Command{
		Use:   "manager <workload-ref>",
		Short: "Transfer an existing node-owned workload between managers or to its native Core",
		Example: `  stackkit service manager media --to standalone-compose --owner-approve
  stackkit service manager media --to komodo --owner-approve
  stackkit service manager media --to komodo --resume handover-<operation-id> --owner-approve`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if inspect {
				if to != "" || resume != "" || nativeCore || cancelTransfer {
					return machineAwareCommandError(cmd, fmt.Errorf("manager --status cannot mutate or resume an attachment"))
				}
				result, err := servicecontrol.InspectOwnedWorkloadManager(getWorkDir(), args[0])
				if err != nil {
					return machineAwareCommandError(cmd, err)
				}
				return writeManagerTransitionResult(cmd, result, outputJSON)
			}
			if cancelTransfer {
				if !nativeCore || to != "komodo" || resume == "" {
					return machineAwareCommandError(cmd, fmt.Errorf("--cancel requires --native-core --to komodo and the exact --resume operation"))
				}
				result, err := servicecontrol.CancelNativeKomodoCoreTransfer(commandContext(cmd), getWorkDir(), args[0], resume, approved)
				if err != nil {
					return machineAwareCommandError(cmd, err)
				}
				return writeManagerTransitionResult(cmd, result, outputJSON)
			}
			if nativeCore {
				if to != "komodo" {
					return machineAwareCommandError(cmd, fmt.Errorf("--native-core requires --to komodo"))
				}
				result, err := servicecontrol.TransitionNativeKomodoCore(commandContext(cmd), getWorkDir(), args[0], resume, approved, func(state platformdeploy.HostDeployment) (string, error) {
					authority, err := inspectNativeV2GeneratedAuthority(commandContext(cmd), getWorkDir(), specFile)
					if err != nil {
						return "", err
					}
					for _, target := range authority.Plan.ApplyRequirements().RuntimeInstances {
						if target.WorkloadRef == args[0] && target.RuntimeAdapter != nil && target.RuntimeAdapter.ID == "komodo" &&
							target.InstanceRef == state.InstanceRef && len(target.SiteRefs) == 1 && target.SiteRefs[0] == state.SiteRef &&
							len(target.NodeRefs) == 1 && target.NodeRefs[0] == state.NodeRef {
							return authority.Plan.Binding().PlanHash, nil
						}
					}
					return "", fmt.Errorf("fresh generated Plan does not select this exact node workload through Komodo")
				})
				if err != nil {
					return machineAwareCommandError(cmd, err)
				}
				return writeManagerTransitionResult(cmd, result, outputJSON)
			}
			result, err := servicecontrol.TransitionAppliedWorkloadManager(commandContext(cmd), getWorkDir(), args[0], to, resume, approved, func(state platformdeploy.HostDeployment) (platformdeploy.HostManagerPlanBinding, error) {
				authority, err := inspectNativeV2AppliedAuthority(commandContext(cmd), getWorkDir(), specFile)
				if err != nil {
					return platformdeploy.HostManagerPlanBinding{}, err
				}
				applied, err := nativeAppliedWorkloadDeployment(authority, args[0])
				if err != nil {
					return platformdeploy.HostManagerPlanBinding{}, err
				}
				if state.InstanceRef != applied.InstanceRef || state.ArtifactDigest != applied.ArtifactDigest || state.SiteRef != applied.SiteRef || state.NodeRef != applied.NodeRef {
					return platformdeploy.HostManagerPlanBinding{}, fmt.Errorf("current signed Apply differs from existing workload custody")
				}
				planPath := filepath.Join(authority.WorkspaceRoot, filepath.FromSlash(authority.OutputRoot), ".stackkit", "resolved-plan.json")
				controller, err := servicecontrol.NewOSController(authority.WorkspaceRoot, planPath, authority.Plan)
				if err != nil {
					return platformdeploy.HostManagerPlanBinding{}, err
				}
				return controller.ManagerPlanBinding(state, authority.Lineage.ApplyResultHash)
			})
			if err != nil {
				return machineAwareCommandError(cmd, err)
			}
			return writeManagerTransitionResult(cmd, result, outputJSON)
		},
	}
	command.Flags().StringVar(&to, "to", "", "Target manager: standalone-compose or komodo")
	command.Flags().StringVar(&resume, "resume", "", "Resume the exact pending handover operation")
	command.Flags().BoolVar(&nativeCore, "native-core", false, "Transfer an existing Komodo workload to this node's native Core using its fresh generated Plan")
	command.Flags().BoolVar(&cancelTransfer, "cancel", false, "Cancel the exact pending native Core transfer before any target attachment request")
	command.Flags().BoolVar(&approved, "owner-approve", false, "Explicitly approve the owner-bound manager transition")
	command.Flags().BoolVar(&inspect, "status", false, "Read the durable manager and handover outcome without mutation")
	command.Flags().BoolVar(&outputJSON, "json", false, "Write the versioned manager result as JSON")
	return command
}

func writeManagerTransitionResult(cmd *cobra.Command, result servicecontrol.ManagerTransitionResult, outputJSON bool) error {
	if outputJSON {
		return writeServiceJSON(cmd, result)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: manager=%s status=%s operation=%s\n", result.Project, result.To, result.Status, result.OperationID)
	return err
}
