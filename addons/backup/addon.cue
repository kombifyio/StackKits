// Package backup - Backup Add-On
//
// Single-engine encrypted backups built on Kopia. This file defines the
// self-hosted contract: Kopia engine + Kopia Web UI behind Traefik on the
// operator's own host, managed from the dashboard or `stackkit backup` CLI.
//
// Database consistency is handled INTERNALLY via pre-snapshot hooks (sqlite,
// postgres, redis, mariadb, mongodb). Users do not configure separate tools
// like Litestream or pgBackRest — the addon detects which DBs run in the
// stack and wires up the right hook automatically.
//
// Kopia remains the primary operational backup engine. A portable emergency
// export is modeled as the complementary fallback for tool-independent restore:
// encrypted tar/zip archives, DB dumps, manifests, and checksums that can be
// read without a working Kopia repository.
//
// Engine choice rationale:
//   - Kopia (Apache-2.0): client-side encryption, dedup, compression, S3/B2/
//     SFTP/Hetzner-Storagebox backends, native Web-UI, Repository Server
//     for multi-host fan-in.
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
		stackkits: ["basement-kit", "cloud-kit"]
		contexts: ["local", "cloud", "pi"]
		requires: []
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

	// Data classes covered by the default policy. Module/use-case metadata
	// narrows this list per volume; cache/generated data remains excluded
	// unless a module explicitly marks it as user content.
	dataClasses: [...#DataClass] | *["config", "secrets", "platform-state", "database", "user-content", "documents", "photos", "telemetry-timeseries", "serverless-config"]

	// Internal, generator-facing view of dataClasses resolved against the
	// canonical policy table. This is deliberately hidden so it does not
	// become another user knob in stack specs.
	_resolvedPolicy: #PolicyForDataClasses & {
		classes: dataClasses
	}

	// Retention policy (mapped to Kopia policy at apply time).
	retention: #RetentionPolicy

	// Backup targets (local + offsite).
	targets: #BackupTargets

	// Immutability / append-only protection on the offsite repository.
	// Default 7 days protects against ransomware on the host.
	immutability: #ImmutabilityConfig

	// Restore drill: monthly automated restore verification.
	restoreDrill: #RestoreDrillConfig

	// Recovery layers around Kopia: hardened single-server defaults and a
	// portable emergency export.
	resilience: #BackupResilienceConfig

	// Web UI exposure for the self-hosted path.
	webUI: #WebUIConfig

	// Notification channels for backup failures.
	notify?: #NotifyConfig
}

#RetentionPolicy: {
	keepDaily:   int | *7
	keepWeekly:  int | *4
	keepMonthly: int | *6
	keepYearly:  int | *0
}

#DataClass: "config" | "secrets" | "platform-state" | "database" | "user-content" | "documents" | "photos" | "large-media" | "telemetry-timeseries" | "serverless-config" | "cache-generated"

#DataClassPolicy: {
	class: #DataClass

	// Whether the class is included by default for self-hosted StackKits.
	defaultIncluded: bool

	// Minimum local cadence. Managed tiers may add tighter schedules.
	schedule: string

	// Operator-facing restore expectation for this class.
	restoreMode: "file" | "database-hook" | "volume" | "reseed" | "exclude"
}

#DataClassPolicyByClass: {
	config: {class: "config", defaultIncluded: true, schedule: "pre-change + daily", restoreMode: "file"}
	secrets: {class: "secrets", defaultIncluded: true, schedule: "pre-change + daily", restoreMode: "file"}
	"platform-state": {class: "platform-state", defaultIncluded: true, schedule: "pre-change + daily", restoreMode: "file"}
	database: {class: "database", defaultIncluded: true, schedule: "daily", restoreMode: "database-hook"}
	"user-content": {class: "user-content", defaultIncluded: true, schedule: "daily", restoreMode: "volume"}
	documents: {class: "documents", defaultIncluded: true, schedule: "daily", restoreMode: "volume"}
	photos: {class: "photos", defaultIncluded: true, schedule: "daily", restoreMode: "volume"}
	"large-media": {class: "large-media", defaultIncluded: false, schedule: "operator-selected", restoreMode: "volume"}
	"telemetry-timeseries": {class: "telemetry-timeseries", defaultIncluded: true, schedule: "daily", restoreMode: "database-hook"}
	"serverless-config": {class: "serverless-config", defaultIncluded: true, schedule: "pre-change + daily", restoreMode: "file"}
	"cache-generated": {class: "cache-generated", defaultIncluded: false, schedule: "never", restoreMode: "exclude"}
}

#DefaultDataClassPolicies: [
	#DataClassPolicyByClass.config,
	#DataClassPolicyByClass.secrets,
	#DataClassPolicyByClass["platform-state"],
	#DataClassPolicyByClass.database,
	#DataClassPolicyByClass["user-content"],
	#DataClassPolicyByClass.documents,
	#DataClassPolicyByClass.photos,
	#DataClassPolicyByClass["large-media"],
	#DataClassPolicyByClass["telemetry-timeseries"],
	#DataClassPolicyByClass["serverless-config"],
	#DataClassPolicyByClass["cache-generated"],
]

#PolicyForDataClasses: {
	classes: [...#DataClass]

	policies: [for class in classes {
		#DataClassPolicyByClass[class]
	}]

	byClass: {
		for policy in policies {
			"\(policy.class)": policy
		}
	}

	included: [for policy in policies if policy.defaultIncluded {
		policy.class
	}]

	excluded: [for policy in policies if !policy.defaultIncluded {
		policy.class
	}]
}

