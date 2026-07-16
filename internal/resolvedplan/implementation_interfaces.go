package resolvedplan

import (
	"fmt"
	"sort"
)

const dockerHTTPReadonlyInterfaceKind = "docker-http-readonly-v1"
const dockerSocketDirectInterfaceKind = "docker-socket-direct-v1"
const dockerSocketPathSourceDaemonBinding = "daemon-binding"

var dockerHTTPReadonlyBaselineScopes = []string{
	"CONTAINERS",
	"EVENTS",
	"NETWORKS",
	"PING",
	"VERSION",
}

// moduleRenderPlacementContext contains only nodes already authorized for the
// selected module by provider and site-kind resolution. Render-unit placement
// may narrow that set only when the authority contract is unambiguous; it may
// never manufacture a node or choose the lexicographically first candidate.
type moduleRenderPlacementContext struct {
	siteRefs    []string
	nodeRefs    []string
	nodeSites   map[string]string
	nodeDaemons map[string]map[string]runtimeDaemonFact
}

func newModuleRenderPlacementContext(siteRefs, nodeRefs []string, nodes []nodeView) moduleRenderPlacementContext {
	context := moduleRenderPlacementContext{
		siteRefs:    sortStringsUnique(siteRefs),
		nodeRefs:    sortStringsUnique(nodeRefs),
		nodeSites:   make(map[string]string, len(nodeRefs)),
		nodeDaemons: make(map[string]map[string]runtimeDaemonFact, len(nodeRefs)),
	}
	eligible := make(map[string]struct{}, len(context.nodeRefs))
	for _, nodeRef := range context.nodeRefs {
		eligible[nodeRef] = struct{}{}
	}
	for _, node := range nodes {
		if _, ok := eligible[node.id]; ok {
			context.nodeSites[node.id] = node.siteRef
			context.nodeDaemons[node.id] = node.runtimeDaemons
		}
	}
	return context
}

func resolveModuleRenderUnitPlacement(moduleID, unitID string, contract map[string]any, context moduleRenderPlacementContext) (map[string]any, []string, []string, []any, error) {
	path := "catalog.modules." + moduleID + ".renderUnits." + unitID + ".placement"
	placement, exists, err := optionalObjectField(contract, "catalog.modules."+moduleID+".renderUnits."+unitID, "placement")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if !exists {
		placement = map[string]any{"scope": "module", "cardinality": "single"}
	}
	scope, err := stringField(placement, path, "scope")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	cardinality, err := stringField(placement, path, "cardinality")
	if err != nil {
		return nil, nil, nil, nil, err
	}

	resolved := map[string]any{"scope": scope, "cardinality": cardinality}
	daemonRef, hasDaemonRef, err := optionalStringField(placement, path, "daemonRef")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if hasDaemonRef {
		resolved["daemonRef"] = daemonRef
	}

	if err := validateRenderUnitPlacementShape(scope, cardinality, hasDaemonRef, len(context.nodeRefs), path); err != nil {
		return nil, nil, nil, nil, err
	}

	if len(context.nodeRefs) == 0 {
		return nil, nil, nil, nil, fail(ErrUnresolvedPlacement, path, "render unit has no already eligible nodes")
	}
	siteRefs := make([]string, 0, len(context.nodeRefs))
	daemonBindings := make([]any, 0, len(context.nodeRefs))
	for _, nodeRef := range context.nodeRefs {
		siteRef, ok := context.nodeSites[nodeRef]
		if !ok || siteRef == "" {
			return nil, nil, nil, nil, fail(ErrUnresolvedPlacement, path, "eligible node %q has no resolved site identity", nodeRef)
		}
		if !contains(context.siteRefs, siteRef) {
			return nil, nil, nil, nil, fail(ErrContractConflict, path, "eligible node %q resolves to site %q outside the module placement", nodeRef, siteRef)
		}
		siteRefs = append(siteRefs, siteRef)
		if cardinality == "one-per-daemon" {
			daemon, exists := context.nodeDaemons[nodeRef][daemonRef]
			if !exists {
				return nil, nil, nil, nil, fail(ErrUnresolvedPlacement, path+".daemonRef", "eligible node %q has no uniquely observed runtime daemon %q", nodeRef, daemonRef)
			}
			daemonBindings = append(daemonBindings, map[string]any{
				"siteRef": siteRef, "nodeRef": nodeRef, "daemonRef": daemonRef,
				"instanceRef": daemon.instanceRef, "engine": daemon.engine, "socketPath": daemon.socketPath,
			})
		}
	}
	return resolved, sortStringsUnique(siteRefs), append([]string(nil), context.nodeRefs...), daemonBindings, nil
}

