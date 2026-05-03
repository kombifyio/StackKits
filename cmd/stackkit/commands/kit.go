package commands

// kit.go implements `stackkit kit` subcommands.
//
// `stackkit kit import` reads the kit definition (stackkit.yaml + optional
// resolved CUE) and POSTs it to the Admin API. ADR-0012 mandates one-way
// CUE -> DB ingest; this CLI is the only blessed entry point.
//
// Usage:
//   stackkit kit import --kit base-kit [--endpoint URL] [--token TOKEN] [--dry-run]
//
// The CLI:
//   1. Loads <kit>/stackkit.yaml as a generic YAML map (preserves all sections).
//   2. Adds meta fields (cueSourcePath, importedBy).
//   3. Optionally posts to Admin API; --dry-run prints the payload.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	kitImportPath     string
	kitImportEndpoint string
	kitImportToken    string
	kitImportBy       string
	kitImportDryRun   bool
	kitImportOutput   string
)

var kitCmd = &cobra.Command{
	Use:   "kit",
	Short: "StackKit kit-definition operations (ADR-0012)",
	Long: `Manage StackKit kit-level definitions in the DB.

The kit-import path reads a kit's stackkit.yaml + (optional) resolved CUE
and pushes it into the sk_stackkit + child tables via the Admin API.
This is a one-way CUE -> DB ingest. The DB cannot push back to CUE.

Once imported, the sk_stackkit row is locked: direct admin-UI edits are
blocked by a DB-level trigger. Re-importing is the only way to update.`,
}

var kitImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Import a kit definition (stackkit.yaml) into the DB",
	RunE:  runKitImport,
}

func init() {
	kitImportCmd.Flags().StringVar(&kitImportPath, "kit", "", "Kit directory (e.g. base-kit, modern-homelab, ha-kit). Required.")
	kitImportCmd.Flags().StringVar(&kitImportEndpoint, "endpoint", "", "Admin API base URL. Defaults to $STACKKIT_ADMIN_ENDPOINT.")
	kitImportCmd.Flags().StringVar(&kitImportToken, "token", "", "Bearer token. Defaults to $STACKKIT_ADMIN_TOKEN.")
	kitImportCmd.Flags().StringVar(&kitImportBy, "imported-by", "", "Identifier of the importer. Defaults to $USER or 'cli'.")
	kitImportCmd.Flags().BoolVar(&kitImportDryRun, "dry-run", false, "Compute payload + hash; do not write to DB. Endpoint accepts dryRun=true and returns what would change.")
	kitImportCmd.Flags().StringVar(&kitImportOutput, "output", "", "Write the resolved payload to this path (JSON). If unset and --dry-run, prints to stdout.")

	kitCmd.AddCommand(kitImportCmd)
	rootCmd.AddCommand(kitCmd)
}

