// Package ownernotify delivers owner notifications from the server a StackKit
// is installed on to the channels the owner declared in the StackSpec
// notification intent.
//
// The ResolvedPlan carries only a secret-free projection of each channel
// (name, type, destination hash, secret references). Delivery reads the
// destination from the canonical StackSpec on the same node, recomputes the
// projection and refuses a channel whose destination no longer matches the
// verified plan. Credentials resolve through local custody at send time and
// never enter a receipt, a log line or an error message.
package ownernotify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	// StatusAPIVersion identifies the `notifications` projection of status.
	StatusAPIVersion = "stackkit.owner-notification-status/v1"
	// ReceiptAPIVersion identifies one delivery receipt under .stackkit/runs.
	ReceiptAPIVersion = "stackkit.owner-notification-receipt/v1"

	StateNotConfigured = "not-configured"
	StateConfigured    = "configured"

	// EventVerify is the delivery check a Verify or an explicit test sends; it
	// is always admitted because it proves the channel rather than reporting a
	// lifecycle event.
	EventVerify = "delivery-check"

	receiptDirectory = "owner-notifications"
	maxResponseBytes = 4096
)

// ErrNotConfigured reports that the verified plan declares no owner channel.
var ErrNotConfigured = errors.New("owner notifications: not configured; declare spec.notifications with at least one owner channel (ntfy, email, gotify, webhook, slack, discord or telegram)")

// PlanChannel is the secret-free plan projection of one channel.
type PlanChannel struct {
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	DestinationHash string   `json:"destinationHash"`
	SecretRefs      []string `json:"secretRefs"`
	Delivery        string   `json:"delivery"`
}

// PlanIntent is the plan projection of the owner's notification intent.
type PlanIntent struct {
	APIVersion string        `json:"apiVersion"`
	Channels   []PlanChannel `json:"channels"`
	Events     []string      `json:"events"`
}

// ChannelStatus is the status view of one channel; it names no destination.
type ChannelStatus struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Delivery string `json:"delivery"`
}

// Status is the additive `notifications` projection of `stackkit status`.
type Status struct {
	APIVersion string          `json:"apiVersion"`
	State      string          `json:"state"`
	Channels   []ChannelStatus `json:"channels,omitempty"`
	Events     []string        `json:"events,omitempty"`
	// LastDelivery is the newest local delivery receipt, when one exists.
	LastDelivery *Receipt `json:"lastDelivery,omitempty"`
	Attention    []string `json:"attention,omitempty"`
}

// Message is one owner notification. Body text must not carry secrets; callers
// build it from evidence summaries only.
type Message struct {
	Event    string
	Title    string
	Body     string
	Priority int
	Tags     []string
}

// Receipt records one delivery attempt. It never carries the destination,
// the credential or the message body.
type Receipt struct {
	APIVersion      string `json:"apiVersion"`
	Event           string `json:"event"`
	Channel         string `json:"channel"`
	Type            string `json:"type"`
	DestinationHash string `json:"destinationHash"`
	PlanHash        string `json:"planHash"`
	Result          string `json:"result"`
	Reason          string `json:"reason,omitempty"`
	AttemptedAt     string `json:"attemptedAt"`
}

// SecretResolver returns the material behind one secret reference from local
// custody.
type SecretResolver func(ref string) ([]byte, error)

// Sender delivers notifications. The zero value uses net/http and net/smtp.
type Sender struct {
	HTTPClient *http.Client
	// SendMail overrides SMTP delivery in tests.
	SendMail func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error
	Now      func() time.Time
}

// PlanIntentFromPlan decodes the notification projection of a verified plan.
// It returns nil when the plan declares no channel.
func PlanIntentFromPlan(plan resolvedplan.ResolvedPlan) (*PlanIntent, error) {
	raw, ok := plan["notifications"]
	if !ok || raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("owner notifications: encode plan projection: %w", err)
	}
	var intent PlanIntent
	if err := json.Unmarshal(encoded, &intent); err != nil {
		return nil, fmt.Errorf("owner notifications: decode plan projection: %w", err)
	}
	if intent.APIVersion != resolvedplan.NotificationIntentAPIVersion || len(intent.Channels) == 0 {
		return nil, fmt.Errorf("owner notifications: unsupported plan projection %q", intent.APIVersion)
	}
	return &intent, nil
}

