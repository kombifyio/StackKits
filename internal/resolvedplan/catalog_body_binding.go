package resolvedplan

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
)

// expectedCatalogBodyBinding retains an immutable, CUE-normalized catalog.
// Contract hashes prove identity, but hashes are copyable strings: persisted
// plans must also keep every catalog-owned field equal to the body that hash
// names. Deployment-specific inputs and placements are validated separately.
type expectedCatalogBodyBinding struct {
	catalog *indexedCatalog
}

func (v *CUEContractValidator) bindExpectedCatalogBodies(catalog Catalog) error {
	if v == nil || !v.initialized {
		return fmt.Errorf("CUE contract validator is not initialized")
	}
	frozen, err := freezeCatalog(catalog)
	if err != nil {
		return fmt.Errorf("freeze authority catalog bodies: %w", err)
	}
	indexed, err := indexCatalog(frozen)
	if err != nil {
		return fmt.Errorf("index authority catalog bodies: %w", err)
	}
	binding := &expectedCatalogBodyBinding{catalog: indexed}
	if v.boundCatalog != nil && !reflect.DeepEqual(v.boundCatalog, binding) {
		return fmt.Errorf("CUE contract validator is already bound to different catalog bodies")
	}
	v.boundCatalog = binding
	return nil
}

func (v *CUEContractValidator) validateBoundCatalogBodies(plan ResolvedPlan) error {
	if v == nil || v.boundCatalog == nil || v.boundCatalog.catalog == nil {
		return fmt.Errorf("CUE contract validator has no bound catalog bodies")
	}
	catalog := v.boundCatalog.catalog
	capabilityProviders, err := resolvedCapabilityProviders(plan, catalog)
	if err != nil {
		return err
	}
	workloadProviders, workloadModules, workloadPlacements, err := validateResolvedWorkloadBodies(plan, catalog)
	if err != nil {
		return err
	}
	runtimeAdapterProviders, runtimeAdapterModules, err := resolvedWorkloadRuntimeAdapterOwners(plan)
	if err != nil {
		return err
	}
	if err := validateResolvedSelectionGraph(plan, catalog, capabilityProviders, workloadProviders, runtimeAdapterProviders); err != nil {
		return err
	}
	if err := validateResolvedProviderBodies(plan, catalog, capabilityProviders, workloadProviders, runtimeAdapterProviders); err != nil {
		return err
	}
	if err := validateResolvedModuleBodies(plan, catalog, capabilityProviders, workloadProviders, runtimeAdapterProviders, workloadModules, runtimeAdapterModules, workloadPlacements); err != nil {
		return err
	}
	if err := validateResolvedModuleCoverage(plan, catalog, capabilityProviders); err != nil {
		return err
	}
	if err := validateRuntimeNetworkProjection(plan); err != nil {
		return err
	}
	if err := validateRouteOriginProjection(plan, catalog, capabilityProviders); err != nil {
		return err
	}
	if err := validateRouteCapabilityRealizationBodies(plan, catalog, capabilityProviders); err != nil {
		return err
	}
	if err := validateBridgePublicationProjection(plan); err != nil {
		return err
	}
	if err := validateExternalHostPlanProjection(plan); err != nil {
		return err
	}
	if err := validateHomeAccessPlanProjection(plan); err != nil {
		return err
	}
	if err := validateBackupTargetPlanProjection(plan); err != nil {
		return err
	}
	if err := validateHomeBackupTargetPlanProjection(plan); err != nil {
		return err
	}
	if err := validateFederationLinkPlanProjection(plan); err != nil {
		return err
	}
	if err := validateGenerationArtifactsProjection(plan, catalog); err != nil {
		return err
	}
	if err := validateResolvedGateBodies(plan, catalog, capabilityProviders); err != nil {
		return err
	}
	if err := validatePrivilegedInterfaceApprovalProjection(plan, catalog); err != nil {
		return err
	}
	if err := validateExecutionReadinessProjection(plan); err != nil {
		return err
	}
	return nil
}

//nolint:gocyclo // Route-origin verification exhaustively compares the derived route, node, instance, and module identities.
func validateRouteOriginProjection(plan ResolvedPlan, catalog *indexedCatalog, capabilityProviders map[string]string) error {
	moduleValues, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	serviceEndpoints, err := indexResolvedServiceEndpoints(objectMapsAsAny(moduleValues))
	if err != nil {
		return err
	}
	if err := bindServiceEndpointHealthContracts(serviceEndpoints, catalog.modules); err != nil {
		return err
	}
	nodeSites, _, _, err := resolvedTopologyIndex(plan)
	if err != nil {
		return err
	}
	targetSpec, err := resolvedModuleTargetSpec(plan)
	if err != nil {
		return err
	}
	controlPlane, err := objectField(map[string]any(plan), "resolvedPlan", "controlPlane")
	if err != nil {
		return err
	}
	authoritySiteRef, err := stringField(controlPlane, "resolvedPlan.controlPlane", "authoritySiteRef")
	if err != nil {
		return err
	}
	network, err := objectField(map[string]any(plan), "resolvedPlan", "network")
	if err != nil {
		return err
	}
	configuration, err := objectField(network, "resolvedPlan.network", "configuration")
	if err != nil {
		return err
	}
	routes, err := objectListField(network, "resolvedPlan.network", "routes")
	if err != nil {
		return err
	}
	backendPools, err := objectListField(network, "resolvedPlan.network", "backendPools")
	if err != nil {
		return err
	}
	if len(backendPools) != len(routes) {
		return fmt.Errorf("resolvedPlan.network.backendPools must contain exactly one pool per route")
	}
	poolsByID := make(map[string]map[string]any, len(backendPools))
	for index, pool := range backendPools {
		path := fmt.Sprintf("resolvedPlan.network.backendPools[%d]", index)
		poolID, err := stringField(pool, path, "id")
		if err != nil {
			return err
		}
		if _, duplicate := poolsByID[poolID]; duplicate {
			return fmt.Errorf("%s.id %q is duplicated", path, poolID)
		}
		poolsByID[poolID] = pool
	}
	accessPolicies := map[string]any{}
	planData, err := objectField(map[string]any(plan), "resolvedPlan", "data")
	if err != nil {
		return err
	}
	if len(routes) > 0 {
		accessPolicies, err = objectField(map[string]any(plan), "resolvedPlan", "access")
		if err != nil {
			return err
		}
	}
	for index, route := range routes {
		path := fmt.Sprintf("resolvedPlan.network.routes[%d]", index)
		routeID, err := stringField(route, path, "id")
		if err != nil {
			return err
		}
		moduleRef, err := stringField(route, path, "moduleRef")
		if err != nil {
			return err
		}
		serviceRef, err := stringField(route, path, "serviceRef")
		if err != nil {
			return err
		}
		endpoint, err := resolveRouteServiceEndpoint(serviceEndpoints, moduleRef, serviceRef, authoritySiteRef, nodeSites, path, targetSpec)
		if err != nil {
			return err
		}
		if err := validateResolvedRouteEndpoint(route, path, endpoint, planData); err != nil {
			return err
		}
		poolRef, err := stringField(route, path, "backendPoolRef")
		if err != nil {
			return err
		}
		havePool, exists := poolsByID[poolRef]
		if !exists {
			return fmt.Errorf("%s.backendPoolRef %q has no persisted backend pool", path, poolRef)
		}
		wantPool, err := buildRouteBackendPool(routeID, endpoint)
		if err != nil {
			return fmt.Errorf("recompute %s backend pool: %w", path, err)
		}
		if equal, err := canonicalEqual(havePool, wantPool); err != nil {
			return err
		} else if !equal {
			return fmt.Errorf("resolvedPlan.network.backendPools.%s is not the exact compiler-derived route backend pool", poolRef)
		}
		delete(poolsByID, poolRef)
		exposure, err := stringField(route, path, "exposure")
		if err != nil {
			return err
		}
		protocol, err := stringField(route, path, "protocol")
		if err != nil {
			return err
		}
		routeRequirements, err := routeCapabilityRequirementsFromProjection(route, path)
		if err != nil {
			return err
		}
		wantTLS, err := resolveRouteTLS(routeID, protocol, exposure, configuration, catalog, capabilityProviders, routeRequirements)
		if err != nil {
			return fmt.Errorf("recompute %s.tls: %w", path, err)
		}
		haveTLS, err := objectField(route, path, "tls")
		if err != nil {
			return err
		}
		if equal, err := canonicalEqual(haveTLS, wantTLS); err != nil {
			return err
		} else if !equal {
			return fmt.Errorf("%s.tls is not the exact compiler-derived network TLS projection", path)
		}
		haveAccess, err := objectField(route, path, "access")
		if err != nil {
			return err
		}
		policyRef, err := stringField(haveAccess, path+".access", "policyRef")
		if err != nil {
			return err
		}
		policy, exists := accessPolicies[policyRef]
		if !exists {
			return fmt.Errorf("%s.access.policyRef %q has no persisted access policy", path, policyRef)
		}
		wantAccess, err := resolveRouteAccess(routeID, exposure, policyRef, policy)
		if err != nil {
			return fmt.Errorf("recompute %s.access: %w", path, err)
		}
		if equal, err := canonicalEqual(haveAccess, wantAccess); err != nil {
			return err
		} else if !equal {
			return fmt.Errorf("%s.access is not the exact compiler-derived access projection", path)
		}
	}
	if len(poolsByID) != 0 {
		return fmt.Errorf("resolvedPlan.network.backendPools contains orphan pools")
	}
	return nil
}

func validateRuntimeNetworkProjection(plan ResolvedPlan) error {
	haveModules, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	resolvedModules := make([]any, 0, len(haveModules))
	for index, module := range haveModules {
		clone, err := cloneObject(module, false)
		if err != nil {
			return fmt.Errorf("clone resolvedPlan.modules[%d] for runtime-network reconstruction: %w", index, err)
		}
		resolvedModules = append(resolvedModules, clone)
	}
	nodes, err := runtimeNetworkNodeViews(plan, resolvedModules)
	if err != nil {
		return err
	}
	wantNetworks, err := resolveImplementationInterfaces(resolvedModules, nodes)
	if err != nil {
		return fmt.Errorf("recompute resolvedPlan.runtimeNetworks: %w", err)
	}
	if equal, err := canonicalEqual(resolvedModules, objectMapsAsAny(haveModules)); err != nil {
		return err
	} else if !equal {
		return fmt.Errorf("resolvedPlan module interface bindings are not the exact compiler-derived projection")
	}
	haveNetworks, err := objectListField(map[string]any(plan), "resolvedPlan", "runtimeNetworks")
	if err != nil {
		return err
	}
	if equal, err := canonicalEqual(wantNetworks, objectMapsAsAny(haveNetworks)); err != nil {
		return err
	} else if !equal {
		return fmt.Errorf("resolvedPlan.runtimeNetworks is not the exact compiler-derived implementation-interface projection")
	}
	return nil
}

