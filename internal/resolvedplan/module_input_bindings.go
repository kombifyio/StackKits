package resolvedplan

import (
	"fmt"
	"sort"
	"strings"
)

const (
	moduleInputSourceDeviceEnrollment = "identity.deviceEnrollment"
	moduleInputSourceNetworkRoutes    = "network.routes"
	moduleInputTypeDeviceEnrollment   = "device-enrollment-public-v1"
	moduleInputTypeNetworkRoutesV4    = "authority-bound-service-route-list-v4"
	moduleInputTypeNetworkRoutes      = moduleInputTypeNetworkRoutesV4
)

type moduleRenderInputBinding struct {
	targetRef    string
	sourceRef    string
	valueType    string
	cardinality  string
	required     bool
	defaultValue any
	hasDefault   bool
	raw          map[string]any
}

type moduleRenderInputSource struct {
	identity map[string]any
	network  map[string]any
	gates    map[string]any
}

func moduleRenderInputBindings(unit map[string]any, unitPath string) ([]moduleRenderInputBinding, error) {
	rawBindings, err := objectListOptional(unit, "inputBindings")
	if err != nil {
		return nil, fail(ErrContractConflict, unitPath+".inputBindings", "%v", err)
	}
	publicRefs, err := stringListField(unit, unitPath, "publicInputRefs", false)
	if err != nil {
		return nil, err
	}
	secretRefs, err := stringListField(unit, unitPath, "secretInputRefs", false)
	if err != nil {
		return nil, err
	}
	planRefs, err := stringListField(unit, unitPath, "planInputRefs", false)
	if err != nil {
		return nil, err
	}
	publicSet, secretSet, planSet := moduleInputStringSet(publicRefs), moduleInputStringSet(secretRefs), moduleInputStringSet(planRefs)
	seen := make(map[string]struct{}, len(rawBindings))
	bindings := make([]moduleRenderInputBinding, 0, len(rawBindings))
	for index, raw := range rawBindings {
		path := fmt.Sprintf("%s.inputBindings[%d]", unitPath, index)
		binding, err := parseModuleRenderInputBinding(raw, path)
		if err != nil {
			return nil, err
		}
		if _, exists := publicSet[binding.targetRef]; !exists {
			return nil, fail(ErrContractConflict, path+".targetRef", "bound target %q is not a declared public input", binding.targetRef)
		}
		if _, exists := secretSet[binding.targetRef]; exists {
			return nil, fail(ErrContractConflict, path+".targetRef", "bound target %q aliases a secret input", binding.targetRef)
		}
		if _, exists := planSet[binding.targetRef]; exists {
			return nil, fail(ErrContractConflict, path+".targetRef", "bound target %q aliases a compiler plan input", binding.targetRef)
		}
		if _, duplicate := seen[binding.targetRef]; duplicate {
			return nil, fail(ErrContractConflict, path+".targetRef", "bound target %q is duplicated", binding.targetRef)
		}
		seen[binding.targetRef] = struct{}{}
		bindings = append(bindings, binding)
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].targetRef < bindings[j].targetRef })
	return bindings, nil
}

