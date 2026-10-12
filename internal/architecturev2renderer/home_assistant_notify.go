package architecturev2renderer

import (
	"encoding/json"
	"reflect"
	"strings"
)

const (
	homeAssistantNotifySettingRef = "kombify-notify"
	// HomeAssistantNotifyTarget is the directory inside the Home Assistant
	// configuration volume where the generated notify platform file and its
	// secrets file are bind-mounted, apart from the owner's own secrets.yaml.
	HomeAssistantNotifyTarget = "/config/stackkit-notify"
	// homeAssistantNotifyInclude is the one configuration line the right adds.
	homeAssistantNotifyInclude = "notify: !include stackkit-notify/notify.yaml"
)

// HomeAssistantNotifyDescriptor is the opt-in "kombify notify" right of the
// Home Assistant component. The catalog declares it with the workload setting
// that enables it; a rendered bundle carries it only while the owner turned
// that setting on. It holds no address and no secret: the node adapter binds
// both at Apply from the container network and its own custody.
type HomeAssistantNotifyDescriptor struct {
	SettingRef string `json:"settingRef"`
	Target     string `json:"target"`
}

func governedHomeAssistantNotify() *HomeAssistantNotifyDescriptor {
	return &HomeAssistantNotifyDescriptor{SettingRef: homeAssistantNotifySettingRef, Target: HomeAssistantNotifyTarget}
}

// validateHomeAssistantNotify admits the right, declared or enabled, only for
// the governed Home Assistant component and only in its exact shape.
func validateHomeAssistantNotify(moduleRef string, component selectedPaaSRuntimeComponent, path string) error {
	if component.HomeAssistantNotify == nil {
		return nil
	}
	if moduleRef != homeAssistantWorkloadModuleID || component.ID != homeAssistantWorkloadUnitID ||
		!reflect.DeepEqual(component.HomeAssistantNotify, governedHomeAssistantNotify()) {
		return fail(ErrInvalidPlan, path+".homeAssistantNotify", "kombify notify is admitted only for the governed Home Assistant component")
	}
	return nil
}

// materializeHomeAssistantNotify keeps the declared right only while the
// owner's boolean setting is true.
func materializeHomeAssistantNotify(component *selectedPaaSRuntimeComponent, values map[string]json.RawMessage, path string) error {
	declared := component.HomeAssistantNotify
	component.HomeAssistantNotify = nil
	if declared == nil {
		return nil
	}
	raw, set := values[declared.SettingRef]
	if !set {
		return nil
	}
	var enabled bool
	if err := json.Unmarshal(raw, &enabled); err != nil {
		return fail(ErrInvalidPlan, path+"."+declared.SettingRef, "must be a boolean")
	}
	if enabled {
		component.HomeAssistantNotify = declared
	}
	return nil
}

func homeAssistantNotifyEnabled(components []selectedPaaSRuntimeComponent) bool {
	for _, component := range components {
		if component.HomeAssistantNotify != nil {
			return true
		}
	}
	return false
}

// homeAssistantNotifyConfigurationConsistent requires the configuration to
// include the notify platform exactly when the right is enabled, so the
// default configuration stays byte-identical for an owner who never opts in.
func homeAssistantNotifyConfigurationConsistent(body string, enabled bool) bool {
	return strings.Contains(body, homeAssistantNotifyInclude) == enabled
}