//nolint:gocyclo // Node-view reconstruction decodes every optional runtime and module projection before accepting the plan.
func runtimeNetworkNodeViews(plan ResolvedPlan, modules []any) (map[string]nodeView, error) {
	nodeValues, err := objectListField(map[string]any(plan), "resolvedPlan", "nodes")
	if err != nil {
		return nil, err
	}
	nodes := make(map[string]nodeView, len(nodeValues))
	daemonInstanceOwners := make(map[string]map[string]string, len(nodeValues))
	daemonSocketOwners := make(map[string]map[string]string, len(nodeValues))
	for index, node := range nodeValues {
		path := fmt.Sprintf("resolvedPlan.nodes[%d]", index)
		id, err := stringField(node, path, "id")
		if err != nil {
			return nil, err
		}
		siteRef, err := stringField(node, path, "siteRef")
		if err != nil {
			return nil, err
		}
		enabled, err := boolFieldDefault(node, path, "enabled", true)
		if err != nil {
			return nil, err
		}
		roles, err := stringListField(node, path, "roles", false)
		if err != nil {
			return nil, err
		}
		if _, duplicate := nodes[id]; duplicate {
			return nil, fmt.Errorf("%s.id %q duplicates a resolved topology node", path, id)
		}
		nodes[id] = nodeView{
			id: id, siteRef: siteRef, roles: roles, enabled: enabled, object: node,
			runtimeDaemons: map[string]runtimeDaemonFact{},
		}
		daemonInstanceOwners[id] = map[string]string{}
		daemonSocketOwners[id] = map[string]string{}
	}
	for moduleIndex, rawModule := range modules {
		module, err := asObject(rawModule, fmt.Sprintf("resolvedPlan.modules[%d]", moduleIndex))
		if err != nil {
			return nil, err
		}
		moduleID, err := stringField(module, fmt.Sprintf("resolvedPlan.modules[%d]", moduleIndex), "id")
		if err != nil {
			return nil, err
		}
		units, err := objectListField(module, "resolvedPlan.modules."+moduleID, "renderUnits")
		if err != nil {
			return nil, err
		}
		for unitIndex, unit := range units {
			unitPath := fmt.Sprintf("resolvedPlan.modules.%s.renderUnits[%d]", moduleID, unitIndex)
			instances, err := objectListField(unit, unitPath, "instances")
			if err != nil {
				return nil, err
			}
			for instanceIndex, instance := range instances {
				instancePath := fmt.Sprintf("%s.instances[%d]", unitPath, instanceIndex)
				daemonRef, hasDaemon, err := optionalStringField(instance, instancePath, "daemonRef")
				if err != nil {
					return nil, err
				}
				if !hasDaemon {
					continue
				}
				nodeRef, err := stringField(instance, instancePath, "nodeRef")
				if err != nil {
					return nil, err
				}
				node, exists := nodes[nodeRef]
				if !exists {
					return nil, fmt.Errorf("%s.nodeRef %q has no resolved topology node", instancePath, nodeRef)
				}
				instanceRef, err := stringField(instance, instancePath, "daemonInstanceRef")
				if err != nil {
					return nil, err
				}
				engine, err := stringField(instance, instancePath, "daemonEngine")
				if err != nil {
					return nil, err
				}
				socketPath, err := stringField(instance, instancePath, "daemonSocketPath")
				if err != nil {
					return nil, err
				}
				if err := validateUnixSocketPath(socketPath, instancePath+".daemonSocketPath", ErrContractConflict); err != nil {
					return nil, err
				}
				fact := runtimeDaemonFact{instanceRef: instanceRef, engine: engine, socketPath: socketPath}
				if existing, duplicate := node.runtimeDaemons[daemonRef]; duplicate && existing != fact {
					return nil, fmt.Errorf("%s carries conflicting daemon identity for node %q daemon %q", instancePath, nodeRef, daemonRef)
				}
				if owner, duplicate := daemonInstanceOwners[nodeRef][instanceRef]; duplicate && owner != daemonRef {
					return nil, fmt.Errorf("%s.daemonInstanceRef %q aliases node %q daemons %q and %q", instancePath, instanceRef, nodeRef, owner, daemonRef)
				}
				if owner, duplicate := daemonSocketOwners[nodeRef][socketPath]; duplicate && owner != daemonRef {
					return nil, fmt.Errorf("%s.daemonSocketPath %q aliases node %q daemons %q and %q", instancePath, socketPath, nodeRef, owner, daemonRef)
				}
				node.runtimeDaemons[daemonRef] = fact
				daemonInstanceOwners[nodeRef][instanceRef] = daemonRef
				daemonSocketOwners[nodeRef][socketPath] = daemonRef
				nodes[nodeRef] = node
			}
		}
	}
	return nodes, nil
}

func validateGenerationArtifactsProjection(plan ResolvedPlan, catalog *indexedCatalog) error {
	modules, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	generation, err := objectField(map[string]any(plan), "resolvedPlan", "generation")
	if err != nil {
		return err
	}
	target, err := stringField(generation, "resolvedPlan.generation", "target")
	if err != nil {
		return err
	}
	outputRoot, err := stringField(generation, "resolvedPlan.generation", "outputRoot")
	if err != nil {
		return err
	}
	want, err := deriveGenerationArtifacts(catalog.planArtifacts, objectMapsAsAny(modules), target, outputRoot)
	if err != nil {
		return fmt.Errorf("recompute resolvedPlan.generation.artifacts: %w", err)
	}
	have, err := objectListField(generation, "resolvedPlan.generation", "artifacts")
	if err != nil {
		return err
	}
	// generation.artifacts is a CUE set. Preserve the field name while
	// canonicalizing so canonicalJSON applies the same set ordering used by the
	// persisted plan; comparing the detached slices would incorrectly make the
	// compiler's internal ID order part of the contract.
	equal, err := canonicalEqual(
		map[string]any{"artifacts": objectMapsAsAny(have)},
		map[string]any{"artifacts": want},
	)
	if err != nil {
		return err
	}
	if !equal {
		return fmt.Errorf("resolvedPlan.generation.artifacts is not the exact compiler-derived catalog projection")
	}
	return nil
}

func validatePrivilegedInterfaceApprovalProjection(plan ResolvedPlan, catalog *indexedCatalog) error {
	modules, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	gates, err := objectField(map[string]any(plan), "resolvedPlan", "gates")
	if err != nil {
		return err
	}
	compiler := &Compiler{catalog: catalog}
	want, err := compiler.resolvePrivilegedInterfaceApprovals(objectMapsAsAny(modules), gates)
	if err != nil {
		return fmt.Errorf("recompute resolvedPlan.privilegedInterfaceApprovals: %w", err)
	}
	have, err := objectListField(map[string]any(plan), "resolvedPlan", "privilegedInterfaceApprovals")
	if err != nil {
		return err
	}
	equal, err := canonicalEqual(objectMapsAsAny(have), want)
	if err != nil {
		return err
	}
	if !equal {
		return fmt.Errorf("resolvedPlan.privilegedInterfaceApprovals is not the exact compiler-derived approval projection")
	}
	return nil
}

// validateExecutionReadinessProjection keeps the persisted decision derived
// from the exact provider/module realization, artifact, evidence, renderer,
// and output-root projection that the authority-bound plan carries. A caller
// cannot turn a blocked plan into a ready plan by recomputing planHash.
func validateExecutionReadinessProjection(plan ResolvedPlan) error {
	providers, err := objectListField(map[string]any(plan), "resolvedPlan", "providers")
	if err != nil {
		return err
	}
	modules, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	generation, err := objectField(map[string]any(plan), "resolvedPlan", "generation")
	if err != nil {
		return err
	}
	artifacts, err := objectListField(generation, "resolvedPlan.generation", "artifacts")
	if err != nil {
		return err
	}
	renderer, err := objectField(generation, "resolvedPlan.generation", "renderer")
	if err != nil {
		return err
	}
	rendererID, err := stringField(renderer, "resolvedPlan.generation.renderer", "id")
	if err != nil {
		return err
	}
	outputRoot, err := stringField(generation, "resolvedPlan.generation", "outputRoot")
	if err != nil {
		return err
	}
	evidence, err := stringListField(map[string]any(plan), "resolvedPlan", "evidence", true)
	if err != nil {
		return err
	}

	bridge, hasBridge, err := optionalObjectField(map[string]any(plan), "resolvedPlan", "bridge")
	if err != nil {
		return err
	}
	if !hasBridge {
		bridge = nil
	}
	gates, err := objectField(map[string]any(plan), "resolvedPlan", "gates")
	if err != nil {
		return err
	}
	healthGates, err := objectListField(gates, "resolvedPlan.gates", "health")
	if err != nil {
		return err
	}
	var routeHealth []any
	for _, gate := range healthGates {
		targetKind, err := stringField(gate, "resolvedPlan.gates.health", "targetKind")
		if err != nil {
			return err
		}
		if targetKind == "route" {
			routeHealth = append(routeHealth, gate)
		}
	}
	want, err := buildExecutionReadiness(
		objectMapsAsAny(providers),
		objectMapsAsAny(modules),
		objectMapsAsAny(artifacts),
		evidence,
		rendererID,
		outputRoot,
		routeHealth,
		bridge,
	)
	if err != nil {
		return fmt.Errorf("recompute resolvedPlan.executionReadiness: %w", err)
	}
	have, err := objectField(map[string]any(plan), "resolvedPlan", "executionReadiness")
	if err != nil {
		return err
	}
	equal, err := canonicalEqual(have, want)
	if err != nil {
		return err
	}
	if !equal {
		return fmt.Errorf("resolvedPlan.executionReadiness is not the exact compiler-derived readiness projection")
	}
	return nil
}

func objectMapsAsAny(values []map[string]any) []any {
	result := make([]any, len(values))
	for index := range values {
		result[index] = values[index]
	}
	return result
}

//nolint:gocyclo // Selection-graph validation is the fail-closed cross-product of dependencies, conflicts, providers, and modules.
func validateResolvedSelectionGraph(plan ResolvedPlan, catalog *indexedCatalog, capabilityProviders, workloadProviders, runtimeAdapterProviders map[string]string) error {
	selectedCapabilities := make(map[string]struct{}, len(capabilityProviders))
	selectedProviders := make(map[string]struct{})
	for capabilityID, providerID := range capabilityProviders {
		selectedCapabilities[capabilityID] = struct{}{}
		selectedProviders[providerID] = struct{}{}
	}
	for _, providerID := range workloadProviders {
		selectedProviders[providerID] = struct{}{}
	}
	for _, providerID := range runtimeAdapterProviders {
		selectedProviders[providerID] = struct{}{}
	}
	moduleValues, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	selectedModules := make(map[string]struct{}, len(moduleValues))
	selectedModuleProviders := make(map[string]string, len(moduleValues))
	for index, module := range moduleValues {
		id, err := stringField(module, fmt.Sprintf("resolvedPlan.modules[%d]", index), "id")
		if err != nil {
			return err
		}
		selectedModules[id] = struct{}{}
		providerRef, _, err := optionalStringField(module, fmt.Sprintf("resolvedPlan.modules[%d]", index), "providerRef")
		if err != nil {
			return err
		}
		selectedModuleProviders[id] = providerRef
	}
	addons, hasAddons, err := optionalObjectField(map[string]any(plan), "resolvedPlan", "addons")
	if err != nil {
		return err
	}
	selectedAddons := make(map[string]struct{})
	if hasAddons {
		for id := range addons {
			selectedAddons[id] = struct{}{}
		}
	}
	selectedContracts := make(map[string]struct{}, len(selectedCapabilities)+len(selectedProviders)+len(selectedAddons)+len(workloadProviders))
	for id := range selectedCapabilities {
		selectedContracts[id] = struct{}{}
	}
	for id := range selectedProviders {
		selectedContracts[id] = struct{}{}
	}
	for id := range selectedAddons {
		selectedContracts[id] = struct{}{}
	}
	for workloadID := range workloadProviders {
		selectedContracts[workloadID] = struct{}{}
	}
	for adapterID := range runtimeAdapterProviders {
		selectedContracts[adapterID] = struct{}{}
	}

	sites, err := objectListField(map[string]any(plan), "resolvedPlan", "sites")
	if err != nil {
		return err
	}
	selectedSiteKinds := map[string]struct{}{}
	for index, site := range sites {
		kind, err := stringField(site, fmt.Sprintf("resolvedPlan.sites[%d]", index), "kind")
		if err != nil {
			return err
		}
		selectedSiteKinds[kind] = struct{}{}
	}
	for capabilityID := range selectedCapabilities {
		contract := catalog.capabilities[capabilityID]
		if err := requireSelectedCapabilityRequirements(contract, "catalog.capabilities."+capabilityID, selectedCapabilities, catalog); err != nil {
			return err
		}
		conflicts, err := stringListField(contract, "catalog.capabilities."+capabilityID, "conflicts", false)
		if err != nil {
			return err
		}
		if err := rejectSelectedConflicts("catalog.capabilities."+capabilityID, conflicts, selectedContracts); err != nil {
			return err
		}
		supportedKinds, err := stringListField(contract, "catalog.capabilities."+capabilityID, "supportedSiteKinds", true)
		if err != nil {
			return err
		}
		if !setsIntersect(selectedSiteKinds, supportedKinds) {
			return fmt.Errorf("resolved capability %q supports no site kind in the persisted topology", capabilityID)
		}
	}
	for providerID := range selectedProviders {
		contract := catalog.providers[providerID]
		if err := requireSelectedCapabilityRequirements(contract, "catalog.providers."+providerID, selectedCapabilities, catalog); err != nil {
			return err
		}
		conflicts, err := stringListField(contract, "catalog.providers."+providerID, "conflicts", false)
		if err != nil {
			return err
		}
		if err := rejectSelectedConflicts("catalog.providers."+providerID, conflicts, selectedContracts); err != nil {
			return err
		}
	}
	kit, err := objectField(map[string]any(plan), "resolvedPlan", "kit")
	if err != nil {
		return err
	}
	kitSlug, err := stringField(kit, "resolvedPlan.kit", "slug")
	if err != nil {
		return err
	}
	for addonID := range selectedAddons {
		contract := catalog.addons[addonID]
		if contract == nil {
			return fmt.Errorf("resolvedPlan.addons.%s has no bound add-on body", addonID)
		}
		supportedKits, err := stringListField(contract, "catalog.addons."+addonID, "supportedKits", true)
		if err != nil {
			return err
		}
		if !contains(supportedKits, kitSlug) {
			return fmt.Errorf("resolvedPlan.addons.%s does not support bound kit %q", addonID, kitSlug)
		}
		provides, err := stringListField(contract, "catalog.addons."+addonID, "provides", true)
		if err != nil {
			return err
		}
		for _, capabilityID := range provides {
			if _, selected := selectedCapabilities[capabilityID]; !selected {
				return fmt.Errorf("resolvedPlan.addons.%s omits provided capability %q", addonID, capabilityID)
			}
		}
		if err := requireSelectedCapabilityRequirements(contract, "catalog.addons."+addonID, selectedCapabilities, catalog); err != nil {
			return err
		}
		conflicts, err := stringListField(contract, "catalog.addons."+addonID, "conflicts", false)
		if err != nil {
			return err
		}
		if err := rejectSelectedConflicts("catalog.addons."+addonID, conflicts, selectedContracts); err != nil {
			return err
		}
	}
	for moduleID := range selectedModules {
		contract := catalog.modules[moduleID]
		if contract == nil {
			return fmt.Errorf("resolvedPlan.modules.%s has no bound module body", moduleID)
		}
		requires, err := stringListField(contract, "catalog.modules."+moduleID, "requires", false)
		if err != nil {
			return err
		}
		for _, dependencyID := range requires {
			if _, selected := selectedModules[dependencyID]; !selected {
				return fmt.Errorf("resolvedPlan.modules.%s omits bound dependency %q", moduleID, dependencyID)
			}
		}
	}
	if err := detectModuleCycles(selectedModuleProviders, catalog); err != nil {
		return fmt.Errorf("resolvedPlan.modules contains a dependency cycle rejected by the compiler: %w", err)
	}
	return nil
}

