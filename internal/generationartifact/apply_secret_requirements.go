package generationartifact

import (
	"fmt"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

func appendApplyModuleSecretSources(modules map[string]applyModule, workloadByModule map[string]string, result *ApplyRequirements) error {
	for _, moduleID := range sortedApplyMapKeys(modules) {
		if workloadByModule[moduleID] != "" {
			continue
		}
		sources := map[string]string{}
		for _, unit := range modules[moduleID].units {
			for inputRef, secretRef := range unit.secretRefs {
				if _, capabilityBound := unit.secretBindings[inputRef]; capabilityBound {
					continue
				}
				if previous, duplicate := sources[inputRef]; duplicate && previous != secretRef {
					return fail(ErrInvalidPlan, "resolvedPlan.modules."+moduleID+".renderUnits", "module secret input %q resolves inconsistently across render units", inputRef)
				}
				sources[inputRef] = secretRef
			}
		}
		for _, inputRef := range sortedApplyMapKeys(sources) {
			result.Secrets = append(result.Secrets, ApplySecretRequirement{
				ID: "source/module/" + moduleID + "/" + inputRef, SourceKind: "module", SourceRef: moduleID, SourceInputRef: inputRef,
				OwnerKind: "module-intent", OwnerRef: moduleID, InputRef: inputRef, SecretRef: sources[inputRef],
			})
		}
	}
	return nil
}

func appendApplyIntentSecretRequirements(plan resolvedplan.ResolvedPlan, result *ApplyRequirements) error {
	for _, collection := range []struct {
		field string
		kind  string
	}{
		{field: "capabilities", kind: "capability"},
		{field: "workloads", kind: "workload"},
	} {
		rawValues, ok := plan[collection.field].([]any)
		if !ok {
			return fail(ErrInvalidPlan, "resolvedPlan."+collection.field, "must be an array")
		}
		for index, raw := range rawValues {
			path := fmt.Sprintf("resolvedPlan.%s[%d]", collection.field, index)
			value, ok := raw.(map[string]any)
			if !ok {
				return fail(ErrInvalidPlan, path, "must be an object")
			}
			id, err := requiredString(value, "id", path+".id")
			if err != nil {
				return err
			}
			if err := appendApplySecretSource(value, path, collection.kind, id, result); err != nil {
				return err
			}
		}
	}
	rawAddons, exists := plan["addons"]
	if !exists {
		return nil
	}
	addons, ok := rawAddons.(map[string]any)
	if !ok {
		return fail(ErrInvalidPlan, "resolvedPlan.addons", "must be an object")
	}
	for addonID, raw := range addons {
		path := "resolvedPlan.addons." + addonID
		addon, ok := raw.(map[string]any)
		if !ok {
			return fail(ErrInvalidPlan, path, "must be an object")
		}
		if err := appendApplySecretSource(addon, path, "addon", addonID, result); err != nil {
			return err
		}
	}
	return nil
}

func appendApplySecretSource(owner map[string]any, path, sourceKind, sourceRef string, result *ApplyRequirements) error {
	raw, exists := owner["secretRefs"]
	if !exists {
		return nil
	}
	secretRefs, err := applyStringMap(map[string]any{"secretRefs": raw}, "secretRefs", path+".secretRefs")
	if err != nil {
		return err
	}
	for inputRef, secretRef := range secretRefs {
		result.Secrets = append(result.Secrets, ApplySecretRequirement{
			ID: "source/" + sourceKind + "/" + sourceRef + "/" + inputRef, SourceKind: sourceKind, SourceRef: sourceRef, SourceInputRef: inputRef,
			OwnerKind: sourceKind + "-intent", OwnerRef: sourceRef, InputRef: inputRef, SecretRef: secretRef,
		})
	}
	return nil
}

func validateApplySecretSourceClosure(requirements []ApplySecretRequirement) error {
	for _, source := range requirements {
		if source.OwnerKind != "capability-intent" && source.OwnerKind != "workload-intent" && source.OwnerKind != "module-intent" && source.OwnerKind != "addon-intent" {
			continue
		}
		matched := false
		for _, consumer := range requirements {
			if consumer.OwnerKind == source.OwnerKind || consumer.SourceKind != source.SourceKind || consumer.SourceRef != source.SourceRef || consumer.SourceInputRef != source.SourceInputRef || consumer.SecretRef != source.SecretRef {
				continue
			}
			matched = true
			break
		}
		if !matched {
			return fail(ErrInvalidPlan, "resolvedPlan."+source.SourceKind+"s."+source.SourceRef+".secretRefs."+source.SourceInputRef, "secret intent has no exact Apply consumer")
		}
	}
	return nil
}
