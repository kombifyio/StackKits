package resolvedplan

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

type resolvedRuntimeListener struct {
	id               string
	moduleRef        string
	unitRef          string
	instanceRef      string
	nodeRef          string
	componentRef     string
	transport        string
	bindAddress      string
	port             int
	targetPort       int
	sharing          string
	listenerGroupRef string
	exposure         string
	sourceRouteRefs  []string
}

type runtimeListenerSocketKey struct {
	nodeRef   string
	transport string
	port      int
}

type runtimeListenerPhysicalBinding struct {
	address netip.Addr
	owner   string
}

func normalizeRuntimeListenerContracts(unit map[string]any, path string) error {
	listeners, err := objectListOptional(unit, "runtimeListeners")
	if err != nil {
		return err
	}
	normalized := make([]map[string]any, 0, len(listeners))
	for index, listener := range listeners {
		listenerPath := fmt.Sprintf("%s.runtimeListeners[%d]", path, index)
		clone, err := cloneObject(listener, true)
		if err != nil {
			return fmt.Errorf("%s: clone listener: %w", listenerPath, err)
		}
		sourceServiceRefs, err := stringListField(listener, listenerPath, "sourceServiceRefs", false)
		if err != nil {
			return err
		}
		clone["sourceServiceRefs"] = stringSliceAny(sortStringsUnique(sourceServiceRefs))
		normalized = append(normalized, clone)
	}
	sort.Slice(normalized, func(i, j int) bool {
		left, _ := normalized[i]["id"].(string)
		right, _ := normalized[j]["id"].(string)
		return left < right
	})
	unit["runtimeListeners"] = objectMapsAsAny(normalized)
	return nil
}

func buildRuntimeListeners(modules, routes []any) ([]any, error) {
	routeRefs, err := indexRuntimeListenerRouteRefs(routes)
	if err != nil {
		return nil, err
	}
	listeners := make([]resolvedRuntimeListener, 0)
	physicalBindings := map[runtimeListenerSocketKey][]runtimeListenerPhysicalBinding{}
	for moduleIndex, rawModule := range modules {
		modulePath := fmt.Sprintf("resolvedPlan.modules[%d]", moduleIndex)
		module, err := asObject(rawModule, modulePath)
		if err != nil {
			return nil, err
		}
		moduleRef, err := stringField(module, modulePath, "id")
		if err != nil {
			return nil, err
		}
		units, err := objectListField(module, modulePath, "renderUnits")
		if err != nil {
			return nil, err
		}
		for unitIndex, unit := range units {
			unitPath := fmt.Sprintf("%s.renderUnits[%d]", modulePath, unitIndex)
			unitRef, err := stringField(unit, unitPath, "id")
			if err != nil {
				return nil, err
			}
			declarations, err := objectListField(unit, unitPath, "runtimeListeners")
			if err != nil {
				return nil, err
			}
			if len(declarations) == 0 {
				continue
			}
			instances, err := objectListField(unit, unitPath, "instances")
			if err != nil {
				return nil, err
			}
			for instanceIndex, instance := range instances {
				instancePath := fmt.Sprintf("%s.instances[%d]", unitPath, instanceIndex)
				instanceRef, err := stringField(instance, instancePath, "id")
				if err != nil {
					return nil, err
				}
				nodeRef, exists, err := optionalStringField(instance, instancePath, "nodeRef")
				if err != nil {
					return nil, err
				}
				if !exists {
					return nil, fail(ErrContractConflict, instancePath+".nodeRef", "runtime listener declarations require an exact node-local instance")
				}
				for declarationIndex, declaration := range declarations {
					declarationPath := fmt.Sprintf("%s.runtimeListeners[%d]", unitPath, declarationIndex)
					listener, err := resolveRuntimeListener(moduleRef, unitRef, instanceRef, nodeRef, declaration, declarationPath, routeRefs)
					if err != nil {
						return nil, err
					}
					address, err := netip.ParseAddr(listener.bindAddress)
					if err != nil {
						return nil, fail(ErrContractConflict, declarationPath+".bindAddress", "invalid runtime listener IP address")
					}
					address = address.Unmap()
					socketKey := runtimeListenerSocketKey{
						nodeRef: listener.nodeRef, transport: listener.transport, port: listener.port,
					}
					for _, existing := range physicalBindings[socketKey] {
						if runtimeListenerAddressesOverlap(existing.address, address) {
							return nil, fail(ErrContractConflict, declarationPath, "physical runtime listener conflicts with %q", existing.owner)
						}
					}
					physicalBindings[socketKey] = append(physicalBindings[socketKey], runtimeListenerPhysicalBinding{
						address: address, owner: listener.id,
					})
					listeners = append(listeners, listener)
				}
			}
		}
	}
	sort.Slice(listeners, func(i, j int) bool { return listeners[i].id < listeners[j].id })
	result := make([]any, len(listeners))
	for index, listener := range listeners {
		projected := map[string]any{
			"id": listener.id, "moduleRef": listener.moduleRef, "unitRef": listener.unitRef,
			"instanceRef": listener.instanceRef, "nodeRef": listener.nodeRef,
			"componentRef": listener.componentRef, "transport": listener.transport,
			"bindAddress": listener.bindAddress, "port": listener.port, "targetPort": listener.targetPort,
			"sharing": listener.sharing, "sourceRouteRefs": stringSliceAny(listener.sourceRouteRefs), "exposure": listener.exposure,
		}
		if listener.listenerGroupRef != "" {
			projected["listenerGroupRef"] = listener.listenerGroupRef
		}
		result[index] = projected
	}
	return result, nil
}

