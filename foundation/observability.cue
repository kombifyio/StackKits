// Package foundation - Observability configuration schemas
package foundation

// #LoggingConfig defines logging settings
#LoggingConfig: {
	// Logging driver
	driver: "json-file" | "journald" | "syslog" | "loki" | "none" | *"json-file"

	// Log level
	level: "debug" | "info" | "warn" | "error" | *"info"

	// Max log file size
	maxSize: string | *"50m"

	// Max number of log files
	maxFile: int | *5

	// Compress rotated logs
	compress: bool | *true

	// Log format
	format: "json" | "text" | *"json"

	// Include timestamps
	timestamps: bool | *true

	// Loki configuration (if driver = loki)
	loki?: {
		url:     string
		tenant?: string
		labels?: [string]: string
		batchSize: int | *1048576
		batchWait: string | *"1s"
	}
}

// #HealthCheck defines health check configuration
#HealthCheck: {
	// Enable health checks
	enabled: bool | *true

	// Check command or endpoint (simple format)
	command?: string

	// HTTP health check
	http?: {
		path:   string
		port:   uint16
		scheme: "http" | "https" | *"http"
	}

	// TCP health check
	tcp?: {
		port: uint16
	}

	// Docker Compose style test command (array format)
	// Example: ["CMD", "wget", "-q", "--spider", "http://localhost:8080/health"]
	test?: [...string]

	// Interval between checks
	interval: string | *"30s"

	// Timeout for each check
	timeout: string | *"10s"

	// Number of retries before unhealthy
	retries: int & >=1 & <=10 | *3

	// Start period (grace time)
	startPeriod: string | *"5s"
}

// #MetricsConfig defines metrics collection settings
#MetricsConfig: {
	// Enable metrics collection
	enabled: bool | *true

	// Metrics backend
	backend: "prometheus" | "influxdb" | "victoriametrics" | "none" | *"prometheus"

	// Metrics port
	port: uint16 | *9090

	// Metrics path
	path: string | *"/metrics"

	// Scrape interval
	scrapeInterval: string | *"15s"

	// Retention period
	retention: string | *"15d"

	// Enable remote write
	remoteWrite?: {
		url:       string
		username?: string
		password?: =~"^secret://"
	}

	// Node exporter configuration
	nodeExporter?: {
		enabled: bool | *true
		port:    uint16 | *9100
	}

	// Container exporter
	containerExporter?: {
		enabled: bool | *true
		port:    uint16 | *9323
	}
}

// #MonitoringSignals defines which OTLP signal lanes a StackKit enables.
#MonitoringSignals: {
	metrics: bool | *true
	logs:    bool | *false
	traces:  bool | *false
}

// #MonitoringProfile names the supported capacity classes for the OTLP-first
// monitoring contract. It is a capacity decision, not a backend selection.
#MonitoringProfile: "pi" | "single-node" | "multi-node"

// #MonitoringResourceBudget is a generated-runtime budget. It contains only
// allocation limits, never a provider, endpoint, credential, or backend
// implementation detail.
#MonitoringResourceBudget: close({
	enabled?:       bool
	cpuMilli:       int & >=50 & <=4000
	memoryMiB:      int & >=64 & <=8192
	ephemeralMiB:   int & >=64 & <=16384
	persistentMiB:  int & >=0 & <=1048576
	maxRetentionDays: int & >=0 & <=365
})

// #MonitoringProfileBudget is the closed, compiler-owned capacity projection
// for one supported profile. A disabled backend has a zero allocation and
// cannot be selected by that profile.
#MonitoringProfileBudget: close({
	profile:          #MonitoringProfile
	agent:            #MonitoringResourceBudget
	gateway:          #MonitoringResourceBudget & {enabled: bool}
	victoriametrics:  #MonitoringResourceBudget & {enabled: bool}
	logs:             #MonitoringResourceBudget & {enabled: bool}
	traces:           #MonitoringResourceBudget & {enabled: bool}
})