func requireSelectedCapabilityRequirements(contract map[string]any, path string, selected map[string]struct{}, catalog *indexedCatalog) error {
	required, err := requirements(contract, path)
	if err != nil {
		return err
	}
	for _, requirement := range required {
		_, exists := selected[requirement.id]
		if exists {
			if err := validateRequirementVersion(requirement, path+".requires", catalog); err != nil {
				return err
			}
		}
		if requirement.optional {
			continue
		}
		if !exists {
			return fmt.Errorf("%s requires selected capability %q", path, requirement.id)
		}
	}
	return nil
}

func rejectSelectedConflicts(path string, conflicts []string, selected map[string]struct{}) error {
	for _, conflict := range conflicts {
		if _, exists := selected[conflict]; exists {
			return fmt.Errorf("%s conflicts with selected contract %q", path, conflict)
		}
	}
	return nil
}

func setsIntersect(selected map[string]struct{}, candidates []string) bool {
	for _, candidate := range candidates {
		if _, exists := selected[candidate]; exists {
			return true
		}
	}
	return false
}

func resolvedCapabilityProviders(plan ResolvedPlan, catalog *indexedCatalog) (map[string]string, error) {
	values, err := objectListField(map[string]any(plan), "resolvedPlan", "capabilities")
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(values))
	for index, value := range values {
		path := fmt.Sprintf("resolvedPlan.capabilities[%d]", index)
		id, err := stringField(value, path, "id")
		if err != nil {
			return nil, err
		}
		if _, exists := catalog.capabilities[id]; !exists {
			return nil, fmt.Errorf("%s id %q has no bound catalog body", path, id)
		}
		if err := requireCatalogOptionalObjectField(value, catalog.capabilities[id], path, "tlsProfile"); err != nil {
			return nil, err
		}
		providerRef, err := stringField(value, path, "providerRef")
		if err != nil {
			return nil, err
		}
		provider, exists := catalog.providers[providerRef]
		if !exists {
			return nil, fmt.Errorf("%s providerRef %q has no bound provider body", path, providerRef)
		}
		provides, err := stringListField(provider, "catalog.providers."+providerRef, "provides", true)
		if err != nil {
			return nil, err
		}
		if !contains(provides, id) {
			return nil, fmt.Errorf("%s providerRef %q does not provide %q in the bound catalog", path, providerRef, id)
		}
		result[id] = providerRef
	}
	if err := validateResolvedCapabilityProviderEligibility(plan, catalog, result); err != nil {
		return nil, err
	}
	return result, nil
}

func validateResolvedCapabilityProviderEligibility(plan ResolvedPlan, catalog *indexedCatalog, selections map[string]string) error {
	sites, err := objectListField(map[string]any(plan), "resolvedPlan", "sites")
	if err != nil {
		return err
	}
	topologyKinds := make(map[string]struct{}, len(sites))
	for index, site := range sites {
		kind, err := stringField(site, fmt.Sprintf("resolvedPlan.sites[%d]", index), "kind")
		if err != nil {
			return err
		}
		topologyKinds[kind] = struct{}{}
	}
	for _, capabilityID := range mapKeys(selections) {
		contract := catalog.capabilities[capabilityID]
		supportedKinds, err := stringListField(contract, "catalog.capabilities."+capabilityID, "supportedSiteKinds", true)
		if err != nil {
			return err
		}
		var requiredSiteKinds []string
		for kind := range topologyKinds {
			if contains(supportedKinds, kind) {
				requiredSiteKinds = append(requiredSiteKinds, kind)
			}
		}
		sort.Strings(requiredSiteKinds)
		if len(requiredSiteKinds) == 0 {
			return fmt.Errorf("resolved capability %q supports none of the persisted topology site kinds", capabilityID)
		}
		candidates, err := catalog.providerCandidates(capabilityID, requiredSiteKinds)
		if err != nil {
			return err
		}
		providerID := selections[capabilityID]
		if !contains(candidates, providerID) {
			return fmt.Errorf("resolved capability %q selects provider %q, which cannot realize every required site kind %v", capabilityID, providerID, requiredSiteKinds)
		}
	}
	return nil
}

//nolint:gocyclo // Provider-body validation keeps every resolved provider field bound to its catalog authority.
type resolvedWorkloadPlacement struct {
	siteRefs []string
	nodeRefs []string
}

func validateResolvedWorkloadBodies(plan ResolvedPlan, catalog *indexedCatalog) (map[string]string, map[string]string, map[string]resolvedWorkloadPlacement, error) {
	values, err := objectListField(map[string]any(plan), "resolvedPlan", "workloads")
	if err != nil {
		return nil, nil, nil, err
	}
	providers := make(map[string]string, len(values))
	modules := make(map[string]string, len(values))
	placements := make(map[string]resolvedWorkloadPlacement, len(values))
	nodeSites, nodeKinds, enabledNodes, err := resolvedTopologyIndex(plan)
	if err != nil {
		return nil, nil, nil, err
	}
	resolvedModules, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return nil, nil, nil, err
	}
	modulesByID, err := indexObjectsByID(resolvedModules, "resolvedPlan.modules")
	if err != nil {
		return nil, nil, nil, err
	}
	for index, value := range values {
		path := fmt.Sprintf("resolvedPlan.workloads[%d]", index)
		id, err := stringField(value, path, "id")
		if err != nil {
			return nil, nil, nil, err
		}
		contract := catalog.workloads[id]
		if contract == nil {
			return nil, nil, nil, fmt.Errorf("%s id %q has no bound workload body", path, id)
		}
		version, err := metadataVersion(contract, "catalog.workloads."+id)
		if err != nil {
			return nil, nil, nil, err
		}
		haveVersion, err := stringField(value, path, "version")
		if err != nil || haveVersion != version {
			return nil, nil, nil, fmt.Errorf("%s.version does not match the bound workload body", path)
		}
		wantHash, err := canonicalHash(contract, true)
		if err != nil {
			return nil, nil, nil, err
		}
		haveHash, err := stringField(value, path, "contractHash")
		if err != nil || haveHash != wantHash {
			return nil, nil, nil, fmt.Errorf("%s.contractHash does not match the bound workload body", path)
		}
		for _, field := range []string{"kind", "functionalCapabilities", "dataClasses"} {
			if err := requireCatalogField(value, contract, path, field); err != nil {
				return nil, nil, nil, err
			}
		}
		resolvedAlternative, err := objectField(value, path, "alternative")
		if err != nil {
			return nil, nil, nil, err
		}
		alternativeID, err := stringField(resolvedAlternative, path+".alternative", "id")
		if err != nil {
			return nil, nil, nil, err
		}
		alternative, err := workloadAlternative(contract, id, alternativeID)
		if err != nil {
			return nil, nil, nil, err
		}
		wantAlternativeHash, err := canonicalHash(alternative, true)
		if err != nil {
			return nil, nil, nil, err
		}
		haveAlternativeHash, err := stringField(resolvedAlternative, path+".alternative", "contractHash")
		if err != nil || haveAlternativeHash != wantAlternativeHash {
			return nil, nil, nil, fmt.Errorf("%s.alternative.contractHash does not match the bound alternative body", path)
		}
		for _, field := range []string{"providerRef", "moduleRef"} {
			if err := requireCatalogField(resolvedAlternative, alternative, path+".alternative", field); err != nil {
				return nil, nil, nil, err
			}
		}
		for _, field := range []string{"route", "setup"} {
			if err := requireCatalogObjectField(resolvedAlternative, alternative, path+".alternative", field); err != nil {
				return nil, nil, nil, err
			}
		}
		if err := validateResolvedWorkloadRuntimeAdapter(resolvedAlternative, alternative, catalog, path+".alternative"); err != nil {
			return nil, nil, nil, err
		}
		inputs, err := objectField(alternative, path+".alternative", "inputs")
		if err != nil {
			return nil, nil, nil, err
		}
		settingsContract, err := objectField(inputs, path+".alternative.inputs", "settings")
		if err != nil {
			return nil, nil, nil, err
		}
		secretContract, err := objectField(inputs, path+".alternative.inputs", "secretInputs")
		if err != nil {
			return nil, nil, nil, err
		}
		settings, _, err := optionalObjectField(value, path, "settings")
		if err != nil {
			return nil, nil, nil, err
		}
		secretRefs, _, err := optionalObjectField(value, path, "secretRefs")
		if err != nil {
			return nil, nil, nil, err
		}
		if err := validateWorkloadInputMap(settings, settingsContract, path+".settings"); err != nil {
			return nil, nil, nil, err
		}
		if err := validateWorkloadInputMap(secretRefs, secretContract, path+".secretRefs"); err != nil {
			return nil, nil, nil, err
		}
		providerID, err := stringField(resolvedAlternative, path+".alternative", "providerRef")
		if err != nil {
			return nil, nil, nil, err
		}
		moduleID, err := stringField(resolvedAlternative, path+".alternative", "moduleRef")
		if err != nil {
			return nil, nil, nil, err
		}
		if owner, duplicate := modules[moduleID]; duplicate && owner != id {
			return nil, nil, nil, fmt.Errorf("%s module %q is already bound to workload %q", path, moduleID, owner)
		}
		siteRefs, err := stringListField(value, path, "siteRefs", true)
		if err != nil {
			return nil, nil, nil, err
		}
		nodeRefs, err := stringListField(value, path, "nodeRefs", true)
		if err != nil {
			return nil, nil, nil, err
		}
		supportedKinds, err := stringListField(contract, "catalog.workloads."+id, "supportedSiteKinds", true)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, nodeRef := range nodeRefs {
			if !contains(enabledNodes, nodeRef) || !contains(siteRefs, nodeSites[nodeRef]) || !contains(supportedKinds, nodeKinds[nodeRef]) {
				return nil, nil, nil, fmt.Errorf("%s.nodeRefs contains node %q outside the bound workload placement envelope", path, nodeRef)
			}
		}
		resolvedModule := modulesByID[moduleID]
		if resolvedModule == nil {
			return nil, nil, nil, fmt.Errorf("%s alternative module %q is absent", path, moduleID)
		}
		moduleContract := catalog.modules[moduleID]
		if moduleContract == nil {
			return nil, nil, nil, fmt.Errorf("%s alternative module %q has no bound module body", path, moduleID)
		}
		if err := validateResolvedWorkloadInputFanout(resolvedModule, moduleContract, moduleID, settings, secretRefs, settingsContract, secretContract, path); err != nil {
			return nil, nil, nil, err
		}
		providers[id], modules[moduleID], placements[moduleID] = providerID, id, resolvedWorkloadPlacement{siteRefs: siteRefs, nodeRefs: nodeRefs}
	}
	return providers, modules, placements, nil
}

