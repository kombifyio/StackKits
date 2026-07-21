package resolvedplan

import (
	"fmt"
	"reflect"
)

// resolvedServiceEndpoint is catalog-owned service identity plus the exact
// placement of its owning resolved render unit. Routes may choose an external
// listener port, host, path, access policy, and allowed exposure, but cannot
// invent or widen this backend contract.
type resolvedServiceEndpoint struct {
	moduleRef               string
	unitRef                 string
	serviceRef              string
	upstreamProtocol        string
	targetPort              int
	requiredPrivilege       string
	allowedIngressProtocols []string
	allowedExposures        []string
	originSelector          string
	healthRef               string
	healthContract          map[string]any
	data                    *serviceEndpointDataRequirement
	siteRefs                []string
	nodeRefs                []string
	instanceRefs            []string
	instanceSites           map[string]string
	instanceNodes           map[string]string
}

type serviceEndpointDataRequirement struct {
	bindingRef      string
	requiredClasses []string
	locality        string
}

type resolvedServiceEndpointIndex map[string]map[string]resolvedServiceEndpoint

//nolint:gocyclo // Endpoint indexing validates the complete nested module/unit/endpoint body and rejects ambiguity atomically.
func indexResolvedServiceEndpoints(modules []any) (resolvedServiceEndpointIndex, error) {
	result := make(resolvedServiceEndpointIndex)
	for moduleIndex, rawModule := range modules {
		module, err := asObject(rawModule, fmt.Sprintf("modules[%d]", moduleIndex))
		if err != nil {
			return nil, err
		}
		moduleID, err := stringField(module, fmt.Sprintf("modules[%d]", moduleIndex), "id")
		if err != nil {
			return nil, err
		}
		if _, duplicate := result[moduleID]; duplicate {
			return nil, fail(ErrContractConflict, fmt.Sprintf("modules[%d].id", moduleIndex), "module %q is duplicated", moduleID)
		}
		result[moduleID] = map[string]resolvedServiceEndpoint{}
		units, err := objectListField(module, "modules."+moduleID, "renderUnits")
		if err != nil {
			return nil, err
		}
		for unitIndex, unit := range units {
			unitPath := fmt.Sprintf("modules.%s.renderUnits[%d]", moduleID, unitIndex)
			unitID, err := stringField(unit, unitPath, "id")
			if err != nil {
				return nil, err
			}
			siteRefs, err := stringListField(unit, unitPath, "siteRefs", true)
			if err != nil {
				return nil, err
			}
			nodeRefs, err := stringListField(unit, unitPath, "nodeRefs", true)
			if err != nil {
				return nil, err
			}
			instances, err := objectListField(unit, unitPath, "instances")
			if err != nil {
				return nil, err
			}
			instanceRefs := make([]string, 0, len(instances))
			instanceSites := make(map[string]string, len(instances))
			instanceNodes := make(map[string]string, len(instances))
			for instanceIndex, instance := range instances {
				instancePath := fmt.Sprintf("%s.instances[%d]", unitPath, instanceIndex)
				instanceRef, err := stringField(instance, instancePath, "id")
				if err != nil {
					return nil, err
				}
				instanceRefs = append(instanceRefs, instanceRef)
				instanceSiteRef, hasInstanceSite, err := optionalStringField(instance, instancePath, "siteRef")
				if err != nil {
					return nil, err
				}
				if hasInstanceSite {
					instanceSites[instanceRef] = instanceSiteRef
				}
				instanceNodeRef, hasInstanceNode, err := optionalStringField(instance, instancePath, "nodeRef")
				if err != nil {
					return nil, err
				}
				if hasInstanceNode {
					instanceNodes[instanceRef] = instanceNodeRef
				}
			}
			endpoints, err := objectListOptional(unit, "serviceEndpoints")
			if err != nil {
				return nil, err
			}
			for endpointIndex, endpoint := range endpoints {
				endpointPath := fmt.Sprintf("%s.serviceEndpoints[%d]", unitPath, endpointIndex)
				serviceRef, err := stringField(endpoint, endpointPath, "serviceRef")
				if err != nil {
					return nil, err
				}
				if _, duplicate := result[moduleID][serviceRef]; duplicate {
					return nil, fail(ErrContractConflict, endpointPath+".serviceRef", "service %q is exported more than once by module %q", serviceRef, moduleID)
				}
				upstreamProtocol, err := stringField(endpoint, endpointPath, "upstreamProtocol")
				if err != nil {
					return nil, err
				}
				targetPort, err := intField(endpoint, endpointPath, "targetPort")
				if err != nil {
					return nil, err
				}
				requiredPrivilege, hasRequiredPrivilege, err := optionalStringField(endpoint, endpointPath, "requiredPrivilege")
				if err != nil {
					return nil, err
				}
				if !hasRequiredPrivilege {
					requiredPrivilege = "user"
				}
				allowedExposures, err := stringListField(endpoint, endpointPath, "allowedExposures", true)
				if err != nil {
					return nil, err
				}
				if len(allowedExposures) != len(sortStringsUnique(allowedExposures)) {
					return nil, fail(ErrContractConflict, endpointPath+".allowedExposures", "exposures must be unique")
				}
				allowedIngressProtocols, err := stringListField(endpoint, endpointPath, "allowedIngressProtocols", true)
				if err != nil {
					return nil, err
				}
				if len(allowedIngressProtocols) != len(sortStringsUnique(allowedIngressProtocols)) {
					return nil, fail(ErrContractConflict, endpointPath+".allowedIngressProtocols", "protocols must be unique")
				}
				originSelector, err := stringField(endpoint, endpointPath, "originSelector")
				if err != nil {
					return nil, err
				}
				healthRef, err := stringField(endpoint, endpointPath, "healthRef")
				if err != nil {
					return nil, err
				}
				dataRequirement, err := parseServiceEndpointDataRequirement(endpoint, endpointPath)
				if err != nil {
					return nil, err
				}
				result[moduleID][serviceRef] = resolvedServiceEndpoint{
					moduleRef: moduleID, unitRef: unitID, serviceRef: serviceRef,
					upstreamProtocol: upstreamProtocol, targetPort: targetPort, requiredPrivilege: requiredPrivilege,
					allowedIngressProtocols: sortStringsUnique(allowedIngressProtocols), allowedExposures: sortStringsUnique(allowedExposures),
					originSelector: originSelector, healthRef: healthRef, data: dataRequirement,
					siteRefs: sortStringsUnique(siteRefs), nodeRefs: sortStringsUnique(nodeRefs), instanceRefs: sortStringsUnique(instanceRefs), instanceSites: instanceSites, instanceNodes: instanceNodes,
				}
			}
		}
	}
	return result, nil
}

