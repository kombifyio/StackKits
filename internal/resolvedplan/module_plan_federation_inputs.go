package resolvedplan

import (
	"fmt"
)

func safeModuleFederationPolicy(moduleID string, source modulePlanInputSource) (map[string]any, error) {
	if moduleID != "stackkits-modern-federation-policy-manifest" {
		return nil, fmt.Errorf("module %q cannot consume the Modern federation policy projection", moduleID)
	}
	bridge, err := requireFederationSource(source)
	if err != nil {
		return nil, err
	}
	overlay, err := objectField(bridge, "bridge", "overlay")
	if err != nil {
		return nil, err
	}
	safeOverlay, err := selectObjectFields(overlay, "bridge.overlay", []string{
		"contractRef", "implementation", "initiation", "outboundEstablished", "trafficMode",
		"advertisePrivateSubnets", "advertiseDefaultRoute", "allowBroadRoutes", "peerSiteRefs",
	})
	if err != nil {
		return nil, err
	}
	policy, err := objectField(bridge, "bridge", "policy")
	if err != nil {
		return nil, err
	}
	safePolicy, err := selectObjectFields(policy, "bridge.policy", []string{
		"defaultDeny", "allowedFlows", "allowRFC1918Transit", "cloudMayEnrollDevices", "cloudMayIssueDeviceCredentials",
	})
	if err != nil {
		return nil, err
	}
	publications, err := objectListField(bridge, "bridge", "publications")
	if err != nil {
		return nil, err
	}
	safePublications, err := selectFederationPublicationFields(publications)
	if err != nil {
		return nil, err
	}
	dataPolicy, err := safeFederationDataPolicy(source.data)
	if err != nil {
		return nil, err
	}
	partition, err := safeFederationPartitionPolicy(source.failurePolicy)
	if err != nil {
		return nil, err
	}
	return normalizedObject(map[string]any{
		"overlay": safeOverlay, "publications": safePublications, "policy": safePolicy,
		"dataAuthority": dataPolicy, "partition": partition,
	}, "federationPolicy")
}

func safeModuleFederationLinkPolicy(moduleID string, source modulePlanInputSource) (map[string]any, error) {
	if moduleID != "stackkits-federation-link-runtime" {
		return nil, fmt.Errorf("module %q cannot consume the Federation link policy projection", moduleID)
	}
	bridge, err := requireFederationSource(source)
	if err != nil {
		return nil, err
	}
	overlay, err := objectField(bridge, "bridge", "overlay")
	if err != nil {
		return nil, err
	}
	safeOverlay, err := selectObjectFields(overlay, "bridge.overlay", []string{
		"contractRef", "implementation", "initiation", "outboundEstablished", "trafficMode",
		"advertisePrivateSubnets", "advertiseDefaultRoute", "allowBroadRoutes", "peerSiteRefs",
	})
	if err != nil {
		return nil, err
	}
	partition, err := safeFederationPartitionPolicy(source.failurePolicy)
	if err != nil {
		return nil, err
	}
	return normalizedObject(map[string]any{"overlay": safeOverlay, "partition": partition}, "federationLinkPolicy")
}

func safeModuleFederationControlActions(moduleID string, source modulePlanInputSource) (map[string]any, error) {
	if moduleID != "stackkits-federation-control-agent-runtime" {
		return nil, fmt.Errorf("module %q cannot consume Federation control actions", moduleID)
	}
	bridge, err := requireFederationSource(source)
	if err != nil {
		return nil, err
	}
	agent, err := objectField(bridge, "bridge", "controlAgent")
	if err != nil {
		return nil, err
	}
	enabled, err := boolFieldDefault(agent, "bridge.controlAgent", "enabled", false)
	if err != nil {
		return nil, err
	}
	moduleRef, err := stringField(agent, "bridge.controlAgent", "moduleRef")
	if err != nil {
		return nil, err
	}
	contractHash, err := stringField(agent, "bridge.controlAgent", "providerContractHash")
	if err != nil {
		return nil, err
	}
	allowlist, err := stringListField(agent, "bridge.controlAgent", "actionAllowlist", true)
	if err != nil {
		return nil, err
	}
	actions, err := objectListField(agent, "bridge.controlAgent", "actions")
	if err != nil {
		return nil, err
	}
	projected := make([]any, 0, len(actions))
	for index, action := range actions {
		path := fmt.Sprintf("bridge.controlAgent.actions[%d]", index)
		safeAction, err := selectObjectFields(action, path, []string{
			"id", "contractRef", "capabilityRef", "moduleRef", "transport", "issuerRef", "audience", "maxTTLSeconds",
			"destructive", "approvalClass", "approvalReceiptRequired", "requiresSignedActions", "requiresNonce",
			"requiresResolvedPlanHash", "requiresIdempotencyKey", "capabilityScopedActions", "replayProtection",
			"requiresApprovalForDestructive",
		})
		if err != nil {
			return nil, err
		}
		projected = append(projected, safeAction)
	}
	partition, err := safeFederationPartitionPolicy(source.failurePolicy)
	if err != nil {
		return nil, err
	}
	return normalizedObject(map[string]any{
		"enabled": enabled, "moduleRef": moduleRef, "contractHash": contractHash,
		"actionAllowlist": stringSliceAny(allowlist), "actions": projected, "partition": partition,
	}, "federationControlActions")
}

func safeModuleFederationBackupPolicy(moduleID string, source modulePlanInputSource) (map[string]any, error) {
	if moduleID != "stackkits-federation-backup-runtime" {
		return nil, fmt.Errorf("module %q cannot consume the Federation backup policy projection", moduleID)
	}
	dataPolicy, err := safeFederationDataPolicy(source.data)
	if err != nil {
		return nil, err
	}
	partition, err := safeFederationPartitionPolicy(source.failurePolicy)
	if err != nil {
		return nil, err
	}
	return normalizedObject(map[string]any{"dataAuthority": dataPolicy, "partition": partition}, "federationBackupPolicy")
}

