package ownernotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// httpChannelTypes are the channel types delivered by one HTTP request to an
// owner-declared endpoint. Slack, Discord and Telegram carry their credential
// inside the endpoint (webhook URL or bot token), so that material is a
// custody secret reference resolved at send time.
var httpChannelTypes = map[string]struct{}{"gotify": {}, "webhook": {}, "slack": {}, "discord": {}, "telegram": {}, "pagerduty": {}}

// ChannelDeliveryBound reports whether a channel type has a local delivery
// adapter in this package.
func ChannelDeliveryBound(channelType string) bool {
	switch channelType {
	case "ntfy", "email":
		return true
	}
	_, ok := httpChannelTypes[channelType]
	return ok
}

func (s Sender) sendHTTPChannel(ctx context.Context, channelType string, destination map[string]any, resolve SecretResolver, message Message) error {
	var (
		method  = http.MethodPost
		target  string
		body    []byte
		headers = map[string]string{}
		err     error
	)
	switch channelType {
	case "gotify":
		target, body, headers, err = gotifyRequest(destination, resolve, message)
	case "webhook":
		method, target, body, headers, err = webhookRequest(destination, resolve, message)
	case "slack":
		target, body, err = chatWebhookRequest(destination, resolve, "slack", map[string]any{
			"text": message.Title + "\n" + message.Body,
		}, func(payload map[string]any) {
			if channel := stringValue(destination["channel"]); channel != "" {
				payload["channel"] = channel
			}
			if user := stringValue(destination["username"]); user != "" {
				payload["username"] = user
			}
		})
	case "discord":
		target, body, err = chatWebhookRequest(destination, resolve, "discord", map[string]any{
			"content": truncateRunes(message.Title+"\n"+message.Body, 2000),
		}, nil)
	case "telegram":
		target, body, err = telegramRequest(destination, resolve, message)
	case "pagerduty":
		target, body, err = pagerdutyRequest(destination, resolve, message)
	default:
		return fmt.Errorf("%s delivery has no local adapter", channelType)
	}
	if err != nil {
		return err
	}
	if body != nil && headers["Content-Type"] == "" {
		headers["Content-Type"] = "application/json"
	}
	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s request could not be built", channelType)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		// A transport error echoes the request URL, which can carry the
		// webhook or bot credential; report the class only.
		return fmt.Errorf("%s endpoint unreachable", channelType)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("%s endpoint answered HTTP %d", channelType, response.StatusCode)
	}
	return nil
}

func gotifyRequest(destination map[string]any, resolve SecretResolver, message Message) (string, []byte, map[string]string, error) {
	base, err := httpURL(stringValue(destination["url"]), "gotify url")
	if err != nil {
		return "", nil, nil, err
	}
	token, err := resolveSecret(resolve, stringValue(destination["token"]))
	if err != nil {
		return "", nil, nil, err
	}
	priority := message.Priority
	if priority == 0 {
		priority = intValue(destination["priority"], 5)
	}
	body, _ := json.Marshal(map[string]any{"title": message.Title, "message": message.Body, "priority": priority})
	// The application token travels in a header, never in the URL.
	return strings.TrimRight(base, "/") + "/message", body, map[string]string{"X-Gotify-Key": string(token)}, nil
}

func webhookRequest(destination map[string]any, resolve SecretResolver, message Message) (string, string, []byte, map[string]string, error) {
	target, err := httpURL(stringValue(destination["url"]), "webhook url")
	if err != nil {
		return "", "", nil, nil, err
	}
	method := stringValue(destination["method"])
	if method == "" {
		method = http.MethodPost
	}
	if method != http.MethodPost && method != http.MethodPut {
		return "", "", nil, nil, errors.New("webhook method must be POST or PUT")
	}
	headers := map[string]string{}
	if declared, ok := destination["headers"].(map[string]any); ok {
		names := make([]string, 0, len(declared))
		for name := range declared {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if strings.ContainsAny(name, "\r\n:") || strings.ContainsAny(stringValue(declared[name]), "\r\n") {
				return "", "", nil, nil, errors.New("webhook header is not a valid HTTP header")
			}
			headers[name] = stringValue(declared[name])
		}
	}
	if ref := stringValue(destination["authorization"]); ref != "" {
		material, err := resolveSecret(resolve, ref)
		if err != nil {
			return "", "", nil, nil, err
		}
		headers["Authorization"] = string(material)
	}
	body, _ := json.Marshal(map[string]any{
		"event": message.Event, "title": message.Title, "message": message.Body, "priority": message.Priority, "tags": message.Tags,
	})
	return method, target, body, headers, nil
}

func chatWebhookRequest(destination map[string]any, resolve SecretResolver, kind string, payload map[string]any, extend func(map[string]any)) (string, []byte, error) {
	material, err := resolveSecret(resolve, stringValue(destination["webhookUrl"]))
	if err != nil {
		return "", nil, err
	}
	target, err := httpURL(string(material), kind+" webhook url")
	if err != nil {
		return "", nil, err
	}
	if extend != nil {
		extend(payload)
	}
	body, _ := json.Marshal(payload)
	return target, body, nil
}

func telegramRequest(destination map[string]any, resolve SecretResolver, message Message) (string, []byte, error) {
	token, err := resolveSecret(resolve, stringValue(destination["botToken"]))
	if err != nil {
		return "", nil, err
	}
	chatID := stringValue(destination["chatId"])
	if chatID == "" {
		return "", nil, errors.New("telegram chatId is required")
	}
	if strings.ContainsAny(string(token), "/?# ") {
		return "", nil, errors.New("telegram bot token is malformed")
	}
	body, _ := json.Marshal(map[string]any{"chat_id": chatID, "text": truncateRunes(message.Title+"\n"+message.Body, 4096)})
	return "https://api.telegram.org/bot" + string(token) + "/sendMessage", body, nil
}

// pagerDutyEventsURL is the PagerDuty Events API v2 endpoint; the routing key
// is the only credential and travels in the body, as the API requires.
const pagerDutyEventsURL = "https://events.pagerduty.com/v2/enqueue"

func pagerdutyRequest(destination map[string]any, resolve SecretResolver, message Message) (string, []byte, error) {
	key, err := resolveSecret(resolve, stringValue(destination["routingKey"]))
	if err != nil {
		return "", nil, err
	}
	body, _ := json.Marshal(map[string]any{
		"routing_key":  string(key),
		"event_action": "trigger",
		"payload": map[string]any{
			"summary":  truncateRunes(message.Title+": "+message.Body, 1024),
			"source":   "stackkit",
			"severity": "error",
		},
	})
	return pagerDutyEventsURL, body, nil
}

// httpURL admits an absolute http(s) URL without embedded credentials.
func httpURL(raw, label string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
		return "", errors.New(label + " must be an http(s) URL without embedded credentials")
	}
	return parsed.String(), nil
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}