// runtimeListenerAddressesOverlap is deliberately fail-closed for an
// unspecified address across IP families: whether an IPv6 wildcard also owns
// the IPv4 socket is host configuration, not provider- or Kit-owned policy.
func runtimeListenerAddressesOverlap(left, right netip.Addr) bool {
	return left == right || left.IsUnspecified() || right.IsUnspecified()
}

func resolveRuntimeListener(moduleRef, unitRef, instanceRef, nodeRef string, declaration map[string]any, path string, routeRefs map[string]map[string][]string) (resolvedRuntimeListener, error) {
	result := resolvedRuntimeListener{moduleRef: moduleRef, unitRef: unitRef, instanceRef: instanceRef, nodeRef: nodeRef}
	declarationRef, err := stringField(declaration, path, "id")
	if err != nil {
		return result, err
	}
	result.id = strings.Join([]string{moduleRef, unitRef, instanceRef, declarationRef}, "/")
	if result.componentRef, err = stringField(declaration, path, "componentRef"); err != nil {
		return result, err
	}
	if result.transport, err = stringField(declaration, path, "transport"); err != nil {
		return result, err
	}
	if result.bindAddress, err = stringField(declaration, path, "bindAddress"); err != nil {
		return result, err
	}
	if result.port, err = intField(declaration, path, "port"); err != nil {
		return result, err
	}
	if result.targetPort, err = intField(declaration, path, "targetPort"); err != nil {
		return result, err
	}
	if result.sharing, err = stringField(declaration, path, "sharing"); err != nil {
		return result, err
	}
	if result.listenerGroupRef, _, err = optionalStringField(declaration, path, "listenerGroupRef"); err != nil {
		return result, err
	}
	if result.exposure, err = stringField(declaration, path, "exposure"); err != nil {
		return result, err
	}
	sourceServiceRefs, err := stringListField(declaration, path, "sourceServiceRefs", false)
	if err != nil {
		return result, err
	}
	for _, serviceRef := range sourceServiceRefs {
		result.sourceRouteRefs = append(result.sourceRouteRefs, routeRefs[moduleRef][serviceRef]...)
	}
	result.sourceRouteRefs = sortStringsUnique(result.sourceRouteRefs)
	return result, nil
}

func indexRuntimeListenerRouteRefs(routes []any) (map[string]map[string][]string, error) {
	result := map[string]map[string][]string{}
	for index, rawRoute := range routes {
		path := fmt.Sprintf("resolvedPlan.network.routes[%d]", index)
		route, err := asObject(rawRoute, path)
		if err != nil {
			return nil, err
		}
		moduleRef, err := stringField(route, path, "moduleRef")
		if err != nil {
			return nil, err
		}
		serviceRef, err := stringField(route, path, "serviceRef")
		if err != nil {
			return nil, err
		}
		routeRef, err := stringField(route, path, "id")
		if err != nil {
			return nil, err
		}
		if result[moduleRef] == nil {
			result[moduleRef] = map[string][]string{}
		}
		result[moduleRef][serviceRef] = append(result[moduleRef][serviceRef], routeRef)
	}
	return result, nil
}

func validateRuntimeListenerProjection(plan ResolvedPlan) error {
	modules, err := objectListField(map[string]any(plan), "resolvedPlan", "modules")
	if err != nil {
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
	have, err := objectListField(network, "resolvedPlan.network", "runtimeListeners")
	if err != nil {
		return err
	}
	want, err := buildRuntimeListeners(objectMapsAsAny(modules), objectMapsAsAny(routes))
	if err != nil {
		return fmt.Errorf("recompute resolvedPlan.network.runtimeListeners: %w", err)
	}
	equal, err := canonicalEqual(
		map[string]any{"runtimeListeners": objectMapsAsAny(have)},
		map[string]any{"runtimeListeners": want},
	)
	if err != nil {
		return err
	}
	if !equal {
		return fmt.Errorf("resolvedPlan.network.runtimeListeners is not the exact compiler-derived projection")
	}
	return nil
}
