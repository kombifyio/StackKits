package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/spf13/cobra"
)

func newCapacityCommand() *cobra.Command {
	var inventoryPath, moduleRoot string
	command := &cobra.Command{
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		Use:         "capacity [spec-file]",
		Short:       "Assess selected module demand against observed host capacity",
		Long:        "Resolve the current CUE-bound selection and print its advisory capacity assessment as JSON. Missing measurements remain unverified. No files or runtime state are changed; this does not replace Apply admission.",
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, source := getWorkDir(), specFile
			if len(args) == 1 {
				source = args[0]
			}
			spec, err := os.ReadFile(resolvePathFromWorkDir(wd, source))
			if err != nil {
				return fmt.Errorf("read capacity StackSpec: %w", err)
			}
			var inventory []byte
			if inventoryPath != "" {
				inventory, err = os.ReadFile(resolvePathFromWorkDir(wd, inventoryPath))
				if err != nil {
					return fmt.Errorf("read capacity Inventory: %w", err)
				}
			}
			service, err := newArchitectureV2CLIService(wd, moduleRoot, os.Getenv(architectureAuthorityRootEnv))
			if err != nil {
				return err
			}
			result, err := service.AssessCapacity(architecturev2.ResolveInput{StackSpec: spec, Inventory: inventory})
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		},
	}
	command.Flags().StringVar(&inventoryPath, "inventory", "", "Observed Inventory; missing capacity remains unverified")
	command.Flags().StringVar(&moduleRoot, "module-root", "", "Architecture authority root")
	return command
}
