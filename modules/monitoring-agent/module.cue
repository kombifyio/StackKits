// Package monitoringagent — OpenTelemetry Collector monitoring agent module.
//
// Deploys an OTel Collector in agent mode on the node. Collects host and
// container metrics via hostmetrics + dockerstats receivers and pushes them
// via OTLP/gRPC to TechStack (or a monitoring-core gateway when present).
//
// This is the standard monitoring agent for all supported hardware tiers
// (Pi 4B, Pi 5, x86 servers). Requires 4 GB+ RAM on the host.
//
// Profiles:
//   standard — 30s interval, host + container metrics, ~150 MB RAM (default)
//   full     — 10s interval, adds per-process metrics, ~250 MB RAM
package monitoringagent

import "github.com/kombifyio/stackkits/base"

// Contract declares what this module requires and provides.
Contract: base.#ModuleContract & {
	metadata: {
		name:        "monitoring-agent"
		displayName: "Monitoring Agent (OTel)"
		version:     "1.0.0"
		layer:       "L2-platform-ingress"
		description: "OpenTelemetry Collector agent — collects host and container metrics and pushes via OTLP/gRPC to TechStack or monitoring-core"
		maturity:    "opt-in"
		testScenarios: ["SK-S1", "SK-S4"]
	}

	requires: {
		infrastructure: {
			docker:            true
			persistentStorage: false
			network:           "shared"
		}
	}

	provides: {
		capabilities: {
			"metrics-collection": true
			"otlp-agent":         true
			"host-metrics":       true
			"container-metrics":  true
		}
	}

	settings: {
		flexible: {
			// Canonical monitoring-agent contract.
			// Consumers should use this collector object rather than bespoke flat fields.
			collector: base.#OtelCollectorConfig
		}
	}

	contexts: {
		local: {}
		cloud: {}
		pi: {}
	}

	services: "otel-collector": base.#ServiceDefinition & {
		name:     "otel-collector"
		type:     "observability"
		image:    "otel/opentelemetry-collector-contrib"
		tag:      "0.114.0"
		upstream: {
			github: {repo: "open-telemetry/opentelemetry-collector-contrib"}
		}
		required: false
		status:   "implemented"

		placement: {
			nodeType: "all"
			strategy: "single"
		}

		network: {
			// No Traefik exposure — agent only pushes outbound.
			networks: ["base_net"]
		}

		volumes: [
			{
				// Read-only Docker socket for dockerstats receiver
				source:      "/var/run/docker.sock"
				target:      "/var/run/docker.sock"
				type:        "bind"
				readOnly:    true
				description: "Docker socket for container metrics collection"
			},
			{
				// Host filesystem mount for hostmetrics (disk/fs collectors)
				source:      "/"
				target:      "/hostfs"
				type:        "bind"
				readOnly:    true
				description: "Host filesystem root for disk and filesystem metrics"
			},
			{
				// OTel Collector config (generated from this module's template)
				source:      "./monitoring-agent/otelcol-config.yaml"
				target:      "/etc/otelcol/config.yaml"
				type:        "bind"
				readOnly:    true
				description: "OTel Collector configuration"
			},
		]

		environment: {
			// Hard memory cap passed to the Go runtime alongside memory_limiter.
			// Keeps GC pressure low on constrained devices.
			GOMEMLIMIT:                          "{{.monitoring_agent_gomemlimit}}"
			OTEL_ENDPOINT:                       "{{.monitoring_agent_otlp_endpoint}}"
			KOMBIFY_OTEL_COLLECTION_INTERVAL:    "{{.monitoring_agent_collection_interval}}"
			KOMBIFY_OTEL_BATCH_TIMEOUT:          "{{.monitoring_agent_batch_timeout}}"
			KOMBIFY_OTEL_DOCKER_ENDPOINT:        "{{.monitoring_agent_docker_endpoint}}"
			KOMBIFY_OTEL_MEMORY_LIMIT_MIB:       "{{.monitoring_agent_memory_limit_mib}}"
			KOMBIFY_OTEL_MEMORY_SPIKE_LIMIT_MIB: "{{.monitoring_agent_memory_spike_limit_mib}}"
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
			// Pi 4B headroom: 256 MB soft, 512 MB hard max (spike absorption)
			memory:    "256m"
			memoryMax: "512m"
			cpus:      0.25
		}

		security: {
			// hostmetrics + dockerstats require no elevated privileges
			noNewPrivileges: true
			capDrop: ["ALL"]
		}

		output: {
			description: "OTel Collector agent — pushing host+container metrics via OTLP/gRPC"
		}
	}
}
