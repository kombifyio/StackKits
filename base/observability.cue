// Package base - Observability configuration schemas
package base

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
		url:      string
		tenant?:  string
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

// #MonitoringSignalsConfig defines which OTLP lanes are active for a StackKit.
// Metrics are the default baseline. Logs and traces remain opt-in lanes.
#MonitoringSignalsConfig: {
	metrics: bool | *true
	logs:    bool | *false
	traces:  bool | *false
}

// #MonitoringGatewayConfig configures the optional collector fan-in gateway
// used by monitoring-core when a StackKit wants central aggregation.
#MonitoringGatewayConfig: {
	enabled: bool | *false
	memoryLimitMiB: int & >=128 & <=1024 | *256
	batchTimeout: string | *"15s"
	maxConcurrentStreams: int & >=10 & <=200 | *50
	remoteWriteEndpoint: string | *"http://victoriametrics:8428/api/v1/write"
}

// #MonitoringProfile controls how much data is collected per node.
// Adjust based on hardware tier:
//   standard — Pi 4B/5, x86 servers (default)
//   full     — Servers with ample RAM; adds per-process and extended traces
#MonitoringProfile: "standard" | "full"

// #OtelCollectorConfig configures the OpenTelemetry Collector agent deployed
// on every monitored node. This is the standard monitoring agent for all
// hardware tiers from Pi 4B upward.
#OtelCollectorConfig: {
	// Enable OTel Collector agent
	enabled: bool | *true

	// Monitoring profile (controls collection intervals and active scrapers)
	profile: #MonitoringProfile | *"standard"

	// OTLP/gRPC endpoint of the receiver (TechStack core or monitoring-core gateway)
	// Default points to TechStack on the same host or a named Docker service.
	endpoint: string | *"techstack:4317"

	// TLS for the OTLP export connection
	tls: {
		insecure: bool | *true
		// Path to CA cert (if insecure=false)
		caFile?: string
	}

	// Memory limit for the OTel Collector process.
	// Pi 4B (4 GB): 256 MB is comfortable.
	// Pi 5 (8 GB): can raise to 512 MB.
	memoryLimitMiB: int & >=64 & <=1024 | *256

	// Collection intervals per profile.
	// standard: 30s — good balance for Pi 4B.
	// full: 10s — for servers where granularity matters.
	collectionInterval: {
		"standard": "30s"
		"full":     "10s"
	}[profile]

	// Push batch timeout — groups samples before sending to reduce network calls.
	batchTimeout: {
		"standard": "30s"
		"full":     "15s"
	}[profile]

	// Which metric scrapers are active per profile.
	scrapers: {
		cpu:        true
		memory:     true
		disk:       true
		filesystem: true
		network:    true
		load:       true
		// paging (swap) — less relevant on Pi, enabled on full
		paging: profile == "full"
		// per-process metrics — expensive, only on full
		process: profile == "full"
	}

	// Docker stats receiver — collects per-container CPU, RAM, net, block I/O.
	// Requires /var/run/docker.sock mounted.
	dockerStats: {
		enabled:  bool | *true
		endpoint: string | *"unix:///var/run/docker.sock"
	}

	// hwmon (hardware temperature sensors) — always enabled.
	// Critical for Pi in enclosures to detect thermal throttling.
	hwmon: {
		enabled: bool | *true
	}
}

// #MonitoringConfig is the canonical StackKits monitoring surface.
// The collector baseline lives here; gateway and backend settings remain
// optional extensions without changing the default OTLP contract.
#MonitoringConfig: {
	enabled: bool | *true
	profile: #MonitoringProfile | *"standard"
	signals: #MonitoringSignalsConfig
	collector: #OtelCollectorConfig & {
		profile: profile
	}
	gateway: #MonitoringGatewayConfig
	backend: #VictoriaMetricsConfig
}

// #ObservabilityConfig is the canonical shared observability surface for all
// StackKits. New monitoring work should extend observability.monitoring rather
// than adding bespoke top-level metrics backends.
#ObservabilityConfig: {
	logging:    #LoggingConfig
	health:     #HealthCheck
	monitoring: #MonitoringConfig
	alerting:   #AlertingConfig
	backup:     #BackupConfig

	// Legacy compatibility surface. Keep during the migration so older specs can
	// still describe generic metrics settings while the canonical path moves to
	// observability.monitoring.
	metrics?: #MetricsConfig
}

// #VictoriaMetricsConfig configures a VictoriaMetrics single-node instance
// as an optional add-on backend. When enabled, all agents remote-write here
// instead of (or in addition to) TechStack's embedded TSDB.
// Requires approx. 300–600 MB RAM on the host node.
#VictoriaMetricsConfig: {
	// Enable VictoriaMetrics backend
	enabled: bool | *false

	// Data retention period
	retentionPeriod: string | *"30d"

	// Data directory
	dataDir: string | *"/var/lib/victoriametrics"

	// Memory cap as percentage of available host RAM (VM default: 60%)
	memoryAllowedPercent: int & >=20 & <=80 | *40

	// Expose PromQL HTTP API on this port (also accepts remote_write + OTLP)
	port: uint16 | *8428

	// Maximum concurrent read queries
	maxConcurrentRequests: int | *4

	// Enable built-in vmagent to scrape additional targets (e.g. node_exporter)
	vmagent?: {
		enabled: bool | *false
		// Additional scrape targets (beyond what agents push)
		scrapeTargets?: [...string]
	}

	// Enable Grafana sidecar for dashboards
	grafana?: {
		enabled: bool | *false
		port:    uint16 | *3000
		// Admin password ref
		adminPassword?: =~"^secret://"
	}
}

// #MetricsConfig defines legacy generic metrics settings.
// New StackKits monitoring work should use #MonitoringConfig instead.
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

// #AlertReceiver defines an alert destination
#AlertReceiver: {
	// Receiver name
	name: string

	// Receiver type
	type: "email" | "slack" | "discord" | "telegram" | "webhook" | "pagerduty"

	// Email configuration
	email?: {
		to:       [...string]
		from?:    string
		smarthost: string
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
		url:     string
		method:  "POST" | "PUT" | *"POST"
		headers?: [string]: string
	}
}

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

// #BackupConfig defines backup settings
#BackupConfig: {
	// Enable backups
	enabled: bool | *true

	// Backup backend
	backend: "restic" | "borgbackup" | "rclone" | "rsync" | *"restic"

	// Backup schedule (cron format)
	schedule: string | *"0 3 * * *"

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

	// Pre-backup hooks
	preHooks: [...string] | *[]

	// Post-backup hooks
	postHooks: [...string] | *[]

	// Encryption key (for restic/borg)
	encryptionKey?: =~"^secret://"
}

// #BackupDestination defines a backup target
#BackupDestination: {
	// Destination name
	name: string

	// Destination type
	type: "local" | "s3" | "b2" | "sftp" | "rclone"

	// Local path (if type = local)
	path?: string

	// S3 configuration (if type = s3)
	s3?: {
		bucket:   string
		endpoint?: string
		region:   string | *"us-east-1"
		accessKey: =~"^secret://"
		secretKey: =~"^secret://"
	}

	// B2 configuration (if type = b2)
	b2?: {
		bucket:     string
		keyId:      =~"^secret://"
		applicationKey: =~"^secret://"
	}

	// SFTP configuration (if type = sftp)
	sftp?: {
		host:     string
		port:     uint16 | *22
		user:     string
		password?: =~"^secret://"
		keyPath?: string
		path:     string
	}

	// rclone remote name (if type = rclone)
	rcloneRemote?: string
}
