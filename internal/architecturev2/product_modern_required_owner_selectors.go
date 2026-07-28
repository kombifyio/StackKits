package architecturev2

func productModernFederationPolicySelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-modern-federation-policy-manifest",
		ProviderRef: "stackkits-modern-federation-policy",
		ModuleRef:   "stackkits-modern-federation-policy-manifest", UnitRef: "policy-bundle",
		RuntimeKind: "native", RuntimeDelivery: "stackkit",
	}
}

func productFederationBackupSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-federation-backup-runtime",
		ProviderRef: "stackkits-federation-backup",
		ModuleRef:   "stackkits-federation-backup-runtime", UnitRef: "executor-contract",
		RuntimeKind: "host", RuntimeDelivery: "stackkit",
	}
}

func productFederationObservabilitySelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-federation-observability-runtime",
		ProviderRef: "stackkits-federation-observability",
		ModuleRef:   "stackkits-federation-observability-runtime", UnitRef: "executor-contract",
		RuntimeKind: "host", RuntimeDelivery: "stackkit",
	}
}

func productHomePrivateRemoteAccessSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-home-private-remote-access-runtime",
		ProviderRef: "stackkits-home-private-remote-access",
		ModuleRef:   "stackkits-home-private-remote-access-runtime", UnitRef: "executor-contract",
		RuntimeKind: "host", RuntimeDelivery: "stackkit",
	}
}
