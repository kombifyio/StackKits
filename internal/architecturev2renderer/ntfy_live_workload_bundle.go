package architecturev2renderer

import "strings"

// The explicit ntfy-live alternative forwards only to connected subscribers.
// It is not a fallback for durable notification delivery or native accounts.
func ntfyLiveWorkload() aiAddOnWorkload {
	return aiAddOnWorkload{
		kind: "NtfyLiveWorkloadBundle", name: "ntfy live notifications", moduleID: "stackkits-ntfy-live-runtime", unitID: "ntfy-live",
		workloadRef: "automation-notifications", alternativeRef: "ntfy-live", release: strings.TrimPrefix(ntfyLiveRelease, "v"),
		imageRef: ntfyLiveImageRef, imageDigest: ntfyLiveImageDigest, port: 80,
		schemaBoundary:    "|peer:none|delivery:live-only|cache:noop|auth:ingress-admin|attachments:disabled|webpush:disabled",
		requiredPrivilege: "admin", ingressAuth: "forward-auth",
		component: selectedPaaSRuntimeComponent{
			ID: "ntfy-live", Role: "application", Lifecycle: "daemon",
			Image:     selectedPaaSRuntimeImage{Ref: ntfyLiveImageRef, Digest: ntfyLiveImageDigest},
			DependsOn: []string{}, NetworkRefs: []string{"ntfy-live-internal"},
			Command:     []string{"serve", "--cache-duration", "0", "--enable-login=false", "--enable-signup=false", "--enable-reservations=false"},
			Environment: map[string]string{"NTFY_LOG_LEVEL": "WARN", "NTFY_AUTH_FILE": "", "NTFY_DATABASE_URL": "", "NTFY_WEB_PUSH_FILE": "", "NTFY_WEB_PUSH_PUBLIC_KEY": "", "NTFY_WEB_PUSH_PRIVATE_KEY": "", "NTFY_ATTACHMENT_CACHE_DIR": "", "NTFY_UPSTREAM_BASE_URL": ""},
			Health:      selectedPaaSRuntimeHealth{Kind: "http", Path: "/v1/health", Port: 80},
			Resources:   &selectedPaaSRuntimeLimits{MemoryLimit: "256m", MemoryReservation: "64m"},
		},
	}
}

func NtfyLiveWorkloadBundleRendererContract() RendererContract { return ntfyLiveWorkload().contract() }
func ParseNtfyLiveWorkloadBundle(data []byte) (AIAddOnWorkloadBundleDescriptor, error) {
	return ntfyLiveWorkload().parse(data)
}
