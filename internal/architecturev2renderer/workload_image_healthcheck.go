package architecturev2renderer

// workloadImageHealthcheckStartIntervals holds, per pinned image digest, the
// start interval of the image's own HEALTHCHECK. Docker fills every duration
// a container health check leaves unset from the image, so a Compose health
// check without a start interval runs with the image's; the native provider
// reads that value back and would plan it away on every apply. Pinning it in
// the Compose health check makes the configured value the observed one on
// both executions. Read from the image configuration at the digest; an image
// upgrade re-reads it.
var workloadImageHealthcheckStartIntervals = map[string]string{
	// ghcr.io/immich-app/postgres:14-vectorchord0.4.3-pgvectors0.2.0:
	// HEALTHCHECK --interval=5m --start-period=5m --start-interval=5s
	// (Immich and Immich Lite database).
	"sha256:bcf63357191b76a916ae5eb93464d65c07511da41e3bf7a8416db519b40b1c23": "5s",
}

// WorkloadImageHealthcheckStartInterval returns the start interval a Compose
// health check of the image with this digest must pin, or "".
func WorkloadImageHealthcheckStartInterval(imageDigest string) string {
	return workloadImageHealthcheckStartIntervals[imageDigest]
}
