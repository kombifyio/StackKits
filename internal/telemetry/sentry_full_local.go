//go:build kombify_full_local

package telemetry

import "time"

const retainLocalFailureEvidence = true

// SentryCaptureOptions retains call-site compatibility without linking Sentry.
// Transport is an ignored compatibility value in Full Local builds. Standard
// builds retain the exact sentry.Transport type and delivery behavior.
type SentryCaptureOptions struct {
	Transport    any
	FlushTimeout time.Duration
}

// ResolveSentryRuntime keeps scrubbed local metadata, never sender credentials.
// The build profile disables delivery independently of the process environment.
func ResolveSentryRuntime(cfg SentryConfig, lookup EnvLookup) SentryRuntime {
	runtime := resolveSentryMetadata(cfg, lookup)
	runtime.Environment = RedactTelemetryValue(runtime.Environment)
	runtime.Release = RedactTelemetryValue(runtime.Release)
	return runtime
}

func captureRemoteSentryFailure(SentryRuntime, string, SentryCaptureOptions) (string, bool, string) {
	return "", false, ""
}