// BuildStatus renders the status projection. It never fails: an unreadable
// receipt directory only omits the last delivery.
func BuildStatus(intent *PlanIntent, workspace string) *Status {
	if intent == nil {
		return &Status{
			APIVersion: StatusAPIVersion, State: StateNotConfigured,
			Attention: []string{"Declare spec.notifications with an ntfy topic or an email channel so a failed backup or an available kit update reaches you."},
		}
	}
	status := &Status{APIVersion: StatusAPIVersion, State: StateConfigured, Events: append([]string(nil), intent.Events...)}
	for _, channel := range intent.Channels {
		status.Channels = append(status.Channels, ChannelStatus{Name: channel.Name, Type: channel.Type, Delivery: channel.Delivery})
		if channel.Delivery != "bound" {
			status.Attention = append(status.Attention, fmt.Sprintf("Channel %q (%s) is declared but has no local delivery adapter yet; ntfy, email, gotify, webhook, slack, discord and telegram deliver today.", channel.Name, channel.Type))
		}
	}
	status.LastDelivery = LatestReceipt(workspace)
	if status.LastDelivery == nil {
		status.Attention = append(status.Attention, "No delivery check recorded; run `stackkit notify test` to prove the channel.")
	}
	return status
}

// SpecChannels returns the declared channels of a canonical StackSpec keyed by
// name, each paired with its recomputed plan projection.
func SpecChannels(canonicalStackSpec []byte) (map[string]SpecChannel, error) {
	var spec struct {
		Notifications *struct {
			Channels []map[string]any `json:"channels"`
		} `json:"notifications"`
	}
	decoder := json.NewDecoder(bytes.NewReader(canonicalStackSpec))
	decoder.UseNumber()
	if err := decoder.Decode(&spec); err != nil {
		return nil, fmt.Errorf("owner notifications: decode canonical StackSpec: %w", err)
	}
	result := map[string]SpecChannel{}
	if spec.Notifications == nil {
		return result, nil
	}
	for _, channel := range spec.Notifications.Channels {
		projected, err := resolvedplan.ProjectNotificationChannel(channel)
		if err != nil {
			return nil, err
		}
		name, _ := projected["name"].(string)
		hash, _ := projected["destinationHash"].(string)
		channelType, _ := projected["type"].(string)
		destination, _ := channel[channelType].(map[string]any)
		result[name] = SpecChannel{Name: name, Type: channelType, DestinationHash: hash, Destination: destination}
	}
	return result, nil
}

// SpecChannel is one declared channel with its destination as authored.
type SpecChannel struct {
	Name            string
	Type            string
	DestinationHash string
	Destination     map[string]any
}

// Deliver sends message to every plan channel that admits its event and
// writes one receipt per attempt. channelFilter, when set, limits delivery to
// that channel name. It returns the receipts and an error when any admitted
// channel failed.
func (s Sender) Deliver(
	ctx context.Context,
	workspace, planHash string,
	intent *PlanIntent,
	specChannels map[string]SpecChannel,
	resolve SecretResolver,
	message Message,
	channelFilter string,
) ([]Receipt, error) {
	if intent == nil {
		return nil, ErrNotConfigured
	}
	if message.Event != EventVerify && !contains(intent.Events, message.Event) {
		return nil, nil
	}
	var receipts []Receipt
	var failures []error
	matched := false
	for _, channel := range intent.Channels {
		if channelFilter != "" && channel.Name != channelFilter {
			continue
		}
		matched = true
		receipt := Receipt{
			APIVersion: ReceiptAPIVersion, Event: message.Event, Channel: channel.Name, Type: channel.Type,
			DestinationHash: channel.DestinationHash, PlanHash: planHash, AttemptedAt: s.now().UTC().Format(time.RFC3339Nano),
		}
		err := s.deliverOne(ctx, channel, specChannels, resolve, message)
		if err != nil {
			receipt.Result, receipt.Reason = "failed", err.Error()
			failures = append(failures, fmt.Errorf("channel %q: %w", channel.Name, err))
		} else {
			receipt.Result = "delivered"
		}
		if writeErr := WriteReceipt(workspace, receipt); writeErr != nil {
			failures = append(failures, fmt.Errorf("channel %q: record receipt: %w", channel.Name, writeErr))
		}
		receipts = append(receipts, receipt)
	}
	if channelFilter != "" && !matched {
		return nil, fmt.Errorf("owner notifications: channel %q is not declared in the verified plan", channelFilter)
	}
	return receipts, errors.Join(failures...)
}

func (s Sender) deliverOne(ctx context.Context, channel PlanChannel, specChannels map[string]SpecChannel, resolve SecretResolver, message Message) error {
	if channel.Delivery != "bound" {
		return fmt.Errorf("%s delivery is declared but not bound on this node", channel.Type)
	}
	declared, ok := specChannels[channel.Name]
	if !ok {
		return errors.New("the StackSpec no longer declares this channel; re-run generate and apply")
	}
	if declared.Type != channel.Type || declared.DestinationHash != channel.DestinationHash {
		return errors.New("the StackSpec destination differs from the verified plan; re-run generate and apply")
	}
	switch channel.Type {
	case "ntfy":
		return s.sendNtfy(ctx, declared.Destination, resolve, message)
	case "email":
		return s.sendEmail(declared.Destination, resolve, message)
	default:
		if _, ok := httpChannelTypes[channel.Type]; ok {
			return s.sendHTTPChannel(ctx, channel.Type, declared.Destination, resolve, message)
		}
		return fmt.Errorf("%s delivery has no local adapter", channel.Type)
	}
}

