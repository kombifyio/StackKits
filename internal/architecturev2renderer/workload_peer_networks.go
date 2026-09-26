package architecturev2renderer

import "slices"

// governedPeerNetworks lists the only add-on components that may join the
// internal network of another workload, and exactly which one. Joining lets
// an add-on reach its primary application (and that network's other
// services), so it is never a generic right.
var governedPeerNetworks = map[string]struct {
	component string
	networks  []selectedPaaSPeerNetwork
}{
	immichPublicProxyWorkloadModuleID: {component: "immich-public-proxy", networks: []selectedPaaSPeerNetwork{{WorkloadRef: "photos", NetworkRef: "immich-internal"}}},
	immichKioskWorkloadModuleID:       {component: "immich-kiosk", networks: []selectedPaaSPeerNetwork{{WorkloadRef: "photos", NetworkRef: "immich-internal"}}},
	immichPowerToolsWorkloadModuleID:  {component: "immich-power-tools", networks: []selectedPaaSPeerNetwork{{WorkloadRef: "photos", NetworkRef: "immich-internal"}}},
}

// validatePeerNetworks accepts a component's peer networks only when they are
// exactly the governed declaration of its module.
func validatePeerNetworks(moduleRef string, component selectedPaaSRuntimeComponent, path string) error {
	if len(component.PeerNetworks) == 0 {
		return nil
	}
	rights, ok := governedPeerNetworks[moduleRef]
	if !ok || rights.component != component.ID || !slices.Equal(component.PeerNetworks, rights.networks) {
		return fail(ErrInvalidPlan, path+".peerNetworks", "peer networks are admitted only for their governed add-on component")
	}
	return nil
}

func parsePeerNetworks(component selectedPaaSRuntimeComponent, moduleRef, path string) ([]ApplicationDeliveryPeerNetwork, error) {
	if err := validatePeerNetworks(moduleRef, component, path); err != nil {
		return nil, err
	}
	networks := make([]ApplicationDeliveryPeerNetwork, 0, len(component.PeerNetworks))
	for _, network := range component.PeerNetworks {
		networks = append(networks, ApplicationDeliveryPeerNetwork{WorkloadRef: network.WorkloadRef, NetworkRef: network.NetworkRef})
	}
	if len(networks) == 0 {
		return nil, nil
	}
	return networks, nil
}