func validateResolvedWorkloadRuntimeAdapter(resolvedAlternative, alternative map[string]any, catalog *indexedCatalog, path string) error {
	resolvedRuntime, err := objectField(resolvedAlternative, path, "runtime")
	if err != nil {
		return err
	}
	contractRuntime, err := objectField(alternative, path, "runtime")
	if err != nil {
		return err
	}
	allowed, err := stringListField(contractRuntime, path+".runtime", "allowedAdapterRefs", false)
	if err != nil {
		return err
	}
	resolvedAdapter, exists, err := optionalObjectField(resolvedRuntime, path+".runtime", "adapter")
	if err != nil {
		return err
	}
	if len(allowed) == 0 {
		if exists {
			return fmt.Errorf("%s.runtime.adapter is not allowed by the bound workload alternative", path)
		}
		return nil
	}
	if !exists {
		return fmt.Errorf("%s.runtime.adapter is required by the bound workload alternative", path)
	}
	adapterID, err := stringField(resolvedAdapter, path+".runtime.adapter", "id")
	if err != nil || !contains(allowed, adapterID) {
		return fmt.Errorf("%s.runtime.adapter.id is not allowed by the bound workload alternative", path)
	}
	providerID, err := stringField(resolvedAdapter, path+".runtime.adapter", "providerRef")
	if err != nil {
		return err
	}
	provider := catalog.providers[providerID]
	if provider == nil {
		return fmt.Errorf("%s.runtime.adapter.providerRef has no bound provider body", path)
	}
	ownedRefs, err := stringListField(provider, "catalog.providers."+providerID, "runtimeAdapterRefs", false)
	if err != nil || !contains(ownedRefs, adapterID) {
		return fmt.Errorf("%s.runtime.adapter provider does not own adapter %q", path, adapterID)
	}
	providerVersion, err := metadataVersion(provider, "catalog.providers."+providerID)
	if err != nil {
		return err
	}
	providerHash, err := canonicalHash(provider, true)
	if err != nil {
		return err
	}
	if resolvedAdapter["providerVersion"] != providerVersion || resolvedAdapter["providerContractHash"] != providerHash {
		return fmt.Errorf("%s.runtime.adapter provider authority does not match the bound catalog body", path)
	}
	moduleID, err := stringField(resolvedAdapter, path+".runtime.adapter", "moduleRef")
	if err != nil {
		return err
	}
	module := catalog.modules[moduleID]
	if module == nil {
		return fmt.Errorf("%s.runtime.adapter.moduleRef has no bound module body", path)
	}
	declaredProvider, err := stringField(module, "catalog.modules."+moduleID, "providerRef")
	if err != nil || declaredProvider != providerID {
		return fmt.Errorf("%s.runtime.adapter module belongs to another provider", path)
	}
	moduleAdapter, err := objectField(module, "catalog.modules."+moduleID, "runtimeAdapter")
	if err != nil || moduleAdapter["id"] != adapterID {
		return fmt.Errorf("%s.runtime.adapter module does not implement adapter %q", path, adapterID)
	}
	moduleVersion, err := metadataVersion(module, "catalog.modules."+moduleID)
	if err != nil {
		return err
	}
	moduleHash, err := canonicalHash(module, true)
	if err != nil {
		return err
	}
	if resolvedAdapter["moduleVersion"] != moduleVersion || resolvedAdapter["moduleContractHash"] != moduleHash {
		return fmt.Errorf("%s.runtime.adapter module authority does not match the bound catalog body", path)
	}
	return nil
}

func resolvedWorkloadRuntimeAdapterOwners(plan ResolvedPlan) (map[string]string, map[string]string, error) {
	workloads, err := objectListField(map[string]any(plan), "resolvedPlan", "workloads")
	if err != nil {
		return nil, nil, err
	}
	providers := make(map[string]string)
	modules := make(map[string]string)
	for index, workload := range workloads {
		path := fmt.Sprintf("resolvedPlan.workloads[%d].alternative.runtime", index)
		alternative, err := objectField(workload, path, "alternative")
		if err != nil {
			return nil, nil, err
		}
		runtime, err := objectField(alternative, path, "runtime")
		if err != nil {
			return nil, nil, err
		}
		adapter, exists, err := optionalObjectField(runtime, path, "adapter")
		if err != nil {
			return nil, nil, err
		}
		if !exists {
			continue
		}
		adapterID, err := stringField(adapter, path+".adapter", "id")
		if err != nil {
			return nil, nil, err
		}
		providerID, err := stringField(adapter, path+".adapter", "providerRef")
		if err != nil {
			return nil, nil, err
		}
		moduleID, err := stringField(adapter, path+".adapter", "moduleRef")
		if err != nil {
			return nil, nil, err
		}
		if owner, exists := providers[adapterID]; exists && owner != providerID {
			return nil, nil, fmt.Errorf("%s.adapter %q resolves to conflicting providers", path, adapterID)
		}
		if owner, exists := modules[moduleID]; exists && owner != adapterID {
			return nil, nil, fmt.Errorf("%s.adapter module %q resolves to conflicting adapters", path, moduleID)
		}
		providers[adapterID] = providerID
		modules[moduleID] = adapterID
	}
	return providers, modules, nil
}

func validateResolvedWorkloadInputFanout(module, contract map[string]any, moduleID string, settings, secretRefs, settingsContract, secretContract map[string]any, workloadPath string) error {
	defaults, hasDefaults, err := optionalObjectField(contract, "catalog.modules."+moduleID, "inputDefaults")
	if err != nil {
		return err
	}
	if !hasDefaults {
		defaults = map[string]any{}
	}
	allowedSettings, err := stringListField(settingsContract, workloadPath+".alternative.inputs.settings", "allowedRefs", false)
	if err != nil {
		return err
	}
	allowedSecrets, err := stringListField(secretContract, workloadPath+".alternative.inputs.secretInputs", "allowedRefs", false)
	if err != nil {
		return err
	}
	moduleValues := make(map[string]any, len(defaults)+len(settings))
	for key, value := range defaults {
		if !contains(allowedSettings, key) {
			return fmt.Errorf("catalog.modules.%s.inputDefaults.%s is not governed by %s.alternative inputs", moduleID, key, workloadPath)
		}
		moduleValues[key] = value
	}
	for key, value := range settings {
		moduleValues[key] = value
	}
	units, err := objectListField(module, "resolvedPlan.modules."+moduleID, "renderUnits")
	if err != nil {
		return err
	}
	for index, unit := range units {
		unitPath := fmt.Sprintf("resolvedPlan.modules.%s.renderUnits[%d]", moduleID, index)
		publicRefs, err := stringListField(unit, unitPath, "publicInputRefs", false)
		if err != nil {
			return err
		}
		expectedValues := map[string]any{}
		for _, key := range publicRefs {
			if !contains(allowedSettings, key) {
				return fmt.Errorf("%s.%s is not governed by %s.alternative inputs", unitPath+".publicInputRefs", key, workloadPath)
			}
			if value, exists := moduleValues[key]; exists {
				expectedValues[key] = value
			}
		}
		values, _, err := optionalObjectField(unit, unitPath, "values")
		if err != nil {
			return err
		}
		equal, err := canonicalEqual(values, expectedValues)
		if err != nil {
			return err
		}
		if !equal {
			return fmt.Errorf("%s.values is not the exact workload-governed public input projection", unitPath)
		}
		declaredSecrets, err := stringListField(unit, unitPath, "secretInputRefs", false)
		if err != nil {
			return err
		}
		resolvedSecrets, _, err := optionalObjectField(unit, unitPath, "secretRefs")
		if err != nil {
			return err
		}
		expectedSecrets := map[string]any{}
		for _, key := range declaredSecrets {
			if !contains(allowedSecrets, key) {
				return fmt.Errorf("%s.%s is not governed by %s.alternative inputs", unitPath+".secretInputRefs", key, workloadPath)
			}
			if value, exists := secretRefs[key]; exists {
				expectedSecrets[key] = value
			}
		}
		equal, err = canonicalEqual(resolvedSecrets, expectedSecrets)
		if err != nil {
			return err
		}
		if !equal {
			return fmt.Errorf("%s.secretRefs is not the exact workload-governed secret input projection", unitPath)
		}
	}
	return nil
}

func validateResolvedProviderBodies(plan ResolvedPlan, catalog *indexedCatalog, capabilityProviders, workloadProviders, runtimeAdapterProviders map[string]string) error {
	capabilityValues, err := objectListField(map[string]any(plan), "resolvedPlan", "capabilities")
	if err != nil {
		return err
	}
	capabilities, err := indexObjectsByID(capabilityValues, "resolvedPlan.capabilities")
	if err != nil {
		return err
	}
	values, err := objectListField(map[string]any(plan), "resolvedPlan", "providers")
	if err != nil {
		return err
	}
	actual := make(map[string]map[string]any, len(values))
	for index, value := range values {
		path := fmt.Sprintf("resolvedPlan.providers[%d]", index)
		id, err := stringField(value, path, "id")
		if err != nil {
			return err
		}
		if _, duplicate := actual[id]; duplicate {
			return fmt.Errorf("%s duplicates provider %q", path, id)
		}
		actual[id] = value
	}
	expectedIDs := make(map[string]struct{})
	for _, providerRef := range capabilityProviders {
		expectedIDs[providerRef] = struct{}{}
	}
	for _, providerRef := range workloadProviders {
		expectedIDs[providerRef] = struct{}{}
	}
	for _, providerRef := range runtimeAdapterProviders {
		expectedIDs[providerRef] = struct{}{}
	}
	if !sameStringSet(mapKeys(actual), mapKeys(expectedIDs)) {
		return fmt.Errorf("resolvedPlan.providers is not the exact provider set selected by resolved capabilities, workloads, and runtime adapters")
	}

	nodeSites, nodeKinds, enabledNodes, err := resolvedTopologyIndex(plan)
	if err != nil {
		return err
	}
	haProviderRef, haProviderSites, _, err := resolvedHAProviderPlacement(plan)
	if err != nil {
		return err
	}
	for _, id := range mapKeys(actual) {
		provider := actual[id]
		contract := catalog.providers[id]
		if contract == nil {
			return fmt.Errorf("resolvedPlan.providers.%s has no bound catalog body", id)
		}
		path := "resolvedPlan.providers." + id
		if err := requireCatalogObjectField(provider, contract, path, "realization"); err != nil {
			return err
		}
		if err := requireCatalogField(provider, contract, path, "certificateIssuers"); err != nil {
			return err
		}
		if err := requireCatalogField(provider, contract, path, "overlayContracts"); err != nil {
			return err
		}
		if err := requireCatalogField(provider, contract, path, "remoteActionContracts"); err != nil {
			return err
		}
		wantProvides := capabilitiesForProvider(capabilityProviders, id)
		haveProvides, err := stringListField(provider, path, "provides", true)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(haveProvides, wantProvides) {
			return fmt.Errorf("%s.provides is not the exact selected capability projection", path)
		}
		var wantWorkloads []string
		for workloadID, providerRef := range workloadProviders {
			if providerRef == id {
				wantWorkloads = append(wantWorkloads, workloadID)
			}
		}
		wantWorkloads = sortStringsUnique(wantWorkloads)
		haveWorkloads, err := stringListField(provider, path, "workloadRefs", false)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(haveWorkloads, wantWorkloads) {
			return fmt.Errorf("%s.workloadRefs is not the exact selected workload projection", path)
		}
		var wantAdapters []string
		for adapterID, providerRef := range runtimeAdapterProviders {
			if providerRef == id {
				wantAdapters = append(wantAdapters, adapterID)
			}
		}
		wantAdapters = sortStringsUnique(wantAdapters)
		haveAdapters, err := stringListField(provider, path, "runtimeAdapterRefs", false)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(haveAdapters, wantAdapters) {
			return fmt.Errorf("%s.runtimeAdapterRefs is not the exact selected adapter projection", path)
		}
		supportedKinds, err := stringListField(contract, "catalog.providers."+id, "supportedSiteKinds", true)
		if err != nil {
			return err
		}
		wantSites := eligibleTopologySites(enabledNodes, nodeSites, nodeKinds, supportedKinds)
		if id == haProviderRef {
			wantSites = haProviderSites
		}
		haveSites, err := stringListField(provider, path, "siteRefs", true)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(haveSites, wantSites) {
			return fmt.Errorf("%s.siteRefs is not the exact bound provider placement projection", path)
		}
		if err := validateResolvedProviderOwnerBody(provider, contract, capabilities, capabilityProviders, id, path); err != nil {
			return err
		}
	}
	return nil
}

