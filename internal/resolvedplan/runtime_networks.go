package resolvedplan

import (
	"fmt"
	"sort"
)

type runtimeRenderInstance struct {
	instance          map[string]any
	instanceRef       string
	siteRef           string
	nodeRef           string
	daemonRef         string
	daemonInstanceRef string
	daemonEngine      string
	daemonSocketPath  string
}

func validateRuntimeNetworkProviderPlacement(moduleID, unitID string, unit map[string]any) error {
	path := "modules." + moduleID + ".renderUnits." + unitID + ".placement"
	placement, err := objectField(unit, "modules."+moduleID+".renderUnits."+unitID, "placement")
	if err != nil {
		return err
	}
	scope, err := stringField(placement, path, "scope")
	if err != nil {
		return err
	}
	cardinality, err := stringField(placement, path, "cardinality")
	if err != nil {
		return err
	}
	if scope != "node-local" || cardinality != "one-per-daemon" {
		return fail(ErrContractConflict, path, "an HTTP implementation-interface provider must resolve as node-local/one-per-daemon, got %q/%q", scope, cardinality)
	}
	return nil
}

func newImplementationInterfaceProvider(
	moduleID, unitID string,
	contract, instance map[string]any,
	nodes map[string]nodeView,
) (implementationInterfaceProvider, error) {
	path := "modules." + moduleID + ".renderUnits." + unitID
	identity, err := resolvedRuntimeRenderInstance(instance, path+".instances", nodes)
	if err != nil {
		return implementationInterfaceProvider{}, err
	}
	if identity.daemonRef == "" || identity.daemonInstanceRef == "" {
		return implementationInterfaceProvider{}, fail(ErrContractConflict, path+".instances."+identity.instanceRef, "runtime-network provider instance must carry an exact daemonRef and daemonInstanceRef")
	}
	interfaceRef, err := stringField(contract, path+".providesInterfaces", "id")
	if err != nil {
		return implementationInterfaceProvider{}, err
	}
	contractDaemonRef, err := stringField(contract, path+".providesInterfaces."+interfaceRef, "daemonRef")
	if err != nil {
		return implementationInterfaceProvider{}, err
	}
	if contractDaemonRef != identity.daemonRef {
		return implementationInterfaceProvider{}, fail(ErrContractConflict, path+".providesInterfaces."+interfaceRef+".daemonRef", "contract daemon %q does not identify provider instance daemon %q", contractDaemonRef, identity.daemonRef)
	}
	endpoint, err := objectField(contract, path+".providesInterfaces."+interfaceRef, "endpoint")
	if err != nil {
		return implementationInterfaceProvider{}, err
	}
	networkRef, err := stringField(endpoint, path+".providesInterfaces."+interfaceRef+".endpoint", "networkRef")
	if err != nil {
		return implementationInterfaceProvider{}, err
	}
	networkInstanceRef := identity.instanceRef + "-network-" + networkRef + "-interface-" + interfaceRef
	owner := runtimeNetworkOwner(moduleID, unitID, identity.instanceRef, interfaceRef)
	network := map[string]any{
		"id":                networkInstanceRef,
		"networkRef":        networkRef,
		"siteRef":           identity.siteRef,
		"nodeRef":           identity.nodeRef,
		"daemonRef":         identity.daemonRef,
		"daemonInstanceRef": identity.daemonInstanceRef,
		"owner":             owner,
		"members": []any{
			runtimeNetworkMember("provider", moduleID, unitID, identity.instanceRef, interfaceRef),
		},
	}
	return implementationInterfaceProvider{
		moduleRef:          moduleID,
		unitRef:            unitID,
		instanceRef:        identity.instanceRef,
		contract:           contract,
		instance:           instance,
		siteRef:            identity.siteRef,
		nodeRef:            identity.nodeRef,
		daemonRef:          identity.daemonRef,
		daemonInstanceRef:  identity.daemonInstanceRef,
		networkRef:         networkRef,
		networkInstanceRef: networkInstanceRef,
		owner:              owner,
		network:            network,
	}, nil
}

