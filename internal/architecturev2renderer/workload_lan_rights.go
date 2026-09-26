package architecturev2renderer

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
)

// governedLANRights lists the only module components that may publish a
// non-HTTP LAN listener or receive a host device. Each right stays inert until
// the owner turns on the named workload setting; the catalog declares the
// right and the renderer materializes it only from that setting.
var governedLANRights = map[string]struct {
	component string
	listeners []selectedPaaSLANListener
	device    *selectedPaaSDevicePassthrough
}{
	mosquittoWorkloadModuleID: {
		component: "mosquitto",
		listeners: []selectedPaaSLANListener{{Port: 1883, Protocol: "tcp", SettingRef: "lan-listener"}},
	},
	zigbee2mqttWorkloadModuleID: {
		component: "zigbee2mqtt",
		device:    &selectedPaaSDevicePassthrough{SettingRef: "usb-device", Target: "/dev/zigbee"},
	},
}

// hostDevicePattern admits stable serial adapter paths only: a by-id link
// or a USB/ACM tty. Block devices, GPUs and raw buses are never passed.
var hostDevicePattern = regexp.MustCompile(`^/dev/(serial/by-id/[A-Za-z0-9._:+-]+|tty(USB|ACM)[0-9]{1,3})$`)

// ValidHostDevicePath reports whether a host device path may be passed through.
func ValidHostDevicePath(path string) bool { return hostDevicePattern.MatchString(path) }

// validateDeclaredLANRights checks the catalog declaration of a component
// against the governed rights of its module before materialization.
func validateDeclaredLANRights(moduleRef string, component selectedPaaSRuntimeComponent, path string) error {
	if len(component.Devices) != 0 {
		return fail(ErrInvalidPlan, path, "catalog components declare device passthrough, never a device")
	}
	if len(component.LANListeners) == 0 && component.DevicePassthrough == nil {
		return nil
	}
	rights, ok := governedLANRights[moduleRef]
	if !ok || rights.component != component.ID ||
		!slices.Equal(component.LANListeners, rights.listeners) ||
		!sameDevicePassthrough(component.DevicePassthrough, rights.device) {
		return fail(ErrInvalidPlan, path, "LAN listeners and device passthrough are admitted only for their governed component")
	}
	return nil
}

func sameDevicePassthrough(a, b *selectedPaaSDevicePassthrough) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// materializeLANRights turns the declared rights of a component into the
// enabled ones for this render: a listener only when its boolean setting is
// true, a device only when its setting names an admitted host device path.
func materializeLANRights(component *selectedPaaSRuntimeComponent, values map[string]json.RawMessage, path string) error {
	listeners := component.LANListeners
	component.LANListeners = nil
	for _, listener := range listeners {
		raw, set := values[listener.SettingRef]
		if !set {
			continue
		}
		var enabled bool
		if err := json.Unmarshal(raw, &enabled); err != nil {
			return fail(ErrInvalidPlan, path+"."+listener.SettingRef, "must be a boolean")
		}
		if enabled {
			component.LANListeners = append(component.LANListeners, listener)
		}
	}
	if passthrough := component.DevicePassthrough; passthrough != nil {
		component.DevicePassthrough = nil
		if raw, set := values[passthrough.SettingRef]; set {
			var hostPath string
			if err := json.Unmarshal(raw, &hostPath); err != nil || !ValidHostDevicePath(hostPath) {
				return fail(ErrInvalidPlan, path+"."+passthrough.SettingRef, "must be a serial adapter path under /dev/serial/by-id or a ttyUSB/ttyACM device")
			}
			component.Devices = []selectedPaaSDevice{{HostPath: hostPath, Target: passthrough.Target}}
		}
	}
	return nil
}