// resolveModuleRenderUnitInstances lowers one immutable logical render-unit
// contract into the exact executable invocations authorized by its resolved
// placement. A module-scoped unit deliberately has no fabricated locality;
// node-scoped instances always carry one exact site and node identity.
func resolveModuleRenderUnitInstances(moduleID, unitID string, placement map[string]any, context moduleRenderPlacementContext, daemonBindings []any) ([]any, error) {
	placementPath := "catalog.modules." + moduleID + ".renderUnits." + unitID + ".placement"
	scope, cardinality, err := resolvedRenderPlacementIdentity(placement, placementPath)
	if err != nil {
		return nil, err
	}
	if scope == "module" && cardinality == "single" {
		return []any{map[string]any{
			"id": unitID + "-logical", "scope": "module", "outputs": []any{},
		}}, nil
	}
	if scope != "node-local" {
		return nil, fail(ErrContractConflict, placementPath, "unsupported render-unit placement %q/%q", scope, cardinality)
	}
	daemonByNode, err := indexResolvedRenderDaemonBindings(moduleID, unitID, daemonBindings)
	if err != nil {
		return nil, err
	}
	instances, err := resolveNodeLocalRenderInstances(unitID, placementPath, cardinality, context, daemonByNode)
	if err != nil {
		return nil, err
	}
	if err := validateResolvedRenderInstanceCardinality(instances, daemonByNode, cardinality, placementPath); err != nil {
		return nil, err
	}
	return instances, nil
}

func resolvedRenderPlacementIdentity(placement map[string]any, placementPath string) (string, string, error) {
	scope, err := stringField(placement, placementPath, "scope")
	if err != nil {
		return "", "", err
	}
	cardinality, err := stringField(placement, placementPath, "cardinality")
	if err != nil {
		return "", "", err
	}
	return scope, cardinality, nil
}

func indexResolvedRenderDaemonBindings(moduleID, unitID string, daemonBindings []any) (map[string]map[string]any, error) {
	daemonByNode := make(map[string]map[string]any, len(daemonBindings))
	for index, rawBinding := range daemonBindings {
		bindingPath := fmt.Sprintf("catalog.modules.%s.renderUnits.%s.daemonBindings[%d]", moduleID, unitID, index)
		binding, err := asObject(rawBinding, bindingPath)
		if err != nil {
			return nil, err
		}
		nodeRef, err := stringField(binding, bindingPath, "nodeRef")
		if err != nil {
			return nil, err
		}
		if _, duplicate := daemonByNode[nodeRef]; duplicate {
			return nil, fail(ErrContractConflict, bindingPath+".nodeRef", "node %q has more than one resolved daemon binding", nodeRef)
		}
		daemonByNode[nodeRef] = binding
	}
	return daemonByNode, nil
}

func resolveNodeLocalRenderInstances(
	unitID, placementPath, cardinality string,
	context moduleRenderPlacementContext,
	daemonByNode map[string]map[string]any,
) ([]any, error) {
	instances := make([]any, 0, len(context.nodeRefs))
	seenIDs := make(map[string]struct{}, len(context.nodeRefs))
	for _, nodeRef := range context.nodeRefs {
		instanceID, instance, err := resolveNodeLocalRenderInstance(unitID, nodeRef, placementPath, cardinality, context, daemonByNode)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seenIDs[instanceID]; duplicate {
			return nil, fail(ErrContractConflict, placementPath, "render instance ID %q is not unique", instanceID)
		}
		seenIDs[instanceID] = struct{}{}
		instances = append(instances, instance)
	}
	return instances, nil
}