#BackupResilienceConfig: {
	singleServer:    #SingleServerBackupSafety
	multiServer:     #MultiServerBackupSafety
	emergencyExport: #EmergencyExportConfig
}

#SingleServerBackupSafety: {
	enabled: bool | *true

	// Target posture for a one-node system: live data, local Kopia repo, and
	// one off-host/export copy. This remains a contract until the generator
	// can materialize hard warnings for missing offsite targets.
	minimumRecoveryCopies: int & >=2 & <=3 | *3

	requireLocalRepo:         bool | *true
	requireEmergencyExport:   bool | *true
	requireRestoreDrill:      bool | *true
	requireOffHostCopy:       bool | *true
	recommendOffsite:         bool | *true
	recommendImmutableCopy:   bool | *true
	kopiaIndependentFallback: "portable-archive" | *"portable-archive"
}

#MultiServerBackupTopology: "two-server" | "three-node-ha" | "five-node-ha" | "five-manager-ha" | "geo-redundant"

#MultiServerBackupSafety: {
	enabled: bool | *true

	requireOffsiteRepo:           bool | *true
	requireEmergencyExport:       bool | *true
	requireRestoreDrill:          bool | *true
	requireSharedVolumeInventory: bool | *true
	requirePlacementSpread:       bool | *true
	requireManagedServerlessPlan: bool | *false

	media: {
		documentsMode:           "include"
		photosMode:              *"include" | "manifest-only" | "exclude"
		largeMediaMode:          *"manifest-only" | "include" | "exclude"
		excludeGeneratedCaches:  bool | *true
		requireExternalMediaMap: bool | *true
	}

	performance: {
		profile:                    *"balanced" | "low-io" | "throughput"
		avoidPrimaryOnlySnapshots:  bool | *true
		staggerNodeSnapshots:       bool | *true
		maxConcurrentNodeSnapshots: int & >=1 | *1
		preferRepoServerFanIn:      bool | *true
	}

	*{
		topology:                 "three-node-ha"
		minServers:               3
		minManagers:              3
		quorumSize:               2
		toleratedManagerFailures: 1
		capacityHeadroomNodes:    0
		releaseReadyHA:           true
		coordinationMode:         "quorum-aware"
	} | {
		topology:                     "two-server"
		minServers:                   2
		minManagers:                  1
		quorumSize:                   1
		toleratedManagerFailures:     0
		capacityHeadroomNodes:        1
		releaseReadyHA:               false
		coordinationMode:             "warm-standby"
		requireManagedServerlessPlan: true
	} | {
		topology:                 "five-node-ha"
		minServers:               5
		minManagers:              3
		quorumSize:               2
		toleratedManagerFailures: 1
		capacityHeadroomNodes:    2
		releaseReadyHA:           true
		coordinationMode:         "quorum-aware"
	} | {
		topology:                 "five-manager-ha"
		minServers:               5
		minManagers:              5
		quorumSize:               3
		toleratedManagerFailures: 2
		capacityHeadroomNodes:    0
		releaseReadyHA:           true
		coordinationMode:         "quorum-aware"
	} | {
		topology:                     "geo-redundant"
		minServers:                   3
		minManagers:                  3
		quorumSize:                   2
		toleratedManagerFailures:     1
		capacityHeadroomNodes:        0
		releaseReadyHA:               true
		coordinationMode:             "geo-aware"
		requireManagedServerlessPlan: true
	}
}

#EmergencyExportConfig: {
	enabled: bool | *true

	// "portable-archive" is the Kopia-independent fallback. Provider-native
	// snapshots can complement it on managed infrastructure but must not be
	// the only export path for self-hosted hosts.
	mode:   *"portable-archive" | "provider-native-snapshot"
	format: *"tar.zst.age" | "tar.gz.age" | "zip.age"

	schedule: string | *"0 3 * * 0"

	includeClasses: [...#DataClass] | *[
		"config",
		"secrets",
		"platform-state",
		"database",
		"documents",
		"serverless-config",
	]

	// Large media defaults to a manifest-only export so operators can restore
	// or reattach NAS/object-store libraries without paying for duplicate
	// weekly archives. Set include only for explicitly managed media backups.
	largeMediaMode: *"manifest-only" | "include" | "exclude"

	target: #EmergencyExportTarget | *{
		name: "emergency-export"
		type: "local"
		path: "/backup/emergency-export"
	}

	manifest: {
		enabled:               bool | *true
		includeRestoreRunbook: bool | *true
		includeChecksums:      bool | *true
	}
}

#EmergencyExportTarget: {
	name: string
	type: "local" | "s3" | "b2" | "sftp"

	path?: string

	s3?: {
		endpoint:  string
		bucket:    string
		accessKey: =~"^secret://"
		secretKey: =~"^secret://"
		region:    string | *"us-east-1"
	}

	b2?: {
		bucket:     string
		accountId:  =~"^secret://"
		accountKey: =~"^secret://"
	}

	sftp?: {
		host:     string
		user:     string
		password: =~"^secret://"
		path:     string | *"/backup/emergency-export"
	}
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
		provider: *"b2" | "hetzner-storagebox" | "s3" | "kombify-r2"

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
	emergencyExport:  bool | *true
	webUIUrl?:        string
	immutabilityDays: int
	lastBackup?:      string
	nextScheduled?:   string
}

// Placement eligibility (PUBLISHABLE metadata, base/placement.cue
// #PlacementSupport). Explicit safe-open S1 defaults; managed-serverless
// stays opt-in via Control-Plane enablement.
placementSupport: {
	local_only:         true
	standard:           true
	managed_serverless: false
}