// #MonitoringProfileBudgets is the single source for the values shown in the
// operator documentation and emitted to future generated runtime artifacts.
// Pi stays collector-only; durable retention begins only at the explicit
// single-node and multi-node tiers.
#MonitoringProfileBudgets: close({
	pi: {
		profile: "pi"
		agent: {cpuMilli: 100, memoryMiB: 128, ephemeralMiB: 256, persistentMiB: 0, maxRetentionDays: 0}
		gateway: {enabled: false, cpuMilli: 50, memoryMiB: 64, ephemeralMiB: 64, persistentMiB: 0, maxRetentionDays: 0}
		victoriametrics: {enabled: false, cpuMilli: 50, memoryMiB: 64, ephemeralMiB: 64, persistentMiB: 0, maxRetentionDays: 0}
		logs: {enabled: true, cpuMilli: 50, memoryMiB: 64, ephemeralMiB: 128, persistentMiB: 0, maxRetentionDays: 7}
		traces: {enabled: true, cpuMilli: 50, memoryMiB: 64, ephemeralMiB: 128, persistentMiB: 0, maxRetentionDays: 0}
	}
	"single-node": {
		profile: "single-node"
		agent: {cpuMilli: 200, memoryMiB: 256, ephemeralMiB: 512, persistentMiB: 0, maxRetentionDays: 0}
		gateway: {enabled: true, cpuMilli: 500, memoryMiB: 512, ephemeralMiB: 1024, persistentMiB: 0, maxRetentionDays: 0}
		victoriametrics: {enabled: true, cpuMilli: 500, memoryMiB: 1024, ephemeralMiB: 1024, persistentMiB: 10240, maxRetentionDays: 14}
		logs: {enabled: true, cpuMilli: 100, memoryMiB: 128, ephemeralMiB: 256, persistentMiB: 0, maxRetentionDays: 14}
		traces: {enabled: true, cpuMilli: 100, memoryMiB: 128, ephemeralMiB: 256, persistentMiB: 0, maxRetentionDays: 0}
	}
	"multi-node": {
		profile: "multi-node"
		agent: {cpuMilli: 250, memoryMiB: 384, ephemeralMiB: 1024, persistentMiB: 0, maxRetentionDays: 0}
		gateway: {enabled: true, cpuMilli: 1000, memoryMiB: 1024, ephemeralMiB: 2048, persistentMiB: 0, maxRetentionDays: 0}
		victoriametrics: {enabled: true, cpuMilli: 1000, memoryMiB: 2048, ephemeralMiB: 2048, persistentMiB: 51200, maxRetentionDays: 30}
		logs: {enabled: true, cpuMilli: 200, memoryMiB: 256, ephemeralMiB: 512, persistentMiB: 0, maxRetentionDays: 30}
		traces: {enabled: true, cpuMilli: 200, memoryMiB: 256, ephemeralMiB: 512, persistentMiB: 0, maxRetentionDays: 0}
	}
})

// #MonitoringLogLaneConfig is the optional, provider-free OTLP log intent.
// Loki is a direction only: its deployment, endpoint and credentials remain
// adapter-owned and cannot enter the StackKits monitoring contract.
#MonitoringLogLaneConfig: close({
	enabled:       true
	protocol:      "otlp"
	direction:     "loki"
	lifecycle:     "external"
	retentionDays: int & >=1 & <=365
	budget:        #MonitoringResourceBudget & {enabled: true}
})

// #MonitoringTraceSampling fixes the portable sampling intent. Tempo is a
// direction rather than a StackKits-owned backend lifecycle.
#MonitoringTraceSampling: close({
	mode:  "parentbased-ratio"
	ratio: 0.1
})

// #MonitoringTraceLaneConfig is the optional, provider-free OTLP trace intent.
// It deliberately contains neither a Tempo endpoint nor credential material.
#MonitoringTraceLaneConfig: close({
	enabled:   true
	protocol:  "otlp"
	direction: "tempo"
	lifecycle: "external"
	sampling:  #MonitoringTraceSampling
	budget:    #MonitoringResourceBudget & {enabled: true}
})

#MonitoringOptionalSignalLanes: close({
	logs?:   #MonitoringLogLaneConfig
	traces?: #MonitoringTraceLaneConfig
})

// #MonitoringModuleRuntimeProfile is the concrete, secret-free input that a
// module renderer receives for a selected observability capacity class. It
// keeps the profile projection in CUE rather than duplicating values in module
// templates. Transport endpoints and backend credentials are intentionally
// absent.
#MonitoringModuleRuntimeProfile: close({
	profile:         #MonitoringProfile
	agent:           #MonitoringProfileBudgets[profile].agent
	gateway:         #MonitoringProfileBudgets[profile].gateway
	victoriametrics: #MonitoringProfileBudgets[profile].victoriametrics
})

// #OtelBatchConfig is a closed collector batching policy. Endpoint and
// credential material are deliberately absent: batching is local runtime
// behavior, while transport authority remains outside this contract.
#OtelBatchConfig: close({
	timeout:          "15s" | "30s"
	sendBatchSize:    int & >=100 & <=10000
	sendBatchMaxSize: int & >=sendBatchSize & <=20000
})