func resolveNodeLocalRenderInstance(
	unitID, nodeRef, placementPath, cardinality string,
	context moduleRenderPlacementContext,
	daemonByNode map[string]map[string]any,
) (string, map[string]any, error) {
	siteRef, exists := context.nodeSites[nodeRef]
	if !exists || siteRef == "" {
		return "", nil, fail(ErrUnresolvedPlacement, placementPath, "eligible node %q has no resolved site identity", nodeRef)
	}
	instanceID := unitID + "-node-" + nodeRef
	instance := map[string]any{
		"id": instanceID, "scope": "node-local", "siteRef": siteRef, "nodeRef": nodeRef, "outputs": []any{},
	}
	if cardinality == "one-per-daemon" {
		return resolveDaemonRenderInstance(instanceID, nodeRef, placementPath, instance, daemonByNode)
	}
	if cardinality != "single" && cardinality != "one-per-node" {
		return "", nil, fail(ErrContractConflict, placementPath, "unsupported node-local render-unit cardinality %q", cardinality)
	}
	return instanceID, instance, nil
}

func resolveDaemonRenderInstance(
	instanceID, nodeRef, placementPath string,
	instance map[string]any,
	daemonByNode map[string]map[string]any,
) (string, map[string]any, error) {
	binding, exists := daemonByNode[nodeRef]
	if !exists {
		return "", nil, fail(ErrUnresolvedPlacement, placementPath+".daemonRef", "eligible node %q has no exact resolved daemon binding", nodeRef)
	}
	daemonRef, err := stringField(binding, placementPath+".daemonBinding", "daemonRef")
	if err != nil {
		return "", nil, err
	}
	daemonInstanceRef, err := stringField(binding, placementPath+".daemonBinding", "instanceRef")
	if err != nil {
		return "", nil, err
	}
	daemonEngine, err := stringField(binding, placementPath+".daemonBinding", "engine")
	if err != nil {
		return "", nil, err
	}
	daemonSocketPath, err := stringField(binding, placementPath+".daemonBinding", "socketPath")
	if err != nil {
		return "", nil, err
	}
	if err := validateUnixSocketPath(daemonSocketPath, placementPath+".daemonBinding.socketPath", ErrContractConflict); err != nil {
		return "", nil, err
	}
	instanceID += "-daemon-" + daemonInstanceRef
	instance["id"] = instanceID
	instance["daemonRef"] = daemonRef
	instance["daemonInstanceRef"] = daemonInstanceRef
	instance["daemonEngine"] = daemonEngine
	instance["daemonSocketPath"] = daemonSocketPath
	return instanceID, instance, nil
}

func validateResolvedRenderInstanceCardinality(
	instances []any,
	daemonByNode map[string]map[string]any,
	cardinality, placementPath string,
) error {
	if len(instances) == 0 {
		return fail(ErrUnresolvedPlacement, placementPath, "node-local render unit has no executable instances")
	}
	if cardinality == "single" && len(instances) != 1 {
		return fail(ErrUnresolvedPlacement, placementPath, "node-local single placement resolved %d instances", len(instances))
	}
	if cardinality == "one-per-daemon" && len(daemonByNode) != len(instances) {
		return fail(ErrContractConflict, placementPath+".daemonRef", "daemon bindings do not map one-to-one to render instances")
	}
	return nil
}

func validateRenderUnitPlacementShape(scope, cardinality string, hasDaemonRef bool, eligibleNodes int, path string) error {
	switch scope + "/" + cardinality {
	case "module/single":
		if hasDaemonRef {
			return fail(ErrContractConflict, path+".daemonRef", "module placement cannot select a daemon")
		}
	case "node-local/single":
		if hasDaemonRef {
			return fail(ErrContractConflict, path+".daemonRef", "single node-local placement cannot select a daemon")
		}
		if eligibleNodes != 1 {
			return fail(ErrUnresolvedPlacement, path, "node-local single placement requires exactly one already eligible node; got %d (an implicit first-node choice is forbidden)", eligibleNodes)
		}
	case "node-local/one-per-node":
		if hasDaemonRef {
			return fail(ErrContractConflict, path+".daemonRef", "one-per-node placement cannot select a daemon")
		}
	case "node-local/one-per-daemon":
		if !hasDaemonRef {
			return fail(ErrContractConflict, path+".daemonRef", "one-per-daemon placement requires an explicit daemonRef")
		}
	default:
		return fail(ErrContractConflict, path, "unsupported render-unit placement %q/%q", scope, cardinality)
	}
	return nil
}

