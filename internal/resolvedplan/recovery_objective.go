package resolvedplan

import (
	"fmt"
	"sort"

	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

type recoveryObjectiveInput struct {
	maxDataLossSeconds  int
	recoveryTimeSeconds int
}

// validateRecoveryObjectives enforces the parts of a DataBinding recovery
// objective that can be proven from a resolved plan. Fresh snapshot age and
// restore evidence remain lifecycle concerns and are intentionally absent.
func validateRecoveryObjectives(data, backupPolicy map[string]any, workloads []any) error {
	objectives, err := recoveryObjectiveInputs(data)
	if err != nil {
		return err
	}
	if len(objectives) == 0 {
		return nil
	}
	triggerInterval, err := recoveryObjectiveTriggerInterval(backupPolicy)
	if err != nil {
		return err
	}
	for bindingRef, objective := range objectives {
		objectivePath := "resolvedPlan.data.bindings." + bindingRef + ".recoveryObjective"
		if objective.maxDataLossSeconds < triggerInterval {
			return fail(ErrContractConflict, objectivePath+".maxDataLossSeconds", "cannot meet the objective with the global %s backup cadence and jitter", recoveryCadenceName(backupPolicy))
		}
		matched := false
		for index, rawWorkload := range workloads {
			workloadPath := fmt.Sprintf("resolvedPlan.workloads[%d]", index)
			workload, err := asObject(rawWorkload, workloadPath)
			if err != nil {
				return err
			}
			alternative, err := objectField(workload, workloadPath, "alternative")
			if err != nil {
				return err
			}
			infrastructure, exists, err := optionalObjectField(alternative, workloadPath+".alternative", "infrastructure")
			if err != nil {
				return err
			}
			if !exists {
				continue
			}
			binding, err := objectField(infrastructure, workloadPath+".alternative.infrastructure", "dataBinding")
			if err != nil {
				return err
			}
			workloadBindingRef, err := stringField(binding, workloadPath+".alternative.infrastructure.dataBinding", "bindingRef")
			if err != nil {
				return err
			}
			if workloadBindingRef != bindingRef {
				continue
			}
			matched = true
			if err := validateRecoveryObjectiveWorkload(infrastructure, alternative, workloadPath, bindingRef); err != nil {
				return err
			}
		}
		if !matched {
			return fail(ErrContractConflict, objectivePath, "is not bound to a selected workload")
		}
	}
	return nil
}

// ProjectRecoveryObjectives projects only objectives represented by the
// target-local application volumes. The complete binding/source coverage is
// checked by validateRecoveryObjectives before rendering the policy.
func ProjectRecoveryObjectives(data map[string]any, applications []localbackuppolicy.ApplicationVolume) ([]localbackuppolicy.RecoveryObjective, error) {
	inputs, err := recoveryObjectiveInputs(data)
	if err != nil {
		return nil, err
	}
	workloadsByBinding := make(map[string]map[string]struct{})
	for _, application := range applications {
		if _, exists := inputs[application.DataBindingRef]; !exists {
			continue
		}
		refs := workloadsByBinding[application.DataBindingRef]
		if refs == nil {
			refs = make(map[string]struct{})
			workloadsByBinding[application.DataBindingRef] = refs
		}
		refs[application.WorkloadRef] = struct{}{}
	}
	result := make([]localbackuppolicy.RecoveryObjective, 0, len(workloadsByBinding))
	for bindingRef, workloadSet := range workloadsByBinding {
		workloadRefs := make([]string, 0, len(workloadSet))
		for workloadRef := range workloadSet {
			workloadRefs = append(workloadRefs, workloadRef)
		}
		sort.Strings(workloadRefs)
		objective := inputs[bindingRef]
		result = append(result, localbackuppolicy.RecoveryObjective{
			BindingRef:          bindingRef,
			WorkloadRefs:        workloadRefs,
			MaxDataLossSeconds:  objective.maxDataLossSeconds,
			RecoveryTimeSeconds: objective.recoveryTimeSeconds,
		})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].BindingRef < result[right].BindingRef })
	if err := localbackuppolicy.ValidateRecoveryObjectives(result); err != nil {
		return nil, err
	}
	return result, nil
}

func recoveryObjectiveInputs(data map[string]any) (map[string]recoveryObjectiveInput, error) {
	bindings, exists, err := optionalObjectField(data, "resolvedPlan.data", "bindings")
	if err != nil {
		return nil, err
	}
	if !exists {
		return map[string]recoveryObjectiveInput{}, nil
	}
	result := make(map[string]recoveryObjectiveInput)
	for _, bindingRef := range sortedStringMapKeys(bindings) {
		path := "resolvedPlan.data.bindings." + bindingRef
		binding, err := asObject(bindings[bindingRef], path)
		if err != nil {
			return nil, err
		}
		objective, exists, err := optionalObjectField(binding, path, "recoveryObjective")
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		maxDataLossSeconds, err := intField(objective, path+".recoveryObjective", "maxDataLossSeconds")
		if err != nil {
			return nil, err
		}
		recoveryTimeSeconds, err := intField(objective, path+".recoveryObjective", "recoveryTimeSeconds")
		if err != nil {
			return nil, err
		}
		if maxDataLossSeconds < 0 || recoveryTimeSeconds <= 0 {
			return nil, fail(ErrContractConflict, path+".recoveryObjective", "recovery objective seconds are outside the governed ranges")
		}
		result[bindingRef] = recoveryObjectiveInput{
			maxDataLossSeconds:  maxDataLossSeconds,
			recoveryTimeSeconds: recoveryTimeSeconds,
		}
	}
	return result, nil
}