func bindServiceEndpointHealthContracts(index resolvedServiceEndpointIndex, catalogModules map[string]map[string]any) error {
	for moduleID, endpoints := range index {
		module, exists := catalogModules[moduleID]
		if !exists {
			return fail(ErrContractConflict, "modules."+moduleID, "module is missing from the bound catalog")
		}
		healthContracts, err := objectListOptional(module, "health")
		if err != nil {
			return err
		}
		healthByID := make(map[string]map[string]any, len(healthContracts))
		for healthIndex, healthContract := range healthContracts {
			healthPath := fmt.Sprintf("modules.%s.health[%d]", moduleID, healthIndex)
			healthID, err := stringField(healthContract, healthPath, "id")
			if err != nil {
				return err
			}
			if _, duplicate := healthByID[healthID]; duplicate {
				return fail(ErrContractConflict, healthPath+".id", "health contract %q is duplicated", healthID)
			}
			healthByID[healthID] = healthContract
		}
		for serviceRef, endpoint := range endpoints {
			healthContract, exists := healthByID[endpoint.healthRef]
			if !exists {
				return fail(ErrContractConflict, "modules."+moduleID+".serviceEndpoints."+serviceRef+".healthRef", "health contract %q is not declared by module %q", endpoint.healthRef, moduleID)
			}
			endpoint.healthContract = healthContract
			endpoints[serviceRef] = endpoint
		}
	}
	return nil
}

