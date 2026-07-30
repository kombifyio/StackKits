package commands

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/standaloneoperations"
	"github.com/spf13/cobra"
)

const standaloneOperationAnnotation = "stackkit.io/standalone-operation"

var operationsJSON bool

var operationsCmd = &cobra.Command{
	Use:   "operations",
	Short: "List the canonical standalone lifecycle operation contracts",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		operations := standaloneoperations.All()
		if operationsJSON {
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(operations)
		}
		for _, operation := range operations {
			access := "read"
			if operation.Mutation {
				access = "mutation + Owner approval"
			}
			_, _ = fmt.Fprintf(
				cmd.OutOrStdout(),
				"%-18s %-28s %s\n",
				operation.ID,
				access,
				operation.Description,
			)
		}
		return nil
	},
}

func init() {
	if err := standaloneoperations.ValidateCatalog(); err != nil {
		panic(err)
	}
	for _, operation := range standaloneoperations.All() {
		path := operation.Command
		for index, argument := range path {
			if strings.HasPrefix(argument, "-") {
				path = path[:index]
				break
			}
		}
		command, remaining, err := rootCmd.Find(path)
		if err != nil || len(remaining) != 0 {
			panic(fmt.Sprintf("bind standalone operation %s to CLI path %v: %v", operation.ID, path, err))
		}
		if command.Annotations == nil {
			command.Annotations = map[string]string{}
		}
		if existing := command.Annotations[standaloneOperationAnnotation]; existing != "" && existing != string(operation.ID) {
			panic(fmt.Sprintf("CLI command %q already belongs to standalone operation %q", command.CommandPath(), existing))
		}
		command.Annotations[standaloneOperationAnnotation] = string(operation.ID)
	}
	operationsCmd.Flags().BoolVar(&operationsJSON, "json", false, "Emit the operation contracts as JSON")
	rootCmd.AddCommand(operationsCmd)
}
