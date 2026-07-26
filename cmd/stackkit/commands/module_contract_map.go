package commands

import skcue "github.com/kombifyio/stackkits/internal/cue"

func moduleContractToCanonicalMap(contract skcue.ModuleContract) map[string]interface{} {
	result := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":        contract.Metadata.Name,
			"displayName": contract.Metadata.DisplayName,
			"version":     contract.Metadata.Version,
			"layer":       contract.Metadata.Layer,
			"description": contract.Metadata.Description,
			"core":        contract.Metadata.Core,
		},
	}
	if contract.Requires != nil {
		result["requires"] = requiresMap(contract.Requires)
	}
	if contract.Provides != nil {
		result["provides"] = providesMap(contract.Provides)
	}
	if contract.Settings != nil {
		result["settings"] = settingsMap(contract.Settings)
	}
	if len(contract.Provisioners) > 0 {
		result["provisioners"] = provisionersMap(contract.Provisioners)
	}
	return result
}

func requiresMap(requirements *skcue.RequiresSpec) map[string]interface{} {
	services := map[string]interface{}{}
	for name, service := range requirements.Services {
		services[name] = map[string]interface{}{
			"minVersion": service.MinVersion,
			"provides":   service.Provides,
			"optional":   service.Optional,
		}
	}
	return map[string]interface{}{
		"services": services,
		"infrastructure": map[string]interface{}{
			"docker":            requirements.Infrastructure.Docker,
			"network":           requirements.Infrastructure.Network,
			"dockerSocket":      requirements.Infrastructure.DockerSocket,
			"persistentStorage": requirements.Infrastructure.PersistentStorage,
			"minMemory":         requirements.Infrastructure.MinMemory,
			"arch":              requirements.Infrastructure.Arch,
		},
	}
}

func providesMap(provides *skcue.ProvidesSpec) map[string]interface{} {
	capabilities := map[string]interface{}{}
	for name, enabled := range provides.Capabilities {
		capabilities[name] = enabled
	}
	middleware := map[string]interface{}{}
	for name, definition := range provides.Middleware {
		middleware[name] = map[string]interface{}{
			"type":        definition.Type,
			"description": definition.Description,
		}
	}
	endpoints := map[string]interface{}{}
	for name, endpoint := range provides.Endpoints {
		endpoints[name] = map[string]interface{}{
			"url":         endpoint.URL,
			"internal":    endpoint.Internal,
			"description": endpoint.Description,
		}
	}
	return map[string]interface{}{
		"capabilities": capabilities,
		"middleware":   middleware,
		"endpoints":    endpoints,
	}
}

func settingsMap(settings *skcue.SettingsSpec) map[string]interface{} {
	permanent := map[string]interface{}{}
	for name, value := range settings.Perma {
		permanent[name] = value
	}
	flexible := map[string]interface{}{}
	for name, value := range settings.Flexible {
		flexible[name] = value
	}
	return map[string]interface{}{"perma": permanent, "flexible": flexible}
}

func provisionersMap(provisioners map[string]skcue.ProvisionerDef) map[string]interface{} {
	result := map[string]interface{}{}
	for name, provisioner := range provisioners {
		result[name] = map[string]interface{}{
			"image":       provisioner.Image,
			"command":     provisioner.Command,
			"dependsOn":   provisioner.DependsOn,
			"networks":    provisioner.Networks,
			"environment": provisioner.Environment,
		}
	}
	return result
}

func mapField(object map[string]interface{}, key string) map[string]interface{} {
	if value, exists := object[key]; exists {
		if field, ok := value.(map[string]interface{}); ok {
			return field
		}
	}
	return map[string]interface{}{}
}