func parseModuleRenderInputBinding(raw map[string]any, path string) (moduleRenderInputBinding, error) {
	allowed := map[string]struct{}{
		"targetRef": {}, "sourceRef": {}, "valueType": {}, "cardinality": {}, "required": {}, "defaultValue": {},
	}
	for field := range raw {
		if _, ok := allowed[field]; !ok {
			return moduleRenderInputBinding{}, fail(ErrContractConflict, path+"."+field, "field is not part of the closed module input binding contract")
		}
	}
	targetRef, err := stringField(raw, path, "targetRef")
	if err != nil {
		return moduleRenderInputBinding{}, err
	}
	sourceRef, err := stringField(raw, path, "sourceRef")
	if err != nil {
		return moduleRenderInputBinding{}, err
	}
	valueType, err := stringField(raw, path, "valueType")
	if err != nil {
		return moduleRenderInputBinding{}, err
	}
	cardinality, err := stringField(raw, path, "cardinality")
	if err != nil {
		return moduleRenderInputBinding{}, err
	}
	requiredValue, exists := raw["required"]
	if !exists {
		return moduleRenderInputBinding{}, fail(ErrContractConflict, path+".required", "required boolean is missing")
	}
	required, ok := requiredValue.(bool)
	if !ok {
		return moduleRenderInputBinding{}, fail(ErrContractConflict, path+".required", "expected boolean")
	}
	defaultValue, hasDefault := raw["defaultValue"]
	if required && hasDefault {
		return moduleRenderInputBinding{}, fail(ErrContractConflict, path+".defaultValue", "required bindings cannot declare a default")
	}
	if !required && !hasDefault {
		return moduleRenderInputBinding{}, fail(ErrContractConflict, path+".defaultValue", "optional bindings require an exact typed default")
	}
	if err := validateModuleInputBindingShape(sourceRef, valueType, cardinality, defaultValue, hasDefault, path); err != nil {
		return moduleRenderInputBinding{}, err
	}
	clone, err := cloneObject(raw, true)
	if err != nil {
		return moduleRenderInputBinding{}, err
	}
	return moduleRenderInputBinding{
		targetRef: targetRef, sourceRef: sourceRef, valueType: valueType, cardinality: cardinality,
		required: required, defaultValue: defaultValue, hasDefault: hasDefault, raw: clone,
	}, nil
}

func validateModuleInputBindingShape(sourceRef, valueType, cardinality string, defaultValue any, hasDefault bool, path string) error {
	switch sourceRef {
	case moduleInputSourceDeviceEnrollment:
		if valueType != moduleInputTypeDeviceEnrollment || cardinality != "single" {
			return fail(ErrContractConflict, path, "identity.deviceEnrollment requires type %q and single cardinality", moduleInputTypeDeviceEnrollment)
		}
		if hasDefault {
			projected, err := projectPublicDeviceEnrollment(defaultValue, path+".defaultValue", true)
			if err != nil {
				return err
			}
			if equal, err := canonicalEqual(defaultValue, projected); err != nil {
				return err
			} else if !equal {
				return fail(ErrContractConflict, path+".defaultValue", "device enrollment default is not the exact public projection")
			}
		}
	case moduleInputSourceNetworkRoutes:
		if valueType != moduleInputTypeNetworkRoutesV4 || cardinality != "list" {
			return fail(ErrContractConflict, path, "network.routes requires current type %q and list cardinality", moduleInputTypeNetworkRoutesV4)
		}
		if hasDefault {
			projected, err := projectPublicRouteList(defaultValue, path+".defaultValue", true, true)
			if err != nil {
				return err
			}
			if equal, err := canonicalEqual(defaultValue, projected); err != nil {
				return err
			} else if !equal {
				return fail(ErrContractConflict, path+".defaultValue", "route default is not the exact secret-safe public projection")
			}
		}
	default:
		return fail(ErrContractConflict, path+".sourceRef", "unsupported resolved-plan input source %q", sourceRef)
	}
	return nil
}

func moduleRenderInputBindingsAny(bindings []moduleRenderInputBinding) []any {
	result := make([]any, 0, len(bindings))
	for _, binding := range bindings {
		result = append(result, binding.raw)
	}
	return result
}

func bindResolvedModuleRenderInputs(modules []any, source moduleRenderInputSource) error {
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
			bindings, err := moduleRenderInputBindings(unit, unitPath)
			if err != nil {
				return err
			}
			values, err := objectField(unit, unitPath, "values")
			if err != nil {
				return err
			}
			for _, binding := range bindings {
				if _, exists := values[binding.targetRef]; exists {
					return fail(ErrContractConflict, unitPath+".values."+binding.targetRef, "compiler-bound public input was already populated")
				}
				value, available, err := source.resolve(binding)
				if err != nil {
					return fail(ErrContractConflict, unitPath+".inputBindings."+binding.targetRef, "%v", err)
				}
				if !available {
					if binding.required {
						return fail(ErrUnrealizedModule, unitPath+".inputBindings."+binding.targetRef, "required resolved-plan source %q is unavailable", binding.sourceRef)
					}
					value = binding.defaultValue
				}
				normalized, err := normalizeJSON(value, false, unitPath+".values."+binding.targetRef)
				if err != nil {
					return err
				}
				values[binding.targetRef] = normalized
			}
			unit["inputBindings"] = moduleRenderInputBindingsAny(bindings)
			unit["values"] = values
		}
	}
	return nil
}

