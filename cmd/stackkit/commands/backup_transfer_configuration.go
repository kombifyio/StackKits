package commands

import (
	"context"
	"errors"
	"slices"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/spf13/cobra"
)

// applicationTransferConfigurationDocument is the source's portable
// application configuration: public settings and effective module profiles of
// one selection, never secret material, placement or routes. The receiving
// candidate is authored from it and its digest travels through the destination
// admission into the verified outcome.
type applicationTransferConfigurationDocument struct {
	APIVersion                  string                                                              `json:"apiVersion"`
	Workloads                   []string                                                            `json:"workloads"`
	Configuration               map[string]backuplifecycle.ApplicationTransferWorkloadConfiguration `json:"configuration"`
	PortableConfigurationDigest string                                                              `json:"portableConfigurationDigest"`
}

func configureApplicationTransferConfigurationCommand() {
	configuration := &cobra.Command{Use: "transfer-configuration", Short: "Project one selected application's portable public configuration for a receiving candidate", Args: machineAwareNoArgs, RunE: runApplicationTransferConfiguration}
	configuration.Flags().String("use-case", "", "Project every currently selected member of this catalog use case")
	configuration.Flags().StringArray("workload", nil, "Exact selected application to project; repeat")
	backupCmd.AddCommand(configuration)
}

func runApplicationTransferConfiguration(cmd *cobra.Command, _ []string) error {
	workloads, _ := cmd.Flags().GetStringArray("workload")
	useCase, _ := cmd.Flags().GetString("use-case")
	if (len(workloads) == 0) == (useCase == "") {
		return errors.New("transfer configuration requires either --use-case or --workload")
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), backupLongOperationTimeout)
	defer cancel()
	authority, err := inspectNativeV2BackupAuthorityForRequest(ctx, getWorkDir(), specFile)
	if err != nil {
		return err
	}
	if useCase != "" {
		catalog, catalogErr := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
		if catalogErr != nil {
			return catalogErr
		}
		workloads, catalogErr = catalog.SelectedUseCaseWorkloads(authority.Plan, useCase)
		if catalogErr != nil {
			return catalogErr
		}
	}
	configuration, err := restoreactivation.ApplicationTransferConfiguration(authority.Plan, workloads)
	if err != nil {
		return err
	}
	digest, err := backuplifecycle.ApplicationTransferConfigurationDigest(configuration)
	if err != nil {
		return err
	}
	selected := make([]string, 0, len(configuration))
	for workload := range configuration {
		selected = append(selected, workload)
	}
	slices.Sort(selected)
	return writeCommandResult(cmd, cmd.CommandPath(), applicationTransferConfigurationDocument{
		APIVersion: "stackkit.application-transfer-configuration/v1", Workloads: selected, Configuration: configuration, PortableConfigurationDigest: digest,
	})
}