func resolvedConsumerInstance(moduleID, unitID string, instance map[string]any, nodes map[string]nodeView, index int) (runtimeRenderInstance, error) {
	path := fmt.Sprintf("modules.%s.renderUnits.%s.instances[%d]", moduleID, unitID, index)
	identity, err := resolvedRuntimeRenderInstance(instance, path, nodes)
	if err != nil {
		return runtimeRenderInstance{}, err
	}
	if identity.nodeRef == "" || identity.siteRef == "" {
		return runtimeRenderInstance{}, fail(ErrUnresolvedPlacement, path, "HTTP interface consumer must be an exact node-local render instance")
	}
	return identity, nil
}

//nolint:gocyclo // Runtime-instance decoding validates every optional binding and its referenced node before use.
func resolvedRuntimeRenderInstance(instance map[string]any, path string, nodes map[string]nodeView) (runtimeRenderInstance, error) {
	instanceRef, err := stringField(instance, path, "id")
	if err != nil {
		return runtimeRenderInstance{}, err
	}
	scope, err := stringField(instance, path+"."+instanceRef, "scope")
	if err != nil {
		return runtimeRenderInstance{}, err
	}
	if scope != "node-local" {
		return runtimeRenderInstance{}, fail(ErrUnresolvedPlacement, path+"."+instanceRef+".scope", "runtime-network participant must be node-local, got %q", scope)
	}
	siteRef, err := stringField(instance, path+"."+instanceRef, "siteRef")
	if err != nil {
		return runtimeRenderInstance{}, err
	}
	nodeRef, err := stringField(instance, path+"."+instanceRef, "nodeRef")
	if err != nil {
		return runtimeRenderInstance{}, err
	}
	node, exists := nodes[nodeRef]
	if !exists {
		return runtimeRenderInstance{}, fail(ErrUnresolvedPlacement, path+"."+instanceRef+".nodeRef", "node %q has no resolved topology identity", nodeRef)
	}
	if node.siteRef != siteRef {
		return runtimeRenderInstance{}, fail(ErrContractConflict, path+"."+instanceRef+".siteRef", "instance site %q does not match node %q site %q", siteRef, nodeRef, node.siteRef)
	}
	daemonRef, hasDaemonRef, err := optionalStringField(instance, path+"."+instanceRef, "daemonRef")
	if err != nil {
		return runtimeRenderInstance{}, err
	}
	daemonInstanceRef, hasDaemonInstanceRef, err := optionalStringField(instance, path+"."+instanceRef, "daemonInstanceRef")
	if err != nil {
		return runtimeRenderInstance{}, err
	}
	daemonEngine, hasDaemonEngine, err := optionalStringField(instance, path+"."+instanceRef, "daemonEngine")
	if err != nil {
		return runtimeRenderInstance{}, err
	}
	daemonSocketPath, hasDaemonSocketPath, err := optionalStringField(instance, path+"."+instanceRef, "daemonSocketPath")
	if err != nil {
		return runtimeRenderInstance{}, err
	}
	if hasDaemonRef != hasDaemonInstanceRef || hasDaemonRef != hasDaemonEngine || hasDaemonRef != hasDaemonSocketPath {
		return runtimeRenderInstance{}, fail(ErrContractConflict, path+"."+instanceRef, "daemonRef, daemonInstanceRef, daemonEngine, and daemonSocketPath must either all be present or all be absent")
	}
	if hasDaemonRef {
		if err := validateUnixSocketPath(daemonSocketPath, path+"."+instanceRef+".daemonSocketPath", ErrContractConflict); err != nil {
			return runtimeRenderInstance{}, err
		}
		daemon, exists := node.runtimeDaemons[daemonRef]
		if !exists || daemon.instanceRef != daemonInstanceRef || daemon.engine != daemonEngine || daemon.socketPath != daemonSocketPath {
			return runtimeRenderInstance{}, fail(ErrContractConflict, path+"."+instanceRef, "daemon identity %q/%q/%q/%q is not the exact observed %q daemon on node %q", daemonRef, daemonInstanceRef, daemonEngine, daemonSocketPath, daemonRef, nodeRef)
		}
	}
	return runtimeRenderInstance{
		instance: instance, instanceRef: instanceRef, siteRef: siteRef, nodeRef: nodeRef,
		daemonRef: daemonRef, daemonInstanceRef: daemonInstanceRef,
		daemonEngine: daemonEngine, daemonSocketPath: daemonSocketPath,
	}, nil
}

