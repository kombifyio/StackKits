package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
)

type SentryConfig struct {
	DSN                string
	Environment        string
	Release            string
	RunID              string
	TenantDeploymentID string
	StackKit           string
	Phase              string
	FailureClass       string
	Provider           string
	RolloutMode        string
	Tags               map[string]string
}

type SentryRuntime struct {
	Enabled            bool
	DSN                string
	Environment        string
	Release            string
	ForbiddenAuthToken bool
	Tags               map[string]string
	Issues             []string
}

type SentryCaptureOptions struct {
	Transport    sentry.Transport
	FlushTimeout time.Duration
}

type SentryFailureEvent struct {
	EventID         string            `json:"eventId"`
	SentryEventID   string            `json:"sentryEventId,omitempty"`
	SentryDelivered bool              `json:"sentryDelivered"`
	CapturedAt      time.Time         `json:"capturedAt"`
	Status          string            `json:"status"`
	Message         string            `json:"message"`
	Environment     string            `json:"environment,omitempty"`
	Release         string            `json:"release,omitempty"`
	Tags            map[string]string `json:"tags,omitempty"`
	Issues          []string          `json:"issues,omitempty"`
	Redaction       string            `json:"redaction"`
	DeliveryMode    string            `json:"deliveryMode"`
}

func ResolveSentryRuntime(cfg SentryConfig, lookup EnvLookup) SentryRuntime {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	authTokenConfigured := lookupTrimmed(lookup, "SENTRY_AUTH_TOKEN") != "" || lookupTrimmed(lookup, "SENTRY_API_AUTH_TOKEN") != ""
	dsn := firstNonEmpty(lookupTrimmed(lookup, "SENTRY_DSN"), cfg.DSN)
	environment := firstNonEmpty(
		lookupTrimmed(lookup, "SENTRY_ENVIRONMENT"),
		lookupTrimmed(lookup, "STACKKIT_ENVIRONMENT"),
		cfg.Environment,
	)
	release := firstNonEmpty(lookupTrimmed(lookup, "SENTRY_RELEASE"), cfg.Release)
	tags := map[string]string{
		"source": "stackkit-cli",
	}
	addTag(tags, "rollout_mode", cfg.RolloutMode)
	addTag(tags, "tenant_deployment_id", cfg.TenantDeploymentID)
	addTag(tags, "run_id", cfg.RunID)
	addTag(tags, "stackkit", cfg.StackKit)
	addTag(tags, "phase", cfg.Phase)
	addTag(tags, "failure_class", cfg.FailureClass)
	addTag(tags, "provider", cfg.Provider)
	for key, value := range cfg.Tags {
		addTag(tags, key, value)
	}

	var issues []string
	if authTokenConfigured {
		issues = append(issues, "Sentry auth/API token must not be configured on target nodes")
	}
	return SentryRuntime{
		Enabled:            dsn != "" && !authTokenConfigured,
		DSN:                dsn,
		Environment:        environment,
		Release:            release,
		ForbiddenAuthToken: authTokenConfigured,
		Tags:               tags,
		Issues:             issues,
	}
}

func CaptureSentryFailure(root string, cfg SentryConfig, message string, lookup EnvLookup) (string, SentryRuntime, error) {
	return CaptureSentryFailureWithOptions(root, cfg, message, lookup, SentryCaptureOptions{})
}

func CaptureSentryFailureWithOptions(root string, cfg SentryConfig, message string, lookup EnvLookup, options SentryCaptureOptions) (string, SentryRuntime, error) {
	runtime := ResolveSentryRuntime(cfg, lookup)
	if !runtime.Enabled {
		return "", runtime, nil
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return "", runtime, fmt.Errorf("sentry failure evidence root is required")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", runtime, err
	}
	sentryEventID, sentryDelivered, issue := captureRemoteSentryFailure(runtime, message, options)
	if issue != "" {
		runtime.Issues = append(runtime.Issues, issue)
	}
	status := "captured-local"
	deliveryMode := "local-evidence"
	if sentryDelivered {
		status = "captured"
		deliveryMode = "sentry+local-evidence"
	}
	event := SentryFailureEvent{
		EventID:         newEventID(),
		SentryEventID:   sentryEventID,
		SentryDelivered: sentryDelivered,
		CapturedAt:      time.Now().UTC(),
		Status:          status,
		Message:         RedactTelemetryValue(message),
		Environment:     runtime.Environment,
		Release:         runtime.Release,
		Tags:            runtime.Tags,
		Issues:          runtime.Issues,
		Redaction:       "secret-like values redacted before persistence; DSN and auth tokens are not persisted",
		DeliveryMode:    deliveryMode,
	}
	path := filepath.Join(root, "sentry-event.json")
	data, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		return "", runtime, err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", runtime, err
	}
	return path, runtime, nil
}