func safeModuleFederationObservability(moduleID string, source modulePlanInputSource) (map[string]any, error) {
	if moduleID != "stackkits-federation-observability-runtime" {
		return nil, fmt.Errorf("module %q cannot consume Federation observability authority", moduleID)
	}
	bridge, err := requireFederationSource(source)
	if err != nil {
		return nil, err
	}
	publications, err := objectListField(bridge, "bridge", "publications")
	if err != nil {
		return nil, err
	}
	publicationEvidence := make([]any, 0, len(publications))
	for index, publication := range publications {
		path := fmt.Sprintf("bridge.publications[%d]", index)
		projected, err := selectObjectFields(publication, path, []string{
			"serviceRef", "sourceSiteRef", "edgeSiteRef", "healthGateRef", "defaultClosed",
		})
		if err != nil {
			return nil, err
		}
		publicationEvidence = append(publicationEvidence, projected)
	}
	policy, err := objectField(bridge, "bridge", "policy")
	if err != nil {
		return nil, err
	}
	flows, err := objectListField(policy, "bridge.policy", "allowedFlows")
	if err != nil {
		return nil, err
	}
	flowEvidence := make([]any, 0, len(flows))
	for index, flow := range flows {
		path := fmt.Sprintf("bridge.policy.allowedFlows[%d]", index)
		projected, err := selectObjectFields(flow, path, []string{
			"fromSiteRef", "toSiteRef", "serviceRef", "protocol", "ports", "dataClasses", "serviceIdentityRequired",
		})
		if err != nil {
			return nil, err
		}
		if methods, exists := flow["methods"]; exists {
			projected["methods"] = methods
		}
		flowEvidence = append(flowEvidence, projected)
	}
	partition, err := safeFederationPartitionPolicy(source.failurePolicy)
	if err != nil {
		return nil, err
	}
	return normalizedObject(map[string]any{
		"evidenceOnly": true, "publications": publicationEvidence, "flows": flowEvidence, "partition": partition,
	}, "federationObservability")
}

func requireFederationSource(source modulePlanInputSource) (map[string]any, error) {
	if source.bridge == nil || source.data == nil || source.failurePolicy == nil {
		return nil, fmt.Errorf("Modern federation projections require resolved bridge, data, and partition authority")
	}
	return source.bridge, nil
}

func safeFederationPartitionPolicy(failurePolicy map[string]any) (map[string]any, error) {
	if failurePolicy == nil {
		return nil, fmt.Errorf("Modern federation partition policy is unavailable")
	}
	return selectObjectFields(failurePolicy, "failurePolicy", []string{
		"onCloudLoss", "onLinkLoss", "cloudEdge", "localIdentityAuthorityAvailable",
		"maxStaleVerificationSeconds", "denyNewCrossSiteSessions", "issuedSessions", "newSessions",
	})
}

func safeFederationDataPolicy(data map[string]any) (map[string]any, error) {
	if data == nil {
		return nil, fmt.Errorf("Modern federation data authority is unavailable")
	}
	defaultAuthority, err := stringField(data, "data", "defaultAuthority")
	if err != nil {
		return nil, err
	}
	rawBindings, exists := data["bindings"]
	if !exists {
		return nil, fmt.Errorf("data.bindings is required")
	}
	bindings, err := asObject(rawBindings, "data.bindings")
	if err != nil {
		return nil, err
	}
	projected := make(map[string]any, len(bindings))
	for bindingRef, raw := range bindings {
		binding, err := asObject(raw, "data.bindings."+bindingRef)
		if err != nil {
			return nil, err
		}
		safeBinding, err := selectObjectFields(binding, "data.bindings."+bindingRef, []string{
			"classes", "primarySiteRef", "cloudCopyAllowed",
		})
		if err != nil {
			return nil, err
		}
		if replicas, exists := binding["replicaSiteRefs"]; exists {
			safeBinding["replicaSiteRefs"] = replicas
		}
		if cloudCopyPolicy, exists := binding["cloudCopyPolicy"]; exists {
			policy, err := asObject(cloudCopyPolicy, "data.bindings."+bindingRef+".cloudCopyPolicy")
			if err != nil {
				return nil, err
			}
			safePolicy, err := selectObjectFields(policy, "data.bindings."+bindingRef+".cloudCopyPolicy", []string{
				"policyRef", "allowedClasses", "allowPrimary", "allowReplicas",
			})
			if err != nil {
				return nil, err
			}
			safeBinding["cloudCopyPolicy"] = safePolicy
		}
		projected[bindingRef] = safeBinding
	}
	return normalizedObject(map[string]any{"defaultAuthority": defaultAuthority, "bindings": projected}, "federationDataAuthority")
}

func selectFederationPublicationFields(publications []map[string]any) ([]any, error) {
	projected := make([]any, 0, len(publications))
	for index, publication := range publications {
		path := fmt.Sprintf("bridge.publications[%d]", index)
		safePublication, err := selectObjectFields(publication, path, []string{
			"serviceRef", "sourceSiteRef", "edgeSiteRef", "host", "protocol", "port", "path", "defaultClosed",
			"tls", "auth", "origin", "rateLimit", "moduleRef", "unitRef", "originNodeRefs", "originInstanceRefs",
			"upstreamProtocol", "targetPort", "healthGateRef", "dataBindingRef", "access",
		})
		if err != nil {
			return nil, err
		}
		projected = append(projected, safePublication)
	}
	return projected, nil
}
