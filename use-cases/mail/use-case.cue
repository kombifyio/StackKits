// Package mail defines the Private Mail use case package.
//
// Mail is client-first (decision 2026-09-24): Roundcube Webmail is the first
// executable client for a mailbox the owner already has at an external
// provider. StackKits installs no mail server, no SMTP listener and no DNS or
// MX records. A self-hosted mail server (Stalwart or mailcow) remains a later,
// separate selection and is deliberately not declared as a tool here, so this
// package never implies an executable server.
package mail

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {
		name:        "mail"
		useCaseRef:  "mail"
		displayName: "Private Mail"
		version:     "0.2.0"
		layer:       "application"
		category:    "mail"
		lifecycle:   "experimental"
		description: "Private webmail through Roundcube for an existing external IMAP/SMTP mailbox. No mail server, DNS or MX record is created."
	}

	selection: {
		role: "optional"
		defaultTool: {
			moduleSlug: "roundcube"
			role:       "primary"
			required:   true
			rationale:  "Roundcube is a mature, self-contained webmail client: it gives the owner a private web interface to an existing mailbox without taking on mail delivery, reputation or DNS."
			capabilities: ["webmail-client", "mailbox-access", "address-book"]
		}
		alternatives: []
	}

	defaultRuntimeProfile: "self-hosted-webmail"
	runtimeProfiles: "kombify-managed-paperwork": {
		displayName: "kombify Paperwork (managed with kombify Cloud)"
		description: "kombify Paperwork as a managed SaaS mail client that comes with kombify Cloud. kombify operates it; this profile deploys nothing on the owner's nodes."
		realization: "control-plane"
		placementModes: ["managed-serverless"]
		managedServerlessEligible: true
		requiresControlPlane:      true
		requiresLocalBridge:       false
		notes: [
			"Declared handoff only (owner decision 2026-09-24): Paperwork is offered as a managed kombify Cloud service, not as a self-hosted edition. It is not the default and not an entitlement; access follows the kombify Cloud offering.",
			"The Roundcube default stays account-free in Standard Mode and never requires this profile.",
		]
	}
	runtimeProfiles: "self-hosted-webmail": {
		displayName: "Self-hosted Webmail"
		description: "Roundcube with a local SQLite database runs on one owner-selected node through Standalone Compose and connects out to the owner's existing IMAP and SMTP servers."
		realization: "oss"
		placementModes: ["local-only", "standard"]
		managedServerlessEligible: false
		requiresControlPlane:      false
		requiresLocalBridge:       false
		notes: [
			"`stackkit setup mail` stores the owner's IMAP and SMTP servers (host, port, ssl or starttls; never the password). Until then Roundcube refuses logins; the login form never asks for a server.",
			"Device setup: the mail route serves Mozilla autoconfig, Outlook autodiscover and an unsigned Apple configuration profile generated from the stored servers.",
			"Mail stays with the owner's provider. StackKits backs up Roundcube's own database: preferences, identities and the address book.",
		]
	}

	computeTiers: {
		low: {included: true, moduleSlug: "roundcube", functions: ["webmail-client", "mailbox-access", "address-book"], load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}, notes: ["Roundcube with SQLite reserves 128 MiB and is idle between requests; it fits the low graph like Vaultwarden."]}
		standard: {included: true, moduleSlug: "roundcube", functions: ["webmail-client", "mailbox-access", "address-book"], load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}, notes: ["Same application and reservation as low."]}
		high: {included: true, moduleSlug: "roundcube", functions: ["webmail-client", "mailbox-access", "address-book"], load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}, notes: ["Same application and reservation as standard; high adds no mail server."]}
	}

	tools: roundcube: {
		moduleSlug: "roundcube"
		role:       "primary"
		required:   true
		rationale:  "Digest-pinned Roundcube Webmail with native login, login rate limiting, IP-bound sessions and a key derived from owner custody."
		capabilities: ["webmail-client", "mailbox-access", "address-book"]
	}

	connectors: stackkit: {
		kind:      "stackkit"
		name:      "stackkit"
		owner:     "stackkit"
		endpoint:  "/mcp"
		transport: "streamable-http"
		auth:      "stackkit-mcp-token"
		capabilities: ["lifecycle", "setup", "evidence"]
	}

	setup: {
		defaultPolicy: "on_demand"
		drops: [{
			name:        "mailbox-login"
			policy:      "on_demand"
			description: "After owner approval, store the owner's IMAP and SMTP servers for Roundcube and device setup, then verify that the existing mailbox signs in through Roundcube's own login form and sign out. A failed check restores the previous servers. The password is used once and never stored."
		}]
	}

	evidence: {
		healthChecks: ["roundcube-http"]
		required: ["route", "backup", "owner-bootstrap", "runtime-owner", "removal"]
	}

	lifecycle: foundation.#StandardUseCaseLifecycle & {
		stages: setup: {}
	}

	agentSurface: {
		equipPolicy:  "on-generate"
		lifecycleMcp: {}
		productMcps: []
		apis: []
		skills: [{
			id:       "mail-client"
			audience: "product-user"
			source:   "stackkits"
			path:     "use-cases/mail/agent/mail-client/SKILL.md"
		}]
		cliHelpers: [{
			command: "stackkit setup mail"
			purpose: "Verify a real mailbox login through Roundcube after owner approval."
		}, {
			command: "stackkit agent mcp-config"
			purpose: "Print the stackkit lifecycle MCP client connection."
		}]
		configBaseline: {
			status: "omitted"
			reason: "Roundcube reads a governed StackKits startup file; the owner's mailbox servers are stored by the setup action on the persistent mailbox volume, so there is no separate owner configuration file."
		}
	}
}
