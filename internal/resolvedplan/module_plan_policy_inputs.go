package resolvedplan

import (
	"fmt"

	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

func safeModuleBackupPolicy(policy map[string]any) (map[string]any, error) {
	if policy == nil {
		return nil, fmt.Errorf("backup policy projection is unavailable")
	}
	projection, err := selectObjectFields(policy, "backupPolicy", []string{
		"apiVersion", "kind", "schedule", "dataClasses", "retention", "restoreVerification",
	})
	if err != nil {
		return nil, err
	}
	schedule, err := asObject(projection["schedule"], "backupPolicy.schedule")
	if err != nil {
		return nil, err
	}
	safeSchedule, err := selectObjectFields(schedule, "backupPolicy.schedule", []string{"cadence", "minuteUTC", "jitterSeconds"})
	if err != nil {
		return nil, err
	}
	for _, field := range []string{"hourUTC", "weekdayUTC"} {
		if value, exists := schedule[field]; exists {
			safeSchedule[field] = value
		}
	}
	retention, err := asObject(projection["retention"], "backupPolicy.retention")
	if err != nil {
		return nil, err
	}
	safeRetention, err := selectObjectFields(retention, "backupPolicy.retention", []string{
		"keepDaily", "keepWeekly", "keepMonthly", "keepYearly",
	})
	if err != nil {
		return nil, err
	}
	restoreVerification, err := asObject(projection["restoreVerification"], "backupPolicy.restoreVerification")
	if err != nil {
		return nil, err
	}
	safeRestoreVerification, err := selectObjectFields(restoreVerification, "backupPolicy.restoreVerification", []string{
		"required", "cadence", "maxEvidenceAgeDays", "evidenceRef",
	})
	if err != nil {
		return nil, err
	}
	if value, exists := policy["retentionByDataClass"]; exists {
		classes, err := asObject(value, "backupPolicy.retentionByDataClass")
		if err != nil {
			return nil, err
		}
		retentions := make(map[string]any, len(classes))
		for class, raw := range classes {
			if !localbackuppolicy.ValidBackupDataClass(class) {
				return nil, fmt.Errorf("backupPolicy retention class %q is not semantic", class)
			}
			retention, err := asObject(raw, "backupPolicy.retentionByDataClass."+class)
			if err != nil {
				return nil, err
			}
			selected, err := selectObjectFields(retention, "backupPolicy.retentionByDataClass."+class, []string{"keepDaily", "keepWeekly", "keepMonthly", "keepYearly"})
			if err != nil {
				return nil, err
			}
			retentions[class] = selected
		}
		projection["retentionByDataClass"] = retentions
	}
	if services, exists := policy["marketplaceServices"]; exists {
		projection["marketplaceServices"] = services
	}
	projection["schedule"] = safeSchedule
	projection["retention"] = safeRetention
	projection["restoreVerification"] = safeRestoreVerification
	return normalizedObject(projection, "backupPolicy")
}

func safeModuleDriftPolicy(policy map[string]any) (map[string]any, error) {
	if policy == nil {
		return nil, fmt.Errorf("drift policy projection is unavailable")
	}
	projection, err := selectObjectFields(policy, "driftPolicy", []string{
		"apiVersion", "kind", "enabled", "schedule", "subjects", "evidence", "response",
	})
	if err != nil {
		return nil, err
	}
	schedule, err := asObject(projection["schedule"], "driftPolicy.schedule")
	if err != nil {
		return nil, err
	}
	safeSchedule, err := selectObjectFields(schedule, "driftPolicy.schedule", []string{"cadence", "minuteUTC", "jitterSeconds"})
	if err != nil {
		return nil, err
	}
	for _, field := range []string{"hourUTC", "weekdayUTC"} {
		if value, exists := schedule[field]; exists {
			safeSchedule[field] = value
		}
	}
	evidence, err := asObject(projection["evidence"], "driftPolicy.evidence")
	if err != nil {
		return nil, err
	}
	safeEvidence, err := selectObjectFields(evidence, "driftPolicy.evidence", []string{
		"required", "evidenceRef", "maxEvidenceAgeMinutes", "onMissing",
	})
	if err != nil {
		return nil, err
	}
	response, err := asObject(projection["response"], "driftPolicy.response")
	if err != nil {
		return nil, err
	}
	safeResponse, err := selectObjectFields(response, "driftPolicy.response", []string{
		"differenceAction", "reconcileApproval",
	})
	if err != nil {
		return nil, err
	}
	projection["schedule"] = safeSchedule
	projection["evidence"] = safeEvidence
	projection["response"] = safeResponse
	return normalizedObject(projection, "driftPolicy")
}

// safeModuleAvailability returns the closed HA policy that is already
// selected by the resolved Kit definition. It deliberately excludes provider
// lifecycle, endpoints, credentials, host inventory, and arbitrary policy
// extensions. Only the exact HA realization module may consume it.
func safeModuleAvailability(moduleID string, availability map[string]any) (map[string]any, error) {
	if availability == nil {
		return nil, fmt.Errorf("availability projection is unavailable")
	}
	enabled, err := boolFieldDefault(availability, "availability", "enabled", false)
	if err != nil || !enabled {
		return nil, fmt.Errorf("availability projection requires an enabled HA add-on")
	}
	moduleRef, err := stringField(availability, "availability", "moduleRef")
	if err != nil {
		return nil, err
	}
	if moduleID != moduleRef || !contains([]string{
		"stackkits-ha-basement-warm-runtime", "stackkits-ha-basement-quorum-runtime",
		"stackkits-ha-cloud-warm-runtime", "stackkits-ha-cloud-quorum-runtime",
		"stackkits-ha-modern-warm-runtime", "stackkits-ha-modern-quorum-runtime",
	}, moduleID) {
		return nil, fmt.Errorf("module %q cannot consume the selected HA availability projection", moduleID)
	}
	policy, err := selectObjectFields(availability, "availability", []string{
		"mode", "policyRef", "realizationRef", "moduleRef", "selector",
		"rpoSeconds", "rtoSeconds", "failureDomainSpread", "fencing",
	})
	if err != nil {
		return nil, err
	}
	rawFailureModel, err := objectField(availability, "availability", "failureModel")
	if err != nil {
		return nil, err
	}
	failureModel, err := selectObjectFields(rawFailureModel, "availability.failureModel", []string{
		"basis", "memberSiteScope", "partitionBehavior",
	})
	if err != nil {
		return nil, err
	}
	members, err := objectListField(availability, "availability", "selectedMembers")
	if err != nil {
		return nil, err
	}
	projectedMembers := make([]any, 0, len(members))
	for index, member := range members {
		projected, err := selectObjectFields(member, fmt.Sprintf("availability.selectedMembers[%d]", index), []string{"nodeRef", "siteRef", "failureDomain"})
		if err != nil {
			return nil, err
		}
		projectedMembers = append(projectedMembers, projected)
	}
	return normalizedObject(map[string]any{"policy": policy, "failureModel": failureModel, "members": projectedMembers}, "availability")
}

func safeModuleHostRuntimePolicy(install, system map[string]any) (map[string]any, error) {
	installProjection, err := selectObjectFields(install, "install", []string{"mode", "runtime"})
	if err != nil {
		return nil, err
	}
	platform, err := objectField(install, "install", "platform")
	if err != nil {
		return nil, err
	}
	platformProjection, err := selectObjectFields(platform, "install.platform", []string{"management", "fallbackAllowed", "setupPolicy"})
	if err != nil {
		return nil, err
	}
	installProjection["platform"] = platformProjection
	host, err := objectField(system, "system", "host")
	if err != nil {
		return nil, err
	}
	result := map[string]any{"install": installProjection, "host": host}
	if container, exists, err := optionalObjectField(system, "system", "container"); err != nil {
		return nil, err
	} else if exists {
		containerProjection, err := selectOptionalObjectFields(container, "system.container", []string{"engine", "rootless", "liveRestore", "storageDriver", "dataRoot", "logDriver"})
		if err != nil {
			return nil, err
		}
		result["container"] = containerProjection
	}
	return normalizedObject(result, "hostRuntimePolicy")
}

func safeModuleStoragePolicy(storage map[string]any) (map[string]any, error) {
	return normalizedObject(storage, "storagePolicy")
}

func safeModuleNetworkPolicy(network map[string]any, path string) (map[string]any, error) {
	configuration, err := objectField(network, "network", "configuration")
	if err != nil {
		return nil, err
	}
	result, err := selectObjectFields(configuration, "network.configuration", []string{"mode", "domain", "transport", "dns"})
	if err != nil {
		return nil, err
	}
	tls, err := objectField(configuration, "network.configuration", "tls")
	if err != nil {
		return nil, err
	}
	tlsProjection, err := selectOptionalObjectFields(tls, "network.configuration.tls", []string{"defaultMode", "minVersion"})
	if err != nil {
		return nil, err
	}
	result["tls"] = tlsProjection
	return normalizedObject(result, path)
}
