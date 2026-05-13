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
