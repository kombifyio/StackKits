package resolvedplan

import "fmt"

type moduleSecretInputBinding struct {
	targetRef, capabilityRef, key string
}

func moduleRenderSecretInputBindings(unit map[string]any, unitPath string) (map[string]moduleSecretInputBinding, error) {
	rawBindings, exists, err := optionalObjectField(unit, unitPath, "secretInputBindings")
	if err != nil || !exists {
		return map[string]moduleSecretInputBinding{}, err
	}
	secretRefs, err := stringListField(unit, unitPath, "secretInputRefs", false)
	if err != nil {
		return nil, err
	}
	declared := stringSet(secretRefs)
	bindings := make(map[string]moduleSecretInputBinding, len(rawBindings))
	for targetRef, raw := range rawBindings {
		path := unitPath + ".secretInputBindings." + targetRef
		binding, ok := raw.(map[string]any)
		if !ok {
			return nil, fail(ErrContractConflict, path, "must be an object")
		}
		for field := range binding {
			switch field {
			case "source", "capabilityRef", "key":
			default:
				return nil, fail(ErrContractConflict, path+"."+field, "field is not part of the closed module secret input binding contract")
			}
		}
		source, err := stringField(binding, path, "source")
		if err != nil {
			return nil, err
		}
		if source != "capability-secret" {
			return nil, fail(ErrContractConflict, path+".source", "unsupported secret source %q", source)
		}
		capabilityRef, err := stringField(binding, path, "capabilityRef")
		if err != nil {
			return nil, err
		}
		key, err := stringField(binding, path, "key")
		if err != nil {
			return nil, err
		}
		if _, ok := declared[targetRef]; !ok {
			return nil, fail(ErrContractConflict, path, "target is not a declared secret input")
		}
		bindings[targetRef] = moduleSecretInputBinding{targetRef: targetRef, capabilityRef: capabilityRef, key: key}
	}
	return bindings, nil
}

func resolveModuleCapabilitySecretRefs(moduleID, providerID string, contract map[string]any, resolved *resolution) (map[string]any, error) {
	path := "catalog.modules." + moduleID
	provides, err := stringListField(contract, path, "provides", false)
	if err != nil {
		return nil, err
	}
	provided := stringSet(provides)
	units, err := objectListField(contract, path, "renderUnits")
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	sources := map[string]moduleSecretInputBinding{}
	for index, unit := range units {
		unitPath := fmt.Sprintf("%s.renderUnits[%d]", path, index)
		bindings, err := moduleRenderSecretInputBindings(unit, unitPath)
		if err != nil {
			return nil, err
		}
		for targetRef, binding := range bindings {
			if _, ok := provided[binding.capabilityRef]; !ok {
				return nil, fail(ErrContractConflict, unitPath+".secretInputBindings."+targetRef+".capabilityRef", "capability %q is not provided by module %q", binding.capabilityRef, moduleID)
			}
			if selectedProvider := resolved.providerByCap[binding.capabilityRef]; selectedProvider != providerID {
				return nil, fail(ErrContractConflict, unitPath+".secretInputBindings."+targetRef+".capabilityRef", "capability %q is not selected through governing provider %q", binding.capabilityRef, providerID)
			}
			secretRef, ok := resolved.secretRefsByCap[binding.capabilityRef][binding.key]
			if !ok {
				return nil, fail(ErrUnrealizedModule, unitPath+".secretInputBindings."+targetRef, "capability secret %s/%s is unavailable", binding.capabilityRef, binding.key)
			}
			if previous, duplicate := sources[targetRef]; duplicate && previous != binding {
				return nil, fail(ErrContractConflict, unitPath+".secretInputBindings."+targetRef, "secret input is bound to conflicting capability sources")
			}
			clone, err := cloneObject(map[string]any{"value": secretRef}, false)
			if err != nil {
				return nil, err
			}
			sources[targetRef], result[targetRef] = binding, clone["value"]
		}
	}
	return result, nil
}
