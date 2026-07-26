package architecturev2

// productMonitoringAgentSelector is the exact remote-only Product Runtime
// seam for the node-local collector intent. The selector grants no endpoint,
// credential, daemon, provider, or local Operations authority.
func productMonitoringAgentSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-monitoring-agent-runtime",
		ProviderRef: "stackkits-monitoring-agent", ModuleRef: "stackkits-monitoring-agent-runtime", UnitRef: "collector-intent",
		RuntimeKind: "native", RuntimeDelivery: "stackkit",
	}
}
