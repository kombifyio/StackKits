package resolvedplan

import (
	"fmt"
)

var allowedModulePlanInputRefs = map[string]struct{}{
	"stackId": {}, "kit": {}, "sites": {}, "controlPlane": {},
	"bridge": {}, "bridgePublications": {}, "bridgeOriginMTLS": {}, "identity": {}, "data": {}, "failurePolicy": {},
	"federationPolicy": {}, "federationLinkPolicy": {}, "federationControlActions": {}, "federationBackupPolicy": {}, "federationObservability": {}, "backupPolicy": {}, "driftPolicy": {}, "observability": {},
	"identityTrust": {}, "localReachability": {}, "homeLANDiscovery": {},
	"homeAccessRequirements": {}, "externalHomeAccessBindings": {},
	"backupTargetRequirements": {}, "externalBackupTargetBindings": {},
	"homeBackupTargetRequirements": {}, "externalHomeBackupTargetBindings": {},
	"homeAccessHandoff": {}, "homeOffsiteBackup": {}, "cloudOffsiteBackup": {},
	"federationLinkRequirements": {}, "externalFederationLinkBindings": {},
	"availability":  {},
	"moduleTargets": {}, "moduleCapabilities": {}, "hostRuntimePolicy": {},
	"storagePolicy": {}, "localNetworkPolicy": {}, "cloudNetworkPolicy": {}, "publicEdge": {}, "publicTLS": {}, "internalPKI": {}, "cloudAdminMesh": {},
}

func validateModulePlanInputRefs(refs []string, path string) ([]string, error) {
	seen := make(map[string]struct{}, len(refs))
	validated := append([]string(nil), refs...)
	for index, ref := range validated {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		if _, allowed := allowedModulePlanInputRefs[ref]; !allowed {
			return nil, fail(ErrContractConflict, itemPath, "unsupported compiler plan input %q", ref)
		}
		if _, duplicate := seen[ref]; duplicate {
			return nil, fail(ErrContractConflict, itemPath, "compiler plan input %q is duplicated", ref)
		}
		seen[ref] = struct{}{}
	}
	return sortStringsUnique(validated), nil
}

type modulePlanInputSource struct {
	stackID                          string
	kit                              map[string]any
	sites                            []any
	controlPlane                     map[string]any
	bridge                           map[string]any
	identity                         map[string]any
	identityTrust                    map[string]any
	data                             map[string]any
	failurePolicy                    map[string]any
	backupPolicy                     map[string]any
	driftPolicy                      map[string]any
	observability                    map[string]any
	localReachability                map[string]any
	homeLANDiscovery                 map[string]any
	homeAccessRequirements           map[string]any
	externalHomeAccessBindings       map[string]any
	backupTargetRequirements         map[string]any
	externalBackupTargetBindings     map[string]any
	homeBackupTargetRequirements     map[string]any
	externalHomeBackupTargetBindings map[string]any
	federationLinkRequirements       map[string]any
	externalFederationLinkBindings   map[string]any
	availability                     map[string]any
	nodes                            []any
	capabilities                     []any
	providers                        []any
	install                          map[string]any
	system                           map[string]any
	storage                          map[string]any
	network                          map[string]any
	gates                            map[string]any
}

// bindResolvedModulePlanInputs is the only compiler seam that populates a
// render unit's planInputs. It runs after resolveBridgePlan and copies only a
// closed, catalog-declared view. User module settings, secretRefs, raw nodes,
// management addresses, and daemon/socket facts are intentionally unreachable.
func bindResolvedModulePlanInputs(modules []any, source modulePlanInputSource) error {
	for moduleIndex, rawModule := range modules {
		module, err := asObject(rawModule, fmt.Sprintf("modules[%d]", moduleIndex))
		if err != nil {
			return err
		}
		moduleID, err := stringField(module, fmt.Sprintf("modules[%d]", moduleIndex), "id")
		if err != nil {
			return err
		}
		units, err := objectListField(module, "modules."+moduleID, "renderUnits")
		if err != nil {
			return err
		}
		for unitIndex, unit := range units {
			unitPath := fmt.Sprintf("modules.%s.renderUnits[%d]", moduleID, unitIndex)
			refs, err := stringListField(unit, unitPath, "planInputRefs", false)
			if err != nil {
				return err
			}
			refs, err = validateModulePlanInputRefs(refs, unitPath+".planInputRefs")
			if err != nil {
				return err
			}
			inputs := make(map[string]any, len(refs))
			for _, ref := range refs {
				projection, err := source.resolve(ref, moduleID, module)
				if err != nil {
					return fail(ErrContractConflict, unitPath+".planInputRefs", "%v", err)
				}
				inputs[ref] = projection
			}
			unit["planInputRefs"] = stringSliceAny(refs)
			unit["planInputs"] = inputs
		}
	}
	return nil
}