func (s Sender) sendNtfy(ctx context.Context, destination map[string]any, resolve SecretResolver, message Message) error {
	server := strings.TrimRight(stringValue(destination["serverUrl"]), "/")
	topic := stringValue(destination["topic"])
	parsed, err := url.Parse(server)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
		return errors.New("ntfy serverUrl must be an http(s) URL without embedded credentials")
	}
	if topic == "" || strings.ContainsAny(topic, "/?#") {
		return errors.New("ntfy topic must be a single path segment")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/"+url.PathEscape(topic), strings.NewReader(message.Body))
	if err != nil {
		return errors.New("ntfy request could not be built")
	}
	request.Header.Set("Title", message.Title)
	priority := message.Priority
	if priority == 0 {
		priority = intValue(destination["priority"], 3)
	}
	request.Header.Set("Priority", strconv.Itoa(priority))
	if len(message.Tags) > 0 {
		request.Header.Set("Tags", strings.Join(message.Tags, ","))
	}
	if ref := stringValue(destination["token"]); ref != "" {
		token, err := resolveSecret(resolve, ref)
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bearer "+string(token))
	}
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		// The transport error can echo the request URL, which names the
		// topic; report the class only.
		return errors.New("ntfy server unreachable")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("ntfy server answered HTTP %d", response.StatusCode)
	}
	return nil
}

func (s Sender) sendEmail(destination map[string]any, resolve SecretResolver, message Message) error {
	smarthost := stringValue(destination["smarthost"])
	host, _, err := net.SplitHostPort(smarthost)
	if err != nil || host == "" {
		return errors.New("email smarthost must be host:port")
	}
	var recipients []string
	if list, ok := destination["to"].([]any); ok {
		for _, entry := range list {
			if value := stringValue(entry); value != "" {
				recipients = append(recipients, value)
			}
		}
	}
	if len(recipients) == 0 {
		return errors.New("email channel declares no recipient")
	}
	from := stringValue(destination["from"])
	if from == "" {
		from = "stackkit@" + host
	}
	var auth smtp.Auth
	if ref := stringValue(destination["password"]); ref != "" {
		password, err := resolveSecret(resolve, ref)
		if err != nil {
			return err
		}
		auth = smtp.PlainAuth("", from, string(password), host)
	}
	var body bytes.Buffer
	fmt.Fprintf(&body, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
		headerSafe(from), headerSafe(strings.Join(recipients, ", ")), headerSafe(message.Title), s.now().UTC().Format(time.RFC1123Z), message.Body)
	send := s.SendMail
	if send == nil {
		send = sendMailRequireTLS
	}
	if err := send(smarthost, auth, from, recipients, body.Bytes()); err != nil {
		return errors.New("email smarthost refused or was unreachable")
	}
	return nil
}

// sendMailRequireTLS is smtp.SendMail with STARTTLS required, so a smarthost
// password never crosses the network in clear text.
func sendMailRequireTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	host, _, _ := net.SplitHostPort(addr)
	client, err := smtp.Dial(addr)
	if err != nil {
		return err
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return errors.New("smarthost does not offer STARTTLS")
	}
	if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
		return err
	}
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(msg); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func resolveSecret(resolve SecretResolver, ref string) ([]byte, error) {
	if resolve == nil {
		return nil, errors.New("no local secret custody resolver")
	}
	material, err := resolve(ref)
	if err != nil || len(bytes.TrimSpace(material)) == 0 {
		// Never echo the reference or the custody error, which may name it.
		return nil, errors.New("channel credential is not in local custody; deliver it through Wallet host delivery or `stackkit secrets` before sending")
	}
	return bytes.TrimSpace(material), nil
}

// WriteReceipt stores one owner-only receipt under .stackkit/runs.
func WriteReceipt(workspace string, receipt Receipt) error {
	dir := filepath.Join(workspace, ".stackkit", "runs", receiptDirectory)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	nonce := make([]byte, 6)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	attempted, err := time.Parse(time.RFC3339Nano, receipt.AttemptedAt)
	if err != nil {
		return err
	}
	name := attempted.UTC().Format("20060102-150405.000000000") + "-" + hex.EncodeToString(nonce) + ".json"
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), append(encoded, '\n'), 0o600)
}

// LatestReceipt returns the newest readable receipt, or nil.
func LatestReceipt(workspace string) *Receipt {
	dir := filepath.Join(workspace, ".stackkit", "runs", receiptDirectory)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var receipt Receipt
		if json.Unmarshal(data, &receipt) == nil && receipt.APIVersion == ReceiptAPIVersion {
			return &receipt
		}
	}
	return nil
}

func (s Sender) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func intValue(value any, fallback int) int {
	switch typed := value.(type) {
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed)
		}
	case float64:
		return int(typed)
	case int:
		return typed
	}
	return fallback
}

func headerSafe(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}
