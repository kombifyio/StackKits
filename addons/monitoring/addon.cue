// Package monitoring - Monitoring Add-On
//
// Full observability stack for multi-node homelab:
//   - VictoriaMetrics: Drop-in Prometheus replacement (lower resource usage)
//   - Grafana: Dashboards and visualization
//   - Loki: Log aggregation
//   - Grafana Alloy: Unified telemetry agent (replaces Promtail + node-exporter)
//
// License:
//   - VictoriaMetrics: Apache-2.0
//   - Grafana: AGPL-3.0
//   - Loki: AGPL-3.0
//   - Grafana Alloy: Apache-2.0
//
// Placement:
//   - VictoriaMetrics, Grafana, Loki: Cloud node
//   - Alloy, cAdvisor, node-exporter: All nodes (daemonset)
//
// Usage:
//   addons: monitoring: monitoring.#Config & {
//       victoriametrics: retention: "30d"
//   }

package monitoring

// #Config defines monitoring add-on configuration
#Config: {
	_addon: {
		name:        "monitoring"
		displayName: "Monitoring Stack"
		version:     "1.0.0"
		layer:       "OBSERVABILITY"
		description: "VictoriaMetrics + Grafana + Loki + Alloy"
	}

	_compatibility: {
		stackkits: ["base-kit", "dev-homelab", "modern-homelab", "ha-kit"]
		contexts:  ["local", "cloud"]
		requires:  []
		conflicts: []
	}

	enabled: bool | *true

	// VictoriaMetrics configuration
	victoriametrics: #VictoriaMetricsConfig

	// Grafana configuration
	grafana: #GrafanaConfig

	// Loki configuration
	loki: #LokiConfig

	// Alloy configuration (unified agent)
	alloy: #AlloyConfig
}

// #VictoriaMetricsConfig - Time-series database (Prometheus replacement)
#VictoriaMetricsConfig: {
	enabled: bool | *true

	// Data retention period
	retention: string | *"30d"

	// Scrape interval
	scrapeInterval: string | *"15s"

	// Deduplication for HA setups
	dedup: {
		enabled: bool | *false
	}

	// Resource limits
	resources: {
		memory: string | *"512m"
		cpus:   number | *1.0
	}
}

// #GrafanaConfig - Dashboards
#GrafanaConfig: {
	enabled: bool | *true

	// Anonymous access
	anonymousAccess: bool | *false

	// Pre-installed plugins
	plugins: [...string] | *[
		"grafana-piechart-panel",
		"grafana-clock-panel",
	]

	// Pre-configured dashboards
	dashboards: {
		nodeExporter: bool | *true
		docker:       bool | *true
		traefik:      bool | *true
	}

	// Datasources
	datasources: {
		victoriametrics: bool | *true
		loki:            bool | *true
	}

	// Resource limits
	resources: {
		memory: string | *"512m"
		cpus:   number | *1.0
	}
}

// #LokiConfig - Log aggregation
#LokiConfig: {
	enabled: bool | *true

	// Log retention
	retention: string | *"168h"

	// Ingestion limits
	ingestionRateLimit: string | *"4MB"
	ingestionBurstSize: string | *"6MB"

	// Query limits
	maxQueryLookback: string | *"168h"

	// Resource limits
	resources: {
		memory: string | *"512m"
		cpus:   number | *1.0
	}
}

// #AlloyConfig - Unified telemetry agent (replaces Promtail + node-exporter)
#AlloyConfig: {
	enabled: bool | *true

	// Collect logs (replaces Promtail)
	collectLogs: bool | *true

	// Collect metrics (replaces node-exporter scraping)
	collectMetrics: bool | *true

	// Collect traces (optional, for application tracing)
	collectTraces: bool | *false

	// Resource limits (per-node agent)
	resources: {
		memory: string | *"128m"
		cpus:   number | *0.25
	}
}

// Service definitions

#VictoriaMetricsService: {
	name:        "victoriametrics"
	displayName: "VictoriaMetrics"
	image:       "victoriametrics/victoria-metrics:v1.106.1"
	category:    "monitoring"

	placement: {
		nodeType: "cloud"
		strategy: "single"
	}

	ports: [
		{container: 8428, host: 8428, protocol: "tcp", name: "http"},
	]

	volumes: [
		{name: "vm-data", path: "/storage", type: "volume"},
	]

	traefik: {
		enabled: true
		rule:    "Host(`vm.{{.domain}}`)"
	}
}

#GrafanaService: {
	name:        "grafana"
	displayName: "Grafana"
	image:       "grafana/grafana:11.4"
	category:    "monitoring"

	placement: {
		nodeType: "cloud"
		strategy: "single"
	}

	ports: [
		{container: 3000, host: 3004, protocol: "tcp", name: "web"},
	]

	volumes: [
		{name: "grafana-data", path: "/var/lib/grafana", type: "volume"},
	]

	traefik: {
		enabled: true
		rule:    "Host(`grafana.{{.domain}}`)"
	}
}

#LokiService: {
	name:        "loki"
	displayName: "Loki"
	image:       "grafana/loki:3.3"
	category:    "monitoring"

	placement: {
		nodeType: "cloud"
		strategy: "single"
	}

	ports: [
		{container: 3100, host: 3100, protocol: "tcp", name: "http"},
	]

	volumes: [
		{name: "loki-data", path: "/loki", type: "volume"},
	]
}

#AlloyService: {
	name:        "grafana-alloy"
	displayName: "Grafana Alloy"
	image:       "grafana/alloy:v1.5"
	category:    "monitoring"

	placement: {
		nodeType: "all"
		strategy: "daemonset"
	}

	ports: [
		{container: 12345, host: 12345, protocol: "tcp", name: "http"},
	]

	volumes: [
		{name: "alloy-data", path: "/var/lib/alloy", type: "volume"},
		{host: "/var/log", path: "/var/log", type: "bind", readOnly: true},
		{host: "/var/run/docker.sock", path: "/var/run/docker.sock", type: "bind", readOnly: true},
	]
}

#CadvisorService: {
	name:        "cadvisor"
	displayName: "cAdvisor"
	image:       "gcr.io/cadvisor/cadvisor:v0.49.1"
	category:    "monitoring"

	placement: {
		nodeType: "all"
		strategy: "daemonset"
	}

	ports: [
		{container: 8080, host: 8082, protocol: "tcp", name: "http"},
	]

	volumes: [
		{host: "/", path: "/rootfs", type: "bind", readOnly: true},
		{host: "/var/run", path: "/var/run", type: "bind", readOnly: true},
		{host: "/sys", path: "/sys", type: "bind", readOnly: true},
		{host: "/var/lib/docker", path: "/var/lib/docker", type: "bind", readOnly: true},
	]
}

#NodeExporterService: {
	name:        "node-exporter"
	displayName: "Node Exporter"
	image:       "prom/node-exporter:v1.8.2"
	category:    "monitoring"

	placement: {
		nodeType: "all"
		strategy: "daemonset"
	}

	ports: [
		{container: 9100, host: 9100, protocol: "tcp", name: "metrics"},
	]

	volumes: [
		{host: "/proc", path: "/host/proc", type: "bind", readOnly: true},
		{host: "/sys", path: "/host/sys", type: "bind", readOnly: true},
		{host: "/", path: "/rootfs", type: "bind", readOnly: true},
	]
}

// #Outputs defines what this add-on exports
#Outputs: {
	metricsUrl:   string | *"http://victoriametrics:8428"
	grafanaUrl:   string | *"https://grafana.{{.domain}}"
	lokiUrl:      string | *"http://loki:3100"
	alloyEnabled: bool | *true
}
