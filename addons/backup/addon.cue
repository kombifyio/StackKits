// Package backup - Backup Add-On
//
// Single-engine encrypted backups built on Kopia. Follows the 3-2-1 rule
// (3 copies, 2 different media, 1 offsite) and ships with two surfaces:
//
//   1. Self-Hosted (Free): Kopia engine + Kopia Web-UI behind Traefik on the
//      user's own host. The user manages everything from one dashboard or via
//      the `stackkit backup` CLI.
//
//   2. SaaS (Paid, kombify-TechStack): Same engine, but driven by a central
//      Backup-Controller via the `stackkit-backup-agent` binary. Users see
//      only the kombify-TechStack web UI.
//
// Database consistency is handled INTERNALLY via pre-snapshot hooks (sqlite,
// postgres, redis, mariadb, mongodb). Users do not configure separate tools
// like Litestream or pgBackRest — the addon detects which DBs run in the
// stack and wires up the right hook automatically.
//
// Engine choice rationale (see docs/ADR/ADR-0016-backup-single-engine-kopia.md):
//   - Kopia (Apache-2.0): client-side encryption, dedup, compression, S3/B2/
//     SFTP/Hetzner-Storagebox backends, native Web-UI, Repository Server
//     for multi-host fan-in (used by the SaaS path).
//   - Restic (BSD-2): the previous engine; supported only as a one-shot
//     migration importer (`stackkit backup migrate-from-restic`) for two
//     releases, then removed.
//
// Offsite providers (unchanged from v1):
//   - Backblaze B2 (cheapest object storage)
//   - Hetzner Storage Box (SFTP)
//   - Any S3-compatible endpoint (with optional Object Lock)
//
// Usage:
//   addons: backup: backup.#Config & {
//       schedule: "0 2 * * *"
//       targets: offsite: enabled: true
//   }

package backup

// #Config defines backup add-on configuration.
//
// All knobs that are NOT in this struct are intentionally invisible to the
// user. DB-quiesce hooks, integrity checks, and restore drills are wired up
// automatically based on what the surrounding StackKit deploys.
#Config: {
	_addon: {
		name:        "backup"
		displayName: "Backup"
		version:     "2.0.0"
		layer:       "INFRASTRUCTURE"
		description: "Kopia-based encrypted backups with built-in DB hooks and 3-2-1 strategy"
	}

	_compatibility: {
		stackkits: ["base-kit", "dev-homelab", "modern-homelab", "ha-kit"]
		contexts:  ["local", "cloud", "pi"]
		requires:  []
		conflicts: []
	}

	enabled: bool | *true

	// Backup engine. Kopia is the only supported engine going forward.
	// "restic-import" is a transitional value that triggers a one-shot
	// migration of an existing Restic repository into Kopia and then
	// switches to Kopia. See restic-importer.cue.
	engine: *"kopia" | "restic-import"

	// Backup schedule (cron, host TZ).
	schedule: string | *"0 2 * * *"

	// Retention policy (mapped to Kopia policy at apply time).
	retention: #RetentionPolicy

	// Backup targets (local + offsite).
	targets: #BackupTargets

	// Immutability / append-only protection on the offsite repository.
	// Default 7 days protects against ransomware on the host.
	immutability: #ImmutabilityConfig

	// Restore drill: monthly automated restore verification.
	restoreDrill: #RestoreDrillConfig

	// Web UI exposure (Self-Hosted-Pfad). Disabled when agentMode is on.
	webUI: #WebUIConfig

	// Agent mode (SaaS-Pfad). When enabled, scheduling and reporting are
	// driven by the kombify Backup-Controller instead of local cron.
	agentMode: #AgentModeConfig

	// Notification channels for backup failures.
	notify?: #NotifyConfig

	// Web-UI and agentMode are mutually exclusive: a host either runs in
	// self-hosted mode (web UI on, agent off) or as part of a fleet (agent
	// on, web UI off — control plane is the SaaS dashboard).
	if agentMode.enabled {
		webUI: enabled: false
	}
}

#RetentionPolicy: {
	keepDaily:   int | *7
	keepWeekly:  int | *4
	keepMonthly: int | *6
	keepYearly:  int | *0
}