func normalizeResolvedUnitInterfaces(unit map[string]any, path string) error {
	provided, err := normalizeImplementationInterfaceList(unit, path, "providesInterfaces", true)
	if err != nil {
		return err
	}
	required, err := normalizeImplementationInterfaceList(unit, path, "requiresInterfaces", false)
	if err != nil {
		return err
	}
	unit["providesInterfaces"] = provided
	unit["requiresInterfaces"] = required
	return nil
}

func normalizeImplementationInterfaceList(unit map[string]any, path, field string, provider bool) ([]any, error) {
	contracts, err := objectListOptional(unit, field)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(contracts))
	result := make([]any, 0, len(contracts))
	for index, contract := range contracts {
		contractPath := fmt.Sprintf("%s.%s[%d]", path, field, index)
		id, err := stringField(contract, contractPath, "id")
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fail(ErrContractConflict, contractPath+".id", "implementation interface %q is duplicated in render unit", id)
		}
		seen[id] = struct{}{}
		kind, err := stringField(contract, contractPath, "kind")
		if err != nil {
			return nil, err
		}
		if provider && kind != dockerHTTPReadonlyInterfaceKind {
			return nil, fail(ErrContractConflict, contractPath+".kind", "render units may only provide %q", dockerHTTPReadonlyInterfaceKind)
		}
		if !provider && kind != dockerHTTPReadonlyInterfaceKind && kind != dockerSocketDirectInterfaceKind {
			return nil, fail(ErrContractConflict, contractPath+".kind", "unsupported implementation interface kind %q", kind)
		}
		if _, forbidden := contract["providerBindings"]; forbidden {
			return nil, fail(ErrContractConflict, contractPath+".providerBindings", "provider bindings are compiler-owned and forbidden in catalog authority")
		}
		scopes, err := stringListField(contract, contractPath, "scopes", true)
		if err != nil {
			return nil, err
		}
		clone, err := cloneObject(contract, true)
		if err != nil {
			return nil, err
		}
		clone["scopes"] = stringSliceAny(sortStringsUnique(scopes))
		result = append(result, clone)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].(map[string]any)["id"].(string) < result[j].(map[string]any)["id"].(string)
	})
	return result, nil
}

type implementationInterfaceProvider struct {
	moduleRef          string
	unitRef            string
	instanceRef        string
	contract           map[string]any
	instance           map[string]any
	siteRef            string
	nodeRef            string
	daemonRef          string
	daemonInstanceRef  string
	networkRef         string
	networkInstanceRef string
	owner              map[string]any
	network            map[string]any
}

func resolveImplementationInterfaces(modules []any, nodes map[string]nodeView) ([]any, error) {
	providers, runtimeNetworks, err := indexResolvedImplementationProviders(modules, nodes)
	if err != nil {
		return nil, err
	}
	for _, rawModule := range modules {
		module := rawModule.(map[string]any)
		moduleID := module["id"].(string)
		units, err := objectListField(module, "modules."+moduleID, "renderUnits")
		if err != nil {
			return nil, err
		}
		for _, unit := range units {
			unitID, err := stringField(unit, "modules."+moduleID+".renderUnits", "id")
			if err != nil {
				return nil, err
			}
			if err := bindResolvedUnitRequirements(moduleID, unitID, unit, providers, nodes); err != nil {
				return nil, err
			}
		}
	}
	finalizeRuntimeNetworkOrder(runtimeNetworks, modules)
	return runtimeNetworks, nil
}

