// Package telemetry contains opt-in runtime telemetry setup helpers.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type OTelConfig struct {
	ServiceName        string
	ServiceVersion     string
	Environment        string
	RunID              string
	TenantDeploymentID string
	StackKit           string
	Provider           string
	NodeID             string
}

type OTelRuntime struct {
	Enabled            bool
	Endpoint           string
	Protocol           string
	Headers            map[string]string
	ServiceName        string
	ServiceVersion     string
	Environment        string
	RunID              string
	TenantDeploymentID string
	StackKit           string
	Provider           string
	NodeID             string
	ResourceAttributes map[string]string
}

type EnvLookup func(string) (string, bool)

type SpanHandle struct {
	span oteltrace.Span
}

func SetupOTel(ctx context.Context, cfg OTelConfig) (OTelRuntime, func(context.Context) error, error) {
	return SetupOTelWithLookup(ctx, cfg, os.LookupEnv)
}

func SetupOTelWithLookup(ctx context.Context, cfg OTelConfig, lookup EnvLookup) (OTelRuntime, func(context.Context) error, error) {
	runtime := ResolveOTelRuntime(cfg, lookup)
	if !runtime.Enabled {
		return runtime, func(context.Context) error { return nil }, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	exporter, err := newTraceExporter(ctx, runtime)
	if err != nil {
		return runtime, func(context.Context) error { return nil }, err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(
			exporter,
			sdktrace.WithBatchTimeout(time.Second),
			sdktrace.WithExportTimeout(5*time.Second),
		),
		sdktrace.WithResource(resource.NewWithAttributes("", attrsToKeyValues(runtime.ResourceAttributes)...)),
	)
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return runtime, func(ctx context.Context) error {
		if ctx == nil {
			ctx = context.Background()
		}
		defer otel.SetTracerProvider(previousProvider)
		defer otel.SetTextMapPropagator(previousPropagator)
		return provider.Shutdown(ctx)
	}, nil
}

func ResolveOTelRuntime(cfg OTelConfig, lookup EnvLookup) OTelRuntime {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	endpoint := lookupTrimmed(lookup, "OTEL_EXPORTER_OTLP_ENDPOINT")
	serviceName := firstNonEmpty(
		lookupTrimmed(lookup, "OTEL_SERVICE_NAME"),
		strings.TrimSpace(cfg.ServiceName),
		"stackkit-cli",
	)
	serviceVersion := firstNonEmpty(
		lookupTrimmed(lookup, "OTEL_SERVICE_VERSION"),
		strings.TrimSpace(cfg.ServiceVersion),
	)
	environment := firstNonEmpty(
		lookupTrimmed(lookup, "STACKKIT_ENVIRONMENT"),
		lookupTrimmed(lookup, "SENTRY_ENVIRONMENT"),
		strings.TrimSpace(cfg.Environment),
	)
	provider := firstNonEmpty(
		lookupTrimmed(lookup, "STACKKIT_PROVIDER"),
		lookupTrimmed(lookup, "STACKKIT_E2E_CLOUD_NODE_ENGINE"),
		strings.TrimSpace(cfg.Provider),
	)
	nodeID := firstNonEmpty(
		lookupTrimmed(lookup, "STACKKIT_NODE_ID"),
		lookupTrimmed(lookup, "STACKKIT_TARGET_NODE_ID"),
		strings.TrimSpace(cfg.NodeID),
	)
	protocol := firstNonEmpty(lookupTrimmed(lookup, "OTEL_EXPORTER_OTLP_PROTOCOL"), "grpc")
	headers := parseOTLPHeaders(lookupTrimmed(lookup, "OTEL_EXPORTER_OTLP_HEADERS"))
	attrs := map[string]string{
		"service.name": serviceName,
	}
	addAttr(attrs, "service.version", serviceVersion)
	addAttr(attrs, "deployment.environment.name", environment)
	addAttr(attrs, "stackkit.run_id", cfg.RunID)
	addAttr(attrs, "stackkit.tenant_deployment_id", cfg.TenantDeploymentID)
	addAttr(attrs, "stackkit.stackkit", cfg.StackKit)
	addAttr(attrs, "stackkit.provider", provider)
	addAttr(attrs, "stackkit.node_id", nodeID)

	return OTelRuntime{
		Enabled:            endpoint != "",
		Endpoint:           endpoint,
		Protocol:           protocol,
		Headers:            headers,
		ServiceName:        serviceName,
		ServiceVersion:     serviceVersion,
		Environment:        environment,
		RunID:              strings.TrimSpace(cfg.RunID),
		TenantDeploymentID: strings.TrimSpace(cfg.TenantDeploymentID),
		StackKit:           strings.TrimSpace(cfg.StackKit),
		Provider:           provider,
		NodeID:             nodeID,
		ResourceAttributes: attrs,
	}
}

func StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, SpanHandle) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, span := otel.Tracer("github.com/kombifyio/stackkits").Start(
		ctx,
		strings.TrimSpace(name),
		oteltrace.WithAttributes(attrsToKeyValues(attrs)...),
	)
	return ctx, SpanHandle{span: span}
}