// #OtelRetryConfig fixes transient-delivery behavior without carrying an
// endpoint or authentication value.
#OtelRetryConfig: close({
	enabled:         true
	initialInterval: "5s"
	maxInterval:     "30s" | "60s"
})

// #OtelResourceEnrichmentConfig permits only deterministic local identity
// detectors. It cannot inject caller-supplied resource attributes.
#OtelResourceEnrichmentConfig: close({
	detectors: ["system"] | ["system", "docker"]
	override:  false
})

// #OtelAgentProcessingConfig is the per-node collector profile. It is tuned
// for low-overhead collection and keeps system plus Docker identity.
#OtelAgentProcessingConfig: close({
	batch:      #OtelBatchConfig & {timeout: "30s", sendBatchSize: 1000, sendBatchMaxSize: 2000}
	retry:      #OtelRetryConfig & {maxInterval: "30s"}
	enrichment: #OtelResourceEnrichmentConfig & {detectors: ["system", "docker"]}
})

// #OtelGatewayProcessingConfig is the fan-in profile. It batches more data,
// retains only system identity, and uses the bounded longer retry interval.
#OtelGatewayProcessingConfig: close({
	batch:      #OtelBatchConfig & {timeout: "15s", sendBatchSize: 5000, sendBatchMaxSize: 10000}
	retry:      #OtelRetryConfig & {maxInterval: "60s"}
	enrichment: #OtelResourceEnrichmentConfig & {detectors: ["system"]}
})

// #OtelCollectorAuthConfig defines outbound OTLP authentication.
#OtelCollectorAuthConfig: {
	mode: "none" | "headers" | *"none"
	headers?: [string]: string
}

// #OtelCollectorTLSConfig defines outbound OTLP TLS behavior.
#OtelCollectorTLSConfig: {
	insecure: bool | *true
	caFile?:  string
}

// #OtelCollectorConfig is the canonical per-node collector contract.
#OtelCollectorConfig: {
	enabled:  bool | *true
	endpoint: string | *"techstack:4317"
	protocol: "grpc" | "http/protobuf" | *"grpc"
	signals:  #MonitoringSignals
	auth:     #OtelCollectorAuthConfig
	tls:      #OtelCollectorTLSConfig
	resource?: [string]: string
	processing: #OtelAgentProcessingConfig
}

// #VictoriaMetricsConfig configures the optional retention backend.
#VictoriaMetricsConfig: {
	enabled:               bool | *false
	retentionDays:         *14 | (int & >=1 & <=365)
	retention:             "\(retentionDays)d"
	remoteWriteEndpoint:   string | *"http://victoriametrics:8428/api/v1/write"
	memoryAllowedPercent:  int & >=10 & <=80 | *40
	maxConcurrentRequests: int & >=1 & <=128 | *8
}

// #MonitoringGatewayConfig configures the optional collector gateway.
#MonitoringGatewayConfig: {
	enabled:              bool | *false
	endpoint:             string | *"otel-gateway:4317"
	batchTimeout:         string | *"15s"
	maxConcurrentStreams: int & >=1 & <=1000 | *50
	memoryLimitMiB:       int & >=64 & <=4096 | *200
	memorySpikeLimitMiB:  int & >=32 & <=2048 | *50
	forwardToTechStack:   bool | *false
	techStackEndpoint?:   string
	processing:           #OtelGatewayProcessingConfig
}

// #MonitoringBaselineConfig contains the mandatory per-node OTLP collector
// surface. It deliberately has no dashboard, storage, or gateway selection:
// disabling or changing optional backends must never change what the local
// collector observes or how it emits telemetry.
#MonitoringBaselineConfig: close({
	// Keep the user-facing signal declaration and the collector projection
	// identical. A backend may be added or removed, but it can never cause the
	// node collector to observe a different signal set.
	signals: #MonitoringSignals
	let baselineSignals = signals
	collector: #OtelCollectorConfig & {
		signals: baselineSignals
	}
})

// #MonitoringBackends contains independently optional telemetry consumers.
// Each backend owns only its own configuration and is never a collector input.
// Backend endpoints and credentials are supplied at runtime by the owning
// adapter; this plan-level contract carries no secret material.
#MonitoringBackendOwner: close({
	module:  "monitoring-core"
	service: "otel-gateway" | "victoriametrics"
})