func resolvedHAProviderPlacement(plan ResolvedPlan) (string, []string, []string, error) {
	availability, err := objectField(map[string]any(plan), "resolvedPlan", "availability")
	if err != nil {
		return "", nil, nil, err
	}
	enabled, err := boolFieldDefault(availability, "resolvedPlan.availability", "enabled", false)
	if err != nil || !enabled {
		return "", nil, nil, err
	}
	providerRef, err := stringField(availability, "resolvedPlan.availability", "providerRef")
	if err != nil {
		return "", nil, nil, err
	}
	members, err := objectListField(availability, "resolvedPlan.availability", "selectedMembers")
	if err != nil {
		return "", nil, nil, err
	}
	siteRefs := make([]string, 0, len(members))
	nodeRefs := make([]string, 0, len(members))
	for index, member := range members {
		path := fmt.Sprintf("resolvedPlan.availability.selectedMembers[%d]", index)
		siteRef, err := stringField(member, path, "siteRef")
		if err != nil {
			return "", nil, nil, err
		}
		nodeRef, err := stringField(member, path, "nodeRef")
		if err != nil {
			return "", nil, nil, err
		}
		siteRefs = append(siteRefs, siteRef)
		nodeRefs = append(nodeRefs, nodeRef)
	}
	return providerRef, sortStringsUnique(siteRefs), sortStringsUnique(nodeRefs), nil
}

func validateResolvedProviderOwnerBody(provider, contract map[string]any, capabilities map[string]map[string]any, capabilityProviders map[string]string, providerID, path string) error {
	realization, err := objectField(contract, "catalog.providers", "realization")
	if err != nil {
		return err
	}
	kind, err := stringField(realization, "catalog.providers.realization", "kind")
	if err != nil {
		return err
	}
	owner, hasOwner, err := optionalObjectField(provider, path, "owner")
	if err != nil {
		return err
	}
	if kind != "host" && kind != "external" {
		if hasOwner {
			return fmt.Errorf("%s.owner is forbidden by bound provider realization %q", path, kind)
		}
		return nil
	}
	if !hasOwner {
		return fmt.Errorf("%s.owner is required by bound provider realization %q", path, kind)
	}
	wantRef, err := stringField(realization, "catalog.providers.realization", "ownerRef")
	if err != nil {
		return err
	}
	for field, want := range map[string]string{"ref": wantRef, "kind": kind} {
		have, err := stringField(owner, path+".owner", field)
		if err != nil {
			return err
		}
		if have != want {
			return fmt.Errorf("%s.owner.%s does not match the bound provider body", path, field)
		}
	}
	wantSupport, err := objectField(realization, "catalog.providers.realization", "realizationSupport")
	if err != nil {
		return err
	}
	haveSupport, err := objectField(owner, path+".owner", "realizationSupport")
	if err != nil {
		return err
	}
	if equal, err := canonicalEqual(haveSupport, wantSupport); err != nil {
		return err
	} else if !equal {
		return fmt.Errorf("%s.owner.realizationSupport does not match the bound provider body", path)
	}
	wantInputs, err := expectedProviderOwnerInputs(realization, capabilities, capabilityProviders, providerID)
	if err != nil {
		return err
	}
	haveInputs, err := objectField(owner, path+".owner", "inputs")
	if err != nil {
		return err
	}
	if equal, err := canonicalEqual(haveInputs, wantInputs); err != nil {
		return err
	} else if !equal {
		return fmt.Errorf("%s.owner.inputs is not the exact projection of bound inputBindings and resolved capability inputs", path)
	}
	return nil
}

func expectedProviderOwnerInputs(realization map[string]any, capabilities map[string]map[string]any, capabilityProviders map[string]string, providerID string) (map[string]any, error) {
	values := map[string]any{}
	secretRefs := map[string]any{}
	bindings, exists, err := optionalObjectField(realization, "catalog.providers.realization", "inputBindings")
	if err != nil || !exists {
		return map[string]any{"values": values, "secretRefs": secretRefs}, err
	}
	for _, inputRef := range sortedStringMapKeys(bindings) {
		binding, err := asObject(bindings[inputRef], "catalog.providers.realization.inputBindings."+inputRef)
		if err != nil {
			return nil, err
		}
		capabilityRef, err := stringField(binding, "catalog.providers.realization.inputBindings."+inputRef, "capabilityRef")
		if err != nil {
			return nil, err
		}
		capability, exists := capabilities[capabilityRef]
		if !exists {
			return nil, fmt.Errorf("bound provider input %q references unselected capability %q", inputRef, capabilityRef)
		}
		if selectedProvider := capabilityProviders[capabilityRef]; selectedProvider != providerID {
			return nil, fmt.Errorf("bound provider input %q references capability %q selected through provider %q, not owner %q", inputRef, capabilityRef, selectedProvider, providerID)
		}
		key, err := stringField(binding, "catalog.providers.realization.inputBindings."+inputRef, "key")
		if err != nil {
			return nil, err
		}
		source, err := stringField(binding, "catalog.providers.realization.inputBindings."+inputRef, "source")
		if err != nil {
			return nil, err
		}
		switch source {
		case "capability-setting":
			settings, hasSettings, err := optionalObjectField(capability, "resolvedPlan.capabilities."+capabilityRef, "settings")
			if err != nil {
				return nil, err
			}
			if hasSettings {
				if value, present := settings[key]; present {
					clone, err := cloneObject(map[string]any{"value": value}, false)
					if err != nil {
						return nil, err
					}
					values[inputRef] = clone["value"]
				}
			}
		case "capability-secret":
			refs, hasRefs, err := optionalObjectField(capability, "resolvedPlan.capabilities."+capabilityRef, "secretRefs")
			if err != nil {
				return nil, err
			}
			if hasRefs {
				if value, present := refs[key]; present {
					secretRefs[inputRef] = value
				}
			}
		default:
			return nil, fmt.Errorf("bound provider input %q uses unsupported source %q", inputRef, source)
		}
	}
	return map[string]any{"values": values, "secretRefs": secretRefs}, nil
}

//nolint:gocyclo // Module-body validation exhaustively binds placement, inputs, render units, and selected capabilities.
func validateResolvedModuleBodies(plan ResolvedPlan, catalog *indexedCatalog, capabilityProviders, workloadProviders, runtimeAdapterProviders, workloadModules, runtimeAdapterModules map[string]string, workloadPlacements map[string]resolvedWorkloadPlacement) error {
	values, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	actual := make(map[string]map[string]any, len(values))
	for index, value := range values {
		path := fmt.Sprintf("resolvedPlan.modules[%d]", index)
		id, err := stringField(value, path, "id")
		if err != nil {
			return err
		}
		if _, duplicate := actual[id]; duplicate {
			return fmt.Errorf("%s duplicates module %q", path, id)
		}
		actual[id] = value
	}

	selectedProviders := make(map[string]struct{})
	for _, providerRef := range capabilityProviders {
		selectedProviders[providerRef] = struct{}{}
	}
	for _, providerRef := range workloadProviders {
		selectedProviders[providerRef] = struct{}{}
	}
	for _, providerRef := range runtimeAdapterProviders {
		selectedProviders[providerRef] = struct{}{}
	}
	requiredModules := make(map[string]struct{})
	allowedModules := make(map[string]struct{})
	for providerID := range selectedProviders {
		realization, err := objectField(catalog.providers[providerID], "catalog.providers."+providerID, "realization")
		if err != nil {
			return err
		}
		moduleRefs, err := objectField(realization, "catalog.providers."+providerID+".realization", "moduleRefs")
		if err != nil {
			return err
		}
		for _, field := range []string{"required", "optional"} {
			ids, err := stringListField(moduleRefs, "catalog.providers."+providerID+".realization.moduleRefs", field, false)
			if err != nil {
				return err
			}
			for _, id := range ids {
				allowedModules[id] = struct{}{}
				if field == "required" {
					requiredModules[id] = struct{}{}
				}
			}
		}
	}
	for id := range requiredModules {
		if _, exists := actual[id]; !exists {
			return fmt.Errorf("resolvedPlan.modules omits required bound module %q", id)
		}
	}
	for moduleID := range workloadModules {
		if _, exists := actual[moduleID]; !exists {
			return fmt.Errorf("resolvedPlan.modules omits workload-selected module %q", moduleID)
		}
	}
	for moduleID := range runtimeAdapterModules {
		if _, exists := actual[moduleID]; !exists {
			return fmt.Errorf("resolvedPlan.modules omits runtime-adapter-selected module %q", moduleID)
		}
	}
	for id := range actual {
		if _, allowed := allowedModules[id]; !allowed {
			return fmt.Errorf("resolvedPlan.modules.%s is not selected by a bound provider module contract", id)
		}
	}

	_, nodeKinds, enabledNodes, err := resolvedTopologyIndex(plan)
	if err != nil {
		return err
	}
	moduleTargetSpec, err := resolvedModuleTargetSpec(plan)
	if err != nil {
		return err
	}
	for _, id := range mapKeys(actual) {
		module := actual[id]
		contract := catalog.modules[id]
		if contract == nil {
			return fmt.Errorf("resolvedPlan.modules.%s has no bound catalog body", id)
		}
		path := "resolvedPlan.modules." + id
		providerRef, err := stringField(contract, "catalog.modules."+id, "providerRef")
		if err != nil {
			return err
		}
		haveProviderRef, err := stringField(module, path, "providerRef")
		if err != nil {
			return err
		}
		if haveProviderRef != providerRef {
			return fmt.Errorf("%s.providerRef does not match the bound module body", path)
		}
		if err := requireCatalogField(module, contract, path, "role"); err != nil {
			return err
		}
		if err := requireCatalogObjectField(module, contract, path, "runtime"); err != nil {
			return err
		}
		if err := requireCatalogObjectField(module, contract, path, "realizationSupport"); err != nil {
			return err
		}
		for _, field := range []string{"nodeSelection", "runtimeRequirements", "enforcementRequirement", "runtimeOwnerRequirement", "runtimeAdapter", "runtimeAdapterAgent"} {
			if err := requireCatalogOptionalObjectField(module, contract, path, field); err != nil {
				return err
			}
		}
		wantRequires, err := stringListField(contract, "catalog.modules."+id, "requires", false)
		if err != nil {
			return err
		}
		haveRequires, err := stringListField(module, path, "requires", false)
		if err != nil {
			return err
		}
		haveRequiresUnique := sortStringsUnique(haveRequires)
		if len(haveRequires) != len(haveRequiresUnique) || !reflect.DeepEqual(haveRequiresUnique, sortStringsUnique(wantRequires)) {
			return fmt.Errorf("%s.requires does not match the bound module body", path)
		}
		wantProvides, err := selectedModuleCapabilities(contract, providerRef, capabilityProviders)
		if err != nil {
			return err
		}
		haveProvides, err := stringListField(module, path, "provides", true)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(haveProvides, wantProvides) {
			return fmt.Errorf("%s.provides is not the exact selected capability projection", path)
		}
		providerKinds, err := stringListField(catalog.providers[providerRef], "catalog.providers."+providerRef, "supportedSiteKinds", true)
		if err != nil {
			return err
		}
		providerNodes := map[string][]string{providerRef: eligibleNodesForKinds(enabledNodes, nodeKinds, providerKinds)}
		wantSites, wantNodes, err := resolveModuleTargetsWithInventoryAttestation(id, providerRef, contract, moduleTargetSpec, providerNodes, false)
		if err != nil {
			return fmt.Errorf("%s placement cannot be reconstructed from its bound contract: %w", path, err)
		}
		if placement, isWorkloadModule := workloadPlacements[id]; isWorkloadModule {
			wantSites, wantNodes = placement.siteRefs, placement.nodeRefs
		}
		haveNodes, err := stringListField(module, path, "nodeRefs", true)
		if err != nil {
			return err
		}
		haveSites, err := stringListField(module, path, "siteRefs", true)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(haveNodes, wantNodes) || !reflect.DeepEqual(haveSites, wantSites) {
			return fmt.Errorf("%s site/node placement is not the exact bound module projection", path)
		}
		if err := validateResolvedRenderUnitBodies(module, contract, path); err != nil {
			return err
		}
		if err := validateResolvedModuleInputProjection(module, contract, path); err != nil {
			return err
		}
		if err := validateResolvedModuleSecretInputProjection(plan, module, path); err != nil {
			return err
		}
		if err := validateResolvedModulePlanInputProjection(plan, module, path); err != nil {
			return err
		}
	}
	return nil
}

