//go:build !kombify_full_local

package telemetry

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/kombifyio/stackkits/internal/runtimeprofile"
)

const retainLocalFailureEvidence = false

type SentryCaptureOptions struct {
	Transport    sentry.Transport
	FlushTimeout time.Duration
}

func ResolveSentryRuntime(cfg SentryConfig, lookup EnvLookup) SentryRuntime {
	if runtimeprofile.FullLocal() {
		return SentryRuntime{}
	}
	if lookup == nil {
		lookup = os.LookupEnv
	}
	authTokenConfigured := lookupTrimmed(lookup, "SENTRY_AUTH_TOKEN") != "" || lookupTrimmed(lookup, "SENTRY_API_AUTH_TOKEN") != ""
	dsn := firstNonEmpty(lookupTrimmed(lookup, "SENTRY_DSN"), cfg.DSN)
	metadata := resolveSentryMetadata(cfg, lookup)

	var issues []string
	if authTokenConfigured {
		issues = append(issues, "Sentry auth/API token must not be configured on target nodes")
	}
	return SentryRuntime{
		Enabled:            dsn != "" && !authTokenConfigured,
		DSN:                dsn,
		Environment:        metadata.Environment,
		Release:            metadata.Release,
		ForbiddenAuthToken: authTokenConfigured,
		Tags:               metadata.Tags,
		Issues:             issues,
	}
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
		timeout = 5 * time.Second
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

func copySentryTags(tags map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range tags {
		addTag(out, key, value)
	}
	return out
}
