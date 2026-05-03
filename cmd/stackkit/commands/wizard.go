package commands

// wizard.go implements `stackkit wizard report` which posts locally-captured
// wizard answers (YAML/JSON spec the user filled out) to the Admin API for
// telemetry, intent capture, and future Tier-3 promotion analysis.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	wizardReportFile      string
	wizardReportEndpoint  string
	wizardReportToken     string
	wizardReportStackKit  string
	wizardReportContext   string
	wizardReportCompute   string
	wizardReportTenant    string
	wizardReportSource    string
	wizardReportIntents   []string
	wizardReportDryRun    bool
)

var wizardCmd = &cobra.Command{
	Use:   "wizard",
	Short: "Wizard telemetry operations",
}

var wizardReportCmd = &cobra.Command{
	Use:   "report",
	Short: "Post wizard answers and free-form intents to the Admin API",
	RunE:  runWizardReport,
}

func init() {
	wizardReportCmd.Flags().StringVar(&wizardReportFile, "answers", "", "Path to wizard answers (.yaml or .json). Required unless --intent is set.")
	wizardReportCmd.Flags().StringVar(&wizardReportEndpoint, "endpoint", "", "Admin API base URL. Defaults to $STACKKIT_ADMIN_ENDPOINT.")
	wizardReportCmd.Flags().StringVar(&wizardReportToken, "token", "", "Bearer token. Defaults to $STACKKIT_ADMIN_TOKEN.")
	wizardReportCmd.Flags().StringVar(&wizardReportStackKit, "stackkit", "", "StackKit slug (e.g. base-kit)")
	wizardReportCmd.Flags().StringVar(&wizardReportContext, "derived-context", "", "Derived context (local/cloud/pi)")
	wizardReportCmd.Flags().StringVar(&wizardReportCompute, "derived-compute", "", "Derived compute tier (shared/dedicated)")
	wizardReportCmd.Flags().StringVar(&wizardReportTenant, "tenant", "", "Tenant UUID (optional)")
	wizardReportCmd.Flags().StringVar(&wizardReportSource, "source", "cli", "Origin marker (cli/ui/api)")
	wizardReportCmd.Flags().StringSliceVar(&wizardReportIntents, "intent", nil, "Free-form intent text (repeatable)")
	wizardReportCmd.Flags().BoolVar(&wizardReportDryRun, "dry-run", false, "Print payload without POSTing")

	wizardCmd.AddCommand(wizardReportCmd)
	rootCmd.AddCommand(wizardCmd)
}

type wizardReportPayload struct {
	TenantID          string                 `json:"tenantId,omitempty"`
	StackkitSlug        string                 `json:"stackkitSlug,omitempty"`
	WizardSchemaVersion string                 `json:"wizardSchemaVersion"`
	Answers             map[string]interface{} `json:"answers"`
	DerivedContext      string                 `json:"derivedContext,omitempty"`
	DerivedComputeTier  string                 `json:"derivedComputeTier,omitempty"`
	Source              string                 `json:"source"`
	UserAgent           string                 `json:"userAgent"`
	Intents             []string               `json:"intents,omitempty"`
}

func runWizardReport(cmd *cobra.Command, args []string) error {
	endpoint := wizardReportEndpoint
	if endpoint == "" {
		endpoint = os.Getenv("STACKKIT_ADMIN_ENDPOINT")
	}
	if endpoint == "" && !wizardReportDryRun {
		return fmt.Errorf("--endpoint or $STACKKIT_ADMIN_ENDPOINT required (or use --dry-run)")
	}

	token := wizardReportToken
	if token == "" {
		token = os.Getenv("STACKKIT_ADMIN_TOKEN")
	}

	answers := map[string]interface{}{}
	if wizardReportFile != "" {
		raw, err := os.ReadFile(wizardReportFile)
		if err != nil {
			return fmt.Errorf("read answers file: %w", err)
		}
		if err := yaml.Unmarshal(raw, &answers); err != nil {
			if err2 := json.Unmarshal(raw, &answers); err2 != nil {
				return fmt.Errorf("parse answers (yaml=%v, json=%v)", err, err2)
			}
		}
	}

	payload := wizardReportPayload{
		TenantID:            wizardReportTenant,
		StackkitSlug:        wizardReportStackKit,
		WizardSchemaVersion: "1.0.0",
		Answers:             answers,
		DerivedContext:      wizardReportContext,
		DerivedComputeTier:  wizardReportCompute,
		Source:              wizardReportSource,
		UserAgent:           fmt.Sprintf("stackkit/%s (%s/%s)", version, runtime.GOOS, runtime.GOARCH),
		Intents:             wizardReportIntents,
	}

	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	if wizardReportDryRun {
		fmt.Println(string(body))
		return nil
	}

	url := trimTrailingSlash(endpoint) + "/api/v1/sk/wizard/answers"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s: status=%d body=%s", url, resp.StatusCode, string(respBody))
	}

	printSuccess("Wizard answer reported to %s", url)
	printVerbose("response: %s", string(respBody))
	return nil
}