func validateResolvedModuleCoverage(plan ResolvedPlan, catalog *indexedCatalog, capabilityProviders map[string]string) error {
	moduleValues, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	for _, capabilityID := range mapKeys(capabilityProviders) {
		providerID := capabilityProviders[capabilityID]
		realization, err := objectField(catalog.providers[providerID], "catalog.providers."+providerID, "realization")
		if err != nil {
			return err
		}
		kind, err := stringField(realization, "catalog.providers."+providerID+".realization", "kind")
		if err != nil {
			return err
		}
		switch kind {
		case "host", "external":
			ownerRef, err := stringField(realization, "catalog.providers."+providerID+".realization", "ownerRef")
			if err != nil {
				return err
			}
			if ownerRef != providerID {
				return fmt.Errorf("resolved capability %q selects provider %q without its exact owner contract", capabilityID, providerID)
			}
			continue
		case "contract":
			continue
		case "topology":
			if err := validateTopologyProviderRealization(providerID, catalog.providers[providerID], realization); err != nil {
				return err
			}
			continue
		case "none":
			return fmt.Errorf("resolved capability %q selects provider %q without an approved realization", capabilityID, providerID)
		case "modules":
		default:
			return fmt.Errorf("catalog.providers.%s.realization.kind %q is unsupported", providerID, kind)
		}

		covered := false
		for index, module := range moduleValues {
			path := fmt.Sprintf("resolvedPlan.modules[%d]", index)
			moduleProvider, err := stringField(module, path, "providerRef")
			if err != nil {
				return err
			}
			if moduleProvider != providerID {
				continue
			}
			provides, err := stringListField(module, path, "provides", true)
			if err != nil {
				return err
			}
			if contains(provides, capabilityID) {
				covered = true
				break
			}
		}
		if !covered {
			return fmt.Errorf("resolved capability %q has no selected bound module covering provider %q", capabilityID, providerID)
		}
	}
	return nil
}

//nolint:gocyclo // Input projection intentionally checks presence, classification, defaults, and exact catalog equality together.
func validateResolvedModuleInputProjection(module, contract map[string]any, path string) error {
	defaults, hasDefaults, err := optionalObjectField(contract, "catalog.modules", "inputDefaults")
	if err != nil {
		return err
	}
	if !hasDefaults {
		defaults = map[string]any{}
	}
	units, err := objectListField(module, path, "renderUnits")
	if err != nil {
		return err
	}
	type resolvedUnitInputs struct {
		id         string
		publicRefs []string
		secretRefs []string
		values     map[string]any
		secrets    map[string]any
	}
	resolvedUnits := make([]resolvedUnitInputs, 0, len(units))
	publicValues := map[string]any{}
	secretValues := map[string]any{}
	publicPresent := map[string]bool{}
	for index, unit := range units {
		unitPath := fmt.Sprintf("%s.renderUnits[%d]", path, index)
		id, err := stringField(unit, unitPath, "id")
		if err != nil {
			return err
		}
		publicRefs, err := stringListField(unit, unitPath, "publicInputRefs", false)
		if err != nil {
			return err
		}
		secretRefs, err := stringListField(unit, unitPath, "secretInputRefs", false)
		if err != nil {
			return err
		}
		values, err := objectField(unit, unitPath, "values")
		if err != nil {
			return err
		}
		secrets, err := objectField(unit, unitPath, "secretRefs")
		if err != nil {
			return err
		}
		resolvedUnits = append(resolvedUnits, resolvedUnitInputs{id: id, publicRefs: publicRefs, secretRefs: secretRefs, values: values, secrets: secrets})
		for key, value := range values {
			publicPresent[key] = true
			if first, exists := publicValues[key]; exists {
				equal, err := canonicalEqual(first, value)
				if err != nil {
					return err
				}
				if !equal {
					return fmt.Errorf("%s public input %q resolves differently across render units", path, key)
				}
			} else {
				publicValues[key] = value
			}
		}
		for key, value := range secrets {
			if first, exists := secretValues[key]; exists {
				equal, err := canonicalEqual(first, value)
				if err != nil {
					return err
				}
				if !equal {
					return fmt.Errorf("%s secret input %q resolves differently across render units", path, key)
				}
			} else {
				secretValues[key] = value
			}
		}
	}
	for _, unit := range resolvedUnits {
		for _, inputRef := range unit.publicRefs {
			_, defaulted := defaults[inputRef]
			if !defaulted && !publicPresent[inputRef] {
				continue
			}
			if _, exists := unit.values[inputRef]; !exists {
				return fmt.Errorf("%s.renderUnits.%s.values omits module-level public input %q", path, unit.id, inputRef)
			}
		}
		for _, inputRef := range unit.secretRefs {
			if _, exists := unit.secrets[inputRef]; !exists {
				return fmt.Errorf("%s.renderUnits.%s.secretRefs omits required module-level secret input %q", path, unit.id, inputRef)
			}
		}
	}
	return nil
}

func validateResolvedRenderUnitBodies(module, contract map[string]any, path string) error {
	haveUnits, err := objectListField(module, path, "renderUnits")
	if err != nil {
		return err
	}
	wantUnits, err := objectListField(contract, "catalog.modules", "renderUnits")
	if err != nil {
		return err
	}
	haveByID, err := indexObjectsByID(haveUnits, path+".renderUnits")
	if err != nil {
		return err
	}
	wantByID, err := indexObjectsByID(wantUnits, "catalog.modules.renderUnits")
	if err != nil {
		return err
	}
	if !sameStringSet(mapKeys(haveByID), mapKeys(wantByID)) {
		return fmt.Errorf("%s.renderUnits is not the exact bound render-unit set", path)
	}
	for _, id := range mapKeys(haveByID) {
		have := haveByID[id]
		want := wantByID[id]
		unitPath := path + ".renderUnits." + id
		for _, field := range []string{"id", "kind", "rendererRef", "templateRef", "version", "contractHash"} {
			if !reflect.DeepEqual(have[field], want[field]) {
				return fmt.Errorf("%s.%s does not match the bound render-unit body", unitPath, field)
			}
		}
		for _, field := range []string{"publicInputRefs", "secretInputRefs", "planInputRefs", "outputs"} {
			haveList, err := stringListField(have, unitPath, field, false)
			if err != nil {
				return err
			}
			wantList, err := stringListField(want, "catalog.modules.renderUnits."+id, field, false)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(sortStringsUnique(haveList), sortStringsUnique(wantList)) {
				return fmt.Errorf("%s.%s does not match the bound render-unit body", unitPath, field)
			}
		}
		if equal, err := canonicalEqual(
			map[string]any{"inputBindings": have["inputBindings"], "secretInputBindings": have["secretInputBindings"]},
			map[string]any{"inputBindings": want["inputBindings"], "secretInputBindings": want["secretInputBindings"]},
		); err != nil {
			return err
		} else if !equal {
			return fmt.Errorf("%s input bindings do not match the bound render-unit body", unitPath)
		}
		if err := requireCatalogObjectField(have, want, unitPath, "placement"); err != nil {
			return err
		}
		if err := validateResolvedServiceEndpointBodies(have, want, unitPath); err != nil {
			return err
		}
		if err := validateResolvedProvidedInterfaceBodies(have, want, unitPath); err != nil {
			return err
		}
		if err := validateResolvedRequirementBodies(have, want, unitPath); err != nil {
			return err
		}
	}
	return nil
}

func validateResolvedModuleSecretInputProjection(plan ResolvedPlan, module map[string]any, path string) error {
	moduleID, err := stringField(module, path, "id")
	if err != nil {
		return err
	}
	providerRef, err := stringField(module, path, "providerRef")
	if err != nil {
		return err
	}
	rawCapabilities, err := objectListField(map[string]any(plan), "resolvedPlan", "capabilities")
	if err != nil {
		return err
	}
	capabilities, err := indexObjectsByID(rawCapabilities, "resolvedPlan.capabilities")
	if err != nil {
		return err
	}
	units, err := objectListField(module, path, "renderUnits")
	if err != nil {
		return err
	}
	for index, unit := range units {
		unitPath := fmt.Sprintf("%s.renderUnits[%d]", path, index)
		unitID, err := stringField(unit, unitPath, "id")
		if err != nil {
			return err
		}
		unitPath = path + ".renderUnits." + unitID
		bindings, err := moduleRenderSecretInputBindings(unit, unitPath)
		if err != nil {
			return err
		}
		secrets, err := objectField(unit, unitPath, "secretRefs")
		if err != nil {
			return err
		}
		for targetRef, binding := range bindings {
			capability, exists := capabilities[binding.capabilityRef]
			if !exists {
				return fmt.Errorf("%s.secretInputBindings.%s references absent capability %q", unitPath, targetRef, binding.capabilityRef)
			}
			capabilityProvider, err := stringField(capability, "resolvedPlan.capabilities."+binding.capabilityRef, "providerRef")
			if err != nil {
				return err
			}
			if capabilityProvider != providerRef {
				return fmt.Errorf("%s.secretInputBindings.%s capability is not owned by module provider %q", unitPath, targetRef, providerRef)
			}
			capabilitySecrets, exists, err := optionalObjectField(capability, "resolvedPlan.capabilities."+binding.capabilityRef, "secretRefs")
			if err != nil || !exists {
				return fmt.Errorf("%s.secretInputBindings.%s capability secret source is unavailable", unitPath, targetRef)
			}
			sourceSecret, sourceExists := capabilitySecrets[binding.key]
			targetSecret, targetExists := secrets[targetRef]
			if !sourceExists || !targetExists {
				return fmt.Errorf("%s.secretInputBindings.%s does not resolve an exact source and target", unitPath, targetRef)
			}
			equal, err := canonicalEqual(sourceSecret, targetSecret)
			if err != nil {
				return err
			}
			if !equal {
				return fmt.Errorf("%s.secretRefs.%s is not the exact capability secret projection for module %q", unitPath, targetRef, moduleID)
			}
		}
	}
	return nil
}

// validateResolvedModulePlanInputProjection reconstructs every compiler-owned
// plan input from the persisted plan. Re-hashing a plan after changing,
// omitting, or widening planInputs therefore cannot manufacture authority.
//
//nolint:gocyclo // Persisted-plan rebound validation exhaustively compares every governed module input projection.
func validateResolvedModulePlanInputProjection(plan ResolvedPlan, module map[string]any, path string) error {
	source, err := modulePlanInputSourceFromResolvedPlan(plan)
	if err != nil {
		return err
	}
	moduleID, err := stringField(module, path, "id")
	if err != nil {
		return err
	}
	units, err := objectListField(module, path, "renderUnits")
	if err != nil {
		return err
	}
	for index, unit := range units {
		unitPath := fmt.Sprintf("%s.renderUnits[%d]", path, index)
		unitID, err := stringField(unit, unitPath, "id")
		if err != nil {
			return err
		}
		unitPath = path + ".renderUnits." + unitID
		refs, err := stringListField(unit, unitPath, "planInputRefs", false)
		if err != nil {
			return err
		}
		if normalized := sortStringsUnique(refs); len(normalized) != len(refs) {
			return fmt.Errorf("%s.planInputRefs contains duplicate or empty refs", unitPath)
		}
		inputs, err := objectField(unit, unitPath, "planInputs")
		if err != nil {
			return err
		}
		if !sameStringSet(mapKeys(inputs), refs) {
			return fmt.Errorf("%s.planInputs is not the exact 1:1 planInputRefs projection", unitPath)
		}
		for _, ref := range refs {
			want, err := source.resolve(ref, moduleID, module)
			if err != nil {
				return fmt.Errorf("recompute %s.planInputs.%s: %w", unitPath, ref, err)
			}
			equal, err := canonicalEqual(inputs[ref], want)
			if err != nil {
				return err
			}
			if !equal {
				return fmt.Errorf("%s.planInputs.%s is not the exact compiler-derived projection", unitPath, ref)
			}
		}
		bindings, err := moduleRenderInputBindings(unit, unitPath)
		if err != nil {
			return err
		}
		if len(bindings) == 0 {
			continue
		}
		values, err := objectField(unit, unitPath, "values")
		if err != nil {
			return err
		}
		identity, err := objectField(map[string]any(plan), "resolvedPlan", "identity")
		if err != nil {
			return err
		}
		identityTrust, err := objectField(map[string]any(plan), "resolvedPlan", "identityTrust")
		if err != nil {
			return err
		}
		failurePolicy, err := objectField(map[string]any(plan), "resolvedPlan", "failurePolicy")
		if err != nil {
			return err
		}
		sites, err := objectListField(map[string]any(plan), "resolvedPlan", "sites")
		if err != nil {
			return err
		}
		stackID, err := stringField(map[string]any(plan), "resolvedPlan", "stackId")
		if err != nil {
			return err
		}
		network, err := objectField(map[string]any(plan), "resolvedPlan", "network")
		if err != nil {
			return err
		}
		gates, err := objectField(map[string]any(plan), "resolvedPlan", "gates")
		if err != nil {
			return err
		}
		install, err := objectField(map[string]any(plan), "resolvedPlan", "install")
		if err != nil {
			return err
		}
		system, err := objectField(map[string]any(plan), "resolvedPlan", "system")
		if err != nil {
			return err
		}
		storage, err := objectField(map[string]any(plan), "resolvedPlan", "storage")
		if err != nil {
			return err
		}
		kit, err := objectField(map[string]any(plan), "resolvedPlan", "kit")
		if err != nil {
			return err
		}
		bindingSource := moduleRenderInputSource{
			stackID: stackID, kit: kit, sites: objectMapsAsAny(sites),
			identity: identity, identityTrust: identityTrust, failurePolicy: failurePolicy, network: network, gates: gates,
			install: install, system: system, storage: storage,
		}
		for _, binding := range bindings {
			want, available, err := bindingSource.resolve(binding)
			if err != nil {
				return fmt.Errorf("recompute %s.values.%s: %w", unitPath, binding.targetRef, err)
			}
			if !available {
				if binding.required {
					return fmt.Errorf("recompute %s.values.%s: required source %s is unavailable", unitPath, binding.targetRef, binding.sourceRef)
				}
				want = binding.defaultValue
			}
			have, exists := values[binding.targetRef]
			if !exists {
				return fmt.Errorf("%s.values.%s omits compiler-bound public input", unitPath, binding.targetRef)
			}
			equal, err := canonicalEqual(have, want)
			if err != nil {
				return err
			}
			if !equal {
				return fmt.Errorf("%s.values.%s is not the exact compiler-derived input binding", unitPath, binding.targetRef)
			}
		}
	}
	return nil
}

