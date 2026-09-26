package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	esphomeWorkloadModuleID    = "stackkits-esphome-runtime"
	esphomeWorkloadUnitID      = "esphome"
	esphomeWorkloadTemplateRef = "builtin://workloads/esphome/bundle/v2.json"
	esphomeWorkloadVersion     = "2.0.0"
	esphomeWorkloadOutputRef   = "workloads/esphome/bundle.json"
)

const esphomeWorkloadRendererSchema = `stackkit.workload-bundle/v2|EsphomeWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:esphome|release:` +
	esphomeRelease + `|secret-material:not-included`

type EsphomeWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func EsphomeWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(esphomeWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit",
		TemplateRef: esphomeWorkloadTemplateRef, Version: esphomeWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type esphomeWorkloadBundleRenderer struct{ contract RendererContract }

func newEsphomeWorkloadBundleRenderer() esphomeWorkloadBundleRenderer {
	return esphomeWorkloadBundleRenderer{contract: EsphomeWorkloadBundleRendererContract()}
}

func (r esphomeWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateEsphomeWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.esphome-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: esphomeWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

func ParseEsphomeWorkloadBundle(data []byte) (EsphomeWorkloadBundleDescriptor, error) {
	path := "esphomeWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return EsphomeWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Esphome workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "EsphomeWorkloadBundle" ||
		bundle.Workload.Ref != "smart-home-esphome" || bundle.Workload.AlternativeRef != "esphome" ||
		bundle.Workload.ModuleRef != esphomeWorkloadModuleID || bundle.Workload.Release != esphomeRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != esphomeWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return EsphomeWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Esphome "+esphomeRelease+" contract")
	}
	if len(bundle.SecretRefs) != 0 {
		return EsphomeWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "Esphome single-container contract accepts no secret material")
	}
	components, err := validateEsphomeRuntimeComponents(bundle.Components, path+".components")
	if err != nil {
		return EsphomeWorkloadBundleDescriptor{}, err
	}
	if err := validateEsphomeServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return EsphomeWorkloadBundleDescriptor{}, err
	}
	if bundle.DeliveryRoute != nil {
		if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, esphomeWorkloadModuleID, "smart-home-esphome", 6052, path+".deliveryRoute"); err != nil {
			return EsphomeWorkloadBundleDescriptor{}, err
		}
	}
	descriptor := EsphomeWorkloadBundleDescriptor{
		WorkloadRef: "smart-home-esphome", ModuleRef: esphomeWorkloadModuleID, Release: esphomeRelease,
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

func validateEsphomeWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + esphomeWorkloadModuleID + ".renderUnits." + esphomeWorkloadUnitID
	if unit.ModuleID() != esphomeWorkloadModuleID || unit.ID() != esphomeWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", esphomeWorkloadModuleID, esphomeWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered Esphome workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != esphomeImageRef ||
		!hasDigest || imageDigest != esphomeImageDigest || !hasEntry || entry != esphomeWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact Esphome "+esphomeRelease+" contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Esphome requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, esphomeWorkloadModuleID, "smart-home-esphome", 6052, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if len(unit.SecretInputRefs()) != 0 || !emptyJSONObject(unit.SecretRefsJSON()) ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Esphome bundle accepts no free inputs or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != esphomeWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", esphomeWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	components, err = validateEsphomeRuntimeComponents(components, path+".runtime.components")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact media endpoint")
	}
	if err := validateEsphomeServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "EsphomeWorkloadBundle",
		SecretRefs: map[string]string{}, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "smart-home-esphome", "esphome"
	bundle.Workload.ModuleRef, bundle.Workload.Release = esphomeWorkloadModuleID, esphomeRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

func validateEsphomeRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) ([]selectedPaaSRuntimeComponent, error) {
	if len(components) != 1 || components[0].ID != "esphome" || components[0].Lifecycle != "daemon" ||
		components[0].Image.Ref != esphomeImageRef || components[0].Image.Digest != esphomeImageDigest ||
		components[0].Health.Kind != "http" || components[0].Health.Path != "/" || components[0].Health.Port != 6052 || !sameEnvironment(components[0].Environment, esphomeEnvironment()) {
		return nil, fail(ErrInvalidPlan, path, "Esphome runtime graph differs from the closed "+esphomeRelease+" contract")
	}
	want := map[string]selectedPaaSRuntimeVolume{
		"config": {ID: "config", Target: "/config", Class: "persistent", Backup: true},
	}
	if len(components[0].Volumes) != len(want) {
		return nil, fail(ErrInvalidPlan, path+".volumes", "Esphome volume set differs from the closed contract")
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
			return nil, fail(ErrInvalidPlan, path+".volumes", "Esphome volume %q differs from the closed contract", volume.ID)
		}
	}
	return components, nil
}

func validateEsphomeServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "smart-home-esphome" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 6052 ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "esphome-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) || endpoint.Data.BindingRef != "smart-home-esphome" ||
		endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "media route authority differs from the governed Esphome endpoint")
	}
	return nil
}

func esphomeEnvironment() map[string]string {
	return map[string]string{}
}