// #MonitoringGatewayBackend is the independent fan-in owner. It can receive
// OTLP only when explicitly selected and cannot change per-node collection.
#MonitoringGatewayBackend: close({
	owner: #MonitoringBackendOwner & {service: "otel-gateway"}
	config: #MonitoringGatewayConfig & {enabled: true}
})

// #MonitoringVictoriaMetricsBackend is the independent retention owner. It
// carries storage settings only; routing/collector changes remain with the
// separately selected gateway owner.
#MonitoringVictoriaMetricsBackend: close({
	owner: #MonitoringBackendOwner & {service: "victoriametrics"}
	config: #VictoriaMetricsConfig & {enabled: true}
})

#MonitoringBackends: close({
	gateway?:        #MonitoringGatewayBackend
	victoriametrics?: #MonitoringVictoriaMetricsBackend
})

// #MonitoringConfig is the canonical OTLP-first monitoring surface. Baseline
// collection is always complete on its own; optional backends are an additive,
// closed projection and cannot reconfigure the baseline collector.
#MonitoringConfig: close({
	enabled:          bool | *true
	profile:          #MonitoringProfile | *"single-node"
	budget:           #MonitoringProfileBudgets[profile]
	baseline:         #MonitoringBaselineConfig
	optionalBackends?: #MonitoringBackends
	optionalSignals?:  #MonitoringOptionalSignalLanes
	if baseline.signals.logs == false {
		optionalSignals?: logs?: _|_
	}
	if baseline.signals.traces == false {
		optionalSignals?: traces?: _|_
	}
	if baseline.signals.logs == true {
		optionalSignals?: logs?: {
			budget:        #MonitoringProfileBudgets[profile].logs
			retentionDays: <=#MonitoringProfileBudgets[profile].logs.maxRetentionDays
		}
	}
	if baseline.signals.traces == true {
		optionalSignals?: traces?: {
			budget: #MonitoringProfileBudgets[profile].traces
		}
	}
	if profile == "pi" {
		optionalBackends: close({})
	}
	if profile == "single-node" {
		optionalBackends?: {
			gateway?: config: {
				memoryLimitMiB:      <=budget.gateway.memoryMiB
				memorySpikeLimitMiB: <=budget.gateway.memoryMiB
			}
			victoriametrics?: config: retentionDays: <=budget.victoriametrics.maxRetentionDays
		}
	}
	if profile == "multi-node" {
		optionalBackends?: {
			gateway?: config: {
				memoryLimitMiB:      <=budget.gateway.memoryMiB
				memorySpikeLimitMiB: <=budget.gateway.memoryMiB
			}
			victoriametrics?: config: retentionDays: <=budget.victoriametrics.maxRetentionDays
		}
	}
})

// #AlertingConfig defines alerting settings
#AlertingConfig: {
	// Enable alerting
	enabled: bool | *false

	// Alerting backend
	backend: "alertmanager" | "pagerduty" | "opsgenie" | "webhook" | *"alertmanager"

	// Alert receivers
	receivers?: [...#AlertReceiver]

	// Alert rules
	rules?: [...#AlertRule]
}

// #NotificationChannelV1 defines typed alerting channels. Dedicated credential
// fields require opaque secret:// references (Golden Rules §4.5).
// Legacy backup channels retain their existing format until their consumers
// migrate; this schema does not establish runtime notification delivery.
#NotificationChannelV1: {
	// Channel name; defaults to the type so a single channel needs no name.
	name: string | *type

	type: "email" | "ntfy" | "gotify" | "slack" | "discord" | "telegram" | "webhook" | "pagerduty"

	// Email configuration
	email?: {
		to: [...string] & [_, ...]
		from?:     string
		smarthost: string
		password?: =~"^secret://"
	}

	// ntfy configuration (self-hosted or ntfy.sh topic)
	ntfy?: {
		serverUrl: string | *"https://ntfy.sh"
		topic:     string
		token?:    =~"^secret://"
		priority:  int & >=1 & <=5 | *3
	}

	// Gotify configuration
	gotify?: {
		url:      string
		token:    =~"^secret://"
		priority: int & >=1 & <=10 | *5
	}

	// Slack configuration
	slack?: {
		webhookUrl: =~"^secret://"
		channel:    string
		username:   string | *"AlertManager"
	}

	// Discord configuration
	discord?: {
		webhookUrl: =~"^secret://"
	}

	// Telegram configuration
	telegram?: {
		botToken: =~"^secret://"
		chatId:   string
	}

	// Webhook configuration
	webhook?: {
		url:    string
		method: "POST" | "PUT" | *"POST"
		headers?: [string]: string
		// Authorization material travels as a secret reference, never inline.
		authorization?: =~"^secret://"
	}

	// PagerDuty configuration
	pagerduty?: {
		routingKey: =~"^secret://"
	}

	// The type-specific block for the selected type is required.
	if type == "email" {email: _}
	if type == "ntfy" {ntfy: _}
	if type == "gotify" {gotify: _}
	if type == "slack" {slack: _}
	if type == "discord" {discord: _}
	if type == "telegram" {telegram: _}
	if type == "webhook" {webhook: _}
	if type == "pagerduty" {pagerduty: _}
}