func runtimeNetworkOwner(moduleRef, unitRef, instanceRef, interfaceRef string) map[string]any {
	return map[string]any{
		"moduleRef": moduleRef, "unitRef": unitRef, "instanceRef": instanceRef, "interfaceRef": interfaceRef,
	}
}

func runtimeNetworkMember(role, moduleRef, unitRef, instanceRef, interfaceRef string) map[string]any {
	return map[string]any{
		"role": role, "moduleRef": moduleRef, "unitRef": unitRef, "instanceRef": instanceRef, "interfaceRef": interfaceRef,
	}
}

func runtimeNetworkBinding(provider implementationInterfaceProvider, role, interfaceRef string) map[string]any {
	return map[string]any{
		"networkInstanceRef": provider.networkInstanceRef,
		"networkRef":         provider.networkRef,
		"role":               role,
		"interfaceRef":       interfaceRef,
		"siteRef":            provider.siteRef,
		"nodeRef":            provider.nodeRef,
		"daemonRef":          provider.daemonRef,
		"daemonInstanceRef":  provider.daemonInstanceRef,
		"owner": runtimeNetworkOwner(
			provider.owner["moduleRef"].(string),
			provider.owner["unitRef"].(string),
			provider.owner["instanceRef"].(string),
			provider.owner["interfaceRef"].(string),
		),
	}
}

func appendRuntimeNetworkMember(network map[string]any, member map[string]any) {
	network["members"] = append(network["members"].([]any), member)
}

func appendInstanceNetworkBinding(instance map[string]any, binding map[string]any) {
	instance["networkBindings"] = append(instance["networkBindings"].([]any), binding)
}

func finalizeRuntimeNetworkOrder(runtimeNetworks []any, modules []any) {
	for _, rawNetwork := range runtimeNetworks {
		network := rawNetwork.(map[string]any)
		members := network["members"].([]any)
		sort.Slice(members, func(i, j int) bool {
			return runtimeNetworkMemberLess(members[i].(map[string]any), members[j].(map[string]any))
		})
	}
	for _, rawModule := range modules {
		module := rawModule.(map[string]any)
		for _, unit := range module["renderUnits"].([]any) {
			for _, instance := range unit.(map[string]any)["instances"].([]any) {
				bindings := instance.(map[string]any)["networkBindings"].([]any)
				sort.Slice(bindings, func(i, j int) bool {
					return runtimeNetworkBindingLess(bindings[i].(map[string]any), bindings[j].(map[string]any))
				})
			}
		}
	}
}

func runtimeNetworkMemberLess(left, right map[string]any) bool {
	if left["role"] != right["role"] {
		return left["role"] == "provider"
	}
	for _, field := range []string{"moduleRef", "unitRef", "instanceRef", "interfaceRef"} {
		if left[field] != right[field] {
			return left[field].(string) < right[field].(string)
		}
	}
	return false
}

func runtimeNetworkBindingLess(left, right map[string]any) bool {
	for _, field := range []string{"networkInstanceRef", "role", "interfaceRef"} {
		if left[field] != right[field] {
			return left[field].(string) < right[field].(string)
		}
	}
	return false
}
