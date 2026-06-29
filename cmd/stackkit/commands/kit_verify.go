package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/kitio"
	"github.com/spf13/cobra"
)

var (
	kitVerifySlug     string
	kitVerifyKitDir   string
	kitVerifyEndpoint string
	kitVerifyToken    string
	kitVerifyStrict   bool
)

var kitVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify the DB representation of a kit matches the local CUE/yaml source",
	Long: `Compares the live Admin API kit definition (GET /kit-export) against
the in-tree stackkit.yaml. Used as a CI guard:

	stackkit kit verify --slug basement-kit --kit-dir basement-kit

Exit codes:
  0 — DB and filesystem agree (cosmetic-only differences allowed)
  1 — Critical differences found (or --strict and any cosmetic diff)
  2 — Could not connect to Admin API or read local yaml

When STACKKIT_ADMIN_ENDPOINT is set, the API path is the default; pass
--kit-dir explicitly for the filesystem source. With --strict, any diff
fails (used for production gating).`,
	RunE: runKitVerify,
}

func init() {
	kitVerifyCmd.Flags().StringVar(&kitVerifySlug, "slug", "", "Kit slug to fetch from Admin API (defaults to dir basename)")
	kitVerifyCmd.Flags().StringVar(&kitVerifyKitDir, "kit-dir", "", "Path to the kit directory holding stackkit.yaml. Required.")
	kitVerifyCmd.Flags().StringVar(&kitVerifyEndpoint, "endpoint", "", "Admin API base URL. Defaults to $STACKKIT_ADMIN_ENDPOINT.")
	kitVerifyCmd.Flags().StringVar(&kitVerifyToken, "token", "", "Bearer token. Defaults to $STACKKIT_ADMIN_TOKEN.")
	kitVerifyCmd.Flags().BoolVar(&kitVerifyStrict, "strict", false, "Treat any difference (incl. cosmetic) as failure")

	kitCmd.AddCommand(kitVerifyCmd)
}

func runKitVerify(cmd *cobra.Command, args []string) error {
	if kitVerifyKitDir == "" {
		return fmt.Errorf("--kit-dir is required")
	}
	slug := kitVerifySlug
	if slug == "" {
		slug = filepath.Base(filepath.Clean(kitVerifyKitDir))
	}

	yamlPath := filepath.Join(kitVerifyKitDir, "stackkit.yaml")
	yamlBytes, err := os.ReadFile(yamlPath)
	if err != nil {
		exitWith(2, "read %s: %v", yamlPath, err)
	}

	localDef, err := kitio.Import(yamlBytes)
	if err != nil {
		exitWith(2, "import local yaml: %v", err)
	}

	client, _, err := loadAdminClient(kitVerifyEndpoint, kitVerifyToken)
	if err != nil {
		exitWith(2, "admin client: %v", err)
	}
	dbDef, err := client.FetchKitDefinition(slug)
	if err != nil {
		exitWith(2, "fetch kit-export: %v", err)
	}

	diffs := kitio.Diff(localDef, dbDef)
	critical := 0
	cosmetic := 0
	for _, d := range diffs {
		if d.Severity == "critical" {
			critical++
		} else {
			cosmetic++
		}
	}

	printInfo("kit %s: %d critical / %d cosmetic differences", slug, critical, cosmetic)
	for _, d := range diffs {
		fmt.Printf("  [%s] %s\n    local:      %v\n    admin-db:   %v\n    note:       %s\n",
			d.Severity, d.Path, d.Original, d.Reconstructed, d.Note)
	}

	if critical > 0 {
		return fmt.Errorf("kit %s has %d CRITICAL differences (DB diverged from filesystem)", slug, critical)
	}
	if kitVerifyStrict && cosmetic > 0 {
		return fmt.Errorf("kit %s has %d cosmetic differences (--strict)", slug, cosmetic)
	}

	printSuccess("kit %s: DB matches filesystem (cosmetic-only ok)", slug)
	return nil
}

func exitWith(code int, format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(code)
}