// #AlertReceiver is an alert destination; it is the shared channel contract.
#AlertReceiver: #NotificationChannelV1

// #AlertRule defines an alerting rule
#AlertRule: {
	// Rule name
	name: string

	// Alert expression (PromQL)
	expr: string

	// Duration before firing
	for: string | *"5m"

	// Severity level
	severity: "critical" | "warning" | "info" | *"warning"

	// Alert labels
	labels?: [string]: string

	// Alert annotations
	annotations?: {
		summary?:     string
		description?: string
		runbook?:     string
	}
}

// #BackupEngine is the StackKits backup engine contract.
// Kopia is the active engine. "restic-import" is transitional and exists only
// to run the one-shot importer before switching the kit back to Kopia.
#BackupEngine: "kopia" | "restic-import"

// #BackupDataClass owns the legacy configuration/portable-archive vocabulary.
// The backup add-on aliases this table. Native ResolvedPlan instead uses the
// versioned #BackupDataClassV1/#BackupPolicyV1 contract in architecture_v2.cue.
// Migrating legacy names, cron strings or retention fields also requires their
// Go readers and existing archives to migrate; a schema-only switch is unsafe.
#BackupDataClass: "config" | "secrets" | "platform-state" | "database" | "user-content" | "documents" | "photos" | "large-media" | "telemetry-timeseries" | "serverless-config" | "cache-generated"

// #BackupDataClassPolicy is the resolved scheduling/restore contract for one
// class of state. It is generator-facing metadata, not a user knob.
#BackupDataClassPolicy: {
	class: #BackupDataClass

	defaultIncluded: bool
	schedule:        string
	restoreMode:     "file" | "database-hook" | "volume" | "reseed" | "exclude"
}

#BackupDataClassPolicyByClass: {
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

#BackupPolicyForClasses: {
	classes: [...#BackupDataClass]

	policies: [for class in classes {
		#BackupDataClassPolicyByClass[class]
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

// #BackupResilienceConfig describes recovery layers around the primary Kopia
// repository. The alternative path is intentionally portable and simple: it is
// not a second day-to-day backup engine, but a tool-independent emergency
// export for the state needed to rebuild a host or managed deployment.
#BackupResilienceConfig: {
	singleServer:      #SingleServerBackupSafety
	multiServer:       #MultiServerBackupSafety
	emergencyExport:   #BackupEmergencyExportConfig
	managedServerless: #ManagedServerlessRecoveryConfig
}

// #SingleServerBackupSafety captures the safer default posture for one-node
// StackKits: local repo, offsite leg, restore drill, and a portable export.
#SingleServerBackupSafety: {
	enabled: bool | *true

	// Number of recovery copies the operator should end up with: live data,
	// local backup repo, and offsite/export copy.
	minimumRecoveryCopies: int & >=2 & <=3 | *3

	requireLocalRepo:       bool | *true
	requireEmergencyExport: bool | *true
	requireRestoreDrill:    bool | *true
	requireOffHostCopy:     bool | *true
	recommendOffsite:       bool | *true
	recommendImmutableCopy: bool | *true
	kopiaIndependentFallback: "portable-archive" | *"portable-archive"
}

// #MultiServerBackupTopology classifies the recovery posture for clustered
// StackKits. A two-server setup is useful redundancy, but it is not release-
// ready HA because it cannot maintain quorum after one server disappears.
#MultiServerBackupTopology: "two-server" | "three-node-ha" | "five-node-ha" | "five-manager-ha" | "geo-redundant"

// #MultiServerBackupSafety captures release-readiness expectations for HA and
// fleet backup strategies: quorum-aware topology, offsite recovery, emergency
// export, shared-volume inventory, media policy, and backup workload shaping.
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

// #BackupEmergencyExportConfig is the Kopia-independent fallback. It contains
// plain manifests plus encrypted archives and database dumps that can be read
// with commodity tooling if the Kopia client or repo format is unavailable.
#BackupEmergencyExportConfig: {
	enabled: bool | *true

	mode:   *"portable-archive" | "provider-native-snapshot"
	// One portable implementation: standard tar/gzip encrypted with age.
	format: "tar.gz.age"

	schedule: string | *"0 3 * * 0"

	includeClasses: [...#BackupDataClass] | *[
		"config",
		"secrets",
		"platform-state",
		"database",
		"documents",
		"serverless-config",
	]

	// Large media is cost-sensitive. The default records manifests and paths
	// so an operator knows what must be restored from a NAS/object store, while
	// explicit opt-in can include the bytes.
	largeMediaMode: *"manifest-only" | "include" | "exclude"

	target: #BackupDestination | *{
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

// #ManagedServerlessRecoveryConfig is the server-independent recovery layer.
// It protects control-plane state and provider-native data handles so a
// deployment can be recreated without relying on the original customer node.
#ManagedServerlessRecoveryConfig: {
	enabled: bool | *false

	noServerDependency: true
	authority:          "control-plane"

	protectedClasses: [...#BackupDataClass] | *[
		"config",
		"secrets",
		"platform-state",
		"serverless-config",
	]

	controlPlaneSnapshot:  bool | *true
	providerNativeBackups: bool | *true
	requireProviderDataHandles: bool | *true
	requireRebuildIntent:       bool | *true
	portableManifest:      bool | *true
	preChangeSnapshot:     bool | *true

	schedule: string | *"pre-change + hourly metadata"
}