func (source moduleRenderInputSource) resolve(binding moduleRenderInputBinding) (any, bool, error) {
	switch binding.sourceRef {
	case moduleInputSourceDeviceEnrollment:
		if source.identity == nil {
			return nil, false, nil
		}
		value, exists := source.identity["deviceEnrollment"]
		if !exists || value == nil {
			return nil, false, nil
		}
		projected, err := projectPublicDeviceEnrollment(value, "resolvedPlan.identity.deviceEnrollment", false)
		return projected, err == nil, err
	case moduleInputSourceNetworkRoutes:
		if source.network == nil {
			return nil, false, nil
		}
		if _, exists := source.network["routes"]; !exists {
			return nil, false, nil
		}
		projected, err := projectPublicRouteListFromNetwork(source.network, source.gates, "resolvedPlan.network", true, true)
		return projected, err == nil, err
	default:
		return nil, false, fmt.Errorf("unsupported resolved-plan input source %q", binding.sourceRef)
	}
}

func projectPublicDeviceEnrollment(value any, path string, alreadyPublic bool) (map[string]any, error) {
	input, err := asObject(value, path)
	if err != nil {
		return nil, err
	}
	stringsOut := map[string]string{}
	for _, field := range []string{"mode", "authoritySiteRef", "endpointExposure", "hardwareBackedKey"} {
		stringsOut[field], err = stringField(input, path, field)
		if err != nil {
			return nil, err
		}
	}
	result := map[string]any{}
	for key, item := range stringsOut {
		result[key] = item
	}
	for _, field := range []string{
		"remoteEnrollment", "requireOwnerStepUp", "requireLocalPairingProof", "requireDeviceGeneratedKey",
		"requirePossessionProof", "revocationSupported",
	} {
		item, exists := input[field]
		boolean, ok := item.(bool)
		if !exists || !ok {
			return nil, fail(ErrInvalidInput, path+"."+field, "expected boolean")
		}
		result[field] = boolean
	}
	lifetimeField := "credentialTTLSeconds"
	if alreadyPublic {
		lifetimeField = "lifetimeSeconds"
	}
	lifetime, err := intField(input, path, lifetimeField)
	if err != nil {
		return nil, err
	}
	result["lifetimeSeconds"] = lifetime
	return result, nil
}

func projectPublicRouteList(value any, path string, withProbe, withAuthority bool) ([]any, error) {
	raw, ok := value.([]any)
	if !ok {
		return nil, fail(ErrInvalidInput, path, "expected route list, got %T", value)
	}
	result := make([]any, 0, len(raw))
	for index, item := range raw {
		route, err := asObject(item, fmt.Sprintf("%s[%d]", path, index))
		if err != nil {
			return nil, err
		}
		pool, err := objectField(route, fmt.Sprintf("%s[%d]", path, index), "backendPool")
		if err != nil {
			return nil, err
		}
		var probe map[string]any
		if withProbe {
			rawProbe, err := objectField(route, fmt.Sprintf("%s[%d]", path, index), "healthProbe")
			if err != nil {
				return nil, err
			}
			probe, err = projectPublicRouteHealthProbe(rawProbe, route, pool, fmt.Sprintf("%s[%d].healthProbe", path, index), true)
			if err != nil {
				return nil, err
			}
		}
		projected, err := projectPublicRoute(route, pool, probe, fmt.Sprintf("%s[%d]", path, index), withAuthority)
		if err != nil {
			return nil, err
		}
		result = append(result, projected)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].(map[string]any)["id"].(string) < result[j].(map[string]any)["id"].(string)
	})
	return result, nil
}

