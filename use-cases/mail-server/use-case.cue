// Package mail_server defines the Own Mail Server use case package.
//
// Mail v1 has two separately selectable parts under the Mail main use case
// (owner decision 2026-09-24, ADR-0046): the `mail` client (Roundcube) and this
// own mail server. Stalwart is the default. mailcow is the planned alternative
// behind a separate feasibility study; it is not declared as a tool because no
// executable module exists, so this package never implies one.
package mail_server

import "github.com/kombifyio/stackkits/foundation"

Package: foundation.#UseCasePackage & {
	metadata: {
		name:        "mail-server"
		useCaseRef:  "mail-server"
		displayName: "Own Mail Server"
		version:     "0.1.0"
		layer:       "application"
		category:    "mail-server"
		lifecycle:   "experimental"
		description: "Your own mail server through Stalwart on a Cloud node: domain mailboxes with SMTP, submission and IMAP. DNS records are printed for the owner, never created."
	}

	selection: {
		role: "optional"
		defaultTool: {
			moduleSlug: "stalwart"
			role:       "primary"
			required:   true
			rationale:  "Stalwart is a single-binary, memory-light mail server with built-in DKIM, SPF, DMARC, spam filtering, ACME and a management API, so one container covers receiving, sending and mailbox access."
			capabilities: ["mail-server", "mailbox-hosting", "smtp-delivery", "imap-access"]
		}
		alternatives: []
	}

	defaultRuntimeProfile: "cloud-mail-server"
	runtimeProfiles: "cloud-mail-server": {
		displayName: "Own mail server on a Cloud node"
		description: "Stalwart with its embedded RocksDB store runs on one public Cloud node through Standalone Compose; the node publishes SMTP (25), submissions (465), submission (587), IMAPS (993) and ManageSieve (4190)."
		realization: "oss"
		placementModes: ["standard"]
		managedServerlessEligible: false
		requiresControlPlane:      false
		requiresLocalBridge:       false
		notes: [
			"The mail host name is the workload's route host, mail-server.<domain>. The web administration is served there through the router with Stalwart's own login.",
			"Home nodes are refused for now: sending from a home connection needs an outbound relay, and StackKits has no owner relay input yet (ADR-0046).",
			"StackKits never creates DNS records. The setup action prints MX, SPF, DKIM, DMARC and client autoconfiguration records for the owner to publish.",
		]
	}

	computeTiers: {
		low: {included: true, moduleSlug: "stalwart", functions: ["mail-server", "mailbox-hosting", "smtp-delivery", "imap-access"], load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}, notes: ["Stalwart reserves 512 MiB for RocksDB caches and spam filtering and is idle between deliveries."]}
		standard: {included: true, moduleSlug: "stalwart", functions: ["mail-server", "mailbox-hosting", "smtp-delivery", "imap-access"], load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}, notes: ["Same application and reservation as low."]}
		high: {included: true, moduleSlug: "stalwart", functions: ["mail-server", "mailbox-hosting", "smtp-delivery", "imap-access"], load: {residency: "always-on", baseline: "idle-resident", burst: "interactive"}, notes: ["Same application and reservation as standard."]}
	}

	tools: stalwart: {
		moduleSlug: "stalwart"
		role:       "primary"
		required:   true
		rationale:  "Digest-pinned Stalwart with a custody-held fallback administrator, a governed first start and an owner-approved domain setup."
		capabilities: ["mail-server", "mailbox-hosting", "smtp-delivery", "imap-access"]
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
			name:        "mail-domain"
			policy:      "on_demand"
			description: "After owner approval, create the owner's mail domain and first mailbox in Stalwart, print the DNS records to publish, and verify an IMAP login and a submission handshake on the node. The mailbox password is used once and never stored."
		}]
	}

	evidence: {
		healthChecks: ["stalwart-http"]
		required: ["route", "backup", "owner-bootstrap", "runtime-owner", "removal"]
	}

	lifecycle: foundation.#StandardUseCaseLifecycle & {
		stages: setup: {}
	}

	agentSurface: {
		equipPolicy:  "on-generate"
		lifecycleMcp: {}
		productMcps: []
		apis: [{
			id:       "stalwart-jmap"
			protocol: "rest"
			purpose:  "Stalwart's JMAP management API (x: object methods) for domains, accounts, DKIM keys and settings."
			auth:     "stalwart-administrator"
		}]
		skills: [{
			id:       "mail-server"
			audience: "stack-operator"
			source:   "stackkits"
			path:     "use-cases/mail-server/agent/mail-server/SKILL.md"
		}]
		cliHelpers: [{
			command: "stackkit setup mail-server"
			purpose: "Create the mail domain and first mailbox after owner approval, print DNS records and verify IMAP and submission."
		}, {
			command: "stackkit agent mcp-config"
			purpose: "Print the stackkit lifecycle MCP client connection."
		}]
		configBaseline: {
			status: "omitted"
			reason: "Stalwart keeps its settings in its own store; StackKits governs only the entrypoint and first-start seed, and the owner changes settings in Stalwart's administration."
		}
	}
}
