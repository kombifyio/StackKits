package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
)

var (
	kitUnlockSlug     string
	kitUnlockEndpoint string
	kitUnlockToken    string
	kitUnlockReason   string
	kitUnlockConfirm  bool
)

var kitUnlockCmd = &cobra.Command{
	Use:   "unlock",
	Short: "Unlock a kit so a corrected re-import can land (audited)",
	Long: `Sets is_locked=false on the sk_stackkit row. Required when an earlier
kit-import wrote bad content and a re-import needs to overwrite it
without going through direct SQL bypass.

This goes through the admin endpoint POST /api/v1/sk/registry/stackkits/
{slug}/unlock which:
  1. Verifies the caller can authenticate (service-auth or admin session)
  2. Sets sk.kit_import_context = 'true' inside the transaction
  3. UPDATE sk_stackkit SET is_locked = false, source_of_truth = 'db'
  4. The audit trigger records the unlock action with actor + reason

After unlock, run kit import again with corrected yaml. The new import
re-locks the row.

Always provide a --reason — it lands in the audit metadata for ops
post-mortems.`,
	RunE: runKitUnlock,
}

func init() {
	kitUnlockCmd.Flags().StringVar(&kitUnlockSlug, "slug", "", "Kit slug. Required.")
	kitUnlockCmd.Flags().StringVar(&kitUnlockEndpoint, "endpoint", "", "Admin API base URL.")
	kitUnlockCmd.Flags().StringVar(&kitUnlockToken, "token", "", "Bearer token.")
	kitUnlockCmd.Flags().StringVar(&kitUnlockReason, "reason", "", "Why are you unlocking? Lands in audit log. Required.")
	kitUnlockCmd.Flags().BoolVar(&kitUnlockConfirm, "yes", false, "Skip the interactive confirmation prompt.")
	kitCmd.AddCommand(kitUnlockCmd)
}

type unlockResult struct {
	Status        string `json:"status"`
	Slug          string `json:"slug"`
	IsLocked      bool   `json:"isLocked"`
	SourceOfTruth string `json:"sourceOfTruth"`
}

func runKitUnlock(cmd *cobra.Command, args []string) error {
	if kitUnlockSlug == "" {
		return fmt.Errorf("--slug is required")
	}
	if kitUnlockReason == "" {
		return fmt.Errorf("--reason is required (audit log)")
	}
	if !kitUnlockConfirm {
		printWarning("about to unlock kit %q — re-import is the only way to re-lock", kitUnlockSlug)
		printInfo("reason: %s", kitUnlockReason)
		printInfo("re-run with --yes to confirm")
		return fmt.Errorf("confirmation required")
	}

	client, endpoint, err := loadAdminClient(kitUnlockEndpoint, kitUnlockToken)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/api/v1/sk/registry/stackkits/%s/unlock", endpoint, kitUnlockSlug)
	body, _ := json.Marshal(map[string]interface{}{
		"reason": kitUnlockReason,
	})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := attachClientAuth(req, client); err != nil {
		return err
	}

	resp, err := client.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s: status=%d", url, resp.StatusCode)
	}

	var result unlockResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		printSuccess("kit %s unlock POSTed (response decode failed: %v)", kitUnlockSlug, err)
		return nil
	}
	printSuccess("kit %s unlocked (is_locked=%v, source_of_truth=%s)", result.Slug, result.IsLocked, result.SourceOfTruth)
	printInfo("next: run `stackkit kit import --kit %s` with corrected yaml to re-lock", result.Slug)
	return nil
}
