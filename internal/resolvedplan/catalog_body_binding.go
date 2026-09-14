package resolvedplan

// Fast Gate may batch this package with ./internal/architecturev2renderer in one go test process.

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
	data, err := objectField(map[string]any(plan), "resolvedPlan", "data")
	if err != nil {
		return err
	}
	backupPolicy, err := objectField(map[string]any(plan), "resolvedPlan", "backupPolicy")
	if err != nil {
		return err
	}
	workloads, err := objectListField(map[string]any(plan), "resolvedPlan", "workloads")
	if err != nil {
		return err
	}
	if err := validateRecoveryObjectives(data, backupPolicy, objectMapsAsAny(workloads)); err != nil {
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
	if err := validateModuleResourceDemandProjection(plan); err != nil {
		return err
	}
	if err := validateRuntimeAdmissionProjection(plan); err != nil {
		return err
	}
	if err := validateRuntimeNetworkProjection(plan); err != nil {
		return err
	}
	if err := validateRouteOriginProjection(plan, catalog, capabilityProviders); err != nil {
		return err
	}
	if err := validateRuntimeListenerProjection(plan); err != nil {
		return err
	}
	if err := validateRouteCapabilityRealizationBodies(plan, catalog, capabilityProviders); err != nil {
		return err
	}
	if err := validateBridgePublicationProjection(plan, catalog.modules); err != nil {
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

func validateModuleResourceDemandProjection(plan ResolvedPlan) error {
	modules, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
		return err
	}
	expected, err := aggregateModuleResourceDemand(objectMapsAsAny(modules))
	if err != nil {
		return err
	}
	equal, err := canonicalEqual(plan["resourceDemand"], expected)
	if err != nil {
		return err
	}
	if !equal {
		return fmt.Errorf("resolvedPlan.resourceDemand does not match the bound module-profile projection")
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

func requireCatalogOptionalField(have, want map[string]any, path, field string) error {
	haveValue, haveExists := have[field]
	wantValue, wantExists := want[field]
	if haveExists != wantExists {
		return fmt.Errorf("%s.%s presence does not match the bound catalog body", path, field)
	}
	if !wantExists {
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
