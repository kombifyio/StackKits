package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/kitio"
	"github.com/spf13/cobra"
)

var (
	kitExportSlug     string
	kitExportEndpoint string
	kitExportToken    string
	kitExportOutput   string
	kitExportFormat   string
	kitExportFromYAML string
)

var kitExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export a kit definition from DB (or local yaml) into yaml/CUE/tfvars/compose",
	Long: `Reverse of "stackkit kit import". Reads either:
  - the live Admin API (--from-api ENDPOINT --token TOKEN, --slug X) — fetches GET /kit-export
  - a local yaml file (--from-yaml PATH) — useful for testing without API

and writes regenerated artifacts to --output DIR. Formats:
  --format yaml     (stackkit.yaml only)
  --format cue      (stackfile.cue + services.cue)
  --format tfvars   (kit.tfvars.json — kit-level Terraform contract)
  --format compose  (kit-overview.compose.yml)
  --format all      (everything; default)`,
	RunE: runKitExport,
}

func init() {
	kitExportCmd.Flags().StringVar(&kitExportSlug, "slug", "", "Kit slug (e.g. base-kit). Required when using --from-api.")
	kitExportCmd.Flags().StringVar(&kitExportEndpoint, "from-api", "", "Admin API base URL. Defaults to $STACKKIT_ADMIN_ENDPOINT.")
	kitExportCmd.Flags().StringVar(&kitExportToken, "token", "", "Bearer token. Defaults to $STACKKIT_ADMIN_TOKEN.")
	kitExportCmd.Flags().StringVar(&kitExportFromYAML, "from-yaml", "", "Skip API and load kit definition from this stackkit.yaml. Useful for offline tests.")
	kitExportCmd.Flags().StringVar(&kitExportOutput, "output", "", "Output directory. Required.")
	kitExportCmd.Flags().StringVar(&kitExportFormat, "format", "all", "Output format: yaml | cue | tfvars | compose | all")

	kitCmd.AddCommand(kitExportCmd)
}

func runKitExport(cmd *cobra.Command, args []string) error {
	if kitExportOutput == "" {
		return fmt.Errorf("--output is required")
	}

	def, err := loadKitDefinition()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(kitExportOutput, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", kitExportOutput, err)
	}

	switch kitExportFormat {
	case "yaml":
		return writeYAMLOnly(def)
	case "cue":
		return writeCUEOnly(def)
	case "tfvars":
		return kitio.ExportTerraform(def, filepath.Join(kitExportOutput, "deploy"))
	case "compose":
		return kitio.ExportCompose(def, filepath.Join(kitExportOutput, "compose"))
	case "all", "":
		return writeAll(def)
	default:
		return fmt.Errorf("unknown --format %q (yaml | cue | tfvars | compose | all)", kitExportFormat)
	}
}

func loadKitDefinition() (kitio.KitDefinition, error) {
	if kitExportFromYAML != "" {
		bytes, err := os.ReadFile(kitExportFromYAML)
		if err != nil {
			return kitio.KitDefinition{}, fmt.Errorf("read %s: %w", kitExportFromYAML, err)
		}
		return kitio.Import(bytes)
	}

	if kitExportSlug == "" {
		return kitio.KitDefinition{}, fmt.Errorf("--slug or --from-yaml required")
	}

	client, _, err := loadAdminClient(kitExportEndpoint, kitExportToken)
	if err != nil {
		return kitio.KitDefinition{}, fmt.Errorf("admin client: %w (or use --from-yaml for offline)", err)
	}
	def, err := client.FetchKitDefinition(kitExportSlug)
	if err != nil {
		return kitio.KitDefinition{}, fmt.Errorf("fetch from admin: %w", err)
	}
	return def, nil
}

func writeYAMLOnly(def kitio.KitDefinition) error {
	out, err := kitio.ExportYAML(def)
	if err != nil {
		return fmt.Errorf("export yaml: %w", err)
	}
	yamlPath := filepath.Join(kitExportOutput, "stackkit.yaml")
	if err := os.WriteFile(yamlPath, out, 0o644); err != nil {
		return fmt.Errorf("write yaml: %w", err)
	}
	printSuccess("yaml -> %s (%d bytes)", yamlPath, len(out))
	return nil
}

func writeCUEOnly(def kitio.KitDefinition) error {
	stackfile, services, err := kitio.ExportCUE(def)
	if err != nil {
		return fmt.Errorf("export cue: %w", err)
	}
	if err := os.WriteFile(filepath.Join(kitExportOutput, "stackfile.cue"), stackfile, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(kitExportOutput, "services.cue"), services, 0o644); err != nil {
		return err
	}
	printSuccess("cue -> stackfile.cue + services.cue in %s", kitExportOutput)
	return nil
}

func writeAll(def kitio.KitDefinition) error {
	if err := writeYAMLOnly(def); err != nil {
		return err
	}
	if err := writeCUEOnly(def); err != nil {
		return err
	}
	if err := kitio.ExportTerraform(def, filepath.Join(kitExportOutput, "deploy")); err != nil {
		return fmt.Errorf("tfvars: %w", err)
	}
	printSuccess("tfvars -> %s/deploy/", kitExportOutput)
	if err := kitio.ExportCompose(def, filepath.Join(kitExportOutput, "compose")); err != nil {
		return fmt.Errorf("compose: %w", err)
	}
	printSuccess("compose -> %s/compose/", kitExportOutput)
	return nil
}