func captureRemoteSentryFailure(runtime SentryRuntime, message string, options SentryCaptureOptions) (string, bool, string) {
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:              runtime.DSN,
		Environment:      runtime.Environment,
		Release:          runtime.Release,
		SampleRate:       1,
		SendDefaultPII:   false,
		AttachStacktrace: false,
		MaxBreadcrumbs:   8,
		Transport:        options.Transport,
		BeforeSend: func(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			return sanitizeSentryEvent(event)
		},
	})
	if err != nil {
		return "", false, fmt.Sprintf("sentry client init failed: %v", err)
	}
	defer client.Close()

	event := sentry.NewEvent()
	event.Level = sentry.LevelError
	event.Logger = "stackkit-cli"
	event.Platform = "go"
	event.Message = RedactTelemetryValue(message)
	event.Environment = runtime.Environment
	event.Release = runtime.Release
	event.Timestamp = time.Now().UTC()
	event.Tags = copySentryTags(runtime.Tags)
	event.Breadcrumbs = []*sentry.Breadcrumb{{
		Type:      "default",
		Category:  "stackkit.rollout",
		Message:   RedactTelemetryValue(message),
		Level:     sentry.LevelError,
		Timestamp: time.Now().UTC(),
		Data:      sentryBreadcrumbData(runtime.Tags),
	}}
	event.Contexts = map[string]sentry.Context{
		"stackkit": sentry.Context{
			"run_id":               runtime.Tags["run_id"],
			"tenant_deployment_id": runtime.Tags["tenant_deployment_id"],
			"stackkit":             runtime.Tags["stackkit"],
			"provider":             runtime.Tags["provider"],
			"phase":                runtime.Tags["phase"],
			"failure_class":        runtime.Tags["failure_class"],
			"rollout_mode":         runtime.Tags["rollout_mode"],
		},
	}
	id := client.CaptureEvent(event, nil, nil)
	if id == nil {
		return "", false, "sentry event dropped before delivery"
	}
	timeout := options.FlushTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if !client.FlushWithContext(ctx) {
		return string(*id), false, "sentry flush timed out"
	}
	return string(*id), true, ""
}

func sanitizeSentryEvent(event *sentry.Event) *sentry.Event {
	if event == nil {
		return nil
	}
	event.Message = RedactTelemetryValue(event.Message)
	event.User = sentry.User{}
	event.Request = nil
	for key, value := range event.Tags {
		event.Tags[key] = RedactTelemetryValue(value)
	}
	for _, breadcrumb := range event.Breadcrumbs {
		if breadcrumb == nil {
			continue
		}
		breadcrumb.Message = RedactTelemetryValue(breadcrumb.Message)
		for key, value := range breadcrumb.Data {
			if str, ok := value.(string); ok {
				breadcrumb.Data[key] = RedactTelemetryValue(str)
			}
		}
	}
	for name, ctx := range event.Contexts {
		event.Contexts[name] = redactSentryContext(ctx)
	}
	for i := range event.Exception {
		event.Exception[i].Value = RedactTelemetryValue(event.Exception[i].Value)
	}
	return event
}

func redactSentryContext(ctx sentry.Context) sentry.Context {
	out := sentry.Context{}
	for key, value := range ctx {
		switch typed := value.(type) {
		case string:
			out[key] = RedactTelemetryValue(typed)
		case map[string]interface{}:
			nested := sentry.Context{}
			for nestedKey, nestedValue := range typed {
				if str, ok := nestedValue.(string); ok {
					nested[nestedKey] = RedactTelemetryValue(str)
				} else {
					nested[nestedKey] = nestedValue
				}
			}
			out[key] = nested
		default:
			out[key] = value
		}
	}
	return out
}

func copySentryTags(tags map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range tags {
		addTag(out, key, value)
	}
	return out
}

func sentryBreadcrumbData(tags map[string]string) map[string]interface{} {
	data := map[string]interface{}{}
	for key, value := range tags {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		data[key] = RedactTelemetryValue(value)
	}
	return data
}

func RedactTelemetryValue(input string) string {
	return telemetrySecretPair.ReplaceAllString(input, "$1=<redacted>")
}

func addTag(tags map[string]string, key, value string) {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return
	}
	tags[key] = RedactTelemetryValue(value)
}

var telemetrySecretPair = regexp.MustCompile(`(?i)(token|password|secret|api[_-]?key|dsn)=([^\s,;]+)`)

func newEventID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}
