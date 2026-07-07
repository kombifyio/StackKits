package commands

// registry_tool_update_evidence.go implements `stackkit registry
// tool-update-evidence` (ADR-0028 Decision 3, step 4): the service-auth
// callback the tool-update-pr.yml workflow posts after a bump PR's gates run.
// It writes an sk_tool_update_run row and flips the observation status via the
// Admin endpoint. Auth reuses the same service-auth path as `module release`
// (SERVICE_AUTH_SECRET mints an HS256 svc="stackkits" JWT).

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var (
	tueObservationID string
	tueEndpoint      string
	tueToken         string
	tueOutcome       string
	tuePRURL         string
	tuePRNumber      int
	tueChecks        string
	tueDryRun        bool
)

var toolUpdateEvidenceCmd = &cobra.Command{
	Use:   "tool-update-evidence",
	Short: "Post tool-update verification evidence to the Admin registry (ADR-0028)",
	Long: "Record an sk_tool_update_run for an upstream-release bump PR and flip the\n" +
		"observation status. Called by tool-update-pr.yml once the PR gates have run.",
	RunE: runToolUpdateEvidence,
}

func init() {
	toolUpdateEvidenceCmd.Flags().StringVar(&tueObservationID, "observation-id", "", "sk_tool_release_observation id. Required.")
	toolUpdateEvidenceCmd.Flags().StringVar(&tueEndpoint, "endpoint", "", "Admin API base URL. Required.")
	toolUpdateEvidenceCmd.Flags().StringVar(&tueToken, "token", "", "Bearer token. Defaults to $STACKKIT_ADMIN_TOKEN/$KOMBIFY_ADMIN_API_KEY; $SERVICE_AUTH_SECRET takes precedence.")
	toolUpdateEvidenceCmd.Flags().StringVar(&tueOutcome, "outcome", "", "Verification outcome: verify_green | verify_red. Required.")
	toolUpdateEvidenceCmd.Flags().StringVar(&tuePRURL, "pr-url", "", "URL of the bump PR")
	toolUpdateEvidenceCmd.Flags().IntVar(&tuePRNumber, "pr-number", 0, "Number of the bump PR")
	toolUpdateEvidenceCmd.Flags().StringVar(&tueChecks, "checks", "{}", "JSON object of gate results (cue_vet/contract_hash/module_smoke/e2e + run URLs)")
	toolUpdateEvidenceCmd.Flags().BoolVar(&tueDryRun, "dry-run", false, "Build the request but do not POST")

	registryCmd.AddCommand(toolUpdateEvidenceCmd)
}

// toolUpdateEvidencePayload matches the Admin sk_tool_update_run insert.
type toolUpdateEvidencePayload struct {
	ObservationID string          `json:"observation_id"`
	PRURL         string          `json:"pr_url,omitempty"`
	PRNumber      int             `json:"pr_number,omitempty"`
	Outcome       string          `json:"outcome"`
	Checks        json.RawMessage `json:"checks"`
}

func runToolUpdateEvidence(cmd *cobra.Command, args []string) error {
	if tueObservationID == "" {
		return fmt.Errorf("--observation-id is required")
	}
	if tueEndpoint == "" {
		return fmt.Errorf("--endpoint is required")
	}
	switch tueOutcome {
	case "verify_green", "verify_red":
	default:
		return fmt.Errorf("--outcome must be verify_green or verify_red, got %q", tueOutcome)
	}
	if !json.Valid([]byte(tueChecks)) {
		return fmt.Errorf("--checks is not valid JSON")
	}

	payload := toolUpdateEvidencePayload{
		ObservationID: tueObservationID,
		PRURL:         tuePRURL,
		PRNumber:      tuePRNumber,
		Outcome:       tueOutcome,
		Checks:        json.RawMessage(tueChecks),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal evidence: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/sk/registry/tool-updates/%s/evidence", trimTrailingSlash(tueEndpoint), tueObservationID)
	if tueDryRun {
		printInfo("--dry-run POST %s", url)
		fmt.Println(string(body))
		return nil
	}

	token := resolveToolUpdateToken()
	if err := postJSON(url, token, body); err != nil {
		return fmt.Errorf("post evidence: %w", err)
	}
	printSuccess("posted tool-update evidence for observation %s (%s)", tueObservationID, tueOutcome)
	return nil
}

func resolveToolUpdateToken() string {
	if tueToken != "" {
		return tueToken
	}
	return resolveModuleReleaseToken()
}
