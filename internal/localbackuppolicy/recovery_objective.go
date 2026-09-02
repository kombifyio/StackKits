package localbackuppolicy

import (
	"fmt"
	"slices"
)

// RecoveryObjective is the target-local projection of one CUE DataBinding
// recovery objective. WorkloadRefs are derived from the selected application
// volumes; callers must not treat them as an independent source of authority.
type RecoveryObjective struct {
	BindingRef          string   `json:"bindingRef"`
	WorkloadRefs        []string `json:"workloadRefs"`
	MaxDataLossSeconds  int      `json:"maxDataLossSeconds"`
	RecoveryTimeSeconds int      `json:"recoveryTimeSeconds"`
}

// ValidateRecoveryObjectives checks the canonical target-local encoding used
// by the local Kopia policy artifact. Runtime evidence is deliberately outside
// this type; these values are goals, not proof that recovery is possible.
func ValidateRecoveryObjectives(objectives []RecoveryObjective) error {
	previousBinding := ""
	for index, objective := range objectives {
		if objective.BindingRef == "" || !applicationRefPattern.MatchString(objective.BindingRef) {
			return fmt.Errorf("recovery objective %d has an invalid bindingRef", index)
		}
		if index > 0 && objective.BindingRef <= previousBinding {
			return fmt.Errorf("recovery objectives are not in canonical binding order")
		}
		previousBinding = objective.BindingRef
		if len(objective.WorkloadRefs) == 0 {
			return fmt.Errorf("recovery objective %q has no target-local workload", objective.BindingRef)
		}
		if !slices.IsSorted(objective.WorkloadRefs) {
			return fmt.Errorf("recovery objective %q workloadRefs are not sorted", objective.BindingRef)
		}
		for workloadIndex, workloadRef := range objective.WorkloadRefs {
			if !applicationRefPattern.MatchString(workloadRef) {
				return fmt.Errorf("recovery objective %q has an invalid workloadRef", objective.BindingRef)
			}
			if workloadIndex > 0 && workloadRef == objective.WorkloadRefs[workloadIndex-1] {
				return fmt.Errorf("recovery objective %q repeats workloadRef %q", objective.BindingRef, workloadRef)
			}
		}
		if objective.MaxDataLossSeconds < 0 {
			return fmt.Errorf("recovery objective %q has a negative maxDataLossSeconds", objective.BindingRef)
		}
		if objective.RecoveryTimeSeconds <= 0 {
			return fmt.Errorf("recovery objective %q has a non-positive recoveryTimeSeconds", objective.BindingRef)
		}
	}
	return nil
}

// validateRecoveryObjectives binds every objective to the complete workload set
// represented by this policy's already governed, target-local source volumes.
func (policy Policy) validateRecoveryObjectives() error {
	if err := validateRecoveryObjectiveSources(policy.RecoveryObjectives, policy.Source.ApplicationVolumes); err != nil {
		return err
	}
	if len(policy.RecoveryObjectives) == 0 {
		return nil
	}
	if policy.Schedule == nil {
		return fmt.Errorf("local Kopia recovery objectives require a resolved backup schedule")
	}
	interval, err := policy.Schedule.MaximumTriggerIntervalSeconds()
	if err != nil {
		return err
	}
	for _, objective := range policy.RecoveryObjectives {
		if objective.MaxDataLossSeconds < interval {
			return fmt.Errorf("recovery objective %q is shorter than its backup trigger interval and jitter", objective.BindingRef)
		}
	}
	return nil
}

func validateRecoveryObjectiveSources(objectives []RecoveryObjective, applications []ApplicationVolume) error {
	if err := ValidateRecoveryObjectives(objectives); err != nil {
		return err
	}
	for _, objective := range objectives {
		if !slices.Equal(objective.WorkloadRefs, recoveryWorkloads(applications, objective.BindingRef)) {
			return fmt.Errorf("recovery objective %q does not match its backup source workloads", objective.BindingRef)
		}
	}
	return nil
}

func recoveryWorkloads(applications []ApplicationVolume, bindingRef string) []string {
	var workloads []string
	for _, volume := range applications {
		if volume.DataBindingRef == bindingRef {
			workloads = append(workloads, volume.WorkloadRef)
		}
	}
	slices.Sort(workloads)
	return slices.Compact(workloads)
}