func projectPublicRouteListFromNetwork(network, gates map[string]any, path string, withProbe, withAuthority bool) ([]any, error) {
	routes, err := objectListField(network, path, "routes")
	if err != nil {
		return nil, err
	}
	pools, err := objectListField(network, path, "backendPools")
	if err != nil {
		return nil, err
	}
	poolsByID := make(map[string]map[string]any, len(pools))
	for index, pool := range pools {
		poolID, err := stringField(pool, fmt.Sprintf("%s.backendPools[%d]", path, index), "id")
		if err != nil {
			return nil, err
		}
		poolsByID[poolID] = pool
	}
	healthByID := map[string]map[string]any{}
	if withProbe {
		if gates == nil {
			return nil, fail(ErrInvalidInput, "resolvedPlan.gates", "route health gates are unavailable for v3 projection")
		}
		health, err := objectListField(gates, "resolvedPlan.gates", "health")
		if err != nil {
			return nil, err
		}
		for index, gate := range health {
			gateID, err := stringField(gate, fmt.Sprintf("resolvedPlan.gates.health[%d]", index), "id")
			if err != nil {
				return nil, err
			}
			healthByID[gateID] = gate
		}
	}
	result := make([]any, 0, len(routes))
	for index, route := range routes {
		routePath := fmt.Sprintf("%s.routes[%d]", path, index)
		poolRef, err := stringField(route, routePath, "backendPoolRef")
		if err != nil {
			return nil, err
		}
		pool, exists := poolsByID[poolRef]
		if !exists {
			return nil, fail(ErrContractConflict, routePath+".backendPoolRef", "backend pool %q does not exist", poolRef)
		}
		var probe map[string]any
		if withProbe {
			healthRef, err := stringField(route, routePath, "healthGateRef")
			if err != nil {
				return nil, err
			}
			healthGate, exists := healthByID[healthRef]
			if !exists {
				return nil, fail(ErrContractConflict, routePath+".healthGateRef", "health gate %q does not exist", healthRef)
			}
			probe, err = projectPublicRouteHealthProbe(healthGate, route, pool, routePath+".healthProbe", false)
			if err != nil {
				return nil, err
			}
		}
		projected, err := projectPublicRoute(route, pool, probe, routePath, withAuthority)
		if err != nil {
			return nil, err
		}
		result = append(result, projected)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].(map[string]any)["id"].(string) < result[j].(map[string]any)["id"].(string)
	})
	return result, nil
}

func projectPublicRoute(route, pool, probe map[string]any, path string, withAuthority bool) (map[string]any, error) {
	result := map[string]any{}
	for _, field := range []string{"id", "serviceRef", "moduleRef", "originSiteRef", "exposure", "protocol", "upstreamProtocol", "healthGateRef", "backendPoolRef"} {
		value, err := stringField(route, path, field)
		if err != nil {
			return nil, err
		}
		result[field] = value
	}
	for _, field := range []string{"port", "targetPort"} {
		value, err := intField(route, path, field)
		if err != nil {
			return nil, err
		}
		result[field] = value
	}
	for _, field := range []string{"host", "path"} {
		if value, exists, err := optionalStringField(route, path, field); err != nil {
			return nil, err
		} else if exists {
			result[field] = value
		}
	}
	nodes, err := stringListField(route, path, "originNodeRefs", true)
	if err != nil {
		return nil, err
	}
	result["originNodeRefs"] = stringSliceAny(sortStringsUnique(nodes))
	publicPool, err := projectPublicBackendPool(pool, path+".backendPool")
	if err != nil {
		return nil, err
	}
	result["backendPool"] = publicPool
	if probe != nil {
		result["healthProbe"] = probe
	}
	if withAuthority {
		authorities, err := projectPublicRouteCapabilityAuthorities(route, path)
		if err != nil {
			return nil, err
		}
		result["capabilityAuthorities"] = authorities
	}
	access, err := objectField(route, path, "access")
	if err != nil {
		return nil, err
	}
	result["access"], err = projectPublicRouteAccess(access, path+".access")
	if err != nil {
		return nil, err
	}
	tls, err := objectField(route, path, "tls")
	if err != nil {
		return nil, err
	}
	required, ok := tls["required"].(bool)
	if !ok {
		return nil, fail(ErrInvalidInput, path+".tls.required", "expected boolean")
	}
	mode, err := stringField(tls, path+".tls", "mode")
	if err != nil {
		return nil, err
	}
	publicTLS := map[string]any{"required": required, "mode": mode}
	if minVersion, exists, err := optionalStringField(tls, path+".tls", "minVersion"); err != nil {
		return nil, err
	} else if exists {
		publicTLS["minVersion"] = minVersion
	}
	for _, field := range []string{"profileRef", "issuerRef"} {
		if value, exists, err := optionalStringField(tls, path+".tls", field); err != nil {
			return nil, err
		} else if exists {
			publicTLS[field] = value
		}
	}
	if ownerCapabilityRef, exists, err := optionalStringField(tls, path+".tls", "ownerCapabilityRef"); err != nil {
		return nil, err
	} else if exists {
		publicTLS["ownerCapabilityRef"] = ownerCapabilityRef
	}
	result["tls"] = publicTLS
	return result, nil
}