func indexResolvedImplementationProviders(modules []any, nodes map[string]nodeView) ([]implementationInterfaceProvider, []any, error) {
	providers := make([]implementationInterfaceProvider, 0)
	runtimeNetworks := make([]any, 0)
	seenNetworkIDs := map[string]struct{}{}
	for _, rawModule := range modules {
		module := rawModule.(map[string]any)
		moduleID := module["id"].(string)
		units, err := objectListField(module, "modules."+moduleID, "renderUnits")
		if err != nil {
			return nil, nil, err
		}
		for _, unit := range units {
			unitID, err := stringField(unit, "modules."+moduleID+".renderUnits", "id")
			if err != nil {
				return nil, nil, err
			}
			contracts, err := objectListOptional(unit, "providesInterfaces")
			if err != nil {
				return nil, nil, err
			}
			instances, err := objectListField(unit, "modules."+moduleID+".renderUnits."+unitID, "instances")
			if err != nil {
				return nil, nil, err
			}
			for _, instance := range instances {
				instance["networkBindings"] = []any{}
			}
			if len(contracts) == 0 {
				continue
			}
			if len(instances) == 0 {
				return nil, nil, fail(ErrContractConflict, "modules."+moduleID+".renderUnits."+unitID+".instances", "runtime-network provider contract has no exact render instances")
			}
			if err := validateRuntimeNetworkProviderPlacement(moduleID, unitID, unit); err != nil {
				return nil, nil, err
			}
			for _, contract := range contracts {
				for _, instance := range instances {
					provider, err := newImplementationInterfaceProvider(moduleID, unitID, contract, instance, nodes)
					if err != nil {
						return nil, nil, err
					}
					if _, duplicate := seenNetworkIDs[provider.networkInstanceRef]; duplicate {
						return nil, nil, fail(ErrContractConflict, "modules."+moduleID+".renderUnits."+unitID+".instances", "runtime network ID %q is not globally unique", provider.networkInstanceRef)
					}
					seenNetworkIDs[provider.networkInstanceRef] = struct{}{}
					providers = append(providers, provider)
					runtimeNetworks = append(runtimeNetworks, provider.network)
					appendInstanceNetworkBinding(provider.instance, runtimeNetworkBinding(provider, "provider", provider.owner["interfaceRef"].(string)))
				}
			}
		}
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].networkInstanceRef < providers[j].networkInstanceRef })
	sort.Slice(runtimeNetworks, func(i, j int) bool {
		return runtimeNetworks[i].(map[string]any)["id"].(string) < runtimeNetworks[j].(map[string]any)["id"].(string)
	})
	return providers, runtimeNetworks, nil
}

func bindResolvedUnitRequirements(moduleID, unitID string, unit map[string]any, providers []implementationInterfaceProvider, nodes map[string]nodeView) error {
	path := "modules." + moduleID + ".renderUnits." + unitID
	requirements, err := objectListOptional(unit, "requiresInterfaces")
	if err != nil {
		return err
	}
	instances, err := objectListField(unit, path, "instances")
	if err != nil {
		return err
	}
	for index, requirement := range requirements {
		requirementPath := fmt.Sprintf("%s.requiresInterfaces[%d]", path, index)
		kind, err := stringField(requirement, requirementPath, "kind")
		if err != nil {
			return err
		}
		if kind == dockerSocketDirectInterfaceKind {
			if err := validateResolvedDirectSocketRequirement(requirement, unit, path, requirementPath); err != nil {
				return err
			}
			delete(requirement, "providerBindings")
			continue
		}
		if kind != dockerHTTPReadonlyInterfaceKind {
			return fail(ErrContractConflict, requirementPath+".kind", "unsupported implementation interface kind %q", kind)
		}
		if len(instances) == 0 {
			return fail(ErrUnrealizedModule, requirementPath, "HTTP interface requirement has no exact consumer render instances")
		}
		consumerInterfaceRef, err := stringField(requirement, requirementPath, "id")
		if err != nil {
			return err
		}
		bindings := make([]any, 0, len(instances))
		for instanceIndex, consumerInstance := range instances {
			consumer, err := resolvedConsumerInstance(moduleID, unitID, consumerInstance, nodes, instanceIndex)
			if err != nil {
				return err
			}
			candidates, err := matchingImplementationProviders(requirement, consumer, providers)
			if err != nil {
				return fail(ErrContractConflict, requirementPath, "invalid interface contract: %v", err)
			}
			if len(candidates) == 0 {
				return fail(ErrUnrealizedModule, requirementPath, "no exact same-site/same-node/same-daemon provider exists for consumer instance %q", consumer.instanceRef)
			}
			if len(candidates) > 1 {
				return fail(ErrContractConflict, requirementPath, "consumer instance %q has %d matching provider instances; an implicit provider choice is forbidden", consumer.instanceRef, len(candidates))
			}
			provider := candidates[0]
			binding, err := implementationProviderBinding(provider, consumer.instanceRef)
			if err != nil {
				return err
			}
			bindings = append(bindings, binding)
			appendRuntimeNetworkMember(provider.network, runtimeNetworkMember("consumer", moduleID, unitID, consumer.instanceRef, consumerInterfaceRef))
			appendInstanceNetworkBinding(consumer.instance, runtimeNetworkBinding(provider, "consumer", consumerInterfaceRef))
		}
		sort.Slice(bindings, func(i, j int) bool {
			left := bindings[i].(map[string]any)
			right := bindings[j].(map[string]any)
			if left["consumerInstanceRef"] != right["consumerInstanceRef"] {
				return left["consumerInstanceRef"].(string) < right["consumerInstanceRef"].(string)
			}
			return left["networkInstanceRef"].(string) < right["networkInstanceRef"].(string)
		})
		requirement["providerBindings"] = bindings
	}
	return nil
}

