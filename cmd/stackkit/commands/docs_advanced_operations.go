package commands

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/advancedcatalog"
	"github.com/spf13/cobra"
)

var (
	docsAdvancedOperationsOutput string
	docsAdvancedOperationsCheck  bool
)

var docsEmitAdvancedOperationsCmd = &cobra.Command{
	Use:   "emit-advanced-operations",
	Short: "Emit the Advanced operations catalog (stackkit.advanced-operations/v1)",
	Long: `Write the machine-readable catalog of every Advanced operation an
orchestrator may dispatch: exact argv templates, admission requirements, input
and result contracts, rollout event phases and the denial envelope.

The release archive ships this file. With --check nothing is written; the
command fails when the file differs from the catalog source.`,
	Example: `  # Regenerate the committed catalog after changing an Advanced operation
  stackkit docs emit-advanced-operations

  # Fail when the committed catalog is stale
  stackkit docs emit-advanced-operations --check`,
	Args:        cobra.NoArgs,
	Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	RunE:        runDocsEmitAdvancedOperations,
}

func init() {
	docsEmitAdvancedOperationsCmd.Flags().StringVar(&docsAdvancedOperationsOutput, "out", advancedcatalog.DefaultPath, "Catalog JSON path, relative to --chdir")
	docsEmitAdvancedOperationsCmd.Flags().BoolVar(&docsAdvancedOperationsCheck, "check", false, "Write nothing; fail when the catalog file is missing or stale")
	docsCmd.AddCommand(docsEmitAdvancedOperationsCmd)
}

func runDocsEmitAdvancedOperations(cmd *cobra.Command, _ []string) error {
	data, err := advancedcatalog.Render()
	if err != nil {
		return err
	}
	output := filepath.FromSlash(docsAdvancedOperationsOutput)
	if !filepath.IsAbs(output) {
		output = filepath.Join(getWorkDir(), output)
	}
	if docsAdvancedOperationsCheck {
		current, readErr := os.ReadFile(output)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		if bytes.Equal(bytes.ReplaceAll(current, []byte("\r\n"), []byte("\n")), data) {
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s matches the Advanced operations catalog\n", docsAdvancedOperationsOutput)
			return err
		}
		return fmt.Errorf("%s is missing or stale: regenerate it with `mise run docs:advanced-ops:generate` and commit the result", docsAdvancedOperationsOutput)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(output, data, 0o644); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", docsAdvancedOperationsOutput)
	return err
}
