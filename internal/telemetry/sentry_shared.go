package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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

func resolveSentryMetadata(cfg SentryConfig, lookup EnvLookup) SentryRuntime {
	if lookup == nil {
		lookup = os.LookupEnv
	}
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

	return SentryRuntime{Environment: environment, Release: release, Tags: tags}
}

func CaptureSentryFailure(root string, cfg SentryConfig, message string, lookup EnvLookup) (string, SentryRuntime, error) {
	return CaptureSentryFailureWithOptions(root, cfg, message, lookup, SentryCaptureOptions{})
}

func CaptureSentryFailureWithOptions(root string, cfg SentryConfig, message string, lookup EnvLookup, options SentryCaptureOptions) (string, SentryRuntime, error) {
	runtime := ResolveSentryRuntime(cfg, lookup)
	// Full Local keeps scrubbed failure receipts even though delivery is disabled.
	if !runtime.Enabled && !retainLocalFailureEvidence {
		return "", runtime, nil
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return "", runtime, fmt.Errorf("sentry failure evidence root is required")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", runtime, err
	}
	var sentryEventID string
	var sentryDelivered bool
	if runtime.Enabled {
		var issue string
		sentryEventID, sentryDelivered, issue = captureRemoteSentryFailure(runtime, message, options)
		if issue != "" {
			runtime.Issues = append(runtime.Issues, issue)
		}
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
