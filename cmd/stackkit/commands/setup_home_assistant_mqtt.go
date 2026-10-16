package commands

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

type homeAssistantMQTTSetup struct {
	Request              appsetup.HomeAssistantMQTTRequest
	BrokerArtifactDigest string
}

// homeAssistantAppliedMQTT narrows the verified Plan/Apply to an explicitly
// linked broker. An independent selection grants no broker or custody access.
func homeAssistantAppliedMQTT(authority nativeV2AppliedAuthority, home nativehost.SelectedPaaSWorkloadDeployment) (*homeAssistantMQTTSetup, error) {
	var intent struct {
		Workloads []struct {
			ID       string                     `json:"id"`
			Settings map[string]json.RawMessage `json:"settings"`
		} `json:"workloads"`
	}
	if err := json.Unmarshal(authority.Plan.Canonical(), &intent); err != nil {
		return nil, errors.New("Home Assistant MQTT link intent could not be read from the verified Plan")
	}
	linked := false
	for _, workload := range intent.Workloads {
		if workload.ID != "smart-home-mqtt" {
			continue
		}
		raw, declared := workload.Settings["home-assistant-link"]
		if !declared {
			return nil, nil
		}
		var enabled *bool
		if err := json.Unmarshal(raw, &enabled); err != nil || enabled == nil {
			return nil, errors.New("Home Assistant MQTT link intent requires an explicit boolean")
		}
		linked = *enabled
		break
	}
	if !linked {
		return nil, nil
	}
	broker, err := nativeAppliedWorkloadDeployment(authority, "smart-home-mqtt")
	if err != nil {
		return nil, fmt.Errorf("Home Assistant MQTT setup requires its applied broker: %w", err)
	}
	if broker.SiteRef != home.SiteRef || broker.NodeRef != home.NodeRef || broker.RuntimeAdapter.ID != home.RuntimeAdapter.ID {
		return nil, errors.New("Home Assistant MQTT setup requires the selected broker on the same node and runtime adapter")
	}
	if _, err := architecturev2renderer.ParseMosquittoWorkloadBundle(broker.Bundle); err != nil {
		return nil, err
	}
	bundle, err := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(broker.Bundle)
	if err != nil {
		return nil, err
	}
	connected := false
	for _, component := range bundle.Components {
		if component.ID != "mosquitto" {
			continue
		}
		for _, peer := range component.PeerNetworks {
			if peer.WorkloadRef == "smart-home" && peer.NetworkRef == "home-assistant-internal" {
				connected = true
			}
		}
	}
	if !connected {
		return nil, errors.New("the applied MQTT broker does not carry the Home Assistant network required by its link intent; regenerate and apply before setup")
	}
	material, err := localevidence.ResolveLocalSecretMaterial(authority.WorkspaceRoot, bundle.SecretRefs["mqtt-password"])
	if err != nil {
		return nil, err
	}
	defer clear(material)
	return &homeAssistantMQTTSetup{Request: appsetup.HomeAssistantMQTTRequest{Broker: "mosquitto", Port: 1883, Username: "stackkit", Password: string(material)}, BrokerArtifactDigest: broker.ArtifactDigest}, nil
}
