package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"text/tabwriter"

	"github.com/kombifyio/stackkits/internal/auth"
	"github.com/spf13/cobra"
)

var (
	kitListEndpoint string
	kitListToken    string
	kitListJSON     bool
)

var kitListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all kits in the Admin DB with lock + source-of-truth state",
	Long: `Calls GET /api/v1/sk/registry/stackkits and renders the result as a
table (default) or JSON (--json). Useful for "what's currently in the DB?"
checks before unlock / re-import.

Auth resolves the same way as kit import / export:
  SERVICE_AUTH_SECRET → HS256 service-auth (preferred)
  STACKKIT_ADMIN_TOKEN / KOMBIFY_ADMIN_API_KEY → legacy Bearer
  none → unauthenticated (admin will 401)`,
	RunE: runKitList,
}

func init() {
	kitListCmd.Flags().StringVar(&kitListEndpoint, "endpoint", "", "Admin API base URL. Defaults to STACKKIT_ADMIN_ENDPOINT or ADMIN_PUBLIC_API_URL.")
	kitListCmd.Flags().StringVar(&kitListToken, "token", "", "Bearer token. Defaults to STACKKIT_ADMIN_TOKEN.")
	kitListCmd.Flags().BoolVar(&kitListJSON, "json", false, "Emit JSON instead of human-readable table.")
	kitCmd.AddCommand(kitListCmd)
}

type stackkitRow struct {
	Slug             string `json:"slug"`
	Name             string `json:"name"`
	Version          string `json:"version"`
	Lifecycle        string `json:"lifecycle"`
	IsLocked         bool   `json:"isLocked"`
	SourceOfTruth    string `json:"sourceOfTruth"`
	CueSourcePath    string `json:"cueSourcePath"`
	LastImportedAt   string `json:"lastImportedAt"`
	LastImportedBy   string `json:"lastImportedBy"`
	LastImportedHash string `json:"lastImportedHash"`
}

func runKitList(cmd *cobra.Command, args []string) error {
	client, endpoint, err := loadAdminClient(kitListEndpoint, kitListToken)
	if err != nil {
		return fmt.Errorf("admin client: %w", err)
	}
	url := fmt.Sprintf("%s/api/v1/sk/registry/stackkits?pageSize=100", endpoint)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if err := attachClientAuth(req, client); err != nil {
		return err
	}

	resp, err := client.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s: status=%d", url, resp.StatusCode)
	}

	var page struct {
		Data []stackkitRow `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if kitListJSON {
		out, _ := json.MarshalIndent(page.Data, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SLUG\tVERSION\tLIFECYCLE\tLOCK\tSOURCE\tCUE PATH\tIMPORTED BY\tHASH")
	for _, r := range page.Data {
		lock := " "
		if r.IsLocked {
			lock = "🔒"
		}
		hashShort := r.LastImportedHash
		if len(hashShort) > 8 {
			hashShort = hashShort[:8]
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Slug, r.Version, r.Lifecycle, lock, r.SourceOfTruth, r.CueSourcePath, r.LastImportedBy, hashShort)
	}
	return tw.Flush()
}

// attachClientAuth is shared by kit list/unlock/history — they each build a
// raw http.Request because the admin endpoints aren't all wrapped in
// AdminClient methods. Mirrors AdminClient.attachAuth.
func attachClientAuth(req *http.Request, _ interface{}) error {
	if secret := os.Getenv("SERVICE_AUTH_SECRET"); secret != "" {
		token, err := auth.SignServiceToken("stackkits", "administration", secret, auth.DefaultTokenTTL)
		if err != nil {
			return err
		}
		req.Header.Set(auth.HeaderServiceAuth, token)
		return nil
	}
	if t := os.Getenv("STACKKIT_ADMIN_TOKEN"); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
		return nil
	}
	if t := os.Getenv("KOMBIFY_ADMIN_API_KEY"); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	return nil
}
