package localbackuppolicy

import "fmt"

// RecoveryObjectiveProjectionAPIVersion identifies the optional, derived
// recovery-objective extension on the compiler-owned backup-source input.
const RecoveryObjectiveProjectionAPIVersion = "stackkit.recovery-objective-projection/v1"

// RecoveryObjectiveProjection carries only target-local recovery goals. It
// deliberately sits outside Source so SourceDigest remains bound to the
// physical data selection alone.
type RecoveryObjectiveProjection struct {
	APIVersion string              `json:"apiVersion"`
	Objectives []RecoveryObjective `json:"objectives"`
}

// BackupSourceProjection is the existing flat Source input with an optional
// typed recovery extension. Embedding preserves the fieldless legacy JSON
// representation when the extension is absent.
type BackupSourceProjection struct {
	Source
	RecoveryObjectiveProjection *RecoveryObjectiveProjection `json:"recoveryObjectiveProjection,omitempty"`
}

// RecoveryObjectivesForTarget narrows the logical-unit projection to the
// exact node-local application volumes selected by the runtime renderer.
func RecoveryObjectivesForTarget(projection *RecoveryObjectiveProjection, sourceApplications []ApplicationVolume, siteRef, nodeRef string) ([]RecoveryObjective, error) {
	if projection == nil {
		return nil, nil
	}
	if projection.APIVersion != RecoveryObjectiveProjectionAPIVersion {
		return nil, fmt.Errorf("recovery objective projection uses unsupported apiVersion %q", projection.APIVersion)
	}
	if len(projection.Objectives) == 0 {
		return nil, fmt.Errorf("recovery objective projection has no objectives")
	}
	if err := validateRecoveryObjectiveSources(projection.Objectives, sourceApplications); err != nil {
		return nil, err
	}
	applications, err := ApplicationVolumesForTarget(sourceApplications, siteRef, nodeRef)
	if err != nil {
		return nil, err
	}
	result := make([]RecoveryObjective, 0, len(projection.Objectives))
	for _, objective := range projection.Objectives {
		selected := recoveryWorkloads(applications, objective.BindingRef)
		if len(selected) == 0 {
			continue
		}
		objective.WorkloadRefs = selected
		result = append(result, objective)
	}
	if err := ValidateRecoveryObjectives(result); err != nil {
		return nil, err
	}
	return result, nil
}