func (source modulePlanInputSource) resolve(ref, moduleID string, module map[string]any) (any, error) {
	if _, allowed := allowedModulePlanInputRefs[ref]; !allowed {
		return nil, fmt.Errorf("unsupported compiler plan input %q", ref)
	}
	var value any
	switch ref {
	case "stackId":
		value = source.stackID
	case "kit":
		value = source.kit
	case "sites":
		return safeModulePlanSites(source.sites)
	case "controlPlane":
		value = source.controlPlane
	case "bridge":
		if source.bridge == nil {
			return nil, fmt.Errorf("bridge projection is unavailable")
		}
		value = source.bridge
	case "bridgePublications":
		return safeModuleBridgePublications(moduleID, module, source.bridge)
	case "bridgeOriginMTLS":
		return safeModuleBridgeOriginMTLS(moduleID, source.bridge, source.identityTrust)
	case "identity":
		value = source.identity
	case "identityTrust":
		if source.identityTrust == nil {
			return nil, fmt.Errorf("identity trust projection is unavailable")
		}
		value = source.identityTrust
	case "data":
		value = source.data
	case "failurePolicy":
		value = source.failurePolicy
	case "backupPolicy":
		return safeModuleBackupPolicy(source.backupPolicy)
	case "driftPolicy":
		return safeModuleDriftPolicy(source.driftPolicy)
	case "observability":
		return safeModuleOTLPBaseline(moduleID, source.observability)
	case "federationPolicy":
		return safeModuleFederationPolicy(moduleID, source)
	case "federationLinkPolicy":
		return safeModuleFederationLinkPolicy(moduleID, source)
	case "federationControlActions":
		return safeModuleFederationControlActions(moduleID, source)
	case "federationBackupPolicy":
		return safeModuleFederationBackupPolicy(moduleID, source)
	case "federationObservability":
		return safeModuleFederationObservability(moduleID, source)
	case "localReachability":
		value = source.localReachability
	case "homeLANDiscovery":
		value = source.homeLANDiscovery
	case "homeAccessRequirements":
		return safeModuleHomeAccessProjection(moduleID, module, source.homeAccessRequirements, true)
	case "externalHomeAccessBindings":
		return safeModuleHomeAccessProjection(moduleID, module, source.externalHomeAccessBindings, false)
	case "homeAccessHandoff":
		return safeModuleHomeAccessHandoff(moduleID, module, source)
	case "backupTargetRequirements":
		return safeModuleBackupTargetProjection(moduleID, module, source.backupTargetRequirements, true)
	case "externalBackupTargetBindings":
		return safeModuleBackupTargetProjection(moduleID, module, source.externalBackupTargetBindings, false)
	case "homeBackupTargetRequirements":
		return safeModuleHomeBackupTargetProjection(moduleID, module, source.homeBackupTargetRequirements, true)
	case "externalHomeBackupTargetBindings":
		return safeModuleHomeBackupTargetProjection(moduleID, module, source.externalHomeBackupTargetBindings, false)
	case "homeOffsiteBackup":
		return safeModuleHomeOffsiteBackup(moduleID, module, source)
	case "cloudOffsiteBackup":
		return safeModuleCloudOffsiteBackup(moduleID, module, source)
	case "federationLinkRequirements":
		return safeModuleFederationLinkProjection(moduleID, module, source.federationLinkRequirements, true)
	case "externalFederationLinkBindings":
		return safeModuleFederationLinkProjection(moduleID, module, source.externalFederationLinkBindings, false)
	case "availability":
		return safeModuleAvailability(moduleID, source.availability)
	case "moduleTargets":
		return safeModuleTargets(moduleID, module, source.nodes)
	case "moduleCapabilities":
		return safeModuleCapabilities(moduleID, module, source.capabilities)
	case "hostRuntimePolicy":
		return safeModuleHostRuntimePolicy(source.install, source.system)
	case "storagePolicy":
		return safeModuleStoragePolicy(source.storage)
	case "localNetworkPolicy":
		return safeModuleNetworkPolicy(source.network, "localNetworkPolicy")
	case "cloudNetworkPolicy":
		return safeModuleNetworkPolicy(source.network, "cloudNetworkPolicy")
	case "publicEdge":
		return safeModulePublicEdge(moduleID, module, source.capabilities, source.network, source.gates)
	case "publicTLS":
		return safeModulePublicTLS(moduleID, module, source.capabilities, source.providers, source.network)
	case "internalPKI":
		return safeModuleInternalPKI(moduleID, module, source)
	case "cloudAdminMesh":
		return safeModuleCloudAdminMesh(moduleID, module, source.capabilities, source.nodes, source.network, source.gates)
	}
	return normalizeJSON(value, false, ref)
}