func recoveryObjectiveTriggerInterval(backupPolicy map[string]any) (int, error) {
	schedule, err := objectField(backupPolicy, "resolvedPlan.backupPolicy", "schedule")
	if err != nil {
		return 0, err
	}
	cadence, err := stringField(schedule, "resolvedPlan.backupPolicy.schedule", "cadence")
	if err != nil {
		return 0, err
	}
	minuteUTC, err := intField(schedule, "resolvedPlan.backupPolicy.schedule", "minuteUTC")
	if err != nil {
		return 0, err
	}
	jitterSeconds, err := intField(schedule, "resolvedPlan.backupPolicy.schedule", "jitterSeconds")
	if err != nil {
		return 0, err
	}
	var hourUTC *int
	if _, exists := schedule["hourUTC"]; exists {
		value, err := intField(schedule, "resolvedPlan.backupPolicy.schedule", "hourUTC")
		if err != nil {
			return 0, err
		}
		hourUTC = &value
	}
	weekdayUTC, _, err := optionalStringField(schedule, "resolvedPlan.backupPolicy.schedule", "weekdayUTC")
	if err != nil {
		return 0, err
	}
	resolved := localbackuppolicy.Schedule{
		Cadence: cadence, MinuteUTC: minuteUTC, HourUTC: hourUTC,
		WeekdayUTC: weekdayUTC, JitterSeconds: jitterSeconds,
	}
	interval, err := resolved.MaximumTriggerIntervalSeconds()
	if err != nil {
		return 0, fail(ErrContractConflict, "resolvedPlan.backupPolicy.schedule", "%v", err)
	}
	return interval, nil
}

func recoveryCadenceName(backupPolicy map[string]any) string {
	schedule, ok := backupPolicy["schedule"].(map[string]any)
	if !ok {
		return "resolved"
	}
	if cadence, ok := schedule["cadence"].(string); ok {
		return cadence
	}
	return "resolved"
}

func validateRecoveryObjectiveWorkload(infrastructure, alternative map[string]any, workloadPath, bindingRef string) error {
	runtime, exists, err := optionalObjectField(alternative, workloadPath+".alternative", "runtime")
	if err != nil {
		return err
	}
	if !exists {
		return fail(ErrContractConflict, workloadPath+".alternative.runtime", "recovery objective requires a Standalone-Compose runtime")
	}
	adapter, exists, err := optionalObjectField(runtime, workloadPath+".alternative.runtime", "adapter")
	if err != nil {
		return err
	}
	if !exists {
		return fail(ErrContractConflict, workloadPath+".alternative.runtime.adapter", "recovery objective requires a Standalone-Compose runtime")
	}
	adapterID, err := stringField(adapter, workloadPath+".alternative.runtime.adapter", "id")
	if err != nil {
		return err
	}
	if adapterID != "standalone-compose" {
		return fail(ErrContractConflict, workloadPath+".alternative.runtime.adapter.id", "recovery objective has no governed local backup source for adapter %q", adapterID)
	}
	storage, err := objectField(infrastructure, workloadPath+".alternative.infrastructure", "storageAllocation")
	if err != nil {
		return err
	}
	allocations, err := objectListField(storage, workloadPath+".alternative.infrastructure.storageAllocation", "allocations")
	if err != nil {
		return err
	}
	selected, err := localKopiaWorkloadBackupAllocations(infrastructure, workloadPath+".alternative.infrastructure")
	if err != nil {
		return err
	}
	selectedKeys := make(map[string]struct{}, len(selected))
	for _, allocation := range selected {
		selectedKeys[allocation.componentRef+"/"+allocation.volumeRef] = struct{}{}
	}
	persistentCount := 0
	for index, rawAllocation := range allocations {
		path := fmt.Sprintf("%s.alternative.infrastructure.storageAllocation.allocations[%d]", workloadPath, index)
		allocation, err := asObject(rawAllocation, path)
		if err != nil {
			return err
		}
		class, err := stringField(allocation, path, "class")
		if err != nil {
			return err
		}
		if class != "persistent" {
			continue
		}
		persistentCount++
		allocationBindingRef, err := stringField(allocation, path, "dataBindingRef")
		if err != nil {
			return err
		}
		if allocationBindingRef != bindingRef {
			return fail(ErrContractConflict, path+".dataBindingRef", "persistent volume is outside recovery objective binding %q", bindingRef)
		}
		backup, err := boolFieldDefault(allocation, path, "backup", false)
		if err != nil {
			return err
		}
		if !backup {
			return fail(ErrContractConflict, path+".backup", "recovery objective requires every persistent volume in binding %q to be backup-enabled", bindingRef)
		}
		componentRef, err := stringField(allocation, path, "componentRef")
		if err != nil {
			return err
		}
		volumeRef, err := stringField(allocation, path, "volumeRef")
		if err != nil {
			return err
		}
		if _, covered := selectedKeys[componentRef+"/"+volumeRef]; !covered {
			return fail(ErrContractConflict, path, "persistent volume is missing from the governed backup source")
		}
	}
	if persistentCount == 0 || len(selected) == 0 {
		return fail(ErrContractConflict, workloadPath+".alternative.infrastructure", "recovery objective binding %q has no persistent local backup coverage", bindingRef)
	}
	if len(selected) != persistentCount {
		return fail(ErrContractConflict, workloadPath+".alternative.infrastructure", "recovery objective binding %q does not have complete persistent local backup coverage", bindingRef)
	}
	return nil
}