func buildRouteHealthGate(routeID string, backendPool map[string]any, endpoint resolvedServiceEndpoint) (map[string]any, error) {
	if endpoint.healthContract == nil {
		return nil, fail(ErrContractConflict, "spec.routes."+routeID+".healthGateRef", "source module health contract is unavailable")
	}
	healthPath := "modules." + endpoint.moduleRef + ".health." + endpoint.healthRef
	kind, err := stringField(endpoint.healthContract, healthPath, "kind")
	if err != nil {
		return nil, err
	}
	timeoutSeconds, err := intField(endpoint.healthContract, healthPath, "timeoutSeconds")
	if err != nil {
		return nil, err
	}
	poolID, err := stringField(backendPool, "route backend pool", "id")
	if err != nil {
		return nil, err
	}
	gate := map[string]any{
		"phase": "post-apply", "kind": "contract", "execution": "contract-only",
		"protocol": endpoint.upstreamProtocol, "port": endpoint.targetPort,
		"timeoutSeconds": timeoutSeconds, "targetKind": "route", "targetRef": routeID,
		"routeRef": routeID, "backendPoolRef": poolID, "siteRefs": stringSliceAny(endpoint.siteRefs),
		"nodeRefs": stringSliceAny(endpoint.nodeRefs), "sourceHealthGateRef": contractID("module-" + endpoint.moduleRef + "-" + endpoint.healthRef),
		"scope": "each-backend-member", "required": true,
	}
	switch endpoint.upstreamProtocol {
	case "http", "https":
		if kind == "http" {
			contractPort, err := intField(endpoint.healthContract, healthPath, "port")
			if err != nil {
				return nil, err
			}
			if contractPort != endpoint.targetPort {
				return nil, fail(ErrContractConflict, healthPath+".port", "health port %d does not match service target port %d", contractPort, endpoint.targetPort)
			}
			path, err := stringField(endpoint.healthContract, healthPath, "path")
			if err != nil {
				return nil, err
			}
			expectedStatuses, exists := endpoint.healthContract["expectedStatuses"]
			if !exists {
				return nil, fail(ErrContractConflict, healthPath+".expectedStatuses", "http route health contract has no expected statuses")
			}
			gate["kind"], gate["execution"] = "http", "probe"
			gate["method"], gate["followRedirects"] = "GET", false
			gate["path"], gate["expectedStatuses"] = path, expectedStatuses
		}
	case "tcp":
		if kind == "tcp" {
			contractPort, err := intField(endpoint.healthContract, healthPath, "port")
			if err != nil {
				return nil, err
			}
			if contractPort != endpoint.targetPort {
				return nil, fail(ErrContractConflict, healthPath+".port", "health port %d does not match service target port %d", contractPort, endpoint.targetPort)
			}
			gate["kind"], gate["execution"] = "tcp", "probe"
		}
	}
	contractHash, err := canonicalHash(gate, true)
	if err != nil {
		return nil, fmt.Errorf("hash route health gate %s: %w", routeID, err)
	}
	gate["contractHash"] = contractHash
	gate["id"] = contractID(routeID + "-health-" + contractHash[len("sha256:"):len("sha256:")+12])
	return gate, nil
}