func validateResolvedDirectSocketRequirement(requirement, unit map[string]any, unitPath, requirementPath string) error {
	endpoint, err := objectField(requirement, requirementPath, "endpoint")
	if err != nil {
		return err
	}
	pathSelection, err := parseDirectSocketEndpointPath(endpoint, requirementPath+".endpoint", ErrContractConflict)
	if err != nil {
		return err
	}
	requiredDaemonRef, err := stringField(requirement, requirementPath, "daemonRef")
	if err != nil {
		return err
	}
	daemonBindings, err := objectListField(unit, unitPath, "daemonBindings")
	if err != nil {
		return err
	}
	if len(daemonBindings) == 0 {
		return fail(ErrUnresolvedPlacement, requirementPath, "direct Docker socket requirement has no selected daemon bindings")
	}
	for index, binding := range daemonBindings {
		bindingPath := fmt.Sprintf("%s.daemonBindings[%d]", unitPath, index)
		bindingDaemonRef, err := stringField(binding, bindingPath, "daemonRef")
		if err != nil {
			return err
		}
		if bindingDaemonRef != requiredDaemonRef {
			return fail(ErrContractConflict, bindingPath+".daemonRef", "selected daemon %q does not match direct requirement daemon %q", bindingDaemonRef, requiredDaemonRef)
		}
		bindingEngine, err := stringField(binding, bindingPath, "engine")
		if err != nil {
			return err
		}
		if bindingEngine != "docker" {
			return fail(ErrContractConflict, bindingPath+".engine", "direct Docker socket requirement cannot bind runtime engine %q", bindingEngine)
		}
		bindingSocketPath, err := stringField(binding, bindingPath, "socketPath")
		if err != nil {
			return err
		}
		if err := validateUnixSocketPath(bindingSocketPath, bindingPath+".socketPath", ErrContractConflict); err != nil {
			return err
		}
		if !pathSelection.fromDaemonBinding && bindingSocketPath != pathSelection.fixedPath {
			return fail(ErrContractConflict, requirementPath+".endpoint.path", "direct requirement socket %q does not match selected daemon binding socket %q", pathSelection.fixedPath, bindingSocketPath)
		}
	}
	return nil
}

