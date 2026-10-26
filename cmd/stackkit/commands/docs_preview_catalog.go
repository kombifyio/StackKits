package commands

import (
	"fmt"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/usecasecatalog"
	"github.com/spf13/cobra"
)

func init() {
	var source releaseindex.PreviewCandidateSource
	var producer releaseindex.PreviewCandidateProducer
	var catalogRoot, declaredVersion, output string
	command := &cobra.Command{Use: "emit-preview-candidate-catalog", Short: "Project canonical CUE facts for an internal source-bound candidate", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := filepath.Abs(getWorkDir())
			if err != nil {
				return err
			}
			catalog, err := usecasecatalog.GeneratePreviewCatalog(root, catalogRoot, declaredVersion, source, producer)
			if err != nil {
				return err
			}
			if output == "" {
				return fmt.Errorf("candidate catalog output path is required")
			}
			return usecasecatalog.WriteJSON(output, catalog)
		}}
	flags := command.Flags()
	flags.StringVar(&catalogRoot, "catalog-root", "", "Actual clean exported CUE tree used by the candidate builder")
	flags.StringVar(&declaredVersion, "declared-version", "", "Descriptive compiler version, never a new public release")
	flags.StringVar(&source.Repository, "source-repository", "", "Exact producer source repository")
	flags.StringVar(&source.Commit, "source-sha", "", "Exact source and signer commit")
	flags.StringVar(&source.Tree, "source-tree", "", "Exact source Git tree")
	flags.StringVar(&source.ExportTree, "export-tree", "", "Exact exported build Git tree")
	flags.StringVar(&producer.Workflow, "producer-workflow", "", "Exact producer workflow path")
	flags.StringVar(&producer.WorkflowRef, "producer-workflow-ref", "", "Exact main workflow reference")
	flags.StringVar(&producer.WorkflowSHA, "producer-workflow-sha", "", "Exact producer signer commit")
	flags.StringVar(&producer.RunID, "producer-run-id", "", "Exact producer run")
	flags.StringVar(&producer.RunAttempt, "producer-run-attempt", "", "Exact producer attempt")
	flags.StringVar(&producer.Event, "producer-event", "", "Exact producer trigger")
	flags.StringVar(&output, "output", "", "Candidate catalog output file")
	docsCmd.AddCommand(command)
}
