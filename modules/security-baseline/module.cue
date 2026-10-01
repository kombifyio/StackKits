// Package security_baseline — Universal host-level hardening for StackKits.
//
// What this module really applies, on every managed node: security-only
// unattended upgrades (apt) or the daily apk upgrade job, and a fixed set of
// kernel parameters (net.ipv4.tcp_syncookies, kernel.kptr_restrict,
// kernel.dmesg_restrict, fs.protected_hardlinks/symlinks/fifos/regular).
//
// It does NOT install or configure a firewall, sshd hardening or fail2ban.
// Those need site-kind inputs this input-free Foundation unit does not have, so
// its evidence records them as "delegated" to the site-kind owners:
//   - Home sites: stackkits-home-host-security-runtime (nftables default-drop
//     inbound that admits the LAN, overlay interfaces and container bridges,
//     key-only sshd, fail2ban; never cuts the management path).
//   - Cloud sites: stackkits-cloud-host-security-runtime (nftables default-deny
//     with declared-service ingress, key-only sshd, fail2ban).
//
// This is a Foundation-layer module. It configures the HOST, not a container.
// `stackkit apply` runs the exact CUE-owned host policy
// (internal/securitybaseline, Architecture v2) and writes
// `.stackkit/security-baseline.json` (stackkit.security-baseline/v2).
// `stackkit host security verify` observes every control again, including the
// delegated ones, as expiring stackkit.host-security-evidence/v1. The UFW-based
// legacy-v1 script has no live applier. For CUE contract purposes, this module
// declares host capabilities and ships no Docker service or container-based
// provisioner.
package security_baseline

import "github.com/kombifyio/stackkits/foundation"

Contract: foundation.#ModuleContract & {
	metadata: {
		name:        "security-baseline"
		displayName: "Security Baseline"
		version:     "0.1.1"
		layer:       "L1-foundation"
		description: "Universal host-level hardening: security-only unattended upgrades and managed kernel parameters on every node. Firewall, ssh and fail2ban belong to the Home and Cloud host-security owners. Foundation layer."
		maturity:    "default"
		testScenarios: ["SK-S1", "SK-S2", "SK-S3"]
	}

	requires: {
		infrastructure: {
			// Uses the host directly (SSH+sudo via Terraform), not Docker.
			docker:            false
			persistentStorage: false
		}
	}

	provides: {
		capabilities: {
			// Firewall, brute-force protection and ssh hardening are provided
			// by the Home and Cloud host-security owners, not by this module.
			"firewall":               false
			"brute-force-protection": false
			"auto-updates":           true
			"ssh-hardening":          false
			"kernel-hardening":       true
		}
	}

	// Legacy v1 settings (UFW, ports, fail2ban): nothing in the Architecture v2
	// path reads them.
	settings: {
		perma: {
			// Default UFW policies — rarely changed.
			defaultIncomingPolicy: *"deny" | "reject"
			defaultOutgoingPolicy: *"allow" | "deny"
		}
		flexible: {
			// SSH port — flexible so users with non-standard setups can override.
			sshPort: *22 | int & >0 & <65536

			// Allowlisted incoming ports besides 80/443. HTTP/HTTPS are always allowed.
			extraAllowedPorts: [...int] | *[]

			// fail2ban bantime in seconds.
			fail2banBanTime: *3600 | int & >=60

			// fail2ban maxretry before ban.
			fail2banMaxRetry: *5 | int & >=1

			// unattended-upgrades: include security updates only (true) or all (false).
			securityUpdatesOnly: *true | bool

			// SSH: disable password authentication entirely.
			sshPasswordAuth: *false | bool

			// SSH: keep root key-only unless the host provisions a non-root transport.
			sshPermitRoot: *false | bool
		}
	}

	contexts: {
		// Same hardening defaults across all contexts.
		local: {}
		cloud: {}
		pi: {}
	}

	// No Docker services — this module configures the host through the CLI
	// apply path and emits security-baseline evidence.
	services: {}
}
