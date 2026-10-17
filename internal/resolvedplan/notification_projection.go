package resolvedplan

import (
	"sort"
	"strings"
)

// NotificationIntentAPIVersion identifies the secret-free owner notification
// projection carried by a ResolvedPlan.
const NotificationIntentAPIVersion = "stackkit.notification-intent/v1"

// notificationDeliveryBound lists the channel types with a local delivery
// adapter. A type without one stays "declared-unbound" until delivery
// evidence exists for it.
var notificationDeliveryBound = map[string]struct{}{"ntfy": {}, "email": {}, "gotify": {}, "webhook": {}, "slack": {}, "discord": {}, "telegram": {}, "pagerduty": {}}

// buildNotificationProjection lowers the owner's StackSpec notification intent
// into the plan. The projection keeps channel name, type, the event allowlist,
// the secret references and a hash of the declared destination; the
// destination itself (server, topic, recipients) stays in the StackSpec on the
// node, so the plan never carries an address an observer could use to read or
// spam the owner's channel.
func buildNotificationProjection(intent map[string]any) (map[string]any, error) {
	if intent == nil {
		return nil, nil
	}
	rawChannels, ok := intent["channels"].([]any)
	if !ok || len(rawChannels) == 0 {
		return nil, fail(ErrInvalidInput, "spec.notifications.channels", "at least one owner notification channel is required")
	}
	channels := make([]any, 0, len(rawChannels))
	seen := map[string]struct{}{}
	for index, raw := range rawChannels {
		channel, err := asObject(raw, "spec.notifications.channels")
		if err != nil {
			return nil, err
		}
		projected, err := ProjectNotificationChannel(channel)
		if err != nil {
			return nil, err
		}
		name, _ := projected["name"].(string)
		if _, duplicate := seen[name]; duplicate {
			return nil, fail(ErrInvalidInput, "spec.notifications.channels", "channel %d repeats name %q", index, name)
		}
		seen[name] = struct{}{}
		channels = append(channels, projected)
	}
	rawEvents, ok := intent["events"].([]any)
	if !ok || len(rawEvents) == 0 {
		return nil, fail(ErrInvalidInput, "spec.notifications.events", "the canonical notification intent must carry an event allowlist")
	}
	events := make([]string, 0, len(rawEvents))
	for _, raw := range rawEvents {
		event, ok := raw.(string)
		if !ok || strings.TrimSpace(event) == "" {
			return nil, fail(ErrInvalidInput, "spec.notifications.events", "event must be a non-empty string")
		}
		events = append(events, event)
	}
	eventList := make([]any, 0, len(events))
	for _, event := range sortStringsUnique(events) {
		eventList = append(eventList, event)
	}
	return map[string]any{
		"apiVersion": NotificationIntentAPIVersion,
		"channels":   channels,
		"events":     eventList,
	}, nil
}

// ProjectNotificationChannel returns the secret-free plan projection of one
// declared #NotificationChannelV1. The delivery adapter recomputes it from the
// StackSpec to prove that the channel it is about to use is the one the
// verified plan binds.
func ProjectNotificationChannel(channel map[string]any) (map[string]any, error) {
	channelType, _ := channel["type"].(string)
	if strings.TrimSpace(channelType) == "" {
		return nil, fail(ErrInvalidInput, "spec.notifications.channels", "channel type is required")
	}
	name, _ := channel["name"].(string)
	if strings.TrimSpace(name) == "" {
		name = channelType
	}
	destination, ok := channel[channelType].(map[string]any)
	if !ok {
		return nil, fail(ErrInvalidInput, "spec.notifications.channels."+name, "the %s block is required", channelType)
	}
	destinationHash, err := canonicalHash(map[string]any{"type": channelType, "destination": destination}, false)
	if err != nil {
		return nil, err
	}
	refs := []string{}
	collectNotificationSecretRefs(destination, &refs)
	refList := make([]any, 0, len(refs))
	for _, ref := range sortStringsUnique(refs) {
		refList = append(refList, ref)
	}
	delivery := "declared-unbound"
	if _, bound := notificationDeliveryBound[channelType]; bound {
		delivery = "bound"
	}
	return map[string]any{
		"name":            name,
		"type":            channelType,
		"destinationHash": destinationHash,
		"secretRefs":      refList,
		"delivery":        delivery,
	}, nil
}

func collectNotificationSecretRefs(value any, refs *[]string) {
	switch typed := value.(type) {
	case string:
		if secretRefPattern.MatchString(typed) {
			*refs = append(*refs, typed)
		}
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			collectNotificationSecretRefs(typed[key], refs)
		}
	case []any:
		for _, child := range typed {
			collectNotificationSecretRefs(child, refs)
		}
	}
}