func matchingImplementationProviders(requirement map[string]any, consumer runtimeRenderInstance, providers []implementationInterfaceProvider) ([]implementationInterfaceProvider, error) {
	requiredDaemonRef, err := stringField(requirement, "requirement", "daemonRef")
	if err != nil {
		return nil, err
	}
	if consumer.daemonRef != "" && consumer.daemonRef != requiredDaemonRef {
		return nil, fail(ErrContractConflict, "requirement.daemonRef", "daemon-local consumer instance belongs to %q, not required daemon %q", consumer.daemonRef, requiredDaemonRef)
	}
	var matches []implementationInterfaceProvider
	for _, provider := range providers {
		if provider.nodeRef != consumer.nodeRef || provider.siteRef != consumer.siteRef {
			continue
		}
		if consumer.daemonInstanceRef != "" && (provider.daemonRef != consumer.daemonRef || provider.daemonInstanceRef != consumer.daemonInstanceRef) {
			continue
		}
		equal, err := implementationContractsCompatible(requirement, provider.contract)
		if err != nil {
			return nil, err
		}
		if equal {
			matches = append(matches, provider)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		left := matches[i].networkInstanceRef
		right := matches[j].networkInstanceRef
		return left < right
	})
	return matches, nil
}

func implementationContractsCompatible(requirement, provider map[string]any) (bool, error) {
	for _, field := range []string{"kind", "protocol", "version", "daemonRef", "policyProfile", "coLocation"} {
		want, err := stringField(requirement, "requirement", field)
		if err != nil {
			return false, err
		}
		have, err := stringField(provider, "provider", field)
		if err != nil {
			return false, err
		}
		if want != have {
			return false, nil
		}
	}
	wantEndpoint, err := objectField(requirement, "requirement", "endpoint")
	if err != nil {
		return false, err
	}
	haveEndpoint, err := objectField(provider, "provider", "endpoint")
	if err != nil {
		return false, err
	}
	for _, field := range []string{"ref", "visibility", "transport", "networkRef", "address"} {
		want, err := stringField(wantEndpoint, "requirement.endpoint", field)
		if err != nil {
			return false, err
		}
		have, err := stringField(haveEndpoint, "provider.endpoint", field)
		if err != nil {
			return false, err
		}
		if want != have {
			return false, nil
		}
	}
	wantPort, err := intField(wantEndpoint, "requirement.endpoint", "port")
	if err != nil {
		return false, err
	}
	havePort, err := intField(haveEndpoint, "provider.endpoint", "port")
	if err != nil {
		return false, err
	}
	if wantPort != havePort {
		return false, nil
	}
	wantedScopes, err := stringListField(requirement, "requirement", "scopes", true)
	if err != nil {
		return false, err
	}
	providedScopes, err := stringListField(provider, "provider", "scopes", true)
	if err != nil {
		return false, err
	}
	for _, scope := range wantedScopes {
		if !contains(providedScopes, scope) {
			return false, nil
		}
	}
	return true, nil
}

func implementationProviderBinding(provider implementationInterfaceProvider, consumerInstanceRef string) (map[string]any, error) {
	interfaceRef, err := stringField(provider.contract, "provider", "id")
	if err != nil {
		return nil, err
	}
	daemonRef, err := stringField(provider.contract, "provider", "daemonRef")
	if err != nil {
		return nil, err
	}
	policyProfile, err := stringField(provider.contract, "provider", "policyProfile")
	if err != nil {
		return nil, err
	}
	endpoint, err := objectField(provider.contract, "provider", "endpoint")
	if err != nil {
		return nil, err
	}
	endpointRef, err := stringField(endpoint, "provider.endpoint", "ref")
	if err != nil {
		return nil, err
	}
	networkRef, err := stringField(endpoint, "provider.endpoint", "networkRef")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"interfaceRef":        interfaceRef,
		"moduleRef":           provider.moduleRef,
		"unitRef":             provider.unitRef,
		"providerInstanceRef": provider.instanceRef,
		"consumerInstanceRef": consumerInstanceRef,
		"siteRef":             provider.siteRef,
		"nodeRef":             provider.nodeRef,
		"daemonRef":           daemonRef,
		"daemonInstanceRef":   provider.daemonInstanceRef,
		"networkRef":          networkRef,
		"networkInstanceRef":  provider.networkInstanceRef,
		"policyProfile":       policyProfile,
		"endpointRef":         endpointRef,
	}, nil
}
