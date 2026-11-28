// Package diagnose defines the single versioned failure-signature playbook
// table shared across CLI, /mcp, stackkit-watchdog, and docs (ADR-0051).
package diagnose

#BlastRadius: "core" | "app" | "extension" | "host"

#RepairEffect: "" | "restart-service" | "reconcile-compose" | "reload-router" | "renew-tls" | "retry-appsetup" | "reconcile-coolify-proxy" | "gc-dangling-images" | "restore-over-live" | "clear-active-lease"

#FailurePlaybook: {
	id:                    string
	blastRadius:           #BlastRadius
	summary:               string
	autoRepairEffect:      #RepairEffect
	openDefaultAllowed:    bool
	requiresHumanApproval: bool
	hardStop:              bool
	guidance:              string
}

Playbooks: [string]: #FailurePlaybook
Playbooks: {
	"container-stopped": {
		id:                    "container-stopped"
		blastRadius:           "app"
		summary:               "Declared container is stopped or exited unexpectedly while OpenTofu state is unchanged."
		autoRepairEffect:      "restart-service"
		openDefaultAllowed:    true
		requiresHumanApproval: false
		hardStop:              false
		guidance:              "Restart the stopped service under node action_lease or run `stackkit repair restart-service --target <service>`."
	}
	"container-oom": {
		id:                    "container-oom"
		blastRadius:           "app"
		summary:               "Container was terminated by the Linux OOM killer."
		autoRepairEffect:      "restart-service"
		openDefaultAllowed:    true
		requiresHumanApproval: false
		hardStop:              false
		guidance:              "Restart once within circuit-breaker limits; if OOM repeats, raise the module memory profile before retrying."
	}
	"image-digest-drift": {
		id:                    "image-digest-drift"
		blastRadius:           "app"
		summary:               "Running container image digest differs from the ResolvedPlan SHA-256 pin."
		autoRepairEffect:      "reconcile-compose"
		openDefaultAllowed:    true
		requiresHumanApproval: false
		hardStop:              false
		guidance:              "Reconcile the Compose root back to the pinned ResolvedPlan image digest."
	}
	"router-stale": {
		id:                    "router-stale"
		blastRadius:           "core"
		summary:               "Kit-Traefik router probe failed or route table is out of sync."
		autoRepairEffect:      "reload-router"
		openDefaultAllowed:    true
		requiresHumanApproval: false
		hardStop:              false
		guidance:              "Reload Kit-Traefik router configuration and re-verify Access Gate routes."
	}
	"tls-cert-expiring": {
		id:                    "tls-cert-expiring"
		blastRadius:           "core"
		summary:               "TLS certificate is missing, invalid, or within renewal window."
		autoRepairEffect:      "renew-tls"
		openDefaultAllowed:    true
		requiresHumanApproval: false
		hardStop:              false
		guidance:              "Trigger ACME or Owner CA certificate renewal via `stackkit repair renew-tls`."
	}
	"appsetup-degraded": {
		id:                    "appsetup-degraded"
		blastRadius:           "app"
		summary:               "Optional Tier-2 app personalization failed while Tier-1 safety and routing remain intact."
		autoRepairEffect:      "retry-appsetup"
		openDefaultAllowed:    true
		requiresHumanApproval: false
		hardStop:              false
		guidance:              "Retry Tier-2 personalization with `stackkit retry-appsetup <workload>` or complete the first-run wizard behind Access Gate."
	}
	"coolify-proxy-port-drift": {
		id:                    "coolify-proxy-port-drift"
		blastRadius:           "extension"
		summary:               "Coolify internal proxy drifted toward host ports 80/443 or ACME ownership."
		autoRepairEffect:      "reconcile-coolify-proxy"
		openDefaultAllowed:    true
		requiresHumanApproval: false
		hardStop:              false
		guidance:              "Re-apply Option-A HTTP-only Coolify proxy overlay without host ports 80/443."
	}
	"adguard-dns-down": {
		id:                    "adguard-dns-down"
		blastRadius:           "core"
		summary:               "Local AdGuard Home resolver is unreachable."
		autoRepairEffect:      "restart-service"
		openDefaultAllowed:    true
		requiresHumanApproval: false
		hardStop:              false
		guidance:              "Restart AdGuard Home; Connected mode uses pinned fallback DNS, Offline/Local-Only stays local-only and never switches to public DNS."
	}
	"disk-full-floor": {
		id:                    "disk-full-floor"
		blastRadius:           "host"
		summary:               "Host free disk space dropped below the safe operating floor."
		autoRepairEffect:      ""
		openDefaultAllowed:    false
		requiresHumanApproval: true
		hardStop:              true
		guidance:              "Hard-stop automation immediately; do not auto-prune images (gc-dangling-images requires explicit owner approval). Free disk space and re-run diagnose."
	}
	"clock-skew-exceeded": {
		id:                    "clock-skew-exceeded"
		blastRadius:           "host"
		summary:               "Host clock skew exceeds TLS/OIDC/lease safety threshold."
		autoRepairEffect:      ""
		openDefaultAllowed:    false
		requiresHumanApproval: true
		hardStop:              true
		guidance:              "Hard-stop automation immediately; restore NTP/RTC time synchronization before running lifecycle mutations."
	}
	"custody-missing-or-corrupt": {
		id:                    "custody-missing-or-corrupt"
		blastRadius:           "host"
		summary:               "Node Owner custody or encrypted state key is missing or unreadable."
		autoRepairEffect:      ""
		openDefaultAllowed:    false
		requiresHumanApproval: true
		hardStop:              true
		guidance:              "Hard-stop automation immediately; restore custody from verified Kopia backup with explicit Owner approval."
	}
	"circuit-breaker-tripped": {
		id:                    "circuit-breaker-tripped"
		blastRadius:           "host"
		summary:               "Auto-repair reached the circuit breaker cap (max 2 auto-repairs per signature per hour)."
		autoRepairEffect:      ""
		openDefaultAllowed:    false
		requiresHumanApproval: true
		hardStop:              true
		guidance:              "Automation halted for this signature; inspect the redacted incident envelope and resolve root cause manually."
	}
	"active-lease-held": {
		id:                    "active-lease-held"
		blastRadius:           "host"
		summary:               "Another host writer currently holds the unified node action_lease."
		autoRepairEffect:      ""
		openDefaultAllowed:    false
		requiresHumanApproval: true
		hardStop:              true
		guidance:              "Wait for the active writer to finish; no agent or watchdog may clear an active lease."
	}
}