func projectPublicRouteCapabilityAuthorities(route map[string]any, path string) ([]any, error) {
	requirements, err := routeCapabilityRequirementsFromProjection(route, path)
	if err != nil {
		return nil, err
	}
	seenRoles := make(map[string]struct{}, len(requirements))
	result := make([]any, 0, len(requirements))
	for _, requirement := range requirements {
		if requirement.role != "access" && requirement.role != "transport" && requirement.role != "edge" && requirement.role != "egress" {
			return nil, fail(ErrContractConflict, path+".capabilityRealizations", "unsupported route capability role %q", requirement.role)
		}
		if _, duplicate := seenRoles[requirement.role]; duplicate {
			return nil, fail(ErrContractConflict, path+".capabilityRealizations", "route capability role %q is duplicated", requirement.role)
		}
		seenRoles[requirement.role] = struct{}{}
		result = append(result, map[string]any{"capabilityRef": requirement.capabilityRef, "role": requirement.role})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].(map[string]any)["capabilityRef"].(string) < result[j].(map[string]any)["capabilityRef"].(string)
	})
	return result, nil
}

func projectPublicRouteHealthProbe(input, route, pool map[string]any, path string, alreadyPublic bool) (map[string]any, error) {
	kind, err := stringField(input, path, "kind")
	if err != nil {
		return nil, err
	}
	protocol, err := stringField(input, path, "protocol")
	if err != nil {
		return nil, err
	}
	port, err := intField(input, path, "port")
	if err != nil {
		return nil, err
	}
	timeoutSeconds, err := intField(input, path, "timeoutSeconds")
	if err != nil {
		return nil, err
	}
	if timeoutSeconds < 1 || timeoutSeconds > 300 {
		return nil, fail(ErrContractConflict, path+".timeoutSeconds", "timeout must be between 1 and 300 seconds")
	}
	if !alreadyPublic {
		execution, err := stringField(input, path, "execution")
		if err != nil {
			return nil, err
		}
		if execution != "probe" {
			return nil, fail(ErrContractConflict, path+".execution", "v3 renderer input requires an executable probe descriptor")
		}
		routeID, err := stringField(route, path, "id")
		if err != nil {
			return nil, err
		}
		poolID, err := stringField(pool, path, "id")
		if err != nil {
			return nil, err
		}
		for field, expected := range map[string]string{"targetKind": "route", "targetRef": routeID, "routeRef": routeID, "backendPoolRef": poolID} {
			actual, err := stringField(input, path, field)
			if err != nil {
				return nil, err
			}
			if actual != expected {
				return nil, fail(ErrContractConflict, path+"."+field, "route health gate is not bound to the exact route and backend pool")
			}
		}
	}
	routeProtocol, err := stringField(route, path, "upstreamProtocol")
	if err != nil {
		return nil, err
	}
	poolProtocol, err := stringField(pool, path, "upstreamProtocol")
	if err != nil {
		return nil, err
	}
	routePort, err := intField(route, path, "targetPort")
	if err != nil {
		return nil, err
	}
	poolPort, err := intField(pool, path, "targetPort")
	if err != nil {
		return nil, err
	}
	if protocol != routeProtocol || protocol != poolProtocol || port != routePort || port != poolPort {
		return nil, fail(ErrContractConflict, path, "health probe protocol and port do not match the exact route backend pool")
	}
	result := map[string]any{"kind": kind, "protocol": protocol, "port": port, "timeoutSeconds": timeoutSeconds}
	switch kind {
	case "http":
		if protocol != "http" && protocol != "https" {
			return nil, fail(ErrContractConflict, path+".protocol", "http probe requires http or https")
		}
		method, err := stringField(input, path, "method")
		if err != nil || method != "GET" {
			return nil, fail(ErrContractConflict, path+".method", "http probe requires GET")
		}
		redirects, exists := input["followRedirects"]
		if !exists || redirects != false {
			return nil, fail(ErrContractConflict, path+".followRedirects", "http probe redirects must be disabled")
		}
		probePath, err := stringField(input, path, "path")
		if err != nil || !strings.HasPrefix(probePath, "/") {
			return nil, fail(ErrContractConflict, path+".path", "http probe path must be absolute")
		}
		statuses, exists := input["expectedStatuses"].([]any)
		if !exists || len(statuses) == 0 {
			return nil, fail(ErrContractConflict, path+".expectedStatuses", "http probe requires expected statuses")
		}
		seen := map[int]struct{}{}
		for index := range statuses {
			status, err := intField(map[string]any{"status": statuses[index]}, path+".expectedStatuses", "status")
			if err != nil || status < 100 || status > 599 {
				return nil, fail(ErrContractConflict, fmt.Sprintf("%s.expectedStatuses[%d]", path, index), "invalid HTTP status")
			}
			if _, duplicate := seen[status]; duplicate {
				return nil, fail(ErrContractConflict, path+".expectedStatuses", "statuses must be unique")
			}
			seen[status] = struct{}{}
		}
		result["method"], result["followRedirects"] = "GET", false
		result["path"], result["expectedStatuses"] = probePath, statuses
	case "tcp":
		if protocol != "tcp" {
			return nil, fail(ErrContractConflict, path+".protocol", "tcp probe requires tcp")
		}
	default:
		return nil, fail(ErrContractConflict, path+".kind", "v3 renderer input requires http or tcp probe")
	}
	return result, nil
}

