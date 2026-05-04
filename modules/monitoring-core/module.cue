// Package monitoringcore — VictoriaMetrics central monitoring backend (optional add-on).
//
// Deploys VictoriaMetrics single-node as a long-term metrics store.
// When enabled, monitoring-agent instances should point their OTLP endpoint
// at this module's gateway service instead of TechStack directly.
//
// Architecture:
//   monitoring-agent nodes  →  OTLP/gRPC (4317)  →  otel-gateway
//   otel-gateway            →  Remote Write       →  VictoriaMetrics
//   TechStack               →  PromQL HTTP        →  VictoriaMetrics
//   Grafana (optional)      →  PromQL HTTP        →  VictoriaMetrics
//
// Resource requirements on the host:
//   VictoriaMetrics:  ~300–600 MB RAM (tuned to 40% of host RAM)
//   otel-gateway:     ~150 MB RAM
//   Grafana (opt):    ~200 MB RAM
//   Total:            ~450–950 MB RAM
//
// Requires Pi 4B (4 GB) or better as the designated central monitoring node.
package monitoringcore

import "github.com/kombifyio/stackkits/base"

Contract: base.#ModuleContract & {
	metadata: {
		name:        "monitoring-core"
		displayName: "Monitoring Core (VictoriaMetrics)"
		version:     "1.0.0"
		layer:       "L2-platform-ingress"
		description: "Optional VictoriaMetrics backend with OTel gateway — extends standard monitoring-agent with long-term storage and Grafana dashboards"
	}

	requires: {
		services: {
			traefik: {
				minVersion: "3.0"
				provides: ["reverse-proxy"]
				optional: true
			}
		}
		infrastructure: {
			docker:            true
			persistentStorage: true
			network:           "shared"
		}
	}

	provides: {
		capabilities: {
			"metrics-storage":  true
			"promql-api":        true
			"otlp-gateway":      true
			"long-term-metrics": true
			"grafana":           true
		}
		endpoints: {
			victoriametrics: {
				url:         "http://victoriametrics.{{.domain}}:8428"
				description: "VictoriaMetrics PromQL API + remote_write + OTLP ingestion"
			}
			grafana: {
				url:         "https://grafana.{{.domain}}"
				description: "Grafana dashboards (optional)"
			}
		}
	}

	settings: {
		flexible: {
			backend: base.#VictoriaMetricsConfig & {
				enabled: true
			}
			gateway: base.#MonitoringGatewayConfig & {
				enabled: true
			}
		}
	}

	contexts: {
		local: {}
		cloud: {}
		pi:    {}
	}

	// OTel gateway — receives OTLP/gRPC from all monitoring-agent nodes,
	// fans out to VictoriaMetrics via Remote Write.
	services: "otel-gateway": base.#ServiceDefinition & {
		name:     "otel-gateway"
		type:     "observability"
		image:    "otel/opentelemetry-collector-contrib"
		tag:      "0.114.0"
		required: true
		status:   "implemented"
		needs: ["victoriametrics"]

		placement: {
			nodeType: "all"
			strategy: "single"
		}

		network: {
			networks: ["base_net"]
		}

		volumes: [{
			source:      "./monitoring-core/gateway-config.yaml"
			target:      "/etc/otelcol/config.yaml"
			type:        "bind"
			readOnly:    true
			description: "OTel gateway configuration"
		}]

		environment: {
			GOMEMLIMIT:                               "{{.monitoring_core_gateway_gomemlimit}}"
			KOMBIFY_OTEL_GATEWAY_BATCH_TIMEOUT:       "{{.monitoring_core_gateway_batch_timeout}}"
			KOMBIFY_OTEL_GATEWAY_MAX_CONCURRENT_STREAMS: "{{.monitoring_core_gateway_max_concurrent_streams}}"
			KOMBIFY_OTEL_GATEWAY_MEMORY_LIMIT_MIB:    "{{.monitoring_core_gateway_memory_limit_mib}}"
			KOMBIFY_OTEL_GATEWAY_MEMORY_SPIKE_LIMIT_MIB: "{{.monitoring_core_gateway_memory_spike_limit_mib}}"
			KOMBIFY_OTEL_GATEWAY_REMOTE_WRITE_ENDPOINT:  "{{.monitoring_core_gateway_remote_write_endpoint}}"
		}

		healthCheck: {
			enabled: true
			http: {
				path:   "/metrics"
				port:   8888
				scheme: "http"
			}
			interval:    "30s"
			timeout:     "10s"
			retries:     3
			startPeriod: "15s"
		}

		resources: {
			memory:    "256m"
			memoryMax: "512m"
			cpus:      0.5
		}

		security: {
			noNewPrivileges: true
			capDrop: ["ALL"]
		}

		output: {
			description: "OTel Collector gateway — aggregates OTLP from all agents and writes to VictoriaMetrics"
		}
	}

	// VictoriaMetrics single-node — central long-term TSDB.
	services: "victoriametrics": base.#ServiceDefinition & {
		name:     "victoriametrics"
		type:     "database"
		image:    "victoriametrics/victoria-metrics"
		tag:      "v1.139.0"
		required: true
		status:   "implemented"

		placement: {
			nodeType: "all"
			strategy: "single"
		}

		network: {
			traefik: {
				enabled: false // Internal only — not exposed via Traefik by default
			}
			networks: ["base_net"]
		}

		volumes: [{
			source:      "victoriametrics-data"
			target:      "/victoria-metrics-data"
			type:        "volume"
			backup:      true
			description: "VictoriaMetrics time series data"
		}]

		command: [
			"-storageDataPath=/victoria-metrics-data",
			"-retentionPeriod={{.monitoring_core_vm_retention_period}}",
			"-memory.allowedPercent={{.monitoring_core_vm_memory_allowed_percent}}",
			"-maxConcurrentInserts={{.monitoring_core_vm_max_concurrent_requests}}",
			"-search.maxConcurrentRequests={{.monitoring_core_vm_max_concurrent_requests}}",
			"-search.maxQueryDuration=30s",
			"-enableTCP6=false",
		]

		environment: {
			// VM CLI flags passed as environment is not the standard VM pattern;
			// flags are passed via Docker command instead (see reference-compose.yml)
		}

		healthCheck: {
			enabled: true
			http: {
				path:   "/health"
				port:   8428
				scheme: "http"
			}
			interval:    "30s"
			timeout:     "10s"
			retries:     3
			startPeriod: "20s"
		}

		resources: {
			// Tuned for Pi 4B (4 GB): VM caps itself to 40% of host RAM via flag
			memory:    "512m"
			memoryMax: "1g"
			cpus:      1.0
		}

		security: {
			noNewPrivileges: true
			capDrop: ["ALL"]
		}

		output: {
			url:         "http://victoriametrics:8428"
			description: "VictoriaMetrics PromQL API — query metrics from all nodes"
		}
	}
}