func runKitImport(cmd *cobra.Command, args []string) error {
	if kitImportPath == "" {
		return fmt.Errorf("--kit is required (e.g. --kit base-kit)")
	}
	absPath, err := filepath.Abs(kitImportPath)
	if err != nil {
		return fmt.Errorf("resolve kit path: %w", err)
	}

	yamlPath := filepath.Join(absPath, "stackkit.yaml")
	yamlBytes, err := os.ReadFile(yamlPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", yamlPath, err)
	}

	// Parse as generic map so we preserve every section the YAML carries.
	// Server-side mapping decides which sections end up in which table.
	var def map[string]interface{}
	if err := yaml.Unmarshal(yamlBytes, &def); err != nil {
		return fmt.Errorf("parse yaml: %w", err)
	}
	def = normalizeYAML(def)

	// Inject meta fields the server uses
	kitSlug := filepath.Base(strings.TrimRight(absPath, string(filepath.Separator)))
	def["cueSourcePath"] = kitSlug
	by := kitImportBy
	if by == "" {
		if u := os.Getenv("USER"); u != "" {
			by = u
		} else {
			by = "cli"
		}
	}
	def["importedBy"] = by
	def["dryRun"] = kitImportDryRun

	// Pull slug from metadata.name for endpoint URL
	md, _ := def["metadata"].(map[string]interface{})
	slugFromYAML, _ := md["name"].(string)
	if slugFromYAML == "" {
		return fmt.Errorf("metadata.name missing in %s", yamlPath)
	}

	payload, err := json.MarshalIndent(def, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	if kitImportOutput != "" {
		if err := os.WriteFile(kitImportOutput, payload, 0o644); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
		printInfo("payload written to %s", kitImportOutput)
	}

	if kitImportDryRun && kitImportEndpoint == "" && os.Getenv("STACKKIT_ADMIN_ENDPOINT") == "" && os.Getenv("ADMIN_API_URL") == "" {
		// Pure offline dry-run, no endpoint at all
		if kitImportOutput == "" {
			fmt.Println(string(payload))
		}
		printSuccess("kit %s parsed (dry-run, no endpoint configured)", slugFromYAML)
		return nil
	}

	endpoint := kitImportEndpoint
	if endpoint == "" {
		endpoint = os.Getenv("STACKKIT_ADMIN_ENDPOINT")
	}
	if endpoint == "" {
		endpoint = os.Getenv("ADMIN_PUBLIC_API_URL")
	}
	if endpoint == "" {
		endpoint = os.Getenv("ADMIN_API_URL")
	}
	if endpoint == "" {
		return fmt.Errorf("--endpoint, $STACKKIT_ADMIN_ENDPOINT, or $ADMIN_PUBLIC_API_URL required (or use --dry-run without endpoint for local validation)")
	}
	// Strip trailing /api/v1 — endpoint URL below appends the full path
	endpoint = strings.TrimSuffix(strings.TrimRight(endpoint, "/"), "/api/v1")

	token := kitImportToken
	if token == "" {
		token = os.Getenv("STACKKIT_ADMIN_TOKEN")
	}
	if token == "" {
		token = os.Getenv("KOMBIFY_ADMIN_API_KEY")
	}

	url := fmt.Sprintf("%s/api/v1/sk/registry/stackkits/%s/kit-import",
		trimTrailingSlash(endpoint), slugFromYAML)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := attachKitClientAuth(req, token); err != nil {
		return fmt.Errorf("attach auth: %w", err)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s: status=%d body=%s", url, resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	_ = json.Unmarshal(respBody, &result)
	status, _ := result["status"].(string)
	hash, _ := result["contractHash"].(string)
	switch status {
	case "no-op":
		printSuccess("kit %s unchanged (hash=%s)", slugFromYAML, shortHash(hash))
	case "dry-run":
		printSuccess("kit %s dry-run OK (hash=%s)", slugFromYAML, shortHash(hash))
		fmt.Println(string(respBody))
	case "created":
		printSuccess("kit %s CREATED (hash=%s)", slugFromYAML, shortHash(hash))
	case "updated":
		printSuccess("kit %s UPDATED (hash=%s)", slugFromYAML, shortHash(hash))
	default:
		printSuccess("kit %s imported (status=%s)", slugFromYAML, status)
		fmt.Println(string(respBody))
	}
	return nil
}

// normalizeYAML walks a yaml.Unmarshal result and converts
// map[interface{}]interface{} into map[string]interface{} so json.Marshal
// works. yaml.v3 already returns string-keyed maps in most cases, but
// nested arrays of maps occasionally need this pass.
func normalizeYAML(in interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	switch m := in.(type) {
	case map[string]interface{}:
		for k, v := range m {
			out[k] = normalizeValue(v)
		}
	case map[interface{}]interface{}:
		for k, v := range m {
			ks, ok := k.(string)
			if !ok {
				ks = fmt.Sprintf("%v", k)
			}
			out[ks] = normalizeValue(v)
		}
	}
	return out
}

func normalizeValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}, map[interface{}]interface{}:
		return normalizeYAML(t)
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, x := range t {
			out[i] = normalizeValue(x)
		}
		return out
	default:
		return v
	}
}
