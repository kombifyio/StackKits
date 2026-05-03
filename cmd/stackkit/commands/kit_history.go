package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

var (
	kitHistorySlug     string
	kitHistoryEndpoint string
	kitHistoryToken    string
	kitHistoryLimit    int
	kitHistoryJSON     bool
)

var kitHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "Show audit-log entries for a kit (who imported when, lock changes, bypasses)",
	Long: `Calls GET /api/v1/sk/registry/stackkits/{slug}/audit and prints the
last N entries in chronological order (newest first).

Each row records:
  - action: create | update | delete | unlock | relock
  - actor:  who triggered (last_imported_by from kit-import, or current_user
    for direct SQL)
  - hash:   last_imported_hash before → after
  - lock:   is_locked before → after
  - bypass: TRUE if sk.kit_import_context was active during the op
            (legitimate kit-import OR a direct psql SET LOCAL bypass)

Use --json for machine-readable output. The /audit endpoint paginates;
--limit defaults to 20.`,
	RunE: runKitHistory,
}

func init() {
	kitHistoryCmd.Flags().StringVar(&kitHistorySlug, "slug", "", "Kit slug. Required.")
	kitHistoryCmd.Flags().StringVar(&kitHistoryEndpoint, "endpoint", "", "Admin API base URL.")
	kitHistoryCmd.Flags().StringVar(&kitHistoryToken, "token", "", "Bearer token.")
	kitHistoryCmd.Flags().IntVar(&kitHistoryLimit, "limit", 20, "Maximum number of audit entries to fetch.")
	kitHistoryCmd.Flags().BoolVar(&kitHistoryJSON, "json", false, "Emit JSON instead of human-readable table.")
	kitCmd.AddCommand(kitHistoryCmd)
}

type auditEntry struct {
	ID                  string `json:"id"`
	Action              string `json:"action"`
	Actor               string `json:"actor"`
	HashBefore          string `json:"hashBefore"`
	HashAfter           string `json:"hashAfter"`
	WasLockedBefore     bool   `json:"wasLockedBefore"`
	WasLockedAfter      bool   `json:"wasLockedAfter"`
	SourceOfTruthBefore string `json:"sourceOfTruthBefore"`
	SourceOfTruthAfter  string `json:"sourceOfTruthAfter"`
	ContextBypass       bool   `json:"contextBypass"`
	CreatedAt           string `json:"createdAt"`
}

func runKitHistory(cmd *cobra.Command, args []string) error {
	if kitHistorySlug == "" {
		return fmt.Errorf("--slug is required")
	}
	client, endpoint, err := loadAdminClient(kitHistoryEndpoint, kitHistoryToken)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/api/v1/sk/registry/stackkits/%s/audit?limit=%d",
		endpoint, kitHistorySlug, kitHistoryLimit)
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
		Data []auditEntry `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if kitHistoryJSON {
		out, _ := json.MarshalIndent(page.Data, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	if len(page.Data) == 0 {
		fmt.Printf("kit %s: no audit entries\n", kitHistorySlug)
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TIMESTAMP\tACTION\tACTOR\tBYPASS\tLOCK Δ\tHASH Δ")
	for _, e := range page.Data {
		ts := formatTS(e.CreatedAt)
		bypass := ""
		if e.ContextBypass {
			bypass = "✱"
		}
		lockDelta := boolDelta(e.WasLockedBefore, e.WasLockedAfter)
		hashDelta := hashDelta(e.HashBefore, e.HashAfter)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", ts, e.Action, e.Actor, bypass, lockDelta, hashDelta)
	}
	return tw.Flush()
}

func formatTS(s string) string {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC().Format("2006-01-02 15:04:05Z")
	}
	return s
}

func boolDelta(before, after bool) string {
	if before == after {
		if before {
			return "🔒→🔒"
		}
		return " → "
	}
	if after {
		return " →🔒"
	}
	return "🔒→ "
}

func hashDelta(before, after string) string {
	short := func(s string) string {
		if len(s) > 8 {
			return s[:8]
		}
		if s == "" {
			return "(empty)"
		}
		return s
	}
	if before == after {
		return short(after)
	}
	return short(before) + "→" + short(after)
}
