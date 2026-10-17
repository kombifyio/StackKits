package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/ownernotify"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/spf13/cobra"
)

var (
	notifyJSON         bool
	notifyChannel      string
	notifyResolvedPlan string
)

var notifyCmd = &cobra.Command{
	Use:   "notify",
	Short: "Owner notifications declared in the StackSpec",
	Annotations: map[string]string{
		noDeployObservabilityAnnotation: "true",
	},
}

var notifyTestCmd = &cobra.Command{
	Use:   "test",
	Short: "Send a delivery check to the owner notification channels",
	Args:  cobra.NoArgs,
	Long: `Send a delivery check to every owner notification channel the verified
ResolvedPlan declares (spec.notifications), or to one channel with --channel.

The plan carries only a secret-free projection of each channel. The check
reads the destination from the StackSpec on this server, refuses a channel
whose destination no longer matches the verified plan, resolves credentials
from local custody and records one receipt per channel under
.stackkit/runs/owner-notifications. Receipts never contain the topic,
recipients, credentials or message body.`,
	Example: `  # Prove every declared channel
  stackkit notify test

  # Prove one channel and print the receipts as JSON
  stackkit notify test --channel ntfy --json`,
	RunE: runNotifyTest,
}

func init() {
	notifyTestCmd.Flags().BoolVar(&notifyJSON, "json", false, "Output receipts as JSON")
	notifyTestCmd.Flags().StringVar(&notifyChannel, "channel", "", "Deliver only to this declared channel name")
	notifyTestCmd.Flags().StringVar(&notifyResolvedPlan, "resolved-plan", "", "Canonical Architecture v2 ResolvedPlan (default: beneath the StackSpec generation.outputRoot)")
	notifyCmd.AddCommand(notifyTestCmd)
	rootCmd.AddCommand(notifyCmd)
}

func runNotifyTest(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	wd := getWorkDir()
	message := ownernotify.Message{
		Event: ownernotify.EventVerify,
		Title: "StackKit delivery check",
		Body:  "Your StackKit can reach you on this channel. No action is needed.",
		Tags:  []string{"white_check_mark"},
	}
	receipts, err := deliverOwnerNotification(ctx, wd, notifyResolvedPlan, notifyChannel, message)
	if notifyJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		if encodeErr := encoder.Encode(map[string]any{"apiVersion": "stackkit.notify-test/v1", "receipts": receipts}); encodeErr != nil {
			return encodeErr
		}
	} else {
		for _, receipt := range receipts {
			line := fmt.Sprintf("%s (%s): %s", receipt.Channel, receipt.Type, receipt.Result)
			if receipt.Reason != "" {
				line += " - " + receipt.Reason
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), line)
		}
	}
	return err
}

// deliverOwnerNotification sends message through the channels of the verified
// plan and returns the receipts it recorded.
func deliverOwnerNotification(ctx context.Context, wd, planPath, channel string, message ownernotify.Message) ([]ownernotify.Receipt, error) {
	plan, planHash, err := loadVerifiedArchitectureV2Plan(wd, planPath)
	if err != nil {
		return nil, fmt.Errorf("notify: %w", err)
	}
	intent, err := ownernotify.PlanIntentFromPlan(plan)
	if err != nil {
		return nil, err
	}
	if intent == nil {
		return nil, ownernotify.ErrNotConfigured
	}
	canonical, err := loadCanonicalLocalSecretSpec()
	if err != nil {
		return nil, fmt.Errorf("notify: read canonical StackSpec: %w", err)
	}
	specChannels, err := ownernotify.SpecChannels(canonical)
	if err != nil {
		return nil, err
	}
	resolve := func(ref string) ([]byte, error) { return localevidence.ResolveLocalSecretMaterial(wd, ref) }
	return ownernotify.Sender{}.Deliver(ctx, wd, planHash, intent, specChannels, resolve, message, strings.TrimSpace(channel))
}

// loadVerifiedArchitectureV2Plan reads and verifies the canonical ResolvedPlan
// the same way status does.
func loadVerifiedArchitectureV2Plan(wd, planPath string) (resolvedplan.ResolvedPlan, string, error) {
	planPath = strings.TrimSpace(planPath)
	if planPath == "" {
		outputRoot, err := historicalGenerationOutputRoot(wd, specFile)
		if err != nil {
			return nil, "", fmt.Errorf("read StackSpec generation output root: %w", err)
		}
		if outputRoot == "" {
			return nil, "", errors.New("a canonical StackSpec v2 generation output root is required")
		}
		planPath = filepath.Join(outputRoot, ".stackkit", "resolved-plan.json")
	}
	planPath = resolvePathFromWorkDir(wd, planPath)
	raw, err := os.ReadFile(planPath)
	if err != nil {
		return nil, "", fmt.Errorf("read canonical ResolvedPlan %s: %w", planPath, err)
	}
	authority, err := newArchitectureV2ExecutionGate().newAuthority()
	if err != nil {
		return nil, "", fmt.Errorf("open Architecture v2 authority: %w", err)
	}
	if closer, ok := authority.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	verified, err := authority.VerifyCanonicalPlan(raw)
	if err != nil {
		return nil, "", fmt.Errorf("verify canonical ResolvedPlan: %w", err)
	}
	plan, err := resolvedplan.DecodeCanonicalPlan(verified.Canonical())
	if err != nil {
		return nil, "", fmt.Errorf("decode verified ResolvedPlan: %w", err)
	}
	return plan, verified.Binding().PlanHash, nil
}
