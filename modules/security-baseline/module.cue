// Package security_baseline — Host-level security hardening for StackKits.
//
// Ships UFW (deny-all-incoming, allow SSH/80/443), fail2ban (SSH jail),
// unattended-upgrades, SSH hardening (key-only, no password auth),
// and sysctl hardening.
//
// This is a Foundation-layer module. It configures the HOST, not a container.
// The public beta implementation is applied by `stackkit apply` on apt-based
// Ubuntu hosts and writes `.stackkit/security-baseline.json` evidence. For CUE
// contract purposes, this module declares host capabilities and ships no Docker
// service or container-based provisioner.
package security_baseline

import "github.com/kombifyio/stackkits/base"

Contract: base.#ModuleContract & {
	metadata: {
		name:        "security-baseline"
		displayName: "Security Baseline"
		version:     "0.1.1"
		layer:       "L1-foundation"
		description: "Host-level hardening: UFW, fail2ban, SSH hardening, unattended-upgrades, sysctl. Foundation layer, mandatory for the BaseKit public beta."
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
			"firewall":               true
			"brute-force-protection": true
			"auto-updates":           true
			"ssh-hardening":          true
			"kernel-hardening":       true
		}
	}

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
