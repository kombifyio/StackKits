// Package security_baseline — Host-level security hardening for StackKits.
//
// Ships UFW (deny-all-incoming, allow 22/80/443), fail2ban (SSH + Traefik jails),
// unattended-upgrades, SSH hardening (no root, key-only, no password auth),
// and sysctl hardening.
//
// This is a Foundation-layer module. It configures the HOST, not a container.
// The Terraform implementation uses `null_resource` with `remote-exec` provisioners.
// For CUE contract purposes, this module declares capabilities but ships no
// Docker service or container-based provisioner.
//
// STATUS: Scaffolded for V6 Phase 2. Terraform implementation pending.
package security_baseline

import "github.com/kombifyio/stackkits/base"

Contract: base.#ModuleContract & {
	metadata: {
		name:        "security-baseline"
		displayName: "Security Baseline"
		version:     "0.1.0"
		layer:       "L1-foundation"
		description: "Host-level hardening: UFW, fail2ban, SSH hardening, unattended-upgrades, sysctl. Foundation layer, mandatory in V6."
		testScenarios: ["SK-S3", "SK-S4"]
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

			// SSH: disable root login entirely.
			sshPermitRoot: *false | bool
		}
	}

	contexts: {
		// Same hardening defaults across all contexts.
		local: {}
		cloud: {}
		pi: {}
	}

	// No Docker services — this module configures the host via Terraform
	// null_resource + remote-exec in modules/security-baseline/terraform/.
	services: {}
}
