package resolvedplan

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
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
	originSelection         map[string]any
	resolvedOriginSelection map[string]any
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
				originSelection, hasOriginSelection, err := optionalObjectField(endpoint, endpointPath, "originSelection")
				if err != nil {
					return nil, err
				}
				if hasOriginSelection {
					originSelection, err = cloneObject(originSelection, true)
					if err != nil {
						return nil, err
					}
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
					originSelector: originSelector, originSelection: originSelection, healthRef: healthRef, data: dataRequirement,
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
	case "http":
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
	case "https":
		// HTTPS is not executable from the public route descriptor alone. A
		// future executor-private target binding must first bind SNI, peer
		// identity, and trust roots. Keep the source contract's port exact, but
		// do not claim a probe until that TLS authority exists.
		if kind == "http" {
			contractPort, err := intField(endpoint.healthContract, healthPath, "port")
			if err != nil {
				return nil, err
			}
			if contractPort != endpoint.targetPort {
				return nil, fail(ErrContractConflict, healthPath+".port", "health port %d does not match service target port %d", contractPort, endpoint.targetPort)
			}
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
	if endpoint.resolvedOriginSelection != nil {
		selection, err := cloneObject(endpoint.resolvedOriginSelection, true)
		if err != nil {
			return nil, err
		}
		pool["originSelection"] = selection
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

type routeBackendCandidate struct {
	siteRef, nodeRef, instanceRef string
	siteKind, siteFailureDomain   string
	nodeFailureDomain             string
	roles                         []string
}

func resolveRouteServiceEndpoint(index resolvedServiceEndpointIndex, moduleRef, serviceRef, authoritySiteRef string, nodeSites map[string]string, routePath string, topology ...*specView) (resolvedServiceEndpoint, error) {
	moduleEndpoints, exists := index[moduleRef]
	if !exists {
		return resolvedServiceEndpoint{}, fail(ErrUnrealizedModule, routePath+".moduleRef", "module %q is not resolved", moduleRef)
	}
	endpoint, exists := moduleEndpoints[serviceRef]
	if !exists {
		return resolvedServiceEndpoint{}, fail(ErrUnrealizedModule, routePath+".serviceRef", "service %q is not exported by resolved module %q", serviceRef, moduleRef)
	}
	candidates := make([]routeBackendCandidate, 0, len(endpoint.instanceRefs))
	for _, instanceRef := range endpoint.instanceRefs {
		siteRef := endpoint.instanceSites[instanceRef]
		nodeRef := endpoint.instanceNodes[instanceRef]
		if nodeSites[nodeRef] != siteRef || !contains(endpoint.siteRefs, siteRef) || !contains(endpoint.nodeRefs, nodeRef) {
			return resolvedServiceEndpoint{}, fail(ErrUnresolvedPlacement, routePath, "service %q instance %q is not bound to an enabled resolved Site and node", serviceRef, instanceRef)
		}
		candidate := routeBackendCandidate{siteRef: siteRef, nodeRef: nodeRef, instanceRef: instanceRef}
		if endpoint.originSelector == "multi-zone" || endpoint.originSelector == "edge-pool" {
			if len(topology) != 1 || topology[0] == nil {
				return resolvedServiceEndpoint{}, fail(ErrContractConflict, routePath+".serviceRef", "selector %q requires the exact resolved topology", endpoint.originSelector)
			}
			spec := topology[0]
			site, siteExists := spec.siteByID[siteRef]
			node, nodeExists := spec.nodeByID[nodeRef]
			if !siteExists || !nodeExists || !node.enabled || node.siteRef != siteRef {
				return resolvedServiceEndpoint{}, fail(ErrUnresolvedPlacement, routePath, "service %q instance %q is not bound to an enabled resolved Site and node", serviceRef, instanceRef)
			}
			candidate.siteKind = site.kind
			candidate.roles = node.roles
			var err error
			candidate.siteFailureDomain, err = stringField(site.object, "spec.sites."+siteRef, "failureDomain")
			if err != nil {
				return resolvedServiceEndpoint{}, err
			}
			candidate.nodeFailureDomain, err = stringField(node.object, "spec.nodes."+nodeRef, "failureDomain")
			if err != nil {
				return resolvedServiceEndpoint{}, err
			}
		}
		candidates = append(candidates, candidate)
	}
	selected := make([]routeBackendCandidate, 0, len(candidates))
	switch endpoint.originSelector {
	case "single-site":
		if len(endpoint.siteRefs) != 1 {
			return resolvedServiceEndpoint{}, fail(ErrUnresolvedPlacement, routePath, "single-site service %q is ambiguous across endpoint sites %v", serviceRef, endpoint.siteRefs)
		}
		for _, candidate := range candidates {
			if candidate.siteRef == endpoint.siteRefs[0] {
				selected = append(selected, candidate)
			}
		}
	case "control-authority-site":
		if !contains(endpoint.siteRefs, authoritySiteRef) {
			return resolvedServiceEndpoint{}, fail(ErrUnresolvedPlacement, routePath, "service %q is not placed on control authority site %q", serviceRef, authoritySiteRef)
		}
		for _, candidate := range candidates {
			if candidate.siteRef == authoritySiteRef {
				selected = append(selected, candidate)
			}
		}
	case "multi-zone", "edge-pool":
		if endpoint.originSelection == nil {
			return resolvedServiceEndpoint{}, fail(ErrContractConflict, routePath+".serviceRef", "service %q selector %q has no explicit origin selection policy", serviceRef, endpoint.originSelector)
		}
		siteKinds, err := stringListField(endpoint.originSelection, routePath+".originSelection", "siteKinds", true)
		if err != nil {
			return resolvedServiceEndpoint{}, err
		}
		requiredRoles, err := stringListField(endpoint.originSelection, routePath+".originSelection", "requiredRoles", false)
		if err != nil {
			return resolvedServiceEndpoint{}, err
		}
		for _, candidate := range candidates {
			if !contains(siteKinds, candidate.siteKind) || !containsAllStrings(candidate.roles, requiredRoles) {
				continue
			}
			selected = append(selected, candidate)
		}
		resolvedSelection, err := resolveRouteOriginSelection(endpoint, selected, routePath)
		if err != nil {
			return resolvedServiceEndpoint{}, err
		}
		endpoint.resolvedOriginSelection = resolvedSelection
	default:
		return resolvedServiceEndpoint{}, fail(ErrContractConflict, routePath+".serviceRef", "service %q uses unsupported origin selector %q", serviceRef, endpoint.originSelector)
	}
	if len(selected) == 0 {
		return resolvedServiceEndpoint{}, fail(ErrUnresolvedPlacement, routePath, "service %q has no exact resolved endpoint instances", serviceRef)
	}
	sort.Slice(selected, func(i, j int) bool {
		left, right := selected[i], selected[j]
		if left.siteRef != right.siteRef {
			return left.siteRef < right.siteRef
		}
		if left.nodeFailureDomain != right.nodeFailureDomain {
			return left.nodeFailureDomain < right.nodeFailureDomain
		}
		if left.nodeRef != right.nodeRef {
			return left.nodeRef < right.nodeRef
		}
		return left.instanceRef < right.instanceRef
	})
	endpoint.siteRefs, endpoint.nodeRefs, endpoint.instanceRefs = nil, nil, nil
	for _, candidate := range selected {
		if !contains(endpoint.siteRefs, candidate.siteRef) {
			endpoint.siteRefs = append(endpoint.siteRefs, candidate.siteRef)
		}
		if !contains(endpoint.nodeRefs, candidate.nodeRef) {
			endpoint.nodeRefs = append(endpoint.nodeRefs, candidate.nodeRef)
		}
		endpoint.instanceRefs = append(endpoint.instanceRefs, candidate.instanceRef)
	}
	return endpoint, nil
}

func containsAllStrings(have, required []string) bool {
	for _, value := range required {
		if !contains(have, value) {
			return false
		}
	}
	return true
}

func resolveRouteOriginSelection(endpoint resolvedServiceEndpoint, selected []routeBackendCandidate, routePath string) (map[string]any, error) {
	policyPath := routePath + ".originSelection"
	minSites, err := intField(endpoint.originSelection, policyPath, "minSites")
	if err != nil {
		return nil, err
	}
	siteSpread, err := intField(endpoint.originSelection, policyPath, "siteFailureDomainSpread")
	if err != nil {
		return nil, err
	}
	nodeSpread, err := intField(endpoint.originSelection, policyPath, "nodeFailureDomainSpread")
	if err != nil {
		return nil, err
	}
	sites, siteFailureDomains, nodeFailureDomains := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	for _, candidate := range selected {
		sites[candidate.siteRef] = struct{}{}
		siteFailureDomains[candidate.siteFailureDomain] = struct{}{}
		nodeFailureDomains[candidate.siteRef+"\x00"+candidate.nodeFailureDomain] = struct{}{}
	}
	if len(sites) < minSites || len(siteFailureDomains) < siteSpread || len(nodeFailureDomains) < nodeSpread {
		return nil, fail(ErrUnresolvedPlacement, policyPath, "selector %q resolved %d Sites, %d Site failure domains, and %d site-scoped node failure domains; requires at least %d/%d/%d", endpoint.originSelector, len(sites), len(siteFailureDomains), len(nodeFailureDomains), minSites, siteSpread, nodeSpread)
	}
	resolved, err := cloneObject(endpoint.originSelection, true)
	if err != nil {
		return nil, err
	}
	resolved["siteFailureDomains"] = stringSliceAny(sortedStringSetKeys(siteFailureDomains))
	nodeDomains := make([]any, 0, len(nodeFailureDomains))
	for _, scoped := range sortedStringSetKeys(nodeFailureDomains) {
		parts := strings.SplitN(scoped, "\x00", 2)
		nodeDomains = append(nodeDomains, map[string]any{"siteRef": parts[0], "failureDomain": parts[1]})
	}
	resolved["nodeFailureDomains"] = nodeDomains
	return resolved, nil
}

func sortedStringSetKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
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
	originSelector, err := stringField(route, routePath, "originSelector")
	if err != nil {
		return err
	}
	if originSelector != endpoint.originSelector {
		return fmt.Errorf("%s.originSelector is not the catalog-owned service endpoint selector", routePath)
	}
	originSiteRefs, err := stringListField(route, routePath, "originSiteRefs", true)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(originSiteRefs, endpoint.siteRefs) {
		return fmt.Errorf("%s.originSiteRefs is not the exact service endpoint origin set", routePath)
	}
	originSiteRef, hasOriginSiteRef, err := optionalStringField(route, routePath, "originSiteRef")
	if err != nil {
		return err
	}
	if endpoint.originSelector == "single-site" || endpoint.originSelector == "control-authority-site" {
		if !hasOriginSiteRef || len(endpoint.siteRefs) != 1 || originSiteRef != endpoint.siteRefs[0] {
			return fmt.Errorf("%s.originSiteRef is not the exact single-Site service endpoint origin", routePath)
		}
	} else {
		if hasOriginSiteRef {
			return fmt.Errorf("%s.originSiteRef invents a primary Site for selector %q", routePath, endpoint.originSelector)
		}
		selection, exists, err := optionalObjectField(route, routePath, "originSelection")
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%s.originSelection is missing", routePath)
		}
		equal, err := canonicalEqual(selection, endpoint.resolvedOriginSelection)
		if err != nil || !equal {
			return fmt.Errorf("%s.originSelection is not the exact catalog-owned selection policy", routePath)
		}
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
	switch endpoint.data.locality {
	case "primary-site":
		if len(endpoint.siteRefs) != 1 || endpoint.siteRefs[0] != primarySiteRef {
			return fail(ErrContractConflict, routePath+".serviceRef", "service endpoint %q must be co-located with data primary site %q", endpoint.serviceRef, primarySiteRef)
		}
	case "primary-or-replica":
		replicaSiteRefs, err := stringListField(bindingObject, dataPath+".bindings."+endpoint.data.bindingRef, "replicaSiteRefs", false)
		if err != nil {
			return err
		}
		for _, originSiteRef := range endpoint.siteRefs {
			if originSiteRef != primarySiteRef && !contains(replicaSiteRefs, originSiteRef) {
				return fail(ErrContractConflict, routePath+".serviceRef", "service endpoint %q origin Site %q is not a governed data primary or replica", endpoint.serviceRef, originSiteRef)
			}
		}
	default:
		return fail(ErrContractConflict, routePath+".serviceRef", "service endpoint %q uses unsupported data locality %q", endpoint.serviceRef, endpoint.data.locality)
	}
	return nil
}