func (h SpanHandle) AddEvent(name string, attrs map[string]string) {
	if h.span == nil {
		return
	}
	h.span.AddEvent(strings.TrimSpace(name), oteltrace.WithAttributes(attrsToKeyValues(attrs)...))
}

func (h SpanHandle) SetAttributes(attrs map[string]string) {
	if h.span == nil {
		return
	}
	h.span.SetAttributes(attrsToKeyValues(attrs)...)
}

func (h SpanHandle) RecordError(err error) {
	if h.span == nil || err == nil {
		return
	}
	h.span.RecordError(errors.New(RedactTelemetryValue(err.Error())))
}

func (h SpanHandle) SetRolloutStatus(status, description string) {
	if h.span == nil {
		return
	}
	switch strings.TrimSpace(status) {
	case "failed":
		h.span.SetStatus(codes.Error, RedactTelemetryValue(description))
	case "succeeded", "success":
		h.span.SetStatus(codes.Ok, "")
	}
}

func (h SpanHandle) End() {
	if h.span != nil {
		h.span.End()
	}
}

func (h SpanHandle) IsRecording() bool {
	return h.span != nil && h.span.IsRecording()
}

func lookupTrimmed(lookup EnvLookup, key string) string {
	value, ok := lookup(key)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func addAttr(attrs map[string]string, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	attrs[key] = value
}

func newTraceExporter(ctx context.Context, runtime OTelRuntime) (sdktrace.SpanExporter, error) {
	switch strings.ToLower(strings.TrimSpace(runtime.Protocol)) {
	case "", "grpc":
		return otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpointURL(runtime.Endpoint),
			otlptracegrpc.WithHeaders(runtime.Headers),
			otlptracegrpc.WithTimeout(5*time.Second),
		)
	case "http", "http/protobuf":
		return otlptracehttp.New(ctx,
			otlptracehttp.WithEndpointURL(runtime.Endpoint),
			otlptracehttp.WithHeaders(runtime.Headers),
			otlptracehttp.WithTimeout(5*time.Second),
		)
	default:
		return nil, fmt.Errorf("unsupported OTLP trace protocol %q", runtime.Protocol)
	}
}

func attrsToKeyValues(attrs map[string]string) []attribute.KeyValue {
	kvs := make([]attribute.KeyValue, 0, len(attrs))
	for key, value := range attrs {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		kvs = append(kvs, attribute.String(key, RedactTelemetryValue(value)))
	}
	return kvs
}

func parseOTLPHeaders(value string) map[string]string {
	headers := map[string]string{}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, rawValue, ok := strings.Cut(part, "=")
		key = strings.TrimSpace(key)
		rawValue = strings.TrimSpace(rawValue)
		if !ok || key == "" || rawValue == "" {
			continue
		}
		headers[key] = rawValue
	}
	return headers
}