func validateResolvedServiceEndpointBodies(haveUnit, wantUnit map[string]any, path string) error {
	return validateResolvedIndexedBodies(
		haveUnit,
		wantUnit,
		path,
		"serviceEndpoints",
		"service endpoint",
		indexServiceEndpointBodies,
	)
}

func indexServiceEndpointBodies(values []map[string]any, path string) (map[string]map[string]any, error) {
	result := make(map[string]map[string]any, len(values))
	for index, value := range values {
		serviceRef, err := stringField(value, fmt.Sprintf("%s[%d]", path, index), "serviceRef")
		if err != nil {
			return nil, err
		}
		if _, duplicate := result[serviceRef]; duplicate {
			return nil, fmt.Errorf("%s duplicates serviceRef %q", path, serviceRef)
		}
		result[serviceRef] = value
	}
	return result, nil
}

func validateResolvedProvidedInterfaceBodies(haveUnit, wantUnit map[string]any, path string) error {
	return validateResolvedIndexedBodies(
		haveUnit,
		wantUnit,
		path,
		"providesInterfaces",
		"provider-interface",
		indexObjectsByID,
	)
}

type resolvedBodyIndexer func([]map[string]any, string) (map[string]map[string]any, error)

func validateResolvedIndexedBodies(
	haveUnit, wantUnit map[string]any,
	path, field, bodyKind string,
	index resolvedBodyIndexer,
) error {
	have, err := objectListOptional(haveUnit, field)
	if err != nil {
		return err
	}
	want, err := objectListOptional(wantUnit, field)
	if err != nil {
		return err
	}
	haveByKey, err := index(have, path+"."+field)
	if err != nil {
		return err
	}
	wantByKey, err := index(want, "catalog.modules.renderUnits."+field)
	if err != nil {
		return err
	}
	if !sameStringSet(mapKeys(haveByKey), mapKeys(wantByKey)) {
		return fmt.Errorf("%s.%s is not the exact bound %s set", path, field, bodyKind)
	}
	for _, key := range mapKeys(haveByKey) {
		if equal, err := canonicalEqual(haveByKey[key], wantByKey[key]); err != nil {
			return err
		} else if !equal {
			return fmt.Errorf("%s.%s.%s does not match the bound %s body", path, field, key, bodyKind)
		}
	}
	return nil
}

func validateResolvedRequirementBodies(haveUnit, wantUnit map[string]any, path string) error {
	have, err := objectListOptional(haveUnit, "requiresInterfaces")
	if err != nil {
		return err
	}
	want, err := objectListOptional(wantUnit, "requiresInterfaces")
	if err != nil {
		return err
	}
	haveByID, err := indexObjectsByID(have, path+".requiresInterfaces")
	if err != nil {
		return err
	}
	wantByID, err := indexObjectsByID(want, "catalog.modules.renderUnits.requiresInterfaces")
	if err != nil {
		return err
	}
	if !sameStringSet(mapKeys(haveByID), mapKeys(wantByID)) {
		return fmt.Errorf("%s.requiresInterfaces is not the exact bound requirement set", path)
	}
	for _, id := range mapKeys(haveByID) {
		projection, err := cloneObject(haveByID[id], false)
		if err != nil {
			return err
		}
		delete(projection, "providerBindings")
		if equal, err := canonicalEqual(projection, wantByID[id]); err != nil {
			return err
		} else if !equal {
			return fmt.Errorf("%s.requiresInterfaces.%s does not match the bound requirement body", path, id)
		}
	}
	return nil
}

//nolint:gocyclo // Gate-body validation is the exhaustive fail-closed boundary for provider, module, health, and lifecycle gates.
func validateResolvedGateBodies(plan ResolvedPlan, catalog *indexedCatalog, capabilityProviders map[string]string) error {
	providerValues, err := objectListField(map[string]any(plan), "resolvedPlan", "providers")
	if err != nil {
		return err
	}
	providers, err := indexObjectsByID(providerValues, "resolvedPlan.providers")
	if err != nil {
		return err
	}
	moduleValues, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	modules, err := indexObjectsByID(moduleValues, "resolvedPlan.modules")
	if err != nil {
		return err
	}
	nodeSites, nodeKinds, enabledNodes, err := resolvedTopologyIndex(plan)
	if err != nil {
		return err
	}
	providerNodes := make(map[string][]string, len(providers))
	providerSites := make(map[string][]string, len(providers))
	haProviderRef, _, haProviderNodes, err := resolvedHAProviderPlacement(plan)
	if err != nil {
		return err
	}
	for id, provider := range providers {
		supportedKinds, err := stringListField(catalog.providers[id], "catalog.providers."+id, "supportedSiteKinds", true)
		if err != nil {
			return err
		}
		providerNodes[id] = eligibleNodesForKinds(enabledNodes, nodeKinds, supportedKinds)
		if id == haProviderRef {
			providerNodes[id] = haProviderNodes
		}
		providerSites[id], err = stringListField(provider, "resolvedPlan.providers."+id, "siteRefs", true)
		if err != nil {
			return err
		}
	}

	expectedHealth := map[string]map[string]any{}
	appendHealth := func(targetKind, targetRef string, contract map[string]any, siteRefs, nodeRefs []string) error {
		gates, err := materializeContractHealthGates(targetKind, targetRef, contract, siteRefs, nodeRefs, nodeSites)
		if err != nil {
			return err
		}
		for _, gate := range gates {
			gateID := gate["id"].(string)
			if _, duplicate := expectedHealth[gateID]; duplicate {
				return fmt.Errorf("bound catalog projects duplicate health gate %q", gateID)
			}
			expectedHealth[gateID] = gate
		}
		return nil
	}
	for _, capabilityID := range mapKeys(capabilityProviders) {
		providerID := capabilityProviders[capabilityID]
		if err := appendHealth("capability", capabilityID, catalog.capabilities[capabilityID], providerSites[providerID], providerNodes[providerID]); err != nil {
			return err
		}
	}
	for _, providerID := range mapKeys(providers) {
		if err := appendHealth("provider", providerID, catalog.providers[providerID], providerSites[providerID], providerNodes[providerID]); err != nil {
			return err
		}
	}
	for _, moduleID := range mapKeys(modules) {
		siteRefs, err := stringListField(modules[moduleID], "resolvedPlan.modules."+moduleID, "siteRefs", true)
		if err != nil {
			return err
		}
		nodeRefs, err := stringListField(modules[moduleID], "resolvedPlan.modules."+moduleID, "nodeRefs", true)
		if err != nil {
			return err
		}
		if err := appendHealth("module", moduleID, catalog.modules[moduleID], siteRefs, nodeRefs); err != nil {
			return err
		}
	}
	serviceEndpoints, err := indexResolvedServiceEndpoints(objectMapsAsAny(moduleValues))
	if err != nil {
		return err
	}
	if err := bindServiceEndpointHealthContracts(serviceEndpoints, catalog.modules); err != nil {
		return err
	}
	network, err := objectField(map[string]any(plan), "resolvedPlan", "network")
	if err != nil {
		return err
	}
	routes, err := objectListField(network, "resolvedPlan.network", "routes")
	if err != nil {
		return err
	}
	backendPools, err := objectListField(network, "resolvedPlan.network", "backendPools")
	if err != nil {
		return err
	}
	poolsByID, err := indexObjectsByID(backendPools, "resolvedPlan.network.backendPools")
	if err != nil {
		return err
	}
	for routeIndex, route := range routes {
		routePath := fmt.Sprintf("resolvedPlan.network.routes[%d]", routeIndex)
		routeID, err := stringField(route, routePath, "id")
		if err != nil {
			return err
		}
		moduleRef, err := stringField(route, routePath, "moduleRef")
		if err != nil {
			return err
		}
		serviceRef, err := stringField(route, routePath, "serviceRef")
		if err != nil {
			return err
		}
		endpoint, exists := serviceEndpoints[moduleRef][serviceRef]
		if !exists {
			return fmt.Errorf("%s has no bound service endpoint", routePath)
		}
		poolRef, err := stringField(route, routePath, "backendPoolRef")
		if err != nil {
			return err
		}
		backendPool, exists := poolsByID[poolRef]
		if !exists {
			return fmt.Errorf("%s.backendPoolRef %q has no bound pool", routePath, poolRef)
		}
		gate, err := buildRouteHealthGate(routeID, backendPool, endpoint)
		if err != nil {
			return err
		}
		gateID := gate["id"].(string)
		if _, duplicate := expectedHealth[gateID]; duplicate {
			return fmt.Errorf("bound catalog projects duplicate route health gate %q", gateID)
		}
		expectedHealth[gateID] = gate
	}

	gates, err := objectField(map[string]any(plan), "resolvedPlan", "gates")
	if err != nil {
		return err
	}
	healthValues, err := objectListField(gates, "resolvedPlan.gates", "health")
	if err != nil {
		return err
	}
	actualHealth, err := indexObjectsByID(healthValues, "resolvedPlan.gates.health")
	if err != nil {
		return err
	}
	if !sameStringSet(mapKeys(actualHealth), mapKeys(expectedHealth)) {
		return fmt.Errorf("resolvedPlan.gates.health is not the exact selected catalog health projection")
	}
	for _, gateID := range mapKeys(actualHealth) {
		if equal, err := canonicalEqual(actualHealth[gateID], expectedHealth[gateID]); err != nil {
			return err
		} else if !equal {
			return fmt.Errorf("resolvedPlan.gates.health.%s does not match its bound catalog health body", gateID)
		}
	}

	evidenceValues, err := objectListField(gates, "resolvedPlan.gates", "evidence")
	if err != nil {
		return err
	}
	actualEvidence, err := indexObjectsByID(evidenceValues, "resolvedPlan.gates.evidence")
	if err != nil {
		return err
	}
	var scenarios []string
	for _, gate := range evidenceValues {
		scenario, err := stringField(gate, "resolvedPlan.gates.evidence", "scenario")
		if err != nil {
			return err
		}
		scenarios = append(scenarios, scenario)
	}
	scenarios = sortStringsUnique(scenarios)
	healthIDs := mapKeys(expectedHealth)
	artifacts, err := objectField(map[string]any(plan), "resolvedPlan", "generation")
	if err != nil {
		return err
	}
	artifactValues, err := objectListField(artifacts, "resolvedPlan.generation", "artifacts")
	if err != nil {
		return err
	}
	var artifactIDs []string
	for index, artifact := range artifactValues {
		id, err := stringField(artifact, fmt.Sprintf("resolvedPlan.generation.artifacts[%d]", index), "id")
		if err != nil {
			return err
		}
		artifactIDs = append(artifactIDs, id)
	}
	artifactIDs = sortStringsUnique(artifactIDs)
	expectedEvidence := make(map[string]map[string]any, len(scenarios))
	for index, scenario := range scenarios {
		id := fmt.Sprintf("evidence-%03d", index+1)
		expectedEvidence[id] = map[string]any{
			"id": id, "scenario": scenario, "phase": "verify", "producer": "evidence-runner", "required": true,
			"healthGateRefs": stringSliceAny(healthIDs), "artifactRefs": stringSliceAny(artifactIDs),
		}
	}
	if !sameStringSet(mapKeys(actualEvidence), mapKeys(expectedEvidence)) {
		return fmt.Errorf("resolvedPlan.gates.evidence IDs are not the deterministic bound evidence projection")
	}
	for _, gateID := range mapKeys(actualEvidence) {
		if equal, err := canonicalEqual(actualEvidence[gateID], expectedEvidence[gateID]); err != nil {
			return err
		} else if !equal {
			return fmt.Errorf("resolvedPlan.gates.evidence.%s is not the deterministic bound evidence projection", gateID)
		}
	}
	evidenceIDByScenario := make(map[string]string, len(expectedEvidence))
	for gateID, gate := range expectedEvidence {
		evidenceIDByScenario[gate["scenario"].(string)] = gateID
	}
	for _, providerID := range mapKeys(providers) {
		owner, hasOwner, err := optionalObjectField(providers[providerID], "resolvedPlan.providers."+providerID, "owner")
		if err != nil {
			return err
		}
		if !hasOwner {
			continue
		}
		var wantHealthRefs []string
		for gateID, gate := range expectedHealth {
			if gate["targetKind"] == "provider" && gate["targetRef"] == providerID {
				wantHealthRefs = append(wantHealthRefs, gateID)
			}
		}
		wantHealthRefs = sortStringsUnique(wantHealthRefs)
		haveHealthRefs, err := stringListField(owner, "resolvedPlan.providers."+providerID+".owner", "healthGateRefs", true)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(haveHealthRefs, wantHealthRefs) {
			return fmt.Errorf("resolvedPlan.providers.%s.owner.healthGateRefs is not the exact bound provider health projection", providerID)
		}
		providerEvidence, err := stringListField(catalog.providers[providerID], "catalog.providers."+providerID, "evidence", true)
		if err != nil {
			return err
		}
		wantEvidenceRefs := make([]string, 0, len(providerEvidence))
		for _, scenario := range providerEvidence {
			gateID, exists := evidenceIDByScenario[scenario]
			if !exists {
				return fmt.Errorf("bound provider %q evidence scenario %q has no deterministic evidence gate", providerID, scenario)
			}
			wantEvidenceRefs = append(wantEvidenceRefs, gateID)
		}
		wantEvidenceRefs = sortStringsUnique(wantEvidenceRefs)
		haveEvidenceRefs, err := stringListField(owner, "resolvedPlan.providers."+providerID+".owner", "evidenceGateRefs", true)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(haveEvidenceRefs, wantEvidenceRefs) {
			return fmt.Errorf("resolvedPlan.providers.%s.owner.evidenceGateRefs is not the exact bound provider evidence projection", providerID)
		}
	}
	wantApply := map[string]any{
		"requireFreshPlanHash": true, "requireCompatibleCLI": true, "requireCompatibleRuntime": true,
		"requireGenerationArtifacts": true, "requireResolvedSecrets": true,
	}
	haveApply, err := objectField(gates, "resolvedPlan.gates", "apply")
	if err != nil {
		return err
	}
	if equal, err := canonicalEqual(haveApply, wantApply); err != nil {
		return err
	} else if !equal {
		return fmt.Errorf("resolvedPlan.gates.apply does not match the bound compiler gate contract")
	}
	return nil
}

