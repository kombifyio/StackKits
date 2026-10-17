package ownernotify

import (
	"fmt"
	"strings"
)

const (
	// EventApplyFailed reports an Apply that stopped before the runtime converged.
	EventApplyFailed = "apply-failed"
	// EventUpgradeAvailable reports a newer StackKit release the owner can adopt.
	EventUpgradeAvailable = "upgrade-available"
	// EventDriftDetected reports a runtime that no longer matches the applied plan.
	EventDriftDetected = "drift-detected"
	// EventCertificateExpiring reports a certificate that will expire soon.
	EventCertificateExpiring = "certificate-expiring"
)

const maxLifecycleFacts = 5

// LifecycleInput carries the facts of one lifecycle event. Facts are short
// states, counts, versions or subject names the caller owns; they are cleaned
// to one line and never carry a path, location or credential.
type LifecycleInput struct {
	Event string
	// Subject names what the event is about (a service, version or certificate).
	Subject string
	// Facts are the short evidence lines of the message.
	Facts []string
	// Remedy is the exact command the owner can run next.
	Remedy string
}

// LifecycleEvents are the events ComposeLifecycleEvent can write.
var LifecycleEvents = []string{EventApplyFailed, EventUpgradeAvailable, EventDriftDetected, EventCertificateExpiring}

// IsLifecycleEvent reports whether event is one ComposeLifecycleEvent writes.
func IsLifecycleEvent(event string) bool { return contains(LifecycleEvents, event) }

// ComposeLifecycleEvent writes the owner message of a lifecycle event. It
// reports ok=false for an event it does not know.
func ComposeLifecycleEvent(input LifecycleInput) (Message, bool) {
	title, tags, priority := "", []string(nil), 3
	switch input.Event {
	case EventApplyFailed:
		title, tags, priority = "StackKit apply failed", []string{"rotating_light"}, 4
	case EventUpgradeAvailable:
		title, tags = "StackKit update available", []string{"arrow_up"}
	case EventDriftDetected:
		title, tags, priority = "StackKit runtime drifted from the applied plan", []string{"warning"}, 4
	case EventCertificateExpiring:
		title, tags, priority = "A certificate is about to expire", []string{"lock"}, 4
	default:
		return Message{}, false
	}
	if subject := oneLine(input.Subject); subject != "" {
		title += ": " + subject
	}
	var body strings.Builder
	facts := input.Facts
	if len(facts) > maxLifecycleFacts {
		facts = facts[:maxLifecycleFacts]
	}
	for _, fact := range facts {
		if line := oneLine(fact); line != "" {
			fmt.Fprintf(&body, "- %s\n", line)
		}
	}
	if remedy := oneLine(input.Remedy); remedy != "" {
		fmt.Fprintf(&body, "Next: %s\n", remedy)
	}
	text := strings.TrimSpace(body.String())
	if text == "" {
		text = "See `stackkit status` for details."
	}
	return Message{Event: input.Event, Title: title, Body: text, Priority: priority, Tags: tags}, true
}

func oneLine(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 160 {
		value = value[:160]
	}
	return value
}
