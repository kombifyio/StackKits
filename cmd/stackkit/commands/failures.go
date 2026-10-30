package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/failureknowledge"
	"github.com/spf13/cobra"
)

func newFailuresCommand() *cobra.Command {
	command := &cobra.Command{
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		Use:         "failures",
		Short:       "Look up known failures and their resolutions offline",
		Long: "Search the failure resolution snapshot shipped with this release. A resolution names its symptoms, " +
			"where it applies, the root cause and the product procedures that fix it. It is advice: apply a fix only " +
			"through the referenced procedure.",
	}
	command.AddCommand(newFailuresLookupCommand(), newFailuresSnapshotCommand())
	return command
}

func newFailuresLookupCommand() *cobra.Command {
	var signature, text, snapshotPath, ollamaURL string
	var topK int
	command := &cobra.Command{
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		Use:         "lookup [description]",
		Short:       "Find resolutions by failure signature or description",
		Long: "Look up promoted resolutions in the offline snapshot: an exact failure signature first, then the words " +
			"of the description (FTS5). With a local Ollama serving bge-m3 (--ollama-url or " + failureknowledge.OllamaURLEnv +
			") the description is also matched by meaning. Prints JSON; nothing leaves this host except the " +
			"description sent to that local Ollama.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				if text != "" {
					return errors.New("give the description either as argument or with --text")
				}
				text = args[0]
			}
			snapshot, err := openFailureSnapshot(cmd, snapshotPath)
			if err != nil {
				return err
			}
			defer snapshot.Close()
			if ollamaURL == "" {
				ollamaURL = strings.TrimSpace(os.Getenv(failureknowledge.OllamaURLEnv))
			}
			result, err := snapshot.Lookup(cmd.Context(), failureknowledge.Query{
				Signature: signature, Text: text, TopK: topK, OllamaURL: ollamaURL,
			})
			if err != nil {
				return err
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(result)
		},
	}
	command.Flags().StringVar(&signature, "signature", "", "Failure signature (64 hex characters) from the failure normalizer")
	command.Flags().StringVar(&text, "text", "", "Error text or a description of the failure")
	command.Flags().StringVar(&snapshotPath, "snapshot", "", "Snapshot file (default: "+failureknowledge.SnapshotEnv+", then beside the stackkit binary)")
	command.Flags().StringVar(&ollamaURL, "ollama-url", "", "Ollama with bge-m3 on this host (localhost or a loopback address) for the semantic step")
	command.Flags().IntVar(&topK, "top", 5, "Number of results (1 to 20)")
	return command
}

func newFailuresSnapshotCommand() *cobra.Command {
	command := &cobra.Command{
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		Use:         "snapshot",
		Short:       "Build or describe the failure resolution snapshot",
	}
	var catalogPath, output string
	build := &cobra.Command{
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		Use:         "build",
		Short:       "Build the SQLite FTS5 snapshot from a resolution catalog",
		Long: "Build the versioned snapshot from a catalog of promoted resolutions (" + failureknowledge.CatalogSchema +
			"). The release build runs this over the catalog in internal/failureknowledge/data.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if catalogPath == "" || output == "" {
				return errors.New("--catalog and --output are required")
			}
			catalog, digest, err := failureknowledge.LoadCatalog(catalogPath)
			if err != nil {
				return err
			}
			info, err := failureknowledge.BuildSnapshot(cmd.Context(), catalog, digest, output)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
		},
	}
	build.Flags().StringVar(&catalogPath, "catalog", "", "Resolution catalog JSON")
	build.Flags().StringVar(&output, "output", "", "Snapshot file to write")

	var snapshotPath string
	info := &cobra.Command{
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
		Use:         "info",
		Short:       "Print the version and size of the snapshot",
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			snapshot, err := openFailureSnapshot(cmd, snapshotPath)
			if err != nil {
				return err
			}
			defer snapshot.Close()
			return json.NewEncoder(cmd.OutOrStdout()).Encode(snapshot.Info())
		},
	}
	info.Flags().StringVar(&snapshotPath, "snapshot", "", "Snapshot file (default: "+failureknowledge.SnapshotEnv+", then beside the stackkit binary)")
	command.AddCommand(build, info)
	return command
}

func openFailureSnapshot(cmd *cobra.Command, path string) (*failureknowledge.Snapshot, error) {
	if path == "" {
		found, ok := failureknowledge.DefaultSnapshotPath()
		if !ok {
			return nil, fmt.Errorf("no failure snapshot found; this build ships %s beside the stackkit binary or in ../lib/stackkit, or set %s",
				failureknowledge.SnapshotFileName, failureknowledge.SnapshotEnv)
		}
		path = found
	}
	return failureknowledge.OpenSnapshot(cmd.Context(), path)
}
