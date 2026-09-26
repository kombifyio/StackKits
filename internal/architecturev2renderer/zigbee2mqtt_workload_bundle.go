package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
)

const (
	zigbee2mqttWorkloadModuleID    = "stackkits-zigbee2mqtt-runtime"
	zigbee2mqttWorkloadUnitID      = "zigbee2mqtt"
	zigbee2mqttWorkloadTemplateRef = "builtin://workloads/zigbee2mqtt/bundle/v2.json"
	zigbee2mqttWorkloadVersion     = "2.0.0"
	zigbee2mqttWorkloadOutputRef   = "workloads/zigbee2mqtt/bundle.json"
)

const zigbee2mqttWorkloadRendererSchema = `stackkit.workload-bundle/v2|Zigbee2mqttWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:zigbee2mqtt|release:` +
	zigbee2mqttRelease + `|secret-material:mqtt-password-reference|device:owner-chosen-serial-adapter|settings:mqtt-server,zigbee-adapter`

type Zigbee2mqttWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func Zigbee2mqttWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(zigbee2mqttWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: zigbee2mqttWorkloadTemplateRef, Version: zigbee2mqttWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type zigbee2mqttWorkloadBundleRenderer struct{ contract RendererContract }

func newZigbee2mqttWorkloadBundleRenderer() zigbee2mqttWorkloadBundleRenderer {
	return zigbee2mqttWorkloadBundleRenderer{contract: Zigbee2mqttWorkloadBundleRendererContract()}
}

func (r zigbee2mqttWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateZigbee2mqttWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.zigbee2mqtt-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: zigbee2mqttWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

func ParseZigbee2mqttWorkloadBundle(data []byte) (Zigbee2mqttWorkloadBundleDescriptor, error) {
	path := "zigbee2mqttWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return Zigbee2mqttWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Zigbee2mqtt workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "Zigbee2mqttWorkloadBundle" ||
		bundle.Workload.Ref != "smart-home-zigbee" || bundle.Workload.AlternativeRef != "zigbee2mqtt" ||
		bundle.Workload.ModuleRef != zigbee2mqttWorkloadModuleID || bundle.Workload.Release != zigbee2mqttRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != zigbee2mqttWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return Zigbee2mqttWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Zigbee2mqtt "+zigbee2mqttRelease+" contract")
	}
	if len(bundle.SecretRefs) != 1 || !validSecretReference(bundle.SecretRefs["mqtt-password"]) {
		return Zigbee2mqttWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "Zigbee2MQTT requires only the opaque MQTT password reference")
	}
	for index, component := range bundle.Components {
		if _, _, err := parseLANRights(component, zigbee2mqttWorkloadModuleID, fmt.Sprintf("%s.components[%d]", path, index)); err != nil {
			return Zigbee2mqttWorkloadBundleDescriptor{}, err
		}
	}
	components, err := validateZigbee2mqttRuntimeComponents(bundle.Components, path+".components")
	if err != nil {
		return Zigbee2mqttWorkloadBundleDescriptor{}, err
	}
	if err := validateZigbee2mqttServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return Zigbee2mqttWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, zigbee2mqttWorkloadModuleID, "smart-home-zigbee", 8080, path+".deliveryRoute"); err != nil {
			return Zigbee2mqttWorkloadBundleDescriptor{}, err
		}
	}
	descriptor := Zigbee2mqttWorkloadBundleDescriptor{
		WorkloadRef: "smart-home-zigbee", ModuleRef: zigbee2mqttWorkloadModuleID, Release: zigbee2mqttRelease,
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef,
		Components: make([]SelectedPaaSWorkloadComponentDescriptor, len(components)),
	}
	if bundle.DeliveryRoute != nil {
		descriptor.Route = bundle.DeliveryRoute.descriptor()
	}
	for index, component := range components {
		descriptor.Components[index] = SelectedPaaSWorkloadComponentDescriptor{
			ID: component.ID, Lifecycle: component.Lifecycle,
			ImageRef: component.Image.Ref, ImageDigest: component.Image.Digest,
		}
	}
	return descriptor, nil
}

