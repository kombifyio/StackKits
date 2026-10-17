package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/ownernotify"
	"github.com/spf13/cobra"
)

const lifecycleEmitTimeout = 20 * time.Second

var (
	notifyEventSubject string
	notifyEventFacts   []string
	notifyEventRemedy  string
)

var notifyEventCmd = &cobra.Command{
	Use:   "event <apply-failed|upgrade-available|drift-detected|certificate-expiring>",
	Short: "Send one lifecycle event to the owner notification channels",
	Args:  cobra.ExactArgs(1),
	Long: `Send one lifecycle event to the owner notification channels. Use it from a
timer or an automation for events StackKit does not observe by itself
(upgrade-available, certificate-expiring); apply-failed and drift-detected are
sent automatically by 'stackkit apply' and 'stackkit drift detect'.

The event is sent only when it is in spec.notifications.events. Facts are short
states or counts; never put a path, repository location or credential in them.`,
	Example: `  stackkit notify event certificate-expiring --subject app.example.test --fact "expires in 9 days"
  stackkit notify event upgrade-available --fact "0.27.0 is available" --remedy "stackkit upgrade"`,
	RunE: runNotifyEvent,
}

func init() {
	notifyEventCmd.Flags().StringVar(&notifyEventSubject, "subject", "", "What the event is about (service, version or certificate name)")
	notifyEventCmd.Flags().StringArrayVar(&notifyEventFacts, "fact", nil, "A short state or count line (repeatable)")
	notifyEventCmd.Flags().StringVar(&notifyEventRemedy, "remedy", "", "The command the owner can run next")
	notifyEventCmd.Flags().BoolVar(&notifyJSON, "json", false, "Output the receipts as JSON")
	notifyEventCmd.Flags().StringVar(&notifyChannel, "channel", "", "Deliver only to this declared channel name")
	notifyEventCmd.Flags().StringVar(&notifyResolvedPlan, "resolved-plan", "", "Canonical Architecture v2 ResolvedPlan (default: beneath the StackSpec generation.outputRoot)")
	notifyCmd.AddCommand(notifyEventCmd)
}

func runNotifyEvent(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	message, ok := ownernotify.ComposeLifecycleEvent(ownernotify.LifecycleInput{
		Event: strings.TrimSpace(args[0]), Subject: notifyEventSubject, Facts: notifyEventFacts, Remedy: notifyEventRemedy,
	})
	if !ok {
		return fmt.Errorf("notify: %q is not a lifecycle event (%s)", args[0], strings.Join(ownernotify.LifecycleEvents, ", "))
	}
	wd := getWorkDir()
	plan, _, err := loadVerifiedArchitectureV2Plan(wd, notifyResolvedPlan)
	if err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	intent, err := ownernotify.PlanIntentFromPlan(plan)
	if err != nil {
		return err
	}
	if intent == nil {
		return ownernotify.ErrNotConfigured
	}
	admitted := containsString(intent.Events, message.Event)
	var receipts []ownernotify.Receipt
	var deliverErr error
	if admitted {
		receipts, deliverErr = deliverOwnerNotification(ctx, wd, notifyResolvedPlan, notifyChannel, message)
	}
	if notifyJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(map[string]any{
			"apiVersion": "stackkit.notify-event/v1", "event": message.Event, "admitted": admitted, "receipts": receipts,
		}); err != nil {
			return err
		}
		return deliverErr
	}
	out := cmd.OutOrStdout()
	if !admitted {
		_, _ = fmt.Fprintf(out, "Not sent: %s is not in spec.notifications.events.\n", message.Event)
	}
	for _, receipt := range receipts {
		line := fmt.Sprintf("%s (%s): %s", receipt.Channel, receipt.Type, receipt.Result)
		if receipt.Reason != "" {
			line += " - " + receipt.Reason
		}
		_, _ = fmt.Fprintln(out, line)
	}
	return deliverErr
}

// emitOwnerLifecycleEvent sends a lifecycle event the command itself observed.
// It is best effort and silent when the owner declared no channel, did not
// admit the event or no verified plan exists yet: a notification problem never
// changes the result of the command that observed the event.
func emitOwnerLifecycleEvent(ctx context.Context, wd string, input ownernotify.LifecycleInput) {
	message, ok := ownernotify.ComposeLifecycleEvent(input)
	if !ok {
		return
	}
	plan, _, err := loadVerifiedArchitectureV2Plan(wd, "")
	if err != nil {
		return
	}
	intent, err := ownernotify.PlanIntentFromPlan(plan)
	if err != nil || intent == nil || !containsString(intent.Events, message.Event) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lifecycleEmitTimeout)
	defer cancel()
	if _, err := deliverOwnerNotification(ctx, wd, "", "", message); err != nil && !humanOutputSuppressed() {
		_, _ = fmt.Fprintf(os.Stderr, "  Owner notification %s was not delivered; see `stackkit notify test`.\n", message.Event)
	}
}