func modulePlanInputSourceFromResolvedPlan(plan ResolvedPlan) (modulePlanInputSource, error) {
	top := map[string]any(plan)
	stackID, err := stringField(top, "resolvedPlan", "stackId")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	kit, err := objectField(top, "resolvedPlan", "kit")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	sites, err := objectListField(top, "resolvedPlan", "sites")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	controlPlane, err := objectField(top, "resolvedPlan", "controlPlane")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	bridge, _, err := optionalObjectField(top, "resolvedPlan", "bridge")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	identity, err := objectField(top, "resolvedPlan", "identity")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	identityTrust, err := objectField(top, "resolvedPlan", "identityTrust")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	data, err := objectField(top, "resolvedPlan", "data")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	failurePolicy, err := objectField(top, "resolvedPlan", "failurePolicy")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	backupPolicy, _, err := optionalObjectField(top, "resolvedPlan", "backupPolicy")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	driftPolicy, _, err := optionalObjectField(top, "resolvedPlan", "driftPolicy")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	observability, _, err := optionalObjectField(top, "resolvedPlan", "observability")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	network, err := objectField(top, "resolvedPlan", "network")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	gates, err := objectField(top, "resolvedPlan", "gates")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	localReachability, err := buildLocalReachability(network, objectMapsAsAny(sites))
	if err != nil {
		return modulePlanInputSource{}, err
	}
	homeLANDiscovery, err := objectField(top, "resolvedPlan", "homeLANDiscovery")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	homeAccessRequirements, err := objectField(top, "resolvedPlan", "homeAccessRequirements")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	externalHomeAccessBindings, err := objectField(top, "resolvedPlan", "externalHomeAccessBindings")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	backupTargetRequirements, err := objectField(top, "resolvedPlan", "backupTargetRequirements")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	externalBackupTargetBindings, err := objectField(top, "resolvedPlan", "externalBackupTargetBindings")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	homeBackupTargetRequirements, err := objectField(top, "resolvedPlan", "homeBackupTargetRequirements")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	externalHomeBackupTargetBindings, err := objectField(top, "resolvedPlan", "externalHomeBackupTargetBindings")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	federationLinkRequirements, err := objectField(top, "resolvedPlan", "federationLinkRequirements")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	availability, err := objectField(top, "resolvedPlan", "availability")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	externalFederationLinkBindings, err := objectField(top, "resolvedPlan", "externalFederationLinkBindings")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	nodes, err := objectListField(top, "resolvedPlan", "nodes")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	capabilities, err := objectListField(top, "resolvedPlan", "capabilities")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	providers, err := objectListField(top, "resolvedPlan", "providers")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	install, err := objectField(top, "resolvedPlan", "install")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	system, err := objectField(top, "resolvedPlan", "system")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	storage, err := objectField(top, "resolvedPlan", "storage")
	if err != nil {
		return modulePlanInputSource{}, err
	}
	return modulePlanInputSource{
		stackID: stackID, kit: kit, sites: objectMapsAsAny(sites),
		controlPlane: controlPlane, bridge: bridge, identity: identity, identityTrust: identityTrust,
		data: data, failurePolicy: failurePolicy, backupPolicy: backupPolicy, driftPolicy: driftPolicy, observability: observability, localReachability: localReachability,
		homeLANDiscovery: homeLANDiscovery, homeAccessRequirements: homeAccessRequirements, externalHomeAccessBindings: externalHomeAccessBindings,
		backupTargetRequirements: backupTargetRequirements, externalBackupTargetBindings: externalBackupTargetBindings,
		homeBackupTargetRequirements: homeBackupTargetRequirements, externalHomeBackupTargetBindings: externalHomeBackupTargetBindings,
		federationLinkRequirements: federationLinkRequirements, externalFederationLinkBindings: externalFederationLinkBindings,
		availability: availability,
		nodes:        objectMapsAsAny(nodes), capabilities: objectMapsAsAny(capabilities), providers: objectMapsAsAny(providers),
		install: install, system: system, storage: storage, network: network, gates: gates,
	}, nil
}

func resolvedPlanObjectByID(values []any, wantedID, path string) (map[string]any, error) {
	var match map[string]any
	for index, raw := range values {
		value, err := asObject(raw, fmt.Sprintf("%s[%d]", path, index))
		if err != nil {
			return nil, err
		}
		id, err := stringField(value, fmt.Sprintf("%s[%d]", path, index), "id")
		if err != nil {
			return nil, err
		}
		if id != wantedID {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("%s contains duplicate id %q", path, wantedID)
		}
		match = value
	}
	if match == nil {
		return nil, fmt.Errorf("%s has no id %q", path, wantedID)
	}
	return match, nil
}

func selectObjectFields(source map[string]any, path string, fields []string) (map[string]any, error) {
	result := make(map[string]any, len(fields))
	for _, field := range fields {
		value, exists := source[field]
		if !exists {
			return nil, fmt.Errorf("%s.%s is required", path, field)
		}
		result[field] = value
	}
	return result, nil
}

func selectOptionalObjectFields(source map[string]any, path string, fields []string) (map[string]any, error) {
	result := make(map[string]any, len(fields))
	for _, field := range fields {
		if value, exists := source[field]; exists {
			result[field] = value
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("%s has no represented fields", path)
	}
	return result, nil
}

func normalizedObject(value map[string]any, path string) (map[string]any, error) {
	normalized, err := normalizeJSON(value, false, path)
	if err != nil {
		return nil, err
	}
	object, ok := normalized.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s projection has unexpected type %T", path, normalized)
	}
	return object, nil
}
