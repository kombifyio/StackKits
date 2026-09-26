package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	mosquittoWorkloadModuleID    = "stackkits-mosquitto-runtime"
	mosquittoWorkloadUnitID      = "mosquitto"
	mosquittoWorkloadTemplateRef = "builtin://workloads/mosquitto/bundle/v2.json"
	mosquittoWorkloadVersion     = "2.0.0"
	mosquittoWorkloadOutputRef   = "workloads/mosquitto/bundle.json"
)

const mosquittoWorkloadRendererSchema = `stackkit.workload-bundle/v2|MosquittoWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:mosquitto|release:` +
	mosquittoRelease + `|secret-material:mqtt-password-reference|lan-listener:1883-owner-setting`

type MosquittoWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func MosquittoWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(mosquittoWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: mosquittoWorkloadTemplateRef, Version: mosquittoWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type mosquittoWorkloadBundleRenderer struct{ contract RendererContract }

func newMosquittoWorkloadBundleRenderer() mosquittoWorkloadBundleRenderer {
	return mosquittoWorkloadBundleRenderer{contract: MosquittoWorkloadBundleRendererContract()}
}

func (r mosquittoWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateMosquittoWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.mosquitto-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: mosquittoWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

func ParseMosquittoWorkloadBundle(data []byte) (MosquittoWorkloadBundleDescriptor, error) {
	path := "mosquittoWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return MosquittoWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Mosquitto workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "MosquittoWorkloadBundle" ||
		bundle.Workload.Ref != "smart-home-mqtt" || bundle.Workload.AlternativeRef != "mosquitto" ||
		bundle.Workload.ModuleRef != mosquittoWorkloadModuleID || bundle.Workload.Release != mosquittoRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != mosquittoWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return MosquittoWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Mosquitto "+mosquittoRelease+" contract")
	}
	if len(bundle.SecretRefs) != 1 || !validSecretReference(bundle.SecretRefs["mqtt-password"]) {
		return MosquittoWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "Mosquitto requires only the opaque MQTT password reference")
	}
	for index, component := range bundle.Components {
		if _, _, err := parseLANRights(component, mosquittoWorkloadModuleID, fmt.Sprintf("%s.components[%d]", path, index)); err != nil {
			return MosquittoWorkloadBundleDescriptor{}, err
		}
	}
	components, err := validateMosquittoRuntimeComponents(bundle.Components, path+".components")
	if err != nil {
		return MosquittoWorkloadBundleDescriptor{}, err
	}
	if err := validateMosquittoServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return MosquittoWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, mosquittoWorkloadModuleID, "smart-home-mqtt", 8080, path+".deliveryRoute"); err != nil {
			return MosquittoWorkloadBundleDescriptor{}, err
		}
	}
	descriptor := MosquittoWorkloadBundleDescriptor{
		WorkloadRef: "smart-home-mqtt", ModuleRef: mosquittoWorkloadModuleID, Release: mosquittoRelease,
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

func validateMosquittoWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + mosquittoWorkloadModuleID + ".renderUnits." + mosquittoWorkloadUnitID
	if unit.ModuleID() != mosquittoWorkloadModuleID || unit.ID() != mosquittoWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", mosquittoWorkloadModuleID, mosquittoWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Mosquitto workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != mosquittoImageRef ||
		!hasDigest || imageDigest != mosquittoImageDigest || !hasEntry || entry != mosquittoWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact Mosquitto "+mosquittoRelease+" contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Mosquitto requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, settings, err := validateApplicationDeliveryInputsWithSettings(unit, mosquittoWorkloadModuleID, "smart-home-mqtt", 8080, []string{"lan-listener"}, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || len(secretRefs) != 1 || !validSecretReference(secretRefs["mqtt-password"]) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "Mosquitto requires the opaque MQTT password reference")
	}
	if !sameStringSet(unit.SecretInputRefs(), []string{"mqtt-password"}) ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Mosquitto bundle accepts no free inputs or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != mosquittoWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", mosquittoWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	for index := range components {
		if err := validateDeclaredLANRights(mosquittoWorkloadModuleID, components[index], path+".runtime.components"); err != nil {
			return selectedPaaSWorkloadBundle{}, err
		}
		if err := materializeLANRights(&components[index], settings, path+".inputs"); err != nil {
			return selectedPaaSWorkloadBundle{}, err
		}
	}
	components, err = validateMosquittoRuntimeComponents(components, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact MQTT broker endpoint")
	}
	if err := validateMosquittoServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "MosquittoWorkloadBundle",
		SecretRefs: secretRefs, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "smart-home-mqtt", "mosquitto"
	bundle.Workload.ModuleRef, bundle.Workload.Release = mosquittoWorkloadModuleID, mosquittoRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validateMosquittoRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 1 || components[0].ID != "mosquitto" || components[0].Lifecycle != "daemon" ||
		components[0].Image.Ref != mosquittoImageRef || components[0].Image.Digest != mosquittoImageDigest ||
		components[0].Health.Kind != "http" || components[0].Health.Path != "/api/v1/systree" || components[0].Health.Port != 8080 || !sameEnvironment(components[0].Environment, mosquittoEnvironment()) ||
		!sameEnvironment(components[0].SecretEnvironment, map[string]string{"MQTT_PASSWORD": "mqtt-password"}) || !mosquittoCommandMatches(components[0].Command) {
		return nil, fail(ErrInvalidPlan, path, "Mosquitto runtime graph differs from the closed "+mosquittoRelease+" contract")
	}
	want := map[string]selectedPaaSRuntimeVolume{
		"data": {ID: "data", Target: "/mosquitto/data", Class: "persistent", Backup: true},
	}
	if len(components[0].Volumes) != len(want) {
		return nil, fail(ErrInvalidPlan, path+".volumes", "Mosquitto volume set differs from the closed contract")
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
			return nil, fail(ErrInvalidPlan, path+".volumes", "Mosquitto volume %q differs from the closed contract", volume.ID)
		}
	}
	return components, nil
}

func validateMosquittoServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "smart-home-mqtt" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 8080 ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "mosquitto-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) || endpoint.Data.BindingRef != "smart-home-mqtt" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "MQTT broker route authority differs from the governed Mosquitto endpoint")
	}
	return nil
}

func mosquittoEnvironment() map[string]string {
	return map[string]string{}
}

func mosquittoCommandMatches(command []string) bool {
	var want []string
	return json.Unmarshal([]byte(mosquittoCommandJSON), &want) == nil && exactStringList(command, want)
}
