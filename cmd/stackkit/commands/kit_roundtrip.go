package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/kitio"
	"github.com/spf13/cobra"
)

var (
	kitRoundtripPath     string
	kitRoundtripEndpoint string
	kitRoundtripToken    string
	kitRoundtripJSON     bool
)

var kitRoundtripCmd = &cobra.Command{
	Use:   "roundtrip",
	Short: "Validate a kit roundtrip lossless: import -> export -> diff",
	Long: `Two modes:

LOCAL (default, no API):
	stackkit kit roundtrip --kit base-kit
	→ Reads <kit>/stackkit.yaml, imports it, exports it, re-imports the
	  exported yaml, and compares the two KitDefinitions structurally.
	  Cosmetic differences (yaml comments, quote-style) are ignored.

LIVE (--from-api):
	stackkit kit roundtrip --kit base-kit --from-api $STACKKIT_ADMIN_ENDPOINT
	→ Imports <kit>/stackkit.yaml via POST kit-import, fetches it back via
	  GET kit-export, compares server-shape against original yaml-shape.

Outputs a structured RoundTripReport. Critical differences = test failure;
cosmetic-only = test pass with note.`,
	RunE: runKitRoundtrip,
}

func init() {
	kitRoundtripCmd.Flags().StringVar(&kitRoundtripPath, "kit", "", "Kit directory (e.g. base-kit). Required.")
	kitRoundtripCmd.Flags().StringVar(&kitRoundtripEndpoint, "from-api", "", "Admin API base URL. Triggers live mode. Defaults to local mode.")
	kitRoundtripCmd.Flags().StringVar(&kitRoundtripToken, "token", "", "Bearer token. Defaults to $STACKKIT_ADMIN_TOKEN.")
	kitRoundtripCmd.Flags().BoolVar(&kitRoundtripJSON, "json", false, "Emit RoundTripReport as JSON instead of human-readable summary.")

	kitCmd.AddCommand(kitRoundtripCmd)
}

func runKitRoundtrip(cmd *cobra.Command, args []string) error {
	if kitRoundtripPath == "" {
		return fmt.Errorf("--kit is required")
	}

	yamlPath := filepath.Join(kitRoundtripPath, "stackkit.yaml")
	yamlBytes, err := os.ReadFile(yamlPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", yamlPath, err)
	}

	if kitRoundtripEndpoint == "" {
		// Local roundtrip
		report, err := kitio.LocalRoundTrip(yamlBytes)
		if err != nil {
			return err
		}
		return emitReport(report)
	}

	return runLiveRoundtrip(yamlBytes)
}

func runLiveRoundtrip(yamlBytes []byte) error {
	def, err := kitio.Import(yamlBytes)
	if err != nil {
		return fmt.Errorf("import yaml: %w", err)
	}

	client, _, errc := loadAdminClient(kitRoundtripEndpoint, kitRoundtripToken)
	if errc != nil {
		return fmt.Errorf("admin client: %w", errc)
	}

	// Import via POST kit-import
	imp, err := client.PostKitImport(def.Metadata.Name, def, false)
	if err != nil {
		return fmt.Errorf("kit-import POST: %w", err)
	}
	printInfo("kit-import status=%s contract_hash=%s", imp.Status, shortHash(imp.ContractHash))

	// Fetch back via GET kit-export
	fetched, err := client.FetchKitDefinition(def.Metadata.Name)
	if err != nil {
		return fmt.Errorf("kit-export GET: %w", err)
	}

	hashLocal, _ := kitio.CanonicalHash(def)
	hashRemote, _ := kitio.CanonicalHash(fetched)
	diffs := kitio.Diff(def, fetched)

	report := kitio.RoundTripReport{
		Slug:              def.Metadata.Name,
		OriginalHash:      hashLocal,
		ReconstructedHash: hashRemote,
		HashesEqual:       hashLocal == hashRemote,
		Differences:       diffs,
		CosmeticOnly:      onlyCosmetic(diffs),
		Formats:           []string{"yaml-via-api"},
	}
	return emitReport(report)
}

func emitReport(report kitio.RoundTripReport) error {
	if kitRoundtripJSON {
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(raw))
		return nil
	}

	if report.CosmeticOnly && report.HashesEqual {
		printSuccess("kit %s lossless roundtrip (hash %s)", report.Slug, shortHash(report.OriginalHash))
		return nil
	}
	if report.CosmeticOnly {
		printSuccess("kit %s structurally identical (hashes differ — cosmetic yaml drift only)", report.Slug)
		return nil
	}

	printError("kit %s has %d differences", report.Slug, len(report.Differences))
	for _, d := range report.Differences {
		fmt.Printf("  [%s] %s: %v -> %v   %s\n", d.Severity, d.Path, d.Original, d.Reconstructed, d.Note)
	}
	if !report.CosmeticOnly {
		return fmt.Errorf("roundtrip failed: %d critical differences", criticalCount(report.Differences))
	}
	return nil
}

func onlyCosmetic(diffs []kitio.FieldDifference) bool {
	for _, d := range diffs {
		if d.Severity == "critical" {
			return false
		}
	}
	return true
}

func criticalCount(diffs []kitio.FieldDifference) int {
	n := 0
	for _, d := range diffs {
		if d.Severity == "critical" {
			n++
		}
	}
	return n
}