func buildRouteBackendPool(routeID string, endpoint resolvedServiceEndpoint) (map[string]any, error) {
	members := make([]any, 0, len(endpoint.instanceRefs))
	for _, instanceRef := range endpoint.instanceRefs {
		siteRef := endpoint.instanceSites[instanceRef]
		nodeRef := endpoint.instanceNodes[instanceRef]
		if siteRef == "" || nodeRef == "" || !contains(endpoint.siteRefs, siteRef) || !contains(endpoint.nodeRefs, nodeRef) {
			return nil, fail(ErrUnresolvedPlacement, "spec.routes."+routeID, "service %q instance %q has no exact selected site and node", endpoint.serviceRef, instanceRef)
		}
		members = append(members, map[string]any{"siteRef": siteRef, "nodeRef": nodeRef, "instanceRef": instanceRef})
	}
	pool := map[string]any{
		"routeRef": routeID, "serviceRef": endpoint.serviceRef, "moduleRef": endpoint.moduleRef, "unitRef": endpoint.unitRef,
		"originSelector": endpoint.originSelector, "upstreamProtocol": endpoint.upstreamProtocol, "targetPort": endpoint.targetPort,
		"members": members,
	}
	hash, err := canonicalHash(pool, false)
	if err != nil {
		return nil, fmt.Errorf("hash route backend pool %s: %w", routeID, err)
	}
	pool["id"] = contractID(routeID + "-pool-" + hash[len("sha256:"):len("sha256:")+12])
	return pool, nil
}

func parseServiceEndpointDataRequirement(endpoint map[string]any, endpointPath string) (*serviceEndpointDataRequirement, error) {
	data, exists, err := optionalObjectField(endpoint, endpointPath, "data")
	if err != nil || !exists {
		return nil, err
	}
	bindingRef, err := stringField(data, endpointPath+".data", "bindingRef")
	if err != nil {
		return nil, err
	}
	requiredClasses, err := stringListField(data, endpointPath+".data", "requiredClasses", true)
	if err != nil {
		return nil, err
	}
	if len(requiredClasses) != len(sortStringsUnique(requiredClasses)) {
		return nil, fail(ErrContractConflict, endpointPath+".data.requiredClasses", "data classes must be unique")
	}
	locality, err := stringField(data, endpointPath+".data", "locality")
	if err != nil {
		return nil, err
	}
	return &serviceEndpointDataRequirement{bindingRef: bindingRef, requiredClasses: sortStringsUnique(requiredClasses), locality: locality}, nil
}

func resolveRouteServiceEndpoint(index resolvedServiceEndpointIndex, moduleRef, serviceRef, authoritySiteRef string, nodeSites map[string]string, routePath string) (resolvedServiceEndpoint, error) {
	moduleEndpoints, exists := index[moduleRef]
	if !exists {
		return resolvedServiceEndpoint{}, fail(ErrUnrealizedModule, routePath+".moduleRef", "module %q is not resolved", moduleRef)
	}
	endpoint, exists := moduleEndpoints[serviceRef]
	if !exists {
		return resolvedServiceEndpoint{}, fail(ErrUnrealizedModule, routePath+".serviceRef", "service %q is not exported by resolved module %q", serviceRef, moduleRef)
	}
	selectedSiteRef := ""
	switch endpoint.originSelector {
	case "single-site":
		if len(endpoint.siteRefs) != 1 {
			return resolvedServiceEndpoint{}, fail(ErrUnresolvedPlacement, routePath, "single-site service %q is ambiguous across endpoint sites %v", serviceRef, endpoint.siteRefs)
		}
		selectedSiteRef = endpoint.siteRefs[0]
	case "control-authority-site":
		if !contains(endpoint.siteRefs, authoritySiteRef) {
			return resolvedServiceEndpoint{}, fail(ErrUnresolvedPlacement, routePath, "service %q is not placed on control authority site %q", serviceRef, authoritySiteRef)
		}
		selectedSiteRef = authoritySiteRef
	default:
		return resolvedServiceEndpoint{}, fail(ErrContractConflict, routePath+".serviceRef", "service %q uses unsupported origin selector %q", serviceRef, endpoint.originSelector)
	}
	selectedNodes := make([]string, 0, len(endpoint.nodeRefs))
	for _, nodeRef := range endpoint.nodeRefs {
		if nodeSites[nodeRef] == selectedSiteRef {
			selectedNodes = append(selectedNodes, nodeRef)
		}
	}
	selectedInstances := make([]string, 0, len(endpoint.instanceRefs))
	for _, instanceRef := range endpoint.instanceRefs {
		if endpoint.instanceSites[instanceRef] == selectedSiteRef {
			selectedInstances = append(selectedInstances, instanceRef)
		}
	}
	if len(selectedNodes) == 0 || len(selectedInstances) == 0 {
		return resolvedServiceEndpoint{}, fail(ErrUnresolvedPlacement, routePath, "service %q has no exact resolved endpoint instances", serviceRef)
	}
	endpoint.siteRefs = []string{selectedSiteRef}
	endpoint.nodeRefs = sortStringsUnique(selectedNodes)
	endpoint.instanceRefs = sortStringsUnique(selectedInstances)
	return endpoint, nil
}