#BackupTargets: {
	// Local backup (same machine or NAS).
	local: {
		enabled: bool | *true
		path:    string | *"/backup/kopia"
	}

	// Offsite backup (cloud storage). The 3-2-1 "1 offsite copy".
	offsite: {
		enabled:  bool | *false
		provider: *"b2" | "hetzner-storagebox" | "s3"

		if provider == "b2" {
			b2: {
				bucket:     string
				accountId:  =~"^secret://"
				accountKey: =~"^secret://"
			}
		}

		if provider == "hetzner-storagebox" {
			hetzner: {
				host:     string
				user:     string
				password: =~"^secret://"
				path:     string | *"/backup"
			}
		}

		if provider == "s3" {
			s3: {
				endpoint:  string
				bucket:    string
				accessKey: =~"^secret://"
				secretKey: =~"^secret://"
				region:    string | *"us-east-1"
			}
		}
	}
}

#ImmutabilityConfig: {
	// Days the offsite copy is locked against deletion / overwrite.
	// Maps to S3 Object Lock retention or B2 file-lock period.
	// 0 disables immutability — discouraged.
	retentionDays: int | *7

	// Mode: "compliance" cannot be shortened even by the root account.
	// "governance" can be lifted by privileged users (rare, for restore-test).
	mode: *"governance" | "compliance"
}

#RestoreDrillConfig: {
	// Run a real restore from a random snapshot once per period.
	enabled: bool | *true

	// How often (cron). Monthly default — enough to catch silent corruption.
	schedule: string | *"0 4 1 * *"

	// Where to materialize the test restore. tmpfs is preferred so the
	// drill cannot accidentally pollute the working dataset.
	target: *"tmpfs" | "volume"
}

#WebUIConfig: {
	// Expose Kopia Web-UI behind Traefik. Only effective in self-hosted mode.
	enabled: bool | *true

	// Subdomain prefix; resolved against the StackKit's domain at apply time.
	subdomain: string | *"backups"

	// Always behind login-gateway (TinyAuth + PocketID forward-auth).
	// Hard-wired to true; we never expose Kopia directly.
	authRequired: true
}

#AgentModeConfig: {
	// Run as a controlled fleet member instead of standalone.
	enabled: bool | *false

	// gRPC endpoint of the kombify Backup-Controller.
	controllerEndpoint?: string

	// Per-host enrollment token, issued by the controller during onboarding.
	enrollmentToken?: =~"^secret://"

	// Tenant the host belongs to (set by the controller).
	tenantId?: string
}

#NotifyConfig: {
	// Notification on failure (always on). Successful runs are silent.
	onFailure: bool | *true

	// Channels to fan out to.
	channels: [...#NotifyChannel]
}

#NotifyChannel: {
	type: "email" | "webhook" | "gotify"
	url:  string
}

// =============================================================================
// SERVICE DEFINITIONS
// =============================================================================

// #KopiaAgentService runs Kopia on every node and performs scheduled snapshots.
// In agentMode this container is replaced/wrapped by stackkit-backup-agent.
#KopiaAgentService: {
	name:        "kopia-agent"
	displayName: "Kopia Backup Agent"
	image:       "kopia/kopia:0.18"
	category:    "backup"

	placement: {
		nodeType: "all"
		strategy: "daemonset"
	}

	volumes: [
		{host: "/backup", path: "/backup", type: "bind"},
		{host: "/var/lib/docker/volumes", path: "/source/docker-volumes", type: "bind", readOnly: true},
		{name: "kopia-cache", path: "/app/cache", type: "volume"},
		{name: "kopia-config", path: "/app/config", type: "volume"},
	]

	environment: {
		KOPIA_PASSWORD: =~"^secret://"
	}
}

// #KopiaWebUIService exposes the Kopia Web UI behind Traefik. Only deployed
// in self-hosted mode (#Config.webUI.enabled).
#KopiaWebUIService: {
	name:        "kopia-ui"
	displayName: "Kopia Web UI"
	image:       "kopia/kopia:0.18"
	category:    "backup"

	placement: {
		nodeType: "main"
		strategy: "single"
	}

	ports: [
		{container: 51515, host: 51515, protocol: "tcp", name: "ui"},
	]

	volumes: [
		{name: "kopia-config", path: "/app/config", type: "volume"},
		{name: "kopia-cache", path: "/app/cache", type: "volume"},
	]

	traefik: {
		enabled: true
		// Subdomain comes from #WebUIConfig at template time.
		rule: "Host(`{{.backup.webUI.subdomain}}.{{.domain}}`)"
		// login-gateway middleware is appended automatically by the platform
		// because #WebUIConfig.authRequired is true.
	}
}

// =============================================================================
// OUTPUTS
// =============================================================================

// #Outputs is what this add-on exports to the deployment summary.
#Outputs: {
	engine:           string | *"kopia"
	localRepoPath:    string | *"/backup/kopia"
	offsiteEnabled:   bool
	webUIUrl?:        string
	immutabilityDays: int
	lastBackup?:      string
	nextScheduled?:   string
}
