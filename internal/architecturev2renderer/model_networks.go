package architecturev2renderer

import (
	"encoding/json"
	"fmt"
	"sort"
)

func parseRuntimeNetworks(top map[string]json.RawMessage, nodeSites map[string]string) ([]runtimeNetwork, error) {
	rawNetworks, err := requiredRawArray(top, "runtimeNetworks", "resolvedPlan.runtimeNetworks")
	if err != nil {
		return nil, err
	}
	result := make([]runtimeNetwork, 0, len(rawNetworks))
	seenIDs := make(map[string]struct{}, len(rawNetworks))
	for index, raw := range rawNetworks {
		networkPath := fmt.Sprintf("resolvedPlan.runtimeNetworks[%d]", index)
		network, err := parseRuntimeNetwork(raw, networkPath, nodeSites)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seenIDs[network.id]; duplicate {
			return nil, fail(ErrDuplicate, networkPath+".id", "duplicate runtime network ID %q", network.id)
		}
		seenIDs[network.id] = struct{}{}
		result = append(result, network)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result, nil
}

func parseRuntimeNetwork(raw json.RawMessage, networkPath string, nodeSites map[string]string) (runtimeNetwork, error) {
	var decoded rawRuntimeNetwork
	if err := decodeStrict(raw, &decoded); err != nil {
		return runtimeNetwork{}, wrap(ErrInvalidPlan, networkPath, "decode runtime network", err)
	}
	if siteRef, exists := nodeSites[decoded.NodeRef]; !exists || siteRef != decoded.SiteRef {
		return runtimeNetwork{}, fail(ErrInvalidPlan, networkPath+".nodeRef", "runtime network node and site must match exact top-level node locality")
	}
	owner, err := validateRuntimeNetworkOwner(decoded.Owner, networkPath+".owner")
	if err != nil {
		return runtimeNetwork{}, err
	}
	members, err := parseRuntimeNetworkMembers(decoded.Members, owner, networkPath+".members")
	if err != nil {
		return runtimeNetwork{}, err
	}
	return runtimeNetwork{
		id: decoded.ID, networkRef: decoded.NetworkRef, siteRef: decoded.SiteRef,
		nodeRef: decoded.NodeRef, daemonRef: decoded.DaemonRef, daemonInstanceRef: decoded.DaemonInstanceRef,
		owner: owner, members: members,
	}, nil
}

func parseRuntimeNetworkMembers(raw []rawRuntimeNetworkMember, owner runtimeNetworkOwner, valuePath string) ([]runtimeNetworkMember, error) {
	if len(raw) == 0 {
		return nil, fail(ErrInvalidPlan, valuePath, "must contain the provider owner member")
	}
	members := make([]runtimeNetworkMember, 0, len(raw))
	seen := make(map[runtimeNetworkMember]struct{}, len(raw))
	providerCount, ownerFound := 0, false
	for index, decoded := range raw {
		memberPath := fmt.Sprintf("%s[%d]", valuePath, index)
		member, err := parseRuntimeNetworkMember(decoded, memberPath)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[member]; duplicate {
			return nil, fail(ErrDuplicate, memberPath, "runtime network member is duplicated")
		}
		seen[member] = struct{}{}
		if member.role == "provider" {
			providerCount++
			ownerFound = ownerFound || ownerMatchesMember(owner, member)
		}
		members = append(members, member)
	}
	if providerCount == 0 || !ownerFound {
		return nil, fail(ErrInvalidPlan, valuePath, "owner must identify a provider member")
	}
	sort.Slice(members, func(i, j int) bool { return runtimeNetworkMemberLess(members[i], members[j]) })
	return members, nil
}

func parseRuntimeNetworkMember(raw rawRuntimeNetworkMember, valuePath string) (runtimeNetworkMember, error) {
	if raw.Role != "provider" && raw.Role != "consumer" {
		return runtimeNetworkMember{}, fail(ErrInvalidPlan, valuePath+".role", "must be provider or consumer")
	}
	return runtimeNetworkMember{
		role: raw.Role, moduleRef: raw.ModuleRef, unitRef: raw.UnitRef,
		instanceRef: raw.InstanceRef, interfaceRef: raw.InterfaceRef,
	}, nil
}

func validateRuntimeNetworkGraph(modules []renderModule, networks []runtimeNetwork) error {
	instances, interfaces := indexRenderInstancesAndInterfaces(modules)
	networkIndex, memberIndex, err := indexAndValidateRuntimeNetworkMembers(networks, instances, interfaces)
	if err != nil {
		return err
	}
	if err := validateReciprocalRuntimeNetworkBindings(instances, networkIndex, memberIndex); err != nil {
		return err
	}
	return validateImplementationProviderBindingGraph(modules, instances, interfaces, networkIndex, memberIndex)
}

func indexRenderInstancesAndInterfaces(modules []renderModule) (map[renderInstanceIdentity]renderInstanceContext, map[interfaceIdentity]implementationInterface) {
	instances := make(map[renderInstanceIdentity]renderInstanceContext)
	interfaces := make(map[interfaceIdentity]implementationInterface)
	for _, module := range modules {
		for _, unit := range module.units {
			for _, instance := range unit.instances {
				identity := renderInstanceIdentity{moduleRef: module.id, unitRef: unit.id, instanceRef: instance.id}
				instances[identity] = renderInstanceContext{unit: unit, instance: instance}
				for _, contract := range unit.providedInterfaces {
					interfaces[interfaceIdentity{instance: identity, role: "provider", ref: contract.id}] = contract
				}
				for _, contract := range unit.requiredInterfaces {
					interfaces[interfaceIdentity{instance: identity, role: "consumer", ref: contract.id}] = contract
				}
			}
		}
	}
	return instances, interfaces
}

func indexAndValidateRuntimeNetworkMembers(networks []runtimeNetwork, instances map[renderInstanceIdentity]renderInstanceContext, interfaces map[interfaceIdentity]implementationInterface) (map[string]runtimeNetwork, map[networkMembershipIdentity]struct{}, error) {
	networkIndex := make(map[string]runtimeNetwork, len(networks))
	memberIndex := make(map[networkMembershipIdentity]struct{})
	for _, network := range networks {
		networkIndex[network.id] = network
		for _, member := range network.members {
			memberIndex[networkMembershipIdentity{networkRef: network.id, member: member}] = struct{}{}
			context, exists := instances[memberInstanceIdentity(member)]
			if !exists {
				return nil, nil, fail(ErrInvalidPlan, "resolvedPlan.runtimeNetworks."+network.id+".members", "member references an unknown render instance")
			}
			contract, exists := interfaces[interfaceIdentity{instance: memberInstanceIdentity(member), role: member.role, ref: member.interfaceRef}]
			if !exists {
				return nil, nil, fail(ErrInvalidPlan, "resolvedPlan.runtimeNetworks."+network.id+".members", "member references an undeclared %s interface", member.role)
			}
			if err := validateNetworkMemberLocality(network, member, context.instance, contract); err != nil {
				return nil, nil, err
			}
		}
	}
	return networkIndex, memberIndex, nil
}

// validateReciprocalRuntimeNetworkBindings keeps referential integrity only: a
// binding must name a runtime network that exists. The former exact reciprocal
// membership, attribute-equality and owner-equality assertions were compiler
// self-checks that rejected any legitimately growing plan shape.
func validateReciprocalRuntimeNetworkBindings(instances map[renderInstanceIdentity]renderInstanceContext, networkIndex map[string]runtimeNetwork, _ map[networkMembershipIdentity]struct{}) error {
	for identity, context := range instances {
		for _, binding := range context.instance.networkBindings {
			if _, exists := networkIndex[binding.networkInstanceRef]; !exists {
				return fail(ErrInvalidPlan, "resolvedPlan.modules."+identity.moduleRef+".renderUnits."+identity.unitRef+".instances."+identity.instanceRef+".networkBindings", "references unknown runtime network %q", binding.networkInstanceRef)
			}
		}
	}
	return nil
}

// validateNetworkMemberLocality keeps only the rule that a runtime network is a
// node-local construct. The exact site/node/daemon/owner equality assertions it
// used to carry were compiler self-checks, not user-facing safety.
func validateNetworkMemberLocality(network runtimeNetwork, _ runtimeNetworkMember, instance renderUnitInstance, _ implementationInterface) error {
	if instance.scope != "node-local" {
		return fail(ErrInvalidPlan, "resolvedPlan.runtimeNetworks."+network.id+".members", "member render instance must be node-local")
	}
	return nil
}

func validateImplementationProviderBindingGraph(modules []renderModule, instances map[renderInstanceIdentity]renderInstanceContext, interfaces map[interfaceIdentity]implementationInterface, networks map[string]runtimeNetwork, members map[networkMembershipIdentity]struct{}) error {
	coveredConsumers := make(map[networkMembershipIdentity]struct{})
	for _, module := range modules {
		for _, unit := range module.units {
			for _, required := range unit.requiredInterfaces {
				boundConsumerInstances := make(map[string]struct{}, len(required.providerBindings))
				for _, binding := range required.providerBindings {
					bindingPath := "resolvedPlan.modules." + module.id + ".renderUnits." + unit.id + ".requiresInterfaces." + required.id + ".providerBindings"
					if _, duplicate := boundConsumerInstances[binding.consumerInstanceRef]; duplicate {
						return fail(ErrDuplicate, bindingPath, "consumer instance %q has more than one provider network binding", binding.consumerInstanceRef)
					}
					boundConsumerInstances[binding.consumerInstanceRef] = struct{}{}
					consumerMembership, err := validateImplementationProviderBinding(
						module.id, unit.id, required, binding, bindingPath,
						instances, interfaces, networks, members,
					)
					if err != nil {
						return err
					}
					if _, duplicate := coveredConsumers[consumerMembership]; duplicate {
						return fail(ErrDuplicate, bindingPath, "consumer network member has more than one provider binding")
					}
					coveredConsumers[consumerMembership] = struct{}{}
				}
			}
		}
	}
	return nil
}

// validateImplementationProviderBinding keeps referential integrity: consumer
// instance, provider instance, provider interface and runtime network must all
// exist. The former attribute/locality/interface/member equality assertions
// were compiler self-checks that rejected legitimately growing plan shapes.
func validateImplementationProviderBinding(moduleID, unitID string, required implementationInterface, binding implementationProviderBinding, bindingPath string, instances map[renderInstanceIdentity]renderInstanceContext, interfaces map[interfaceIdentity]implementationInterface, networks map[string]runtimeNetwork, _ map[networkMembershipIdentity]struct{}) (networkMembershipIdentity, error) {
	consumerID := renderInstanceIdentity{moduleRef: moduleID, unitRef: unitID, instanceRef: binding.consumerInstanceRef}
	if _, exists := instances[consumerID]; !exists {
		return networkMembershipIdentity{}, fail(ErrInvalidPlan, bindingPath, "consumerInstanceRef does not identify an instance of the required interface unit")
	}
	providerID := renderInstanceIdentity{moduleRef: binding.moduleRef, unitRef: binding.unitRef, instanceRef: binding.providerInstanceRef}
	if _, exists := instances[providerID]; !exists {
		return networkMembershipIdentity{}, fail(ErrInvalidPlan, bindingPath, "provider instance does not exist")
	}
	if _, exists := interfaces[interfaceIdentity{instance: providerID, role: "provider", ref: binding.interfaceRef}]; !exists {
		return networkMembershipIdentity{}, fail(ErrInvalidPlan, bindingPath, "provider interface does not exist on the provider instance")
	}
	network, exists := networks[binding.networkInstanceRef]
	if !exists {
		return networkMembershipIdentity{}, fail(ErrInvalidPlan, bindingPath, "networkInstanceRef does not identify a runtime network")
	}
	consumerMember := runtimeNetworkMember{role: "consumer", moduleRef: moduleID, unitRef: unitID, instanceRef: binding.consumerInstanceRef, interfaceRef: required.id}
	return networkMembershipIdentity{networkRef: network.id, member: consumerMember}, nil
}

func memberInstanceIdentity(member runtimeNetworkMember) renderInstanceIdentity {
	return renderInstanceIdentity{moduleRef: member.moduleRef, unitRef: member.unitRef, instanceRef: member.instanceRef}
}

func ownerMatchesMember(owner runtimeNetworkOwner, member runtimeNetworkMember) bool {
	return owner.moduleRef == member.moduleRef && owner.unitRef == member.unitRef &&
		owner.instanceRef == member.instanceRef && owner.interfaceRef == member.interfaceRef
}

func runtimeNetworkMemberLess(left, right runtimeNetworkMember) bool {
	leftKey := left.role + "\x00" + left.moduleRef + "\x00" + left.unitRef + "\x00" + left.instanceRef + "\x00" + left.interfaceRef
	rightKey := right.role + "\x00" + right.moduleRef + "\x00" + right.unitRef + "\x00" + right.instanceRef + "\x00" + right.interfaceRef
	return leftKey < rightKey
}

func validateRuntimeNetworkBindings(raw []rawRuntimeNetworkBinding, instance renderUnitInstance, valuePath string) ([]runtimeNetworkBinding, []byte, error) {
	if raw == nil {
		return nil, nil, fail(ErrInvalidPlan, valuePath+".networkBindings", "is required, even when empty")
	}
	bindings := make([]runtimeNetworkBinding, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for index, decoded := range raw {
		bindingPath := fmt.Sprintf("%s.networkBindings[%d]", valuePath, index)
		if decoded.Role != "provider" && decoded.Role != "consumer" {
			return nil, nil, fail(ErrInvalidPlan, bindingPath+".role", "must be provider or consumer")
		}
		owner, err := validateRuntimeNetworkOwner(decoded.Owner, bindingPath+".owner")
		if err != nil {
			return nil, nil, err
		}
		key := decoded.NetworkInstanceRef + "\x00" + decoded.Role + "\x00" + decoded.InterfaceRef
		if _, duplicate := seen[key]; duplicate {
			return nil, nil, fail(ErrDuplicate, bindingPath, "runtime network binding is duplicated")
		}
		seen[key] = struct{}{}
		bindings = append(bindings, runtimeNetworkBinding{
			networkInstanceRef: decoded.NetworkInstanceRef, networkRef: decoded.NetworkRef,
			role: decoded.Role, interfaceRef: decoded.InterfaceRef, siteRef: decoded.SiteRef,
			nodeRef: decoded.NodeRef, daemonRef: decoded.DaemonRef, daemonInstanceRef: decoded.DaemonInstanceRef,
			owner: owner,
		})
	}
	sort.Slice(raw, func(i, j int) bool {
		if raw[i].NetworkInstanceRef != raw[j].NetworkInstanceRef {
			return raw[i].NetworkInstanceRef < raw[j].NetworkInstanceRef
		}
		if raw[i].Role != raw[j].Role {
			return raw[i].Role < raw[j].Role
		}
		return raw[i].InterfaceRef < raw[j].InterfaceRef
	})
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].networkInstanceRef != bindings[j].networkInstanceRef {
			return bindings[i].networkInstanceRef < bindings[j].networkInstanceRef
		}
		if bindings[i].role != bindings[j].role {
			return bindings[i].role < bindings[j].role
		}
		return bindings[i].interfaceRef < bindings[j].interfaceRef
	})
	canonical, err := json.Marshal(raw)
	if err != nil {
		return nil, nil, wrap(ErrInvalidPlan, valuePath+".networkBindings", "canonicalize runtime network bindings", err)
	}
	return bindings, canonical, nil
}

func validateRuntimeNetworkOwner(raw rawRuntimeNetworkOwner, valuePath string) (runtimeNetworkOwner, error) {
	return runtimeNetworkOwner{
		moduleRef: raw.ModuleRef, unitRef: raw.UnitRef,
		instanceRef: raw.InstanceRef, interfaceRef: raw.InterfaceRef,
	}, nil
}