func validateRouteEndpointContract(endpoint resolvedServiceEndpoint, spec *specView, routePath, exposure, protocol string, targetPort int) error {
	if !contains(endpoint.allowedIngressProtocols, protocol) {
		return fail(ErrContractConflict, routePath+".protocol", "service endpoint %q does not allow ingress protocol %q", endpoint.serviceRef, protocol)
	}
	if endpoint.targetPort != targetPort {
		return fail(ErrContractConflict, routePath+".targetPort", "route target port %d differs from catalog-owned service endpoint target port %d", targetPort, endpoint.targetPort)
	}
	if !contains(endpoint.allowedExposures, exposure) {
		return fail(ErrContractConflict, routePath+".exposure", "service endpoint %q does not allow exposure %q", endpoint.serviceRef, exposure)
	}
	if endpoint.data != nil {
		if err := validateServiceEndpointData(endpoint, spec.data, "spec.data", routePath); err != nil {
			return err
		}
	}
	return nil
}

func validateServiceEndpointAccess(endpoint resolvedServiceEndpoint, access map[string]any, path string) error {
	privilege, err := stringField(access, path, "privilege")
	if err != nil {
		return err
	}
	if privilege != endpoint.requiredPrivilege {
		return fail(ErrProfileMismatch, path+".privilege", "service endpoint %q requires privilege %q, got %q", endpoint.serviceRef, endpoint.requiredPrivilege, privilege)
	}
	return nil
}

