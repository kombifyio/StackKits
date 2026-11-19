package resolvedplan

import (
	"fmt"
)

// safeModuleOTLPBaseline is the sole seam that makes the secret-free OTLP
// baseline and optional signal lanes visible to the future monitoring-agent
// module. It deliberately rebuilds the projection instead of forwarding its
// source so backend, header, credential, and CA authority remain unreachable.
func safeModuleOTLPBaseline(moduleID string, observability map[string]any) (map[string]any, error) {
	if moduleID != "stackkits-monitoring-agent-runtime" {
		return nil, fmt.Errorf("module %q cannot consume OTLP baseline authority", moduleID)
	}
	if observability == nil {
		return nil, fmt.Errorf("OTLP baseline projection is unavailable")
	}
	signals, err := objectField(observability, "observability", "signals")
	if err != nil {
		return nil, err
	}
	collector, err := objectField(observability, "observability", "collector")
	if err != nil {
		return nil, err
	}
	tls, err := objectField(collector, "observability.collector", "tls")
	if err != nil {
		return nil, err
	}
	safeProfile, err := selectObjectFields(observability, "observability", []string{"profile", "agentBudget"})
	if err != nil {
		return nil, err
	}
	safeSignals, err := selectObjectFields(signals, "observability.signals", []string{"metrics", "logs", "traces"})
	if err != nil {
		return nil, err
	}
	safeCollector, err := selectObjectFields(collector, "observability.collector", []string{"enabled", "endpoint", "protocol"})
	if err != nil {
		return nil, err
	}
	safeTLS, err := selectObjectFields(tls, "observability.collector.tls", []string{"insecure"})
	if err != nil {
		return nil, err
	}
	safeCollector["tls"] = safeTLS
	projected := map[string]any{
		"profile": safeProfile["profile"], "agentBudget": safeProfile["agentBudget"],
		"signals": safeSignals, "collector": safeCollector,
	}
	optionalSignals, exists, err := optionalObjectField(observability, "observability", "optionalSignals")
	if err != nil {
		return nil, err
	}
	if exists {
		safeOptionalSignals, err := safeModuleOTLPOptionalSignals(optionalSignals, safeSignals)
		if err != nil {
			return nil, err
		}
		projected["optionalSignals"] = safeOptionalSignals
	}
	return normalizedObject(projected, "observability")
}

func safeModuleOTLPOptionalSignals(optionalSignals, signals map[string]any) (map[string]any, error) {
	projected := map[string]any{}
	for _, laneName := range []string{"logs", "traces"} {
		lane, exists, err := optionalObjectField(optionalSignals, "observability.optionalSignals", laneName)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		enabled, ok := signals[laneName].(bool)
		if !ok || !enabled {
			return nil, fmt.Errorf("observability.optionalSignals.%s requires observability.signals.%s=true", laneName, laneName)
		}
		fields := []string{"enabled", "protocol", "direction", "lifecycle"}
		if laneName == "logs" {
			fields = append(fields, "retentionDays")
		}
		safeLane, err := selectObjectFields(lane, "observability.optionalSignals."+laneName, fields)
		if err != nil {
			return nil, err
		}
		budget, err := objectField(lane, "observability.optionalSignals."+laneName, "budget")
		if err != nil {
			return nil, err
		}
		safeBudget, err := selectObjectFields(budget, "observability.optionalSignals."+laneName+".budget", []string{
			"enabled", "cpuMilli", "memoryMiB", "ephemeralMiB", "persistentMiB", "maxRetentionDays",
		})
		if err != nil {
			return nil, err
		}
		safeLane["budget"] = safeBudget
		if laneName == "traces" {
			sampling, err := objectField(lane, "observability.optionalSignals.traces", "sampling")
			if err != nil {
				return nil, err
			}
			safeSampling, err := selectObjectFields(sampling, "observability.optionalSignals.traces.sampling", []string{"mode", "ratio"})
			if err != nil {
				return nil, err
			}
			safeLane["sampling"] = safeSampling
		}
		projected[laneName] = safeLane
	}
	if len(projected) == 0 {
		return nil, fmt.Errorf("observability.optionalSignals has no represented lanes")
	}
	return projected, nil
}