// #BackupConfig defines backup settings
#BackupConfig: {
	// Enable backups
	enabled: bool | *true

	// Backup engine. Kopia is the only active engine.
	engine: #BackupEngine | *"kopia"

	// Legacy alias retained for older YAML specs. If present, it must agree
	// with engine so specs cannot encode contradictory backup modes.
	backend?: engine

	// Backup schedule (cron format)
	schedule: string | *"0 2 * * *"

	// Data classes covered by this policy.
	dataClasses: [...#BackupDataClass] | *["config", "secrets", "platform-state", "database", "user-content", "documents", "photos", "telemetry-timeseries", "serverless-config"]

	// Internal, generator-facing policy view derived from dataClasses.
	_resolvedPolicy: #BackupPolicyForClasses & {
		classes: dataClasses
	}

	// Recovery layers around the primary Kopia repository.
	resilience: #BackupResilienceConfig

	// Backup retention
	retention: {
		daily:   int | *7
		weekly:  int | *4
		monthly: int | *6
		yearly:  int | *1
	}

	// Backup destinations
	destinations: [...#BackupDestination] | *[]

	// Paths to backup
	paths: [...string] | *["/opt/stacks", "/var/lib/docker/volumes"]

	// Paths to exclude
	excludes: [...string] | *["*.tmp", "*.log", "cache/"]

	// Pre-backup hooks. User specs should avoid these; the backup add-on
	// derives database consistency hooks from module metadata.
	preHooks: [...string] | *[]

	// Post-backup hooks. User specs should avoid these for the same reason.
	postHooks: [...string] | *[]

	// Encryption key used by Kopia. This must be a secret reference.
	encryptionKey?: =~"^secret://"
}

// #BackupDestination defines a backup target.
// Single canonical definition. Shape mirrors the YAML spec contract in
// pkg/models/models.go (BackupDestinationSpec): flat, camelCase, per-type
// fields.
#BackupDestination: {
	// Destination name
	name: string

	// Destination type
	type: "local" | "s3" | "b2" | "sftp"

	// Local path (if type = local)
	path?: string

	// S3 configuration (if type = s3)
	s3Bucket?:   string
	s3Endpoint?: string // Custom S3 endpoint (MinIO, Wasabi, etc.)
	s3Region?:   string
	// Inline credentials MUST be secret references (Golden Rules §4.5)
	s3AccessKey?: =~"^secret://"
	s3SecretKey?: =~"^secret://"

	// B2 configuration (if type = b2)
	b2Bucket?:         string
	b2KeyId?:          =~"^secret://"
	b2ApplicationKey?: =~"^secret://"

	// SFTP configuration (if type = sftp)
	sftpHost?:     string
	sftpPort?:     uint16
	sftpUser?:     string
	sftpPassword?: =~"^secret://"
	sftpKeyPath?:  string
	sftpPath?:     string
}
