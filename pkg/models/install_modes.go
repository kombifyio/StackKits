package models

import "strings"

// NormalizeInstallMode maps public and legacy install-mode values onto the
// current three-mode contract.
func NormalizeInstallMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", InstallModeBootstrapped, InstallModeSimpleLegacy:
		return InstallModeBootstrapped
	case InstallModeBare:
		return InstallModeBare
	case InstallModeAdvanced, InstallModeTerramate, InstallModeAdvancedTM:
		return InstallModeAdvanced
	default:
		return strings.ToLower(strings.TrimSpace(mode))
	}
}

func IsKnownInstallMode(mode string) bool {
	switch NormalizeInstallMode(mode) {
	case InstallModeBare, InstallModeBootstrapped, InstallModeAdvanced:
		return true
	default:
		return false
	}
}

func IsLegacyInstallMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case InstallModeSimpleLegacy, InstallModeTerramate, InstallModeAdvancedTM:
		return true
	default:
		return false
	}
}

func IsExplicitTerramateInstallMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case InstallModeTerramate, InstallModeAdvancedTM:
		return true
	default:
		return false
	}
}

func (s *StackSpec) EffectiveInstallMode() string {
	if s == nil {
		return InstallModeDefault
	}
	return NormalizeInstallMode(s.Mode)
}

func (s *StackSpec) UsesAdvancedIAC() bool {
	return s != nil && IsExplicitTerramateInstallMode(s.Mode)
}

func NormalizeSetupPolicy(policy string) string {
	return strings.ToLower(strings.TrimSpace(policy))
}

func IsKnownSetupPolicy(policy string) bool {
	switch NormalizeSetupPolicy(policy) {
	case "", SetupPolicyManual, SetupPolicyOnDemand, SetupPolicyAutomatic:
		return true
	default:
		return false
	}
}

type SetupPolicyResolver struct {
	spec *StackSpec
	mode string
}

func NewSetupPolicyResolver(spec *StackSpec) SetupPolicyResolver {
	mode := InstallModeDefault
	if spec != nil {
		mode = spec.EffectiveInstallMode()
	}
	return SetupPolicyResolver{spec: spec, mode: mode}
}

func (r SetupPolicyResolver) InstallMode() string {
	return r.mode
}

func (r SetupPolicyResolver) PlatformPolicy() string {
	if r.mode == InstallModeBare {
		return SetupPolicyManual
	}
	if r.spec != nil {
		if policy := NormalizeSetupPolicy(r.spec.Bootstrap.PlatformPolicy); policy != "" {
			return policy
		}
	}
	return r.modePlatformPolicy()
}

func (r SetupPolicyResolver) ApplicationDefaultPolicy() string {
	if r.mode == InstallModeBare {
		return SetupPolicyManual
	}
	if r.spec != nil {
		if policy := NormalizeSetupPolicy(r.spec.Bootstrap.ApplicationDefaultPolicy); policy != "" {
			return policy
		}
	}
	return r.modeApplicationPolicy("", "")
}

func (r SetupPolicyResolver) EffectivePlatformServicePolicy(tool string, aliases ...string) string {
	if r.mode == InstallModeBare {
		return SetupPolicyManual
	}
	if policy := r.servicePolicy(append([]string{tool}, aliases...)); policy != "" {
		return policy
	}
	return r.PlatformPolicy()
}

func (r SetupPolicyResolver) EffectiveApplicationPolicy(useCase, tool string, aliases ...string) string {
	if r.mode == InstallModeBare {
		return SetupPolicyManual
	}
	if policy := r.servicePolicy(append([]string{tool}, aliases...)); policy != "" {
		return policy
	}
	if policy := r.useCasePolicy(useCase); policy != "" {
		return policy
	}
	if r.spec != nil {
		if policy := NormalizeSetupPolicy(r.spec.Bootstrap.ApplicationDefaultPolicy); policy != "" {
			return policy
		}
	}
	if policy := r.modeApplicationPolicy(useCase, tool); policy != "" {
		return policy
	}
	return SetupPolicyManual
}

func (r SetupPolicyResolver) modePlatformPolicy() string {
	if r.mode == InstallModeBare {
		return SetupPolicyManual
	}
	return SetupPolicyAutomatic
}

func (r SetupPolicyResolver) modeApplicationPolicy(useCase, tool string) string {
	if r.mode == InstallModeBare {
		return SetupPolicyManual
	}
	if r.mode == InstallModeAdvanced && isManagedRuntimeIntelligenceTool(useCase, tool) {
		return SetupPolicyAutomatic
	}
	return SetupPolicyOnDemand
}

func isManagedRuntimeIntelligenceTool(useCase, tool string) bool {
	switch normalizePolicyKey(useCase) {
	case "techstack", "runtime-intelligence", "runtime_intelligence", "kombify-desk", "kombify_desk", "kombifydesk":
		return true
	}
	switch normalizePolicyKey(tool) {
	case "techstack", "kombify-desk", "kombify_desk", "kombifydesk":
		return true
	default:
		return false
	}
}

func (r SetupPolicyResolver) useCasePolicy(useCase string) string {
	if r.spec == nil || useCase == "" {
		return ""
	}
	if raw, ok := lookupPolicyConfig(r.spec.Application, useCase); ok {
		if policy, ok := setupPolicyFromConfig(raw); ok {
			return policy
		}
	}
	return ""
}

func (r SetupPolicyResolver) servicePolicy(keys []string) string {
	if r.spec == nil || len(keys) == 0 {
		return ""
	}
	for _, key := range keys {
		if raw, ok := lookupPolicyConfig(r.spec.Services, key); ok {
			if policy, ok := setupPolicyFromConfig(raw); ok {
				return policy
			}
		}
	}
	return ""
}

func lookupPolicyConfig(values map[string]any, key string) (any, bool) {
	if len(values) == 0 {
		return nil, false
	}
	normalizedKey := normalizePolicyKey(key)
	for candidate, value := range values {
		if normalizePolicyKey(candidate) == normalizedKey {
			return value, true
		}
	}
	return nil, false
}

func setupPolicyFromConfig(raw any) (string, bool) {
	switch value := raw.(type) {
	case map[string]any:
		return setupPolicyFromStringMap(value)
	case map[any]any:
		converted := make(map[string]any, len(value))
		for k, v := range value {
			if key, ok := k.(string); ok {
				converted[key] = v
			}
		}
		return setupPolicyFromStringMap(converted)
	case map[string]string:
		for _, key := range []string{"policy", "setupPolicy"} {
			if policy := NormalizeSetupPolicy(value[key]); policy != "" {
				return policy, true
			}
		}
	case string:
		if policy := NormalizeSetupPolicy(value); policy != "" {
			return policy, true
		}
	}
	return "", false
}

func setupPolicyFromStringMap(values map[string]any) (string, bool) {
	for _, key := range []string{"policy", "setupPolicy"} {
		if policy, ok := stringValue(values[key]); ok {
			return NormalizeSetupPolicy(policy), true
		}
	}
	if setup, ok := values["setup"]; ok {
		switch setupMap := setup.(type) {
		case map[string]any:
			return setupPolicyFromStringMap(setupMap)
		case map[any]any:
			converted := make(map[string]any, len(setupMap))
			for k, v := range setupMap {
				if key, ok := k.(string); ok {
					converted[key] = v
				}
			}
			return setupPolicyFromStringMap(converted)
		}
	}
	return "", false
}

func stringValue(value any) (string, bool) {
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	return text, text != ""
}

func normalizePolicyKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	return value
}