func validateZigbee2mqttWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + zigbee2mqttWorkloadModuleID + ".renderUnits." + zigbee2mqttWorkloadUnitID
	if unit.ModuleID() != zigbee2mqttWorkloadModuleID || unit.ID() != zigbee2mqttWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", zigbee2mqttWorkloadModuleID, zigbee2mqttWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Zigbee2mqtt workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != zigbee2mqttImageRef ||
		!hasDigest || imageDigest != zigbee2mqttImageDigest || !hasEntry || entry != zigbee2mqttWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact Zigbee2mqtt "+zigbee2mqttRelease+" contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Zigbee2mqtt requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, settings, err := validateApplicationDeliveryInputsWithSettings(unit, zigbee2mqttWorkloadModuleID, "smart-home-zigbee", 8080, []string{"usb-device", "mqtt-server", "zigbee-adapter"}, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || len(secretRefs) != 1 || !validSecretReference(secretRefs["mqtt-password"]) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "Zigbee2MQTT requires the opaque MQTT password reference")
	}
	if !sameStringSet(unit.SecretInputRefs(), []string{"mqtt-password"}) ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Zigbee2mqtt bundle accepts no free inputs or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != zigbee2mqttWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", zigbee2mqttWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	for index := range components {
		if err := validateDeclaredLANRights(zigbee2mqttWorkloadModuleID, components[index], path+".runtime.components"); err != nil {
			return selectedPaaSWorkloadBundle{}, err
		}
	}
	components, err = validateZigbee2mqttRuntimeComponents(components, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if err := materializeLANRights(&components[0], settings, path+".inputs"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if len(components[0].Devices) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs.usb-device", "Zigbee2MQTT requires the owner's Zigbee adapter")
	}
	if err := applyZigbee2mqttSettings(&components[0], settings, path+".inputs"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact Zigbee2MQTT endpoint")
	}
	if err := validateZigbee2mqttServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "Zigbee2mqttWorkloadBundle",
		SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "smart-home-zigbee", "zigbee2mqtt"
	bundle.Workload.ModuleRef, bundle.Workload.Release = zigbee2mqttWorkloadModuleID, zigbee2mqttRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validateZigbee2mqttRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 1 || components[0].ID != "zigbee2mqtt" || components[0].Lifecycle != "daemon" ||
		components[0].Image.Ref != zigbee2mqttImageRef || components[0].Image.Digest != zigbee2mqttImageDigest ||
		components[0].Health.Kind != "http" || components[0].Health.Path != "/" || components[0].Health.Port != 8080 || !zigbee2mqttEnvironmentMatches(components[0].Environment) ||
		!sameEnvironment(components[0].SecretEnvironment, map[string]string{"ZIGBEE2MQTT_CONFIG_MQTT_PASSWORD": "mqtt-password"}) || len(components[0].Command) != 0 {
		return nil, fail(ErrInvalidPlan, path, "Zigbee2mqtt runtime graph differs from the closed "+zigbee2mqttRelease+" contract")
	}
	want := map[string]selectedPaaSRuntimeVolume{
		"data": {ID: "data", Target: "/app/data", Class: "persistent", Backup: true},
	}
	if len(components[0].Volumes) != len(want) {
		return nil, fail(ErrInvalidPlan, path+".volumes", "Zigbee2mqtt volume set differs from the closed contract")
	}
	for _, volume := range components[0].Volumes {
		expected, ok := want[volume.ID]
		if volume.ID == "library" && volume.HostPath != "" {
			if !safeCoreHostBootstrapStoragePath(volume.HostPath) {
				return nil, fail(ErrInvalidPlan, path+".volumes", "media source must be a clean path beneath a governed storage root")
			}
			expected.HostPath = volume.HostPath
		}
		if !ok || volume != expected {
			return nil, fail(ErrInvalidPlan, path+".volumes", "Zigbee2mqtt volume %q differs from the closed contract", volume.ID)
		}
	}
	return components, nil
}

func validateZigbee2mqttServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "smart-home-zigbee" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 8080 ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "zigbee2mqtt-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) || endpoint.Data.BindingRef != "smart-home-zigbee" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "Zigbee2MQTT route authority differs from the governed endpoint")
	}
	return nil
}

func zigbee2mqttEnvironment() map[string]string {
	return map[string]string{"ZIGBEE2MQTT_DATA": "/app/data", "ZIGBEE2MQTT_CONFIG_SERIAL_PORT": "/dev/zigbee", "ZIGBEE2MQTT_CONFIG_FRONTEND_ENABLED": "true", "ZIGBEE2MQTT_CONFIG_FRONTEND_PORT": "8080", "ZIGBEE2MQTT_CONFIG_HOMEASSISTANT_ENABLED": "true", "ZIGBEE2MQTT_CONFIG_MQTT_USER": "stackkit", "Z2M_ONBOARD_NO_SERVER": "1"}
}

var (
	zigbee2mqttMQTTServerPattern = regexp.MustCompile(`^mqtts?://[A-Za-z0-9.-]+(:[0-9]{1,5})?$`)
	zigbee2mqttAdapters          = []string{"zstack", "ember", "deconz", "zigate", "zboss", "zoh"}
)

// zigbee2mqttEnvironmentMatches accepts the closed environment plus the two
// owner settings the renderer projects, each in its admitted form.
func zigbee2mqttEnvironmentMatches(environment map[string]string) bool {
	static := map[string]string{}
	for key, value := range environment {
		switch key {
		case "ZIGBEE2MQTT_CONFIG_MQTT_SERVER":
			if !zigbee2mqttMQTTServerPattern.MatchString(value) {
				return false
			}
		case "ZIGBEE2MQTT_CONFIG_SERIAL_ADAPTER":
			if !slices.Contains(zigbee2mqttAdapters, value) {
				return false
			}
		default:
			static[key] = value
		}
	}
	return sameEnvironment(static, zigbee2mqttEnvironment())
}

// applyZigbee2mqttSettings projects the owner's broker address and optional
// adapter type into the Zigbee2MQTT environment.
func applyZigbee2mqttSettings(component *selectedPaaSRuntimeComponent, settings map[string]json.RawMessage, path string) error {
	var server string
	if raw, ok := settings["mqtt-server"]; !ok || json.Unmarshal(raw, &server) != nil || !zigbee2mqttMQTTServerPattern.MatchString(server) {
		return fail(ErrInvalidPlan, path+".mqtt-server", "requires an mqtt:// or mqtts:// broker address")
	}
	component.Environment["ZIGBEE2MQTT_CONFIG_MQTT_SERVER"] = server
	if raw, ok := settings["zigbee-adapter"]; ok {
		var adapter string
		if json.Unmarshal(raw, &adapter) != nil || !slices.Contains(zigbee2mqttAdapters, adapter) {
			return fail(ErrInvalidPlan, path+".zigbee-adapter", "names an unsupported Zigbee adapter type")
		}
		component.Environment["ZIGBEE2MQTT_CONFIG_SERIAL_ADAPTER"] = adapter
	}
	return nil
}