func resolvedTopologyIndex(plan ResolvedPlan) (map[string]string, map[string]string, []string, error) {
	sites, err := objectListField(map[string]any(plan), "resolvedPlan", "sites")
	if err != nil {
		return nil, nil, nil, err
	}
	siteKinds := make(map[string]string, len(sites))
	for index, site := range sites {
		path := fmt.Sprintf("resolvedPlan.sites[%d]", index)
		id, err := stringField(site, path, "id")
		if err != nil {
			return nil, nil, nil, err
		}
		kind, err := stringField(site, path, "kind")
		if err != nil {
			return nil, nil, nil, err
		}
		siteKinds[id] = kind
	}
	nodes, err := objectListField(map[string]any(plan), "resolvedPlan", "nodes")
	if err != nil {
		return nil, nil, nil, err
	}
	nodeSites := make(map[string]string, len(nodes))
	nodeKinds := make(map[string]string, len(nodes))
	var enabled []string
	for index, node := range nodes {
		path := fmt.Sprintf("resolvedPlan.nodes[%d]", index)
		id, err := stringField(node, path, "id")
		if err != nil {
			return nil, nil, nil, err
		}
		siteRef, err := stringField(node, path, "siteRef")
		if err != nil {
			return nil, nil, nil, err
		}
		kind, exists := siteKinds[siteRef]
		if !exists {
			return nil, nil, nil, fmt.Errorf("%s.siteRef %q has no resolved site", path, siteRef)
		}
		nodeSites[id] = siteRef
		nodeKinds[id] = kind
		isEnabled, err := boolFieldDefault(node, path, "enabled", true)
		if err != nil {
			return nil, nil, nil, err
		}
		if isEnabled {
			enabled = append(enabled, id)
		}
	}
	sort.Strings(enabled)
	return nodeSites, nodeKinds, enabled, nil
}

func resolvedModuleTargetSpec(plan ResolvedPlan) (*specView, error) {
	sites, err := objectListField(map[string]any(plan), "resolvedPlan", "sites")
	if err != nil {
		return nil, err
	}
	siteKinds := make(map[string]string, len(sites))
	siteByID := make(map[string]siteView, len(sites))
	siteViews := make([]siteView, 0, len(sites))
	for index, site := range sites {
		path := fmt.Sprintf("resolvedPlan.sites[%d]", index)
		id, err := stringField(site, path, "id")
		if err != nil {
			return nil, err
		}
		kind, err := stringField(site, path, "kind")
		if err != nil {
			return nil, err
		}
		siteKinds[id] = kind
		view := siteView{id: id, kind: kind, object: site}
		siteByID[id] = view
		siteViews = append(siteViews, view)
	}
	sort.Slice(siteViews, func(i, j int) bool { return siteViews[i].id < siteViews[j].id })
	controlPlane, err := objectField(map[string]any(plan), "resolvedPlan", "controlPlane")
	if err != nil {
		return nil, err
	}
	authoritySiteRef, err := stringField(controlPlane, "resolvedPlan.controlPlane", "authoritySiteRef")
	if err != nil {
		return nil, err
	}
	nodes, err := objectListField(map[string]any(plan), "resolvedPlan", "nodes")
	if err != nil {
		return nil, err
	}
	view := &specView{
		controlPlane: controlPlane, authoritySiteRef: authoritySiteRef,
		sites: siteViews, siteByID: siteByID, nodeByID: make(map[string]nodeView, len(nodes)),
	}
	for index, node := range nodes {
		path := fmt.Sprintf("resolvedPlan.nodes[%d]", index)
		id, err := stringField(node, path, "id")
		if err != nil {
			return nil, err
		}
		siteRef, err := stringField(node, path, "siteRef")
		if err != nil {
			return nil, err
		}
		siteKind, exists := siteKinds[siteRef]
		if !exists {
			return nil, fmt.Errorf("%s.siteRef %q has no resolved site", path, siteRef)
		}
		roles, err := stringListField(node, path, "roles", true)
		if err != nil {
			return nil, err
		}
		enabled, err := boolFieldDefault(node, path, "enabled", true)
		if err != nil {
			return nil, err
		}
		nodeView := nodeView{id: id, siteRef: siteRef, roles: roles, enabled: enabled, object: node, siteKind: siteKind}
		view.nodes = append(view.nodes, nodeView)
		view.nodeByID[id] = nodeView
	}
	sort.Slice(view.nodes, func(i, j int) bool { return view.nodes[i].id < view.nodes[j].id })
	return view, nil
}

func eligibleTopologySites(enabledNodes []string, nodeSites, nodeKinds map[string]string, supportedKinds []string) []string {
	var sites []string
	for _, nodeRef := range enabledNodes {
		if contains(supportedKinds, nodeKinds[nodeRef]) {
			sites = append(sites, nodeSites[nodeRef])
		}
	}
	return sortStringsUnique(sites)
}

func eligibleNodesForKinds(enabledNodes []string, nodeKinds map[string]string, supportedKinds []string) []string {
	var result []string
	for _, nodeRef := range enabledNodes {
		if contains(supportedKinds, nodeKinds[nodeRef]) {
			result = append(result, nodeRef)
		}
	}
	return sortStringsUnique(result)
}

func capabilitiesForProvider(selections map[string]string, providerID string) []string {
	var result []string
	for capabilityID, selectedProvider := range selections {
		if selectedProvider == providerID {
			result = append(result, capabilityID)
		}
	}
	return sortStringsUnique(result)
}

func selectedModuleCapabilities(contract map[string]any, providerRef string, selections map[string]string) ([]string, error) {
	provides, err := stringListField(contract, "catalog.modules", "provides", true)
	if err != nil {
		return nil, err
	}
	var result []string
	for capabilityID, selectedProvider := range selections {
		if selectedProvider == providerRef && contains(provides, capabilityID) {
			result = append(result, capabilityID)
		}
	}
	return sortStringsUnique(result), nil
}

func requireCatalogObjectField(have, want map[string]any, path, field string) error {
	haveObject, err := objectField(have, path, field)
	if err != nil {
		return err
	}
	wantObject, err := objectField(want, "catalog authority", field)
	if err != nil {
		return err
	}
	equal, err := canonicalEqual(haveObject, wantObject)
	if err != nil {
		return err
	}
	if !equal {
		return fmt.Errorf("%s.%s does not match the bound catalog body", path, field)
	}
	return nil
}

func requireCatalogField(have, want map[string]any, path, field string) error {
	haveValue, haveField := have[field]
	wantValue, wantField := want[field]
	if haveField != wantField {
		return fmt.Errorf("%s.%s presence does not match the bound catalog body", path, field)
	}
	if !haveField {
		return nil
	}
	equal, err := canonicalEqual(haveValue, wantValue)
	if err != nil {
		return err
	}
	if !equal {
		return fmt.Errorf("%s.%s does not match the bound catalog body", path, field)
	}
	return nil
}

func requireCatalogOptionalObjectField(have, want map[string]any, path, field string) error {
	haveObject, haveField, err := optionalObjectField(have, path, field)
	if err != nil {
		return err
	}
	wantObject, wantField, err := optionalObjectField(want, "catalog authority", field)
	if err != nil {
		return err
	}
	if haveField != wantField {
		return fmt.Errorf("%s.%s presence does not match the bound catalog body", path, field)
	}
	if !haveField {
		return nil
	}
	equal, err := canonicalEqual(haveObject, wantObject)
	if err != nil {
		return err
	}
	if !equal {
		return fmt.Errorf("%s.%s does not match the bound catalog body", path, field)
	}
	return nil
}

func indexObjectsByID(values []map[string]any, path string) (map[string]map[string]any, error) {
	result := make(map[string]map[string]any, len(values))
	for index, value := range values {
		id, err := stringField(value, fmt.Sprintf("%s[%d]", path, index), "id")
		if err != nil {
			return nil, err
		}
		if _, duplicate := result[id]; duplicate {
			return nil, fmt.Errorf("%s duplicates %q", path, id)
		}
		result[id] = value
	}
	return result, nil
}

func canonicalEqual(left, right any) (bool, error) {
	leftJSON, err := canonicalJSON(left, false)
	if err != nil {
		return false, err
	}
	rightJSON, err := canonicalJSON(right, false)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftJSON, rightJSON), nil
}

func mapKeys[V any](values map[string]V) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func sameStringSet(left, right []string) bool {
	return reflect.DeepEqual(sortStringsUnique(left), sortStringsUnique(right))
}
