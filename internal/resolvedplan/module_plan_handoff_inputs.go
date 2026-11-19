package resolvedplan

func safeModuleHomeAccessHandoff(moduleID string, module map[string]any, source modulePlanInputSource) (map[string]any, error) {
	requirements, err := safeModuleHomeAccessProjection(moduleID, module, source.homeAccessRequirements, true)
	if err != nil {
		return nil, err
	}
	bindings, err := safeModuleHomeAccessProjection(moduleID, module, source.externalHomeAccessBindings, false)
	if err != nil {
		return nil, err
	}
	return normalizedObject(map[string]any{"requirements": requirements, "bindings": bindings}, "homeAccessHandoff")
}

func safeModuleHomeOffsiteBackup(moduleID string, module map[string]any, source modulePlanInputSource) (map[string]any, error) {
	requirements, err := safeModuleHomeBackupTargetProjection(moduleID, module, source.homeBackupTargetRequirements, true)
	if err != nil {
		return nil, err
	}
	bindings, err := safeModuleHomeBackupTargetProjection(moduleID, module, source.externalHomeBackupTargetBindings, false)
	if err != nil {
		return nil, err
	}
	return normalizedObject(map[string]any{"requirements": requirements, "bindings": bindings}, "homeOffsiteBackup")
}

func safeModuleCloudOffsiteBackup(moduleID string, module map[string]any, source modulePlanInputSource) (map[string]any, error) {
	requirements, err := safeModuleBackupTargetProjection(moduleID, module, source.backupTargetRequirements, true)
	if err != nil {
		return nil, err
	}
	bindings, err := safeModuleBackupTargetProjection(moduleID, module, source.externalBackupTargetBindings, false)
	if err != nil {
		return nil, err
	}
	return normalizedObject(map[string]any{"requirements": requirements, "bindings": bindings}, "cloudOffsiteBackup")
}