// parseLANRights validates the enabled rights a rendered bundle carries.
func parseLANRights(component selectedPaaSRuntimeComponent, moduleRef, path string) ([]ApplicationDeliveryLANListener, []ApplicationDeliveryDevice, error) {
	if len(component.LANListeners) == 0 && len(component.Devices) == 0 && component.DevicePassthrough == nil {
		return nil, nil, nil
	}
	rights, ok := governedLANRights[moduleRef]
	if !ok || rights.component != component.ID || component.DevicePassthrough != nil {
		return nil, nil, fail(ErrInvalidPlan, path, "LAN listeners and devices are admitted only for their governed component")
	}
	var listeners []ApplicationDeliveryLANListener
	for _, listener := range component.LANListeners {
		if !slices.Contains(rights.listeners, listener) {
			return nil, nil, fail(ErrInvalidPlan, path+".lanListeners", "listener differs from the governed declaration")
		}
		listeners = append(listeners, ApplicationDeliveryLANListener{Port: listener.Port, Protocol: listener.Protocol})
	}
	var devices []ApplicationDeliveryDevice
	for _, device := range component.Devices {
		if rights.device == nil || device.Target != rights.device.Target || !ValidHostDevicePath(device.HostPath) || len(component.Devices) != 1 {
			return nil, nil, fail(ErrInvalidPlan, path+".devices", "device differs from the governed passthrough")
		}
		devices = append(devices, ApplicationDeliveryDevice{HostPath: device.HostPath, Target: device.Target})
	}
	return listeners, devices, nil
}

// validateApplicationDeliveryInputsWithSettings accepts the compiler-owned
// delivery route plus the named owner settings of the workload, and returns
// the route and the raw values of the settings the owner set.
func validateApplicationDeliveryInputsWithSettings(
	unit RenderUnit,
	moduleRef, serviceRef string,
	targetPort int,
	settings []string,
	path string,
) (*applicationDeliveryRoute, map[string]json.RawMessage, error) {
	if !sameStringSet(unit.PublicInputRefs(), append([]string{applicationDeliveryRouteInputRef}, settings...)) ||
		len(unit.PlanInputRefs()) != 0 || !emptyJSONObject(unit.PlanInputsJSON()) {
		return nil, nil, fail(ErrInvalidPlan, path, "requires the delivery route and the declared owner settings only")
	}
	if err := validateDeliveryRouteBindingOnly(unit, path); err != nil {
		return nil, nil, err
	}
	var values map[string]json.RawMessage
	if err := decodeStrict(unit.ValuesJSON(), &values); err != nil {
		return nil, nil, wrap(ErrInvalidPlan, path+".values", "decode delivery inputs", err)
	}
	settingValues := map[string]json.RawMessage{}
	var route *applicationDeliveryRoute
	for key, raw := range values {
		switch {
		case key == applicationDeliveryRouteInputRef:
			if string(raw) == "null" {
				continue
			}
			route = &applicationDeliveryRoute{}
			if err := decodeStrict(raw, route); err != nil {
				return nil, nil, wrap(ErrInvalidPlan, path+".values.delivery-route", "decode exact delivery route", err)
			}
		case slices.Contains(settings, key):
			settingValues[key] = raw
		default:
			return nil, nil, fail(ErrInvalidPlan, path+".values", "carries an undeclared input")
		}
	}
	if route != nil {
		if err := validateParsedApplicationDeliveryRoute(*route, moduleRef, serviceRef, targetPort, path+".values.delivery-route"); err != nil {
			return nil, nil, err
		}
	}
	return route, settingValues, nil
}

// validateDeliveryRouteBindingOnly requires exactly the compiler-owned route
// binding; owner settings reach the unit as plain workload values.
func validateDeliveryRouteBindingOnly(unit RenderUnit, path string) error {
	var bindings []struct {
		TargetRef    string          `json:"targetRef"`
		SourceRef    string          `json:"sourceRef"`
		ValueType    string          `json:"valueType"`
		Cardinality  string          `json:"cardinality"`
		Required     bool            `json:"required"`
		DefaultValue json.RawMessage `json:"defaultValue"`
	}
	if err := decodeStrict(unit.InputBindingsJSON(), &bindings); err != nil || len(bindings) != 1 ||
		bindings[0].TargetRef != applicationDeliveryRouteInputRef ||
		bindings[0].SourceRef != applicationDeliveryRouteSourceRef ||
		bindings[0].ValueType != applicationDeliveryRouteValueType ||
		bindings[0].Cardinality != applicationDeliveryRouteCardinality || bindings[0].Required ||
		string(bytes.TrimSpace(bindings[0].DefaultValue)) != "null" {
		return fail(ErrInvalidPlan, path+".inputBindings", "delivery route binding identity differs from the compiler-owned contract")
	}
	return nil
}