//nolint:gocyclo // Authority rebound validation checks the complete route, exact pool, placement, and data contract atomically.
func validateResolvedRouteEndpoint(route map[string]any, routePath string, endpoint resolvedServiceEndpoint, planData map[string]any) error {
	protocol, err := stringField(route, routePath, "protocol")
	if err != nil {
		return err
	}
	targetPort, err := intField(route, routePath, "targetPort")
	if err != nil {
		return err
	}
	exposure, err := stringField(route, routePath, "exposure")
	if err != nil {
		return err
	}
	upstreamProtocol, err := stringField(route, routePath, "upstreamProtocol")
	if err != nil {
		return err
	}
	if upstreamProtocol != endpoint.upstreamProtocol || !contains(endpoint.allowedIngressProtocols, protocol) || targetPort != endpoint.targetPort || !contains(endpoint.allowedExposures, exposure) {
		return fmt.Errorf("%s is not the exact catalog-owned service endpoint protocol, target port, and exposure projection", routePath)
	}
	access, err := objectField(route, routePath, "access")
	if err != nil {
		return err
	}
	if err := validateServiceEndpointAccess(endpoint, access, routePath+".access"); err != nil {
		return err
	}
	healthGateRef, err := stringField(route, routePath, "healthGateRef")
	if err != nil {
		return err
	}
	originSiteRef, err := stringField(route, routePath, "originSiteRef")
	if err != nil {
		return err
	}
	if len(endpoint.siteRefs) != 1 || originSiteRef != endpoint.siteRefs[0] {
		return fmt.Errorf("%s.originSiteRef is not the exact service endpoint origin", routePath)
	}
	originNodeRefs, err := stringListField(route, routePath, "originNodeRefs", true)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(originNodeRefs, endpoint.nodeRefs) {
		return fmt.Errorf("%s.originNodeRefs is not the exact service endpoint node projection", routePath)
	}
	backendPoolRef, err := stringField(route, routePath, "backendPoolRef")
	if err != nil {
		return err
	}
	routeID, err := stringField(route, routePath, "id")
	if err != nil {
		return err
	}
	wantPool, err := buildRouteBackendPool(routeID, endpoint)
	if err != nil {
		return err
	}
	if backendPoolRef != wantPool["id"] {
		return fmt.Errorf("%s.backendPoolRef is not the exact service endpoint backend pool projection", routePath)
	}
	wantHealthGate, err := buildRouteHealthGate(routeID, wantPool, endpoint)
	if err != nil {
		return err
	}
	if healthGateRef != wantHealthGate["id"] {
		return fmt.Errorf("%s.healthGateRef is not the exact route and backend-pool health projection", routePath)
	}
	if endpoint.data != nil {
		if err := validateServiceEndpointData(endpoint, planData, "resolvedPlan.data", routePath); err != nil {
			return err
		}
	}
	return nil
}

func validateServiceEndpointData(endpoint resolvedServiceEndpoint, data map[string]any, dataPath, routePath string) error {
	bindings, hasBindings, err := optionalObjectField(data, dataPath, "bindings")
	if err != nil {
		return err
	}
	if !hasBindings {
		return fail(ErrContractConflict, routePath+".serviceRef", "service endpoint %q requires a governed data binding", endpoint.serviceRef)
	}
	binding, exists := bindings[endpoint.data.bindingRef]
	if !exists {
		return fail(ErrContractConflict, routePath+".serviceRef", "service endpoint %q requires %s.bindings.%s", endpoint.serviceRef, dataPath, endpoint.data.bindingRef)
	}
	bindingObject, err := asObject(binding, dataPath+".bindings."+endpoint.data.bindingRef)
	if err != nil {
		return err
	}
	classes, err := stringListField(bindingObject, dataPath+".bindings."+endpoint.data.bindingRef, "classes", true)
	if err != nil {
		return err
	}
	for _, requiredClass := range endpoint.data.requiredClasses {
		if !contains(classes, requiredClass) {
			return fail(ErrContractConflict, routePath+".serviceRef", "service endpoint %q requires data class %q", endpoint.serviceRef, requiredClass)
		}
	}
	primarySiteRef, err := stringField(bindingObject, dataPath+".bindings."+endpoint.data.bindingRef, "primarySiteRef")
	if err != nil {
		return err
	}
	originSiteRef := endpoint.siteRefs[0]
	switch endpoint.data.locality {
	case "primary-site":
		if originSiteRef != primarySiteRef {
			return fail(ErrContractConflict, routePath+".serviceRef", "service endpoint %q must be co-located with data primary site %q", endpoint.serviceRef, primarySiteRef)
		}
	case "primary-or-replica":
		replicaSiteRefs, err := stringListField(bindingObject, dataPath+".bindings."+endpoint.data.bindingRef, "replicaSiteRefs", false)
		if err != nil {
			return err
		}
		if originSiteRef != primarySiteRef && !contains(replicaSiteRefs, originSiteRef) {
			return fail(ErrContractConflict, routePath+".serviceRef", "service endpoint %q must be co-located with a data primary or replica site", endpoint.serviceRef)
		}
	default:
		return fail(ErrContractConflict, routePath+".serviceRef", "service endpoint %q uses unsupported data locality %q", endpoint.serviceRef, endpoint.data.locality)
	}
	return nil
}
