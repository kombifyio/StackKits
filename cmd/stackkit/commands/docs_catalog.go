package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/usecasecatalog"
	"github.com/spf13/cobra"
)

var (
	docsReleaseTag      string
	docsSourceSHA       string
	docsPublicSourceSHA string
	docsReleaseURL      string
	docsOutputDir       string
	docsOverviewOutput  string
	docsEvidenceOutput  string
	docsTestReceipt     string
	docsTestsPassed     bool
)

var docsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Generate deterministic StackKits documentation projections",
}

var docsEmitReleaseManifestsCmd = &cobra.Command{
	Use:   "emit-release-manifests",
	Short: "Emit release-bound use-case and compatibility manifests",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, err := filepath.Abs(getWorkDir())
		if err != nil {
			return err
		}
		release := usecasecatalog.ReleaseIdentity{
			Tag: docsReleaseTag, Version: strings.TrimPrefix(docsReleaseTag, "v"),
			SourceSHA: docsSourceSHA, PublicSourceSHA: docsPublicSourceSHA,
			ReleaseURL: docsReleaseURL,
		}
		generatorVersion := version
		if generatorVersion == "dev" || generatorVersion == "unknown" {
			generatorVersion = release.Version
		}
		catalog, compatibility, _, err := usecasecatalog.Generate(root, release, generatorVersion)
		if err != nil {
			return err
		}
		if err := usecasecatalog.WriteJSON(filepath.Join(docsOutputDir, "stackkits-use-case-catalog-v1.json"), catalog); err != nil {
			return err
		}
		if err := usecasecatalog.WriteJSON(filepath.Join(docsOutputDir, "stackkits-compatibility-v1.json"), compatibility); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s and %s\n", filepath.Join(docsOutputDir, "stackkits-use-case-catalog-v1.json"), filepath.Join(docsOutputDir, "stackkits-compatibility-v1.json"))
		return err
	},
}

var docsEmitUseCaseOverviewCmd = &cobra.Command{
	Use:   "emit-use-case-overview",
	Short: "Render the internal derived use-case development overview",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, err := filepath.Abs(getWorkDir())
		if err != nil {
			return err
		}
		release := usecasecatalog.ReleaseIdentity{Tag: "v0.0.0", Version: "0.0.0", SourceSHA: docsSourceSHA, PublicSourceSHA: docsSourceSHA, ReleaseURL: "https://github.com/kombifyio/StackKits/releases/tag/v0.0.0"}
		var receipt *usecasecatalog.TestReceipt
		if docsTestReceipt != "" {
			data, readErr := os.ReadFile(docsTestReceipt)
			if readErr != nil {
				return readErr
			}
			receipt = &usecasecatalog.TestReceipt{}
			if decodeErr := json.Unmarshal(data, receipt); decodeErr != nil {
				return decodeErr
			}
		}
		var catalog usecasecatalog.UseCaseManifest
		var entries []usecasecatalog.InternalUseCase
		if docsTestsPassed {
			catalog, _, entries, err = usecasecatalog.GenerateWithPassedTests(root, release, version)
		} else {
			catalog, _, entries, err = usecasecatalog.GenerateWithReceipt(root, release, version, receipt)
		}
		if err != nil {
			return err
		}
		output := docsOverviewOutput
		if output == "" {
			output = filepath.Join(root, "docs", "USE_CASE_DEVELOPMENT_OVERVIEW.md")
		}
		if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(output, []byte(usecasecatalog.RenderInternalMarkdown(entries)), 0o644); err != nil {
			return err
		}
		evidenceOutput := docsEvidenceOutput
		if evidenceOutput == "" {
			evidenceOutput = filepath.Join(root, "docs", "data", "use-case-evidence", "latest.json")
		}
		evidence, err := usecasecatalog.NewEvidenceManifest(docsSourceSHA, catalog.GeneratedAt, version, entries)
		if err != nil {
			return err
		}
		if err := usecasecatalog.WriteJSON(evidenceOutput, evidence); err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), output)
		return err
	},
}

func init() {
	docsEmitReleaseManifestsCmd.Flags().StringVar(&docsReleaseTag, "release-tag", "", "published release tag (vX.Y.Z)")
	docsEmitReleaseManifestsCmd.Flags().StringVar(&docsSourceSHA, "source-sha", "", "exact private source SHA")
	docsEmitReleaseManifestsCmd.Flags().StringVar(&docsPublicSourceSHA, "public-source-sha", "", "exact public source SHA")
	docsEmitReleaseManifestsCmd.Flags().StringVar(&docsReleaseURL, "release-url", "", "exact public release URL")
	docsEmitReleaseManifestsCmd.Flags().StringVar(&docsOutputDir, "output-dir", "", "manifest output directory")
	for _, name := range []string{"release-tag", "source-sha", "public-source-sha", "release-url", "output-dir"} {
		_ = docsEmitReleaseManifestsCmd.MarkFlagRequired(name)
	}
	docsEmitUseCaseOverviewCmd.Flags().StringVar(&docsSourceSHA, "source-sha", "", "exact source SHA")
	docsEmitUseCaseOverviewCmd.Flags().StringVar(&docsOverviewOutput, "output", "", "Markdown output path")
	docsEmitUseCaseOverviewCmd.Flags().StringVar(&docsEvidenceOutput, "evidence-output", "", "evidence manifest output path")
	docsEmitUseCaseOverviewCmd.Flags().StringVar(&docsTestReceipt, "test-receipt", "", "source-SHA-bound passed test receipt")
	docsEmitUseCaseOverviewCmd.Flags().BoolVar(&docsTestsPassed, "tests-passed-for-source", false, "mark tests complete after the caller synchronously passed catalog tests for source-sha")
	_ = docsEmitUseCaseOverviewCmd.MarkFlagRequired("source-sha")
	docsCmd.AddCommand(docsEmitReleaseManifestsCmd, docsEmitUseCaseOverviewCmd)
}