func projectPublicBackendPool(pool map[string]any, path string) (map[string]any, error) {
	protocol, err := stringField(pool, path, "upstreamProtocol")
	if err != nil {
		return nil, err
	}
	port, err := intField(pool, path, "targetPort")
	if err != nil {
		return nil, err
	}
	members, err := objectListField(pool, path, "members")
	if err != nil {
		return nil, err
	}
	projectedMembers := make([]any, 0, len(members))
	for index, member := range members {
		memberPath := fmt.Sprintf("%s.members[%d]", path, index)
		projected := map[string]any{}
		for _, field := range []string{"siteRef", "nodeRef", "instanceRef"} {
			value, err := stringField(member, memberPath, field)
			if err != nil {
				return nil, err
			}
			projected[field] = value
		}
		projectedMembers = append(projectedMembers, projected)
	}
	return map[string]any{"upstreamProtocol": protocol, "targetPort": port, "members": projectedMembers}, nil
}

func projectPublicRouteAccess(access map[string]any, path string) (map[string]any, error) {
	result := map[string]any{}
	for _, field := range []string{"exposure", "policyExposure", "authentication", "privilege", "policyRef"} {
		value, err := stringField(access, path, field)
		if err != nil {
			return nil, err
		}
		result[field] = value
	}
	for _, field := range []string{"enrolledDeviceRequired", "ownerStepUpRequired", "lanStepDown", "defaultClosed"} {
		value, exists := access[field]
		boolean, ok := value.(bool)
		if !exists || !ok {
			return nil, fail(ErrInvalidInput, path+"."+field, "expected boolean")
		}
		result[field] = boolean
	}
	for _, field := range []string{"allowedSiteRefs", "allowedMethods"} {
		if _, exists := access[field]; !exists {
			continue
		}
		values, err := stringListField(access, path, field, false)
		if err != nil {
			return nil, err
		}
		result[field] = stringSliceAny(sortStringsUnique(values))
	}
	return result, nil
}

func moduleInputStringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
