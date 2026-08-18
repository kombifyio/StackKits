package foundation

// The baseline must be concrete and valid without any telemetry backend.
_monitoringBaselineOnlyCheck: #MonitoringConfig & {
	baseline: {
		signals: {metrics: true, logs: false, traces: false}
		collector: {
			endpoint: "techstack:4317"
			protocol: "grpc"
			auth: {mode: "none"}
			tls:  {insecure: true}
		}
	}
}

// Adding a backend is an independent, closed object; it cannot supply or
// replace collector fields. The concrete baseline intentionally remains equal
// to the backend-free check above.
_monitoringOptionalBackendCheck: #MonitoringConfig & {
	baseline: _monitoringBaselineOnlyCheck.baseline
	optionalBackends: {
		victoriametrics: {
			owner:  {module: "monitoring-core", service: "victoriametrics"}
			config: {enabled: true}
		}
		gateway: {
			owner:  {module: "monitoring-core", service: "otel-gateway"}
			config: {enabled: true}
		}
	}
}

// The backend projection is additive: adding it retains the exact resolved
// baseline, including the collector's signal selection.
_assertMonitoringBackendDoesNotMutateCollector: _monitoringBaselineOnlyCheck.baseline.collector & _monitoringOptionalBackendCheck.baseline.collector
_assertMonitoringBaselineSignalsProjectToCollector: _monitoringBaselineOnlyCheck.baseline.signals & _monitoringBaselineOnlyCheck.baseline.collector.signals
